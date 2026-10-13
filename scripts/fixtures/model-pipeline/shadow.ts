// Test-only extension: compare a normal model with a custom adapter. Its small
// cache-like middleware demonstrates around-call policy without kernel edits.
import {
  cancelBackground, emitModelEvent, inspectBackground, invokeModel,
  readModelInvocation, startBackground, startModelInvocation, streamModel,
  type BackgroundHandle, type LogicalModelRequestV1, type ModelAdapterInput,
  type ModelRequestObserveInput, type ModelResponseV1, type RuntimeHost,
  type RuntimePlugin,
} from "@wuu/plugin-sdk";

const service = "fixture.shadow-compare";
const adapter = "fixture.shadow";
const nativeAdapter = "fixture.native-delegate";
const jobs: BackgroundHandle[] = [];
const adapterRequests: LogicalModelRequestV1[] = [];
const phases: Array<{ phase: string; origin: unknown }> = [];
const middlewareOrigins: Array<{ tag?: string; origin: unknown }> = [];
let adapterCancelled = 0;
let adapterFinalReceiptHeld = 0;
let observerFailures = 0;
let automatic = "";
let baseline: unknown;
let cached: ModelResponseV1 | undefined;
let middlewareCalls = 0;
let middlewareHits = 0;
let mutationRejections = 0;
let failureUsageRewrites = 0;
let budgetAttempts = 0;
let budgetCallbacks = 0;
let budgetFailure: { code?: string; message: string } | undefined;
const unaryValidation: Array<{ mode: string; failed: boolean; usage?: ModelResponseV1["usage"] }> = [];

async function compare(host: RuntimeHost, input: { request: LogicalModelRequestV1; mode?: string; stream?: boolean }) {
  const job = await startBackground(host, {
    key: `shadow-${jobs.length}`, service, method: "compare", timeout_ms: 15_000,
    params: { ...input, request: { ...input.request, adapter: input.mode === "native" ? nativeAdapter : adapter,
      provider_options: { ...input.request.provider_options, fixture_mode: input.mode ?? "success" } } },
  });
  jobs.push(job);
  return job;
}
async function emitResponse(host: RuntimeHost, response: ModelResponseV1) {
  if (response.content) await emitModelEvent(host, { type: "content_delta", content: response.content });
  await emitModelEvent(host, { type: "done", finish_reason: response.finish_reason, stop_reason: response.stop_reason, usage: response.usage });
}

export default {
  concurrentServices: [service],
  initialize() {
    return {
      protocol_version: 3,
      required_services: ["host.model.invoke", "host.model.emit", "host.background"].map(name => ({ name, major_version: 1, required: true })),
      provided_services: [{ name: service, version: "1.0.0", methods: [{ name: "compare", input_schema: "fixture.shadow.input.v1", output_schema: "fixture.shadow.output.v1" }] }],
      model_adapters: [{ id: adapter, version: 1 }, { id: nativeAdapter, version: 1, mode: "delegate" }],
      capabilities: [
        { id: "model.adapter", kind: "decision", version: 1, error_policy: "propagate" },
        { id: "model.middleware", kind: "decision", version: 1, error_policy: "propagate" },
        { id: "model.request.observe", kind: "observe", version: 1, error_policy: "isolate" },
        { id: "plugin.client.request", kind: "decision", version: 1 },
      ],
    };
  },
  async invokeCapability(params, host, execution) {
    if (params.capability === "model.request.observe") {
      const input = params.input as ModelRequestObserveInput;
      if ([...input.request.messages].reverse().find(message => message.role === "user")?.content === "pipeline-isolate") {
        observerFailures++;
        throw new Error("fixture-isolated-observer-error");
      }
      if (!input.request.messages.some(message => message.role === "user" && message.content?.startsWith("pipeline-"))) return { output: {} };
      phases.push({ phase: input.phase, origin: input.origin });
      if (input.phase === "response" && automatic && input.request.messages.some(message => message.content === automatic)) {
        automatic = "";
        baseline = input.result;
        await compare(host, { request: input.request, stream: true });
      }
      return { output: {} };
    }
    if (params.capability === "model.middleware") {
      const input = params.input as { request: LogicalModelRequestV1; continuation_id: string; stream: boolean; origin: unknown };
      const currentUser = [...input.request.messages].reverse().find(message => message.role === "user")?.content;
      middlewareOrigins.push({ tag: currentUser, origin: input.origin });
      if (currentUser === "pipeline-unary-validation") {
        for (const mode of ["negative-usage", "negative-usage-phase", "success", "malformed-response", "success"]) {
          try {
            const result = await invokeModel(host, { request: { ...input.request, adapter,
              provider_options: { ...input.request.provider_options, fixture_mode: mode } }, max_output_tokens: 31 });
            unaryValidation.push({ mode, failed: false, usage: result.response.usage });
          } catch {
            unaryValidation.push({ mode, failed: true });
          }
        }
        return { output: { response: { content: "fixture:unary-validation-completed", finish_reason: "stop" } } };
      }
      if (currentUser === "pipeline-mutation") {
        let rejected = false;
        try {
          await invokeModel(host, { continuation_id: input.continuation_id, request: { ...input.request,
            messages: [...input.request.messages, { role: "user", content: "fixture-forbidden-mutation" }] } });
        } catch (error) {
          rejected = (error as { code?: string }).code === "invalid_params";
        }
        if (!rejected) throw new Error("Changed continuation input was not rejected");
        mutationRejections++;
      }
      if (currentUser === "pipeline-budget") {
        for (let attempt = 0; attempt < 32; attempt++) {
          budgetAttempts++;
          try {
            await invokeModel(host, { request: { ...input.request, adapter,
              provider_options: { ...input.request.provider_options, fixture_mode: "budget" } }, max_output_tokens: 31 });
          } catch (error) {
            budgetFailure = { code: (error as { code?: string }).code, message: String(error) };
            break;
          }
        }
        return { output: { response: { content: "fixture:budget-exercised", finish_reason: "stop" } } };
      }
      const useCache = [...input.request.messages].reverse().find(message => message.role === "user")?.content?.startsWith("pipeline-middleware");
      middlewareCalls++;
      if (input.stream && !useCache) {
        for await (const event of streamModel(host, { request: input.request, continuation_id: input.continuation_id })) {
          if (currentUser === "pipeline-physical-failure" && event.type === "usage") {
            failureUsageRewrites++;
            await emitModelEvent(host, { ...event, usage: { input_tokens: 777, output_tokens: 888,
              cache_creation_tokens: 0, cache_read_tokens: 0, cache_creation_unknown: false } });
          } else {
            await emitModelEvent(host, event);
          }
        }
        return { output: {} };
      }
      let response: ModelResponseV1;
      if (useCache && cached) {
        middlewareHits++;
        response = { ...cached, content: "fixture:middleware-cache-hit", usage: undefined };
      } else {
        response = (await invokeModel(host, { request: input.request, continuation_id: input.continuation_id })).response;
        if (useCache) {
          response = { ...response, content: `middleware:${response.content}`,
            usage: { input_tokens: 777, output_tokens: 888, cache_creation_tokens: 0, cache_read_tokens: 0, cache_creation_unknown: false } };
          cached = response;
        }
      }
      return { output: { response } };
    }
    if (params.capability === "model.adapter") {
      const input = params.input as ModelAdapterInput;
      adapterRequests.push(structuredClone(input.request));
      const mode = input.request.provider_options?.fixture_mode;
      if (mode === "hold") {
        await new Promise<void>(resolve => {
          if (execution.signal.aborted) return resolve();
          execution.signal.addEventListener("abort", () => resolve(), { once: true });
        });
        adapterCancelled++;
        throw new Error("fixture-adapter-cancelled");
      }
      if (mode === "native") {
        const native = { ...input.request, adapter: "native" };
        const response = (await invokeModel(host, { request: native, max_output_tokens: 31 })).response;
        if (input.stream) { await emitResponse(host, response); return { output: {} }; }
        return { output: { response } };
      }
      if (mode === "budget") budgetCallbacks++;
      if (mode === "negative-usage" || mode === "negative-usage-phase" || mode === "malformed-response") {
        return { output: { response: { content: "fixture:malformed-response", finish_reason: "stop",
          phase: mode === "negative-usage" ? undefined : "fixture-invalid",
          usage: { input_tokens: mode === "malformed-response" ? 3 : -1, output_tokens: 2,
            cache_creation_tokens: 0, cache_read_tokens: 0, cache_creation_unknown: false } } } };
      }
      if (mode === "no-usage" || mode === "budget") {
        const response = { content: "fixture:unknown-cost", finish_reason: "stop" };
        if (input.stream) { await emitResponse(host, response); return { output: {} }; }
        return { output: { response } };
      }
      if (mode === "late-error" || mode === "late-cancel") {
        await emitModelEvent(host, { type: "usage", usage: { input_tokens: 1, output_tokens: 1,
          cache_creation_tokens: 0, cache_read_tokens: 0, cache_creation_unknown: false } });
        await emitModelEvent(host, { type: "done", finish_reason: "stop", usage: { input_tokens: 3, output_tokens: 2,
          cache_creation_tokens: 0, cache_read_tokens: 0, cache_creation_unknown: false } });
        if (mode === "late-cancel") {
          adapterFinalReceiptHeld++;
          await new Promise<void>(resolve => {
            if (execution.signal.aborted) return resolve();
            execution.signal.addEventListener("abort", () => resolve(), { once: true });
          });
          adapterCancelled++;
        }
        throw new Error("fixture-failure-after-done");
      }
      if (mode === "error") {
        if (input.stream) await emitModelEvent(host, { type: "content_delta", content: "fixture:before-error" });
        throw new Error("fixture-adapter-failure");
      }
      const response: ModelResponseV1 = { content: "fixture:shadow-result", finish_reason: "stop", stop_reason: "completed",
        usage: { input_tokens: 3, output_tokens: 2, cache_creation_tokens: 0, cache_read_tokens: 0, cache_creation_unknown: false } };
      if (input.stream) { await emitResponse(host, response); return { output: {} }; }
      return { output: { response } };
    }
    const { method, input } = params.input as { method: string; input: Record<string, unknown> };
    let result: unknown;
    switch (method) {
      case "state": result = { jobs, baseline, adapterRequests: adapterRequests.slice(-8), adapterCancelled, adapterFinalReceiptHeld, observerFailures, phases, middlewareOrigins, middlewareCalls, middlewareHits, mutationRejections, failureUsageRewrites, budgetAttempts, budgetCallbacks, budgetFailure, unaryValidation }; break;
      case "arm": automatic = String(input.tag); result = { armed: true }; break;
      case "start": result = await compare(host, input as { request: LogicalModelRequestV1 }); break;
      case "inspect": result = await inspectBackground(host, input as { job_id: string }); break;
      case "cancel": result = await cancelBackground(host, input as { job_id: string }); break;
      default: throw new Error(`Unknown fixture method: ${method}`);
    }
    return { output: { result } };
  },
  async invokeService(params, host) {
    const input = params.params as { request: LogicalModelRequestV1; stream?: boolean };
    const bounded = { request: input.request, max_output_tokens: 31, timeout_ms: 12_000 };
    if (input.stream === false) return await invokeModel(host, bounded);
    const handle = await startModelInvocation(host, bounded);
    const events = [];
    let cursor = 0;
    for (;;) {
      const read = await readModelInvocation(host, { ...handle, cursor, wait_ms: 1000 });
      events.push(...read.events);
      cursor = read.cursor;
      if (read.done) return { events, result: read.result, error: read.error };
    }
  },
} satisfies RuntimePlugin;
