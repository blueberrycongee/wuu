import type { RuntimeHost, ServiceDescriptor, ServiceRequirement, ToolResult } from './index.js';
import type { ToolServiceCall } from './tool-service.js';

/** Batch execution, independent of the selected loop and tool registry. */
export const TOOL_BATCH_SERVICE = 'agent.tool.batch';
export const TOOL_BATCH_RESOURCE_SERVICE = 'tool.batch.resources';
export const TOOL_BATCH_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  name: TOOL_BATCH_SERVICE, version: '1.0.0',
  methods: [{ name: 'execute', input_schema: `${TOOL_BATCH_SERVICE}.execute.input.v1`, output_schema: `${TOOL_BATCH_SERVICE}.execute.output.v1` }],
};
/** Opt into safe streaming-prefix admissions. The host invokes stream_admit
 * before each early effect; false defers it and the remaining prefix. */
export const TOOL_BATCH_STREAMING_SERVICE_DESCRIPTOR: ServiceDescriptor = {
  ...TOOL_BATCH_SERVICE_DESCRIPTOR,
  methods: [...TOOL_BATCH_SERVICE_DESCRIPTOR.methods, {
    name: 'stream_admit', input_schema: `${TOOL_BATCH_SERVICE}.stream.admit.input.v1`,
    output_schema: `${TOOL_BATCH_SERVICE}.stream.admit.output.v1`,
  }],
};
export interface ToolBatchStreamRequest {
  batch_id: string;
  call: ToolServiceCall & { id: string };
  read_only: boolean;
  concurrency_safe: boolean;
}
export interface ToolBatchStreamAdmission { admit: boolean }
export interface ToolBatchRequest {
  id: string;
  /** Immutable admitted canonical calls. Arguments stay JSON, including
   * {"input":"..."} for custom tools; only model transports use literal input.
   * Maximum 1024 calls and 4 MiB of arguments. */
  calls: readonly (ToolServiceCall & { id: string })[];
  /** Selected scheduling policy constraints; resource dispatch enforces them. */
  serial: boolean;
  max_parallel: number;
}
export interface ToolBatchResolution {
  call: ToolServiceCall;
  concurrency_safe: boolean;
  orchestrator: boolean;
  error?: string;
}
export interface ToolBatchOutcome {
  result: ToolResult & { model_text?: string };
  invocation_id?: string;
}
export interface ToolBatchCompletion { completed: true }
export function requireToolBatchResources(): ServiceRequirement[] {
  return [{ name: TOOL_BATCH_RESOURCE_SERVICE, major_version: 1, required: true }];
}
/** Bind only to the current execute callback host. Indexes refer to the original
 * batch; callers cannot substitute tool/actor/arguments. Execute joins duplicate
 * dispatches and returns the durable outcome. Cancel joins physical completion.
 * Settle rejects an unstarted call (is_error must be true); executed outcomes
 * cannot be overwritten. Unsettled calls are rejected if the provider exits.
 * No operation retries an effect or silently selects a different backend.
 */
export function toolBatchResources(host: RuntimeHost, request: Pick<ToolBatchRequest, 'id'>) {
  const call = <T>(method: string, index: number, result?: ToolBatchOutcome['result']): Promise<T> =>
    host.call('host.service.call', {
      service: TOOL_BATCH_RESOURCE_SERVICE, method,
      params: { batch_id: request.id, index, ...(result === undefined ? {} : { result }) },
    }) as Promise<T>;
  return {
    resolve: (index: number) => call<ToolBatchResolution>('resolve', index),
    execute: (index: number) => call<ToolBatchOutcome>('execute', index),
    cancel: (index: number) => call<ToolBatchOutcome>('cancel', index),
    settle: (index: number, result: ToolBatchOutcome['result']) => call<ToolBatchOutcome>('settle', index, result),
  };
}
