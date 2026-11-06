# Opt-in cloud composition

This reference explicitly replaces `driver.default@2` with
`example.durable-driver` and selects two optional native modules:

- `wuu.durable-wait` provides `host.durable-wait@1` over the selected
  `state.documents` authority.
- `wuu.durable-client` is the authenticated, session-authorized remote reader and
  human decision consumer. It does not run a second agent.

Neither module is enabled by default. The ordinary desktop composition still
uses `wuu.default-loop`, with no durable scheduler or approval state machine.
The remote task view is mounted separately and must use the selected remote
connection, not a local runtime.

## Build and configure

Build the core using the repository development instructions. From the checkout:

```sh
npm --prefix packages/plugin-sdk install --ignore-scripts
npm --prefix packages/plugin-sdk run build
npm --prefix examples/plugins/durable-driver install --ignore-scripts
npm --prefix examples/plugins/durable-driver run build
node examples/compositions/durable/configure.mjs \
  /absolute/path/to/wuu /absolute/path/to/workspace > /path/to/durable-config.json
```

The generated configuration is an explicit example, not an installer. Register
the built `examples/plugins/durable-driver/package` directory using the host's normal
plugin installation path, and configure a model provider in the same config.
Keep credentials in the host's normal credential store, not in checkpoints.
The package is a self-contained bundle; do not register the source tree or its
development `node_modules`. The reference disables PTC to demonstrate direct,
non-streaming tool approval. PTC can be enabled explicitly: the host reserves the
outer program call and its source in the proposed batch, while dynamic nested
calls still pass their own ordinary permissions. Durable controls remain
direct-only; a nested call cannot self-approve a wait. Approval-gated batches
never receive automatic recovery permission.

`configure.mjs` binds the existing command execution backend to
`wuu execution-worker`. The worker is a separate JSONL process with a shared
workspace and bounded shutdown. This demonstrates the public execution-environment
adapter, not a VM fleet or a new sandbox policy. Permission, sandbox and execution
admission still belong to Wuu. A lost worker result is `uncertain_tool_effect`;
it never becomes a fabricated committed tool error or an automatic retry.

## Lifetime and recovery

A personal agent identity and its business tasks can outlive many logical runs.
A logical run can outlive multiple finite host turns. Sleeping/waiting settles the
current physical turn without an outward answer, while the task remains pending.
Provider shutdown ends the old scheduler but preserves persisted wake intent for
its successor. Explicit business cancellation is a separate operation.

The plugin keeps bounded IDs, digests and task metadata; the host owns transcript,
tool receipts, usage, authorization and input deduplication. Approval has a finite
expiry. Expiry or disconnect is not evidence that an effect never started.
Re-approval is safe only behind a persisted host `not_started` batch fence.
Any approved batch that may have started remains unknown and is not replayed.

This is an independent Wuu composition. It does not change or claim direct
integration with aha's existing Temporal/shared-machine execution layer, which
currently retains its native driver selection. Product task/delegation state must
not be duplicated by the runtime task graph.

## Verification and limits

```sh
node scripts/durable-driver-e2e.mjs
node scripts/durable-cloud-e2e.mjs
```

The commands leave machine-readable reports, command logs, checkpoints and effect
counts under `.tmp/e2e/durable-cloud/`. Read each report's actual checks and limits;
a unit test, an unexecuted fixture or a skipped process test is not full acceptance.

The separate metadata-only task view intentionally disables blind approvals.
There is no trusted general-purpose action/scope summary for arbitrary tools yet.
An authenticated decision API is not by itself a complete human review surface.
The view is a mountable remote component; its deployment must supply the existing
authenticated connector. It is not installed into desktop navigation by default.
Rendered browser acceptance and external production infrastructure require their
own evidence. This example does not provision networking, authentication secrets,
remote machines, routing, or production high availability.

Children use fresh host-managed context by default. An explicit `fork_seq` copies
the active historical prefix through that exact persisted sequence, with a fresh
session/provider continuation identity and no execution checkpoint. It inherits
the selected entry's recorded agent configuration and projected code-mode store
state, while current permissions and tool restrictions remain authoritative.
Missing legacy snapshots fail explicitly; code state currently maps only ordinary
conversation scopes in the same working directory. The default desktop loop does
not enable these snapshots.
Task token totals can recover reported model usage through host request receipts,
including after a lost reply. Missing or expired receipts remain unavailable or
incomplete. Tool receipts remain authoritative; the task projection does not
invent per-tool cost or reconstruct unreported billing.
