#!/usr/bin/env node
// Real app-server and single-file TypeScript extension acceptance. No real keys
// or remote providers: all inference is synthetic and bound to 127.0.0.1.
// Requires Node 22.13+ and a freshly built bin/wuu (embeds the public SDK).
// Run: WUU_E2E_CORE=.tmp/bin/wuu node scripts/plugin-model-pipeline-e2e.mjs
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
const output = resolve(root, process.env.WUU_PIPELINE_E2E_OUTPUT || ".tmp/e2e/plugin-model-pipeline");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "state");
await mkdir(home);
const events = new EventEmitter();
const privateProviderMarker = "fixture-private-provider-body-sentinel";
const transcript = [], requests = [], providerCalls = [], closed = [];
const result = { started_at: new Date().toISOString(), scratch, checks: [], status: "failed",
  limitations: ["Synthetic provider: wire equality and lifecycle evidence only; no real prompt-cache hit or cost saving is claimed."] };
let child, stderr = "", sequence = 0;

function waitFor(event, history, predicate, label, timeout = 20_000) {
  const previous = history.find(predicate);
  if (previous !== undefined) return Promise.resolve(previous);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off(event, listener); reject(new Error(`Timed out: ${label}`)); }, timeout);
    const listener = value => {
      if (!predicate(value)) return;
      clearTimeout(timer); events.off(event, listener); resolve(value);
    };
    events.on(event, listener);
  });
}

async function rpcRaw(method, params = {}) {
  const request = { id: `pipeline-e2e-${++sequence}`, method, params };
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
    assert.equal(signal, null, "app-server did not stop cleanly after EOF");
    assert.equal(code, 0, stderr);
  } finally { clearTimeout(timer); }
}
function textContent(content) {
  return typeof content === "string" ? content : content?.map(part => part.text ?? "").join("") ?? "";
}
function sendEvent(response, type, fields = {}) {
  response.write(`event: ${type}\ndata: ${JSON.stringify({ type, ...fields })}\n\n`);
}

const provider = createServer(async (request, response) => {
  try {
    let body = "";
    for await (const chunk of request) body += chunk;
    const value = JSON.parse(body);
    const tagged = [...(value.messages ?? [])].reverse().map(message => textContent(message.content)).find(text => text.startsWith("pipeline-"));
    // Anthropic merges adjacent user messages after an unsuccessful turn.
    const tail = tagged?.split("pipeline-").at(-1)?.match(/^[a-z-]+/)?.[0];
    const tag = tail ? `pipeline-${tail}` : "auxiliary";
    const replay = value.max_tokens <= 64;
    const call = { index: providerCalls.length, tag, replay, url: request.url, body: value };
    providerCalls.push(call); events.emit("provider", call);
    if (["pipeline-retry", "pipeline-private-retry"].includes(tag) && providerCalls.filter(entry => entry.tag === tag).length === 1) {
      call.status = 503;
      response.writeHead(503, { "Content-Type": "application/json", "Retry-After": "0" });
      response.end(JSON.stringify({ type: "error", error: { type: "overloaded_error", message: privateProviderMarker } }));
      return;
    }
    response.on("close", () => {
      if (response.writableEnded) return;
      closed.push(call); events.emit("closed", call);
    });
    if (replay && tag.includes("hold")) {
      response.writeHead(200, { "Content-Type": "text/event-stream" });
      response.flushHeaders();
      if (tag.includes("break")) {
        sendEvent(response, "message_start", { message: { usage: { input_tokens: 11, output_tokens: 0 } } });
        sendEvent(response, "content_block_start", { index: 0, content_block: { type: "text" } });
        sendEvent(response, "content_block_delta", { index: 0, delta: { type: "text_delta", text: "fixture:first-chunk" } });
      }
      return;
    }
    const tripwire = value.tools?.find(tool => tool.name.includes("tripwire"));
    const rawTool = value.tools?.find(tool => tool.name.includes("raw_result"));
    const rawReply = !replay && tag === "pipeline-raw-json" && !value.messages.some(message => Array.isArray(message.content) && message.content.some(block => block.type === "tool_result" && block.tool_use_id === "fixture-raw-json-tool"));
    const toolReply = rawReply ? rawTool : replay && tag.includes("context") && tripwire;
    const text = `fixture:${tag}:${replay ? "replay" : "normal"}`;
    const content = toolReply
      ? [{ type: "tool_use", id: rawReply ? "fixture-raw-json-tool" : "fixture-unexecuted-tool", name: toolReply.name, input: {} }]
      : [{ type: "thinking", thinking: "Synthetic native context.", signature: "fixture-native-signature" }, { type: "text", text }];
    const stopReason = toolReply ? "tool_use" : "end_turn";
    if (!value.stream) {
      response.writeHead(200, { "Content-Type": "application/json" });
      response.end(JSON.stringify({ id: "fixture-response", type: "message", role: "assistant", model: value.model,
        content, stop_reason: stopReason, usage: { input_tokens: 11, output_tokens: 5 } }));
      return;
    }
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    sendEvent(response, "message_start", { message: { id: "fixture-response", type: "message", role: "assistant", model: value.model, content: [], usage: { input_tokens: 11, output_tokens: 0 } } });
    if (tag === "pipeline-physical-failure") {
      sendEvent(response, "message_delta", { delta: {}, usage: { output_tokens: 5 } });
      sendEvent(response, "error", { error: { type: "invalid_request_error", message: privateProviderMarker } });
      response.end(); return;
    }
    if (replay && tag.includes("error")) {
      sendEvent(response, "error", { error: { type: "invalid_request_error", message: "fixture-stream-failure" } });
      response.end(); return;
    }
    for (const [index, block] of content.entries()) {
      sendEvent(response, "content_block_start", { index, content_block: block.type === "tool_use" ? { ...block, input: {} } : { type: block.type } });
      if (block.type === "thinking") {
        sendEvent(response, "content_block_delta", { index, delta: { type: "thinking_delta", thinking: block.thinking } });
        sendEvent(response, "content_block_delta", { index, delta: { type: "signature_delta", signature: block.signature } });
      } else if (block.type === "text") {
        sendEvent(response, "content_block_delta", { index, delta: { type: "text_delta", text: block.text } });
      } else {
        sendEvent(response, "content_block_delta", { index, delta: { type: "input_json_delta", partial_json: "{}" } });
      }
      sendEvent(response, "content_block_stop", { index });
    }
    sendEvent(response, "message_delta", { delta: { stop_reason: stopReason }, usage: { output_tokens: 5 } });
    sendEvent(response, "message_stop");
    response.end();
  } catch (error) { response.writeHead(500); response.end(String(error)); }
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));
// Deliberately exclude real home, keys, provider overrides and user state.
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
const configPath = join(home, "config.json");

async function publish(source) {
  const published = spawnSync(binary, ["plugin", "dev", "--watch=false", source], { env, encoding: "utf8", timeout: 20_000 });
  result.publications ??= [];
  result.publications.push({ source, status: published.status, stdout: published.stdout, stderr: published.stderr });
  assert.equal(published.status, 0, published.stderr);
}
const fixtures = {};
async function client(name, method, input = {}) {
  const item = fixtures[name];
  return (await rpc("plugin/client/request", { id: item.id, fingerprint: item.fingerprint,
    request_id: `${name}-${++sequence}`, method, input })).result;
}
function adapterSubmissions() {
  const db = new DatabaseSync(join(home, "sessions/sessions.sqlite3"), { readOnly: true });
  try {
    return db.prepare("SELECT operation_id FROM inference_submissions WHERE protocol = ? ORDER BY started_at, rowid").all("plugin:fixture.shadow");
  } finally { db.close(); }
}
async function terminalJob(name, jobID) {
  const deadline = Date.now() + 20_000;
  let last;
  while (Date.now() < deadline) {
    last = await client(name, "inspect", { job_id: jobID });
    if (last.state !== "running") return last;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`Background job did not terminate: ${JSON.stringify(last)}`);
}
async function fixtureState(name, predicate, label) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const state = await client(name, "state");
    if (predicate(state)) return state;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`Timed out: ${label}`);
}
async function normalTurn(threadID, prompt) {
  const { turn } = await rpc("turn/start", { thread_id: threadID, prompt });
  const complete = await waitFor("message", transcript, message =>
    message.method === "turn/completed" && message.params?.turn?.id === turn.id, `turn ${turn.id}`);
  assert.equal(complete.params.turn.status, "completed", JSON.stringify(complete));
  return turn;
}

try {
  result.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  result.checkout = { revision: spawnSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).stdout.trim(), sources: {} };
  for (const path of ["scripts/plugin-model-pipeline-e2e.mjs", "scripts/fixtures/model-pipeline/replay.ts", "scripts/fixtures/model-pipeline/shadow.ts"]) {
    result.checkout.sources[path] = createHash("sha256").update(await readFile(join(root, path))).digest("hex");
  }
  await writeFile(configPath, JSON.stringify({
    default_provider: "fixture",
    providers: { fixture: { type: "anthropic", base_url: `http://127.0.0.1:${provider.address().port}`,
      api_key: "synthetic-no-real-key", model: "claude-fixture", models: {
        "claude-fixture": { options: { temperature: 0.42, thinking: { type: "enabled", budgetTokens: 16 }, anthropicCache: "auto" } },
        "claude-fixture-next": { options: { temperature: 0.42 } },
        "claude-fixture-alias": { options: { adapter: "fixture.shadow", temperature: 0.61,
          fixture_alias_default: "configured", maxOutputTokens: 4096 },
          variants: { high: { effort: "high" }, low: { effort: "low" } } },
      } } },
    agent: { permission_mode: "standard", model_aliases: {
      comparison: { provider: "fixture", model: "claude-fixture-alias", effort: "high" },
    } }, skills: { enabled: false }, ptc: { enabled: false },
  }));
  for (const name of ["replay", "shadow"]) {
    const path = join(scratch, `${name}.ts`);
    await cp(join(root, `scripts/fixtures/model-pipeline/${name}.ts`), path);
    await publish(path);
  }
  child = spawn(binary, ["app-server", "--workdir", scratch, "--config", configPath], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const message = JSON.parse(line); transcript.push(message); events.emit("message", message); }
    catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const initialized = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "model-pipeline-e2e" } });
  for (const name of ["replay", "shadow"]) {
    const item = initialized.extension_inventory.find(plugin => plugin.name === name);
    assert(item, `${name} single-file fixture missing`);
    assert.equal(item.runtime_state, "active", JSON.stringify(item));
    fixtures[name] = item;
  }
  result.plugins = fixtures;
  result.checks.push("two manifest-free TypeScript extensions loaded with public SDK only");
  // The remaining journeys use only public app-server and extension contracts.
  const registry = await rpc("plugin/registry/introspect");
  for (const service of ["host.model.invoke", "host.model.emit", "host.background"]) {
    assert(registry.services.some(entry => entry.service === service && entry.version === "1.0.0" && entry.kernel), service);
  }
  result.checks.push("versioned invocation, streaming and background services registered");
  const { thread } = await rpc("thread/start", { model: "claude-fixture", provider: "fixture" });
  let normalTurns = 0;
  const turn = async tag => { await normalTurn(thread.id, tag); normalTurns++; };
  await turn("pipeline-seed");
  await turn("pipeline-raw-json");
  await client("replay", "arm", { tag: "pipeline-context" });
  await turn("pipeline-context");
  const observed = await client("replay", "state");
  const snapshot = observed.snapshots.at(-1).request;
  assert.equal(snapshot.version, 1);
  assert.equal(snapshot.provider, "fixture");
  assert.equal(snapshot.model, "claude-fixture");
  assert(snapshot.messages.some(message => message.reasoning_blocks?.some(block => block.signature === "fixture-native-signature")), "native thinking/signature missing from full request");
  assert(snapshot.messages.some(message => message.role === "system"), "system context missing");
  assert(snapshot.messages.some(message => message.tool_result?.structured_content?.score === 1), "structured tool-result context missing");
  const logicalTool = snapshot.tools.find(tool => tool.name.includes("tripwire"));
  assert(logicalTool, "full tool schema missing");
  assert.deepEqual(logicalTool.input_schema.properties.anything, {}, "empty unconstrained schema became null");
  assert.deepEqual(logicalTool.input_schema.properties.nested.properties, {}, "empty nested properties became null");
  assert(snapshot.cache_hint?.prompt_cache_key, "cache identity missing from full request");
  assert.equal(snapshot.provider_options.temperature, 0.42);
  const replayJob = observed.jobs.at(-1);
  assert(replayJob, "response observer did not schedule replay");
  const replay = await terminalJob("replay", replayJob.job_id);
  assert.equal(replay.state, "completed", JSON.stringify(replay));
  assert(!replay.result.error, JSON.stringify(replay));
  assert.equal(replay.result.result.origin.kind, "plugin");
  assert(replay.result.result.origin.plugin_id, "plugin origin missing");
  assert.equal(replay.result.result.response.usage.input_tokens, 11);
  assert.equal(replay.result.result.response.usage.output_tokens, 5);
  assert.equal(replay.result.result.response.finish_reason, "tool_calls");
  assert.equal(replay.result.result.response.tool_calls[0].id, "fixture-unexecuted-tool");
  assert.equal(replay.result.events.filter(event => event.type === "done").length, 1);
  assert.equal(replay.result.events.at(-1).type, "done");
  const native = providerCalls.find(call => !call.replay && call.tag === "pipeline-context");
  const copy = providerCalls.find(call => call.replay && call.tag === "pipeline-context");
  assert(native && copy, "original/replay wire requests missing");
  const contextWire = body => Object.fromEntries(Object.entries(body).filter(([key]) => !["max_tokens", "stream"].includes(key)));
  assert.deepEqual(contextWire(copy.body), contextWire(native.body), "replay changed provider input beyond cap/stream mode");
  assert.equal(copy.body.max_tokens, 31);
  assert(copy.body.messages.some(message => message.content?.some?.(block => block.signature === "fixture-native-signature")));
  assert(JSON.stringify(copy.body).includes("cache_control"), "wire cache markers missing");
  const afterReplay = await client("replay", "state");
  assert.equal(afterReplay.observed, observed.observed, "plugin replay recursively reached normal observers");
  assert.equal(afterReplay.tripwireCalls, 0, "plugin replay executed a returned tool call");
  assert(afterReplay.phases.every(phase => phase.origin.kind === "agent"));
  result.context = { snapshot, replay, original_wire_index: native.index, replay_wire_index: copy.index };
  result.checks.push("full native history, system, tools, options and cache identity observed",
    "bounded same-context replay preserves actual provider wire except output cap/stream",
    "response observation schedules independent work after ordinary foreground completion",
    "plugin-origin inference bypasses observers and never executes returned tool calls",
    "stream has exactly one finish and reports attributable usage");

  const assertOrigin = (origin, threadID) => {
    assert.equal(origin.kind, "agent");
    assert.equal(origin.session_id, threadID);
    assert.equal(origin.thread_id, threadID);
  };
  assertOrigin(observed.snapshots.at(-1).origin, thread.id);
  assert.equal(replay.result.result.origin.session_id, thread.id);
  assert.equal(replay.result.result.origin.thread_id, thread.id);
  const { thread: secondThread } = await rpc("thread/start", { model: "claude-fixture", provider: "fixture" });
  assert.notEqual(secondThread.id, thread.id);
  await normalTurn(secondThread.id, "pipeline-origin-second"); normalTurns++;
  for (const [tag, threadID] of [["pipeline-origin-second", secondThread.id], ["pipeline-origin-first-again", thread.id]]) {
    if (threadID === thread.id) await turn(tag);
    const replayState = await client("replay", "state");
    const shadowState = await client("shadow", "state");
    assertOrigin(replayState.snapshots.at(-1).origin, threadID);
    assertOrigin(replayState.transformOrigins.at(-1).origin, threadID);
    assertOrigin(shadowState.middlewareOrigins.at(-1).origin, threadID);
  }
  result.origin_threads = [thread.id, secondThread.id];
  result.checks.push("foreground transform, middleware and observer origins distinguish threads and preserve descendant attribution");

  const beforeUncapped = providerCalls.length;
  const unscoped = await client("replay", "start", { request: snapshot, stream: false, generation_scope: true, preserve_limits: true });
  const uncapped = await terminalJob("replay", unscoped.job_id);
  assert.equal(uncapped.state, "completed", JSON.stringify(uncapped));
  assert.equal(providerCalls.length, beforeUncapped + 1);
  assert.equal(providerCalls.at(-1).body.max_tokens, native.body.max_tokens);
  assert.deepEqual(contextWire(providerCalls.at(-1).body), contextWire(native.body));
  result.checks.push("generation-scoped host starts background work without foreground execution authority",
    "general invocation preserves the original output limit when no override is requested");

  const aliasRequest = { version: 1, model: snapshot.model, messages: [{ role: "user", content: "pipeline-alias" }], tools: [] };
  const aliasStart = await client("replay", "start", { request: aliasRequest, model_alias: "comparison", stream: false });
  const aliasResult = await terminalJob("replay", aliasStart.job_id);
  assert.equal(aliasResult.state, "completed", JSON.stringify(aliasResult));
  assert.equal(aliasResult.result.response.content, "fixture:shadow-result");
  const inheritedAlias = (await client("shadow", "state")).adapterRequests.at(-1);
  assert.equal(inheritedAlias.provider, "fixture");
  assert.equal(inheritedAlias.model, "claude-fixture-alias");
  assert.equal(inheritedAlias.adapter, "fixture.shadow");
  assert.equal(inheritedAlias.provider_options.effort, "high");
  assert.equal(inheritedAlias.provider_options.temperature, 0.61);
  assert.equal(inheritedAlias.provider_options.fixture_alias_default, "configured");
  assert.equal(inheritedAlias.max_tokens, 31);
  assert(!("maxOutputTokens" in inheritedAlias.provider_options), "alias default bypassed the explicit output cap");
  result.checks.push("model alias supplies omitted effort, provider options and custom adapter defaults");

  const aliasOptionsStart = await client("replay", "start", { model_alias: "comparison", stream: false,
    request: { ...aliasRequest, effort: "low", provider_options: { adapter: "fixture.native-delegate", fixture_mode: "native",
      effort: "max", temperature: 0.19, maxOutputTokens: 900 } } });
  const aliasOptions = await terminalJob("replay", aliasOptionsStart.job_id);
  assert.equal(aliasOptions.state, "completed", JSON.stringify(aliasOptions));
  const overriddenAlias = (await client("shadow", "state")).adapterRequests.at(-1);
  assert.equal(overriddenAlias.adapter, "fixture.native-delegate");
  assert.equal(overriddenAlias.provider_options.effort, "max");
  assert.equal(overriddenAlias.provider_options.temperature, 0.19);
  assert.equal(overriddenAlias.provider_options.fixture_alias_default, "configured");
  assert.equal(overriddenAlias.max_tokens, 31);
  assert.equal(providerCalls.at(-1).body.max_tokens, 31);
  assert.equal(providerCalls.at(-1).body.model, "claude-fixture-alias");
  assert.equal(providerCalls.at(-1).body.output_config.effort, "max");
  result.checks.push("explicit alias invocation options override configured defaults while keeping its output cap");

  const adapterCallsBeforeNative = (await client("shadow", "state")).adapterRequests.length;
  const aliasNativeStart = await client("replay", "start", { model_alias: "comparison", stream: false,
    request: { ...aliasRequest, adapter: "native", effort: "low", provider_options: {
      adapter: "fixture.shadow", temperature: 0.17, maxOutputTokens: 900 } } });
  const aliasNative = await terminalJob("replay", aliasNativeStart.job_id);
  assert.equal(aliasNative.state, "completed", JSON.stringify(aliasNative));
  assert.equal((await client("shadow", "state")).adapterRequests.length, adapterCallsBeforeNative);
  assert.equal(providerCalls.at(-1).body.model, "claude-fixture-alias");
  assert.equal(providerCalls.at(-1).body.max_tokens, 31);
  assert.equal(providerCalls.at(-1).body.temperature, 0.17);
  assert.equal(providerCalls.at(-1).body.output_config.effort, "low");
  result.alias = { inherited: inheritedAlias, overridden: overriddenAlias, native: aliasNative };
  result.checks.push("explicit native adapter selection overrides alias and request option adapter routing");

  await client("shadow", "arm", { tag: "pipeline-shadow" });
  await turn("pipeline-shadow");
  const shadowState = await client("shadow", "state");
  const shadow = await terminalJob("shadow", shadowState.jobs.at(-1).job_id);
  assert.equal(shadow.state, "completed", JSON.stringify(shadow));
  assert.equal(shadow.result.result.response.content, "fixture:shadow-result");
  assert.equal(shadow.result.result.response.usage.input_tokens, 3);
  assert.equal(shadow.result.result.response.usage.output_tokens, 2);
  assert.equal(shadow.result.events.filter(event => event.type === "done").length, 1);
  assert.equal(shadow.result.events.at(-1).type, "done");
  assert.notEqual(shadowState.baseline.response.content, shadow.result.result.response.content);
  const adapterRequest = (await client("shadow", "state")).adapterRequests.at(-1);
  const shadowSnapshot = (await client("replay", "state")).snapshots.at(-1).request;
  assert.deepEqual(adapterRequest.messages, shadowSnapshot.messages);
  assert.deepEqual(adapterRequest.tools, shadowSnapshot.tools);
  assert.deepEqual(adapterRequest.cache_hint, shadowSnapshot.cache_hint);
  result.shadow = { baseline: shadowState.baseline, comparison: shadow, request: adapterRequest };
  result.checks.push("second public extension shadows the same context with a custom adapter",
    "custom adapter streams normalized finish and usage without core edits");

  const adapterError = await client("shadow", "start", { request: snapshot, mode: "error" });
  const errorStatus = await terminalJob("shadow", adapterError.job_id);
  assert(errorStatus.result?.error || errorStatus.error, JSON.stringify(errorStatus));
  assert(errorStatus.result.events.some(event => event.content === "fixture:before-error"));
  assert.equal(errorStatus.result.events.filter(event => event.type === "done").length, 0);
  result.adapter_error = errorStatus;
  result.checks.push("custom adapter streaming error remains failure rather than successful finish");

  const beforeLateError = adapterSubmissions().map(submission => submission.operation_id);
  const lateError = await client("shadow", "start", { request: snapshot, mode: "late-error" });
  const lateStatus = await terminalJob("shadow", lateError.job_id);
  assert(lateStatus.result?.error || lateStatus.error, JSON.stringify(lateStatus));
  assert.equal(lateStatus.result.events.filter(event => event.type === "done").length, 0);
  const lateErrorReceipts = adapterSubmissions().filter(submission => !beforeLateError.includes(submission.operation_id));
  assert.equal(lateErrorReceipts.length, 1);
  result.checks.push("adapter exception after emitted done cannot masquerade as success");

  const beforeLateCancel = adapterSubmissions().map(submission => submission.operation_id);
  const lateCancel = await client("shadow", "start", { request: snapshot, mode: "late-cancel" });
  await fixtureState("shadow", state => state.adapterFinalReceiptHeld === 1, "final adapter receipt acknowledged before cancellation");
  const lateCancelReceipts = adapterSubmissions().filter(submission => !beforeLateCancel.includes(submission.operation_id));
  assert.equal(lateCancelReceipts.length, 1);
  await client("shadow", "cancel", { job_id: lateCancel.job_id });
  assert.equal((await terminalJob("shadow", lateCancel.job_id)).state, "cancelled");

  const beforeMalformedStream = adapterSubmissions().map(submission => submission.operation_id);
  const malformedStream = await client("shadow", "start", { request: snapshot, mode: "malformed-response" });
  const malformedStreamStatus = await terminalJob("shadow", malformedStream.job_id);
  assert(malformedStreamStatus.result?.error || malformedStreamStatus.error, JSON.stringify(malformedStreamStatus));
  assert.equal(malformedStreamStatus.result.events.filter(event => event.type === "done").length, 0);
  const malformedStreamReceipts = adapterSubmissions().filter(submission => !beforeMalformedStream.includes(submission.operation_id));
  assert.equal(malformedStreamReceipts.length, 1);

  const beforeUnaryValidation = adapterSubmissions().map(submission => submission.operation_id);
  await turn("pipeline-unary-validation");
  const unaryValidation = (await client("shadow", "state")).unaryValidation;
  assert.deepEqual(unaryValidation.map(call => call.failed), [true, true, false, true, false]);
  assert(unaryValidation.filter(call => !call.failed).every(call => call.usage.input_tokens === 3 && call.usage.output_tokens === 2));
  const unaryReceipts = adapterSubmissions().filter(submission => !beforeUnaryValidation.includes(submission.operation_id));
  assert.equal(unaryReceipts.length, 5);
  assert(!providerCalls.some(call => call.tag === "pipeline-unary-validation"));

  const nativeErrorRequest = structuredClone(snapshot);
  [...nativeErrorRequest.messages].reverse().find(message => message.role === "user").content = "pipeline-error";
  const nativeError = await client("replay", "start", { request: nativeErrorRequest });
  const nativeErrorStatus = await terminalJob("replay", nativeError.job_id);
  assert(nativeErrorStatus.result?.error || nativeErrorStatus.error, JSON.stringify(nativeErrorStatus));
  assert.equal(nativeErrorStatus.result.events.filter(event => event.type === "done").length, 0);
  result.checks.push("native transport streaming error is surfaced without successful finish");

  const cancellationsBeforeHold = (await client("shadow", "state")).adapterCancelled;
  const heldAdapter = await client("shadow", "start", { request: snapshot, mode: "hold" });
  await fixtureState("shadow", state => state.adapterRequests.some(request => request.provider_options?.fixture_mode === "hold"), "custom adapter started");
  const liveDB = new DatabaseSync(join(home, "sessions/sessions.sqlite3"), { readOnly: true });
  try {
    result.adapter_admission = liveDB.prepare("SELECT s.operation_id, s.outcome, s.cost_state, s.completed_at FROM inference_submissions s JOIN inference_operations o ON s.operation_id = o.id WHERE o.owner_id = ? AND s.protocol = ? AND s.outcome = 'in_flight'").all(`plugin:${fixtures.shadow.id.split(":").at(-1)}`, "plugin:fixture.shadow");
  } finally { liveDB.close(); }
  assert.equal(result.adapter_admission.length, 1, "direct adapter callback ran without durable pre-admission");
  assert.equal(result.adapter_admission[0].completed_at, 0);
  assert.equal(result.adapter_admission[0].cost_state, "unknown_but_billable");
  result.checks.push("direct custom adapter has a durable submission before its callback completes");
  await client("shadow", "cancel", { job_id: heldAdapter.job_id });
  const adapterCancellation = await terminalJob("shadow", heldAdapter.job_id);
  assert.equal(adapterCancellation.state, "cancelled", JSON.stringify(adapterCancellation));
  await fixtureState("shadow", state => state.adapterCancelled === cancellationsBeforeHold + 1, "custom adapter AbortSignal");
  result.checks.push("background cancellation delivers AbortSignal to custom adapter execution");

  const serviceResult = await client("replay", "service", { request: { ...snapshot, adapter: "fixture.shadow" }, stream: false });
  assert.equal(serviceResult.response.content, "fixture:shadow-result");
  assert.equal(serviceResult.origin.plugin_id, fixtures.shadow.id.split(":").at(-1));
  result.checks.push("cross-plugin ordinary service handler owns its live model invocation");

  const noUsageJob = await client("shadow", "start", { request: snapshot, mode: "no-usage" });
  const noUsage = await terminalJob("shadow", noUsageJob.job_id);
  assert.equal(noUsage.state, "completed", JSON.stringify(noUsage));
  assert.equal(noUsage.result.result.response.usage, undefined);

  const beforeDelegate = providerCalls.length;
  const delegated = await client("shadow", "start", { request: snapshot, mode: "native", stream: false });
  const delegation = await terminalJob("shadow", delegated.job_id);
  assert.equal(delegation.state, "completed", JSON.stringify(delegation));
  assert.equal(delegation.result.response.usage.input_tokens, 11);
  assert.equal(providerCalls.length, beforeDelegate + 1);
  result.native_delegation = delegation;
  result.checks.push("custom adapter can explicitly delegate to native transport");

  const beforeMissing = providerCalls.length;
  const missing = await client("replay", "start", { request: { ...snapshot, adapter: "fixture.missing" }, stream: false });
  const missingStatus = await terminalJob("replay", missing.job_id);
  assert.equal(missingStatus.state, "failed", JSON.stringify(missingStatus));
  assert.equal(providerCalls.length, beforeMissing, "missing custom adapter silently fell back to native");
  result.checks.push("missing exact custom adapter fails closed before provider dispatch");

  await turn("pipeline-hold-break");
  const breakSnapshot = (await client("replay", "state")).snapshots.at(-1).request;
  const breakJob = await client("replay", "start", { request: breakSnapshot, early_exit: true });
  const breakStatus = await terminalJob("replay", breakJob.job_id);
  assert.equal(breakStatus.state, "completed", JSON.stringify(breakStatus));
  assert.equal(breakStatus.result.early_exit, true);
  assert(breakStatus.result.events.some(event => event.content === "fixture:first-chunk"));
  await waitFor("closed", closed, call => call.replay && call.tag === "pipeline-hold-break", "early iterator exit provider disconnect");
  result.checks.push("public streaming iterator early exit cancels its native transport");

  await turn("pipeline-retry");
  assert.equal(providerCalls.filter(call => call.tag === "pipeline-retry").length, 2);
  const retryOperation = (await client("replay", "state")).snapshots.at(-1).origin.operation_id;
  result.checks.push("transparent stream middleware preserves retryable native failure recovery");

  const privateRetryRequest = structuredClone(snapshot);
  [...privateRetryRequest.messages].reverse().find(message => message.role === "user").content = "pipeline-private-retry";
  const privateRetryJob = await client("replay", "start", { request: privateRetryRequest });
  const privateRetry = await terminalJob("replay", privateRetryJob.job_id);
  assert.equal(privateRetry.state, "completed", JSON.stringify(privateRetry));
  assert(!privateRetry.result.error, JSON.stringify(privateRetry));
  assert(privateRetry.result.events.some(event => event.type === "lifecycle"), "retry lifecycle was not observed by plugin");
  assert(!JSON.stringify(privateRetry).includes(privateProviderMarker), "raw provider error body crossed the public model boundary");
  result.sanitized_retry = privateRetry;
  result.checks.push("provider error body is excluded from public retry lifecycle events");

  await turn("pipeline-transform");
  const transformed = providerCalls.find(call => !call.replay && call.tag === "pipeline-transform");
  result.transform = { state: await client("replay", "state"), diagnostics: await rpc("plugin/diagnostics/list", { id: fixtures.replay.id, fingerprint: fixtures.replay.fingerprint }) };
  assert.equal(transformed.body.temperature, 0.23);
  assert(JSON.stringify(transformed.body.system).includes("fixture:logical-transform"));
  assert((await client("replay", "state")).snapshots.at(-1).request.messages.some(message => message.content === "fixture:logical-transform"));
  result.checks.push("logical request transform reaches provider and observer before submission");

  await turn("pipeline-mutation");
  assert.equal((await client("shadow", "state")).mutationRejections, 1);
  assert.equal(providerCalls.filter(call => call.tag === "pipeline-mutation").length, 1);
  assert(!JSON.stringify(providerCalls).includes("fixture-forbidden-mutation"));
  result.checks.push("semantically unchanged raw JSON crosses middleware while actual request mutation is rejected");

  await turn("pipeline-middleware-first");
  const middlewareOperation = (await client("replay", "state")).snapshots.at(-1).origin.operation_id;
  const beforeShortCircuit = providerCalls.length;
  await turn("pipeline-middleware-second");
  assert.equal(providerCalls.length, beforeShortCircuit, "middleware short circuit reached provider");
  assert.equal((await client("shadow", "state")).middlewareHits, 1);
  result.checks.push("around-call middleware can inspect result, rewrite output and short circuit provider");

  const failedStart = await rpc("turn/start", { thread_id: thread.id, prompt: "pipeline-physical-failure" });
  normalTurns++;
  const failedTurn = await waitFor("message", transcript, message =>
    message.method === "turn/error" && message.params?.turn?.id === failedStart.turn.id, "native usage failure termination");
  assert.equal(failedTurn.params.turn.status, "failed");
  const failureState = await client("shadow", "state");
  assert(failureState.failureUsageRewrites > 0, "middleware never rewrote the native usage event");
  const failedUsageOperation = (await client("replay", "state")).snapshots.at(-1).origin.operation_id;

  await turn("pipeline-isolate");
  assert((await client("replay", "state")).transformFailures > 0);
  assert((await client("shadow", "state")).observerFailures >= 2);
  assert(providerCalls.some(call => call.tag === "pipeline-isolate"));
  result.checks.push("isolated transform and observer failures do not break normal inference");

  await rpc("config/model/update", { thread_id: thread.id, provider: "fixture", model: "claude-fixture-next" });
  const pinnedStart = await client("replay", "start", { request: snapshot, stream: false });
  const pinned = await terminalJob("replay", pinnedStart.job_id);
  assert.equal(pinned.state, "completed", JSON.stringify(pinned));
  assert.equal(providerCalls.at(-1).body.model, "claude-fixture", "snapshot drifted to new default model");
  await turn("pipeline-model-changed");
  assert.equal(providerCalls.find(call => call.tag === "pipeline-model-changed").body.model, "claude-fixture-next");
  result.checks.push("saved request remains pinned while next foreground request uses changed model");

  const budgetStart = await rpc("turn/start", { thread_id: thread.id, prompt: "pipeline-budget" });
  normalTurns++;
  const budgetTerminal = await waitFor("message", transcript, message =>
    ["turn/completed", "turn/error"].includes(message.method) && message.params?.turn?.id === budgetStart.turn.id, "budget turn termination");
  const budgetState = await client("shadow", "state");
  assert(budgetState.budgetFailure, "default shared workflow budget never refused another physical request");
  assert(budgetState.budgetCallbacks > 0);
  assert.equal(budgetState.budgetAttempts, budgetState.budgetCallbacks + 1, "budget refusal happened after entering the direct adapter callback");
  assert(/budget/i.test(budgetState.budgetFailure.message), JSON.stringify(budgetState.budgetFailure));
  assert(!providerCalls.some(call => call.tag === "pipeline-budget"));
  result.budget = { attempted: budgetState.budgetAttempts, callbacks: budgetState.budgetCallbacks, failure: budgetState.budgetFailure, turn_status: budgetTerminal.params.turn.status };
  result.checks.push("shared workflow budget refuses another direct adapter before its callback starts");

  for (const action of ["cancel", "reload", "disable"]) {
    const tag = `pipeline-hold-${action}`;
    await client("replay", "arm", { tag });
    await turn(tag);
    const active = await client("replay", "state");
    const job = active.jobs.at(-1);
    const held = await waitFor("provider", providerCalls, call => call.replay && call.tag === tag, `${action} provider started`);
    if (action === "cancel") {
      assert.equal((await client("replay", "cancel", { job_id: job.job_id })).cancelled, true);
      assert.equal((await terminalJob("replay", job.job_id)).state, "cancelled");
    } else if (action === "reload") {
      const source = join(scratch, "replay.ts");
      await writeFile(source, (await readFile(source, "utf8")) + "\n// Synthetic same-source reload acceptance.\n");
      const previous = fixtures.replay;
      await publish(source);
      const refreshed = await rpc("extension/catalog/refresh");
      fixtures.replay = refreshed.extension_inventory.find(plugin => plugin.name === "replay");
      fixtures.shadow = refreshed.extension_inventory.find(plugin => plugin.name === "shadow");
      assert.equal(fixtures.replay.runtime_state, "active");
      assert.notEqual(fixtures.replay.fingerprint, previous.fingerprint);
      const stale = await rpcRaw("plugin/client/request", { id: previous.id, fingerprint: previous.fingerprint, request_id: "stale-generation", method: "state", input: {} });
      assert(stale.error, "old generation client identity remained valid");
      assert.equal((await client("replay", "state")).jobs.length, 0);
    } else {
      await rpc("extension/package/update", { id: fixtures.replay.id, fingerprint: fixtures.replay.fingerprint, action: "disable" });
    }
    await waitFor("closed", closed, call => call.index === held.index, `${action} provider transport closed`);
    result.checks.push(`${action} stops generation-owned background inference and provider transport`);
  }

  const enabled = await rpc("extension/package/update", { id: fixtures.replay.id, fingerprint: fixtures.replay.fingerprint, action: "enable" });
  fixtures.replay = enabled.extension_inventory.find(plugin => plugin.name === "replay");
  fixtures.shadow = enabled.extension_inventory.find(plugin => plugin.name === "shadow");
  await client("replay", "arm", { tag: "pipeline-hold-shutdown" });
  await turn("pipeline-hold-shutdown");
  const shutdownCall = await waitFor("provider", providerCalls, call => call.replay && call.tag === "pipeline-hold-shutdown", "shutdown provider started");
  await stopChild();
  await waitFor("closed", closed, call => call.index === shutdownCall.index, "shutdown provider disconnected");
  const db = new DatabaseSync(join(home, "sessions/sessions.sqlite3"), { readOnly: true });
  try {
    result.accounting = {
      operations: db.prepare("SELECT id, workflow_id, owner_id, kind, status FROM inference_operations ORDER BY created_at").all(),
      submissions: db.prepare("SELECT operation_id, cost_state, reported_input_tokens, reported_output_tokens, has_reported_usage, outcome FROM inference_submissions ORDER BY started_at").all(),
      workflows: db.prepare("SELECT owner_id, status FROM inference_workflows ORDER BY created_at").all(),
      sessions: db.prepare("SELECT count(*) AS count FROM sessions").get().count,
      messages: db.prepare("SELECT role, content, tool_calls_json, tool_result_json FROM session_messages ORDER BY seq").all(),
    };
  } finally { db.close(); }
  const accounting = result.accounting;
  assert(accounting.operations.every(operation => operation.status !== "active"), "active inference accounting leaked");
  assert(accounting.workflows.every(workflow => workflow.status !== "active"), "active inference workflow leaked on shutdown");
  assert(accounting.submissions.every(submission => submission.outcome !== "in_flight"), "shutdown left an in-flight physical submission");
  result.checks.push("process shutdown cancels live background transport and persists terminal accounting");
  const replayOwner = `plugin:${fixtures.replay.id.split(":").at(-1)}`;
  const shadowOwner = `plugin:${fixtures.shadow.id.split(":").at(-1)}`;
  for (const owner of [replayOwner, shadowOwner]) {
    const ids = accounting.operations.filter(operation => operation.owner_id === owner).map(operation => operation.id);
    assert(ids.length > 0, `${owner} usage ownership missing`);
    assert(accounting.submissions.some(submission => ids.includes(submission.operation_id) && submission.has_reported_usage), `${owner} reported usage missing`);
  }
  const shadowOperations = accounting.operations.filter(operation => operation.owner_id === shadowOwner).map(operation => operation.id);
  const shadowUsage = accounting.submissions.filter(submission => shadowOperations.includes(submission.operation_id) && submission.has_reported_usage);
  assert.equal(shadowUsage.length, 6, "native delegation duplicated a charged submission");
  assert.equal(shadowUsage.reduce((total, submission) => total + submission.reported_input_tokens + submission.reported_output_tokens, 0), 41);
  result.adapter_terminal_receipts = [];
  for (const [mode, receipts] of [["throw", lateErrorReceipts], ["cancel", lateCancelReceipts]]) {
    const submitted = accounting.submissions.filter(submission => submission.operation_id === receipts[0].operation_id);
    assert.equal(submitted.length, 1, `${mode} duplicated a direct adapter receipt`);
    result.adapter_terminal_receipts.push({ mode, ...submitted[0] });
    assert.equal(submitted[0].outcome, "failed");
    assert.equal(submitted[0].cost_state, "known");
    assert.equal(submitted[0].reported_input_tokens, 3);
    assert.equal(submitted[0].reported_output_tokens, 2);
    result.checks.push(`direct adapter final usage remains known when its callback later ${mode === "throw" ? "throws" : "is cancelled"}`);
  }
  const malformedStreamReceipt = accounting.submissions.find(submission => submission.operation_id === malformedStreamReceipts[0].operation_id);
  assert.equal(malformedStreamReceipt.outcome, "failed");
  assert.equal(malformedStreamReceipt.cost_state, "known");
  assert.equal(malformedStreamReceipt.reported_input_tokens, 3);
  assert.equal(malformedStreamReceipt.reported_output_tokens, 2);
  result.malformed_stream_receipt = malformedStreamReceipt;
  result.checks.push("streaming adapter returned malformed response keeps its independently valid usage receipt");
  const unarySubmissions = unaryReceipts.map(receipt => accounting.submissions.find(submission => submission.operation_id === receipt.operation_id));
  result.unary_validation = unarySubmissions;
  assert.equal(new Set(unaryReceipts.map(receipt => accounting.operations.find(operation => operation.id === receipt.operation_id).workflow_id)).size, 1);
  for (const [index, submission] of unarySubmissions.entries()) {
    assert.equal(submission.outcome, unaryValidation[index].failed ? "failed" : "succeeded");
    if (index < 2) {
      assert.equal(submission.cost_state, "unknown_but_billable");
      assert.equal(submission.has_reported_usage, 0);
      assert.equal(submission.reported_input_tokens, 0);
      assert.equal(submission.reported_output_tokens, 0);
    } else {
      assert.equal(submission.cost_state, "known");
      assert.equal(submission.reported_input_tokens, 3);
      assert.equal(submission.reported_output_tokens, 2);
    }
  }
  result.checks.push("invalid unary usage remains unknown and subsequent valid calls share a healthy workflow",
    "valid unary usage survives unrelated malformed response metadata");
  assert(!accounting.submissions.some(submission => submission.operation_id === delegation.result.origin.operation_id && submission.has_reported_usage), "adapter echoed native usage as a second charged result");
  const unknownCost = accounting.submissions.filter(submission => submission.operation_id === noUsage.result.result.origin.operation_id);
  assert.equal(unknownCost.length, 1);
  assert.equal(unknownCost[0].cost_state, "unknown_but_billable");
  assert.equal(unknownCost[0].has_reported_usage, 0);
  const retried = accounting.submissions.filter(submission => submission.operation_id === retryOperation);
  assert.equal(retried.length, 2);
  assert.deepEqual(retried.map(submission => submission.outcome), ["failed", "succeeded"]);
  const failedPhysicalUsage = accounting.submissions.filter(submission => submission.operation_id === failedUsageOperation);
  result.failed_physical_usage = failedPhysicalUsage[0];
  assert.equal(failedPhysicalUsage.length, 1);
  assert.equal(failedPhysicalUsage[0].outcome, "failed");
  assert.equal(failedPhysicalUsage[0].reported_input_tokens, 11);
  assert.equal(failedPhysicalUsage[0].reported_output_tokens, 5);
  assert.equal(failedPhysicalUsage[0].cost_state, "known");
  result.checks.push("middleware rewrite cannot corrupt physical native usage on a failed stream");
  const originalUsage = accounting.submissions.filter(submission => submission.operation_id === middlewareOperation);
  assert.equal(originalUsage.length, 1);
  assert.equal(originalUsage[0].reported_input_tokens, 11);
  assert.equal(originalUsage[0].reported_output_tokens, 5);
  result.checks.push("missing adapter usage stays unknown and native delegation is counted once",
    "middleware usage rewrite cannot replace native submission billing evidence");
  assert(accounting.messages.some(message => /"score"\s*:\s*1\.0/.test(message.tool_result_json)), "raw 1.0 tool result was not exercised");
  assert.equal(accounting.sessions, 2, "independent inference created a hidden session");
  assert.equal(accounting.messages.filter(message => message.role === "user").length, normalTurns);
  assert(!accounting.messages.some(message => message.content.includes("fixture:shadow-result") || message.content.includes(":replay") || message.tool_calls_json.includes("fixture-unexecuted-tool")), "independent inference polluted user history");
  assert(accounting.messages.some(message => message.content.includes("fixture:middleware-cache-hit")), "middleware short-circuit response was not delivered");
  assert(accounting.messages.some(message => message.content.startsWith("middleware:fixture:")), "middleware response rewrite was not delivered");
  result.checks.push("plugin-owned terminal accounting includes native and custom-adapter usage",
    "all independent inference stays outside visible and durable conversation history");
  await writeFile(join(output, "journal.json"), JSON.stringify(accounting, null, 2));
  result.status = "passed";
  console.log(`PASS ${result.checks.length} model pipeline checks`);
} catch (error) {
  result.error = error.stack; console.error(error.stack); process.exitCode = 1;
} finally {
  try { await stopChild(); } catch (error) { result.status = "failed"; result.shutdown_error = error.stack; process.exitCode = 1; }
  provider.closeAllConnections();
  await new Promise(resolve => provider.close(resolve));
  result.provider_calls = providerCalls.length;
  result.completed_at = new Date().toISOString();
  await writeFile(join(output, "results.json"), JSON.stringify(result, null, 2));
  for (const [name, values] of [["appserver", transcript], ["requests", requests], ["provider", providerCalls]]) {
    await writeFile(join(output, `${name}.jsonl`), values.map(value => JSON.stringify(value)).join("\n") + "\n");
  }
  await writeFile(join(output, "stderr.log"), stderr);
  console.log(`Evidence: ${output}`);
}
