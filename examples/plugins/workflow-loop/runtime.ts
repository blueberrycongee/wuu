import {
  driverServiceDescriptor, driverTurn, requireDriverTurnServices, runJSONLRuntime,
  type DriverCheckpoint, type DriverCreateParams, type DriverExecution,
  type DriverInstanceParams, type DriverResumeParams, type DriverTurnInput,
  type ModelMessageV1, type ModelToolCallV1, type RuntimePlugin,
} from "@wuu/plugin-sdk";

const id = "example.evidence-workflow";
const version = "1.0.0";
const profile = process.env.WUU_WORKFLOW_PROFILE ?? "workflow";

type State = {
  phase: "ready" | "planned" | "executing" | "synthesis" | "complete";
  request?: DriverTurnInput;
  calls?: readonly ModelToolCallV1[];
};
type Instance = {
  execution: DriverExecution;
  checkpoint: DriverCheckpoint<State>;
  running: boolean;
};
const instances = new Map<string, Instance>();
let sequence = 0;
function checkpoint(state: State): DriverCheckpoint<State> {
  return { contract_version: 1, driver_id: id, driver_version: version, state };
}

const plugin: RuntimePlugin = {
  concurrentServices: [`driver.${profile}`],
  initialize() {
    return {
      protocol_version: 3, lifecycle_version: 2,
      provided_services: [driverServiceDescriptor(profile, version)],
      required_services: requireDriverTurnServices(),
    };
  },
  async invokeService({ method, params }, host, execution) {
    if (method === "descriptor") return { id, version, capabilities: ["workflow", "tool_use", "checkpoint"] };
    if (method === "create" || method === "resume") {
      const input = params as DriverResumeParams<State> | DriverCreateParams;
      if (!input.execution?.execution_id) throw new Error("execution_id is required");
      let restored = checkpoint({ phase: "ready" });
      if (method === "resume") {
        restored = (input as DriverResumeParams<State>).checkpoint;
        if (restored.contract_version !== 1 || restored.driver_id !== id || restored.driver_version !== version) {
          throw new Error("incompatible workflow checkpoint");
        }
        if (!["ready", "planned", "executing", "synthesis", "complete"].includes(restored.state?.phase)) {
          throw new Error("unknown workflow phase");
        }
      }
      const instance_id = `workflow-${++sequence}`;
      instances.set(instance_id, { execution: input.execution, checkpoint: restored, running: false });
      return { instance_id, checkpoint: restored };
    }
    const { instance_id } = params as DriverInstanceParams;
    const instance = instances.get(instance_id);
    if (method === "shutdown") { instances.delete(instance_id); return {}; }
    if (!instance) throw new Error("unknown workflow instance");
    if (method === "checkpoint") return instance.checkpoint;
    if (method !== "run") throw new Error("unknown workflow method");
    if (instance.running) throw new Error("workflow is already running");
    instance.running = true;
    const turn = driverTurn(host, instance.execution);
    const save = async (state: State) => {
      execution.signal.throwIfAborted();
      const next = checkpoint(state);
      await turn.checkpoint(next);
      instance.checkpoint = next;
    };
    try {
      const current = await turn.read();
      let state = instance.checkpoint.state;
      if (state.phase === "complete") state = { phase: "ready" };
      if (state.phase === "ready") {
        const plan = await turn.model({
          ...current,
          messages: [...current.messages, { role: "user", content: "Gather the evidence needed for this task in one batch of tools. A separate synthesis stage will write the answer after those results arrive.", hidden: true }],
        });
        const calls = plan.messages.flatMap(message => message.tool_calls ?? []);
        state = { phase: "planned", request: { ...current, messages: [...current.messages, ...plan.messages] }, calls };
        await save(state);
      }
      if (!state.request || !state.calls) throw new Error("workflow checkpoint is missing its planned request");
      let request = state.request;
      if (state.phase === "executing") {
        // Durable tool receipts let recovery continue without replaying effects.
        const results = state.calls.map(call => current.messages.find(message =>
          message.role === "tool" && message.tool_call_id === call.id && message.tool_invocation_id));
        if (results.some(result => !result)) throw new Error("Interrupted tool batch has uncertain effects; reconcile its durable receipts before resuming.");
        request = { ...request, messages: [...request.messages, ...results as ModelMessageV1[]] };
        await save({ phase: "synthesis", request, calls: state.calls });
      } else if (state.phase === "planned") {
        if (state.calls.length) {
          await save({ ...state, phase: "executing" });
          const result = await turn.tools("evidence", request.messages, state.calls);
          request = { ...request, messages: [...request.messages, ...result.messages] };
        }
        await save({ phase: "synthesis", request, calls: state.calls });
      }
      // Keep only the current task and its evidence in active model context.
      // The context service preserves the full transcript before committing this
      // policy's replacement. Reading the committed context also makes a retry
      // after a lost commit reply harmless: an already focused window stays put.
      const context = await turn.context();
      if (!context.can_replace) throw new Error("workflow requires durable context replacement");
      const taskIndex = context.messages.findLastIndex(message => message.role === "user" && !message.hidden);
      if (taskIndex < 0) throw new Error("workflow context has no user task");
      const expectedTask = request.messages.findLast(message => message.role === "user" && !message.hidden);
      const task = context.messages[taskIndex];
      if (!expectedTask || (expectedTask.seq ? task.seq !== expectedTask.seq : expectedTask.client_id ? task.client_id !== expectedTask.client_id : task.content !== expectedTask.content)) {
        throw new Error("New user input cannot be merged into this interrupted workflow; finish or explicitly restart it.");
      }
      const focused = [...context.messages.filter(message => message.role === "system"), ...context.messages.slice(taskIndex)];
      const active = focused.length < context.messages.length
        ? await turn.replaceContext(context.revision, focused) : context;
      request = { ...request, messages: active.messages };
      await save({ phase: "synthesis", request, calls: state.calls });
      // This workflow has a fixed second stage; it does not hand control to the
      // multi-round loop and does not allow the synthesis model to execute tools.
      const answer = await turn.model({ ...request, tools: [], force_tool_name: "" });
      const content = answer.messages.filter(message => message.role === "assistant").map(message => message.content ?? "").join("\n");
      if (!content.trim()) throw new Error("synthesis returned no answer");
      const receipt = await turn.complete(content);
      instance.checkpoint = checkpoint({ phase: "complete" });
      return { status: "succeeded", ...receipt, checkpoint: instance.checkpoint };
    } finally {
      instance.running = false;
    }
  },
  shutdown() { instances.clear(); },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
