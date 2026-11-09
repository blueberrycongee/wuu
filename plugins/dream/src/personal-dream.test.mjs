import assert from "node:assert/strict";
import { test } from "node:test";
import { build } from "esbuild";
import { fileURLToPath } from "node:url";
const result = await build({ entryPoints: [fileURLToPath(new URL("./index.ts", import.meta.url))], alias: { "@wuu/plugin-sdk": fileURLToPath(new URL("../../../packages/plugin-sdk/src/index.ts", import.meta.url)) }, bundle: true, format: "esm", platform: "node", target: "node22.7", write: false });
const { createDreamPlugin } = await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString("base64")}`);

function fixture(t, memoryAvailable = true) {
  const calls = [], values = new Map(), controller = new AbortController();
  let background;
  const host = { async call(service, params) {
    calls.push({ service, params });
    switch (service) {
      case "host.storage.get": assert.equal(params.scope, "user"); return { value: values.get(params.key) ?? null };
      case "host.storage.set": assert.equal(params.scope, "user"); values.set(params.key, params.value); return {};
      case "host.session.context": return { session_id: params.session_id, product_domain: params.session_id === "coding" ? "coding" : "personal", user_space_id: "local", agent_id: "agent-a" };
      case "host.service.call":
        if (params.service === "host.background") {
          assert.equal(params.method, "start");
          background = plugin.invokeService({ service: params.params.service, method: params.params.method, caller: "dream", params: {} }, host, { signal: controller.signal });
          return {job_id:"dream-background",execution_id:"dream-background"};
        }
        assert.equal(params.service, "memory.records"); assert.equal(params.method, "query"); assert.equal(params.params.session_id, "personal-main");
        if (!memoryAvailable) throw new Error("memory.records unavailable");
        return { version: 1, records: [{ id: "fixture", content: "Prefers tea", revision: 2, source_at: "2026-01-01T00:00:00Z" }] };
      case "host.session.create": return { session_id: "dream-child", created: true };
      case "host.session.send": return { state: "queued", session_id: params.session_id, request_id: params.request_id };
      default: throw new Error(`Unexpected service ${service}`);
    }
  } };
  const plugin = createDreamPlugin();
  t.after(async () => { controller.abort(); await background; await plugin.shutdown(host); });
  const client = async (method, input) => (await plugin.invokeCapability({ capability: "plugin.client.request", input: { method, input } }, host)).output.result;
  const complete = async thread_id => plugin.invokeCapability({ capability: "agent.turn.completed", input: { thread_id, turn_id: "turn", succeeded: true, completed_at: new Date().toISOString() } }, host);
  return { host, plugin, client, complete, calls, controller, values };
}

test("Personal Dream uses scoped records, rejects Coding candidates, and inherits responsible agent session", async t => {
  const f = fixture(t);
  await f.plugin.initialize({ plugin_id: "dream", product_domain: "personal", user_space_id: "local" }, f.host);
  assert.equal(f.calls.length, 0, "initialize must only declare services");
  await f.plugin.prepare(f.host, { signal: f.controller.signal });
  await f.plugin.activate(f.host, { signal: f.controller.signal });
  await f.client("dream.update", { enabled: true, interval_days: 1, min_sessions: 1 });
  await f.complete("coding");
  assert.equal(f.calls.filter(call => call.service === "host.session.create").length, 0);
  await f.complete("personal-main");
  // launch is intentionally asynchronous; shutdown awaits the owned work.
  await new Promise(resolve => setImmediate(resolve));
  const created = f.calls.find(call => call.service === "host.session.create");
  assert.equal(created.params.parent_session_id, "personal-main");
  assert.equal(created.params.context_source, "fork");
  assert(created.params.tool_policy?.allow.includes("personal_memory_query"));
  assert(!created.params.tool_policy.allow.includes("personal_memory_correct"));
  assert(!created.params.tool_policy.allow.includes("personal_memory_forget"));
  const sent = f.calls.find(call => call.service === "host.session.send");
  assert(sent);
  assert(!sent.params.input.prompt.includes("project_memory"));
  assert(!f.calls.some(call => call.service === "host.service.call" && call.params.service === "memory.session"));
  assert.equal(JSON.parse(f.values.get("dream.state")).candidates.coding, undefined);
});

test("Personal Dream fails closed when records are unavailable, with no notebook or file fallback", async t => {
  const f = fixture(t, false);
  await f.plugin.initialize({ plugin_id: "dream", product_domain: "personal", user_space_id: "local" }, f.host);
  assert.equal(f.calls.length, 0, "initialize must only declare services");
  await f.plugin.prepare(f.host, { signal: f.controller.signal });
  await f.plugin.activate(f.host, { signal: f.controller.signal });
  await f.client("dream.update", { enabled: true, interval_days: 1, min_sessions: 1 });
  await f.complete("personal-main");
  const state = await f.client("dream.get");
  assert.equal(state.last_status, "skipped");
  assert.match(state.last_error, /memory.records unavailable/);
  assert(!f.calls.some(call => call.service === "host.session.create"));
});

// Scheduler admission and cancellation are public lifecycle boundaries.
test("Dream timer uses a host-admitted owner callback", async t => {
  const f = fixture(t);
  f.plugin.initialize({plugin_id:"dream",product_domain:"personal",user_space_id:"local"}, f.host);
  await f.plugin.prepare(f.host, {signal:f.controller.signal});
  await f.plugin.activate(f.host, {signal:f.controller.signal});
  assert(f.calls.some(call => call.service === "host.service.call" && call.params.service === "host.background"));
  await assert.rejects(f.plugin.invokeService({service:"dream.scheduler",method:"run",caller:"foreign",params:{}}, f.host, {signal:f.controller.signal}), /owner/);
});
