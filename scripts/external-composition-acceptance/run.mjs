#!/usr/bin/env node
// Black-box contracts: independent packed-SDK consumers, public CLI publication,
// real app-server, real worker process, disposable state, and no external model.
// Failures enumerated before implementation: implicit native provider fallback,
// provider swap stealing an admitted turn, stale durable bindings after restart,
// remote commands running locally, changed defaults moving captured processes,
// non-tool scope escalation, cancellation replay, and disable leaking a process.
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { cp, mkdir, mkdtemp, readFile, stat, symlink, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { preparePublicSDK } from "../../packages/plugin-sdk/test/public-package.mjs";

const here = fileURLToPath(new URL(".", import.meta.url));
const root = resolve(here, "../..");
const binarySource = resolve(process.env.WUU_E2E_CORE ?? join(root, "bin/wuu-architecture"));
let binary = binarySource;
const output = resolve(process.env.WUU_EXTERNAL_COMPOSITION_OUTPUT ?? join(root, ".tmp/e2e/external-composition"));
await mkdir(output, { recursive: true });
const evidence = await mkdtemp(join(output, "run-"));
const independent = await mkdtemp(join(tmpdir(), "wuu-external-composition-"));
process.env.npm_config_cache ??= join(independent, "npm-cache");
const report = {
  status: "failed", evidence, independent, scope: process.argv.slice(2), checks: [], commands: [],
  limitations: [
    "Synthetic HTTP inference and control gates; no external model or user state.",
    "Command-backend worker runs on the test machine in a distinct workspace; this is remote protocol/routing acceptance, not SSH or cloud infrastructure acceptance.",
    "host.process coverage is not a complete filesystem, PTY, or native-engine execution-world replacement.",
    "No rendered desktop UI acceptance or arbitrary stateful provider migration is claimed.",
  ],
};
const events = new EventEmitter(), observations = [], held = new Map(), servers = [];
let project, control;
const hash = bytes => createHash("sha256").update(bytes).digest("hex");
const moduleID = path => `local-${hash(path).slice(0, 24)}`;

function command(program, args, options = {}) {
  const entry = { program, args, cwd: options.cwd ?? root };
  report.commands.push(entry);
  try {
    const stdout = execFileSync(program, args, { cwd: root, encoding: "utf8", timeout: 180000, maxBuffer: 32 * 1024 * 1024, ...options });
    entry.status = "passed"; entry.stdout = stdout;
    return stdout;
  } catch (error) {
    entry.status = "failed"; entry.stdout = String(error.stdout ?? ""); entry.stderr = String(error.stderr ?? error);
    throw error;
  }
}
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
function observe(value) { observations.push(value); events.emit("observation", value); }
function userText(messages) {
  const content = messages.findLast(message => message.role === "user" && !message.name)?.content;
  return typeof content === "string" ? content : (content ?? []).map(part => part.text ?? "").join("\n");
}
async function makeServer(name, config) {
  const directory = join(independent, name), home = join(directory, "home"), workspace = join(directory, "workspace");
  await mkdir(home, { recursive: true }); await mkdir(workspace);
  const configPath = join(home, "config.json");
  const env = { PATH: process.env.PATH, HOME: directory, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
  await writeFile(configPath, JSON.stringify(config));
  let child, sequence = 0, stderr = "";
  const transcript = [], sent = [];
  const server = {
    name, home, workspace, directory, transcript,
    publish(path) { return command(binary, ["plugin", "dev", "--watch=false", path], { cwd: directory, env }); },
    async start() {
      child = spawn(binary, ["app-server", "--workdir", workspace, "--config", configPath], { env, stdio: ["pipe", "pipe", "pipe"] });
      child.stderr.setEncoding("utf8").on("data", data => { stderr += data; });
      createInterface({ input: child.stdout }).on("line", line => {
        try { const message = JSON.parse(line); transcript.push(message); events.emit(name, message); }
        catch { stderr += `\nnon-JSON stdout: ${line}`; }
      });
      return await server.rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "external-composition-acceptance" } });
    },
    async raw(method, params = {}) {
      const id = `${name}-${++sequence}`;
      const response = waitFor(name, transcript, message => message.id === id, method);
      const request = { id, method, params }; sent.push(request);
      child.stdin.write(JSON.stringify(request) + "\n");
      return await response;
    },
    async rpc(method, params) {
      let response = await server.raw(method, params);
      const deadline = Date.now() + 20000;
      // The terminal notification precedes execution-lease release. Retry only
      // the protocol's definite pre-admission rejection, never an unknown turn.
      while (method === "turn/start" && Date.now() < deadline &&
        (response.error?.message?.includes("thread execution is owned by another app-server") || response.error?.message?.includes("turn admission can be retried"))) {
        await new Promise(resolve => setTimeout(resolve, 20));
        response = await server.raw(method, params);
      }
      assert(!response.error, `${method}: ${JSON.stringify(response.error)}`);
      return response.result;
    },
    async client(plugin, method, input, raw = false) {
      const response = await server.raw("plugin/client/request", {
        id: plugin.id, fingerprint: plugin.fingerprint, graph_generation: plugin.graph_generation,
        request_id: `${name}-client-${++sequence}`, method, input,
      });
      if (raw) return response;
      assert(!response.error, `${method}: ${JSON.stringify(response.error)}`);
      return response.result.result;
    },
    terminal(turn) {
      return waitFor(name, transcript, message => ["turn/completed", "turn/error"].includes(message.method)
        && (message.params?.turn?.id === turn.id || message.params?.turn_id === turn.id), `terminal ${turn.id}`);
    },
    async turn(thread, prompt) {
      const { turn } = await server.rpc("turn/start", { thread_id: thread.id, prompt });
      return await server.terminal(turn);
    },
    async stop() {
      if (!child || child.exitCode !== null) return;
      const exited = once(child, "exit");
      child.stdin.end();
      const term = setTimeout(() => child.kill("SIGTERM"), 5000), kill = setTimeout(() => child.kill("SIGKILL"), 8000);
      await exited; clearTimeout(term); clearTimeout(kill);
    },
    async save() {
      await writeFile(join(evidence, `${name}.jsonl`), transcript.map(JSON.stringify).join("\n") + "\n");
      await writeFile(join(evidence, `${name}-requests.jsonl`), sent.map(JSON.stringify).join("\n") + "\n");
      await writeFile(join(evidence, `${name}.stderr.log`), stderr);
    },
  };
  servers.push(server); return server;
}
function inventory(catalog, name) {
  const plugin = catalog.extension_inventory.find(item => item.name === name);
  assert.equal(plugin?.runtime_state, "active", JSON.stringify(plugin));
  return plugin;
}
function completed(message) { assert.equal(message.params?.turn?.status, "completed", JSON.stringify(message)); }
function selectedEngine(path, additional = {}) {
  return { version: 4, profile: "minimal", patches: [{ modules: { [moduleID(path)]: true, ...additional } }],
    services: [{ service: "engine.wuu", major: 1, provider: moduleID(path) }] };
}

async function independentEngines(endpoint) {
  const paths = {};
  const engineSource = await readFile(join(project, "engine.ts"), "utf8");
  for (const label of ["first", "second"]) {
    paths[label] = join(project, `engine-${label}.ts`);
    await writeFile(paths[label], engineSource.replace("ACCEPTANCE_ENGINE_LABEL", label).replace("ACCEPTANCE_CONTROL_ENDPOINT", endpoint + "/engine"));
  }
  const controllerPath = join(project, "controller.ts");
  const app = await makeServer("minimal", { composition: selectedEngine(paths.first, { [moduleID(controllerPath)]: true }),
    skills: { enabled: false }, ptc: { enabled: false } });
  app.publish(paths.first); app.publish(controllerPath);
  let catalog = await app.start();
  let controller = inventory(catalog, "controller");
  const old = await app.client(controller, "create", { state: "parent", selection: selectedEngine(paths.first), paths: [paths.first] });
  const next = await app.client(controller, "create", { state: "parent", selection: selectedEngine(paths.second), paths: [paths.second] });
  for (const scope of [old, next]) {
    for (const absent of ["wuu.engines.native", "wuu.default-loop", "wuu.tools.default", "wuu.model.native", "wuu.agent.policy.default"]) {
      assert(!scope.selection.modules.includes(absent), `${absent} leaked into minimal composition`);
    }
    assert(scope.services.services.some(service => service.service === "engine.wuu" && service.provider === moduleID(scope.id === old.id ? paths.first : paths.second)));
  }
  const { thread } = await app.rpc("thread/start", { engine: "wuu", model: "provider-owned" });
  await app.client(controller, "bind", { session: thread.id, id: old.id, expected: "" });
  const { turn } = await app.rpc("turn/start", { thread_id: thread.id, prompt: "hold-old-admission" });
  await waitFor("observation", observations, item => item.phase === "run" && item.prompt === "hold-old-admission", "old engine admitted");
  await app.client(controller, "bind", { session: thread.id, id: next.id, expected: old.id });
  const stale = await app.client(controller, "bind", { session: thread.id, id: old.id, expected: old.id }, true);
  assert(stale.error, "stale compare-and-exchange changed the session binding");
  assert.equal((await app.client(controller, "dispose", { id: old.id })).draining, true);
  held.get("hold-old-admission")?.();
  completed(await app.terminal(turn));
  completed(await app.turn(thread, "after-provider-swap"));
  assert(observations.some(item => item.phase === "run" && item.prompt === "hold-old-admission" && item.label === "first"));
  assert(observations.some(item => item.phase === "run" && item.prompt === "after-provider-swap" && item.label === "second"));
  report.checks.push("minimal engine runs without model configuration, native engine/loop/tools/policy modules, or model HTTP requests",
    "session CAS changes future admission only; disposed old composition drains its already admitted engine turn");

  const broken = await app.client(controller, "create", { state: "parent", selection: {
    ...selectedEngine(paths.second), patches: [{ modules: { [moduleID(paths.second)]: false } }],
  } }, true);
  assert(broken.error, "missing explicitly selected engine was silently replaced");
  completed(await app.turn(thread, "after-failed-preparation"));
  const failure = await app.turn(thread, "external-failure");
  assert.notEqual(failure.params?.turn?.status, "completed", JSON.stringify(failure));
  assert.equal(observations.filter(item => item.phase === "run" && item.prompt === "external-failure").length, 1, "provider failure replayed");
  report.checks.push("failed candidate preparation preserves active provider; provider failure does not replay or select a native fallback");

  const { turn: cancelTurn } = await app.rpc("turn/start", { thread_id: thread.id, prompt: "hold-cancel-admission" });
  await waitFor("observation", observations, item => item.phase === "run" && item.prompt === "hold-cancel-admission", "cancel engine admitted");
  await app.rpc("turn/interrupt", { thread_id: thread.id });
  await app.terminal(cancelTurn);
  await waitFor("observation", observations, item => item.phase === "cancelled" && item.prompt === "hold-cancel-admission", "engine cancellation");
  completed(await app.turn(thread, "after-cancellation"));
  report.checks.push("turn cancellation reaches independent engine; next explicit turn remains usable without replaying cancelled work");
  await app.client(controller, "dispose", { id: next.id });
  const bound = await app.client(controller, "inspectSession", { session: thread.id });
  assert.equal(bound.id, next.id); assert.equal(bound.state, "unavailable");
  await app.stop(); catalog = await app.start(); controller = inventory(catalog, "controller");
  const persisted = await app.client(controller, "inspectSession", { session: thread.id });
  assert.equal(persisted.id, next.id); assert.equal(persisted.state, "unavailable");
  const before = observations.filter(item => item.phase === "run").length;
  const blocked = await app.raw("turn/start", { thread_id: thread.id, prompt: "must-not-fallback-after-restart" });
  if (!blocked.error) assert.notEqual((await app.terminal(blocked.result.turn)).params?.turn?.status, "completed");
  assert.equal(observations.filter(item => item.phase === "run").length, before);
  report.checks.push("retired durable composition remains unavailable after restart; saved session does not silently return to default engine");
  await app.stop();
}

function workerCommand(mode) {
  if (mode === "native") return [binary, "execution-worker"];
  if (mode === "audit") return [process.execPath, join(project, "audit-worker.ts"), process.execPath, join(project, "external-worker.ts")];
  return [process.execPath, join(project, mode === "memory" ? "memory-worker.ts" : "external-worker.ts")];
}
async function remoteProcesses(endpoint, mode = "native") {
  const source = join(project, "process.ts"), name = mode === "native" ? "remote-process" : `${mode}-backend`;
  await writeFile(source, (await readFile(source, "utf8")).replace("ACCEPTANCE_PROCESS_CONTROL_ENDPOINT", endpoint));
  const remote = join(independent, `${name}-worker-root`);
  await mkdir(remote); await writeFile(join(remote, "world.txt"), "REMOTE-WORLD");
  const config = { default_provider: "fixture", providers: { fixture: {
    type: "openai-compatible", base_url: endpoint + "/v1", api_key: "synthetic", model: "fixture",
  } }, composition: { version: 4, profile: "headless", patches: [{ modules: {
    "wuu.tools.default": false, [moduleID(source)]: true,
  } }], services: [{ service: "agent.tools", major: 1, provider: moduleID(source) }] },
    execution_environments: { default: "remote", profiles: { remote: { backend: "command",
      command: workerCommand(mode), workspace: remote } } },
    skills: { enabled: false }, ptc: { enabled: false } };
  const app = await makeServer(name, config);
  await writeFile(join(app.workspace, "world.txt"), "HOST-WORLD");
  app.publish(source); const catalog = await app.start(), plugin = inventory(catalog, "process");
  const { thread } = await app.rpc("thread/start", { permission_mode: "unconfined" });
  async function probe(input, target = thread) {
    const tag = `probe-${observations.length}-${Date.now()}`;
    completed(await app.turn(target, `ACCEPTANCE_CALL:${JSON.stringify({ tag, tool: "process_probe", input })}`));
    const observed = observations.find(item => item.phase === "tool-result" && item.tag === tag);
    assert(observed, `no tool result for ${tag}`);
    return JSON.parse(observed.text);
  }
  const remoteCommand = "printf 'WORLD='; cat world.txt; printf '\\n'; pwd; while :; do sleep 1; done";
  const admitted = await probe({ action: "start", key: "remote-build", command: remoteCommand });
  assert.equal(admitted.ok, true, JSON.stringify(admitted));
  const process = admitted.value.process;
  assert.equal(admitted.value.environment.backend, "command");
  assert.equal(admitted.value.environment.root, remote);
  assert.deepEqual(process.environment, admitted.value.environment);
  let read = await app.client(plugin, "process", { action: "read", process_id: process.id, wait_ms: 1000 });
  const outputDeadline = Date.now() + 30000;
  for (let offset = read.next_offset; !read.output.includes(remote);) {
    assert(Date.now() < outputDeadline, "process did not produce its remote marker");
    const part = await app.client(plugin, "process", { action: "read", process_id: process.id, offset_bytes: offset, wait_ms: 1000 });
    assert(["starting", "running"].includes(part.process.state), JSON.stringify(part));
    read.output += part.output; offset = part.next_offset;
  }
  assert(read.output.includes("WORLD=REMOTE-WORLD")); assert(!read.output.includes("HOST-WORLD"));
  assert.equal(process.session_id, thread.id);
  const forbidden = await app.client(plugin, "process", { action: "environment" }, true);
  assert(forbidden.error, "unscoped client acquired an admitted environment");
  const duplicate = await probe({ action: "start", key: "remote-build", command: remoteCommand });
  assert.equal(duplicate.value.process.id, process.id);
  const conflict = await probe({ action: "start", key: "remote-build", command: "printf different" });
  assert.equal(conflict.ok, false);
  const retiringCommand = "printf 'retirement-observation'; while :; do sleep 1; done";
  const retirement = await probe({ action: "start", key: "retirement-observation", command: retiringCommand });
  assert.equal(retirement.ok, true, JSON.stringify(retirement));
  report.checks.push("selected independent agent.tools replaces removed wuu.tools.default; host.process runs through real worker and remote-only sentinel",
    "environment query requires tool scope; process receipt is idempotent and rejects changed intent");

  const retained = app.client(plugin, "hold-read", { process_id: process.id });
  retained.catch(() => {});
  await waitFor("observation", observations, item => item.phase === "process-held" && item.process_id === process.id, "old process callback admitted");
  await app.rpc("config/general/update", { execution_environments: { default: "local" } });
  // Plugin handles and the app-server's process catalog have separate opaque
  // identities. Resolve this test's unique command in its captured conversation.
  const listed = await app.rpc("process/list", { thread_id: thread.id });
  const captured = listed.processes.filter(item => item.command === remoteCommand);
  assert.equal(captured.length, 1);
  const stable = await app.rpc("process/read", { thread_id: thread.id, process_id: captured[0].id, wait_ms: 0 });
  assert.equal(stable.process.cwd, remote);
  assert.equal(stable.process.status, "running");
  assert(stable.output.includes("WORLD=REMOTE-WORLD"));
  const existing = await probe({ action: "environment" });
  assert.deepEqual(existing.value, process.environment);
  const { thread: local } = await app.rpc("thread/start", { permission_mode: "unconfined" });
  const localEnvironment = await probe({ action: "environment" }, local);
  assert.equal(localEnvironment.value.backend, "local"); assert.equal(localEnvironment.value.root, app.workspace);
  held.get(`process-${process.id}`)?.();
  const settled = await retained;
  assert.equal(settled.read.process.state, "running", "general settings change stopped an admitted process before its retained callback settled");
  assert.deepEqual(settled.read.process.environment, process.environment);
  assert.equal(settled.stopped.reason, "requested_stop");
  assert.deepEqual(settled.stopped.environment, process.environment);
  const currentCatalog = await app.rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "after-settings-refresh" } });
  const currentPlugin = inventory(currentCatalog, "process");
  assert.notEqual(currentPlugin.graph_generation, plugin.graph_generation, "settings refresh did not exercise generation publication");
  const view = await app.rpc("process/list", { thread_id: thread.id });
  const retirementObserved = view.processes.find(item => item.command === retiringCommand);
  assert(retirementObserved, "accepted retirement-observation process disappeared without a record");
  report.default_update_observations ??= [];
  report.default_update_observations.push({ scenario: name, initial_plugin_generation: plugin.graph_generation,
    new_plugin_generation: currentPlugin.graph_generation, process: retirement.value.process, after_retained_callback_release: retirementObserved,
    interpretation: "New graph is active and retained callback completed; independently accepted background process must remain running at its original remote cwd." });
  assert.equal(retirementObserved.cwd, remote);
  assert.equal(retirementObserved.status, "running", "default environment change stopped unrelated accepted background work");
  assert(retirementObserved.pid > 0);
  globalThis.process.kill(retirementObserved.pid, 0);
  const marker = join(remote, "cancelled-must-not-run");
  const cancelled = await probe({ action: "preaborted", key: "cancelled", command: "printf forbidden > cancelled-must-not-run" });
  assert.equal(cancelled.ok, false); await assert.rejects(stat(marker), { code: "ENOENT" });
  report.checks.push("changing default environment affects new sessions; old session and process stay remote through an admitted retained callback, which explicitly stops it before releasing its generation",
    "pre-aborted request has no process effects; explicit stop retains remote receipt and reason");

  const bad = await probe({ action: "start", key: "missing-cwd", cwd: join(remote, "missing"), command: "printf forbidden > must-not-run-locally" });
  assert.equal(bad.ok, false); await assert.rejects(stat(join(app.workspace, "must-not-run-locally")), { code: "ENOENT" });
  report.checks.push("remote start failure never falls back to the host workspace");
  await app.stop();
  assert.throws(() => globalThis.process.kill(retirementObserved.pid, 0), { code: "ESRCH" }, "app-server close leaked an accepted backend process");
  if (mode !== "native") {
    const journal = JSON.parse(await readFile(join(remote, ".acceptance-execution", thread.id, "processes.json"), "utf8"));
    const closed = journal.find(item => item.process.command === retiringCommand)?.process;
    assert.equal(closed?.status, "stopped"); assert.equal(closed.terminal_cause, "requested_stop");
  }
  report.checks.push(`${mode} backend physically stops accepted background work on app-server shutdown, without changing its binding during settings refresh`);
}

async function monitorConsumer(endpoint) {
  const remote = join(independent, "monitor-worker-root");
  await mkdir(remote); await writeFile(join(remote, "world.txt"), "MONITOR-REMOTE-WORLD");
  const app = await makeServer("monitor", {
    default_provider: "fixture", providers: { fixture: { type: "openai-compatible", base_url: endpoint + "/v1", api_key: "synthetic", model: "fixture" } },
    composition: { version: 4, profile: "headless", patches: [{ modules: { monitor: true } }] },
    extensions: { enabled: { "plugin:bundled:monitor": true } },
    execution_environments: { default: "remote", profiles: { remote: { backend: "command", command: [binary, "execution-worker"], workspace: remote } } },
    skills: { enabled: false }, ptc: { enabled: false },
  });
  await writeFile(join(app.workspace, "world.txt"), "MONITOR-HOST-WORLD");
  const catalog = await app.start();
  const plugin = catalog.extension_inventory.find(item => item.id === "plugin:bundled:monitor" || item.name.toLowerCase() === "monitor");
  assert.equal(plugin?.runtime_state, "active", JSON.stringify(plugin));
  const { thread } = await app.rpc("thread/start", { permission_mode: "unconfined" });
  const tag = "bundled-monitor-start";
  completed(await app.turn(thread, `ACCEPTANCE_CALL:${JSON.stringify({ tag, tool: "monitor", input: {
    command: "cat world.txt; while :; do sleep 1; done", description: "Acceptance remote monitor", persistent: true,
  } })}`));
  const result = observations.find(item => item.phase === "tool-result" && item.tag === tag);
  assert(result, "monitor did not return a start receipt");
  const { task_id } = JSON.parse(result.text.split("\n")[0]);
  assert(task_id, result.text);
  let read;
  const deadline = Date.now() + 30000;
  do {
    read = await app.client(plugin, "monitor.tasks", { action: "read", task_id, thread_id: thread.id });
    assert(!read.output.includes("MONITOR-HOST-WORLD"), "Monitor forwarded host callback cwd into the remote process");
    if (read.output.includes("MONITOR-REMOTE-WORLD")) break;
    assert(Date.now() < deadline, "monitor did not publish remote output");
    // Real monitor pacing is timer-owned. The protocol read is non-blocking;
    // wait only for that documented producer's next opportunity to publish.
    await new Promise(resolve => setTimeout(resolve, 100));
  } while (true);
  assert(!read.output.includes("MONITOR-HOST-WORLD"));
  assert.equal(read.process.environment.root, remote);
  await app.client(plugin, "monitor.tasks", { action: "stop", task_id, thread_id: thread.id });
  const stopDeadline = Date.now() + 30000;
  do {
    read = await app.client(plugin, "monitor.tasks", { action: "read", task_id, thread_id: thread.id });
    if (["stopped", "failed"].includes(read.process.state)) break;
    assert(Date.now() < stopDeadline, "monitor process did not stop");
    await new Promise(resolve => setTimeout(resolve, 100));
  } while (true);
  assert.equal(read.process.reason, "requested_stop");
  report.checks.push("second independent consumer (bundled Monitor) starts, reads and stops in the captured remote workspace through public host.process");
  await app.stop();
}

async function externalFilesystem(endpoint, mode = "external") {
  const remote = join(independent, `${mode}-fs-root`); await mkdir(remote);
  const app = await makeServer(`${mode}-fs`, {
    default_provider: "fixture", providers: { fixture: { type: "openai-compatible", base_url: endpoint + "/v1", api_key: "synthetic", model: "fixture" } },
    composition: { version: 4, profile: "headless" },
    execution_environments: { default: "external", profiles: { external: { backend: "command", command: workerCommand(mode), workspace: remote } } },
    skills: { enabled: false }, ptc: { enabled: false },
  });
  await writeFile(join(app.workspace, "artifact.txt"), "HOST-MUST-REMAIN-UNCHANGED");
  await app.start(); const { thread } = await app.rpc("thread/start", { permission_mode: "unconfined" });
  for (const call of [
    { tag: `${mode}-fs-write`, tool: "write_file", input: { path: "artifact.txt", content: "EXTERNAL-FS-ARTIFACT" } },
    { tag: `${mode}-fs-read`, tool: "read_file", input: { path: "artifact.txt" } },
  ]) completed(await app.turn(thread, `ACCEPTANCE_CALL:${JSON.stringify(call)}`));
  if (mode === "memory") await assert.rejects(stat(join(remote, "artifact.txt")), { code: "ENOENT" });
  else assert.equal(await readFile(join(remote, "artifact.txt"), "utf8"), "EXTERNAL-FS-ARTIFACT");
  assert.equal(await readFile(join(app.workspace, "artifact.txt"), "utf8"), "HOST-MUST-REMAIN-UNCHANGED");
  assert(observations.some(item => item.phase === "tool-result" && item.tag === `${mode}-fs-read` && item.text.includes("EXTERNAL-FS-ARTIFACT")));
  if (mode === "audit") {
    const audit = (await readFile(join(remote, "acceptance-fs-audit.jsonl"), "utf8")).trim().split("\n").map(JSON.parse);
    for (const method of ["write_file", "read_file"]) {
      const admissions = audit.filter(item => item.phase === "admitted" && item.method === method);
      assert.equal(admissions.length, 1, `${method} was replayed through the decorator`);
      assert.equal(admissions[0].session, thread.id); assert(admissions[0].identity);
      assert.equal(audit.filter(item => item.phase === "completed" && item.request === admissions[0].request).length, 1);
    }
    report.checks.push("filesystem-only audit decorator explicitly delegates to same-world backend; each real read/write runs once and non-filesystem capabilities remain delegated");
  } else if (mode === "memory") {
    const call = { tag: "memory-fs-refuses-shell", tool: "bash", input: { command: "printf forbidden > forbidden-shell" } };
    completed(await app.turn(thread, `ACCEPTANCE_CALL:${JSON.stringify(call)}`));
    assert(observations.some(item => item.phase === "tool-result" && item.tag === call.tag && item.text.includes("unavailable")));
    await assert.rejects(stat(join(remote, "forbidden-shell")), { code: "ENOENT" });
    await assert.rejects(stat(join(app.workspace, "forbidden-shell")), { code: "ENOENT" });
    report.checks.push("memory filesystem replaces only file capability; no physical file or implicit native shell is created for an incompatible execution world");
  } else report.checks.push("independent SDK execution backend replaces native worker for real read_file/write_file calls; host file remains untouched");
  await app.stop();
}

async function backendProtocol() {
  const workspace = join(independent, "protocol-worker-root"); await mkdir(workspace);
  const child = spawn(process.execPath, [join(project, "external-worker.ts")], { stdio: ["pipe", "pipe", "pipe"] });
  const token = "a".repeat(64), transcript = [], sent = [];
  let sequence = 0, stderr = "";
  child.stderr.setEncoding("utf8").on("data", value => { stderr += value; });
  createInterface({ input: child.stdout }).on("line", line => {
    const message = JSON.parse(line); transcript.push(message); events.emit("backend-protocol", message);
  });
  const send = (id, method, data, secret = token) => {
    const frame = { id, method, data, token: secret }; sent.push(frame); child.stdin.write(JSON.stringify(frame) + "\n");
  };
  const request = async (method, data, secret = token) => {
    const id = `wire-${++sequence}`, result = waitFor("backend-protocol", transcript, message => message.id === id, method);
    send(id, method, data, secret); return await result;
  };
  const binding = { version: 4, root: workspace, session: "protocol-session", identity: "protocol-environment" };
  let record;
  try {
    assert((await request("initialize", { ...binding, version: 3 })).error);
    assert((await request("process/list", null)).error);
    const initialized = await request("initialize", binding);
    assert.deepEqual(initialized.data, { version: 4, root: workspace, capabilities: ["process.start.v1"] });
    const outside = join(independent, "protocol-outside-sentinel");
    await writeFile(outside, "OUTSIDE-UNCHANGED"); await symlink(outside, join(workspace, "outside-link"));
    const symlinkWrite = await request("execute", { actor: binding.session, permission_mode: "unconfined", call: {
      name: "write_file", arguments: JSON.stringify({ path: "outside-link", content: "must-not-write" }),
    } });
    assert(symlinkWrite.error, "fixture file operation followed an existing symlink outside its selected root");
    assert.equal(await readFile(outside, "utf8"), "OUTSIDE-UNCHANGED");
    await symlink(outside + "-missing", join(workspace, "dangling-link"));
    assert((await request("execute", { actor: binding.session, permission_mode: "unconfined", call: {
      name: "write_file", arguments: JSON.stringify({ path: "dangling-link", content: "must-not-write" }),
    } })).error);
    await assert.rejects(stat(outside + "-missing"), { code: "ENOENT" });
    const invalid = await request("process/start", { actor: binding.session, permission_mode: "unconfined", operation_id: "forbidden", command: "touch forbidden" }, "b".repeat(64));
    assert(invalid.error); await assert.rejects(stat(join(workspace, "forbidden")), { code: "ENOENT" });
    const denied = await request("process/start", { actor: binding.session, permission_mode: "read_only", operation_id: "read-only", command: "touch forbidden" });
    assert(denied.error); await assert.rejects(stat(join(workspace, "forbidden")), { code: "ENOENT" });
    const started = await request("process/start", { actor: binding.session, permission_mode: "unconfined", operation_id: "accepted-operation", command: "printf 'external-protocol-ready'; while :; do sleep 1; done" });
    assert(!started.error, JSON.stringify(started)); record = started.data;
    assert.equal(record.operation_id, "accepted-operation"); assert.equal(record.root_thread_id, binding.session);
    send(started.id, "cancel");
    const alive = await request("process/get", { id: record.id });
    assert.equal(alive.data.status, "running", "ending a completed RPC killed its accepted process");
    const output = await request("process/read", { id: record.id, options: { OffsetBytes: 0, MaxBytes: 1024, Wait: 1000000000 } });
    assert(output.data.Output.includes("external-protocol-ready"));
    const pendingID = `wire-${++sequence}`;
    const cancelledRead = waitFor("backend-protocol", transcript, message => message.id === pendingID, "cancelled pending backend read", 5000);
    send(pendingID, "process/read", { id: record.id, options: { OffsetBytes: output.data.EndOffset, MaxBytes: 1024, Wait: 30000000000 } });
    send(pendingID, "cancel");
    assert.match((await cancelledRead).error, /cancelled/);
    assert.equal((await request("process/get", { id: record.id })).data.status, "running", "cancelling an output read killed accepted work");
    const changed = await request("initialize", { ...binding, session: "other-session" }); assert(changed.error);
    const list = await request("process/list", null); assert(list.data.some(item => item.operation_id === record.operation_id));
    report.checks.push("actual external backend rejects wrong protocol/token and read-only shell before effects; pins session and preserves operation receipt",
      "accepted external process survives completed request cancellation and addressed pending-read cancellation; byte-cursor reads do not consume output");
  } finally {
    const exited = once(child, "exit"); child.stdin.end();
    const kill = setTimeout(() => child.kill("SIGKILL"), 5000);
    const [code, signal] = await exited; clearTimeout(kill);
    await writeFile(join(evidence, "external-backend-protocol.jsonl"), transcript.map(JSON.stringify).join("\n") + "\n");
    await writeFile(join(evidence, "external-backend-protocol-requests.jsonl"), sent.map(JSON.stringify).join("\n") + "\n");
    await writeFile(join(evidence, "external-backend-protocol.stderr.log"), stderr);
    assert.equal(code, 0, `backend cleanup failed: ${signal ?? stderr}`);
  }
  const journal = JSON.parse(await readFile(join(workspace, ".acceptance-execution", binding.session, "processes.json"), "utf8"));
  const stopped = journal.find(item => item.process.id === record.id)?.process;
  assert.equal(stopped.status, "stopped"); assert.equal(stopped.terminal_cause, "requested_stop");
  assert.throws(() => process.kill(record.pid, 0), { code: "ESRCH" });
  report.checks.push("transport EOF drains and physically stops accepted external process; stopped receipt remains reconcilable in provider journal");
}

try {
  report.commit = command("git", ["rev-parse", "HEAD"]).trim();
  report.source_dirty = Boolean(command("git", ["status", "--porcelain"]).trim());
  const sdk = resolve(process.env.WUU_EXTERNAL_COMPOSITION_SDK ?? join(root, "packages/plugin-sdk"));
  const prepared = await preparePublicSDK({ sdk, temporary: independent, tarball: process.env.WUU_EXTERNAL_COMPOSITION_SDK_TARBALL });
  project = prepared.project; report.fixture = project; report.sdk_sha256 = hash(await readFile(prepared.packed));
  const delegation = process.argv.includes("--delegation") || process.argv.includes("--delegation-only");
  const backend = delegation || process.argv.includes("--external-backend") || process.argv.includes("--backend-protocol-only") || process.argv.includes("--backend-integration-only");
  const files = ["controller.ts", "engine.ts", "process.ts", "policy.ts", ...(backend ? ["external-worker.ts"] : []), ...(delegation ? ["audit-worker.ts", "memory-worker.ts"] : [])];
  for (const file of files) await cp(join(here, file), join(project, file));
  report.source_sha256 = Object.fromEntries(await Promise.all(["run.mjs", ...files].map(async file => [file, hash(await readFile(join(here, file)))])));
  command(process.execPath, [prepared.tsc, "--noEmit", "--strict", "--skipLibCheck", "--target", "ES2023", "--module", "NodeNext", ...files], { cwd: project });
  report.checks.push("all independent fixtures typecheck against extracted public SDK outside repository; no Wuu internal imports");
  if (process.argv.includes("--prepare-only")) {
    report.status = "prepared";
    console.log(`WUU_EXTERNAL_COMPOSITION_FIXTURE=${project}`);
  } else if (process.argv.includes("--backend-protocol-only")) {
    await backendProtocol(); report.status = "passed";
  } else {
    const coreBytes = await readFile(binarySource);
    report.core_sha256 = hash(coreBytes); report.core_source = binarySource;
    // Integration lanes may rebuild the shared output during this run. Every
    // app-server and worker uses one byte-identical, hash-recorded snapshot.
    binary = join(independent, "wuu-under-test");
    await writeFile(binary, coreBytes, { mode: 0o755 }); report.core_snapshot = binary;
    control = createServer(async (request, response) => {
      try {
        let bytes = ""; for await (const part of request) bytes += part;
        const body = JSON.parse(bytes);
        if (request.url === "/engine") {
          observe(body);
          if (body.phase === "run" && String(body.prompt).startsWith("hold-")) {
            held.set(body.prompt, () => { held.delete(body.prompt); if (!response.writableEnded && !response.destroyed) { response.writeHead(200); response.end("{}"); } });
          } else { response.writeHead(200); response.end("{}"); }
          return;
        }
        if (request.url === "/process-held") {
          const key = `process-${body.process_id}`;
          held.set(key, () => { held.delete(key); if (!response.writableEnded && !response.destroyed) { response.writeHead(200); response.end("{}"); } });
          observe({ phase: "process-held", ...body }); return;
        }
        const prompt = userText(body.messages), marker = prompt.indexOf("ACCEPTANCE_CALL:");
        const call = marker >= 0 && body.tools?.length ? JSON.parse(prompt.slice(marker + "ACCEPTANCE_CALL:".length)) : undefined;
        observe({ phase: "model", call, body });
        const lastUser = body.messages.findLastIndex(message => message.role === "user" && !message.name);
        const tool = body.messages.slice(lastUser + 1).findLast(message => message.role === "tool");
        let delta = { role: "assistant", content: "auxiliary" }, finish_reason = "stop";
        if (call && !tool) {
          const name = body.tools.find(item => item.function?.name === call.tool || new RegExp(`_${call.tool}_[a-f0-9]+$`).test(item.function?.name))?.function.name;
          assert(name, `selected tool ${call.tool} absent: ${JSON.stringify(body.tools.map(item => item.function?.name))}`);
          delta = { role: "assistant", tool_calls: [{ index: 0, id: call.tag, type: "function", function: { name, arguments: JSON.stringify(call.input) } }] };
          finish_reason = "tool_calls";
        } else if (call) {
          const text = typeof tool.content === "string" ? tool.content : tool.content.map(part => part.text ?? "").join("");
          observe({ phase: "tool-result", tag: call.tag, text }); delta.content = text;
        }
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason }] })}\n\ndata: [DONE]\n\n`);
      } catch (error) { response.writeHead(500); response.end(String(error)); observe({ phase: "server-error", error: String(error) }); }
    });
    await new Promise(resolve => control.listen(0, "127.0.0.1", resolve));
    const endpoint = `http://127.0.0.1:${control.address().port}`;
    if (!process.argv.includes("--backend-integration-only") && !process.argv.includes("--delegation-only")) {
      await independentEngines(endpoint);
      assert(!observations.some(item => item.phase === "model"), "minimal independent engine unexpectedly invoked host model");
      await remoteProcesses(endpoint);
      await monitorConsumer(endpoint);
    }
    if (backend && !process.argv.includes("--delegation-only")) {
      await backendProtocol();
      await remoteProcesses(endpoint, "external");
      await externalFilesystem(endpoint);
    }
    if (delegation) {
      await remoteProcesses(endpoint, "audit");
      await externalFilesystem(endpoint, "audit");
      await externalFilesystem(endpoint, "memory");
    }
    if (process.argv.includes("--policy")) {
      const result = command("go", ["test", "-race", "-count=1", "-timeout=2m", "-v", "./internal/runtime", "-run", "^TestDefaultPolicyExternalComposition$"],
        { env: { ...process.env, WUU_EXTERNAL_COMPOSITION_FIXTURE: project } });
      assert.match(result, /--- PASS: TestDefaultPolicyExternalComposition /);
      assert(!result.includes("--- SKIP:"));
      report.checks.push("external agent.policy replaces removed default for retained native loop/tool consumers (Go composition acceptance)");
    }
    report.status = "passed";
  }
} catch (error) { report.error = `${error.stack ?? error}\n${error.stdout ?? ""}${error.stderr ?? ""}`; process.exitCode = 1; console.error(report.error); }
finally {
  for (const release of held.values()) release();
  for (const server of servers) { await server.stop(); await server.save(); }
  control?.closeAllConnections(); control?.close();
  await writeFile(join(evidence, "observations.jsonl"), observations.map(JSON.stringify).join("\n") + "\n");
  await writeFile(join(evidence, "report.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(`External composition ${report.status}: ${evidence}`);
}
