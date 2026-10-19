#!/usr/bin/env node
// Real Wuu stdio app-server + synthetic Codex subprocess, no vendor account or
// inference. Build bin/wuu and bin/fakecodex first. Requires Node 22+.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const fake = resolve(root, process.env.WUU_E2E_CODEX || "bin/fakecodex");
const output = resolve(root, process.env.WUU_EFFORT_E2E_OUTPUT || ".tmp/e2e/native-effort");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const result = { scratch, status: "failed", checks: [], scenarios: [],
  limitations: ["Synthetic native engine: verifies selection, persistence, and wire transport, not vendor inference or GUI interaction."] };
const hash = async file => createHash("sha256").update(await readFile(file)).digest("hex");
const provider = createServer(async (request, response) => {
  for await (const chunk of request) { /* drain synthetic title requests */ }
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  response.end(`data: ${JSON.stringify({ choices: [{ delta: { content: "Fixture conversation" }, finish_reason: "stop" }] })}\n\ndata: [DONE]\n\n`);
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));

async function scenario(configuredEffort) {
  const home = join(scratch, configuredEffort ? "configured" : "model-default");
  await mkdir(home);
  const wirePath = join(home, "native-requests.jsonl"), lifecyclePath = join(home, "native-lifecycle.jsonl");
  // Do not inherit credentials, user configuration, or native home directories.
  const env = { PATH: process.env.PATH, HOME: home, WUU_HOME: home, CODEX_HOME: join(home, "codex"),
    WUU_TEST_CODEX_DEFAULT_EFFORT: configuredEffort, WUU_TEST_CODEX_REPORT_EFFORT: "1",
    WUU_TEST_CODEX_REQUESTS: wirePath, WUU_TEST_CODEX_LIFECYCLE: lifecyclePath };
  await writeFile(join(home, "config.json"), JSON.stringify({ default_provider: "fixture",
    providers: { fixture: { type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}`, api_key: "synthetic", model: "fixture" } },
    engines: Object.fromEntries(["codex", "claude", "cursor", "devin", "dsh", "grok", "hermes", "pi", "opencode", "antigravity"]
      .map(id => [id, id === "codex" ? { enabled: true, binary_path: fake } : { enabled: false }])),
    skills: { enabled: false }, ptc: { enabled: false }, agent: { permission_mode: "standard" } }));
  const events = new EventEmitter(), transcript = [], requests = [], observations = [];
  let sequence = 0, stderr = "";
  const child = spawn(binary, ["app-server", "--safe-mode", "--workdir", home], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  child.on("error", error => { stderr += String(error); });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const message = JSON.parse(line); transcript.push(message); events.emit("message", message); }
    catch { stderr += `\n${line}`; }
  });
  function waitFor(predicate, label) {
    const previous = transcript.find(predicate);
    if (previous) return Promise.resolve(previous);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { events.off("message", listener); reject(new Error(`Timed out: ${label}\n${stderr}`)); }, 15000);
      function listener(message) { if (!predicate(message)) return; clearTimeout(timer); events.off("message", listener); resolve(message); }
      events.on("message", listener);
    });
  }
  async function rpc(method, params = {}) {
    const request = { id: `effort-${++sequence}`, method, params };
    requests.push(request);
    child.stdin.write(JSON.stringify(request) + "\n");
    const response = await waitFor(message => message.id === request.id, method);
    assert(!response.error, `${method}: ${JSON.stringify(response.error)}`);
    return response.result;
  }
  const readWire = async () => (await readFile(wirePath, "utf8")).trim().split("\n").map(line => JSON.parse(line));
  const readLifecycle = async () => (await readFile(lifecyclePath, "utf8")).trim().split("\n").map(line => JSON.parse(line));
  async function run(id, expectedEffort, expectedWire = expectedEffort) {
    const { turn } = await rpc("turn/start", { thread_id: id, prompt: `Check effort ${expectedEffort}` });
    const completed = await waitFor(message => ["turn/completed", "turn/error"].includes(message.method)
      && message.params?.thread_id === id && message.params?.turn?.id === turn.id, turn.id);
    assert.equal(completed.params.turn.status, "completed", JSON.stringify(completed));
    // The fixture applies only the native effort field and echoes its effective
    // setting. The obsolete spelling is ignored, like an unknown serde field.
    assert.equal(completed.params.content, `Reasoning effort: ${expectedEffort}`);
    const wire = (await readWire()).findLast(request => request.method === "turn/start");
    assert.equal(wire.params.effort, expectedWire === null ? undefined : expectedWire);
    assert.equal(Object.hasOwn(wire.params, "reasoningEffort"), false);
    observations.push({ thread_id: id, expectedEffort, expectedWire, wire: wire.params });
    return wire.params;
  }
  try {
    await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "native-effort-e2e" } });
    const catalog = await rpc("engine/list");
    const model = catalog.engines.find(engine => engine.id === "codex").models.find(model => model.id === "gpt-6-astra");
    assert.deepEqual(model.supported_efforts, ["low", "high"]);
    assert.equal(model.default_effort, "high");
    const discoveryInitializations = (await readLifecycle()).filter(event => event.method === "initialize").length;
    const initial = configuredEffort ? "high" : "low";
    const thread = (await rpc("thread/start", { engine: "codex", model: model.id, effort: initial, speed: "standard" })).thread;
    const first = await run(thread.id, initial);
    for (const effort of ["high", "low"]) {
      await rpc("config/model/update", { thread_id: thread.id, effort });
      const resumed = (await rpc("thread/resume", { session_id: thread.id })).thread;
      assert.equal(resumed.model_effort, effort);
      const wire = await run(thread.id, effort);
      assert.equal(wire.threadId, first.threadId);
      assert.equal(wire.serviceTier, "default", "effort changes preserve speed");
    }
    await rpc("config/model/update", { thread_id: thread.id, effort: "" });
    assert.equal(((await rpc("thread/resume", { session_id: thread.id })).thread.model_effort || ""), "");
    await run(thread.id, configuredEffort || model.default_effort);
    await rpc("config/model/update", { thread_id: thread.id, effort: initial });
    await run(thread.id, initial);
    await rpc("config/model/update", { thread_id: thread.id, reset_model: true });
    const reset = await run(thread.id, configuredEffort || model.default_effort);
    assert.equal(reset.model, model.id);
    assert.equal(reset.threadId, first.threadId);
    if (!configuredEffort) {
      // No configured or catalog default for an unknown model: omission must
      // retain the native value, never send an invalid empty effort string.
      await rpc("config/model/update", { thread_id: thread.id, model: "unknown-fixture-model" });
      await run(thread.id, model.default_effort, null);
    }
    const wire = await readWire();
    assert.equal(wire.filter(request => request.method === "thread/start").length, 1);
    assert(wire.filter(request => request.method === "thread/resume").length >= 5);
    const lifecycle = await readLifecycle();
    assert.equal(lifecycle.filter(event => event.method === "initialize").length - discoveryInitializations, 1,
      "all turns reuse the same native host after separate model discovery");
    const label = configuredEffort ? "configured effort overrides catalog default on clear/reset" : "catalog default restores cleared/reset effort on a warm native thread";
    result.checks.push(label); console.log(`PASS ${label}`);
  } finally {
    if (child.exitCode === null && child.signalCode === null) {
      const exited = once(child, "exit"), timer = setTimeout(() => child.kill("SIGKILL"), 5000);
      child.stdin.end();
      try { const [code, signal] = await exited; assert.equal(signal, null); assert.equal(code, 0, stderr); }
      finally { clearTimeout(timer); }
    }
    await writeFile(join(home, "appserver.json"), JSON.stringify({ requests, transcript, observations }, null, 2));
    await writeFile(join(home, "stderr.log"), stderr);
    result.scenarios.push({ configuredEffort, home, observations, wirePath, lifecyclePath });
  }
}
try {
  result.artifacts = { core: { path: binary, sha256: await hash(binary) }, native: { path: fake, sha256: await hash(fake) }, driver_sha256: await hash(fileURLToPath(import.meta.url)) };
  await scenario("");
  await scenario("low");
  result.status = "passed";
} catch (error) {
  result.error = String(error.stack || error); console.error(result.error); process.exitCode = 1;
} finally {
  provider.close();
  await writeFile(join(output, "result.json"), JSON.stringify(result, null, 2));
  console.log(`Evidence: ${output}`);
}
