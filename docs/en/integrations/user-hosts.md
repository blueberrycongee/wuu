# Isolated user hosts

A user host binds one user space and product domain for its entire lifetime. Personal agents use this host rather than a project workspace. Existing Coding hosts keep their current configuration, tools and connection lifecycle.

## Activation and declared isolation

The default `logical_memory` user-host mode separates Personal and Coding memory APIs, injected context, history, indexes and plugin storage by the host-bound product domain. It does not promise operating-system or Shell confidentiality between processes running under the same OS identity. Valid user-space/domain binding remains required; a client cannot select a foreign memory domain.

Internal SDK embeddings can explicitly select `UserHostIsolationMode: "verified_host"`. That stronger mode requires a trusted `UserHostIsolationVerifier`; missing or failed verification returns `user_host_isolation_required` before private state is created. Unknown modes are rejected. A separate directory or caller assertion is not an attestation, and the logical mode never labels the host verified.

`initialize` returns `user_host_isolation: { memory: "logical", host: "unverified" }` for ordinary user hosts. A successfully verified embedding reports `host: "verified"`. These additive capabilities are separate from the immutable `user_space_id` and `product_domain`. Ordinary Coding initialization and permissions are unchanged.

The `app-server` and `remote host` commands accept `--user-space` and `--product-domain` with the default logical-memory policy. A user host cannot inherit a workspace identity or borrow the Coding desktop execution bridge. Model access, connector grants, remote pairing, notification credentials and deployment remain separate setup responsibilities.

## Selected composition and desktop routing

Choose the shipped Personal composition explicitly with `--composition-profile personal`, or set `CompositionProfile: "personal"` in SDK options. The user-space binding does not silently select a profile. Explicit service bindings and exclusions remain authoritative; removing a provider does not enable a native fallback.

Desktop runtime calls may supply `userSpaceId`, `productDomain`, and `compositionProfile`. The source UI retains its original `graph_generation`. Before dispatch, the main process verifies that source identity, initializes the selected target host, and matches the exact plugin ID and fingerprint against its actual inventory. Calls and cancellation retain the target graph and connection captured by that handshake. A different target, failed binding, or removed provider is an error, not permission to replay the call elsewhere. The target independently checks its runtime state, declared service, and resource authorization. UI registration belongs to the source graph; the runtime-only target does not receive synthetic desktop activation reports.

## Input and context receipts

`wuu.client-input-receipts` is an optional host observer selected through the ordinary composition module list or patches. The shipped Personal bundle selects it; a bound Coding host can select it too. A user-space/product-domain binding alone does not enable it. Custom explicit module lists must include it when durable authenticated input receipts are required.

The observer uses the selected `session.domain` provider. Disabling it stops new receipt admission, recovery and history projection; existing persisted records are not deleted. Already admitted work retains its captured composition for settlement. Receipt access still requires the exact host user-space/domain, session ownership and normal resource authorization.

`accepted` and `in_history` do not mean read. The host records `read_at` only when an admitted input's exact client ID and history sequence occur in the assembled agent model context, before downstream model adapters. This is context inclusion, not proof that a model understood the input or a person viewed it. Normal UI and session APIs cannot assert this observation with plugin responses, UI events or supplied receipt fields. Receipt identity, revision and source information remain host-owned within those APIs. Installed extensions and replacement state providers remain trusted code; these receipts are not tamper-proof attestations against a trusted provider.

## Identity and storage

For local desktop development, run `make dev-personal` (an alias for `make dev`).
Personal uses the same default logical-memory policy as ordinary user hosts;
no `personal_dev` build tag or isolation-bypass flag is needed. To use existing
data, set an absolute `WUU_HOME` as described in the [development guide](../project/development.md).

The binding is supplied by the host and returned by `initialize` as `user_space_id` and `product_domain`. Clients validate it before calling Personal services. Extension inventory changes do not change this binding.

With the bundled local storage providers, user data is separated as follows. These paths describe that implementation, not an authority requirement for replacement providers:

- `user-spaces/<user-space>/<domain>/state`: sessions, private plugin storage, memory and managed artifacts
- `user-spaces/<user-space>/<domain>/files`: permitted user working files and explicit attachment working copies

User hosts load an explicit absolute configuration or the user configuration. They do not inherit project instructions, global Coding skills, MCP servers, hooks or external execution environments. Protected private-state paths are excluded from Personal file and process tools. Installed extensions remain trusted code; this is not an untrusted-extension sandbox.

twin exposes one persistent main conversation per Personal user space. Its main Session ID is reused across navigation, reconnect and restart; it is not rotated or rebuilt by joining other sessions. Hidden task executions retain separate Session IDs and requirements versions. Missing main sessions report recovery errors instead of silently creating replacements. Historical identities and task data are retained, while current questions and results return to the main conversation.

## Persistent execution and client lifecycle

The internal SDK creates one `UserHost` execution authority per user runtime. It binds session routing before any client connects. Multiple authenticated transports attach to that authority; closing a client connection or sending protocol `shutdown` only detaches that client. Already admitted work can finish while no client is connected.

Connection-local RPC IDs and cancellation are isolated from other clients. Business request IDs remain unchanged so reconnect retries can recover the same durable receipt. EOF revokes live foreground submission authority, including contexts detached by a callback. It does not undo an already admitted turn.

A slow consumer has a bounded queue and is disconnected without blocking other clients or background completion. The host allows at most 16 simultaneous client transports. The application owner must cancel or close its transports and wait for their `Serve` calls to return before calling SDK `Runtime.Close`. Closing the authority ends its background execution; it is distinct from closing a window.

User-host transports expose `initialize`, `plugin/client/request`, `plugin/client/cancel`, user-question decision methods, device-bound push registration and connection shutdown. Generic Coding thread, workspace, storage, configuration and package-mutation APIs are unavailable on this transport. Desktop extension management retains its normal host context. The Coding account-history exporter detects user-host scope and stops; it does not export Personal data or interpret its inventory as Coding deletions.

Non-interactive remote hosts do not provide generic `ask_user` interaction. A plugin-specific confirmation contract, such as Calendar's immutable operation preview, is a separate client operation.

The persisted owner admits user attention through `session.attention` under its current notification and quiet-hours policy. Raw terminal events do not authorize Personal push. `Runtime.SubscribeUserAttention` is a host-lifetime observer independent of phone attachment. Its bounded, content-free startup buffer rechecks current ownership, generation and policy before replay; it is best-effort notification transport, not a durable provider-delivery receipt. Closing or disabling the owner suppresses late attention.

Notification grouping defaults to off. A configured window (up to 15 minutes) coalesces pending attention separately for each responsible agent, using the first admitted item's fixed deadline. Conversation results remain individual and immediately readable. The host retains at most 128 in-memory batches and a latest-message locator; shutdown can lose pending best-effort notifications. It rechecks the latest item's current policy at flush and relay replay, and conservatively suppresses the batch if that locator is no longer authorized. Phone providers receive one generic agent notification, not a precise per-item digest. Changing the setting to zero affects new admitted attention; already pending windows otherwise keep their original deadline.

Personal history search uses the public conversation projection, with optional inclusive `time_min` and exclusive `time_max` RFC3339 bounds applied before pagination. It searches visible user/assistant text, not hidden reasoning or execution-tool records. Returned sequence addresses navigate to the original main-conversation message; missing legacy timestamps remain unknown. Default Coding trace search is unchanged.

### Bounded unattended authorization

Unattended work uses the selected `execution.authorization@1` provider, not a
saved owner identity or trusted-plugin status. Authenticated admission creates a
finite, revocable grant bound to the selected task authority, current requirements,
actions, resources, model and permission bounds. Exact runs narrow that authority
to their persisted request and session. Personal saves opaque references and
expiry times; it must obtain new authorization when the grant is no longer valid.
A missing or refusing selected provider does not activate a fallback.

The bundled native provider supports **local-installation authority only**, issued
from a trusted local embedding. A paired phone or remote owner connection does
not, by itself, establish durable account validity and cannot issue native
unattended grants. An external provider may support authenticated accounts using
the host-attested issuer, tenant and subject, but must own their durable validity
and revocation checks. Account disablement, logout and device revocation have the
semantics that provider actually implements; the host does not infer one from
another. Closing a GUI connection is distinct from revoking an admitted grant.

Each effect requires fresh selected grant and resource-policy validation after
review and before its durable dispatch claim. Revocation prevents new admissions;
it does not undo an effect already admitted. A saved positive check cannot be
reused after a pending decision, retry or restart. Unknown validation outcomes
fail closed without dispatch.

## Foreground input and attachments

A client submits exact user input in the host-owned `user_input` envelope. Plugin method input contains attachment references only; it cannot supply a file path or substitute bytes. Admission binds the plugin, literal prompt, business request ID, ordered filenames, media types and SHA-256 digests. It can authorize only the bound send. Background callbacks cannot synthesize user authority.

The host accepts up to 16 attachments and 20 MiB total decoded bytes per submission. Personal text is limited to 64,000 UTF-8 bytes. Native clients retain their smaller transport limits. Empty text is valid only with a selected attachment.

History exposes attachment metadata, not local paths or inline file bytes. `host.session.attachment.read` resolves an owned persisted record and returns bounded base64 chunks, with offsets measured in base64 characters. Personal exposes agent- and task-scoped adapters. Clients verify returned metadata and the final digest before showing a preview or download. Native and desktop rendering do not automatically load model-supplied remote images or arbitrary local URLs.

## Desktop surface

twin appears directly below Automations in Wuu's existing navigation. Opening it continues the main conversation. Its right panel lists ongoing work, recent activity and recent outputs continuously, without category tabs. Opening details changes that panel, never the conversation. Routine controls use the existing Automation binding; changing a schedule updates that same routine and binding. Personal notification and quiet-hour preferences live in Wuu's shared settings.

## Message reactions

In the Personal main conversation, the owner can react to a saved assistant
message, and the main assistant can react to a saved original user message.
Each actor has one current Unicode emoji per message; a new emoji replaces it,
and removal clears it. Hidden, truncated, read-only, background, foreign-space,
and unpersisted stream messages are not reaction targets. Coding is unchanged.

Reactions are plugin-owned metadata keyed by canonical session ID and message
sequence, not new conversation messages. They survive reconnect and restart and
are returned with history. Clients use `agents.react` with `session_id`,
`message_seq`, `emoji` (or null), `expected_revision`, and a bounded `request_id`.
This mutation is accepted only through the authenticated client capability, not
the plugin service. The main agent uses `react_to_message`; the host-bound caller
session fixes the actor and target conversation. Background executors cannot use
it. Stale revisions fail, and retries of a retained request receipt return current
state without restoring old feedback. Each message retains 32 request receipts;
older stale requests still fail revision checking.

`agents.reactions` refreshes at most 100 message sequences per request. History
also exposes `reaction_eligible` and `reactions: { owner?, agent?, revision }`.
A successful write emits a content-free `reactions` invalidation through the
host's generation-scoped client invalidation service. Clients reread durable
state and ignore older revisions; reconnect and periodic refresh recover lost
notifications. Invalidation failure does not roll back the saved reaction.

Owner feedback does not start or resume a turn, dispatch a task, answer an
approval, send a push, or require a text reply. On the next natural main-agent
step, the plugin supplies a bounded snapshot of up to 24 recently changed target
messages, each with its latest reaction or removal and a short source excerpt.
Repeated changes are coalesced into current state. A durable bounded locator is
reserved before the authoritative message mutation, so a restart cannot lose a
successful mutation's locator; an unfinished locator never fabricates feedback.
The snapshot supersedes earlier reaction context. Reactions are expression, not
consent, new instructions, or an automatically saved preference. An idle agent
does not immediately run merely to observe an emoji.

Main-agent guidance permits an emoji-only acknowledgment, thanks, or response to
shared progress when no substantive answer is needed; it does not require a
reaction to every message. Questions, requests for help, explanations, and
meaningful action/status updates still need substantive responses. An emoji-only
acknowledgment should not be followed by a redundant “Got it.” Reactions must not
imply that unperformed or unverified work succeeded. These are model instructions,
not a keyword classifier or a guarantee of model behavior.

## Business contracts and external services

The main assistant and hidden task executions share the Personal Memory service with attribution and versioned corrections. Memory can be queried, corrected or forgotten in conversation; there is no separate desktop memory-management page. Forgetting redacts the memory record and prevents its future synthesis use; it does not claim to erase source conversations, user files or backups. Coding memory remains separate.

Task completion verifies persisted successful execution evidence. Main-conversation delivery is tracked separately as pending, submitted, delivered or failed; a completed task does not by itself mean its main-conversation reply has appeared. These checks verify attribution and state transitions, not the substantive correctness of an external result. Task stop persists targeted cancellation intent, including descendant work and queued results; it does not stop sibling tasks. Stopping the current main reply requires its explicit `turn_id`; delayed requests and retries cannot target a newer reply. Already accepted genuine user inputs remain queued. Recovery may remain pending while the execution host is unavailable. Waiting-user questions have their own durable delivery, and an answer updates the original task rather than opening an execution chat.

An in-flight dispatch with no receipt remains pending cancellation, not confirmed stopped. Recovery inspects the original request and targets its current queue or running-turn identity; restoring a task record does not resurrect its old process. A task waiting for the user's answer cannot be completed by a concurrent result submission. Stopping a completed or failed task cancels only its undelivered reply and preserves the saved outcome; an already delivered result is unchanged.

Task-generated questions, results and failure replies run with tools disabled. Explicit delivery retry first checks the original durable receipt: an already completed reply is recovered, and an unknown outcome blocks another attempt. It only retries the main-conversation reply, not the worker. Automatic dispatch recovery retains the original request identity; explicit task resume creates a new requirements version and execution attempt. These guarantees do not establish exactly-once external side effects or authorize repeating an irreversible action.

### Personal file-action review

The first production adapter covers native **local `write_file` and `edit_file`** inside the bound Personal files root. Reads and ordinary questions do not produce approval cards. The main conversation shows the exact target, content/edit arguments, existing-file SHA-256, consequences and action identity. Approval/rejection is accepted only from the live owning desktop connection, bound to the immutable action digest and current revision. An unchanged repeated click returns the existing result; it does not dispatch twice. Symlinks, multiple-link/nonregular files, changed targets, stale requirements, stopped tasks/agents/routines, disabled Personal, safe mode and protected application/Coding state fail closed.

An active worker waits at its original native tool invocation. Approval returns that invocation's real result and tool-call evidence. After restart, the durable action ledger resumes only the same active task/version/session with a deduplicated receipt request. Completion may cite `action_id`; the host verifies the succeeded native receipt and exact task version rather than trusting the continuation message. Rejection/revocation/unknown outcomes do not launch a tool-enabled worker. A durable execution claim with no certain receipt is shown as uncertain and never automatically replayed. Revocation prevents an unstarted action; it does not undo an already completed file change.

AutoReview is **off by default** and explicitly enabled only in Personal settings. Its scope is these two operations, not an account grant or reusable blanket consent. It reuses the native policy reviewer with the retained original human request, exact frozen action and current task context; missing source evidence, reviewer failure or uncertainty falls back to the owner card. Hard denies still apply. Settings changes have a durable actor/time/revision audit and do not modify Coding's reviewer or permission settings.

Coverage exclusions: shell commands, `apply_patch`, MCP/plugin write tools, remote execution, account/OAuth grants and external send/calendar writes are not routed through this adapter. Selected authorization providers are supported only when they implement the fenced `acquire-authorization` / `release-authorization` contract; the same captured provider remains retained through the local effect and cleanup. File-change or permission-request callbacks without a captured local-only guarantee remain unsupported, including callbacks retained by an older toolkit generation. The adapter fails closed rather than skipping security checks or calling arbitrary extension hooks under the file commit lock. The UI preview uses synthetic state and is not evidence of native execution or account connectivity.

The bundled Calendar adapter starts unconnected. The read and write interfaces accept a trusted, user-space/account-bound provider for an already authorized grant; tool input and plugin configuration cannot supply credentials. The shipped default does not wire an authorization provider or writer. An embedding must supply that integration after separate account authorization; source adapter tests do not complete this setup. Its write contract requires a full immutable preview, explicit client confirmation, exact revision and digest, and read-back verification. A lost acknowledgment produces an uncertain outcome that is reconciled without blindly replaying a write. Provider-injected tests do not establish a real OAuth or account outcome.

See the [app-server protocol](app-server-protocol.md), [plugin authoring](../customize/plugin-authoring.md), and [native client setup](../../../clients/native/README.md) for the underlying integration surfaces.

Calendar proposals are reachable from the main conversation without replacing its Session. The review overlay retains the full immutable preview and exact revision/digest confirmation. Personal exposes only a read-only operations-list adapter; confirmation and cancellation remain trusted Calendar client operations. With no authorized provider, the UI reports the disconnected/read-only state and cannot confirm a write.

#### Migrating the legacy review ledger

An existing `personal-review.sqlite` is never silently replaced by an empty
selected ledger. If startup reports `personal_review_legacy_migration_required`,
stop **all desktop, local and remote Personal hosts for that user space**, retain
its backup, and run the local maintenance command with the same `WUU_HOME`, config
and composition-profile override used by that host:

```bash
wuu state migrate-personal-review --user-space local --composition-profile personal
# Inspect the selected provider, source_digest, row counts and marker status.
wuu state migrate-personal-review --user-space local --composition-profile personal \
  --apply --expect-source-digest DIGEST_FROM_INSPECTION
```

Use `--config /absolute/path/config.json` on both calls if the host uses an explicit
config file. Omit `--composition-profile` only when the host also relies on its
configured profile. Inspection is the default (`--dry-run` is equivalent): it
imports no review rows and does not create a migration marker. Both operations
require a verified local owner, current selected resource policy and exclusive
stopped-host presence. Shared or multi-host storage is rejected; use the selected
provider's offline maintenance procedure with exclusive-writer proof instead.

Apply checks the expected digest against the same frozen logical source snapshot
it imports, including committed SQLite WAL data. It copies settings, actions and
audit into the **empty selected provider**, committing the path-bound marker in
the same transaction. It never deletes or updates the source, overwrites selected
records, replays uncertain actions or grants execution permission. The JSON result
reports `migration-required`, `migrated` or `already-migrated`. If transport or
cleanup fails, preserve the source and inspect again before retrying: a matching
committed marker resolves an unknown import outcome without repeating the import.
A changed source or populated incompatible destination requires explicit operator
reconciliation, not a forced overwrite.

#### Replacement-provider maintenance boundary

Ordinary plugin calls cannot mint or forward host maintenance attestations. The
native session provider can validate an admitted maintenance request and bind a
new attestation to its exact selected document-store call. An external session
provider can implement maintenance in its own authority, but forwarding this
admission through `service.call` to a separate external document provider is
**not currently supported** and fails closed. Full chained-provider maintenance
requires a future host-mediated, dependency-bound attenuation contract; selecting
both providers does not grant an administrative bypass.

Imported child compositions also have a direct grant-policy routing limitation:
the child bridge's provider ID is not the original grant provider ID. Direct
child-policy calls therefore fail closed. Product session and effect handlers
retain the parent authority; this does not establish support for every direct
child-graph invocation. See [composition boundaries](../customize/plugin-compositions.md).

### Scoped Personal connectors

The Personal local host now exposes Gmail, Notion, Drive, GitHub, Google Calendar,
and isolated Slack/Feishu adapters through `host.personal`. They are not Coding
or MCP tools. Connection metadata, allowed resources, read/write scopes and
account identity are separate; an installed adapter is not a connected account.
The older Calendar agenda/cache integration remains available. Calendar writes
use the connector action path rather than upgrading a read-only grant silently.

Credentials remain in the OS secure store with no plaintext fallback. A trusted
administrator can run `wuu connector-client client-import --user-space-id SPACE`
and supply an already registered OAuth client's JSON on stdin. The configuration
contains `provider`, `client_id`, optional `client_secret`, and a registered
`redirect_uri` where required. Never paste this JSON into a chat. Importing client
configuration does not authorize an account. Start, inspect and cancel account
consent in Personal settings; the host opens the provider URL without returning
it to model tools. Provider app registration, required scope verification and
real account acceptance remain separate setup steps. HTTPS-only callback
providers require a trusted configured callback transport; a desktop HTTP
loopback is not an interchangeable redirect. `connector-client import` is an
explicit host-only fallback for an already granted token, with provider identity
verification; it is not automatic OAuth setup.

Gmail supports thread search/read, attachments, draft creation and sending.
Notion search/read/query and report append are limited to selected resources.
Drive `drive.file` access means selected/shared files, not unrestricted Drive
search; Docs export is text and PDF is a managed attachment, not claimed PDF text
extraction. GitHub tracking is read-only PRs, issues, check runs and releases in
selected repositories; app/OAuth permissions must not be confused with these
local operation limits. Calendar create/reschedule/cancel requires explicit
notification behavior; editing existing events requires their current ETag.
Slack and Feishu search/mentions filter a bounded page from selected channels,
not an organization-wide index. Continue provider cursors for later pages.

External writes freeze their exact arguments, account, resource, connection
revision and host-derived execution authority. The original tool waits for an
owner decision; only a live owner control can approve the stored digest. New
human requirements, cancelled tasks, revoked connections and disabled Personal
invalidate stale authority. Saved receipts survive restarts. Unknown outcomes
are not replayed, including by changing the request ID. Read-only inspection is
separate from retrying a mutation; an acknowledgement is not proof of email
recipient delivery. Reconciliation or a new deliberate action may still require
an owner. Ordinary connector reads do not need approval. Work data is isolated
from default Personal memory; owners may explicitly allow reads of selected work
resources, otherwise individual cross-domain requests require confirmation.

Large output is paged with a content hash. Binary attachments use the existing
execution-bound artifact importer instead of putting base64 into model context.
Provider text is untrusted content and never supplies user authorization.

Explicit share/import feeds the existing main-chat submission and receipt path.
The browser extension requests only a user-activated current tab, never browser
history. Imported URLs are not automatically fetched. Android shares enter a
Personal draft; iOS shares use a separately enabled, short-lived App Group inbox
and still require the main chat's send action. Native compilation, signing,
entitlements and real-device verification are independent acceptance stages.

`personal_connector_poll` compares one explicitly chosen, bounded read query.
The first result establishes a silent baseline; subsequent changes receive a
persisted sequence/change identity, and an unchanged result stays silent after
restart. Reconnecting starts a new baseline. Checkpoints belong to the verified execution session: a newly created routine
execution starts its own baseline rather than sharing another session’s history.
It does not itself start a background loop, install webhooks or
consume a full-mailbox/provider history feed. A limited first page cannot prove
that every upstream change was observed.
