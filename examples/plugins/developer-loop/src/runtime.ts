import {
  REQUEST_TRANSFORM_CAPABILITY,
  SYSTEM_PROMPT_SECTION_CAPABILITY,
  runJSONLRuntime,
  type RuntimePlugin,
} from "@wuu/plugin-sdk";

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3,
      capabilities: [
        { id: REQUEST_TRANSFORM_CAPABILITY, kind: "transform", version: 1 },
        { id: SYSTEM_PROMPT_SECTION_CAPABILITY, kind: "transform", version: 1 },
      ],
      tools: [{
        id: "developer-loop-echo",
        description: "Return a short confirmation for developer-loop checks.",
        input_schema: { type: "object", properties: {} },
        display: { label: "Echo", capability: "developer-loop.tool.echo", kind: "acceptance" },
      }],
    };
  },
  invokeCapability({ capability, output }) {
    switch (capability) {
      case SYSTEM_PROMPT_SECTION_CAPABILITY:
        return { output: { text: "Use the developer-loop tool when asked to verify plugin activation." } };
      default:
        return { output };
    }
  },
  executeTool(params, _host, execution) {
    if (params.execution_id && params.execution_id !== execution.executionId) {
      throw new Error("tool execution identity does not match its runtime context");
    }
    return { result: { content: [{ type: "text", text: "developer-loop tool ok" }] } };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
