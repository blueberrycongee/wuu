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
128 terminal tasks plus ancestors needed by live descendants, with a 512 KiB
document bound. Model rounds and tool batches are bounded. More than five
consecutive process interruptions without durable progress fails `restart_limit`. Set
`WUU_DURABLE_RESTART_LIMIT` to an integer from 0 through 100 to change this
process-start setting; invalid configuration fails explicitly. Configure these variables in the installed plugin manifest’s explicit `runtime.env` map; the host intentionally does not forward arbitrary ambient environment variables. Intentional
sleep/wait turn settlement does not count as a process crash.

The driver explicitly declares `durable-recovery` and `tool_recovery`. Only this
opt-in path records host execution admission and cancellation fences. After a
host restart, the scheduler requests a fresh physical execution using the host's
exact turn/checkpoint proof, without a synthetic user message or sampling override.
A new input, explicit stop, changed driver or stale proof prevents recovery.
Old sessions without host admission evidence stay blocked. The ordinary desktop
driver does not enable this path or create a program journal.

A checkpoint before dispatch carries batch and call IDs. A recovered batch may
reuse every committed host invocation receipt, including failed results. For an
interrupted ordinary batch, the host may replay only calls whose saved and
current declarations explicitly permit safe replay, using the original admitted
batch and invocation identities. Missing declarations, revoked policy, legacy
checkpoints without an original execution reference, and approval-gated batches
remain fail-closed with `uncertain_tool_effect`. No tool ledger or
transcript is duplicated. Model-only interruptions without a committed response
use persistent retry deadlines and bounded attempts; committed responses are reused.
The public host must advertise single-attempt model dispatch, so host in-memory
backoff does not multiply the durable retry budget. Known transient provider failures use the same backoff, while authentication,
quota, budget, context-overflow and unknown tool-effect failures never take that
blind retry path. `WUU_DURABLE_MODEL_RETRIES` defaults to 3 (0 disables retry,
maximum 100), `WUU_DURABLE_RETRY_BASE_MS` defaults to 2000 (maximum 60000), and
`WUU_DURABLE_RETRY_MAX_MS` defaults to 60000 (maximum 3600000). The base must
not exceed the maximum. Provider retry hints are honored up to one hour; larger
hints end the attempt rather than retry early. A sleep task and its deadline are
persisted with the failed attempt, so process restarts cannot skip backoff.
New user input can start a separate run during backoff, including cancellation
of the pending run. Logs contain only
IDs, phases, attempts and stable error codes. Task usage comes only from host
reported inference receipts, read through a stable model request ID. A crash after
model archival but before the plugin receives its reply can recover those receipts
without repeating inference. Submission IDs deduplicate token and cache counts;
run totals and provider/model groups are projections of host accounting, not a
second billing ledger. Missing or incomplete receipts remain explicitly
`unavailable` or `incomplete`. No token or cost estimate fills that gap.

## Controls

Controls are direct-only model tools. The cloud reference configuration uses
direct tools. Programmatic tool calling can be enabled explicitly; its outer
`run_code` call participates in the same complete-batch admission, and each nested
call still passes current permissions. The existing QuickJS runtime journals
deterministic clock/random values and committed nested results. Recovery requires
the same program, tool catalog, runtime fingerprint and unexpired absolute
deadline. A changed or unsafe unknown call blocks recovery. Approval-gated batches
do not receive automatic replay permission. Nested, unregistered wait calls
cannot report an approved decision.

- `durable_sleep` persists a timer, settles the physical turn with no outward
  answer, and resumes through a stable host input request. The logical task stays
  sleeping until delivery. The active generation submits finite `host.background` jobs every 500 ms;
  each job scans a bounded page. Publication revokes old-generation jobs before
  the successor can admit work.
  shutdown stops polling but preserves successor wake intent.
- `durable_subagent` creates a public child session and submits a stable input.
  Optional `fork_seq` selects an exact inclusive historical boundary; fresh
  context remains the default. The required historical-state host feature
  preserves the entry's agent configuration and projected code-mode store values,
  including the first admitted input. Current permissions still apply; missing
  legacy snapshots or incompatible code-state scopes fail explicitly. Forks do
  not inherit execution checkpoints, and older hosts cannot silently substitute
  the latest history or configuration for that boundary.
  Foreground children suspend the parent physical turn. Background children finish
  independently of that turn and deliver a stable follow-up. Explicit cancellation
  propagates to ordinary owned work; background children survive it. An explicit
  `include_background: true` cancellation also reaches background descendants. At most eight
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
- `durable_context` uses archive-preserving host context replacement. Reset
  clears prior active context, optionally installs `note`, and ends the run with
  `outcome: {status: "unanswered", reason: "reset"}`. Handoff requires a new
  `note` and continues the run in that context. Neither edits physical history.
  Compaction delegates summarization to the selected host provider, with optional
  `note` instructions. Threshold compaction blocks the next request; a provider
  overflow gets one compaction-and-retry attempt. Background proposals run while
  generation continues and are placed only at safe boundaries. Their source and
  result remain in the host-owned proposal archive; checkpoints hold references.
  `WUU_DURABLE_COMPACTION_BACKGROUND_TOKENS` sets the prefetch distance below the
  selected model's blocking threshold (default 32768, zero disables prefetch).
  Set it in the plugin manifest's `runtime.env`; ambient parent variables are
  intentionally not inherited by plugin processes.
  Background failure preserves a valid answer; blocking failure stops inference.
  Reset and handoff cancel prior proposals so no late summary restores old context.
- `durable_cancel` fences scheduled tasks and cancels owned descendants bottom-up.
  A parent remains `completing` until ordinary owned work settles. Background
  work is a boundary unless `include_background: true` is requested.

`durable.tasks@1` is read-only. `list({session_id,run_id?})` and
`get({session_id,run_id?,task_id})` require exact authenticated session scope or
host-attested durable-client admission. Responses include version 1, a store
revision and tasks (or a nullable task). Nonterminal tasks expose a computed `blocked_reason` when their definition is missing, too old or cannot migrate. Inspection does not alter the stored checkpoint or execute task phases; reinstalling compatible code can unblock the retained work. Task records contain no prompts, tool
arguments, output bodies or credentials. The separate remote view only consumes
this contract; it never starts a local driver.

The headless host must select `host.background@1`, `agent.tools@1`,
`host.durable-wait@1` and provide the public session
create/send/inspect/list/cancel/context/history services. Missing selected
providers fail explicitly. This is a single-host reference scheduler, not a
multi-tenant VM fleet or an execution lease service.

## Provider-local task definitions

`TaskRegistry` lets provider authors register a stable task name and numeric
version with JSON input, an initial checkpoint, named phase handlers, an abort
handler, and an optional pure migration. Each task may define its own payload
and phase names. `TaskDefinitions` adapts that same dispatcher to the bundled
`durable.run` v1; this composition currently registers only that definition.

Missing definitions, newer stored versions, unavailable phases, and failed or
missing migrations block execution and scheduler effects. Successful migration
updates input, version, checkpoint and run-task phase in one selected document
store CAS. Host checkpoints are a separate boundary; stale copies reconcile
against the provider document. Strict host driver identity/version and checkpoint
schema checks remain. This is a provider-local extension mechanism, not a remote
arbitrary-task registration API.
