# 身份与资源访问

Wuu 将三个决策分开：

- `identity.authentication@1` 验证传输层凭据，建立调用者身份。
- `resource.authorization@1` 判断该身份能否对请求涉及的每个资源执行指定操作。
- `security.authorize@1` 判断 Agent 能否执行工具。资源授权不会替代工具执行策略，也不会自动扩大文件系统权限。

默认资源策略允许可信本地所有者和已经登记的所有者设备；其他身份必须获得所选策略的明确授权。安装提供方本身就是信任决策。Wuu 不会沙箱化或认证这些提供方。

## 验证外部连接

以提供方认证模式启动 app-server：

```sh
wuu app-server --config /path/to/config.json --workdir /path/to/workspace \
  --authentication=provider --authentication-audience=my-wuu-host
```

输入的第一行必须是 v1 `authenticate` 帧。通过私有输入流发送凭据，不要将其放入命令行参数、URL 或日志：

```ts
import {authenticationHandshake} from '@wuu/plugin-sdk';
child.stdin.write(JSON.stringify(authenticationHandshake('Bearer', token)) + '\n');
```

主机将启动配置中的 audience 传给所选身份提供方。对端不能声明 owner、可信身份或替换 audience。认证缺失、格式错误或验证失败会结束此连接，不会降级为本地访问。默认的 `--authentication=local` 仅适用于嵌入程序的可信本地管道，不能用作不可信网络代理的入口。

成功响应包含身份和有限的 RFC3339 `expires_at`。身份由 `{kind, issuer, tenant, subject}` 构成，`kind` 为 `user`、`device` 或 `agent`。subject 是不透明标识；不同发行方和租户的相同 subject 不是同一身份。解码声明不等于验证身份。

适配器实现 SDK 的 `identityAuthenticationDescriptor()` 服务及 `authenticate` 方法。输入为 `{scheme, token, audience}`，输出为 `{identity, expires_at}`。适配器负责相应协议的签名、发行方、audience、有效期和撤销检查。OIDC 和 SAML 适配器可实现相同契约，但 Wuu 没有提供通用的 OIDC 发现或 SAML 登录实现。

主机会在受保护操作以及输出／重放之前重新验证原凭据。重新验证使用当前选中的提供方，拒绝身份变化，也不会延长连接原有的有效期。提供方报错、被移除或凭据已撤销时，访问会被拒绝。需要新认证会话时应重新连接。

## 选择资源策略

原生提供方为 `wuu.resource-policy.default`，原生消费者为 `wuu.resource-access`。可以通过普通 composition 服务绑定替换提供方：

```json
{
  "service": "resource.authorization",
  "major": 1,
  "provider": "example.enterprise-access"
}
```

替代提供方发布 `resourceAuthorizationDescriptor()` 并处理 `authorize`。请求包含：

| 字段 | 含义 |
| --- | --- |
| `principal` | 已验证、带命名空间的身份 |
| `owner` | 主机建立的所有者权限，不从应用请求参数复制 |
| `action` | 精确 RPC 方法、`event:<名称>` 或 `request:<反向 RPC 名称>` |
| `resources` | 涉及的全部资源；允许决策必须覆盖每一个资源 |
| `attributes` | 规范化的执行意图，例如 provider、model、engine、权限模式和工具边界；不含提示词或凭据 |

返回 `{allow: true}` 或 `{allow: false, reason?: string}`。不要将全局授权理解为对某个命名资源的授权。`{kind: "global"}` 表示进程级操作；`{kind: "session", id: "…"}` 表示对话。分享读取使用独立的 `session-share` 资源，不会获得原对话的任意访问权。资源 ID 以响应主机为作用域；联邦协议单独标识主机。

普通 `thread/start` 同时需要全局创建范围及 `{kind: "workspace", id: "<解析后的绝对根目录>"}`。主机会解析省略的默认目录与符号链接别名，并在创建前重新验证实际目录；只有全局创建授权不够。工作区读取也验证解析后的根目录；Git 仓库根目录不同时，还需该实际根目录的权限。工作区列表逐项过滤目录及当前工作区元数据，并为重放保留相同范围。

可用 `resourceAuthorizationProvider(handler)` 构建 SDK 描述符和分发器。策略输入包含请求的执行意图，因此提供方可以拒绝权限模式升级或未经允许的模型，无需替换传输层。业务适配器可定义有文档说明、带命名空间的新意图属性，但不得复制凭据、提示词或任意内容。

`initialize.resource_access` 发布版本 1 和 `delegated_actions`：它们表示协议能力，不是授权。未经接入执行准入检查的旧修改操作，即使策略返回允许，也会对非所有者关闭。支持的修改范围为普通 `thread/start`、`thread/delete`、`turn/start`、`turn/interrupt`、产物读取取消及单独验证的联邦操作。通用 project／handoff／worktree 创建、compact、排队 turn、模型／设置修改和插件管理尚不属于委派能力。下游新增处理器时需一起注册能力、资源提取和执行前检查，不能绕过策略。

`plugin/client/request` 与 `plugin/client/cancel` 也支持非所有者准入，但不会一次性授权全部插件方法。每次请求必须命名完整路由：`plugin-subject`、`plugin-fingerprint`、`plugin-generation`、`plugin-method`，以及声明依赖的 `plugin-service` 或 ID 为 `plugin.client.request` 的 `plugin-capability`。可取消请求还包含 `plugin-client-request`；用户宿主调用另需宿主绑定的不可变 `user-space`。宿主会独立验证保留图中的真实插件包及已声明能力／服务，并在分发前再次检查所选策略。取消沿用原在途请求的身份和捕获的操作，不重新选择当前图。响应交付与重放重新检查同一路由及嵌套资源范围。插件安装、配置、存储管理及其他尚未支持的管理方法仍不向非所有者开放。

未知 app-server 操作不会对非所有者使用通配授权。每个新方法都必须声明资源。列表与搜索会逐条过滤对话元数据；读取以及附件／产物请求各自验证。通知和反向请求也需要各自的操作权限。允许读取对话不代表允许开始 turn、批准工具或控制本地 UI。

## 撤销与重放

首次分发的属性表示传入意图。创建普通线程或准入 turn 前，主机会解析默认值和持久化的 provider／model／权限模式／engine，再用实际执行属性验证策略；响应与重放也保留这些解析后的属性。有上限的策略应拒绝缺失属性，不能假定匹配。通用 RPC 不支持也不信任工具边界字段；只有真正执行边界的适配器才可暴露它们。

主机不会缓存允许决策。它在分发之前、输出结果之前检查所选策略，检查每条列表记录和事件，并为可重放帧保留原始身份、操作、资源和意图属性。重放传输层在交付之前重新检查这些权限。已撤销的帧会被丢弃；客户端应重新获取已授权快照，而不是依赖缓存的授权。

删除会在生命周期锁等待之后重新检查；turn 在持久化刷新和 hook 后、用户消息追加前、线程互斥锁之外重新检查。策略等待期间也受身份有效期约束。这些是明确的执行准入边界，不是分布式回滚事务。撤销会阻止新的准入和后续数据交付，但不会撤回已接受的工作，也不能保证终止不支持取消的外部后端操作。显式操作取消是另一条机制。联邦操作还会去掉委派调用的环境所有者权限，并应用接收方配置的执行限制。

## 可复现的提供方验收

[独立访问提供方](../../../examples/plugins/independent-access/provider.ts) 使用固定公钥 JWK 验证合成的 Ed25519 签名 JWT，并独立维护授权和撤销状态。它仅导入打包后的公开 SDK。这是契约验收用例，不是生产身份提供方。

```sh
go build -o bin/wuu ./cmd/wuu
node packages/plugin-sdk/test/resource-access-package.mjs
```

测试正常安装独立包，驱动真实认证可执行程序，验证范围内读取／列表元数据、请求权限升级、撤销、所有者冒充、策略不可用以及无效签名／发行方／audience／有效期。脱敏协议记录和可执行文件／包的哈希保存于 `.tmp/e2e/resource-access/`。测试不使用企业账户或推理服务。

## 共享桌面传输边界（v1）

委派远端控制端复用现有桌面进程和会话归属，不另建一个默认拥有者权限的
app-server。`@wuu/protocol` 中公开的 `TrustedTransportPeer`、
`TrustedTransportDispatch` 和回执类型描述可信宿主之间的私有转发协议：

1. 远程宿主通过加密握手和持久化配对记录验证设备密钥。带随机密钥的本地
   回环握手绑定身份、拥有者状态、唯一连接标识和实时认证检查能力；这些字段
   绝不来自 RPC 参数。身份缺失或版本协商失败时拒绝连接。
2. 桌面拒绝远端 `transport/*`，仅通过显式可信的本地 stdio 调用
   `transport/dispatch`。核心按连接固定身份，使用所选资源策略，并在受保护操作
   前重新检查原认证状态。
3. 实时检查能力是带随机 bearer 密钥的回环 `POST /v1/validate`；只有 HTTP 204
   表示有效。重定向、检查服务不可用、授权撤销或过期均拒绝。密钥不会发给远端
   控制端或渲染进程。
4. 核心为每个响应保留原授权闭包并返回私有回执。`transport/check` 重新检查原动作、
   资源、执行约束及操作特定检查；`transport/event` 分别检查每个订阅者的当前策略。
   桌面缓存和 Go 重放缓冲保留并检查这些回执，不以连接是否为拥有者替代资源检查。
   回执丢失或淘汰时拒绝发送。
5. `transport/release` 撤销连接绑定与回执，不接管会话生命周期。注册表和队列有界；
   超限只会使授权失效，不会扩大权限。手机重连可继续使用现有桥接连接；桥接失效后
   重新取得经过授权的快照。

这是供可信嵌入宿主替换实现的小协议，不是另一套登录或可自行选择拥有者身份的公开
接口。身份提供者连接、普通配对连接及嵌套转发不能调用它。下游不必复制完整 Electron
桥接，但必须保留实时认证、逐帧回执和默认拒绝的重放语义。

委派连接的桌面系统命令和旧式插件渲染调用目前默认拒绝，等待这些适配器提供可限定
资源范围的公开契约。这是支持下游组合的基础设施，不是云代理界面或分享成品；现有
拥有者配对的桌面能力保持可用。

设置 `WUU_AUTH_BINARY` 指向构建后的核心，运行
`node desktop/scripts/trusted-desktop-e2e.mjs`。验收在隔离用户目录中构建仅依赖公开 SDK
的独立策略，经真实加密设备链路、桌面桥接和进程池访问现有本地会话，输出
`result.json` 与 `go-test.log`。不要发布合成用户目录和私有传输能力。

桌面转发为请求、响应检查、事件检查和释放操作捕获原本地进程及连接代次；事件授权
来自实际发出事件的连接，不会稍后按工作目录重新查找。选择远端 GUI 连接器时，不会
向其发送本地实时认证能力。可信外壳适配器可在发出有归属的操作时登记固定清理动作；
连接退役前，在有界等待内对原连接代次执行这个无参数释放动作。路由变更后数据读取
仍被隔离，清理不能更换方法、参数或目标，也不会落到替代连接上。
