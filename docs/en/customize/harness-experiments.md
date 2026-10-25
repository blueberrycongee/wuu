# Local harness experiments

Use `wuu eval` to compare a single-file policy against the production engine and independent built-in graders. The controller owns the plan and evidence directory; candidate runtimes own disposable state. This is a local experiment format, separate from the [public result publication contract](../../../evals/README.md).

```bash
wuu eval --live-codex-oauth --model MODEL --task test_failure_fix \
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
