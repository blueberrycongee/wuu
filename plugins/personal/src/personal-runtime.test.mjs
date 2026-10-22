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
      case "host.client.invalidate": assert.deepEqual(params, { topic: "reactions" }); return { delivered: true };
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

// Failure cases: cross-space/session spoofing, service/worker actor spoofing,
// non-user provenance, stale and duplicate mutations, restart loss, oversized
// input, and reactions accidentally becoming turns, approval, or task dispatch.
test("Personal reactions keep ordinary feedback quiet and negative feedback bounded", async t => {
  const scratch = await mkdtemp(join(tmpdir(), "wuu-personal-reactions-runtime-"));
  const output = process.env.WUU_REACTIONS_E2E_OUTPUT ?? join(root, ".tmp/e2e/personal-reactions");
  await mkdir(output, { recursive: true });
  t.after(() => rm(scratch, { recursive: true, force: true }));
  const storage = new Map(), frames = [], active = new Set(), locks = new Set();
  const contexts = {}, turns = new Map();
  let loseFeedbackReply = false;
  let mainSession, mainAgent;
  let failReactionWrite = false;
  const messages = [
    { seq: 1, role: "user", origin: "user", content: "Original owner message" },
    { seq: 2, role: "assistant", content: "Original assistant response" },
    { seq: 3, role: "user", origin: "automation", read_only: true, content: "Synthetic input" },
    { seq: 4, role: "assistant", hidden: true, content: "Hidden context" },
    { seq: 5, role: "assistant", read_only: true, content: "Read-only assistant" },
    { seq: 6, role: "assistant", truncated: true, content: "Truncated assistant" },
  ];
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
      case "host.session.inspect": return { session: contexts[params.session_id], turn: params.request_id ? turns.get(params.request_id) : [...turns.values()].at(-1) };
      case "host.session.send": {
        assert.equal(params.cause, "personal.reaction.feedback"); assert.equal(params.if_running, "queue");
        assert.equal(params.disable_tools, true);
        const turn = turns.get(params.request_id) ?? { request_id: params.request_id, turn_id: `turn-${params.request_id}`, state: "running" };
        turns.set(params.request_id, turn);
        if (loseFeedbackReply) { loseFeedbackReply = false; throw new Error("lost feedback send reply"); }
        return { ...turn, session_id: params.session_id };
      }
      case "host.session.history.read": return { session_id: params.session_id, records: messages.filter(m => m.seq >= (params.start_seq ?? 1) && m.seq <= (params.end_seq ?? Math.max(...messages.map(m => m.seq)))).slice(0, params.limit ?? 50), head_seq: Math.max(...messages.map(m => m.seq)), snapshot_seq: Math.max(...messages.map(m => m.seq)), has_more: false };
      case "host.session.context": return contexts[params.session_id] ?? { session_id: params.session_id, product_domain: "coding" };
      case "host.storage.keys": return { keys: [...storage.keys()].map(key => key.slice("user:".length)) };
      case "host.storage.get": return { value: storage.get(key) ?? null };
      case "host.storage.compare-exchange": {
        if (failReactionWrite && params.key.includes(".reaction.")) throw new Error("fixture storage unavailable");
        const current = storage.get(key) ?? null;
        if (current !== params.expected) return { swapped: false, value: current };
        storage.set(key, params.value); return { swapped: true, value: params.value };
      }
      case "host.client.invalidate": assert.deepEqual(params, { topic: "reactions" }); return { delivered: true };
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
  const client = (method, input = {}) => runtime.call("capability.invoke", { capability: "plugin.client.request", input: { method, input }, execution_id: `client-${frames.length}` }).then(r => r.output.result);
  const binding = (await client("agents.ensure_default")).agent;
  mainSession = binding.main_session_id; mainAgent = binding.agent_id;
  const react = (extra = {}) => client("agents.react", { agent_id: mainAgent, session_id: mainSession, message_seq: 2, emoji: "👍", expected_revision: 0, request_id: "owner-1", ...extra });
  const first = await react(); assert.deepEqual(first.reactions, { owner: "👍", revision: 1 });
  assert.deepEqual(await react(), first, "transport retry returns the same persisted result");
  await assert.rejects(react({ request_id: "owner-stale", emoji: "❤️" }), /revision_conflict/);
  await assert.rejects(react({ request_id: "owner-1", emoji: "❤️" }), /request_id/);
  await assert.rejects(react({ session_id: "foreign" }), /session/);
  await assert.rejects(react({ message_seq: 1 }), /assistant/);
  await assert.rejects(react({ message_seq: 4 }), /visible/);
  await assert.rejects(react({ emoji: "approve payment" }), /emoji/);
  await assert.rejects(react({ emoji: "👍👍" }), /emoji/);
  await assert.rejects(react({ actor: "agent" }), /field/);
  await assert.rejects(runtime.call("service.invoke", { service: "personal.agents", method: "agents.react", params: { agent_id: mainAgent, session_id: mainSession, message_seq: 2, emoji: "❤️", expected_revision: 1, request_id: "forged-owner" }, execution_id: "service-owner" }), /client/);
  const second = await react({ request_id: "owner-2", emoji: "❤️", expected_revision: 1 });
  assert.equal(second.reactions.revision, 2);
  const toolInput = { message_seq: 1, emoji: "✅", expected_revision: 0, request_id: "agent-1" };
  assert.equal((await runtime.tool("react_to_message", toolInput)).reactions.agent, "✅");
  await assert.rejects(runtime.tool("react_to_message", { ...toolInput, actor: "owner" }), /field/);
  await assert.rejects(runtime.tool("react_to_message", { ...toolInput, message_seq: 2 }), /user/);
  await assert.rejects(runtime.tool("react_to_message", { ...toolInput, message_seq: 3 }), /original|visible/);
  await assert.rejects(runtime.tool("react_to_message", toolInput, "coding"), /bound Personal/);
  contexts.foreign = { session_id: "foreign", agent_id: mainAgent, product_domain: "personal", user_space_id: "someone-else" };
  await assert.rejects(runtime.tool("react_to_message", toolInput, "foreign"), /bound Personal/);
  contexts.worker = { session_id: "worker", agent_id: mainAgent, parent_session_id: mainSession, product_domain: "personal", user_space_id: "local" };
  await assert.rejects(runtime.tool("react_to_message", toolInput, "worker"), /executor|main/);
  const beforeStep = frames.length;
  const preStep = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 1, messages: [{ role: "user", content: "A natural next message" }] }, execution_id: "natural-step" });
  // Protect the main/worker context boundary, not literal prompt wording or
  // untested model behavior. Guidance is available even without a source locator.
  assert.equal(preStep.output.append_messages.filter(m => m.id.startsWith("personal.reaction-guidance.")).length, 1);
  const locatedStep = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 2, source_message: { session_id: mainSession, seq: 1 }, messages: [] }, execution_id: "located-step" });
  assert.equal(locatedStep.output.append_messages.filter(m => m.id.startsWith("personal.reaction-guidance.")).length, 1);
  const agentKey = [...storage.keys()].find(key => key.includes(".agent.") && JSON.parse(storage.get(key)).agent_id === mainAgent);
  const workerKey = agentKey.slice(0, agentKey.indexOf(".agent.")) + ".task.guidance-worker";
  storage.set(workerKey, JSON.stringify({ agent_id: mainAgent, task_id: "guidance-worker", state: "running", execution: { session_id: "worker" }, cancel_requested: false, requirements_revision: 1 }));
  const workerStep = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: "worker", step_index: 1, messages: [] }, execution_id: "worker-step" });
  assert(!workerStep.output.append_messages.some(m => m.id.startsWith("personal.reaction-guidance.") || m.id.startsWith("personal.reactions.")));
  await assert.rejects(runtime.tool("react_to_message", toolInput, "worker"), /only the main/);
  storage.delete(workerKey);
  const reactionContext = preStep.output.append_messages.filter(m => m.id.startsWith("personal.reactions."));
  assert.equal(reactionContext.length, 1); assert(reactionContext[0].content.includes("❤️")); assert(!reactionContext[0].content.includes("👍"));
  assert(!frames.slice(beforeStep).some(f => f.frame.params?.service === "host.session.send"));
  const history = await client("agents.history", { agent_id: mainAgent });
  assert.equal(history.records.find(m => m.seq === 1).reactions.agent, "✅");
  assert.equal(history.records.find(m => m.seq === 2).reactions.owner, "❤️");
  assert.equal(history.records.find(m => m.seq === 2).reaction_eligible, true);
  assert.equal(history.records.find(m => m.seq === 5).reaction_eligible, false);
  assert.equal(history.records.find(m => m.seq === 6).reaction_eligible, false);
  await runtime.close(); runtime = await start();
  const restored = await client("agents.reactions", { agent_id: mainAgent, session_id: mainSession, message_seqs: [1, 2] });
  assert.equal(restored.records[1].reactions.owner, "❤️");
  const removed = await react({ request_id: "owner-3", expected_revision: 2, emoji: null });
  assert.deepEqual(removed.reactions, { revision: 3 });
  assert.deepEqual(await react({ request_id: "owner-3", expected_revision: 2, emoji: null }), removed);
  const removedStep = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 2, messages: [] }, execution_id: "removed-step" });
  const removedContext = removedStep.output.append_messages.find(m => m.id.startsWith("personal.reactions.")).content;
  assert(removedContext.includes('"emoji":null')); assert(!removedContext.includes("❤️"));
  assert.deepEqual((await react()).reactions, { revision: 3 }, "old transport retry cannot restore overwritten or removed feedback");
  const race = await Promise.allSettled([react({ request_id: "race-a", expected_revision: 3, emoji: "👍" }), react({ request_id: "race-b", expected_revision: 3, emoji: "❤️" })]);
  assert.equal(race.filter(result => result.status === "fulfilled").length, 1);
  assert.match(race.find(result => result.status === "rejected").reason.message, /revision_conflict/);
  messages.push({ seq: 7, role: "assistant", content: "Must not fabricate feedback" });
  failReactionWrite = true;
  await assert.rejects(react({ message_seq: 7, request_id: "failed-write", emoji: "😂" }), /unavailable/);
  failReactionWrite = false;
  const failedStep = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 3, messages: [] }, execution_id: "failed-step" });
  assert(!JSON.stringify(failedStep).includes("😂"), "reserved pointer is not feedback until the canonical state commits");
  for (let batch = 0; batch < 3; batch++) {
    const seqs = Array.from({ length: 10 }, (_, offset) => 100 + batch * 10 + offset);
    messages.push(...seqs.map(seq => ({ seq, role: "assistant", content: `Synthetic reply ${seq}` })));
    await Promise.all(seqs.map(seq => react({ message_seq: seq, request_id: `many-${seq}` })));
  }
  const feedbackFeed = [...storage.entries()].find(([key]) => key.includes(".reaction-feed."));
  assert.equal(JSON.parse(feedbackFeed[1]).messages.length, 24);
  const previousFeed = feedbackFeed[1]; await react();
  assert.equal(storage.get(feedbackFeed[0]), previousFeed, "an old duplicate cannot reorder the current feedback window");
  const boundedStart = frames.length;
  const boundedStep = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 4, messages: [] }, execution_id: "bounded-step" });
  assert.equal(frames.slice(boundedStart).filter(f => f.direction === "plugin" && f.frame.params?.service === "host.storage.get" && f.frame.params?.params?.key?.includes(".reaction.")).length, 24);
  assert(JSON.stringify(boundedStep).length < 20_000);
  assert.equal((await client("agents.reactions", { session_id: mainSession, message_seqs: [100] })).records[0].reactions.owner, "👍", "older feedback stays durable outside the bounded model window");
  for (const message of messages) if (message.seq >= 100) message.hidden = true;
  const hiddenStep = await runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 5, messages: [] }, execution_id: "hidden-step" });
  assert(hiddenStep.output.append_messages.find(m => m.id.startsWith("personal.reactions.")).content.endsWith("[]"), "unavailable targets supersede earlier feedback with an empty current snapshot");
  await assert.rejects(client("agents.reactions", { session_id: mainSession, message_seqs: Array(101).fill(1) }), /100|bounded/);
  await runtime.close();
  assert(!frames.some(f => f.direction === "plugin" && f.frame.params?.service === "host.session.send"), "feedback must never create a turn or dispatch work");
  // Negative feedback is one source-bound opportunity, never permission to rerun.
  runtime = await start();
  const fresh = seq => messages.push({ seq, role: "assistant", content: "Here is the interface layout I prepared", at: new Date().toISOString() });
  const down = (seq, revision = 0, emoji = "👎", suffix = "") => react({ message_seq: seq, emoji, expected_revision: revision, request_id: `negative-${seq}-${revision}${suffix}` });
  const sends = () => frames.filter(f => f.direction === "plugin" && f.frame.params?.service === "host.session.send");
  fresh(200); loseFeedbackReply = true;
  await down(200); assert.equal(turns.size, 1, "committed reaction survives a lost dispatch reply");
  const event = sends().at(-1).frame.params.params;
  assert.equal(event.session_id, mainSession); assert(!event.relation, "feedback is not a task reply or resumption");
  await down(200); assert.equal(sends().length, 1, "retry consults the host receipt, never redispatches an accepted event");
  const feedbackStep = () => runtime.call("capability.invoke", { capability: "agent.pre_step", input: { session_id: mainSession, step_index: 1, messages: [] }, execution_id: "feedback-step" });
  let step = await feedbackStep();
  assert(step.output.append_messages.some(m => m.id.startsWith("personal.negative-feedback.current.")));
  await assert.rejects(runtime.tool("create_task", {request_id:"feedback-work",title:"Redo it",goal:"Redo it"}), /feedback.*tools/);
  await assert.rejects(runtime.tool("personal_memory_create", {key:"preference",content:"User dislikes this"}), /feedback.*tools/);
  await down(200, 1, null);
  step = await feedbackStep();
  assert(step.output.append_messages.some(m => m.id.startsWith("personal.negative-feedback.suppressed.")), "removal invalidates an already queued event");
  await down(200, 2); assert.equal(turns.size, 1, "toggling feedback never creates a wake loop");
  turns.get(event.request_id).state = "completed";
  await runtime.close(); runtime = await start(); assert.equal(turns.size, 1, "restart does not replay consumed feedback");
  // A running conversation holds feedback until idle; newer owner input wins.
  messages.push({seq:209, role:"user", origin:"user", content:"Prepare another version"});
  fresh(210); contexts[mainSession].state = "running";
  await down(210); assert.equal(turns.size, 1);
  await down(210, 1, "👍"); contexts[mainSession].state = "idle";
  await runtime.close(); runtime = await start(); assert.equal(turns.size, 1, "replacement suppresses pending feedback on recovery");
  fresh(220); contexts[mainSession].state = "running"; await down(220);
  messages.push({seq:221, role:"user", origin:"user", content:"Actually use the other layout"}); contexts[mainSession].state = "idle";
  await runtime.close(); runtime = await start(); assert.equal(turns.size, 1, "new owner message supersedes pending feedback");
  messages.push({seq:230, role:"assistant", content:"Old plan", at:new Date(Date.now()-3600000).toISOString()});
  await down(230); assert.equal(turns.size, 1, "old messages remain quiet");
  fresh(240); await down(240, 0, "😢"); assert.equal(turns.size, 1, "other emotional reactions remain quiet");
  fresh(250); contexts[mainSession].state = "running"; await down(250); contexts[mainSession].state = "idle";
  await runtime.close(); runtime = await start(); assert.equal(turns.size, 2, "pending feedback recovers once when idle");
  await assert.rejects(down(250, 1, "👎👎"), /emoji/);
  await assert.rejects(react({session_id:"worker", message_seq:250, emoji:"👎", request_id:"worker-feedback"}), /session/);
  turns.get(sends().at(-1).frame.params.params.request_id).state = "completed";
  fresh(251); await down(251); assert.equal(turns.size, 2, "disliking a feedback reply cannot chain another feedback turn");
  await runtime.close(); runtime = await start(); assert.equal(turns.size, 2, "feedback chain suppression survives restart");
  messages.push({seq:252, role:"user", origin:"user", content:"Please prepare a different layout"});
  fresh(253); await down(253); assert.equal(turns.size, 3, "a new original owner request permits fresh feedback");
  // Every accepted terminal receipt consumes feedback, including shutdown discard.
  for (const [index, state] of ["failed", "interrupted", "discarded"].entries()) {
    const seq = 260 + index * 10;
    messages.push({seq:seq-1, role:"user", origin:"user", content:"A new explicit request"});
    fresh(seq); loseFeedbackReply = true;
    const before = sends().length;
    await down(seq);
    const receipt = turns.get(sends().at(-1).frame.params.params.request_id);
    receipt.state = state;
    receipt.retryable = state === "discarded";
    await runtime.close(); runtime = await start();
    await down(seq);
    assert.equal(sends().length, before + 1, `${state} feedback must never be replayed`);
  }
  await runtime.close();
  await writeFile(join(output, "evidence.json"), JSON.stringify({ passed: true, runtime_sha256: createHash("sha256").update(await readFile(executable)).digest("hex"), scenarios: ["canonical actor ownership", "replace/remove/idempotency", "stale rejection", "restart persistence", "next-step coalesced feedback", "ordinary reactions stay quiet", "bounded negative-feedback recovery", "superseded and removed feedback suppression", "feedback cannot invoke Personal tools"] }, null, 2));
});
