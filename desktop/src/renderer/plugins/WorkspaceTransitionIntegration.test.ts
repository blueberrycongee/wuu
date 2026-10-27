import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AppServerClientPool } from "../../main/appServerClients";
import { PluginDesktopActivations } from "../../main/pluginDesktopActivations";
import { PluginRuntimeRequests } from "../../main/pluginRuntimeRequests";
import type { ExtensionInventoryRecord, InitializeResult, PluginClientRequestParams, WuuDesktopApi } from "../../shared/protocol";
import { DesktopPluginRuntime, desktopPluginHost } from "./DesktopPluginRuntime";
import type { PluginGenerationApi } from "./PluginHost";
import { showApplicationActionError } from "../ApplicationActionError";
import { clearToasts, ToastViewport } from "../Toast";
import { WuuUIRoot } from "../ui/layers/UILayerHost";

const originalBridge = window.wuu;
afterEach(() => { window.wuu = originalBridge; });

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function inventory(graph: string): ExtensionInventoryRecord[] {
  return [{ id: "user:transition", name: "transition", kind: "plugin", provenance: { kind: "plugin", source: "user", scope: "user" },
    state: "granted", approval_state: "granted", enabled: true, runtime_state: "active", fingerprint: "same-source",
    desktop: { entry: "desktop.js" }, graph_generation: graph }];
}
function process(graph: string) {
  const pending: ReturnType<typeof deferred<{ result: unknown }>>[] = [];
  const calls = vi.fn(async (method: string, _params?: unknown): Promise<unknown> => {
    if (method === "initialize") return { extension_inventory: inventory(graph) };
    if (method === "plugin/client/cancel") return { cancelled: true };
    if (method === "plugin/client/request") { const result = deferred<{ result: unknown }>(); pending.push(result); return result.promise; }
    return {};
  });
  return { calls, pending, request: calls as <T>(method: string, params?: unknown) => Promise<T> };
}
async function fixture() {
  const a = process("graph-a"), b = process("graph-b"), returnedA = process("graph-a-return");
  const owner = new PluginDesktopActivations("transition-integration");
  // Any fallback to the ambient process pool is a test failure, not a synthetic
  // implementation of the route under test.
  const ambient = vi.fn(() => { throw new Error("ambient workspace routing is forbidden"); });
  const requests = new PluginRuntimeRequests({ bindContextRequest: ambient, requestInContext: ambient } as unknown as AppServerClientPool);
  window.wuu = { ...originalBridge,
    loadPluginDesktopModule: async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }),
    reportPluginDesktopActivation: params => owner.report(params),
    requestPluginRuntime: params => requests.request("window", owner.runtimeRequest(params.graph_generation, params.workspace_root), params),
    cancelPluginRuntime: params => requests.cancel("window", params),
  } as WuuDesktopApi;
  const handles: PluginGenerationApi[] = [];
  const runtime = new DesktopPluginRuntime(desktopPluginHost, async () => ({ activate(api: PluginGenerationApi) { handles.push(api); } }));
  const initialize = (backend: typeof a, root: string) => owner.initialize(root, backend.request, {});
  const publish = async (result: InitializeResult, connection: number) => {
    expect(await runtime.sync(result.extension_inventory ?? [], false, connection)).toEqual([]);
    return handles.at(-1)!;
  };
  const call = (api: PluginGenerationApi, kind: "runtime" | "service", root?: string) => kind === "runtime"
    ? api.invokeRuntime("read", { item: "retained" }, root ? { workspaceRoot: root } : undefined)
    : api.invokeService({ service: "fixture.catalog", method: "read", params: { item: "retained" } }, root ? { workspaceRoot: root } : undefined);
  return { a, b, returnedA, owner, ambient, requests, handles, initialize, publish, call,
    async close() { for (const backend of [a, b, returnedA]) for (const pending of backend.pending) pending.resolve({ result: "cleanup" }); await runtime.shutdown(); requests.closeOwner("window"); await owner.disconnect(); },
  };
}

describe("workspace transition across renderer graph and process request ownership", () => {
  it.each(["runtime", "service"] as const)("pins old %s calls and cancellation to A while B initializes", async kind => {
    const h = await fixture();
    try {
      const a = await h.initialize(h.a, "/a");
      const apiA = await h.publish(a, 1);
      const b = await h.initialize(h.b, "/b");
      // B is now the most recently initialized backend, but the mounted graph
      // is still A while its successor is being prepared.
      const pending = h.call(apiA, kind).then(value => ({ value }), error => ({ error }));
      expect(h.a.pending).toHaveLength(1);
      expect(h.b.pending).toHaveLength(0);
      await expect(h.call(apiA, kind, "/b")).rejects.toThrow(/workspace/i);
      expect(h.b.pending).toHaveLength(0);
      await h.publish(b, 2);
      const request = h.a.calls.mock.calls.find(([method]) => method === "plugin/client/request")![1] as PluginClientRequestParams;
      const cancellation = h.a.calls.mock.calls.find(([method]) => method === "plugin/client/cancel")![1];
      expect(cancellation).toMatchObject({ graph_generation: "graph-a", request_id: request.request_id });
      expect(h.b.calls.mock.calls.some(([method]) => method === "plugin/client/cancel")).toBe(false);
      h.a.pending[0].resolve({ result: "late A" });
      expect(await pending).toMatchObject({ error: expect.any(Error) });
      expect(h.ambient).not.toHaveBeenCalled();
    } finally { await h.close(); }
  });

  it.each([["success", false], ["success", true], ["failure", false], ["failure", true]] as const)("fences stale %s across A→B→A (new A process=%s) without hiding a current B failure", async (completion, freshProcess) => {
    const h = await fixture();
    const published: unknown[] = [];
    clearToasts();
    const container = document.createElement("div"); document.body.appendChild(container);
    const root = createRoot(container);
    act(() => root.render(createElement(WuuUIRoot, null, createElement(ToastViewport))));
    try {
      const a = await h.initialize(h.a, "/a");
      const apiA = await h.publish(a, 1);
      const staleA = h.call(apiA, "service").then(value => { published.push(value); return undefined; }, error => error);
      const b = await h.initialize(h.b, "/b");
      const apiB = await h.publish(b, 2);
      const activeB = h.call(apiB, "service").catch(error => error);
      h.b.pending[0].reject(new Error("B catalog is unavailable"));
      const currentFailure = await activeB;
      expect(currentFailure).toMatchObject({ message: "B catalog is unavailable" });
      act(() => showApplicationActionError(currentFailure));
      expect(container.querySelector('[role="alert"]')?.textContent).toContain("B catalog is unavailable");
      act(() => clearToasts());
      const staleB = h.call(apiB, "runtime").then(value => { published.push(value); return undefined; }, error => error);
      const returning = freshProcess ? h.returnedA : h.a;
      const restoredA = await h.publish(await h.initialize(returning, "/a"), 3);
      if (completion === "success") { h.a.pending[0].resolve({ result: "obsolete A" }); h.b.pending[1].resolve({ result: "obsolete B" }); }
      else { h.a.pending[0].reject(new Error("obsolete A transport failed")); h.b.pending[1].reject(new Error("obsolete B transport failed")); }
      for (const error of await Promise.all([staleA, staleB])) {
        expect(error).toMatchObject({ code: "WUU_PLUGIN_SCOPE_RETIRED" });
        act(() => showApplicationActionError(error));
        expect(container.querySelector('[role="alert"]')).toBeNull();
      }
      expect(published).toEqual([]);
      const current = h.call(restoredA, "service");
      returning.pending[freshProcess ? 0 : 1].resolve({ result: "current A" });
      await expect(current).resolves.toBe("current A");
      expect(h.ambient).not.toHaveBeenCalled();
    } finally { act(() => root.unmount()); container.remove(); clearToasts(); await h.close(); }
  });
});
