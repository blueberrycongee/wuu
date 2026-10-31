# 桌面 UI 插件

桌面插件是加载到 Wuu renderer 中的受信任 JavaScript 模块，通过宿主的 React 实例和 UI Kit，在定义好的边界上添加控件、页面和展示方式。可先用[快速上手](desktop-plugin-quickstart.md)创建可运行的包。

## 选择扩展点

| 目标 | API |
| --- | --- |
| 在声明的位置添加小控件 | `registerUIContribution` |
| 改变某个产品概念的展示 | `registerPresenter` |
| 包装或替换已声明的消息、时间线区域 | `registerUIContribution` |
| 添加页面、编辑器或仪表盘 | `registerViewType`，配合 placement 或 manifest 入口 |
| 添加紧凑的检查器区块 | `registerUIContribution`，目标为 `wuu.default-shell/inspector.sections` |
| 渲染特定内容类型 | `registerRenderer` |
| 在会话底部显示临时交互 | `showConversationCard` |
| 发布简短的输入框状态 | `registerComposerStatusSource` |
| 提供草稿行内后缀 | `registerComposerInlineCompletionProvider` |
| 改变外观 | 声明式主题、主题 token 或样式 |

选择能满足需求的最小边界。工具栏按钮不需要替换整个输入框，长仪表盘也不应放进工具栏。视图位置、导航、窗口生命周期和恢复控件由宿主管理。

## Slot 和 Surface

Slot 所有者声明并渲染区域；贡献指向明确的所有者。多个内容注册项按 `order` 组合。

主区域的插件页面激活时，其侧栏会在导航入口下方渲染该插件自己的 `sidebar.primary` 贡献。其 `context` 包含 `ownerPluginId`、`viewTypeId` 和 `viewContext`，后者与页面收到的是同一个不可变 context 对象。可据此展示当前页面的摘要，并隔离不同页面实例的状态。会话侧栏不提供这些字段。

| Slot | 位置 |
| --- | --- |
| `wuu.default-navigation/sidebar.primary`、`wuu.default-navigation/sidebar.footer` | 侧栏内容和底部 |
| `wuu.default-shell/workspace.header`、`wuu.default-shell/conversation.header` | 对应的标题区域 |
| `wuu.default-conversation/conversation.turn.after`、`wuu.default-preview/conversation.turn.after` | 每个已展示回合之后；展示已报告的耗时和用量 |
| `wuu.default-conversation/conversation.tool-activity.after`、`wuu.default-preview/conversation.tool-activity.after` | 已展开的过程详情中，单项工具活动之后 |
| `wuu.default-conversation/conversation.message.before`、`wuu.default-conversation/conversation.message.after` | 每条消息前后 |
| `wuu.default-composer/composer.above`、`wuu.default-composer/composer.toolbar` | 输入框上方或工具栏中 |
| `wuu.default-preview/workspace.rail` | 展开的右侧竖条中的紧凑控件和读数；宽度固定为一个工具栏控件 |

通过带所有者前缀的 Slot 目标组合区域。默认会话 provider 声明 `wuu.default-conversation/conversation.turn` 和 `wuu.default-conversation/conversation.message`；预览 provider 在 `wuu.default-preview` 下声明自己的消息 Slot。轮次边界包含该轮的消息与过程组，不是整个滚动容器。

```js
api.registerUIContribution({
  id: "timeline-frame",
  target: "wuu.default-conversation/conversation.turn",
  mode: "wrap",
  render: ({ children }) => api.react.createElement("section", null, children),
});
```

省略 `mode`（或使用 `content`）可提供内容，替换调用方的 fallback。单内容 Slot 在准备阶段拒绝冲突注册，并保留当前 generation。包装按 `order`、插件 ID、贡献 ID 升序组合，后面的包装位于外层。包装渲染失败会报告诊断并保留内层内容。卸载 Slot 所有者会移除该区域；卸载贡献者只移除其贡献。provider 清理会等待已挂载视图释放 effect。由服务图选择的区域继续使用精确的 provider 绑定，并拒绝包装。本地 SDK 预览采用相同的内容／包装顺序与冲突规则；包装渲染失败会记录到浏览器控制台。

增加指标、注释或上下文操作时，优先使用 `conversation.turn.after` 和 `conversation.tool-activity.after`，不要为了追加内容而包裹原有界面。两个槽位都支持按顺序组合多个插件，在气泡和 IM 样式下使用同一契约。回合追加内容位于原有回合的测量边界内。完整替换 `conversation.turn` 的实现自行拥有其内容，需要自行渲染所需槽位。新增、移除或渲染失败的追加贡献不会重新挂载原有回合和工具详情，保留展开状态与焦点。替换或包裹内容会改变 React 层级，不提供这种状态保留保证。

回合槽位提供 `ConversationTurnSlotContextV1`：可选的 `threadId`、`turnId`，以及冻结的 `snapshot`，包含状态、已报告的时间戳、耗时、Token 用量和用量模型。未知指标仍然缺省，不能当作零。快照仅用于展示，不代表执行操作的权限。工具槽位共用 `ConversationToolActivitySlotContextV1`，包括可选的 `threadId`/`turnId`、`itemId` 和已有的只读 `ToolActivitySnapshot`。预览回合没有持久会话标识时，不得借用当前选中的会话。工具贡献在展开的工具详情中显示，不在紧凑的过程摘要中显示；单个命令或 MCP 工具详情与工具组使用相同槽位。槽位拥有者必须已启用且实际渲染槽位；贡献不会恢复已禁用的提供方。

[Conversation metrics](../../../examples/plugins/conversation-metrics/desktop.js) 是可独立分发的插件示例：显示回合用量、增加本地工具结果查看操作，不需要私有导入或替换应用。它使用 `activatePresentation`，业务服务重新绑定时不会重建展示注册。

这条路径取代了已删除的 `registerSlot`、`registerSurface`、`contributes.slots` 和 `contributes.surfaces`。将 `render(context)` 改为 `render({ context, locale, translate })`，指定完整目标名称，并移除旧 manifest 字段。`registerUIContribution` 不需要重复的 manifest 声明。`contributes.presenters` 声明仍要求 ID、目标、模式、优先级与激活期间的注册一致。

检查器使用同一个公开 Slot 契约。`wuu.default-shell/inspector.sections` 接受多个贡献，通过 `context.snapshot` 传入 `InspectorSnapshotV1`。组件自行渲染区块和标题，没有内容时返回 `null`，通过 `order` 排序。Application 动作绑定到贡献者的 generation。原 `registerInspectorSection`、`when` 和检查器专用 host API 已删除；将显示条件移到组件内，动作改用公开 application 或捕获的插件 API。内置 TODO 模块提供了完整示例。默认信息面板目前由功能开关关闭，仅声明 Slot 不会让它显示。替代 shell 需要声明并渲染自己的 Slot，贡献明确指向该所有者。

贡献除 `context`、`application`、`slots` 外，还接收 `locale` 和 `translate(key, values?)`。使用 `registerLocale` 注册翻译。查找顺序为完整 locale、语言、`en-US`、`en`；缺失时返回 key。打包后的 SDK 预览采用相同契约。

Slot 所有者可通过 `<Slot layout={Layout} ... />` 传入 React 组件，接收排序后的 `items`（`key`、`pluginId`、`id`、`title`、`node`）及 `context`；使用各项的 `key` 渲染选中的 `node`。贡献可选的 `title` 默认使用 ID。布局可以分组或过滤内容，无需另一套注册表。包装位于布局结果外层，空 Slot 使用 `fallback`。服务图选择的 Slot 直接渲染选中的 provider，忽略自定义布局。

默认输入框用该布局将前两个贡献放入工具栏，其余放入带标题的溢出菜单。行内使用紧凑控件，菜单内容应允许换行。输入框 context 包含 `threadId`、`variant`、`mainConversation`、`running`、`readOnly`、`hasDraft` 和 `attachmentCount`。Goal、Monitor 已改用 `registerUIContribution`，应移除旧 `contributes.slots` 声明。`application.composerPresentation.getContributions` 及其展示适配类型已删除；自定义输入框应渲染自身声明的 Slot。

`workspace.rail` Slot 由默认预览 provider 提供，在右侧工具栏展开时显示。贡献按垂直方向排列，共享一个工具栏控件的宽度。使用紧凑的读数或控件，并通过无障碍标签提供完整含义。贡献可通过 `application` 观察活动标签，再通过 `application.composerPresentation` 读取对应的会话、轮次和运行状态。

[Token Speed](../../../examples/plugins/token-speed/README.md) 是使用该 Slot 的独立可安装示范插件。它以两秒滑动窗口估算流式文字、推理和工具调用参数的可观察输出速度；不计入隐藏输出，也不等同于模型实际分词。读数使用固定宽度，单位缩写单独占一行，数值以 320 毫秒动画过渡并遵循减少动态效果设置。尚无输出或轮次结束时显示横线，活动输出流暂停后回落到零。无障碍标签和悬停说明会解释估算含义。

在包含此 Slot 的仓库版本中运行 `wuu plugin install ./examples/plugins/token-speed` 安装示例，或运行 `wuu plugin dev ./examples/plugins/token-speed` 启用热更新。禁用插件会移除读数。

## Presenter 和动作

Presenter 接收 `contractVersion`、目标、可选匹配键、公开 `snapshot`、当前边界的 `host`，以及三种组合方式：

- `next()` 继续执行下一个 Presenter；`next({ snapshot })` 只为下游 Presenter 提供替换快照。
- `fallback` 等价于使用当前快照的 `next()`，保留现有包装的行为。
- `original` 是宿主原生渲染，跳过剩余 Presenter。改变快照不会改变这份原生渲染。

包装始终位于替换之外，优先级越高的包装越靠外。用户首选的替换先执行；替换可自行渲染整个边界，也可显式通过 `next()` / `fallback` 让下游处理。下游 Presenter 只有在输出被纳入时才会挂载。不要修改共享快照；需要调整时向 `next` 传入新值，并保持目标的快照契约。

贡献渲染失败时，使用其未经修改的传入快照回退到下一个 Presenter。插件的其余部分保持加载；重新激活后，失败的贡献可再次运行。

尚未迁移的 Presenter 目标为 `header.conversation`、`header.workspace`、`app.status`、`content.preview` 和 `settings`。TypeScript 类型允许更多字符串，不代表宿主会渲染任意新目标。

通过 `host.actions` 查看本次渲染公布的动作，再用 `host.invoke(action, input)` 调用。宿主检查 generation 是否仍然有效，并验证动作输入和当前状态。动作即使已公布，也可能因输入框只读或禁止发送而拒绝执行。

输入框和导航定制使用公开 UI contribution。在 `wuu.default-composer/composer.above` 或 `composer.toolbar` 添加控件，在 `wuu.default-navigation/sidebar.primary` 或 `sidebar.footer` 添加导航内容。通过 `application.getSnapshot()` 读取当前状态，并使用 `application.subscribe` 订阅；修改草稿调用 `application.invoke("composer.set-draft", { text, ownerTabId })`。完整替换界面时选择 `ui.composer` 或 `ui.navigation` provider。已移除的 `conversation.composer` 和 `navigation.primary` Presenter 目标会在注册时报错；同时删除对应的 manifest presenter 声明。

消息和过程组使用 `wuu.default-conversation` 或 `wuu.default-preview` 下的完整 `conversation.message`、`conversation.process` slot。实际渲染组件直接提供不可变的 `context.snapshot`（`ConversationItemSnapshotV1` 或 `ConversationProcessSnapshotV1`），同时提供 `threadId`、`turnId` 及 `itemId` 或 `itemIds`。消息快照包含流式正文，过程快照反映该组真实的 `active` 和 `streaming` 状态；消息动作也接收同样的语义快照，不再按 ID 查找另一份会话数据。设置 `mode: "wrap"`，不处理的消息类型返回 `children`。编辑消息使用 `application.invoke("history.begin-edit", { threadId, turnId, itemId })`，由应用验证当前状态和 generation 寿命。将 `conversation.item` 和 `conversation.process` Presenter 注册迁移到这些 slot，并删除对应 manifest 声明。过程快照尚不描述压缩通知，因此包含压缩通知的组保留原生渲染。

工具活动使用 `registerUIContribution`，设置 `mode: "wrap"`，完整目标为 `wuu.default-conversation/conversation.tool-activity` 和 `wuu.default-preview/conversation.tool-activity`。叶组件提供 `context.threadId`、`turnId`、`itemId` 及不可变的 `context.snapshot: ToolActivitySnapshot`，其中包含尚未完成的参数和结构化结果。用 `snapshot.capability ?? snapshot.toolName` 选择需要处理的调用，其余调用返回 `children`。多个插件可按贡献顺序包装同一工具活动；包装渲染失败时保留其 children，禁用 generation 后移除包装。旧 `registerToolActivityPresenter` API 和 `conversation.tool-activity` Presenter 目标已删除，应迁移注册并删除对应的 manifest Presenter 声明。参见 [Tool Card Skin](../../../examples/plugins/tool-card-skin/desktop.js)。

富结果正文由宿主单独放置：`inline` 输出位于回答之前，`turn_end` 输出位于回答之后。应使用 `tool-result` renderer 定制这些正文，而不是在工具活动贡献中重复渲染 `structuredResult.content`。

## 交互内容渲染器

内容渲染器也接收 `host.actions` 和 `host.invoke`。已完成的会话资源公布 `conversation.composer.submit` 时，接受 `{ text, context? }`：文本最多 8,000 字符，可选 JSON 上下文最多 16,384 字符。必须在真实按钮点击或该点击引发的原生表单提交中同步调用，键盘激活按钮同样有效。表单提交必须匹配该次点击的表单与提交按钮；单独调用 `requestSubmit()` 不构成动作授权。宿主一次性消费交互凭据，绑定来源会话和产物，再通过普通轮次准入与权限流程提交。会话运行中、来源被替换或卸载、插件被禁用时会拒绝动作，并保留输入框草稿和附件。`metadata.interaction` 提供 `running`、`readOnly` 和可选 `disabledReason`；调用时仍会重新检查。此输入契约仅用于内容渲染器；输入框 Presenter 提交现有草稿，仍不接受动作输入。

需要预览工具参数流时，`tool-result` 注册可声明 `toolInput: { capability }`，并使用字面 MIME `match`。只有当前实际负责该 MIME 的渲染器会收到预览。`content.preview` 标识临时输出；`metadata.toolInput` 提供累计且可能合并的 `argumentsText`、`turnId`、`status` 和可选 `error`。只解析完整且有界的数据单元，保留最近有效预览，不要执行动作或持久化尚未完成的状态。首个匹配的最终资源会沿用预览的 part 标识。

通过普通插件存储保存交互状态，并以宿主提供的会话和产物标识隔离实例。保存的数据只属于当前 Wuu 存储，不提供跨设备同步。恢复状态不得触发提交，也不得覆盖读取尚未完成时用户已经做出的修改。

## View、卡片和状态

View 是注册的组件，可以放在 `navigation`、`primary`、`auxiliary`、`inspector`、`settings` 或 `overlay`。`registerViewPlacement` 请求初始位置；manifest 的 `contributes.navigation`、`workspaceTools`、`settingsPages` 为用户提供宿主管理的打开入口，每个入口都必须引用该插件注册的视图。

`persistence=durable` 用于恢复视图布局状态，不会持久化任意组件状态。卸载后仍需保留的数据应放入命名空间存储。View props 包含访问存储、设置、命令和视图导航的 host API。

网站页面可以在同一个已注册 View 内，将 `api.ui.WebPage({ url, title? })` 作为组件渲染。此组件仅支持 Wuu 桌面主工作台窗口，不支持弹出的会话窗口或远程 Web／移动客户端。它复用桌面 Chromium 页面和共享浏览器登录会话，包括通过现有浏览器设置导入的 Cookie；不会自动读取其他浏览器的 Cookie 库。初始 `url` 必须使用 HTTP(S)，修改后会导航到新地址。宿主提供导航控件，并将原生页面限制在可见区域内。切换到其他工作台视图只会隐藏网页，同一个页面及其内部状态会保留。显式关闭 View、卸载 WebPage 组件、禁用扩展或重新加载其 generation 时，会关闭页面及其弹窗，Cookie 保留在 Wuu 浏览器会话中。恢复 durable View 时打开配置的网址，不恢复之前的网页内部状态。不需要网站专用插件注册表或 manifest。参见[网站页面配方](plugin-recipes.md#添加网站页面)。

会话和插件主视图使用侧栏导航，包括通过 API 打开、没有声明导航入口的视图。它们的标题栏不提供 `tabs`、`activeTabId` 或 tab 操作。插件主视图提供 `canNavigateBack` 和 `header.navigate-back`。只有关闭会把页面从侧栏移除时，宿主才另外提供关闭控件，即通过 API 打开的视图和额外实例；已声明导航入口的页面通过返回或侧栏离开。紧凑工作区标题栏同样提供返回，并为当前标签提供关闭控件。这些仍是 contract version 1 的可选字段和动作，应检查快照和 `host.actions`，不要从已保存视图重建顶部标签栏。辅助面板和插件内容内部仍可使用各自的标签页。

当提供者不支持关闭某个工作区标题栏标签时，该标签可报告 `closable: false`；旧版宿主不提供此字段。支持关闭展开工作区中会话的应用提供者会发布 `previewWorkspace.conversationOpen`，并实现 `preview.open-conversation` 和 `preview.close-conversation`；旧版 V1 提供者省略该字段，会话入口保持打开。调用这些动作前应检查该字段。

会话卡片用于临时交互，不是保存的历史或持久页面；卡片句柄可以更新状态或关闭卡片。输入框状态源通过 `getSnapshot(context)` 返回结构化条目，通过 `subscribe` 通知变化，数据未变时应保持快照引用稳定。状态行、溢出和可选的 `open-session` 动作由 Wuu 渲染处理，不接收每个条目的任意 React 内容。


### 独立会话

主视图可使用 `layout: "user-space"` 隐藏 Coding 工作区面板，同时保留 Wuu 原有导航。此展示提示不授予运行时权限。激活已打开视图时，宿主发布 `workbench.view.activated` 事件，包含 `pluginId` 和 `viewTypeId`；处理器应同时匹配两个标识。

桌面宿主通过 `api.ui.ConversationMessage` 和 `api.ui.ConversationComposer` 复用 Wuu 原生消息渲染器和输入框，包括原有附件选择、粘贴/拖放、预览与发送/停止控件。调用方提供会话标识、受控草稿、附件与传输回调，并负责请求接纳、错误处理及成功接纳后的草稿清理。发送失败应保留原始文本和附件。未提供原生组件的宿主会明确报告输入框不可用，不会悄悄替换成另一套输入框。

`api.ui.ConversationView` 提供相同的阅读列、原生标题栏和搜索对话框、输入框停靠、滚动跟随及按会话恢复阅读位置。调用方提供标题、可选头像/状态、搜索内容、消息和输入框。`ConversationMessage` 接受稳定的 `id`、可选时间、最新回复标记和附件内容；独立会话提供复制/时间，不显示不受支持的 Coding 动作。`api.ui.ConversationAttachment` 使用原生结果卡，加载和已核验的本地 Blob URL 由调用方负责。这些呈现组件不会加载 Coding 历史、选择工作区或读取附件路径，调用方仍须保留范围限定的传输及授权检查。

`ConversationView` 可接收 `sessionId` 和 `sessionScope`，让问题列表、回答、取消和延长处理时间都走同一条经过验证的用户空间/产品域路由。非工具 offer 的 `onQuestionReply(requestId, prompt)` 必须使用稳定请求标识，先把回答接纳进该会话，再解决 offer，同时保留输入框中无关的草稿和附件。hold 仍保留问题待回答，不等于回答。

流式消息应使用稳定条目标识及终态 turn 快照的最终序号与持久历史对账，不按文本去重。停止回复及其重试必须保留选定的 turn ID，避免迟到停止误取消下一条回复。

独立会话不会把草稿、消息或工作区选区交给 Coding 范围的 Presenter 或补全 Provider。不应解析 Coding 工作区路径的消息应使用 `linkPolicy: "external-only"`。这层 UI 边界不能替代运行时范围检查。Personal 默认提供记忆域逻辑隔离，经过验证的操作系统/宿主隔离是独立可选的嵌入模式。

## 输入框行内补全

在 `activate(api)` 中调用 `registerComposerInlineCompletionProvider({ id, order, provideCompletion })` 注册 provider。回调接收只读的 `ComposerInlineCompletionContext` 和 `AbortSignal`，同步或通过 promise 返回 `{ text: "字面文本后缀" }` 或 `null`。没有建议时返回 `null`。宿主将已接受的文本追加到草稿；回调不替换整份草稿，也不接管 textarea。

版本 1 的 context 包含 `kind`、`composerId`、`revision`、可选的 `threadId` 和 `sideThreadId`、`draftText`、`selectionStart`、`selectionEnd` 和 `running`。主输入框和分栏输入框的 `kind` 为 `main`，侧输入框为 `side`，尚无 `sideThreadId` 的新侧草稿也明确标为 `side`。选区偏移使用 JavaScript UTF-16 索引。`composerId` 是不透明标识，作用域为已挂载输入框及其会话/工作区上下文；revision 不是全局会话版本。侧会话输入框的 `threadId` 表示所属主会话，`sideThreadId` 表示已创建的侧会话。

`running` 表示输入框的展示状态。最终回答就绪后，主输入框和分栏输入框可能已显示空闲，而运行时仍在收尾。值为 false 不代表该轮已成功结束。仅在成功完成后提供建议的策略应观察回合生命周期事件，并明确激活或重载后的行为。

需要最近的可见对话时，调用 `api.readVisibleHistory({ kind, threadId, sideThreadId, workspaceRoot, workspaceId, beforeTurnId })`。主会话以 `beforeTurnId` 为排他边界，最多返回此前 20 个完整可见轮次；侧会话按自身的 `sideThreadId` 读取，最多返回 20 次完整对话。结果只有显示的用户文字、最终回复和图片／文件数量，不含媒体字节、名称、路径、工具输出或推理。预测前仍需核对实时成功完成的源轮次；恢复历史不能触发请求。另一个 `host.session.history.read` 运行时服务返回可能含已撤回或隐藏消息的物理审计记录，不应当作可见对话文本。

Provider 按 `order` 升序调用，再依次按插件 ID、注册 ID 排序，逐个尝试。第一个非空结果生效；无结果或 provider 出错时继续尝试下一项。宿主渲染建议文本，并在接受前核对请求是否仍然有效。上下文获取、提示词、防抖和请求预算由插件负责。该 API 不会安装补全 provider 或自动调用模型的策略。

宿主在符合条件的输入状态变化时调用 provider。注册返回带有 `dispose()` 和 `refresh(scope?)` 的句柄。异步缓存或设置更新后，可使用原请求的标识调用 `handle.refresh({ composerId, revision })`；省略作用域时重新检查所有当前符合条件的输入框。宿主取消原请求并重新按顺序调用 provider。刷新不改变输入 revision、不接受文本、不覆盖 Escape 关闭状态，也不会把旧作用域应用到新草稿。已释放或已替换 generation 的句柄不再生效。同一注册项的连续调用会合并，每 100 毫秒最多分发一批刷新；模型请求预算和防抖仍由插件负责，不应在 `provideCompletion` 中无条件触发刷新。

独立工作尚未完成时应尽快返回 `null`，缓存就绪后再刷新。本地片段插件可在工作区索引完成后刷新，翻译插件可在术语表变化后刷新。它们与后台下一条消息预测共用同一边界，不要求调用模型，也不安装任何补全策略。尚未返回的 provider 会阻塞后续 provider，宿主不自动设置超时。

### 建议交互

结果可附带由 provider 管理的不透明 `id`：`{ id: "snippet-42", text: "字面文本后缀" }`。注册时提供 `onDidInteract(event)`，只接收本注册项建议的交互。每次渲染展示都有宿主生成的 `interactionId`；后续回调保留该 ID、可选的 `suggestionId`，以及来源的 `kind`、`composerId`、`revision` 和可选会话 ID。回调不包含后续草稿文本、消息正文或附件。

- `shown`：符合条件的灰字预览已提交到输入框 DOM，不代表用户实际看到，也不保证整个后缀都在可视区域内。
- `accepted`：Tab 已将完整后缀插入可编辑草稿，不代表发送。
- `dismissed`：Escape 明确关闭了可见建议。尚无建议时取消请求、失去焦点或切换上下文均不计为拒绝。
- `dispatched`：输入框调用提交回调，且回调没有抛错或返回 `false`。`mode` 表示请求的 `send`、`queue` 或 `steer` 动作；`edited` 比较交付文本与接受该建议后立即得到的草稿。

`dispatched` 只是 UI 发起提交的边界，不是后端确认、持久化消息 ID、最终队列路由或送达保证。异步提交仍可能失败并恢复草稿。同步拒绝会保留归因供重试，成功发起提交后归因只消费一次。清空或程序替换草稿、切换会话、卸载输入框以及 provider 退役均丢弃归因。普通编辑保留归因，包括删去所有插入文本；`edited` 不保证最终还包含建议中的字符。同一草稿接受多个后缀时，各自保留独立标识和比较基线。目前只支持完整后缀接受。

片段和翻译插件可据此区分插入、后续提交发起和编辑，无需监听其他 provider 的交互或无关消息正文。宿主不保存这些事件，也不发送统计数据。释放或重载后停止回调；回调错误写入诊断，不阻塞输入。事件处理、保存、排序和网络使用策略由插件负责。

原生输入框只在已聚焦、可编辑且光标折叠在草稿末尾时请求建议，空草稿也可使用。输入法组合输入、斜杠命令草稿、打开的菜单或对话框，以及隐藏的折叠提示内容不参与补全。斜杠命令保留 Tab 优先级。无修饰键且非重复触发的 Tab 接受当前建议；没有建议时保留正常焦点导航。Escape 关闭当前建议或取消待处理请求。接受只修改草稿，不发送，并保留原生撤销。后缀支持多行，CRLF 会规范为 LF。

灰字预览裁切在原生输入框的可视区域内，不撑高 textarea。接受时会插入完整后缀，包括超出预览范围的文本。

编辑、选区变化、IME 输入、焦点、上下文或运行状态变化、provider 释放、禁用、重载和卸载会使待处理结果失效。应响应 abort signal，并清理自己创建的定时器和订阅。释放注册句柄会移除贡献，generation 清理也会释放它。忽略取消不能让过期结果改动草稿，但可能让插件自己的工作继续运行。

Provider 会收到尚未发送的草稿。应明确选择哪些数据可以离开机器，并向用户说明模型或网络用途。通过 `api.invokeRuntime` 委派时，将同一 signal 传入 options，再把运行时执行的 signal 继续传给下游工作。[单次模型请求](plugin-authoring.md#单次模型请求)使用已配置的提供商，可能产生费用；取消不会撤销已完成的请求，也不保证提供商已接收的请求免费。

兼容旧桌面宿主时，注册前先检查 `typeof api.registerComposerInlineCompletionProvider === "function"`。对于已支持注册的旧宿主，通过 `typeof handle.refresh === "function"` 检测刷新和交互能力。context 中的 `contractVersion: 1` 只描述此回调结构，与运行时服务版本、Wuu 产品 CalVer 分开判断。准确的公开类型见 [TypeScript SDK](../../../packages/plugin-sdk/src/index.ts)。

工作区相关 provider 还可读取可选的 `workspaceId`（注册项目 ID）、`workspaceRoot`（宿主事件路由根目录）以及 `attachmentCount`、`queuedCount`、`pendingCount`。计数不包含附件字节或排队草稿正文。旧宿主缺少字段时表示未知，不能当作零。这些值参与请求失效判定，分屏也一样。调用运行时应传入已验证的工作区 ID，不应假定当前活动工作区或会话 ID 全局唯一。

`host.session.history.read` 返回物理审计记录，可能包含已撤回或隐藏的消息，不等于可见对话文本。使用多轮上下文时应通过 `api.readVisibleHistory` 读取可见历史，并与实时成功 `turn/completed` 的源轮次核对。

监听 `api.onHostEvent` 的连接相关逻辑应在 `kind: "connection-reset"` 时清除待处理临时任务。远程通知标记 `replayed: true` 时，表示事件到达时有序恢复屏障尚未完成，不能用来触发新的实时任务。普通桌面与实时事件省略该标记。连接重置不表示内核失败，也不会丢弃用户草稿。

## 诊断

插件详情通过 `plugin/diagnostics/list` 查询内置界面模块和已安装插件。请求中的 `id` 和 `fingerprint` 根据本次请求保留的运行代次校验；未知、已禁用和过期的身份仍返回错误。

返回结果包含运行时贡献失败，以及界面上报的注册、渲染和清理失败。界面诊断的 `contribution` 标明窗口连接和阶段，`message` 保留上报的错误。健康插件返回空诊断列表。

## 外观和清理

使用 `api.react` 和 `api.ui`，让控件遵循宿主主题、间距和排版。UI 字号与代码字号应分开。只改外观时优先使用声明式主题 token，公开契约见[界面矩阵](theme-surface-matrix.md)。

桌面上的 `api.ui.Avatar` 和公开图标 `mascot` 共用 Wuu 的圆球耳机形象。Avatar 接受像素尺寸 `size`、`className`、`status`（`idle`、`thinking`、`sending`、`responding`、`queued`、`waiting`、`failed` 或 `interrupted`）以及 `motion`（默认 `static`，也可选 `ambient`）。紧凑导航使用静态头像，较大且可见的身份区域可启用环境动效。状态变化保留圆球、面部和耳机，环境动效遵循减少动态效果偏好与页面可见性。头像仅作装饰，应为外围控件提供标签。未注入桌面头像渲染器的宿主提供静态智能体图标。兼容旧宿主时，应先检测 `api.ui.Avatar` 是否存在。

可以使用选区、尺寸观察器等标准浏览器 API。定位时优先选择公开的 `data-wuu-component`、`data-wuu-slot`、`data-wuu-surface` 锚点，不要依赖私有类名或 React 内部对象；这些锚点也不保证周围 DOM 结构稳定。对于不由 React effect 清理的监听器、观察器、定时器和其他资源，应显式注册清理。

注册项属于插件 generation，卸载时一起移除。原生桌面退出时，Wuu 先卸载 React 消费者、取消待完成的激活、按依赖反序清理插件并发送完激活报告，然后断开 renderer 并停止后端进程。renderer 若在十秒内无法确认清理完成，会被关闭，失败会记录到日志。这种生命周期管理不是安全沙箱，受信任的 renderer 代码和 CSS 仍可能出错。应保留默认界面的恢复路径、处理异步动作错误，并测试禁用和重载，而不只检查初次渲染。包的信任和更新行为见[插件管理](plugins.md)。

### 运行时导航显示名称

`api.setNavigationTitle(id, title)` 只更新调用方插件当前代次已声明的导航入口。标题会去除首尾空白，长度须为 1–64 个 Unicode 字符。入口未声明时返回 `false`；无效标题及已卸载代次的调用会报错。这只是显示覆盖，重新加载后重置，不改变路由、设置页标签或其他插件。用户偏好仍由插件已有运行时服务持久化，加载或保存后再同步已存标题。

### 界面焦点与恢复

默认外壳通过已挂载界面注册的能力协调焦点，不依赖提供者的 CSS 类名或输入框结构。编辑器槽位接收 `context.registerFocusTarget`；预览槽位接收 `context.layout.registerFocusTarget`。在布局 effect 中注册 `UIFocusTargetV1`，并返回注册提供的清理函数。`focus(options)` 聚焦界面自己的可用控件，尚未准备好时返回 `false`；`contains(element)` 判断元素是否属于该界面。聚焦时应遵守 `preventScroll`。分栏会话提供者将外壳的 `context.composerFocusTargets[pane]` 作为对应编辑器的 `registerFocusTarget` 继续传入。外壳负责导航时机，并避免覆盖用户之后的交互；提供者负责自己的 DOM、就绪状态和清理。替代实现可以使用 input 或其他编辑器，无需模仿默认编辑器的类名。

选定界面失败后保留持续的不可用状态，并可提供局部渲染重试。缺失或冲突的提供者需要在设置中修正选择；重试不得静默切换到内置提供者。可选包装器保留子内容，诊断仍记录真实失败。编辑器拥有的选区接收和模型刷新订阅在其卸载时停止，不再作为外壳的副作用运行。
