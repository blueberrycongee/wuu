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
import { cp, mkdir, mkdtemp, readFile, readdir, stat, symlink, writeFile } from "node:fs/promises";
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
const ownedProcesses = new Map();

async function procIdentity(pid) {
  try {
    const raw = await readFile(`/proc/${pid}/stat`, "utf8"), fields = raw.slice(raw.lastIndexOf(")") + 2).trim().split(/\s+/);
    const cmdline = (await readFile(`/proc/${pid}/cmdline`)).toString().split("\0").filter(Boolean);
    const confirm = await readFile(`/proc/${pid}/stat`, "utf8");
    if (confirm.slice(confirm.lastIndexOf(")") + 2).trim().split(/\s+/)[19] !== fields[19]) return null;
    return { pid, state: fields[0], ppid: Number(fields[1]), pgid: Number(fields[2]), starttime: fields[19], cmdline, stat: raw };
  } catch (error) { if (["ENOENT", "ESRCH"].includes(error.code)) return null; throw error; }
}
function sameProcess(left, right) {
  return Boolean(left && right && left.pid === right.pid && left.pgid === right.pgid && left.starttime === right.starttime && JSON.stringify(left.cmdline) === JSON.stringify(right.cmdline));
}
async function ownProcess(label, pid) {
  const identity = await procIdentity(pid);
  assert(identity, `${label}: accepted process ${pid} is absent from /proc`);
  const owned = { ...identity, label };
  ownedProcesses.set(`${pid}:${identity.starttime}`, owned);
  return owned;
}
async function saveProcessEvidence(label) {
  const table = (await Promise.all((await readdir("/proc")).filter(name => /^\d+$/.test(name)).map(name => procIdentity(Number(name))))).filter(Boolean);
  const selected = new Map();
  for (const owned of ownedProcesses.values()) {
    const current = table.find(item => sameProcess(item, owned));
    if (current) selected.set(current.pid, { ...current, label: owned.label });
  }
  // Only descendants and groups of a still-verified process belong to this
  // run. Never infer ownership from a recycled PID or a broad command match.
  for (let changed = true; changed;) {
    changed = false;
    for (const item of table) if (!selected.has(item.pid)) {
      const parent = selected.get(item.ppid), leader = selected.get(item.pgid);
      const owner = parent ?? (leader?.pid === leader?.pgid ? leader : undefined);
      if (owner) { selected.set(item.pid, { ...item, label: owner.label }); changed = true; }
    }
  }
  for (const item of selected.values()) ownedProcesses.set(`${item.pid}:${item.starttime}`, item);
  const snapshots = [...ownedProcesses.values()].map(owned => ({ owned, current: table.find(item => item.pid === owned.pid) ?? null }));
  report.proc_evidence ??= [];
  const filename = `proc-${String(report.proc_evidence.length + 1).padStart(3, "0")}-${label}.json`;
  await writeFile(join(evidence, filename), JSON.stringify({ time: new Date().toISOString(), snapshots }, null, 2) + "\n");
  report.proc_evidence.push(filename);
}
async function signalOwned(owned, signal) {
  const current = await procIdentity(owned.pid);
  if (!current) return "already-gone";
  assert(sameProcess(current, owned), `refusing to signal changed process identity ${owned.pid}`);
  try { process.kill(owned.pid, signal); return "signalled"; }
  catch (error) { if (error.code === "ESRCH") return "already-gone"; throw error; }
}
async function assertProcessGone(owned) {
  const current = await procIdentity(owned.pid);
  assert(!current, `accepted process did not physically disappear: ${JSON.stringify({ owned, current })}`);
  assert.throws(() => process.kill(owned.pid, 0), { code: "ESRCH" });
}

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
  let child, childIdentity, exited, stopping, sequence = 0, stderr = "";
  const transcript = [], sent = [];
  const server = {
    name, home, workspace, directory, transcript,
    publish(path) { return command(binary, ["plugin", "dev", "--watch=false", path], { cwd: directory, env }); },
    async start() {
      child = spawn(binary, ["app-server", "--workdir", workspace, "--config", configPath], { env, stdio: ["pipe", "pipe", "pipe"] });
      exited = once(child, "exit"); stopping = undefined;
      exited.catch(() => {});
      child.stdin.on("error", error => { stderr += `\nstdin: ${error}`; });
      child.stderr.setEncoding("utf8").on("data", data => { stderr += data; });
      createInterface({ input: child.stdout }).on("line", line => {
        try { const message = JSON.parse(line); transcript.push(message); events.emit(name, message); }
        catch { stderr += `\nnon-JSON stdout: ${line}`; }
      });
      await once(child, "spawn");
      childIdentity = await ownProcess(`${name}-app-server`, child.pid);
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
      if (!child) return;
      if (!stopping) stopping = (async () => {
        await saveProcessEvidence(`${name}-before-eof`);
        const began = Date.now(), fallback = [], rescue = [];
        child.stdin.end();
        const emergency = signal => {
          fallback.push(signal);
          const action = (async () => { await saveProcessEvidence(`${name}-before-${signal}`); await signalOwned(childIdentity, signal); })();
          action.catch(() => {}); rescue.push(action);
        };
        // Rescue is only for failed fixtures. Either timer firing is a hard
        // failure, never evidence that app-server EOF cleaned its resources.
        const term = setTimeout(() => emergency("SIGTERM"), 15000), kill = setTimeout(() => emergency("SIGKILL"), 20000);
        let code, signal;
        try { [code, signal] = await exited; }
        finally { clearTimeout(term); clearTimeout(kill); await Promise.all(rescue); }
        report.app_server_shutdowns ??= [];
        report.app_server_shutdowns.push({ name, pid: child.pid, code, signal, elapsed_ms: Date.now() - began, fallback });
        await saveProcessEvidence(`${name}-after-eof`);
        assert.deepEqual(fallback, [], `${name}: app-server EOF needed forced termination`);
        assert.equal(signal, null, `${name}: app-server EOF exited on ${signal}`);
        assert.equal(code, 0, `${name}: app-server EOF exited ${code}`);
      })();
      return await stopping;
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
  const retirementIdentity = await ownProcess(`${name}-retained-managed-process`, retirementObserved.pid);
  const marker = join(remote, "cancelled-must-not-run");
  const cancelled = await probe({ action: "preaborted", key: "cancelled", command: "printf forbidden > cancelled-must-not-run" });
  assert.equal(cancelled.ok, false); await assert.rejects(stat(marker), { code: "ENOENT" });
  report.checks.push("changing default environment affects new sessions; old session and process stay remote through an admitted retained callback, which explicitly stops it before releasing its generation",
    "pre-aborted request has no process effects; explicit stop retains remote receipt and reason");

  const bad = await probe({ action: "start", key: "missing-cwd", cwd: join(remote, "missing"), command: "printf forbidden > must-not-run-locally" });
  assert.equal(bad.ok, false); await assert.rejects(stat(join(app.workspace, "must-not-run-locally")), { code: "ENOENT" });
  report.checks.push("remote start failure never falls back to the host workspace");
  await app.stop();
  await assertProcessGone(retirementIdentity);
  if (mode !== "native") {
    const journal = JSON.parse(await readFile(join(remote, ".acceptance-execution", thread.id, "processes.json"), "utf8"));
    const closed = journal.find(item => item.process.command === retiringCommand)?.process;
    assert.equal(closed?.status, "stopped"); assert.equal(closed.terminal_cause, "requested_stop");
  }
  report.checks.push(`${mode} backend physically stops accepted background work on app-server shutdown, without changing its binding during settings refresh`);
}

async function nativeDelegationShutdown() {
  const name = "native-delegation-shutdown", directory = join(independent, name), remote = join(directory, "workspace");
  await mkdir(remote, { recursive: true });
  const command = [process.execPath, join(project, "audit-worker.ts"), binary, "execution-worker"];
  const child = spawn(command[0], command.slice(1), { env: { PATH: process.env.PATH, HOME: directory, WUU_HOME: join(directory, "home") }, stdio: ["pipe", "pipe", "pipe"] });
  const exited = once(child, "exit"); exited.catch(() => {});
  const transcript = [], sent = [], accepted = [];
  let sequence = 0, stderr = "", peerIdentity;
  child.stdin.on("error", error => { stderr += `\nstdin: ${error}`; });
  child.stderr.setEncoding("utf8").on("data", value => { stderr += value; });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const message = JSON.parse(line); transcript.push(message); events.emit(name, message); }
    catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const request = async (method, data) => {
    const id = `${name}-${++sequence}`, pending = waitFor(name, transcript, message => message.id === id, method);
    const frame = { id, method, data, token: "d".repeat(64) }; sent.push(frame);
    child.stdin.write(JSON.stringify(frame) + "\n");
    const response = await pending; assert(!response.error, JSON.stringify(response)); return response.data;
  };
  await once(child, "spawn");
  const childIdentity = await ownProcess(`${name}-adapter`, child.pid);
  const binding = { version: 4, root: remote, session: "native-delegation-session", identity: "native-delegation-world" };
  report.native_delegation_shutdown = { binding, accepted, command };
  try {
    const initialized = await request("initialize", binding);
    assert.equal(initialized.version, 4); assert(initialized.capabilities.includes("process.start.v1"));
    for (let index = 0; index < 3; index++) {
      const marker = `${name}-${index}-${hash(independent).slice(0, 12)}`;
      const code = `process.on('SIGTERM', () => {}); process.stdout.write('${marker}\\n'); setInterval(() => {}, 1000);`;
      const command = `exec ${JSON.stringify(process.execPath)} -e ${JSON.stringify(code)} -- ${JSON.stringify(marker)}`;
      const record = await request("process/start", { actor: binding.session, permission_mode: "unconfined", operation_id: marker, command });
      assert.equal(record.lifecycle, "managed"); assert.equal(record.cwd, remote);
      let output = "", offset = 0;
      const deadline = Date.now() + 10000;
      while (!output.includes(marker)) {
        assert(Date.now() < deadline, `TERM-resistant process was not ready: ${marker}`);
        const read = await request("process/read", { id: record.id, options: { OffsetBytes: offset, MaxBytes: 4096, Wait: 1000000000 } });
        assert.equal(read.Process.status, "running"); output += read.Output; offset = read.EndOffset;
      }
      const identity = await ownProcess(marker, record.pid);
      assert(identity.cmdline.includes(marker)); accepted.push({ record, identity });
    }
    const live = await request("process/list", null);
    assert.equal(live.length, 3); assert(live.every(item => item.status === "running"));
    report.native_delegation_shutdown.before_eof = live;
  } finally {
    await saveProcessEvidence(`${name}-before-eof`);
    const peers = [...ownedProcesses.values()].filter(item => item.label === `${name}-adapter` && item.cmdline[0] === binary && item.cmdline[1] === "execution-worker");
    if (peers.length === 1) peerIdentity = peers[0];
    const began = Date.now(), fallback = [], rescue = [];
    child.stdin.end();
    const emergency = signal => {
      fallback.push(signal);
      const action = (async () => { await saveProcessEvidence(`${name}-before-${signal}`); await signalOwned(childIdentity, signal); })();
      action.catch(() => {}); rescue.push(action);
    };
    const term = setTimeout(() => emergency("SIGTERM"), 15000), kill = setTimeout(() => emergency("SIGKILL"), 20000);
    let code, signal;
    try { [code, signal] = await exited; }
    finally { clearTimeout(term); clearTimeout(kill); await Promise.all(rescue); }
    report.native_delegation_shutdown.exit = { code, signal, fallback, elapsed_ms: Date.now() - began };
    await writeFile(join(evidence, `${name}.jsonl`), transcript.map(JSON.stringify).join("\n") + "\n");
    await writeFile(join(evidence, `${name}-requests.jsonl`), sent.map(JSON.stringify).join("\n") + "\n");
    await writeFile(join(evidence, `${name}.stderr.log`), stderr);
    await saveProcessEvidence(`${name}-after-eof`);
    assert.deepEqual(fallback, [], "native delegate EOF required harness termination");
    assert.equal(signal, null); assert.equal(code, 0, `native delegate EOF failed: ${stderr}`);
  }
  assert(peerIdentity, "native delegate was not observed as an actual child process");
  await assertProcessGone(childIdentity); await assertProcessGone(peerIdentity);
  for (const item of accepted) {
    await assertProcessGone(item.identity);
    const journal = join(directory, ".wuu-execution", binding.identity, binding.session, "runtime", "processes", `${item.record.id}.json`);
    item.terminal_receipt = JSON.parse(await readFile(journal, "utf8"));
    assert.equal(item.terminal_receipt.status, "stopped");
    assert.equal(item.terminal_receipt.terminal_cause, "requested_stop");
    assert(item.terminal_receipt.stopped_at);
  }
  report.checks.push("explicit SDK delegation to native worker drains three TERM-resistant managed processes only on transport EOF; adapter, native worker and managed processes disappear, terminal receipts persist and adapter exits 0 without harness signals");
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
  const exited = once(child, "exit"); exited.catch(() => {});
  const token = "a".repeat(64), transcript = [], sent = [];
  let sequence = 0, stderr = "";
  child.stdin.on("error", error => { stderr += `\nstdin: ${error}`; });
  child.stderr.setEncoding("utf8").on("data", value => { stderr += value; });
  createInterface({ input: child.stdout }).on("line", line => {
    try { const message = JSON.parse(line); transcript.push(message); events.emit("backend-protocol", message); }
    catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const send = (id, method, data, secret = token) => {
    const frame = { id, method, data, token: secret }; sent.push(frame); child.stdin.write(JSON.stringify(frame) + "\n");
  };
  const request = async (method, data, secret = token) => {
    const id = `wire-${++sequence}`, result = waitFor("backend-protocol", transcript, message => message.id === id, method);
    send(id, method, data, secret); return await result;
  };
  const binding = { version: 4, root: workspace, session: "protocol-session", identity: "protocol-environment" };
  let record, managedIdentity;
  await once(child, "spawn");
  const childIdentity = await ownProcess("external-backend-protocol-worker", child.pid);
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
    managedIdentity = await ownProcess("external-backend-protocol-managed", record.pid);
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
    await saveProcessEvidence("external-backend-protocol-before-eof");
    child.stdin.end();
    let forced = false, rescue;
    const kill = setTimeout(() => {
      forced = true;
      rescue = (async () => { await saveProcessEvidence("external-backend-protocol-before-SIGKILL"); await signalOwned(childIdentity, "SIGKILL"); })();
      rescue.catch(() => {});
    }, 15000);
    const [code, signal] = await exited; clearTimeout(kill); await rescue;
    await writeFile(join(evidence, "external-backend-protocol.jsonl"), transcript.map(JSON.stringify).join("\n") + "\n");
    await writeFile(join(evidence, "external-backend-protocol-requests.jsonl"), sent.map(JSON.stringify).join("\n") + "\n");
    await writeFile(join(evidence, "external-backend-protocol.stderr.log"), stderr);
    await saveProcessEvidence("external-backend-protocol-after-eof");
    report.backend_protocol_shutdown = { code, signal, forced };
    assert.equal(forced, false, "backend EOF required harness SIGKILL");
    assert.equal(signal, null, `backend cleanup failed: ${signal}`);
    assert.equal(code, 0, `backend cleanup failed: ${signal ?? stderr}`);
  }
  const journal = JSON.parse(await readFile(join(workspace, ".acceptance-execution", binding.session, "processes.json"), "utf8"));
  const stopped = journal.find(item => item.process.id === record.id)?.process;
  assert.equal(stopped.status, "stopped"); assert.equal(stopped.terminal_cause, "requested_stop");
  await assertProcessGone(managedIdentity);
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
      await nativeDelegationShutdown();
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
  for (const server of servers) {
    try { await server.stop(); }
    catch (error) {
      report.status = "failed"; process.exitCode = 1;
      (report.cleanup_errors ??= []).push(`${server.name}: ${error.stack ?? error}`);
    }
    await server.save();
  }
  control?.closeAllConnections(); control?.close();
  await writeFile(join(evidence, "observations.jsonl"), observations.map(JSON.stringify).join("\n") + "\n");
  await writeFile(join(evidence, "report.json"), JSON.stringify(report, null, 2) + "\n");
  if (report.status === "failed" && ownedProcesses.size) {
    // Preserve the failure first. Cleanup can only target this run's observed
    // identities, and a changed cmdline/PGID/starttime is never kill authority.
    await saveProcessEvidence("failure-before-owned-cleanup");
    report.fixture_cleanup = [];
    for (const owned of ownedProcesses.values()) {
      try { report.fixture_cleanup.push({ owned, outcome: await signalOwned(owned, "SIGKILL") }); }
      catch (error) { report.fixture_cleanup.push({ owned, outcome: "not-signalled", error: String(error) }); }
    }
    await saveProcessEvidence("failure-after-owned-cleanup");
    await writeFile(join(evidence, "report.json"), JSON.stringify(report, null, 2) + "\n");
  }
  console.log(`External composition ${report.status}: ${evidence}`);
}
