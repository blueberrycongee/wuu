# Projects

A project is a conversation with a coordinator for a larger piece of work in one workspace, such as a feature, a migration, or a cleanup. The coordinator plans the work, keeps your goals and decisions in view, and hands every change to sessions it manages. You review what those sessions produce and decide what reaches the workspace.

## Create a project

In the sidebar, open the workspace's menu and choose **New project**, then name it. A workspace can hold several projects. Each project appears in its workspace with its sessions nested under it.

Tell the coordinator the result you want and the constraints:

```text
Switch catalog search to server-side pagination with 50 results per page.
Keep the public API unchanged.
```

The coordinator reads the workspace, including instructions such as `AGENTS.md` ([configuration](../reference/configuration.md)) and skills, but it cannot change anything. It can read and search files, run read-only commands, and use the web. It cannot edit files, run commands that change anything, or operate the browser. For a small, self-contained task, an ordinary conversation is faster and uses fewer tokens.

## Sessions do the work

The coordinator starts a managed session for each independent piece of work and writes it a brief. A session does not see the project conversation, only its brief and later instructions. Each session is an ordinary conversation that you can open at any time.

In a Git workspace, a session that changes files works in its own Git worktree by default, so parallel sessions do not overwrite each other. A session that works in the workspace directly edits its files in place.

When a session's turn ends, its result appears in the project conversation, and the coordinator decides the next step: a correction, a follow-up session, or telling you what is ready. A result is evidence, not proof that the goal is met. To get an independent check, ask the coordinator to review a change. It starts a separate session from a frozen copy of the change, so the review can run tests without altering it.

## Review and apply changes

When a worktree session finishes a turn with changes, Wuu freezes them as proposed changes. Open the session to see the changed files and the diff. Then choose one:

- **Apply to workspace** writes only that change into the workspace, without staging it. If the workspace has conflicting changes, nothing is applied and the proposal stays open.
- **Open PR** appears when an extension provides it, for example the [Git Delivery example](../../../examples/plugins/git-delivery/README.md).
- **Discard** records that you declined the change.

The coordinator is told what you chose. A worktree session's changes reach the workspace only when you apply them.

## Take over a session

Sending a message in a managed session takes it over. The coordinator stops instructing it and is told. Interrupting a session pauses it the same way. Choose **Return to project** to hand the session back; the coordinator checks what changed before continuing. Turns you run while you are in control are not reported to the coordinator.

## Limits

- Wuu must be running for sessions to work. Results from turns that ended while Wuu was closed arrive when it starts again.
- Projects do not have timers or external triggers yet.
- Each session makes its own model requests, so delegating work uses more tokens than one conversation.
- Archiving or deleting a project ends its management. Its sessions remain as ordinary conversations.

For a quick task delegated inside one conversation, use [subagents](subagents.md).
