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
// fixture provides only host storage and immutable session context, never memory behavior.
const root = fileURLToPath(new URL("../../../", import.meta.url));
const executable = join(root, "internal/plugin/bundled/memory/runtime.mjs");

test("Personal memory shipped runtime persists shared corrections and forget across process replacement", async t => {
  const scratch = await mkdtemp(join(tmpdir(), "wuu-personal-memory-runtime-"));
  const output = process.env.WUU_MEMORY_E2E_OUTPUT ?? join(root, ".tmp/e2e/personal-memory");
  await mkdir(output, { recursive: true });
  t.after(() => rm(scratch, { recursive: true, force: true }));
  const storage = new Map(), frames = [], active = new Set(), locks = new Set();
  const contexts = {
    "main-a": { session_id: "main-a", product_domain: "personal", user_space_id: "local", agent_id: "agent-a" },
    "main-b": { session_id: "main-b", product_domain: "personal", user_space_id: "local", agent_id: "agent-b" },
  };
  const host = async frame => {
    assert.equal(frame.method, "host.service.call");
    const { service, method, params } = frame.params;
    const key = `${params?.scope}:${params?.key}`;
    switch (service) {
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
  async function start() {
    const child = spawn(process.execPath, [executable], { stdio: ["pipe", "pipe", "pipe"] }); active.add(child);
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
    const definition = await call("initialize", { protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 1, plugin_id: "memory", plugin_root: scratch, project_root: scratch, wuu_home: scratch, workspace_state_dir: join(scratch, "state"), product_domain: "personal", user_space_id: "local", supported_host_services: ["host.service.call", "host.session.context", "host.storage.keys", "host.storage.get", "host.storage.compare_exchange"] });
    assert(definition.provided_services.some(service => service.name === "memory.records"));
    assert(!definition.provided_services.some(service => service.name === "memory.notebook"));
    await call("activate");
    return {
      call,
      tool: (tool_id, args, session_id = "main-a") => call("tool.execute", { tool_id, arguments: args, session_id, thread_id: session_id, cwd: scratch, call_id: `tool-${seq}`, execution_id: `execution-${seq}` }).then(result => JSON.parse(result.result.content[0].text)),
      service: (method, params, session_id = "main-b") => call("service.invoke", { service: "memory.records", method, caller: "personal", params: { session_id, ...params }, execution_id: `service-${seq}` }),
      async close() { await call("shutdown"); const exited = once(child, "exit"); child.stdin.end(); await exited; assert.equal(child.exitCode, 0, stderr); },
    };
  }
  t.after(async () => {
    for (const child of active) child.kill();
    await writeFile(join(output, "protocol.jsonl"), frames.map(frame => JSON.stringify(frame)).join("\n") + "\n");
  });
  let runtime = await start();
  const source = (ref, seconds) => [{ kind: "message", ref, occurred_at: new Date(Date.now() - 60_000 + seconds * 1000).toISOString() }];
  const record = (await runtime.tool("personal_memory_create", { key: "address.home", expected_revision: 0, content: "Synthetic Old Street", evidence: "explicit", sources: source("synthetic-message:1", 0) })).record;
  assert.equal((await runtime.service("query", { query: "address" })).records[0].id, record.id);
  const corrected = (await runtime.service("correct", { id: record.id, expected_revision: 1, content: "Synthetic New Street", evidence: "explicit", sources: source("synthetic-message:2", 10) })).record;
  assert.equal(corrected.updated_by_agent_id, "agent-b");
  assert.equal(corrected.contributions[0].agent_id, "agent-a");
  const prompt = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: "main-a", step_index: 1, messages: [{ role: "user", content: "Which address?" }] }, execution_id: "prompt-1" });
  assert(JSON.stringify(prompt).includes("Synthetic New Street")); assert(!JSON.stringify(prompt).includes("Synthetic Old Street"));
  await runtime.close(); runtime = await start();
  assert.equal((await runtime.service("read", { id: record.id })).record.revision, 2);
  await runtime.service("forget", { id: record.id, expected_revision: 2 });
  assert.equal((await runtime.service("query", {})).records.length, 0);
  await assert.rejects(runtime.tool("personal_memory_create", { key: "address.rephrased", expected_revision: 0, content: "Synthetic Old Street again", evidence: "explicit", sources: source("synthetic-message:1", 0) }), /forgotten_source/);
  await assert.rejects(runtime.tool("memory_list", {}), /Coding|unavailable/);
  await runtime.close();
  assert.equal(locks.size, 0);
  assert(!JSON.stringify([...storage.values()]).includes("Street"));
  await writeFile(join(output, "evidence.json"), JSON.stringify({ passed: true, runtime_sha256: createHash("sha256").update(await readFile(executable)).digest("hex"), node: process.version, completed_at: new Date().toISOString(), scenarios: ["two named agents share host-attributed records", "correction freshness at next model step", "restart recovery", "forget removes source data and blocks extraction", "Personal rejects legacy Coding tools"] }, null, 2));
});
