# Local harness experiments

Use `wuu eval` to compare a single-file policy against the production engine and independent built-in graders. The controller owns the plan and evidence directory; candidate runtimes own disposable state. This is a local experiment format, separate from the [public result publication contract](../../../evals/README.md).

```bash
wuu eval --provider authorized-api --model MODEL --task test_failure_fix \
  --max-steps 6 --timeout 2m --freeze plan.json
wuu eval --plan plan.json --artifacts baseline
wuu plugin validate --json ./candidate.ts
wuu eval --plan plan.json --candidate ./candidate.ts --artifacts candidate
wuu eval --compare baseline/report.json,candidate/report.json
```

Freeze makes no model calls. It resolves `all` once, then records ordered task IDs, the embedded task/setup/grader source digest, model, reasoning settings, configuration digest, parent package fingerprints, tool versions and budgets. Running the plan never filters its task list again. Initialization, authentication, missing tools, timeout and unfinished execution count as failures. A changed parent configuration, package set, grader or recorded environment rejects admission and records the fixed denominator as failed.

The scorer identity covers `internal/evalharness/tasks.go`, `trace.go`, and the command-side `eval.go` and `eval_experiment.go`, including tool constraints and acceptance rules. It conservatively requires a new baseline for any edit to those files, even an edit unrelated to scoring.

Each attempt needs a new evidence directory. It contains the original plan, executable digest and Go dependency/build metadata, parent package trees, candidate package and embedded SDK, selected service bindings, active modules and package fingerprints, complete returned execution messages, task results and copied session journals. Temporary state is removed only after evidence export; export errors retain state and fail the attempt. `--keep-workdirs` additionally preserves task workspaces. Artifacts may contain private conversation or tool data; they are not automatically published.

Before model admission, each task writes `runtime-locations.json` with its state, session and workspace paths. Mutable state lives under that task’s evidence directory in `runtime-state/` until export succeeds. After a crash or forced cancellation, preserve that directory and the recorded workspace when investigating incomplete runs. In-flight usage that the provider has not reported remains unknown.

The candidate is prepared through the same TypeScript snapshot adapter as `wuu plugin dev`. Explicit module selection still applies: a candidate excluded by the configured composition fails the loaded-package check. No fallback substitutes another implementation. Single-file imports support Node builtins and `@wuu/plugin-sdk`; use a package directory for other dependencies. `plugin validate` checks source loading and package structure; the evaluation also checks activation and readiness.

The built-in selection rule requires both sides to pass every task, then requires fewer model turns or total tokens with duration no more than 1.25 times baseline. Cache tokens are included in total tokens. Cost remains unavailable when the subscription provider does not report monetary cost. A single accepted pair demonstrates the selection mechanism; it does not establish a general improvement. Keep a separate hypothesis, total experiment budget and repetition plan before running. For a different selection rule, start a separately specified experiment and remeasure its baseline.

For a Go/runtime candidate, build an independent executable and run that process against the same plan. Preserve the exact checkout SHA, complete patch and untracked source snapshot beside the reports before building; the executable digest alone is not a source reconstruction. Configuration values and external credentials are not exported: rerunning requires the same selected configuration and access. Tool version checks do not freeze network services or all machine state. Record external dependencies and hardware separately when they matter.

## Independent controller

[`examples/harness/run-pair.py`](../../../examples/harness/run-pair.py) runs the two attempts sequentially in separate POSIX process groups. It snapshots the plan, executables, source archives and optional TypeScript file before admission. Prepare the archives from the exact build inputs, including dirty patches and untracked source; the controller records their hashes but cannot establish that a binary was built from a claimed archive.

```bash
python3 examples/harness/run-pair.py --plan plan.json \
  --baseline ./baseline-wuu --source-baseline ./baseline-source.tar \
  --candidate ./candidate.ts --output ./pair-001 --timeout 180
# For a host change, also pass:
# --candidate-host ./candidate-wuu --source-candidate ./candidate-source.tar
```

`--timeout` is the predeclared wall-clock limit for each side. The controller retains stdout, stderr, process exit status, reports and a task ledger in `pair.json`. Crashes, incomplete reports and interruptions preserve the original denominator. The ledger recovers already written per-task results and usage even when the final report is missing, and retains the process record after interruption. Missing tasks fail, and a failed process cannot be accepted. Complete reports are compared by the baseline executable's fixed grader rule. Candidate cleanup never removes these files. The controller does not retry, adjust budgets, copy credentials or change the selected configuration. An existing output directory is rejected. Process groups bound cancellation and failure cleanup; trusted plugins still have the user's filesystem access.

To check process cleanup without model calls, run:

```bash
python3 examples/harness/check-process-cleanup.py --output ./cleanup-check
python3 examples/harness/check-pair-recovery.py --output ./recovery-check
```

The cleanup check covers clean exit, successful and failed parents that leave children running, and timeout with ignored termination signals. The recovery check uses synthetic processes to verify completed task records survive a crash or controller cancellation while unfinished tasks remain failed.

## Step policy

`agent.step.policy` version 1 is a `decision` capability. It receives the same immutable transcript view as `agent.pre_step`, after durable input admission and before default-loop compaction and model dispatch. The provider, model and generation are already bound. Policies run by descending priority, then plugin ID. The first stop short-circuits later policies. Request transforms still compose independently when the step continues.

```ts
import type { RuntimePlugin, AgentStepPolicyInput } from "@wuu/plugin-sdk";

export default {
  initialize() {
    return { protocol_version: 3, capabilities: [
      { id: "agent.step.policy", kind: "decision", version: 1 },
    ] };
  },
  invokeCapability({ input }) {
    const step = input as AgentStepPolicyInput;
    return { output: step.step_index >= 4
      ? { stop: true, reason: "local_budget", content: "Stopped at the local policy budget; work may be incomplete." }
      : { stop: false } };
  },
} satisfies RuntimePlugin;
```

For a transcript-dependent example, load [`examples/plugins/repeated-observation.ts`](../../../examples/plugins/repeated-observation.ts) with `wuu plugin dev ./examples/plugins/repeated-observation.ts`. It stops before a third model request when two consecutive identical tool requests return identical observations, and adds matching request guidance. Use it for tasks that do not require polling. Its terminal explanation explicitly leaves task correctness unresolved; this is an experiment, not a demonstrated benchmark improvement.

A stopping policy must return nonempty `reason` and `content`. The content becomes a visible assistant record with host-stamped plugin provenance; it is not counted as a model round. Stopping does not prove correctness, erase accepted input or undo tool effects. Error policy follows the declared capability contract; cancellation always propagates. The default loop applies the decision. Replacement drivers receive it from `admitInput()` and own their algorithm's response. Policies are not invoked after the default model-step budget is exhausted.

## Discover, extend, and retain a decision

`wuu plugin describe --workdir /project --types-dir ./sdk-snapshot` exports the
exact embedded public SDK and reports selected service descriptors, resolved
provider dependencies, unresolved requirements, package source roots and hashes,
module failures, and tool definitions. It opens a **new CLI state generation**
for that project's configuration; it does not attach to an existing desktop
session. `graph_generation` identifies the actual retained graph, independently
of the catalog epoch. The SDK directory must not already exist.

Use a configured, authorized API connection and a predeclared resource budget.
The commands never grant authorization to reuse a personal subscription. Freeze
records the fixed task count, per-task step/time limits and concurrency; monetary
cost is not calculated by this runner. Do not interpret missing cost as zero.

`--experiment-policy policy.json` freezes admission concurrency and the built-in
selection policy. For example:

```json
{"concurrency":2,"acceptance":{"require_all_pass":true,"improvement":"tokens","max_duration_ratio":1.25}}
```

Concurrency is 1–256. Results retain frozen task order regardless of completion
order, each task owns separate runtime state, and configuration is copied before
per-task setup. `improvement` supports `turns-or-tokens`, `turns`, `tokens`,
`quality`, and `non-regression`. These are convenience policies, not a restriction
on externally implemented evaluation strategies.

For other task sources, graders, schedules and acceptance rules, supply
`--experiment-controller controller.json` while freezing. The public SDK's
`ExperimentController` and `dispatchExperimentController` implement protocol 1:
one JSON stdin request `{version:1,method,params}`, one JSON stdout response.
Diagnostics go to stderr. Methods are `tasks`, `schedule`, `setup`, `score`, and
`accept`; acceptance returns `accept`, `reject`, or `revise`. The schedule may
reorder tasks and set concurrency, but must retain each task exactly once. The
resulting schedule is frozen, not recomputed by candidates.

The controller identity is `{ "command": ["/absolute/node", "/absolute/controller.mjs"],
"files": {"/absolute/node":"sha256:…", "/absolute/controller.mjs":"sha256:…"} }`.
Pin every transitive executable/script/data input. Build a standalone bundle of
[`experiment-controller.ts`](../../../examples/plugins/experiment-controller.ts)
against the public SDK; no internal imports or plugin installation are needed.
Hash the exact final files, including the selected Node executable. Controller
inputs are checked before and after each call and copied into attempt evidence.
Each call has a one-minute deadline, an 8 MiB stdout / 256 KiB stderr limit,
and POSIX process-group cleanup; overflow is an explicit failure. The sample's
text grader is deliberately limited, not a demonstrated semantic improvement.

Keep controller code, grading data and evidence outside candidate workspaces.
This trusted-extension system is not a hostile-code sandbox: same-user code can
still access files permitted by the operating system. Hash checks detect changed
inputs; they do not prove immunity to malicious grading interference. Changing
controller inputs requires a new plan and baseline. External-controller plans do
not bind built-in scorer source, so host code changes can be measured without
silently changing the independent grader.

```bash
wuu eval --provider authorized-api --model MODEL --max-steps 6 --timeout 2m \
  --experiment-controller controller.json --experiment-policy policy.json --freeze plan.json
wuu eval --plan plan.json --artifacts baseline
wuu eval --plan plan.json --candidate candidate.ts --artifacts candidate
wuu eval --compare baseline/report.json,candidate/report.json --decision-output decision.json
```

Comparison verifies the retained plan hashes, ordered task denominator and
nonnegative usage. For external controllers it also reopens the complete task
execution, per-task result and retained workspace, then invokes the pinned scorer
in the comparison controller. A candidate's success bit is not an independent
verdict: missing artifacts reject comparison and a failing independent grade
cannot be overridden by the report. These observations are not cryptographic
attestations of real model calls; trusted provider receipts are needed for that.
External-controller workspaces are retained for this verification. `--decision-output` is exclusive: reruns cannot overwrite a
previous decision. On acceptance, select the recorded source through `wuu plugin
dev ./candidate.ts` for subsequent work; on rejection retain the previous source.
Create a new source file and evidence directory for the next candidate. A changed
evaluation specification always requires rebaselining, not reusing a favorable
result.

## Rebuilt host candidates

After building a separate candidate executable from a retained source archive,
`wuu eval --plan plan.json --candidate-host ./candidate-wuu --artifacts host-attempt`
starts it under the original controller. This requires an independent pinned
controller in the plan. `host-attempt.host/` retains a fresh-task checkpoint,
binary/plan hashes, stdout/stderr and exit status even if the candidate crashes.
The worker report must identify the exact launched binary and complete task
set. Timeout and exit clean up the owned process group. The previous host remains
unchanged. This boundary starts fresh evaluation tasks; it **does not** migrate
an arbitrary in-flight stack or roll back external effects. Preserve runtime
state after an uncertain tool result instead of blindly retrying it. The existing
`run-pair.py` controller additionally snapshots source archives and retains a
partial task ledger across controller interruption.

For host-code comparisons, supervise both the baseline executable and candidate with `--candidate-host`. Comparison reads elapsed wall time from the original supervisor’s identity-checked `process.json`, not the candidate’s reported duration. Mixed supervised/unsupervised timings are rejected. `run-pair.py --candidate-host` applies the same supervision to both sides. This measures whole attempt wall time; task-level observations are not independent timing attestations.

To exercise the external-controller protocol with no inference, freeze a plan for
the sample controller, then run:

```bash
python3 examples/harness/check-controller.py --wuu ./bin/wuu --plan plan.json --output ./controller-check
```

This deterministic check writes synthetic execution observations. It verifies
that a fabricated success verdict cannot override the independent grader, then
edits the second candidate and checks a new durable decision. Its retained
`result.json` explicitly labels the run as framework verification, not a real
model experiment or self-improvement result.
