#!/usr/bin/env node
// Requires Node 22.13+, a built packages/plugin-sdk, and bin/wuu.
// Run: node scripts/plugin-model-request-e2e.mjs
// Uses disposable state, the public SDK, and a loopback-only synthetic provider.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { cp, mkdir, mkdtemp, readFile, unlink, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const output = resolve(root, process.env.WUU_MODEL_E2E_OUTPUT || ".tmp/e2e/plugin-model-request");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "state"), pkg = join(scratch, "plugin");
const initializeAttempts = join(scratch, "initialize-attempts.jsonl");
const initializeFailure = join(scratch, "initialize-failure");
const executionLifecycle = join(output, "execution-lifecycle.jsonl");
await writeFile(executionLifecycle, "");
await mkdir(home);
await writeFile(join(home, ".wuu-session-fixture"), "wuu-session-fixture-v1\n");
const fixtureBinary = resolve(root, process.env.WUU_E2E_SESSION_FIXTURE || join(scratch, "session-fixture"));
if (!process.env.WUU_E2E_SESSION_FIXTURE) {
  const build = spawnSync("go", ["build", "-o", fixtureBinary, "./scripts/session-fixture"], { cwd: root, encoding: "utf8" });
  assert.equal(build.status, 0, build.stderr);
}
function fixture(input) {
  const reply = spawnSync(fixtureBinary, [], { input: JSON.stringify({ home, workdir: scratch, ...input }),
    encoding: "utf8", timeout: 20000, maxBuffer: 64 * 1024 * 1024 });
  assert.equal(reply.status, 0, reply.stderr);
  return reply.stdout;
}
function journal() {
  const { documents } = JSON.parse(fixture({ op: "inspect",
    domains: ["inference_operations", "inference_submissions", "inference_workflows"] }));
  return {
    operations: documents.inference_operations.map(document => document.value).sort((a, b) => a.CreatedAt - b.CreatedAt),
    submissions: documents.inference_submissions.map(document => document.value).sort((a, b) => Date.parse(a.StartedAt) - Date.parse(b.StartedAt) || a.Ordinal - b.Ordinal),
    workflows: documents.inference_workflows.map(document => document.value).sort((a, b) => a.CreatedAt - b.CreatedAt),
  };
}

await mkdir(pkg);
const events = new EventEmitter(), transcript = [], requests = [], providerCalls = [], closed = [], transportEvents = [];
const result = { started_at: new Date().toISOString(), scratch, checks: [], status: "failed" };
let child, stderr = "", sequence = 0;

function waitFor(event, history, predicate, label, timeout = 20_000) {
  const previous = history.find(predicate);
  if (previous !== undefined) return Promise.resolve(previous);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      events.off(event, listener);
      reject(new Error(`Timed out: ${label}`));
    }, timeout);
    const listener = value => {
      if (!predicate(value)) return;
      clearTimeout(timer);
      events.off(event, listener);
      resolve(value);
    };
    events.on(event, listener);
  });
}

async function rpcRaw(method, params = {}) {
  const request = { id: `model-e2e-${++sequence}`, method, params };
  requests.push(request);
  child.stdin.write(JSON.stringify(request) + "\n");
  return await waitFor("message", transcript, message => message.id === request.id, method);
}

async function rpc(method, params = {}) {
  const message = await rpcRaw(method, params);
  assert(!message.error, `${method}: ${JSON.stringify(message.error)}`);
  return message.result;
}

async function stopChild() {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
  child.stdin.end();
  try {
    const [code, signal] = await exited;
    assert.equal(signal, null, "appserver did not stop cleanly after EOF");
    assert.equal(code, 0, stderr);
  } finally {
    clearTimeout(timer);
  }
}

const provider = createServer(async (request, response) => {
  let body = "";
  for await (const chunk of request) body += chunk;
  const value = JSON.parse(body);
  const wire = request.url.endsWith("/responses") ? "responses" : request.url.endsWith("/messages") ? "anthropic" : "chat";
  const content = (wire === "responses" ? value.input : value.messages)?.at(-1)?.content;
  const tag = typeof content === "string" ? content : content?.map(part => part.text ?? "").join("") ?? "missing-input";
  const call = { tag, url: request.url, wire, body: value };
  providerCalls.push(call);
  transportEvents.push({ tag, phase: "received", at: new Date().toISOString() });
  events.emit("provider", call);
  response.on("close", () => {
    if (response.writableEnded) return;
    closed.push(tag);
    transportEvents.push({ tag, phase: "disconnected", at: new Date().toISOString() });
    events.emit("closed", tag);
  });
  if (tag === "codex-best-effort") {
    assert.equal(value.stream, true, "Codex backend requires streaming responses");
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end("event: response.output_text.delta\n"
      + `data: ${JSON.stringify({ type: "response.output_text.delta", delta: `fixture:${tag}` })}\n\n`
      + "event: response.completed\n"
      + 'data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":7,"output_tokens":3}}}\n\n');
    return;
  }
  if (request.headers["x-grok-model-override"]) {
    assert.equal(value.stream, true, "Grok Build proxy needs a streaming request for auxiliary inference");
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    if (tag.startsWith("hold-")) {
      response.flushHeaders();
      return;
    }
    response.end(["fixture:", "grok"].map(content => `data: ${JSON.stringify({
      choices: [{ index: 0, delta: { content }, finish_reason: null }],
    })}\n\n`).join("")
      + `data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: "stop" }] })}\n\n`
      + `data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 7, completion_tokens: 3, total_tokens: 10 } })}\n\n`
      + "data: [DONE]\n\n");
    return;
  }
  response.writeHead(200, { "Content-Type": "application/json" });
  if (tag.startsWith("hold-")) {
    response.flushHeaders();
    return;
  }
  response.end(JSON.stringify(wire === "responses" ? {
    id: "fixture-response", status: "completed", model: value.model,
    output: [{ type: "message", role: "assistant", content: [{ type: "output_text", text: `fixture:${tag}` }] }],
    usage: { input_tokens: 7, output_tokens: 3, total_tokens: 10 },
  } : wire === "anthropic" ? {
    id: "fixture-response", type: "message", role: "assistant", model: value.model,
    content: [{ type: "text", text: `fixture:${tag}` }], stop_reason: "end_turn",
    usage: { input_tokens: 7, output_tokens: 3 },
  } : {
    id: "fixture-response", object: "chat.completion", model: value.model,
    choices: [{ index: 0, message: { role: "assistant", content: `fixture:${tag}` }, finish_reason: "stop" }],
    usage: { prompt_tokens: 7, completion_tokens: 3, total_tokens: 10 },
  }));
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));
// Do not inherit credentials, Wuu settings, or provider endpoint overrides.
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath,
  ...(process.env.GOMAXPROCS ? { GOMAXPROCS: process.env.GOMAXPROCS } : {}),
};

try {
  result.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  await cp(join(root, "packages/plugin-sdk/dist"), join(pkg, "sdk"), { recursive: true });
  await writeFile(join(pkg, "plugin.json"), JSON.stringify({
    schema_version: 1, id: "model-fixture", name: "Model fixture", version: "0.1.0",
    runtime: { protocol: "wuu-plugin-v1", command: process.execPath, args: ["runtime.mjs"], timeout: 60 },
    desktop: { entry: "desktop.js" },
  }));
  await writeFile(join(pkg, "desktop.js"), "export function activate() {}\n");
  await writeFile(join(pkg, "package.json"), JSON.stringify({
    name: "model-fixture", version: "0.1.0", type: "module", scripts: { build: 'node -e ""' },
  }));
  await writeFile(join(pkg, "runtime.mjs"), `
import { appendFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { runJSONLRuntime, requestModel, requireModelRequestService, reportExecutionUpdate } from "./sdk/index.js";
runJSONLRuntime({
  initialize() {
    const failurePath = ${JSON.stringify(initializeFailure)};
    const failure = existsSync(failurePath) ? readFileSync(failurePath, "utf8") : "";
    // Pass live preflight, then inject a changed environment for the distinct
    // committed runtime. This must not turn candidate rejection into success.
    if (failure === "after-preflight") writeFileSync(failurePath, "fail");
    const failed = failure === "fail";
    appendFileSync(${JSON.stringify(initializeAttempts)}, JSON.stringify({ at: Date.now(), pid: process.pid, failed }) + "\\n");
    if (failed) throw new Error("fixture candidate initialization refused");
    return {
      protocol_version: 3,
      host_services: [{ id: "host.service.call", required: true }],
      required_services: [requireModelRequestService(), { name: "execution.update", major_version: 1, required: true }],
      capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1 }],
    };
  },
  async invokeCapability(params, host, execution) {
    if (params.input.method === "hold") {
      const tag = params.input.input.tag;
      const record = phase => appendFileSync(${JSON.stringify(executionLifecycle)},
        JSON.stringify({ tag, phase, pid: process.pid, execution_id: execution.executionId }) + "\\n");
      record("started");
      const aborted = new Promise(resolve => {
        const cancel = () => { record("cancelled"); resolve(); };
        if (execution.signal.aborted) cancel();
        else execution.signal.addEventListener("abort", cancel, { once: true });
      });
      try {
        await reportExecutionUpdate(host, { execution_id: execution.executionId,
          message: tag, detail: { generation: tag, pid: process.pid } });
        await aborted;
        return { output: { result: { stopped: true } } };
      } finally { record("settled"); }
    }
    try {
      return { output: { result: await requestModel(host, params.input.input) } };
    } catch (error) {
      return { output: { result: { error: { code: error.code, message: error.message } } } };
    }
  },
}, { input: process.stdin, output: process.stdout });
`);
  const configPath = join(home, "config.json");
  const shapeOverrides = {
    model: "configured-wrong-model", models: ["configured-fallback-model"],
    messages: [{ role: "user", content: "configured-hidden-message" }],
    input: [{ role: "user", content: "configured-hidden-input" }],
    instructions: "configured-hidden-instructions", system: "configured-hidden-system",
    prompt: { id: "configured-remote-prompt" }, suffix: "configured-hidden-suffix",
    prediction: { type: "content", content: "configured-hidden-prediction" },
    previous_response_id: "configured-previous-response", conversation: "configured-conversation",
    context_management: [{ type: "compaction", compact_threshold: 1 }], container: "configured-container",
    tools: [{ type: "web_search_preview" }], tool_choice: "required",
    functions: [{ name: "configured_function", parameters: { type: "object" } }], function_call: "auto",
    parallel_tool_calls: true, parallelToolCalls: true,
    web_search_options: {}, search_parameters: { mode: "auto" }, plugins: [{ id: "web" }],
    stream: true, stream_options: { include_usage: false, fixture_override: true }, background: true, n: 2, best_of: 2,
    modalities: ["text", "audio"], audio: { voice: "alloy", format: "wav" }, store: true,
    maxOutputTokens: 128000, max_output_tokens: 128000, max_tokens: 128000, max_completion_tokens: 128000,
  };
  const fixtureConfig = (type, model, wire_api) => ({
    type, wire_api, base_url: `http://127.0.0.1:${provider.address().port}`,
    api_key: "synthetic-no-real-key", model,
    models: { [model]: { options: {
      ...shapeOverrides, temperature: 0.42, topP: 0.83, seed: 42,
      ...(type === "anthropic" ? { thinking: { type: "enabled", budgetTokens: 16 }, effort: "low", topK: 17 }
        : { reasoningEffort: "low", reasoningSummary: "auto", textVerbosity: "low",
          ...(wire_api === "responses" ? { text: { format: { type: "text" } }, omitStore: true } : { response_format: { type: "text" } }) }),
    } } },
  });
  await writeFile(configPath, JSON.stringify({
    default_provider: "fixture",
    providers: {
      fixture: fixtureConfig("openai-compatible", "gpt-test"),
      responses: fixtureConfig("openai-compatible", "gpt-test-responses", "responses"),
      anthropic: fixtureConfig("anthropic", "claude-test"),
      grok: fixtureConfig("grok-build", "grok-test"),
      unsupported: {
      type: "openai-codex", base_url: `http://127.0.0.1:${provider.address().port}`, stream_transport: "sse",
      api_key: "synthetic-no-real-key", model: "gpt-test",
    } },
    agent: { permission_mode: "standard", model_aliases: {
      short: { provider: "fixture", model: "gpt-test-alias" },
      responses: { provider: "responses", model: "gpt-test-responses" },
      anthropic: { provider: "anthropic", model: "claude-test" },
      grok: { provider: "grok", model: "grok-test" },
      unsupported: { provider: "unsupported", model: "gpt-test" },
    } },
    skills: { enabled: false }, ptc: { enabled: false },
  }));
  const published = spawnSync(binary, ["plugin", "dev", "--watch=false", pkg], { env, encoding: "utf8", timeout: 20_000 });
  result.publish = { status: published.status, stdout: published.stdout, stderr: published.stderr };
  assert.equal(published.status, 0, published.stderr);
  child = spawn(binary, ["app-server", "--workdir", scratch, "--config", configPath], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  createInterface({ input: child.stdout }).on("line", line => {
    try {
      const message = JSON.parse(line);
      transcript.push(message);
      events.emit("message", message);
    } catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const initialized = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "model-service-e2e" } });
  let item = initialized.extension_inventory.find(plugin => plugin.id.endsWith(":model-fixture"));
  assert(item, "published fixture is missing");
  assert.equal(item.runtime_state, "active", JSON.stringify(item));
  result.plugin = item;
  const registry = await rpc("plugin/registry/introspect");
  assert(registry.services.some(service => service.service === "host.model.request" && service.version === "1.0.0" && service.provider === "kernel"));
  result.checks.push("versioned service registration");
  // Real plugin processes keep foreground client calls alive across reloads.
  // Reuse the client request ID across generations and observe exact process
  // ownership. Host execution IDs are opaque: even diagnostic RPCs dispatch
  // authorization callbacks, so allocation arithmetic cannot force collisions.
  // The deterministic runtime diagnostics test covers colliding host-local IDs.
  const snapshots = async () => (await rpc("plugin/executions/list")).executions.filter(execution => execution.plugin_id === "model-fixture");
  const waitForExecution = async tag => {
    const deadline = Date.now() + 10_000;
    do {
      const rows = await snapshots();
      const row = rows.find(row => row.message === tag);
      if (row) return row;
      await new Promise(resolve => setTimeout(resolve, 20));
    } while (Date.now() < deadline);
    throw new Error(`Missing live execution: ${tag}`);
  };
  const hold = tag => {
    const identity = { id: item.id, fingerprint: item.fingerprint, graph_generation: item.graph_generation, request_id: "diagnostics-shared" };
    const pending = { identity, finished: false };
    pending.response = rpcRaw("plugin/client/request", { ...identity, method: "hold", input: { tag } }).then(response => {
      pending.finished = true;
      return response;
    });
    return pending;
  };
  const lifecycle = async () => (await readFile(executionLifecycle, "utf8")).trim().split("\n").filter(Boolean).map(line => JSON.parse(line));
  const waitForSettlement = async row => {
    const deadline = Date.now() + 10_000;
    do {
      const own = (await lifecycle()).filter(event => event.tag === row.message);
      if (own.some(event => event.phase === "settled")) {
        assert.deepEqual(own, ["started", "cancelled", "settled"].map(phase => ({
          tag: row.message, phase, pid: row.detail.pid, execution_id: row.id,
        })), "execution did not cancel and settle in its owning process");
        return own;
      }
      await new Promise(resolve => setTimeout(resolve, 20));
    } while (Date.now() < deadline);
    assert.fail(`execution did not settle in its owning process: ${row.message}`);
  };
  const publishUpdate = async (activationFailure = false) => {
    const source = join(pkg, "runtime.mjs");
    await writeFile(source, (await readFile(source, "utf8")) + "\n// Same-source lifecycle fixture update.\n");
    const deadline = Date.now() + 20_000;
    const attempts = [];
    (result.publication_updates ??= []).push(attempts);
    for (;;) {
      const update = spawnSync(binary, ["plugin", "dev", "--watch=false", pkg], {
        env, encoding: "utf8", timeout: Math.max(1, deadline - Date.now()),
      });
      attempts.push({ at: new Date().toISOString(), status: update.status, signal: update.signal,
        stdout: update.stdout, stderr: update.stderr, error: update.error?.message });
      if (activationFailure && update.status === 2 && /was published, but host activation is incomplete:/.test(update.stderr)) {
        assert.match(update.stderr, /fixture candidate initialization refused/);
        assert.doesNotMatch(update.stderr, /previous generation preserved/);
        break;
      }
      if (update.status === 0) { assert(!activationFailure, "faulted runtime unexpectedly activated"); break; }
      // The watcher may hold a short catalog read lease. Only this exact
      // pre-publication rejection is safe to retry within the original budget.
      const deferred = update.status === 2 && update.stderr.trim() ===
        "error: dev generation refresh deferred; previous generation preserved: plugin catalog is being read or published by another operation";
      assert(deferred && Date.now() < deadline, update.error?.message || update.stderr);
      await new Promise(resolve => setTimeout(resolve, Math.min(20, deadline - Date.now())));
    }
  };
  const reload = async () => {
    await publishUpdate();
    const refreshed = await rpc("extension/catalog/refresh");
    const previous = item;
    item = refreshed.extension_inventory.find(plugin => plugin.id === previous.id);
    assert.equal(item.runtime_state, "active");
    assert.notEqual(item.fingerprint, previous.fingerprint);
  };
  const initialHold = hold("diagnostics-initial");
  const initialRow = await waitForExecution("diagnostics-initial");
  assert.equal(initialRow.generation_id, initialHold.identity.graph_generation);
  result.execution_diagnostics = { before_reload: await snapshots() };
  await reload();
  const secondHold = hold("diagnostics-second");
  const secondRow = await waitForExecution("diagnostics-second");
  assert.equal(secondRow.generation_id, secondHold.identity.graph_generation);
  const during = await snapshots();
  result.execution_diagnostics.during_reload = during;
  assert.equal(during.length, 2, "retained foreground execution disappeared after reload");
  assert.equal(initialHold.identity.request_id, secondHold.identity.request_id);
  assert(initialRow.generation_id && secondRow.generation_id);
  assert.notEqual(initialRow.generation_id, secondRow.generation_id);
  assert.notEqual(initialRow.detail.pid, secondRow.detail.pid);
  assert.equal(initialRow.retained, false);
  assert.equal(secondRow.retained, false);
  assert.deepEqual(during.find(row => row.generation_id === initialRow.generation_id), { ...initialRow, retained: true });
  assert.deepEqual(during.find(row => row.generation_id === secondRow.generation_id), secondRow);
  assert.deepEqual(during, await snapshots(), "unchanged diagnostics must have stable ordering and identities");
  assert.deepEqual(during.map(row => [row.generation_id, row.id]),
    [...during].sort((a, b) => a.generation_id < b.generation_id ? -1 : a.generation_id > b.generation_id ? 1 : a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
      .map(row => [row.generation_id, row.id]));
  // An optional broken replacement publishes a diagnosed, quarantined graph.
  // Both admitted calls keep their exact old owners, now marked retained;
  // fresh requests must not fall back to either old executable. Required
  // composition rejection/backoff is covered by the real-process reload test.
  const heldLifecycle = await lifecycle();
  await writeFile(initializeFailure, "after-preflight");
  await publishUpdate(true);
  const failedPublication = await waitFor("message", transcript, message => message.method === "plugin/inventory/changed"
    && message.params.extension_inventory.some(plugin => plugin.id === item.id
      && plugin.runtime_state === "failed" && plugin.fingerprint !== item.fingerprint), "optional replacement quarantined");
  const failedItem = failedPublication.params.extension_inventory.find(plugin => plugin.id === item.id);
  assert.equal(failedItem.enabled, true, "quarantine silently disabled the selected package");
  assert.match(failedItem.last_error, /fixture candidate initialization refused/);
  assert.notEqual(failedItem.graph_generation, item.graph_generation);
  assert.equal((await rpc("plugin/registry/introspect")).graph_generation, failedItem.graph_generation,
    "failure inventory did not describe the current graph");
  const retained = during.map(row => ({ ...row, retained: true }));
  assert.deepEqual(await snapshots(), retained, "quarantine changed admitted execution ownership");
  const readFailedAttempts = async () => (await readFile(initializeAttempts, "utf8")).trim().split("\n")
    .map(line => JSON.parse(line)).filter(attempt => attempt.failed);
  const failedAttempts = await readFailedAttempts();
  assert.equal(failedAttempts.length, 1, "optional replacement was initialized more than once");
  assert(!retained.some(row => row.detail.pid === failedAttempts[0].pid), "replacement reused an admitted process");
  const rejectedRequests = [];
  for (const [identity, expected] of [
    [secondHold.identity, /graph generation is no longer active/],
    [failedItem, /runtime is no longer active/],
  ]) {
    const rejected = await rpcRaw("plugin/client/request", {
      id: identity.id, fingerprint: identity.fingerprint, graph_generation: identity.graph_generation,
      request_id: "diagnostics-shared", method: "model",
      input: { messages: [{ role: "user", content: "quarantined-must-not-dispatch" }], max_output_tokens: 32 },
    });
    assert.match(rejected.error?.message ?? "", expected, "fresh request reached a quarantined or retained executable");
    rejectedRequests.push(rejected);
  }
  // Cover the former one- and two-second retry boundaries while generating
  // unrelated home notifications. Sticky quarantine must not spawn again.
  const noiseStarted = Date.now(), noiseDeadline = noiseStarted + 3_200;
  do {
    await writeFile(join(home, "unrelated-refresh-noise"), String(Date.now()));
    assert.deepEqual(await snapshots(), retained, "home notifications changed retained ownership");
    assert.deepEqual(await readFailedAttempts(), failedAttempts, "home notifications restarted the quarantined implementation");
    await new Promise(resolve => setTimeout(resolve, 50));
  } while (Date.now() < noiseDeadline);
  const quarantined = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "model-service-e2e" } });
  assert.deepEqual(quarantined.extension_inventory.find(plugin => plugin.id === item.id), failedItem,
    "unchanged quarantine lost its current generation or diagnostic");
  assert.deepEqual(await readFailedAttempts(), failedAttempts);
  assert(!initialHold.finished && !secondHold.finished, "quarantine settled an admitted client request");
  assert.deepEqual(await lifecycle(), heldLifecycle, "quarantine cancelled or dispatched work in an old process");
  assert.equal(providerCalls.length, 0, "quarantined or stale request reached the model provider");
  result.optional_quarantine = { item: failedItem, failed_attempts: failedAttempts, rejected_requests: rejectedRequests,
    noise_observation_ms: Date.now() - noiseStarted, executions_during_failure: await snapshots() };
  item = failedItem;
  // A new source publication retries the failed implementation. Remove only
  // the fixture's synthetic failure flag; accepted old calls still keep owners.
  await unlink(initializeFailure);
  await reload();
  assert.notEqual(item.graph_generation, failedItem.graph_generation);
  assert.deepEqual(await snapshots(), retained, "repaired publication changed admitted execution ownership");
  result.checks.push("optional quarantine publishes diagnostics, rejects fresh work without fallback, preserves retained owners across home noise, and repairs on source publication");
  const thirdHold = hold("diagnostics-third");
  const thirdRow = await waitForExecution("diagnostics-third");
  assert.equal(thirdRow.generation_id, thirdHold.identity.graph_generation);
  assert.equal(thirdHold.identity.request_id, initialHold.identity.request_id);
  const multiple = await snapshots();
  result.execution_diagnostics.multiple_retained = multiple;
  assert.equal(multiple.length, 3);
  assert.equal(multiple.filter(row => row.retained).length, 2);
  assert.equal(new Set(multiple.map(row => `${row.generation_id}/${row.id}`)).size, 3);
  assert.equal(new Set(multiple.map(row => row.detail.pid)).size, 3);
  for (const row of [initialRow, secondRow, thirdRow]) {
    assert.deepEqual(multiple.find(value => value.generation_id === row.generation_id), { ...row, retained: row !== thirdRow });
  }
  assert.equal((await rpc("plugin/client/cancel", initialHold.identity)).cancelled, true);
  assert((await initialHold.response).error);
  result.execution_diagnostics.cancelled_owner = await waitForSettlement(initialRow);
  const afterCancel = await snapshots();
  result.execution_diagnostics.after_cancel = afterCancel;
  assert.deepEqual(afterCancel, multiple.filter(row => row.generation_id !== initialRow.generation_id),
    "old generation cancellation affected another generation");
  assert(!(await lifecycle()).some(event => event.tag !== initialRow.message && event.phase !== "started"),
    "cancellation reached another process with the same request ID");
  // The terminal response can precede final request-map cleanup. Repeated
  // cancellation may still acknowledge that old entry; it must not touch the
  // exact surviving executions or settle their client requests.
  result.execution_diagnostics.late_cancel = await rpc("plugin/client/cancel", initialHold.identity);
  assert.deepEqual(await snapshots(), afterCancel, "late cancellation changed surviving executions");
  const retirementDeadline = Date.now() + 10_000;
  let retirement;
  do {
    // Repeated diagnostics run while the last foreground owner releases and
    // shuts down its host; readers must not pin that generation themselves.
    const [, diagnostics] = await Promise.all([snapshots(), rpc("plugin/generation-diagnostics/list")]);
    retirement = diagnostics.reports.find(report => report.generation_id === initialRow.generation_id);
    if (retirement) break;
    await new Promise(resolve => setTimeout(resolve, 20));
  } while (Date.now() < retirementDeadline);
  assert(retirement, "diagnostic reads kept a released generation alive");
  result.execution_diagnostics.retirement = retirement;
  assert(!secondHold.finished && !thirdHold.finished, "old cancellation settled a replacement client request");
  assert(!(await lifecycle()).some(event => event.tag !== initialRow.message && event.phase !== "started"),
    "late old cleanup cancelled a replacement process");
  assert.deepEqual(await snapshots(), afterCancel, "retired owner cleanup changed surviving executions");
  await rpc("extension/package/update", { id: item.id, fingerprint: item.fingerprint, action: "disable" });
  assert((await secondHold.response).error);
  assert((await thirdHold.response).error);
  await waitForSettlement(secondRow);
  await waitForSettlement(thirdRow);
  assert.deepEqual(await snapshots(), [], "disable left current or retained work visible");
  result.execution_diagnostics.after_disable = await snapshots();
  const enabled = await rpc("extension/package/update", { id: item.id, fingerprint: item.fingerprint, action: "enable" });
  item = enabled.extension_inventory.find(plugin => plugin.id === item.id);
  assert.equal(item.runtime_state, "active");
  result.checks.push("retained execution diagnostics across repeated reload, shared request IDs, exact process cancellation/settlement, late cancellation, and disable");
  const before = await rpc("thread/list");
  const input = tag => ({ messages: [{ role: "system", content: "Return fixture data." }, { role: "user", content: tag }], max_output_tokens: 32 });
  const params = (request_id, value) => ({ id: item.id, fingerprint: item.fingerprint,
    graph_generation: item.graph_generation, request_id, method: "model", input: value });
  const call = async (requestID, value) => (await rpc("plugin/client/request", params(requestID, value))).result;
  const success = await call("success", input("success"));
  assert.deepEqual(await snapshots(), [], "completed invocation remained in diagnostics");
  assert.equal(success.text, "fixture:success");
  assert.equal(success.provider, "fixture");
  assert.equal(success.model, "gpt-test");
  assert.equal(success.usage.input_tokens, 7);
  assert.equal(success.usage.output_tokens, 3);
  assert.equal(success.finish_reason, "stop");
  assert.equal(success.truncated, false);
  result.success = success;
  assert.equal(providerCalls[0].body.max_tokens, 32);
  for (const key of ["maxOutputTokens", "max_output_tokens", "max_completion_tokens"]) {
    assert.equal(providerCalls[0].body[key], undefined, `${key} bypassed the requested output cap`);
  }
  assert.equal(providerCalls[0].body.messages.length, 2);
  assert(!providerCalls[0].body.tools?.length);
  result.checks.push("public SDK response and usage", "explicit messages, output cap, no tools");
  result.checks.push("configured provider options cannot override the output cap");
  for (const [name, model, wire] of [["fixture", "gpt-test", "chat"], ["responses", "gpt-test-responses", "responses"],
    ["anthropic", "claude-test", "anthropic"], ["grok", "grok-test", "chat"]]) {
    const tag = name === "fixture" ? "success" : name;
    const reply = name === "fixture" ? success : await call(name, { ...input(tag), model_alias: name });
    assert.equal(reply.text, `fixture:${tag}`);
    assert.equal(reply.model, model);
    assert.equal(reply.provider, name);
    assert.equal(reply.usage.input_tokens, 7);
    assert.equal(reply.usage.output_tokens, 3);
    const captured = providerCalls.find(call => call.tag === tag);
    assert.equal(captured.wire, wire);
    const body = captured.body;
    assert.equal(body.model, reply.model, "reported model differs from actual wire model");
    const ownedFields = new Set(["model", "messages", "input", "instructions", "system", "stream", "stream_options", "store", "max_tokens", "max_output_tokens"]);
    for (const key of Object.keys(shapeOverrides)) {
      if (!ownedFields.has(key)) assert.equal(body[key], undefined, `${name}: configured ${key} crossed the request boundary`);
    }
    assert.equal(Boolean(body.stream), name === "grok", `${name}: unexpected transport mode`);
    assert.deepEqual(body.stream_options, name === "grok" ? { include_usage: true } : undefined,
      `${name}: transport-owned usage reporting was overridden`);
    assert(!body.store, `${name}: configured server-side storage was enabled`);
    assert.equal(body.temperature, 0.42, `${name}: sampling option was lost`);
    assert.equal(body.top_p, 0.83, `${name}: sampling alias was lost`);
    if (wire === "responses") {
      assert.equal(body.store, undefined, "Responses transport's omitStore compatibility flag was lost");
      assert.equal(body.max_output_tokens, 32);
      assert.equal(body.max_tokens, undefined);
      assert.equal(body.instructions, input(tag).messages[0].content);
      assert.deepEqual(body.input, [{ role: "user", content: tag }]);
      assert.equal(body.messages, undefined);
      assert.equal(body.system, undefined);
      assert.deepEqual(body.reasoning, { effort: "low", summary: "auto" });
      assert.deepEqual(body.text, { format: { type: "text" }, verbosity: "low" });
      assert.equal(body.seed, 42);
    } else {
      assert.equal(body.max_tokens, 32);
      assert.equal(body.max_output_tokens, undefined);
      assert.equal(body.input, undefined);
      assert.equal(body.instructions, undefined);
      if (wire === "anthropic") {
        assert.equal(body.system, input(tag).messages[0].content);
        assert.equal(body.messages.length, 1);
        assert.equal(body.messages[0].role, "user");
        assert.equal(body.messages[0].content.map(part => part.text).join(""), tag);
        assert.deepEqual(body.thinking, { type: "enabled", budget_tokens: 16 });
        assert.deepEqual(body.output_config, { effort: "low" });
        assert.equal(body.top_k, 17);
      } else {
        assert.deepEqual(body.messages, input(tag).messages);
        assert.equal(body.system, undefined);
        assert.equal(body.reasoning_effort, "low");
        assert.equal(body.verbosity, "low");
        assert.deepEqual(body.response_format, { type: "text" });
        assert.equal(body.seed, 42);
      }
    }
    result.checks.push(`${name}: configured request-shape overrides blocked; explicit context, selected model, cap and tuning preserved`);
  }
  const alias = await call("alias", { ...input("alias"), model_alias: "short" });
  assert.equal(alias.model, "gpt-test-alias");
  assert.equal(providerCalls.at(-1).body.model, "gpt-test-alias");
  result.checks.push("configured alias resolution");
  const count = providerCalls.length;
  const unsupported = await call("unsupported-limit", { ...input("unsupported"), model_alias: "unsupported" });
  assert.equal(unsupported.error?.code, "unsupported_limit", JSON.stringify(unsupported));
  assert.equal(providerCalls.length, count, "unsupported output cap reached the provider");
  result.checks.push("adapter dropping output cap is rejected before inference");
  const bestEffort = await call("codex-best-effort", { ...input("codex-best-effort"), model_alias: "unsupported", allow_unbounded_output: true });
  assert.equal(bestEffort.text, "fixture:codex-best-effort", JSON.stringify(bestEffort));
  assert.equal(providerCalls.at(-1).body.max_output_tokens, undefined);
  result.checks.push("explicit best-effort cap allows Codex request without output-token field");
  const validCount = providerCalls.length;
  for (const [name, value] of [
    ["bad-role", { messages: [{ role: "tool", content: "bad" }] }],
    ["oversize", input("x".repeat(262145))],
    ["bad-tokens", { ...input("bad"), max_output_tokens: 8193 }],
    ["bad-timeout", { ...input("bad"), timeout_ms: 45001 }],
    ["bad-alias", { ...input("bad"), model_alias: "missing" }],
    ["extra-field", { ...input("bad"), api_key: "must-not-forward" }],
  ]) {
    assert((await call(name, value)).error, name);
    result.checks.push(`rejected ${name}`);
  }
  assert.equal(providerCalls.length, validCount, "invalid requests reached the provider");
  const beforeDeadline = journal();
  const shortDeadlineMS = 200;
  const timedOut = await call("timeout", { ...input("hold-timeout"), model_alias: "grok", timeout_ms: shortDeadlineMS });
  assert.equal(timedOut.error.code, "deadline_exceeded", JSON.stringify(timedOut));
  const shortDeadlineReachedProvider = providerCalls.some(call => call.tag === "hold-timeout");
  if (shortDeadlineReachedProvider) {
    await waitFor("closed", closed, tag => tag === "hold-timeout", "short-deadline provider disconnect");
  }
  // Admission/configuration also consume the caller's deadline. An expired
  // request that never reached HTTP has no provider connection to disconnect.
  const afterDeadline = journal();
  const shortDeadlineAccounting = {
    operations: afterDeadline.operations.filter(record => !beforeDeadline.operations.some(previous => previous.ID === record.ID)),
    submissions: afterDeadline.submissions.filter(record => !beforeDeadline.submissions.some(previous => previous.ID === record.ID)),
  };
  assert(shortDeadlineAccounting.operations.length <= 1);
  assert(shortDeadlineAccounting.submissions.length <= 1);
  assert(shortDeadlineAccounting.operations.every(record => record.Status === "canceled" && record.Failure?.Category === "deadline"));
  assert(shortDeadlineAccounting.submissions.every(record => record.Outcome === "failed" && record.FailureCategory === "deadline"
    && record.CompletedAt && !record.ReportedUsage && record.OutputBytes === 0));
  if (shortDeadlineReachedProvider) {
    assert.equal(shortDeadlineAccounting.operations.length, 1);
    assert.equal(shortDeadlineAccounting.submissions.length, 1);
    assert.equal(shortDeadlineAccounting.submissions[0].Mode, "stream");
  }
  result.deadlines = { short: { timeout_ms: shortDeadlineMS, reached_provider: shortDeadlineReachedProvider, ...shortDeadlineAccounting } };
  result.checks.push(shortDeadlineReachedProvider
    ? "short deadline cancels an admitted provider request"
    : "short deadline expires before provider dispatch without inventing a connection");

  // This separate case must actually enter streaming transport. Keep the
  // original observation bound; a missing admission fails that precondition,
  // not the subsequent transport-cancellation assertion.
  const [transportCall, transportResult] = await Promise.all([
    waitFor("provider", providerCalls, call => call.tag === "hold-transport-timeout", "transport timeout request started"),
    call("transport-timeout", { ...input("hold-transport-timeout"), model_alias: "grok", timeout_ms: 2000 }),
  ]);
  assert.equal(transportCall.body.stream, true);
  assert.equal(transportResult.error.code, "deadline_exceeded", JSON.stringify(transportResult));
  await waitFor("closed", closed, tag => tag === "hold-transport-timeout", "provider timeout disconnect");
  result.deadlines.transport = { timeout_ms: 2000, reached_provider: true, disconnected: true, error: transportResult.error };
  result.checks.push("deadline cancels aggregated streaming provider transport");
  const cancelled = rpcRaw("plugin/client/request", params("cancel", { ...input("hold-cancel"), model_alias: "grok" }));
  await waitFor("provider", providerCalls, call => call.tag === "hold-cancel", "cancel request started");
  for (const overrides of [{ id: item.id + "-other" }, { fingerprint: "other" }, { request_id: "other" }]) {
    const invalid = await rpc("plugin/client/cancel", { id: item.id, fingerprint: item.fingerprint,
      graph_generation: item.graph_generation, request_id: "cancel", ...overrides });
    assert.equal(invalid.cancelled, false);
    assert(!closed.includes("hold-cancel"));
  }
  assert((await rpcRaw("plugin/client/request", params("cancel", input("duplicate")))).error);
  assert(!providerCalls.some(call => call.tag === "duplicate"));
  const cancellation = await rpc("plugin/client/cancel", { id: item.id, fingerprint: item.fingerprint,
    graph_generation: item.graph_generation, request_id: "cancel" });
  assert.equal(cancellation.cancelled, true);
  assert((await cancelled).error);
  await waitFor("closed", closed, tag => tag === "hold-cancel", "provider cancel disconnect");
  result.checks.push("cancel identity and duplicate request isolation", "client cancel terminates aggregated streaming provider transport");
  assert.deepEqual(await rpc("thread/list"), before);
  result.checks.push("no persisted hidden conversation");
  const disabled = rpcRaw("plugin/client/request", params("disable", input("hold-disable")));
  await waitFor("provider", providerCalls, call => call.tag === "hold-disable", "disable request started");
  await rpc("extension/package/update", { id: item.id, fingerprint: item.fingerprint, action: "disable" });
  assert((await disabled).error);
  await waitFor("closed", closed, tag => tag === "hold-disable", "provider disable disconnect");
  result.checks.push("disable revokes in-flight inference");
  await stopChild();
  const historyJSON = fixture({ op: "read" });
  const persisted = JSON.parse(historyJSON).sessions;
  result.accounting = { ...journal(), sessions: persisted.length,
    messages: persisted.flatMap(thread => thread.history).length };
  const accounting = result.accounting;
  if (!shortDeadlineReachedProvider) assert(!providerCalls.some(call => call.tag === "hold-timeout"), "expired request reached the provider later");
  assert.equal(accounting.operations.length, 9 + shortDeadlineAccounting.operations.length);
  assert(accounting.operations.every(operation => operation.OwnerID === "plugin:model-fixture" && operation.Kind === "auxiliary" && operation.Status !== "active"));
  assert(accounting.workflows.every(workflow => workflow.Status !== "active"));
  assert.equal(accounting.submissions.length, 9 + shortDeadlineAccounting.submissions.length);
  assert.equal(accounting.submissions.filter(submission => submission.Mode === "stream").length,
    4 + shortDeadlineAccounting.submissions.filter(submission => submission.Mode === "stream").length,
    "Grok and Codex logical unary requests must retain their physical streaming receipts");
  const reported = accounting.submissions.filter(submission => submission.ReportedUsage);
  assert.equal(reported.length, 6);
  assert.equal(reported.reduce((total, usage) => total + usage.ReportedUsage.InputTokens + usage.ReportedUsage.OutputTokens, 0), 60);
  assert.equal(accounting.sessions, 0);
  assert.equal(accounting.messages, 0);
  result.checks.push("durable terminal accounting, reported usage, zero sessions/messages");
  result.status = "passed";
  console.log(`PASS ${result.checks.length} model request checks`);
} catch (error) {
  result.error = error.stack;
  console.error(error.stack);
  process.exitCode = 1;
} finally {
  try { await stopChild(); } catch (error) {
    result.status = "failed";
    result.shutdown_error = error.stack;
    process.exitCode = 1;
  }
  provider.closeAllConnections();
  await new Promise(resolve => provider.close(resolve));
  result.provider_calls = providerCalls.length;
  result.transport_events = transportEvents;
  result.completed_at = new Date().toISOString();
  await writeFile(join(output, "results.json"), JSON.stringify(result, null, 2));
  for (const [name, values] of [["appserver", transcript], ["requests", requests], ["provider", providerCalls]]) {
    await writeFile(join(output, `${name}.jsonl`), values.map(value => JSON.stringify(value)).join("\n") + "\n");
  }
  await writeFile(join(output, "stderr.log"), stderr);
  if (result.accounting) await writeFile(join(output, "journal.json"), JSON.stringify(result.accounting, null, 2));
  console.log(`Evidence: ${output}`);
}
