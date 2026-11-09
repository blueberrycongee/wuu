import assert from "node:assert/strict";
import test from "node:test";
import { remoteSessionTransport, validateSessionRoute, type SessionRouteRequest, type SessionRouteResult } from "./session-transport.js";

const request: SessionRouteRequest = {
  version: 1, operation: "send", operation_id: "delivery-1",
  locator: { version: 1, execution_host: "personal", session_id: "conversation-1" },
  params: { request_id: "delivery-1", session_id: "conversation-1", input: { prompt: "hello" } },
};
const reply = (input = request): SessionRouteResult => ({
  version: 1, operation_id: input.operation_id, locator: input.locator,
  status: "completed", result: { state: "queued", session_id: "conversation-1", queue_id: "queue-1" },
});

test("route validation rejects identity/intent disagreement before network effects", () => {
  assert.throws(() => validateSessionRoute({ ...request, params: { ...request.params, request_id: "different" } }));
  assert.throws(() => validateSessionRoute({ ...request, locator: { ...request.locator, session_id: "different" } }));
  assert.throws(() => validateSessionRoute({ ...request, version: 2 }));
  assert.throws(() => validateSessionRoute({ ...request, operation: "cancel", params: { session_id: "conversation-1" } }));
  assert.throws(() => validateSessionRoute({ ...request, operation: "list", params: {} }));
});

test("successful create receipts must identify the created session", async () => {
  const create: SessionRouteRequest = { version: 1, operation: "create", operation_id: "new-1",
    locator: { version: 1, execution_host: "personal" },
    params: { request_id: "new-1", visibility: "user", context_source: "fresh" } };
  const transport = remoteSessionTransport(() => ({ isAttached: () => true,
    call: async () => ({ version: 1, operation_id: create.operation_id, locator: create.locator, status: "completed", result: {} }) }));
  assert.equal((await transport.dispatch(create)).status, "unknown");
});

test("a selected remote route is never substituted with local or another host", async () => {
  let calls = 0;
  const transport = remoteSessionTransport(host => host === "personal" ? {
    isAttached: () => false, call: async () => { calls++; return reply(); },
  } : undefined);
  await assert.rejects(transport.dispatch(request), { code: "session_host_offline" });
  await assert.rejects(transport.dispatch({ ...request, locator: { ...request.locator, execution_host: "other" } }), { code: "session_host_unavailable" });
  assert.equal(calls, 0);
});

test("lost acknowledgement is unknown and never automatically replays a command", async () => {
  let calls = 0;
  const transport = remoteSessionTransport(() => ({
    isAttached: () => true, call: async () => { calls++; throw new Error("connection lost"); },
  }));
  assert.equal((await transport.dispatch(request)).status, "unknown");
  assert.equal(calls, 1);
});

test("remote receipt must match operation and execution authority", async () => {
  for (const changed of [{ ...reply(), operation_id: "other" }, { ...reply(), locator: { ...request.locator, execution_host: "other" } }]) {
    const transport = remoteSessionTransport(() => ({ isAttached: () => true, call: async () => changed }));
    assert.equal((await transport.dispatch(request)).status, "unknown");
  }
});

test("cancellation before dispatch has no effect; cancellation after dispatch does not claim rollback", async () => {
  let calls = 0;
  let complete!: (value: SessionRouteResult) => void;
  const transport = remoteSessionTransport(() => ({
    isAttached: () => true,
    call: async () => { calls++; return await new Promise<SessionRouteResult>(resolve => { complete = resolve; }); },
  }));
  const before = new AbortController(); before.abort();
  await assert.rejects(transport.dispatch(request, before.signal), { name: "AbortError" });
  assert.equal(calls, 0);
  const after = new AbortController();
  const pending = transport.dispatch(request, after.signal);
  after.abort();
  assert.equal((await pending).status, "unknown");
  assert.equal(calls, 1);
  complete(reply());
});

test("adapter preserves stable operation and pinned workdir through the existing RPC client", async () => {
  const calls: unknown[][] = [];
  const transport = remoteSessionTransport(() => ({
    isAttached: () => true,
    call: async (...args: unknown[]) => { calls.push(args); return reply(); },
  }), { workdir: () => "/remote/project" });
  assert.deepEqual(await transport.dispatch(request), reply());
  assert.equal(calls[0][0], "session/federation");
  assert.deepEqual(calls[0][1], request);
  assert.equal(calls[0][3], "/remote/project");
});
