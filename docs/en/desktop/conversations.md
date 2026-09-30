# Conversations and forks

A conversation keeps messages, tool activity, and results together. Continue the same conversation for follow-up work; create a new one for an unrelated task or fork an earlier point to explore another approach.

## Start and organize conversations

Select a workspace, then start a conversation. Click the title in the conversation title bar to rename it. The name is saved immediately for an existing conversation, and a new conversation keeps it when the first message creates the session. The sidebar menu still provides rename, pin, and archive. An archived conversation is hidden from the normal list and can be restored through **Settings → Archive**.

Delete is permanent: it removes saved history and cleans up associated artifacts and any worktree still bound to the conversation. Preserve outputs and changes you need before deleting. Wuu rejects deletion while the conversation, its side chat, or its child agents are active.

After the first send, the workspace sidebar immediately shows the pending conversation. You can switch away and return while it is being created. Stop cancels this wait and retains your input; a late creation response does not send the cancelled message or keep an unused empty conversation. Starting another conversation uses a separate draft.

## Find a conversation

Open **Search conversations** in the sidebar or press **Command + P** (**Ctrl + P** on Windows and Linux). With no query, pinned conversations appear first, followed by recently updated conversations. Type to search titles and history across projects, including archived conversations. Results show their project and, for content matches, a highlighted excerpt in the same row.

Use the arrow keys and Enter, or click a result. Addressable user and assistant message matches reveal and highlight the matching message, including older turns. Title, metadata, and tool-record matches open the conversation normally. Opening an archived result does not restore it to the sidebar. Escape closes search and returns focus to the control that opened it.

## Choose where a new conversation works

Above a new conversation's input, the project control shows the workspace it belongs to. In a Git project, a second control holds the branch and a **Worktree** toggle:

| Worktree | Choosing a branch |
|---|---|
| Off | Checks the branch out in the project. Your editor, terminal, and other conversations in the project see the change. |
| On | Only sets the branch the new worktree starts from. The project stays on its current branch. |

With **Worktree** on, the first message creates a separate directory from the chosen branch's committed content, and the conversation works there. Uncommitted changes in the project are not carried over. The worktree starts without a branch of its own (detached `HEAD`), so a pull request from it needs a named branch first. The toggle applies to one new conversation; the next draft starts in the project again.

## While a task is running

The composer supports these keyboard actions:

| Key | Action |
|---|---|
| Enter | Send the draft; when the engine supports steering a running task, add it to that task |
| Command + Enter (Ctrl + Enter on Windows and Linux) | Queue a non-empty draft for after the current turn, when queuing is available |
| Shift + Enter | Insert a new line |
| Escape | Interrupt a running task when the composer can do so |

The `/` menu handles Enter and Tab as command selection while it is open. Tab otherwise moves focus normally. The send control reflects whether the current engine can steer or only queue a message.

Once the final answer is ready, Enter or the send button starts a normal follow-up, including in split view. Background cleanup and refreshing an already loaded conversation do not block sending. A submission keeps its original conversation and workspace even if you switch away while attachments are preparing.

If sending fails, Wuu restores the input when that composer is still empty. If you have switched away or typed a newer draft, the failed input stays in the original conversation instead. If there is no conversation because its creation failed, the error notice lets you open the retained input as a separate draft or discard it. Failed input is not retried automatically.

Use Stop to interrupt a task. Stopping does not undo commands or file edits, and separately managed background work may need its own stop action. Review the [current diff and command results](workspace-tools.md#review) before continuing.

## Quote part of a response

Select text in a completed assistant response, then choose **Add to conversation**. Choose **Comment** to add an optional comment beside the selected passage first. Each quote waits as a card in that conversation's input tray, including in split view. Open a card to read the full quote, edit its comment, or view the source; remove a quote from its card or from the open panel. You can send a quote without typing another message. Sending, queuing, and steering include both the quoted text and your comment in the model's input.

Selected passages stay with their draft when switching tabs or leaving split view. Failed sends and editing held messages restore the quotes separately from the editable prompt. **View source** highlights the exact selected passage when the original response is loaded and visible. If it is hidden, unloaded, or changed, Wuu reports that the source is unavailable rather than highlighting another occurrence. Open the source conversation and load its response before trying again.

Older hosts can discard quote metadata while retaining the transmitted text; older desktop clients may not display structured quotes. Use matching current host and desktop versions to retain structured quotes through editing and history recovery. Native clients show the flattened quote and comment as text.

## Work with pasted images

Paste an image or select an image file, then send the message. Wuu saves a local working file and gives the model its absolute path alongside the image for vision. You can ask the model to copy, move, or process that file using its normal file and command tools. The working file preserves the bytes received by the backend; desktop image compression still applies before sending.

When context compaction omits image data, the image notes and the summary input's media index retain the working-file path. Compaction does not recreate missing files or extend their expiry.

Working files live in `sessions/<thread-id>/input-images/` under the workspace's Wuu state directory. They expire after seven days. Wuu checks for expired files when the app-server starts and every six hours while it runs, so deletion may happen after the expiry time. This cleanup does not remove the image from conversation history or delete files copied or moved outside the cache. To keep an image, ask the model to save it in the workspace. If its working file has expired, send the image again.

## Fork from an earlier message

Choose **Fork** on a historical message, then select where the new conversation should work:

| Choice | Files used by the new conversation |
|---|---|
| Fork locally | The same directory as the original conversation |
| Fork to a Git worktree | A separate directory created from the source repository's current `HEAD` |

A fork copies history through the selected message. It does **not** restore files to that moment. A worktree starts from committed content and does not copy uncommitted changes; review and commit any changes you want it to inherit before creating it. The dialog explains when worktree creation is unavailable.

## Ask a side question

Use `/side` for questions about progress, output, or alternatives without adding those messages to the main conversation. Availability depends on the engine. For a separate task you want to develop over time, start another conversation or fork instead.

## Use saved sessions from the CLI

```bash
wuu session list
wuu session show --last
wuu session search "keyword"
wuu exec resume THREAD_ID "Continue this task"
```

`wuu exec --continue` resumes the latest session; `--ephemeral` runs without saving one. See [`wuu exec`](../automation/exec.md) for session selection and script options.
