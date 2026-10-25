#!/usr/bin/env node
// Public composition service through an external single-file plugin and the Go
// app-server. Synthetic HTTP inference; real read_file, graph and scope cleanup.
// Failure cases: cross-scope state/model leakage, failed prepare, binding races,
// dispose during a run, and a later admission reviving a disposed provider.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { cp, mkdir, mkdtemp, readFile, stat, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(process.env.WUU_E2E_CORE || join(root, "bin/wuu"));
const output = resolve(process.env.WUU_COMPOSITION_E2E_OUTPUT || join(root, ".tmp/e2e/composition"));
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "state"), workspace = join(scratch, "workspace");
await mkdir(home); await mkdir(workspace);
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
const transcript = [], requests = [], events = new EventEmitter();
const result = { status: "failed", scratch, checks: [], limitations: ["Synthetic model; Go app-server RPC and real tools. Rendered desktop acceptance is separate."] };
let child, stderr = "", sequence = 0, plugin, graphGeneration;
let releaseHeld, releaseProduct, releaseBackground, releaseRetired, releaseSide;
const held = new Promise(resolve => { releaseHeld = resolve; });
const productHeld = new Promise(resolve => { releaseProduct = resolve; });
const sideHeld = new Promise(resolve => { releaseSide = resolve; });
const backgroundHeld = new Promise(resolve => { releaseBackground = resolve; });
const retiredHeld = new Promise(resolve => { releaseRetired = resolve; });

function waitFor(event, history, predicate, label, timeout = 30000) {
  const found = history.find(predicate);
  if (found) return Promise.resolve(found);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off(event, listener); reject(new Error(`Timed out: ${label}`)); }, timeout);
    function listener(value) {
      if (!predicate(value)) return;
      clearTimeout(timer); events.off(event, listener); resolve(value);
    }
    events.on(event, listener);
  });
}
async function rpcRaw(method, params = {}) {
  const id = `composition-${++sequence}`;
  const pending = waitFor("message", transcript, message => message.id === id, method);
  child.stdin.write(JSON.stringify({ id, method, params }) + "\n");
  return await pending;
}
async function rpc(method, params) {
  const reply = await rpcRaw(method, params);
  assert(!reply.error, JSON.stringify(reply.error));
  return reply.result;
}
async function clientRaw(method, input) {
  return await rpcRaw("plugin/client/request", { id: plugin.id, fingerprint: plugin.fingerprint,
    graph_generation: graphGeneration, request_id: `scope-${++sequence}`, method, input });
}
async function client(method, input) {
  const reply = await clientRaw(method, input);
  assert(!reply.error, JSON.stringify(reply.error));
  return reply.result.result;
}
async function startServer(configPath) {
  child = spawn(binary, ["app-server", "--workdir", workspace, "--config", configPath], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const message = JSON.parse(line); transcript.push(message); events.emit("message", message); }
    catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const initialized = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "composition-e2e" } });
  plugin = initialized.extension_inventory.find(item => item.name === "composition-lab");
  assert.equal(plugin?.runtime_state, "active", JSON.stringify(plugin));
  const generations = new Set(initialized.extension_inventory.filter(item => item.graph_generation).map(item => item.graph_generation));
  assert.equal(generations.size, 1);
  graphGeneration = [...generations][0];
}
async function stopServer() {
  if (child && child.exitCode === null) {
    child.stdin.end();
    const termination = setTimeout(() => child.kill("SIGTERM"), 5000);
    const kill = setTimeout(() => child.kill("SIGKILL"), 8000);
    await once(child, "exit");
    clearTimeout(termination); clearTimeout(kill);
  }
}
const provider = createServer(async (request, response) => {
  try {
    let bytes = "";
    for await (const part of request) bytes += part;
    const body = JSON.parse(bytes);
    const prompt = body.messages.filter(message => message.role === "user").map(message => typeof message.content === "string" ? message.content : "").join("\n");
    const tag = body.tools?.length ? [...prompt.matchAll(/scope-case:([a-z-]+)/g)].at(-1)?.[1] : undefined;
    const ordinal = requests.filter(request => request.tag === tag).length;
    const record = { tag: tag ?? "auxiliary", body };
    requests.push(record); events.emit("request", record);
    if (tag === "held") await held;
    if (tag === "product-held") await productHeld;
    if (tag === "side-parent") await sideHeld;
    if (tag === "background-child") await backgroundHeld;
    if (tag === "retired-child") await retiredHeld;
    const lastUser = body.messages.findLastIndex(message => message.role === "user");
    const tool = body.messages.slice(lastUser + 1).findLast(message => message.role === "tool");
    let delta = { role: "assistant", content: `${body.model}:${tool?.content ?? "auxiliary"}` }, finish = "stop";
    if (tag === "background-child" && tool) delta.content = `scope-case:background-delivery ${body.model}:${tool.content}`;
    if (tag === "retired-child" && tool) delta.content = `scope-case:retired-delivery ${body.model}:${tool.content}`;
    if (tag && !["background-delivery", "retired-delivery"].includes(tag) && (!tool || (tag === "side-parent" && ordinal < 2))) {
      const toolName = ["product-parent", "background-parent", "retired-parent"].includes(tag)
        ? body.tools.find(tool => tool.function?.name.includes("subagent_spawn_agent"))?.function.name
        : "read_file";
      assert(toolName && body.tools.some(tool => tool.function?.name === toolName), `missing tool for ${tag}`);
      const args = ["product-parent", "background-parent", "retired-parent"].includes(tag)
        ? { description: "Read child workspace marker", prompt: `scope-case:${tag.replace("parent", "child")} Read marker.txt.`, run_in_background: tag !== "product-parent" }
        : { path: "marker.txt" };
      delta = { role: "assistant", tool_calls: [{ index: 0, id: `read-${tag}-${ordinal}`, type: "function",
        function: { name: toolName, arguments: JSON.stringify(args) } }] };
      finish = "tool_calls";
    }
    if (body.stream) {
      response.writeHead(200, { "Content-Type": "text/event-stream" });
      response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: finish }] })}\n\ndata: [DONE]\n\n`);
    } else {
      response.writeHead(200, { "Content-Type": "application/json" });
      response.end(JSON.stringify({ choices: [{ index: 0, message: delta, finish_reason: finish }] }));
    }
  } catch (error) { response.writeHead(500); response.end(String(error)); }
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));

try {
  result.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  result.source_sha256 = createHash("sha256").update(await readFile(fileURLToPath(import.meta.url))).digest("hex");
  const configPath = join(home, "config.json");
  const source = join(scratch, "composition-lab.ts");
  const moduleID = `local-${createHash("sha256").update(source).digest("hex").slice(0, 24)}`;
  await writeFile(configPath, JSON.stringify({ default_provider: "first", providers: Object.fromEntries(["first", "second"].map(name => [name, {
    type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}/v1`, api_key: "synthetic", model: `model-${name}`,
  }])), composition: { version: 2, profile: "headless", patches: [{ modules: { [moduleID]: true, subagent: true, side: true } }] }, extensions: { enabled: { "plugin:bundled:subagent": true } }, skills: { enabled: false }, ptc: { enabled: false } }));
  await cp(join(root, "examples/plugins/composition-lab.ts"), source);
  const publish = spawnSync(binary, ["plugin", "dev", "--watch=false", source], { env, encoding: "utf8", timeout: 20000 });
  result.publication = { status: publish.status, stdout: publish.stdout, stderr: publish.stderr };
  assert.equal(publish.status, 0, publish.stderr);
  await startServer(configPath);
  result.plugin = plugin;
  result.graph_generation = graphGeneration;
  const [first, second] = await Promise.all([
    client("create", { inherit: true }),
    client("create", { inherit: true, model: { provider: "second", model: "model-second" } }),
  ]);
  result.scopes = { first, second };
  assert.equal(first.parent_generation, second.parent_generation);
  assert.equal(first.model.provider, "first");
  assert.equal(second.model.provider, "second");
  assert.notEqual(first.root, second.root);
  await writeFile(join(first.root, "marker.txt"), "FIRST-SCOPE-MARKER");
  await writeFile(join(second.root, "marker.txt"), "SECOND-SCOPE-MARKER");
  await writeFile(join(workspace, "marker.txt"), "PARENT-MARKER");
  result.checks.push("concurrent scope creation captures the parent graph and separate model routes");

  const failed = await clientRaw("create", { inherit: true, selection: { version: 2, patches: [{ modules: { "wuu.tools.default": false } }] } });
  assert(failed.error, "missing selected tools provider was accepted");
  await client("inspect", { id: first.id });
  result.checks.push("failed preparation preserves the active candidate");

  await client("bind", { agent: "writer", id: first.id, expected: "" });
  const running = client("run", { agent: "writer", prompt: "scope-case:held Read marker.txt." });
  // Attach failure handling immediately while the other client requests run.
  running.catch(() => {});
  await waitFor("request", requests, request => request.tag === "held", "first model admission");
  await client("bind", { agent: "writer", id: second.id, expected: first.id });
  const conflict = await clientRaw("bind", { agent: "writer", id: first.id, expected: first.id });
  assert(conflict.error, "stale compare-and-exchange replaced the binding");
  const draining = await client("dispose", { id: first.id });
  assert.equal(draining.draining, true);
  const other = await client("run", { agent: "writer", prompt: "scope-case:second Read marker.txt." });
  assert(!other.error, other.error);
  assert(other.content.includes("model-second:") && other.content.includes("SECOND-SCOPE-MARKER"));
  assert(!other.content.includes("FIRST-SCOPE-MARKER"));
  releaseHeld();
  const completed = await running;
  assert(!completed.error, completed.error);
  assert(completed.content.includes("model-first:") && completed.content.includes("FIRST-SCOPE-MARKER"));
  assert(!completed.content.includes("SECOND-SCOPE-MARKER"));
  await assert.rejects(stat(first.root), { code: "ENOENT" });
  const retired = await clientRaw("run", { id: first.id, prompt: "must fail before inference" });
  assert(retired.error, "disposed scope admitted another execution");
  result.checks.push("parallel runs retain independent tools, files and model routes",
    "bind uses compare-and-exchange; admitted run retains its graph through dispose and cleanup");

  const { thread } = await rpc("thread/start", { provider: "first", model: "model-first" });
  const { turn } = await rpc("turn/start", { thread_id: thread.id, prompt: "scope-case:parent Read marker.txt." });
  const terminal = await waitFor("message", transcript, message => ["turn/completed", "turn/error"].includes(message.method) && message.params?.turn?.id === turn.id, "parent turn");
  assert.equal(terminal.params.turn.status, "completed", JSON.stringify(terminal));
  const parentRequests = requests.filter(request => request.tag === "parent");
  assert(parentRequests.length >= 2);
  assert(parentRequests.every(request => request.body.model === "model-first"));
  assert(JSON.stringify(parentRequests).includes("PARENT-MARKER"));
  assert(!JSON.stringify(parentRequests).includes("SECOND-SCOPE-MARKER"));
  await client("dispose", { id: second.id });
  await assert.rejects(stat(second.root), { code: "ENOENT" });
  result.checks.push("parent app-server conversation keeps its original route and tools after candidate cleanup");
  const policySource = join(scratch, "repeated-observation.ts");
  await cp(join(root, "examples/plugins/repeated-observation.ts"), policySource);
  const policyModule = `local-${createHash("sha256").update(policySource).digest("hex").slice(0, 24)}`;
  const [productFirst, productSecond] = await Promise.all([
    client("create", { inherit: true, state: "parent", paths: [policySource],
      selection: { version: 2, patches: [{ modules: { [policyModule]: true } }] },
      model: { provider: "second", model: "model-second" } }),
    client("create", { inherit: true, state: "parent" }),
  ]);
  result.product_scopes = { productFirst, productSecond };
  await client("bind", { session: thread.id, id: productFirst.id, expected: "" });
  const { turn: productTurn } = await rpc("turn/start", { thread_id: thread.id, prompt: "scope-case:product-held Read marker.txt." });
  await waitFor("request", requests, request => request.tag === "product-held", "product composition admission");
  await client("bind", { session: thread.id, id: productSecond.id, expected: productFirst.id });
  const side = await rpc("sideThread/sendMessage", { main_thread_id: thread.id, prompt: "scope-case:side-parent Read marker.txt using the attached conversation's admitted composition." });
  await waitFor("request", requests, request => request.tag === "side-parent", "side composition admission");
  const productDrain = await client("dispose", { id: productFirst.id });
  assert.equal(productDrain.draining, true);
  const { thread: untouched } = await rpc("thread/start", { provider: "first", model: "model-first" });
  const { turn: untouchedTurn } = await rpc("turn/start", { thread_id: untouched.id, prompt: "scope-case:untouched Read marker.txt." });
  const untouchedDone = await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === untouchedTurn.id, "unbound concurrent product turn");
  assert.equal(untouchedDone.params.turn.status, "completed", JSON.stringify(untouchedDone));
  releaseProduct();
  const productDone = await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === productTurn.id, "bound product turn");
  assert.equal(productDone.params.turn.status, "completed", JSON.stringify(productDone));
  const { turn: nextTurn } = await rpc("turn/start", { thread_id: thread.id, prompt: "scope-case:product-next Read marker.txt." });
  const nextDone = await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === nextTurn.id, "rebound product turn");
  assert.equal(nextDone.params.turn.status, "completed", JSON.stringify(nextDone));
  await stat(productFirst.root);
  releaseSide();
  const sideDone = await waitFor("message", transcript, message => message.method === "sideThread/event"
    && message.params?.side_thread_id === side.summary.side_thread_id && message.params?.type === "status"
    && ["completed", "failed", "interrupted"].includes(message.params?.summary?.status), "composed side conversation");
  assert.equal(sideDone.params.summary.status, "completed", JSON.stringify(sideDone));
  const sideCalls = requests.filter(request => request.tag === "side-parent");
  assert.equal(sideCalls.length, 2, "side policy must stop after two repeated observations");
  assert(sideCalls.every(request => JSON.stringify(request.body.messages).includes("this task does not support polling")), "side conversation lost its selected request transform");
  const sideHistory = await rpc("sideThread/getHistory", { main_thread_id: thread.id });
  assert(sideHistory.messages.at(-1).text.includes("Stopped because two consecutive identical tool calls"), "side policy did not produce the terminal result");
  assert(sideCalls.every(request => request.body.model === "model-second"), "side conversation escaped the parent's admitted composition after rebind");
  assert(JSON.stringify(sideCalls).includes("PARENT-MARKER"), "side conversation lost the parent's workspace");
  result.checks.push("side conversation retains the active parent's admitted composition through rebind, dispose and parent completion",
    "selected request transform and stopping policy apply together to side conversation tool execution");
  const idleEvents = transcript.length;
  const idleSide = await rpc("sideThread/sendMessage", { main_thread_id: thread.id, prompt: "scope-case:side-idle Read marker.txt using the current composition." });
  const idleDone = await waitFor("message", transcript.slice(idleEvents), message => message.method === "sideThread/event"
    && message.params?.side_thread_id === idleSide.summary.side_thread_id && message.params?.type === "status"
    && ["completed", "failed", "interrupted"].includes(message.params?.summary?.status), "idle composed side conversation");
  assert.equal(idleDone.params.summary.status, "completed", JSON.stringify(idleDone));
  for (const [tag, model] of [["product-held", "model-second"], ["untouched", "model-first"], ["product-next", "model-first"], ["side-idle", "model-first"]]) {
    const calls = requests.filter(request => request.tag === tag);
    assert(calls.length >= 2, `missing actual read_file round trip for ${tag}`);
    assert(calls.every(request => request.body.model === model), `model route crossed for ${tag}`);
    assert(JSON.stringify(calls).includes("PARENT-MARKER"), `product workspace lost for ${tag}`);
  }
  await client("dispose", { id: productSecond.id });
  const denied = await rpcRaw("turn/start", { thread_id: thread.id, prompt: "scope-case:disposed must not run" });
  assert(denied.error, "disposed product selection silently fell back");
  assert(!requests.some(request => request.tag === "disposed"));
  const deniedSide = await rpcRaw("sideThread/sendMessage", { main_thread_id: thread.id, prompt: "scope-case:side-disposed must not run" });
  assert(deniedSide.error, "side conversation revived a disposed selection");
  assert(!requests.some(request => request.tag === "side-disposed"));
  result.checks.push("idle side conversation reads the new selection; disposed selection fails before inference");
  await client("bind", { session: thread.id, id: "", expected: productSecond.id });
  const family = await client("create", { inherit: true, state: "parent", model: { provider: "second", model: "model-second" } });
  result.family_scope = family;
  await client("bind", { session: thread.id, id: family.id, expected: "" });
  const { turn: familyTurn } = await rpc("turn/start", { thread_id: thread.id, prompt: "scope-case:product-parent Delegate the marker read to a foreground child." });
  const familyDone = await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === familyTurn.id, "bundled subagent parent completion");
  assert.equal(familyDone.params.turn.status, "completed", JSON.stringify(familyDone));
  const childCalls = requests.filter(request => request.tag === "product-child");
  assert(childCalls.length >= 2, "bundled child did not perform its real tool round trip");
  assert(childCalls.every(request => request.body.model === "model-second"), "child lost parent composition model");
  assert(JSON.stringify(childCalls).includes("PARENT-MARKER"));
  const parentCalls = requests.filter(request => request.tag === "product-parent");
  assert(JSON.stringify(parentCalls).includes("model-second:"), "foreground child result was not returned to parent");
  const backgroundBoundary = transcript.length;
  const { turn: backgroundTurn } = await rpc("turn/start", { thread_id: thread.id, prompt: "scope-case:background-parent Delegate the marker read in the background." });
  await waitFor("request", requests, request => request.tag === "background-child", "background child admission");
  await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === backgroundTurn.id, "parent returns while child runs");
  releaseBackground();
  await waitFor("request", requests, request => request.tag === "background-delivery", "background child result delivered to parent");
  const deliveredTurn = await waitFor("message", transcript.slice(backgroundBoundary), message => message.method === "turn/started" && message.params?.thread_id === thread.id && message.params?.turn?.id !== backgroundTurn.id, "background completion turn");
  await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === deliveredTurn.params.turn.id, "background completion settled");
  assert(requests.filter(request => ["background-child", "background-delivery"].includes(request.tag)).every(request => request.body.model === "model-second"));
  result.checks.push("background child completion reaches the originating composition after the parent turn ends");
  const retiring = await client("create", { inherit: true, state: "parent", model: { provider: "second", model: "model-second" } });
  const successor = await client("create", { inherit: true, state: "parent" });
  result.retirement_scopes = { retiring, successor };
  await client("bind", { session: thread.id, id: retiring.id, expected: family.id });
  const { turn: retiringTurn } = await rpc("turn/start", { thread_id: thread.id, prompt: "scope-case:retired-parent Delegate before retiring this composition." });
  await waitFor("request", requests, request => request.tag === "retired-child", "retiring child admission");
  await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === retiringTurn.id, "retiring parent returns");
  await client("bind", { session: thread.id, id: successor.id, expected: retiring.id });
  assert.equal((await client("dispose", { id: retiring.id })).draining, true);
  releaseRetired();
  await waitFor("request", requests, request => request.tag === "retired-delivery", "retired origin delivers to rebound parent");
  const retirementDeadline = Date.now() + 10000;
  while (true) {
    try { await stat(retiring.root); }
    catch (error) { if (error.code === "ENOENT") break; throw error; }
    assert(Date.now() < retirementDeadline, "retired origin resources did not drain");
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  assert(requests.filter(request => request.tag === "retired-child").every(request => request.body.model === "model-second"));
  assert(requests.filter(request => request.tag === "retired-delivery").every(request => request.body.model === "model-first"));
  result.checks.push("disposed origin drains child and callback, delivers to the rebound parent, then removes its private resources");
  await client("bind", { session: thread.id, id: family.id, expected: successor.id });
  await client("dispose", { id: successor.id });
  const activeSelection = await client("inspectSession", { session: thread.id });
  assert.equal(activeSelection.id, family.id);
  assert.equal(activeSelection.state, "active");
  await client("dispose", { id: family.id });
  assert.equal((await client("inspectSession", { session: thread.id })).state, "unavailable");
  await assert.rejects(stat(productFirst.root), { code: "ENOENT" });
  result.checks.push("disposed composition releases its private directory after the final side execution drains");
  await stopServer();
  await startServer(configPath);
  await rpc("thread/resume", { session_id: thread.id });
  const restored = await client("inspectSession", { session: thread.id });
  result.restored_selection = restored;
  assert.equal(restored.id, family.id);
  assert.equal(restored.state, "unavailable");
  assert.equal(restored.composition.model.model, "model-second");
  const restartDenied = await rpcRaw("turn/start", { thread_id: thread.id, prompt: "scope-case:restart-denied must not run" });
  assert(restartDenied.error, "restart silently substituted the default composition");
  assert(!requests.some(request => request.tag === "restart-denied"));
  const restartConflict = await clientRaw("bind", { session: thread.id, id: "", expected: "" });
  assert(restartConflict.error, "restart lost compare-and-exchange identity");
  await client("bind", { session: thread.id, id: "", expected: family.id });
  assert.equal((await client("inspectSession", { session: thread.id })).state, "default");
  const { turn: restoredTurn } = await rpc("turn/start", { thread_id: thread.id, prompt: "scope-case:restored Read marker.txt." });
  await waitFor("message", transcript, message => message.method === "turn/completed" && message.params?.turn?.id === restoredTurn.id, "explicit unbind after restart");
  const restoredCalls = requests.filter(request => request.tag === "restored");
  assert(restoredCalls.length >= 2 && restoredCalls.every(request => request.body.model === "model-first"));
  result.checks.push("process restart preserves unavailable composition identity, diagnostics and CAS; explicit unbind restores default execution");
  result.checks.push("bundled subagent creates and runs a child through product session APIs with the parent's composition",
    "product main conversation captures its selected composition; rebind affects the next turn",
    "parallel unbound product conversation remains independent; disposed product bindings fail closed");
  result.status = "passed";
  console.log(`PASS ${result.checks.length} composition checks; evidence ${scratch}`);
} catch (error) {
  result.error = error.stack; console.error(error.stack); process.exitCode = 1;
} finally {
  releaseHeld();
  releaseProduct();
  releaseSide();
  releaseBackground();
  releaseRetired();
  await stopServer();
  provider.closeAllConnections();
  await new Promise(resolve => provider.close(resolve));
  await writeFile(join(scratch, "transcript.json"), JSON.stringify(transcript, null, 2));
  await writeFile(join(scratch, "requests.json"), JSON.stringify(requests, null, 2));
  await writeFile(join(scratch, "app-server.stderr"), stderr);
  await writeFile(join(scratch, "results.json"), JSON.stringify(result, null, 2));
}
