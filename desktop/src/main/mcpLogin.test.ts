import { EventEmitter } from "node:events";
import { afterEach, expect, it, vi } from "vitest";
import { MCPLoginCoordinator } from "./mcpLogin";
import type { MCPLoginPollResult, MCPLoginStartResult } from "../shared/protocol";

class Owner extends EventEmitter {
  destroyed = false;
  constructor(readonly id: number) { super(); }
  isDestroyed() { return this.destroyed; }
  destroy() { this.destroyed = true; this.emit("destroyed"); }
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

const owners: Owner[] = [];
function owner() { const value = new Owner(1); owners.push(value); return value; }
afterEach(() => { owners.splice(0).forEach(value => value.destroy()); vi.useRealTimers(); });

const listed = { servers: [{ name: "docs" }] };
const authorization = { state: "synthetic-state", authorization_url: "https://authorization.example.test/authorize" };
type Request = (method: string, params?: unknown) => Promise<unknown>;

async function admitted(flow: MCPLoginCoordinator, window: Owner, request: Request): Promise<MCPLoginStartResult> {
  const receipt = await Promise.race([flow.start(window, "docs", () => request), Promise.resolve(undefined)]);
  expect(receipt, "the opaque cancellation handle must precede asynchronous startup").toBeDefined();
  return receipt!;
}

it("admits an owned cancellation handle before metadata completes, without allowing duplicate starts", async () => {
  const list = deferred<unknown>();
  const request = vi.fn(() => list.promise);
  const open = vi.fn();
  const flow = new MCPLoginCoordinator(open, vi.fn());
  const window = owner();
  try {
    const receipt = await admitted(flow, window, request);
    expect(flow.poll(window.id, receipt.login_id)).toEqual({ status: "pending", phase: "starting" });
    expect(() => flow.poll(2, receipt.login_id)).toThrow("expired");
    await expect(flow.cancel(2, receipt.login_id)).resolves.toEqual({ ok: false });
    await expect(flow.start(window, "other", () => request)).rejects.toThrow("Finish or cancel");
    await expect(flow.cancel(window.id, receipt.login_id)).resolves.toEqual({ ok: true });
    await expect(flow.cancel(window.id, receipt.login_id)).resolves.toEqual({ ok: false });
    list.resolve(listed);
    await list.promise;
    expect(request).toHaveBeenCalledExactlyOnceWith("mcp/list");
    expect(open).not.toHaveBeenCalled();
    expect(window.listenerCount("destroyed")).toBe(0);
  } finally { window.destroy(); list.resolve(listed); }
});

it.each(["cancel", "destroy"])("does not open a late authorization result after %s and cleans its captured state once", async action => {
  const auth = deferred<unknown>(), entered = deferred<void>(), cleaned = deferred<void>();
  const request = vi.fn(async (method: string) => {
    if (method === "mcp/list") return listed;
    if (method === "mcp/auth/start") { entered.resolve(); return auth.promise; }
    if (method === "mcp/auth/cancel") { cleaned.resolve(); return { ok: true }; }
    throw new Error(`Unexpected request: ${method}`);
  });
  const open = vi.fn(), done = vi.fn();
  const flow = new MCPLoginCoordinator(open, done);
  const window = owner();
  try {
    const receipt = await admitted(flow, window, request);
    await entered.promise;
    if (action === "cancel") await flow.cancel(window.id, receipt.login_id);
    if (action === "destroy") window.destroy();
    auth.resolve(authorization);
    await cleaned.promise;
    expect(request.mock.calls.filter(([method]) => method === "mcp/auth/cancel")).toEqual([
      ["mcp/auth/cancel", { name: "docs", state: authorization.state }],
    ]);
    expect(open).not.toHaveBeenCalled();
    expect(done).not.toHaveBeenCalled();
    expect(() => flow.poll(window.id, receipt.login_id)).toThrow("expired");
  } finally { window.destroy(); auth.resolve(authorization); }
});

it("expires an admitted handle while metadata is still pending", async () => {
  vi.useFakeTimers();
  const list = deferred<unknown>();
  const request = vi.fn(() => list.promise);
  const open = vi.fn();
  const flow = new MCPLoginCoordinator(open, vi.fn());
  const window = owner();
  try {
    const receipt = await admitted(flow, window, request);
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
    list.resolve(listed);
    await list.promise;
    expect(() => flow.poll(window.id, receipt.login_id)).toThrow("expired");
    expect(request).toHaveBeenCalledOnce();
    expect(open).not.toHaveBeenCalled();
    expect(window.listenerCount("destroyed")).toBe(0);
  } finally { window.destroy(); list.resolve(listed); }
});

it("reports asynchronous startup failure through the owning handle and consumes the terminal result once", async () => {
  const list = deferred<unknown>();
  const request = vi.fn(() => list.promise);
  const open = vi.fn();
  const flow = new MCPLoginCoordinator(open, vi.fn());
  const window = owner();
  try {
    const receipt = await admitted(flow, window, request);
    list.reject(new Error("Synthetic metadata failure"));
    let result: MCPLoginPollResult | undefined;
    await vi.waitFor(() => {
      result = flow.poll(window.id, receipt.login_id);
      expect(result.status).toBe("error");
    });
    expect(result).toEqual({ status: "error", error: "Synthetic metadata failure" });
    expect(() => flow.poll(window.id, receipt.login_id)).toThrow("expired");
    expect(open).not.toHaveBeenCalled();
    expect(window.listenerCount("destroyed")).toBe(0);
  } finally { window.destroy(); list.resolve(listed); }
});

it.each([false, true])("keeps callback validation and exchange on the captured request (cancel during exchange=%s)", async cancel => {
  let callback = "";
  const opened = deferred<void>(), exchanging = deferred<void>(), finish = deferred<unknown>(), connected = deferred<void>();
  const request = vi.fn(async (method: string, params?: unknown) => {
    if (method === "mcp/list") return listed;
    if (method === "mcp/auth/start") { callback = (params as { redirect_uri: string }).redirect_uri; return authorization; }
    if (method === "mcp/auth/finish") { exchanging.resolve(); return finish.promise; }
    if (method === "mcp/connect") return { ok: true };
    if (method === "mcp/auth/cancel") return { ok: true };
    throw new Error(`Unexpected request: ${method}`);
  });
  const open = vi.fn(async () => { opened.resolve(); });
  const done = vi.fn(() => { connected.resolve(); });
  const flow = new MCPLoginCoordinator(open, done);
  const window = owner();
  try {
    const receipt = await admitted(flow, window, request);
    await opened.promise;
    await Promise.resolve();
    expect(flow.poll(window.id, receipt.login_id)).toEqual({ status: "pending", phase: "waiting" });
    expect((await fetch(`${callback}?state=wrong&code=synthetic-code`)).status).toBe(400);
    expect((await fetch(`${callback}/wrong?state=${authorization.state}&code=synthetic-code`)).status).toBe(404);
    expect(request.mock.calls.some(([method]) => method === "mcp/auth/finish")).toBe(false);
    expect((await fetch(`${callback}?state=${authorization.state}&code=synthetic-code`)).status).toBe(200);
    await exchanging.promise;
    if (cancel) await flow.cancel(window.id, receipt.login_id);
    finish.resolve({ ok: true });
    await finish.promise;
    if (cancel) {
      expect(request.mock.calls.some(([method]) => method === "mcp/connect")).toBe(false);
      expect(done).not.toHaveBeenCalled();
      expect(() => flow.poll(window.id, receipt.login_id)).toThrow("expired");
    } else {
      await connected.promise;
      expect(flow.poll(window.id, receipt.login_id)).toEqual({ status: "success" });
      expect(() => flow.poll(window.id, receipt.login_id)).toThrow("expired");
      expect(done).toHaveBeenCalledExactlyOnceWith(window.id);
    }
    expect(open).toHaveBeenCalledExactlyOnceWith(authorization.authorization_url);
    expect(request.mock.calls.filter(([method]) => method === "mcp/auth/finish")).toEqual([
      ["mcp/auth/finish", { name: "docs", state: authorization.state, code: "synthetic-code" }],
    ]);
  } finally { window.destroy(); finish.resolve({ ok: true }); }
});
