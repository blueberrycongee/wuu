# Native Personal client

The desktop product now uses one persistent twin conversation. The native multi-assistant surfaces described below are legacy implementation details and have not been adapted or validated for that desktop redesign. Mobile work is deferred; do not treat these surfaces as the current desktop contract.

Wuu's iOS and Android clients use native Personal screens over the public,
versioned extension client service. They do not run desktop plugin JavaScript or
read plugin storage. See [user-scoped hosts](user-hosts.md) for host authority and
[native setup](../../../clients/native/README.md) for build and device checks.

## Entry and identity

Projects retains Coding navigation and its existing default on Coding hosts.
A verified Personal host opens Personal. The selected area and authorized host
are remembered within the current login, so reopening does not require choosing
a workspace or creating a conversation. Initial host setup still uses the
existing authenticated account directory or iOS pairing flow.

Each named assistant binds to its persistent main conversation. Personal exposes
no thread picker, fork or new-thread action. The main assistant shows its name;
specialists also show their responsibility. First use can ensure the default
assistant idempotently. Reconnect, retry and notification opens reuse the same
agent. A missing bound conversation is an error, not a reason to create another.

Personal includes conversation, tasks, Today, shared memory, notification settings
and Calendar views. Today composes public tasks, routines and the current local
day's calendar window, recent results and pending Calendar/specialist decisions.
Agent settings expose name, communication style, specialist responsibility and
pause/resume. Specialist creation confirms an immutable proposal; an interrupted
creation retries the same proposal. Task edits, stop and resume use the displayed task
`revision` as `expected_revision`; execution history uses the separate
`requirements_revision`. A stopping or waiting state is not completion.

## Verified service routing

The live `initialize` response must declare `user_space_id: local` and
`product_domain: personal`. The client also requires an active, enabled Personal
inventory record and its exact fingerprint. Missing, disabled, ambiguous or
foreign-scope discovery fails closed. Plugin inventory notifications may update
fingerprints only using immutable scope retained from that live initialization.
Disconnect clears that scope; an inventory event alone cannot restore authority.

Calls use `plugin/client/request` with explicit Personal routing and the current
contribution fingerprint. Each adapter belongs to one authenticated login and
host. Connection and selection epochs reject late responses. Personal host
notifications and snapshots never enter Coding thread lists. Generic Coding
workspace and thread RPCs are not used on the Personal host.

## Sending, files and offline recovery

A prepared message is not accepted work. Before a send, the native client stores
the exact prompt, attachment bytes and stable business request ID in app-private,
no-backup retry storage scoped to login and host. An uncertain send survives
process restart. Reconnection never resends it automatically; retry is explicit
and uses the original business ID with a fresh transport request ID. Switching
agents is blocked until the user retries or explicitly discards that local retry.
Discarding does not cancel a request that may already have reached the host.
Unreadable retry storage blocks new sends until the user checks history and
explicitly discards the local retry.

Personal accepts up to 64,000 UTF-8 bytes of text, four image/PDF attachments and
3 MiB total file bytes per mobile message. An attachment-only message is valid.
Filenames must fit 255 UTF-8 bytes and contain no path separators or control
characters. The trusted `user_input` envelope contains the exact text and file
bytes; plugin input contains matching digest/name/media-type references.

Downloads use agent/message or task/requirements-version locators with kind,
index and SHA-256. They never accept an arbitrary global path or session. The
phone supports image/PDF previews up to 12 MiB raw bytes, verifies the complete
file digest and reads bounded base64 chunks. Larger files and unsupported formats
explain the limit rather than bypassing it. Ordinary native text controls can use
system keyboard dictation; Wuu does not provide a separate speech recognizer here.

Display history is a separate, bounded, purgeable cache keyed by login, host and
agent. Offline history is visibly read-only; attachments require reconnection.
The cache contains presentation fields and file metadata, not file bytes,
credentials or execution-session identifiers. Account changes, logout and device
revocation close pending writers and clear or prune affected cached history and
retry records. Cached data never grants remote access.

## Memory and Calendar decisions

Memory management requests active and inactive records, exposing conflicts,
future/expired facts, sources and effective times. Corrections use an explicit
user-edit provenance record and the displayed revision. Forgetting requires an
explicit confirmation; it removes shared recall, not original chats, files,
exports or backups. Forgotten records contain no former memory content.

Quiet hours and Personal notification preferences use settings revision checks.
They do not grant OS push permission or subscribe a device automatically.
Calendar agenda reads expose state, freshness and last successful fetch. Empty
or stale results are not proof of availability; checks cover the selected calendar.

Calendar writes require a separately discovered active Personal Calendar
contribution. A confirmation screen freezes the operation ID, revision and
preview digest, showing account, calendar, task version, original and proposed
fields, exact times/timezones, all attendees, notification mode and conflict-check
scope/time. Only an explicit confirmation calls `operations.confirm`. A transport
failure locks that attempted confirmation and offers a read-only status check.
Uncertain/running operations can be reconciled without another write. Completed
operations have provider read-back evidence; notification delivery remains a
separate, unverified claim. Authorization setup is not performed by this screen.

## Notification and URL locators

Notifications are hints, not synchronization or authorization. Personal notification
payloads contain these strings:

| Field | Meaning |
| --- | --- |
| `version` | `1` |
| `product_domain` | `personal` |
| `user_space_id` | `local` |
| `host` | Authorized host public key |
| `recipient` | Current phone's account public key |
| `agent_id` | Persistent named assistant |
| `task_id` | Optional task owned by that assistant |
| `message_seq` | Optional positive canonical decimal sequence in its main history |

The equivalent URL is `wuu://personal` with the same fields except `recipient` as
query parameters. URL opens require a current login and a fresh authorized host
directory. Notification opens additionally require current login-scoped push
consent and matching recipient. Duplicate query fields, unknown versions,
invalid IDs and session/workspace routing are rejected. Newer navigation
supersedes older pending opens. Wrong-owner tasks are rejected; missing messages
retain the original assistant and explain the failed location.

Legacy host-only Coding notifications retain their route. Malformed Personal
payloads never fall back to Coding. No notification or retry creates a new
conversation. APNs/FCM credentials, signed native builds and real-device delivery
must be validated separately from protocol tests.

## History search and notification grouping

The history entry searches the selected named agent by person names, subject or exact text, optionally within local calendar dates. Date bounds are converted to explicit instants and applied by the host before pagination. Selecting a hit opens the original main-message sequence. Editing filters discards stale cursors and late results; searching tools and hidden reasoning is intentionally outside this conversation view.

`agents.history` uses the same semantic visibility and human-input provenance for both the main agent and specialists. Internal identity instructions stay in the physical audit/provider context without becoming user bubbles. Genuine user text is retained even when it resembles those instructions. Visible tool-produced result attachments remain available as assistant attachments without exposing tool output; `tasks.history` remains an execution audit view. See [session history contracts](../customize/plugin-authoring.md#calling-host-services).

Recent and backward `agents.history` reads recover through at most four physical pages of up to 50 records each, plus one single-record snapshot lookup. Recovery stays within that snapshot. If the scanned pages contain only filtered records, an empty result can still have `has_earlier: true`; continue with `end_seq: first_seq - 1` to read earlier messages. Explicit `start_seq` or cursor reads retain their requested source address and do not automatically walk backward.

Memory details include contributor, updating agent, contribution revisions, executor provenance and source/effective/write times where available. A source reference is shown as evidence, not presented as a resolved link unless a matching message locator exists.

Notification settings also expose off/1/5/15-minute coalescing per responsible agent. Grouping changes notifications, not individual conversation history or message availability; it remains distinct from OS push permission and provider delivery.

## Desktop message references and task context

Desktop Personal can reply to an addressed conversation message or select **Reply
to this task** from task details. This opens a floating Replies view with the
original message or task context, related main-conversation messages, and an
independent reply composer. Closing it restores the main draft. The reply remains
part of the same persistent main conversation; this view does not create a new
execution session. A reference identifies a target, not authorization to perform
new work. The main assistant still interprets the user's request.

A message address is `{session_id, seq}`, not a transient rendered item ID.
`agents.send.relation` is carried separately from the exact user text and matches
the authenticated `user_input.relation` envelope. Personal task links live in
`relation.metadata.task_links`, with `task_id`, `requirements_revision`, optional
`execution_session_id`, and optional `question_request_id`. History and live
assistant output retain the relation. Task creation uses the host's current-turn
source message rather than a model-supplied sequence or the newest history row.

Reply targets must be visible messages in the main conversation. Task and execution
ownership, known requirement versions, and pending question identities are checked.
A stale question must be reselected rather than answered against another version.
Retrying the same input cannot silently change its target or apply a task revision
twice. Ordinary unreferenced chat remains ordinary chat. Detailed tools and worker
history stay in the execution view, not the main conversation.
