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
  const background = new AbortController();
  let fireTimer;
  t.mock.method(globalThis, "setInterval", callback => { fireTimer = callback; return 1; });
  t.mock.method(globalThis, "clearInterval", () => {});
  let running;
  const host = { async call(method, params, options) {
    calls.push({ method, params });
    if (method === "host.service.call" && params.service === "host.background") {
      running = plugin.invokeService({ service: params.params.service, method: params.params.method, caller: "automation", params: {} }, host, { signal: background.signal });
      return { job_id: "scheduler-job", execution_id: "scheduler-execution" };
    }
    if (method === "host.storage.get") return { value: JSON.stringify(state) };
    const result = await handler?.(method, params, options);
    if (result !== undefined) return result;
    if (method === "host.storage.set") { state = JSON.parse(params.value); return {}; }
    if (method === "host.session.create") return { session_id: "session-created" };
    if (method === "host.session.send") return { state: "running", turn_id: "turn-created" };
    throw new Error(`unexpected method ${method}`);
  } };
  const plugin = createAutomationPlugin();
  plugin.initialize({ plugin_id: "automation", project_root: "/workspace", workspace_id: "" }, host);
  await plugin.prepare();
  const lifecycle = new AbortController();
  t.after(async () => { lifecycle.abort(); background.abort(); await running; await plugin.shutdown(); });
  return { plugin, host, lifecycle, background, calls, fireTimer: () => fireTimer(), state: () => state, async activate() { await plugin.activate(host, { signal: lifecycle.signal }); await tick(); } };
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

test("generation background cancellation stops scheduling without shutting down foreground transport", async t => {
  const now = Date.now();
  const f = await fixture(t, { tasks: [{ ...task, next_run_at: new Date(now + 60_000).toISOString() }], runs: [] });
  await f.activate();
  f.background.abort();
  t.mock.timers.enable({ apis: ["Date"], now: now + 120_000 });
  await tick();
  f.fireTimer();
  await tick();
  assert.equal(f.calls.filter(call => call.method.startsWith("host.session.")).length, 0);
  // An already admitted foreground request retains this provider until release.
  await f.plugin.invokeService({ service: "automation.scheduler", method: "request", params: { method: "automation.create", input: { prompt: "Retained request", cron: "* * * * *" } } }, f.host);
  assert.equal(f.calls.filter(call => call.method === "host.storage.set").length, 1);
});
