// Test-only extension: observe complete normal requests and replay a snapshot.
// No host internals, credentials, manifests, build step, or production policy.
import {
  cancelBackground, inspectBackground, invokeModel,
  readModelInvocation, startBackground, startModelInvocation, streamModel,
  type BackgroundHandle, type LogicalModelRequestV1, type ModelInvocationHandle,
  type ModelRequestObserveInput, type RuntimeHost, type RuntimePlugin,
} from "@wuu/plugin-sdk";

const service = "fixture.context-replay";
const snapshots: ModelRequestObserveInput[] = [];
const phases: Array<{ phase: string; origin: unknown }> = [];
const transformOrigins: Array<{ tag?: string; origin: unknown }> = [];
const jobs: BackgroundHandle[] = [];
const invocations: ModelInvocationHandle[] = [];
let tripwireCalls = 0;
let transformFailures = 0;
let automatic = "";
let generationHost: RuntimeHost;

async function replay(host: RuntimeHost, input: { request?: LogicalModelRequestV1; stream?: boolean; key?: string; timeout_ms?: number; early_exit?: boolean; preserve_limits?: boolean; model_alias?: string }) {
  const request = input.request ?? snapshots.at(-1)?.request;
  if (!request) throw new Error("No observed request to replay");
  const job = await startBackground(host, {
    key: input.key ?? `replay-${jobs.length}`,
    service, method: "replay", timeout_ms: input.timeout_ms ?? 15_000,
    params: { request, stream: input.stream ?? true, early_exit: input.early_exit, preserve_limits: input.preserve_limits, model_alias: input.model_alias },
  });
  jobs.push(job);
  return job;
}

export default {
  concurrentServices: [service],
  initialize() {
    return {
      protocol_version: 3,
      required_services: [
        ...["host.model.invoke", "host.background"].map(name => ({ name, major_version: 1, required: true })),
        { name: "fixture.shadow-compare", major_version: 1, required: false },
      ],
      provided_services: [{ name: service, version: "1.0.0", methods: [{ name: "replay", input_schema: "fixture.replay.input.v1", output_schema: "fixture.replay.output.v1" }] }],
      capabilities: [
        { id: "model.request.observe", kind: "observe", version: 1, error_policy: "isolate" },
        { id: "model.request.transform", kind: "transform", version: 1, error_policy: "isolate" },
        { id: "plugin.client.request", kind: "decision", version: 1 },
      ],
      tools: [{ id: "tripwire", description: "Synthetic acceptance tripwire; must never run during a replay.", input_schema: { type: "object", properties: { anything: {}, nested: { type: "object", properties: {} } } }, activity: { read_only: true, concurrency_safe: true } },
        { id: "raw_result", description: "Return synthetic structured data for full-context acceptance.", input_schema: { type: "object" }, activity: { read_only: true, concurrency_safe: true } }],
    };
  },
  activate(host) { generationHost = host; },
  async invokeCapability(params, host) {
    if (params.capability === "model.request.transform") {
      const { request, origin } = params.input as { request: LogicalModelRequestV1; origin: unknown };
      transformOrigins.push({ tag: [...request.messages].reverse().find(message => message.role === "user")?.content, origin });
      if ([...request.messages].reverse().find(message => message.role === "user")?.content === "pipeline-isolate") {
        transformFailures++;
        throw new Error("fixture-isolated-transform-error");
      }
      if ([...request.messages].reverse().find(message => message.role === "user")?.content !== "pipeline-transform") return { output: {} };
      return { output: { request: { ...request, temperature: 0.23,
        messages: [{ role: "system", content: "fixture:logical-transform" }, ...request.messages],
        provider_options: { ...request.provider_options, temperature: 0.23 },
      } } };
    }
    if (params.capability === "model.request.observe") {
      const input = params.input as ModelRequestObserveInput;
      if (!input.request.messages.some(message => message.role === "user" && message.content?.startsWith("pipeline-"))) return { output: {} };
      phases.push({ phase: input.phase, origin: input.origin });
      if (input.phase === "request") snapshots.push(structuredClone(input));
      if (input.phase === "response" && automatic && input.request.messages.some(message => message.content === automatic)) {
        automatic = "";
        await replay(host, { request: input.request });
      }
      return { output: {} };
    }
    const { method, input } = params.input as { method: string; input: Record<string, unknown> };
    let result: unknown;
    switch (method) {
      case "state": result = { snapshots: snapshots.slice(-2), observed: snapshots.length, phases, transformOrigins, jobs, invocations, tripwireCalls, transformFailures }; break;
      case "arm": automatic = String(input.tag); result = { armed: true }; break;
      case "start": result = await replay(input.generation_scope ? generationHost : host, input); break;
      case "service": result = await host.call("host.service.call", {
        service: "fixture.shadow-compare", method: "compare", params: input,
      }); break;
      case "inspect": result = await inspectBackground(host, input as { job_id: string }); break;
      case "cancel": result = await cancelBackground(host, input as { job_id: string }); break;
      default: throw new Error(`Unknown fixture method: ${method}`);
    }
    return { output: { result } };
  },
  async invokeService(params, host) {
    const input = params.params as { request: LogicalModelRequestV1; stream: boolean; early_exit?: boolean; preserve_limits?: boolean; model_alias?: string };
    const bounded = { request: input.request, model_alias: input.model_alias, max_output_tokens: input.preserve_limits ? undefined : 31, timeout_ms: 12_000 };
    if (!input.stream) return await invokeModel(host, bounded);
    if (input.early_exit) {
      const events = [];
      for await (const event of streamModel(host, bounded)) {
        events.push(event);
        if (event.type === "content_delta") break;
      }
      return { early_exit: true, events };
    }
    const handle = await startModelInvocation(host, bounded);
    invocations.push(handle);
    const stream = [];
    let cursor = 0;
    for (;;) {
      const read = await readModelInvocation(host, { ...handle, cursor, wait_ms: 1000 });
      stream.push(...read.events);
      cursor = read.cursor;
      if (read.done) return { events: stream, result: read.result, error: read.error };
    }
  },
  executeTool(params) {
    if (params.tool_id === "raw_result") {
      const raw = JSON as typeof JSON & { rawJSON(source: string): unknown };
      return { result: { content: [{ type: "text", text: "fixture:structured-result" }], structured_content: { score: raw.rawJSON("1.0") } } };
    }
    tripwireCalls++;
    return { result: { content: [{ type: "text", text: "UNEXPECTED_REPLAY_TOOL_EXECUTION" }] } };
  },
} satisfies RuntimePlugin;
