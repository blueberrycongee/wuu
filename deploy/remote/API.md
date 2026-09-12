# 账号与连接服务 API

此服务实现 Wuu 手机与电脑的账号、设备目录、会话历史存储、加密连接和通知。用户开启同步后，电脑把会话文字保存到服务器 PostgreSQL；手机可通过 HTTPS 在电脑离线时读取。Agent 和工作区执行仍在电脑，手机通过 `/v1/connect` 中转执行请求和实时回复。

## 代码入口

| 模块 | 实现 |
| --- | --- |
| 独立可执行程序 | `cmd/wuu-server/main.go` |
| 参数、TLS、服务生命周期、可选 Web 资源 | `internal/remote/server` |
| 注册、登录、密码恢复、PostgreSQL 数据库和设备归属 | `internal/remote/account` |
| WebSocket 身份挑战、账号路由、在线状态与撤销 | `internal/remote/relay` |
| APNs / FCM | `internal/remote/relay/native_push.go` |
| 握手与帧加密 | `internal/remote/secure` |
| 连接消息类型 | `internal/remote/wire/wire.go` |
| Go 客户端账号操作 | `internal/remote/account/client.go`、`cmd/wuu/remote_account.go` |
| 手机账号客户端 | `clients/core/src/account.ts` |

## HTTP 接口

所有账号接口位于 `/v1/account`，JSON 请求与响应。需要登录的接口使用 `Authorization: Bearer <token>`，不使用 cookie。身份信息不可写入 URL。响应使用 `Cache-Control: no-store`。正式网络连接必须使用 HTTPS。

| 方法与路径 | 身份 | 请求或结果 |
| --- | --- | --- |
| `GET /healthz` | 无 | 存活检查，返回 `ok`，不代表任何电脑在线。 |
| `GET /v1/account/config` | 无 | `registration`、协议 `version`、可用 `push_platforms`。 |
| `POST /v1/account/register` | 设备公钥持有证明 | 创建账号并登记首台设备，返回 `token`、`username`、`pub`、一次性展示的 `recovery`。 |
| `POST /v1/account/login` | 密码及设备公钥持有证明 | 登录并登记设备，返回 `token`、`username`、`pub`。 |
| `GET /v1/account/devices` | Bearer | 当前账号 `username` 与 `devices`。设备包含 `pub`、`account`、`name`、`role`、`added_at`、`online`。 |
| `DELETE /v1/account/devices/{pub}` | Bearer，同账号 | 撤销设备及其令牌、推送登记，断开账号连接，返回 `{"ok":true}`。 |
| `POST /v1/account/logout` | Bearer | 撤销调用者的设备，返回 `{"ok":true}`。 |
| `POST /v1/account/password` | Bearer 及当前密码 | `username`、`secret`（当前密码）、`password`（新密码）；撤销全部设备，返回新 `recovery`。 |
| `POST /v1/account/recover` | 恢复码 | `username`、`secret`（恢复码）、`password`（新密码）；一次性消耗恢复码、撤销全部设备并返回新 `recovery`。 |
| `GET /v1/account/push` | Bearer | 返回 `enabled` 与 `platform`。 |
| `POST /v1/account/push` | Bearer，手机设备 | `platform` 为 `ios` 或 `android`，`token` 为平台原生推送令牌。服务端必须已配置对应平台。 |
| `DELETE /v1/account/push` | Bearer，手机设备 | 删除推送登记。 |
| `GET /v1/account/history/settings?host={pub}` | Bearer，同账号 | 返回该电脑的 `host`、`enabled`、`generation`，默认关闭。 |
| `POST /v1/account/history/settings` | Bearer，同账号 | 输入 `host`、`enabled`；开启或关闭会更新 generation，关闭会删除该电脑的服务器副本。 |
| `GET /v1/account/history?host={pub}&generation={generation}&after={cursor}` | Bearer，同账号 | 返回设置、最多 100 条 `entries`、`cursor` 和 `more`。 |
| `POST /v1/account/history/thread` | Bearer，电脑设备 | 上传自身的 `generation`、`expected`、`deleted` 和 `thread`；返回新的目录条目。 |
| `GET /v1/account/history/thread?host={pub}&generation={generation}&id={id}` | Bearer，同账号 | 返回 `thread` 和 `revision`；不要求电脑在线。 |

注册和登录请求均包含：

```json
{
  "username": "alice",
  "password": "user-chosen password",
  "name": "My phone",
  "role": "phone",
  "pub": "base64url-ed25519-public-key",
  "proof": "base64url-ed25519-signature"
}
```

电脑使用 `role: "host"`。上面的公钥和签名字段仅说明格式，实际调用必须生成密钥并签名。使用仓库客户端或 `verify.mjs`，不要发送占位值。

公钥是原始 32 字节 Ed25519 公钥；`pub` 和签名使用无填充 base64url。签名内容为 `wuu/relay/auth/v1\0`，后接三个字段，每个字段前加 4 字节大端长度：UTF-8 `wuu/account/enroll/v1:<规范化用户名>`、原始公钥、UTF-8 设备角色。用户名按 trim、小写规范化。设备私钥仅保存在客户端。

错误体为 `{"error":"说明"}`。无效输入返回 400，凭据无效或越权返回 401，注册或推送未启用返回 403，未知接口返回 404，账号或设备冲突返回 409，认证限速或繁忙返回 429，身份查询时数据库不可用返回 503。客户端仅对明确的身份失效清除登录，不把网络故障解释为设备撤销。

## 会话历史同步

数据库 schema 3 增加 `conversation_sync` 和 `conversation_copies`。每台电脑有独立的同步开关、generation 和递增 revision；只有该电脑的身份可以上传自己的会话。`thread` 保存 `id`、`title`、`updated_at`、`status` 和 `messages`，每条消息包含 `id`、`turn_id`、`role`（`user` 或 `assistant`）、`text`。服务器运营方可以读取文字副本；工具输出、隐藏提示、附件和工作区文件不在该接口范围内，文字副本不能用于恢复 Agent 执行。

电脑保持手机访问功能运行时，每轮同步完成后等待 10 秒再检查变化；同步不依赖手机连接。每次上传带前一 revision（`expected`，首次为 `"0"`）和当前 generation，过期上传返回 409。相同内容不会增加 revision。归档保留历史，电脑删除会话后上传删除标记；读取正文返回 404，增量目录中的 `deleted: true` 通知手机清理缓存。单个会话快照上限为 4 MiB，超过时拒绝同步并记录主机错误，不截断正文；先前已同步的副本继续保留。

客户端把目录和 cursor 一起持久化，按 `more` 继续翻页；正文 revision 与目录不一致时重新获取正文。generation 变化时丢弃旧目录及缓存并从新一代开始。关闭同步或移除电脑会删除在线数据库中的副本，备份副本由部署者管理。移除手机仅撤销其读取权限，不删除电脑在服务器上的历史。

历史接口的 401 也可能表示目标电脑被移除。客户端应复核 `/devices`：该接口仍成功时保留登录并清理被移除电脑的缓存；该接口也返回 401 时清理账号凭据及全部历史缓存。临时网络错误保留有效缓存，离线设备在下一次联网时才会获知撤销。

## 设备连接

`/v1/connect` 是 WebSocket 入口。客户端发送协议版本 1 的 `hello`，包括设备 `pub`、`role`；手机还提供所选电脑的 `to`。服务端发送随机 challenge，设备使用私钥签名后发送 `auth`。服务端检查设备属于已登记账号，且目标电脑属于同一账号，才返回 `auth_ok`。

每次路由继续核对账号归属，不能用旧配对的登记消息绕过账号限制。客户端与电脑完成经认证的临时密钥握手后，服务器只转发密文帧。密钥格式及加密向量由 `internal/remote/secure` 与 `clients/core` 共同测试，不需要部署者配置全局会话解密密钥。

设备列表中的在线状态表示当前中继连接存在；电脑断线不会触发云端执行。密码修改、恢复或设备撤销会让账号连接重新握手。HTTP 令牌和设备身份具有不同用途：HTTP 令牌有效期为 90 天，设备身份持续有效直到撤销；令牌过期后需重新登录管理账号。

## 可复现验证

按 [部署说明](README.md) 构建和启动，在隔离数据库上运行 `verify.mjs create`、`verify`、`revoke`。这会真实注册两个账号、登记电脑和手机、验证归属拒绝、撤销与一次性恢复码。停止再启动同一数据库后再次执行 `verify` 可验证持久化。

```sh
export WUU_TEST_DATABASE_URL='postgres://wuu:TEST_PASSWORD@127.0.0.1:5432/wuu_test?sslmode=disable'
go test -race ./internal/remote/account ./internal/remote/host ./internal/remote/relay ./internal/remote/server ./internal/remote/secure
```

测试为每个用例创建独立 schema 并在结束时删除，测试账号需要 CREATE SCHEMA 权限；仅使用专门的测试数据库。未设置 `WUU_TEST_DATABASE_URL` 时数据库集成用例明确跳过；CI 的 Go 检查提供 PostgreSQL 并执行这些用例。

`TestConversationPublisherPersistsHistoryWithoutAnOnlineHost` 使用真实电脑会话存储、app-server 快照、上传器、HTTP 和 PostgreSQL，验证开启同步、服务端重启后的离线读取、电脑恢复后的追加、归档、删除和权限撤销，不调用模型服务。

中继测试包括跨账号帧拒绝、设备撤销后的重连拒绝，以及服务停止时关闭已登录和未完成握手的连接。系统推送配置和真实平台验收要求见 [PUSH.md](PUSH.md)。


## GitHub 登录

`GET /v1/account/config` 增加 `github: boolean`，表示是否启用 GitHub；`registration` 同时控制首次 GitHub 注册。`GET /devices` 增加 `auth_method`（`github` 或 `password`）和用于界面展示的 `display_name`；授权、设备归属仍使用稳定的 Wuu `username`。

所有领取请求使用 POST JSON，返回 `Cache-Control: no-store`。Client Secret 不参与客户端接口。

| 路径（前缀 `/v1/account`） | 方法 | 请求 / 响应 |
| --- | --- | --- |
| `/github/start` | POST | 输入 `challenge`（客户端随机 32 字节 verifier 的 SHA-256，均使用无填充 base64url）和可选 `native`；返回 `request_id`、`authorize_url`、`expires_in` |
| `/github/authorize` | GET | 浏览器打开 `authorize_url`，建立回调 cookie 并跳转 GitHub（S256 PKCE） |
| `/github/callback` | GET | GitHub 回调；验证 state、浏览器 cookie、PKCE，并读取 GitHub 数字用户 ID；仅返回完成页面，不携带 Wuu 令牌 |
| `/github/poll` | POST | 输入 `request_id`、`verifier`；返回 `status: pending`，或 `status: authorized` 和 Wuu `username` |
| `/github/complete` | POST | 输入 `request_id`、`verifier`、`pub`、`role`、`name`、`proof`；proof 与原账号登记一样签署 `wuu/account/enroll/v1:<username>`；返回 `token`、`username`、`pub` |
| `/github/cancel` | POST | 输入 `request_id`、`verifier`，取消尚未领取的登录 |

请求 10 分钟后失效；过期或领取凭据不匹配返回 401。拒绝授权返回明确错误；网络故障可继续轮询，用户也可取消并重新开始。同一请求仅登记一台设备，完成重试返回原会话。数据库 schema 2 增加 `account_identities`，不改变原账号和设备主键。
