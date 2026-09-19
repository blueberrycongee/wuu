# wuu documentation

Wuu is an AI agent workspace for local projects. Give an agent a task, follow its file changes and commands, and return to the saved conversation when you want to continue. You choose the model service and the permissions for its work.

## Start a task

The [quick start](getting-started/index.md) takes you from installing the desktop app to checking your first result. The current release workflow produces an Apple silicon macOS preview; the app includes its own Wuu core. Terminal users can [install the CLI](getting-started/installation.md#install-the-cli) from source.

For everyday work, learn how to select a [workspace](desktop/workspaces.md), manage [conversations and forks](desktop/conversations.md), and inspect [files, diffs, and command output](desktop/workspace-tools.md). [Collaboration](desktop/collaboration.md) provides named agents and group conversations for work that benefits from several participants.

## Configure your workspace

[Model setup](getting-started/model-services.md) explains API connections, subscription credentials, and the difference between a provider and an agent engine. Read [permissions](reference/permissions.md) and [security](reference/security-model.md) before giving an agent access to private projects: local execution does not mean your model requests stay on the device.

Add reusable workflows with [skills](customize/skills.md), connect external tools through [MCP](customize/mcp.md), or install [plugins](customize/plugins.md). For unattended scripts, use [`wuu exec`](automation/exec.md). If a task fails, start with [troubleshooting](help/troubleshooting.md).

## Develop with Wuu

The [development guide](project/development.md) covers building the app. Extension authors can start with an [agent plugin](customize/plugin-quickstart.md) or a [desktop UI plugin](customize/desktop-plugin-quickstart.md); client authors can use the [app-server protocol](integrations/app-server-protocol.md).

[简体中文](../zh-cn/index.md)
