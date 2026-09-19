# Desktop UI 插件

Desktop 插件是在 Wuu Renderer 中加载的受信任 ESM 模块。它可以使用宿主 React 和 UI Kit，
在稳定的界面边界注册组件、页面、样式和动作，而不需要 fork Wuu 或修改宿主源码。

本页先建立 UI 组合模型。完整类型和 manifest 见[插件开发参考](plugin-authoring.md)，可复制的
实现见[插件场景教程](plugin-recipes.md)。

## Wuu 的 UI 怎样被插件扩展

Wuu 保留 App Shell、窗口安全区、导航结构、滚动、Tab、插件管理和恢复路径的最终所有权。
宿主在这些结构中提供不同粒度的语义边界：

```text
Wuu Desktop
├── Sidebar
│   ├── sidebar.primary                 Slot
│   └── sidebar.footer                  Slot
├── Workspace
│   ├── workspace.header                Slot
│   └── View regions                    View
└── Conversation
    ├── conversation.header             Slot / Presenter
    ├── conversation.timeline           Surface
    │   └── conversation.message        Surface / Presenter
    │       ├── conversation.message.before   Slot
    │       └── conversation.message.after    Slot
    └── conversation.composer           Presenter
        ├── composer.above               Slot
        ├── composer.toolbar             Slot
        └── Composer 状态行              结构化状态源
```

插件在运行时注册贡献，Wuu 把它们和原生 UI 组合。禁用、升级或卸载插件时，当前 generation
的全部贡献一起移除。

## 按目标选择机制

| 目标 | 使用 | 例子 |
| --- | --- | --- |
| 在一个固定位置增加小控件 | Slot | Composer 按钮、消息尾部标记、Header 状态 |
| 改造一个具体产品概念 | Presenter | Composer、消息项、工具活动卡片 |
| 包装或替换一段较大的语义区域 | Surface | 对话时间线、单条消息边界 |
| 增加完整页面或复杂工具 | View | Dashboard、历史列表、编辑器、可视化面板 |
| 在环境面板显示短摘要 | Inspector Section | 运行状态、计划摘要、仓库信息 |
| 在对话底部显示临时交互 | Conversation Card | 一次命令的状态和操作 |
| 发布紧凑的 Composer 状态 | Composer 状态源 | 活跃子任务、后台工作 |
| 只改变主题或样式 | Theme tokens / CSS snippets | 配色、字体、密度、语义装饰 |

优先使用能完成需求的最小边界。一个按钮不要替换整个 Composer；长列表不要塞进工具栏。

## Slot：在固定位置增加内容

当前生产 Slot：

| Slot | 位置 |
| --- | --- |
| `sidebar.primary` | 侧栏主要内容区 |
| `sidebar.footer` | 侧栏底部 |
| `workspace.header` | 工作区 Header |
| `conversation.header` | 对话 Header |
| `conversation.message.before` | 每条消息之前 |
| `conversation.message.after` | 每条消息之后 |
| `composer.above` | Composer 上方 |
| `composer.toolbar` | Composer 工具栏 |

Slot 是增量贡献，不接管原生边界。多个插件可以按 `order` 一起出现。

## Composer 状态源：发布紧凑状态

Composer 附近的状态应使用 `api.registerComposerStatusSource`。状态源提供
`getSnapshot(context)` 和 `subscribe(context, listener)`，并返回结构化的
`ComposerStatusItem`。每项包含 `id`、`label`、可选状态和详情，以及类似
`{ kind: "open-session", sessionId }` 的可选声明式动作。

插件只提供数据。Wuu 为每项渲染一个独立胶囊，并统一负责截断、溢出、焦点样式、
排列和动作分发。任意 React 内容和插件 CSS 都不能进入该状态行。状态源通知监听器
之前，应保持 snapshot 的引用稳定。

## Presenter：改造一个产品概念

Presenter 收到四样东西：版本化 snapshot、当前边界可用的 `host.actions`、原生 `fallback`，
以及目标和匹配 key。`mode: "wrap"` 保留并包装当前结果；`mode: "replace"` 接管整个边界。

Tool activity Presenter 只拥有过程外壳和摘要，不拥有富 ToolResult body 的落位。宿主会把有序 `inline` part 放在
过程折叠区与回答之间，把 `turn_end` part 放在回答之后；插件应使用 `tool-result` Renderer
定制每个 body。Presenter 再次渲染 `structuredResult.content` 会造成宿主输出重复。View 是
稳定 Workbench 区域，不是消息时间线插槽。

主要目标包括：

- `conversation.item`
- `conversation.process`
- `conversation.tool-activity`
- `conversation.composer`
- `header.conversation`
- `header.workspace`
- `navigation.primary`
- `app.status`
- `content.preview`
- `settings`

TypeScript 的 `PresentationTarget` 类型为未来 dotted string 留有空间，但当前 manifest
校验只接受上面十个目标。

Action 不是全局权限。只有列在当前 presenter `host.actions` 中的动作才可调用。例如 Composer
边界会按当前状态提供设置草稿、提交、停止或附件动作；只读和禁用状态仍由宿主校验。

当前 Action 词表：

| 边界 | Action ID |
| --- | --- |
| Conversation item | `conversation.item.copy`、`conversation.item.edit`、`conversation.item.retry`、`conversation.item.open-attachment`、`conversation.item.open-tool`、`conversation.item.cancel-process` |
| Composer | `conversation.composer.set-draft`、`conversation.composer.add-attachment`、`conversation.composer.remove-attachment`、`conversation.composer.set-submission-mode`、`conversation.composer.submit`、`conversation.composer.stop` |
| Header | `header.select-tab`、`header.close-tab`、`header.navigate-back`、`header.navigate-forward` |
| Navigation | `navigation.activate-node`、`navigation.pin-node`、`navigation.unpin-node` |
| Status | `status.activate-item` |
| File preview | `file-preview.open`、`file-preview.reveal`、`file-preview.select`、`file-preview.save`、`file-preview.reload` |
| Settings | `settings.open-page`、`settings.update-value`、`settings.refresh` |

这只是公开词表，不代表每次 render 都会提供全部 Action。

会话与插件主视图统一使用侧栏导航，包括通过 API 打开、没有声明导航入口的视图。
它们的标题栏不提供 `tabs`、`activeTabId` 或 tab 操作。插件主视图通过
`canNavigateBack` 和 `header.navigate-back` 返回会话；宿主另行保留关闭控件。
紧凑工作区标题栏同样提供返回导航和宿主关闭控件，不提供 tab 操作。这些字段和动作在 contract version 1 中本来就是
可选项，因此不提升契约版本。Presenter 应根据 snapshot 和 `host.actions` 渲染，
不要从已保存视图重建顶部 tab 栏。辅助面板或插件内容内部的 tabs 不属于主导航。

## Surface：包装较大的语义边界

当前生产 Surface：

| Surface | 范围 |
| --- | --- |
| `conversation.timeline` | 一个 Turn 的时间线和编排组 |
| `conversation.message` | 一条经过脱敏的消息边界 |

Surface 适合结构包装和强视觉变化，不适合只添加一个按钮。每个 Surface 都有原生 fallback；
插件渲染失败时只回退当前边界。

## View：增加完整页面

插件先用 `registerViewType` 注册组件，再用 `registerViewPlacement` 请求初始区域：

- `navigation`
- `primary`
- `auxiliary`
- `inspector`
- `settings`
- `overlay`

宿主负责 Tab、关闭、滚动、持久化和区域布局。插件不会获得任意父节点、分割树或面板尺寸。
面向用户的导航、工作区工具和设置入口应在 manifest 中声明，指向同一插件注册的 View。

## 桌面端间距

`desktop/src/renderer/styles/spacing.css` 统一维护间距角色。表单控件使用
`--control-padding-*`，菜单行使用 `--menu-item-padding-*`，输出摘要使用
`--compact-padding-*`，内容分组使用 `--card-padding`，弹窗和面板正文使用
`--panel-padding`。页面边距与分组间距分别命名，即使默认数值相同。

页面负责外沿，分组负责横向内边距，分组中的行负责纵向内边距，避免嵌套内容重复
添加同一层留白。菜单与表单使用最小高度并由内容自然撑高；密度只调整留白，
不缩小最小点击区域。代码字号保持独立。

宿主控件与插件 UI Kit 共用已有公开变量 `--wuu-space-unit` 和
`--wuu-space-density`。内部间距角色在 `data-wuu-density` 边界重新计算，
让紧凑插件页面的子控件同步变化，并避免重复乘以密度。这些内部名称不是新增的
Extension API 变量。安全区域、代码行号栏和图标对齐可保留专用几何规则。

桌面 Vite 服务启动后，打开 `/dev/design-system/` 查看生产组件示例，打开
`/dev/design-system/viewport.html` 检查窄窗口。验收应覆盖明暗主题、14px/20px
UI 字号、标准/紧凑密度、长内容、菜单和弹窗。间距通过实际渲染检查，测试覆盖
行为，不固定 CSS 声明。

## 可以使用标准 Web API

Desktop 插件是 Renderer 中的受信任代码，因此可以使用 `selectionchange`、
`window.getSelection()`、`ResizeObserver`、`IntersectionObserver` 等标准 Web API。

标准 Web API 不等于可以依赖私有宿主结构。需要判断位置或添加 CSS 时，使用公开语义锚点：

```text
data-wuu-component="turn"
data-wuu-component="message"
data-wuu-component="composer"
data-wuu-component="composer-input"
data-wuu-component="composer-send"
data-wuu-slot="..."
data-wuu-surface="..."
```

不要依赖私有 class、React fiber、组件层级或模拟键盘事件。语义锚点由生产测试约束；私有
DOM 只适合本地实验，不属于兼容承诺。

## 宿主仍然拥有的边界

插件不能替换或隐藏插件管理、安全模式、崩溃恢复和原生窗口生命周期。
Desktop 代码和任意 CSS 具有高信任成本，只安装可信来源。同一来源身份的更新延续信任。

下一步：完成[Desktop 插件快速上手](desktop-plugin-quickstart.md)，再按实际需求查看
[插件场景教程](plugin-recipes.md)。
