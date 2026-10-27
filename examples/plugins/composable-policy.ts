// Run directly: wuu plugin dev ./composable-policy.ts
// Select its agent.policy service in the project/session composition. No
// internal imports, product identity checks, or host rebuild are needed.
import { agentPolicyServiceDescriptor, composeAgentPolicies, type AgentPolicyInput, type RuntimePlugin } from "@wuu/plugin-sdk";

const evaluate=composeAgentPolicies([
 // Replace one strategy without rewriting the loop or other strategies.
 input => input.phase === "schedule" ? {action:"serial"} : undefined,
 input => input.phase === "checkpoint_resume" ? {action:"fail"} : undefined,
 input => input.phase === "transport_retry" ? {action:"fail"} : undefined,
 input => input.phase === "retry" ? {
  action: !input.partial_output && !input.tool_work_started && input.attempt === 0 && input.failure?.recovery?.submission_count === 0 && input.failure?.category === "connection" ? "retry" : "fail",
 max_attempts:2,
 } : undefined,
 input => input.phase === "continue" ? {action:input.finish_reason === "continue" && !input.truncated && input.attempt < 2 ? "continue" : "stop"} : undefined,
]);
const plugin:RuntimePlugin={
 initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[agentPolicyServiceDescriptor()]}),
 async invokeService({method,params}) {
  if(method!=="evaluate")throw new Error(`unsupported method ${method}`);
  return evaluate(params as AgentPolicyInput);
 },
};
export default plugin;
