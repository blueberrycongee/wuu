import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

// A tiny protocol harness, not a substitute for the host's trust or service registry.
function runtime(directory, route) {
  const child = spawn(process.execPath, [new URL(`${directory}/dist/runtime.js`, import.meta.url).pathname], {
    stdio: ["pipe", "pipe", "inherit"],
  });
  const pending = new Map();
  let sequence = 0;
  function send(frame) { child.stdin.write(`${JSON.stringify(frame)}\n`); }
  createInterface({ input: child.stdout }).on("line", async (line) => {
    const frame = JSON.parse(line);
    if (frame.method) {
      try {
        send({ id: frame.id, result: await route(frame) });
      } catch (error) {
        send({ id: frame.id, error: { message: String(error) } });
      }
      return;
    }
    const waiter = pending.get(frame.id);
    pending.delete(frame.id);
    if (frame.error) waiter?.reject(new Error(frame.error.message));
    else waiter?.resolve(frame.result);
  });
  child.on("exit", (code) => {
    for (const waiter of pending.values()) waiter.reject(new Error(`runtime exited: ${code}`));
    pending.clear();
  });
  return {
    request(method, params) {
      return new Promise((resolve, reject) => {
        const id = `smoke-${++sequence}`;
        pending.set(id, { resolve, reject });
        send({ id, method, params });
      });
    },
    stop() { child.stdin.end(); child.kill(); },
  };
}

const provider = runtime("text-analysis", () => { throw new Error("unexpected provider host call"); });
const consumer = runtime("writing-report", async (frame) => {
  assert.equal(frame.method, "host.service.call");
  assert.equal(frame.params.service, "example.text.analysis");
  return provider.request("service.invoke", { ...frame.params, caller: "writing-report-example" });
});
const timeout = setTimeout(() => {
  console.error("composition smoke timed out");
  provider.stop(); consumer.stop(); process.exitCode = 1;
}, 10_000);
try {
  const params = {
    protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 1,
    plugin_root: ".", project_root: ".", wuu_home: ".",
    supported_host_services: ["host.service.call"],
  };
  const provided = await provider.request("initialize", { ...params, plugin_id: "text-analysis-example" });
  const consumed = await consumer.request("initialize", { ...params, plugin_id: "writing-report-example" });
  assert.equal(provided.provided_services[0].name, consumed.required_services[0].name);
  assert.equal(consumed.required_services[0].major_version, 1);
  await provider.request("activate");
  await consumer.request("activate");
  const report = await consumer.request("tool.execute", {
    tool_id: "writing-report", tool: "writing-report", call_id: "example", cwd: ".",
    arguments: { text: "Hello world.\n\nReady to publish." },
  });
  assert.equal(report.result.content[0].text, "31 characters, 5 whitespace-separated words, 2 paragraphs.");
  await assert.rejects(provider.request("service.invoke", {
    service: "example.text.analysis", method: "measure", caller: "writing-report-example", params: { text: 42 },
  }), /text must be a string/);
  await consumer.request("shutdown");
  await provider.request("shutdown");
  console.log("PASS: provider/consumer initialization, routed report, invalid input, and shutdown");
} finally {
  clearTimeout(timeout);
  consumer.stop(); provider.stop();
}
