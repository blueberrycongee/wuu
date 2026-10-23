import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

// Failure cases: a persisted uncertain dispatch is replayed after restart;
// dispatch starts without a durable intent; shutdown accepts late events;
// cancellation is recorded as successful rollback of a remote effect.
const bundled = await build({
  entryPoints: [fileURLToPath(new URL("../src/index.ts", import.meta.url))],
  alias: { "@wuu/plugin-sdk": fileURLToPath(new URL("../../../packages/plugin-sdk/src/index.ts", import.meta.url)) },
  bundle: true, format: "esm", platform: "node", target: "node22.7", write: false,
});
const { createAutomationPlugin } = await import(`data:text/javascript;base64,${Buffer.from(bundled.outputFiles[0].text).toString("base64")}`);
const task = { id: "task-persisted", title: "Inspect", prompt: "Inspect", cron: "* * * * *", timezone: "UTC", mode: "new_thread", workspace_root: "/workspace", recurring: false, paused: false, durable: true, created_at: "2020-01-01T00:00:00Z", next_run_at: "2020-01-01T00:00:00Z" };
const runID = `run-${task.id}-${Date.parse(task.next_run_at) / 1000}`;
const requestID = `automation-${runID}`;
const starting = { id: runID, task_id: task.id, task, request_id: requestID, status: "starting", triggered_at: task.next_run_at };
const tick = () => new Promise(setImmediate);

async function fixture(t, initial, handler) {
  let state = structuredClone(initial);
  const calls = [];
  const host = { async call(method, params, options) {
    calls.push({ method, params });
    if (method === "host.storage.get") return { value: JSON.stringify(state) };
    const result = await handler?.(method, params, options);
    if (result !== undefined) return result;
    if (method === "host.storage.set") { state = JSON.parse(params.value); return {}; }
    if (method === "host.session.create") return { session_id: "session-created" };
    if (method === "host.session.send") return { state: "running", turn_id: "turn-created" };
    throw new Error(`unexpected method ${method}`);
  } };
  const plugin = createAutomationPlugin();
  plugin.initialize({ project_root: "/workspace", workspace_id: "" }, host);
  await plugin.prepare();
  const lifecycle = new AbortController();
  t.after(async () => { lifecycle.abort(); await plugin.shutdown(); });
  return { plugin, host, lifecycle, calls, state: () => state, async activate() { await plugin.activate(host, { signal: lifecycle.signal }); await tick(); } };
}

test("recovery preserves uncertain dispatch without replaying session effects", async t => {
  const f = await fixture(t, { tasks: [task], runs: [starting] });
  await f.activate();
  assert.equal(f.calls.filter(call => call.method.startsWith("host.session.")).length, 0);
  assert.equal(f.state().runs[0].status, "uncertain");
  assert.equal(f.state().runs[0].request_id, requestID);
});

test("a failed durable intent prevents dispatch", async t => {
  t.mock.method(console, "error", () => {});
  const f = await fixture(t, { tasks: [task], runs: [] }, method => {
    if (method === "host.storage.set") throw new Error("storage unavailable");
  });
  await f.activate();
  assert.equal(f.calls.filter(call => call.method.startsWith("host.session.")).length, 0);
});

test("late lifecycle delivery cannot write a disabled generation", async t => {
  const f = await fixture(t, { tasks: [], runs: [starting] });
  f.lifecycle.abort();
  await f.plugin.shutdown();
  const writes = f.calls.filter(call => call.method === "host.storage.set").length;
  await f.plugin.invokeCapability({ capability: "agent.turn.lifecycle", input: { request_id: requestID, state: "completed", thread_id: "session-created", turn_id: "turn-created" } }, f.host);
  assert.equal(f.calls.filter(call => call.method === "host.storage.set").length, writes);
});

test("transport uncertainty is durable and is never an automatic retry", async t => {
  const f = await fixture(t, { tasks: [task], runs: [] }, method => {
    if (method === "host.session.send") throw new Error("connection closed after write");
  });
  await f.activate();
  assert.equal(f.state().runs[0].status, "uncertain");
  const restored = await fixture(t, f.state());
  await restored.activate();
  assert.equal(restored.calls.filter(call => call.method.startsWith("host.session.")).length, 0);
});
