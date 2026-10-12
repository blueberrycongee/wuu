#!/usr/bin/env node
// Requires Node 22.13+, a built packages/plugin-sdk, and bin/wuu.
// Run: node scripts/plugin-model-request-e2e.mjs
// Uses disposable state, the public SDK, and a loopback-only synthetic provider.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { cp, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { DatabaseSync } from "node:sqlite";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const output = resolve(root, process.env.WUU_MODEL_E2E_OUTPUT || ".tmp/e2e/plugin-model-request");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "state"), pkg = join(scratch, "plugin");
await mkdir(home);
await mkdir(pkg);
const events = new EventEmitter(), transcript = [], requests = [], providerCalls = [], closed = [];
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
  events.emit("provider", call);
  response.on("close", () => {
    if (response.writableEnded) return;
    closed.push(tag);
    events.emit("closed", tag);
  });
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
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };

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
import { runJSONLRuntime, requestModel, requireModelRequestService } from "./sdk/index.js";
runJSONLRuntime({
  initialize() {
    return {
      protocol_version: 3,
      host_services: [{ id: "host.service.call", required: true }],
      required_services: [requireModelRequestService()],
      capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1 }],
    };
  },
  async invokeCapability(params, host) {
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
    stream: true, stream_options: { include_usage: true }, background: true, n: 2, best_of: 2,
    modalities: ["text", "audio"], audio: { voice: "alloy", format: "wav" }, store: true, omitStore: true,
    maxOutputTokens: 128000, max_output_tokens: 128000, max_tokens: 128000, max_completion_tokens: 128000,
  };
  const fixtureConfig = (type, model, wire_api) => ({
    type, wire_api, base_url: `http://127.0.0.1:${provider.address().port}`,
    api_key: "synthetic-no-real-key", model,
    models: { [model]: { options: {
      ...shapeOverrides, temperature: 0.42, topP: 0.83, seed: 42,
      ...(type === "anthropic" ? { thinking: { type: "enabled", budgetTokens: 16 }, effort: "low", topK: 17 }
        : { reasoningEffort: "low", reasoningSummary: "auto", textVerbosity: "low",
          ...(wire_api === "responses" ? { text: { format: { type: "text" } } } : { response_format: { type: "text" } }) }),
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
      type: "openai-codex", base_url: `http://127.0.0.1:${provider.address().port}`,
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
  const item = initialized.extension_inventory.find(plugin => plugin.id.endsWith(":model-fixture"));
  assert(item, "published fixture is missing");
  assert.equal(item.runtime_state, "active", JSON.stringify(item));
  result.plugin = item;
  const registry = await rpc("plugin/registry/introspect");
  assert(registry.services.some(service => service.service === "host.model.request" && service.version === "1.0.0" && service.kernel));
  result.checks.push("versioned service registration");
  const before = await rpc("thread/list");
  const input = tag => ({ messages: [{ role: "system", content: "Return fixture data." }, { role: "user", content: tag }], max_output_tokens: 32 });
  const params = (request_id, value) => ({ id: item.id, fingerprint: item.fingerprint, request_id, method: "model", input: value });
  const call = async (requestID, value) => (await rpc("plugin/client/request", params(requestID, value))).result;
  const success = await call("success", input("success"));
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
    const ownedFields = new Set(["model", "messages", "input", "instructions", "system", "stream", "store", "max_tokens", "max_output_tokens"]);
    for (const key of Object.keys(shapeOverrides)) {
      if (!ownedFields.has(key)) assert.equal(body[key], undefined, `${name}: configured ${key} crossed the request boundary`);
    }
    assert(!body.stream, `${name}: configured streaming was enabled`);
    assert(!body.store, `${name}: configured server-side storage was enabled`);
    assert.equal(body.temperature, 0.42, `${name}: sampling option was lost`);
    assert.equal(body.top_p, 0.83, `${name}: sampling alias was lost`);
    if (wire === "responses") {
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
  assert.equal(providerCalls.length, count, "invalid requests reached the provider");
  const timedOut = await call("timeout", { ...input("hold-timeout"), timeout_ms: 200 });
  assert.equal(timedOut.error.code, "deadline_exceeded", JSON.stringify(timedOut));
  await waitFor("closed", closed, tag => tag === "hold-timeout", "provider timeout disconnect");
  result.checks.push("deadline cancels provider transport");
  const cancelled = rpcRaw("plugin/client/request", params("cancel", input("hold-cancel")));
  await waitFor("provider", providerCalls, call => call.tag === "hold-cancel", "cancel request started");
  for (const overrides of [{ id: item.id + "-other" }, { fingerprint: "other" }, { request_id: "other" }]) {
    const invalid = await rpc("plugin/client/cancel", { id: item.id, fingerprint: item.fingerprint, request_id: "cancel", ...overrides });
    assert.equal(invalid.cancelled, false);
    assert(!closed.includes("hold-cancel"));
  }
  assert((await rpcRaw("plugin/client/request", params("cancel", input("duplicate")))).error);
  assert(!providerCalls.some(call => call.tag === "duplicate"));
  const cancellation = await rpc("plugin/client/cancel", { id: item.id, fingerprint: item.fingerprint, request_id: "cancel" });
  assert.equal(cancellation.cancelled, true);
  assert((await cancelled).error);
  await waitFor("closed", closed, tag => tag === "hold-cancel", "provider cancel disconnect");
  result.checks.push("cancel identity and duplicate request isolation", "client cancel terminates provider transport");
  assert.deepEqual(await rpc("thread/list"), before);
  result.checks.push("no persisted hidden conversation");
  const disabled = rpcRaw("plugin/client/request", params("disable", input("hold-disable")));
  await waitFor("provider", providerCalls, call => call.tag === "hold-disable", "disable request started");
  await rpc("extension/package/update", { id: item.id, fingerprint: item.fingerprint, action: "disable" });
  assert((await disabled).error);
  await waitFor("closed", closed, tag => tag === "hold-disable", "provider disable disconnect");
  result.checks.push("disable revokes in-flight inference");
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
  assert.equal(accounting.operations.length, 8);
  assert(accounting.operations.every(operation => operation.owner_id === "plugin:model-fixture" && operation.kind === "auxiliary" && operation.status !== "active"));
  assert(accounting.workflows.every(workflow => workflow.status !== "active"));
  assert.equal(accounting.submissions.length, 8);
  const reported = accounting.submissions.filter(submission => submission.has_reported_usage);
  assert.equal(reported.length, 5);
  assert.equal(reported.reduce((total, usage) => total + usage.reported_input_tokens + usage.reported_output_tokens, 0), 50);
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
  result.completed_at = new Date().toISOString();
  await writeFile(join(output, "results.json"), JSON.stringify(result, null, 2));
  for (const [name, values] of [["appserver", transcript], ["requests", requests], ["provider", providerCalls]]) {
    await writeFile(join(output, `${name}.jsonl`), values.map(value => JSON.stringify(value)).join("\n") + "\n");
  }
  await writeFile(join(output, "stderr.log"), stderr);
  if (result.accounting) await writeFile(join(output, "journal.json"), JSON.stringify(result.accounting, null, 2));
  console.log(`Evidence: ${output}`);
}
