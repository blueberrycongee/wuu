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

export interface DriverModelFailure {
  readonly message: string;
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

/** A model operation failed. Partial messages and reported
 * usage remain available. Classification alone does not authorize replay;
 * inspect partial output and the recovery executor's stopping decision.
 */
export class DriverModelError extends Error {
  readonly failure: DriverModelFailure;
  readonly round: DriverModelRound;

  constructor(failure: DriverModelFailure, round: DriverModelRound) {
    super(failure.message);
    this.name = "DriverModelError";
    this.failure = failure;
    this.round = round;
  }
}

export function requireDriverTurnServices(): ServiceRequirement[] {
  return [
    { name: DRIVER_TURN_SERVICE, major_version: 3, required: true },
    { name: DRIVER_CHECKPOINT_SERVICE, major_version: 1, required: true },
  ];
}

export function driverServiceDescriptor(profile: string, version: string): ServiceDescriptor {
  return {
    name: `driver.${profile}`, version,
    methods: ["descriptor", "create", "resume", "run", "checkpoint", "shutdown"].map(name => ({
      name, input_schema: `driver.${name}.input.v1`, output_schema: `driver.${name}.output.v1`,
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
    const result = await call<DriverModelRound & { readonly failure?: DriverModelFailure }>(method, { request, batch_id });
    if (result.failure) throw new DriverModelError(result.failure, result);
    return result;
  }
  return {
    /** Bound model defaults, available tools, and current active history. */
    read: () => call<DriverTurnInput>("read", {}),
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
     * no volatile fallback when durable context services are unavailable.
     */
    replaceContext: (revision: number, messages: readonly DriverMessage[]) =>
      call<DriverTurnContext>("replace_context", { revision, messages }),
    /** Send exactly the supplied request. It does not consume input, append
     * context, or run transforms. Throws DriverModelError for a failed model
     * operation, preserving its partial result. Transport and authorization
     * failures remain host errors.
     */
    model: (request: LogicalModelRequestV1) => callModel("model", request),
    /** driver.turn 3.1+: reserve a unique batch before dispatch and allow safe
     * tools to start while the model streams. Checkpoint intent first. Join a
     * successful tool_batch_id through tools() using exactly the returned calls.
     * Failed operations cancel and drain their tools; never reuse their batch
     * or call IDs. Partial output and tool_work_started survive in the error.
     */
    modelWithTools: (request: LogicalModelRequestV1, batch_id: string) =>
      callModel("model_with_tools", request, batch_id),
    /** ID is unique within this execution, including failed calls. Persist
     * intent before submitting effects. An interrupted batch needs explicit
     * reconciliation; cancellation cannot undo its external effects. Call IDs
     * must also be unique within the execution. Calls can be chosen directly;
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
    complete: (content: string, messages: readonly DriverMessage[] = []) =>
      call<DriverTurnReceipt>("complete", { content, messages }),
    checkpoint: async <State>(checkpoint: DriverCheckpoint<State>): Promise<void> => {
      await host.call("host.service.call", {
        service: DRIVER_CHECKPOINT_SERVICE, method: "call", params: { ...scope, checkpoint },
      });
    },
  };
}
