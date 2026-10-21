import assert from "node:assert/strict";
import { test } from "node:test";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { createInterface } from "node:readline";
import { mkdtemp, mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { once } from "node:events";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

// Execute the generated shipped entry point and public JSONL transport. The
// Failure cases: absent optional Memory, forged authority, stale correction, lost
// restart data, forgotten-source resurrection, and Coding-tool exposure.
// The fixture supplies only host storage/session services, never Personal authority
// or memory behavior: the shipped Personal runtime creates its own main binding.
const root = fileURLToPath(new URL("../../../", import.meta.url));
const executable = join(root, "internal/plugin/bundled/personal/runtime.mjs");

test("Personal memory shipped runtime persists bound corrections and forget across process replacement", async t => {
  const scratch = await mkdtemp(join(tmpdir(), "wuu-personal-memory-runtime-"));
  const output = process.env.WUU_MEMORY_E2E_OUTPUT ?? join(root, ".tmp/e2e/personal-memory");
  await mkdir(output, { recursive: true });
  t.after(() => rm(scratch, { recursive: true, force: true }));
  const storage = new Map(), frames = [], active = new Set(), locks = new Set();
  const contexts = {};
  let mainSession, mainAgent;
  const host = async frame => {
    assert.equal(frame.method, "host.service.call");
    const { service, method, params } = frame.params;
    const key = `${params?.scope}:${params?.key}`;
    switch (service) {
      case "host.session.create": {
        const session_id = `session-${params.request_id}`;
        const created = !contexts[session_id]; contexts[session_id] ??= { ...params, session_id, state: "idle" };
        return { session_id, created };
      }
      case "host.session.inspect": return { session: contexts[params.session_id] };
      case "host.session.history.read": return { session_id: params.session_id, records: [], head_seq: 0, snapshot_seq: 0, has_more: false };
      case "host.session.context": return contexts[params.session_id] ?? { session_id: params.session_id, product_domain: "coding" };
      case "host.storage.keys": return { keys: [...storage.keys()].map(key => key.slice("user:".length)) };
      case "host.storage.get": return { value: storage.get(key) ?? null };
      case "host.storage.compare-exchange": {
        const current = storage.get(key) ?? null;
        if (current !== params.expected) return { swapped: false, value: current };
        storage.set(key, params.value); return { swapped: true, value: params.value };
      }
      case "host.file-lock":
        if (method === "acquire") { assert.equal(params.root, "workspace_state"); assert.equal(locks.size, 0); locks.add("lock"); return { lock_id: "lock" }; }
        assert.equal(method, "release"); locks.delete(params.lock_id); return { released: true };
      default: throw new Error(`unexpected service ${service}`);
    }
  };
  async function start(pluginID = "personal") {
    const child = spawn(process.execPath, [join(root, `internal/plugin/bundled/${pluginID}/runtime.mjs`)], { stdio: ["pipe", "pipe", "pipe"] }); active.add(child);
    let seq = 0, stderr = ""; const pending = new Map();
    child.stderr.on("data", buffer => { stderr += buffer; });
    const send = frame => { frames.push({ direction: "host", frame }); child.stdin.write(`${JSON.stringify(frame)}\n`); };
    child.on("exit", () => { active.delete(child); for (const entry of pending.values()) entry.reject(new Error(stderr || "runtime closed")); pending.clear(); });
    createInterface({ input: child.stdout }).on("line", line => {
      const frame = JSON.parse(line); frames.push({ direction: "plugin", frame });
      if (frame.method) host(frame).then(result => send({ id: frame.id, result }), error => send({ id: frame.id, error: { code: "fixture_error", message: error.message } }));
      else {
        const entry = pending.get(frame.id); if (!entry) return; pending.delete(frame.id);
        if (frame.error) entry.reject(new Error(frame.error.message)); else entry.resolve(frame.result);
      }
    });
    const call = (method, params) => new Promise((resolve, reject) => {
      const id = `request-${++seq}`, timer = setTimeout(() => { pending.delete(id); reject(new Error(`timeout ${method}: ${stderr}`)); }, 10_000);
      pending.set(id, { resolve: result => { clearTimeout(timer); resolve(result); }, reject: error => { clearTimeout(timer); reject(error); } });
      send({ id, method, params });
    });
    const definition = await call("initialize", { protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 1, plugin_id: pluginID, plugin_root: scratch, project_root: scratch, wuu_home: scratch, workspace_state_dir: join(scratch, "state"), product_domain: "personal", user_space_id: "local", supported_host_services: ["host.service.call", "host.session.context", "host.storage.keys", "host.storage.get", "host.storage.compare_exchange"] });
    if (pluginID === "personal") assert.equal(definition.provided_services.filter(service => service.name === "memory.records").length, 1);
    else { assert.equal(definition.provided_services?.length ?? 0, 0); assert.equal(definition.tools?.length ?? 0, 0); assert.equal(definition.capabilities?.length ?? 0, 0); }
    assert(!definition.provided_services?.some(service => service.name === "memory.notebook"));
    await call("activate");
    return {
      call,
      tool: (tool_id, args, session_id = mainSession) => call("tool.execute", { tool_id, arguments: args, session_id, thread_id: session_id, cwd: scratch, call_id: `tool-${seq}`, execution_id: `execution-${seq}` }).then(result => JSON.parse(result.result.content[0].text)),
      service: (method, params, session_id = mainSession) => call("service.invoke", { service: "memory.records", method, caller: "personal", params: { session_id, ...params }, execution_id: `service-${seq}` }),
      async close() { await call("shutdown"); const exited = once(child, "exit"); child.stdin.end(); await exited; assert.equal(child.exitCode, 0, stderr); },
    };
  }
  t.after(async () => {
    for (const child of active) child.kill();
    await writeFile(join(output, "protocol.jsonl"), frames.map(frame => JSON.stringify(frame)).join("\n") + "\n");
  });
  let runtime = await start();
  const beforeOptional = new Map(storage), optional = await start("memory");
  await assert.rejects(optional.service("query", {}), /Coding|unavailable/);
  await optional.close(); assert.deepEqual(storage, beforeOptional, "optional Memory has no Personal activation or storage effects");
  const binding = (await runtime.call("capability.invoke", { capability: "plugin.client.request", input: { method: "agents.ensure_default", input: {} }, execution_id: "create-main" })).output.result.agent;
  mainSession = binding.main_session_id; mainAgent = binding.agent_id;
  const source = (ref, seconds) => [{ kind: "message", ref, occurred_at: new Date(Date.now() - 60_000 + seconds * 1000).toISOString() }];
  const record = (await runtime.tool("personal_memory_create", { key: "address.home", expected_revision: 0, content: "Synthetic Old Street", evidence: "explicit", sources: source("synthetic-message:1", 0) })).record;
  assert.equal((await runtime.service("query", { query: "address" })).records[0].id, record.id);
  const corrected = (await runtime.service("correct", { id: record.id, expected_revision: 1, content: "Synthetic New Street", evidence: "explicit", sources: source("synthetic-message:2", 10) })).record;
  assert.equal(corrected.updated_by_agent_id, mainAgent);
  assert.equal(corrected.contributions[0].agent_id, mainAgent);
  const prompt = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 1, messages: [{ role: "user", content: "Which address?" }] }, execution_id: "prompt-1" });
  assert(JSON.stringify(prompt).includes("Synthetic New Street")); assert(!JSON.stringify(prompt).includes("Synthetic Old Street"));
  await runtime.close(); runtime = await start();
  assert.equal((await runtime.service("read", { id: record.id })).record.revision, 2);
  await runtime.service("forget", { id: record.id, expected_revision: 2 });
  assert.equal((await runtime.service("query", {})).records.length, 0);
  await assert.rejects(runtime.tool("personal_memory_create", { key: "address.rephrased", expected_revision: 0, content: "Synthetic Old Street again", evidence: "explicit", sources: source("synthetic-message:1", 0) }), /forgotten_source/);
  await assert.rejects(runtime.tool("memory_list", {}), /Coding|unavailable|unknown Personal tool/);
  await runtime.close();
  assert.equal(locks.size, 0);
  assert(!JSON.stringify([...storage.values()]).includes("Street"));
  assert(!frames.some(({ direction, frame }) => direction === "plugin" && ["personal.agents", "memory.records"].includes(frame.params?.service)), "internal memory must not call external plugin services");
  await writeFile(join(output, "evidence.json"), JSON.stringify({ passed: true, runtime_sha256: createHash("sha256").update(await readFile(executable)).digest("hex"), node: process.version, completed_at: new Date().toISOString(), scenarios: ["Personal-only main binding owns host-attributed records", "correction freshness at next model step", "restart recovery", "forget removes source data and blocks extraction", "Personal rejects legacy Coding tools"] }, null, 2));
});
