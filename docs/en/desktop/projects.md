# Projects

Project Agent is experimental and disabled in release builds. There is no setting
to enable it. Existing project conversations remain readable but do not run or
resume queued work. The following guide applies only to builds that explicitly
enable the feature; see [development](../project/development.md).

The Project Agent is your lead: it keeps your goals and decisions in view, works directly when useful, and delegates independent work to sessions it manages. The team reviews and delivers work within your authorization and the project's permission settings.

Project coordination is built into Wuu. Creating and managing sessions, delivering results, taking control, and recovering after a restart do not require an installed or enabled plugin. Extensions can add optional tools or delivery actions, but do not own the project or its sessions.

## Start a project

Projects have their own group at the top of the sidebar. Choose **+** in **Projects** to start one in the current workspace, or **New project** in a workspace's menu to start one there. A project draft opens; **New conversation** turns it back into a conversation draft.

Choose `low`, `medium`, `high`, or `ultra` in the draft's mode picker; new drafts
start at `low`. Configure that mode's Lead, Side, and Worker provider/model choices
in **Settings → Runtime → Project Agent modes** before sending. You can save partial
settings, but all three roles must be complete to create a project. Each mode
names a configurable preset, not a reasoning level. See
[configuration](../reference/configuration.md#project-agent-model-choices).

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

Team members can message each other and the lead directly. Information joins a running turn or waits until the recipient's next turn; a question or request can explicitly wake it. Messages preserve their sender and survive restarts. Important decisions should reach the lead, and a peer message does not grant additional user authorization. Stopping or writing to a member does not remove it from the team or invalidate queued team messages.

For a project created with a mode, the Lead, Side, and Worker model choices are
resolved and saved at creation. They remain fixed after settings changes and
restarts, including for members added later. The lead shows its mode; members show
their model without model or effort controls. Supported speed controls remain
available after creation, and permission behavior is unchanged. To use a different
mode, start another project.

Older projects without a mode keep their existing behavior: new members inherit
the lead's model unless a role default or model alias is selected, and ordinary
conversation controls can change their model.

To bring existing work into a project, drag a conversation from its workspace onto the project in the sidebar. The coordinator manages it from then on and reads its latest answer. Only a conversation of the project's workspace can join.

For a mode-based project, the conversation must already match the saved Worker
provider, model, effort, and variant; joining locks those choices. An ordinary fork
stays an independent conversation without project membership or a preset lock.

A project occupies one row in the sidebar; its sessions do not crowd the workspace list. The row shows when any of its sessions is running. In the project conversation:

- Above the composer, the project's running work opens the project in the right panel, which lists its Side Agent and Workers. Running indicators describe execution, not project membership.
- Each event, such as a session finishing a turn, reads as one centered line between messages. Its session name opens the session, and **Details** shows the text the coordinator received. Consecutive events in a turn fold into one line that counts them; expand it to see each event.

In a Git workspace, a session that changes files works in its own Git worktree by default, so their files are isolated. Worktrees do not resolve interface conflicts: assign one writer to each overlapping scope and verify changes together. A session that works in the workspace directly edits its files in place.

When a session's turn ends, the coordinator decides the next step: a correction, a follow-up session, delivery, or a report to you. A result is evidence, not proof that the goal is met. For an independent check, the coordinator can have another session review a session's worktree or branch and run tests without changing it.

## Deliver changes

A worktree session's changes stay on its own branch until the team delivers them with ordinary Git and `gh` commands. Commit, merge, push, and PR changes each require your authorization or an established workflow for that action, and follow the project's permission mode. Tell the lead how you want delivery, such as a draft pull request instead of a merge.

## Write to or stop a session

You can write to a member at any time. Your message steers the running turn or starts one. The member stays in the project, and the coordinator receives a notice without being woken if idle. The turn's result also includes what you wrote.

Stopping interrupts the current turn; it does not transfer control or require a return step. The interrupted result reaches the coordinator without waking it from idle. Normal completion still wakes the coordinator, and a stopped worktree turn keeps its changes in the worktree. The coordinator can assign later work to the same member.

To make a session an ordinary conversation again, choose **Remove from project** in the project view. Its worktree and undelivered changes stay with the conversation. Removing a member also clears its preset lock.

## Limits

- Wuu must be running for sessions to work. Results from turns that ended while Wuu was closed arrive when it starts again.
- Projects do not have timers or external triggers yet.
- Each session makes its own model requests, so delegating work uses more tokens than one conversation.
- Archiving a project keeps its managed sessions out of the desktop sidebar's ordinary conversation list, including after a reload. It does not archive or stop those sessions; restore the project to access them in its project view. If the project is deleted or missing, surviving sessions appear in the ordinary list so they remain accessible.
- The phone apps open a project's conversations directly; they do not group them.

For a quick task delegated inside one conversation, use [subagents](subagents.md).
