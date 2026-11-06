# Durable cloud driver

An opt-in, headless `driver.default@2` provider. The desktop default composition
is unchanged. Install this package only in the independent durable composition.

```sh
npm install --ignore-scripts
npm run build
```

The build emits a self-contained runtime-only `package/` directory. Install
that directory, not the source checkout or its `node_modules`. It contains
`plugin.json`, a module marker and the bundled `dist/runtime.js`; no dependency
symlinks or SDK source are needed at installation. The runtime uses only the public SDK. The selected
`state.documents` provider owns all durable storage. Host history, model usage,
tool invocation receipts, permission checks and session admission remain the
source of truth; this package stores references, digests and bounded task metadata.

## Recovery and bounds

The checkpoint envelope identifies contract version 1, driver ID and package
version; incompatible versions fail explicitly. Checkpoints are limited to 32 KiB
and never truncated. Each session retains at most 256 live tasks and its newest
128 terminal tasks, with a 512 KiB document bound. Model rounds and tool batches
are bounded. More than five process recoveries fails `restart_limit`. Set
`WUU_DURABLE_RESTART_LIMIT` to an integer from 0 through 100 to change this
process-start setting; invalid configuration fails explicitly. Intentional
sleep/wait turn settlement does not count as a process crash.

A checkpoint before dispatch carries batch and call IDs. A recovered batch may
continue only when every call has a host invocation receipt, including failed
results. A missing receipt fails `uncertain_tool_effect`. No tool ledger or
transcript is duplicated. Interrupted model operations with no committed response
fail `uncertain_model_result` rather than inventing a result. Logs contain only
IDs, phases, attempts and stable error codes. Task usage comes only from host
model replies. If a crash commits inference before its reply/metadata update, the
public driver contract does not expose a usage-receipt lookup: the recovered
round is explicitly `usage_status: unavailable` and any partial run aggregate is
`incomplete`. No token or cost estimate fills that gap; host accounting remains
authoritative.

## Controls

Controls are direct-only model tools. The cloud reference configuration disables
programmatic tool calling so the driver can gate each complete top-level batch.
Nested, unregistered wait calls cannot report an approved decision.

- `durable_sleep` persists a timer, settles the physical turn with no outward
  answer, and resumes through a stable host input request. The logical task stays
  sleeping until delivery. The active generation submits finite `host.background` jobs every 500 ms;
  each job scans a bounded page. Publication revokes old-generation jobs before
  the successor can admit work.
  shutdown stops polling but preserves successor wake intent.
- `durable_subagent` creates a public child session and submits a stable input.
  Foreground children suspend the parent physical turn. Background children finish
  independently of that turn and deliver a stable follow-up. Explicit cancellation
  propagates unless a background child was requested as independent. At most eight
  live children and four ancestry levels are permitted. Results remain in host
  history, not task records.
- `durable_wait` requests an expiring host-owned approval, input or event. Input
  and event waits must be the only call in a batch. An approval wait gates the
  entire proposed batch, including its control call. Only authenticated host
  decisions permit progress; pending, denied, expired and fenced decisions never
  authorize effects. An approved batch still passes ordinary host permissions.
- After an approval expires, send `/retry-wait <request_id>` to request a new
  one-hour approval attempt. The host must positively prove the original batch
  never started and fence it before re-registration. The replacement still needs
  a new human decision; unknown or already-dispatched effects cannot be retried.
- `durable_context` schedules compaction/reset/handoff using archive-preserving
  host context replacement. It never edits physical history.
- `durable_cancel` fences scheduled tasks and cancels non-independent children.

`durable.tasks@1` is read-only. `list({session_id,run_id?})` and
`get({session_id,run_id?,task_id})` require exact authenticated session scope or
host-attested durable-client admission. Responses include version 1, a store
revision and tasks (or a nullable task). Task records contain no prompts, tool
arguments, output bodies or credentials. The separate remote view only consumes
this contract; it never starts a local driver.

The headless host must select `host.background@1`, `agent.tools@1`,
`host.durable-wait@1` and provide the public session
create/send/inspect/list/cancel/context/history services. Missing selected
providers fail explicitly. This is a single-host reference scheduler, not a
multi-tenant VM fleet or an execution lease service.
