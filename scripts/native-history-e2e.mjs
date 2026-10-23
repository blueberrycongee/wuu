#!/usr/bin/env node
// Real Wuu stdio app-server + stateful fake Codex subprocess, no vendor keys or
// inference. Build bin/wuu and bin/fakecodex before running this Node 22.13+ script.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { EventEmitter, once } from "node:events";
import { access, mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const fake = resolve(root, process.env.WUU_E2E_CODEX || "bin/fakecodex");
const output = resolve(root, process.env.WUU_HISTORY_E2E_OUTPUT || ".tmp/e2e/native-history");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "home"), nativePath = join(scratch, "native.json"), controlPath = join(scratch, "control.json");
await mkdir(home);
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath,
  WUU_CODEX_BINARY: fake, WUU_TEST_CODEX_HISTORY: nativePath,
  WUU_TEST_CODEX_HISTORY_CONTROL: controlPath, WUU_TEST_CODEX_REQUESTS: join(scratch, "native-requests.jsonl"),
  WUU_TEST_CODEX_LIFECYCLE: join(scratch, "native-lifecycle.jsonl") };
await writeFile(join(home, ".wuu-session-fixture"), "wuu-session-fixture-v1\n");
await writeFile(join(home, ".wuu-synthetic-fixture"), "wuu-session-fixture-v1\n");
const fixtureBinary = resolve(root, process.env.WUU_E2E_SESSION_FIXTURE || join(scratch, "session-fixture"));
if (!process.env.WUU_E2E_SESSION_FIXTURE) {
  const build = spawnSync("go", ["build", "-o", fixtureBinary, "./scripts/session-fixture"], { cwd: root, encoding: "utf8" });
  assert.equal(build.status, 0, build.stderr);
}
function fixture(input) {
  const reply = spawnSync(fixtureBinary, [], { input: JSON.stringify({ home, workdir: scratch, ...input }),
    encoding: "utf8", timeout: 20000, maxBuffer: 64 * 1024 * 1024 });
  assert.equal(reply.status, 0, reply.stderr);
  return JSON.parse(reply.stdout);
}
const stateProvider = resolve(root, process.env.WUU_E2E_SESSION_PROVIDER || join(scratch, "session-fixture-provider"));
if (!process.env.WUU_E2E_SESSION_PROVIDER) {
  const build = spawnSync("go", ["build", "-o", stateProvider, "./scripts/session-fixture-provider"], { cwd: root, encoding: "utf8" });
  assert.equal(build.status, 0, build.stderr);
}
const faultPath = join(home, "session-fixture-provider", "faults.json");
async function faults(rules) {
  if (!rules.length) { await rm(faultPath); return; }
  await writeFile(faultPath + ".tmp", JSON.stringify({ version: 1, faults: rules }));
  await rename(faultPath + ".tmp", faultPath);
}
const events = new EventEmitter(), transcript = [], checks = [];
let child, stderr = "", sequence = 0;
const result = { scratch, status: "failed", checks };
function waitFor(predicate, label, timeout = 15_000) {
  const value = transcript.find(predicate);
  if (value) return Promise.resolve(value);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off("message", listener); reject(new Error(`Timed out: ${label}\n${stderr}`)); }, timeout);
    function listener(message) { if (!predicate(message)) return; clearTimeout(timer); events.off("message", listener); resolve(message); }
    events.on("message", listener);
  });
}
async function raw(method, params = {}) {
  const id = `history-${++sequence}`;
  child.stdin.write(JSON.stringify({ id, method, params }) + "\n");
  return waitFor(message => message.id === id, method);
}
async function rpc(method, params = {}) {
  const response = await raw(method, params);
  assert(!response.error, `${method}: ${JSON.stringify(response.error)}`);
  return response.result;
}
function check(name) { checks.push(name); console.log(`PASS ${name}`); }
const native = async () => JSON.parse(await readFile(nativePath, "utf8"));
const mode = async value => writeFile(controlPath, JSON.stringify({ mode: value }));
const stored = id => fixture({ op: "read", ids: [id] }).sessions[0].session;
const rows = id => fixture({ op: "read", ids: [id] }).sessions[0].history;
const count = () => fixture({ op: "read" }).sessions.length;
const target = turn => {
  const item = turn.items.findLast(item => item.type === "agent_message");
  assert(item, "fixture turn needs a final answer");
  return { turn_id: turn.id, item_id: item.id, target: { seq: item.seq, source_id: item.source_id, type: item.type } };
};
async function resume(id) { return (await rpc("thread/resume", { session_id: id })).thread; }
async function run(id, prompt) {
  const after = transcript.length;
  const started = await rpc("turn/start", { thread_id: id, prompt });
  const complete = await waitFor(m => transcript.indexOf(m) >= after && ["turn/completed", "turn/error"].includes(m.method) && m.params.thread_id === id && m.params.turn.id === started.turn.id, `turn ${prompt}`);
  return complete.params.turn;
}
async function start() {
  child = spawn(binary, ["app-server", "--workdir", scratch, "--config", join(home, "config.json")], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const value = JSON.parse(line); transcript.push(value); events.emit("message", value); } catch { stderr += `\n${line}`; }
  });
  await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "native-history-e2e" } });
}
async function stop() {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit"), timer = setTimeout(() => child.kill("SIGKILL"), 5000);
  child.stdin.end();
  try { const [code, signal] = await exited; assert.equal(signal, null); assert.equal(code, 0, stderr); } finally { clearTimeout(timer); }
}
const provider = createServer(async (request, response) => {
  for await (const chunk of request) { /* drain synthetic title requests */ }
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  response.end(`data: ${JSON.stringify({ choices: [{ delta: { role: "assistant", content: "Fixture conversation" }, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ choices: [{ delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 1, completion_tokens: 1 } })}\n\ndata: [DONE]\n\n`);
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));
try {
  await writeFile(join(home, "config.json"), JSON.stringify({ default_provider: "fixture",
    providers: { fixture: { type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}`, api_key: "synthetic", model: "fixture" } },
    engines: { codex: { enabled: true, binary_path: fake }, claude: { enabled: false } },
    skills: { enabled: false }, ptc: { enabled: false }, agent: { permission_mode: "standard" } }));
  const installed = spawnSync(stateProvider, ["install", "--home", home], { env, encoding: "utf8" });
  assert.equal(installed.status, 0, installed.stderr);
  await start();
  const source = (await rpc("thread/start", { engine: "codex", model: "gpt-6-astra" })).thread;
  const one = await run(source.id, "one"), two = await run(source.id, "two"), three = await run(source.id, "three");
  const sourceRef = stored(source.id).engine_ref;
  assert(rows(source.id).filter(row => row.role === "user").every(row => row.engine_turn_id));
  const originalNative = structuredClone((await native()).threads[sourceRef]);
  assert.equal(originalNative.turns.length, 3);
  check("new prompts persist native turn identity and reuse the warm engine");

  const fork = (await rpc("thread/fork", { thread_id: source.id, ...target(one) })).thread;
  const forkRef = stored(fork.id).engine_ref;
  assert(forkRef && forkRef !== sourceRef);
  assert.equal((await native()).threads[forkRef].subscribed, false, "new child must release its temporary native subscription");
  assert.deepEqual((await native()).threads[forkRef].turns, originalNative.turns.slice(0, 1));
  assert.deepEqual((await native()).threads[sourceRef], originalNative);
  assert.equal((await resume(fork.id)).turns.length, 1);
  check("fork copies the exact native prefix, including hidden/tool items, without changing parent");
  const forkTurn = await run(fork.id, "child");
  assert(forkTurn.items.some(item => item.text === "context:one;prompt:child"));
  assert.deepEqual((await native()).threads[sourceRef], originalNative);
  check("child continuation receives retained native context and cannot append to parent");

  const liveUser = one.items.find(item => item.type === "user_message");
  const beforeReject = count();
  const userCut = await raw("thread/fork", { thread_id: source.id, turn_id: one.id, item_id: liveUser.id });
  assert(userCut.error, "mid-turn user cutoff must be refused");
  assert.equal(count(), beforeReject);
  check("a partial native turn is refused without publishing a misleading child");

  for (const failure of ["read-error", "fork-error", "ignore-cutoff", "alias-parent", "alias-parent-whitespace", "corrupt-item", "corrupt-number", "incomplete"]) {
    await mode(failure);
    const before = rows(source.id), ref = stored(source.id).engine_ref, sessions = count();
    const nativeBefore = new Set(Object.keys((await native()).threads));
    const response = await raw("thread/fork", { thread_id: source.id, ...target(one) });
    assert(response.error, `${failure} must fail`);
    const user = two.items.find(item => item.type === "user_message");
    assert((await raw("thread/edit-message", { thread_id: source.id, turn_id: two.id, item_id: user.id })).error, `${failure} edit must fail`);
    assert.equal(count(), sessions);
    assert.deepEqual(rows(source.id), before);
    assert.equal(stored(source.id).engine_ref, ref);
    const afterNative = await native();
    assert.deepEqual(afterNative.threads[sourceRef], originalNative);
    for (const [id, thread] of Object.entries(afterNative.threads)) {
      if (!nativeBefore.has(id)) assert.equal(thread.subscribed, false, `${failure} must release rejected child subscription`);
    }
    check(`${failure} leaves original history and binding intact`);
  }
  await mode("");

  // Hold each native history operation at an explicit gate. Reads and Stop
  // must not inherit its subprocess wait, even for the source conversation.
  let heldSource = source;
  for (const operation of ["fork", "edit-message"]) {
    const gate = join(scratch, `release-${operation}`), marker = join(scratch, `${operation}-entered`);
    const selected = operation === "fork" ? target(one) : { turn_id: heldSource.turns[0].id,
      item_id: heldSource.turns[0].items.find(item => item.type === "user_message").id };
    await writeFile(controlPath, JSON.stringify({ mode: "hold-fork", gate, marker }));
    const held = rpc(`thread/${operation}`, { thread_id: heldSource.id, ...selected });
    const deadline = Date.now() + 5000;
    while (!(await access(marker).then(() => true, () => false))) {
      assert(Date.now() < deadline, `native ${operation} did not reach the controlled gate`);
      await new Promise(resolve => setTimeout(resolve, 5));
    }
    if (operation === "edit-message") {
      // The current cache holds eight idle conversations. Exceed it while the
      // edit has released its mutex: its live state must remain pinned.
      for (let i = 0; i < 9; i++) await rpc("thread/start", { engine: "wuu" });
    }
    const independent = Promise.all([resume(heldSource.id), rpc("turn/interrupt", { thread_id: heldSource.id }),
      resume(fork.id), rpc("turn/interrupt", { thread_id: fork.id })]);
    let responsivenessTimer;
    try {
      await Promise.race([independent, new Promise((_, reject) => { responsivenessTimer = setTimeout(() => reject(new Error(`native ${operation} blocked reads/Stop`)), 1500); })]);
    } finally { clearTimeout(responsivenessTimer); await writeFile(gate, "release"); }
    heldSource = (await held).thread;
    assert.deepEqual((await resume(heldSource.id)).turns, heldSource.turns, "native edit must survive cache pressure without stale in-memory history");
    await mode("");
    check(`a pending native ${operation} does not block source or other conversations' read/Stop`);
  }

  const editUser = two.items.find(item => item.type === "user_message");
  const editParams = { thread_id: source.id, turn_id: two.id, item_id: editUser.id };
  await faults([{ kind: "edit-binding", session_id: source.id }]);
  const beforeEdit = rows(source.id);
  const rejected = await raw("thread/edit-message", editParams);
  assert(rejected.error);
  assert.equal(stored(source.id).engine_ref, sourceRef);
  assert.deepEqual(rows(source.id), beforeEdit);
  const retractions = fixture({ op: "inspect", domains: ["session_history_retractions"] }).documents.session_history_retractions;
  assert.equal(retractions.filter(document => document.key === source.id).flatMap(document => document.value).length, 0);
  await faults([]);
  check("failed edit transaction cannot replace native binding or retract visible history");

  const countBeforeCreate = count();
  await faults([{ kind: "fork-create" }]);
  assert((await raw("thread/fork", { thread_id: source.id, ...target(one) })).error);
  assert.equal(count(), countBeforeCreate);
  await faults([]);
  check("failed fork transaction leaves no partially initialized Wuu child");

  const edited = await rpc("thread/edit-message", editParams);
  const editedRef = stored(source.id).engine_ref;
  assert.notEqual(editedRef, sourceRef);
  assert.equal(edited.draft.prompt, "two");
  assert.equal(edited.thread.turns.length, 1);
  assert.deepEqual((await native()).threads[editedRef].turns, originalNative.turns.slice(0, 1));
  assert.deepEqual((await native()).threads[sourceRef], originalNative);
  check("edit atomically switches to the prefix before the selected user turn");
  const warmLifecycle = (await readFile(env.WUU_TEST_CODEX_LIFECYCLE, "utf8")).trim().split("\n").map(JSON.parse);
  assert.equal(warmLifecycle.filter(event => event.method === "initialize").length, 1, "ordinary turns and history mutations must retain one warm native host");
  await stop();
  await start();
  assert.equal((await resume(source.id)).turns.length, 1);
  const replacement = await run(source.id, "replacement");
  assert(replacement.items.some(item => item.text === "context:one;prompt:replacement"));
  assert.deepEqual((await native()).threads[sourceRef], originalNative);
  assert.equal(stored(source.id).engine_ref, editedRef);
  check("restart restores edited binding and excludes removed native turns on continuation");

  const restored = await resume(source.id), firstUser = restored.turns[0].items.find(item => item.type === "user_message");
  const firstEdit = await rpc("thread/edit-message", { thread_id: source.id, turn_id: restored.turns[0].id, item_id: firstUser.id });
  assert.equal(firstEdit.thread.turns.length, 0);
  assert.equal((await native()).threads[stored(source.id).engine_ref].turns.length, 0);
  const fresh = await run(source.id, "reset");
  assert(fresh.items.some(item => item.text === "context:;prompt:reset"));
  check("editing the first turn uses beforeTurnId and resumes with genuinely empty native context");

  await mode("failed-turn");
  const failed = await run(source.id, "failed");
  await mode("");
  const failedUser = failed.items.find(item => item.type === "user_message");
  await rpc("thread/edit-message", { thread_id: source.id, turn_id: failed.id, item_id: failedUser.id });
  assert.equal((await native()).threads[stored(source.id).engine_ref].turns.length, 1);
  check("a failed native turn with no answer remains editable by its durable turn identity");

  // Legacy records remain usable only if stable native output IDs prove the
  // same prefix; neither repeated text nor recycled Wuu turn positions suffice.
  const legacySeed = fixture({ op: "read", ids: [fork.id] }).sessions[0];
  legacySeed.session.id = `legacy-${fork.id}`;
  for (const record of legacySeed.history) delete record.engine_turn_id;
  fixture({ op: "seed", sessions: [legacySeed] });
  const legacy = await resume(legacySeed.session.id);
  const legacyFork = (await rpc("thread/fork", { thread_id: legacy.id, ...target(legacy.turns[0]) })).thread;
  assert.equal((await native()).threads[stored(legacyFork.id).engine_ref].turns.length, 1);
  check("legacy history can fork when native item identity proves its complete turn boundary");
  const missingIdentitySeed = fixture({ op: "read", ids: [legacyFork.id] }).sessions[0];
  missingIdentitySeed.session.id = `unidentified-${legacyFork.id}`;
  for (const record of missingIdentitySeed.history) { delete record.provider_item_id; delete record.engine_turn_id; }
  fixture({ op: "seed", sessions: [missingIdentitySeed] });
  assert((await raw("thread/fork", { thread_id: missingIdentitySeed.session.id })).error);
  check("legacy history without native identity is refused instead of matching repeated text");
  assert.equal((await run(missingIdentitySeed.session.id, "legacy-continues")).status, "completed");
  check("ordinary legacy history without native identity still permits continuation");

  const clean = (await rpc("thread/start", { engine: "codex" })).thread;
  const cleanFork = (await rpc("thread/fork", { thread_id: clean.id })).thread;
  assert.equal(stored(cleanFork.id).engine_ref ?? "", "");
  check("never-run empty native threads still fork safely");

  await mode("reject-turn");
  assert.equal((await run(clean.id, "native-rejects-before-admission")).status, "failed");
  assert(rows(clean.id).every(row => !row.engine_turn_pending));
  await mode("");
  const afterRejection = await run(clean.id, "after-explicit-rejection");
  assert.equal(afterRejection.status, "completed");
  assert(afterRejection.items.some(item => item.text === "context:;prompt:after-explicit-rejection"));
  check("an explicit native rejection clears admission uncertainty and ordinary continuation remains usable");

  const internalErrorThread = (await rpc("thread/start", { engine: "codex" })).thread;
  await mode("internal-error-after-admission");
  assert.equal((await run(internalErrorThread.id, "admitted-with-error-response")).status, "failed");
  assert(rows(internalErrorThread.id).some(row => row.engine_turn_pending), "internal RPC failure cannot prove rejection");
  const admittedTurns = (await native()).threads[stored(internalErrorThread.id).engine_ref].turns.length;
  await mode("");
  assert.equal((await run(internalErrorThread.id, "must-not-repeat-uncertain-input")).status, "failed");
  assert.equal((await native()).threads[stored(internalErrorThread.id).engine_ref].turns.length, admittedTurns);
  check("internal RPC errors retain admission uncertainty and prevent another native input");

  await mode("long-turn");
  const longThread = (await rpc("thread/start", { engine: "codex" })).thread;
  const longTurn = await run(longThread.id, "many-large-tool-results");
  await mode("");
  assert.equal(longTurn.status, "completed");
  assert.equal(longTurn.items.filter(item => item.type === "tool_call").length, 302, "burst native items must not disappear from the visible turn");
  const longFork = (await rpc("thread/fork", { thread_id: longThread.id, ...target(longTurn) })).thread;
  const longNative = await native();
  const longSource = longNative.threads[stored(longThread.id).engine_ref];
  assert(longSource.turns[0].items.filter(item => item.type === "commandExecution").length >= 300);
  assert.deepEqual(longNative.threads[stored(longFork.id).engine_ref].turns, longSource.turns);
  check("large native tool turns complete and fork with every native item intact");

  const nativeStarts = async () => (await readFile(env.WUU_TEST_CODEX_REQUESTS, "utf8")).trim().split("\n").map(JSON.parse).filter(request => request.method === "turn/start").length;
  const emptyStart = (await rpc("thread/start", { engine: "codex" })).thread;
  await mode("terminal-empty-start-response");
  const emptyStarted = await run(emptyStart.id, "completed-before-empty-response");
  assert.equal(emptyStarted.status, "failed", "the invalid response remains an explicit start error");
  assert(rows(emptyStart.id).filter(row => row.role === "user").every(row => row.engine_turn_id && !row.engine_turn_pending));
  assert(rows(emptyStart.id).some(row => row.role === "assistant" && row.content.includes("completed-before-empty-response")), "the completed native output must survive the response error");
  await mode("");
  assert.equal((await run(emptyStart.id, "continue-after-empty-response")).status, "completed");
  check("early native completion plus an empty start response retains output and permits safe continuation");

  const disconnected = (await rpc("thread/start", { engine: "codex" })).thread;
  await mode("disconnect-without-terminal");
  assert.equal((await run(disconnected.id, "disconnect-with-partial")).status, "failed");
  assert(rows(disconnected.id).some(row => row.engine_turn_id && row.engine_turn_pending), "transport failure cannot synthesize native completion");
  assert(rows(disconnected.id).some(row => row.role === "assistant" && row.content.includes("Retained partial output")));
  const beforeDisconnectedRetry = await nativeStarts();
  await mode("");
  assert.equal((await run(disconnected.id, "must-not-repeat-disconnected-input")).status, "failed");
  assert.equal(await nativeStarts(), beforeDisconnectedRetry);
  await mode("settle-on-read");
  assert.equal((await run(disconnected.id, "continue-after-verified-stop")).status, "completed");
  assert(rows(disconnected.id).every(row => !row.engine_turn_pending));
  check("native EOF preserves partial output and pending admission until a native read proves settlement");
  await mode("");
  for (const uncertain of [false, true]) {
    const interrupted = (await rpc("thread/start", { engine: "codex" })).thread;
    await mode(uncertain ? "no-interrupt-reply" : "hold-turn");
    const after = transcript.length;
    const started = await rpc("turn/start", { thread_id: interrupted.id, prompt: "held-native-turn" });
    const deadline = Date.now() + 5000;
    while (!rows(interrupted.id).some(row => row.engine_turn_id)) {
      assert(Date.now() < deadline, "native admission identity was not persisted");
      await new Promise(resolve => setTimeout(resolve, 5));
    }
    await rpc("turn/interrupt", { thread_id: interrupted.id });
    await waitFor(m => transcript.indexOf(m) >= after && ["turn/completed", "turn/error"].includes(m.method) && m.params.thread_id === interrupted.id && m.params.turn.id === started.turn.id, "interrupted native turn");
    if (uncertain) {
      assert(rows(interrupted.id).some(row => row.engine_turn_pending));
      const before = await nativeStarts();
      assert.equal((await run(interrupted.id, "must-not-steer-running-native-turn")).status, "failed");
      assert.equal(await nativeStarts(), before);
      await mode("settle-on-read");
    } else {
      assert(rows(interrupted.id).every(row => !row.engine_turn_pending));
      await mode("");
    }
    const continued = await run(interrupted.id, "after-stop");
    assert.equal(continued.status, "completed");
    assert(continued.items.some(item => item.text === "context:held-native-turn;prompt:after-stop"));
    assert(rows(interrupted.id).every(row => !row.engine_turn_pending));
    check(uncertain ? "an unconfirmed known native turn blocks input until a read proves it has finished" : "normal Stop settles the native turn and permits continuation");
    await mode("");
  }

  const boundary = (await rpc("thread/start", { engine: "codex" })).thread;
  await faults([{ kind: "native-boundary", session_id: boundary.id }]);
  await mode("no-interrupt-reply");
  const boundaryStart = Date.now();
  const rejectedBoundary = await run(boundary.id, "cannot-persist-boundary");
  assert.equal(rejectedBoundary.status, "failed");
  assert(Date.now() - boundaryStart < 12_000, "native interrupt timeout must not wedge Wuu finalization");
  assert.equal((await resume(boundary.id)).id, boundary.id);
  await faults([]);
  await mode("");
  check("failed native boundary persistence and an unresponsive interrupt still finalize within a bounded time");
  const startsBeforeRetry = await nativeStarts();
  assert.equal((await run(boundary.id, "must-not-steer-uncertain-turn")).status, "failed");
  assert.equal(await nativeStarts(), startsBeforeRetry);
  await stop();
  await start();
  assert.equal((await run(boundary.id, "must-not-steer-after-restart")).status, "failed");
  assert.equal(await nativeStarts(), startsBeforeRetry);
  check("unconfirmed native boundaries prevent accidental replay or steering across restart");


  await stop();
  const unsupportedSeed = fixture({ op: "read", ids: [fork.id] }).sessions[0];
  unsupportedSeed.session = { ...unsupportedSeed.session, id: `unsupported-${fork.id}`, engine_id: "claude", provider: "claude" };
  fixture({ op: "seed", sessions: [unsupportedSeed] });
  await start();
  const unsupported = await resume(unsupportedSeed.session.id), unsupportedRows = rows(unsupported.id), unsupportedRef = stored(unsupported.id).engine_ref;
  const unsupportedUser = unsupported.turns[0].items.find(item => item.type === "user_message");
  const unsupportedFork = await raw("thread/fork", { thread_id: unsupported.id });
  const unsupportedEdit = await raw("thread/edit-message", { thread_id: unsupported.id, turn_id: unsupported.turns[0].id, item_id: unsupportedUser.id });
  assert.match(unsupportedFork.error?.message ?? "", /native history cutoff/);
  assert.match(unsupportedEdit.error?.message ?? "", /native history cutoff/);
  assert.deepEqual(rows(unsupported.id), unsupportedRows);
  assert.equal(stored(unsupported.id).engine_ref, unsupportedRef);
  check("unsupported external histories remain readable and unchanged after explicit fork/edit refusal");

  const lifecycle = (await readFile(env.WUU_TEST_CODEX_LIFECYCLE, "utf8")).trim().split("\n").map(JSON.parse);
  // Three explicit app-server generations plus at most four native retirements:
  // uncertain internal admission, injected EOF, unconfirmed Stop, and failed
  // boundary persistence. None may introduce per-turn process recreation.
  const initializations = lifecycle.filter(event => event.method === "initialize").length;
  assert(initializations >= 3 && initializations <= 7, `unexpected native process churn: ${initializations}`);
  const wire = (await readFile(env.WUU_TEST_CODEX_REQUESTS, "utf8")).trim().split("\n").map(JSON.parse);
  assert(!wire.some(request => request.method === "thread/rollback" || request.method === "thread/revert"));
  assert(!wire.some(request => request.method === "thread/unsubscribe" && request.params.threadId === sourceRef), "never unsubscribe the source while cleaning a child");
  check("history operations preserve warm host ownership and never mutate the original native history");
  result.status = "passed";
} catch (error) { result.error = String(error.stack || error); throw error;
} finally {
  try { await stop(); } finally { provider.close();
    await writeFile(join(scratch, "transcript.jsonl"), transcript.map(value => JSON.stringify(value)).join("\n") + "\n");
    await writeFile(join(scratch, "stderr.log"), stderr);
    await writeFile(join(output, "results.json"), JSON.stringify(result, null, 2));
    console.log(`Evidence: ${join(output, "results.json")}`);
  }
}
