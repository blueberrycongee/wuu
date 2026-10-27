import assert from "node:assert/strict";
import test from "node:test";
import { driverTurn, type DriverExecution, type DriverTurnInput } from "./driver.js";
import type { RuntimeHost } from "./index.js";

test("driver preserves the admitted model selection fence through transforms and dispatch", async () => {
  const sent: Record<string, unknown>[] = [];
  let revision = 1;
  const host = { call: async (_method: string, args: { method: string; params: Record<string, unknown> }) => {
    if (args.method === "read") return { version: 1, provider: "selected", model: `model-${revision}`, messages: [], tools: [], model_selection_revision: revision };
    if (args.method === "admit_input") { revision = 2; return { messages: [], context: {}, decision: { stop: false } }; }
    if (args.method === "transform_request") return args.params.request;
    if (args.method === "model") { sent.push(args.params.request as Record<string, unknown>); return { messages: [] }; }
    throw new Error(`unexpected method ${args.method}`);
  }} as unknown as RuntimeHost;
  const turn = driverTurn(host, { execution_id: "active" } as DriverExecution);
  const first = await turn.read();
  await turn.admitInput();
  const second: DriverTurnInput = await turn.transformRequest(await turn.read());
  await turn.model(second);
  assert.equal(first.model_selection_revision, 1);
  assert.equal(sent[0].model_selection_revision, 2);
  assert.equal(sent[0].model, "model-2");
});
