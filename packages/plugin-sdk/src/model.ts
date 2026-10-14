import type {
  RuntimeHost, ServiceRequirement, ModelRequestUsage, ToolResult,
} from "./index.js";

export const MODEL_INVOKE_SERVICE = "host.model.invoke" as const;
export const MODEL_EMIT_SERVICE = "host.model.emit" as const;
export const BACKGROUND_SERVICE = "host.background" as const;
export const MODEL_REQUEST_TRANSFORM_CAPABILITY = "model.request.transform" as const;
export const MODEL_REQUEST_OBSERVE_CAPABILITY = "model.request.observe" as const;
export const MODEL_MIDDLEWARE_CAPABILITY = "model.middleware" as const;
export const MODEL_ADAPTER_CAPABILITY = "model.adapter" as const;

/** Portable model input. Credentials, retry state and execution authority stay in the host. */
export interface LogicalModelRequestV1 {
  readonly version: 1;
  /** Exact adapter ID; omitted uses provider_options.adapter or explicit model_alias defaults. "native" selects the host protocol. */
  readonly adapter?: string;
  readonly provider?: string;
  readonly model: string;
  readonly messages: readonly ModelMessageV1[];
  readonly tools: readonly ModelToolV1[];
  readonly temperature?: number;
  readonly max_tokens?: number;
  readonly effort?: string;
  readonly cache_hint?: ModelCacheHintV1;
  /** Preserve unknown option keys when copying or transforming a request. */
  readonly provider_options?: Readonly<Record<string, unknown>>;
  /** Non-secret compatibility identity for native history; never credential authority. */
  readonly provider_state_scope?: string;
  readonly media_input?: ModelMediaInputV1;
  readonly native_deferred_tool_discovery?: boolean;
  readonly force_tool_name?: string;
}

export type ModelRequestV1 = LogicalModelRequestV1;

export interface ModelCacheHintV1 {
  readonly prompt_cache_key?: string;
  readonly stable_system?: boolean;
  readonly stable_prefix_messages?: number;
  readonly turn_prefix_messages?: number;
  readonly has_compact_summary?: boolean;
}

export interface ModelMediaInputV1 {
  readonly video?: boolean;
  readonly video_known?: boolean;
  readonly image?: boolean;
  readonly image_known?: boolean;
  readonly file?: boolean;
  readonly file_known?: boolean;
}

/** Resolved model input. The host never exports local working-copy paths here. */
export interface ModelImageV1 {
  readonly media_type: string;
  readonly data: string;
  readonly required?: boolean;
  readonly width?: number;
  readonly height?: number;
  readonly provider_item_id?: string;
}

export interface ModelFileV1 {
  readonly media_type: string;
  readonly data: string;
  readonly filename?: string;
  readonly size_bytes?: number;
  readonly required?: boolean;
}

export interface ModelReasoningBlockV1 {
  readonly type: string;
  readonly thinking?: string;
  readonly signature?: string;
  readonly data?: string;
}

/** Keep native JSON verbatim and together with its provider/state compatibility scope. */
export interface ModelProviderItemV1 {
  readonly type: string;
  readonly data: string;
  readonly provider: string;
  readonly scope?: string;
  readonly fallback?: readonly ModelMessageV1[];
}

export interface ModelToolCallV1 {
  readonly id?: string;
  readonly name?: string;
  readonly arguments?: string;
  readonly kind?: "function" | "custom" | "tool_search" | (string & {});
  readonly provider_item_id?: string;
  readonly provider_item_provider?: string;
  readonly provider_item_model?: string;
}

export interface ModelToolV1 {
  readonly name: string;
  readonly description?: string;
  readonly input_schema: Readonly<Record<string, unknown>>;
  readonly output_schema?: Readonly<Record<string, unknown>>;
  readonly freeform?: boolean;
  readonly direct_only?: boolean;
  readonly defer_loading?: boolean;
  readonly cache_stable?: boolean;
}

export interface ModelLoadableToolV1 {
  readonly type: string;
  readonly name: string;
  readonly description?: string;
  readonly input_schema?: Readonly<Record<string, unknown>>;
  readonly output_schema?: Readonly<Record<string, unknown>>;
  readonly defer_loading?: boolean;
}

/** Retains ordered content and artifact references alongside the host-budgeted text. */
export interface ModelToolResultV1 extends ToolResult {
  readonly model_text?: string;
}

export interface ModelMessageV1 {
  readonly role: string;
  readonly name?: string;
  readonly content?: string;
  readonly hidden?: boolean;
  readonly origin?: string;
  readonly origin_id?: string;
  readonly cause?: string;
  readonly read_only?: boolean;
  readonly phase?: string;
  readonly reasoning_content?: string;
  readonly reasoning_blocks?: readonly ModelReasoningBlockV1[];
  readonly provider_items?: readonly ModelProviderItemV1[];
  readonly provider_item_id?: string;
  readonly provider_item_provider?: string;
  readonly provider_item_model?: string;
  readonly images?: readonly ModelImageV1[];
  readonly files?: readonly ModelFileV1[];
  readonly tool_call_id?: string;
  readonly tool_result_kind?: string;
  readonly tool_result?: ModelToolResultV1;
  readonly tool_calls?: readonly ModelToolCallV1[];
  readonly discovered_tools?: readonly ModelLoadableToolV1[];
  readonly finish_reason?: string;
  readonly stop_reason?: string;
  readonly truncated?: boolean;
}

/** Host-authenticated origin. Copying it does not grant authority or change hook routing. */
export interface ModelInvocationOriginV1 {
  readonly kind: "agent" | "plugin";
  readonly plugin_id?: string;
  readonly operation_id?: string;
  readonly parent_operation_id?: string;
  readonly session_id?: string;
  readonly thread_id?: string;
  readonly step_index?: number;
}

export interface ModelResponseV1 {
  readonly content?: string;
  readonly phase?: string;
  readonly provider_item_id?: string;
  readonly provider_item_model?: string;
  readonly reasoning_content?: string;
  readonly reasoning_blocks?: readonly ModelReasoningBlockV1[];
  readonly provider_items?: readonly ModelProviderItemV1[];
  readonly images?: readonly ModelImageV1[];
  readonly tool_calls?: readonly ModelToolCallV1[];
  readonly usage?: ModelRequestUsage;
  readonly stop_reason?: string;
  readonly finish_reason?: string;
  readonly truncated?: boolean;
}

export interface ModelErrorV1 {
  readonly code: string;
  readonly message: string;
}

export interface ModelStreamEventV1 {
  /** Discard accumulated partial output before accepting replacement-attempt events. */
  readonly reset_partial?: boolean;
  /** Opaque diagnostic/retry metadata; preserve it when forwarding events. */
  readonly provider_state?: Readonly<Record<string, unknown>>;
  readonly lifecycle?: Readonly<Record<string, unknown>>;
  readonly type: string;
  readonly content?: string;
  readonly phase?: string;
  readonly provider_item_id?: string;
  readonly image?: ModelImageV1;
  readonly message?: ModelMessageV1;
  readonly reasoning_block?: ModelReasoningBlockV1;
  readonly tool_call?: ModelToolCallV1;
  readonly provider_item?: ModelProviderItemV1;
  readonly provider_event_type?: string;
  readonly usage?: ModelRequestUsage;
  readonly stop_reason?: string;
  readonly finish_reason?: string;
  readonly truncated?: boolean;
  readonly error?: ModelErrorV1;
}

export interface ModelRequestMiddlewareInput {
  readonly request: LogicalModelRequestV1;
  readonly origin: ModelInvocationOriginV1;
}

/** The model.request.transform seam replaces logical input before provider dispatch. */
export interface ModelRequestMiddlewareOutput {
  readonly request?: LogicalModelRequestV1;
}

export interface ModelResponseObserveInput {
  readonly phase: "request" | "response" | "error";
  readonly request: LogicalModelRequestV1;
  readonly origin: ModelInvocationOriginV1;
  readonly result?: ModelInvokeResult;
  readonly event?: ModelStreamEventV1;
  readonly error?: ModelErrorV1;
}
export type ModelRequestObserveInput = ModelResponseObserveInput;

/**
 * Around-call model.middleware receives one single-use, execution-bound continuation.
 * Pass the request unchanged to invokeModel/startModelInvocation with continuation_id.
 * Use model.request.transform to edit input; return a response to short-circuit.
 */
export interface ModelMiddlewareInput {
  readonly version: 1;
  readonly request: LogicalModelRequestV1;
  readonly origin: ModelInvocationOriginV1;
  readonly stream: boolean;
  readonly continuation_id: string;
}
export type ModelMiddlewareOutput = ModelAdapterOutput;

export interface ModelInvokeParams {
  readonly request: LogicalModelRequestV1;
  readonly model_alias?: string;
  readonly timeout_ms?: number;
  readonly max_output_tokens?: number;
  /** Only the current middleware callback may use this once, with unchanged input. */
  readonly continuation_id?: string;
}

export interface ModelInvokeResult {
  readonly response: ModelResponseV1;
  readonly origin: ModelInvocationOriginV1;
}
export interface ModelInvocationHandle { readonly invocation_id: string }
export interface ModelInvocationReadParams extends ModelInvocationHandle {
  readonly cursor?: number;
  readonly wait_ms?: number;
}
export interface ModelInvocationReadResult {
  readonly events: readonly ModelStreamEventV1[];
  readonly cursor: number;
  readonly done: boolean;
  readonly result?: ModelInvokeResult;
  readonly error?: ModelErrorV1;
}

export interface ModelAdapterDescriptor {
  /** Globally unique dotted identifier; "native" is reserved for host protocols. */
  readonly id: string;
  readonly version: 1;
  /** Direct (default) pre-admits one billable-capable callback. Delegate usage is summary-only; host model child calls own their costs. */
  readonly mode?: "direct" | "delegate";
}
export interface ModelAdapterInput {
  readonly adapter_id: string;
  readonly request: LogicalModelRequestV1;
  readonly stream: boolean;
}
export interface ModelAdapterOutput {
  /** For stream=true, either return response or emit a terminal event sequence, never both. */
  readonly response?: ModelResponseV1;
}

export interface BackgroundStartParams {
  /** Unique among the plugin generation's live jobs. */
  readonly key: string;
  /** A service provided by this same plugin generation. */
  readonly service: string;
  readonly method: string;
  readonly params?: unknown;
  readonly timeout_ms?: number;
}
export interface BackgroundHandle {
  readonly job_id: string;
  readonly execution_id: string;
}
export interface BackgroundJobParams { readonly job_id: string }
export interface BackgroundStatus {
  readonly job_id: string;
  readonly state: "running" | "completed" | "cancelled" | "failed";
  readonly result?: unknown;
  readonly error?: ModelErrorV1;
}
export interface BackgroundCancelResult { readonly cancelled: boolean }

export function requireModelInvokeService(): ServiceRequirement {
  return { name: MODEL_INVOKE_SERVICE, major_version: 1, required: true };
}
export function requireModelEmitService(): ServiceRequirement {
  return { name: MODEL_EMIT_SERVICE, major_version: 1, required: true };
}
export function requireBackgroundService(): ServiceRequirement {
  return { name: BACKGROUND_SERVICE, major_version: 1, required: true };
}

/** Independent inference. Returned tool calls are data; this never executes them. */
export async function invokeModel(host: RuntimeHost, input: ModelInvokeParams): Promise<ModelInvokeResult> {
  return await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "call", params: input }) as ModelInvokeResult;
}
export async function startModelInvocation(host: RuntimeHost, input: ModelInvokeParams): Promise<ModelInvocationHandle> {
  return await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "start", params: input }) as ModelInvocationHandle;
}
export async function readModelInvocation(host: RuntimeHost, input: ModelInvocationReadParams): Promise<ModelInvocationReadResult> {
  return await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "read", params: input }) as ModelInvocationReadResult;
}
export async function cancelModelInvocation(host: RuntimeHost, input: ModelInvocationHandle): Promise<void> {
  await host.call("host.service.call", { service: MODEL_INVOKE_SERVICE, method: "cancel", params: input });
}

/** Pull model events with automatic cancellation when iteration exits early. */
export async function* streamModel(
  host: RuntimeHost, input: ModelInvokeParams,
): AsyncGenerator<ModelStreamEventV1, ModelInvokeResult | undefined, void> {
  const handle = await startModelInvocation(host, input);
  let cursor = 0;
  let done = false;
  let failed = false;
  try {
    while (!done) {
      const page = await readModelInvocation(host, { ...handle, cursor, wait_ms: 1000 });
      cursor = page.cursor;
      done = page.done;
      for (const event of page.events) yield event;
      if (page.error) throw Object.assign(new Error(page.error.message), { code: page.error.code });
      if (done) return page.result;
    }
  } catch (error) {
    failed = true;
    throw error;
  } finally {
    if (!done) {
      try { await cancelModelInvocation(host, handle); }
      catch (error) { if (!failed) throw error; }
    }
  }
}

/** Valid only in a live streaming adapter/middleware callback. */
export async function emitModelEvent(host: RuntimeHost, event: ModelStreamEventV1): Promise<void> {
  await host.call("host.service.call", { service: MODEL_EMIT_SERVICE, method: "call", params: event });
}

/** Start independent work in a caller-owned service. Allowed after activation, including timers. */
export async function startBackground(host: RuntimeHost, input: BackgroundStartParams): Promise<BackgroundHandle> {
  return await host.call("host.service.call", { service: BACKGROUND_SERVICE, method: "start", params: input }) as BackgroundHandle;
}
export async function inspectBackground(host: RuntimeHost, input: BackgroundJobParams): Promise<BackgroundStatus> {
  return await host.call("host.service.call", { service: BACKGROUND_SERVICE, method: "inspect", params: input }) as BackgroundStatus;
}
export async function cancelBackground(host: RuntimeHost, input: BackgroundJobParams): Promise<BackgroundCancelResult> {
  return await host.call("host.service.call", { service: BACKGROUND_SERVICE, method: "cancel", params: input }) as BackgroundCancelResult;
}
