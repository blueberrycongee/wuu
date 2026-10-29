import assert from "node:assert/strict";
import test from "node:test";
import {
  getProcessEnvironment, handleRuntimeRequest, readProcess, sendProcessEvent,
  startProcess, stopProcess, RuntimeHostServiceError,
  type PluginProcessEnvironment, type PluginProcessSnapshot,
  type RuntimeHost, type RuntimeHostCallOptions,
} from "./index.js";

// Public failure cases: losing callback identity/cancellation, inventing a local
// binding on an older host, retrying an unknown start, or cancelling an accepted
// process implicitly when its originating call finishes.
test("process helpers preserve the version-1 wire API and accepted handle", async () => {
  const calls: unknown[] = [];
  const snapshot: PluginProcessSnapshot = { id: "process-1", session_id: "thread-1", state: "running", exit_code: 0 };
  const host = { call: async (method: string, params: unknown, options?: RuntimeHostCallOptions) => {
    calls.push({ method, params, options });
    return snapshot;
  } } as RuntimeHost;
  const input = { key: "build", command: "npm run build", timeout_ms: 0 };
  const cancellation = new AbortController();
  const options = { signal: cancellation.signal };
  assert.equal(await startProcess(host, input, options), snapshot);
  cancellation.abort();
  assert.deepEqual(calls, [{ method: "host.service.call", params: { service: "host.process", method: "start", params: input }, options }],
    "an accepted handle is not automatically stopped when the call signal aborts");
  await readProcess(host, { process_id: snapshot.id, offset_bytes: 7, wait_ms: 10 });
  await stopProcess(host, snapshot.id);
  const message = { request_id: "event-1", input: { prompt: "Build ended" } };
  await sendProcessEvent(host, snapshot.id, message);
  assert.deepEqual(calls.slice(1), [
    { method: "host.service.call", params: { service: "host.process", method: "read", params: { process_id: snapshot.id, offset_bytes: 7, wait_ms: 10 } }, options: undefined },
    { method: "host.service.call", params: { service: "host.process", method: "stop", params: { process_id: snapshot.id } }, options: undefined },
    { method: "host.service.call", params: { service: "host.process", method: "send", params: { process_id: snapshot.id, message } }, options: undefined },
  ]);
});

test("environment metadata is supplied by the selected host, never inferred or replaced", async () => {
  const environment: PluginProcessEnvironment = { backend: "ssh", root: "/remote/workspace", identity: "pinned-environment" };
  const calls: unknown[] = [];
  const unsupported = new RuntimeHostServiceError({ code: "method_not_found", message: "environment is not supported" });
  let failure: Error | undefined;
  const host = { call: async (method: string, params: unknown) => {
    calls.push({ method, params });
    if (failure) throw failure;
    return environment;
  } } as RuntimeHost;
  assert.equal(await getProcessEnvironment(host), environment);
  assert.deepEqual(calls, [{ method: "host.service.call", params: { service: "host.process", method: "environment", params: {} } }]);
  failure = unsupported;
  await assert.rejects(getProcessEnvironment(host), error => error === unsupported);
  assert.equal(calls.length, 2, "an old host must not trigger a fallback probe");
  const lostReply = new Error("execution transport disconnected; the last operation may have completed");
  failure = lostReply;
  await assert.rejects(startProcess(host, { key: "install", command: "npm ci" }), error => error === lostReply);
  assert.equal(calls.length, 3, "an unknown start outcome must not retry on any backend");
});

for (const cancel of ["callback", "request"] as const) {
  test(`process helpers retain authenticated callback scope and ${cancel} cancellation`, async () => {
    const callback = new AbortController();
    const request = new AbortController();
    const observed: { params: unknown; signal?: AbortSignal }[] = [];
    const host = { call: async (_method: string, params: unknown, options?: RuntimeHostCallOptions) => {
      observed.push({ params, signal: options?.signal });
      return { backend: "local", root: "/workspace" };
    } } as RuntimeHost;
    const response = await handleRuntimeRequest({
      initialize: () => ({ protocol_version: 3 }),
      invokeService: async (_input, scoped) => {
        await getProcessEnvironment(scoped, { signal: request.signal });
        return {};
      },
    }, { id: "call", method: "service.invoke", params: {
      service: "example.build", method: "build", caller: "upstream", execution_id: "untrusted-copy",
      caller_scope: { id: "upstream-id", plugin_id: "upstream", kind: "tool", session_id: "other-thread" },
    } }, host, { executionId: "host-admitted-execution", signal: callback.signal });
    assert.ok("result" in response);
    assert.deepEqual(observed[0]?.params, {
      service: "host.process", method: "environment", params: {}, execution_id: "host-admitted-execution",
    });
    assert.equal(observed[0]?.signal?.aborted, false);
    (cancel === "callback" ? callback : request).abort();
    assert.equal(observed[0]?.signal?.aborted, true);
    assert.equal(observed.length, 1);
  });
}
