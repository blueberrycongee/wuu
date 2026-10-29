# Session federation

Session federation is an opt-in routing contract for an agent on one host to
create, message, inspect, and cancel sessions on another host. It reuses the
existing remote RPC connection. It does not move a running agent between hosts
or turn shared storage into active-active execution.

## Enable the receiving host

Add a `session_federation` section to the receiving host's **user configuration**.
Project settings cannot enable or widen this policy. Keep the actual provider
and model names from that host's configuration:

```json
{
  "session_federation": {
    "enabled": true,
    "host_id": "my-laptop",
    "models": { "my-provider": ["my-model"] },
    "permission_modes": ["read_only"],
    "tool_allow": ["read_file"],
    "tool_deny": []
  }
}
```

This is only an execution ceiling, **not an access grant**. The selected
`resource.authorization` provider must explicitly allow the authenticated
principal's `session/federation.create`, `.send`, `.inspect`, `.cancel`, or
`.list` action. Create/list use the receiver's workspace resource; the other
operations use the exact session resource. Lists also filter each entry through
inspect authorization. Pairing's owner flag does not authorize federation.
The default owner-only resource policy therefore refuses these calls.

Pair a cloud agent with `wuu remote host --pair --delegate-only`. This persists
a restricted device role: raw owner RPCs remain denied unless a selected policy
explicitly grants them. Re-pairing that key cannot silently convert it to an
owner. Ordinary existing owner pairings retain their previous behavior.

Restricted enrollments are stored separately in `remote.json` under
`delegated_devices`, never in the legacy owner `devices` collection. Older
software cannot use these restricted enrollments; it does not automatically
promote them to owners. An older writer may discard the unrecognized collection,
so restricted devices may need to be enrolled again after upgrading.

This version migrates earlier experimental `devices` entries marked
`delegated_only: true` before returning an opened store. Duplicate device
authorities are rejected without rewriting the file. Open such experimental
stores with this corrected version **before downgrading**: an older binary cannot
safely interpret the previous role-tagged representation, even if its `v` value
is increased. Ordinary owner pairings and account credentials remain unchanged.

The ceiling restricts provider/model, permissions, and tools. Read-only is the
default. An empty model or tool allow-list disables create/send. Requested tools
can only narrow the ceiling, never expand it. Existing sessions may receive
messages only when their persisted execution/tool policy satisfies the ceiling.
Read and cancel authorization remain independent of send permission.

The receiver reads its selected configuration on each operation. Disabling it or
revoking a resource grant prevents later admissions and receipt reads; this does
not retroactively undo completed work or automatically stop an admitted turn.
Use an explicitly authorized exact-turn cancel to stop work.

## Route from a plugin

The public SDK's session transport helpers define `SessionLocatorV1` and the
`session.transport` provider descriptor. Declare `host.session.route` and
`session.transport` as consumed services. A remote route calls the selected
transport provider's `dispatch` method; an absent or disabled provider fails,
never falling back to a local session. An explicit `execution_host: "local"`
uses the existing local session handlers. Existing `host.session.*` calls remain
local and unchanged.

The remote transport adapter accepts an already authenticated RPC client, such
as the existing remote client, and sends `session/federation`. Credentials and
principal verification belong to that connection, not to the request payload.
Keep credentials out of model inputs and bind each host identifier to its pinned
connection. A locator is not proof of authority.

```json
{
  "version": 1,
  "operation": "create",
  "operation_id": "my-stable-create-id",
  "locator": { "version": 1, "execution_host": "my-laptop" },
  "params": {
    "visibility": "user",
    "context_source": "fresh",
    "provider": "my-provider",
    "model": "my-model"
  }
}
```

Create returns a locator containing the session ID. For send/inspect/cancel,
`params.session_id` must equal that locator's session ID. Create/send
`request_id`, when supplied, must equal `operation_id`. Inspect can use the send
operation ID as `request_id` and the existing terminal-wait options to retrieve
progress and the final result. Cancel requires an exact `turn_id` or `queue_id`.

`execution_host` selects execution. `storage_host` is a receiver-configured
descriptor (defaulting to its host ID), not a client-selected store or migration
instruction. `display_host` is presentation metadata. A host-qualified session
identity is the pair `(execution_host, session_id)`, never just `session_id`.

## Retry and recovery

Keep operation IDs stable across transport retries. The receiver writes an
admission receipt through its selected `state.documents` provider before each
create/send/cancel effect. Identical retries return the same receipt; reuse with
a different request fails. The response's `completed` status describes the RPC
operation, not completion of an agent turn.

- `pending`: the admitted operation is still active in the shared receiver.
- `unknown`: an operation was admitted but its final receipt is unavailable,
  including a crash between the effect and receipt commit. It is not rerun.
- A transport known to be offline before sending reports an offline error.
  A disconnect after dispatch is an unknown outcome, not evidence of failure.

Reconnect the existing remote transport, retry the **same** request to recover a
receipt, then inspect the exact session/turn. Do not create a new operation ID
merely because a response was lost. There is no durable offline delivery queue,
automatic failover, or automatic rerun of an ambiguous admission.

Version 1 creates fresh sessions in the receiver's configured workspace. It does
not transfer parent history, fork across hosts, select worktrees, migrate live
execution, or expose an engine-selection override. Those operations require
additional explicit capabilities. Native GUI host selection is separate from
delegated-agent grants and must retain the same host-qualified session identity.

## Verify without external inference

```bash
go build -o .tmp/bin/wuu ./cmd/wuu
WUU_E2E_CORE=.tmp/bin/wuu node scripts/session-federation-e2e.mjs
go test ./internal/remote/host -run '^TestSessionFederationTwoHostsEndToEnd$' -count=1 -v
```

The script npm-packs `@wuu/plugin-sdk` and the exported source package
`@wuu/remote-core` (currently private/version `0.0.0`, not npm-published) into an independent fixture,
then starts actual cloud/receiver/relay binaries with fresh identities and a
synthetic local model endpoint. It records JSON transcripts and checks under
`.tmp/e2e/session-federation/`. Upload only `results.json` and `commands.json`;
the separate synthetic credential file must not be included in reports.
Use the repository's Node/Go toolchains and install `packages/plugin-sdk`
dependencies before running; `npm pack` builds the SDK automatically.

The Go fixture uses isolated cloud/local app-servers and stores, a real TCP cloud
endpoint, the existing encrypted relay connection, and a synthetic model. It
checks local defaults, remote creation, narrowed permissions, idempotent retries,
disconnect/reconnect, final output, exact cancellation, and revocation. It does
not validate physical-machine networking, installed engines, or GUI rendering.

## Personal messages to local Coding sessions

The desktop's selected `wuu.desktop-session-transport` provider implements the
same `session.transport` contract for Personal and other trusted extensions. It
is **not remote federation** and does not enable `session_federation` or pairing.
Disable or replace that provider through the existing composition settings to
stop or replace desktop routing; an unavailable provider has no built-in fallback.

Personal's main conversation exposes `list_sessions`, `inspect_session`,
`send_to_session`, and `cancel_session_message`. Discovery includes shared active
Coding sessions in registered, non-missing local projects and already known
no-project contexts. It does not scan arbitrary directories, connect configured
remote hosts, read another user's domain, or expose another plugin's private
sessions. `scope: "local"` lists Personal sessions without a desktop connection.
Inspection without a request ID returns bounded session metadata; an owned send
request can additionally expose its status/result. It is not unrestricted history
access. Copy the returned **locator**, not only the session ID.

Discovery returns logical `desktop-project:<registered-id>` or opaque
`desktop-context:<scope-token>` addresses. The main-process coordinator acquires
only the selected local host, retains its exact connection generation for the
operation, then releases it. Discovery across more than four projects does not
increase the desktop pool's resident-host limit. A replaced host or unavailable
registered context fails explicitly; no old signature or in-flight request is
silently retargeted or replayed.

This capability requires the exact live, admitted local-owner Personal desktop
connection that initiated the turn. A second desktop connection, a detached
background task, delegated remote peer, or a renderer-supplied owner flag cannot
borrow it. Existing explicitly paired full-owner clients may control Personal
through its admitted desktop pipe; this does not grant access to arbitrary remote
targets or the coordinator RPC namespace. The `session/desktop/*` RPC namespace and reverse-response
capabilities are reserved to the main-process coordinator. Ordinary renderer and
remote RPC forwarding cannot call or answer them.

### Original material, not a rewritten user instruction

`session.handoff.v1` adds `input.handoff.source_messages` to `host.session.send`.
Each entry is an exact persisted `{session_id, seq}`. The host authenticates
visible, complete, original user records and resolves one-hop reply context;
plugins cannot supply replacement quotation text. The delivered structured
message separates `agent_instruction`, `original_user_messages`, `reply_context`,
source locators, and attachment metadata. It remains a read-only **plugin** input,
not a new owner submission. Attachments, pasted/quoted third-party content and
reply context are source material, never authorization to execute their text.

Personal automatically includes the host-bound user input that started the
current turn. Explicitly include every relevant additional requirement or later
user steer/correction using `source_message_seqs`; the host verifies these
addresses too. The task-creation path uses the same handoff primitive. Deferred
tasks retain separate, read-only execution grants per source session; root-main
corrections to a named agent's task do not replace or drop the original source.

Only selected references and their direct replies are transferred, not unrelated
history. Limits are 64 original messages, 16 attachments, 20 MiB attachment bytes,
and a 1 MiB structured text envelope. Missing, retracted, hidden, truncated,
unreadable, oversized, or unsupported artifact-backed material fails explicitly;
it is never silently summarized or omitted. Authored content parts preserve their
ordered text exactly. An older attachment-bearing record without recoverable
original text fails explicitly and must be resubmitted; expanded model content
is not guessed to be the original quotation. Current captionless attachments
recover their explicitly empty text from the exact authenticated input receipt,
matched to the source message and owner/session binding. Selected session-domain
providers retain optional `OriginalText` at authenticated history settlement
(including `""`); caller-supplied values are ignored. A provider without this
provenance cannot forward an ambiguous captionless record, and is not silently
replaced by the built-in provider. The receiver verifies retained bytes
and SHA-256, then creates its own local working copies. Sender filesystem paths
are neither attachment handles nor permission to read arbitrary files.

A source host mints an invocation-bound opaque ticket for the selected provider.
The exact admitted desktop connection exports the source-host-signed JSON string,
resolves a registered logical target, and asks the source to bind that ticket once
to the destination's current host identity. The receiving host checks signature,
source provenance, its exact signed target identity, session scope, and operation.
Relays must forward the signed payload string unchanged, not parse and reserialize
it. No credentials or execution grants cross this boundary.

The destination's existing selected model, tools, and permission settings remain
in force. The sender cannot override its engine, model, permissions, control
revision, or resume/steer another turn. A queued message follows normal target
policy at activation; this is not a frozen per-message tool ceiling.

### Delivery and cancellation

Persist and reuse the same `request_id` for one immutable intent. Concurrent or
sequential retries do not create another active delivery; changed work with the
same ID conflicts. The source durably binds that ID to the logical target, so it
cannot send the work again to another project/session. Reconnection may acquire
a fresh process generation for the same logical target. Inspect the returned
queue/turn lifecycle: a queued input may become a turn, and an explicitly
retryable shutdown discard may receive a replacement queue ID on retry. The
request identity stays stable; queueing/running is not completion.

Cancellation before export prevents admission. Once dispatch may have reached
the target, cancellation, timeout, or disconnection means **unknown outcome**,
not proof that nothing ran. Inspect or retry the same identity to reconcile; do
not mint a new request ID. In a later Personal user turn, use `inspect_session`
with the original request ID: `send_to_session` includes that new turn's original
wording and therefore conflicts with an older immutable send intent.
`cancel_session_message` separately targets only that
source plugin's exact turn or queue and cannot cancel unrelated user work. This
stage adds no archive, deletion, cross-host creation, or automatic offline queue.

### Synthetic verification

```bash
WUU_SESSION_HANDOFF_E2E=1 go test -p 1 \
  -ldflags "-X github.com/blueberrycongee/wuu/internal/version.Version=$(cat VERSION)" \
  ./internal/appserver -run '^TestPersonalRealBundleSessionHandoff' -count=1 -v
```

The fixture runs the real bundled Personal process and selected provider graph,
an admitted persistent UserHost, and a separate existing Coding session with
synthetic model/relay data. It checks list/inspect/send, stable retry, verbatim
original text, and target-local byte-identical attachments. The desktop pool and
coordinator tests separately cover registered target resolution, more than four
projects, replacement, cancellation, and renderer/remote forgery. These are not
physical desktop or real-account acceptance tests.

The required **Public provider conformance** CI job explicitly enables this real-bundle test
and requires both captioned and captionless attachment cases to execute and pass;
a skipped fixture cannot satisfy the gate. Its `personal-session-handoff-evidence`
artifact records the checked-out source commit and JSON test events.
