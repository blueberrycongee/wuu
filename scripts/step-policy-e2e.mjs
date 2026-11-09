#!/usr/bin/env node
// Public single-file loading + real CLI, native file tool and selected loop.
// Failure cases: policy ordering, stop short-circuit, propagate/isolate errors,
// cancellation during policy, duplicate observation, and unload/reload admission.
// Only the HTTP model and the policy probes are fixtures; no real model calls.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, writeFile, cp } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(process.env.WUU_E2E_CORE || join(root, "bin/wuu"));
const output = resolve(process.env.WUU_POLICY_E2E_OUTPUT || join(root, ".tmp/e2e/step-policy"));
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "state");
const workspace = join(scratch, "workspace");
await mkdir(home);
await mkdir(workspace);
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
const probePath = join(scratch, "policy-events.jsonl");
await writeFile(probePath, "");
const calls = [], publications = [], executions = [];
const result = { status: "failed", scratch, checks: [], model: "synthetic HTTP", limitations: ["CLI and process acceptance only; no research improvement or desktop rendering claim"] };

async function command(args, label, timeout = 30000) {
  const process = spawn(binary, args, { env, stdio: ["ignore", "pipe", "pipe"] });
  let stdout = "", stderr = "", timedOut = false;
  process.stdout.on("data", value => { stdout += value; });
  process.stderr.on("data", value => { stderr += value; });
  const timer = setTimeout(() => { timedOut = true; process.kill("SIGTERM"); }, timeout);
  const kill = setTimeout(() => process.kill("SIGKILL"), timeout + 3000);
  try {
    const exit = await new Promise((resolve, reject) => {
      process.once("error", reject);
      process.once("exit", (code, signal) => resolve({ code, signal }));
    });
    await writeFile(join(scratch, `${label}.stdout`), stdout);
    await writeFile(join(scratch, `${label}.stderr`), stderr);
    return { args, ...exit, timedOut, stdout, stderr };
  } finally { clearTimeout(timer); clearTimeout(kill); }
}

const provider = createServer(async (request, response) => {
  try {
    let bytes = "";
    for await (const part of request) bytes += part;
    const body = JSON.parse(bytes);
    const prompt = body.messages.filter(message => message.role === "user").map(message => typeof message.content === "string" ? message.content : "").join("\n");
    const tag = body.tools?.length ? prompt.match(/policy-case:([a-z-]+)/)?.[1] ?? "auxiliary" : "auxiliary";
    const ordinal = calls.filter(call => call.tag === tag).length;
    calls.push({ tag, body });
    const repeat = tag === "repeat" || tag === "unloaded";
    let delta = { role: "assistant", content: `model:${tag}` }, finish = "stop";
    if (repeat && ordinal < 2) {
      assert(body.tools.some(tool => tool.function?.name === "read_file"), "selected native read_file missing");
      delta = { role: "assistant", tool_calls: [{ index: 0, id: `read-${ordinal}`, type: "function",
        function: { name: "read_file", arguments: JSON.stringify({ path: "evidence.txt" }) } }] };
      finish = "tool_calls";
    }
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: finish }] })}\n\ndata: [DONE]\n\n`);
  } catch (error) { response.writeHead(500); response.end(String(error)); }
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));

function probe(name, priority, errorPolicy) {
  return `import { appendFileSync } from "node:fs";
import type { RuntimePlugin, AgentStepPolicyInput } from "@wuu/plugin-sdk";
export default {
  initialize() { return { protocol_version: 3, capabilities: [{ id: "agent.step.policy", kind: "decision", version: 1, priority: ${priority}, error_policy: ${JSON.stringify(errorPolicy)} }] }; },
  async invokeCapability({ input }, _host, execution) {
    const step = input as AgentStepPolicyInput;
    const tag = step.messages.filter(message => message.role === "user").map(message => message.content ?? "").join("\\n").match(/policy-case:([a-z-]+)/)?.[1];
    appendFileSync(${JSON.stringify(probePath)}, JSON.stringify({ name: ${JSON.stringify(name)}, tag, step: step.step_index, thread: step.thread_id }) + "\\n");
    if (${JSON.stringify(name)} === "first") {
      if (tag === "halt") return { output: { stop: true, reason: "fixture_halt", content: "policy:halt" } };
      if (tag === "error") throw new Error("fixture-policy-error");
      if (tag === "cancel") await new Promise((_resolve, reject) => {
        execution.signal.addEventListener("abort", () => reject(new Error("fixture-policy-cancelled")), { once: true });
        execution.signal.throwIfAborted();
      });
    }
    if (${JSON.stringify(name)} === "isolated" && tag === "isolate") throw new Error("fixture-isolated-error");
    return { output: { stop: false } };
  },
} satisfies RuntimePlugin;
`;
}
const probes = async tag => (await readFile(probePath, "utf8")).trim().split("\n").filter(Boolean).map(JSON.parse).filter(value => value.tag === tag);
async function publish(path, label) {
  const run = await command(["plugin", "dev", "--watch=false", path], `publish-${label}`);
  publications.push(run);
  assert.equal(run.code, 0, run.stderr);
}
async function execute(tag, timeout = "10s") {
  const run = await command(["exec", "--config", join(home, "config.json"), "--workdir", workspace,
    "--max-turns", "5", "--timeout", timeout, "--json", `policy-case:${tag}`], `exec-${tag}`);
  executions.push(run);
  return run;
}
try {
  result.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  await writeFile(join(workspace, "evidence.txt"), "Repeated observation fixture.\n");
  await writeFile(join(home, "config.json"), JSON.stringify({ default_provider: "fixture",
    providers: { fixture: { type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}`,
      api_key: "synthetic", model: "gpt-test" } }, ptc: { enabled: false }, skills: { enabled: false } }));
  const candidate = join(scratch, "repeated-observation.ts");
  await cp(join(root, "examples/plugins/repeated-observation.ts"), candidate);
  await publish(candidate, "repeated");
  for (const [name, priority, policy] of [["last", -100, "propagate"], ["first", 100, "propagate"], ["isolated", 200, "isolate"]]) {
    const path = join(scratch, `${name}.ts`);
    await writeFile(path, probe(name, priority, policy));
    await publish(path, name);
  }
  const repeated = await execute("repeat");
  assert.equal(repeated.code, 0, repeated.stderr);
  assert(repeated.stdout.includes("Stopped because two consecutive identical tool calls"), repeated.stdout);
  assert.equal(calls.filter(call => call.tag === "repeat").length, 2);
  const events = await probes("repeat");
  assert.deepEqual(events.map(event => [event.step, event.name]), [[0, "isolated"], [0, "first"], [0, "last"], [1, "isolated"], [1, "first"], [1, "last"], [2, "isolated"], [2, "first"]]);
  assert(calls.filter(call => call.tag === "repeat").every(call => JSON.stringify(call.body).includes("this task does not support polling")));
  result.checks.push("two actual native file reads; stop before third model request; ordered policies short-circuit; request transform also active");
  const halted = await execute("halt");
  assert.equal(halted.code, 0, halted.stderr);
  assert(halted.stdout.includes("policy:halt"));
  assert.equal(calls.filter(call => call.tag === "halt").length, 0);
  assert.deepEqual((await probes("halt")).map(event => event.name), ["isolated", "first"]);
  result.checks.push("first stop skips later policy and inference");
  const failed = await execute("error");
  assert.notEqual(failed.code, 0);
  assert((failed.stdout + failed.stderr).includes("fixture-policy-error"));
  assert.equal(calls.filter(call => call.tag === "error").length, 0);
  assert.deepEqual((await probes("error")).map(event => event.name), ["isolated", "first"]);
  const isolated = await execute("isolate");
  assert.equal(isolated.code, 0, isolated.stderr);
  assert.deepEqual((await probes("isolate")).map(event => event.name), ["isolated", "first", "last"]);
  assert.equal(calls.filter(call => call.tag === "isolate").length, 1);
  result.checks.push("propagated failure blocks inference; isolated failure permits later policy and model");
  // The CLI deadline includes cold process/graph preparation. Leave room to
  // enter the policy; the probe below proves cancellation reached that phase.
  const cancelled = await execute("cancel", "10s");
  assert.notEqual(cancelled.code, 0);
  assert.equal(cancelled.timedOut, false, "CLI failed to cancel its own policy invocation");
  assert.equal(calls.filter(call => call.tag === "cancel").length, 0);
  assert.deepEqual((await probes("cancel")).map(event => event.name), ["isolated", "first"]);
  result.checks.push("CLI timeout cancels external policy without admitting model or later policy");
  await writeFile(candidate, `import type { RuntimePlugin } from "@wuu/plugin-sdk";
export default { initialize() { return { protocol_version: 3, capabilities: [] }; } } satisfies RuntimePlugin;\n`);
  await publish(candidate, "removed-capability");
  const unloaded = await execute("unloaded");
  assert.equal(unloaded.code, 0, unloaded.stderr);
  assert(unloaded.stdout.includes("model:unloaded"));
  assert.equal(calls.filter(call => call.tag === "unloaded").length, 3);
  result.checks.push("single-file reload removes policy; subsequent execution reaches third model request");
  result.status = "passed";
  console.log(`PASS ${result.checks.length} step policy checks`);
} catch (error) {
  result.error = error.stack; process.exitCode = 1; console.error(error.stack);
} finally {
  provider.closeAllConnections();
  await new Promise(resolve => provider.close(resolve));
  await writeFile(join(scratch, "results.json"), JSON.stringify({ ...result, publications, executions, calls }, null, 2));
  console.log(`Evidence: ${scratch}`);
}
