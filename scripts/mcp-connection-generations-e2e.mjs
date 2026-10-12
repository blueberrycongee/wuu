#!/usr/bin/env node
// Build bin/wuu first, then run: node scripts/mcp-connection-generations-e2e.mjs
// Real app-server process, disposable state, loopback MCP peers, no real credentials.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { watch } from "node:fs";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const output = resolve(root, process.env.WUU_MCP_E2E_OUTPUT || ".tmp/e2e/mcp-connection-generations");
await mkdir(output, { recursive: true });
const report = {
  started_at: new Date().toISOString(),
  binary, core_sha256: createHash("sha256").update(await readFile(binary)).digest("hex"),
  cases: [], status: "failed",
};

function waitFor(events, event, history, predicate, label, timeout = 10_000) {
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

// Startup diagnostics provide an acknowledgement for every queued operation,
// including one skipped without network I/O. Watch acknowledgements rather than
// sleeping before asserting that a disconnected server remained disconnected.
async function waitForStartup(home, names) {
  const path = join(home, "log", "debug.log");
  await new Promise((resolve, reject) => {
    const watcher = watch(path, check);
    const timer = setTimeout(() => finish(new Error("Timed out: background startup completion")), 10_000);
    function finish(error) { clearTimeout(timer); watcher.close(); error ? reject(error) : resolve(); }
    async function check() {
      try {
        const log = await readFile(path, "utf8");
        if (names.every(name => log.includes(`mcp server "${name}" `))) finish();
      } catch (error) { finish(error); }
    }
    check();
  });
}

async function runCase(action, blockedMethod, queued = false) {
  const scratch = await mkdtemp(join(output, `${queued ? "queued-" : ""}${action}-${blockedMethod.replaceAll("/", "-")}-`));
  const home = join(scratch, "state");
  await mkdir(home);
  const result = { action, blockedMethod, queued, scratch, checks: [], status: "failed" };
  const events = new EventEmitter(), transcript = [], requests = [], wire = [];
  let sequence = 0, child, stderr = "";
  const held = new Map(), didHold = new Set();
  const names = queued ? ["queue_1", "queue_2", "queue_3", "queue_4", "queue_5"] : ["race", "retained"];
  const sessions = new Map();
  function record(value) { wire.push(value); events.emit("wire", value); }
  const peer = createServer(async (request, response) => {
    const name = request.url.slice(1);
    const session = request.headers["mcp-session-id"];
    if (request.method !== "POST") {
      record({ verb: request.method, name, session });
      response.writeHead(request.method === "DELETE" ? 204 : 405).end();
      return;
    }
    let body = "";
    for await (const chunk of request) body += chunk;
    const rpc = JSON.parse(body);
    const call = { verb: "POST", name, session, method: rpc.method, id: rpc.id };
    record(call);
    const reply = result => response.end(JSON.stringify({ jsonrpc: "2.0", id: rpc.id, result }));
    response.setHeader("Content-Type", "application/json");
    let send;
    switch (rpc.method) {
      case "server/discover":
        send = () => response.end(JSON.stringify({ jsonrpc: "2.0", id: rpc.id, error: { code: -32601, message: "legacy fixture" } }));
        break;
      case "initialize": {
        const revision = (sessions.get(name) || 0) + 1;
        sessions.set(name, revision);
        response.setHeader("Mcp-Session-Id", `${name}-${revision}`);
        send = () => reply({ protocolVersion: "2025-06-18", capabilities: { tools: {} }, serverInfo: { name: "loopback", version: "1" } });
        break;
      }
      case "tools/list": {
        const count = queued ? sessions.get(name) : session === "race-1" || name === "retained" ? 1 : 2;
        send = () => reply({ tools: Array.from({ length: count }, (_, index) => ({
          name: `source_${index}`, description: `Fixture ${session}`, inputSchema: { type: "object", properties: {} },
        })) });
        break;
      }
      default:
        send = () => response.writeHead(202).end();
    }
    if (rpc.method === blockedMethod && !didHold.has(name) && (queued ? didHold.size < 4 : name === "race")) {
      didHold.add(name);
      held.set(name, send);
      response.on("close", () => {
        if (!response.writableEnded) record({ event: "blocked-request-cancelled", name, method: rpc.method });
      });
      record({ event: "blocked", name, method: rpc.method });
      return;
    }
    send();
  });
  await new Promise(resolve => peer.listen(0, "127.0.0.1", resolve));
  const endpoint = `http://127.0.0.1:${peer.address().port}`;
  const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home };
  async function rpc(method, params = {}) {
    const request = { id: `mcp-e2e-${++sequence}`, method, params };
    requests.push(request);
    child.stdin.write(JSON.stringify(request) + "\n");
    const message = await waitFor(events, "message", transcript, value => value.id === request.id, method);
    assert(!message.error, `${method}: ${JSON.stringify(message.error)}`);
    return message.result;
  }
  async function stopChild() {
    if (!child || child.exitCode !== null || child.signalCode !== null) return;
    const exited = once(child, "exit");
    const timer = setTimeout(() => child.kill("SIGKILL"), 8_000);
    child.stdin.end();
    try {
      const [code, signal] = await exited;
      assert.equal(signal, null, "app-server shutdown exceeded its fixture deadline");
      assert.equal(code, 0, stderr);
    } finally { clearTimeout(timer); }
  }
  try {
    const configPath = join(home, "config.json");
    await writeFile(configPath, JSON.stringify({
      default_provider: "fixture",
      providers: { fixture: { type: "openai-compatible", base_url: endpoint, model: "fixture", api_key: "synthetic-unused" } },
      mcp_servers: Object.fromEntries(names.map(name => [name, { url: `${endpoint}/${name}`, transport: "http" }])),
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
    await rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "mcp-lifecycle-e2e" } });
    if (queued) {
      await waitFor(events, "wire", wire, () => didHold.size === 4, "four active background MCP barriers");
      const target = names.find(name => !didHold.has(name));
      assert(target, "the startup concurrency limit must leave one server queued");
      result.target = target;
      const changed = await rpc(`mcp/${action}`, { name: target });
      result.actionStatus = changed.status;
      assert.equal(changed.status.connected, action !== "disconnect");
      for (const release of held.values()) release();
      held.clear();
      await waitForStartup(home, names);
      const listed = await rpc("mcp/list");
      result.afterRelease = listed.servers;
      const current = listed.servers.find(server => server.name === target);
      assert.equal(current.connected, action !== "disconnect");
      assert.equal(current.tool_count, action === "disconnect" ? 0 : 1);
      assert.equal(sessions.get(target) || 0, action === "disconnect" ? 0 : 1,
        "queued startup must not create a connection after a newer control operation");
      for (const server of listed.servers.filter(server => server.name !== target)) {
        assert.equal(server.connected, true, "unrelated queued work must complete");
        assert.equal(server.tool_count, 1);
      }
      result.checks.push("four-connection startup limit leaves the target queued",
        "newer control operation supersedes queued startup before admission",
        "all unrelated startup connections finish");
      const reconnected = await rpc("mcp/connect", { name: target });
      assert.equal(reconnected.status.connected, true);
      assert.equal(reconnected.status.tool_count, action === "disconnect" ? 1 : 2);
      const disconnected = await rpc("mcp/disconnect", { name: target });
      assert.equal(disconnected.status.connected, false);
      assert.equal(disconnected.status.state, "stopped");
      result.checks.push("explicit later reconnect and disconnect still work");
      await stopChild();
    } else {
      await waitFor(events, "wire", wire, value => value.event === "blocked", "background MCP barrier");
      if (action === "close") {
        await stopChild();
      } else {
        const changed = await rpc(`mcp/${action}`, { name: "race" });
        result.actionStatus = changed.status;
        assert.equal(changed.status.connected, action !== "disconnect");
        if (action !== "disconnect") assert.equal(changed.status.tool_count, 2);
      }
      // Check before releasing the response: receiving it must not be necessary
      // for user cancellation or supersession to take effect.
      await waitFor(events, "wire", wire, value => value.event === "blocked-request-cancelled", "old request cancellation before release", 2_000);
      result.checks.push("pending background request cancelled before the peer response");
      held.get("race")();
      held.clear();
      if (action !== "close") {
        // Explicitly connect the unaffected server to settle its startup attempt.
        const retained = await rpc("mcp/connect", { name: "retained" });
        assert.equal(retained.status.connected, true);
        assert.equal(retained.status.tool_count, 1);
        const listed = await rpc("mcp/list");
        result.afterRelease = listed.servers;
        const race = listed.servers.find(server => server.name === "race");
        assert.equal(race.connected, action !== "disconnect");
        assert.equal(race.tool_count, action === "disconnect" ? 0 : 2);
        result.checks.push("released stale response does not restore the old catalog", "unrelated MCP server remains usable");
        const reconnected = await rpc("mcp/connect", { name: "race" });
        assert.equal(reconnected.status.connected, true);
        assert.equal(reconnected.status.tool_count, 2);
        const disconnected = await rpc("mcp/disconnect", { name: "race" });
        assert.equal(disconnected.status.connected, false);
        assert.equal(disconnected.status.state, "stopped");
        result.checks.push("explicit later reconnect and disconnect still work");
        await stopChild();
      }
    }
    result.checks.push("app-server shuts down cleanly");
    result.status = "passed";
  } catch (error) {
    result.error = error.stack;
  } finally {
    for (const release of held.values()) release();
    try { await stopChild(); } catch (error) { result.cleanupError = error.stack; result.status = "failed"; }
    peer.closeAllConnections();
    await new Promise(resolve => peer.close(resolve));
    await writeFile(join(scratch, "rpc.json"), JSON.stringify({ requests, transcript }, null, 2) + "\n");
    await writeFile(join(scratch, "wire.json"), JSON.stringify(wire, null, 2) + "\n");
    await writeFile(join(scratch, "stderr.log"), stderr);
    await writeFile(join(scratch, "result.json"), JSON.stringify(result, null, 2) + "\n");
  }
  console.log(`${result.status}: ${action} during ${queued ? "queued startup before " : ""}${blockedMethod}`);
  return result;
}

for (const action of ["disconnect", "connect", "refresh", "close"]) {
  for (const method of ["initialize", "tools/list"]) report.cases.push(await runCase(action, method));
}
for (const action of ["disconnect", "connect", "refresh"]) {
  report.cases.push(await runCase(action, "tools/list", true));
}
report.status = report.cases.every(result => result.status === "passed") ? "passed" : "failed";
report.finished_at = new Date().toISOString();
await writeFile(join(output, "results.json"), JSON.stringify(report, null, 2) + "\n");
console.log(`${report.status}: ${join(output, "results.json")}`);
if (report.status !== "passed") process.exitCode = 1;
