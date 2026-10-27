import type { ServiceDescriptor, ServiceRequirement, RuntimeHost } from "./index.js";
import type { DriverFailure } from "./driver.js";
import type { ModelToolCallV1 } from "./model.js";

export const AGENT_POLICY_SERVICE = "agent.policy";
export type AgentPolicyPhase = "retry" | "transport_retry" | "checkpoint_resume" | "continue" | "schedule";
export interface AgentPolicyInput {
 readonly phase: AgentPolicyPhase;
 /** Durable protocol-complete items, including reasoning-only checkpoints. */
 readonly completed_messages?: number;
 readonly execution_id?: string;
 readonly max_attempts?: number;
 readonly initial_delay_ms?: number;
 readonly max_delay_ms?: number;
 readonly recovery_hint?: string;
 readonly step_index: number;
 readonly attempt: number;
 readonly failure?: DriverFailure;
 readonly partial_output?: boolean;
 readonly tool_work_started?: boolean;
 readonly finish_reason?: string;
 readonly truncated?: boolean;
 readonly tool_calls?: readonly ModelToolCallV1[];
}
export interface AgentPolicyDecision {
 readonly action: "fail" | "resume" | "recover" | "retry" | "stop" | "continue" | "serial" | "parallel_safe";
 readonly delay_ms?: number;
 /** Required on retry; first decision pins the total attempt budget. */
 readonly max_attempts?: number;
 readonly reason?: string;
 readonly max_parallel?: number;
 readonly recovery_action?: "replay_same_payload" | "wait_then_replay" | "refresh_credential" | "switch_transport";
}
/** Undefined delegates to the next strategy. A decision short circuits; errors
 * and cancellation abort the chain. Strategies must not replay a downstream
 * call or conceal an uncertain external effect. */
export type AgentPolicyStrategy = (input: AgentPolicyInput, signal?: AbortSignal) => AgentPolicyDecision | undefined | Promise<AgentPolicyDecision | undefined>;

export function validateAgentPolicyDecision(input: AgentPolicyInput, decision: AgentPolicyDecision): void {
 const actions: Record<AgentPolicyPhase, readonly string[]> = {
  checkpoint_resume:["fail","resume"], retry:["fail","recover","retry"], transport_retry:["fail","recover"], continue:["stop","continue"], schedule:["serial","parallel_safe"],
 };
 if(input.phase==="schedule"&&decision.action==="parallel_safe"&&(!Number.isSafeInteger(decision.max_parallel)||(decision.max_parallel??0)<1||(decision.max_parallel??0)>64)) throw new Error("parallel schedule requires a capacity between 1 and 64");
 if (!actions[input.phase]?.includes(decision.action)) throw new Error(`invalid ${input.phase} policy action ${decision.action}`);
 if (!Number.isSafeInteger(decision.delay_ms ?? 0) || (decision.delay_ms ?? 0)<0 || (decision.delay_ms ?? 0)>60000 || ((decision.delay_ms ?? 0)!==0&&decision.action!=="retry"&&input.phase!=="transport_retry")) throw new Error("invalid policy retry delay");
 if(decision.action==="retry"&&(!Number.isSafeInteger(decision.max_attempts)||(decision.max_attempts??0)<1)) throw new Error("retry policy requires a positive max_attempts budget");
 const recovery=input.failure?.recovery;
 if(input.phase==="checkpoint_resume"&&decision.action==="resume"&&((input.completed_messages??0)<1||input.tool_work_started||(input.tool_calls?.length??0)>0||input.failure?.category!=="replay_unsafe"||recovery?.stop_reason!=="replay_unsafe"||(input.max_attempts??0)<2||input.attempt>=(input.max_attempts??0)-1)) throw new Error("checkpoint resume requires completed progress and an unexhausted receipt budget without tool effects");
 if(input.phase==="retry"&&decision.action==="retry"&&recovery&&(recovery.stop_reason==="retry_limit"||recovery.stop_reason==="workflow_budget_exceeded"||recovery.budget_dimension)) throw new Error("policy cannot renew an exhausted inference budget");
 if(decision.action==="retry"&&(!recovery||recovery.submission_count!==0)) throw new Error("policy cannot retry an unknown or submitted model operation");
 if((input.phase==="retry"||input.phase==="transport_retry")&&decision.action!=="fail"&&(input.partial_output||input.tool_work_started)) throw new Error("policy cannot replay partial output or accepted tool effects");
 if(input.phase==="transport_retry"&&decision.action==="recover") {
  if(!decision.recovery_action||!["replay_same_payload","wait_then_replay","refresh_credential","switch_transport"].includes(decision.recovery_action)) throw new Error("invalid transport recovery action");
  if(!recovery) throw new Error("transport recovery requires a submission receipt");
 }
}
/** Composition belongs to the selected provider, not a second host registry.
 * A chain can call declared dependent services through the ordinary service API.
 * All strategies observe the same immutable snapshot in the supplied order. */
export function composeAgentPolicies(strategies: readonly AgentPolicyStrategy[]) {
 const ordered=[...strategies];
 return async (input: AgentPolicyInput, signal?: AbortSignal): Promise<AgentPolicyDecision> => {
  for(const strategy of ordered) {
   signal?.throwIfAborted();
   const decision=await strategy(structuredClone(input),signal);
   signal?.throwIfAborted();
   if(decision!==undefined) {validateAgentPolicyDecision(input,decision);return decision;}
  }
  throw new Error(`no strategy decided ${input.phase} policy`);
 };
}
export function agentPolicyServiceDescriptor(): ServiceDescriptor {
 return {name:AGENT_POLICY_SERVICE,version:"1.0.0",methods:[{name:"evaluate",input_schema:"agent.policy.input.v1",output_schema:"agent.policy.decision.v1"}]};
}
export function requireAgentPolicyService(): ServiceRequirement {
 return {name:AGENT_POLICY_SERVICE,major_version:1,required:true};
}
export function agentPolicy(host: RuntimeHost) {
 return {evaluate:async(input:AgentPolicyInput):Promise<AgentPolicyDecision>=>{
  const result=await host.call("host.service.call",{service:AGENT_POLICY_SERVICE,method:"evaluate",params:input}) as AgentPolicyDecision;
  validateAgentPolicyDecision(input,result);return result;
 }};
}
