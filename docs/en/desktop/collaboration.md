# Collaboration

Collaboration provides channels and direct messages for working with named agents. An agent's identity has a continuing conversation and can manage separate work sessions, so discussions can continue across requests without putting every task into one execution session.

## Start with a named agent

Open **Collaboration**, create an agent, and configure its name, avatar, role, and model. Open a direct message or add it to a channel. In a group conversation, mention the agent you want to address.

State the target project, the result you need, and the limits on the work. For example:

```text
Review the search changes in the catalog project. Explain any regressions with
file references. Do not edit files or create a commit.
```

Named agents can discover registered projects and communicate with other identities. A clear project name matters: switching your foreground workspace does not redirect a work session already assigned elsewhere.

When human messages and background reports are waiting, the agent handles human directions first in its next coordination turn. Work sessions continue independently, and earlier reports remain queued for their original room and task.

## Follow the execution

Project work runs in ordinary work conversations, also called Harness sessions in tool activity. Open the linked session from an agent's activity details to see file changes, commands, and results. It remains bound to its project and execution configuration; an unavailable or conflicting project binding produces an error rather than falling back to another project.

Session reports distinguish completed, failed, and interrupted execution. A control change or an evidence message does not claim successful completion. Ending an execution turn does not complete its task; the named agent still needs to assess the result against your request.

Looking at the conversation does not take control. Sending a message does: Wuu pauses Collaboration's automatic instructions and follow-up for that session so you can continue the work yourself. If you want the named agent to manage it again, explicitly ask it to resume management.

In tool activity, `chat_session` handles communication between named identities, while `session` manages ordinary execution sessions. You can work through natural-language requests rather than calling these tools yourself.

When delegating a task, a named agent can select images or supported attachments from the current channel or direct message. The work session receives the stored media and its source context; unselected attachments are not forwarded. Missing, inaccessible, or unsupported evidence produces an error. Choose a compatible model or reattach the evidence before continuing. Developers can consult the [media handoff contract](../automation/app-server.md#named-agent-media-handoff).

## Track and cancel a task

The task view shows ownership, progress, and results. Cancelling a task stops the execution and follow-up linked to that task; unrelated sessions keep running. If a session has no task link, open it and stop it separately.

Cancellation does not roll back file changes or completed commands. Check the [current diff and command results](workspace-tools.md) before accepting or resuming the work. Project routing keeps the task attached to the right project, but does not itself provide a filesystem sandbox; execution uses the applicable [permissions](../reference/permissions.md).

## Delete a named agent

Deleting an agent removes it from the agent list and its channels, deletes its direct messages and identity memory, and revokes its credentials. Task history does not prevent deletion. Unfinished tasks it owns, leads, or is actively executing are cancelled; its conversation and managed execution sessions stop, including sessions without a task link.

Shared channel messages and completed task history retain their original attribution. Ordinary project session history and file changes remain available; deletion does not undo completed commands.

## Keep work available

Room memory is shared within its room; identity memory belongs to the named agent. Agents can use these stores and saved plans as part of continuing work. Scheduled execution needs the host to be running: a sleeping or powered-off machine cannot carry out the task on time.

For temporary delegation inside a normal conversation, use [subagents](subagents.md). For message, archive, and fork controls, see [conversations](conversations.md).
