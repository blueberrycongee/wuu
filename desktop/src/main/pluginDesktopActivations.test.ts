import { describe, expect, it, vi } from "vitest";
import type { ExtensionInventoryRecord, InitializeResult, PluginDesktopActivationParams } from "../shared/protocol";
import { EventEmitter } from "node:events";
import { observeDesktopActivationDocument, PluginDesktopActivations } from "./pluginDesktopActivations";

const inventory = (graph: string) => [{ id: "shell", fingerprint: "source", graph_generation: graph, desktop: {} }] as ExtensionInventoryRecord[];
const report = (graph: string): PluginDesktopActivationParams => ({ id: "shell", fingerprint: "source", graph_generation: graph, activation: 1, phase: "registration", status: "active" });
function server(graph: string) {
  const calls = vi.fn(async (method: string, _params?: unknown): Promise<unknown> => method === "initialize" ? { extension_inventory: inventory(graph) } : {});
  return { calls, request: calls as <T>(method: string, params?: unknown) => Promise<T> };
}

// IPC identity/lifetime contracts: no renderer impersonation, no routing a stale
// report to a replacement process, and no revival after navigation/disconnect.
describe("desktop activation IPC lifetime", () => {
  it("injects the main-owned document identity and keeps old graphs bound to their process", async () => {
    const first = server("graph-a"), second = server("graph-b");
    const owner = new PluginDesktopActivations(7);
    await owner.initialize("/a", first.request, {});
    await owner.initialize("/b", second.request, {});
    await owner.report({ ...report("graph-a"), renderer_id: "spoof" } as PluginDesktopActivationParams);
    await owner.report(report("graph-b"));
    const wire = first.calls.mock.calls[1][1] as Record<string, unknown>;
    expect(wire.renderer_id).toMatch(/^7:/);
    expect(wire.renderer_id).not.toBe("spoof");
    expect(second.calls.mock.calls[1][1]).toEqual({ ...report("graph-b"), renderer_id: wire.renderer_id });
    await owner.disconnect();
    expect(first.calls.mock.calls.at(-1)).toEqual(["plugin/desktop-activation", { renderer_id: wire.renderer_id, disconnected: true }]);
    expect(second.calls.mock.calls.at(-1)).toEqual(first.calls.mock.calls.at(-1));
  });

  it("keeps overlapping initializes bound to their own captured process", async () => {
    const first = server("first"), second = server("second"), owner = new PluginDesktopActivations(13);
    let release!: () => void;
    first.calls.mockImplementationOnce(() => new Promise(resolve => { release = () => resolve({ extension_inventory: inventory("first") }); }));
    const pending = owner.initialize("/same", first.request, {});
    await owner.initialize("/same", second.request, {});
    release(); await pending;
    await owner.report(report("first"));
    await owner.report(report("second"));
    expect(first.calls.mock.calls[1]?.[1]).toMatchObject({ graph_generation: "first" });
    expect(second.calls.mock.calls[1]?.[1]).toMatchObject({ graph_generation: "second" });
  });

  it("routes committed inventory notifications but rejects unknown graphs", async () => {
    const backend = server("first"), owner = new PluginDesktopActivations(8);
    await owner.initialize("/a", backend.request, {});
    owner.observeInventory("/a", inventory("second"));
    await owner.report(report("second"));
    await expect(owner.report(report("unseen"))).rejects.toThrow(/graph/i);
    expect(backend.calls).toHaveBeenCalledTimes(2);
  });

  it("keeps the route for inventory committed while initialize is in flight", async () => {
    const backend = server("before"), owner = new PluginDesktopActivations(12);
    backend.calls.mockImplementationOnce(async () => {
      owner.observeInventory("/a", inventory("after"));
      return { extension_inventory: inventory("before") };
    });
    await owner.initialize("/a", backend.request, {});
    await expect(owner.report(report("after"))).resolves.toBeUndefined();
  });

  it("waits for accepted reports before disconnect and rejects late reports", async () => {
    const backend = server("graph"), owner = new PluginDesktopActivations(9);
    await owner.initialize("/a", backend.request, {});
    let release!: () => void;
    backend.calls.mockImplementationOnce(() => new Promise(resolve => { release = () => resolve({}); }));
    const pending = owner.report(report("graph"));
    await vi.waitFor(() => expect(release).toBeTypeOf("function"));
    const closed = owner.disconnect();
    await expect(owner.report(report("graph"))).rejects.toThrow(/disconnected/i);
    expect(backend.calls).toHaveBeenCalledTimes(2);
    release(); await pending; await closed; await owner.disconnect();
    expect(backend.calls).toHaveBeenCalledTimes(3);
  });

  it("cannot revive an association when initialize resolves after document destruction", async () => {
    const owner = new PluginDesktopActivations(10);
    let release!: (value: InitializeResult) => void;
    const calls = vi.fn(() => new Promise<InitializeResult>(resolve => { release = resolve; }));
    const pending = owner.initialize("/a", calls as <T>() => Promise<T>, {});
    await owner.disconnect();
    release({ extension_inventory: inventory("late") } as InitializeResult);
    await expect(pending).rejects.toThrow(/disconnected/i);
    await expect(owner.report(report("late"))).rejects.toThrow(/disconnected/i);
    expect(calls).toHaveBeenCalledTimes(1);
  });

  it("surfaces transport failures and still attempts document disconnect", async () => {
    const backend = server("graph"), owner = new PluginDesktopActivations(11);
    await owner.initialize("/a", backend.request, {});
    backend.calls.mockRejectedValueOnce(new Error("owning process gone"));
    await expect(owner.report(report("graph"))).rejects.toThrow("owning process gone");
    await owner.disconnect();
    expect(backend.calls.mock.calls.at(-1)?.[0]).toBe("plugin/desktop-activation");
  });
});

it("pins runtime calls to the initialized graph owner across A→B→A and rejects redirect", async () => {
  const a = server("graph-a"), b = server("graph-b"), owner = new PluginDesktopActivations(22);
  await owner.initialize("/a", a.request, {});
  await owner.initialize("/b", b.request, {});
  await owner.runtimeRequest("graph-a")("plugin/client/request", { graph_generation: "graph-a" });
  expect(a.calls.mock.calls.at(-1)?.[0]).toBe("plugin/client/request");
  expect(b.calls).toHaveBeenCalledTimes(1);
  expect(() => owner.runtimeRequest("graph-a", "/b")).toThrow(/workspace/i);
  expect(() => owner.runtimeRequest("unknown")).toThrow(/graph/i);
  await owner.initialize("/a", a.request, {});
  await owner.runtimeRequest("graph-b", "/b")("plugin/client/request", {});
  expect(b.calls).toHaveBeenCalledTimes(2);
  await owner.disconnect();
  expect(() => owner.runtimeRequest("graph-a")).toThrow(/disconnected/i);
});

it("accepts equivalent workdir inventory publication before the next initialize", async () => {
  const backend = server("before"), owner = new PluginDesktopActivations(23);
  await owner.initialize("/a/./workspace/", backend.request, {});
  owner.observeInventory("/a/workspace", inventory("after"));
  await expect(owner.report(report("after"))).resolves.toBeUndefined();
});

it("rebuilds a lost owner only through one authoritative matching handshake", async () => {
  const backend = server("graph"), owner = new PluginDesktopActivations(24);
  const handshake = vi.fn(async () => { await owner.initialize("/workspace", backend.request, {}); });
  await Promise.all([owner.recoverGraph("graph", handshake), owner.recoverGraph("graph", handshake)]);
  expect(handshake).toHaveBeenCalledTimes(1);
  await expect(owner.report(report("graph"))).resolves.toBeUndefined();
  await expect(owner.recoverGraph("forged", handshake)).rejects.toMatchObject({ code: "WUU_DESKTOP_GRAPH_OWNER_MISSING", recovery: "reinitialize-required" });
  expect(handshake).toHaveBeenCalledTimes(1);
  expect(() => owner.runtimeRequest("forged")).toThrow(/graph/i);
});

it("does not admit a graph merely because a fresh workspace initialized successfully", async () => {
  const backend = server("new-graph"), owner = new PluginDesktopActivations(25);
  await expect(owner.recoverGraph("old-graph", async () => { await owner.initialize("/workspace", backend.request, {}); }))
    .rejects.toMatchObject({ code: "WUU_DESKTOP_GRAPH_OWNER_MISSING", recovery: "reinitialize-required" });
  expect(backend.calls.mock.calls.map(call => call[0])).toEqual(["initialize"]);
  await expect(owner.report(report("old-graph"))).rejects.toThrow(/owning process/);
});

it("keeps graph ownership on canceled navigation and retires only on committed navigation", async () => {
  const backend = server("graph"), owner = new PluginDesktopActivations(26);
  await owner.initialize("/workspace", backend.request, {});
  const events = new EventEmitter();
  const disconnected = vi.fn(() => { void owner.disconnect(); });
  observeDesktopActivationDocument(events as unknown as Parameters<typeof observeDesktopActivationDocument>[0], disconnected);
  events.emit("did-start-navigation", { isMainFrame: true, isSameDocument: false });
  events.emit("did-fail-load", {}, -3, "ERR_ABORTED");
  await expect(owner.report(report("graph"))).resolves.toBeUndefined();
  expect(disconnected).not.toHaveBeenCalled();
  events.emit("did-navigate-in-page", {}, "/workspace#section", true);
  expect(disconnected).not.toHaveBeenCalled();
  events.emit("did-navigate", {}, "file:///new-document", 200, "OK");
  expect(disconnected).toHaveBeenCalledTimes(1);
  await expect(owner.report(report("graph"))).rejects.toThrow(/disconnected/i);
});

it("does not revive ownership when a recovery handshake finishes after document loss", async () => {
  const owner = new PluginDesktopActivations(27);
  let finish!: (result: InitializeResult) => void;
  const request = vi.fn(() => new Promise<InitializeResult>(resolve => { finish = resolve; })) as <T>(method: string, params?: unknown) => Promise<T>;
  const recovery = owner.recoverGraph("graph", async () => { await owner.initialize("/workspace", request, {}); });
  const rejected = expect(recovery).rejects.toThrow(/disconnected/i);
  await vi.waitFor(() => expect(finish).toBeTypeOf("function"));
  await owner.disconnect();
  finish({ extension_inventory: inventory("graph") } as InitializeResult);
  await rejected;
  expect(() => owner.runtimeRequest("graph")).toThrow(/disconnected/i);
});

it("preserves handshake transport failure and never retries it in a loop", async () => {
  const owner = new PluginDesktopActivations(28), failure = new Error("process initialize failed");
  const handshake = vi.fn(async () => { throw failure; });
  await expect(owner.recoverGraph("graph", handshake)).rejects.toBe(failure);
  await expect(owner.recoverGraph("graph", handshake)).rejects.toBe(failure);
  expect(handshake).toHaveBeenCalledTimes(1);
});

it("does not attribute restarted-process inventory to the exited workspace process", async () => {
  const first = server("old-process"), second = server("new-process"), owner = new PluginDesktopActivations(29);
  await owner.initialize("/workspace", first.request, {});
  owner.invalidateWorkspace("/workspace/.");
  owner.observeInventory("/workspace", inventory("new-process"));
  expect(() => owner.runtimeRequest("new-process")).toThrow(/graph/i);
  await owner.initialize("/workspace", second.request, {});
  await owner.report(report("new-process"));
  expect(second.calls.mock.calls.at(-1)?.[0]).toBe("plugin/desktop-activation");
  expect(first.calls).toHaveBeenCalledTimes(1);
});

it("recovers runtime admission before ACK and shares the one handshake with ACK", async () => {
  const backend = server("graph"), owner = new PluginDesktopActivations(30);
  const handshake = vi.fn(async () => { await owner.initialize("/workspace", backend.request, {}); });
  const [route] = await Promise.all([
    owner.recoverRuntimeRequest("graph", "/workspace", handshake),
    owner.recoverGraph("graph", handshake).then(() => owner.report(report("graph"))),
  ]);
  await route("plugin/client/request", {});
  expect(handshake).toHaveBeenCalledTimes(1);
  expect(backend.calls.mock.calls.map(call => call[0]).filter(method => method === "plugin/client/request")).toHaveLength(1);
});

it("never retries a dispatched runtime failure or recovers a known workspace mismatch", async () => {
  const backend = server("graph"), owner = new PluginDesktopActivations(31);
  await owner.initialize("/workspace", backend.request, {});
  const handshake = vi.fn(async () => {}), failure = new Error("delivery outcome unknown");
  const route = await owner.recoverRuntimeRequest("graph", "/workspace", handshake);
  backend.calls.mockRejectedValueOnce(failure);
  await expect(route("plugin/client/request", {})).rejects.toBe(failure);
  expect(handshake).not.toHaveBeenCalled();
  expect(backend.calls).toHaveBeenCalledTimes(2);
  await expect(owner.recoverRuntimeRequest("graph", "/other", handshake)).rejects.toThrow(/workspace/i);
  expect(handshake).not.toHaveBeenCalled();
});

it("rejects unknown graphs and workspace mismatch after pre-dispatch recovery without dispatch", async () => {
  const backend = server("graph"), owner = new PluginDesktopActivations(32);
  const handshake = vi.fn(async () => { await owner.initialize("/workspace", backend.request, {}); });
  await expect(owner.recoverRuntimeRequest("forged", undefined, handshake)).rejects.toMatchObject({ code: "WUU_DESKTOP_GRAPH_OWNER_MISSING" });
  await expect(owner.recoverRuntimeRequest("graph", "/other", handshake)).rejects.toThrow(/workspace/i);
  expect(backend.calls.mock.calls.map(call => call[0])).toEqual(["initialize"]);
});

it("maps an explicit scoped runtime only after matching source and target inventories", async () => {
  const owner = new PluginDesktopActivations(42), source = server("source-graph"), target = server("target-graph");
  await owner.initialize("/source", source.request, {});
  const identity = { id: "shell", fingerprint: "source", graph_generation: "source-graph" };
  const acquire = vi.fn(async () => ({ workdir: "/target", request: target.request, params: {} }));
  const bound = await owner.scopedRuntimeRequest(identity, "user/domain/profile", acquire);
  await bound("plugin/client/request", { ...identity, method: "send" });
  expect(target.calls.mock.calls.at(-1)?.[1]).toMatchObject({ graph_generation: "target-graph", method: "send" });
  expect(await owner.scopedRuntimeRequest(identity, "user/domain/profile", acquire)).toBe(bound);
  expect(acquire).toHaveBeenCalledTimes(1);
  await expect(owner.scopedRuntimeRequest({ ...identity, fingerprint: "forged" }, "other", acquire)).rejects.toThrow(/identity/i);
  await owner.disconnect();
  expect(target.calls.mock.calls.some(([method]) => method === "plugin/desktop-activation")).toBe(false);
  await expect(bound("plugin/client/request", { ...identity, method: "send" })).rejects.toThrow(/disconnected/i);
  await bound("plugin/client/cancel", { ...identity, request_id: "pending" });
  expect(target.calls.mock.calls.at(-1)?.[1]).toMatchObject({ graph_generation: "target-graph", request_id: "pending" });
});

it("does not dispatch or rebind when a target plugin differs or initialization fails", async () => {
  const owner = new PluginDesktopActivations(43), source = server("source-graph"), target = server("target-graph");
  await owner.initialize("/source", source.request, {});
  target.calls.mockImplementation(async () => ({ extension_inventory: [{ ...inventory("target-graph")[0], fingerprint: "different" }] }));
  const acquire = vi.fn(async () => ({ workdir: "/target", request: target.request, params: {} }));
  const identity = { id: "shell", fingerprint: "source", graph_generation: "source-graph" };
  await expect(owner.scopedRuntimeRequest(identity, "scope", acquire)).rejects.toThrow(/identity/i);
  await expect(owner.scopedRuntimeRequest(identity, "scope", acquire)).rejects.toThrow(/identity/i);
  expect(acquire).toHaveBeenCalledTimes(1);
  expect(target.calls).toHaveBeenCalledTimes(1);
});
