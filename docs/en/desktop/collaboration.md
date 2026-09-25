# Collaboration

Collaboration is a project conversation with a named agent. The agent reads and coordinates; ordinary execution sessions do the implementation. Group channels are hidden in desktop and native navigation. Existing channel data and protocol methods remain available.

## Start a project conversation

Open **Collaboration**, create an agent, and configure its name, avatar, role, and model. Start a direct message and select a registered workspace. The same agent can have conversations for different projects; the navigation label includes the project name.

State the result and constraints, for example:

```text
Switch catalog search to server-side pagination, with 50 results per page.
Preserve the public API. Show me the candidate diff before delivery.
```

The conversation's project is the default for delegated execution. Switching the foreground workspace does not redirect existing work. An unavailable project produces an error. Existing unbound conversations keep their history; start a project-bound conversation to use explicit project routing.

The coordinator can read files, discuss decisions, maintain tasks and manage sessions. It cannot edit files, execute commands or operate a browser itself. A read-only judgment can be delivered directly; changes go to an execution session. Sessions inherit the named agent's model configuration.

## Follow and control execution

Task cards appear in the timeline with status, linked sessions and cancellation. The conversation's session panel opens any managed execution. Execution reports distinguish completion, failure and interruption; finishing a turn alone does not complete the task.

Opening a session does not take control. Sending a message does: automatic instructions for that session pause. **Return to manager** explicitly restores management so the agent can inspect your changes before continuing. You can also interrupt the coordinator from the conversation composer.

Cancellation stops execution and follow-up associated with the task. It does not undo commands or file changes. Held replies and completed turns without a published reply are visible in the conversation, so silence can be distinguished from unfinished delivery.

A task has a versioned goal, constraints and recorded decisions. Updates use the current revision. A changed goal or constraint invalidates old execution results and interrupts their turns; the existing session can continue with corrected instructions. Parallel Git implementations use separate worktrees by default. Explicit shared-directory execution permits one writer at a time. Project routing is not a filesystem sandbox; the applicable [permissions](../reference/permissions.md) still apply.

## Review a candidate

Expand **Review candidate** on a task card. A candidate contains a frozen Git diff, the execution's conclusion, recorded checks, unresolved questions and a link to its session. Implementation decisions are recorded with the Work. Tasks that require verification receive a separate read-only reviewer on the frozen candidate; a missing or inconclusive receipt is not a passing check.

Choose one of these delivery actions:

- **Apply to project** applies the reviewed patch without staging it. Conflicts stop application and preserve existing changes. Goal changes require another review.
- **Open PR** uses an installed Git delivery extension. The optional [Git Delivery example](../../../examples/plugins/git-delivery/README.md) creates a draft GitHub PR from only the candidate patch in a separate checkout. It requires Git, Node and authenticated `gh`; disabling it removes the action. Core Collaboration does not publish or merge PRs.
- **Discard** records your decision and keeps the execution history. Discarding the current candidate invalidates its verification and asks the coordinator to reassess.

A recorded check is evidence supplied by the execution, not proof of production acceptance. Inspect the diff, commands and unresolved questions before choosing a delivery action. Candidates without Git changes can still be read but do not provide a Git delivery action.

## Memory and scheduled follow-up

Open **Memory and scheduled tasks** to inspect and edit project or identity memory. Project memory is shared by conversations bound to that workspace and is injected into their coordination and delegated execution. Identity memory belongs to the named agent across projects. Work decisions provide task-specific context; workers can read the original room messages and their own history, but cannot read the coordinator's private transcript or pending private deliveries.

Ask the agent in the conversation to schedule a one-time or recurring follow-up. Conversation timers survive the originating execution session; the panel shows their next occurrence and offers pause, resume and cancel. Work states also have host-managed progress deadlines. A stalled task moves to a visible state requiring attention rather than waiting indefinitely for a model to remember it.

The host must be running to execute work or timers. Persistent scheduling does not make a sleeping or powered-off computer available.

## Delete a conversation or agent

Deleting a project conversation removes that DM, not the named agent. Deleting the agent revokes its credentials, removes its DMs and identity memory, cancels unfinished work it owns or executes, and stops its managed sessions. Completed history retains attribution. Ordinary execution history and file changes remain; deletion does not undo completed commands.

Sessions still managed by the deleted agent, including paused sessions, move to **Settings → Archive → Agent archive**. They do not appear in workspace navigation, unread lists, or the ordinary conversation archive. You can restore them as independent conversations. Sessions you already took over or released from management stay where they are. Wuu also moves orphaned managed sessions left by earlier versions into this archive.

For temporary delegation inside a normal conversation, use [subagents](subagents.md). Developers can consult the [media handoff contract](../automation/app-server.md#named-agent-media-handoff).
