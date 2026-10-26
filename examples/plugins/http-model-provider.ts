/** A minimal external OpenAI-compatible model provider. Select model.adapter@1
 * to this plugin and set the model's options.endpoint. It supports text/tools;
 * richer media, compaction and credential recovery can extend the same adapter
 * operations. Credentials, if needed, are explicit options.api_key here; use a
 * declared credential service for a distributable provider. No host imports. */
import { createModelSubmission, requireModelReceiptService, updateModelSubmission,
  type ModelAdapterInput, type ModelResponseV1, type RuntimePlugin } from "@wuu/plugin-sdk";

const adapter = "example.http-model";
export default {
  initialize() {
    return { protocol_version: 3, lifecycle_version: 2,
      required_services: [requireModelReceiptService()],
      provided_services: [{ name: "model.adapter", version: "1.0.0", methods: [
        { name: "describe", input_schema: "model.adapter.describe.input.v1", output_schema: "model.adapter.describe.output.v1" },
      ] }],
      model_adapters: [{ id: adapter, version: 1, mode: "receipt" }],
      capabilities: [{ id: "model.adapter", kind: "decision", version: 1, error_policy: "propagate" }],
    };
  },
  async invokeService() { return { id: adapter, version: 1, mode: "receipt" }; },
  async invokeCapability(params, host, execution) {
    const input = params.input as ModelAdapterInput;
    if (!input.receipt) throw new Error("A physical model receipt is required");
    const request = input.request;
    const endpoint = request.provider_options?.endpoint;
    if (typeof endpoint !== "string") throw new Error("Set options.endpoint to an OpenAI-compatible endpoint");
    const body = JSON.stringify({ model: request.model, messages: request.messages.map(message => ({
      role: message.role, content: message.content ?? "", ...(message.tool_call_id ? { tool_call_id: message.tool_call_id } : {}),
      ...(message.tool_calls?.length ? { tool_calls: message.tool_calls.map(call => ({ id: call.id, type: "function", function: { name: call.name, arguments: call.arguments } })) } : {}),
    })), ...(request.tools.length ? { tools: request.tools.map(tool => ({ type: "function", function: { name: tool.name, description: tool.description, parameters: tool.input_schema } })) } : {}),
      ...(request.max_tokens ? { max_tokens: request.max_tokens } : {}), stream: false });
    // The durable grant precedes fetch. Never retry an uncertain grant or send.
    const submission = await createModelSubmission(host, input.receipt, { provider: request.provider ?? adapter,
      protocol: "openai.chat", transport: "http", mode: "external", request_bytes: Buffer.byteLength(body) });
    let response: ModelResponseV1;
    try {
      const key = request.provider_options?.api_key;
      const result = await fetch(endpoint, { method: "POST", signal: execution.signal,
        headers: { "content-type": "application/json", ...(typeof key === "string" ? { authorization: `Bearer ${key}` } : {}) }, body });
      if (!result.ok) throw new Error(`Provider returned HTTP ${result.status}`);
      const value = await result.json() as { choices: Array<{ message: { content?: string; tool_calls?: Array<{ id: string; function: { name: string; arguments: string } }> }; finish_reason?: string }>; usage?: { prompt_tokens: number; completion_tokens: number } };
      const choice = value.choices[0];
      if (!choice) throw new Error("Provider response has no choice");
      response = { content: choice.message.content ?? "", finish_reason: choice.finish_reason === "tool_calls" ? "tool_calls" : "stop",
        tool_calls: choice.message.tool_calls?.map(call => ({ id: call.id, name: call.function.name, arguments: call.function.arguments })),
        usage: value.usage ? { input_tokens: value.usage.prompt_tokens, output_tokens: value.usage.completion_tokens } : undefined };
    } catch (error) {
      await updateModelSubmission(host, input.receipt, { id: submission.id, outcome: "failed",
        failure: { origin: "network", category: execution.signal.aborted ? "canceled" : "unknown", confidence: "low" } }, true);
      throw error;
    }
    // A lost settlement response is not replayed and cannot report success.
    await updateModelSubmission(host, input.receipt, { id: submission.id, outcome: "succeeded", usage: response.usage }, true);
    return { output: { response } };
  },
} satisfies RuntimePlugin;
