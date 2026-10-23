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
export interface DriverModelRound {
  /** Admitted durable input contributions followed by the assistant response. */
  readonly messages: readonly DriverMessage[];
  readonly usage?: {
    readonly input_tokens: number;
    readonly output_tokens: number;
    readonly cache_creation_tokens?: number;
    readonly cache_read_tokens?: number;
  };
}

export function requireDriverTurnServices(): ServiceRequirement[] {
  return [DRIVER_TURN_SERVICE, DRIVER_CHECKPOINT_SERVICE].map(name => ({ name, major_version: 1, required: true }));
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
 * round. Model calls never execute tools or choose the next action. Calls are
 * serialized; cancellation or completion rejects further operations.
 */
export function driverTurn(host: RuntimeHost, execution: DriverExecution) {
  const scope = { execution_id: execution.execution_id };
  async function call<T>(method: string, params: object): Promise<T> {
    return await host.call("host.service.call", {
      service: DRIVER_TURN_SERVICE, method, params: { ...scope, ...params },
    }) as T;
  }
  return {
    /** Bound model defaults, available tools, and current active history. */
    read: () => call<DriverTurnInput>("read", {}),
    context: () => call<DriverTurnContext>("context", {}),
    /** Replace active model context at the revision returned by context().
     * Accepted originals are archived first. The replacement and concurrent
     * durable tail commit atomically; cancellation cannot undo that commit.
     * An archive remains durable even if the context commit fails. There is
     * no volatile fallback when durable context services are unavailable.
     */
    replaceContext: (revision: number, messages: readonly DriverMessage[]) =>
      call<DriverTurnContext>("replace_context", { revision, messages }),
    model: (request: LogicalModelRequestV1) => call<DriverModelRound>("model", { request }),
    /** ID is unique within this execution, including failed calls. Persist
     * intent before submitting effects. An interrupted batch needs explicit
     * reconciliation; cancellation cannot undo its external effects. Call IDs
     * must also be unique within the execution. Calls can be chosen directly;
     * missing declarations are recorded automatically. Pending calls can be
     * submitted in subsets. The reply contains only tool result messages.
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
