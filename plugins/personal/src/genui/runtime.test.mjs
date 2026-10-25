import { createHash } from "node:crypto";
import assert from "node:assert/strict";
import { test } from "node:test";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { createInterface } from "node:readline";
import { build } from "esbuild";
import { mkdtemp, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { fileURLToPath, pathToFileURL } from "node:url";
import { join } from "node:path";

// Exercise shipped JSONL runtime, owned history projection and authenticated
// client dispatch. Only host persistence/session services and the model output
// are synthetic; these results are not evidence of a live model call.
test("shipped Personal GenUI model-output to history to owner action survives restart", async t => {
  const root = fileURLToPath(new URL("../../../../", import.meta.url));
  const scratch = await mkdtemp(join(tmpdir(), "twin-genui-runtime-")); t.after(() => rm(scratch, { recursive: true, force: true }));
  await build({ entryPoints: [join(root, "plugins/personal/src/genui/fixtures.ts")], outfile: join(scratch, "fixtures.mjs"), bundle: true, platform: "node", format: "esm" });
  const { fixtures } = await import(pathToFileURL(join(scratch, "fixtures.mjs")));
  const storage = new Map(), contexts = {}, deliveries = new Map(), receipts = new Map(), frames = [];
  const genuiToolName = `plugin_personal_present_interactive_${createHash("sha256").update("personal\0present_interactive_answer\0" + "0").digest("hex").slice(0, 16)}`;
  let historyToolName = genuiToolName;
  let publishedResult, main, mainAgent, loseAck = false, processes = [];
  const source = { seq: 2, role: "assistant", content: "", tool_calls: JSON.stringify([{ id: "model-call-one", type: "function", function: { name: "present_interactive_answer", arguments: "{}" } }]) };
  const host = async frame => {
    const { service, params: p } = frame.params;
    switch (service) {
      case "host.storage.get": return { value: storage.get(p.key) ?? null };
      case "host.storage.keys": return { keys: [...storage.keys()] };
      case "host.storage.compare-exchange": {
        if ((storage.get(p.key) ?? null) !== p.expected) return { swapped: false, value: storage.get(p.key) ?? null };
        storage.set(p.key, p.value); return { swapped: true, value: p.value };
      }
      case "host.session.create": {
        const session_id = `session-${p.request_id}`; contexts[session_id] ??= { ...p, session_id, state: "idle" }; return { session_id, created: true };
      }
      case "host.session.context": return contexts[p.session_id] ?? { session_id: p.session_id, product_domain: "coding" };
      case "host.session.inspect": return { session: contexts[p.session_id], receipt: receipts.get(p.request_id) };
      case "host.session.history.read": return { session_id: p.session_id, records: p.session_id === main ? [{ seq: 1, role: "user", origin: "user", content: "Synthetic checklist request" }, source, ...(publishedResult ? [{ seq: 3, role: "tool", name: historyToolName, tool_call_id: "model-call-one", tool_result: JSON.stringify(publishedResult) }] : [])] : [], head_seq: 3, snapshot_seq: 3, has_more: false };
      case "host.session.send": {
        assert.equal(p.session_id, main); assert.equal(p.disable_tools, p.cause === "personal.chat" ? undefined : true);
        assert.equal(p.user_input_request_id, p.cause === "personal.chat" ? "owner-chat" : "owner-submit");
        assert.match(frame.params.execution_id, /owner-/);
        if (!deliveries.has(p.request_id)) {
          deliveries.set(p.request_id, p);
          receipts.set(p.request_id, { request_id: p.user_input_request_id, host_request_id: p.request_id, session_id: main, user_space_id: "fixture-space", product_domain: "personal", agent_id: mainAgent, plugin_id: "personal", receipt_id: "fixture-receipt", receipt_state: "accepted", execution_state: "queued", revision: 1 });
        }
        if (loseAck) { loseAck = false; throw new Error("synthetic ACK loss"); }
        return { state: "queued", session_id: p.session_id, queue_id: p.request_id, receipt: receipts.get(p.request_id) };
      }
      case "host.client.invalidate": return { delivered: true };
      default: throw new Error(`unexpected host service ${service}`);
    }
  };
  async function start(domain = "personal") {
    const child = spawn(process.execPath, [join(root, "internal/plugin/bundled/personal/runtime.mjs")], { stdio: ["pipe", "pipe", "pipe"] }); processes.push(child);
    let seq = 0, stderr = ""; const pending = new Map(); child.stderr.on("data", b => { stderr += b; });
    const send = frame => { frames.push({ direction: "host", frame }); child.stdin.write(`${JSON.stringify(frame)}\n`); };
    createInterface({ input: child.stdout }).on("line", line => {
      const frame = JSON.parse(line); frames.push({ direction: "plugin", frame });
      if (frame.method) void host(frame).then(result => send({ id: frame.id, result }), error => send({ id: frame.id, error: { code: "fixture", message: error.message } }));
      else { const p = pending.get(frame.id); if (p) { pending.delete(frame.id); frame.error ? p.reject(new Error(frame.error.message)) : p.resolve(frame.result); } }
    });
    const call = (method, params) => new Promise((resolve, reject) => {
      const id = `client-${++seq}`, timer = setTimeout(() => reject(new Error(`timeout ${method} ${stderr}`)), 5000);
      pending.set(id, { resolve: v => { clearTimeout(timer); resolve(v); }, reject: e => { clearTimeout(timer); reject(e); } }); send({ id, method, params });
    });
    const definition = await call("initialize", { protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 1, plugin_id: "personal", plugin_root: scratch, project_root: scratch, wuu_home: scratch, workspace_state_dir: scratch, user_state_dir: scratch, user_space_id: "fixture-space", product_domain: domain, supported_host_services: ["host.service.call", "host.session.context", "host.storage.keys", "host.storage.get", "host.storage.compare_exchange"] });
    await call("activate");
    return { call, definition, client: (method, input = {}) => call("capability.invoke", { capability: "plugin.client.request", input: { method, input: { agent_id: mainAgent, ...input } }, execution_id: `owner-${seq}` }).then(r => r.output.result), close: async () => { await call("shutdown"); const exited = once(child, "exit"); child.stdin.end(); await exited; assert.equal(child.exitCode, 0, stderr); } };
  }
  t.after(async () => {
    for (const child of processes) if (child.exitCode === null) child.kill();
    const output = process.env.GENUI_RUNTIME_OUTPUT || join(root, ".tmp/personal-genui"); await mkdir(output, { recursive: true });
    await writeFile(join(output, "shipped-runtime-protocol.jsonl"), frames.map(f => JSON.stringify(f)).join("\n") + "\n");
  });
  let runtime = await start(); assert(runtime.definition.tools.some(t => t.id === "present_interactive_answer"));
  const created = await runtime.client("agents.ensure_default"); main = created.agent.main_session_id; mainAgent = created.agent.agent_id;
  const tool = await runtime.call("tool.execute", { tool_id: "present_interactive_answer", tool: genuiToolName, turn_id: "host-turn-one", arguments: { spec: fixtures.checklist }, session_id: main, thread_id: main, call_id: "model-call-one", execution_id: "model-execution" });
  assert.equal(tool.result.structured_content.type, "personal.genui");
  publishedResult = tool.result;
  const history = await runtime.client("agents.history");
  const interactive = history.records.find(r => r.seq === 3); assert.equal(interactive.ui_parts.length, 1); assert.match(interactive.content, /示例清单/);
  const originalResult = structuredClone(publishedResult);
  publishedResult.structured_content.schemaVersion = 99;
  const unsupported = await runtime.client("agents.history");
  assert.equal(unsupported.records.find(r => r.seq === 3)?.content, fixtures.checklist.fallbackText);
  assert.equal(unsupported.records.find(r => r.seq === 3)?.ui_parts, undefined);
  publishedResult = structuredClone(originalResult);
  const surfaceKey = [...storage.keys()].find(key => key.endsWith(publishedResult.structured_content.surfaceId));
  const storedSurface = storage.get(surfaceKey); storage.delete(surfaceKey);
  await runtime.close(); runtime = await start();
  const missing = await runtime.client("agents.history");
  assert.equal(missing.records.find(r => r.seq === 3)?.content, fixtures.checklist.fallbackText);
  historyToolName = "worker_private_tool";
  assert.equal((await runtime.client("agents.history")).records.find(r => r.seq === 3), undefined);
  historyToolName = genuiToolName;
  publishedResult.content = [{ type: "text", text: "unverified raw worker log" }];
  assert.equal((await runtime.client("agents.history")).records.find(r => r.seq === 3), undefined);
  publishedResult = originalResult; storage.set(surfaceKey, storedSurface);
  const surface = interactive.ui_parts[0].surface; const state = structuredClone(surface.state); state.items[0].done = true;
  const request = { surfaceId: surface.surfaceId, actionId: "save", expectedRevision: 0, eventId: "owner-save", state };
  const saved = await runtime.client("genui.action", { request }); assert.equal(saved.surface.revision, 1); assert.equal(deliveries.size, 0);
  await assert.rejects(() => runtime.call("service.invoke", { service: "personal.agents", method: "genui.action", params: { agent_id: mainAgent, request }, execution_id: "forged-service" }), /authenticated|owner/);
  await assert.rejects(() => runtime.client("genui.action", { request, owner: "fake" }), /unknown|unexpected|invalid/);
  await runtime.close(); runtime = await start();
  const restored = await runtime.client("genui.read", { surface_id: surface.surfaceId }); assert.equal(restored.surface.state.items[0].done, true);
  loseAck = true;
  const submitted = await runtime.client("genui.action", { request: { ...request, expectedRevision: 1, eventId: "owner-submit", actionId: "submit" } });
  assert.equal(submitted.delivery, "pending"); assert.equal(deliveries.size, 0);
  const admitted = await runtime.client("genui.retry", { surface_id: surface.surfaceId });
  assert.equal(admitted.delivery, "accepted"); assert.equal(deliveries.size, 1);
  await runtime.close(); runtime = await start();
  const recovered = await runtime.client("genui.retry", { surface_id: surface.surfaceId }); assert.equal(recovered.delivery, "accepted"); assert.equal(deliveries.size, 1);
  const sent = [...deliveries.values()][0]; assert.equal(JSON.parse(sent.input.context_blocks[0].content).state.items[0].done, true);
  const second = await runtime.call("tool.execute", { tool_id: "present_interactive_answer", tool: genuiToolName, turn_id: "host-turn-two", arguments: { spec: fixtures.checklist }, session_id: main, thread_id: main, call_id: "model-call-two", execution_id: "model-execution" });
  const fresh = second.result.meta.personal_genui;
  const edited = structuredClone(fresh.state); edited.items[0].done = true;
  await runtime.client("genui.sync", { request: { surfaceId: fresh.surfaceId, expectedRevision: 0, state: edited } });
  assert.equal(deliveries.size, 1);
  const chat = { request_id: "owner-chat", prompt: "Read my current choices", genui_context: [{ surfaceId: fresh.surfaceId, state: edited }] };
  await runtime.client("agents.send", chat);
  const normal = [...deliveries.values()].find(p => p.cause === "personal.chat");
  assert.equal(JSON.parse(normal.input.context_blocks[0].content).surfaces[0].state.items[0].done, true);
  await runtime.close(); runtime = await start();
  assert.equal((await runtime.client("genui.read", { surface_id: fresh.surfaceId })).surface.state.items[0].done, true);
  await runtime.client("agents.send", chat);
  const changed = structuredClone(chat); changed.genui_context[0].state.items[0].done = false;
  await assert.rejects(() => runtime.client("agents.send", changed), /request_id_conflict/);
  assert.equal(deliveries.size, 2);
  await runtime.close();
  const coding = await start("coding"); assert.equal(coding.definition.tools.length, 0); await coding.close();
});
