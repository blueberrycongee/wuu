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

Presenter 接收 `contractVersion`、目标、可选匹配键、公开快照、当前边界的 `host` 和 `fallback`。包装模式应把当前 fallback 保留在输出中；替换模式负责整个边界。宿主处理多个替换之间的竞争并组合包装，渲染失败时在受影响的边界回退。

当前内置目标为 `conversation.item`、`conversation.process`、`conversation.tool-activity`、`conversation.composer`、`header.conversation`、`header.workspace`、`navigation.primary`、`app.status`、`content.preview` 和 `settings`。TypeScript 类型允许更多字符串，不代表宿主会渲染任意新目标。

通过 `host.actions` 查看本次渲染公布的动作，再用 `host.invoke(action, input)` 调用。宿主检查 generation 是否仍然有效，并验证动作输入和当前状态。动作即使已公布，也可能因输入框只读或禁止发送而拒绝执行。

例如，`conversation.composer.set-draft` 接受字符串，移除附件接受附件 ID，发送和停止不接受输入。不要把 SDK 常量当作已实现动作：当前输入框并不公布 `conversation.composer.set-submission-mode`。

工具活动 Presenter 定制执行摘要和控件。富结果正文由宿主单独放置：`inline` 输出位于回答之前，`turn_end` 输出位于回答之后。应使用 `tool-result` renderer 定制这些正文，而不是在工具活动 Presenter 中重复渲染 `structuredResult.content`。

## View、卡片和状态

View 是注册的组件，可以放在 `navigation`、`primary`、`auxiliary`、`inspector`、`settings` 或 `overlay`。`registerViewPlacement` 请求初始位置；manifest 的 `contributes.navigation`、`workspaceTools`、`settingsPages` 为用户提供宿主管理的打开入口，每个入口都必须引用该插件注册的视图。

`persistence=durable` 用于恢复视图布局状态，不会持久化任意组件状态。卸载后仍需保留的数据应放入命名空间存储。View props 包含访问存储、设置、命令和视图导航的 host API。

会话和插件主视图使用侧栏导航，包括通过 API 打开、没有声明导航入口的视图。它们的标题栏不提供 `tabs`、`activeTabId` 或 tab 操作。插件主视图提供 `canNavigateBack` 和 `header.navigate-back`，并由宿主单独提供关闭控件；紧凑工作区标题栏也采用这种方式。这些仍是 contract version 1 的可选字段和动作，应检查快照和 `host.actions`，不要从已保存视图重建顶部标签栏。辅助面板和插件内容内部仍可使用各自的标签页。

会话卡片用于临时交互，不是保存的历史或持久页面；卡片句柄可以更新状态或关闭卡片。输入框状态源通过 `getSnapshot(context)` 返回结构化条目，通过 `subscribe` 通知变化，数据未变时应保持快照引用稳定。状态行、溢出和可选的 `open-session` 动作由 Wuu 渲染处理，不接收每个条目的任意 React 内容。

## 外观和清理

使用 `api.react` 和 `api.ui`，让控件遵循宿主主题、间距和排版。UI 字号与代码字号应分开。只改外观时优先使用声明式主题 token，公开契约见[界面矩阵](theme-surface-matrix.md)。

可以使用选区、尺寸观察器等标准浏览器 API。定位时优先选择公开的 `data-wuu-component`、`data-wuu-slot`、`data-wuu-surface` 锚点，不要依赖私有类名或 React 内部对象；这些锚点也不保证周围 DOM 结构稳定。对于不由 React effect 清理的监听器、观察器、定时器和其他资源，应显式注册清理。

注册项属于插件 generation，卸载时一起移除。这种生命周期管理不是安全沙箱，受信任的 renderer 代码和 CSS 仍可能出错。应保留默认界面的恢复路径、处理异步动作错误，并测试禁用和重载，而不只检查初次渲染。包的信任和更新行为见[插件管理](plugins.md)。
