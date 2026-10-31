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

## Personal tasks in an existing session

The host grant gateway advertises `host_features: ["session-route.v1"]` from `executionGrants(host).describe()`. An absent feature means this routing operation is unavailable on an older host; ordinary Personal tasks remain supported and no replacement worker is created.

Personal's `list_sessions` tool asks the selected transport for available registered
sessions and unavailable hosts. Discovery is not a control grant. Select the exact
host-qualified session when names are ambiguous. `create_task` accepts an
`existing_session` locator; `resume_task` and `update_task` retain that target and
start a new queued request. They do not steer an unrelated active turn, fork the
session, or create a replacement when its host disappears.

The source grant remains anchored to Personal's own controller conversation.
Its selected task authority reports the existing locator and permitted operations;
the host binds those facts into the grant digest and exact `session-route`
resource. `executionGrants(host).route(reference, request)` permits only that
run's send, inspect, and exact cancel. A locator or prompt claiming approval is
not authority. The receiver separately authenticates the caller and applies its
resource policy and execution ceiling. Neither session ownership nor parentage
changes. Provider/model configuration is not part of this operation.

The desktop Personal profile selects `wuu.desktop-session-transport`. It uses
already-registered live receiver connections and their advertised nonsecret
transport identity, never a newly spawned replacement or a cross-runtime
`local` alias. Discovery is filtered by receiver resource policy. The bridge
carries a live source-validation proof, never a caller-asserted owner privilege.
The federation path remains nonowner; owner-local checks the genuine receiver
connection instead. Unsupported, ambiguous, or unconfigured receivers
remain unavailable. The adapter covers registered desktop local-core connections; it does not
silently upgrade an undelegated remote connection.

The registered catalog distinguishes two explicit paths with optional
`route_kind` metadata (omission means legacy `federation`):

- `owner-local`: an already-registered local Coding runtime, reached through its
  genuine trusted desktop owner connection. This path does not require federation
  configuration or a second receiver-grant store. The source task still needs its
  exact existing-target authorization; the receiver uses a fixed nonhuman actor,
  preserves ownership/parentage, and rechecks live source validity at execution.
- `federation`: the separately advertised federation identity and receiver resource
  policy. Registration alone does not grant delegated federation access.

Each path has a distinct stable opaque host identity. Copy the catalog locator;
do not add `route_kind` to a dispatch request or reinterpret an old federation
locator as owner-local. A denial, timeout, or unavailable connection never triggers
fallback to the other path. Ambiguous names require target selection, not guessing.
Personal does not enable federation, acquire credentials, or forward an owner bit
from a caller-supplied envelope. Remote and untrusted connections do not acquire
the owner-local path.

Personal stores the target and request identity with its existing task record.
A lost response is reconciled by inspecting that request, not by creating another
worker. Cancellation targets only the matching queue/turn, not the entire session.
A wait timeout or detached display alone does not cancel work. Closing the trusted source desktop pipe or process is different: its receiver peer is revoked, while durable task/request records remain for honest reconciliation. The transport does not silently rebind the run to another desktop connection. Worker terminal
output returns to Personal as a **worker response**, not independent verification
of its claims. The matter remains awaiting review, including when the response is a question; detailed history stays in the original session. Late results from
stopped or superseded runs do not revive the task.


The main controller can use `accept_task_result` after reviewing the exact worker
request and current task revision. It records its assessment and evidence without
rewriting the raw response or granting further execution permission. Acceptance
is a controller decision, not independent verification. Feedback notices remain
tools-disabled; delivery of a notice is not an automatic orchestration turn.
An expired or revoked source grant reports that authorization is required; it is
not silently replaced by owner authority. The original target UI remains the
place to stop work when no valid delegated cleanup grant is available.

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

Existing-target delegation is text-only. The controller goal is labeled separately from the authenticated original user message, when one is bound to the task; routine or controller-origin tasks do not invent a human original. An attached original request leaves a revisable task with an explicit unavailable explanation before any worker send. Files are not copied between hosts and source paths are never converted into receiver-local paths. Handle required files at the target, then revise the task to continue without attachment transfer.

### Registered desktop bridge acceptance

Run `node scripts/registered-session-transport-e2e.mjs` with the repository Go and Node toolchains and installed desktop dependencies. It builds real source/receiver app-server test processes and exercises the actual registered desktop pool, exact existing-session send/replay, terminal result, and receiver policy revocation. A second journey loads the generated Personal plugin in the actual trusted-local UserHost. Its main model turn calls `list_sessions`, selects the uniquely named catalog target, and calls `create_task` with that returned locator. Both owner-local and federation journeys hold the target response until the originating main turn has completed, then verify task lineage, grant issuance/restoration, worker-result reconciliation, original user provenance, and stable replay. That Coding receiver uses the default owner-only policy with federation disabled; no custom receiver authorization setup is needed. The separate federation test retains an explicit receiver-policy fixture. Both use a synthetic model; neither establishes external account/physical-machine deployment or automatic controller wake after the worker response.
