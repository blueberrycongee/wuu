#!/usr/bin/env node
// Real source-built app-server, public single-file CLI publication, isolated
// state and loopback-only inference. Failure contracts: invalid optional plugin
// quarantine, accepted work retention, later send admission, repair and failed
// replacement fencing. This is not the user's original plugin or desktop QA.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(process.env.WUU_E2E_CORE || join(root, "bin/wuu"));
const output = resolve(process.env.WUU_ISOLATION_EVIDENCE || join(root, ".tmp/e2e/plugin-failure-isolation"));
const expectBlocked = process.argv.includes("--expect-blocked");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-")), home = join(scratch, "state");
await mkdir(home);
const transcript = [], calls = [], publications = [], checks = [], events = new EventEmitter();
const result = { status: "failed", scratch, checks, expectBlocked, binary,
  binary_sha256: createHash("sha256").update(await readFile(binary)).digest("hex"),
  source_revision: spawnSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).stdout.trim(),
  source_dirty: Boolean(spawnSync("git", ["status", "--porcelain"], { cwd: root, encoding: "utf8" }).stdout.trim()),
  limitations: ["Synthetic observer and model; no original token-speed source, desktop rendering, user profile, or installed app was inspected."] };
let child, stderr = "", sequence = 0, held;
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
const config = join(home, "config.json"), source = join(scratch, "observer.ts"), probe = join(scratch, "observer.jsonl");
await writeFile(probe, "");
function waitFor(event, history, predicate, label, timeout = 20000) {
  const previous = history.find(predicate); if (previous) return Promise.resolve(previous);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off(event, listener); reject(new Error("Timed out: " + label)); }, timeout);
    const listener = value => { if (predicate(value)) { clearTimeout(timer); events.off(event, listener); resolve(value); } };
    events.on(event, listener);
  });
}
async function raw(method, params = {}) {
  const id = String(++sequence); child.stdin.write(JSON.stringify({ id, method, params }) + "\n");
  return waitFor("message", transcript, message => message.id === id, method);
}
async function rpc(method, params = {}) {
  const response = await raw(method, params); assert(!response.error, `${method}: ${JSON.stringify(response.error)}`); return response.result;
}
async function settledSend(threadID, prompt) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const response = await raw("turn/start", { thread_id: threadID, prompt });
    if (!response.error?.message?.includes("thread execution is owned by another app-server") &&
        !response.error?.message?.includes("turn admission can be retried")) return response;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error("Turn admission transition did not settle");
}
async function completed(turn) {
  const message = await waitFor("message", transcript, message => message.method === "turn/completed" && message.params.turn.id === turn.id, "turn completed");
  assert.equal(message.params.turn.status, "completed", JSON.stringify(message));
}
async function inventoryWith(name, state) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const catalog = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "plugin-failure-isolation" } });
    if (catalog.extension_inventory.some(item => item.name === name && item.runtime_state === state)) return catalog;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`Plugin inventory never reached ${name}: ${state}`);
}
function plugin(mode) {
  return `import { appendFileSync } from "node:fs";
import type { RuntimePlugin } from "@wuu/plugin-sdk";
export default {
  initialize() {
    appendFileSync(${JSON.stringify(probe)}, JSON.stringify({ phase: "initialize", mode: ${JSON.stringify(mode)} }) + "\\n");
    ${mode === "throw" ? 'throw new Error("synthetic initialize failure");' : `return { ${mode === "invalid" ? "" : "protocol_version: 3,"} capabilities: [{ id: "agent.turn.completed", kind: "observe", version: 1 }] };`}
  },
  invokeCapability(params) {
    appendFileSync(${JSON.stringify(probe)}, JSON.stringify({ phase: "invoke", input: params.input }) + "\\n");
    return { output: {} };
  }
} satisfies RuntimePlugin;\n`;
}
async function publish(mode, path = source) {
  await writeFile(path, plugin(mode));
  await publishPath(mode, path);
}
async function publishPath(mode, path) {
  const deadline = Date.now() + 20000;
  for (;;) {
    const execution = spawnSync(binary, ["plugin", "dev", "--watch=false", path], {
      cwd: scratch, env, encoding: "utf8", timeout: Math.max(1, deadline - Date.now()),
    });
    publications.push({ mode, path, at: new Date().toISOString(), status: execution.status,
      signal: execution.signal, stdout: execution.stdout, stderr: execution.stderr, error: execution.error?.message });
    if (execution.status === 0) return;
    // A short watcher lease may reject publication before any generation is
    // changed. Retry only that exact contract, preserving the same input bytes;
    // all preparation/runtime failures and unknown outcomes remain failures.
    const deferred = execution.status === 2 && execution.stderr.trim() ===
      "error: dev generation refresh deferred; previous generation preserved: plugin catalog is being read or published by another operation";
    assert(deferred && Date.now() < deadline, execution.error?.message || execution.stderr);
    await new Promise(resolve => setTimeout(resolve, Math.min(20, deadline - Date.now())));
  }
}
// Raw wire peers deliberately bypass the SDK's own initializer validation.
async function publishRaw(id, mode, requires = []) {
  const path = join(scratch, id);
  await mkdir(path, { recursive: true });
  await writeFile(join(path, "package.json"), JSON.stringify({ private: true, scripts: { build: "node -e ''" } }));
  await writeFile(join(path, "plugin.json"), JSON.stringify({ id, name: id, schema_version: 1, requires,
    runtime: { protocol: "wuu-plugin-v1", command: process.execPath, args: ["runtime.mjs"], timeout: mode === "prepare-barrier" ? 30 : 1 } }));
  await writeFile(join(path, "runtime.mjs"), `import { createInterface } from "node:readline";
import { appendFileSync, existsSync, writeFileSync } from "node:fs";
const mode = ${JSON.stringify(mode)}, control = ${JSON.stringify(join(scratch, id + ".crash"))};
const emit = value => process.stdout.write(JSON.stringify(value) + "\\n");
createInterface({ input: process.stdin }).on("line", line => {
  const request = JSON.parse(line);
  appendFileSync(${JSON.stringify(probe)}, JSON.stringify({ raw: ${JSON.stringify(id)}, mode, method: request.method }) + "\\n");
  let result = {};
  if (request.method === "initialize") {
    if (mode === "hang") return;
    result = { lifecycle_version: 2, ...(mode === "invalid" ? {} : { protocol_version: 3 }), capabilities: [{ id: "agent.turn.completed", kind: "observe", version: 1 }] };
  }
  if (request.method === "prepare" && mode === "prepare-failure") { emit({ id: request.id, error: { code: "fixture_prepare", message: "optional preparation failed" } }); return; }
  if (request.method === "prepare" && mode === "prepare-hang") return;
  if (request.method === "prepare" && mode === "prepare-barrier") {
    writeFileSync(control + ".entered", "held");
    const timer = setInterval(() => { if (existsSync(control + ".release")) { clearInterval(timer); emit({ id: request.id, result: {} }); } }, 20);
    return;
  }
  if (request.method === "activate" && mode === "crash") setInterval(() => { if (existsSync(control)) process.exit(23); }, 20);
  if (request.method === "capability.invoke") result = { output: {} };
  emit({ id: request.id, result });
  if (request.method === "shutdown") process.exit(0);
});\n`);
  await publishPath(mode, path);
  return path;
}
const provider = createServer(async (request, response) => {
  let bytes = ""; for await (const part of request) bytes += part;
  const body = JSON.parse(bytes), prompt = body.messages.filter(message => message.role === "user").at(-1)?.content;
  const tag = typeof prompt === "string" ? prompt : JSON.stringify(prompt);
  calls.push({ tag, body });
  const finish = () => {
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: "assistant", content: "answer:" + tag }, finish_reason: "stop" }] })}\n\ndata: [DONE]\n\n`);
  };
  if (tag === "held accepted turn") { held = finish; events.emit("held", { tag }); } else finish();
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));
try {
  await writeFile(config, JSON.stringify({ default_provider: "fixture", providers: { fixture: { type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}/v1`, api_key: "synthetic", model: "fixture" } }, ptc: { enabled: false }, skills: { enabled: false } }));
  child = spawn(binary, ["app-server", "--workdir", scratch, "--config", config], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  createInterface({ input: child.stdout }).on("line", line => { const message = JSON.parse(line); transcript.push(message); events.emit("message", message); });
  await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "plugin-failure-isolation" } });
  const { thread } = await rpc("thread/start");
  const send = async prompt => {
    const response = await settledSend(thread.id, prompt);
    assert(!response.error, `turn/start: ${JSON.stringify(response.error)}`);
    return response.result;
  };
  await completed((await send("healthy baseline")).turn);
  checks.push("existing conversation sends and completes before publication");
  const accepted = (await send("held accepted turn")).turn;
  if (!held) await waitFor("held", [], () => true, "model accepted held request");
  await publish("invalid");
  held(); held = null;
  await completed(accepted);
  checks.push("accepted turn completes using retained generation across invalid publication");
  if (expectBlocked) {
    const blocked = await settledSend(thread.id, "must not be globally blocked");
    assert(blocked.error); assert.match(JSON.stringify(blocked.error), /protocol v1 cannot declare capabilities or host services|explicit protocol_version|protocol_version.*(?:2|3)/);
    const retry = await raw("turn/start", { thread_id: thread.id, prompt: "retry draft" });
    assert.match(JSON.stringify(retry.error), /plugin generation refresh is waiting to retry/);
    checks.push("reproduced invalid optional plugin globally blocking existing-conversation send and retry");
  } else {
    const initial = await settledSend(thread.id, "same draft after quarantine");
    assert(!initial.error, JSON.stringify(initial.error));
    await completed(initial.result.turn);
    const refreshed = await inventoryWith("observer", "failed");
    const failed = refreshed.extension_inventory.find(item => item.name === "observer");
    assert(failed, JSON.stringify(refreshed));
    assert.match(JSON.stringify(failed), /protocol v1 cannot declare capabilities or host services|explicit protocol_version|protocol_version.*(?:2|3)/);
    result.quarantined = failed;
    for (const prompt of ["same draft after quarantine", "second healthy send"]) await completed((await send(prompt)).turn);
    checks.push("invalid optional observer remains diagnostic-only; same conversation repeatedly sends");
    const attempts = (await readFile(probe, "utf8")).trim().split("\n").map(JSON.parse).filter(row => row.phase === "initialize").length;
    await completed((await send("no backoff gate")).turn);
    assert.equal((await readFile(probe, "utf8")).trim().split("\n").map(JSON.parse).filter(row => row.phase === "initialize").length, attempts);
    checks.push("quarantined startup failure does not rebuild before each turn");
    await publish("throw", join(scratch, "throwing-observer.ts"));
    const thrown = await rpc("extension/catalog/refresh");
    assert.match(JSON.stringify(thrown.extension_inventory), /synthetic initialize failure/);
    await completed((await send("unrelated initialize throw")).turn);
    checks.push("independent optional initialize exception is isolated with diagnostic");
    await publish("valid");
    await rpc("extension/catalog/refresh");
    await completed((await send("repaired observer")).turn);
    const observerDeadline = Date.now() + 5000;
    while (!(await readFile(probe, "utf8")).split("\n").filter(Boolean).map(JSON.parse).some(row => row.phase === "invoke")) {
      assert(Date.now() < observerDeadline, "repaired observer never received settled turn");
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    checks.push("publishing repaired source activates observer and preserves send");
    await publish("invalid");
    const rejectedReplacement = await rpc("extension/catalog/refresh");
    assert.match(JSON.stringify(rejectedReplacement.extension_inventory), /protocol v1 cannot declare capabilities or host services|explicit protocol_version|protocol_version.*(?:2|3)/);
    await completed((await send("failed optional replacement remains isolated")).turn);
    checks.push("bad optional replacement is quarantined with diagnostics without blocking unrelated send");
    await publish("valid");
    await rpc("extension/catalog/refresh");
    await completed((await send("repaired active replacement")).turn);
    checks.push("repairing failed active replacement unblocks admission");
    await publishRaw("raw-invalid-observer", "invalid");
    await completed((await send("raw invalid handshake")).turn);
    let catalog = await inventoryWith("raw-invalid-observer", "failed");
    assert.match(JSON.stringify(catalog.extension_inventory.find(item => item.name === "raw-invalid-observer")), /protocol v1 cannot declare capabilities or host services|explicit protocol_version|protocol_version.*(?:2|3)/);
    checks.push("raw malformed initialize response is quarantined independently of SDK validation");
    await publishRaw("crash-provider", "crash");
    await publishRaw("crash-dependent", "valid", ["crash-provider"]);
    await completed((await send("before optional crash")).turn);
    await inventoryWith("crash-dependent", "active");
    await writeFile(join(scratch, "crash-provider.crash"), "crash the synthetic process\n");
    const crashStarts = (await readFile(probe, "utf8")).split("\n").filter(Boolean).map(JSON.parse).filter(row => row.raw === "crash-provider" && row.method === "initialize").length;
    const crashDeadline = Date.now() + 10000;
    do {
      catalog = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "plugin-failure-isolation" } });
      if (catalog.extension_inventory.some(item => item.name === "crash-provider" && item.runtime_state === "failed")) break;
      await new Promise(resolve => setTimeout(resolve, 25));
    } while (Date.now() < crashDeadline);
    assert(catalog.extension_inventory.some(item => item.name === "crash-provider" && item.runtime_state === "failed"));
    await completed((await send("unrelated send after optional crash")).turn);
    catalog = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "plugin-failure-isolation" } });
    const dependent = catalog.extension_inventory.find(item => item.name === "crash-dependent");
    assert.equal(dependent.runtime_state, "failed", JSON.stringify(dependent));
    assert.match(dependent.last_error, /requires/);
    await completed((await send("repeat healthy send without crash restart loop")).turn);
    assert.equal((await readFile(probe, "utf8")).split("\n").filter(Boolean).map(JSON.parse).filter(row => row.raw === "crash-provider" && row.method === "initialize").length, crashStarts,
      "automatic recovery restarted unchanged crashing plugin bytes");
    checks.push("crashed optional runtime and its required consumer quarantine; unrelated conversation recovers");
    await publishRaw("crash-provider", "valid");
    await completed((await send("provider repair restores unchanged dependent")).turn);
    catalog = await inventoryWith("crash-dependent", "active");
    assert.equal(catalog.extension_inventory.find(item => item.name === "crash-dependent").runtime_state, "active");
    checks.push("changed provider source automatically restores its unchanged required consumer");
    await publishRaw("additive-held", "prepare-barrier");
    const enteredDeadline = Date.now() + 10000;
    while (!(await readFile(join(scratch, "additive-held.crash.entered"), "utf8").catch(() => ""))) {
      assert(Date.now() < enteredDeadline, "additive candidate never entered real prepare barrier");
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    await completed((await send("completes while optional preparation remains held")).turn);
    assert(!(await readFile(probe, "utf8")).split("\n").filter(Boolean).map(JSON.parse).some(row => row.raw === "additive-held" && row.method === "activate"));
    await writeFile(join(scratch, "additive-held.crash.release"), "release only after turn completed\n");
    await inventoryWith("additive-held", "active");
    checks.push("new send completes while real additive plugin prepare is held; activation follows explicit barrier release");
    for (const mode of ["hang", "prepare-failure", "prepare-hang"]) {
      await publishRaw("optional-" + mode, mode);
      const started = performance.now();
      await completed((await send("unrelated send after " + mode)).turn);
      const elapsed = performance.now() - started;
      catalog = await inventoryWith("optional-" + mode, "failed");
      const failed = catalog.extension_inventory.find(item => item.name === "optional-" + mode);
      assert.equal(failed.runtime_state, "failed", JSON.stringify(failed));
      result.failure_latency_ms ??= {}; result.failure_latency_ms[mode] = Math.round(elapsed);
    }
    checks.push("optional initialize/prepare timeout and prepare exception settle into quarantine with measured send delay");
    result.limitations.push("Only strictly additive inferred addons prepare without fencing; replacements and authority-changing transitions remain fenced.");
    const disabled = catalog.extension_inventory.find(item => item.name === "optional-hang");
    await rpc("extension/package/update", { id: disabled.id, fingerprint: disabled.fingerprint, action: "disable" });
    await completed((await send("send after disabling quarantined addon")).turn);
    checks.push("explicit disable removes a quarantined addon and conversation remains usable");
    const exited = once(child, "exit"); child.stdin.end(); await exited;
    child = spawn(binary, ["app-server", "--workdir", scratch, "--config", config], { env, stdio: ["pipe", "pipe", "pipe"] });
    child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
    createInterface({ input: child.stdout }).on("line", line => { const message = JSON.parse(line); transcript.push(message); events.emit("message", message); });
    const restarted = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "plugin-failure-isolation-restarted" } });
    assert.equal(restarted.status, "ready");
    await completed((await send("persisted conversation after restart with broken addons")).turn);
    checks.push("fresh core startup isolates broken optional addons and resumes persisted conversation");
  }
  result.status = "passed";
} catch (error) { result.error = error.stack; process.exitCode = 1; }
finally {
  if (held) held();
  if (child && child.exitCode === null) {
    const exited = once(child, "exit"), timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    child.stdin.end(); await exited; clearTimeout(timer);
  }
  provider.closeAllConnections(); await new Promise(resolve => provider.close(resolve));
  await writeFile(join(scratch, "wire.jsonl"), transcript.map(value => JSON.stringify(value)).join("\n") + "\n");
  await writeFile(join(scratch, "core.stderr.log"), stderr);
  await writeFile(join(scratch, "result.json"), JSON.stringify({ ...result, calls, publications }, null, 2));
  console.log(`${result.status}: ${scratch}/result.json`); if (result.error) console.error(result.error);
}
