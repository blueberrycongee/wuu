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
    if (method === "host.storage.get") return { value };
    const result = await handler?.(method, params);
    if (result !== undefined) return result;
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
