# 桌面插件 API

桌面模块导出 `activate(api)`，使用绑定 generation 的 `PluginGenerationApi`。类型来自匹配的 Wuu 源码目录中的 `packages/plugin-sdk`；运行时使用宿主提供的 `api.react` 和 `api.ui`。

| 契约 | 指南 |
| --- | --- |
| Slot、Surface、Presenter、View、卡片和状态源 | [桌面 UI 插件](../customize/desktop-plugins.md) |
| 输入框行内补全 provider | [输入框行内补全 provider](../customize/desktop-plugins.md#输入框行内补全) |
| 运行时模型服务 | [单次模型请求](../customize/plugin-authoring.md#单次模型请求) |
| 完整本地包和构建命令 | [桌面插件快速上手](../customize/desktop-plugin-quickstart.md) |
| 草稿动作、工作区页面和运行时调用 | [桌面插件配方](../customize/plugin-recipes.md) |
| manifest、运行时生命周期、存储和分发 | [编写插件](../customize/plugin-authoring.md) |
| 公开主题 token 和语义锚点 | [主题界面矩阵](../customize/theme-surface-matrix.md) |

注册方法返回可释放句柄，注册项属于当前 generation。展示边界公布的动作只适用于该次渲染，不是全局命令权限。私有 renderer 模块、DOM 嵌套和 React 内部对象不属于公开 API。

`api.signal` 在激活后保持有效，直到该 generation 被释放时中止。异步启动有时间上限；超时和被更新操作取代的语义见[桌面 generation 生命周期](../customize/plugin-system.md#桌面-generation-与组合)。

## 稳定展示与绑定服务图的业务作用域

纯展示模块可以导出 `activatePresentation(api)`，使用
`ApplicationPresentationAPI`。桌面与独立预览宿主优先使用这个入口，而非
`activate`。它提供宿主 React、manifest 身份、贡献发现、展示生命周期的
signal，以及 shell、slot 和贡献注册；不提供 application 写入者、运行时调用
或业务 provider 注册。

权威服务图变化时，健康且未改变的展示代码保留注册项和 React 组件身份。
选中的业务 provider 重新激活后，渲染参数获得新的 `application` 作用域；
该对象在同一业务生命周期内保持身份稳定。
应把它加入 effect 依赖，不要存入模块级状态。作用域在创建时即固定权限归属，
不会等第一次方法调用才绑定。此前捕获的动作、订阅和句柄
始终属于旧 provider，不会重定向到后继者。退役后的观察订阅为空，不会向
旧或新 provider 添加监听器。写入者交接期间可显示已经读取的
同步展示值，但不能执行旧业务操作。准备或激活失败仍显示恢复入口，不会
另选 provider，也不会继续运行旧写入者。

代码变化、禁用、缺失必需依赖或连接替换仍会释放展示。主动切换选中的 surface
可以替换其渲染树。
只导出 `activate(api)` 的模块保留原来的保守服务图生命周期。只有注册项不依赖
业务权限时才应选择新入口；运行时工作应放在另一个普通模块中。

环境快照可提供 `actionBusyPending`，区分仍在进行的占用查询与已确定的繁忙或
不可用结果。保留的 Git 状态只属于精确匹配的上下文和工作树根路径；另一目标
状态未知时只能把已有 Git 控件保留为不可交互的中性状态，不显示旧工作区的分支名称。
若此前没有 Git 控件，不能仅因状态待定就新增；确认目标是仓库后才一次性显示。

## 由应用 provider 拥有的工作台与贡献发现

`api.contributions` 是公开的 `UIContributionRegistryV1`，提供命令、视图、
布局声明、内容渲染器、presenter、会话卡片、主题 token、翻译和输入框 provider
的稳定不可变快照与订阅，以及激活身份、挂载代次保留和渲染诊断。业务 provider
无需导入内部 `PluginHost` 或访问桌面单例。挂载保留会等待 React 子树清理效果后
才清理代次，但不会授权已退役代次接收新调用。

选中的 `ui.application` 通过 `application.workbench` 提供公开
`ApplicationWorkbenchV1`，拥有视图与布局状态、打开和关闭视图、捕获的视图及
presentation API，以及层、内容和内嵌视图的渲染。默认实现保留持久布局版本 2；
应用切换检查点同时保留仅限会话的视图。原生视图和主题效果由应用生命周期清理，
不能只删除注册表记录；初始化尚未取得 inventory 时，不能把它误当成空列表删除恢复状态。

声明 slot **不会自动创建产品行为**。`workspace.tools`、`workspace.view`、
`conversation.cards`、`conversation.message.actions`、两个 `header.*`、
`content.preview` 与 `workbench.layers` 不再触发宿主隐式适配器。默认 shell、
conversation 和 preview provider 使用 `registerUIContribution` 显式注册实现。
外部 surface 可以自行消费公开贡献注册表，shell 可以通过显式贡献调用
`application.workbench.renderLayers({ settingsOnly: false })`。卸载所有者会移除
其行为与效果，不会偷偷恢复另一个默认工作台。

捕获的视图 API 在异步工作前后检查代次；同 fingerprint 重载也不会让旧操作改指向
新代次。已经发生的外部副作用不会因此回滚，结果不确定时不能盲目重试。
