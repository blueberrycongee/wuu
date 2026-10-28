import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ExtensionInventoryRecord, WuuDesktopApi } from "../../shared/protocol";
import { DesktopPluginRuntime, desktopPluginHost } from "./DesktopPluginRuntime";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { ApplicationShellOutlet } from "./ApplicationShell";

const originalWuu = window.wuu;

afterEach(() => {
  vi.useRealTimers();
  window.wuu = originalWuu;
});

describe("DesktopPluginRuntime", () => {
  // Public opt-in: code owns presentation; each business binding owns actions.
  // Cover two independent shell authors, stopped-writer gaps, late results, and disable.
  it.each(["atlas", "lumen"])("retains %s presentation DOM while replacing only business authority", async author => {
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }));
    const host = new PluginHost({ react: React });
    let release!: () => void, stopped!: () => void;
    const stoppedGate = new Promise<void>(resolve => { stopped = resolve; });
    const resume = new Promise<void>(resolve => { release = resolve; });
    let lazyInvoke!: import("../../../../packages/plugin-sdk/src/ui").ApplicationServiceV1["invoke"];
    const scopes: import("../../../../packages/plugin-sdk/src/ui").ApplicationServiceV1[] = [];
    const calls: string[] = [], signals: AbortSignal[] = [];
    let subscriptions = 0;
    let business = 0, mounts = 0, registrations = 0, repaint!: () => void;
    const runtime = new DesktopPluginRuntime(host, async id => id === "user:business" ? { activate(api: PluginGenerationApi) {
      const version = ++business, snapshot = { revision: business };
      const fileSnapshot = { ownerTabId: "draft", interactionOwnerKey: `draft:${version}`, enabled: true, canAskSide: false, comments: [] };
      api.registerApplicationProvider({ id: "ui.application", prepare: () => ({
        async activate() { if (version === 2) await resume; },
        deactivate() { calls.push(`stop:${version}`); stopped(); return undefined; }, dispose() {},
        createScope(signal: AbortSignal) { signals.push(signal); return {
          settingsControls: { connectionDisplay: { label(key: string, fallback: string) { signal.throwIfAborted(); return `${key}:${fallback}:${version}`; } } },
          webWorkspace: { fork() { return this; }, async dispose() {}, onReveal() { signal.throwIfAborted(); subscriptions++; return () => {}; } },
          workbench: { renderLayers({ settingsOnly }: { settingsOnly: boolean }) { signal.throwIfAborted(); return settingsOnly ? "settings" : `rail:${version}`; } },
          fileSelection: { getSnapshot({ ownerTabId }: { ownerTabId: string }) { signal.throwIfAborted(); if (ownerTabId !== "draft") throw new Error("Wrong draft owner"); return fileSnapshot; } },
          contractVersion: 1, getSnapshot() { signal.throwIfAborted(); return snapshot; },
          subscribe() { signal.throwIfAborted(); return () => {}; },
          async invoke() { signal.throwIfAborted(); calls.push(`action:${version}`); },
        } as unknown as import("../../../../packages/plugin-sdk/src/ui").ApplicationServiceV1; },
      }) });
    } } : { activatePresentation(api: import("../../../../packages/plugin-sdk/src/ui").ApplicationPresentationAPI) {
      if (id.endsWith("-lazy")) {
        api.registerApplicationShell({ id: "ui.shell", render: ({ application }) => { lazyInvoke = application.invoke; return null; } });
        return;
      }
      registrations++;
      expect("application" in api).toBe(false);
      expect("invokeRuntime" in api).toBe(false);
      api.registerApplicationShell({ id: "ui.shell", render: ({ application }) => {
        const [paint, update] = React.useState(0); repaint = () => update(value => value + 1);
        React.useEffect(() => { mounts++; }, []);
        React.useEffect(() => application.webWorkspace.onReveal(() => {}), [application, paint]);
        const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
        if (!scopes.includes(application)) scopes.push(application);
        const selection = application.fileSelection.getSnapshot({ ownerTabId: "draft" });
        const rail = application.workbench.renderLayers({ settingsOnly: false });
        const label = application.settingsControls.connectionDisplay.label("model", author);
        return React.createElement("main", null, React.createElement("textarea", { defaultValue: "unsent" }),
          React.createElement("button", { onClick: () => { void application.invoke("runtime.refresh", {}); } }, `${author}:${snapshot.revision}`), React.createElement("aside", null, selection.interactionOwnerKey, rail, label));
      } });
    } });
    const provider = { ...dependencyPlugin("business"), graph_generation: "one" };
    const shell = { ...dependencyPlugin(author, ["business"]), graph_generation: "one", resolved_required_dependencies: ["business"] };
    const lazyShell = { ...shell, id: `${shell.id}-lazy`, provenance: { ...shell.provenance, plugin_id: `${author}-lazy` } };
    await host.selectApplicationBinding({ pluginId: provider.id, generation: provider.fingerprint!, id: "ui.application" });
    expect(await runtime.sync([provider, shell, lazyShell])).toEqual([]);
    const element = document.createElement("div"), root = createRoot(element); document.body.append(element);
    const render = () => root.render(React.createElement(React.Fragment, null,
      React.createElement(ApplicationShellOutlet, { host, binding: { pluginId: shell.id, generation: shell.fingerprint!, id: "ui.shell" } }),
      React.createElement(ApplicationShellOutlet, { host, binding: { pluginId: lazyShell.id, generation: lazyShell.fingerprint!, id: "ui.shell" } })));
    await act(async () => render());
    const textarea = element.querySelector("textarea")!; textarea.focus(); textarea.setSelectionRange(2, 4); textarea.scrollTop = 12;
    let releaseOldCallback!: () => void;
    const oldCallbackGate = new Promise<void>(resolve => { releaseOldCallback = resolve; });
    const oldInvoke = scopes[0].invoke, oldLazyInvoke = lazyInvoke;
    const lateLazyCallback = oldCallbackGate.then(() => oldLazyInvoke("runtime.refresh", {}));
    void lateLazyCallback.catch(() => {});
    const lateCallback = oldCallbackGate.then(() => oldInvoke("runtime.refresh", {}));
    void lateCallback.catch(() => {});
    try {
      let update!: ReturnType<typeof runtime.sync>;
      act(() => { update = runtime.sync([{ ...provider, graph_generation: "two" }, { ...shell, graph_generation: "two" }, { ...lazyShell, graph_generation: "two" }]); });
      await act(async () => { await stoppedGate; repaint(); render(); });
      expect(signals[0].aborted).toBe(true);
      await expect(scopes[0].invoke("runtime.refresh", {})).rejects.toBeDefined();
      expect(element.querySelector("textarea")).toBe(textarea);
      expect(element.querySelector("button")!.textContent).toBe(`${author}:1`);
      expect(element.querySelector("aside")!.textContent).toBe(`draft:1rail:1model:${author}:1`);
      expect(subscriptions).toBe(1);
      await act(async () => { release(); expect(await update).toEqual([]); });
      expect(registrations).toBe(1); expect(mounts).toBe(1);
      expect(element.querySelector("textarea")).toBe(textarea);
      expect(textarea.value).toBe("unsent"); expect(textarea.selectionStart).toBe(2); expect(textarea.selectionEnd).toBe(4);
      expect(document.activeElement).toBe(textarea);
      expect(element.querySelector("button")!.textContent).toBe(`${author}:2`);
      await act(async () => element.querySelector("button")!.click());
      releaseOldCallback(); await expect(lateCallback).rejects.toBeDefined();
      await expect(lateLazyCallback).rejects.toBeDefined();
      expect(element.querySelector("aside")!.textContent).toBe(`draft:2rail:2model:${author}:2`);
      expect(subscriptions).toBe(2);
      expect(calls).toEqual(["stop:1", "action:2"]);
      await expect(scopes[0].invoke("runtime.refresh", {})).rejects.toBeDefined();
    } finally { release(); releaseOldCallback(); act(() => root.unmount()); element.remove(); await runtime.shutdown(); }
  });

  it("reactivates a failed opted-in surface on the next graph instead of retaining its error boundary", async () => {
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }));
    const host = new PluginHost({ react: React });
    let broken = true, registrations = 0;
    const runtime = new DesktopPluginRuntime(host, async () => ({ activatePresentation(api: import("../../../../packages/plugin-sdk/src/ui").ApplicationPresentationAPI) {
      registrations++;
      api.registerApplicationShell({ id: "ui.shell", render: () => { if (broken) throw new Error("Temporary presentation failure"); return React.createElement("main", null, "Recovered surface"); } });
    } }));
    const shell = { ...dependencyPlugin("repairable"), graph_generation: "one" };
    expect(await runtime.sync([shell])).toEqual([]);
    const element = document.createElement("div"), root = createRoot(element);
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      await act(async () => root.render(React.createElement(ApplicationShellOutlet, { host, binding: { pluginId: shell.id, generation: shell.fingerprint!, id: "ui.shell" } })));
      expect(element.querySelector("main")).toBeNull();
      broken = false;
      await act(async () => { expect(await runtime.sync([{ ...shell, graph_generation: "two" }])).toEqual([]); });
      expect(registrations).toBe(2);
      expect(element.querySelector("main")?.textContent).toBe("Recovered surface");
    } finally { errors.mockRestore(); act(() => root.unmount()); await runtime.shutdown(); }
  });

  it.each([false, true])("awaits source presentation before dependent scope abort and clears retirement after activation (%s)", async failActivation => {
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }));
    const host = new PluginHost({ react: React });
    const events: string[] = [];
    let replacement = false, cleanupEntered!: () => void, releaseCleanup!: () => void;
    const cleanupStarted = new Promise<void>(resolve => { cleanupEntered = resolve; });
    const cleanupGate = new Promise<void>(resolve => { releaseCleanup = resolve; });
    let abortedSource: { retirement: ReturnType<typeof host.applicationProviders.getRetirement>; current: unknown; binding: number } | undefined;
    const runtime = new DesktopPluginRuntime(host, async id => ({ activate(api: PluginGenerationApi) {
      if (id === "user:business") {
        const next = replacement;
        api.registerApplicationProvider({ id: "ui.application", prepare: () => ({
          activate() { if (next && failActivation) throw new Error("replacement activation failed"); },
          deactivate() { events.push("business:stop"); return undefined; }, dispose() {}, createScope() { throw new Error("unused"); },
        }) });
      } else {
        api.registerApplicationShell({ id: "ui.shell", render: () => null });
        if (!replacement) {
          api.signal.addEventListener("abort", () => {
            abortedSource = { retirement: host.applicationProviders.getRetirement(), current: host.applicationProviders.current, binding: host.applicationProviders.getBindingRevision() };
          }, { once: true });
          api.registerCleanup(async () => { cleanupEntered(); await cleanupGate; events.push("shell:cleaned"); });
        }
      }
    } }));
    const business = { ...dependencyPlugin("business"), graph_generation: "graph-one" };
    const shell = { ...dependencyPlugin("shell", ["business"]), resolved_required_dependencies: ["business"] };
    await host.selectApplicationBinding({ pluginId: business.id, generation: business.fingerprint!, id: "ui.application" });
    await runtime.sync([business, shell]);
    const source = host.applicationProviders.current, binding = host.applicationProviders.getBindingRevision();
    let captureEntered!: () => void, releaseCapture!: () => void;
    const captureStarted = new Promise<void>(resolve => { captureEntered = resolve; });
    const captureGate = new Promise<void>(resolve => { releaseCapture = resolve; });
    const unsubscribe = host.applicationProviders.subscribeRetirementPreparation(request => {
      expect(request.binding).toBe(binding);
      expect(request.signal.aborted).toBe(false);
      captureEntered(); return captureGate;
    });
    replacement = true;
    const next = { ...business, fingerprint: "candidate", graph_generation: "graph-two" };
    await host.selectApplicationBinding({ pluginId: next.id, generation: next.fingerprint, id: "ui.application" });
    const pending = runtime.sync([next, shell]);
    try {
      await captureStarted;
      expect(abortedSource).toBeUndefined();
      expect(host.applicationProviders.current).toBe(source);
      expect(host.applicationProviders.getRetirement()).toBeUndefined();
      releaseCapture();
      await cleanupStarted;
      expect(abortedSource).toEqual({ retirement: { binding }, current: source, binding });
      expect(host.applicationProviders.getRetirement()).toEqual({ binding });
      expect(events).not.toContain("business:stop");
    } finally { releaseCapture(); releaseCleanup(); unsubscribe(); }
    const failures = await pending;
    expect(failures.some(failure => failure.pluginId === business.id)).toBe(failActivation);
    if (!failActivation) expect(failures).toEqual([]);
    expect(host.applicationProviders.getRetirement()).toBeUndefined();
    expect(events.indexOf("shell:cleaned")).toBeLessThan(events.indexOf("business:stop"));
    await runtime.shutdown();
  });

  // Failure cases: graph-only replacement removes the selected root while loading,
  // failed staging loses last-good UI, or keeping it delays a real disable.
  it.each(["bridge", "register"])("keeps the selected shell mounted during held %s after disabling an unrelated plugin", async (stage) => {
    let release!: () => void;
    let signalStarted!: () => void;
    const gate = new Promise<void>(resolve => { release = resolve; });
    const started = new Promise<void>(resolve => { signalStarted = resolve; });
    let replacing = false;
    const suspend = async (point: string) => {
      if (replacing && stage === point) { signalStarted(); await gate; }
    };
    installDesktopModuleLoader(async ({ id, fingerprint }) => {
      if (id === "user:shell") await suspend("bridge");
      return { id, fingerprint, digest: "a".repeat(64), url: id };
    });
    const host = new PluginHost({ react: React });
    const handles: PluginGenerationApi[] = [];
    const runtime = new DesktopPluginRuntime(host, async id => ({ async activate(api: PluginGenerationApi) {
      if (id !== "user:shell") {
        api.registerCommand({ id: "unrelated", title: "Unrelated", execute() {} });
        return;
      }
      const activation = handles.push(api);
      api.registerApplicationShell({ id: "ui.shell", render: () => React.createElement("main", null, `Shell ${activation}`) });
      await suspend("register");
    } }));
    const shell = { ...dependencyPlugin("shell"), graph_generation: "first" };
    const unrelated = { ...dependencyPlugin("unrelated"), graph_generation: "first" };
    await runtime.sync([shell, unrelated]);
    const element = document.createElement("div"), root = createRoot(element);
    await act(async () => root.render(React.createElement(ApplicationShellOutlet, {
      host, binding: { pluginId: shell.id, generation: shell.fingerprint!, id: "ui.shell" },
    })));
    const available: boolean[] = [];
    const unsubscribe = host.subscribe(() => available.push(host.getApplicationShells().some(item => item.pluginId === shell.id)));
    let pending: ReturnType<DesktopPluginRuntime["sync"]> | undefined;
    try {
      replacing = true;
      act(() => { pending = runtime.sync([{ ...shell, graph_generation: "second" }, { ...unrelated, enabled: false, graph_generation: "second" }]); });
      await started;
      expect(host.getCommands()).toEqual([]);
      expect(element.querySelector("main")?.textContent).toBe("Shell 1");
      expect(element.querySelector('[role="alert"]')).toBeNull();
      expect(handles[0].signal.aborted).toBe(false);
      await act(async () => { release(); expect(await pending).toEqual([]); });
      expect(element.querySelector("main")?.textContent).toBe("Shell 2");
      expect(available).not.toContain(false);
      expect(handles[0].signal.aborted).toBe(true);
      expect(handles[1].signal.aborted).toBe(false);
      unsubscribe();
      act(() => { pending = runtime.sync([{ ...shell, graph_generation: "second", enabled: false }]); });
      expect(element.querySelector("main")).toBeNull();
      expect(element.querySelector('[role="alert"]')).not.toBeNull();
      expect(handles[1].signal.aborted).toBe(true);
      await pending;
    } finally {
      unsubscribe();
      act(() => root.unmount());
      release();
      await pending;
      await runtime.sync([]);
    }
  });

  it.each([false, true])("prepares business before draining consumers and reports a committed Go graph honestly (%s)", async committed => {
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }));
    const host = new PluginHost({ react: React });
    let broken = false;
    const runtime = new DesktopPluginRuntime(host, async id => ({ activate(api: PluginGenerationApi) {
      if (id === "user:business") api.registerApplicationProvider({ id: "ui.application", prepare: () => {
        if (broken) throw new Error("candidate build failed");
        return { activate() {}, deactivate: () => undefined, dispose() {}, createScope: () => { throw new Error("unused"); } };
      } });
      else api.registerApplicationShell({ id: "ui.shell", render: () => React.createElement("main", null, "Current shell") });
    } }));
    const business = { ...dependencyPlugin("business"), graph_generation: "graph-one" };
    const shell = { ...dependencyPlugin("shell", ["business"]), resolved_required_dependencies: ["business"] };
    await host.selectApplicationBinding({ pluginId: business.id, generation: business.fingerprint!, id: "ui.application" });
    expect(await runtime.sync([business, shell])).toEqual([]);
    const previousShell = host.getApplicationShells()[0];
    const previousBusiness = host.applicationProviders.current;
    broken = true;
    const candidate = { ...business, fingerprint: "candidate", graph_generation: committed ? "graph-two" : "graph-one" };
    await host.selectApplicationBinding({ pluginId: candidate.id, generation: candidate.fingerprint, id: "ui.application" });
    const failures = await runtime.sync([candidate, shell]);
    expect(failures).toHaveLength(1);
    expect(host.applicationProviders.getRetirement()).toBeUndefined();
    expect(host.getApplicationShells()[0]).toBe(previousShell);
    expect(host.applicationProviders.current).toBe(committed ? undefined : previousBusiness);
    if (committed) expect(host.applicationProviders.failure).toContain("Go graph graph-two is committed");
    await runtime.shutdown();
  });

  it.each(["runtime", "service"])("fences %s requests and cancellation across same-source graph replacement", async (kind) => {
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: "fixture" }));
    const request = vi.fn<WuuDesktopApi["requestPluginRuntime"]>();
    const cancel = vi.fn<WuuDesktopApi["cancelPluginRuntime"]>(async () => ({ cancelled: true }));
    window.wuu.reportPluginDesktopActivation = vi.fn(async () => {});
    window.wuu.requestPluginRuntime = request;
    window.wuu.cancelPluginRuntime = cancel;
    let finish!: (value: { result: unknown }) => void;
    request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const handles: PluginGenerationApi[] = [];
    const runtime = new DesktopPluginRuntime(desktopPluginHost, async () => ({ activate: (api: PluginGenerationApi) => { handles.push(api); } }));
    const call = (api: PluginGenerationApi, signal?: AbortSignal) => kind === "runtime"
      ? api.invokeRuntime("read", { item: "one" }, { workspaceId: "workspace", workspaceRoot: "/workspace", signal })
      : api.invokeService({ service: "catalog", method: "read", params: { item: "one" } }, { workspaceId: "workspace", workspaceRoot: "/workspace", signal });
    try {
      await runtime.sync([{ ...inventoryPlugin(), graph_generation: "first" }]);
      const pending = call(handles[0]);
      expect(request).toHaveBeenCalledExactlyOnceWith({
        id: "user:demo", fingerprint: "fingerprint-one", graph_generation: "first", request_id: expect.any(String),
        ...(kind === "service" ? { service: "catalog" } : {}),
        method: "read", input: { item: "one" }, workspace_id: "workspace", workspace_root: "/workspace",
      });
      await runtime.sync([{ ...inventoryPlugin(), graph_generation: "second" }]);
      expect(handles).toHaveLength(2);
      expect(handles[0].signal.aborted).toBe(true);
      expect(handles[1].signal.aborted).toBe(false);
      expect(cancel).toHaveBeenCalledExactlyOnceWith({ id: "user:demo", fingerprint: "fingerprint-one", graph_generation: "first", request_id: request.mock.calls[0][0].request_id });
      finish({ result: "old result" });
      await expect(pending).rejects.toThrow();
      await expect(call(handles[0])).rejects.toThrow("no longer active");
      request.mockResolvedValueOnce({ result: "new result" });
      const controller = new AbortController();
      await expect(call(handles[1], controller.signal)).resolves.toBe("new result");
      expect(request.mock.calls[1][0]).toMatchObject({ graph_generation: "second" });
      expect(request.mock.calls[1][0].request_id).not.toBe(request.mock.calls[0][0].request_id);
      controller.abort();
      expect(cancel).toHaveBeenCalledTimes(1);
    } finally {
      await runtime.sync([]);
    }
  });

  it("preserves public manifest slot names across installed graph replacement", async () => {
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }));
    const host = new PluginHost({ react: React });
    const handles: PluginGenerationApi[] = [];
    const runtime = new DesktopPluginRuntime(host, async () => ({ activate: (api: PluginGenerationApi) => {
      handles.push(api);
      api.registerUISlot({ id: "body", cardinality: "many" });
    } }));
    try {
      await runtime.sync([{ ...dependencyPlugin("demo"), graph_generation: "first" }]);
      await runtime.sync([{ ...dependencyPlugin("demo"), graph_generation: "second" }]);
      expect(handles).toHaveLength(2);
      expect(host.getUISlot("demo/body")?.pluginId).toBe("user:demo");
      expect(handles[0].signal.aborted).toBe(true);
      expect(handles[1].signal.aborted).toBe(false);
    } finally {
      await runtime.sync([]);
    }
  });

  it.each(["connection", "graph"])("reactivates on a fresh %s after retained consumers finish cleanup", async (change) => {
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }));
    const report = vi.fn<WuuDesktopApi["reportPluginDesktopActivation"]>(async () => {});
    const invoke = vi.fn(async () => "result");
    const host = new PluginHost({ react: React, reportActivation: report, invokeRuntime: invoke });
    const events: string[] = [];
    const handles: PluginGenerationApi[] = [];
    const runtime = new DesktopPluginRuntime(host, async (id) => ({ activate(api: PluginGenerationApi) {
      handles.push(api);
      events.push(`start:${id}`);
      api.registerCleanup(() => { events.push(`stop:${id}`); });
    } }));
    const inventory = [dependencyPlugin("consumer", ["provider"]), dependencyPlugin("provider")].map(plugin => ({ ...plugin, graph_generation: "same-graph" }));
    await runtime.sync(inventory, false, 0);
    await host.flushActivationReports();
    await runtime.sync(inventory, false, 0);
    expect(handles).toHaveLength(2);
    const release = host.retainGeneration("user:consumer", "fingerprint-one");
    const connection = change === "connection" ? 1 : 0;
    const replacement = inventory.map(plugin => ({ ...plugin, graph_generation: change === "graph" ? "new-graph" : "same-graph" }));
    const reconnect = runtime.sync(replacement, false, connection);
    await vi.waitFor(() => expect(handles[1].signal.aborted).toBe(true));
    const overlapping = change === "connection" ? runtime.sync(replacement, false, connection) : Promise.resolve([]);
    expect(handles[0].signal.aborted).toBe(false);
    expect(handles).toHaveLength(2);
    release();
    expect(await reconnect).toEqual([]);
    expect(await overlapping).toEqual([]);
    await host.flushActivationReports();
    if (change === "connection") {
      expect(events).toEqual(["start:user:provider", "start:user:consumer", "stop:user:consumer", "stop:user:provider", "start:user:provider", "start:user:consumer"]);
    } else {
      expect(events.filter(event => event === "start:user:provider")).toHaveLength(2);
      expect(events.filter(event => event === "stop:user:provider")).toHaveLength(1);
      expect(events.indexOf("stop:user:consumer")).toBeLessThan(events.indexOf("stop:user:provider"));
      expect(events.at(-1)).toBe("start:user:consumer");
    }
    expect(report.mock.calls.map(([value]) => [value.phase, value.activation, value.graph_generation])).toEqual([
      ["registration", 1, "same-graph"], ["registration", 1, "same-graph"],
      ["registration", 2, replacement[0].graph_generation], ["registration", 2, replacement[0].graph_generation],
    ]);
    await expect(handles[1].invokeService({ service: "catalog", method: "read" })).rejects.toThrow("no longer active");
    expect(invoke).not.toHaveBeenCalled();
    await expect(handles[3].invokeService({ service: "catalog", method: "read" })).resolves.toBe("result");
    await runtime.sync([], false, connection);
  });

  it.each([false, true])("activates independent plugins while another is pending, then safe mode=%s", async (safeMode) => {
    let release!: () => void;
    let signalStarted!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const started = new Promise<void>((resolve) => { signalStarted = resolve; });
    const healthy = vi.fn();
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `wuu-plugin://module/${id}.js`,
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async (url) => ({
      activate: url.includes("slow") ? async () => { signalStarted(); await gate; } : healthy,
    }));
    const pending = runtime.sync([
      { ...inventoryPlugin(), id: "user:slow" },
      { ...inventoryPlugin(), id: "user:healthy" },
    ]);
    try {
      await started;
      await vi.waitFor(() => expect(host.isGenerationActive("user:healthy", "fingerprint-one")).toBe(true), { timeout: 100 });
      expect(healthy).toHaveBeenCalledTimes(1);
      expect(host.isGenerationActive("user:slow", "fingerprint-one")).toBe(false);
      if (safeMode) {
        await runtime.sync([], true);
        expect(host.isGenerationActive("user:healthy", "fingerprint-one")).toBe(false);
        expect(await pending).toEqual([]);
      }
      release();
      expect(await pending).toEqual([]);
      expect(host.isGenerationActive("user:slow", "fingerprint-one")).toBe(!safeMode);
    } finally {
      release();
      await pending;
      await runtime.sync([]);
    }
  });

  // Shutdown must fence late inventory updates, cancel staged activation, wait
  // for mounted consumers, and flush cleanup reports while the backend is alive.
  it("drains renderer generations before shutdown resolves", async () => {
    const events: string[] = [];
    let started!: () => void;
    const pendingStarted = new Promise<void>(resolve => { started = resolve; });
    installDesktopModuleLoader(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id }));
    const host = new PluginHost({ react: React, reportActivation: async report => { events.push(`report:${report.id}:${report.phase}`); } });
    const runtime = new DesktopPluginRuntime(host, async id => ({ async activate(api: PluginGenerationApi) {
      api.registerCleanup(() => events.push(`cleanup:${id}`));
      if (id === "user:pending") {
        started();
        await new Promise<void>(resolve => api.signal.addEventListener("abort", () => resolve(), { once: true }));
      }
    } }));
    const provider = { ...dependencyPlugin("provider"), graph_generation: "graph" };
    const consumer = { ...dependencyPlugin("consumer", ["provider"]), graph_generation: "graph" };
    expect(await runtime.sync([consumer, provider])).toEqual([]);
    const releaseView = host.retainGeneration(consumer.id, consumer.fingerprint!);
    const staging = runtime.sync([consumer, provider, { ...dependencyPlugin("pending", ["provider"]), graph_generation: "graph" }]);
    await pendingStarted;
    const stopping = runtime.shutdown();
    let finished = false;
    void stopping.then(() => { finished = true; });
    await staging;
    expect(finished).toBe(false);
    expect(events).not.toContain("cleanup:user:provider");
    releaseView();
    await stopping;
    expect(events.indexOf("cleanup:user:consumer")).toBeLessThan(events.indexOf("cleanup:user:provider"));
    expect(events).toContain("cleanup:user:pending");
    expect(events).toContain("report:user:provider:cleanup");
    expect(await runtime.sync([consumer, provider])).toEqual([]);
    expect(host.isGenerationActive(provider.id, provider.fingerprint!)).toBe(false);
    await runtime.shutdown();
  });

  it("orders dependency activation, reloads unchanged consumers, and cleans up in reverse", async () => {
    const events: string[] = [];
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `${id}/${fingerprint}`,
    })));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), async (url) => ({
      activate(api: { registerCleanup(fn: () => void): void }) {
        events.push(`start:${url}`);
        api.registerCleanup(() => events.push(`stop:${url}`));
      },
    }));
    const provider = dependencyPlugin("provider");
    const consumer = dependencyPlugin("consumer", ["provider"]);
    expect(await runtime.sync([consumer, provider])).toEqual([]);
    expect(events).toEqual(["start:user:provider/fingerprint-one", "start:user:consumer/fingerprint-one"]);
    events.length = 0;
    await runtime.sync([consumer, { ...provider, fingerprint: "two" }]);
    expect(events.indexOf("stop:user:consumer/fingerprint-one")).toBeLessThan(events.indexOf("stop:user:provider/fingerprint-one"));
    expect(events.at(-1)).toBe("start:user:consumer/fingerprint-one");
    events.length = 0;
    await runtime.sync([consumer, { ...provider, enabled: false }]);
    expect(events).toEqual(["stop:user:consumer/fingerprint-one", "stop:user:provider/two"]);
  });

  it.each(["required", "transitive", "optional"])("cleans up a pending %s consumer before replacing its provider", async (relationship) => {
    const events: string[] = [];
    let release!: () => void;
    let signalStarted!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const started = new Promise<void>((resolve) => { signalStarted = resolve; });
    let consumerStarts = 0;
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `${id}/${fingerprint}`,
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async (url) => ({
      async activate(api: { registerCleanup(fn: () => void): void }) {
        events.push(`start:${url}`);
        api.registerCleanup(() => events.push(`stop:${url}`));
        if (url.startsWith("user:consumer/") && ++consumerStarts === 1) {
          signalStarted();
          await gate;
        }
      },
    }));
    const provider = dependencyPlugin("provider");
    const consumer = relationship === "optional"
      ? { ...dependencyPlugin("consumer"), resolved_dependencies: ["provider"] }
      : dependencyPlugin("consumer", [relationship === "transitive" ? "bridge" : "provider"]);
    const bridge = relationship === "transitive" ? [dependencyPlugin("bridge", ["provider"])] : [];
    const pending = runtime.sync([consumer, ...bridge, provider]);
    try {
      await started;
      expect(await runtime.sync([consumer, ...bridge, { ...provider, fingerprint: "two" }])).toEqual([]);
      expect(events).toContain("stop:user:consumer/fingerprint-one");
      expect(events).toContain("stop:user:provider/fingerprint-one");
      expect(events.indexOf("stop:user:consumer/fingerprint-one")).toBeLessThan(events.indexOf("stop:user:provider/fingerprint-one"));
      expect(host.isGenerationActive(consumer.id, consumer.fingerprint!)).toBe(true);
      expect(host.isGenerationActive(provider.id, "two")).toBe(true);
    } finally {
      release();
      await pending;
      await runtime.sync([]);
    }
  });

  it("preserves the current pending consumer across same-fingerprint supersession without blocking independent plugins", async () => {
    const events: string[] = [];
    const releases: (() => void)[] = [];
    const gates = [0, 1].map(() => new Promise<void>((resolve) => { releases.push(resolve); }));
    const signalStarted: (() => void)[] = [];
    const started = [0, 1].map(() => new Promise<void>((resolve) => { signalStarted.push(resolve); }));
    let consumerStarts = 0;
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `${id}/${fingerprint}`,
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async (url) => ({
      async activate(api: PluginGenerationApi) {
        if (url.startsWith("user:consumer/")) {
          const index = consumerStarts++;
          events.push(`start:consumer:${index}`);
          api.registerCleanup(() => events.push(`stop:consumer:${index}`));
          signalStarted[index]?.();
          await gates[index];
        } else {
          if (url.startsWith("user:independent/")) api.registerCommand({ id: "ready", title: "Ready", execute() {} });
          events.push(`start:${url}`);
          api.registerCleanup(() => events.push(`stop:${url}`));
        }
      },
    }));
    const provider = dependencyPlugin("provider");
    const consumer = dependencyPlugin("consumer", ["provider"]);
    const inventory = [consumer, provider];
    const independentActive = new Promise<void>((resolve) => {
      const unsubscribe = host.subscribe(() => {
        if (!host.isGenerationActive("user:independent", "fingerprint-one")) return;
        unsubscribe();
        resolve();
      });
    });
    const first = runtime.sync(inventory);
    let second: ReturnType<DesktopPluginRuntime["sync"]> | undefined;
    try {
      await started[0];
      second = runtime.sync([...inventory, dependencyPlugin("independent")]);
      await started[1];
      await independentActive;
      expect(host.isGenerationActive("user:independent", "fingerprint-one")).toBe(true);
      expect(await first).toEqual([]);
      releases[0]!();
      expect(events.filter((event) => event === "start:user:provider/fingerprint-one")).toHaveLength(1);
      expect(events).not.toContain("stop:user:provider/fingerprint-one");
      expect(await runtime.sync([consumer, { ...provider, fingerprint: "two" }, dependencyPlugin("independent")])).toEqual([]);
      expect(events).toContain("stop:consumer:1");
      expect(events).toContain("stop:user:provider/fingerprint-one");
      expect(events.indexOf("stop:consumer:1")).toBeLessThan(events.indexOf("stop:user:provider/fingerprint-one"));
      expect(host.isGenerationActive(consumer.id, consumer.fingerprint!)).toBe(true);
    } finally {
      releases.forEach((release) => release());
      await Promise.all([first, second]);
      await runtime.sync([]);
    }
  });

  it("blocks missing, cyclic, inactive and transitively unavailable required dependencies", async () => {
    const load = vi.fn(async () => ({ activate() {} }));
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: id,
    })));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), load);
    await runtime.sync([
      dependencyPlugin("missing", ["absent"]), dependencyPlugin("transitive", ["missing"]),
      dependencyPlugin("cycle-a", ["cycle-b"]), dependencyPlugin("cycle-b", ["cycle-a"]),
      { ...dependencyPlugin("inactive"), runtime_state: "inactive" },
      dependencyPlugin("inactive-consumer", ["inactive"]),
    ]);
    expect(load).not.toHaveBeenCalled();
  });

  it("honors core version mismatches without reimplementing version matching in the renderer", async () => {
    const load = vi.fn(async () => ({ activate() {} }));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), load);
    const plugin = { ...dependencyPlugin("consumer"), activation_issues: [{ kind: "version_mismatch" as const, related_plugin_id: "provider" }] };
    const read = vi.fn(async ({ id, fingerprint }: { id: string; fingerprint: string }) => ({
      id, fingerprint, digest: "a".repeat(64), url: id,
    }));
    installDesktopModuleLoader(read);
    await runtime.sync([plugin]);
    expect(read).not.toHaveBeenCalled();
    expect(load).not.toHaveBeenCalled();
  });

  it.each(["reject", "timeout"])("cascades desktop startup %s while independent plugins still activate", async (failure) => {
    vi.useFakeTimers();
    const activated: string[] = [];
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: id,
    })));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), async (id) => ({
      activate() {
        if (id === "user:provider") {
          if (failure === "timeout") return new Promise<void>(() => {});
          throw new Error("provider failed");
        }
        activated.push(id);
      },
    }));
    const pending = runtime.sync([
      dependencyPlugin("consumer", ["provider"]), dependencyPlugin("provider"), dependencyPlugin("independent"),
      dependencyPlugin("transitive", ["consumer"]),
    ]);
    await vi.advanceTimersByTimeAsync(30_000);
    const failures = await pending;
    expect(activated).toEqual(["user:independent"]);
    expect(failures.map((failure) => failure.pluginId).sort()).toEqual(["user:consumer", "user:provider", "user:transitive"]);
    await runtime.sync([]);
  });

  it.each(["reject", "bridge", "import", "register"])("keeps optional %s failure nonblocking and stable, then rebinds on recovery", async (failure) => {
    vi.useFakeTimers();
    const events: string[] = [];
    let fail = true;
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => {
      if (id === "user:provider" && fail && failure === "bridge") return new Promise<never>(() => {});
      return { id, fingerprint, digest: "a".repeat(64), url: id };
    }));
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), async (id) => {
      if (id === "user:provider" && fail && failure === "import") return new Promise(() => {});
      return { activate(api: { registerCleanup(fn: () => void): void }) {
        events.push(id);
        if (id === "user:provider" && fail) {
          if (failure === "register") return new Promise<void>(() => {});
          throw new Error("optional provider failed");
        }
        api.registerCleanup(() => events.push(`stop:${id}`));
      } };
    });
    const consumer = { ...dependencyPlugin("consumer"), resolved_dependencies: ["provider"] };
    const inventory = [consumer, dependencyPlugin("provider")];
    const pending = runtime.sync(inventory);
    await vi.advanceTimersByTimeAsync(30_000);
    const failures = await pending;
    const startedProvider = failure === "reject" || failure === "register";
    expect(events).toEqual(startedProvider ? ["user:provider", "user:consumer"] : ["user:consumer"]);
    expect(failures.map((failure) => failure.pluginId)).toEqual(["user:provider"]);
    events.length = 0;
    const retry = runtime.sync(inventory);
    await vi.advanceTimersByTimeAsync(30_000);
    await retry;
    expect(events).toEqual(startedProvider ? ["user:provider"] : []);
    fail = false;
    events.length = 0;
    await runtime.sync(inventory);
    expect(events).toEqual(["user:provider", "stop:user:consumer", "user:consumer"]);
    events.length = 0;
    await runtime.sync(inventory);
    expect(events).toEqual([]);
    await runtime.sync([]);
  });

  it("loads approved generations once and unloads disabled plugins", async () => {
    const load = vi.fn(async () => ({
      activate: (api: { registerStyle(style: { id: string; css: string }): unknown }) => {
        api.registerStyle({ id: "theme", css: ".plugin-theme {}" });
      },
    }));
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
        id,
        fingerprint,
        digest: "a".repeat(64),
        url: "wuu-plugin://module/" + "a".repeat(64) + ".js",
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, load);
    const plugin = inventoryPlugin();

    expect(await runtime.sync([plugin])).toEqual([]);
    expect(await runtime.sync([plugin])).toEqual([]);
    expect(load).toHaveBeenCalledTimes(1);
    expect(document.head.querySelector("style[data-wuu-plugin-id='user:demo']")).not.toBeNull();

    await runtime.sync([{ ...plugin, enabled: false }]);
    expect(document.head.querySelector("style[data-wuu-plugin-id='user:demo']")).toBeNull();
  });

  it("suppresses desktop execution in safe mode and unloads prior contributions", async () => {
    const cleanup = vi.fn();
    const activate = vi.fn((api: { registerCleanup(fn: () => void): void }) => {
      api.registerCleanup(cleanup);
    });
    const load = vi.fn(async () => ({ activate }));
    const read = vi.fn<WuuDesktopApi["loadPluginDesktopModule"]>(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: "wuu-plugin://module/demo.js",
    }));
    installDesktopModuleLoader(read);
    const runtime = new DesktopPluginRuntime(new PluginHost({ react: React }), load);
    const plugin = inventoryPlugin();

    expect(await runtime.sync([plugin], true)).toEqual([]);
    expect(read).not.toHaveBeenCalled();
    expect(activate).not.toHaveBeenCalled();

    // Core marks desktop-only plugins active even without a backend process.
    expect(await runtime.sync([plugin], false)).toEqual([]);
    expect(activate).toHaveBeenCalledTimes(1);
    expect(await runtime.sync([plugin], true)).toEqual([]);
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(read).toHaveBeenCalledTimes(1);
    expect(await runtime.sync([plugin], false)).toEqual([]);
    expect(activate).toHaveBeenCalledTimes(2);
    await runtime.sync([]);
  });

  it.each(["disable", "safe-mode"])("removes active contributions before conflict preferences settle on %s", async (action) => {
    const cleanup = vi.fn();
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: "wuu-plugin://module/demo.js",
    })));
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, async () => ({
      activate(api: { registerCleanup(fn: () => void): void }) { api.registerCleanup(cleanup); },
    }));
    const plugin = inventoryPlugin();
    await runtime.sync([plugin]);
    let finish!: (value: Record<string, string>) => void;
    window.wuu!.getPluginConflictPreferences = vi.fn(() => new Promise<Record<string, string>>((resolve) => { finish = resolve; }));
    const pending = runtime.sync(action === "disable" ? [{ ...plugin, enabled: false }] : [plugin], action === "safe-mode");
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(host.isGenerationActive(plugin.id, plugin.fingerprint!)).toBe(false);
    finish({});
    expect(await pending).toEqual([]);
  });

  it.each([
    ["bridge", "safe-mode"], ["import", "safe-mode"], ["register", "safe-mode"],
    ["bridge", "newer-sync"], ["import", "newer-sync"], ["register", "newer-sync"],
    ["bridge", "timeout"], ["import", "timeout"], ["register", "timeout"],
  ])("settles stalled %s on %s without allowing late activation", async (stage, action) => {
    vi.useFakeTimers();
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    let started!: () => void;
    const staging = new Promise<void>((resolve) => { started = resolve; });
    let wait = true;
    const suspend = async (point: string): Promise<void> => {
      if (stage !== point || !wait) return;
      started();
      await gate;
    };
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => {
      await suspend("bridge");
      return { id, fingerprint, digest: "a".repeat(64), url: fingerprint };
    }));
    const cleanup = vi.fn();
    let candidate: PluginGenerationApi | undefined;
    const activate = vi.fn(async (api: PluginGenerationApi) => {
      if (wait) candidate = api;
      api.registerCleanup(cleanup);
      await suspend("register");
      api.registerCommand({ id: "working", title: "Working", execute: () => api.generation });
    });
    const host = new PluginHost({ react: React, activationTimeoutMs: 100 });
    const runtime = new DesktopPluginRuntime(host, async () => {
      await suspend("import");
      return { activate };
    });
    const plugin = inventoryPlugin();
    const pending = runtime.sync([plugin]);
    await staging;
    if (action === "timeout") {
      await vi.advanceTimersByTimeAsync(100);
      expect(await pending).toEqual([expect.objectContaining({ pluginId: plugin.id })]);
    } else {
      wait = false;
      expect(await runtime.sync(action === "newer-sync" ? [{ ...plugin, fingerprint: "two" }] : [], action === "safe-mode")).toEqual([]);
      expect(await pending).toEqual([]);
    }
    expect(candidate?.signal.aborted).toBe(stage === "register" ? true : undefined);
    expect(host.isGenerationActive(plugin.id, plugin.fingerprint!)).toBe(false);
    expect(cleanup).toHaveBeenCalledTimes(stage === "register" ? 1 : 0);
    const calls = activate.mock.calls.length;
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(activate).toHaveBeenCalledTimes(calls);
    expect(host.getCommands().map((command) => command.generation)).toEqual(action === "newer-sync" ? ["two"] : []);
    expect(host.getGenerationDiagnostics(plugin.id, plugin.fingerprint!)).toHaveLength(action === "timeout" ? 1 : 0);
    await runtime.sync([]);
  });

  it.each([
    ["import", "source"], ["register", "source"], ["timeout", "source"],
    ["import", "graph"], ["register", "graph"], ["timeout", "graph"],
  ])("keeps the last working generation after replacement %s fails on %s change", async (failure, change) => {
    vi.useFakeTimers();
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: `wuu-plugin://module/${fingerprint}.js`,
    })));
    const cleanup = vi.fn();
    let replacing = false;
    const load = vi.fn(async () => {
      if (replacing) {
        if (failure === "timeout") return { activate() { return new Promise<void>(() => {}); } };
        if (failure === "import") throw new Error("replacement import failed");
        return { activate() { throw new Error("replacement registration failed"); } };
      }
      return { activate(api: {
        registerCleanup(fn: () => void): void;
        registerCommand(command: { id: string; title: string; execute(): string }): unknown;
      }) {
        api.registerCleanup(cleanup);
        api.registerCommand({ id: "working", title: "Working", execute: () => "last good" });
      } };
    });
    const host = new PluginHost({ react: React });
    const runtime = new DesktopPluginRuntime(host, load);
    const plugin = inventoryPlugin();
    expect(await runtime.sync([plugin])).toEqual([]);
    replacing = true;
    const replacement = change === "graph" ? { ...plugin, graph_generation: "replacement-graph" } : { ...plugin, fingerprint: "fingerprint-two" };
    const pending = runtime.sync([replacement]);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(await pending).toEqual([
      expect.objectContaining({ pluginId: plugin.id, fingerprint: replacement.fingerprint }),
    ]);
    expect(cleanup).not.toHaveBeenCalled();
    expect(host.isGenerationActive(plugin.id, plugin.fingerprint!)).toBe(true);
    expect(host.getCommands()[0]!.execute()).toBe("last good");
    // Returning to the working inventory does not activate an already live run again.
    expect(await runtime.sync([plugin])).toEqual([]);
    expect(load).toHaveBeenCalledTimes(2);
    await runtime.sync([replacement], true);
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(host.getCommands()).toEqual([]);
  });

  it("keeps activation failures isolated", async () => {
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
        id,
        fingerprint,
        digest: "b".repeat(64),
        url: "wuu-plugin://module/" + "b".repeat(64) + ".js",
    })));
    const runtime = new DesktopPluginRuntime(
      new PluginHost({ react: React }),
      async () => ({ activate: () => { throw new Error("activation failed"); } }),
    );

    const failures = await runtime.sync([inventoryPlugin()]);
    expect(failures).toEqual([
      expect.objectContaining({ pluginId: "user:demo", fingerprint: "fingerprint-one" }),
    ]);
  });

  it("enforces manifest declarations for desktop UI registrations", async () => {
    installDesktopModuleLoader(vi.fn(async ({ id, fingerprint }) => ({
      id,
      fingerprint,
      digest: "c".repeat(64),
      url: "wuu-plugin://module/" + "c".repeat(64) + ".js",
    })));
    const runtime = new DesktopPluginRuntime(
      new PluginHost({ react: React }),
      async () => ({
        activate: (api: { registerPresenter(contribution: { id: string; target: string; mode: string; render(): null }): unknown }) => {
          api.registerPresenter({ target: "conversation.item", mode: "wrap", id: "undeclared", render: () => null });
        },
      }),
    );

    const failures = await runtime.sync([inventoryPlugin()]);
    expect(failures[0]?.error).toEqual(expect.objectContaining({
      message: expect.stringContaining("is not declared in the manifest"),
    }));
  });
});

function inventoryPlugin(): ExtensionInventoryRecord {
  return {
    id: "user:demo",
    name: "demo",
    kind: "plugin",
    provenance: { kind: "plugin", source: "user", scope: "user" },
    state: "granted",
    approval_state: "granted",
    enabled: true,
    runtime_state: "active",
    fingerprint: "fingerprint-one",
    desktop: { entry: "desktop.js" },
  };
}

function installDesktopModuleLoader(
  loader: WuuDesktopApi["loadPluginDesktopModule"],
): void {
  const api = { ...(window.wuu ?? {}) } as WuuDesktopApi;
  api.loadPluginDesktopModule = loader;
  window.wuu = api;
}

function dependencyPlugin(id: string, requires: string[] = []): ExtensionInventoryRecord {
  const plugin = inventoryPlugin();
  return { ...plugin, id: `user:${id}`, provenance: { ...plugin.provenance, plugin_id: id }, requires };
}
