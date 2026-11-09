import {
  getProcessEnvironment, readProcess, requireProcessService, startProcess, stopProcess,
  TOOL_SERVICE_DESCRIPTOR, type ToolServiceCallRequest, type ToolServiceFinalizeRequest,
  type RuntimeHost, type RuntimePlugin,
} from "@wuu/plugin-sdk";

const controlEndpoint = "ACCEPTANCE_PROCESS_CONTROL_ENDPOINT";

interface Input {
  action: "start" | "read" | "stop" | "environment" | "preaborted";
  key?: string; command?: string; cwd?: string; process_id?: string;
  offset_bytes?: number; wait_ms?: number; timeout_ms?: number;
}

async function dispatch(input: Input, host: RuntimeHost) {
  switch (input.action) {
    case "environment": return await getProcessEnvironment(host);
    case "start": return {
      environment: await getProcessEnvironment(host),
      process: await startProcess(host, { key: input.key!, command: input.command!, cwd: input.cwd, timeout_ms: input.timeout_ms }),
    };
    case "read": return await readProcess(host, { process_id: input.process_id!, offset_bytes: input.offset_bytes, max_bytes: 65536, wait_ms: input.wait_ms });
    case "stop": return await stopProcess(host, input.process_id!);
    case "preaborted": {
      const controller = new AbortController();
      controller.abort(new Error("acceptance pre-aborted"));
      return await startProcess(host, { key: input.key!, command: input.command! }, { signal: controller.signal });
    }
  }
}

const definition = {
  name: "process_probe", description: "Exercise an admitted process binding in a disposable environment.",
  input_schema: { type: "object", required: ["action"], additionalProperties: false, properties: {
    action: { type: "string", enum: ["start", "read", "stop", "environment", "preaborted"] },
    key: { type: "string" }, command: { type: "string" }, cwd: { type: "string" }, process_id: { type: "string" },
    offset_bytes: { type: "integer" }, wait_ms: { type: "integer" }, timeout_ms: { type: "integer" },
  } },
};

export default {
  concurrentCapabilities: ["plugin.client.request"],
  initialize: () => ({
    protocol_version: 3, lifecycle_version: 2,
    provided_services: [TOOL_SERVICE_DESCRIPTOR],
    required_services: [requireProcessService()],
    capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1 }],
  }),
  async invokeService({ method, params }, host) {
    switch (method) {
      case "list": return { tools: [definition], catalog: [], catalog_prompt: "", system_fragment: "" };
      case "resolve": return {
        found: (params as ToolServiceCallRequest).call.name === definition.name, supports: true,
        metadata: { orchestrator: false, read_only: false, concurrency_safe: true, destructive: false, risk: "high", reason: "acceptance process" },
      };
      case "prepare": return { messages: [] };
      case "freeze": case "unfreeze": return {};
      case "finalize": return { result: (params as ToolServiceFinalizeRequest).result };
      case "execute": {
        const { call } = params as ToolServiceCallRequest;
        if (call.name !== definition.name) throw new Error("tool unavailable in independent provider");
        let value: unknown;
        try { value = { ok: true, value: await dispatch(JSON.parse(call.arguments ?? "{}") as Input, host) }; }
        catch (error) { value = { ok: false, error: error instanceof Error ? error.message : String(error) }; }
        return { result: { content: [{ type: "text", text: JSON.stringify(value) }] } };
      }
      default: throw new Error(`unknown external tools method ${method}`);
    }
  },
  async invokeCapability(params, host) {
    const { method, input } = params.input as { method: string; input: Input };
    if (method === "hold-read") {
      const response = await fetch(controlEndpoint + "/process-held", { method: "POST", body: JSON.stringify({ process_id: input.process_id }) });
      if (!response.ok) throw new Error("process hold gate failed");
      const read = await readProcess(host, { process_id: input.process_id!, max_bytes: 65536 });
      const stopped = await stopProcess(host, input.process_id!);
      return { output: { result: { read, stopped } } };
    }
    return { output: { result: await dispatch(input, host) } };
  },
} satisfies RuntimePlugin;
