import { describe, expect, it, vi } from "vitest";
import type { ExtensionInventoryRecord, InitializeResult, PluginDesktopActivationParams } from "../shared/protocol";
import { PluginDesktopActivations } from "./pluginDesktopActivations";

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

  it("routes committed inventory notifications but rejects unknown graphs", async () => {
    const backend = server("first"), owner = new PluginDesktopActivations(8);
    await owner.initialize("/a", backend.request, {});
    owner.observeInventory("/a", inventory("second"));
    await owner.report(report("second"));
    await expect(owner.report(report("unseen"))).rejects.toThrow(/graph/i);
    expect(backend.calls).toHaveBeenCalledTimes(2);
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
