# Workspaces and projects

A workspace is a local folder. Select the right directory before starting a task
so the agent does not change files in the wrong project.

## Add a workspace

In the **Workspaces** area of the sidebar, choose **Add workspace**:

- **Use existing folder:** register an existing project directory;
- **Create blank project:** create and register a new empty directory.

Files stay in their original location. Adding a workspace does not itself upload the
whole project. During tasks, prompts, relevant file contents, and tool results may
be sent to your chosen provider; see [security](../reference/security-model.md).

Without a project, you can start in **Conversations**. Project-less conversations
share a persistent Wuu-managed directory where they can create files and open a
terminal. To keep results in your own project, specify the target workspace.

## Switch between and organize projects

Select a project name in the sidebar to see its conversations, reopen one, or start
a new one there. Multiple conversations may change the same folder at once. Use a
[worktree fork](conversations.md#fork-from-an-earlier-message) when you need file isolation.

After moving or renaming a directory, choose **Relocate…** from the workspace menu
and select its new location. This preserves the project identity and conversation
ownership without adding a second project with the same name.

**Remove workspace** removes the sidebar registration, leaving files and saved
conversations intact. Use conversation archive or delete actions to manage history;
see [conversations and branches](conversations.md).

## Workspace boundary

Choose **Read only** for inspection and **Standard** for edits. If access is denied
outside the workspace, check that the target directory has been added before
considering unconfined mode. See [permission modes](../reference/permissions.md)
for command sandbox and sensitive-path restrictions.

Use [files, diffs, and terminals](workspace-tools.md) to check the result.
