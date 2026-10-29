# 可替换提供者

Wuu 通过同一张带版本的服务图选择会话、工具、agent 策略、存储、权限、协调和应用外壳实现。原生模块与进程插件声明相同的服务描述及依赖。选择提供者不会自动迁移数据，也不会授予调用方其他模块的权限。

模块和绑定配置见[产品组合](plugin-system.md)，进程实现见[运行时编写指南](plugin-authoring.md)，渲染器外壳见 [UI SDK](ui-sdk.md)。下文的 TypeScript 名称均由 `@wuu/plugin-sdk` 导出，无需导入源码仓库内部路径。

## 服务与职责边界

| 服务 | 随产品提供的实现 | 作者入口 |
| --- | --- | --- |
| `session.domain@1` | `wuu.session.default` | `sessionDomain`, `sessionDomainProvider`, `sessionDomainDescriptor` |
| `agent.tools@1` | `wuu.tools.default` | `toolService`, `TOOL_SERVICE_DESCRIPTOR` |
| `agent.tool.batch@1` | `wuu.tools.v1-batch` | `toolBatchResources`, `TOOL_BATCH_SERVICE_DESCRIPTOR` |
| `session.title@1` | `wuu.title.default` | `titleService`, `TITLE_SERVICE_DESCRIPTOR` |
| `agent.prompt@1` | `wuu.prompt.default` | `promptService`, `PROMPT_SERVICE_DESCRIPTOR` |
| `agent.policy@1` | `wuu.agent.policy.default` | `agentPolicy`, `agentPolicyServiceDescriptor`, `composeAgentPolicies` |
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

## 局部扩展 provider

整块替换 provider 仍然受支持。对于小范围行为变更，也可以用适配插件提供相同契约，
在 `required_services` 中声明该契约，并转发未修改的方法；不需要复制默认实现，
也不需要导入宿主内部代码。

`delegateService(descriptor, overrides)` 会生成 `invokeService` 处理函数。
只有指定的方法执行自定义逻辑，其余已声明的方法通过**当前回调的** `RuntimeHost`
调用配置的委托目标。完整的[设置装饰示例](../../../examples/plugins/settings-decorator.ts)
仅在设置读取视图中补充缺失的默认值，设置写入及不透明状态的读写仍由原 provider 完成。

两条路由由组合配置的管理者选择。将以下示例 ID 换成已安装或发现的适配插件 ID；
选择模块不会自动安装或授予信任。

```json
{
  "version": 4,
  "profile": "headless",
  "patches": [{
    "modules": {"example.settings-decorator": true},
    "services": [
      {"service": "state.plugin-settings", "major": 1, "provider": "example.settings-decorator"},
      {"consumer": "example.settings-decorator", "service": "state.plugin-settings", "major": 1, "provider": "wuu.settings.local"}
    ]
  }]
}
```

第一条是全局选择，第二条只改变适配插件已声明的依赖。委托目标既可以是原生 provider，
也可以是外部插件。整条链仍使用同一个依赖图：目标不可用、消费者未声明依赖、
同名方法的 schema 不兼容或出现依赖环时，准备阶段会拒绝候选组合。
消费者模块也必须包含在组合中。

显式委托目标缺失、禁用或失败时，不会回退到全局 provider；覆盖方法报错时，也不会
补跑原方法。`remove_services` 通过相同的 `consumer`、`service` 和 `major`
只移除这一条局部选择。服务诊断保留消费者和绑定来源，继承的运行时组合也保留这些路由。

委托保留适配插件自身的身份、捕获的 generation、取消和清理依赖；它**不会**冒充
上游调用者、转移调用者的工具权限，或让不同 provider 的资源句柄可以互换。
这类方法仍须遵循各自契约明确的所有权和委托机制。

对于已获准入的 `agent.tools.execute`，在覆盖方法中显式调用
`continueService<ToolServiceExecution>(host, input)`，参见
[工具服务装饰器](../../../examples/plugins/tool-service-decorator.ts)。在
`required_host_features` 中声明 `SERVICE_CONTINUATION_HOST_FEATURE`
（`service.continuation.v1`），提供 `TOOL_SERVICE_DESCRIPTOR`，在 runtime 需求中声明
`agent.tools@1`；仅 renderer 的依赖不能建立后端委托边。
与设置示例相同，将装饰器选为全局提供者，并将它自身的同服务依赖绑定到原生或外部实现。
其他方法仍可通过普通 `delegateService` 转发。

独立的 `host.service.continue` 入口只接受当前回调的执行 ID 和不透明的
`continuation_id`。SDK 绑定实际回调 ID；宿主捕获原服务主版本、方法、参数、工具身份、
generation 和已配置的委托目标。续调不能改写请求、改选目标，也不能把权限借给普通
下游回调。每层装饰器只能续调一次，委托失败也会消耗它，不会重试或回退。
回调返回、取消或显式移除/禁用后失效；正常 generation 替换保留已获准入的回调及其
原依赖，旧续调不会跳转到新实现。原生工具提供者同样必须拥有当前回调自己的工具
准入，检查发生在初始化后端之前；继承的后端 scope 或上游 `caller_scope` 都不够。
过去仅在原生实现中碰巧可用的普通 `execute` 转发，现在必须改用 `continueService`。
进程执行仍经过所选权限与沙箱服务；保留工作不会保留先前的权限决策。

运行 `node scripts/service-continuation-e2e.mjs` 可验证仅依赖打包 SDK 的装饰器和独立
进程后端，覆盖多层装饰、权限拒绝、原生与外部委托的普通调用拒绝、重复及失败续调、
取消、所有者及委托者
移除、暂停后的权限撤销，以及暂停在续调前发生的 generation 替换。报告与日志保存在 `.tmp/e2e/service-continuation/`。

在启用 `project_agent` 的开发构建中，[项目装饰器](../../../examples/plugins/project-decorator.ts)
只修改指令，并启用 `concurrentServices`，因为托管会话创建可能在返回前重入协调服务。
将其选为 `project.coordination@1` 提供者，再把它的同服务依赖绑定到
`wuu.project-coordination`（或另一个委托提供者）。原生协调器处于所选协调服务委托链中时，
仍拥有自己的调度器，也支持多层装饰器；无关服务依赖不会启动协调调度。移除或禁用
所选装饰器不会回退到原生协调器。停止自主派发后，已获准入的回合仍保留原服务图。

每个组合中的 `state.documents@1` 只有一个逻辑数据集和权限边界。局部路由可以连接
被选中 provider 的适配链，普通消费者不能绕过该边界直接选择基础实现。
独立数据集应使用隔离的[运行时组合](plugin-compositions.md)。整块替换 provider
和经验证的状态迁移要求不变。

安装仓库依赖后运行 `node scripts/provider-delegation-e2e.mjs`。它使用打包后的公共 SDK
编译独立适配插件，验证原生和外部委托链、持久设置、项目委托调度、保留回合结算、拒绝及清理路径，并将
`report.json`、`verification.log` 写入 `.tmp/e2e/provider-delegation/`。


## 替换单个内置工具

选择原生 `agent.tools@1` 注册表时，可以用受信任的插件工具替换 `web_search`，其他工具保留原实现。先安装并启用插件，再在可信用户的 agent 配置中填写实际的插件清单 ID 和声明的工具 ID；项目设置不能选择替代实现：

```json
{
  "agent": {
    "tool_replacements": {
      "web_search": {"plugin_id": "example-search", "tool_id": "search"}
    }
  }
}
```

[合成搜索示例](../../../examples/plugins/synthetic-web-search.ts)可直接运行，用于说明插件编写；它返回固定的演示结果，并不进行真实搜索。使用 `wuu plugin dev` 安装后，将上方的 `example-search` 替换成 Wuu 返回的插件 ID。目前支持 `web_search` 和下述受限的 `read_file` 文本与可选图片契约。替代实现必须声明与原工具完全相同的输入 schema、freeform 和 direct-only 设置，不能是编排工具。插件描述以所选原生工具的名称暴露，插件自身的命名空间名称则隐藏。目标缺失、禁用或不兼容时会失败，不会回退；删除绑定是显式选择，后续准备的 generation 使用原生实现。

对于 `read_file`，在同一 `tool_replacements` 映射中选择插件及工具。[本地读取示例](../../../examples/plugins/local-text-reader.ts)读取不超过 256 KiB 的真实本地文件；使用 `wuu plugin dev` 安装，并绑定其实际插件 ID 和工具 ID `read`。它是可信本地代码，运行在宿主路径检查边界之外。注册信息必须声明 `replacement_contracts: ["read_file.text.v1"]`、只读且非编排的 activity，并与原工具的输入 schema 和执行标记完全一致。每次新读取时，提供者收到路径、可选的从 1 开始的行偏移和行数限制，以及含 `cwd` 的认证回调上下文。文本读取返回 `structured_content: {text, total_lines}`，并可按下文所述附加分页游标：`text` 是保留原始换行的完整所请求 UTF-8 行范围，`total_lines` 是源的总行数。宿主校验范围一致性和大小上限，为模型生成行号，并向 Code Mode 提供原始文本。响应无效或失败时调用报错，不切换到其他读取者。

提供者还可在 `replacement_contracts` 中声明 `read_file.image.v1`，接受本地图片的新读取请求。宿主识别所请求的本地文件为图片，并拒绝行范围。提供者只返回一个内联 `image` 内容项，包含匹配的 `mime_type` 和 base64 数据；插件结果协议将解码后的输入限制为 2 MiB。宿主检查格式与解码尺寸、拒绝动态图 GIF，并使用原生图片处理路径，再向支持图片的模型与 Code Mode 转发。小图可能保留原始元数据和字节；该处理不等于清除元数据。响应标记为未经验证的扩展内容。仅声明文本契约的提供者仍会拒绝图片请求。

本地契约仅支持普通文本与受支持的图片，其他二进制及直接使用插件自定义续读 token 均不支持。宿主在调用前执行原生路径及敏感文件检查；插件进程仍是受信任代码，这些检查不会自动限制其进程内操作。兼容原生格式的历史 `read_file` 续读和托管会话文件继续由宿主读取，即使插件已退役也一样，仍须通过原有权限检查。插件退役后，Code Mode 只暴露历史续读入口，新读取会失败。扩展返回的内容标记为 `source: "extension"`、`content_verified: false`，不会建立原生的活动文件新鲜度记录；成功读取后，同一文件的旧记录会失效。该记录是上下文信息，并不是编辑权限。提供者应在工具描述中向模型说明这些限制。

对于固定的远程工作区，可信文本提供者还可声明 `read_file.remote.v1` 并依赖 `execution.native-tool-continuation@1`。通过 `wuu plugin dev` 安装[远程读取示例](../../../examples/plugins/remote-text-reader.ts)，在可信用户设置的 `agent.tool_replacements.read_file` 中绑定实际插件 ID 和工具 ID `read`。回调内的 `getProcessEnvironment(host)` 返回宿主选定的后端、根目录和身份，`input.cwd` 是该远端根目录。调用一次无参数的 `continueNativeTool(host)`，由固定的 worker 执行原始读取，再返回有界的 `structured_content: {text, total_lines}`。宿主和 worker 分别复查权限，仅支持文本的插件只收到校验后的文本。worker 不支持能力、actor 或环境变化、取消、目标禁用或插件退役时调用失败，不回退本地。同一受信插件可主动把仍存活的回调 host 分享给进程内其他回调；宿主绑定原始权限，不隔离插件进程内部的代码。

远程替换支持工作区相对路径的新鲜 UTF-8 文本读取，可指定行偏移和行数。提供者还可声明 `read_file.image.v1`：worker 必须先对经过授权的原始请求返回受支持的图片，宿主校验并规范化有界图片后才交给提供者。提供者返回一个内联图片，宿主再次校验并将结果标记为未经验证的扩展内容。[远程图片读取示例](../../../examples/plugins/remote-image-reader.ts)同时支持文本和图片。图片请求不能选择行范围；直接字节选择器仍不支持。启用远程分页后可使用下文说明的宿主续读 token。worker 必须支持带版本的无观察读取和观察提交能力。宿主验证最终结果后，会有条件地失效同一文件的旧原生观察；并发产生的较新原生观察会保留。直接调用和 Code Mode 遵循同一规则。同一父调用中的嵌套读取必须属于同一个 worker 和 actor，宿主会批量提交；不兼容的批次会安全拒绝。宿主在提交前重新检查每个参与项的权限；此后撤权不会追溯撤销已准入的提交。扩展输出仍未验证，也不建立原生新鲜度观察。 如果接触过远程读取的 Code Mode 父调用失败或取消，Wuu 会丢弃其所有自由文本和媒体，因为普通工具也可能产生由远程数据派生的输出。成功的父调用以及没有远程读取的程序保留原有输出。程序中其他工具已经产生的效果不会回滚。 使用持久工具账本时，嵌套工具详情和 hook 上下文在父调用结束后才投影；接触过远程读取的父调用失败时，这些内容会被抑制。 选择远程读取替换时，Code Mode 要求 worker 声明 `code.stateless.v1`，并在该程序中禁用 `store`、`load` 和 `remove`：否则程序状态会先于远程观察提交。其他 Code Mode 程序保持原有状态行为。使用此组合前须更新旧版 worker。

以前读取产生的原生格式字节续读 token 和工作区行续读 token 也由宿主原生 reader 处理。宿主还原路径和范围，按当前原生权限重新授权；工作区路径由选定 worker 读取，当前会话托管根目录下的产物由宿主读取。worker 会检查文件摘要和原生路径边界。token 必须包含有效 SHA-256 且不能超过大小限制；缺失或无效摘要会被拒绝。这些旧 token 没有认证，也不绑定会话、actor、插件或 worker，可能被伪造；所选插件不会转换或授权此原生路径。worker 重启后，另一个有权限的 worker 可继续读取未变化的文件；这与下文绑定 generation 和 worker 的插件分页 token 不同。原生字节读取保持原有观察语义，不加入扩展结算。工作区行续读需要持久工具账本，以及在现有观察结算协议之外声明 `read.native-observation.v1` 的 worker；旧 worker 会在读取之前拒绝请求。worker 私下暂存原生观察，宿主在返回读取结果前及结算过程中重新检查当前权限；只有父调用成功且授权通过后才发布观察。较新的观察（包括通过硬链接产生的观察）优先。同一批次中对同一物理文件的多次原生行读取，或原生行读取与扩展读取的组合，会安全拒绝；同一 worker 和 actor 上不同文件的读取可一起结算。会话托管的原生行续读及没有持久账本的直接调用仍不支持。远程路径不会回退到其他本地工作区文件。

远程文本提供者还可声明 `read_file.pagination.v1`；[远程分页示例](../../../examples/plugins/remote-paginated-reader.ts)展示了该契约。第一页通过选定的 worker 读取，宿主将不透明 token 绑定到插件 generation、会话、actor、环境身份和根目录、worker 实例，以及 worker 返回的整个文件 SHA-256。续读时宿主根据还原的路径和范围重新检查权限，再由同一 worker 验证文件摘要。文件变化、worker 或环境不同、generation 退役、token 过期或 worker 缺少实例身份时，续读会安全失败。插件游标和 worker 摘要不会交给模型。worker 验证源文件稳定并不代表提供者转换后的内容可信，扩展输出仍未经验证。单次原生读取仍受 worker 的大小和范围限制。

如果结算期间连接或存储确认丢失，结果可能未知，受影响的会话不能重放该调用。重新打开会话时，只有经过认证的原 worker 实例及其保留的凭据仍然可用，才能协调未决结果；不同实例或已过期的记录不能用于确认。已准备但未提交的决定会先确认撤销，再记为已撤销。如果原 worker 已退出或记录已过期，请在新会话中继续；Wuu 不假定分布式提交具有原子性。恢复依赖所选 `state.documents` 提供者保持记录完整并遵守原子事务语义；此机制不能防御恶意存储提供者篡改记录。新 actor 或新会话拥有独立读取状态。运行 `node scripts/remote-read-e2e.mjs` 可验证打包 SDK 插件与真实 command backend worker、Code Mode 和失败路径；该测试不验证生产远程服务或真实模型。

文本提供者还可声明 `read_file.pagination.v1`：当所请求范围仍有剩余行时，在 `text` 和 `total_lines` 外返回非空 `next_cursor`。每页必须向前推进、不超出请求范围，且 `total_lines` 保持一致；范围读完后必须省略 `next_cursor`。宿主返回不透明的 `continuation.next` 供下一次 `read_file` 调用，插件游标不会直接交给模型。续读时宿主验证 token，并针对还原出的路径重新检查原生及插件授权，再将原插件游标、下一行偏移及剩余限制交给提供者。提供者必须支持对同一快照重放续读；内容变化时应拒绝过期游标。宿主无法验证外部提供者所声称的快照。[本地读取示例](../../../examples/plugins/local-text-reader.ts)在页间校验内容摘要。

宿主 token 包含加密的提供者状态，绑定会话、执行角色、工作目录与确切的提供者 generation。插件游标最多 4 KiB，token 30 分钟后过期。原提供者 generation 仍可用时允许重试；退役、切换到另一 generation 或 Wuu 重启后，token 会失效，不会迁移给其他提供者。失败或取消的调用不会留下宿主逐页游标条目。原生格式的历史续读和宿主结果恢复仍使用原有路径。

运行 `node scripts/extension-read-e2e.mjs` 可验证独立进程及受控模型、直接和 Code Mode 调用、历史续读、权限和退役。该测试不证明任意提供者受到进程隔离，也不证明其内容与本地文件一致。

成功结果必须在文本或结构化结果中提供单个 JSON 对象：`action` 为 `web_search`、`query` 与请求一致；`evidence` 包含 `id`、`kind: "search"`、`source`、`source_tier`、RFC 3339 格式的 `retrieved_at` 和 `version_matched_to_repo`；此外提供 `results`（最多 10 项且每项有 URL）或非空 `error`。同时提供文本与结构化内容时，两者必须一致。evidence ID 与 source tier 只允许字母、数字、下划线和连字符。`version_matched_to_repo` 优先使用去除首尾空白并规范化空格后的 `version_hint`；没有时，按可用字段将 `package_context` 组合为 `ecosystem name@version`。超过 160 个 Unicode 字符时截断并加 `...`；没有版本信息时用 `unknown`。宿主拒绝不兼容结果，并将有效 evidence 记录到现有 evidence context。

被选中的 `web_search` 替代实现可通过 `execution.native-tool-continuation@1` 继续一次原生搜索。在 `required_services` 中声明 `{name: NATIVE_TOOL_CONTINUATION_SERVICE, major_version: 1, required: true}`，然后在 `executeTool` 回调中调用 `await continueNativeTool(host)`；两者均由 `@wuu/plugin-sdk` 导出。 [原生搜索装饰器示例](../../../examples/plugins/native-search-decorator.ts)展示了为返回标题添加标记的完整替代实现。宿主将调用绑定到精确的替代执行、原始参数和 generation，调用方不能选择其他工具、路径、身份或参数。普通插件工具和后代执行不会获得自己的续调。同一受信插件可以主动把仍存活的回调 host 分享给其进程内的其他并发回调；使用该 host 会消耗原回调的续调机会。宿主检查原始所有者、参数和生命周期，但不隔离插件进程内部的并发。失败也会消耗这次机会，宿主不会自动重试或回退。取消或回调结束后权限失效，但已发往搜索服务的请求可能已经发生。

原生结果返回给插件时不会发布中间 evidence。宿主校验替代实现的最终结果，并将有效 evidence 写入现有上下文；替代实现失败或取消时不会发布中间 evidence。现有工具账本记录外层替代调用，不会额外创建原生调用回执。此续调也支持上述受限远程 `read_file` 契约，但不是通用工作区 API。

运行 `node scripts/native-continuation-e2e.mjs` 可验证受控原生搜索替身与独立插件进程，包括直接和代码模式调用、单次使用、身份、撤权、取消及保留的 generation。受控搜索是测试替身；该检查不覆盖真实搜索服务、真实模型推理或渲染界面。

如果会话设置了工具允许列表，必须同时允许 `web_search` 和该插件工具的命名空间公开名称。运行 `wuu plugin describe --workdir <workspace>`，在 `tools` 中查看插件工具注册的名称；名称由插件 ID、声明的工具 ID 和根据冲突情况确定的后缀组成。该命令检查新建的 CLI generation，使用结果前应确认它与会话所选的 generation 一致。

工具调度采用原工具和替代实现中更严格的活动声明，插件执行经过原工具策略与插件工具授权；禁用任一名称都会阻止该执行。由宿主处理的 `read_file` 续读和托管文件调用使用原生工具授权，不依赖插件的可用性或授权。插件以已安装扩展的信任和自身回调身份运行。原生文件和进程保护不会自动约束插件进程内的任意代码：托管副作用应使用带认证的宿主网关，替代实现也必须执行适当检查。已准入工作保留其 generation；显式移除或禁用会撤销目标。冻结的工具表保留定义直到当前轮次释放，但每次执行仍检查目标是否可用。

运行 `node scripts/tool-replacement-e2e.mjs`，可验证打包 SDK 合成插件、受控模型、直接和 code mode 调用、worker 与侧会话、授权及 generation 更新。该测试不验证外部搜索服务、真实模型质量或界面呈现。

## 会话、工具与 agent 策略提供者

`session.domain@1` 替换会话领域操作，包括元数据、历史、控制与恢复转换、成员准入和推理日志资源。`state.documents@1` 只替换文档存储；单独选择它不会替换会话算法。原生会话提供者拥有这些算法并使用所选文档存储。外部会话提供者自行实现公共操作；缺少方法或调用失败后，宿主不会代为运行原生算法。

使用 `sessionDomainProvider(handlers)` 声明已实现的方法，并分发类型化参数数组和结果数组；`sessionDomain(host).call(method, args)` 是绑定消费者。保留导出记录的字段名，包括明确规定的 Go 大小写。日志句柄属于认证调用者及提供者 generation。遇到 `session_outcome_unknown`，应先检查持久结果再决定是否重试，不能把它当作写入失败的证明。OS 锁和执行身份仍由宿主负责，原子成员变更与领域决策由提供者负责。

`agent.tools@1` 替换工具注册表和执行流程。`list` 提供定义、发现目录和工具面事实，`resolve` 提供可用性与元数据，`execute` 执行所选实现；`freeze`、`unfreeze`、`prepare` 和 `finalize` 保持回合内的工具面与结果行为。贡献工具是另一种操作：贡献内容进入所选注册表的策略。原生提供者仅在被选中时初始化 Toolkit；外部选择不能隐式初始化原生 Toolkit 或合并其目录。通过 `toolService(host)`、`TOOL_SERVICE_DESCRIPTOR` 及认证宿主网关实现工具与 OS 操作。

`finalize` 对归一化后的结果只执行一次，包括执行失败和通过所选批次资源结算的拒绝。其 `additional_context` 追加在同一次调用的 execute 上下文之后；`discovered_tools` 按名称合并，同名时采用 finalize 定义。即使并行调用乱序完成，这些补充信息仍绑定到对应的已结算调用。发现的工具定义仅附在该调用对模型可见的工具结果中。上下文也适用于嵌套调用，并保存在下一次模型输入回执中，不进入普通会话历史，也不泄漏到之后的新回合。hook 装饰器、原生服务适配器和所选批次不会从旧式共享的“最后一次结果”上下文槽推断调用归属；这些集成必须提供按调用归属的上下文。共享槽 API 仅保留给没有流式推测或嵌套执行的直接串行叶子工具消费方使用。

`agent.prompt@1` 拥有主会话和 worker 的完整系统提示词及分段元数据。`promptService(host).assemble(request)` 接收环境、指令、技能、所选工具面事实和显式贡献；worker 请求还在 `worker` 中携带角色、隔离方式与身份事实。提供者决定措辞和顺序，消费者不能添加原生基础提示词、追加 worker 策略，或把空结果替换为继承指令。错误应阻止请求。会话标题使用下述独立选择的 `session.title` 服务。插件命令的侧边对话提示词仍独立于主会话/worker 契约；只替换 `agent.prompt` 不会替换这两种行为。

### 替换单个提示词分段

原生提示词提供者在主会话和 worker 结果的分段符合布局要求时，声明可选的 `wuu.prompt.sections.v1` 布局。装饰器可以调用 `promptService(host).assemble(request)`，再使用 `@wuu/plugin-sdk` 的 `await replacePromptSection(result, key, replacement)`。该方法返回新结果，保留其他分段及其元数据。[提示词分段装饰器](../../../examples/plugins/prompt-section-decorator.ts) 展示了如何替换 `environment`，同时保留委托提供者的其他提示词。

将装饰器选为全局 `agent.prompt@1` 提供者，并将装饰器自身的 `agent.prompt@1` 依赖绑定到 `wuu.prompt.default`，绑定方式与前面的提供者部分扩展相同。也可以将多个装饰器依次绑定，每层都必须保留声明的布局。委托提供者缺失或替换失败时，请求会报错，不会回退到原生提供者。

该布局以两个 LF 字节连接 UTF-8 分段内容；每个分段记录 UTF-8 字节数及 SHA-256 前 16 字节的小写十六进制值。替换前，方法会校验布局声明、非空且唯一的 key、元数据、分隔符、字节边界、哈希和完整内容；目标 key 必须存在，替换文本不能为空。结果会更新目标分段的长度和哈希。未声明布局或布局不兼容的结果会被拒绝；普通 `agent.prompt@1` 消费者仍可使用未声明该布局的提供者。提示词分段是行为指引，不能替代权限控制。

`agent.compaction@1` 通过 `describe`、`compact`、`window`、`handoff`、`note-plan`、`compact-note`、`recover` 和 `note-attempt` 拥有上下文策略。使用 `compactionService(host)` 与 `COMPACTION_SERVICE_DESCRIPTOR`，在要求时携带捕获的策略 key。笔记准备、触发间隔、重试和完整推理消息由所选策略负责；宿主负责历史校验、checkpoint 提交、用量记账及已获准入的模型传输。恢复调用同一个所选服务，不隐式切换到原生替代品。handoff 的字节上限为零表示策略不设字节上限，模型和传输限制仍然有效。旧压缩 capability 是原生提供者的显式兼容行为，不是外部提供者的第二条选择路径。

这些契约保留 Go 宿主和公共 SDK。作者通过普通服务声明及回调绑定的 `RuntimeHost` 实现进程插件，无须替换内核、导入私有源码或使用特权原生 API。

## 完整工具批次

`agent.tool.batch@1` 可在不替换循环或 `agent.tools@1` 注册表的情况下替换最终批次执行。所选提供者的 `execute` 接收不可变批次 ID、原始调用及所选串行/容量约束，自行决定调用检查、顺序、并行分发、逐调用取消与拒绝。等待所有提交的资源操作完成后，才返回 `{ completed: true }`。

若同一进程同时提供批次与工具服务，需在 `concurrentServices` 中声明两个服务名，使等待中的工具回调能够重入该进程。

声明 `TOOL_BATCH_SERVICE_DESCRIPTOR` 和 `requireToolBatchResources()`，在当前回调中使用 `toolBatchResources(host, request)`，通过 `tool.batch.resources@1` 调用 `resolve(index)`、`execute(index)`、`cancel(index)` 和 `settle(index, errorResult)`。索引只能指向已准入的原始调用，不能替换参数或执行者身份。重复 `execute` 等待同一次执行，不重复副作用，并返回已完成持久结算的结果和 invocation ID。`cancel` 阻止排队调用，或取消并等待正在执行的调用；`settle` 可拒绝尚未执行的调用，但不能覆盖已执行的结果。无论执行顺序如何，模型结果都按原始调用顺序返回。

资源边界保留权限、hooks、所选工具路由、PTC/原生嵌套调用权限、容量互斥、结果预算、日志和持久调用账本。提供者报错或缺失完成回执时，会取消并等待已接受工作，保留已完成结果，并对未启动调用恰好结算一次；不会重试结果不明的副作用，也不会换用其他后端。回调绑定已准入的 generation，新选择不会重路由在途批次。每批限制为 1–1024 个调用，原始参数合计不超过 4 MiB。基础描述符接收**完整最终批次**。提供者可额外声明 `TOOL_BATCH_STREAMING_SERVICE_DESCRIPTOR`，实现 `stream_admit`：每个完整、只读且并发安全的前缀调用先通过 `ToolBatchStreamRequest` 请求准入，只有 `{ admit: true }` 才允许提前执行，拒绝则将其及后续前缀延迟到最终批次。该回调本身不能分发副作用；最终 `execute` 仍收到全部调用，`resources.execute(index)` 等待已准入的同一次执行，不会重复执行。错误取消流并保留既有账本重试防护。这里不使用模块 ID 特判。默认适配器实现此可选方法，保留工具与模型重叠执行；只声明基础描述符的外部提供者则缓冲到最终响应。

`wuu.tools.v1-batch` 是兼容现有 `agent.tools@1` 提供者的显式适配器，使用相同公共资源，按容量并行分发安全调用，并串行执行不安全调用。产品 profile 默认包含它；自定义显式模块白名单需加入它或选择外部批次提供者。移除所选提供者会明确报不可用，不自动 fallback。[外部逆序示例](../../../examples/plugins/independent-providers/batch.ts) 与 `node scripts/tool-batch-e2e.mjs` 验证独立打包 SDK 后端，并禁用默认工具注册表及批次适配器。原生回合资源测试还会等待所选适配器启动工具后才完成模型流，并验证最终批次 join 不会重复副作用。

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

### 权威存储与部署拓扑

被选中的 provider 可声明 `documentStoreDescriptor({ authority: true })`，并实现可在准备阶段调用的 `authority {}`。`documentStore(host).authority()` 返回 `StateAuthority`。它描述的是**已配置的部署方式**，不是数据库所支持的所有拓扑：

```ts
const authority: StateAuthority = {
  version: 1,
  mode: "provider",
  store_id: "account/dataset-stable-identity",
  storage_topology: "multi-host",
  execution_topology: "single-host",
  coordinator: "database-transaction-domain",
  cas: "store",
  durable_receipts: true,
};
```

声明 authority 的外部 provider 要求宿主支持 `state.documents.authority.v1`。宿主在 initialize 的 `supported_host_features` 中列出精确名称；公共 SDK 发现 `state.documents` descriptor 含有 `authority` 时，会在返回 initialize 成功之前自动检查该声明，并在响应中添加 `required_host_features`。缺少声明、仅支持其他版本或缺少所需名称都会拒绝初始化，避免旧版 v3 宿主悄悄将本地日志用于 provider 自有权威。未知的宿主能力名称在未被显式要求时忽略，不能暗示已支持其他名称。没有 `authority` 的插件保留原有兼容性。不使用 SDK、直接实现协议的作者必须自行执行相同的宿主声明检查；仅返回 required 元数据不够，因为旧宿主会忽略它。初始化仍只负责声明，协商成功前不能产生业务效果。

- **`host-journal`** 保留现有同机准入、待定写入恢复和已验证迁移语义；两种拓扑均须为 `single-host`。原生 SQLite/JSONL provider 显式声明此模式。为兼容已有 v1 provider，未声明 `authority` 方法仍表示这一已文档化的旧契约，不会选择另一个后端。已声明方法的 schema 不匹配、结果不完整、调用失败或语义不受支持时均拒绝继续，不回退到旧模式。
- **`provider`** 由所选后端承担持久权威。宿主仅记录身份／拓扑元数据，不保存待定事务的业务载荷，也不建立完整文档镜像。普通调用和重开不会导出全部数据，也不与本机记住的修订号比较。其他获准的数据写入者可以推进远端修订号，由全库 CAS 和持久的相同操作 ID 回执处理冲突。空的 Wuu home 可以从所选远端权威启动。其他选定 provider 仍可能在本地保存各自的配置、产物或凭据；本契约不会迁移这些内容。
- **`store_id`** 标识一个逻辑数据集，而不是插件、URL 或临时进程。准入前该身份必须已持久化，在捕获的 provider generation 内必须不变。数据集重置／替换后，provider 必须拒绝旧绑定的操作，不得悄悄转向新数据集。宿主跨重开固定该身份，遇到不同数据集则拒绝。字节相同的副本并不能证明它们是同一实时权威。
- **`storage_topology: "multi-host"`** 要求 provider 控制的统一事务协调器，由 `coordinator` 标识；它必须原子比较全库修订号，并将全部写入与操作回执一起持久化。进程内锁、最终一致性、单独写入回执或后写覆盖都不满足条件。这是存储事务域，**不是执行租约**。
- **`execution_topology`** 单独声明部署中的执行写入者安排。允许多读取者／数据客户端的云数据库可以服务一个执行主机。原生会话域允许这种部署，但会在准备阶段拒绝 `multi-host` 执行：它的 OS 租约不能约束不同机器上的进程。启用该拓扑前，替代会话域／协调器必须实现分布式执行 fencing。可信 provider 的错误拓扑声明不会因此变安全；远端会话路由也不提供这种协调器。

从 host-journal 干净切换到 provider 模式，要求停止全部旧写入者、没有尚未解决的待定事务，并完整导出候选后端，确认文档**和回执**均匹配旧日志检查点。旧提交响应丢失／结果未知时，应先重新打开原 provider 完成对账；选择远端 provider 不是猜测结果的许可。切换保留旧数据与日志作为恢复材料，仅发布 provider 身份固定记录，不会清除既有本地副本。此后仅修改配置不能让旧日志在远端权威背后重新成为写入者。

Provider 自有权威的迁移仍须走其显式管理流程：fence **全部**源写入者，转移文档、修订号、回执及逻辑身份，完整验证目标，再发布目标绑定。仅当该逻辑权威确实迁移时才保留 `store_id` 和协调器身份；独立新数据集必须使用新身份。宿主不会把导入确认或复制字节当作迁移成功。更换已固定的数据集、返回 host-journal 或改变部署拓扑都会被拒绝，不会自动重新绑定或删除固定记录；目前尚未提供宿主重新绑定管理 API。运行中的消费者不能通过 import 覆盖已准入的权威。

运行 `node scripts/state-authority-acceptance/run.mjs` 可验证使用公共 SDK 的独立进程与 HTTP 后端。可复查日志覆盖：移除原生文档 provider 后创建／重开原生会话、持久提交响应丢失后的精确重试、冲突、空 home 启动、停止写入后验证迁移、禁用／离线失败，以及宿主不产生业务数据日志。该 fixture 是本机回环测试后端，不是生产数据库或分布式执行实现。

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
| 产物 | `import`、`resolve`、`read`、`read-range`、`fork`、`delete-thread`。提供者 scope 来自获得准入的消费者。读取要求正数 `max_bytes`，返回 base64 字节与 MIME 类型。Resolve 返回该产物是否由提供者管理。普通工具导入使用带认证的网关。 |
| 数据索引 | `host.data.query@2` 提供 `call` 查询、`latest-context` 有界最新请求元数据读取，以及 `append-turn` 完整回合原子追加。查询接收 thread、可选事件类型、turn 和 limit；未知类型名也可作为过滤条件。省略或零 limit 不设置显式上限；负数返回 `invalid_params`。事件不授予配置或凭据访问。 |

原生配置文件缺失时读出 `source: null`，存在但零字节时读出 `source: ""`。解码前先判断 `null`，客户端不得将这两个值互相归一化。配置 CAS 的 `source: null` 表示提供零个替换字节：原生文件模式会写入空文件，而不是删除；可信项目模式拒绝空 JSON。CAS 响应即使在空文件已存在时也可能保留 `null`，因此该字段描述返回的源字节，不是文件是否存在。

配置和设置 revision 都是不透明字符串，与文档存储的数字 revision 不同。这两个服务不提供文档事务回执协议。只有确定的 CAS 冲突可以在重读后重试；不可用或已取消的写入需要先核对结果。不要记录凭据字节、原始配置或包含秘密的有效配置。

原生配置模式保留来源边界：显式文件模式使用指定文件，嵌入模式从传入配置开始，显式项目模式使用项目基础配置和设置层，仅在 `.wuu.json` 不存在时回退到 `wuu.json`。叠加在用户配置上的受限项目覆盖不会获得替换凭据或授权的权限。

模型认证及登录/刷新状态使用捕获 generation 的凭据服务。原生 auth 适配器可以在不覆盖目标已有条目的前提下导入旧账号数据，该兼容行为属于原生实现。外部凭据提供者不会被悄悄包上原生文件查询。进行中的登录 ID 保持关联原 generation，直至清理结束。

## 制品预览与下载

`ARTIFACT_STORE_DESCRIPTOR` 当前声明版本 `1.2.0`。
`artifactStore(host).readRange` 接收 `{ thread_id, uri, sha256, offset, max_bytes }`；
偏移按解码后的字节计算，`max_bytes` 范围为 1 至 1,048,576。结果包含 base64
`data`、`mime_type`、`offset`、快照总大小 `total` 和完整快照的 `sha256`。
返回长度必须为 `min(max_bytes, total - offset)`；越界、摘要变化或快照不可用时失败。
不支持 `read-range` 的提供者明确失败，宿主不会整份下载来模拟范围读取。

制品 ID 与提供者 URI 都是不透明元数据。桌面端不从中推断本地路径、工作区、
执行主机或权限。预览通过来源会话已认证的路由获取绑定到当前文档的临时读取凭据。
核心验证已发布的制品元数据，在每次操作期间保留所选提供者，并用捕获的代次 ID
防止切换到另一实现；每个范围请求重新检查会话权限。释放凭据、关闭来源文档或
提供者代次变化都会停止后续读取。预览不创建云端载荷的本地缓存。

浏览器适配器明确声明不同的 `bounded-memory` 来源能力：每个制品最多 8 MiB，
全部活动凭据合计最多 32 MiB。它在公开临时 Blob URL 前验证完整摘要，不持久化
字节，并在清理时撤销 URL。超大资源和不支持的 MIME 类型明确失败；这不等同于
原生范围流式传输。调用方可通过 `application.artifacts.capabilities.source` 查询限制。

原生桌面端仅在显式保存、外部打开或在文件管理器中显示操作时落地字节。外部应用收到私有、
不可执行的临时副本，交付前验证完整摘要。取消或失败会删除副本；成功交付的副本
由操作系统临时目录生命周期管理，避免退出 Wuu 时删除仍被其他应用打开的文档。
HTML 不论由哪个提供者保存，都保持沙盒限制。

## 会话产物清理

`ARTIFACT_STORE_DESCRIPTOR` 以 `1.2.0` 声明 `state.artifacts@1`；`1.1.0` 新增了 `delete-thread`：输入 `{ thread_id }`，结果 `{ thread_id, deleted: true }`。获得准入的消费者使用捕获的 host 调用 `artifactStore(host).deleteThread({ thread_id })`。成功表示所选提供者已移除该会话当前的载荷及清单；会话不存在或已经为空也成功。其他会话、fork 副本、索引数据和会话文档不属于此操作范围。

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

## 可组合的循环策略

`agent.policy@1` 为工具调度、继续和恢复选择一个 provider，默认模块为
`wuu.agent.policy.default`。默认循环、子 Agent、流式传输、非流式模型调用和
独立的 `host.model.invoke` 调用都使用捕获的 generation 绑定。移除选中的
provider 会停止这些调用；错误或空决策不会切换到另一实现。请求变换与上下文
贡献继续使用现有有序契约，`agent.compaction` 独立拥有上下文替换策略。

运行时插件声明 `agentPolicyServiceDescriptor()` 并实现 `evaluate`。
`composeAgentPolicies(strategies)` 按提供的顺序传入隔离的输入快照：返回
`undefined` 委托下一策略，返回决策则短路。链耗尽、抛错、非法动作或取消都会
终止操作。组合 provider 可以声明其他公开服务为依赖，而不导入其实现；宿主
不存在另一套策略选择注册表。

| 阶段 | 观察与决策 |
| --- | --- |
| `schedule` | 在回合产生副作用前执行一次。`serial` 禁止流式提前启动工具，包含嵌套调用在内一次只运行一个叶子工具；`parallel_safe` 要求 `max_parallel`（1–64），在回合批次及嵌套叶子调用间共享容量，并保留工具的并发安全声明。 |
| `continue` | 无工具的回复后执行。`stop` 结束回合；`continue` 在步骤及连续轮次预算内请求下一轮模型调用。 |
| `retry` | 失败的循环步骤可选择 `fail`、使用有界上下文溢出恢复的 `recover`，或对证明尚未提交的请求显式 `retry`。重试必须提供 `max_attempts`，首次值固定总尝试上限，后续决策不能续领预算。 |
| `transport_retry` | 接收已结束物理尝试的失败、提交次数、尝试预算及退避配置。`fail` 终止；`recover` 给出 `recovery_action`（`replay_same_payload`、`wait_then_replay`、`refresh_credential` 或 `switch_transport`）及可选 `delay_ms`。仍须满足适配器能力及持久化工作流预算。 |

循环重试不能重放部分输出、已接纳的工具副作用、提交结果不明的操作，也不能重置
耗尽的推理预算。对于 `transport_retry`，当消费者能够丢弃失败尝试且已安装重放安全检查时，
宿主可设置 `partial_output_resettable`。恢复决策仍须通过安全检查，以保护已完成的检查点及
已接纳或结果不明的工具副作用；没有此能力时，部分输出会阻止传输恢复。传输恢复保留物理
提交回执，包括结果不明但可能计费的提交；选中策略可以在预算内重新尝试模型调用。等待可取消，每次决策最多等待 60 秒。失败尝试的用量和持久回执
会保留；刷新凭证、切换传输和重试仍需既有的预写回执及适配器支持。

完整循环替换通过 `driverTurn(host, execution).evaluatePolicy()` 使用策略，
仍自行控制模型调用、工具、检查点和完成操作。`settings().has_policy` 表示
runner 是否绑定策略客户端。直接构造未组合 Go runner 的嵌入式测试不能证明
产品路径中的 provider 选择。

`examples/plugins/composable-policy.ts` 是单文件示例。将其复制到仓库外，运行
`wuu plugin dev ./composable-policy.ts`，并在目标组合中选择它的
`agent.policy@1` 绑定。`node scripts/loop-policy-e2e.mjs` 会安装打包后的公开
SDK，验证两个并行策略实现、子 Agent 继承及移除，证据保存在
`.tmp/e2e/loop-policy/`。其中受控模型验证路由和控制流，不代表模型质量提升或
真实自我改进实验。

### 子 Agent 执行状态

原生子 Agent 领域通过所选 `state.documents@1` 提供方保存线程元数据、结果投递认领、
任务与运行索引、队列取消标记、报告以及工作 Agent 的终态和待提交快照。父执行捕获该
绑定；后续执行、恢复和应用读取不会在提供方失败后重新打开原生文件存储。即使另一
原生提供方仍处于活动状态，禁用所选提供方也会让这些操作不可用。

每次领域修改比较提供方 revision，并原子提交相关索引和事件。只有明确的 revision
冲突才会重试。不确定响应会在错误中保留原始事务；它不授权重复工具副作用或重新构造
写入。待提交的 driver 状态与最后一次终态结果分开保存，因此恢复检查点不会发布未提交
的结果投递。

首次使用该格式前，停止仍可能写入旧子 Agent 文件的旧版 Wuu 进程。迁移读取发现的所有
历史工作区根目录，且仅当所选提供方确认领域不存在时才导入；提供方错误不会触发导入，
后续读取也不会使用过期旧文件。原始文件保留至通常的会话删除，以便恢复。会话删除留下
空的权威状态，重试清理不会重新导入旧子 Agent 数据。更换文档提供方仍须执行上文所述
的完整停写迁移，包括这些命名空间和事务回执。

报告 Markdown 是状态提交后生成的派生产物。生成失败会明确报告状态已提交、产物尚未
完成，而不是声称回滚。生成过程在文件锁内重读当前报告，避免较早写入方延迟执行后
覆盖较新的已提交报告。

内嵌浏览器的标签注册表同样使用选中的文档提供者。标签 URL、保留状态与重启恢复记录
会从全部已知历史产物目录一次性迁移；删除会话会写入权威空镜像，防止旧文件复活。
原生与外部引擎会话的工具包均走这条路径。状态提供者不可用时，浏览器宿主动作会被阻止；
宿主动作执行后的持久化失败会明确报告动作可能已完成，不会静默重放或假报成功。

## 引擎会话

`engine.<id>@1` 是执行与管理边界。随产品提供的 `wuu.engines.native` 模块提供原生
Wuu 循环及外部 CLI 适配器；`wuu.engine-execution` 消费所选服务。生产引擎注册表只是
组合图的清单投影，不是第二套选择机制。替换插件可以提供 `engine.wuu`，无需继承原生
实现或身份特权。所选提供方缺失或禁用时，不会生成替代实现。

版本 1 的 `descriptor` 响应可声明 `requires_host_model: boolean`。原生 Wuu 循环
声明 `true`；自行管理模型访问和凭据的引擎声明 `false`，包括不需要模型的
`engine.wuu` 替换实现。`engine/list` 保留这项客户端就绪状态与模型来源元数据：
`true` 让默认输入框选择宿主已配置的模型并检查凭据就绪状态，`false` 则使用引擎自己的
模型选择。它不是凭据验证、后端授权或新增的服务端会话创建前提。提供方的执行和就绪
检查独立于此声明，不能借它绕过模型认证或权限。旧描述符省略该字段时，
客户端保留旧规则：`wuu` 需要宿主模型配置，其他引擎自行认证。显式 `false` 不会
被当作省略。

绑定一起传递模型、推理强度、速度、模型选项、指令、权限模式、MCP 端点和恢复引用。
回调网关绑定具体提供方及捕获的代际，仅持有不透明令牌不能获得权限。公开的
`host.engine.session` 操作承载流事件、审批、用户输入及持久化原生引用；`run_loop`
委托给所选的完整循环驱动，而非固定内置算法。宿主管理的继续执行、规范历史及转向
行为由能力声明决定，不按引擎名称分支。

关闭绑定会话会先取消并排空活动执行，再释放提供方代际。关闭调用者取消只停止等待，
不会中断清理；再次关闭等待同一次清理完成。原生 warm scope 保留跨回合复用的 CLI
进程，并随所属服务器关闭而释放。已确认的终态回执使用独立、有界的 `settle_turn` /
`settle_reference` 清理操作；这些操作不能登记新的待执行回合、启动工具或恢复生成。
不确定的中断不会被静默重试。

同一所选引擎服务还提供 `describe`、`models`、`auth`、`quota` 和 `history_fork`；
不支持的操作明确失败。模型发现按上下文保留过期或部分结果，刷新需显式请求。配额查询
需主动选择；远程调用不能启动 `local-cli` 认证。历史分叉要求原生边界证据，不会通过
重放历史副作用来伪造不支持的分叉。禁用的原生引擎仍可在设置中查看元数据，但不能启动认证、配额/
模型发现或历史操作。运行 `node scripts/engine-provider-e2e.mjs` 验证独立安装的公开
SDK 替换，并用假 CLI 生命周期测试验证进程和持久化取消行为；两者都不需要付费模型
或用户的 Codex 任务会话。

### 模型传输容量

`model.capacity@1` 选择准入与冷却策略，默认模块为 `wuu.model.capacity.default`。
`wuu.model.capacity.resources` 提供的 `host.model.capacity@1` 是单独保留的物理资源上限。
嵌入方通过显式共享 `runtime.CapacityResources`，让多个会话使用同一资源所有者；派生组合
和代际重载通过原生工厂继承它，隔离策略不会凭空增加物理槽位。独立所有者表示有意隔离资源。
此保证限于进程内，不声称跨进程统一账户配额。

原生、SDK 回执与 direct 适配器在物理提交回执边界统一经过两个契约；delegate 适配器不为子
调用重复占位。所有适配器共享宿主已知的配置提供者路由上限。原生传输还使用凭证与端点派生的
不透明账户域，使原生提供者别名共享容量；任意外部账户别名必须显式声明共同的
`capacity_scope`，不得直接放入凭证。多个不同范围按稳定顺序获取，排队取消会释放全部已获租约。

隐式资源所有者在整个 Session/OpenState 生命周期保留根租约，包括容量模块被禁用的期间。
已准备候选与保留代际另持租约，替换和修复不会提前销毁所有者。显式所有者由调用方负责：先关闭
所属会话与状态，再调用 `CapacityResources.Close(ctx)`。关闭会停止准入、唤醒排队请求并停止
策略计时器；等待超时不会提前释放仍在执行的物理工作，保留引用继续负责排空。

物理容量与回调权限的生命周期不同。禁用或逻辑取消不会释放未结算的物理槽位；权威终态结算会
立即释放槽位，因此同一回调可以继续提交下一次物理请求。选中的服务图与回执权限仍保留到原始
物理 RPC 完成或进程终止得到确认。未确认的终止不会被报告为成功。退役等待超时会报告清理仍在
进行，而非假报已终止；保留作用域继续授权清理结算。

运行 `node scripts/capacity-provider-e2e.mjs`，验证独立安装的公开 SDK 策略以及外部回执取消、
延迟结算、进程尾部终止与共享单槽位所有者的组合行为，无需付费模型调用。

检查点续跑使用独立的 `agent.policy` 阶段 `checkpoint_resume`，只接受
`resume` 或 `fail`，不接受 `retry`。`completed_messages` 统计已经持久化且
协议完整的条目，包括仅有推理内容的进展。续跑从保存的历史发起新采样，
不会重放中断前的请求。所选策略必须明确授权；宿主仍拒绝缺少检查点、
存在工具副作用或回执／工作流预算耗尽的续跑。不认识该阶段的策略会明确失败。

## 独立的目标与自动化呈现

| 包 | 拥有的行为 | 公开依赖或服务 |
| --- | --- | --- |
| `goal` | 持久状态、持续执行、目标模型工具与 `/goal` 提示 | 提供 `goal.control@1` |
| `goal-ui` | 输入区目标控制与显示 | 消费 `goal.control@1` |
| `automation` | 持久计划、运行记录与后台调度 | 提供 `automation.scheduler@1` |
| `automation-client` | 模型的 `cron` 工具 | 消费 `automation.scheduler@1` |
| `automation-ui` | 自动化目录与侧栏 | 消费 `automation.scheduler@1` |

移除 UI 包不会移除运行时的数据、工具或正在进行的工作。替换 UI 只需安装一个
声明相同服务依赖的独立桌面包，不需要导入旧 UI 或运行时源码。也可以选择兼容
的运行时而不复制呈现代码。UI 依赖为可选：运行时禁用或不可用时显示明确的
服务错误并禁用修改，而不是导致应用崩溃或暗中换用内置调度器；后续成功读取
会恢复控件。cron 工具要求调度服务可用，绝不在本地偷偷调度。

SDK 导出 `GOAL_CONTROL_SERVICE_DESCRIPTOR`、`GoalControlRequestV1`、
`GoalControlResultV1` 和 `GoalSnapshotV1`。在 `desktop.required_services`
声明 `goal.control@1` 后调用：

```ts
const result = await api.invokeService<GoalControlResultV1>({
  service: 'goal.control', method: 'request',
  params: { method: 'get_goal', input: { thread_id: threadId } },
});
```

请求方法包括 `get_goal`、`create_goal`（还需要 `objective`）、`pause`、`resume`
与 `clear`，都需要 `thread_id`。返回值包含 `goal`，不存在或已清除时为 `null`。
创建或继续可启动普通会话回合。完成和受阻仍由模型工具提交，不是 UI 控制方法。
调用使用经认证的消费者服务图和 execution，UI 不能指定实现提供者。目标存储
仍归原来的 `goal` 身份所有，拆分呈现不会移动或复制状态。旧的
`plugin.client.request` 端点仍适配到相同控制逻辑，供旧组合包客户端使用，直到
该 v1 capability 退役。

显式 composition 可以独立选择 `goal-ui`、`automation-ui`
及对应运行时。第三方现有组合 manifest 仍受支持。composition 迁移保留拆分前
的内置桌面选择；新的显式 composition 不会根据运行时选择推断并加入 UI。
headless composition 不需要任何一个 UI 包。
