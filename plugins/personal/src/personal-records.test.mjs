import assert from "node:assert/strict";
import { test } from "node:test";
import { build } from "esbuild";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

// Public plugin behavior: test persistence, host-bound authority, stale evidence,
// concurrent corrections, forget recovery, expiration and the unchanged notebook.
const result = await build({
  entryPoints: [fileURLToPath(new URL("./personal-records.ts", import.meta.url))],
  alias: { "@wuu/plugin-sdk": fileURLToPath(new URL("../../../packages/plugin-sdk/src/index.ts", import.meta.url)) },
  bundle: true, format: "esm", platform: "node", target: "node22.7", write: false,
});
const { PersonalRecords } = await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString("base64")}`);

// Persistence and permission failure cases stay isolated from orchestration;
// the shipped-runtime suite verifies the real Personal-owned authority wiring.
function createRecordsHarness() {
  let records;
  return {
    initialize(params, host) {
      records = new PersonalRecords("local", host, "personal", {
        context: session_id => host.call("host.service.call", { service: "personal.agents", method: "agents.memory-context", params: { session_id } }),
        evidence: (session_id, ref) => host.memory("memory.source", session_id, { ref }),
        records: session_id => host.memory("memory.read", session_id),
        operation: (session_id, id) => host.memory("memory.operation", session_id, { id }),
        commit: (session_id, input) => host.memory("memory.commit", session_id, input),
      });
      return { provided_services: [{ name: "memory.records" }] };
    },
    async activate() {},
    async invokeService(call, host, execution) {
      if (call.service !== "memory.records") throw new Error("unavailable in Personal");
      const { session_id, ...input } = call.params;
      return records.invoke(call.method, input, session_id, host, execution);
    },
    async userCorrection(input, host, execution) {
      return records.invoke("correct", input, "a-main", host, execution, true);
    },
    async executeTool(call, host, execution) {
      if (!call.tool_id.startsWith("personal_memory_")) throw new Error("unavailable in Personal");
      return { result: { content: [{ type: "text", text: JSON.stringify(await records.invoke(call.tool_id.slice(16), call.arguments, call.session_id, host, execution)) }] } };
    },
    async invokeCapability(call, host, execution) { return { output: await records.preStep(call.input, host, execution) }; },
  };
}

class Host {
  storage = new Map();
  journal = new Map();
  operations = new Map();
  loseCommitReply = false;
  contexts = new Map([
    ["a-main", { session_id: "a-main", agent_id: "agent-a", product_domain: "personal", user_space_id: "local" }],
    ["b-main", { session_id: "b-main", agent_id: "agent-b", product_domain: "personal", user_space_id: "local" }],
    ["a-worker", { session_id: "a-worker", agent_id: "agent-a", product_domain: "personal", user_space_id: "local", parent_session_id: "a-main" }],
    ["foreign", { session_id: "foreign", agent_id: "foreign-agent", product_domain: "personal", user_space_id: "another-user" }],
    ["coding", { session_id: "coding", product_domain: "coding" }],
  ]);
  evidence = new Map();
  locks = new Map();
  releases = new Map();
  counter = 0;
  async memory(operation, session_id, input = {}) {
    const { value } = await this.call("host.service.call", { service: "host.personal", method: "call", params: { operation, session_id, input } });
    return value;
  }
  async call(service, params) {
    switch (service) {
      case "host.session.context": {
        const context = this.contexts.get(params.session_id);
        if (!context) throw new Error("session not found");
        return structuredClone(context);
      }
      case "host.storage.keys": return { keys: [...this.storage.keys()] };
      case "host.storage.get": return { value: this.storage.get(params.key) ?? null };
      case "host.storage.compare_exchange": {
        assert(!params.key.startsWith("personal.records.v1."), "new memory writes must not mutate legacy plugin storage");
        const current = this.storage.get(params.key) ?? null;
        if (current !== params.expected) return { swapped: false, value: current };
        this.storage.set(params.key, params.value);
        return { swapped: true, value: params.value };
      }
      case "host.service.call": {
        if (params.service === "host.personal") {
          assert.equal(params.method, "call");
          const { operation, session_id, input } = params.params;
          const context = this.contexts.get(session_id);
          assert.equal(context?.product_domain, "personal");
          assert.equal(context?.user_space_id, "local");
          let value;
          if (operation === "memory.read") value = { records: Object.fromEntries(this.journal) };
          else if (operation === "memory.source") {
            const evidence = this.evidence.get(input.ref);
            if (!evidence) throw new Error("unsupported or unavailable memory evidence");
            value = structuredClone(evidence);
            if (!["a-main", "b-main"].includes(session_id)) delete value.content;
          } else if (operation === "memory.operation") {
            const saved = this.operations.get(input.id);
            if (saved) assert.equal(saved.session_id, session_id, "intent belongs to its original session");
            if (saved?.redacted) throw new Error("forgotten_record: publication receipt was redacted");
            if (saved?.result?.swapped && this.journal.get(saved.write.key) !== saved.result.value) throw new Error("revision_conflict: memory receipt was superseded");
            value = saved ? { write: structuredClone(saved.write) } : null;
          } else {
            assert.equal(operation, "memory.commit");
            assert.match(input.key, /^mem_[a-f0-9]{64}$/);
            let saved = this.operations.get(input.id);
            if (saved) {
              assert.equal(saved.session_id, session_id);
              assert.deepEqual(saved.write, input, "stable operation must preserve exact first prepared bytes");
            } else {
              saved = { session_id, write: structuredClone(input) };
              this.operations.set(input.id, saved);
              const current = this.journal.get(input.key) ?? this.storage.get(`personal.records.v1.${input.key}`) ?? null;
              if (current !== input.expected) saved.result = { swapped: false, value: current };
              else {
                const record = JSON.parse(input.value);
                assert.equal(record.id, input.key); assert.equal(record.product_domain, "personal"); assert.equal(record.user_space_id, "local");
                assert.equal(record.revision, (current ? JSON.parse(current).revision : 0) + 1);
                this.journal.set(input.key, input.value);
                if (record.status === "forgotten") {
                  saved.write.expected = null; saved.redacted = true;
                  for (const prior of this.operations.values()) {
                    if (prior !== saved && prior.write.key === input.key) {
                      prior.write.expected = null; prior.write.value = ""; prior.result = null; prior.redacted = true;
                    }
                  }
                }
                saved.result = { swapped: true, value: input.value };
              }
            }
            value = saved.result;
            if (this.loseCommitReply) { this.loseCommitReply = false; throw new Error("memory commit reply lost"); }
          }
          return { ok: true, value: structuredClone(value) };
        }
        if (params.service === "personal.agents") {
          assert.equal(params.method, "agents.memory-context");
          const context = this.contexts.get(params.params.session_id);
          if (!context?.agent_id) throw new Error("unknown Personal identity");
          return { session_id: context.session_id, agent_id: context.agent_id, role: ["a-main", "b-main"].includes(context.session_id) ? "main" : "background" };
        }
        assert.equal(params.service, "host.file-lock");
        if (params.method === "release") {
          this.releases.get(params.params.lock_id)?.();
          this.releases.delete(params.params.lock_id);
          return { released: true };
        }
        assert.equal(params.method, "acquire");
        const key = `${params.params.root}:${params.params.path}`;
        const previous = this.locks.get(key);
        let release;
        const pending = new Promise(resolve => { release = resolve; });
        this.locks.set(key, pending);
        await previous;
        const lock_id = `lock-${++this.counter}`;
        this.releases.set(lock_id, () => { if (this.locks.get(key) === pending) this.locks.delete(key); release(); });
        return { lock_id };
      }
      default: throw new Error(`Unexpected service ${service}`);
    }
  }
}
const execution = { executionId: "fixture", signal: new AbortController().signal };
const day = (offset = 0) => new Date(Date.now() - 86_400_000 + offset * 1000).toISOString();
const source = (ref, offset = 0) => [{ kind: "message", ref, occurred_at: day(offset) }];
function value(result) { return JSON.parse(result.result.content[0].text); }
async function fixture(t, host = new Host(), domain = "personal") {
  const dir = await mkdtemp(join(tmpdir(), "wuu-personal-memory-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const plugin = createRecordsHarness();
  const params = { protocol_version: 3, plugin_id: "personal", plugin_root: dir, project_root: dir, wuu_home: dir,
    workspace_state_dir: join(dir, "workspace"), ...(domain === "personal" ? { product_domain: "personal", user_space_id: "local" } : {}) };
  const definition = await plugin.initialize(params, host, { signal: execution.signal });
  await plugin.activate(host, { signal: execution.signal });
  const service = (method, params, session_id = "a-main") => plugin.invokeService({ service: "memory.records", method, caller: "fixture", params: { session_id, ...params } }, host, execution);
  const tool = (method, args, session_id = "a-main") => plugin.executeTool({ tool_id: `personal_memory_${method}`, arguments: args, session_id, call_id: "fixture", cwd: dir, tool: method }, host, execution).then(value);
  return { plugin, params, definition, service, tool, host, dir };
}
const createArgs = (key, ref = key) => ({ key, expected_revision: 0, content: "Prefers tea", evidence: "explicit", sources: source(ref) });

test("Personal records are shared, host-attributed and survive plugin restart without migrating Coding", async t => {
  const f = await fixture(t);
  const a = (await f.tool("create", createArgs("drink"))).record;
  assert.equal(a.contributor_agent_id, "agent-a");
  const b = (await f.tool("create", { ...createArgs("food"), content: "Enjoys noodles" }, "b-main")).record;
  assert.equal(b.contributor_agent_id, "agent-b");
  const worker = (await f.tool("create", createArgs("travel"), "a-worker")).record;
  assert.equal(worker.contributor_agent_id, "agent-a");
  assert.equal(worker.contributions[0].executor_session_id, "a-worker");
  assert.equal(worker.status, "candidate");
  assert.equal((await f.service("query", {}, "b-main")).records.length, 2);
  const fresh = createRecordsHarness();
  await fresh.initialize(f.params, f.host, { signal: execution.signal });
  await fresh.activate(f.host, { signal: execution.signal });
  const loaded = await fresh.invokeService({ service: "memory.records", method: "read", caller: "fixture", params: { session_id: "b-main", id: a.id } }, f.host, execution);
  assert.equal(loaded.record.content, "Prefers tea");
  assert.equal(f.host.releases.size, 0);
});

test("domain, userspace and contributor cannot be supplied to bypass authority", async t => {
  const f = await fixture(t);
  for (const injected of [{ product_domain: "coding" }, { user_space_id: "another-user" }, { contributor_agent_id: "agent-b" }, { session_id: "b-main" }]) {
    await assert.rejects(f.tool("create", { ...createArgs("blocked"), ...injected }), /unexpected|not allowed|context/i);
  }
  await assert.rejects(f.service("query", {}, "foreign"), /space|scope|domain/i);
  await assert.rejects(f.service("query", {}, "coding"), /domain|scope/i);
  await assert.rejects(f.plugin.invokeService({ service: "memory.notebook", method: "index", caller: "fixture", params: {} }, f.host, execution), /domain|Personal|unavailable/i);
  await assert.rejects(f.plugin.executeTool({ tool_id: "memory_read", arguments: { file: "MEMORY.md" } }, f.host, execution), /domain|Personal|unavailable/i);

});

test("simultaneous cross-agent corrections have one winner and expose current revision", async t => {
  const f = await fixture(t);
  const first = (await f.service("create", createArgs("address"))).record;
  const changes = ["a-main", "b-main"].map((actor, i) => f.service("correct", { id: first.id, expected_revision: 1, content: `New address ${i}`, evidence: "explicit", sources: source(`correction-${i}`, i + 10) }, actor));
  const results = await Promise.allSettled(changes);
  assert.equal(results.filter(result => result.status === "fulfilled").length, 1);
  const failure = results.find(result => result.status === "rejected");
  assert.match(failure.reason.message, /revision_conflict.*2/);
  const current = (await f.service("read", { id: first.id })).record;
  assert.equal(current.revision, 2);
  assert.equal(current.contributions.length, 2);
  assert.equal(f.host.releases.size, 0);
});

test("ordered UI edits with equal timestamps survive replacement without admitting stale or replayed evidence", async t => {
  const f = await fixture(t);
  const original = (await f.service("create", createArgs("same-millisecond"))).record;
  const time = day(10);
  const edit = (revision, ref, occurred_at = time) => ({ id: original.id, expected_revision: revision, content: ref, evidence: "explicit", sources: [{ kind: "user_edit", ref: `personal-ui:${ref}`, occurred_at }] });
  const first = await f.plugin.userCorrection(edit(1, "first"), f.host, execution);
  assert.equal(first.record.source_at, time);
  const restarted = await fixture(t, f.host);
  await assert.rejects(restarted.service("correct", edit(2, "untrusted")), /stale_source/);
  const concurrent = await Promise.allSettled([restarted.plugin.userCorrection(edit(2, "second"), f.host, execution), f.plugin.userCorrection(edit(2, "competing"), f.host, execution)]);
  assert.equal(concurrent.filter(result => result.status === "fulfilled").length, 1);
  assert.match(concurrent.find(result => result.status === "rejected").reason.message, /revision_conflict/);
  const winner = concurrent.find(result => result.status === "fulfilled").value.record;
  assert.equal(winner.revision, 3);
  await assert.rejects(restarted.plugin.userCorrection(edit(3, winner.content), f.host, execution), /stale_source|superseded_source/);
  await assert.rejects(restarted.plugin.userCorrection(edit(3, "older", day(9)), f.host, execution), /stale_source/);
  assert.equal((await restarted.service("read", { id: original.id })).record.revision, 3);
});

test("correction wins over old extraction and refreshes another agent's next model step", async t => {
  const f = await fixture(t);
  const initial = { ...createArgs("address", "old-chat"), content: "Lives at Old Street" };
  const first = (await f.service("create", initial)).record;
  const input = { session_id: "a-main", step_index: 0, messages: [{ role: "user", content: "What address should I use?" }] };
  const before = await f.plugin.invokeCapability({ capability: "agent.pre_step", input, output: {} }, f.host, execution);
  assert.match(JSON.stringify(before), /Old Street/);
  await f.service("correct", { id: first.id, expected_revision: 1, content: "Lives at New Street", evidence: "explicit", sources: source("new-chat", 10) }, "b-main");
  const after = await f.plugin.invokeCapability({ capability: "agent.pre_step", input: { ...input, step_index: 1 }, output: {} }, f.host, execution);
  assert.match(JSON.stringify(after), /New Street/);
  assert(!JSON.stringify(after).includes("Old Street"));
  await assert.rejects(f.service("create", initial), /revision_conflict/, "superseded receipts cannot replay stale facts");
  await assert.rejects(f.service("revise", { id: first.id, expected_revision: 2, content: "Lives at Old Street", evidence: "explicit", sources: source("old-chat") }), /stale_source|superseded_source/);
  await assert.rejects(f.service("create", createArgs("renamed-address", "old-chat")), /superseded_source/);
  await assert.rejects(f.service("revise", { id: first.id, expected_revision: 2, content: "New guess", evidence: "inferred", sources: source("guess", 30) }, "b-main"), /contributor|author/);
});

test("forget purges content and traces, blocks old-source recreation across restart, and leaves Coding alone", async t => {
  const f = await fixture(t);
  const first = (await f.service("create", { ...createArgs("private-location", "chat-sensitive"), content: "Sensitive address to forget", topic: "Sensitive location", task_id: "private-trip" })).record;
  const forgotten = await f.service("forget", { id: first.id, expected_revision: 1 }, "b-main");
  assert.equal(forgotten.record.status, "forgotten");
  assert.equal((await f.service("query", {})).records.length, 0);
  assert(!JSON.stringify([...f.host.journal.values()]).includes("Sensitive"));
  assert(!JSON.stringify([...f.host.journal.values()]).includes("chat-sensitive"));
  assert(!JSON.stringify([...f.host.journal.values()]).includes("private-trip"));
  const receipts = JSON.stringify([...f.host.operations.values()]);
  for (const secret of ["Sensitive", "chat-sensitive", "private-trip"]) assert(!receipts.includes(secret), "forget also redacts prior values from terminal receipts");
  await assert.rejects(f.service("create", createArgs("rephrased-location", "chat-sensitive")), /forgotten_source/);
  const fresh = await fixture(t, f.host);
  await assert.rejects(fresh.service("create", createArgs("new-record", "chat-sensitive")), /forgotten_source/);
  await assert.rejects(fresh.service("correct", { id: first.id, expected_revision: 2, content: "Restore old fact", evidence: "explicit", sources: source("new-chat", 10) }), /forgotten/);
  assert.match(JSON.stringify(forgotten.retention), /chat|histor/i);
});

test("expired, future-effective and conflicting facts stay out of ordinary recall", async t => {
  const f = await fixture(t);
  const expires_at = new Date(Date.now() - 1000).toISOString();
  await f.service("create", { ...createArgs("temporary"), expires_at });
  await f.service("create", { ...createArgs("future"), effective_at: new Date(Date.now() + 86_400_000).toISOString() });
  const current = (await f.service("create", createArgs("disputed"))).record;
  const conflict = await f.service("revise", { id: current.id, expected_revision: 1, content: "Maybe coffee", evidence: "inferred", sources: source("uncertain", 20) });
  assert.equal(conflict.record.status, "conflict");
  assert.equal((await f.service("query", {})).records.length, 0);
  assert.equal((await f.service("query", { include_inactive: true })).records.length, 3);
  const resolved = await f.service("correct", { id: current.id, expected_revision: 2, content: "Prefers tea", evidence: "explicit", sources: source("resolved", 40) }, "b-main");
  assert.equal(resolved.record.status, "active");
  assert.equal((await f.service("query", {})).records.length, 1);
});

test("cancelled writes do not publish data or retain locks", async t => {
  const f = await fixture(t);
  const controller = new AbortController(); controller.abort();
  await assert.rejects(f.plugin.invokeService({ service: "memory.records", method: "create", caller: "fixture", params: { ...createArgs("cancelled"), session_id: "a-main" } }, f.host, { executionId: "cancelled", signal: controller.signal }));
  assert.equal(f.host.storage.size, 0);
  assert.equal(f.host.journal.size, 0);
  assert.equal(f.host.operations.size, 0);
  assert.equal(f.host.releases.size, 0);
});

test("legacy Memory prompt provenance invalidates stale context after Personal reload and avoids unchanged reinjection", async t => {
  const f = await fixture(t);
  const first = (await f.service("create", { ...createArgs("address"), content: "Lives at Original Street" })).record;
  const initial = { session_id: "a-main", step_index: 0, messages: [{ role: "user", content: "Which address should I use?" }] };
  const before = await f.plugin.invokeCapability({ capability: "agent.pre_step", input: initial }, f.host, execution);
  const emitted = before.output.append_messages[0];
  const messages = [...initial.messages, { role: "user", hidden: true, read_only: true, origin: "plugin", origin_id: `memory:${emitted.id}`, content: emitted.content }];
  const unchanged = await f.plugin.invokeCapability({ capability: "agent.pre_step", input: { ...initial, messages: messages.map(message => message.origin_id ? { ...message, origin_id: `personal:${emitted.id}` } : message) } }, f.host, execution);
  assert.equal(unchanged.output.append_messages?.length ?? 0, 0);
  await f.service("correct", { id: first.id, expected_revision: 1, content: "Lives at Updated Street", evidence: "explicit", sources: source("correction-reload", 10) }, "b-main");
  const restarted = createRecordsHarness(); await restarted.initialize(f.params, f.host, { signal: execution.signal });
  const after = await restarted.invokeCapability({ capability: "agent.pre_step", input: { ...initial, messages: [...messages, { role: "user", content: "Please arrange it" }] } }, f.host, execution);
  assert.match(JSON.stringify(after), /Updated Street/);
  assert(!JSON.stringify(after).includes("Original Street"));
});

test("the same source reference cannot produce duplicate independently forgettable facts", async t => {
  const f = await fixture(t);
  const first = (await f.service("create", createArgs("drink-original", "chat-1#drink"))).record;
  await assert.rejects(f.service("create", createArgs("drink-duplicate", "chat-1#drink")), /source_already_recorded/);
  assert.equal((await f.service("query", {})).records.length, 1);
  await f.service("forget", { id: first.id, expected_revision: 1 });
  assert.equal((await f.service("query", {})).records.length, 0);
});


test("background identities cannot publish, correct, forget or mutate authoritative records", async t => {
  const f = await fixture(t);
  const accepted = (await f.tool("create", { ...createArgs("language"), tier: "core" })).record;
  const update = { id: accepted.id, expected_revision: 1, content: "Different preference", evidence: "explicit", sources: source("worker-change", 20) };
  for (const method of ["revise", "correct"]) await assert.rejects(f.tool(method, update, "a-worker"), /main|authoritative/);
  await assert.rejects(f.tool("forget", { id: accepted.id, expected_revision: 1 }, "a-worker"), /main/);
  await assert.rejects(f.tool("create", { ...createArgs("worker-core"), tier: "core" }, "a-worker"), /core|main/);
  const candidate = (await f.tool("create", { ...createArgs("candidate"), evidence: "inferred" }, "a-worker")).record;
  assert.equal(candidate.status, "candidate");
  assert.equal(candidate.evidence, "inferred");
  assert(!(await f.tool("query", {})).records.some(r => r.id === candidate.id));
  const revised = (await f.tool("revise", { ...update, id: candidate.id, sources: source("candidate-new", 20), evidence: "inferred" }, "a-worker")).record;
  assert.equal(revised.status, "candidate");
  const promoted = (await f.tool("correct", { ...update, id: candidate.id, expected_revision: 2, sources: source("user-confirmed", 40), tier: "core" })).record;
  assert.equal(promoted.status, "active");
  assert.equal(promoted.tier, "core");
  f.host.contexts.set("unbound-root", { session_id: "unbound-root", agent_id: "agent-a", product_domain: "personal", user_space_id: "local" });
  await assert.rejects(f.tool("forget", { id: accepted.id, expected_revision: 1 }, "unbound-root"), /main/);
  const originalCall = f.host.call.bind(f.host);
  f.host.call = (service, params) => { if (service === "host.service.call" && params.service === "personal.agents") throw new Error("authority unavailable"); return originalCall(service, params); };
  await assert.rejects(f.tool("create", createArgs("no-authority")), /authority unavailable/);
  assert.equal((await f.tool("read", { id: accepted.id })).record.revision, 1);
});

test("bounded core and recent projections need no keyword and invalidate on forget for fresh workers", async t => {
  const f = await fixture(t);
  const core = (await f.tool("create", { ...createArgs("language"), content: "Use Mandarin", tier: "core" })).record;
  await f.tool("create", { ...createArgs("current-trip"), content: "Visiting Kyoto", tier: "recent" });
  await f.tool("create", { ...createArgs("legacy-detail"), content: "Not automatically core" });
  const step = async (session_id = "a-main") => (await f.plugin.invokeCapability({ capability: "agent.pre_step", input: { session_id, messages: [{ role: "user", content: "Hello" }] } }, f.host, execution)).output;
  let output = await step();
  assert.match(JSON.stringify(output), /Use Mandarin/);
  assert.match(JSON.stringify(output), /Visiting Kyoto/);
  assert(!JSON.stringify(output).includes("Not automatically core"));
  assert(!JSON.stringify(output).includes("contributions"));
  await f.tool("forget", { id: core.id, expected_revision: 1 });
  output = await step("a-worker");
  assert(!JSON.stringify(output).includes("Use Mandarin"));
  const snapshot = JSON.parse(output.append_messages[0].content.split("\n").at(-1));
  assert(Buffer.byteLength(JSON.stringify(snapshot.records)) <= 6000);
});

test("query pagination is exhaustive and rejects reuse after change or changed filters", async t => {
  const f = await fixture(t);
  for (let i = 0; i < 105; i++) await f.tool("create", createArgs(`record-${i}`));
  const first = await f.tool("query", { limit: 100 });
  assert.equal(first.records.length, 100);
  assert(first.next_cursor);
  const last = await f.tool("query", { limit: 100, cursor: first.next_cursor });
  assert.equal(last.records.length, 5);
  assert.equal(last.next_cursor, undefined);
  assert.equal(new Set([...first.records, ...last.records].map(r => r.id)).size, 105);
  await assert.rejects(f.tool("query", { limit: 100, query: "tea", cursor: first.next_cursor }), /cursor/);
  await f.tool("forget", { id: last.records[0].id, expected_revision: 1 });
  await assert.rejects(f.tool("query", { limit: 100, cursor: first.next_cursor }), /cursor/);
});


test("oversized and stale recent facts stay queryable without overflowing automatic projection", async t => {
  const f = await fixture(t);
  const large = (await f.tool("create", { ...createArgs("oversized"), tier: "core", content: "长".repeat(3000) })).record;
  await f.tool("create", { ...createArgs("old-context"), tier: "recent", content: "Old travel context", sources: [{ kind: "message", ref: "old-context", occurred_at: new Date(Date.now() - 15 * 86_400_000).toISOString() }] });
  const expiring = (await f.tool("create", { ...createArgs("expired-core"), tier: "core", content: "Expired profile", expires_at: new Date(Date.now() - 1000).toISOString() })).record;
  for (let i = 0; i < 20; i++) await f.tool("create", { ...createArgs(`core-${i}`), tier: "core", content: "档".repeat(200) });
  const output = (await f.plugin.invokeCapability({ capability: "agent.pre_step", input: { session_id: "a-worker", messages: [{ role: "user", content: "Hello" }] } }, f.host, execution)).output;
  const snapshot = JSON.parse(output.append_messages[0].content.split("\n").at(-1));
  assert(!snapshot.records.some(r => [large.id, expiring.id].includes(r.id)));
  assert(!JSON.stringify(snapshot).includes("Old travel context"));
  assert(Buffer.byteLength(JSON.stringify(snapshot.records)) <= 6000);
  assert.equal((await f.tool("read", { id: large.id })).record.content.length, 3000);
  assert.equal((await f.tool("read", { id: expiring.id })).record.availability, "expired");
});


test("main accepts a verified candidate without new user evidence or changing its evidence grade", async t => {
  const f = await fixture(t);
  const ref = "personal-message:a-main:1", occurred_at = day();
  f.host.evidence.set(ref, { ref, content: "I usually arrange meetings before lunch", occurred_at });
  const candidate = (await f.tool("create", { ...createArgs("morning-meetings"), evidence: "inferred", sources: [{ kind: "message", ref, occurred_at: day(50) }] }, "a-worker")).record;
  assert.equal(candidate.sources[0].occurred_at, occurred_at);
  const projection = (await f.plugin.invokeCapability({ capability: "agent.pre_step", input: { session_id: "a-main", messages: [{ role: "user", content: "Hi" }] } }, f.host, execution)).output;
  const snapshot = JSON.parse(projection.append_messages[0].content.split("\n").at(-1));
  assert(snapshot.candidates.some(item => item.id === candidate.id));
  assert(!snapshot.records.some(item => item.id === candidate.id));
  const evidence = await f.tool("evidence", { id: candidate.id });
  assert.equal(evidence.sources[0].content, "I usually arrange meetings before lunch");
  await assert.rejects(f.tool("accept", { id: candidate.id, expected_revision: 1 }, "a-worker"), /main/);
  await assert.rejects(f.tool("evidence", { id: candidate.id }, "a-worker"), /main/);
  const accepted = (await f.tool("accept", { id: candidate.id, expected_revision: 1 })).record;
  assert.equal(accepted.status, "active"); assert.equal(accepted.evidence, "inferred"); assert.equal(accepted.tier, "detail");
  for (const field of ["content", "sources", "source_at", "effective_at", "expires_at"]) assert.deepEqual(accepted[field], candidate[field]);
  assert.equal(accepted.contributions.at(-1).operation, "accept");
  assert.deepEqual((await f.tool("accept", { id: candidate.id, expected_revision: 1 })).record, accepted, "exact retry replays the committed receipt");
  await f.tool("forget", { id: candidate.id, expected_revision: 2 });
  await assert.rejects(f.tool("create", { ...createArgs("renamed"), sources: [{ kind: "message", ref, occurred_at }] }, "a-worker"), /forgotten_source/);
});

test("candidate admission fails closed for unsupported evidence, unavailable source, expiry and forged changes", async t => {
  const f = await fixture(t);
  const unsupported = (await f.tool("create", createArgs("legacy-candidate"), "a-worker")).record;
  await assert.rejects(f.tool("accept", { id: unsupported.id, expected_revision: 1 }), /source|evidence/);
  const ref = "personal-message:a-main:2";
  f.host.evidence.set(ref, { ref, content: "Temporary context", occurred_at: day() });
  const candidate = (await f.tool("create", { ...createArgs("expires"), sources: [{ kind: "message", ref, occurred_at: day() }], expires_at: new Date(Date.now() - 1000).toISOString() }, "a-worker")).record;
  await assert.rejects(f.tool("accept", { id: candidate.id, expected_revision: 1 }), /expired|effective/);
  await assert.rejects(f.tool("accept", { id: candidate.id, expected_revision: 1, content: "replacement" }), /unexpected/);
  const ref2 = "personal-message:a-main:3";
  f.host.evidence.set(ref2, { ref: ref2, content: "Another context", occurred_at: day() });
  const lost = (await f.tool("create", { ...createArgs("lost"), sources: [{ kind: "message", ref: ref2, occurred_at: day() }] }, "a-worker")).record;
  f.host.evidence.delete(ref2);
  await assert.rejects(f.tool("accept", { id: lost.id, expected_revision: 1 }), /evidence/);
  assert.equal((await f.tool("read", { id: lost.id })).record.status, "candidate");
});


test("accept uses the reviewed revision and never admits a candidate changed or forgotten after review", async t => {
  const f = await fixture(t);
  const firstRef = "personal-message:a-main:11", nextRef = "personal-message:a-main:12";
  f.host.evidence.set(firstRef, { ref: firstRef, content: "First source", occurred_at: day() });
  f.host.evidence.set(nextRef, { ref: nextRef, content: "Newer source", occurred_at: day(30) });
  const candidate = (await f.tool("create", { ...createArgs("revision-candidate"), sources: [{ kind: "message", ref: firstRef }] }, "a-worker")).record;
  const reviewed = await f.tool("evidence", { id: candidate.id });
  await f.tool("revise", { id: candidate.id, expected_revision: 1, content: "Revised candidate", evidence: "inferred", sources: [{ kind: "message", ref: nextRef }] }, "a-worker");
  await assert.rejects(f.tool("accept", { id: candidate.id, expected_revision: reviewed.record.revision }), /revision_conflict/);
  await f.tool("forget", { id: candidate.id, expected_revision: 2 });
  await assert.rejects(f.tool("accept", { id: candidate.id, expected_revision: 3 }), /forgotten/);
});


test("source review cannot return a forgotten candidate after an in-flight evidence read", async t => {
  const f = await fixture(t), ref = "personal-message:a-main:21";
  f.host.evidence.set(ref, { ref, content: "User fact", occurred_at: day() });
  const candidate = (await f.tool("create", { ...createArgs("forget-in-review"), sources: [{ kind: "message", ref }] }, "a-worker")).record;
  const originalCall = f.host.call.bind(f.host);
  let entered, release;
  const reading = new Promise(resolve => { entered = resolve; });
  const gate = new Promise(resolve => { release = resolve; });
  f.host.call = async (service, params) => {
    if (service === "host.service.call" && params.service === "host.personal" && params.params.operation === "memory.source") { entered(); await gate; }
    return originalCall(service, params);
  };
  const pending = f.tool("evidence", { id: candidate.id });
  await reading;
  await f.tool("forget", { id: candidate.id, expected_revision: 1 });
  release();
  await assert.rejects(pending, /changed|revision/);
});


// Failure cases: retained legacy bytes overriding newer journal state, a lost
// committed response being rewritten with a new timestamp, and stale intent replay.
test("journal authority overrides legacy rows and replays exact commits after lost responses and restart", async t => {
  const f = await fixture(t);
  const original = (await f.tool("create", createArgs("legacy-record"))).record;
  const legacy = f.host.journal.get(original.id);
  f.host.storage.set(`personal.records.v1.${original.id}`, legacy);
  f.host.journal.delete(original.id);
  const correction = { id: original.id, expected_revision: 1, content: "Updated journal fact", evidence: "explicit", sources: source("journal-correction", 10) };
  f.host.loseCommitReply = true;
  await assert.rejects(f.tool("correct", correction), /reply lost/);
  const committed = f.host.journal.get(original.id);
  const restarted = await fixture(t, f.host);
  const reordered = { sources: correction.sources.map(({ occurred_at, ref, kind }) => ({ occurred_at, ref, kind })), evidence: correction.evidence, content: correction.content, expected_revision: correction.expected_revision, id: correction.id };
  const recovered = (await restarted.tool("correct", reordered)).record;
  assert.equal(recovered.revision, 2);
  assert.equal(f.host.journal.get(original.id), committed, "retry must preserve generated timestamps and exact prepared bytes");
  assert.equal(f.host.storage.get(`personal.records.v1.${original.id}`), legacy, "legacy remains read-only compatibility input");
  assert.equal((await restarted.tool("query", {})).records[0].content, "Updated journal fact");
  await assert.rejects(restarted.tool("correct", { ...correction, content: "Changed stale intent" }), /revision_conflict/);
  await restarted.tool("forget", { id: original.id, expected_revision: 2 });
  const latest = await fixture(t, f.host);
  assert.equal((await latest.tool("query", {})).records.length, 0);
  assert.equal((await latest.tool("read", { id: original.id })).record.status, "forgotten");
  await assert.rejects(latest.tool("correct", correction), /forgotten_record/);
  await assert.rejects(latest.tool("create", createArgs("legacy-resurrection", "legacy-record")), /forgotten_source/);
});
