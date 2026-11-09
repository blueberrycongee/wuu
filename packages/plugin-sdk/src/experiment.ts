/** Public, process-independent experiment controller protocol (version 1).
 * Controllers are trusted evaluation infrastructure, not candidate plugins.
 * Pin their complete code/data closure before baseline admission. Keep controller
 * files and evidence outside candidate workspaces; ordinary same-user processes
 * are not a hostile-code security boundary.
 */
export interface ExperimentTask {
  readonly id: string;
  readonly name: string;
  readonly prompt: string;
  readonly required_tools?: readonly string[];
  readonly forbidden_tools?: readonly string[];
}

export interface ExperimentVerification {
  readonly passed: boolean;
  readonly reason?: string;
  readonly evidence?: readonly {
    readonly check: string;
    readonly passed: boolean;
    readonly summary?: string;
    readonly path?: string;
    readonly expected?: string;
    readonly observed?: string;
  }[];
}

export interface ExperimentControllerIdentity {
  /** Absolute executable, followed by arguments. No shell interpolation. */
  readonly command: readonly string[];
  /** SHA-256 (sha256: prefix) of executable and every transitive script/data input. */
  readonly files: Readonly<Record<string, string>>;
}

export interface ExperimentPolicy {
  readonly concurrency: number;
  readonly acceptance: {
    readonly require_all_pass: boolean;
    readonly improvement: "turns-or-tokens" | "turns" | "tokens" | "quality" | "non-regression";
    readonly max_duration_ratio: number;
  };
}

export interface ExperimentTaskResult {
  readonly task_id: string;
  readonly success: boolean;
  readonly turns: number;
  readonly input_tokens: number;
  readonly output_tokens: number;
  readonly cache_read_tokens?: number;
  readonly cache_creation_tokens?: number;
  readonly duration_ms: number;
  readonly error?: string;
  readonly verification_reason?: string;
}

export interface ExperimentReport {
  readonly experiment: {
    readonly plan_sha256: string;
    readonly executable_sha256: string;
    readonly loaded_packages: Readonly<Record<string, string>>;
  };
  readonly results: readonly ExperimentTaskResult[];
  readonly duration_ms: number;
}

export interface ExperimentController {
  tasks(): Promise<{ tasks: readonly ExperimentTask[] }>;
  schedule(input: { tasks: readonly ExperimentTask[]; policy: ExperimentPolicy }): Promise<{ task_ids: readonly string[]; concurrency: number }>;
  setup(input: { task: ExperimentTask; root: string }): Promise<{ error?: string }>;
  score(input: { task: ExperimentTask; root: string; answer: string }): Promise<ExperimentVerification>;
  accept(input: { plan: unknown; baseline: ExperimentReport; candidate: ExperimentReport }): Promise<{
    decision: "accept" | "reject" | "revise";
    reason: string;
  }>;
}

/** Use one JSON request on stdin and one JSON response on stdout. Diagnostic
 * logs belong on stderr. The host starts a fresh process for each invocation,
 * imposes a one-minute deadline, and checks the pinned closure before and after.
 * This dispatcher is transport-neutral so it also works in a test harness.
 */
export async function dispatchExperimentController(controller: ExperimentController, input: unknown): Promise<unknown> {
  if (!input || typeof input !== "object") throw new Error("invalid experiment request");
  const request = input as { version?: number; method?: string; params?: unknown };
  if (request.version !== 1) throw new Error("unsupported experiment protocol version");
  switch (request.method) {
    case "tasks": return controller.tasks();
    case "schedule": return controller.schedule(request.params as Parameters<ExperimentController["schedule"]>[0]);
    case "setup": return controller.setup(request.params as Parameters<ExperimentController["setup"]>[0]);
    case "score": return controller.score(request.params as Parameters<ExperimentController["score"]>[0]);
    case "accept": return controller.accept(request.params as Parameters<ExperimentController["accept"]>[0]);
    default: throw new Error(`unknown experiment method ${request.method}`);
  }
}
