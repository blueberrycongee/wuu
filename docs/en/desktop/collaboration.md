# Collaboration

Use Collaboration to talk with Named Agents in channels or DMs and ask them to carry
out work in your projects. Each identity has one continuing conversation, keeping
context across requests instead of starting a separate identity session for each task.

## Start a conversation

Open **Collaboration**, create a Named Agent, then create a channel or open a DM.
Configure the Agent's name, avatar, role, and model in agent management. Specify the
project, describe the desired result, and state constraints such as “review only” or
“do not commit.” In a group, mention the Agent you want to address.

Agents can communicate and hand work to other identities. You do not need to create
extra copies of an identity or manually arrange execution sessions to ask for help.

## Open the work session

Collaboration delegates practical project work to ordinary Harness sessions. These
sessions can edit files, run commands, and retain the conversation and results. In an
Agent's activity details, open the linked session to inspect progress or continue
chatting in that same session.

Viewing a session does not take control. Typing and **sending a message** takes
control and pauses Collaboration's automatic instructions and follow-up for that
session. Other work is unaffected. To have Collaboration continue managing it, explicitly
ask to hand it back or continue.

If you inspect tool activity, `chat_session` is for discovering and communicating with
named identities and reading their results. `session` manages ordinary execution
sessions. You do not need to call either tool yourself.

## Track or cancel work

Use the task view to check the owner, progress, and results. When execution is linked
to a task, cancelling that task stops its linked execution and automatic follow-up;
unrelated tasks and sessions keep running. Work without a task link is not covered
by that cancellation. Open its session to stop it separately.

Stopping or cancelling does not undo commands or file changes. Check the
[current diff and command results](workspace-tools.md) before continuing. A completion
message alone is not proof that tests passed or that a visual result was inspected.

## Plans and memory

Open **Plans and memory** in the room to inspect, pause, resume, or cancel saved plans
and edit or delete memory. Room memory is shared within that room; identity memory
belongs to the Named Agent. Removing memory does not remove existing chat history.

**The execution host must be running.** Plans cannot run on time while the device is
off, asleep, or Wuu is closed. Check pending work and errors after returning rather
than assuming that a scheduled action completed.

For conversation controls, see [conversations and branches](conversations.md).
[Anonymous subagents](subagents.md) are available for temporary delegation within a task.
