# 可替换提供者

Wuu 通过同一张带版本的服务图选择会话、工具、agent 策略、存储、权限、协调和应用外壳实现。原生模块与进程插件声明相同的服务描述及依赖。选择提供者不会自动迁移数据，也不会授予调用方其他模块的权限。

模块和绑定配置见[产品组合](plugin-system.md)，进程实现见[运行时编写指南](plugin-authoring.md)，渲染器外壳见 [UI SDK](ui-sdk.md)。下文的 TypeScript 名称均由 `@wuu/plugin-sdk` 导出，无需导入源码仓库内部路径。

## 服务与职责边界

| 服务 | 随产品提供的实现 | 作者入口 |
| --- | --- | --- |
| `session.domain@1` | `wuu.session.default` | `sessionDomain`, `sessionDomainProvider`, `sessionDomainDescriptor` |
| `agent.tools@1` | `wuu.tools.default` | `toolService`, `TOOL_SERVICE_DESCRIPTOR` |
| `session.title@1` | `wuu.title.default` | `titleService`, `TITLE_SERVICE_DESCRIPTOR` |
| `agent.prompt@1` | `wuu.prompt.default` | `promptService`, `PROMPT_SERVICE_DESCRIPTOR` |
| `agent.compaction@1` | `wuu.compaction.default` | `compactionService`, `COMPACTION_SERVICE_DESCRIPTOR` |
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

## 会话、工具与 agent 策略提供者

`session.domain@1` 替换会话领域操作，包括元数据、历史、控制与恢复转换、成员准入和推理日志资源。`state.documents@1` 只替换文档存储；单独选择它不会替换会话算法。原生会话提供者拥有这些算法并使用所选文档存储。外部会话提供者自行实现公共操作；缺少方法或调用失败后，宿主不会代为运行原生算法。

使用 `sessionDomainProvider(handlers)` 声明已实现的方法，并分发类型化参数数组和结果数组；`sessionDomain(host).call(method, args)` 是绑定消费者。保留导出记录的字段名，包括明确规定的 Go 大小写。日志句柄属于认证调用者及提供者 generation。遇到 `session_outcome_unknown`，应先检查持久结果再决定是否重试，不能把它当作写入失败的证明。OS 锁和执行身份仍由宿主负责，原子成员变更与领域决策由提供者负责。

`agent.tools@1` 替换工具注册表和执行流程。`list` 提供定义、发现目录和工具面事实，`resolve` 提供可用性与元数据，`execute` 执行所选实现；`freeze`、`unfreeze`、`prepare` 和 `finalize` 保持回合内的工具面与结果行为。贡献工具是另一种操作：贡献内容进入所选注册表的策略。原生提供者仅在被选中时初始化 Toolkit；外部选择不能隐式初始化原生 Toolkit 或合并其目录。通过 `toolService(host)`、`TOOL_SERVICE_DESCRIPTOR` 及认证宿主网关实现工具与 OS 操作。

`agent.prompt@1` 拥有主会话和 worker 的完整系统提示词及分段元数据。`promptService(host).assemble(request)` 接收环境、指令、技能、所选工具面事实和显式贡献；worker 请求还在 `worker` 中携带角色、隔离方式与身份事实。提供者决定措辞和顺序，消费者不能添加原生基础提示词、追加 worker 策略，或把空结果替换为继承指令。错误应阻止请求。会话标题使用下述独立选择的 `session.title` 服务。插件命令的侧边对话提示词仍独立于主会话/worker 契约；只替换 `agent.prompt` 不会替换这两种行为。

`agent.compaction@1` 通过 `describe`、`compact`、`window`、`handoff`、`note-plan`、`compact-note`、`recover` 和 `note-attempt` 拥有上下文策略。使用 `compactionService(host)` 与 `COMPACTION_SERVICE_DESCRIPTOR`，在要求时携带捕获的策略 key。笔记准备、触发间隔、重试和完整推理消息由所选策略负责；宿主负责历史校验、checkpoint 提交、用量记账及已获准入的模型传输。恢复调用同一个所选服务，不隐式切换到原生替代品。handoff 的字节上限为零表示策略不设字节上限，模型和传输限制仍然有效。旧压缩 capability 是原生提供者的显式兼容行为，不是外部提供者的第二条选择路径。

这些契约保留 Go 宿主和公共 SDK。作者通过普通服务声明及回调绑定的 `RuntimeHost` 实现进程插件，无须替换内核、导入私有源码或使用特权原生 API。

## 会话标题策略

`session.title@1` 是会话执行消费者的可选依赖，随产品提供的 `wuu.title.default` 模块实现原生标题策略。不选择该服务会禁用自动标题生成和显式重新生成；所选提供者失败时报告错误，不选择原生 fallback。标题服务独立于 `agent.prompt` 选择。

使用 `TITLE_SERVICE_DESCRIPTOR` 实现 `plan` 和 `resolve`，通过 `@wuu/plugin-sdk` 的 `titleService(host)` 调用捕获的绑定。`plan` 接收 `TitlePlanRequest`：模型元数据、宿主投影的 `human_messages`、现有标题与摘要、只读/临时/父会话事实，以及 `force`/`persist`。这些事实不授予权限。返回的 `TitlePlan` 选择以下一种动作：

| 动作 | 结果与行为 |
| --- | --- |
| `skip` | 可选 `reason`；不执行推理或写入标题。 |
| `complete` | 返回非空 `title`；不要求模型运行时，不应用原生标题清理或长度策略。 |
| `infer` | 返回使用公共 `ModelMessageV1` 的完整 `messages` 和可选 `temperature`；宿主调用所选模型传输。 |

消息字段使用公共模型 wire（`role`、`content`、`reasoning_content` 等已声明字段），不使用 Go 记录序列化名称。显式零温度会被保留并覆盖配置的采样默认值，模型能力限制仍优先。推理成功后，`resolve({ text })` 返回 `{ title }`；SDK helper `titleService(host).resolve(text)` 直接返回字符串。解析后的标题为空时跳过持久化。提供者拥有触发条件、来源选择、提示词措辞、采样策略和输出清理；替换原生提供者即替换这些业务策略。

宿主保留认证输入、重复任务抑制、捕获的 generation、模型回执记账、原子生成标题更新及通知。回执失败会阻止保存或交付生成的标题。生成标题更新不会覆盖已有标题，包括并发的手动重命名。SDK 调用成功不能证明自动触发、取消和保留 generation 的清理均已通过产品验收，应分别验证这些路径。

## 产品 profile 与渲染器绑定

组合配置版本 2 包含 `headless` 和 `desktop` profile。两者均使用 `wuu.base` bundle；desktop 再加入包含默认外壳及设置渲染器的 `wuu.desktop`。显式配置优先于推导出的产品默认值。Profile 按顺序应用 bundle，再应用显式模块、服务选择及 patch。显式空模块集合保持为空。纳入包不会授予信任或绕过准入，依赖仍需有效选择。配置格式见[产品组合](plugin-system.md)。

外壳拥有布局，必需的界面区域拥有独立的所选服务绑定。外壳中绑定服务的 slot 按插件、generation 和贡献身份解析所选内容，不依赖外壳自己的 slot 命名空间。所选外壳或必需区域缺失时，应报告不可用，不能渲染原生替代品。通用可选 slot 仍可声明 fallback。使用公共 `ApplicationGenerationAPI` 视图、动作和 [UI SDK](ui-sdk.md)，不要依赖私有应用状态。选择另一个外壳本身不能证明所有业务副作用或界面区域都已迁入独立提供者。

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

关闭 deadline 限制调用者的等待时间，不代表未完成清理的生命周期结束。应保留提供者依赖、注册表绑定和包资源，直到实际清理完成，并明确报告超时或仍待完成的清理。关闭超时不允许提前释放这些依赖，也不能证明旧行为已经全部停止。应通过实际产品入口验证取消、延迟清理和撤销。

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

### 分页导出

`store.export()` 仍发送 `export {}` 并返回完整 `StateSnapshot`。实现分页的提供者声明 `documentStoreDescriptor({ paged_export: true })`。只有显式启用才将原有 `export` 方法的输入/输出 schema 标识改为 `state.documents.export.input.v2` 和 `state.documents.export.output.v2`；服务仍为 `state.documents@1`。其他方法和旧 descriptor 保持 v1 schema。分页没有独立的线上方法，也不会自动回退为全量导出。

使用公开 `StateExportPageRequest` 类型调用 `store.exportPage(request)`，线上请求为 `export { page: request }`：

```ts
const page = await documentStore(host).exportPage({
  section: "documents", target_bytes: 1024 * 1024, max_items: 100,
});
```

`StateExportPage` 返回 `{ format: "page.v1", revision, generation, section, documents, receipts, done }`。未使用的分段为空数组。文档按 `(namespace, key)` 的 UTF-8 字节顺序排列；回执按整数 revision 严格递增，revision 使用十进制字符串。游标不能转成 JavaScript number，也不能使用 locale 排序或 UTF-16 排序。

文档续页从最后一条实际文档生成 `after: { namespace, key }`，并将 `at_revision` 和 `generation` 固定为第一页返回值。文档结束后，以相同绑定导出 `section: "receipts"`，从最后一条回执生成 `after: { revision }` 继续。所有续页和所有回执请求都必须同时提供这两个绑定；文档首页若指定绑定，也须成对提供。`done: false` 要求至少返回一项，并越过请求游标。revision 或 generation 变化使本次扫描失效；丢弃不完整结果，从一致的权威存储重新开始读取。SDK 不会重试错误。

`target_bytes` 是 1 到 33554432（32 MiB）之间的整数，`max_items` 是 1 到 10000 之间的整数。字节目标计算实际 UTF-8 JSON 页，且至少须容纳外层字段；无效预算返回 `invalid_request`。多项页必须满足目标；单条超大记录可以超过软目标，分页不会新增文档大小硬上限。外部进程传输现有的 64 MiB 帧限制仍然有效：单条记录无法放入帧时明确失败，不走回退路径。宿主 checkpoint 扫描默认请求每页 32 MiB、256 项。

应逐页消费，而不是重新积累完整快照。宿主 checkpoint 的 payload 内存为 `O(页 + 最大记录)`，另需 `O(回执 ID)` 的校验元数据。这不代表固定 RAM 上限：JSONL 冷启动重放已有大帧仍有独立的内存分配边界，`export {}` 也仍会将完整快照载入内存。迁移核对必须覆盖两个分段的全部内容，包括回执摘要；单页结果或导入确认都不足以证明迁移完成。

## 凭据、配置、设置、产物与索引

| 协议 | 可观察行为 |
| --- | --- |
| 凭据 | `get`、`set`、`delete`、`compare-exchange`。字节使用标准 base64。`null` 表示不存在，`""` 表示存在且为空。CAS 比较字节；`value: null` 删除，`{ exchanged: false }` 表示确定不匹配。`set` 的值不能为 null。 |
| 配置 | `read`、`compare-exchange`。`source` 用 base64 保留完整原始字节，字节缺失时为 `null`；`config` 是验证后的产品有效配置。`expected_revision` 比较源身份。响应可能包含 `committed: true` 和 `activation_error`；此时应报告写入已持久化，但应用失败。 |
| 插件设置 | 对 `{ subject, scope: "user" \| "workspace" }` 执行 `read`、`update`、`read-state`、`update-state`。更新在 `expected_revision` 上替换完整值映射，设置还携带 fingerprint。收到 `settings_conflict` 后先重读，再构造更新。 |
| 产物 | `import`、`resolve`、`read`、`fork`、`delete-thread`。提供者 scope 来自获得准入的消费者。读取要求正数 `max_bytes`，返回 base64 字节与 MIME 类型。Resolve 返回该产物是否由提供者管理。普通工具导入使用带认证的网关。 |
| 数据索引 | `host.data.query@2` 提供 `call` 查询、`latest-context` 有界最新请求元数据读取，以及 `append-turn` 完整回合原子追加。查询接收 thread、可选事件类型、turn 和 limit；未知类型名也可作为过滤条件。省略或零 limit 不设置显式上限；负数返回 `invalid_params`。事件不授予配置或凭据访问。 |

原生配置文件缺失时读出 `source: null`，存在但零字节时读出 `source: ""`。解码前先判断 `null`，客户端不得将这两个值互相归一化。配置 CAS 的 `source: null` 表示提供零个替换字节：原生文件模式会写入空文件，而不是删除；可信项目模式拒绝空 JSON。CAS 响应即使在空文件已存在时也可能保留 `null`，因此该字段描述返回的源字节，不是文件是否存在。

配置和设置 revision 都是不透明字符串，与文档存储的数字 revision 不同。这两个服务不提供文档事务回执协议。只有确定的 CAS 冲突可以在重读后重试；不可用或已取消的写入需要先核对结果。不要记录凭据字节、原始配置或包含秘密的有效配置。

原生配置模式保留来源边界：显式文件模式使用指定文件，嵌入模式从传入配置开始，显式项目模式使用项目基础配置和设置层，仅在 `.wuu.json` 不存在时回退到 `wuu.json`。叠加在用户配置上的受限项目覆盖不会获得替换凭据或授权的权限。

模型认证及登录/刷新状态使用捕获 generation 的凭据服务。原生 auth 适配器可以在不覆盖目标已有条目的前提下导入旧账号数据，该兼容行为属于原生实现。外部凭据提供者不会被悄悄包上原生文件查询。进行中的登录 ID 保持关联原 generation，直至清理结束。

## 会话产物清理

`ARTIFACT_STORE_DESCRIPTOR` 以 `1.1.0` 声明 `state.artifacts@1`，新增 `delete-thread`：输入 `{ thread_id }`，结果 `{ thread_id, deleted: true }`。获得准入的消费者使用捕获的 host 调用 `artifactStore(host).deleteThread({ thread_id })`。成功表示所选提供者已移除该会话当前的载荷及清单；会话不存在或已经为空也成功。其他会话、fork 副本、索引数据和会话文档不属于此操作范围。

删除前先停止产物生产者并等待其完成。该操作不会禁止同一 ID 之后再次导入。原生导入、fork 和删除按目标会话跨进程串行化，并保留锁文件 inode。清理仍是普通的已准入服务调用，不绕过生命周期准入。会话文档已移除但产物清理失败时，应报告部分完成，并保留同一提供者和会话以供显式重试清理；不要重建文档，也不要用删除原生路径代替所选提供者。调用结果不确定时，helper 不会自动重试；删除具有幂等性，调用者可以显式再次执行。

## 完整回合索引

选中的 `host.data.query@2` 提供者同时拥有完整回合写入和查询。v1 只授予读取权限，v2 增加追加权限，因此消费者必须明确声明主版本 2。`DATA_QUERY_DESCRIPTOR` 以 `2.0.0` 声明 `call`、`append-turn` 和 `latest-context` 三个方法，不提供隐式 v1 适配或另一个原生写入者。

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

`dataIndex(host).latestContext({ thread_id })` 调用 `latest-context`，返回 `DataLatestContextResult`。它选择最近提交且包含请求的回合中的最后一条请求。事件数组最多包含四个事件：匹配的回合元数据、恰好一条 `context_requests` 记录、可选的同一步 provider state，以及可选的终态元数据。之后没有请求的回合不会给旧请求换上新的回合信息。该投影排除工具载荷、checkpoint、回答正文和此前步骤；空数组表示确定不存在已索引请求，错误不得变成部分结果或旧数据的成功响应。

最新上下文响应具有 16 MiB 预算。所需元数据超过预算时返回 `limit_exceeded`，不得截断或回退到全历史读取。原生提供者将投影放在同一个原子索引文档的历史事件之前提交；普通读取在该前缀结束，与历史总量无关。旧格式 1 文档和仅有 JSONL 的旧数据兼容读取也受同一 16 MiB 预算限制；更大的旧数据会明确失败，直到下一次普通追加完成导入或升级。离线 replay 仍可使用。此边界只适用于最新上下文读取；原生追加和诊断导出仍重写累计历史，不承诺有界写入性能。

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

## 在产品中验证替换

通过实际消费者验证要替换的能力。选中一个文档存储只能证明存储替换，不能证明会话创建、历史或恢复策略已被替换。注册额外工具只能证明工具贡献，不能证明工具选择或分发已被替换。渲染另一个 shell 只能证明 shell 选择，不能证明其中每个界面区域都能独立替换。

使用合成数据，记录选中的提供者、调用入口、实际结果和可重复执行的命令或产物。依赖替换实现前，覆盖以下情况：

| 情况 | 可观察结果 |
| --- | --- |
| 选中替换实现 | 产品操作到达该提供者并返回其结果；原实现不再执行同一个操作。 |
| 移除选中提供者，或让其失败 | 消费者报告依赖不可用或原始错误，不通过原生 fallback 继续执行。 |
| 活跃工作期间改变组合 | 已获准入的工作保留捕获的绑定；新工作使用新获准入的服务图，撤销对保留的 generation 仍然有效。 |
| 取消与释放 | 停止等待，在释放提供者资源之前完成依赖方清理；已释放的订阅或计时器不再产生效果。 |
| 在请求数据中填写其他所有者身份 | 领域字段不会改变认证调用者的权限。 |
| 替换有状态提供者 | 显式迁移保留所需数据和回执；准备失败保留当前活跃组合。 |

对实际交付的每个产品入口执行检查。直接调用 SDK 客户端可以验证请求编码和错误传播，却不能证明桌面或无界面消费者实际经过了该客户端。将这些结果与真实推理、原生界面验收分开记录。
