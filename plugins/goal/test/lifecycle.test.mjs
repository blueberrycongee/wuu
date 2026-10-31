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
    if (method === "host.storage.compare_exchange") {
      if (params.expected !== value) return { swapped: false, value };
      value = params.value;
      return { swapped: true, value };
    }
    if (method === "host.session.inspect") return { turn: { state: "running", turn_id: "turn" } };
    if (method === "host.session.cancel") return { cancelled: true };
    throw new Error(`unexpected method ${method}`);
  } };
  const plugin = createGoalPlugin();
  plugin.initialize({}, host);
  return { plugin, host, calls, state: () => { const parsed = JSON.parse(value); return parsed?.version === 1 ? parsed.goals : parsed; } };
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
    if (fail && method === "host.storage.compare_exchange") throw new Error("storage unavailable");
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
  const f = fixture();
  await f.plugin.prepare();
  await f.host.call("host.storage.set", { value: null });
  const effects = f.calls.length;
  await f.plugin.activate();
  const result = await f.plugin.invokeService({ service: "goal.control", method: "request", params: {
    method: "get_goal", input: { thread_id: "thread" },
  } }, f.host);
  assert.equal(result.goal, null);
  await f.plugin.shutdown();
  assert.equal(f.calls.slice(effects).some(call => call.method.startsWith("host.session.")), false);
});

// Retained foreground generations share one key. Stale writers must fail rather
// than replace newer goals, cancel newer requests, or replay a continuation.
const tool = (plugin, host, tool_id, thread_id, args) => plugin.executeTool({ tool_id, thread_id, arguments: args }, host);
async function replacement(f) {
  const next = createGoalPlugin();
  next.initialize({}, f.host);
  await next.prepare();
  await next.activate();
  return next;
}

test("retained writer cannot erase a new generation's unrelated goal", async () => {
  const f = fixture("{}");
  await f.plugin.activate();
  await tool(f.plugin, f.host, "create_goal", "old", { objective: "Old work" });
  const next = await replacement(f);
  await tool(next, f.host, "create_goal", "new", { objective: "New work" });
  const current = structuredClone(f.state());
  await assert.rejects(tool(f.plugin, f.host, "update_goal", "old", { status: "complete" }), /changed|retired/);
  await f.plugin.shutdown();
  assert.deepEqual(f.state(), current);
  assert.equal(f.calls.some(call => call.method.startsWith("host.session.")), false);
});

test("handoff fences even an empty or already paused state", async () => {
  for (const state of [{}, { thread: { ...goal, status: "paused" } }]) {
    const f = fixture(JSON.stringify(state));
    await f.plugin.activate();
    await replacement(f);
    const effects = f.calls.length;
    await f.plugin.shutdown();
    assert.deepEqual(f.state(), state);
    assert.equal(f.calls.slice(effects).some(call => call.method.startsWith("host.session.")), false);
    await assert.rejects(tool(f.plugin, f.host, "create_goal", "new", { objective: "Stale" }), /stopped|retired/);
  }
});

test("old shutdown cannot cancel the replacement's newly accepted work", async () => {
  const f = fixture(undefined, method => method === "host.session.send" ? { turn_id: "replacement-turn" } : undefined);
  await f.plugin.activate();
  const next = await replacement(f);
  await next.invokeService({ service: "goal.control", method: "request", params: { method: "resume", input: { thread_id: "thread" } } }, f.host);
  const current = structuredClone(f.state()), effects = f.calls.length;
  await f.plugin.shutdown();
  assert.deepEqual(f.state(), current);
  assert.equal(f.calls.slice(effects).some(call => call.method.startsWith("host.session.")), false);
});

test("a continuation accepted across handoff keeps its request identity without replay", async () => {
  let entered, release;
  const started = new Promise(resolve => { entered = resolve; });
  const gate = new Promise(resolve => { release = resolve; });
  const f = fixture("{}", async method => {
    if (method === "host.session.send") { entered(); await gate; return { turn_id: "accepted-late" }; }
  });
  await f.plugin.activate();
  const sending = f.plugin.invokeService({ service: "goal.control", method: "request", params: { method: "create_goal", input: { thread_id: "thread", objective: "Work" } } }, f.host);
  await started;
  const request = f.state().thread.pending.request_id;
  await replacement(f);
  release();
  await assert.rejects(sending, /changed|retired/);
  await f.plugin.shutdown();
  assert.equal(f.state().thread.pending.request_id, request);
  assert.equal(f.state().thread.status, "paused");
  assert.equal(f.calls.filter(call => call.method === "host.session.send").length, 1);
  assert.equal(f.calls.some(call => call.method === "host.session.cancel"), false);
});

test("failed shutdown persistence never cancels a still-durable pending request", async () => {
  let fail = false;
  const f = fixture(undefined, method => {
    if (fail && method === "host.storage.compare_exchange") throw new Error("storage unavailable");
  });
  await f.plugin.activate();
  fail = true;
  const effects = f.calls.length;
  await assert.rejects(f.plugin.shutdown(), /storage unavailable/);
  assert.deepEqual(f.state().thread.pending, goal.pending);
  assert.equal(f.calls.slice(effects).some(call => call.method.startsWith("host.session.")), false);
});

test("activation CAS conflict cannot publish a snapshot read before another writer", async () => {
  let block = false, entered, release;
  const started = new Promise(resolve => { entered = resolve; });
  const gate = new Promise(resolve => { release = resolve; });
  const f = fixture("{}", async method => {
    if (block && method === "host.storage.compare_exchange") { block = false; entered(); await gate; }
  });
  await f.plugin.activate();
  const next = createGoalPlugin();
  next.initialize({}, f.host);
  await next.prepare();
  block = true;
  const activating = next.activate();
  await started;
  await tool(f.plugin, f.host, "create_goal", "late", { objective: "Committed during activation" });
  const current = structuredClone(f.state());
  release();
  await assert.rejects(activating, /changed|retired/);
  await next.shutdown();
  assert.deepEqual(f.state(), current);
});

test("CAS rejects old shutdown racing takeover after its ownership read", async () => {
  let block = false, entered, release;
  const started = new Promise(resolve => { entered = resolve; });
  const gate = new Promise(resolve => { release = resolve; });
  const f = fixture(undefined, async method => {
    if (block && method === "host.storage.compare_exchange") { block = false; entered(); await gate; }
  });
  await f.plugin.activate();
  block = true;
  const closing = f.plugin.shutdown();
  await started;
  await replacement(f);
  const current = structuredClone(f.state());
  release();
  await assert.rejects(closing, /changed|retired/);
  assert.deepEqual(f.state(), current);
  assert.equal(f.calls.some(call => call.method.startsWith("host.session.")), false);
});

test("cancellation already in flight at takeover never targets the replacement request", async () => {
  let block = false, oldRunning = true, entered, release;
  const started = new Promise(resolve => { entered = resolve; });
  const gate = new Promise(resolve => { release = resolve; });
  const f = fixture(undefined, async (method, params) => {
    if (method === "host.session.inspect") {
      const turn = oldRunning ? { state: "running", turn_id: "turn" } : { state: "completed", turn_id: "turn" };
      if (block) { block = false; entered(); await gate; }
      return { turn };
    }
    if (method === "host.session.cancel") {
      assert.equal(params.turn_id, "turn", "never target the replacement turn");
      const cancelled = oldRunning;
      oldRunning = false;
      return { cancelled };
    }
    if (method === "host.session.send") return { turn_id: "replacement-turn" };
  });
  await f.plugin.activate();
  block = true;
  const closing = f.plugin.shutdown();
  await started;
  const next = await replacement(f);
  await next.invokeService({ service: "goal.control", method: "request", params: { method: "resume", input: { thread_id: "thread" } } }, f.host);
  const current = structuredClone(f.state());
  release();
  await assert.rejects(closing, /changed|retired/);
  assert.deepEqual(f.state(), current);
  assert.equal(f.state().thread.pending.turn_id, "replacement-turn");
});

test("activation rejects a CAS acknowledgement for a different document", async () => {
  const f = fixture("{}", method => method === "host.storage.compare_exchange" ? { swapped: true, value: "{}" } : undefined);
  await assert.rejects(f.plugin.activate(), /changed|retired/);
  await f.plugin.shutdown();
  assert.deepEqual(f.state(), {});
  assert.equal(f.calls.some(call => call.method.startsWith("host.session.")), false);
});

test("resume and clear retain an unobserved request while old dispatch can still be accepted", async () => {
  let entered, release, accepted = false, sends = 0;
  const started = new Promise(resolve => { entered = resolve; });
  const gate = new Promise(resolve => { release = resolve; });
  const f = fixture("{}", async method => {
    if (method === "host.session.send") { if (++sends > 1) throw new Error("unexpected replay"); entered(); await gate; accepted = true; return { turn_id: "late-turn" }; }
    if (method === "host.session.inspect") return { turn: accepted ? { state: "running", turn_id: "late-turn" } : null };
  });
  await f.plugin.activate();
  const sending = f.plugin.invokeService({ service: "goal.control", method: "request", params: { method: "create_goal", input: { thread_id: "thread", objective: "Work" } } }, f.host);
  await started;
  const request = f.state().thread.pending.request_id;
  const next = await replacement(f);
  for (const method of ["resume", "clear"]) {
    await assert.rejects(next.invokeService({ service: "goal.control", method: "request", params: { method, input: { thread_id: "thread" } } }, f.host), /not.*observed|uncertain/);
    assert.equal(f.state().thread.pending.request_id, request);
    assert.equal(f.state().thread.status, "paused");
  }
  release();
  await assert.rejects(sending, /changed|retired/);
  assert.equal(f.state().thread.pending.request_id, request);
  assert.equal(f.calls.filter(call => call.method === "host.session.send").length, 1);
  await next.invokeService({ service: "goal.control", method: "request", params: { method: "clear", input: { thread_id: "thread" } } }, f.host);
  assert.equal(f.state().thread.status, "cleared");
});

test("static system prompt contribution remains pure during generation preparation", async () => {
  const f = fixture();
  await f.plugin.prepare();
  const effects = f.calls.length;
  const result = await f.plugin.invokeCapability({ capability: "agent.system_prompt.section", input: {}, output: {} }, f.host);
  assert.equal(typeof result.output.text, "string");
  assert.equal(f.calls.length, effects);
});

test("a proven unavailable-service rejection can resume after service recovery", async () => {
  let unavailable = true;
  const f = fixture("{}", method => {
    if (method === "host.session.send") {
      if (unavailable) throw Object.assign(new Error("session service is unavailable"), { code: "session_send_unavailable" });
      return { turn_id: "recovered-turn" };
    }
  });
  await f.plugin.activate();
  const request = method => f.plugin.invokeService({ service: "goal.control", method: "request", params: { method, input: { thread_id: "thread", ...(method === "create_goal" ? { objective: "Work" } : {}) } } }, f.host);
  await assert.rejects(request("create_goal"), /unavailable/);
  assert.equal(f.state().thread.status, "paused");
  assert.equal(f.state().thread.pending, undefined);
  unavailable = false;
  const result = await request("resume");
  assert.equal(result.goal.status, "active");
  assert.equal(f.state().thread.pending.turn_id, "recovered-turn");
  assert.equal(f.calls.filter(call => call.method === "host.session.send").length, 2);
});

test("retired Goal stops contributing stale context without aborting retained foreground work", async () => {
  const f = fixture("{}");
  await f.plugin.activate();
  await tool(f.plugin, f.host, "create_goal", "thread", { objective: "Old context" });
  await replacement(f);
  const current = structuredClone(f.state());
  for (let step = 0; step < 2; step++) {
    const result = await f.plugin.invokeCapability({ capability: "agent.pre_step", input: { thread_id: "thread" }, output: {} }, f.host);
    assert.deepEqual(result, { output: {} });
  }
  const prompt = await f.plugin.invokeCapability({ capability: "agent.system_prompt.section", input: {}, output: {} }, f.host);
  assert.equal(typeof prompt.output.text, "string");
  await assert.rejects(tool(f.plugin, f.host, "update_goal", "thread", { status: "complete" }), /stopped|retired/);
  assert.deepEqual(f.state(), current);
});
