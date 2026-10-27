// Independent metadata-only consumer; no internal imports or content observer.
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import {
  MODEL_REQUEST_METRICS_CAPABILITY, invokeModel, requireModelInvokeService, requireBackgroundService, startBackground,
  type ModelRequestMetricsInput, type RuntimePlugin,
} from "@wuu/plugin-sdk";

const service = "fixture.metrics-analysis";
const generation = randomUUID();
const records: ModelRequestMetricsInput[] = [];
let mode = "normal", held = 0, cancelled = 0, failures = 0, inferences = 0, activeCallbacks = 0, audit = "";
export default {
  concurrentServices: [service],
  concurrentCapabilities: [MODEL_REQUEST_METRICS_CAPABILITY, "plugin.client.request"],
  initialize(params) {
    audit = join(params.project_root, "metrics-audit.jsonl");
    return { protocol_version: 3, required_services: [requireModelInvokeService(), requireBackgroundService()],
      provided_services: [{ name: service, version: "1.0.0", methods: [{ name: "analyze", input_schema: "fixture.metrics.input.v1", output_schema: "fixture.metrics.output.v1" }] }], capabilities: [
      { id: MODEL_REQUEST_METRICS_CAPABILITY, kind: "observe", version: 1 },
      { id: "plugin.client.request", kind: "decision", version: 1 },
    ] };
  },
  async invokeCapability(params, host, execution) {
    if (params.capability === MODEL_REQUEST_METRICS_CAPABILITY) {
      activeCallbacks++;
      try {
        const input = params.input as ModelRequestMetricsInput;
        records.push(input);
        appendFileSync(audit, JSON.stringify({ generation, event: "received", input }) + "\n");
        const behavior = mode;
        mode = "normal";
        if (behavior === "hold") {
          held++;
          // Deliberately ignore abort settlement: an RPC timeout must not admit
          // another reentrant callback behind this still-active handler.
          await new Promise<void>(() => {
            const cancelledSignal = () => {
              cancelled++;
              appendFileSync(audit, JSON.stringify({ generation, event: "cancelled", request_id: input.request_id }) + "\n");
            };
            if (execution.signal.aborted) cancelledSignal();
            else execution.signal.addEventListener("abort", cancelledSignal, { once: true });
          });
        }
        if (behavior === "infer") {
          await invokeModel(host, { request: { version: 1, model: "claude-fixture", messages: [{ role: "user", content: "pipeline-metrics-self" }], tools: [] }, max_output_tokens: 31 });
          inferences++;
        }
        if (behavior === "background") {
          await startBackground(host, { key: `metrics-${input.request_id}`, service, method: "analyze", timeout_ms: 10000, params: {} });
        }
        if (behavior === "fail") { failures++; throw new Error("private-metrics-observer-error"); }
        return { output: {} };
      } finally { activeCallbacks--; }
    }
    const input = params.input as { method: string; input: { mode?: string } };
    if (input.method === "mode") mode = input.input.mode ?? "normal";
    return { output: { result: { generation, records, held, cancelled, failures, inferences, activeCallbacks } } };
  },
  async invokeService(_params, host) {
    await invokeModel(host, { request: { version: 1, model: "claude-fixture", messages: [{ role: "user", content: "pipeline-metrics-background" }], tools: [] }, max_output_tokens: 31 });
    inferences++;
    return {};
  },
} satisfies RuntimePlugin;
