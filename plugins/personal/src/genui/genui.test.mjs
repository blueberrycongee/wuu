import assert from "node:assert/strict";
import { test, after } from "node:test";
import { build } from "esbuild";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { pathToFileURL } from "node:url";

// Failure contracts: untrusted programs and state, owner/session spoofing,
// stale writes, duplicate submit, lost ACK, restart, and cross-surface isolation.
const output = await mkdtemp(`${tmpdir()}/personal-genui-test-`);
await build({ entryPoints: ["service.ts", "schema.ts", "fixtures.ts", "model-output.ts"].map(name => new URL(name, import.meta.url).pathname), outdir: output, bundle: true, platform: "node", format: "esm" });
const { PersonalGenUI, toolSource } = await import(pathToFileURL(`${output}/service.js`));
const { validateSpec, validateState, evaluate } = await import(pathToFileURL(`${output}/schema.js`));
const { fixtures } = await import(pathToFileURL(`${output}/fixtures.js`));
const { publishModelOutput } = await import(pathToFileURL(`${output}/model-output.js`));
const binding = { owner: "fixture-owner", session: "fixture-main", agent: "fixture-twin" };
const owner = { ...binding, domain: "personal", source: "owner" };
const model = { ...owner, source: "model" };
function harness() {
  const storage = new Map(), deliveries = new Map(), receipts = new Map(); let sends = 0, loseACK = false;
  const host = { async call(method, p) {
    if (method === "host.storage.get") return { value: storage.get(p.key) ?? null };
    if (method === "host.storage.compare_exchange") {
      if ((storage.get(p.key) ?? null) !== p.expected) return { swapped: false, value: storage.get(p.key) ?? null };
      storage.set(p.key, p.value); return { swapped: true, value: p.value };
    }
    if (method === "host.session.inspect") return { receipt: receipts.get(p.request_id) };
    if (method === "host.session.send") {
      assert.equal(p.user_input_request_id, p.resume_receipt_id ? receipts.get(p.request_id).request_id : JSON.parse(p.input.context_blocks[0].content).eventId);
      if (!receipts.has(p.request_id)) receipts.set(p.request_id, { request_id: p.user_input_request_id, host_request_id: p.request_id, session_id: p.session_id, user_space_id: binding.owner, product_domain: "personal", agent_id: binding.agent, plugin_id: "personal", receipt_id: `receipt-${p.user_input_request_id}`, receipt_state: "accepted", execution_state: "queued", revision: 1 });
      if (p.resume_receipt_id) { assert.equal(p.input.prompt, ""); assert.equal(p.disable_tools, undefined); receipts.get(p.request_id).execution_state = "queued"; }
      if (!deliveries.has(p.request_id)) { deliveries.set(p.request_id, p); sends++; }
      if (loseACK) { loseACK = false; throw new Error("fixture lost ACK"); }
      return { state: "queued", session_id: p.session_id, queue_id: p.request_id, receipt: receipts.get(p.request_id) };
    }
    throw new Error(`unexpected host method ${method}`);
  } };
  return { service: () => new PersonalGenUI(host, binding), host, storage, deliveries, receipts, sends: () => sends, dropACK: () => { loseACK = true; } };
}
const publish = (service, name = "checklist", id = name) => service.publish({ ...toolSource(id, `turn-${id}`, "fixture-ui-tool"), spec: structuredClone(fixtures[name]) }, model);
const event = (surface, kind, eventId, state = surface.state) => ({ surfaceId: surface.surfaceId, actionId: kind, expectedRevision: surface.revision, eventId, state });

test("bounded component language accepts three compositions and rejects executable/unknown fields", () => {
  for (const spec of Object.values(fixtures)) validateSpec(spec);
  for (const change of [s => s.schemaVersion = 99, s => s.url = "https://example.com", s => s.tree.children.push({ id: "bad", type: "script", value: "alert(1)" }), s => s.actions.push({ id: "escape", kind: "tool", label: "Run" }), s => s.fields.__proto__ = { bad: true }, s => s.fallbackText = "x".repeat(70000)]) {
    const spec = structuredClone(fixtures.checklist); change(spec); assert.throws(() => validateSpec(spec));
  }
  const deep = structuredClone(fixtures.checklist);
  for (let i = 0; i < 15; i++) deep.tree = { id: `deep${i}`, type: "stack", children: [deep.tree] };
  assert.throws(() => validateSpec(deep));
  assert.throws(() => evaluate({ op: "fetch", args: [] }, {}));
  const mistyped = structuredClone(fixtures.calculator); mistyped.tree.children[0].visible = { get: "count" };
  assert.throws(() => validateSpec(mistyped));
  const wrongMath = structuredClone(fixtures.calculator); wrongMath.tree.children.push({ id: "badmath", type: "text", value: { op: "mul", args: [true, 2] } });
  assert.throws(() => validateSpec(wrongMath));
  assert.equal(evaluate({ op: "mul", args: [{ get: "price" }, { get: "count" }] }, { price: 4, count: 3 }), 12);
  assert.throws(() => evaluate({ op: "div", args: [1, 0] }, {}));
});

test("state validation rejects shape, prototype, bounds and list overflow", () => {
  const spec = fixtures.calculator;
  assert.throws(() => validateState(spec, { ...Object.fromEntries(Object.entries(spec.fields).map(([k,v]) => [k,v.initial])), count: "3" }));
  const h = fixtures.checklist;
  assert.throws(() => validateState(h, { items: Array(60).fill({ text: "a", done: false }) }));
  assert.throws(() => validateState(h, JSON.parse('{"__proto__":{"admin":true}}')));
});

test("publish is model-only, owner scoped, immutable and replay safe", async () => {
  const h = harness(), s = h.service();
  await assert.rejects(() => publish({ publish: (p) => s.publish(p, owner) }), /forbidden/);
  const a = await publish(s), again = await publish(s);
  assert.deepEqual(a, again);
  await assert.rejects(() => s.publish({ ...toolSource("checklist", "turn-checklist", "fixture-ui-tool"), messageId: "other", spec: fixtures.calculator }, model), /idempotency_conflict/);
  for (const actor of [{ ...owner, owner: "other" }, { ...owner, session: "worker" }, { ...owner, domain: "coding" }, model]) {
    await assert.rejects(() => s.act(event(a, "save", "bad"), actor), /forbidden/);
  }
  await assert.rejects(() => s.act({ ...event(a, "save", "bad"), owner: binding.owner }, owner), /invalid/);
  assert.equal(h.sends(), 0);
});

test("CAS persists independent surfaces across service restart and rejects stale and conflicting retries", async () => {
  const h = harness(), s = h.service(), a = await publish(s), b = await publish(s, "checklist", "second");
  const state = structuredClone(a.state); state.items[0].done = true;
  const e = event(a, "save", "save-one", state);
  const results = await Promise.all([s.act(e, owner), h.service().act(e, owner)]);
  assert.equal(results[0].surface.revision, 1); assert.deepEqual(results[0], results[1]);
  assert.equal((await h.service().read(a.surfaceId, owner)).state.items[0].done, true);
  assert.equal((await s.read(b.surfaceId, owner)).state.items[0].done, false);
  await assert.rejects(() => s.act(event(a, "save", "stale"), owner), /revision_conflict/);
  await assert.rejects(() => s.act({ ...e, state: a.state }, owner), /idempotency_conflict/);
  await assert.rejects(() => s.act({ ...e, eventId: "unknown", actionId: "tasks-stop" }, owner), /unknown_action/);
  assert.equal(h.sends(), 0);
});

test("explicit submit durably freezes actual state, recovers lost ACK with the same request ID", async () => {
  const h = harness(), s = h.service(), a = await publish(s, "calculator");
  const state = { ...a.state, count: 7, price: 13 };
  h.dropACK(); const request = event(a, "submit", "continue-one", state);
  const pending = await s.act(request, owner);
  assert.equal(pending.delivery, "pending"); assert.equal(h.sends(), 0);
  assert.equal(pending.submission.requestId, request.eventId);
  await s.retrySubmission(a.surfaceId, owner, h.host); assert.equal(h.sends(), 1);
  const recovered = await h.service().act(request, owner);
  assert.equal(recovered.delivery, "accepted"); assert.equal(h.sends(), 1);
  const sent = [...h.deliveries.values()][0];
  assert.equal(sent.session_id, binding.session);
  assert.equal(sent.disable_tools, true);
  const data = JSON.parse(sent.input.context_blocks[0].content);
  assert.equal(data.state.count, 7); assert.equal(data.state.price, 13);
  assert.equal(data.actionId, "submit"); assert.equal(data.revision, 1);
  assert.equal(data.businessStateChanged, false);
  const current = await s.read(a.surfaceId, owner);
  await assert.rejects(() => s.act(event(current, "submit", "second-submit"), owner), /already_submitted/);
});

test("temporary fields never persist and unknown capabilities cannot be smuggled through state", async () => {
  const h = harness(), s = h.service(), a = await publish(s, "comparison");
  const result = await s.act(event(a, "save", "draft", { ...a.state, expanded: true }), owner);
  assert.equal(result.surface.state.expanded, undefined);
  await assert.rejects(() => s.act(event(result.surface, "save", "forge", { ...result.surface.state, tool: "complete_task" }), owner), /invalid/);
});

test("model output carries typed UI metadata and fallback; model cannot choose source identity", async () => {
  const h = harness(), s = h.service();
  const result = await publishModelOutput(s, { spec: fixtures.checklist }, model, toolSource("call-one", "turn-one", "fixture-ui-tool"));
  assert.equal(result.structured_content.type, "personal.genui");
  assert.equal(result.content[0].text, fixtures.checklist.fallbackText);
  assert.equal(result.meta.personal_genui.messageId, toolSource("call-one", "turn-one", "fixture-ui-tool").messageId);
  await assert.rejects(() => publishModelOutput(s, { spec: fixtures.checklist, messageId: "forged" }, model, toolSource("call-one", "turn-one", "fixture-ui-tool")), /invalid/);
});

test("history uses persisted tool result plus source identity, never an assistant call ID alone", async () => {
  const h = harness(), s = h.service();
  const source = toolSource("reused-call", "turn-one", "fixture-ui-tool");
  const first = await publishModelOutput(s, { spec: fixtures.checklist }, model, source);
  const second = await publishModelOutput(s, { spec: fixtures.checklist }, model, toolSource("reused-call", "turn-two", "fixture-ui-tool"));
  assert.notEqual(first.structured_content.surfaceId, second.structured_content.surfaceId);
  const part = await s.projectToolResult(JSON.stringify(first), "reused-call", "fixture-ui-tool", owner);
  assert.equal(part.parts[0].surface.surfaceId, first.structured_content.surfaceId);
  assert.deepEqual(await s.projectToolResult(JSON.stringify(first), "another-call", "fixture-ui-tool", owner), { parts: [] });
  assert.deepEqual(await s.projectToolResult(JSON.stringify(first), "reused-call", "another-tool", owner), { parts: [] });
  assert.deepEqual(await s.projectToolResult(JSON.stringify({ content: [{ type: "text", text: JSON.stringify(first) }] }), "reused-call", "fixture-ui-tool", owner), { parts: [] });
  await assert.rejects(() => s.projectToolResult(JSON.stringify(first), "reused-call", "fixture-ui-tool", { ...owner, session: "other" }), /forbidden/);
});

test("a refresh can inspect and recover pending delivery without minting a second user event", async () => {
  const h = harness(), a = await publish(h.service()); h.dropACK();
  await h.service().act(event(a, "submit", "submit-after-refresh"), owner);
  assert.equal((await h.service().inspect(a.surfaceId, owner)).delivery, "pending");
  const recovered = await h.service().retrySubmission(a.surfaceId, owner, h.host);
  assert.equal(recovered.delivery, "accepted"); assert.equal(h.sends(), 1);
  await h.service().retrySubmission(a.surfaceId, owner); assert.equal(h.sends(), 1);
  await assert.rejects(() => h.service().retrySubmission(a.surfaceId, model), /forbidden/);
});

test("held inputs require a fresh explicit resume; unknown outcomes never replay", async () => {
  const h = harness(), a = await publish(h.service());
  const request = event(a, "submit", "held-submit");
  await h.service().act(request, owner);
  await assert.rejects(() => h.service().retrySubmission(a.surfaceId, owner), /live_client/);
  await h.service().retrySubmission(a.surfaceId, owner, h.host);
  const receipt = [...h.receipts.values()][0]; receipt.execution_state = "held";
  const held = await h.service().inspect(a.surfaceId, owner);
  assert.equal(held.delivery, "held"); assert.equal(held.submission.resumeReceiptId, receipt.receipt_id);
  await assert.rejects(() => h.service().retrySubmission(a.surfaceId, owner, h.host), /resume/);
  await h.service().retrySubmission(a.surfaceId, owner, h.host, receipt.receipt_id);
  assert.equal(receipt.execution_state, "queued"); assert.equal(h.sends(), 1);
  receipt.receipt_state = "in_history"; receipt.execution_state = "unconfirmed";
  const uncertain = await h.service().retrySubmission(a.surfaceId, owner, h.host);
  assert.equal(uncertain.delivery, "unconfirmed"); assert.equal(uncertain.submission, undefined); assert.equal(h.sends(), 1);
  receipt.session_id = "another-session";
  await assert.rejects(() => h.service().inspect(a.surfaceId, owner), /receipt_scope/);
});

test("concurrent distinct stale mutations have one winner and cannot lose accepted data", async () => {
  const h = harness(), a = await publish(h.service(), "calculator");
  const outcomes = await Promise.allSettled([h.service().act(event(a, "save", "one", { count: 9, price: 100 }), owner), h.service().act(event(a, "save", "two", { count: 6, price: 200 }), owner)]);
  assert.equal(outcomes.filter(r => r.status === "fulfilled").length, 1);
  assert.equal(outcomes.filter(r => r.status === "rejected" && String(r.reason).includes("revision_conflict")).length, 1);
  const saved = await h.service().read(a.surfaceId, owner); assert.equal(saved.revision, 1);
  assert.ok(saved.state.count === 9 || saved.state.count === 6);
});

after(() => rm(output, { recursive: true, force: true }));

test("final UTF-8 submit envelope is budgeted before freezing and oversized input remains editable", async () => {
  const h = harness(), s = h.service();
  const spec = { schemaVersion: 1, title: "Large draft", fallbackText: "Large draft fallback", fields: Object.fromEntries(Array.from({ length: 16 }, (_, i) => [`f${i}`, { type: "string", scope: "persistent", initial: "", maxLength: 4000 }])), tree: { id: "root", type: "stack", children: [] }, actions: [{ id: "submit", kind: "submit", label: "Explain" }] };
  const a = await s.publish({ ...toolSource("budget", "budget-turn", "fixture-ui-tool"), spec }, model);
  const state = Object.fromEntries(Object.keys(spec.fields).map(key => [key, "界".repeat(1300)]));
  const request = event(a, "submit", "budget-event", state);
  while (Buffer.byteLength(JSON.stringify(request)) + 4 <= 65530) state.f0 += "😀";
  while (Buffer.byteLength(JSON.stringify(request)) < 65530) state.f0 += "x";
  validateState(spec, state);
  assert.equal(Buffer.byteLength(JSON.stringify(request)), 65530);
  await assert.rejects(() => s.act(request, owner), /submission_too_large/);
  const unchanged = await s.read(a.surfaceId, owner); assert.equal(unchanged.submitted, false); assert.equal(unchanged.revision, 0); assert.equal(h.sends(), 0);
  const smaller = { ...a.state, f0: "修改后的选择😀" };
  await s.act(event(unchanged, "submit", "corrected-event", smaller), owner);
  await s.retrySubmission(a.surfaceId, owner, h.host);
  const sent = [...h.deliveries.values()][0];
  assert.ok(Buffer.byteLength(JSON.stringify(sent)) <= 65536); assert.ok(Buffer.byteLength(sent.input.context_blocks[0].content) <= 65536);
});

test("definitive pre-admission refusal reopens a surface, while ambiguous attempts never unlock or change identity", async () => {
  const h = harness(), s = h.service(), a = await publish(s, "calculator");
  const rejection = () => Object.assign(new Error("invalid send request"), { name: "RuntimeHostServiceError", code: "invalid_request" });
  const rejectedHost = { async call() { throw rejection(); } };
  await s.act(event(a, "submit", "rejected-once"), owner);
  const refused = await s.retrySubmission(a.surfaceId, owner, rejectedHost);
  assert.equal(refused.delivery, "rejected"); assert.equal(refused.surface.submitted, false); assert.equal(refused.surface.revision, 2);
  await assert.rejects(() => s.act(event(a, "submit", "rejected-once"), owner), /submission_rejected/);
  await s.act(event(refused.surface, "submit", "corrected-submit", { count: 9, price: 50 }), owner);
  await s.retrySubmission(a.surfaceId, owner, h.host); assert.equal(h.sends(), 1);
  const b = await publish(s, "calculator", "unknown-surface");
  await s.act(event(b, "submit", "unknown-event"), owner);
  const ambiguous = await s.retrySubmission(b.surfaceId, owner, { async call() { throw new Error("transport disappeared"); } });
  assert.equal(ambiguous.delivery, "pending"); assert.equal(ambiguous.surface.submitted, true);
  const laterRefusal = await s.retrySubmission(b.surfaceId, owner, rejectedHost);
  assert.equal(laterRefusal.delivery, "pending"); assert.equal(laterRefusal.surface.submitted, true);
  await assert.rejects(() => s.act(event(laterRefusal.surface, "submit", "replacement-event"), owner), /already_submitted/);
});

test("a legacy G1 oversized outbox is recoverable without sending or discarding its choices", async () => {
  const h = harness(), s = h.service();
  const spec = { schemaVersion: 1, title: "Legacy draft", fallbackText: "Legacy fallback", fields: { rows: { type: "list", scope: "persistent", maxItems: 16, initial: [], item: { text: { type: "string", initial: "", maxLength: 4000 } } } }, tree: { id: "root", type: "stack", children: [] }, actions: [{ id: "submit", kind: "submit", label: "Explain" }] };
  const a = await s.publish({ ...toolSource("legacy", "legacy-turn", "fixture-ui-tool"), spec }, model);
  const state = { rows: Array.from({ length: 16 }, () => ({ text: "界".repeat(1300) })) };
  const request = event(a, "submit", "legacy-event", state);
  while (Buffer.byteLength(JSON.stringify(request)) + 4 <= 65530) state.rows[0].text += "😀";
  const key = [...h.storage.keys()][0], stored = JSON.parse(h.storage.get(key));
  stored.surface = { ...stored.surface, state, submitted: true, revision: 1 };
  const submission = { schemaVersion: 1, surfaceId: a.surfaceId, messageId: a.messageId, actionId: "submit", eventId: request.eventId, revision: 1, state, businessStateChanged: false };
  assert.ok(Buffer.byteLength(JSON.stringify(submission)) > 65536);
  stored.receipts[request.eventId] = { fingerprint: "legacy-fingerprint", revision: 1, submission };
  h.storage.set(key, JSON.stringify(stored));
  const result = await h.service().retrySubmission(a.surfaceId, owner, h.host);
  assert.equal(result.delivery, "rejected"); assert.equal(result.surface.submitted, false); assert.deepEqual(result.surface.state, state); assert.equal(h.sends(), 0);
  await s.act(event(result.surface, "submit", "smaller-event", { rows: [{ text: "修正后的选择" }] }), owner);
  await s.retrySubmission(a.surfaceId, owner, h.host); assert.equal(h.sends(), 1);
});

test("a concurrent unknown attempt prevents another attempt's definitive refusal from thawing the outbox", async () => {
  const h = harness(), s = h.service(), a = await publish(s, "calculator");
  await s.act(event(a, "submit", "concurrent-event"), owner);
  let enter, release;
  const entered = new Promise(resolve => { enter = resolve; });
  const wait = new Promise(resolve => { release = resolve; });
  const first = s.retrySubmission(a.surfaceId, owner, { async call() { enter(); await wait; throw Object.assign(new Error("service host.session.send call failed: the selected agent engine cannot enforce disable_tools"), { name: "RuntimeHostServiceError", code: "service_unavailable" }); } });
  await entered;
  await s.retrySubmission(a.surfaceId, owner, { async call() { throw new Error("unknown transport outcome"); } });
  release(); const result = await first;
  assert.equal(result.delivery, "pending"); assert.equal(result.surface.submitted, true);
  await s.retrySubmission(a.surfaceId, owner, h.host); assert.equal(h.sends(), 1);
});

test("registry refusal mapping is exact and generic service_unavailable remains fenced", async () => {
  for (const reason of ["context block exceeds 65536 bytes", "the selected agent engine cannot enforce disable_tools", "user input does not match the admitted client submission", "synthetic uncertain transport outcome", "the selected agent engine cannot enforce disable_tools after acceptance"]) {
    const h = harness(), s = h.service(), a = await publish(s, "calculator");
    await s.act(event(a, "submit", "registry-refusal"), owner);
    const result = await s.retrySubmission(a.surfaceId, owner, { async call() { throw Object.assign(new Error(`service host.session.send call failed: ${reason}`), { name: "RuntimeHostServiceError", code: "service_unavailable" }); } });
    const known = !reason.startsWith("synthetic") && !reason.endsWith("after acceptance");
    assert.equal(result.delivery, known ? "rejected" : "pending");
    assert.equal(result.surface.submitted, !known);
    assert.equal(h.sends(), 0);
  }
});

// Autosync must remain quiet, reject stale owners, and freeze unsaved input for a queued turn.
test("autosync persists without delivery and context captures unsynced edits with owner isolation", async () => {
  const h = harness(), s = h.service(), a = await publish(s), b = await publish(s, "checklist", "other");
  const state = structuredClone(a.state); state.items[0].done = true;
  const request = { surfaceId: a.surfaceId, expectedRevision: 0, state };
  const saved = await s.sync(request, owner);
  assert.equal(saved.surface.revision, 1);
  assert.equal((await s.sync(request, owner)).surface.revision, 1);
  assert.equal((await h.service().read(a.surfaceId, owner)).state.items[0].done, true);
  assert.equal((await s.read(b.surfaceId, owner)).state.items[0].done, false);
  await assert.rejects(() => s.sync({ ...request, state: a.state }, owner), /revision_conflict/);
  await assert.rejects(() => s.sync(request, model), /forbidden/);
  const draft = structuredClone(state); draft.items[0].text = "latest unsynced edit";
  const blocks = await s.context([{ surfaceId: a.surfaceId, state: draft }, { surfaceId: b.surfaceId, state: b.state }], owner);
  draft.items[0].text = "later edit";
  const data = JSON.parse(blocks[0].content);
  assert.equal(data.surfaces[0].state.items[0].text, "latest unsynced edit");
  assert.equal(data.surfaces[1].state.items[0].done, false);
  await assert.rejects(() => s.context([{ surfaceId: a.surfaceId, state: { ...state, tool: "run" } }], owner), /invalid/);
  await assert.rejects(() => s.context([{ surfaceId: a.surfaceId, state }], { ...owner, agent: "other" }), /forbidden/);
  await assert.rejects(() => s.context([{ surfaceId: a.surfaceId, state }, { surfaceId: a.surfaceId, state }], owner), /invalid/);
  assert.equal(h.sends(), 0);
});
