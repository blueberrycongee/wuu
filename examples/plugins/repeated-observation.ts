import type { AgentStepPolicyInput, AgentStepPolicyOutput, RuntimePlugin } from "@wuu/plugin-sdk";

// Opt-in experiment: stop an agent that repeats one identical tool request and
// receives the same observation twice. Repetition can be intentional (polling),
// so this policy is appropriate only for tasks that do not require polling.
function decide(input: AgentStepPolicyInput): AgentStepPolicyOutput {
  const [firstRequest, firstResult, nextRequest, nextResult] = input.messages.slice(-4);
  if (input.step_index < 2 || firstRequest?.role !== "assistant" || firstResult?.role !== "tool"
    || nextRequest?.role !== "assistant" || nextResult?.role !== "tool") return { stop: false };
  const first = firstRequest.tool_calls;
  const next = nextRequest.tool_calls;
  if (first?.length !== 1 || next?.length !== 1 || !first[0].id || !next[0].id
    || firstResult.tool_call_id !== first[0].id || nextResult.tool_call_id !== next[0].id
    || !first[0].name || first[0].name !== next[0].name || first[0].arguments !== next[0].arguments
    || !firstResult.content || firstResult.content !== nextResult.content) return { stop: false };
  return { stop: true, reason: "repeated_observation",
    content: "Stopped because two consecutive identical tool calls returned the same observation. The task may be incomplete; review the repeated result before continuing." };
}

const plugin: RuntimePlugin = {
  initialize() {
    return { protocol_version: 3, capabilities: [
      { id: "agent.step.policy", kind: "decision", version: 1, priority: 10, error_policy: "propagate" },
      { id: "agent.request.transform", kind: "transform", version: 1 },
    ] };
  },
  invokeCapability(params) {
    if (params.capability === "agent.step.policy") return { output: decide(params.input as AgentStepPolicyInput) };
    if (params.capability === "agent.request.transform") return { output: { prepend_system_messages: [
      "An identical tool call with the same observation twice in a row ends this turn. Use each observation to choose the next action; this task does not support polling.",
    ] } };
    throw new Error(`Unsupported capability: ${params.capability}`);
  },
};
export default plugin;
