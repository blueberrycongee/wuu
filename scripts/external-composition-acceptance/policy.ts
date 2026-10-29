import {
  agentPolicyServiceDescriptor, composeAgentPolicies, runJSONLRuntime,
  type AgentPolicyInput, type RuntimePlugin,
} from "@wuu/plugin-sdk";

// An independent implementation, not a decorator around the shipped policy.
// The retained loop and tool consumers must obey this provider's decisions.
const evaluate = composeAgentPolicies([
  input => input.phase === "schedule" ? { action: "serial" } : undefined,
  input => input.phase === "continue" ? { action: "stop", reason: "external acceptance policy" } : undefined,
  () => ({ action: "fail", reason: "external policy prohibits replay" }),
]);

const plugin: RuntimePlugin = {
  initialize: () => ({
    protocol_version: 3, lifecycle_version: 2,
    provided_services: [agentPolicyServiceDescriptor()],
  }),
  async invokeService({ method, params }, _host, execution) {
    if (method !== "evaluate") throw new Error(`unsupported policy method ${method}`);
    if (process.env.WUU_ACCEPTANCE_POLICY_MODE === "error") throw new Error("selected policy unavailable");
    return await evaluate(params as AgentPolicyInput, execution.signal);
  },
};

await runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout });
