import {
  DriverOperationError, driverServiceDescriptor, driverTurn, requireDriverTurnServices, runJSONLRuntime,
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
      provided_services: [driverServiceDescriptor("default")],
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
    const settings = await turn.settings();
    if (!settings.can_archive || !settings.can_compact) throw new Error("execution resources unavailable");
    await turn.observe({ kind: "request_context", request_context: { message_count: request.messages.length } });
    checkpoint = { ...checkpoint, state: { phase: "dispatching" } };
    await turn.checkpoint(checkpoint);
    const round = await turn.modelWithTools(request, "evidence");
    if (round.tool_batch_id !== "evidence" || !round.tool_work_started) throw new Error("missing streaming execution receipt");
    const calls = round.messages.flatMap(message => message.tool_calls ?? []);
    const results = await turn.tools(round.tool_batch_id, [...request.messages, ...round.messages], calls);
    const content = results.messages.map(message => message.content ?? "").join("\n");
    const archive = await turn.archiveContext();
    if (archive.head <= 0 || archive.messages.some(message => !message.seq)) throw new Error("archive lost durable addresses");
    const context = await turn.context();
    await turn.observe({ kind: "before_compact", compact: { reason: "manual" } });
    let compactFailure: DriverOperationError | undefined;
    try {
      await turn.compact(context.messages, { reason: "manual", last_successful_tokens: 0, failed_request_tokens: 0, target_total_tokens: 100 });
    } catch (error) {
      if (!(error instanceof DriverOperationError) || error.failure.code !== "summary_output_limit") throw error;
      compactFailure = error;
    }
    if (!compactFailure) throw new Error("expected a classified summary limit");
    await turn.observe({ kind: "after_compact", compact: { reason: "manual", failure: compactFailure.failure } });
    const after = await turn.context();
    if (after.revision !== context.revision || JSON.stringify(after.messages) !== JSON.stringify(context.messages)) throw new Error("failed proposal changed accepted history");
    const state = await turn.contextState();
    const usage = { last_response_total: 13, last_successful_request_tokens: 13, last_response_contract: "fixture", pending_delta: 0, adjustment: "provider" };
    await turn.saveContextState({ ...state, usage });
    if ((await turn.contextState()).usage?.last_response_total !== 13) throw new Error("context estimate did not survive resource calls");
    const receipt = await turn.complete(content, [], { finish_reason: "stop", stop_reason: "evidence_complete" });
    checkpoint = { ...checkpoint, state: { phase: "complete" } };
    return { status: "succeeded", ...receipt, checkpoint };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch(error => {
  console.error(error);
  process.exitCode = 1;
});
