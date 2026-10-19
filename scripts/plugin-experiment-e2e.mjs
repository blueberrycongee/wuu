#!/usr/bin/env node
// Exercise the real CLI, agent tool bridge, and app-server generation lifecycle.
// Uses synthetic source/state and a loopback provider; no paid inference or keys.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { watch } from "node:fs";
import { appendFile, mkdir, mkdtemp, readFile, readdir, rm, stat, symlink, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(import.meta.url);
if (process.argv[2] === "--evaluate") await evaluateFixture();
else await run();

function deadline(promise, label, ms = 20_000) {
  let timer;
  return Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`Timeout: ${label}`)), ms); })])
    .finally(() => clearTimeout(timer));
}

// The evaluator uses the shipped JSONL adapter and executes a registered tool.
// A filesystem barrier proves both command processes are alive concurrently.
async function evaluateFixture() {
  const root = process.env.WUU_EXPERIMENT_PACKAGE;
  const manifest = JSON.parse(await readFile(join(root, "plugin.json"), "utf8"));
  const barrier = process.argv[3];
  await writeFile(join(barrier, `${process.pid}.ready`), "ready");
  const watcher = watch(barrier);
  try {
    await deadline(new Promise((resolve, reject) => {
      const check = () => readdir(barrier).then(entries => { if (entries.filter(name => name.endsWith(".ready")).length >= 2) resolve(); }, reject);
      watcher.on("change", check); watcher.on("error", reject); check();
    }), "two evaluator commands reach the barrier", 12_000);
  } finally { watcher.close(); }
  const child = spawn(process.execPath, manifest.runtime.args, { cwd: root, stdio: ["pipe", "pipe", "pipe"] });
  let sequence = 0, stderr = "";
  const pending = new Map();
  child.stderr.setEncoding("utf8").on("data", data => stderr += data);
  createInterface({ input: child.stdout }).on("line", line => {
    const frame = JSON.parse(line), waiting = pending.get(frame.id);
    if (!waiting) return;
    pending.delete(frame.id);
    if (frame.error) waiting.reject(new Error(JSON.stringify(frame.error))); else waiting.resolve(frame.result);
  });
  child.on("exit", code => { for (const waiting of pending.values()) waiting.reject(new Error(`Plugin exited ${code}: ${stderr}`)); });
  const call = (method, params = {}) => {
    const id = String(++sequence);
    const promise = new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
    child.stdin.write(JSON.stringify({ id, method, params }) + "\n");
    return deadline(promise, method);
  };
  try {
    await call("initialize", { protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 1,
      plugin_id: manifest.id, plugin_root: root, project_root: process.cwd(), wuu_home: process.env.WUU_HOME,
      supported_host_services: ["host.service.call"] });
    await call("activate");
    const response = await call("tool.execute", { tool_id: "read", arguments: {}, session_id: "evaluation",
      execution_id: "evaluation-read", call_id: "evaluation-read", cwd: process.cwd() });
    assert(!response.result?.is_error, JSON.stringify(response));
    const text = response.result.content.find(part => part.type === "text").text;
    await call("shutdown");
    const exited = once(child, "exit"); child.stdin.end(); await deadline(exited, "plugin exit");
    console.log(JSON.stringify({ text, wuu_home: process.env.WUU_HOME, home: process.env.HOME, cwd: process.cwd(), package: root }));
  } finally { if (child.exitCode === null) child.kill("SIGKILL"); }
}

async function run() {
  const binary = resolve(process.env.WUU_E2E_CORE || "bin/wuu");
  const output = resolve(process.env.WUU_EXPERIMENT_E2E_OUTPUT || ".tmp/e2e/plugin-experiment");
  await mkdir(output, { recursive: true });
  const scratch = await mkdtemp(join(output, "run-")), home = join(scratch, "state"), source = join(scratch, "extension.ts");
  await mkdir(home);
  const lifecycle = join(scratch, "lifecycle.jsonl");
  await writeFile(lifecycle, "");
  const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
  const transcript = [], providerCalls = [], commands = [], events = new EventEmitter(), failures = [];
  const result = { status: "failed", started_at: new Date().toISOString(), scratch, binary,
    binary_sha256: createHash("sha256").update(await readFile(binary)).digest("hex"), checks: [],
    limitations: ["Synthetic loopback provider and extension; no paid-model quality, desktop rendering, filesystem sandbox, or network sandbox claim."] };
  let app, appStderr = "", sequence = 0, candidateA, candidateB, heldResponse;
  const counts = new Map();
  const pluginSource = (version, broken = false) => `import { appendFileSync, readFileSync } from "node:fs";
import type { RuntimePlugin } from "@wuu/plugin-sdk";
const version = ${JSON.stringify(version)};
const journal = ${JSON.stringify(lifecycle)};
export default {
 initialize() { ${broken ? 'throw new Error("synthetic initialize failure");' : ''}
  return { protocol_version: 2, tools: [{id:"read", description:"Experiment " + version, input_schema:{type:"object"}}],
   capabilities:[{id:"agent.system_prompt.section",kind:"transform",version:1}] }; },
 invokeCapability() { return {output:{text:"EXPERIMENT_PROMPT_" + version}}; },
 executeTool() { const disk = readFileSync(new URL(import.meta.url), "utf8").match(/const version = "([^"]+)"/)[1];
  appendFileSync(journal, JSON.stringify({event:"read", version, pid:process.pid}) + "\\n");
  return {result:{content:[{type:"text",text:"memory:"+version+";disk:"+disk}]}}; },
 shutdown() { appendFileSync(journal, JSON.stringify({event:"shutdown",version,pid:process.pid}) + "\\n"); }
} satisfies RuntimePlugin;\n`;
  const check = (name, fn) => { fn(); result.checks.push(name); console.log("PASS", name); };
  const assertStopped = async pid => {
    let running = true;
    for (let attempt = 0; attempt < 100 && running; attempt++) {
      try {
        process.kill(pid, 0);
        if (process.platform === "linux") running = !/^State:\s+Z/m.test(await readFile(`/proc/${pid}/status`, "utf8"));
      } catch (error) { if (["ESRCH", "ENOENT"].includes(error.code)) running = false; else throw error; }
      if (running) await new Promise(resolve => setTimeout(resolve, 20));
    }
    assert.equal(running, false, `cancelled descendant ${pid} remains running`);
  };
  const waitFor = (event, history, predicate, label) => {
    const found = history.find(predicate); if (found) return Promise.resolve(found);
    let listener;
    return deadline(new Promise(resolve => { listener = value => { if (predicate(value)) resolve(value); }; events.on(event, listener); }), label)
      .finally(() => events.off(event, listener));
  };
  const cli = async (args, expectedSuccess = true, commandEnv = env) => {
    const child = spawn(binary, args, { cwd: scratch, env: commandEnv, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "", stderr = "";
    child.stdout.setEncoding("utf8").on("data", data => stdout += data);
    child.stderr.setEncoding("utf8").on("data", data => stderr += data);
    const timer = setTimeout(() => child.kill("SIGKILL"), 60_000);
    const [code, signal] = await once(child, "exit"); clearTimeout(timer);
    commands.push({ args, code, signal, stdout, stderr });
    if (expectedSuccess) assert.equal(code, 0, `${args.join(" ")}: ${stderr}\n${stdout}`);
    else assert.notEqual(code, 0, "command should fail");
    return { code, stdout, stderr, json: () => JSON.parse(stdout) };
  };
  const capture = async () => (await cli(["plugin", "experiment", "capture", "--json", "--workdir", scratch, source])).json();
  const select = (action, candidate, expected, success = true) => cli(["plugin", "experiment", action, "--json", "--workdir", scratch,
    `--expect-current=${expected}`, candidate.id, candidate.candidate_id], success);
  const fingerprint = async () => JSON.parse(await readFile(join(home, "dev", "generations", candidateA.id, "receipt.json"), "utf8")).fingerprint;
  const send = (response, code) => {
    const delta = code ? { role: "assistant", tool_calls: [{ index: 0, id: `fixture-call-${providerCalls.length}`, type: "function",
      function: { name: "run_code", arguments: JSON.stringify({ input: code }) } }] } : { role: "assistant", content: "Fixture complete" };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: code ? "tool_calls" : "stop" }] })}\n\ndata: [DONE]\n\n`);
  };
  const provider = createServer(async (request, response) => {
    try {
      let raw = ""; for await (const chunk of request) raw += chunk;
      const body = JSON.parse(raw), messages = body.messages ?? [];
      const stage = [...messages].reverse().find(message => message.role === "user" && typeof message.content === "string" && message.content.startsWith("experiment:"))?.content.slice(11);
      if (!stage) { send(response); return; }
      const number = (counts.get(stage) ?? 0) + 1; counts.set(stage, number);
      const call = { stage, number, body }; providerCalls.push(call);
      const surface = JSON.stringify(body.tools) + messages.filter(message => message.role === "system").map(message => message.content).join("\n");
      const tool = surface.match(/plugin_local_[a-z0-9_]+_read_[a-f0-9]{16}/)?.[0];
      assert(tool, `plugin tool missing at ${stage}`);
      const toolOutput = [...messages].reverse().find(message => message.role === "tool")?.content ?? "";
      const expected = stage === "next-B" || stage === "rollback-A" ? "B" : "A";
      assert(surface.includes("EXPERIMENT_PROMPT_" + expected), `${stage} prompt lost pinned ${expected}`);
      if (stage === "discover-manager") {
        if (number === 1) send(response, `text(await tools.plugin_manager({action:"status"}));`);
        else { assert(String(toolOutput).includes(candidateA.id)); send(response); }
        return;
      }
      if (number === 1) {
        if (stage === "capture-B") {
          send(response, `await tools.write_file(${JSON.stringify({ path: source, content: pluginSource("B") })}); const receipt = await tools.plugin_manager(${JSON.stringify({ action: "capture", path: source })}); text("RECEIPT:" + receipt.model_text); text(await tools.${tool}({}));`);
        } else if (stage === "rollback-A") {
          send(response, `const receipt = await tools.plugin_manager(${JSON.stringify({ action: "rollback", id: candidateA.id, candidate_id: candidateA.candidate_id, expected_current: candidateB.fingerprint })}); text("RECEIPT:" + receipt.model_text); text(await tools.${tool}({}));`);
        } else send(response, `text(await tools.${tool}({}));`);
      } else {
        assert(String(toolOutput).includes(`memory:${expected};disk:${expected}`), `${stage} returned incorrect tool bytes: ${toolOutput}`);
        if (stage === "capture-B") {
          const receipt = String(toolOutput).split("\n").find(line => line.startsWith("RECEIPT:"));
          assert(receipt, toolOutput); candidateB = JSON.parse(receipt.slice(8)).experiment;
        }
        if (stage === "hold-A" && number === 2) {
          heldResponse = () => send(response, `text(await tools.${tool}({}));`);
          events.emit("held", call); return;
        }
        send(response);
      }
      events.emit("provider", call);
    } catch (error) { failures.push(error.stack); response.writeHead(500); response.end(String(error)); }
  });
  await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));
  const rpc = async (method, params = {}) => {
    const id = String(++sequence); app.stdin.write(JSON.stringify({ id, method, params }) + "\n");
    const response = await waitFor("message", transcript, value => value.id === id, method);
    assert(!response.error, `${method}: ${JSON.stringify(response.error)}`); return response.result;
  };
  let thread;
  const begin = async stage => {
    // Admission advertises a retryable response while the watcher publishes.
    for (let attempt = 0; ; attempt++) {
      try { return (await rpc("turn/start", { thread_id: thread, prompt: "experiment:" + stage })).turn.id; }
      catch (error) {
        if (attempt > 100 || !String(error).includes("execution is owned by another app-server")) throw error;
        await new Promise(resolve => setTimeout(resolve, 20));
      }
    }
  };
  const finish = async turn => {
    const message = await waitFor("message", transcript, value => ["turn/completed", "turn/error"].includes(value.method) && (value.params?.turn?.id === turn || value.params?.turn_id === turn), "turn completion");
    assert.equal(message.method, "turn/completed", JSON.stringify(message)); assert.deepEqual(failures, []);
  };
  const runTurn = async stage => finish(await begin(stage));
  const checkManagedCleanup = async mode => {
    const directory = join(scratch, `cleanup-${mode}`), ready = join(scratch, `cleanup-${mode}.ready`);
    await mkdir(directory);
    const runtimePID = join(scratch, `${mode}-runtime.pid`), hookPID = join(scratch, `${mode}-hook.pid`);
    const mcpPIDs = mode === "mcp" ? [0, 1, 2].map(index => join(scratch, `mcp-${index}.pid`)) : [];
    const runtimeCode = `import {createInterface} from 'node:readline'; import {writeFileSync} from 'node:fs';
writeFileSync(${JSON.stringify(runtimePID)}, String(process.pid)); setInterval(()=>{},1000);
createInterface({input:process.stdin}).on('line',line=>{const m=JSON.parse(line);if(m.method==='shutdown')return;
const result=m.method==='initialize'?{protocol_version:1,lifecycle_version:1}:null;console.log(JSON.stringify({id:m.id,result}));});`;
    const servers = Object.fromEntries(mcpPIDs.map((path, index) => [`fixture-${index}`, { command: process.execPath, args: ["-e", `
const {createInterface}=require('node:readline'),{writeFileSync}=require('node:fs');setInterval(()=>{},1000);
createInterface({input:process.stdin}).on('line',line=>{const m=JSON.parse(line);if(m.id===undefined)return;let result={};
if(m.method==='initialize')result={protocolVersion:'2024-11-05',capabilities:{tools:{}},serverInfo:{name:'fixture',version:'1'}};
if(m.method==='tools/list'){result={tools:[]};writeFileSync(${JSON.stringify(path)},String(process.pid));}
console.log(JSON.stringify({jsonrpc:'2.0',id:m.id,result}));});`] }]));
    const hookCode = mode === "hook"
      ? `const fs=require('node:fs');fs.writeFileSync(${JSON.stringify(hookPID)},String(process.pid));fs.writeFileSync(${JSON.stringify(ready)},'ready');setInterval(()=>{},1000);`
      : `const fs=require('node:fs');const timer=setInterval(()=>{if(${JSON.stringify(mcpPIDs)}.every(path=>fs.existsSync(path))){clearInterval(timer);fs.writeFileSync(${JSON.stringify(ready)},'ready');}},10);`;
    const quote = text => "'" + text.replaceAll("'", "'\\''") + "'";
    await writeFile(join(directory, "package.json"), JSON.stringify({ name: `experiment-cleanup-${mode}`, version: "1.0.0", scripts: { build: "true" } }));
    await writeFile(join(directory, "runtime.mjs"), runtimeCode);
    await writeFile(join(directory, "plugin.json"), JSON.stringify({ schema_version: 1, id: `experiment-cleanup-${mode}`, name: "Cleanup fixture",
      runtime: { protocol: "wuu-plugin-v1", command: process.execPath, args: ["runtime.mjs"] }, mcp_servers: servers,
      hooks: { SessionEnd: [{ command: `exec ${quote(process.execPath)} -e ${quote(hookCode)}` }] } }));
    const candidate = (await cli(["plugin", "experiment", "capture", "--json", "--workdir", scratch, directory])).json();
    const args = ["plugin", "experiment", "eval", "--json", "--timeout", "30s", candidate.id, candidate.candidate_id,
      "--", binary, "plugin", "experiment", "check-runtime", candidate.id, candidate.fingerprint];
    const watcher = watch(scratch);
    const reached = deadline(new Promise((resolve, reject) => {
      const inspect = () => stat(ready).then(resolve, error => { if (error.code !== "ENOENT") reject(error); });
      watcher.on("change", inspect); watcher.on("error", reject); inspect();
    }), `managed ${mode} cleanup barrier`);
    const child = spawn(binary, args, { cwd: scratch, env, stdio: ["ignore", "pipe", "pipe"] });
    const exited = once(child, "exit");
    let stdout = "", stderr = "";
    child.stdout.setEncoding("utf8").on("data", text => stdout += text);
    child.stderr.setEncoding("utf8").on("data", text => stderr += text);
    try {
      await reached; watcher.close();
      const cancelledAt = Date.now(); child.kill("SIGTERM");
      const [code, signal] = await deadline(exited, `managed ${mode} cleanup settles`, 15_000);
      commands.push({ args, code, signal, stdout, stderr });
      assert.equal(code, 0, stderr); assert.equal(JSON.parse(stdout).runs[0].status, "cancelled");
      const paths = [runtimePID, ...mcpPIDs, ...(mode === "hook" ? [hookPID] : [])];
      const pids = await Promise.all(paths.map(async path => Number(await readFile(path, "utf8"))));
      for (const pid of pids) { assert(pid > 0); await assertStopped(pid); }
      (result.managed_cleanup ??= []).push({ mode, duration_ms: Date.now() - cancelledAt, pids });
      check(mode === "hook" ? "cancellation stops SessionEnd hook and retained runtime" : "cancellation retires three connected MCP servers and runtime within one grace window", () => {});
    } finally {
      watcher.close();
      if (child.exitCode === null) { child.kill("SIGTERM"); await deadline(exited, "cleanup evaluator exit", 15_000); }
      // The acceptance fixture owns these groups. Reap them after a failed
      // assertion too, so a detected cleanup regression does not leak test work.
      for (const path of [runtimePID, ...mcpPIDs, ...(mode === "hook" ? [hookPID] : [])]) {
        try {
          const pid = Number(await readFile(path, "utf8"));
          if (pid > 0) process.kill(-pid, "SIGKILL");
        } catch (error) { if (!["ENOENT", "ESRCH"].includes(error.code)) throw error; }
      }
    }
  };
  try {
    await writeFile(source, pluginSource("A")); candidateA = await capture();
    await assert.rejects(stat(join(home, "dev", "plugins", `${candidateA.id}.json`)), { code: "ENOENT" });
    const missingTrust = await select("select", candidateA, "", false);
    check("capture keeps production trust and publication unchanged", () => assert.match(missingTrust.stderr, /no production development authorization/));
    await cli(["plugin", "dev", "--watch=false", source]);
    assert.equal(await fingerprint(), candidateA.fingerprint);
    const configPath = join(home, "config.json");
    await writeFile(configPath, JSON.stringify({ default_provider: "fixture", providers: { fixture: { type: "openai-compatible",
      base_url: `http://127.0.0.1:${provider.address().port}`, api_key: "synthetic", model: "gpt-fixture" } }, agent: { permission_mode: "unconfined" }, skills: { enabled: false } }));
    app = spawn(binary, ["app-server", "--workdir", scratch, "--config", configPath], { env, stdio: ["pipe", "pipe", "pipe"] });
    result.app_server_pid = app.pid;
    app.stderr.setEncoding("utf8").on("data", data => appStderr += data);
    createInterface({ input: app.stdout }).on("line", line => { try { const message = JSON.parse(line); transcript.push(message); events.emit("message", message); } catch { appStderr += line; } });
    await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "plugin-experiment-e2e" } });
    thread = (await rpc("thread/start", {})).thread.id;
    await runTurn("discover-manager");
    await runTurn("capture-B");
    check("agent writes and captures B while its turn and live default retain A", () => { assert(candidateB.candidate_id); assert.notEqual(candidateA.fingerprint, candidateB.fingerprint); });
    assert.equal(await fingerprint(), candidateA.fingerprint);
    const barrier = join(scratch, "parallel-barrier"); await mkdir(barrier);
    const evaluation = (await cli(["plugin", "experiment", "eval", "--json", "--concurrency", "2", "--timeout", "30s", candidateA.id,
      candidateA.candidate_id, candidateB.candidate_id, "--", process.execPath, script, "--evaluate", barrier])).json();
    const observed = await Promise.all(evaluation.runs.map(async row => JSON.parse((await readFile(row.stdout.path, "utf8")).trim())));
    check("parallel evaluators execute exact A and B tools in distinct homes and workspaces", () => {
      assert(evaluation.runs.every(row => row.status === "succeeded"), JSON.stringify(evaluation));
      assert.deepEqual(observed.map(row => row.text), ["memory:A;disk:A", "memory:B;disk:B"]);
      for (const key of ["wuu_home", "home", "cwd", "package"]) assert.notEqual(observed[0][key], observed[1][key]);
    });
    result.evaluation_report = evaluation.report_path;
    const held = begin("hold-A");
    await waitFor("held", providerCalls, call => call.stage === "hold-A" && call.number === 2, "old turn reaches model barrier");
    const selected = (await select("select", candidateB, candidateA.fingerprint)).json();
    await waitFor("message", transcript, message => message.method === "plugin/inventory/changed" && message.params?.epoch >= selected.epoch, "B publication observed by same app-server");
    heldResponse(); heldResponse = null; await finish(await held);
    check("live selection keeps the same host PID and old active turn reads A after publication", () => { assert.equal(app.pid, result.app_server_pid); assert.equal(app.exitCode, null); assert.equal(counts.get("hold-A"), 3); });
    await runTurn("next-B");
    check("next idle turn adopts B", () => assert.equal(counts.get("next-B"), 2));
    await runTurn("rollback-A"); await runTurn("next-A");
    check("agent rollback preserves its B turn and next idle turn adopts archived A", () => { assert.equal(counts.get("rollback-A"), 2); assert.equal(counts.get("next-A"), 2); });
    const stale = await select("select", candidateB, candidateB.fingerprint, false);
    check("stale compare-and-swap rejects selection", () => assert.match(stale.stderr, /selection conflict/));
    assert.equal(await fingerprint(), candidateA.fingerprint);
    await writeFile(source, pluginSource("BROKEN", true)); const broken = await capture();
    const failed = await select("select", broken, candidateA.fingerprint, false);
    check("startup failure is rejected before production publication", () => assert.match(failed.stderr, /readiness|preflight|initialize|startup/));
    assert.equal(await fingerprint(), candidateA.fingerprint);
    const stuckPIDPath = join(scratch, "stuck-initialize.pid");
    await writeFile(source, pluginSource("STUCK").replace(" initialize() {", ` async initialize() { appendFileSync(${JSON.stringify(stuckPIDPath)}, String(process.pid)); await new Promise(() => {});`));
    const stuck = await capture(), stuckStarted = Date.now();
    const stuckSelection = await select("select", stuck, candidateA.fingerprint, false);
    const stuckPID = Number(await readFile(stuckPIDPath, "utf8"));
    assert(stuckPID > 0); await assertStopped(stuckPID);
    check("startup deadline reclaims the separately grouped stuck runtime and preserves A", () => {
      assert.match(stuckSelection.stderr, /readiness|startup/);
      assert(Date.now() - stuckStarted < 40_000, "startup readiness exceeded its bounded budget and cleanup grace");
    });
    assert.equal(await fingerprint(), candidateA.fingerprint);
    await appendFile(join(candidateB.package_root, "extension.ts"), "\n// tampered after capture\n");
    const corrupt = await select("select", candidateB, candidateA.fingerprint, false);
    check("changed candidate bytes cannot replace the default", () => assert.match(corrupt.stderr, /changed after capture/));
    assert.equal(await fingerprint(), candidateA.fingerprint);
    await appendFile(join(home, "experiments", "candidates", broken.id, broken.candidate_id, "candidate.json"), " ");
    const changedMetadata = await select("select", broken, candidateA.fingerprint, false);
    check("changed candidate metadata fails its content address", () => assert.match(changedMetadata.stderr, /metadata does not match its content address/));
    await rm(join(candidateB.package_root, "extension.ts"));
    await symlink(source, join(candidateB.package_root, "extension.ts"));
    const escapingPackage = await select("select", candidateB, candidateA.fingerprint, false);
    check("candidate package symlinks are rejected before use", () => assert.match(escapingPackage.stderr, /symlink/i));
    const linkedHome = join(scratch, "linked-state"), linkedTarget = join(scratch, "linked-target");
    await mkdir(linkedHome); await mkdir(linkedTarget); await symlink(linkedTarget, join(linkedHome, "experiments"));
    const escapedCapture = await cli(["plugin", "experiment", "capture", "--json", "--workdir", scratch, source], false, { ...env, WUU_HOME: linkedHome });
    assert.match(escapedCapture.stderr, /symlink/i);
    await assert.rejects(stat(join(linkedTarget, "candidates")), { code: "ENOENT" });
    check("capture rejects a symlinked store before creating outside directories", () => {});
    const timeout = (await cli(["plugin", "experiment", "eval", "--json", "--timeout", "200ms", candidateA.id, candidateA.candidate_id,
      "--", process.execPath, "-e", "process.on('SIGTERM',()=>{}); setInterval(()=>{},1000)"])).json();
    check("evaluation deadline produces retained timeout evidence", () => assert.equal(timeout.runs[0].status, "timed_out"));
    const bounded = (await cli(["plugin", "experiment", "eval", "--json", "--output-limit", "1024", candidateA.id, candidateA.candidate_id,
      "--", process.execPath, "-e", "process.stdout.write('x'.repeat(100000))"])).json();
    check("evaluation output is bounded without confusing exit zero with quality", () => { assert.equal(bounded.runs[0].status, "succeeded"); assert.equal(bounded.runs[0].stdout.bytes, 1024); assert.equal(bounded.runs[0].stdout.truncated, true); assert.match(bounded.interpretation, /does not establish candidate quality/); });
    const pidFile = join(scratch, "cancel-processes.json"), cancelWatcher = watch(scratch);
    const processesReady = deadline(new Promise((resolve, reject) => {
      const inspect = () => readFile(pidFile, "utf8").then(text => resolve(JSON.parse(text)), error => { if (error.code !== "ENOENT") reject(error); });
      cancelWatcher.on("change", inspect); cancelWatcher.on("error", reject); inspect();
    }), "cancellable process group ready");
    const cancelArgs = ["plugin", "experiment", "eval", "--json", "--timeout", "30s", candidateA.id, candidateA.candidate_id, "--", process.execPath, "-e",
      "const fs=require('node:fs'); const child=require('node:child_process').spawn(process.execPath,['-e',\"process.on('SIGTERM',()=>{});setInterval(()=>{},1000)\"],{stdio:'ignore'}); fs.writeFileSync(process.argv[1],JSON.stringify([process.pid,child.pid])); process.on('SIGTERM',()=>{});setInterval(()=>{},1000)", pidFile];
    const cancellable = spawn(binary, cancelArgs, { cwd: scratch, env, stdio: ["ignore", "pipe", "pipe"] });
    let cancelledOutput = "", cancelledError = "";
    cancellable.stdout.setEncoding("utf8").on("data", text => cancelledOutput += text);
    cancellable.stderr.setEncoding("utf8").on("data", text => cancelledError += text);
    const processIDs = await processesReady.finally(() => cancelWatcher.close());
    const cancelledExit = once(cancellable, "exit"); cancellable.kill("SIGINT");
    const [cancelCode, cancelSignal] = await deadline(cancelledExit, "cancelled evaluation settles");
    commands.push({ args: cancelArgs, code: cancelCode, signal: cancelSignal, stdout: cancelledOutput, stderr: cancelledError });
    const cancelled = JSON.parse(cancelledOutput);
    check("explicit cancellation persists the terminal report", () => { assert.equal(cancelCode, 0); assert.equal(cancelled.runs[0].status, "cancelled"); });
    for (const pid of processIDs) await assertStopped(pid);
    check("explicit cancellation stops both command and ordinary descendant", () => {});
    const deniedRoot = join(scratch, "other-workspace"), neverRun = join(scratch, "unexpected-evaluator"); await mkdir(deniedRoot);
    await cli(["plugin", "experiment", "eval", "--json", "--workspace", deniedRoot, "--", "--workspace=", candidateA.id, candidateA.candidate_id,
      "--", process.execPath, "-e", `require('node:fs').writeFileSync(${JSON.stringify(neverRun)},'bad')`], false);
    await assert.rejects(stat(neverRun), { code: "ENOENT" });
    check("flag-like plugin IDs cannot override evaluator workspace restrictions", () => {});
    await checkManagedCleanup("hook");
    await checkManagedCleanup("mcp");
    await runTurn("after-failures");
    check("failed experiments leave A usable in the original app-server", () => assert.equal(app.exitCode, null));
    result.status = "passed";
  } catch (error) { result.error = error.stack; throw error; }
  finally {
    if (heldResponse) { try { heldResponse(); } catch {} }
    if (app && app.exitCode === null) {
      const exited = once(app, "exit"), timer = setTimeout(() => app.kill("SIGKILL"), 5000);
      app.stdin.end(); await exited; clearTimeout(timer);
    }
    provider.closeAllConnections(); await new Promise(resolve => provider.close(resolve));
    await writeFile(join(scratch, "app-server.jsonl"), transcript.map(value => JSON.stringify(value)).join("\n") + "\n");
    await writeFile(join(scratch, "provider.jsonl"), providerCalls.map(value => JSON.stringify(value)).join("\n") + "\n");
    await writeFile(join(scratch, "commands.json"), JSON.stringify(commands, null, 2));
    await writeFile(join(scratch, "app-server.stderr.log"), appStderr);
    result.provider_errors = failures;
    result.finished_at = new Date().toISOString();
    await writeFile(join(output, "result.json"), JSON.stringify(result, null, 2) + "\n");
    console.log("Evidence:", join(output, "result.json"));
  }
}
