import { connect, type Socket } from "node:net";
import { createInterface } from "node:readline";
import { once } from "node:events";
import { afterEach, expect, it, vi } from "vitest";
import { RemoteAppServerBridge, type RemoteAppServerEndpoint } from "./remoteAppServerBridge";
import type { ExtensionInventoryRecord } from "../shared/protocol";
const bridges: RemoteAppServerBridge[] = [];
const sockets: Socket[] = [];
afterEach(() => { for (const socket of sockets.splice(0)) socket.destroy(); for (const bridge of bridges.splice(0)) bridge.stop(); });
async function peer(endpoint: RemoteAppServerEndpoint, token = endpoint.token) {
  const socket = connect(Number(endpoint.address.split(":")[1]), "127.0.0.1"); sockets.push(socket);
  const lines = createInterface({ input: socket })[Symbol.asyncIterator]();
  await once(socket, "connect");
  socket.write(JSON.stringify({ token }) + "\n");
  return { socket, send: (value: unknown) => socket.write(JSON.stringify(value) + "\n"), next: async () => { const line = await lines.next(); return line.done ? undefined : JSON.parse(line.value); } };
}
function fixture(request = vi.fn(async (_cwd: string, _method: string, _params?: unknown): Promise<unknown> => ({}))) {
  const bridge = new RemoteAppServerBridge(request); bridges.push(bridge); return { bridge, request };
}
it("rejects unauthenticated local connections before dispatching requests", async () => {
  const { bridge, request } = fixture();
  const bad = await peer(await bridge.start("/workspace"), "wrong-token");
  expect(await bad.next()).toBeUndefined(); expect(request).not.toHaveBeenCalled();
});
it("rejects local CLI login before forwarding a remote request", async () => {
  const { bridge, request } = fixture();
  const phone = await peer(await bridge.start("/workspace"));
  await phone.next();
  phone.send({ id: "login", method: "engine/authenticate", params: { engine_id: "grok", method_id: "local-cli" } });
  expect(await phone.next()).toMatchObject({ id: "login", error: { message: expect.stringContaining("local desktop") } });
  expect(request).not.toHaveBeenCalled();
  phone.send({ id: "acp", method: "engine/authenticate", params: { engine_id: "cursor", method_id: "browser" } });
  expect(await phone.next()).toMatchObject({ id: "acp", result: {} });
  expect(request).toHaveBeenCalledTimes(1);
});
it.each([false, true])("rejects ChatGPT login over remote transport before desktop or headless dispatch (compact=%s)", async (compact) => {
  const { bridge, request } = fixture();
  const phone = await peer(await bridge.start("/workspace"));
  await phone.next();
  if (compact) {
    phone.send({ id: "workspaces", method: "workspace/list", params: { remote_delivery: 1 } });
    await phone.next();
    request.mockClear();
  }
  for (const action of ["start", "poll", "cancel"]) {
    phone.send({ id: action, method: `auth/chatgpt/login/${action}`, params: { login_id: "existing-attempt" } });
    expect(await phone.next()).toMatchObject({ id: action, error: { message: expect.stringContaining("local desktop") } });
  }
  expect(request).not.toHaveBeenCalled();
  phone.send({ id: "accounts", method: "auth/chatgpt/accounts" });
  expect(await phone.next()).toMatchObject({ id: "accounts", result: {} });
  expect(request).toHaveBeenCalledTimes(1);
});
it("isolates replies with colliding client ids and broadcasts the same live events", async () => {
  const { bridge } = fixture(vi.fn(async (cwd, method) => ({ cwd, method })));
  const endpoint = await bridge.start("/default");
  const a = await peer(endpoint), b = await peer(endpoint);
  expect(await a.next()).toEqual({ ready: true }); expect(await b.next()).toEqual({ ready: true });
  a.send({ id: "1", method: "thread/resume", workdir: "/alpha" });
  b.send({ id: "1", method: "thread/list", workdir: "/beta" });
  expect(await a.next()).toEqual({ id: "1", result: { cwd: "/alpha", method: "thread/resume" } });
  expect(await b.next()).toEqual({ id: "1", result: { cwd: "/beta", method: "thread/list" } });
  const event = { workdir: "/alpha", kind: "notification" as const, message: { method: "turn/completed", params: { thread_id: "t" } } };
  bridge.publish(event);
  const expected = { ...event.message, workdir: "/alpha" };
  expect(await a.next()).toEqual(expected); expect(await b.next()).toEqual(expected);
});
it("deduplicates pending requests and returns the original result for both deliveries", async () => {
  let complete!: (value: unknown) => void, admitted!: () => void;
  const started = new Promise<void>(resolve => { admitted = resolve; });
  const request = vi.fn(() => { admitted(); return new Promise(resolve => { complete = resolve; }); });
  const { bridge } = fixture(request);
  const endpoint = await bridge.start("/workspace");
  const phone = await peer(endpoint), observer = await peer(endpoint);
  await phone.next(); await observer.next();
  const command = { id: "turn-1", method: "turn/start", params: { prompt: "once" } };
  phone.send(command); phone.send(command);
  await started;
  complete({ accepted: true });
  expect(await phone.next()).toEqual({ id: "turn-1", result: { accepted: true } });
  expect(await phone.next()).toEqual({ id: "turn-1", result: { accepted: true } });
  phone.socket.destroy();
  bridge.publish({ workdir: "/workspace", kind: "notification", message: { method: "turn/completed", params: {} } });
  expect((await observer.next()).method).toBe("turn/completed"); expect(request).toHaveBeenCalledTimes(1);
});

it("keeps snapshot replies before subsequent events without waiting for promise continuations", async () => {
  const bridge = new RemoteAppServerBridge(async (_cwd, _method, _params, reply) => {
    reply({ result: { version: 1 } });
    bridge.publish({ workdir: "/workspace", kind: "notification", message: { method: "thread/updated", params: { version: 2 } } });
    return { version: 1 };
  });
  bridges.push(bridge);
  const phone = await peer(await bridge.start("/workspace"));
  await phone.next();
  phone.send({ id: "snapshot", method: "thread/resume" });
  expect(await phone.next()).toEqual({ id: "snapshot", result: { version: 1 } });
  expect((await phone.next()).params).toEqual({ version: 2 });
});

it("negotiates lazy images per connection and resolves edited attachments before dispatch", async () => {
  const image = {media_type:"image/png", data:"a".repeat(300_000)};
  const { bridge, request } = fixture(vi.fn(async (_cwd, method, params) => method === "turn/start" ? params : ({images:[image]})));
  const endpoint = await bridge.start("/workspace");
  const compact = await peer(endpoint), legacy = await peer(endpoint);
  await compact.next(); await legacy.next();
  compact.send({id:1, method:"workspace/list", params:{remote_delivery:1}});
  const projected = (await compact.next()).result;
  expect(projected.images[0].data).toBe("");
  legacy.send({id:1, method:"workspace/list"});
  expect((await legacy.next()).result.images[0]).toEqual(image);
  compact.send({id:2, method:"turn/start", params:projected});
  await compact.next();
  expect(request.mock.calls.at(-1)?.[2]).toEqual({images:[image]});
  compact.send({id:3, method:"remote/attachment/read", params:{ref:projected.images[0].remote_ref}});
  expect((await compact.next()).result.data.length).toBe(128 * 1024);
});

it("routes terminal events only to their attachment and releases its sessions on close", async () => {
  let closed!: (id: number) => void;
  const disconnected = new Promise<number>(resolve => { closed = resolve; });
  const bridge = new RemoteAppServerBridge(async (_cwd, _method, _params, _reply, owner) => ({ owner }), closed);
  bridges.push(bridge);
  const endpoint = await bridge.start('/workspace');
  const a = await peer(endpoint), b = await peer(endpoint);
  await a.next(); await b.next();
  a.send({id:1,method:'desktop/terminal/start'}); b.send({id:1,method:'desktop/terminal/start'});
  const ownerA = (await a.next()).result.owner, ownerB = (await b.next()).result.owner;
  expect(ownerA).not.toBe(ownerB);
  bridge.notifyPeer(ownerA,'desktop/terminal/event',{data:'private output'});
  bridge.notifyPeer(ownerB,'desktop/terminal/event',{data:'other output'});
  expect((await a.next()).params.data).toBe('private output');
  expect((await b.next()).params.data).toBe('other output');
  a.socket.destroy(); expect(await disconnected).toBe(ownerA);
  bridge.notifyPeer(ownerA,'desktop/terminal/event',{data:'after close'});
  bridge.notifyPeer(ownerB,'desktop/terminal/event',{data:'still connected'});
  expect((await b.next()).params.data).toBe('still connected');
});

it("owns remote activation identities and retains graph routes across workspace switches and reconnects", async () => {
  const inventory = (graph: string) => [{ id: "shell", fingerprint: "source", graph_generation: graph, desktop: {} }] as ExtensionInventoryRecord[];
  const process = (graph: string) => vi.fn(async (method: string, _params?: unknown): Promise<unknown> => method === "initialize" ? { extension_inventory: inventory(graph) } : {});
  const first = process("first"), second = process("second");
  let current = first;
  const bridge = new RemoteAppServerBridge(async (cwd, method, params, _reply, _peer, activation) => {
    if (method === "initialize") return activation.initialize(cwd, current as <T>(method: string, params?: unknown) => Promise<T>, params);
    throw new Error(`Unexpected dispatch: ${method}`);
  });
  bridges.push(bridge);
  const endpoint = await bridge.start("/a");
  const a = await peer(endpoint), b = await peer(endpoint);
  await a.next(); await b.next();
  const initialize = async (phone: typeof a, id: number, workdir: string) => {
    phone.send({ id, method: "initialize", workdir });
    expect(await phone.next()).toHaveProperty("result.extension_inventory");
  };
  const report = async (phone: typeof a, id: number, graph: string, extra: object = {}) => {
    phone.send({ id, method: "plugin/desktop-activation", workdir: "/unregistered", params: {
      id: "shell", fingerprint: "source", graph_generation: graph, activation: 1,
      phase: "registration", status: "active", renderer_id: "spoof", disconnected: true, ...extra,
    } });
    return phone.next();
  };
  await initialize(a, 1, "/a"); await initialize(b, 1, "/a");
  expect(await report(a, 2, "first")).toHaveProperty("result");
  expect(await report(b, 2, "first")).toHaveProperty("result");
  const wireA = first.mock.calls[2][1] as Record<string, unknown>;
  const wireB = first.mock.calls[3][1] as Record<string, unknown>;
  expect(wireA.renderer_id).toMatch(/^remote:/);
  expect(wireA.renderer_id).not.toBe(wireB.renderer_id);
  expect(wireA).not.toHaveProperty("disconnected");
  current = second;
  await initialize(a, 3, "/b");
  await report(a, 4, "first", { phase: "cleanup", status: "disposed" });
  expect(first.mock.calls.at(-1)?.[1]).toMatchObject({ renderer_id: wireA.renderer_id, phase: "cleanup" });
  bridge.publish({ kind: "notification", workdir: "/b", message: { method: "plugin/inventory/changed", params: { extension_inventory: inventory("next") } } });
  await a.next(); await b.next();
  expect(await report(a, 5, "next")).toHaveProperty("result");
  expect(second.mock.calls.at(-1)?.[1]).toMatchObject({ graph_generation: "next", renderer_id: wireA.renderer_id });
  expect(await report(b, 3, "next")).toHaveProperty("error");
  a.socket.destroy();
  await vi.waitFor(() => expect(second.mock.calls.at(-1)?.[1]).toEqual({ renderer_id: wireA.renderer_id, disconnected: true }));
  expect(first.mock.calls.at(-1)?.[1]).toEqual({ renderer_id: wireA.renderer_id, disconnected: true });
  const replacement = await peer(endpoint);
  await replacement.next();
  expect(await report(replacement, 1, "first", { phase: "cleanup", status: "disposed" })).toHaveProperty("error");
  await initialize(replacement, 2, "/a");
  expect(await report(replacement, 3, "second")).toHaveProperty("result");
  expect((second.mock.calls.at(-1)?.[1] as Record<string, unknown>).renderer_id).not.toBe(wireA.renderer_id);
});
