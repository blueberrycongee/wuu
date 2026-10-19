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

// The preview lane is ephemeral and peer-owned, unlike replayable chat RPC.
// Exercise lease recovery and credit limits before adding the capture bridge.
import { RemoteActivityPreviews, type RemotePreviewSource } from "./remoteActivityPreview";
import type { ActivitySession } from "../shared/protocol";
import type { NativePreviewFrame } from "../shared/nativePreview";
import { createRemotePreviewSource } from "./remotePreviewSources";
import type { BrowserHostCoordinator } from "./browserHostWindows";

const previewActivity: ActivitySession = {
  id: "activity", execution_id: "run", thread_id: "thread", workdir: "/workspace",
  kind: "cua", target: "Finder", process_id: 1, window_id: 10,
  state: "active", controller: "agent", created_at: "2026-10-19T00:00:00Z", updated_at: "2026-10-19T00:00:00Z",
};
function previewFixture() {
  let callbacks!: { frame(frame: NativePreviewFrame): void; state(state: "paused" | "error" | "ended", message?: string): void };
  const source = { start: vi.fn(), stop: vi.fn(), acknowledgeFrame: vi.fn(), requestKeyframe: vi.fn() } satisfies RemotePreviewSource;
  const notify = vi.fn((_owner: number, _method: string, _params: unknown) => true);
  const previews = new RemoteActivityPreviews({
    notify,
    createSource: (_activity, handlers) => { callbacks = handlers; return source; },
  });
  const frame = (sequence: number, data = Buffer.from([1]), keyframe = sequence === 1) => callbacks.frame({
    sequence, generation: 1, configuration: 1, codec: "avc1.42E01F", keyframe,
    timestamp: sequence * 1000, width: 960, height: 600, description: keyframe ? [1, 66, 224, 31] : undefined, data,
  });
  return { previews, source, notify, frame };
}

it("keeps preview pixels unicast and bounds native capture to three unacknowledged frames", () => {
  const { previews, source, notify, frame } = previewFixture();
  const { stream_id } = previews.open(1, previewActivity);
  previews.request(1, "desktop/activity/preview/renew", { stream_id });
  try {
    frame(1); frame(2); frame(3);
    expect(notify.mock.calls.filter(call => call[1] === "desktop/activity/preview/frame")).toHaveLength(3);
    expect(source.acknowledgeFrame.mock.calls).toEqual([[1], [2]]);
    expect(() => previews.request(2, "desktop/activity/preview/ack", { stream_id, sequence: 1 })).toThrow("owner");
    expect(source.acknowledgeFrame.mock.calls).toEqual([[1], [2]]);
    previews.request(1, "desktop/activity/preview/ack", { stream_id, sequence: 1 });
    expect(source.acknowledgeFrame.mock.calls).toEqual([[1], [2], [3]]);
    frame(4);
    previews.request(1, "desktop/activity/preview/ack", { stream_id, sequence: 1 });
    expect(source.acknowledgeFrame.mock.calls).toEqual([[1], [2], [3]]);
    previews.request(1, "desktop/activity/preview/ack", { stream_id, sequence: 2, recovery: true });
    expect(source.requestKeyframe).toHaveBeenCalledOnce();
    expect(notify.mock.calls.every(call => call[0] === 1)).toBe(true);
  } finally { previews.shutdown(); }
});

it("expires abandoned previews but keeps an explicitly renewed static view alive", () => {
  vi.useFakeTimers();
  const { previews, source } = previewFixture();
  try {
    const { stream_id } = previews.open(1, previewActivity);
    vi.advanceTimersByTime(4_000);
    previews.request(1, "desktop/activity/preview/renew", { stream_id });
    vi.advanceTimersByTime(4_999);
    expect(source.stop).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(source.stop).toHaveBeenCalledOnce();
    expect(() => previews.request(1, "desktop/activity/preview/renew", { stream_id })).toThrow();
  } finally { previews.shutdown(); vi.useRealTimers(); }
});

it("ends the exact target on execution replacement and rejects an in-flight stale activity snapshot", async () => {
  const { previews, source, frame, notify } = previewFixture();
  const opened = previews.open(1, previewActivity);
  previews.request(1, "desktop/activity/preview/renew", opened);
  let resolve!: (activities: ActivitySession[]) => void;
  const pending = previews.openCurrent(2, "/workspace", {
    thread_id: "thread", activity_id: "activity", execution_id: "run",
  }, () => new Promise(done => { resolve = done; }));
  previews.updateActivity({ ...previewActivity, execution_id: "next-run", updated_at: "2026-10-19T00:00:01Z" });
  expect(source.stop).toHaveBeenCalledOnce();
  frame(1);
  expect(notify.mock.calls.filter(call => call[1] === "desktop/activity/preview/frame")).toHaveLength(0);
  resolve([previewActivity]);
  await expect(pending).rejects.toThrow("current");
  previews.shutdown();
});

it("fails oversized or blocked video cleanly instead of dropping a dependent H264 frame", () => {
  const first = previewFixture();
  const firstOpened = first.previews.open(1, previewActivity);
  first.previews.request(1, "desktop/activity/preview/renew", firstOpened);
  first.frame(1, Buffer.alloc(128 * 1024 + 1));
  expect(first.source.stop).toHaveBeenCalledOnce();
  expect(first.notify.mock.calls.at(-1)?.[2]).toMatchObject({ state: "error" });
  first.previews.shutdown();
  const second = previewFixture();
  const secondOpened = second.previews.open(1, previewActivity);
  second.previews.request(1, "desktop/activity/preview/renew", secondOpened);
  second.notify.mockReturnValue(false);
  second.frame(1);
  expect(second.source.stop).toHaveBeenCalledOnce();
  second.previews.shutdown();
});

it("closes preview capture when its peer or workspace disappears without touching another peer", () => {
  const { previews, source } = previewFixture();
  previews.open(1, previewActivity);
  previews.closeOwner(2);
  expect(source.stop).not.toHaveBeenCalled();
  previews.dropWorkdir("/elsewhere");
  expect(source.stop).not.toHaveBeenCalled();
  previews.closeOwner(1);
  expect(source.stop).toHaveBeenCalledOnce();
  previews.shutdown();
});


it("starts capture only after the viewer installs its stream and renews", () => {
  const { previews, source, frame, notify } = previewFixture();
  const opened = previews.open(1, previewActivity);
  expect(source.start).not.toHaveBeenCalled();
  frame(1);
  expect(notify.mock.calls.filter(call => call[1] === "desktop/activity/preview/frame")).toHaveLength(0);
  previews.request(1, "desktop/activity/preview/renew", opened);
  expect(source.start).toHaveBeenCalledOnce();
  previews.request(1, "desktop/activity/preview/renew", opened);
  expect(source.start).toHaveBeenCalledOnce();
  frame(1);
  expect(notify.mock.calls.filter(call => call[1] === "desktop/activity/preview/frame")).toHaveLength(1);
  previews.shutdown();
});

it("retains a recovery keyframe when native capture restarts with its network window full", () => {
  const { previews, source, notify, frame } = previewFixture();
  const opened = previews.open(1, previewActivity);
  previews.request(1, "desktop/activity/preview/renew", opened);
  frame(1); frame(2); frame(3);
  frame(4, Buffer.from([4]), true);
  expect(source.stop).not.toHaveBeenCalled();
  expect(notify.mock.calls.filter(call => call[1] === "desktop/activity/preview/frame")).toHaveLength(3);
  previews.request(1, "desktop/activity/preview/ack", { ...opened, sequence: 1 });
  expect(notify.mock.calls.filter(call => call[1] === "desktop/activity/preview/frame")).toHaveLength(4);
  expect(source.acknowledgeFrame.mock.calls).toEqual([[1], [2]]);
  previews.request(1, "desktop/activity/preview/ack", { ...opened, sequence: 2 });
  expect(source.acknowledgeFrame.mock.calls.at(-1)).toEqual([4]);
  previews.shutdown();
});

it("honors activity changes during a cross-context snapshot routed to its owning workdir", async () => {
  const { previews, source } = previewFixture();
  let resolve!: (activities: ActivitySession[]) => void;
  const pending = previews.openCurrent(1, "/caller-context", {
    thread_id: "thread", activity_id: "activity", execution_id: "run",
  }, () => new Promise(done => { resolve = done; }));
  previews.updateActivity({ ...previewActivity, state: "stopped", controller: "none", updated_at: "2026-10-19T00:00:01Z" });
  resolve([previewActivity]);
  await expect(pending).rejects.toThrow("live preview");
  expect(source.start).not.toHaveBeenCalled();
  previews.shutdown();
});

it("does not let a delayed open replace a newer viewer or survive peer disconnect", async () => {
  const { previews, source } = previewFixture();
  let resolve!: (activities: ActivitySession[]) => void;
  const input = { thread_id: "thread", activity_id: "activity", execution_id: "run" };
  const stale = previews.openCurrent(1, "/workspace", input, () => new Promise(done => { resolve = done; }));
  const current = await previews.openCurrent(1, "/workspace", input, async () => [previewActivity]);
  resolve([previewActivity]);
  await expect(stale).rejects.toThrow("superseded");
  previews.request(1, "desktop/activity/preview/renew", current);
  expect(source.start).toHaveBeenCalledOnce();
  const disconnected = previews.openCurrent(1, "/workspace", input, () => new Promise(done => { resolve = done; }));
  previews.closeOwner(1);
  resolve([previewActivity]);
  await expect(disconnected).rejects.toThrow("superseded");
  previews.shutdown();
});

it("releases a browser subscription closed while its cached frame is replayed during start", () => {
  const unsubscribe = vi.fn();
  const browser = {
    subscribePreview: vi.fn((_activity: ActivitySession, frame: (value: NativePreviewFrame) => void) => {
      // Joining another viewer's screencast synchronously delivers its latest JPEG.
      frame({ sequence: 1, generation: 1, configuration: 1, codec: "jpeg", keyframe: true,
        timestamp: 0, width: 960, height: 600, data: Buffer.from([1]) });
      return unsubscribe;
    }),
  } as unknown as BrowserHostCoordinator;
  const previews = new RemoteActivityPreviews({
    notify: (_owner, method) => method !== "desktop/activity/preview/frame",
    createSource: (activity, handlers) => createRemotePreviewSource(activity, handlers, browser),
  });
  try {
    const opened = previews.open(1, { ...previewActivity, kind: "browser" });
    previews.request(1, "desktop/activity/preview/renew", opened);
    expect(() => previews.request(1, "desktop/activity/preview/renew", opened)).toThrow("owner");
    expect(unsubscribe).toHaveBeenCalledOnce();
  } finally { previews.shutdown(); }
  expect(unsubscribe).toHaveBeenCalledOnce();
});
