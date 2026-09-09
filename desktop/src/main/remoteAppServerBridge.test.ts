import { connect, type Socket } from "node:net";
import { createInterface } from "node:readline";
import { once } from "node:events";
import { afterEach, expect, it, vi } from "vitest";
import { RemoteAppServerBridge, type RemoteAppServerEndpoint } from "./remoteAppServerBridge";
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
