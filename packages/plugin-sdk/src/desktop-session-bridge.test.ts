import assert from "node:assert/strict";
import test from "node:test";
import { RuntimeHostServiceError, type RuntimeHost, type RuntimeHostCallOptions } from "./index.js";
import { desktopSessionTransport, DESKTOP_SESSION_GATEWAY_DESCRIPTOR, requireDesktopSessionGateway } from "./desktop-session-bridge.js";
import type { SessionRouteRequest, SessionRouteResult } from "./session-transport.js";

// Synthetic host admission supplies this opaque ticket to its selected provider;
// the provider helper must neither invent one nor replace its immutable intent.
const issued: SessionRouteRequest = {
  version: 1, operation: "send", operation_id: "stable-intent",
  locator: { version: 1, execution_host: "desktop-project:registered", session_id: "existing" },
  params: { session_id: "existing", request_id: "stable-intent", input: { prompt: "task" } },
  transport_ticket: { id: "host-issued-opaque-ticket", source_host: "admitted-source" },
};
const receipt: SessionRouteResult = { version: 1, operation_id: issued.operation_id, locator: issued.locator, status: "completed", result: { session_id: "existing", turn_id: "accepted-once" } };
const host = (call: (method: string, params: any, options?: RuntimeHostCallOptions) => Promise<unknown>, supported = true): RuntimeHost => ({ supports: () => supported, call } as RuntimeHost);

test("selected desktop provider forwards only its host-issued ticket through the public gateway", async () => {
  const calls: any[] = [];
  const transport = desktopSessionTransport(host(async (method, params, options) => {
    calls.push({ method, params, options });
    assert.equal(params.params.ticket_id, issued.transport_ticket!.id);
    return receipt;
  }));
  assert.deepEqual(await transport.dispatch(issued), receipt);
  assert.deepEqual(calls, [{ method: "host.service.call", params: { service: "host.desktop.session", method: "dispatch", params: { ticket_id: issued.transport_ticket!.id } }, options: { signal: undefined } }]);
  assert.deepEqual(requireDesktopSessionGateway(), { name: "host.desktop.session", major_version: 1, required: true });
  assert.equal(DESKTOP_SESSION_GATEWAY_DESCRIPTOR.methods[0].input_schema, "desktop.session.ticket.v1");
});

test("missing ticket, invalid scope, and pre-cancellation cannot synthesize admission or call the host", async () => {
  let calls = 0;
  const transport = desktopSessionTransport(host(async () => { calls++; return receipt; }));
  await assert.rejects(transport.dispatch({ ...issued, transport_ticket: undefined }), /host-issued.*ticket/i);
  await assert.rejects(transport.dispatch({ ...issued, transport_ticket: { id: "", source_host: "source" } }), /host-issued.*ticket/i);
  await assert.rejects(transport.dispatch({ ...issued, locator: { ...issued.locator, session_id: "another" } }));
  const cancelled = new AbortController(); cancelled.abort();
  await assert.rejects(transport.dispatch(issued, cancelled.signal), { name: "AbortError" });
  assert.equal(calls, 0);
});

test("disabled or unsupported gateway is explicit and never falls back to another route", async () => {
  let calls = 0;
  await assert.rejects(desktopSessionTransport(host(async () => { calls++; return receipt; }, false)).dispatch(issued), { code: "session_host_unavailable" });
  assert.equal(calls, 0);
  const error = new RuntimeHostServiceError({ code: "service_unavailable", message: "selected provider is disabled" });
  await assert.rejects(desktopSessionTransport(host(async () => { calls++; throw error; })).dispatch(issued), { code: "service_unavailable" });
  assert.equal(calls, 1);
});

test("lost response and invalid receipt retain stable intent and never automatically replay", async () => {
  for (const result of [undefined, { ...receipt, operation_id: "different" }, { ...receipt, locator: { ...issued.locator, execution_host: "another-owner" } }]) {
    let calls = 0;
    const transport = desktopSessionTransport(host(async () => { calls++; if (result === undefined) throw new Error("connection lost"); return result; }));
    const outcome = await transport.dispatch(issued);
    assert.equal(outcome.status, "unknown");
    assert.equal(outcome.operation_id, issued.operation_id);
    assert.deepEqual(outcome.locator, issued.locator);
    assert.equal(calls, 1);
  }
});

test("post-dispatch cancellation ends the wait as unknown, never claims rollback or sends cancel", async () => {
  let complete!: (result: SessionRouteResult) => void;
  const calls: string[] = [];
  const controller = new AbortController();
  const transport = desktopSessionTransport(host(async (_method, params, options) => {
    calls.push(params.method);
    assert.equal(options?.signal, controller.signal);
    return await new Promise<SessionRouteResult>(resolve => { complete = resolve; });
  }));
  const pending = transport.dispatch(issued, controller.signal);
  controller.abort();
  assert.equal((await pending).status, "unknown");
  assert.deepEqual(calls, ["dispatch"]);
  complete(receipt);
});
