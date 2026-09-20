# 扩展 Wuu

按需要改变的行为选择扩展方式。可复用的任务流程可以写成技能，已有工具服务可以通过 MCP 接入。需要受管理的代码、宿主服务或桌面界面时，再使用插件。

| 目标 | 从这里开始 |
|---|---|
| 复用任务流程 | [技能](skills.md)与[技能编写](skill-authoring.md) |
| 连接本地或远程工具服务 | [MCP](mcp.md) |
| 在生命周期事件前后执行检查 | [Hook](hooks.md) |
| 安装打包的 agent 或桌面能力 | [Wuu 插件](plugins.md) |
| 选择主题或配置插件 | [主题与设置](themes-settings.md) |
| 保存长期信息 | [记忆](memory.md)与可选的 [Dream](dream.md) |

技能提供指令和资源；MCP 暴露另一个进程或服务的工具；Hook 在支持的事件上运行命令或模型检查。插件可以把这些贡献与 agent 代码、桌面代码、主题和设置组合在同一个包生命周期中。

## 开发扩展

需要模型可调用的工具或运行时行为，从 [agent 插件快速开始](plugin-quickstart.md)入手；需要界面贡献，从[桌面插件快速开始](desktop-plugin-quickstart.md)入手。[桌面扩展指南](desktop-plugins.md)说明可用的界面边界，[实用示例](plugin-recipes.md)展示具体做法。

[编写参考](plugin-authoring.md)提供包字段和 API，[系统架构](plugin-system.md)说明加载、生命周期和兼容性边界。编写本地扩展前，不必先读完整份架构参考。

## 理解信任范围

技能虽然是文本，也能影响工具使用。MCP 服务和 Hook 可以执行代码或向外发送数据。agent 插件在受管理的进程中运行，桌面插件则在 renderer 中运行受信任代码。Wuu 不为已安装扩展提供沙箱或安全认证。

agent 权限模式约束支持的工具执行路径，不约束你安装的所有任意代码。使用扩展前，应检查来源和数据访问方式；详见[安全模型](../reference/security-model.md)。
