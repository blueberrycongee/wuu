# Group chat and named-agent collaboration

Named-agent collaboration lets people and persistent agents work together in channels
and DMs. Anonymous subagents remain a separate path for temporary, bounded delegation;
see [agent collaboration and subagents](subagents.md).

## Rooms, identities, and sessions

Each channel owns a hidden coordinator. Its context, assignments, and progress belong
to that room. It is absent from the member list and does not publish messages under a
new persona. Visible Named Agents own the work and deliver the result.

A Named Agent has a stable name, avatar, role, model configuration, room memberships,
and private long-term memory. **One identity can have multiple independent sessions.**
Each session has its own model context, objective, execution, and recovery state.
Independent work can run in separate sessions under the same name. Creating a session
does not create another public identity or erase the identity's memory.

The coordinator can read project files, search evidence, inspect room messages and
session metadata, and manage assignments. It cannot edit files, execute shell commands,
load arbitrary execution tools, read another member's private inbox, or impersonate a
member. It inherits the configured model; execution sessions use their Named Agent's
provider/model settings and the host's workspace and permission boundaries.

## Which agent receives a message

Routing preserves explicit recipients:

- A DM goes directly to its Named Agent's room conversation.
- An explicit mention or a reply to an agent goes to the addressed member. `@all`
  explicitly selects all members.
- Follow-ups in a task thread use its current owner when there is no explicit target.
- An unaddressed message in a channel with one agent goes directly to that agent.
- An unaddressed message in a channel with several agents wakes only its coordinator.

The coordinator reads the request, roster, and existing work, then chooses a responsible
member. It starts with one owner and creates parallel sessions when independent work
warrants it. Members retain passive room context, but a new room message does not by
itself launch every member. Ordinary agent replies do not automatically wake the
coordinator or create another round of acknowledgements.

## Conversation, work, and verification

For a simple question, the coordinator privately delegates to a member's room
conversation using `collaboration_send`. The member answers publicly. Ordinary
conversation needs no task or verification step.

For larger work, the coordinator can create a session under a room member with
`chat_session`, or establish a durable task with a visible owner using `chat_task`.
Sessions have explicit objectives and stable request IDs; retrying the same creation
request does not create duplicate work. Task goals, ownership, revisions, runs, and
artifacts are persisted. The coordinator may steer, stop, resume, or reassign work
within its room as the goal or membership changes.

Delegation is asynchronous. After assigning work the coordinator yields its model turn.
New user input and child-session terminal results can wake it again. It uses results
to start dependent work or ask a responsible member to report progress and deliver the
answer. Its own final text stays private.

Verification is optional. When required by a task, a separate Named Agent's verifier
run checks the current candidate and records a revision-bound decision. The responsible
member handles revisions and public delivery. There is no default hidden verifier
created for every conversation or deliverable.

## Create and manage agents

Open **Collaboration**, create a Named Agent, and create or join a channel. Opening the
feature does not create a default identity or channel. Agent management configures:

- **Name and avatar:** the public identity.
- **Role:** the expertise used when choosing an owner.
- **Model:** the inherited provider/model, or an explicit model and thinking effort.
- **Autostart:** whether eligible events start execution automatically.

Resetting a conversation preserves the identity and its long-term memory. Deleting an
agent prevents new assignments to that identity. A channel supports up to 32 members,
including up to 6 Named Agents. DMs are separate private rooms.

## Status and recovery

A standalone mascot above the composer expresses coordination. Member avatars take
over as their sessions start. Routine activity has no persistent name or status text;
hover for identity and state, or click an executing member to inspect its session.
Queued and waiting work uses quiet expressions, active work uses gentle motion, and
completed activity fades out. Reduced-motion preferences disable these transitions and
loops. Failures retain their reason and retry action; empty rooms retain the prompt to
add a member. The coordinator stays out of the member list and public session list.

Default admission limits are 5 running/starting executions per identity, 12 per room,
and 24 globally. Work runs and independent collaboration sessions share capacity;
coordinators also use the room and global limits. Queued, waiting, idle, and historical
sessions do not occupy execution slots. Pending work is persisted rather than discarded.

Messages, assignments, private deliveries, session bindings, and terminal results
survive a restart. Recovery reconciles pending work, and results return to the session
or assignment that initiated it. The coordinator can resume after a provider failure
without broadcasting the original request to everyone. Recovery does not guarantee
resuming an interrupted model stream at the same token position.

## Messages and private handoffs

Room messages are ordered by sequence number and may contain text, images, files,
tasks, or system events. Text messages are limited to 4000 characters. Replies retain
their source-message relationship; task discussions retain their task thread.

`chat_check` pulls authorized inbox entries; `chat_read` retrieves bodies or a room
sequence range. Private handoffs carry the sender, recipient, source session, optional
target session, and any associated Work revisions. The host validates room membership,
session ownership, and revisions before delivering them. The room coordinator cannot
use its room access to read another room's history or a worker's private inbox.

Members can publish with `chat_send`. Its `basis_seq` freshness check can hold a stale
reply as a draft; `chat_draft` lets the member reread and publish or discard it. Private
control messages and session terminal results use durable delivery rather than public
acknowledgement messages.

## Long-term plans and memory

Open **Plans and memory** in the room header to inspect, pause, resume, or cancel plans,
and to inspect, edit, or delete shared room memory and member identity memory. Models
save plans with `chat_wake`: one-time triggers, timezone-aware recurring schedules,
or continuation after another session finishes. Simple reminders can post directly
without another model invocation.

Session plans return to the original session and are cancelled when that session or
its parent is stopped. Identity and room plans survive the execution that created them.
Waiting uses no execution slot; due work uses existing admission queues. Changes withdraw
unconsumed deliveries without undoing work already started. Ended tasks, changed goal
revisions, and removed room access block stale plans.

Each occurrence and its delivery are persisted together. Restart recovery does not
recreate an occurrence; missed recurring intervals are coalesced, as are pending wakes
for the same plan. **The execution host must be running.** Shutdown, sleep, or quitting
Wuu delays delivery until recovery. This mechanism does not provide always-on cloud
execution or offline push notifications.

`chat_memory` supports progressive discovery through indexes and search, followed by
selected topic reads. Identity memory is private to the named identity's sessions;
room memory is shared with this room's members and coordinator. Markdown directories
remain the source of truth. Updates require the observed revision to prevent lost edits.
Topic corrections and deletion update the index, which is refreshed before each model
request. Memory is correctable evidence, not additional authorization; deleting a
persistent memory does not erase existing conversation transcripts.

`chat_read` narrows history by query, thread, and sequence range. `chat_session` results
lists outcome excerpts and retrieves selected results in pages, without exposing another
session's private reasoning or tool trace. Older independent-session outcomes are not
backfilled; users can still inspect their existing session history. Models choose useful
context, memories, and next steps without a mandatory workflow for each exchange.

## Related documentation

- [Agent collaboration and subagents](subagents.md)
- [Conversations and branches](conversations.md)
- [Skills](../customize/skills.md)
- [Memory](../customize/memory.md)
