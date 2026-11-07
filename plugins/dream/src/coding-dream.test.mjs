import assert from "node:assert/strict";
import { test } from "node:test";
import { build } from "esbuild";
import { fileURLToPath } from "node:url";
const result = await build({ entryPoints: [fileURLToPath(new URL("./index.ts", import.meta.url))], alias: { "@wuu/plugin-sdk": fileURLToPath(new URL("../../../packages/plugin-sdk/src/index.ts", import.meta.url)) }, bundle: true, format: "esm", platform: "node", target: "node22.7", write: false });
const { createDreamPlugin } = await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString("base64")}`);

function fixture(t, { notebook } = {}) {
  const calls = [], values = new Map(), controller = new AbortController();
  let background;
  const host = { async call(service, params) {
    calls.push({ service, params });
    switch (service) {
      case "host.storage.get": assert.equal(params.scope, "workspace"); return { value: values.get(params.key) ?? null };
      case "host.storage.set": assert.equal(params.scope, "workspace"); values.set(params.key, params.value); return {};
      case "host.session.context": return { session_id: params.session_id, product_domain: "coding", user_space_id: "", agent_id: "", parent_session_id: "", visibility: params.session_id.startsWith("plugin-") || params.session_id === "dream-child" ? "plugin" : "user" };
      case "host.service.call":
        if (params.service === "host.background") {
          background = plugin.invokeService({ service: params.params.service, method: params.params.method, caller: "dream", params: {} }, host, { signal: controller.signal });
          return { job_id: "dream-background", execution_id: "dream-background" };
        }
        if (params.service === "memory.session") { assert.equal(params.method, "read"); return { exists: true, content: "# Project Memory\n\n- Use make test" }; }
        if (params.service === "memory.notebook") {
          assert.equal(params.method, "list");
          if (!notebook) throw new Error("service memory.notebook is unavailable");
          return notebook;
        }
        throw new Error(`Unexpected service ${params.service}`);
      case "host.session.create": return { session_id: "dream-child", created: true };
      case "host.session.send": return { state: "queued", session_id: params.session_id, request_id: params.request_id };
      default: throw new Error(`Unexpected service ${service}`);
    }
  } };
  const plugin = createDreamPlugin();
  t.after(async () => { controller.abort(); await background; await plugin.shutdown(host); });
  const client = async (method, input) => (await plugin.invokeCapability({ capability: "plugin.client.request", input: { method, input } }, host)).output.result;
  const complete = async (thread_id, minute = 0) => plugin.invokeCapability({ capability: "agent.turn.completed", input: { thread_id, turn_id: "turn", succeeded: true, completed_at: new Date(Date.UTC(2026, 0, 1, 0, minute)).toISOString() } }, host);
  const settle = async (state, final_output = "") => {
    const sent = calls.findLast(call => call.service === "host.session.send");
    await plugin.invokeCapability({ capability: "agent.turn.lifecycle", input: { request_id: sent.params.request_id, state, final_output } }, host);
  };
  const start = async () => {
    await plugin.initialize({ plugin_id: "dream", product_domain: "coding" }, host);
    await plugin.prepare(host, { signal: controller.signal });
    await plugin.activate(host, { signal: controller.signal });
    await client("dream.update", { enabled: true, interval_days: 7, min_sessions: 100 });
  };
  const run = async () => { await client("dream.run"); await new Promise(resolve => setImmediate(resolve)); };
  const prompt = () => calls.findLast(call => call.service === "host.session.send").params.input.prompt;
  return { host, plugin, client, complete, settle, start, run, prompt, calls, values };
}

test("Coding Dream ignores plugin-owned sessions, including its own background session", async t => {
  const f = fixture(t);
  await f.start();
  await f.complete("plugin-memory-job");
  await f.complete("user-a", 1);
  await f.run();
  await f.complete("dream-child", 2);
  assert.deepEqual(Object.keys((await f.client("dream.get")).candidates), ["user-a"]);
});

test("Coding Dream reviews a bounded batch of candidates and retains the rest and late arrivals", async t => {
  const f = fixture(t);
  await f.start();
  for (let i = 1; i <= 22; i++) await f.complete(`s${i}`, i);
  await f.run();
  const created = f.calls.find(call => call.service === "host.session.create");
  assert.equal(created.params.parent_session_id, "s22");
  for (let i = 3; i <= 22; i++) assert(f.prompt().includes(`s${i} `), `prompt lists s${i}`);
  assert(!f.prompt().includes("s1 ") && !f.prompt().includes("s2 "));
  await f.complete("s23", 30);
  await f.settle("completed", "Corrected the test command.");
  const state = await f.client("dream.get");
  assert.deepEqual(Object.keys(state.candidates).sort(), ["s1", "s2", "s23"]);
  assert.equal(state.last_status, "completed");
  assert.equal(state.last_summary, "Corrected the test command.");
  assert.deepEqual(JSON.parse(f.values.get("dream.state")).candidates, state.candidates);
});

test("Coding Dream keeps every candidate when a run fails", async t => {
  const f = fixture(t);
  await f.start();
  await f.complete("a", 1); await f.complete("b", 2);
  await f.run();
  await f.settle("failed");
  const state = await f.client("dream.get");
  assert.deepEqual(Object.keys(state.candidates).sort(), ["a", "b"]);
  assert.equal(state.last_status, "failed");
});

test("Coding Dream runs without the optional notebook and reports notebook health when available", async t => {
  const without = fixture(t);
  await without.start();
  await without.complete("a", 1);
  await without.run();
  assert(without.prompt().includes("Use make test"));

  const f = fixture(t, { notebook: {
    version: 1,
    index: { found: true, bytes: 24_000, lines: 12, max_bytes: 25_000, max_lines: 200, truncated: false },
    files: [
      { file: "feedback_tests.md", bytes: 120, indexed: true, reads: 9, verified: "2026-01-01" },
      { file: "orphan.md", bytes: 80, indexed: false, reads: 4 },
    ],
    dangling: ["ghost.md"],
  } });
  await f.start();
  await f.complete("a", 1);
  await f.run();
  for (const file of ["ghost.md", "orphan.md"]) assert(f.prompt().includes(file), `prompt reports ${file}`);
});

test("Coding Dream restores retained candidates after a restart", async t => {
  const f = fixture(t);
  await f.start();
  await f.complete("a", 1);
  await f.run();
  await f.plugin.shutdown(f.host);
  const restarted = createDreamPlugin();
  await restarted.initialize({ plugin_id: "dream", product_domain: "coding" }, f.host);
  await restarted.prepare(f.host, { signal: new AbortController().signal });
  const state = (await restarted.invokeCapability({ capability: "plugin.client.request", input: { method: "dream.get" } }, f.host)).output.result;
  assert.equal(state.last_status, "failed");
  assert.deepEqual(Object.keys(state.candidates), ["a"]);
  await restarted.shutdown(f.host);
});
