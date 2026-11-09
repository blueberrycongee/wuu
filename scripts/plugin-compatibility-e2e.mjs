#!/usr/bin/env node
// Retained old package + shipped JS against current Go host, without rebuilding
// either historical artifact. Synthetic settings only; no models or credentials.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const fixture = join(root, "scripts/fixtures/plugin-compatibility");
const output = resolve(process.env.WUU_COMPATIBILITY_EVIDENCE ?? join(root, ".tmp/plugin-compatibility"));
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "home"), temporary = join(scratch, "tmp");
await mkdir(home); await mkdir(temporary);
const env = { ...process.env, HOME: home, XDG_CONFIG_HOME: join(home, ".config"), XDG_DATA_HOME: join(home, ".local/share"), WUU_HOME: join(home, ".wuu"), TMPDIR: temporary, GOMAXPROCS: "2" };
const report = { status: "failed", checks: [], artifacts: {}, limitations: ["Runtime process/settings/protocol compatibility; not rendered desktop compatibility or every historical SDK/service major."] };
const transcript = [];
let child, stderr = "", sequence = 0;
try {
  const provenance = JSON.parse(await readFile(join(fixture, "2026-10-22/provenance.json"), "utf8"));
  report.historicalRevision = provenance.revision;
  for (const [file, expected] of Object.entries(provenance.files)) {
    const digest = createHash("sha256").update(await readFile(join(fixture, "2026-10-22", file))).digest("hex");
    assert.equal(digest, expected, `Frozen artifact changed: ${file}`);
    report.artifacts[file] = digest;
  }
  // Rebuilding the old SDK here would erase the old/new boundary. It is read
  // directly from the retained package, including its original package.json.
  child = spawn(process.execPath, [join(fixture, "probe.mjs")], { cwd: scratch, env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.setEncoding("utf8").on("data", text => { stderr += text; });
  const pending = new Map();
  createInterface({ input: child.stdout }).on("line", line => {
    try {
      const message = JSON.parse(line);
      transcript.push({ direction: "plugin-to-host", ...message });
      if (message.method) {
        assert.equal(message.method, "host.service.call");
        assert.equal(message.params.method, "call");
        const { service, params } = message.params;
        let result;
        if (service === "host.settings.get") {
          assert.deepEqual(params, { key: "label" });
          result = { value: "additive-settings", optional_future_field: { ignored: true } };
        } else {
          assert.equal(service, "host.settings.list");
          assert.deepEqual(params, {});
          result = { entries: { label: "additive-settings" }, revision: "new-host-revision", scope_revisions: { user: "r1", workspace: "r2" }, restart_required: false, optional_future_field: true };
        }
        send({ id: message.id, result });
      } else {
        const waiter = pending.get(message.id);
        if (waiter) { pending.delete(message.id); clearTimeout(waiter.timer); waiter.resolve(message); }
      }
    } catch (error) {
      for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(error); }
      pending.clear();
    }
  });
  function send(message) {
    transcript.push({ direction: "host-to-plugin", ...message });
    child.stdin.write(JSON.stringify(message) + "\n");
  }
  async function rpc(method, params) {
    const id = `compat-${++sequence}`;
    const reply = new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(new Error(`Timeout: ${method}; ${stderr}`)); }, 5000);
      pending.set(id, { resolve, reject, timer });
    });
    send({ id, method, params });
    const message = await reply;
    assert(!message.error, JSON.stringify(message.error));
    return message.result;
  }
  const initialized = await rpc("initialize", { protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 2, supported_host_services: ["host.service.call"], plugin_id: "historical-probe", plugin_root: fixture, project_root: scratch, wuu_home: home, optional_future_field: { accepted: true } });
  assert.equal(initialized.protocol_version, 3);
  assert.equal(initialized.lifecycle_version, 2);
  await rpc("prepare");
  await rpc("activate");
  const { output: observed } = await rpc("capability.invoke", { capability: "plugin.client.request", input: {}, execution_id: "compatibility-callback", optional_future_field: true });
  assert.equal(observed.prepared, "additive-settings");
  assert.equal(observed.value, "additive-settings");
  assert.deepEqual(observed.entries, { label: "additive-settings" });
  await rpc("shutdown");
  const exited = once(child, "exit");
  child.stdin.end();
  await exited;
  assert.equal(child.exitCode, 0, stderr);
  report.checks.push("historical SDK ignores additive initialize, invocation, settings get/list response fields");

  const tests = [
    "TestHistoricalPluginArtifactOnCurrentHost", "TestPluginCompatibilityProtocolBoundaries",
    "TestFailedCandidateClosesStartedProcessesAndPreservesOldGeneration",
    "TestLegacyCompositionAddsExtractedProviders", "TestExtractedProviderMigrationHonorsExplicitChoices",
    "TestLegacyModelAdapterMigrationPreservesExternalSelection", "TestLegacyJournalMigrationRecovery",
  ];
  // Isolate runtime HOME, not the compiler's dependency/build caches. Otherwise
  // this focused gate rebuilds the entire module and uploads a synthetic cache.
  const caches = spawnSync("go", ["env", "-json", "GOCACHE", "GOPATH", "GOMODCACHE"], { cwd: root, encoding: "utf8" });
  assert.equal(caches.status, 0, `Go toolchain unavailable: ${caches.error ?? caches.stderr}`);
  Object.assign(env, JSON.parse(caches.stdout));
  report.currentHostRevision = spawnSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).stdout.trim();
  report.workingTreeDirty = Boolean(spawnSync("git", ["status", "--porcelain"], { cwd: root, encoding: "utf8" }).stdout.trim());
  report.nodeVersion = process.version;
  const execution = spawnSync("go", ["test", "-json", "-p", "2", "-count=1", "-run", `^(${tests.join("|")})$`, "./internal/runtime", "./internal/composition", "./internal/execution"], { cwd: root, env, encoding: "utf8", timeout: 180000, maxBuffer: 16 << 20 });
  await writeFile(join(scratch, "go-test.jsonl"), execution.stdout ?? "");
  await writeFile(join(scratch, "go-test.stderr.log"), execution.stderr ?? "");
  assert.equal(execution.status, 0, `Go host compatibility failed: ${execution.error ?? execution.stderr}; see ${scratch}/go-test.jsonl`);
  const events = execution.stdout.trim().split("\n").map(line => JSON.parse(line));
  for (const name of tests) {
    assert(events.some(event => event.Test === name && event.Action === "pass"), `Required gate did not pass: ${name}`);
  }
  report.checks.push("current Go host: configure/prepare/activate/invoke, failed upgrade, reload, retained settings, revoke, version diagnostics, no selected-provider fallback");
  report.checks.push("legacy composition projection and execution journal migration preserve existing selections/history");
  report.status = "passed";
} catch (error) {
  report.error = String(error.stack ?? error);
  process.exitCode = 1;
} finally {
  if (child && child.exitCode === null) child.kill("SIGKILL");
  await writeFile(join(scratch, "wire.jsonl"), transcript.map(frame => JSON.stringify(frame)).join("\n") + "\n");
  await writeFile(join(scratch, "plugin.stderr.log"), stderr);
  await writeFile(join(scratch, "report.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(`${report.status}: ${scratch}/report.json`);
  if (report.error) console.error(report.error);
}
