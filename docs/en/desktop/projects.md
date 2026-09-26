# Projects

The Project Agent is your lead: it keeps your goals and decisions in view, works directly when useful, and delegates independent work to sessions it manages. You review proposals from isolated sessions and decide what reaches the workspace.

## Start a project

Projects have their own group at the top of the sidebar. Choose **+** in **Projects** to start one in the current workspace, or **New project** in a workspace's menu to start one there. A project draft opens; **New conversation** turns it back into a conversation draft.

Tell the coordinator the result you want and the constraints:

```text
Switch catalog search to server-side pagination with 50 results per page.
Keep the public API unchanged.
```

Sending the first message creates the project and names it after that message's first line. Rename it like any conversation. A workspace can hold several projects.

The lead uses the same tools and permission settings as an ordinary conversation, including workspace instructions such as `AGENTS.md` ([configuration](../reference/configuration.md)) and skills. It can make changes directly when its permission mode allows them; those edits affect its current workspace immediately. Small tasks do not need a team.

## A lead, an optional Side Agent, and Workers

For sustained implementation, the lead can keep one persistent **Side Agent** and add scoped **Workers** for investigation, changes, or verification. The side can create workers too; their completion reports go to both the side that dispatched them and the lead. Workers can communicate but cannot create more project sessions. These are roles on ordinary conversations: they share the same history, model settings, tools, worktrees, and user controls. Reusing a side preserves its context, including after a restart.

The lead delegates goals, constraints, ownership boundaries, dependencies, and acceptance evidence. It should leave implementation decisions to the agent inspecting the code, which can challenge mistaken assumptions. Sessions do not see the lead's conversation: each needs a self-contained brief and relevant user instructions. The lead remains responsible for reviewing and verifying the combined result.

Team members can message each other and the lead directly. Information joins a running turn or waits until the recipient's next turn; a question or request can explicitly wake it. Messages preserve their sender and survive restarts. Important decisions should reach the lead, and a peer message does not grant additional user authorization. Human takeover invalidates queued messages involving that session; returning it does not replay those old requests.

A new member inherits the lead's model unless a configured model alias is selected. Its model can then be changed using the ordinary conversation controls. No extra model or session runtime is introduced.

To bring existing work into a project, drag a conversation from its workspace onto the project in the sidebar. The coordinator manages it from then on and reads its latest answer. Only a conversation of the project's workspace can join.

A project occupies one row in the sidebar; its sessions do not crowd the workspace list. The row shows when any of its sessions is running and how many proposals await your review. In the project conversation:

- Above the composer, the project's running work and pending reviews open the project in the right panel: what awaits review, then every session. A session that you took over or paused says so.
- Each event, such as a session finishing a turn, reads as one line with **Review** and **Open session**. **Details** shows the text the coordinator received.

In a Git workspace, a session that changes files works in its own Git worktree by default, so their files are isolated. Worktrees do not resolve interface conflicts: assign one writer to each overlapping scope and verify changes together. A session that works in the workspace directly edits its files in place.

When a session's turn ends, the coordinator decides the next step: a correction, a follow-up session, or telling you what is ready. A result is evidence, not proof that the goal is met. To get an independent check, ask the coordinator to review a change. It starts a separate session from a frozen copy of the change, so the review can run tests without altering it.

## Review and apply changes

When a worktree session finishes a turn with changes, Wuu freezes them as a proposal: every change of the session that has not been applied or published yet. A newer proposal from the same session replaces the older one, so there is always one proposal to decide. The proposal also appears under the turn that produced it. Choose **Review** to open its files and diff in the right panel, then choose one:

- **Apply to workspace** writes the change into the workspace, without staging it. If the workspace has conflicting changes, nothing is applied and the proposal stays open.
- **Open PR** appears when an extension provides it, for example the [Git Delivery example](../../../examples/plugins/git-delivery/README.md). The PR is the proposal's decision; its link stays on the proposal.
- **Reject** records that you declined the change. The changes stay in the session's worktree and appear in its next proposal unless the session reverts them.

The coordinator is told what you chose. A session's next proposal starts from what you applied or published, so a delivered change is never offered again.

## Write to a session or take it over

You can write to a managed session at any time. Your message steers the running turn or starts one, the project keeps managing the session, and the coordinator reads what you wrote with the turn's result.

To work in a session without the coordinator, choose **Take over** in its title bar. Stopping a turn pauses management the same way. The coordinator stops instructing the session until you choose **Return to project**, and it then checks what changed before continuing. Turns you run in the meantime still leave a proposal for you, but they are not reported to the coordinator.

To make a session an ordinary conversation again, choose **Remove from project** in the project view. Decide its pending proposal first.

## Limits

- Wuu must be running for sessions to work. Results from turns that ended while Wuu was closed arrive when it starts again.
- Projects do not have timers or external triggers yet.
- Each session makes its own model requests, so delegating work uses more tokens than one conversation.
- Archiving or deleting a project ends its management. Its sessions remain as ordinary conversations.
- The phone apps open a project's conversations directly; they do not group them.

For a quick task delegated inside one conversation, use [subagents](subagents.md).
