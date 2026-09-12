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

The room shows coordination activity alongside member activity: coordinating, queued,
work in progress, or the names of members processing work. A failed coordinator shows
its error and a **Retry coordination** action. An empty room prompts you to add a member.
The coordinator does not appear as a selectable member or as a public execution session.

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

## Related documentation

- [Agent collaboration and subagents](subagents.md)
- [Conversations and branches](conversations.md)
- [Skills](../customize/skills.md)
- [Memory](../customize/memory.md)
