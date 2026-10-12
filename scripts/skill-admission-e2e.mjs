#!/usr/bin/env node
// Real app-server, plugin process and model/tool turns with isolated fixtures.
// No user configuration, API keys, paid calls, browser or desktop is involved.
// WUU_E2E_CORE=.tmp/wuu-core node scripts/skill-admission-e2e.mjs
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const output = resolve(root, process.env.WUU_SKILL_E2E_OUTPUT || ".tmp/e2e/skill-admission");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(tmpdir(), "wuu-skill-admission-"));
const home = join(scratch, "state"), project = join(scratch, "project");
await mkdir(home); await mkdir(project);
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
const messages = [], providerCalls = [], events = new EventEmitter();
const result = { status: "failed", scratch, checks: [], limitations: ["Synthetic model, real app-server and plugin transport; no visual acceptance or real-model reasoning claim."] };
let child, stderr = "", sequence = 0, pluginTool;

function waitFor(predicate, label) {
  const previous = messages.find(predicate);
  if (previous) return Promise.resolve(previous);
  return new Promise((resolve, reject) => {
    const listener = message => {
      if (!predicate(message)) return;
      clearTimeout(timer); events.off("message", listener); resolve(message);
    };
    const timer = setTimeout(() => { events.off("message", listener); reject(new Error(`Timed out: ${label}`)); }, 20000);
    events.on("message", listener);
  });
}
async function rpc(method, params = {}) {
  const id = `skill-e2e-${++sequence}`;
  child.stdin.write(JSON.stringify({ id, method, params }) + "\n");
  const response = await waitFor(message => message.id === id, method);
  assert(!response.error, `${method}: ${JSON.stringify(response.error)}`);
  return response.result;
}
async function turn(thread, prompt) {
  const start = providerCalls.length;
  const { turn } = await rpc("turn/start", { thread_id: thread.id, prompt });
  const completed = await waitFor(message => message.method === "turn/completed" && message.params?.turn?.id === turn.id, turn.id);
  assert.equal(completed.params.turn.status, "completed", JSON.stringify(completed));
  return providerCalls.slice(start);
}
async function writeSkill(name, metadata, body) {
  const dir = join(project, ".agents", "skills", name);
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "SKILL.md"), `---\nname: ${name}\ndescription: Fixture ${name}\n${metadata}\n---\n${body}\n`);
}
const text = value => typeof value === "string" ? value : value?.map(part => part.text ?? "").join("") ?? "";
const lastToolResult = calls => {
  const transcript = calls.at(-1)?.messages ?? [];
  const start = transcript.findLastIndex(message => message.role === "user" && text(message.content).startsWith("skill-e2e:"));
  return text(transcript.slice(start + 1).findLast(message => message.role === "tool")?.content);
};
const provider = createServer(async (request, response) => {
  try {
    let raw = "";
    for await (const chunk of request) raw += chunk;
    const body = JSON.parse(raw);
    providerCalls.push(body);
    pluginTool ||= body.tools?.find(tool => tool.function?.description === "Fixture skill contribution tool")?.function.name;
    const latestUser = body.messages.findLast(message => message.role === "user" && text(message.content).startsWith("skill-e2e:"));
    const input = text(latestUser?.content);
    const load = input.match(/^skill-e2e:load:([a-z-]+)$/);
    const execute = input === "skill-e2e:execute";
    const invoke = body.messages.at(-1)?.role === "user" && (load || execute);
    const content = invoke ? { tool_calls: [{ index: 0, id: `skill-call-${providerCalls.length}`, type: "function", function: {
      name: load ? "load_skill" : pluginTool,
      arguments: JSON.stringify(load ? { name: load[1] } : {}),
    } }] } : { content: "Fixture turn completed." };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.write(`data: ${JSON.stringify({ id: "fixture", choices: [{ index: 0, delta: { role: "assistant", ...content }, finish_reason: null }] })}\n\n`);
    response.end(`data: ${JSON.stringify({ id: "fixture", choices: [{ index: 0, delta: {}, finish_reason: invoke ? "tool_calls" : "stop" }], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } })}\n\ndata: [DONE]\n\n`);
  } catch (error) { response.writeHead(500); response.end(String(error)); }
});

try {
  await new Promise((resolve, reject) => { provider.once("error", reject); provider.listen(0, "127.0.0.1", resolve); });
  result.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  result.script_sha256 = createHash("sha256").update(await readFile(fileURLToPath(import.meta.url))).digest("hex");
  const config = join(home, "config.json");
  const engines = Object.fromEntries(["codex", "claude", "cursor", "devin", "grok", "hermes", "pi", "opencode", "antigravity"].map(id => [id, { enabled: false }]));
  await writeFile(config, JSON.stringify({ default_provider: "fixture", engines,
    providers: { fixture: { type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}/v1`,
      api_key: "synthetic-fixture-only", model: "fixture", models: { fixture: {}, "fixture-next": {} } } },
    agent: { tool_loading: "flat", permission_mode: "standard" }, ptc: { enabled: false },
  }));
  const pluginPath = join(scratch, "skill-fixture.ts");
  await writeFile(pluginPath, `export default {
    initialize() { return { tools: [{ id: "lookup", description: "Fixture skill contribution tool", input_schema: { type: "object" }, activity: { read_only: true, concurrency_safe: true } }] }; },
    executeTool() { return { result: { content: [{ type: "text", text: "CONTRIBUTED_TOOL_EXECUTED" }] } }; },
  };\n`);
  const publication = spawnSync(binary, ["plugin", "dev", "--watch=false", pluginPath], { env, encoding: "utf8", timeout: 20000 });
  assert.equal(publication.status, 0, publication.stderr);
  child = spawn(binary, ["app-server", "--workdir", project, "--config", config], { env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", value => { stderr += value; });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const message = JSON.parse(line); messages.push(message); events.emit("message", message); }
    catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const initialized = await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "skill-admission-e2e" } });
  const plugin = initialized.extension_inventory.find(item => item.name === "skill-fixture");
  assert.equal(plugin?.runtime_state, "active", JSON.stringify(initialized.extension_inventory));
  const { thread: seed } = await rpc("thread/start", { provider: "fixture", model: "fixture" });
  await turn(seed, "skill-e2e:seed");
  assert(pluginTool, "The provider must receive the actual plugin-owned tool name.");
  await writeSkill("manual", "disable-model-invocation: true\nuser-invocable: true", "MANUAL_SKILL_BODY ${ARGUMENTS}");
  await writeSkill("plugin-guide", `allowed-tools: [${pluginTool}]`, "PLUGIN_SKILL_BODY");
  await writeSkill("missing-guide", "allowed-tools: [plugin_missing_tool]", "UNAVAILABLE_SKILL_BODY");
  await writeSkill("restricted-manual", "disable-model-invocation: true\nuser-invocable: true", "RESTRICTED_MANUAL_BODY");
  await rpc("extension/catalog/refresh");
  // Different pinned model exercises the shadow toolkit's prompt construction.
  const { thread } = await rpc("thread/start", { provider: "fixture", model: "fixture-next" });
  const catalog = (await rpc("skill/list", { thread_id: thread.id })).skills;
  assert(catalog.some(skill => skill.name === "plugin-guide"), "Installed plugin tool must make its skill available.");
  assert(!catalog.some(skill => skill.name === "missing-guide"), "Unknown tool cannot satisfy a skill requirement.");
  const manual = catalog.find(skill => skill.name === "manual");
  assert(manual?.user_invocable && manual.disable_model_invoke, JSON.stringify(manual));
  const identity = { name: manual.name, source: manual.source, ...(manual.project ? { project: manual.project } : { path: manual.path }) };
  const manualCalls = await turn(thread, `/skill ${JSON.stringify(identity)}\nmanual-user-arguments`);
  assert(manualCalls.some(body => body.messages.some(message => message.role === "user" && text(message.content).includes("MANUAL_SKILL_BODY manual-user-arguments"))), "Manual invocation must expand the selected instructions before model admission.");
  result.checks.push("manual-only skill remains selectable and loads only on explicit user invocation");
  const denied = await turn(thread, "skill-e2e:load:manual");
  assert.match(lastToolResult(denied), /skill.*manual.*not found/i);
  assert(!lastToolResult(denied).includes("MANUAL_SKILL_BODY"));
  assert(!lastToolResult(denied).split("available:").at(-1).includes("manual"), "Unknown-name hints must not advertise manual-only skills.");
  result.checks.push("synthetic-provider tool call rejects manual-only skill and omits it from available-name hints");
  const allowed = await turn(thread, "skill-e2e:load:plugin-guide");
  assert(lastToolResult(allowed).includes("PLUGIN_SKILL_BODY"));
  const executed = await turn(thread, "skill-e2e:execute");
  assert(lastToolResult(executed).includes("CONTRIBUTED_TOOL_EXECUTED"));
  const system = allowed.flatMap(body => body.messages.filter(message => message.role === "system").map(message => text(message.content))).join("\n");
  assert(system.includes("<name>plugin-guide</name>") && !system.includes("<name>manual</name>"));
  result.checks.push("pinned-model prompt, skill loader and real contributed tool agree on availability");
  const { thread: restricted } = await rpc("thread/start", { provider: "fixture", model: "fixture" });
  // Seed isolated host-owned metadata before constructing the thread runtime.
  const seededPolicy = spawnSync("python3", ["-c", "import sys,sqlite3; c=sqlite3.connect(sys.argv[1]); c.execute('update sessions set tool_policy_json=? where id=?',(sys.argv[3],sys.argv[2])); c.commit()", join(home, "sessions", "sessions.sqlite3"), restricted.id, JSON.stringify({ allow: ["load_skill"] })], { encoding: "utf8" });
  assert.equal(seededPolicy.status, 0, seededPolicy.stderr);
  const restrictedCatalog = (await rpc("skill/list", { thread_id: restricted.id })).skills;
  assert(!restrictedCatalog.some(skill => skill.name === "plugin-guide"));
  const restrictedLoad = await turn(restricted, "skill-e2e:load:plugin-guide");
  assert.deepEqual(restrictedLoad[0].tools.map(tool => tool.function.name), ["load_skill"]);
  assert.match(lastToolResult(restrictedLoad), /skill.*plugin-guide.*not found/i);
  assert(!lastToolResult(restrictedLoad).includes("PLUGIN_SKILL_BODY"));
  const restrictedSelection = restrictedCatalog.find(skill => skill.name === "restricted-manual");
  assert(restrictedSelection?.user_invocable && restrictedSelection.disable_model_invoke);
  const restrictedManual = await turn(restricted, `/skill ${JSON.stringify({ name: restrictedSelection.name, source: restrictedSelection.source, ...(restrictedSelection.project ? { project: restrictedSelection.project } : { path: restrictedSelection.path }) })}`);
  assert(restrictedManual.some(body => body.messages.some(message => message.role === "user" && text(message.content).includes("RESTRICTED_MANUAL_BODY"))));
  result.checks.push("session policy excludes plugin-dependent skills from current catalogs and automatic loads while explicit manual-only selection remains valid");
  await rpc("extension/package/update", { id: plugin.id, fingerprint: plugin.fingerprint, action: "disable" });
  const revoked = await turn(thread, "skill-e2e:load:plugin-guide");
  assert.match(lastToolResult(revoked), /skill.*plugin-guide.*not found/i);
  assert(!(await rpc("skill/list", { thread_id: thread.id })).skills.some(skill => skill.name === "plugin-guide"));
  result.checks.push("disabling the plugin removes dependent skill admission after generation adoption");
  result.status = "passed";
} catch (error) {
  result.error = error.stack || String(error);
  process.exitCode = 1;
} finally {
  if (child && child.exitCode === null && child.signalCode === null) {
    const exited = once(child, "exit");
    const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    child.stdin.end();
    await exited;
    clearTimeout(timer);
  }
  provider.closeAllConnections();
  await new Promise(resolve => provider.close(resolve));
  await writeFile(join(output, "evidence.json"), JSON.stringify(result, null, 2) + "\n");
  await writeFile(join(output, "transcript.json"), JSON.stringify(messages, null, 2) + "\n");
  await writeFile(join(output, "provider-requests.json"), JSON.stringify(providerCalls, null, 2) + "\n");
  await writeFile(join(output, "stderr.log"), stderr);
  console.log(JSON.stringify(result, null, 2));
}
