#!/usr/bin/env node
// Real app-server/public extension journey against loopback Responses servers.
// Requires Node 22.13+, desktop dependencies and a freshly built WUU_E2E_CORE.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const { WebSocketServer } = createRequire(new URL("../desktop/package.json", import.meta.url))("ws");
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const output = resolve(root, process.env.WUU_RESPONSES_E2E_OUTPUT || ".tmp/e2e/responses-tool-capability");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-")), home = join(scratch, "state");
await mkdir(home);
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
const events = new EventEmitter(), messages = [], calls = [], sent = [], closed = [], responseTags = new Map();
const result = { started_at: new Date().toISOString(), checks: [], status: "failed",
  limitations: ["Loopback synthetic inference; no real provider, user credentials or live billing.", "Disposable unconfined test profile; process-sandbox enforcement is not under test."] };
let child, stderr = "", sequence = 0;
const source = 'text("literal: 中文 \\n \\t \\\\ ${value} `tick`");';
const tool = { name: "run_code", description: "Run source text.", freeform: true,
  input_schema: { type: "object", properties: { input: { type: "string" } }, required: ["input"], additionalProperties: false } };
const models = {
  "gpt-fixture": { id: "gpt-5" }, "portable-fixture": {},
  "gpt-fixture-off": { id: "gpt-5", options: { freeform_tool: false } },
  "portable-fixture-on": { options: { freeform_tool: true } },
  "friendly-model": { id: "portable-wire" },
  "gpt-5.4": { id: "portable-wire" }, "friendly-supported": { id: "gpt-5.2" },
  ...Object.fromEntries(["gpt-4.1", "gpt-4o", "gpt-oss-120b", "openai/unknown-model", "codex-unknown-model",
    "gpt-5-unknown", "gpt-5.99", "gpt-5-2025-08-07", "gpt-5.3-codex", "openai/gpt-5", "gpt-6-astra"].map(id => [id, {}])),
};
const routes = [["gpt-fixture", true], ["portable-fixture", false], ["gpt-fixture-off", false], ["portable-fixture-on", true],
  ["gpt-4.1", false], ["gpt-4o", false], ["gpt-oss-120b", false], ["openai/unknown-model", false],
  ["codex-unknown-model", false], ["gpt-5-unknown", false], ["gpt-5.99", false], ["gpt-5.4", false],
  ["friendly-supported", true], ["gpt-5-2025-08-07", true], ["gpt-5.3-codex", true], ["openai/gpt-5", true], ["gpt-6-astra", true]];

function waitFor(event, values, predicate, label, timeout = 20_000) {
  const previous = values.find(predicate);
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
  const request = { id: `wire-${++sequence}`, method, params };
  sent.push(request); child.stdin.write(JSON.stringify(request) + "\n");
  return waitFor("message", messages, value => value.id === request.id, method);
}
async function rpc(method, params = {}) {
  const reply = await rpcRaw(method, params);
  assert(!reply.error, `${method}: ${JSON.stringify(reply.error)}`);
  return reply.result;
}
async function stop() {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit"), timer = setTimeout(() => child.kill("SIGKILL"), 5000);
  child.stdin.end();
  try { const [code, signal] = await exited; assert.equal(signal, null); assert.equal(code, 0, stderr); }
  finally { clearTimeout(timer); }
}
function handle(body, transport, send, onClose) {
  const tag = body.input?.filter(item => item.role === "user").map(item => typeof item.content === "string"
    ? item.content : item.content?.map(part => part.text ?? "").join("")).findLast(text => text?.startsWith("wire-")) ?? responseTags.get(body.previous_response_id) ?? "auxiliary";
  const call = { index: calls.length, transport, tag, body };
  calls.push(call); events.emit("provider", call);
  onClose(() => { closed.push(tag); events.emit("closed", tag); });
  if (tag === "wire-cancel") return;
  const declaration = body.tools?.find(item => item.name === "run_code");
  const hasCurrentResult = body.input?.some(item => item.call_id === "call_current" && item.type.endsWith("_output"));
  const current = declaration && !hasCurrentResult
    ? declaration.type === "custom"
      ? { type: "custom_tool_call", id: "ctc_current", call_id: "call_current", name: "run_code", input: source }
      : { type: "function_call", id: "fc_current", call_id: "call_current", name: "run_code", arguments: JSON.stringify({ input: source }) }
    : { type: "message", id: "msg_current", role: "assistant", content: [{ type: "output_text", text: "fixture:completed" }] };
  const response = { id: `resp_${calls.length}`, status: "completed", model: body.model, output: [current],
    usage: { input_tokens: 7, output_tokens: 3, total_tokens: 10 } };
  responseTags.set(response.id, tag);
  if (transport === "unary") { send(response); return; }
  send({ type: "response.output_item.added", output_index: 0, item: current });
  send({ type: "response.output_item.done", output_index: 0, item: current });
  send({ type: "response.completed", response });
}
const provider = createServer(async (request, response) => {
  try {
    let bytes = ""; for await (const part of request) bytes += part;
    const body = JSON.parse(bytes), transport = body.stream ? "sse" : "unary";
    response.writeHead(200, { "Content-Type": body.stream ? "text/event-stream" : "application/json" });
    response.flushHeaders();
    handle(body, transport, value => {
      if (transport === "unary") response.end(JSON.stringify(value));
      else { response.write(`data: ${JSON.stringify(value)}\n\n`); if (value.type === "response.completed") response.end(); }
    }, callback => response.on("close", callback));
  } catch (error) { response.destroy(error); }
});
const websocket = new WebSocketServer({ server: provider });
websocket.on("connection", socket => socket.on("message", bytes => {
  try { handle(JSON.parse(bytes.toString()), "websocket", value => socket.send(JSON.stringify(value)), callback => socket.once("close", callback)); }
  catch (error) { socket.close(1011, error.message.slice(0, 100)); }
}));
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));

try {
  result.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  result.source_sha256 = createHash("sha256").update(await readFile(fileURLToPath(import.meta.url))).digest("hex");
  const config = join(home, "config.json"), pluginPath = join(scratch, "wire-fixture.ts");
  const service = stream_transport => ({ type: "openai-compatible", wire_api: "responses", stream_transport,
    base_url: `http://127.0.0.1:${provider.address().port}`, api_key: "synthetic-no-real-key", model: "gpt-fixture", models });
  await writeFile(config, JSON.stringify({ default_provider: "openai-named-bridge",
    providers: { "openai-named-bridge": service("sse"), websocket: service("websocket"), cached: service("websocket-cached") },
    agent: { permission_mode: "unconfined", model_aliases: { fallback: { provider: "openai-named-bridge", model: "friendly-model" } } },
    skills: { enabled: false }, ptc: { enabled: true, node_executable: process.execPath } }));
  await writeFile(pluginPath, `
import { invokeModel, startModelInvocation, readModelInvocation } from "@wuu/plugin-sdk";
export default {
  initialize() { return { protocol_version: 3, required_host_services: [{ id: "host.service.call", required: true }],
    required_services: [{ name: "host.model.invoke", major_version: 1, required: true }],
    capabilities: [{ id: "plugin.client.request", kind: "decision", version: 1 }] }; },
  async invokeCapability(params, host) {
    const input = params.input.input;
    const request = { request: input.request, model_alias: input.model_alias, max_output_tokens: 64, timeout_ms: 10000 };
    if (!input.stream) return { output: { result: await invokeModel(host, request) } };
    const handle = await startModelInvocation(host, request);
    let cursor = 0; const events = [];
    for (;;) {
      const read = await readModelInvocation(host, { ...handle, cursor, wait_ms: 1000 });
      events.push(...read.events); cursor = read.cursor;
      if (read.done) return { output: { result: { ...read.result, error: read.error, events } } };
    }
  }
};
`);
  const publish = spawnSync(binary, ["plugin", "dev", "--watch=false", pluginPath], { env, encoding: "utf8", timeout: 20_000 });
  assert.equal(publish.status, 0, publish.stderr);
  child = spawn(binary, ["app-server", "--workdir", scratch, "--config", config], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const value = JSON.parse(line); messages.push(value); events.emit("message", value); }
    catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const initialized = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "responses-tool-capability-e2e" } });
  const plugin = initialized.extension_inventory.find(item => item.name === "wire-fixture");
  assert.equal(plugin?.runtime_state, "active", JSON.stringify(plugin));
  const params = (id, input) => ({ id: plugin.id, fingerprint: plugin.fingerprint, request_id: id, method: "invoke", input });
  const invoke = async (id, input) => (await rpc("plugin/client/request", params(id, input))).result;
  const request = (model, tag, provider = "openai-named-bridge") => ({ version: 1, provider, model, cache_hint: { prompt_cache_key: tag }, tools: [tool], force_tool_name: "run_code",
    messages: [{ role: "user", content: tag },
      { role: "assistant", tool_calls: [{ id: "call_history", name: "run_code", kind: "custom", provider_item_id: "ctc_history", arguments: JSON.stringify({ input: source }) }] },
      { role: "tool", tool_call_id: "call_history", tool_result_kind: "custom", content: "historical output" }] });
  for (const transport of ["unary", "sse", "websocket"]) {
    for (const [model, custom] of routes) {
      const tag = `wire-${transport}-${model}`;
      const original = request(model, tag, transport === "websocket" ? "websocket" : "openai-named-bridge");
      // Aliases opt in to configured defaults; exact logical requests preserve
      // their own options, so this request spells the compatibility override.
      original.provider_options = models[model].options ?? {};
      const reply = await invoke(tag, { request: original, stream: transport !== "unary" });
      assert(!reply.error, JSON.stringify(reply));
      const call = calls.find(item => item.tag === tag), wire = call.body;
      assert.equal(call.transport, transport);
      const declaration = wire.tools.find(item => item.name === "run_code");
      assert.equal(declaration.type, custom ? "custom" : "function", `wire capability: ${model}`);
      assert.equal(wire.model, models[model].id ?? model);
      assert.deepEqual(wire.tool_choice, { type: declaration.type, name: "run_code" });
      assert.equal(wire.freeform_tool, undefined, "local option leaked to provider");
      assert(custom ? !declaration.parameters : declaration.parameters.properties.input.type === "string");
      const historyCall = wire.input.find(item => item.call_id === "call_history" && !item.type.endsWith("_output"));
      const historyResult = wire.input.find(item => item.call_id === "call_history" && item.type.endsWith("_output"));
      assert.equal(historyCall.type, custom ? "custom_tool_call" : "function_call");
      assert.equal(historyResult.type, custom ? "custom_tool_call_output" : "function_call_output");
      assert.equal(custom ? historyCall.input : JSON.parse(historyCall.arguments).input, source);
      if (!custom) assert.equal(historyCall.id, undefined, "custom item ID crossed to function replay");
      assert.equal(reply.response.tool_calls[0].kind ?? "function", custom ? "custom" : "function");
      assert.equal(JSON.parse(reply.response.tool_calls[0].arguments).input, source);
      result.checks.push(`${transport}/${model}: declaration, choice, replay, literal bytes and local option`);
    }
  }
  const alias = await invoke("wire-alias", { request: request("gpt-fixture", "wire-alias"), model_alias: "fallback" });
  assert(!alias.error, JSON.stringify(alias));
  const aliasWire = calls.find(item => item.tag === "wire-alias").body;
  assert.equal(aliasWire.model, "portable-wire");
  assert.equal(aliasWire.tools.find(item => item.name === "run_code").type, "function");
  assert.equal(aliasWire.freeform_tool, undefined);
  result.checks.push("model alias uses API identity without relying on an explicit compatibility option");
  const summaries = initialized.providers.find(provider => provider.name === "openai-named-bridge").models;
  for (const [model, custom] of [...routes, ["friendly-model", false]]) {
    assert.equal(!!summaries.find(item => item.id === model)?.capabilities?.freeform_tool, custom, `capability summary: ${model}`);
  }
  result.checks.push("capability summaries use actual API identity and compatibility options");
  for (const providerName of ["openai-named-bridge", "cached"]) {
    for (const [model, custom] of routes.slice(0, 4)) {
      const { thread } = await rpc("thread/start", { model, provider: providerName });
      const { turn } = await rpc("turn/start", { thread_id: thread.id, prompt: `wire-agent-${providerName}-${model}` });
      const complete = await waitFor("message", messages, value => value.method === "turn/completed" && value.params?.turn?.id === turn.id, `agent ${model}`);
      assert.equal(complete.params.turn.status, "completed", JSON.stringify(complete));
      const journey = calls.filter(item => item.tag === `wire-agent-${providerName}-${model}`);
      const wire = journey.at(-1).body;
      if (providerName === "cached") assert(wire.previous_response_id, "tool result did not reuse WebSocket continuation");
      const trace = (await readFile(complete.params.trace_path, "utf8")).trim().split("\n").map(line => JSON.parse(line));
      const recorded = trace.find(item => item.type === "turn" && item.data?.turn_id === turn.id);
      assert.equal(!!recorded?.data?.model_profile?.freeform_tool, custom, `trace capability: ${model}`);
      const actual = wire.input.find(item => item.call_id === "call_current" && item.type.endsWith("_output"));
      assert(actual?.output.includes("literal: 中文"), `PTC did not execute: ${JSON.stringify(actual)}`);
      assert(!actual.output.includes("Error"), actual.output);
      result.checks.push(`${providerName}/${model}: normal turn executes, replays PTC and records actual capability`);
    }
  }
  const cancellation = rpcRaw("plugin/client/request", params("wire-cancel", { request: request("portable-fixture", "wire-cancel"), stream: true }));
  await waitFor("provider", calls, item => item.tag === "wire-cancel", "cancellable inference");
  assert((await rpc("plugin/client/cancel", { id: plugin.id, fingerprint: plugin.fingerprint, request_id: "wire-cancel" })).cancelled);
  assert((await cancellation).error);
  await waitFor("closed", closed, value => value === "wire-cancel", "provider cancellation");
  result.checks.push("function fallback preserves public invocation cancellation");
  result.status = "passed";
  console.log(`PASS ${result.checks.length} Responses tool capability journeys`);
} catch (error) { result.error = error.stack; console.error(error.stack); process.exitCode = 1; }
finally {
  try { await stop(); } catch (error) { result.shutdown_error = error.stack; result.status = "failed"; process.exitCode = 1; }
  for (const socket of websocket.clients) socket.terminate();
  websocket.close(); provider.closeAllConnections(); await new Promise(resolve => provider.close(resolve));
  result.completed_at = new Date().toISOString();
  await writeFile(join(output, "results.json"), JSON.stringify(result, null, 2));
  for (const [name, values] of [["appserver", messages], ["requests", sent], ["provider", calls]]) {
    await writeFile(join(output, `${name}.jsonl`), values.map(value => JSON.stringify(value)).join("\n") + "\n");
  }
  await writeFile(join(output, "stderr.log"), stderr);
  console.log(`Evidence: ${output}`);
}
