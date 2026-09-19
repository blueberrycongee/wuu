# wuu 文档

Wuu 是面向本地项目的 AI Agent 工作台。你可以交给 Agent 一个任务，查看它修改的文件和运行的命令，之后回到保存的对话继续工作。使用哪个模型服务、允许它做什么，由你决定。

## 开始一个任务

[快速开始](getting-started/index.md)介绍从安装桌面应用到检查第一个结果的完整过程。当前发布流程提供 Apple 芯片 macOS 预览版，应用自带 Wuu 核心程序。习惯使用终端的用户也可以从源码[安装 CLI](getting-started/installation.md#安装-cli)。

日常使用时，可以先了解怎样选择[工作区](desktop/workspaces.md)、管理[对话与分叉](desktop/conversations.md)，以及检查[文件、改动和命令输出](desktop/workspace-tools.md)。需要多方参与的任务，可以在[协作](desktop/collaboration.md)中使用具名 Agent 和群聊。

## 配置工作环境

[模型配置](getting-started/model-services.md)介绍 API 连接、订阅凭据，以及模型服务与执行引擎的区别。将私有项目交给 Agent 前，请先了解[权限](reference/permissions.md)和[安全边界](reference/security-model.md)：任务在本机执行，不代表模型请求也留在本机。

你可以用 [Skills](customize/skills.md)复用工作流程，通过 [MCP](customize/mcp.md)连接外部工具，或安装[插件](customize/plugins.md)。从脚本执行任务可使用 [`wuu exec`](automation/exec.md)；遇到问题时，先看[故障排查](help/troubleshooting.md)。

## 参与开发

[开发指南](project/development.md)介绍应用的构建方法。编写扩展可以从 [Agent 插件](customize/plugin-quickstart.md)或[桌面 UI 插件](customize/desktop-plugin-quickstart.md)入手；开发客户端可参考 [app-server 协议](../en/integrations/app-server-protocol.md)（英文）。

[English](../en/index.md)
