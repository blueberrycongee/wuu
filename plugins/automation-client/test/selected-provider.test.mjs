import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

// Failure cases: cron silently writes the default provider, an unavailable
// selected provider falls back locally, or forwarding loses caller cancellation.
const bundled = await build({
  entryPoints: [fileURLToPath(new URL("../src/index.ts", import.meta.url))],
  alias: { "@wuu/plugin-sdk": fileURLToPath(new URL("../../../packages/plugin-sdk/src/index.ts", import.meta.url)) },
  bundle: true, format: "esm", platform: "node", target: "node22.7", write: false,
});
const { default: plugin } = await import(`data:text/javascript;base64,${Buffer.from(bundled.outputFiles[0].text).toString("base64")}`);
const output = response => JSON.parse(response.result.content[0].text);

test("cron routes all actions to the selected scheduler with the caller signal", async () => {
  const calls = [], signal = new AbortController().signal;
  const replacementTask = { id: "replacement-task", prompt: "Provider-owned result" };
  const host = { async call(method, params, options) {
    calls.push({ method, params, options });
    assert.equal(method, "host.service.call");
    assert.equal(params.service, "automation.scheduler");
    assert.equal(params.method, "request");
    assert.equal(options.signal, signal);
    switch (params.params.method) {
      case "automation.list": return { tasks: [replacementTask], workspace: { root: "/other" } };
      case "automation.create": return replacementTask;
      case "automation.remove": return { ok: true };
      default: throw new Error("unexpected request");
    }
  } };
  const invoke = arguments_ => plugin.executeTool({ tool_id: "cron", arguments: arguments_, session_id: "origin-session" }, host, { signal });
  assert.deepEqual(output(await invoke({ action: "list" })), [replacementTask]);
  assert.deepEqual(output(await invoke({ action: "add", mode: "thread_heartbeat", prompt: "Wake", cron: "* * * * *" })), { action: "cron", task: replacementTask });
  assert.equal(calls[1].params.params.input.heartbeat_thread_id, "origin-session");
  assert.deepEqual(output(await invoke({ action: "remove", id: " replacement-task " })), { action: "cron", removed: "replacement-task" });
  assert.equal(calls.length, 3);
});

test("unavailable selected scheduler fails once without local work or retry", async () => {
  const expected = Object.assign(new Error("selected scheduler unavailable"), { code: "service_unavailable" });
  let calls = 0;
  const host = { async call() { calls++; throw expected; } };
  await assert.rejects(plugin.executeTool({ tool_id: "cron", arguments: { action: "add", prompt: "Never schedule locally", cron: "* * * * *" } }, host, { signal: new AbortController().signal }), error => error === expected);
  assert.equal(calls, 1);
});
