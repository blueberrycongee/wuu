import {
  driverServiceDescriptor, driverTurn, requireDriverTurnServices, runJSONLRuntime,
  type DriverCheckpoint, type DriverCreateParams, type DriverResumeParams,
  type RuntimePlugin,
} from "@wuu/plugin-sdk";

const id = "fixture.streaming-evidence";
const version = "1.0.0";
type State = { phase: "ready" | "dispatching" | "complete" };
let execution: DriverCreateParams["execution"];
let checkpoint: DriverCheckpoint<State> = {
  contract_version: 1, driver_id: id, driver_version: version, state: { phase: "ready" },
};

const plugin: RuntimePlugin = {
  concurrentServices: ["driver.default"],
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      provided_services: [driverServiceDescriptor("default", version)],
      required_services: requireDriverTurnServices(),
    };
  },
  async invokeService({ method, params }, host) {
    if (method === "descriptor") return { id, version, capabilities: ["tool_use", "checkpoint"] };
    if (method === "create" || method === "resume") {
      const input = params as DriverResumeParams<State>;
      execution = input.execution;
      if (method === "resume") {
        checkpoint = input.checkpoint;
        if (checkpoint.driver_id !== id || checkpoint.driver_version !== version || checkpoint.contract_version !== 1) {
          throw new Error("incompatible checkpoint");
        }
        if (checkpoint.state.phase === "dispatching") throw new Error("interrupted effects require receipt reconciliation");
      }
      return { instance_id: "streaming", checkpoint };
    }
    if (method === "checkpoint") return checkpoint;
    if (method === "shutdown") return {};
    if (method !== "run") throw new Error("unknown method");
    const turn = driverTurn(host, execution);
    const request = await turn.read();
    checkpoint = { ...checkpoint, state: { phase: "dispatching" } };
    await turn.checkpoint(checkpoint);
    const round = await turn.modelWithTools(request, "evidence");
    if (round.tool_batch_id !== "evidence" || !round.tool_work_started) throw new Error("missing streaming execution receipt");
    const calls = round.messages.flatMap(message => message.tool_calls ?? []);
    const results = await turn.tools(round.tool_batch_id, [...request.messages, ...round.messages], calls);
    const content = results.messages.map(message => message.content ?? "").join("\n");
    const receipt = await turn.complete(content);
    checkpoint = { ...checkpoint, state: { phase: "complete" } };
    return { status: "succeeded", ...receipt, checkpoint };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch(error => {
  console.error(error);
  process.exitCode = 1;
});
