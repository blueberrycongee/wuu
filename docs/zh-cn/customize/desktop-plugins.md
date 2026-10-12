# 桌面 UI 插件

桌面插件是加载到 Wuu renderer 中的受信任 JavaScript 模块，通过宿主的 React 实例和 UI Kit，在定义好的边界上添加控件、页面和展示方式。可先用[快速上手](desktop-plugin-quickstart.md)创建可运行的包。

## 选择扩展点

| 目标 | API |
| --- | --- |
| 在现有位置添加小控件 | `registerSlot` |
| 改变某个产品概念的展示 | `registerPresenter` |
| 包装或替换消息、时间线区域 | `registerSurface` |
| 添加页面、编辑器或仪表盘 | `registerViewType`，配合 placement 或 manifest 入口 |
| 添加紧凑的检查器区块 | `registerInspectorSection` |
| 渲染特定内容类型 | `registerRenderer` |
| 在会话底部显示临时交互 | `showConversationCard` |
| 发布简短的输入框状态 | `registerComposerStatusSource` |
| 提供草稿行内后缀 | `registerComposerInlineCompletionProvider` |
| 改变外观 | 声明式主题、主题 token 或样式 |

选择能满足需求的最小边界。工具栏按钮不需要替换整个输入框，长仪表盘也不应放进工具栏。视图位置、导航、窗口生命周期和恢复控件由宿主管理。

## Slot 和 Surface

Slot 在原生界面旁添加内容，多个注册项按 `order` 组合。

| Slot | 位置 |
| --- | --- |
| `sidebar.primary`、`sidebar.footer` | 侧栏内容和底部 |
| `workspace.header`、`conversation.header` | 对应的标题区域 |
| `conversation.message.before`、`conversation.message.after` | 每条消息前后 |
| `composer.above`、`composer.toolbar` | 输入框上方或工具栏中 |

Surface 有 `conversation.timeline` 和 `conversation.message` 两种，接收 context 和 fallback，模式为 `wrap` 或 `replace`。时间线边界是一轮对话的时间线与编排组，不代表插件接管整个会话滚动容器。

manifest 的 `contributes.slots`、`surfaces`、`presenters` 描述贡献，桌面模块仍需注册实际渲染。提供声明列表后，注册的 ID、目标、模式和顺序必须与声明一致，且声明的贡献必须在激活期间注册。

## Presenter 和动作

Presenter 接收 `contractVersion`、目标、可选匹配键、公开 `snapshot`、当前边界的 `host`，以及三种组合方式：

- `next()` 继续执行下一个 Presenter；`next({ snapshot })` 只为下游 Presenter 提供替换快照。
- `fallback` 等价于使用当前快照的 `next()`，保留现有包装的行为。
- `original` 是宿主原生渲染，跳过剩余 Presenter。改变快照不会改变这份原生渲染。

包装始终位于替换之外，优先级越高的包装越靠外。用户首选的替换先执行；替换可自行渲染整个边界，也可显式通过 `next()` / `fallback` 让下游处理。下游 Presenter 只有在输出被纳入时才会挂载。不要修改共享快照；需要调整时向 `next` 传入新值，并保持目标的快照契约。

贡献渲染失败时，使用其未经修改的传入快照回退到下一个 Presenter。插件的其余部分保持加载；重新激活后，失败的贡献可再次运行。

当前内置目标为 `conversation.item`、`conversation.process`、`conversation.tool-activity`、`conversation.composer`、`header.conversation`、`header.workspace`、`navigation.primary`、`app.status`、`content.preview` 和 `settings`。TypeScript 类型允许更多字符串，不代表宿主会渲染任意新目标。

通过 `host.actions` 查看本次渲染公布的动作，再用 `host.invoke(action, input)` 调用。宿主检查 generation 是否仍然有效，并验证动作输入和当前状态。动作即使已公布，也可能因输入框只读或禁止发送而拒绝执行。

例如，`conversation.composer.set-draft` 接受字符串，移除附件接受附件 ID，发送和停止不接受输入。不要把 SDK 常量当作已实现动作：当前输入框并不公布 `conversation.composer.set-submission-mode`。

工具活动 Presenter 定制执行摘要和控件。富结果正文由宿主单独放置：`inline` 输出位于回答之前，`turn_end` 输出位于回答之后。应使用 `tool-result` renderer 定制这些正文，而不是在工具活动 Presenter 中重复渲染 `structuredResult.content`。

## View、卡片和状态

View 是注册的组件，可以放在 `navigation`、`primary`、`auxiliary`、`inspector`、`settings` 或 `overlay`。`registerViewPlacement` 请求初始位置；manifest 的 `contributes.navigation`、`workspaceTools`、`settingsPages` 为用户提供宿主管理的打开入口，每个入口都必须引用该插件注册的视图。

`persistence=durable` 用于恢复视图布局状态，不会持久化任意组件状态。卸载后仍需保留的数据应放入命名空间存储。View props 包含访问存储、设置、命令和视图导航的 host API。

会话和插件主视图使用侧栏导航，包括通过 API 打开、没有声明导航入口的视图。它们的标题栏不提供 `tabs`、`activeTabId` 或 tab 操作。插件主视图提供 `canNavigateBack` 和 `header.navigate-back`。只有关闭会把页面从侧栏移除时，宿主才另外提供关闭控件，即通过 API 打开的视图和额外实例；已声明导航入口的页面通过返回或侧栏离开。紧凑工作区标题栏同样提供返回，并为当前标签提供关闭控件。这些仍是 contract version 1 的可选字段和动作，应检查快照和 `host.actions`，不要从已保存视图重建顶部标签栏。辅助面板和插件内容内部仍可使用各自的标签页。

会话卡片用于临时交互，不是保存的历史或持久页面；卡片句柄可以更新状态或关闭卡片。输入框状态源通过 `getSnapshot(context)` 返回结构化条目，通过 `subscribe` 通知变化，数据未变时应保持快照引用稳定。状态行、溢出和可选的 `open-session` 动作由 Wuu 渲染处理，不接收每个条目的任意 React 内容。

## 输入框行内补全

在 `activate(api)` 中调用 `registerComposerInlineCompletionProvider({ id, order, provideCompletion })` 注册 provider。回调接收只读的 `ComposerInlineCompletionContext` 和 `AbortSignal`，同步或通过 promise 返回 `{ text: "字面文本后缀" }` 或 `null`。没有建议时返回 `null`。宿主将已接受的文本追加到草稿；回调不替换整份草稿，也不接管 textarea。

版本 1 的 context 包含 `kind`、`composerId`、`revision`、可选的 `threadId` 和 `sideThreadId`、`draftText`、`selectionStart`、`selectionEnd` 和 `running`。主输入框和分栏输入框的 `kind` 为 `main`，侧输入框为 `side`，尚无 `sideThreadId` 的新侧草稿也明确标为 `side`。选区偏移使用 JavaScript UTF-16 索引。`composerId` 是不透明标识，作用域为已挂载输入框及其会话/工作区上下文；revision 不是全局会话版本。侧会话输入框的 `threadId` 表示所属主会话，`sideThreadId` 表示已创建的侧会话。

`running` 表示输入框的展示状态。最终回答就绪后，主输入框和分栏输入框可能已显示空闲，而运行时仍在收尾。值为 false 不代表该轮已成功结束。仅在成功完成后提供建议的策略应观察回合生命周期事件，并明确激活或重载后的行为。

需要主会话上下文时，通过 `api.invokeRuntime` 传入 `threadId`，在运行时处理函数中调用已声明的 `host.session.history.read` 服务。重载后也能读取普通用户会话，其他插件的私有会话仍不可访问。插件自行选择有限的历史范围。侧会话历史独立存储，此运行时服务未开放读取；不要把 `sideThreadId` 当成它的 `session_id`。需要主会话历史的 provider 可以对 `kind: "side"` 返回 `null`，无需访问宿主私有状态。

Provider 按 `order` 升序调用，再依次按插件 ID、注册 ID 排序，逐个尝试。第一个非空结果生效；无结果或 provider 出错时继续尝试下一项。宿主渲染建议文本，并在接受前核对请求是否仍然有效。上下文获取、提示词、防抖和请求预算由插件负责。该 API 不会安装补全 provider 或自动调用模型的策略。

宿主在符合条件的输入状态变化时调用 provider。版本 1 没有由 provider 主动请求刷新的方法；无关的宿主事件或插件设置变化本身不会重新调用已返回的回调。等待事件或防抖应限制在当前可取消请求内，并设定时限；不符合条件时返回 `null`。尚未返回的 provider 会阻塞后续 provider，宿主不自动为 provider 设置超时。

原生输入框只在已聚焦、可编辑且光标折叠在草稿末尾时请求建议，空草稿也可使用。输入法组合输入、斜杠命令草稿、打开的菜单或对话框，以及隐藏的折叠提示内容不参与补全。斜杠命令保留 Tab 优先级。无修饰键且非重复触发的 Tab 接受当前建议；没有建议时保留正常焦点导航。Escape 关闭当前建议或取消待处理请求。接受只修改草稿，不发送，并保留原生撤销。后缀支持多行，CRLF 会规范为 LF。

灰字预览裁切在原生输入框的可视区域内，不撑高 textarea。接受时会插入完整后缀，包括超出预览范围的文本。

编辑、选区变化、IME 输入、焦点、上下文或运行状态变化、provider 释放、禁用、重载和卸载会使待处理结果失效。应响应 abort signal，并清理自己创建的定时器和订阅。释放注册句柄会移除贡献，generation 清理也会释放它。忽略取消不能让过期结果改动草稿，但可能让插件自己的工作继续运行。

Provider 会收到尚未发送的草稿。应明确选择哪些数据可以离开机器，并向用户说明模型或网络用途。通过 `api.invokeRuntime` 委派时，将同一 signal 传入 options，再把运行时执行的 signal 继续传给下游工作。[单次模型请求](plugin-authoring.md#单次模型请求)使用已配置的提供商，可能产生费用；取消不会撤销已完成的请求，也不保证提供商已接收的请求免费。

兼容旧桌面宿主时，注册前先检查 `typeof api.registerComposerInlineCompletionProvider === "function"`。context 中的 `contractVersion: 1` 只描述此回调结构，与运行时服务版本、Wuu 产品 CalVer 分开判断。准确的公开类型见 [TypeScript SDK](../../../packages/plugin-sdk/src/index.ts)。

## 外观和清理

使用 `api.react` 和 `api.ui`，让控件遵循宿主主题、间距和排版。UI 字号与代码字号应分开。只改外观时优先使用声明式主题 token，公开契约见[界面矩阵](theme-surface-matrix.md)。

可以使用选区、尺寸观察器等标准浏览器 API。定位时优先选择公开的 `data-wuu-component`、`data-wuu-slot`、`data-wuu-surface` 锚点，不要依赖私有类名或 React 内部对象；这些锚点也不保证周围 DOM 结构稳定。对于不由 React effect 清理的监听器、观察器、定时器和其他资源，应显式注册清理。

注册项属于插件 generation，卸载时一起移除。这种生命周期管理不是安全沙箱，受信任的 renderer 代码和 CSS 仍可能出错。应保留默认界面的恢复路径、处理异步动作错误，并测试禁用和重载，而不只检查初次渲染。包的信任和更新行为见[插件管理](plugins.md)。
