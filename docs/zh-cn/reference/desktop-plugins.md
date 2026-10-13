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
