import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

// Failure cases: corrupt persistence reaches activation after commit; discarded
// candidates write state or cancel a live generation's pending turn; reload
// replays autonomous work; shutdown loses cancellation identities or failures.
const bundled = await build({
  entryPoints: [fileURLToPath(new URL("../src/index.ts", import.meta.url))],
  alias: { "@wuu/plugin-sdk": fileURLToPath(new URL("../../../packages/plugin-sdk/src/index.ts", import.meta.url)) },
  bundle: true, format: "esm", platform: "node", target: "node22.7", write: false,
});
const { createGoalPlugin } = await import(`data:text/javascript;base64,${Buffer.from(bundled.outputFiles[0].text).toString("base64")}`);
const goal = { id: "goal", thread_id: "thread", objective: "Finish", status: "active", tokens_used: 12, time_used_seconds: 3, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", active_since: "2026-01-01T00:00:00Z", pending: { request_id: "accepted-request", turn_id: "turn" } };
function fixture(value = JSON.stringify({ thread: goal }), handler) {
  const calls = [];
  const host = { async call(method, params) {
    calls.push({ method, params });
    const result = await handler?.(method, params);
    if (result !== undefined) return result;
    if (method === "host.storage.get") return { value };
    if (method === "host.storage.set") { value = params.value; return {}; }
    if (method === "host.session.inspect") return { turn: { state: "running", turn_id: "turn" } };
    if (method === "host.session.cancel") return { cancelled: true };
    throw new Error(`unexpected method ${method}`);
  } };
  const plugin = createGoalPlugin();
  plugin.initialize({}, host);
  return { plugin, host, calls, state: () => JSON.parse(value) };
}

test("invalid persisted goals fail preparation without state or session effects", async () => {
  for (const value of ["{", "[]", JSON.stringify({ thread: goal, broken: { tokens_used: "invalid" } })]) {
    const f = fixture(value);
    await assert.rejects(async () => f.plugin.prepare(), /load goals/);
    await f.plugin.shutdown();
    assert.deepEqual(f.calls.map(call => call.method), ["host.storage.get"]);
  }
});

test("discarding a prepared candidate leaves the live generation's pending work untouched", async () => {
  const f = fixture();
  await f.plugin.prepare();
  await f.plugin.shutdown();
  assert.deepEqual(f.calls.map(call => call.method), ["host.storage.get"]);
  assert.deepEqual(f.state().thread, goal);
});

test("activation pauses restored work while retaining its accepted request identity", async () => {
  const f = fixture();
  await f.plugin.prepare();
  assert.equal(f.calls.length, 1);
  await f.plugin.activate();
  assert.equal(f.state().thread.status, "paused");
  assert.deepEqual(f.state().thread.pending, goal.pending);
  assert.equal(f.state().thread.tokens_used, 12);
  assert.equal(f.calls.some(call => call.method.startsWith("host.session.")), false);
  await f.plugin.shutdown();
  assert.equal(f.state().thread.pending, undefined);
  assert.equal(f.state().thread.ignored_interruptions.turn, true);
  assert.equal(f.calls.find(call => call.method === "host.session.inspect").params.request_id, goal.pending.request_id);
});

test("shutdown surfaces storage failure instead of claiming a persisted stop", async () => {
  let fail = false;
  const f = fixture(undefined, method => {
    if (fail && method === "host.storage.set") throw new Error("storage unavailable");
  });
  await f.plugin.prepare();
  await f.plugin.activate();
  fail = true;
  await assert.rejects(f.plugin.shutdown(), /storage unavailable/);
  assert.deepEqual(f.state().thread.pending, goal.pending);
});

// The host keeps the previous generation available while a candidate prepares.
// Both instances use the same durable workspace key, not separate snapshots.
test("activation loads goals changed by the live generation after preparation", async () => {
  const live = fixture("{}", method => method === "host.session.send" ? { turn_id: "new-turn", queue_id: "new-queue" } : undefined);
  await live.plugin.prepare();
  await live.plugin.activate();
  const tool = (tool_id, thread_id, args) => live.plugin.executeTool({ tool_id, thread_id, arguments: args }, live.host);
  await tool("create_goal", "thread", { objective: "Original work" });
  const candidate = createGoalPlugin();
  candidate.initialize({}, live.host);
  await candidate.prepare();
  const discarded = createGoalPlugin();
  discarded.initialize({}, live.host);
  await discarded.prepare();
  await tool("update_goal", "thread", { status: "complete" });
  await live.plugin.invokeService({ service: "goal.control", method: "request", params: {
    method: "create_goal", input: { thread_id: "added", objective: "New work" },
  } }, live.host);
  const latest = live.state();
  const effects = live.calls.length;
  await discarded.shutdown();
  assert.equal(live.calls.length, effects);
  assert.deepEqual(live.state(), latest);
  await candidate.activate();
  assert.deepEqual(live.state().thread, latest.thread);
  assert.deepEqual(live.state().added.pending, latest.added.pending);
  assert.equal(live.state().added.objective, "New work");
  assert.equal(live.state().added.status, "paused");
  const result = await candidate.invokeService({ service: "goal.control", method: "request", params: {
    method: "get_goal", input: { thread_id: "added" },
  } }, live.host);
  assert.equal(result.goal.objective, "New work");
  assert.equal(live.calls.filter(call => call.method === "host.session.send").length, 1);
});

test("activation rejects state corrupted after preparation without writing cached goals", async () => {
  const f = fixture();
  await f.plugin.prepare();
  await f.host.call("host.storage.set", { value: "{broken" });
  const effects = f.calls.length;
  await assert.rejects(f.plugin.activate(), /load goals/);
  await f.plugin.shutdown();
  assert.deepEqual(f.calls.slice(effects).map(call => call.method), ["host.storage.get"]);
});

test("activation honors cleared storage instead of reviving the prepared snapshot", async () => {
  let cleared = false;
  const f = fixture(undefined, method => cleared && method === "host.storage.get" ? { value: null } : undefined);
  await f.plugin.prepare();
  cleared = true;
  const effects = f.calls.length;
  await f.plugin.activate();
  const result = await f.plugin.invokeService({ service: "goal.control", method: "request", params: {
    method: "get_goal", input: { thread_id: "thread" },
  } }, f.host);
  assert.equal(result.goal, null);
  await f.plugin.shutdown();
  assert.deepEqual(f.calls.slice(effects).map(call => call.method), ["host.storage.get"]);
});
