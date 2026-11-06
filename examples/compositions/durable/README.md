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
development `node_modules`. The reference disables PTC and uses direct,
non-streaming tool batches so the host can reserve the exact proposed approval
batch before effects begin. Nested calls cannot self-approve a wait.

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

Children use fresh host-managed context. Their fork/source sequence records
lineage, not a copy of the parent's checkpoint or uncommitted effects. Task token
totals use reported host model usage; lost replies are marked unavailable or
incomplete. Tool receipts remain authoritative, but the task projection does not
invent per-tool cost or reconstruct billing after a lost model reply.
