# 可替换提供者

Wuu 通过同一张带版本的服务图选择存储、权限策略、协调和应用外壳实现。原生模块与进程插件声明相同的服务描述及依赖。选择提供者不会自动迁移数据，也不会授予调用方其他模块的权限。

模块和绑定配置见[产品组合](plugin-system.md)，进程实现见[运行时编写指南](plugin-authoring.md)，渲染器外壳见 [UI SDK](ui-sdk.md)。下文的 TypeScript 名称均由 `@wuu/plugin-sdk` 导出，无需导入源码仓库内部路径。

## 服务与职责边界

| 服务 | 随产品提供的实现 | 作者入口 |
| --- | --- | --- |
| `state.documents@1` | `wuu.state.sqlite`、`wuu.state.jsonl` | `documentStore`、`documentStoreDescriptor`、`StateQuery` |
| `state.credentials@1` | `wuu.credentials.os`、`wuu.credentials.file` | `credentialStore`、`CREDENTIAL_STORE_DESCRIPTOR` |
| `state.config@1` | `wuu.config.local` | `configurationStore`、`CONFIG_SOURCE_DESCRIPTOR` |
| `state.plugin-settings@1` | `wuu.settings.local` | `pluginSettingsStore`、`PLUGIN_SETTINGS_STORE_DESCRIPTOR` |
| `state.artifacts@1` | `wuu.artifacts.local` | `artifactStore`、`ARTIFACT_STORE_DESCRIPTOR` |
| `host.data.query@2` | `wuu.index.trace` | `dataIndex`、`queryData`、`DATA_QUERY_DESCRIPTOR` |
| `security.authorize@1` | `wuu.permissions.default` | `authorize`、`AUTHORIZATION_SERVICE_DESCRIPTOR` |
| `project.coordination@1` | `wuu.project-coordination` | `projectCoordination`、`PROJECT_COORDINATION_DESCRIPTOR` |
| `host.managed-session@1` | `wuu.managed-sessions` | `managedSessions`、`MANAGED_SESSION_DESCRIPTOR` |
| `ui.shell@1` | `wuu.default-shell` 或渲染器插件 | `APPLICATION_SHELL_SERVICE`、`ApplicationGenerationAPI` |

提供者将描述符放入 `provided_services`；消费者在 `required_services` 中声明服务名和上表对应的主版本号，缺失时必须阻止激活的依赖还需 `required: true`。导入客户端本身不授予调用权限。渲染器服务声明放在包的 `desktop.services` 中。

底层状态服务供实现者和获得调用准入的消费者使用。普通插件通过 `host.settings.get/list`、`host.storage.*`、`host.credentials.request` 和 `importArtifact(host, input)` 操作自身设置、不透明存储、带认证的请求及工具产物。这些网关保留所有权检查，再路由到所选后端。请求里的 `subject`、`owner`、`plugin_id` 或 `session_id` 都是领域数据，不是身份凭证。

宿主保留执行准入、取消、生命周期顺序、OS 锁、权限执行、托管会话创建和传输。所选权限提供者决定允许或拒绝；允许结果不会关闭原生沙箱约束。协调提供者拥有角色策略、指令、工作状态转换和调度。外壳拥有布局与展示，应用动作仍由宿主提供。禁用提供者会停止其行为；所选实现失败不会悄悄启用原生替代品。

渲染器消费者声明 `desktop.required_services`，通过 `api.invokeService({ service, method, params }, options)` 调用。宿主根据该消费者的依赖选择提供者，并绑定插件身份、generation 和 execution。Options 携带工作区选择和取消。`invokeRuntime` 仍是不透明地请求同一插件的运行时，不负责选择服务实现。将 UI 消费者与业务提供者保持为可独立选择的模块，才能让同一 UI 使用替代实现而不启动旧提供者的行为。

## 使用当前回调的绑定

从当前工具、服务或能力回调收到的 `RuntimeHost` 构造客户端。它携带该回调的执行作用域。不要将它缓存为进程全局客户端，不要在一次操作中途重新获取最新服务图，也不要在调用失败后直接访问文件作为后备。

```ts
import {
  configurationStore,
  type RuntimeHost,
  type ServiceRequirement,
} from "@wuu/plugin-sdk";

// Include this declaration in initialize().required_services.
export const requiredServices = [
  { name: "state.config", major_version: 1, required: true },
] satisfies ServiceRequirement[];

export async function replaceSource(host: RuntimeHost, source: string) {
  const config = configurationStore(host);
  const before = await config.read();
  // source is standard base64 containing the complete replacement bytes.
  return config.compareExchange({ expected_revision: before.revision, source });
}
```

这些帮助函数只发起一次调用，保留错误及 `RuntimeHostServiceError.code`，不增加重试或后备逻辑。`service_unavailable`、取消和撤销都不授权改走新 generation。已保留的工作继续使用捕获的服务图；移除模块会跨保留 generation 撤销它。依赖方清理结束前，提供者保持可用。普通读写方法不是通用清理接口；关闭期间所需的持久化准入由共享生命周期所有者负责。

只有配置的 `read` 与插件设置的 `read` / `read-state` 声明准备阶段读取，用于不产生持久写入的候选验证；注册表仍检查真实的准备准入。其余上述状态方法要求活动执行准入。

## 有界文档查询与持久化结果

`StateQuery` 对 `namespace`、可选 `prefix`、显式 `keys`、包含下界 `start` 和不包含上界 `end` 取交集。键按 UTF-8 字节排序，`reverse` 反转顺序。空 `keys` 数组不选择任何文档。正数 `limit` 范围为 1–10000；省略或零仍表示无界读取。

```ts
import { documentStore, type RuntimeHost } from "@wuu/plugin-sdk";

export async function readNotes(host: RuntimeHost) {
  const store = documentStore(host);
  const query = { namespace: "research.notes", prefix: "item/", limit: 100 };
  let page = await store.read(query);
  const documents = [...page.documents];
  while (page.next_cursor) {
    page = await store.read({
      ...query, cursor: page.next_cursor, at_revision: page.revision,
    });
    documents.push(...page.documents);
  }
  return documents;
}
```

游标是本方向上最后一个键的排他边界，不是不透明快照令牌。继续读取时保持选择条件不变，并携带返回的 revision。存储内任意位置的中间提交都会产生 `state_conflict`；丢弃已累计的页，从第一页重新执行纯读取。该机制不保留旧快照。Revision 是十进制字符串，不要转换为 JavaScript number。

事务包含稳定的 `id`、`expected_revision` 和不重复的写入。整个存储的 revision 必须匹配，包括其他 namespace 的变化和新插入的键。成功提交在确认前已原子、持久地完成。响应丢失或提交结果未知时，重试**完全相同的事务**，包括 ID、预期 revision 和写入内容，以取回持久化回执。相同 ID 搭配不同意图会得到 `state_operation_conflict`。确定的冲突允许重新读取并构造新事务；结果未知的写入不允许这样做。

取消会停止等待，并阻止尚未获得准入的工作；它不能撤销持久提交或外部副作用。存储回执不授权重放推理、HTTP 写入、工具副作用或消息投递。这些操作需要各自的核对证据。

导出包含 revision、文档和带意图摘要的回执，迁移必须完整保留三者。可选 `compact()` 返回 `StateCompaction`，不改变文档 revision 或回执。可选 `import(snapshot)` 返回 `StateImportResult`；收到确认不等于已验证迁移。提供者仅在实现后通过 `documentStoreDescriptor({ compact: true, import: true })` 声明这两个方法。基础协议仍是 `read`、`commit`、`export`。

产品的文档会话存储覆盖会话元数据/历史、执行与恢复记录、inbox/control 状态、驱动绑定和 Project 状态。旧 SQLite 转换读取已停止的写入方，保留源文件和原始行，并在同一事务提交转换文档及完成标记。未知的非空表和目标数据冲突会明确失败。完成标记防止旧行覆盖已经演进的文档；旧源必须保持不变。

仅修改后端配置不构成数据迁移。先停止业务消费者，导出所选权威存储，导入目标，完整导出核对文档和回执，再发布目标并允许消费者重启。原生提供者还保护共享的持久权威指针和活动成员关系；权威变化后旧句柄会失败。不要让两个权威并发写入，也不要把恢复旧配置当成撤销迁移。验证恢复前，保留原存储及所需 WAL 文件。

原生目录管理使用带显式目录的 `wuu state check/export/import/migrate`，见[文档存储命令](plugin-system.md)。外部提供者需要实现相应服务协议和迁移流程，不能依赖原生目录命令完成自身后端迁移。

## 凭据、配置、设置、产物与索引

| 协议 | 可观察行为 |
| --- | --- |
| 凭据 | `get`、`set`、`delete`、`compare-exchange`。字节使用标准 base64。`null` 表示不存在，`""` 表示存在且为空。CAS 比较字节；`value: null` 删除，`{ exchanged: false }` 表示确定不匹配。`set` 的值不能为 null。 |
| 配置 | `read`、`compare-exchange`。`source` 用 base64 保留完整原始字节，字节缺失时为 `null`；`config` 是验证后的产品有效配置。`expected_revision` 比较源身份。响应可能包含 `committed: true` 和 `activation_error`；此时应报告写入已持久化，但应用失败。 |
| 插件设置 | 对 `{ subject, scope: "user" \| "workspace" }` 执行 `read`、`update`、`read-state`、`update-state`。更新在 `expected_revision` 上替换完整值映射，设置还携带 fingerprint。收到 `settings_conflict` 后先重读，再构造更新。 |
| 产物 | `import`、`resolve`、`read`、`fork`。提供者 scope 来自获得准入的消费者。读取要求正数 `max_bytes`，返回 base64 字节与 MIME 类型。Resolve 返回该产物是否由提供者管理。普通工具导入使用带认证的网关。 |
| 数据索引 | `host.data.query@2` 提供 `call` 查询和 `append-turn` 完整回合原子追加。查询接收 thread、可选事件类型、turn 和 limit；未知类型名也可作为过滤条件。省略或零 limit 不设置显式上限；负数返回 `invalid_params`。事件不授予配置或凭据访问。 |

原生配置文件缺失时读出 `source: null`，存在但零字节时读出 `source: ""`。解码前先判断 `null`，客户端不得将这两个值互相归一化。配置 CAS 的 `source: null` 表示提供零个替换字节：原生文件模式会写入空文件，而不是删除；可信项目模式拒绝空 JSON。CAS 响应即使在空文件已存在时也可能保留 `null`，因此该字段描述返回的源字节，不是文件是否存在。

配置和设置 revision 都是不透明字符串，与文档存储的数字 revision 不同。这两个服务不提供文档事务回执协议。只有确定的 CAS 冲突可以在重读后重试；不可用或已取消的写入需要先核对结果。不要记录凭据字节、原始配置或包含秘密的有效配置。

原生配置模式保留来源边界：显式文件模式使用指定文件，嵌入模式从传入配置开始，显式项目模式使用项目基础配置和设置层，仅在 `.wuu.json` 不存在时回退到 `wuu.json`。叠加在用户配置上的受限项目覆盖不会获得替换凭据或授权的权限。

模型认证及登录/刷新状态使用捕获 generation 的凭据服务。原生 auth 适配器可以在不覆盖目标已有条目的前提下导入旧账号数据，该兼容行为属于原生实现。外部凭据提供者不会被悄悄包上原生文件查询。进行中的登录 ID 保持关联原 generation，直至清理结束。

## 完整回合索引

选中的 `host.data.query@2` 提供者同时拥有完整回合写入和查询。v1 只授予读取权限，v2 增加追加权限，因此消费者必须明确声明主版本 2。`DATA_QUERY_DESCRIPTOR` 以 `2.0.0` 声明两个方法，不提供隐式 v1 适配或另一个原生写入者。

```ts
import {
  dataIndex, type DataAppendTurn, type RuntimeHost, type ServiceRequirement,
} from "@wuu/plugin-sdk";

export const requiredServices = [
  { name: "host.data.query", major_version: 2, required: true },
] satisfies ServiceRequirement[];

export async function recordTurn(host: RuntimeHost, batch: DataAppendTurn) {
  const index = dataIndex(host);
  const receipt = await index.appendTurn(batch);
  const facts = await index.query({
    thread_id: batch.thread_id, turn_id: batch.turn_id,
  });
  return { receipt, facts };
}
```

`DataAppendTurn` 包含 `thread_id`、`turn_id` 和 `events`。每个事件必须携带匹配的两个 ID 和 JSON `data`；整批以 `turn` 事件开始，以 `final` 结束，中间的未知事件类型原样保留。提供者必须原子提交完整批次，不得只提交前缀。

`DataAppendTurnResult` 包含 `version`、两个 ID 和 `committed`。相同回合、相同事件批次再次追加时，返回带 `duplicate: true` 的已提交回执；同一回合使用不同事件则返回 `index_conflict`。取消或传输失败可能导致结果不确定。SDK 每次调用只尝试一次，不会自动重试不确定的追加；先向选中提供者核对结果，再决定下一步。

`queryData(host, input)` 和 `dataIndex(host).query(input)` 访问同一个选中的 v2 服务，保留原查询过滤器和事件格式。结果保持提供者顺序，先过滤再应用 limit；`events: null` 和空数组保留各自的协议值。消费者可以用返回事件计算上下文摘要，无须打开原生 trace 文件。

查询结果和追加回执都可包含 `diagnostic_path`。它只是导出元数据，不是访问权限或备用数据源。旧 JSONL 导入和诊断导出由选中的原生提供者拥有；使用外部索引不会触发原生文件读写。导出失败不能撤销已确认的追加。禁用选中索引会停止其行为，不会自动启用原生存储。延迟完成的回合应一直保留获准执行时的 generation，直到追加结束。

## Project 协调协议

`project.coordination@1` 承载业务策略，`host.managed-session@1` 承载执行机制。原生协调器只在启用 Project Agent 功能的构建中选用；托管会话机制独立支持普通托管会话。默认协调器声明 `state.documents@1`、`host.managed-session@1` 和 `host.background@1`；每个消费者使用自己捕获的服务图身份。

`projectCoordination(host)` 为 `session`、`work`、`membership`、`enqueue` 和 `admission` 提供类型化调用。`ProjectSessionRequest` 与 `ProjectWorkRequest` 保留既有动作词汇。Session/work/membership 的结果随动作变化，因此在调用者验证前明确保留为 `unknown`。完整服务描述还包括下列提供者回调：

| 方法 | 输入与结果 |
| --- | --- |
| `session`、`work` | 带 `actor_id`、可选 `call_id` 和 `session` 或 `work` 的命令 envelope。结果取决于动作。 |
| `membership` | `project_id`、`session_id`、`action: "adopt" \| "release"`。 |
| `admission` | 空请求，返回 `{ max_parallel }`。容量约束仍由宿主 membership/lease 事务执行。 |
| `enqueue` | 项目、会话和客户端 ID，cause、content、wake。记录输入与实际投递是两件事。 |
| `work-list`、`member-work`、`coordinator` | 项目查询 envelope；成员查询还含 `session_id`。分别返回工作列表、`{ work, found }`、`{ project, found }`。 |
| `inbox`、`schedule`、`recover`、`capacity` | 会话/项目 envelope；capacity 还接收 `action` 并返回布尔值。没有项目参数的 recover 扫描协调器 inbox。 |
| `create`、`turn`、`user-input` | 命令 envelope 中包含原生 thread-start 选择、turn 完成或聊天消息数据；不是普通 `host.session.*` 请求结构。 |
| `instructions`、`tool-config` | 直接传序列化 `session.Session`，**不套命令 envelope**；返回指令文本或 `{ allow?, deny? }`。 |
| `tool-policy`、`tool-validate` | `{ member, call, read_only, policy }`，使用原生 session 与工具调用数据。`ProjectToolPolicy` 标识写 lease 和工作 revision；取得 OS lease 后再次验证。 |
| `scheduler` | 仅所有者可调用的长期后台回调。注册表认证调用者；设置领域 ID 不能启动它。 |

原生回调结构由[协调分发器](../../../internal/appserver/project_provider.go)和[工具策略协议](../../../internal/appserver/project_work_tools.go)定义。SDK 帮助客户端覆盖常用业务命令，不复制全部原生 session/turn 记录。替换提供者必须处理消费者实际调用的方法，保留取消与结构化错误，并在清理结束前保留依赖。停止调度会取消计时器和等待，不会把已获准入的前台工作改路由到新提供者。

`managedSessions(host).create(input)` 复用公共会话创建字段，新增 `owner`、`source`、可选角色、显式会话 ID、共享工作区会话 ID 和初始 inbox 输入。显式身份与初始输入一同提交。`submit` 返回 `{ admitted, capacity_full }`：容量已满是需要处理的结果，不表示消息已经执行。`allow_start: false` 只尝试 steering。`interrupt` 指向宿主工作区内已存在的会话。

SDK 通过 `ManagedSessionTextMessage` 支持文本提交。其 `Role`、`Content`、`ClientID` 和来源字段保留当前 Go wire 大小写，`display_content` 则有显式 JSON 标签。`ManagedSessionInbox` 同样使用 `ClientID`、`SessionID`、`Content`、`Wake` 和 `Controls`。不要替换为模型请求的 snake_case 消息类型。Control revision 阻止接管或释放后的旧指令执行，execution ID 来自宿主认证的回调。

这些协议说明可调用行为。SDK 包测试和合成 UI 预览不能证明完整产品迁移、真实推理或 macOS 原生界面验收。依赖替换实现前，应在真实选定的产品服务图中验证，并保留可重复检查的证据。
