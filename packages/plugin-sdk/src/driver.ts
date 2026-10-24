import type { RuntimeHost, ServiceDescriptor, ServiceRequirement } from "./index.js";
import type { LogicalModelRequestV1, ModelMessageV1, ModelToolCallV1 } from "./model.js";

export const DRIVER_TURN_SERVICE = "driver.turn";
export const DRIVER_CHECKPOINT_SERVICE = "driver.checkpoint";

export interface DriverExecution {
  readonly session_id: string;
  readonly execution_id: string;
}
export interface DriverCheckpoint<State = unknown> {
  readonly contract_version: 1;
  readonly driver_id: string;
  readonly driver_version: string;
  readonly state: State;
}
export interface DriverInstanceParams { readonly instance_id: string }
export interface DriverCreateParams { readonly execution: DriverExecution }
export interface DriverResumeParams<State = unknown> extends DriverCreateParams {
  readonly checkpoint: DriverCheckpoint<State>;
}
export interface DriverTurnReceipt { readonly receipt_id: string }
/** Durable addresses are correlation metadata, never execution authority. */
export interface DriverMessage extends ModelMessageV1 {
  readonly tool_invocation_id?: string;
  readonly client_id?: string;
  readonly seq?: number;
  readonly steered?: boolean;
}
export interface DriverTurnInput extends LogicalModelRequestV1 {
  readonly messages: readonly DriverMessage[];
}
export interface DriverTurnContext {
  readonly messages: readonly DriverMessage[];
  /** Execution-local revision. Any accepted message or archive changes it. */
  readonly revision: number;
  readonly archive_head: number;
  /** Both durable archive and atomic context checkpoint are available. */
  readonly can_replace: boolean;
}
export interface DriverInputAdmission {
  readonly messages: readonly DriverMessage[];
  readonly context: DriverTurnContext;
}
export interface DriverContextBlock {
  readonly kind: string;
  readonly title?: string;
  readonly source?: string;
  readonly content: string;
  readonly token_budget?: number;
}
export interface DriverContextSegment {
  /** Model projection of this segment, including any typed blocks. */
  readonly messages: readonly DriverMessage[];
  readonly blocks?: readonly DriverContextBlock[];
  readonly lifecycle: string;
  readonly placement: string;
  readonly cache_policy: string;
  readonly durable: boolean;
  readonly visible_in_ui: boolean;
}
export interface DriverModelRound {
  /** The assistant response. Input admission is a separate operation. */
  readonly messages: readonly DriverMessage[];
  /** Reserved batch from modelWithTools. Join a successful nonempty receipt
   * with tools() before input, new effects, context replacement, or completion.
   */
  readonly tool_batch_id?: string;
  /** Some tool work began, even if the model operation later failed. */
  readonly tool_work_started?: boolean;
  readonly usage?: {
    readonly input_tokens: number;
    readonly output_tokens: number;
    readonly cache_creation_tokens?: number;
    readonly cache_read_tokens?: number;
  };
}

export interface DriverFailure {
  readonly message: string;
  readonly code?: "empty_answer" | "summary_output_limit";
  readonly stop_reason?: string;
  readonly origin: string;
  readonly category: string;
  readonly provider_code?: string;
  readonly http_status?: number;
  readonly retry_after_ms?: number;
  readonly context_overflow: boolean;
  readonly recovery?: {
    readonly operation_id?: string;
    readonly attempt_count: number;
    readonly retry_count: number;
    readonly max_attempts: number;
    readonly submission_count: number;
    readonly stop_reason: string;
    readonly budget_dimension?: string;
  };
}

/** Classified operation failure. Classification alone does not authorize
 * replay; inspect partial output and the recovery executor's stopping decision.
 */
export class DriverOperationError extends Error {
  readonly failure: DriverFailure;

  constructor(failure: DriverFailure) {
    super(failure.message);
    this.name = "DriverOperationError";
    this.failure = failure;
  }
}

/** Model failures retain partial messages, usage and accepted-tool evidence. */
export class DriverModelError extends DriverOperationError {
  readonly round: DriverModelRound;

  constructor(failure: DriverFailure, round: DriverModelRound) {
    super(failure);
    this.name = "DriverModelError";
    this.round = round;
  }
}

/** Service protocol version 2. Driver identity and checkpoint format versions
 * are independent of the service version. Failed results retain evidence.
 */
export type DriverRunResult<State = unknown> = {
  readonly checkpoint: DriverCheckpoint<State>;
  readonly receipt_id?: string;
} & (
  | { readonly status: "succeeded"; readonly failure?: never }
  | { readonly status: "failed"; readonly failure: DriverFailure }
  | { readonly status: "canceled"; readonly failure?: DriverFailure }
);

export interface DriverSystemSection {
  readonly key: string;
  readonly static: boolean;
  readonly bytes: number;
  readonly hash?: string;
}
export interface DriverTurnSettings {
  readonly max_steps: number;
  readonly streaming_tools: boolean;
  readonly provider_observation_key: string;
  readonly model_variant: string;
  readonly context_tokens: number;
  readonly input_tokens: number;
  readonly output_reserve_tokens: number;
  readonly compact_threshold_tokens: number;
  readonly compact_threshold_percent: number;
  readonly compact_keep_recent_tokens: number;
  readonly disable_auto_compact: boolean;
  readonly force_initial_compact: boolean;
  readonly compact_only: boolean;
  readonly fresh_context_tokens: number;
  readonly can_archive: boolean;
  readonly can_compact: boolean;
  readonly system_sections?: readonly DriverSystemSection[];
}
export interface DriverContextState {
  readonly retained?: {
    readonly messages: readonly { readonly after_durable: number; readonly message: DriverMessage }[];
    readonly durable_len: number;
    readonly durable_hash: string;
  };
  /** Context estimates; these cannot replace authoritative inference usage. */
  readonly usage?: {
    readonly last_response_total: number;
    readonly last_successful_request_tokens: number;
    readonly last_response_contract: string;
    readonly pending_delta: number;
    readonly adjustment: string;
  };
}
export interface DriverCompactBudget {
  readonly reason: string;
  readonly last_successful_tokens: number;
  readonly failed_request_tokens: number;
  readonly target_total_tokens: number;
  /** Request a fresh-context proposal from the selected policy. */
  readonly window?: DriverContextWindow;
}
export interface DriverContextWindow {
  readonly history_head: number;
  readonly fixed_tokens: number;
  readonly target_tokens: number;
}
export interface DriverRequestContextSummary {
  /** The resource supplies the execution's step index. */
  readonly step_index?: number;
  readonly transient_messages?: number;
  readonly content_bytes?: number;
  readonly block_kinds?: readonly string[];
  readonly block_kind_counts?: Readonly<Record<string, number>>;
  readonly block_kind_bytes?: Readonly<Record<string, number>>;
  readonly segment_lifecycle_counts?: Readonly<Record<string, number>>;
  readonly segment_placement_counts?: Readonly<Record<string, number>>;
  readonly segment_cache_policy_counts?: Readonly<Record<string, number>>;
  readonly message_count?: number;
  readonly system_messages?: number;
  readonly hidden_messages?: number;
  readonly tool_count?: number;
  readonly stable_prefix?: number;
  readonly turn_prefix?: number;
  readonly dynamic_context_bytes?: number;
  readonly system_bytes?: number;
  readonly stable_prefix_bytes?: number;
  readonly turn_prefix_bytes?: number;
  readonly message_bytes?: number;
  readonly tool_schema_bytes?: number;
  readonly loadable_tool_count?: number;
  readonly loadable_tool_schema_bytes?: number;
  readonly loadable_tool_surface_hash?: string;
  readonly system_hash?: string;
  readonly stable_prefix_hash?: string;
  readonly turn_prefix_hash?: string;
  readonly tool_surface_hash?: string;
  readonly prompt_cache_key?: string;
  readonly input_tokens?: number;
  readonly output_tokens?: number;
  readonly cache_creation_tokens?: number;
  readonly cache_read_tokens?: number;
  readonly system_sections?: readonly DriverSystemSection[];
}
export interface DriverCompactObservation {
  readonly reason: string;
  readonly status?: string;
  readonly tokens_before?: number;
  readonly tokens_after?: number;
  readonly messages_before?: number;
  readonly messages_after?: number;
  readonly summary?: string;
  readonly last_response_total?: number;
  readonly pending_delta?: number;
  readonly usage_adjustment?: string;
  readonly error?: string;
  /** after_compact passes this classified cause to configured hooks. */
  readonly failure?: DriverFailure;
  readonly output_limit?: boolean;
}
export type DriverTurnObservation =
  | { readonly kind: "request_context"; readonly request_context: DriverRequestContextSummary }
  | { readonly kind: "before_compact" | "after_compact" | "compact_start" | "compact_result" | "compact_attempt"; readonly compact: DriverCompactObservation };
export interface DriverTurnArchive {
  readonly messages: readonly DriverMessage[];
  readonly head: number;
}
export interface DriverCompletion {
  readonly finish_reason?: string;
  readonly stop_reason?: string;
  readonly truncated?: boolean;
}

export function requireDriverTurnServices(): ServiceRequirement[] {
  return [
    { name: DRIVER_TURN_SERVICE, major_version: 4, required: true },
    { name: DRIVER_CHECKPOINT_SERVICE, major_version: 1, required: true },
  ];
}

export function driverServiceDescriptor(profile: string): ServiceDescriptor {
  return {
    name: `driver.${profile}`, version: "2.0.0",
    methods: ["descriptor", "create", "resume", "run", "checkpoint", "shutdown"].map(name => ({
      name, input_schema: `driver.${name}.input.v1`, output_schema: `driver.${name}.output.v${name === "run" ? 2 : 1}`,
      ...(name === "descriptor" ? { preparation: true } : {}),
      ...(name === "shutdown" ? { cleanup: true } : {}),
    })),
  };
}

/** Operations are bound to one active execution and its provider generation.
 * Model events stream through the session; the reply contains the completed
 * round. model() does not execute tools; modelWithTools() explicitly opts into
 * overlapping safe tools. The driver chooses the next action. Calls are
 * serialized; cancellation or completion rejects further operations.
 */
export function driverTurn(host: RuntimeHost, execution: DriverExecution) {
  const scope = { execution_id: execution.execution_id };
  async function call<T>(method: string, params: object): Promise<T> {
    return await host.call("host.service.call", {
      service: DRIVER_TURN_SERVICE, method, params: { ...scope, ...params },
    }) as T;
  }
  async function callModel(method: string, request: LogicalModelRequestV1, batch_id?: string): Promise<DriverModelRound> {
    const result = await call<DriverModelRound & { readonly failure?: DriverFailure }>(method, { request, batch_id });
    if (result.failure) throw new DriverModelError(result.failure, result);
    return result;
  }
  return {
    /** Bound model defaults, available tools, and current active history. */
    read: () => call<DriverTurnInput>("read", {}),
    /** Selected settings and capabilities. Reading performs no effects. */
    settings: () => call<DriverTurnSettings>("settings", {}),
    /** Identity-bound request insertions and context estimates. */
    contextState: () => call<DriverContextState>("context_state", {}),
    /** Persist insertions before dispatch. An absent retained field clears
     * insertions; absent usage leaves estimates unchanged. */
    saveContextState: async (state: DriverContextState): Promise<void> => {
      await call("save_context_state", { state });
    },
    /** Decision observations and configured compaction hooks. */
    observe: async (observation: DriverTurnObservation): Promise<void> => {
      await call("observe", { observation });
    },
    /** Propose replacement context using the selected compaction provider. The driver
     * validates and explicitly commits the result with replaceContext(). */
    compact: async (messages: readonly DriverMessage[], budget: DriverCompactBudget): Promise<readonly DriverMessage[]> => {
      const result = await call<{ readonly messages: readonly DriverMessage[]; readonly failure?: DriverFailure }>("compact", { messages, budget });
      if (result.failure) throw new DriverOperationError(result.failure);
      return result.messages;
    },
    /** Archive accepted history and return durable addresses without replacing
     * active context. An interrupted reply cannot undo committed history. */
    archiveContext: () => call<DriverTurnArchive>("archive_context", {}),
    /** Consume queued input at the driver's chosen boundary. Accepted steering
     * is archived before returning its durable addresses. This operation does
     * not build a request or call the model; retries do not repeat admission.
     */
    admitInput: () => call<DriverInputAdmission>("admit_input", {}),
    /** Current request-only context, including tool-produced context. The
     * driver chooses placement and retention; reading does not persist it.
     */
    requestContext: async (): Promise<readonly DriverContextSegment[]> =>
      (await call<{ readonly segments: readonly DriverContextSegment[] }>("request_context", {})).segments,
    /** Apply configured request transforms to the driver's assembled request.
     * Returns an independent request; neither admits input nor calls a model.
     */
    transformRequest: (request: DriverTurnInput) => call<DriverTurnInput>("transform_request", { request }),
    context: () => call<DriverTurnContext>("context", {}),
    /** Replace active model context at the revision returned by context().
     * Accepted originals are archived first. The replacement and concurrent
     * durable tail commit atomically; cancellation cannot undo that commit.
     * An archive remains durable even if the context commit fails. There is
     * no automatic volatile fallback. Explicit volatile mode is accepted only
     * when no durable context writer is configured, and promises no checkpoint.
     */
    replaceContext: (revision: number, messages: readonly DriverMessage[], options: { readonly volatile?: boolean } = {}) =>
      call<DriverTurnContext>("replace_context", { revision, messages, ...options }),
    /** Send exactly the supplied request. It does not consume input, append
     * context, or run transforms. Throws DriverModelError for a failed model
     * operation, preserving its partial result. Transport and authorization
     * failures remain host errors.
     */
    model: (request: LogicalModelRequestV1) => callModel("model", request),
    /** Reserve a unique batch before dispatch and allow safe
     * tools to start while the model streams. Checkpoint intent first. Join a
     * successful tool_batch_id through tools() using exactly the returned calls.
     * Failed operations cancel and drain their tools; never reuse their batch
     * ID. Partial output and tool_work_started survive in the error.
     */
    modelWithTools: (request: LogicalModelRequestV1, batch_id: string) =>
      callModel("model_with_tools", request, batch_id),
    /** ID is unique within this execution, including failed calls. Persist
     * intent before submitting effects. An interrupted batch needs explicit
     * reconciliation; cancellation cannot undo its external effects. Call IDs
     * must be unique within a model round. A new model operation may reuse a
     * provider call ID after prior batches settle. Calls can be chosen directly;
     * missing declarations are recorded automatically. Pending calls can be
     * submitted in subsets. A modelWithTools receipt instead joins its complete
     * batch, preserving call order, identities, and arguments. Already started
     * work is awaited once. The reply contains only tool result messages.
     */
    tools: (batch_id: string, messages: readonly DriverMessage[], calls: readonly ModelToolCallV1[]) =>
      call<{ readonly messages: readonly DriverMessage[] }>("tools", { batch_id, messages, calls }),
    /** Model/tool messages are already recorded. Pass only additional authored
     * messages here. Completion cannot erase accepted history or tool effects.
     */
    complete: (content: string, messages: readonly DriverMessage[] = [], completion: DriverCompletion = {}) =>
      call<DriverTurnReceipt>("complete", { content, messages, ...completion }),
    checkpoint: async <State>(checkpoint: DriverCheckpoint<State>): Promise<void> => {
      await host.call("host.service.call", {
        service: DRIVER_CHECKPOINT_SERVICE, method: "call", params: { ...scope, checkpoint },
      });
    },
  };
}
