# Conversations and forks

A conversation keeps messages, tool activity, and results together. Continue the same conversation for follow-up work; create a new one for an unrelated task or fork an earlier point to explore another approach.

## Start and organize conversations

Select a workspace, then start a conversation. Click the title in the conversation title bar to rename it. The name is saved immediately for an existing conversation, and a new conversation keeps it when the first message creates the session. The sidebar menu still provides rename, pin, and archive. An archived conversation is hidden from the normal list and can be restored through **Settings → Archive**.

Delete is permanent: it removes saved history and cleans up associated artifacts and any worktree still bound to the conversation. Preserve outputs and changes you need before deleting. Wuu rejects deletion while the conversation, its side chat, or its child agents are active.

After the first send, the workspace sidebar immediately shows the pending conversation. You can switch away and return while it is being created. Stop cancels this wait and retains your input; a late creation response does not send the cancelled message or keep an unused empty conversation. Starting another conversation uses a separate draft.

## Use Fusion

In **Settings → Runtime → Fusion**, enable Fusion and choose separate Lead and Sidekick models and reasoning levels. You can make it the default for new conversations. In the composer's model service menu, choose **Fusion**; selecting a regular model leaves the mode. Fusion runs on the built-in Wuu engine without plugins or the experimental Project Agent build.

The main conversation is the Lead: it plans, briefs the Sidekick, reviews the changes and tests, and replies to you. The Sidekick works in an ordinary persistent conversation with its own tools and history, in the same workspace. Review corrections continue in that Sidekick. Both models are pinned when the conversation enables Fusion; later settings changes affect new conversations. The model menu and conversation information display the pinned pair; change the defaults in Settings for new conversations.

Each delegated task records its requirements revision and actual Sidekick report. The Lead checks the work and evidence, then accepts that exact report or sends consolidated correction feedback to the same Sidekick. A successful Sidekick turn remains awaiting review until the Lead accepts it. A delegation waits for its exact result by default. A timeout or an explicit background dispatch leaves the Sidekick running and delivers its result once when ready. Adding a requirement while the Lead waits wakes it to handle your message. The Lead can update an active task, including asking it to wrap up and report. Updates advance the requirements revision, so an older report cannot be accepted for newer requirements. A task can narrow Sidekick permissions to read-only; it never widens the conversation permissions. **Stop** interrupts the Lead and Sidekick and invalidates their old queued work; history and completed changes remain. The Lead must confirm that the Sidekick is idle before taking over the same files.

The model menu lists the conversation's actual Lead and Sidekick models. The turn header shows which role is working and retains Sidekick failures after the Lead finishes. Click **Message from Sidekick** to open its actual conversation in split view, even during execution or after failure or cancellation. The Sidekick pane is read-only; send requirements in the Lead conversation. Expand process details for public progress, failure diagnostics, cancellation reasons, task elapsed time, review correction count, and recorded usage for both conversations. Fusion coordinates writes to the shared workspace, while the Lead can read and review during execution. Normal usage statistics include both conversations. Wuu does not estimate savings from a hypothetical single-model run.

If the app server exits during a task, recovery marks consumed work with no live executor as failed and reports the interruption to the Lead. It preserves history and file changes without replaying that work. The Lead can inspect the result and explicitly request a correction or delegate a new task. Inputs that had not started remain queued.

Write coordination lasts for each tool call. A background command can keep writing after that call returns, so the participants must finish or stop commands that modify task files before handing them over. Development servers and watchers may remain when their output paths do not conflict with the other participant's edits; the brief or report must identify those processes and paths. Fusion does not enforce exclusive file access for background processes.

Moving the Lead to a linked worktree moves both conversations together. The Sidekick must be idle before this move and cannot change its workspace independently.

## Find a conversation

Open **Search conversations** in the sidebar or press **Command + P** (**Ctrl + P** on Windows and Linux). With no query, pinned conversations appear first, followed by recently updated conversations. Type to search titles and history across projects, including archived conversations. Results show their project and, for content matches, a highlighted excerpt in the same row. Otherwise identical results also show their last-updated date and time.

Use the arrow keys and Enter, or click a result. Addressable user and assistant message matches reveal and highlight the matching message, including older turns. Long messages load their full content and scroll to the matching text. Title, metadata, and tool-record matches open the conversation normally. Opening an archived result does not restore it to the sidebar. Escape closes search and returns focus to the control that opened it.

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

Select displayed text in an assistant response, including while it is still streaming, then choose **Add to conversation**. Choose **Comment** to add an optional comment beside the selected passage first. Appended output and turn completion keep the selection and comment open while the selected text remains valid; changes to that passage dismiss the menu. Selections appear as quote cards in the attachment tray above that conversation's input, including in split view. Each quote captures the displayed text at that moment. Open a card to read the passage, edit its comment, remove the selection, or open its source. You can send a selection without typing another message. Sending, queuing, and steering include both the quoted text and your comment in the model's input.

Selected passages stay with their draft when switching tabs or leaving split view. Failed sends and editing held messages restore the quotes separately from the editable prompt. **View source** highlights the exact selected passage when the original response is loaded and visible. If it is hidden, unloaded, or changed, Wuu reports that the source is unavailable rather than highlighting another occurrence. Open the source conversation and load its response before trying again.

Older hosts can discard quote metadata while retaining the transmitted text; older desktop clients may not display structured quotes. Use matching current host and desktop versions to retain structured quotes through editing and history recovery. Native clients show the flattened quote and comment as text.

## Attach pasted text

Long pasted text appears as a `.txt` attachment above the input, alongside images, documents, and quotes. Your existing instruction stays in the input; if you paste over selected text, only that selection is removed. Click the card to put the full text back into the input for editing; it then sends as ordinary message text. Leave it attached to send it as a file. Removing one attachment leaves the others and your instruction intact. Short text pastes normally.

Sending creates a UTF-8 working file in `sessions/<thread-id>/input-attachments/` under the workspace's Wuu state directory. The model receives a file reference and reads the content with its file tools when needed, rather than receiving the whole paste as inline context. Conversation history retains the exact text snapshot, including whitespace, for preview, editing, and resubmission.

Text working files use the same seven-day expiry as image working files. Cleanup does not delete the history snapshot or copies saved outside the cache. Opening history does not recreate an expired file; resending the attachment creates a new working copy. Save files in the workspace when you need them beyond seven days.

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

Use `/side` for questions about progress, output, or alternatives without adding those messages to the main conversation. You can also select text in an assistant reply, even while it is streaming and choose **Ask in side chat**. The side composer shows the same selected-text tag; sending a question includes the passage as source context. Availability depends on the engine. For a separate task you want to develop over time, start another conversation or fork instead.

## Use saved sessions from the CLI

```bash
wuu session list
wuu session show --last
wuu session search "keyword"
wuu exec resume THREAD_ID "Continue this task"
```

`wuu exec --continue` resumes the latest session; `--ephemeral` runs without saving one. See [`wuu exec`](../automation/exec.md) for session selection and script options.
