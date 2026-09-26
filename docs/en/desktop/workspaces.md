# Workspaces

A workspace connects conversations to a local folder. The agent reads and changes the files in that folder, so choose the workspace before sending a task.

## Add a workspace

Choose **Add workspace** in the sidebar. **Use existing folder** selects a folder already on disk; **Create blank workspace** opens a folder dialog where you can create a directory. Neither option copies existing files into Wuu.

Adding a folder registers its path. It does not upload the whole folder. During a task, however, the agent can send relevant file contents and tool results to the selected model provider. See the [security model](../reference/security-model.md).

## Work without a workspace

Use **Conversations** when the task does not belong to a registered workspace. These conversations use a persistent Wuu-managed directory and can still create files or open a terminal. They share that directory rather than receiving an isolated folder for each conversation.

For work you want to keep in a repository, add and select that repository first. Tell the agent where existing outputs should go rather than assuming that switching the sidebar will move them.

## Switch between workspaces

Select a workspace to see its conversations or start a new one. Reopening a saved conversation continues its own work; changing the foreground selection does not redirect other running sessions.

Conversations in the same folder share its files. Separate conversation history does not prevent two agents from editing the same file. Use a [Git worktree fork](conversations.md#fork-from-an-earlier-message) when parallel changes need separate directories.

## Move or remove a workspace

If you move a workspace folder on disk, choose **Relocate…** from its workspace menu and select the new path. Wuu updates the path while retaining the workspace identity used by its state and conversation history. Relocate does not move the files for you. A missing folder is reported as unavailable rather than silently replaced with another workspace.

**Remove workspace** removes its registration from the sidebar. It does not delete the folder's files or saved conversations. Manage history with the archive and delete actions described under [conversations](conversations.md).

## Check access boundaries

For the Wuu engine, use **Read only** for inspection and **Standard** for editing. If a tool reports an out-of-workspace denial, check the target path and workspace registration before changing permissions. Workspace selection, Git isolation, and command confinement solve different problems; see [permission modes](../reference/permissions.md).
