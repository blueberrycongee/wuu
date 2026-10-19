#!/usr/bin/env node
// Requires Node 22.13+ and bin/wuu built with current bundled plugin assets.
// Run: node scripts/next-message-model-e2e.mjs
// Exercises the real plugin process, Go host/model service and HTTP transport.
// Only the model provider is synthetic; all requests stay on loopback.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { DatabaseSync } from "node:sqlite";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const output = resolve(root, process.env.WUU_NEXT_MESSAGE_E2E_OUTPUT || ".tmp/e2e/next-message-model");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "state");
await mkdir(home);
const events = new EventEmitter(), transcript = [], requests = [], providerCalls = [], closed = [];
const releases = new Map();
const result = { started_at: new Date().toISOString(), scratch, checks: [], status: "failed" };
let child, item, stderr = "", sequence = 0;

function waitFor(event, history, predicate, label, timeout = 45_000) {
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
  const request = { id: `next-message-e2e-${++sequence}`, method, params };
  requests.push(request);
  child.stdin.write(JSON.stringify(request) + "\n");
  return await waitFor("message", transcript, message => message.id === request.id, method);
}

async function rpc(method, params = {}) {
  const message = await rpcRaw(method, params);
  assert(!message.error, `${method}: ${JSON.stringify(message.error)}`);
  return message.result;
}

const clientParams = (method, input = {}, request_id = `client-${sequence + 1}`) => ({
  id: item.id, fingerprint: item.fingerprint, request_id, method, input,
});
const call = async (method, input, requestID) =>
  (await rpc("plugin/client/request", clientParams(method, input, requestID))).result;
const turn = (tag, extra = {}) => ({
  threadId: "fixture-visible-thread", turnId: `fixture-turn-${tag}`,
  turns: [{ id: `fixture-turn-${tag}`, userText: tag, assistantText: "The requested check is complete.",
    imageCount: 0, fileCount: 0, ...extra }],
});
const predict = (tag, extra) => call("next-message.predict", turn(tag, extra));

async function setEnabled(enabled) {
  const updated = await rpc("extension/package/update", {
    id: item.id, fingerprint: item.fingerprint, action: enabled ? "enable" : "disable",
  });
  item = updated.extension_inventory.find(plugin => plugin.id === item.id);
  assert.equal(item.enabled, enabled);
  assert.equal(item.runtime_state, enabled ? "active" : "inactive");
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
  } finally { clearTimeout(timer); }
}

const provider = createServer(async (request, response) => {
  let body = "";
  for await (const chunk of request) body += chunk;
  const value = JSON.parse(body);
  const context = JSON.parse(value.messages.at(-1).content);
  const tag = context.turns.at(-1).user;
  const captured = { tag, at: Date.now(), url: request.url, body: value };
  providerCalls.push(captured);
  response.on("close", () => {
    if (response.writableEnded) return;
    const closure = { tag, at: Date.now() };
    closed.push(closure);
    events.emit("closed", closure);
  });
  events.emit("provider", captured);
  if (tag === "provider-error") {
    response.writeHead(400, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ error: { message: "Synthetic permanent fixture error" } }));
    return;
  }
  if (tag === "grok-stream" || tag === "grok-high" || tag === "grok-default") {
    assert.equal(value.stream, true, "Grok suggestion request must use the proxy's streaming transport");
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { content: '{"suggestion":"Run the focused checks"}' }, finish_reason: "stop" }],
      usage: { prompt_tokens: 7, completion_tokens: 3, total_tokens: 10 } })}\n\ndata: [DONE]\n\n`);
    return;
  }
  response.writeHead(200, { "Content-Type": "application/json" });
  if (tag.startsWith("hold-")) {
    response.flushHeaders();
    return;
  }
  if (tag.startsWith("slow-success")) await new Promise(resolve => setTimeout(resolve, 22_000));
  if (tag.startsWith("gated-")) await new Promise(resolve => { releases.set(tag, resolve); });
  const text = tag === "ambiguous" ? '{"suggestion":null}'
    : tag === "malformed" ? '{"suggestion":"first","suggestion":"second"}'
      : tag === "too-long" ? JSON.stringify({ suggestion: "x".repeat(101) })
        : tag === "length-boundary" ? JSON.stringify({ suggestion: "x".repeat(100) })
      : JSON.stringify({ suggestion: "Run the focused checks" });
  response.end(JSON.stringify({
    id: "fixture-response", object: "chat.completion", model: value.model,
    choices: [{ index: 0, message: { role: "assistant", content: text }, finish_reason: tag === "truncated" ? "length" : "stop" }],
    usage: { prompt_tokens: 7, completion_tokens: 3, total_tokens: 10 },
  }));
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));

// Never inherit real credentials, provider overrides or the user's Wuu state.
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath,
  ...(process.env.GOMAXPROCS ? { GOMAXPROCS: process.env.GOMAXPROCS } : {}),
};

try {
  result.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  result.runtime_sha256 = createHash("sha256").update(await readFile(join(root, "internal/plugin/bundled/next-message/runtime.mjs"))).digest("hex");
  const configPath = join(home, "config.json");
  await writeFile(configPath, JSON.stringify({
    default_provider: "fixture",
    providers: {
      fixture: {
        type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}`,
        api_key: "synthetic-no-real-key", model: "gpt-test",
        models: { "gpt-test": { options: {
          max_tokens: 128000, max_output_tokens: 128000, max_completion_tokens: 128000,
          tools: [{ type: "web_search_preview" }], stream: true, store: true,
        } } },
      },
      grok: { type: "grok-build", base_url: `http://127.0.0.1:${provider.address().port}`,
        api_key: "synthetic-no-real-key", model: "grok-4.6" },
    },
    agent: { permission_mode: "standard", model_aliases: {
      short: { provider: "fixture", model: "gpt-test-short" },
    } },
    skills: { enabled: false }, ptc: { enabled: false },
  }));
  child = spawn(binary, ["app-server", "--workdir", scratch, "--config", configPath], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  createInterface({ input: child.stdout }).on("line", line => {
    try {
      const message = JSON.parse(line);
      transcript.push(message);
      events.emit("message", message);
    } catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const initialized = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "next-message-model-e2e" } });
  item = initialized.extension_inventory.find(plugin => plugin.id.endsWith(":next-message"));
  assert(item, `bundled next-message package is missing from the built core: ${JSON.stringify(initialized.extension_inventory.map(plugin => plugin.id))}; ${stderr}`);
  assert.equal(item.enabled, false);
  assert.equal(item.runtime_state, "inactive");
  result.default_plugin = item;
  assert((await rpcRaw("plugin/client/request", clientParams("next-message.predict", turn("disabled")))).error);
  assert.equal(providerCalls.length, 0, "disabled plugin made an inference request");
  result.checks.push("default-off package rejects client prediction without inference");

  await setEnabled(true);
  assert.deepEqual(await call("next-message.get"), { settings: {} });
  assert.deepEqual(await predict("unconfigured"), { suggestion: null });
  assert.equal(providerCalls.length, 0, "unconfigured plugin chose a model");
  assert((await rpcRaw("plugin/client/request", clientParams("next-message.update", {}))).error);
  assert.deepEqual(await call("next-message.update", { provider: "fixture", model: "gpt-test" }), { settings: { provider: "fixture", model: "gpt-test" } });
  result.checks.push("unconfigured plugin makes no model request and rejects an empty model selection");
  const beforeThreads = await rpc("thread/list");
  assert.deepEqual(await predict("success"), { suggestion: "Run the focused checks" });
  const first = providerCalls.at(-1);
  assert.equal(first.url, "/chat/completions");
  assert.equal(first.body.model, "gpt-test");
  assert.equal(first.body.max_tokens, 128);
  assert.equal(first.body.max_output_tokens, undefined);
  assert.equal(first.body.max_completion_tokens, undefined);
  assert(!first.body.tools?.length);
  assert(!first.body.stream);
  assert(!first.body.store);
  assert.deepEqual(first.body.messages.map(message => message.role), ["system", "user"]);
  assert.deepEqual(JSON.parse(first.body.messages[1].content), {
    turns: [{ user: "success", assistant: turn("success").turns[0].assistantText }],
  });
  assert(!JSON.stringify(first.body).includes("fixture-visible-thread"));
  assert(!JSON.stringify(first.body).includes("fixture-turn-success"));
  assert.equal(providerCalls.length, 1);
  assert.deepEqual(await predict("success"), { suggestion: null });
  assert.equal(providerCalls.length, 1, "same completed turn submitted twice");
  result.checks.push("real plugin uses its selected provider and model, explicit visible text, no identifiers/history/tools, and 128-token wire cap", "one inference per completed turn");

  await call("next-message.update", { provider: "grok", model: "grok-4.6" });
  assert.deepEqual(await predict("grok-stream"), { suggestion: "Run the focused checks" });
  assert.equal(providerCalls.at(-1).body.stream, true);
  assert.equal(providerCalls.at(-1).body.model, "grok-4.6");
  assert.equal(providerCalls.at(-1).body.reasoning_effort, "low");
  assert.deepEqual(await call("next-message.update", { provider: "grok", model: "grok-4.6", variant: "high" }),
    { settings: { provider: "grok", model: "grok-4.6", variant: "high" } });
  await setEnabled(false);
  await setEnabled(true);
  assert.deepEqual(await call("next-message.get"), { settings: { provider: "grok", model: "grok-4.6", variant: "high" } });
  assert.deepEqual(await predict("grok-high"), { suggestion: "Run the focused checks" });
  assert.equal(providerCalls.at(-1).body.reasoning_effort, "high", "selected tier was not used on the wire");
  assert.deepEqual(await call("next-message.update", { provider: "grok", model: "grok-4.6", variant: "" }),
    { settings: { provider: "grok", model: "grok-4.6", variant: "" } });
  assert.deepEqual(await predict("grok-default"), { suggestion: "Run the focused checks" });
  assert.equal(providerCalls.at(-1).body.reasoning_effort, "high", "model default silently kept the legacy low preference");
  const beforeInvalidVariant = providerCalls.length;
  assert.deepEqual(await call("next-message.update", { provider: "grok", model: "grok-4.6", variant: "unsupported-tier" }),
    { settings: { provider: "grok", model: "grok-4.6", variant: "unsupported-tier" } });
  assert.deepEqual(await predict("grok-unsupported"), { suggestion: null });
  assert.equal(providerCalls.length, beforeInvalidVariant, "unsupported tier silently used the default model tier");
  await call("next-message.update", { provider: "fixture", model: "gpt-test" });
  result.checks.push("Grok suggestion streams with legacy low tier, persists explicit high tier, honors model default, and rejects an unavailable tier");

  const promptBytes = Buffer.byteLength(first.body.messages[0].content);
  const contextOverhead = Buffer.byteLength(JSON.stringify({ turns: [{ user: "x", assistant: "a" }] }));
  const boundaryText = "x".repeat(8192 - promptBytes - contextOverhead + 1);
  assert.deepEqual(await predict("byte-boundary", { userText: boundaryText, assistantText: "a" }), { suggestion: "Run the focused checks" });
  assert.equal(providerCalls.at(-1).body.messages.reduce((sum, message) => sum + Buffer.byteLength(message.content), 0), 8192);
  const beforeInvalid = providerCalls.length;
  for (const input of [
    turn("over-budget", { userText: boundaryText + "x", assistantText: "a" }),
    turn("unicode-over-budget", { userText: "🙂".repeat(2049) }),
    { ...turn("hidden-context"), history: [{ role: "user", content: "hidden" }] },
  ]) assert((await rpcRaw("plugin/client/request", clientParams("next-message.predict", input))).error);
  assert.equal(providerCalls.length, beforeInvalid, "invalid input crossed the model service boundary");
  result.checks.push("8192-byte input accepted exactly; larger UTF-8/context and hidden-field requests rejected before inference");

  assert.deepEqual(await call("next-message.update", { model_alias: "short" }), { settings: { model_alias: "short" } });
  assert.deepEqual(await predict("alias"), { suggestion: "Run the focused checks" });
  assert.equal(providerCalls.at(-1).body.model, "gpt-test-short");
  assert.equal(providerCalls.at(-1).body.max_tokens, 128);
  await setEnabled(false);
  await setEnabled(true);
  assert.deepEqual(await call("next-message.get"), { settings: { model_alias: "short" } });
  await call("next-message.update", { model_alias: "missing-alias" });
  const beforeMissingAlias = providerCalls.length;
  assert.deepEqual(await predict("missing-alias"), { suggestion: null });
  assert.equal(providerCalls.length, beforeMissingAlias, "unknown alias silently fell back to another model");
  await call("next-message.update", { provider: "fixture", model: "gpt-test" });
  result.checks.push("configured alias reaches provider and persists across plugin restart; unknown alias abstains without fallback");

  assert.deepEqual(await predict("length-boundary"), { suggestion: "x".repeat(100) });
  for (const tag of ["ambiguous", "malformed", "too-long", "truncated", "provider-error"]) {
    const count = providerCalls.length;
    assert.deepEqual(await predict(tag), { suggestion: null });
    assert.equal(providerCalls.length, count + 1);
    assert.deepEqual(await predict(tag), { suggestion: null });
    assert.equal(providerCalls.length, count + 1, `${tag} retried inference`);
  }
  result.checks.push("100-character boundary accepted; longer, null, malformed, truncated and provider errors abstain without retry");

  const parallelStarted = Date.now();
  const parallel = Promise.all(["one", "two"].map(id => call("next-message.predict", {
    ...turn(`slow-success-${id}`), threadId: `parallel-thread-${id}`,
  })));
  assert.deepEqual(await parallel, Array.from({ length: 2 }, () => ({ suggestion: "Run the focused checks" })));
  result.parallel_transport_ms = Date.now() - parallelStarted;
  assert(result.parallel_transport_ms >= 21000 && result.parallel_transport_ms < 30000,
    `independent 22-second predictions were serialized: ${result.parallel_transport_ms}ms`);
  assert.deepEqual(await call("next-message.get"), { settings: { provider: "fixture", model: "gpt-test" } });
  result.checks.push("two independent 22-second predictions overlap and leave the plugin healthy");

  const timed = predict("hold-timeout");
  const timeoutCall = await waitFor("provider", providerCalls, call => call.tag === "hold-timeout", "timeout request started");
  // Stagger the independent execution so its own 30-second budget remains
  // live when the first deadline fires; release it only after that timeout.
  await new Promise(resolve => setTimeout(resolve, 5000));
  const timeoutSurvivor = call("next-message.predict", {
    ...turn("gated-timeout-survivor"), threadId: "timeout-survivor-thread",
  });
  await waitFor("provider", providerCalls, call => call.tag === "gated-timeout-survivor", "timeout survivor started");
  assert.deepEqual(await timed, { suggestion: null });
  const timeoutClosed = await waitFor("closed", closed, entry => entry.tag === "hold-timeout", "timeout disconnect");
  result.timeout_transport_ms = timeoutClosed.at - timeoutCall.at;
  assert(result.timeout_transport_ms >= 29000 && result.timeout_transport_ms < 34000, `unexpected 30-second budget: ${result.timeout_transport_ms}ms`);
  assert(!closed.some(entry => entry.tag === "gated-timeout-survivor"), "one deadline cancelled another thread's prediction");
  releases.get("gated-timeout-survivor")();
  assert.deepEqual(await timeoutSurvivor, { suggestion: "Run the focused checks" });
  const afterTimeout = providerCalls.length;
  assert.deepEqual(await predict("hold-timeout"), { suggestion: null });
  assert.equal(providerCalls.length, afterTimeout);
  result.checks.push("30-second request budget disconnects only its own HTTP transport without retry or plugin retirement");

  const cancelledParams = clientParams("next-message.predict", turn("hold-cancel"), "cancel-prediction");
  const cancelled = rpcRaw("plugin/client/request", cancelledParams);
  await waitFor("provider", providerCalls, call => call.tag === "hold-cancel", "cancel request started");
  const duringCancel = providerCalls.length;
  const duplicate = predict("hold-cancel");
  const cancelSurvivor = call("next-message.predict", {
    ...turn("gated-cancel-survivor"), threadId: "cancel-survivor-thread",
  });
  await waitFor("provider", providerCalls, call => call.tag === "gated-cancel-survivor", "cancel survivor started");
  const identity = { id: item.id, fingerprint: item.fingerprint, request_id: cancelledParams.request_id };
  assert.equal((await rpc("plugin/client/cancel", { ...identity, request_id: "unrelated" })).cancelled, false);
  assert(!closed.some(entry => entry.tag === "hold-cancel"));
  const cancelStarted = Date.now();
  assert.equal((await rpc("plugin/client/cancel", identity)).cancelled, true);
  assert((await cancelled).error, "cancelled execution was reported as a successful abstention");
  const cancelClosed = await waitFor("closed", closed, entry => entry.tag === "hold-cancel", "cancel disconnect");
  result.abort_transport_ms = cancelClosed.at - cancelStarted;
  assert(result.abort_transport_ms < 2500, `abort waited for the request deadline: ${result.abort_transport_ms}ms`);
  assert(!closed.some(entry => entry.tag === "gated-cancel-survivor"), "client cancel aborted another thread's prediction");
  releases.get("gated-cancel-survivor")();
  assert.deepEqual(await cancelSurvivor, { suggestion: "Run the focused checks" });
  assert.deepEqual(await duplicate, { suggestion: null });
  assert.equal(providerCalls.length, duringCancel + 1, "concurrent duplicate started a second inference");
  const afterCancel = providerCalls.length;
  assert.deepEqual(await predict("hold-cancel"), { suggestion: null });
  assert.equal(providerCalls.length, afterCancel);
  result.checks.push("client abort disconnects only its execution, preserves another prediction and retains deduplication");

  const disabled = rpcRaw("plugin/client/request", clientParams("next-message.predict", turn("hold-disable")));
  await waitFor("provider", providerCalls, call => call.tag === "hold-disable", "disable request started");
  const disableStarted = Date.now();
  await setEnabled(false);
  assert((await disabled).error);
  const disableClosed = await waitFor("closed", closed, entry => entry.tag === "hold-disable", "disable disconnect");
  result.disable_transport_ms = disableClosed.at - disableStarted;
  assert(result.disable_transport_ms < 2500, `disable waited for the request deadline: ${result.disable_transport_ms}ms`);
  assert.deepEqual((await rpc("plugin/executions/list")).executions, []);
  assert.deepEqual(await rpc("thread/list"), beforeThreads);
  result.checks.push("package disable revokes in-flight inference and leaves no live execution or hidden thread");

  await stopChild();
  const db = new DatabaseSync(join(home, "sessions/sessions.sqlite3"), { readOnly: true });
  try {
    result.accounting = {
      operations: db.prepare("SELECT owner_id, kind, status FROM inference_operations ORDER BY created_at").all(),
      submissions: db.prepare("SELECT cost_state, reported_input_tokens, reported_output_tokens, has_reported_usage FROM inference_submissions ORDER BY started_at").all(),
      workflows: db.prepare("SELECT owner_id, status FROM inference_workflows ORDER BY created_at").all(),
      sessions: db.prepare("SELECT count(*) AS count FROM sessions").get().count,
      messages: db.prepare("SELECT count(*) AS count FROM session_messages").get().count,
    };
  } finally { db.close(); }
  const accounting = result.accounting;
  assert.equal(accounting.operations.length, providerCalls.length);
  assert.equal(accounting.submissions.length, providerCalls.length);
  assert(accounting.operations.every(operation => operation.owner_id === "plugin:next-message" && operation.kind === "auxiliary" && operation.status !== "active"));
  assert(accounting.workflows.every(workflow => workflow.status !== "active"));
  const reported = accounting.submissions.filter(submission => submission.has_reported_usage);
  assert.equal(reported.length, 15);
  assert.equal(reported.reduce((total, usage) => total + usage.reported_input_tokens + usage.reported_output_tokens, 0), 150);
  assert.equal(accounting.sessions, 0);
  assert.equal(accounting.messages, 0);
  result.checks.push("durable plugin-owned terminal accounting and reported usage; zero sessions/messages persisted");
  result.status = "passed";
  console.log(`PASS ${result.checks.length} next-message real model-service checks`);
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
  result.completed_at = new Date().toISOString();
  await writeFile(join(output, "results.json"), JSON.stringify(result, null, 2));
  for (const [name, values] of [["appserver", transcript], ["requests", requests], ["provider", providerCalls], ["transport-close", closed]]) {
    await writeFile(join(output, `${name}.jsonl`), values.map(value => JSON.stringify(value)).join("\n") + "\n");
  }
  await writeFile(join(output, "stderr.log"), stderr);
  if (result.accounting) await writeFile(join(output, "journal.json"), JSON.stringify(result.accounting, null, 2));
  console.log(`Evidence: ${output}`);
}
