# 账号与连接服务 API

此服务实现 Wuu 手机与电脑的账号、设备目录、加密连接和通知。会话、工作区与 Agent 运行在电脑；服务器没有会话消息数据库。手机通过 `/v1/connect` 转发现有 app-server RPC。

## 代码入口

| 模块 | 实现 |
| --- | --- |
| 独立可执行程序 | `cmd/wuu-server/main.go` |
| 参数、TLS、服务生命周期、可选 Web 资源 | `internal/remote/server` |
| 注册、登录、密码恢复、SQLite 数据库和设备归属 | `internal/remote/account` |
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

错误体为 `{"error":"说明"}`。无效输入返回 400，凭据无效或越权返回 401，注册或推送未启用返回 403，未知接口返回 404，账号或设备冲突返回 409，认证限速或繁忙返回 429。客户端仅对明确的身份失效清除登录，不把网络故障解释为设备撤销。

## 设备连接

`/v1/connect` 是 WebSocket 入口。客户端发送协议版本 1 的 `hello`，包括设备 `pub`、`role`；手机还提供所选电脑的 `to`。服务端发送随机 challenge，设备使用私钥签名后发送 `auth`。服务端检查设备属于已登记账号，且目标电脑属于同一账号，才返回 `auth_ok`。

每次路由继续核对账号归属，不能用旧配对的登记消息绕过账号限制。客户端与电脑完成经认证的临时密钥握手后，服务器只转发密文帧。密钥格式及加密向量由 `internal/remote/secure` 与 `clients/core` 共同测试，不需要部署者配置全局会话解密密钥。

设备列表中的在线状态表示当前中继连接存在；电脑断线不会触发云端执行。密码修改、恢复或设备撤销会让账号连接重新握手。HTTP 令牌和设备身份具有不同用途：HTTP 令牌有效期为 90 天，设备身份持续有效直到撤销；令牌过期后需重新登录管理账号。

## 可复现验证

按 [部署说明](README.md) 构建和启动，在隔离数据库上运行 `verify.mjs create`、`verify`、`revoke`。这会真实注册两个账号、登记电脑和手机、验证归属拒绝、撤销与一次性恢复码。停止再启动同一数据库后再次执行 `verify` 可验证持久化。

```sh
go test -race ./internal/remote/account ./internal/remote/relay ./internal/remote/server ./internal/remote/secure
```

中继测试包括跨账号帧拒绝、设备撤销后的重连拒绝，以及服务停止时关闭已登录和未完成握手的连接。系统推送配置和真实平台验收要求见 [PUSH.md](PUSH.md)。
