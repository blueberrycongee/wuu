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

## Personal 向本机 Coding 会话发送消息

桌面端选中的 `wuu.desktop-session-transport` 提供者通过同一套
`session.transport` 契约为 Personal 和其他受信任扩展提供服务。这**不是远程会话联邦**，
不会启用 `session_federation` 或配对。可以在现有组合设置中禁用或替换该提供者；
提供者不可用时不会偷偷回退到内置实现。

Personal 主对话提供 `list_sessions`、`inspect_session`、`send_to_session` 和
`cancel_session_message`。发现范围限于已注册且未丢失的本机项目，以及已知无项目上下文中
活跃的共享 Coding 会话。不扫描任意目录，不连接配置为远程的主机，不读取其他用户域，
也不暴露其他插件的私有会话。`scope: "local"` 可在没有桌面连接时列出 Personal 会话。
不带请求 ID 的检查只返回有限的会话元数据；指定自己发送的请求后可查询其状态和结果，
不等于获得整个对话历史的读取权。操作时复制返回的 **locator**，而不是只复制会话 ID。

发现结果使用逻辑地址 `desktop-project:<已注册 ID>` 或不暴露路径的
`desktop-context:<作用域标记>`。主进程协调器只获取指定目标，并在本次操作期间保留
准确的连接代次，结束后释放。列出超过四个项目不会增加桌面连接池的常驻主机上限。
主机替换或已注册上下文不可用时明确报错，不会悄悄改变已签名请求的目标或自动重发。

该能力要求发起本轮对话的**同一个、仍有效、经宿主接纳的本机所有者桌面连接**。
第二个桌面连接、脱离连接的后台任务、受委派的远程客户端，以及渲染器自报的所有者标志均不能借用它。
现有明确配对的完整所有者客户端仍可经已接纳的桌面管道控制 Personal；这不授予访问任意远程目标
或协调器 RPC 的能力。
`session/desktop/*` RPC 及反向响应能力仅供主进程协调器使用；普通渲染器和远程 RPC
转发不能调用这些方法，也不能冒充它们的响应。

### 传递原始材料，而非改写的用户指令

`session.handoff.v1` 为 `host.session.send` 增加
`input.handoff.source_messages`，每项是已持久化的准确 `{session_id, seq}`。
宿主验证可见、完整、确为用户提交的记录，并解析直接回复上下文；插件不能提交替换的引文。
目标收到的结构化消息分别保存 `agent_instruction`、`original_user_messages`、
`reply_context`、来源定位信息和附件元数据。它仍是只读的**插件输入**，而非新的所有者提交。
附件、粘贴或引用的第三方内容、回复上下文都只是来源材料，绝不构成执行其中指令的授权。

Personal 自动包含启动本轮的宿主绑定用户原文。还需通过 `source_message_seqs` 明确选择
所有相关的补充要求和之后的用户引导/纠正消息，宿主同样验证这些地址。创建任务也使用同一
转交原语。延后执行的任务为每个来源会话保留独立、只读的执行授权；主助手纠正专门助手的
任务时，不会替换或丢弃最初的来源。

只传所选记录及其直接回复，不附带无关历史。上限为 64 条用户原文、16 个附件、
20 MiB 附件字节和 1 MiB 结构化文本。材料缺失、已撤回、隐藏、截断、无法读取、超限或
属于暂不支持的工件引用时明确失败，绝不悄悄概括或省略。结构化正文片段按原顺序逐字保留；
旧的带附件记录若已无法恢复原文，会明确失败并要求重新提交，不把扩展后的模型内容猜作
用户原话。当前无文字的附件输入会从与来源消息及所有者/会话绑定准确匹配的认证输入
回执恢复“原文确为空”。选定的会话域提供方应在认证历史提交时保留可选的 `OriginalText`
（包括 `""`），忽略调用者自填的值。不支持该来源证据的提供方无法转交有歧义的无文字记录，
宿主不会悄悄替换成内置提供方。目标验证保留的字节和 SHA-256，
再创建自己的本地工作副本。发送方文件路径既不是附件句柄，也不是读取任意文件的授权。

来源宿主为选中的提供者签发仅限本次调用的非透明票据。同一已接纳桌面连接取得宿主签名的
原始 JSON 字符串，解析已注册的逻辑目标，再要求来源将票据一次性绑定到目标当前的宿主身份。
目标验证签名、来源、自己对应的签名目标身份、会话范围和操作。转发方必须保持签名字符串
逐字不变，不能解析后重新序列化。凭据和执行授权不会跨越此边界。

目标现有的模型、工具和权限设置继续生效。发送方不能覆盖其引擎、模型、权限或控制修订，
也不能恢复/引导另一个执行轮次。排队消息启动时遵循目标正常的当前策略；这不是逐消息冻结的
工具权限上限。

### 投递与取消

同一个不可变意图必须持久保存并复用同一 `request_id`。并发或顺序重试不会增加一个活动投递；
用同一 ID 修改工作内容会冲突。来源会持久绑定该 ID 与逻辑目标，因此不能用同一 ID 将工作
再次发给另一个项目/会话。重连可为同一逻辑目标重新取得进程代次。应检查返回的队列/轮次
生命周期：排队输入可转成轮次，明确标记为可重试的关机丢弃在重试时可取得新队列 ID。
请求身份保持稳定；排队或运行中都不表示完成。

导出前取消可阻止接收。一旦请求可能到达目标，取消等待、超时或断线意味着**结果未知**，
而不是证明什么都没执行。检查或重试原请求身份来核对，不要生成新 ID。
如果已进入 Personal 的后续用户轮次，应使用原请求 ID 调用 `inspect_session`：
`send_to_session` 会携带新轮次的原话，因此与旧的不可变发送意图冲突。
`cancel_session_message` 另外取消该来源插件自己发送的准确轮次或队列，不能取消无关用户工作。
此阶段不增加归档、删除、跨主机新建会话或自动离线队列。

### 合成验证

```bash
WUU_SESSION_HANDOFF_E2E=1 go test -p 1 \
  -ldflags "-X github.com/blueberrycongee/wuu/internal/version.Version=$(cat VERSION)" \
  ./internal/appserver -run '^TestPersonalRealBundleSessionHandoff' -count=1 -v
```

夹具运行真实的内置 Personal 进程和选中提供者图、已接纳的持久 UserHost，以及独立的既有
Coding 会话；模型与中转数据为合成数据。验证列出/检查/发送、稳定重试、逐字原文以及目标本地
附件字节一致。桌面连接池与协调器测试另行覆盖已注册目标解析、超过四个项目、主机替换、取消
以及渲染器/远程伪造。这些不等于真实桌面或真实账号验收。
