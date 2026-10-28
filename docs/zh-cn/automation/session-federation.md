# 跨主机会话

跨主机会话是显式启用的路由契约：一台主机上的 Agent 可以创建、发送、检查或取消另一台主机上的会话。它复用已有远程 RPC 连接，不会迁移运行中的 Agent，也不把共享存储变成双活执行。

## 启用接收主机

在接收主机的**用户配置**中添加以下配置；项目配置不能启用或扩大此权限上限。provider 和 model 必须替换为该主机已有的真实配置名称：

```json
{
  "session_federation": {
    "enabled": true,
    "host_id": "my-laptop",
    "models": { "my-provider": ["my-model"] },
    "permission_modes": ["read_only"],
    "tool_allow": ["read_file"],
    "tool_deny": []
  }
}
```

这只是执行能力上限，**不是访问授权**。选中的 `resource.authorization` 提供者仍须显式允许已验证身份的 `session/federation.create`、`.send`、`.inspect`、`.cancel` 或 `.list` 操作。create/list 使用接收主机的工作区资源，其余操作使用精确会话资源；列表还会逐条检查 inspect 权限。配对设备的 owner 标记不授予此权限，因此默认的 owner-only 策略拒绝跨主机会话请求。

上限约束 provider/model、权限模式和工具，默认只读。模型或工具白名单为空时不能创建或发送。调用者只能进一步收窄工具。发送到已有会话时，该会话持久保存的执行权限和工具策略也必须满足上限；读取、取消与发送权限独立。

接收方每次操作读取所选配置。禁用能力或撤销资源授权后，后续请求和回执读取被拒绝；已经完成的工作不会撤回，已接收的 turn 也不会自动停止。停止工作需使用单独授权的精确 turn 取消操作。

使用 `wuu remote host --pair --delegate-only` 为云端代理配对。该设备会持久保存
受限角色；没有明确策略授权时，不能调用普通所有者 RPC。同一密钥重新配对
不能隐式升级为所有者。已有普通所有者配对行为保持不变。

受限配对单独保存在 `remote.json` 的 `delegated_devices` 中，绝不写入旧版用于
所有者的 `devices` 集合。旧版软件无法使用这些受限配对，也不会自动把它们
提升为所有者。旧版写入程序可能丢弃不认识的集合，因此再次升级后可能需要
重新登记受限设备。

本版本打开较早实验格式时，会先迁移 `devices` 中标记为
`delegated_only: true` 的条目；发现重复设备权限记录则拒绝打开，且不改写文件。
这类实验数据必须先用修正版打开，**再考虑降级**：旧版无法安全解释原来的
逐条角色标记，即使增加 `v` 版本号也无效。普通所有者配对及账户凭据保持不变。

## 从插件路由

公共 SDK 的会话传输辅助函数定义 `SessionLocatorV1` 和 `session.transport` 提供者描述。声明消费 `host.session.route` 和 `session.transport`；远程请求调用所选传输提供者的 `dispatch`。提供者缺失或禁用时直接失败，不回退到本地会话。`execution_host: "local"` 显式使用已有本地处理器；原有 `host.session.*` 调用仍保持本地行为。

远程适配器接受已经认证的 RPC 客户端，例如已有远程客户端，再发送 `session/federation`。凭据和身份验证属于连接，不能由请求载荷声明。不要把凭据放入模型输入；每个主机标识应绑定固定主机身份的连接。locator 本身不是授权证明。

```json
{
  "version": 1,
  "operation": "create",
  "operation_id": "my-stable-create-id",
  "locator": { "version": 1, "execution_host": "my-laptop" },
  "params": {
    "visibility": "user",
    "context_source": "fresh",
    "provider": "my-provider",
    "model": "my-model"
  }
}
```

创建返回含 session ID 的 locator。send/inspect/cancel 的 `params.session_id` 必须与 locator 相同；create/send 如提供 `request_id`，必须等于 `operation_id`。inspect 可用 send 的 operation ID 作为 `request_id`，配合已有终态等待参数获取进度和结果。cancel 必须指定精确 `turn_id` 或 `queue_id`。

`execution_host` 选择执行主机。`storage_host` 是接收方配置的描述（默认与主机 ID 相同），不是调用者选择存储或发起迁移的指令；`display_host` 仅描述展示位置。会话身份是 `(execution_host, session_id)`，不能只用 session ID。

## 重试和恢复

跨连接重试应保留同一 operation ID。接收方通过选中的 `state.documents`，先持久保存 create/send/cancel 的接收回执，再执行副作用。相同请求重试返回原回执；同一 ID 换成不同请求会失败。响应 `completed` 表示 RPC 操作完成，不表示 Agent turn 已完成。

- `pending`：共享接收方中的已接收操作仍在进行。
- `unknown`：操作已接收，但最终回执不可用，例如执行后、写入最终回执前崩溃；不会自动重复执行。
- 发送前已知离线返回离线错误；发送后断线表示结果未知，不表示执行失败。

重连已有远程传输后，以**同一请求**查询回执，再检查精确会话/turn。不能仅因为响应丢失就换 operation ID。当前不提供持久离线投递队列、自动故障转移或模糊结果自动重跑。

第一版仅在接收方配置的工作区创建 fresh 会话；不跨主机传递父会话历史、fork、选择 worktree、迁移正在执行的会话或覆盖 engine。上述功能需要额外的显式能力。原生 GUI 的主机选择复用同一主机限定路由，与 Agent 委派授权分开。

## 无外部推理验证

```bash
go build -o .tmp/bin/wuu ./cmd/wuu
WUU_E2E_CORE=.tmp/bin/wuu node scripts/session-federation-e2e.mjs
go test ./internal/remote/host -run '^TestSessionFederationTwoHostsEndToEnd$' -count=1 -v
```

测试使用隔离的云端/本地 app-server 与存储、真实 TCP 云端入口、已有加密 relay 连接和合成模型，覆盖默认本地行为、远程创建、权限收窄、幂等重试、断线重连、最终结果、精确取消和撤权。它不代替真实机器联网、已安装引擎或 GUI 渲染验收。

脚本通过 npm 打包公开 SDK 及远程客户端的导出源码包
`@wuu/remote-core`（当前为 private、版本 `0.0.0`，不是 npm 已发布包），在独立目录启动真实云端、接收端与
中继进程，使用全新身份及本地模拟模型。JSON 验收记录保存在
`.tmp/e2e/session-federation/`，不记录设备私钥。

验收产物只上传 `results.json` 和 `commands.json`，不要包含独立的模拟凭据文件。
使用仓库要求的 Node/Go 工具链，先安装 `packages/plugin-sdk` 依赖；`npm pack`
会自动构建 SDK。
