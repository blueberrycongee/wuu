import type { RuntimeHost, RuntimeHostCallOptions, ServiceDescriptor, ServiceRequirement, ModelRequestUsage } from "./index.js";
import type { DriverMessage, DriverFailure, DriverContextState, DriverRequestContextSummary } from "./driver.js";
import type { ModelImageV1, ModelReasoningBlockV1, ModelToolCallV1, ModelToolResultV1, ModelProviderItemV1 } from "./model.js";

export const ENGINE_GATEWAY_SERVICE = "host.engine.session";
/** Provider-native permission/checkpoint data. Never interpreted as authority. */
export type EngineJSONValue = null | boolean | number | string | readonly EngineJSONValue[] | { readonly [key: string]: EngineJSONValue };

export interface EngineBinding {
 readonly thread_id: string;
 readonly root_dir: string;
 readonly model?: string;
 readonly effort?: string;
 readonly speed?: string;
 readonly model_options?: Readonly<Record<string, string>>;
 readonly permission_mode?: string;
 readonly instructions?: string;
 readonly mcp_servers?: readonly EngineMCPServer[];
 readonly external_ref?: string;
 readonly gateway_id: string;
}

export interface EngineMCPServer {
 readonly name: string;
 readonly url: string;
}

export interface EngineSessionParams {
 readonly session_id: string;
}

export interface EngineRunParams extends EngineSessionParams {
 readonly messages: readonly DriverMessage[];
}

export interface EngineRunResult {
 readonly result: EngineResultV1;
 readonly failure?: DriverFailure;
}

export interface EngineInterruptParams extends EngineSessionParams {
 readonly reason: string;
}

export interface EngineSteerParams extends EngineSessionParams {
 readonly message: DriverMessage;
}

/** Only return unsupported/rejected when the input was definitely NOT accepted.
 * Throw/transport loss remains unconfirmed and must not cause automatic replay.
 * Omitted disposition retains compatibility with accepted turn_id responses.
 */
export interface EngineSteerResult {
 readonly turn_id?: string;
 readonly disposition?: "accepted" | "unsupported" | "rejected";
}

export interface ModelStepSummaryV1 {
 readonly provider: string;
 readonly model: string;
 readonly api_model?: string;
 readonly revision?: number;
 readonly step_index: number;
 readonly operation_id: string;
 readonly context_window_tokens?: number;
}
export interface ModelStepUsageV1 extends ModelStepSummaryV1 {
 readonly input_tokens: number;
 readonly output_tokens: number;
 readonly cache_creation_tokens?: number;
 readonly cache_read_tokens?: number;
 readonly context_tokens?: number;
}

export interface EngineResultV1 {
 readonly model_steps?: readonly ModelStepUsageV1[];
 readonly version: 1;
 readonly content?: string;
 readonly new_messages: readonly DriverMessage[];
 readonly durable_new_messages: readonly DriverMessage[];
 readonly durable_messages_tracked?: boolean;
 readonly history_archive_head_seq?: number;
 readonly history_rewritten?: boolean;
 readonly retained_request_context?: NonNullable<DriverContextState["retained"]>;
 readonly input_tokens?: number;
 readonly output_tokens?: number;
 readonly cache_creation_tokens?: number;
 readonly cache_read_tokens?: number;
 readonly context_tokens?: number;
 readonly finish_reason?: string;
 readonly stop_reason?: string;
 readonly truncated?: boolean;
 readonly receipt_diagnostic?: string;
 readonly driver_id?: string;
 readonly driver_version?: string;
 readonly driver_contract_version?: number;
 readonly driver_status?: string;
 readonly driver_checkpoint?: EngineJSONValue;
}

export interface EngineEventV1 {
 readonly model_step?: ModelStepSummaryV1;
 readonly version: 1;
 readonly type: string;
 readonly content?: string;
 readonly phase?: string;
 readonly provider_item_id?: string;
 readonly message?: DriverMessage;
 readonly image?: ModelImageV1;
 readonly reasoning_block?: ModelReasoningBlockV1;
 readonly tool_call?: ModelToolCallV1;
 readonly tool_result?: string;
 readonly tool_result_detail?: ModelToolResultV1;
 readonly compact_reason?: string;
 readonly compact_phase?: string;
 readonly compact_summary?: string;
 readonly todo_update?: EngineTodoUpdateV1;
 readonly agent_activity?: EngineAgentActivityV1;
 readonly request_context?: DriverRequestContextSummary;
 readonly provider_state?: EngineProviderStateV1;
 readonly provider_item?: ModelProviderItemV1;
 readonly provider_event_type?: string;
 readonly lifecycle?: EngineLifecycleV1;
 readonly failure?: DriverFailure;
 readonly usage?: ModelRequestUsage;
 readonly stop_reason?: string;
 readonly finish_reason?: string;
 readonly truncated?: boolean;
}

export interface EngineProviderStateV1 {
 readonly step_index: number;
 readonly provider: string;
 readonly protocol: string;
 readonly transport: string;
 readonly configured_transport: string;
 readonly replay_mode: string;
 readonly previous_response_id_used: boolean;
 readonly connection_reused: boolean;
 readonly diagnostic: string;
 readonly transport_failure_phase: string;
 readonly failed_transport: string;
 readonly fallback_transport: string;
 readonly events_emitted: boolean;
 readonly fallback_active: boolean;
 readonly fallback_reason: string;
 readonly fallback_pin_status: string;
 readonly fallback_retry_after_ms: number;
 readonly fallback_ttlms: number;
 readonly input_items: number;
 readonly full_input_items: number;
 readonly delta_input_items: number;
}

export interface EngineLifecycleV1 {
 readonly phase: string;
 readonly operation_id: string;
 readonly operation_kind: string;
 readonly workload_profile: string;
 readonly payload_version: number;
 readonly attempt_id: string;
 readonly attempt: number;
 readonly max_attempts: number;
 readonly submission_id: string;
 readonly submission_count: number;
 readonly retry_count: number;
 readonly max_retries: number;
 readonly retry_in_ns: number;
 readonly elapsed_ns: number;
 readonly reason: string;
 readonly failure_category: string;
 readonly recovery_action: string;
 readonly budget_dimension: string;
 readonly replay_reason: string;
 readonly workflow: EngineWorkflowBudgetV1;
 readonly reset_partial: boolean;
}

export interface EngineWorkflowBudgetV1 {
 readonly workflow_id: string;
 readonly spec: EngineBudgetSpecV1;
 readonly operations: number;
 readonly attempts: number;
 readonly submissions: number;
 readonly transport_switches: number;
 readonly credential_refreshes: number;
 readonly payload_transforms: number;
 readonly child_operations: number;
 readonly recovery_wait_millis: number;
 readonly known_submissions: number;
 readonly estimated_submissions: number;
 readonly unknown_billable_submissions: number;
 readonly known_usage: ModelRequestUsage;
 readonly estimated_usage: ModelRequestUsage;
}

export interface EngineBudgetSpecV1 {
 readonly max_operations: EngineBudgetLimitV1;
 readonly max_attempts: EngineBudgetLimitV1;
 readonly max_submissions: EngineBudgetLimitV1;
 readonly max_same_payload_replays: EngineBudgetLimitV1;
 readonly max_transport_switches: EngineBudgetLimitV1;
 readonly max_credential_refreshes: EngineBudgetLimitV1;
 readonly max_payload_transforms: EngineBudgetLimitV1;
 readonly max_child_operations: EngineBudgetLimitV1;
 readonly max_recovery_wait_millis: EngineBudgetLimitV1;
 readonly max_unknown_billable_submissions: EngineBudgetLimitV1;
 readonly max_usage_tokens: EngineBudgetLimitV1;
}

export interface EngineBudgetLimitV1 {
 readonly set: boolean;
 readonly value: number;
}

export interface EngineTodoUpdateV1 {
 readonly explanation: string;
 readonly todos: readonly EngineTodoItemV1[];
}

export interface EngineTodoItemV1 {
 readonly content: string;
 readonly status: string;
}

export interface EngineAgentActivityV1 {
 readonly id: string;
 readonly engine: string;
 readonly label: string;
 readonly state: string;
}

export interface EngineApprovalRequestV1 {
 readonly kind: string;
 readonly engine_id: string;
 readonly thread_id: string;
 readonly turn_id: string;
 readonly item_id: string;
 readonly command: string;
 readonly cwd: string;
 readonly file_path: string;
 readonly reason: string;
 readonly permissions?: EngineJSONValue;
}

export interface EngineUserInputRequestV1 {
 readonly engine_id: string;
 readonly thread_id: string;
 readonly turn_id: string;
 readonly item_id: string;
 readonly blocking: boolean;
 readonly questions: readonly EngineUserInputQuestionV1[];
}

export interface EngineUserInputQuestionV1 {
 readonly id: string;
 readonly header: string;
 readonly question: string;
 readonly allow_custom: boolean;
 readonly secret: boolean;
 readonly options: readonly EngineUserInputOptionV1[];
}

export interface EngineUserInputOptionV1 {
 readonly label: string;
 readonly description: string;
}

export type EngineApprovalDecision = "accept" | "accept_for_session" | "decline" | "cancel";
export interface EngineDescriptor {
 readonly enabled: boolean;
 readonly id: string;
 readonly version: string;
 readonly capabilities?: readonly string[];
}

/** Register the same session and administrative contract used by shipped engines.
 * Retain/release bind the selected implementation before a session is opened.
 * Closing must cancel and drain the session before releasing its resources.
 */
export function engineServiceDescriptor(id: string): ServiceDescriptor {
 return {name:`engine.${id}`,version:"1.0.0",methods:[
  "descriptor", "retain", "release", "bind", "run", "interrupt", "steer", "close", "refresh", "describe", "models", "auth", "quota", "history_fork",
 ].map(name=>({name,input_schema:`engine.${name.replaceAll("_","-")}.input.v1`,output_schema:`engine.${name.replaceAll("_","-")}.output.v1`,
 ...(name === "close" || name === "release" ? {cleanup:true} : {})}))};
}
export function requireEngineGateway(): ServiceRequirement {
 return {name:ENGINE_GATEWAY_SERVICE,major_version:1,required:true};
}

/** Callback authority is checked against the caller and captured session. IDs
 * are correlation, not bearer grants. A failed operation never authorizes replay.
 * runLoop returns partial results together with failure; it does not throw away
 * canonical history. An engine can instead own execution and emit its own events.
 */
export function engineGateway(host: RuntimeHost, gatewayID: string, options: Pick<RuntimeHostCallOptions,"signal"> = {}) {
 async function call<T>(method:string, params:object, cleanup = false):Promise<T> {
  return await host.call("host.service.call",{service:ENGINE_GATEWAY_SERVICE,method,params:{gateway_id:gatewayID,...params}},cleanup ? {cleanup:true} : options) as T;
 }
 return {
  emit: (event:EngineEventV1) => call<void>("event",{event}),
  persistReference: (reference:string) => call<void>("persist_reference",{reference}),
  persistTurn: (reference:string,pending:boolean) => call<void>("persist_turn",{reference,pending}),
  /** Only confirmed receipts may settle after cancellation; no new work starts. */
  settleReference: (reference:string) => call<void>("settle_reference",{reference},true),
  settleTurn: (reference:string) => call<void>("settle_turn",{reference},true),
  approval: (approval:EngineApprovalRequestV1) => call<EngineApprovalDecision>("approval",{approval}),
  input: (input:EngineUserInputRequestV1) => call<Readonly<Record<string,readonly string[]>>>("input",{input}),
  runLoop: (messages:readonly DriverMessage[]) => call<EngineRunResult>("run_loop",{messages}),
 };
}
