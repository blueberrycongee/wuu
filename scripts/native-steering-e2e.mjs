#!/usr/bin/env node
// Real Wuu stdio server and a controlled native subprocess. No credentials,
// external inference, or user data. Set WUU_E2E_CORE to a source-built binary.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { appendFile, chmod, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { DatabaseSync } from "node:sqlite";
import { fileURLToPath } from "node:url";

if (process.argv.includes("--native-fixture")) {
  const turns = new Map();
  const statePath = process.env.WUU_STEER_NATIVE_LOG + ".state.json";
  const threads = new Map(existsSync(statePath) ? JSON.parse(readFileSync(statePath, "utf8")) : []);
  const save = () => writeFileSync(statePath, JSON.stringify([...threads]));
  const history = threadID => ({ id: threadID, turns: (threads.get(threadID) ?? []).map(turn => ({ id: turn.id, status: turn.status, items: turn.items, itemsView: "full" })) });
  let threadIndex = 0, turnIndex = 0;
  const write = value => process.stdout.write(JSON.stringify(value) + "\n");
  const log = value => appendFile(process.env.WUU_STEER_NATIVE_LOG, JSON.stringify({ ...value, pid: process.pid }) + "\n");
  const reply = (id, result) => write({ id, result });
  const finish = (turnID, status = "completed") => {
    const turn = turns.get(turnID);
    if (!turn) return;
    turn.status = status;
    const text = "Native answer: " + turn.inputs.join(" | ");
    const item = { id: "answer-" + turnID, type: "agentMessage", text, phase: "final_answer" };
    turn.items.push(item);
    save();
    write({ method: "item/completed", params: { threadId: turn.threadID, turnId: turnID, item } });
    write({ method: "turn/completed", params: { threadId: turn.threadID, turn: { id: turnID, status } } });
  };
  const control = createServer(async (req, res) => {
    let raw = ""; for await (const part of req) raw += part;
    const body = JSON.parse(raw || "{}");
    if (body.action === "finish") finish(body.turnID, body.status);
    if (body.action === "ack") {
      const turn = turns.get(body.turnID);
      if (turn?.heldReply) { reply(turn.heldReply, { turnId: body.turnID }); delete turn.heldReply; }
    }
    res.end("ok");
  });
  await new Promise(resolve => control.listen(0, "127.0.0.1", resolve));
  await log({ control: control.address().port });
  createInterface({ input: process.stdin }).on("line", async line => {
    const request = JSON.parse(line), p = request.params ?? {};
    await log({ request });
    switch (request.method) {
      case "initialize": reply(request.id, { userAgent: "synthetic-native-steering", codexHome: process.env.HOME }); break;
      case "model/list": reply(request.id, { data: [{ id: "fixture", model: "fixture", displayName: "Fixture", isDefault: true }], nextCursor: null }); break;
      case "thread/start": {
        const id = `native-thread-${process.pid}-${++threadIndex}`;
        threads.set(id, []); save();
        reply(request.id, { thread: { id }, model: "fixture" }); break;
      }
      case "thread/resume": reply(request.id, { thread: { id: p.threadId }, model: "fixture" }); break;
      case "thread/read": reply(request.id, { thread: history(p.threadId) }); break;
      case "thread/fork": {
        const source = threads.get(p.threadId) ?? [];
        const index = source.findIndex(turn => turn.id === (p.lastTurnId ?? p.beforeTurnId));
        if (index < 0) { write({ id: request.id, error: { code: -32602, message: "native cutoff not found" } }); break; }
        const id = `native-thread-${process.pid}-${++threadIndex}`;
        threads.set(id, structuredClone(source.slice(0, index + (p.lastTurnId ? 1 : 0)))); save();
        reply(request.id, { thread: history(id) }); break;
      }
      case "thread/unsubscribe": reply(request.id, {}); break;
      case "turn/start": {
        const id = `native-turn-${process.pid}-${++turnIndex}`;
        const turn = { id, threadID: p.threadId, status: "inProgress", inputs: p.input.map(v => v.text ?? v.type),
          items: [{ id: "user-" + id, type: "userMessage", content: p.input }] };
        turns.set(id, turn);
        threads.set(p.threadId, [...(threads.get(p.threadId) ?? []), turn]); save();
        reply(request.id, { turn: { id } });
        write({ method: "item/agentMessage/delta", params: { threadId: p.threadId, turnId: id, itemId: "waiting-" + id, delta: "Native turn is waiting." } });
        await log({ started: id, threadID: p.threadId });
        break;
      }
      case "turn/steer": {
        const turn = turns.get(p.expectedTurnId), text = p.input.map(v => v.text ?? v.type).join(" ");
        if (text.startsWith("UNSUPPORTED")) { write({ id: request.id, error: { code: -32601, message: "Method not found" } }); break; }
        if (!turn || turn.status !== "inProgress" || turn.threadID !== p.threadId || text.startsWith("REJECT")) {
          write({ id: request.id, error: { code: -32600, message: "no matching active turn to steer" } }); break;
        }
        turn.inputs.push(text);
        const item = { id: "user-" + p.clientUserMessageId, type: "userMessage", content: p.input };
        turn.items.push(item); save();
        write({ method: "item/completed", params: { threadId: p.threadId, turnId: p.expectedTurnId, item } });
        await log({ accepted: p.clientUserMessageId, turnID: p.expectedTurnId });
        if (text.startsWith("INTERNAL")) { write({ id: request.id, error: { code: -32603, message: "native submission reply was lost" } }); break; }
        if (text.startsWith("DELAY") || text.startsWith("LOST")) { turn.heldReply = request.id; break; }
        reply(request.id, { turnId: text.startsWith("BAD_ID") ? "wrong-native-turn" : p.expectedTurnId });
        break;
      }
      case "turn/interrupt": finish(p.turnId, "interrupted"); reply(request.id, {}); break;
      case "initialized": break;
      default: if (request.id) reply(request.id, {});
    }
  });
  process.stdin.on("end", () => { control.close(); process.exit(0); });
} else {
  await run();
}

async function run() {
  const binary = resolve(process.env.WUU_E2E_CORE || "bin/wuu");
  const output = resolve(process.env.WUU_STEER_E2E_OUTPUT || ".tmp/e2e/native-steering");
  await mkdir(output, { recursive: true });
  const scratch = await mkdtemp(join(output, "run-")), home = join(scratch, "home");
  await mkdir(home);
  const nativeLog = join(scratch, "native.jsonl"), fake = join(scratch, "native-fixture"), config = join(home, "config.json");
  await writeFile(nativeLog, "");
  const quote = value => "'" + value.replaceAll("'", "'\\''") + "'";
  await writeFile(fake, `#!/bin/sh\nexec ${quote(process.execPath)} ${quote(fileURLToPath(import.meta.url))} --native-fixture "$@"\n`);
  await chmod(fake, 0o755);
  await writeFile(config, JSON.stringify({ default_provider: "fixture", providers: { fixture: { type: "openai-compatible", base_url: "http://127.0.0.1:1/v1", api_key: "synthetic-only", model: "fixture", models: { fixture: {} } } }, engines: { ...Object.fromEntries(["claude", "cursor", "devin", "dsh", "grok", "hermes", "pi", "opencode", "antigravity"].map(id => [id, { enabled: false }])), codex: { enabled: true, binary_path: fake } }, agent: { permission_mode: "standard" }, ptc: { enabled: false } }));
  const transcript = [], clients = [], events = new EventEmitter(), checks = [];
  const results = { status: "failed", scratch, checks, binary, binary_sha256: createHash("sha256").update(await readFile(binary)).digest("hex"), limitations: ["Synthetic native protocol subprocess; no official engine binary, external inference, desktop UI, or crash-durability/fsync claim."] };
  let sequence = 0, db;
  function check(name, fn) { fn(); checks.push(name); console.log("PASS", name); }
  function waitFor(predicate, label, timeout = 12000) {
    const found = transcript.find(predicate); if (found) return Promise.resolve(found);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { events.off("message", listener); reject(new Error("Timeout: " + label)); }, timeout);
      function listener(message) { if (!predicate(message)) return; clearTimeout(timer); events.off("message", listener); resolve(message); }
      events.on("message", listener);
    });
  }
  const native = async () => (await readFile(nativeLog, "utf8")).split("\n").filter(Boolean).map(JSON.parse);
  async function nativeWait(predicate, label, timeout = 12000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      const found = (await native()).find(predicate); if (found) return found;
      await new Promise(resolve => setTimeout(resolve, 20));
    }
    throw new Error("Timeout: " + label);
  }
  async function control(turnID, action = "finish") {
    const records = await native(), started = records.find(row => row.started === turnID), port = records.find(row => row.pid === started.pid && row.control)?.control;
    assert(port); await fetch(`http://127.0.0.1:${port}`, { method: "POST", body: JSON.stringify({ action, turnID }) });
  }
  async function raw(client, method, params = {}) {
    const id = String(++sequence); client.child.stdin.write(JSON.stringify({ id, method, params }) + "\n");
    return waitFor(message => message.client === client.name && message.id === id, method);
  }
  async function rpc(client, method, params) { const response = await raw(client, method, params); assert(!response.error, `${method}: ${JSON.stringify(response.error)}`); return response.result; }
  async function start(name) {
    const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_CODEX_BINARY: fake, WUU_STEER_NATIVE_LOG: nativeLog, WUU_NODE_EXECUTABLE: process.execPath };
    const child = spawn(binary, ["app-server", "--workdir", scratch, "--config", config], { env, stdio: ["pipe", "pipe", "pipe"] });
    const client = { child, name, stderr: "" }; clients.push(client);
    child.stderr.setEncoding("utf8").on("data", value => client.stderr += value);
    createInterface({ input: child.stdout }).on("line", line => { try { const message = { ...JSON.parse(line), client: name }; transcript.push(message); events.emit("message", message); } catch { client.stderr += "\n" + line; } });
    await rpc(client, "initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "native-steering-e2e" } }); return client;
  }
  async function kill(client) { const exit = once(client.child, "exit"); client.child.kill("SIGKILL"); await exit; }
  async function create(client) { return (await rpc(client, "thread/start", { engine: "codex", model: "fixture", cwd: scratch })).thread.id; }
  async function begin(client, id, prompt = "Start native work") {
    const before = new Set((await native()).filter(row => row.started).map(row => row.started));
    const turn = (await rpc(client, "turn/start", { thread_id: id, prompt })).turn.id;
    // A native start is the admission barrier; avoid assuming RPC/event order.
    const nativeTurn = await nativeWait(row => row.started && !before.has(row.started), "native start");
    return { turn, nativeTurn: nativeTurn.started };
  }
  const steer = (client, id, turn, clientID, prompt, extra = {}) => rpc(client, "turn/steer", { thread_id: id, expected_turn_id: turn, client_id: clientID, prompt, ...extra });
  const resume = (client, id) => rpc(client, "thread/resume", { session_id: id, response_only: true });
  const held = result => result.held_user_messages ?? [];
  const row = id => db.prepare("SELECT * FROM held_user_work WHERE id=?").get(id);
  const messages = id => db.prepare("SELECT * FROM session_messages WHERE client_id=?").all(id);
  const receipt = (client, id, turn, clientID) => waitFor(m => m.client === client.name && m.method === "item/completed" && m.params?.turn_id === turn && m.params?.item?.source_id === clientID, "consumed " + clientID);
  try {
    let a = await start("a1");
    const { readdir } = await import("node:fs/promises");
    async function findDB(dir) { for (const e of await readdir(dir, { withFileTypes: true })) { const p = join(dir, e.name); if (e.isDirectory()) { const found = await findDB(p); if (found) return found; } else if (e.name === "sessions.sqlite3") return p; } }
    const id = await create(a), active = await begin(a, id);
    db = new DatabaseSync(await findDB(home));
    db.exec("PRAGMA busy_timeout=5000");
    const firstSteer = await steer(a, id, active.turn, "native-one", "First supplement");
    await nativeWait(m => m.request?.method === "turn/steer", "current native turn receives steering", process.env.WUU_EXPECT_BASELINE ? 1000 : 12000);
    await receipt(a, id, active.turn, "native-one");
    const request = (await native()).find(m => m.request?.method === "turn/steer").request;
    check("current native turn receives human input with stable client ID and native expectedTurnId", () => { assert.equal(firstSteer.delivery, "active_turn"); assert.equal(request.params.expectedTurnId, active.nativeTurn); assert.equal(request.params.clientUserMessageId, "native-one"); assert.equal(messages("native-one").length, 1); assert.equal(row("native-one").state, "consumed"); });
    const image = { media_type: "image/png", data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYPj/HwADAgH/5ncLrgAAAABJRU5ErkJggg==" };
    await steer(a, id, active.turn, "native-two", "Second supplement", { images: [image], active_document: { path: "guide.md" } });
    await receipt(a, id, active.turn, "native-two");
    const imageRequest = (await native()).find(m => m.request?.params?.clientUserMessageId === "native-two").request;
    check("second steer stays in the same turn and uses the existing image-input conversion", () => { assert.equal(imageRequest.params.expectedTurnId, active.nativeTurn); assert(imageRequest.params.input.some(input => input.type === "image" && input.url.startsWith("data:image/png;base64,"))); assert.equal(messages("native-two").length, 1); });
    const duplicate = await raw(a, "turn/steer", { thread_id: id, expected_turn_id: active.turn, client_id: "native-one", prompt: "duplicate" });
    check("acknowledged stable ID cannot be sent twice", () => assert(duplicate.error));
    await control(active.nativeTurn);
    await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === active.turn, "native completion");
    check("native answer includes both supplements without a successor turn", () => { const answer = db.prepare("SELECT content FROM session_messages WHERE session_id=? AND role='assistant' ORDER BY seq DESC LIMIT 1").get(id); assert(answer.content.includes("First supplement")); assert(answer.content.includes("Second supplement")); });
    if (process.env.WUU_STEER_HISTORY === "1") {
      const historyRows = db.prepare("SELECT * FROM session_messages WHERE session_id=? ORDER BY seq").all(id);
      check("acknowledged supplements persist the same native ID without clearing primary admission", () => {
        const users = historyRows.filter(row => row.role === "user");
        assert.equal(users.length, 3, "native user items must not duplicate acknowledged Wuu messages");
        assert(users.every(row => row.engine_turn_id === active.nativeTurn));
        assert(users.every(row => row.engine_turn_pending === 0));
      });
      const restored = (await resume(a, id)).thread, selected = restored.turns[0];
      const final = selected.items.findLast(item => item.type === "agent_message");
      const supplemental = selected.items.find(item => item.source_id === "native-one");
      assert(final && supplemental);
      const before = historyRows, sourceRef = db.prepare("SELECT engine_ref FROM sessions WHERE id=?").get(id).engine_ref;
      const full = (await rpc(a, "thread/fork", { thread_id: id, turn_id: selected.id, item_id: final.id })).thread;
      const copiedUsers = db.prepare("SELECT * FROM session_messages WHERE session_id=? AND role='user'").all(full.id);
      check("complete native turn fork preserves primary input and every acknowledged supplement", () => {
        assert.equal(copiedUsers.length, 3);
        assert(copiedUsers.every(row => row.engine_turn_id === active.nativeTurn));
        assert.notEqual(db.prepare("SELECT engine_ref FROM sessions WHERE id=?").get(full.id).engine_ref, sourceRef);
      });
      for (const method of ["thread/fork", "thread/edit-message"]) {
        const partial = await raw(a, method, { thread_id: id, turn_id: selected.id, item_id: supplemental.id });
        check(`${method} refuses a supplemental cut inside one native turn`, () => {
          assert(partial.error, "partial native turn must not be guessed");
          assert.deepEqual(db.prepare("SELECT * FROM session_messages WHERE session_id=? ORDER BY seq").all(id), before);
          assert.equal(db.prepare("SELECT engine_ref FROM sessions WHERE id=?").get(id).engine_ref, sourceRef);
        });
      }
    }
    const rejectID = await create(a), rejectTurn = await begin(a, rejectID);
    await steer(a, rejectID, rejectTurn.turn, "native-rejected", "REJECT safely");
    await waitFor(m => m.method === "turn/held" && m.params?.thread_id === rejectID && m.params?.error, "rejected held");
    check("definite rejection preserves editable held input without history or retry", () => { assert.equal(row("native-rejected").state, "pending"); assert.equal(messages("native-rejected").length, 0); });
    await control(rejectTurn.nativeTurn); await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === rejectTurn.turn, "reject turn completes");
    const rejectedView = await resume(a, rejectID);
    check("rejected steering does not automatically run in the next turn", () => { assert.equal(held(rejectedView)[0].id, "native-rejected"); assert.equal(rejectedView.pending_user_messages?.length ?? 0, 0); });
    const delayID = await create(a), delayTurn = await begin(a, delayID);
    const wrong = await raw(a, "turn/steer", { thread_id: delayID, expected_turn_id: active.turn, client_id: "wrong-wuu-turn", prompt: "Do not send" });
    check("stale Wuu turn precondition fails before native submission", () => assert(wrong.error));
    await steer(a, delayID, delayTurn.turn, "native-delayed", "DELAY until acknowledgement");
    await nativeWait(m => m.accepted === "native-delayed", "delayed native admission");
    check("native dispatch seals recovery input without prematurely consuming it", () => { assert.equal(row("native-delayed").state, "dispatching"); assert.equal(messages("native-delayed").length, 0); });
    const live = await resume(a, delayID);
    check("slow native acknowledgement does not block the app-server request loop", () => assert.equal(live.thread.id, delayID));
    const parallelID = await create(a), parallelTurn = await begin(a, parallelID);
    await steer(a, parallelID, parallelTurn.turn, "native-parallel", "Independent supplement");
    await receipt(a, parallelID, parallelTurn.turn, "native-parallel");
    const parallelRequest = (await native()).find(m => m.request?.params?.clientUserMessageId === "native-parallel").request;
    check("another native conversation can steer while an acknowledgement is held", () => { assert.equal(parallelRequest.params.expectedTurnId, parallelTurn.nativeTurn); assert.notEqual(parallelRequest.params.expectedTurnId, delayTurn.nativeTurn); });
    await control(parallelTurn.nativeTurn); await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === parallelTurn.turn, "parallel turn ends");
    for (const [method, params] of [
      ["turn/unsteer", { steer_id: "native-delayed" }],
      ["turn/requeue", { steer_id: "native-delayed" }],
      ["turn/update-queued", { queue_id: "native-delayed", prompt: "edited" }],
      ["turn/queue", { client_id: "native-delayed", prompt: "duplicate" }],
    ]) {
      const response = await raw(a, method, { thread_id: delayID, ...params });
      check(`${method} cannot mutate an in-flight native delivery`, () => assert(response.error));
    }
    await control(delayTurn.nativeTurn, "finish");
    await control(delayTurn.nativeTurn, "ack");
    await receipt(a, delayID, delayTurn.turn, "native-delayed");
    await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === delayTurn.turn, "terminal joins late ack");
    check("terminal history waits for an already in-flight acknowledgement", () => { assert.equal(row("native-delayed").state, "consumed"); assert.equal(messages("native-delayed").length, 1); });
    const internalID = await create(a), internalTurn = await begin(a, internalID);
    await steer(a, internalID, internalTurn.turn, "native-internal", "INTERNAL outcome uncertain");
    await waitFor(m => m.method === "turn/held" && m.params?.thread_id === internalID && m.params?.error, "internal native error held");
    check("internal native errors do not prove rejection or allow replay", () => { assert.equal(row("native-internal").state, "dispatching"); assert.equal(messages("native-internal").length, 0); });
    await control(internalTurn.nativeTurn); await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === internalTurn.turn, "internal error turn ends");
    const badID = await create(a), badTurn = await begin(a, badID);
    await steer(a, badID, badTurn.turn, "native-bad-id", "BAD_ID receipt");
    await waitFor(m => m.method === "turn/held" && m.params?.thread_id === badID && m.params?.error, "mismatched ack held");
    const badView = await resume(a, badID);
    check("mismatched native turn acknowledgement is held as unconfirmed", () => { assert.equal(row("native-bad-id").state, "dispatching"); assert.equal(messages("native-bad-id").length, 0); assert.equal(held(badView)[0].delivery_unconfirmed, true); });
    const badRequeue = await raw(a, "turn/requeue", { thread_id: badID, steer_id: "native-bad-id" });
    check("unconfirmed delivery cannot be reported as consumed by requeue", () => { assert(badRequeue.error); assert.equal(row("native-bad-id").state, "dispatching"); });
    await control(badTurn.nativeTurn); await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === badTurn.turn, "bad ack turn ends");
    const lostID = await create(a), lostTurn = await begin(a, lostID);
    await steer(a, lostID, lostTurn.turn, "native-lost", "LOST response");
    await nativeWait(m => m.accepted === "native-lost", "lost native admission");
    await rpc(a, "turn/interrupt", { thread_id: lostID });
    await waitFor(m => ["turn/completed", "turn/error"].includes(m.method) && m.params?.turn?.id === lostTurn.turn, "cancelled native turn");
    check("cancellation after send retains uncertain input without claiming consumption", () => { assert.equal(row("native-lost").state, "dispatching"); assert.equal(messages("native-lost").length, 0); });
    const faultID = await create(a), faultTurn = await begin(a, faultID);
    db.exec("CREATE TRIGGER fail_native_ack BEFORE INSERT ON session_messages WHEN NEW.client_id='native-save-fault' BEGIN SELECT RAISE(ABORT,'synthetic ack persistence failure'); END");
    await steer(a, faultID, faultTurn.turn, "native-save-fault", "Accepted but disk fails");
    await waitFor(m => m.method === "turn/held" && m.params?.thread_id === faultID && m.params?.error, "failed ack persistence held");
    db.exec("DROP TRIGGER fail_native_ack");
    check("acknowledged native input stays non-replayable when history persistence fails", () => { assert.equal(row("native-save-fault").state, "dispatching"); assert.equal(messages("native-save-fault").length, 0); });
    await control(faultTurn.nativeTurn); await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === faultTurn.turn, "fault turn ends");
    const claimID = await create(a), claimTurn = await begin(a, claimID);
    db.exec("CREATE TRIGGER fail_native_claim BEFORE UPDATE ON held_user_work WHEN NEW.id='native-claim-fault' AND NEW.state='dispatching' BEGIN SELECT RAISE(ABORT,'synthetic claim failure'); END");
    await steer(a, claimID, claimTurn.turn, "native-claim-fault", "Must never be sent");
    await waitFor(m => m.method === "turn/held" && m.params?.thread_id === claimID && m.params?.error, "failed claim held");
    db.exec("DROP TRIGGER fail_native_claim");
    const claimedRequests = await native();
    check("failed durable claim sends no native input and preserves held payload", () => { assert.equal(row("native-claim-fault").state, "pending"); assert(!claimedRequests.some(m => m.request?.params?.clientUserMessageId === "native-claim-fault")); });
    await control(claimTurn.nativeTurn); await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === claimTurn.turn, "claim turn ends");
    const crossID = await create(a), crossTurn = await begin(a, crossID);
    await steer(a, crossID, crossTurn.turn, "native-cross-gate", "DELAY crossing process edit");
    await nativeWait(m => m.accepted === "native-cross-gate", "cross-process gate admitted");
    await steer(a, crossID, crossTurn.turn, "native-cross-edited", "Original supplemental file", { files: [{ filename: "original.txt", media_type: "text/plain", data: Buffer.from("original bytes").toString("base64") }], active_document: { path: "original.md" } });
    const b = await start("b1");
    await resume(b, crossID);
    await rpc(b, "turn/update-queued", { thread_id: crossID, queue_id: "native-cross-edited", prompt: "Edited in another process", files: [{ filename: "changed.txt", media_type: "text/plain", data: Buffer.from("edited bytes").toString("base64") }] });
    const editedRowBefore = row("native-cross-edited");
    await control(crossTurn.nativeTurn, "ack");
    await receipt(a, crossID, crossTurn.turn, "native-cross-gate");
    await waitFor(m => m.client === a.name && m.method === "turn/held" && m.params?.thread_id === crossID && m.params?.error?.includes("changed before native dispatch"), "cross-process stale send fenced");
    const crossNative = await native();
    const editedRow = row("native-cross-edited");
    check("cross-process edit fences stale native steering and preserves edited attachments", () => { assert.equal(editedRow.state, "pending"); assert.equal(editedRow.message_json, editedRowBefore.message_json); assert(!crossNative.some(m => m.request?.params?.clientUserMessageId === "native-cross-edited")); assert.equal(messages("native-cross-edited").length, 0); });
    const editedMessage = JSON.parse(editedRow.message_json);
    const editedFile = editedMessage.Files[0];
    assert.equal(await readFile(editedFile.local_path, "utf8"), "edited bytes");
    await steer(a, crossID, crossTurn.turn, "native-cross-edited", "Use the canonical edited payload");
    await receipt(a, crossID, crossTurn.turn, "native-cross-edited");
    const retriedNative = (await native()).filter(m => m.request?.params?.clientUserMessageId === "native-cross-edited");
    check("explicit held retry sends canonical edited file context exactly once", () => { assert.equal(retriedNative.length, 1); const input = retriedNative[0].request.params.input; assert(input.some(v => v.text?.includes("Edited in another process") && v.text.includes(editedFile.local_path))); assert.equal(row("native-cross-edited").state, "consumed"); });
    await control(crossTurn.nativeTurn);
    await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === crossTurn.turn, "cross-process test ends");
    const releaseID = await create(a), releaseTurn = await begin(a, releaseID);
    db.exec("CREATE TRIGGER fail_native_release BEFORE UPDATE ON held_user_work WHEN NEW.id='native-release-fault' AND OLD.state='dispatching' AND NEW.state='pending' BEGIN SELECT RAISE(ABORT,'synthetic release failure'); END");
    await steer(a, releaseID, releaseTurn.turn, "native-release-fault", "UNSUPPORTED release fails");
    await nativeWait(m => m.request?.params?.clientUserMessageId === "native-release-fault", "unsupported release request");
    await control(releaseTurn.nativeTurn);
    await waitFor(m => m.method === "turn/completed" && m.params?.turn?.id === releaseTurn.turn, "release fault turn ends");
    db.exec("DROP TRIGGER fail_native_release");
    check("failed native fallback release publishes recoverable held state", () => { assert.equal(row("native-release-fault").state, "dispatching"); assert(transcript.some(m => m.method === "turn/held" && m.params?.thread_id === releaseID && m.params?.messages?.some(v => v.id === "native-release-fault" && v.delivery_unconfirmed)), "no turn/held notification after failed release"); });
    const fallbackID = await create(a), fallbackTurn = await begin(a, fallbackID);
    await steer(a, fallbackID, fallbackTurn.turn, "native-unsupported", "UNSUPPORTED queued fallback");
    await nativeWait(m => m.request?.params?.clientUserMessageId === "native-unsupported", "unsupported request");
    await control(fallbackTurn.nativeTurn);
    const fallbackThread = (await native()).find(m => m.started === fallbackTurn.nativeTurn).threadID;
    const queuedNative = await nativeWait(m => m.started && m.started !== fallbackTurn.nativeTurn && m.threadID === fallbackThread, "fallback next native turn");
    check("unsupported native steering uses one ordinary queued successor", () => assert.equal(row("native-unsupported").state, "consumed"));
    await control(queuedNative.started);
    await waitFor(m => m.method === "turn/completed" && m.params?.thread_id === fallbackID && m.params?.turn?.id !== fallbackTurn.turn, "fallback turn completes");
    const crashID = await create(a), crashTurn = await begin(a, crashID);
    await steer(a, crashID, crashTurn.turn, "native-crash", "LOST crash before acknowledgement");
    await nativeWait(m => m.accepted === "native-crash", "crash native admission");
    await kill(a); a = await start("a2");
    for (const [threadID, clientID] of [[crashID, "native-crash"], [internalID, "native-internal"], [badID, "native-bad-id"], [lostID, "native-lost"], [faultID, "native-save-fault"]]) {
      const restored = await resume(a, threadID);
      check(`restart restores ${clientID} as visible unconfirmed input`, () => { assert(held(restored).some(m => m.id === clientID && m.delivery_unconfirmed)); assert.equal(restored.pending_user_messages?.length ?? 0, 0); });
      const retry = await raw(a, "turn/steer", { thread_id: threadID, client_id: clientID, prompt: "Do not replay" });
      check(`restart fences explicit retry of ${clientID}`, () => assert(retry.error));
    }
    const mutationResponses = [];
    for (const [method, params] of [
      ["turn/requeue", { steer_id: "native-crash" }],
      ["turn/unsteer", { steer_id: "native-crash" }],
      ["turn/dequeue", { queue_id: "native-crash" }],
      ["turn/update-queued", { queue_id: "native-crash", prompt: "edited" }],
      ["turn/queue", { client_id: "native-crash", prompt: "duplicate" }],
      ["turn/start", { client_id: "native-crash", prompt: "duplicate" }],
    ]) mutationResponses.push([method, await raw(a, method, { thread_id: crashID, ...params })]);
    check("every mutation and dispatch route fences unconfirmed input after restart", () => { for (const [method, response] of mutationResponses) assert(response.error, method + " must reject"); assert.equal(row("native-crash").state, "dispatching"); });
    const finalNative = await native();
    check("unconfirmed inputs were each submitted only once", () => { for (const id of ["native-crash", "native-internal", "native-bad-id", "native-lost", "native-save-fault"]) assert.equal(finalNative.filter(m => m.request?.params?.clientUserMessageId === id).length, 1); });
    check("server stderr contains no race detector report", () => assert(!clients.some(client => client.stderr.includes("DATA RACE"))));
    results.status = "passed";
  } catch (error) {
    results.error = String(error); console.error(error);
  } finally {
    for (const client of clients) if (client.child.exitCode === null && client.child.signalCode === null) client.child.kill("SIGKILL");
    db?.close();
    await writeFile(join(output, "results.json"), JSON.stringify(results, null, 2));
    await writeFile(join(output, "transcript.json"), JSON.stringify(transcript, null, 2));
    await writeFile(join(output, "native.json"), JSON.stringify(await native(), null, 2));
    await writeFile(join(output, "stderr.log"), clients.map(c => c.name + "\n" + c.stderr).join("\n"));
    process.exitCode = results.status === "passed" ? 0 : 1;
  }
}
