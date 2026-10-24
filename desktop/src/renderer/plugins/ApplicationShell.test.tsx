import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { PluginDesktopActivationParams } from "../../shared/protocol";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { ApplicationPreviewContent } from "./ApplicationPreviewContent";
import { createApplicationSettingsControls } from "./hostApplicationSettingsControls";
import { ApplicationShellOutlet } from "./ApplicationShell";

function fixture(options: Partial<PluginHostOptions> = {}) {
  const listeners = new Set<() => void>();
  const service: ApplicationServiceV1 = {
    contractVersion: 1,
    settingsControls: createApplicationSettingsControls(),
    conversationText: { getSnapshot: () => undefined, subscribe: () => () => {} },
    previewContent: new ApplicationPreviewContent({ platform: "linux",
      listWorkspaceDirectory: async () => { throw new Error("Not configured"); },
      readWorkspaceFile: async () => { throw new Error("Not configured"); },
      revealWorkspaceItem: async () => {}, showWorkspaceItemMenu: async () => ({ action: "none" }),
    }),
    getSnapshot: vi.fn(),
    subscribe: (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    invoke: vi.fn(async () => undefined) as ApplicationServiceV1["invoke"],
  };
  return { host: new PluginHost({ react: React, application: service, ...options }), service, listeners };
}

// Failure cases: no implicit root fallback, stale handles after same-fingerprint
// reload, child ownership/unload, single-slot conflicts, and recursive slots.
describe("application shell assembly", () => {
  it.each(["shell/navigation", "canonical/navigation"])("selects an independently owned surface from %s and fails visibly when its provider unloads", async target => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "shell", generation: "root", register: api => {
      api.registerUISlot({ id: "navigation", cardinality: "one", service: "ui.navigation" });
      api.registerApplicationShell({ id: "ui.shell", render: ({ slots }) => <slots.Slot name="shell/navigation" fallback={<span>Implicit default</span>} /> });
    } });
    for (const pluginId of ["default", "alternate"]) await host.activateGeneration({ pluginId, generation: "v1", register: api => {
      api.registerUIContribution({ id: "ui.navigation", target, render: () => <span>{pluginId} navigation</span> });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    const render = (pluginId: string) => act(() => root.render(<ApplicationShellOutlet host={host}
      binding={{ pluginId: "shell", generation: "root", id: "ui.shell" }}
      surfaceBindings={{ "ui.navigation": { pluginId, generation: "v1", id: "ui.navigation" } }} />));
    render("alternate");
    expect(container.textContent).toBe("alternate navigation");
    render("default");
    expect(container.textContent).toBe("default navigation");
    act(() => host.unload("default"));
    expect(container.textContent).toContain("Selected UI surface is unavailable");
    expect(container.textContent).not.toContain("Implicit default");
    act(() => host.unload("shell"));
    expect(container.textContent).toContain("Selected application shell is unavailable");
    act(() => root.unmount());
  });

  it.each(["root", "nested"])("reports %s render failure without a healthy view overwriting it", async (target) => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    const { host } = fixture({ reportActivation: report });
    const broken = () => { throw new Error("private render detail"); };
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: api => {
      api.registerUISlot({ id: "body", cardinality: "many" });
      api.registerApplicationShell({ id: "ui.shell", render: target === "root" ? broken : ({ slots }) => <slots.Slot name="shell/body" /> });
    } });
    if (target === "nested") await host.activateGeneration({ pluginId: "child", generation: "source", graphGeneration: "graph", register: api => {
      api.registerUIContribution({ id: "broken", target: "shell/body", order: 0, render: broken });
      api.registerUIContribution({ id: "healthy", target: "shell/body", order: 1, render: () => <span>Healthy sibling</span> });
    } });
    const root = createRoot(document.createElement("div"));
    const logging = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "source", id: "ui.shell" }} />));
      await host.flushActivationReports();
      const owner = target === "root" ? "shell" : "child";
      const reports = report.mock.calls.map(call => call[0]).filter(item => item.id === owner);
      expect(reports[0]).toMatchObject({ phase: "registration", status: "active", graph_generation: "graph", activation: 1 });
      expect(reports.at(-1)).toMatchObject({ phase: "render", status: "failed" });
      if (target === "root") expect(reports.some(item => item.phase === "render" && item.status === "active")).toBe(false);
      expect(JSON.stringify(reports)).not.toContain("private render detail");
    } finally { act(() => root.unmount()); logging.mockRestore(); }
  });

  it("reports provider cleanup after view cleanup and preserves cleanup failures", async () => {
    const order: string[] = [];
    const report = vi.fn(async (params: PluginDesktopActivationParams) => { order.push(`${params.phase}:${params.status}`); });
    const { host } = fixture({ reportActivation: report });
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: api => {
      api.registerCleanup(() => { order.push("provider-cleanup"); throw new Error("private cleanup detail"); });
      api.registerApplicationShell({ id: "ui.shell", render: () => {
        React.useEffect(() => () => { order.push("view-cleanup"); }, []);
        return <main>Mounted</main>;
      } });
    } });
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "source", id: "ui.shell" }} />));
    await act(async () => { host.unload("shell"); });
    await host.flushActivationReports();
    expect(order.indexOf("view-cleanup")).toBeLessThan(order.indexOf("provider-cleanup"));
    expect(order.at(-1)).toBe("cleanup:failed");
    expect(JSON.stringify(report.mock.calls)).not.toContain("private cleanup detail");
    act(() => root.unmount());
  });

  it("reports failed registration before cleanup and numbers same-code retries", async () => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    const { host } = fixture({ reportActivation: report });
    await expect(host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => { throw new Error("broken"); } })).rejects.toThrow("broken");
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => {} });
    await host.flushActivationReports();
    expect(report.mock.calls.map(call => call[0])).toMatchObject([
      { activation: 1, phase: "registration", status: "failed" },
      { activation: 1, phase: "cleanup", status: "disposed" },
      { activation: 2, phase: "registration", status: "active" },
    ]);
  });

  it("does not send late disposal of a retained activation after the replacement registers", async () => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    const { host } = fixture({ reportActivation: report });
    const activate = () => host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => {} });
    await activate();
    const release = host.retainGeneration("shell", "source");
    await activate();
    await host.flushActivationReports();
    release(); await Promise.resolve(); await host.flushActivationReports();
    const reports = report.mock.calls.map(call => call[0]);
    expect(reports.at(-1)).toMatchObject({ activation: 2, phase: "registration", status: "active" });
    expect(reports.filter(item => item.activation === 1 && item.phase === "cleanup")).toHaveLength(0);
  });

  it("clears a reporting failure only after that module registers successfully again", async () => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    report.mockRejectedValueOnce(new Error("temporary failure"));
    const { host } = fixture({ reportActivation: report });
    const activate = (pluginId: string) => host.activateGeneration({ pluginId, generation: "source", graphGeneration: "graph", register: () => {} });
    await activate("failed"); await host.flushActivationReports();
    await activate("healthy"); await host.flushActivationReports();
    expect(host.getActivationReportFailure()).toContain("temporary failure");
    await activate("failed"); await host.flushActivationReports();
    expect(host.getActivationReportFailure()).toBeUndefined();
  });

  it("keeps committed UI live but exposes a mandatory ACK transport failure", async () => {
    const { host } = fixture({ reportActivation: async () => { throw new Error("bridge unavailable"); } });
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => {} });
    await host.flushActivationReports();
    expect(host.hasActivePlugin("shell")).toBe(true);
    expect(host.getActivationReportFailure()).toContain("bridge unavailable");
  });


  it("renders only the exact selected provider and removes its UI on unload", async () => {
    const { host } = fixture();
    for (const id of ["default", "research"]) await host.activateGeneration({
      pluginId: id, generation: "v1", register: api => {
        api.registerApplicationShell({ id: "ui.shell", render: () => <main>{id}</main> });
      },
    });
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "research", generation: "v1", id: "ui.shell" }} />));
      expect(container.querySelector("main")?.textContent).toBe("research");
      act(() => host.unload("research"));
      expect(container.querySelector("main")).toBeNull();
      expect(container.textContent).not.toContain("default");
      expect(container.querySelector('[role="alert"]')).not.toBeNull();
    } finally { act(() => root.unmount()); }
  });

  it("aborts settings reads and subscriptions with the generation and fences captured methods", async () => {
    const { host, service } = fixture();
    let notify!: () => void;
    const stopped = vi.fn();
    let finish!: (value: { servers: [] }) => void;
    const list = vi.spyOn(service.settingsControls, "listMCPServers").mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    vi.spyOn(service.settingsControls, "subscribeRemote").mockImplementation(listener => { notify = listener; return stopped; });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "settings", generation: "v1", register: value => { api = value; } });
    const controls = api.application.settingsControls;
    const listener = vi.fn();
    const unsubscribe = controls.subscribeRemote(listener);
    const capturedRead = controls.listMCPServers;
    const pending = capturedRead();
    const rejected = expect(pending).rejects.toThrow();
    await host.activateGeneration({ pluginId: "settings", generation: "v2", register: () => {} });
    await rejected;
    notify();
    expect(listener).not.toHaveBeenCalled();
    expect(stopped).toHaveBeenCalledTimes(1);
    unsubscribe();
    expect(stopped).toHaveBeenCalledTimes(1);
    finish({ servers: [] });
    await expect(capturedRead()).rejects.toThrow(/no longer active/);
    expect(list).toHaveBeenCalledTimes(1);
  });

  it("fences nested preferences, deferred background updates and typed subscriptions on replacement", async () => {
    const { host, service } = fixture();
    const controls = service.settingsControls;
    const setAlias = vi.fn();
    const stop = vi.fn();
    let notify!: (theme: "light" | "dark") => void;
    let update!: Parameters<typeof controls.updateBackground>[0];
    let finish!: () => void;
    Object.defineProperty(controls, "connectionDisplay", { value: { ...controls.connectionDisplay, setAlias } });
    vi.spyOn(controls, "observeAppliedTheme").mockImplementation(listener => { notify = listener; return stop; });
    vi.spyOn(controls, "updateBackground").mockImplementation(callback => {
      update = callback; return new Promise(resolve => { finish = resolve; });
    });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "settings", generation: "v1", register: value => { api = value; } });
    const scoped = api.application.settingsControls;
    const capturedAlias = scoped.connectionDisplay.setAlias;
    const listener = vi.fn();
    const unsubscribe = scoped.observeAppliedTheme(listener);
    notify("dark"); expect(listener).toHaveBeenCalledWith("dark");
    const updater = vi.fn(() => null);
    const pending = scoped.updateBackground(updater);
    const rejected = expect(pending).rejects.toThrow();
    await host.activateGeneration({ pluginId: "settings", generation: "v2", register: () => {} });
    await rejected;
    expect(() => capturedAlias("provider", "Old")).toThrow();
    expect(setAlias).not.toHaveBeenCalled();
    expect(() => update(null)).toThrow(); expect(updater).not.toHaveBeenCalled();
    notify("light"); expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe(); expect(stop).toHaveBeenCalledTimes(1);
    finish();
  });

  it("rejects a removed preview generation promptly and fences its captured file methods", async () => {
    const { host, service } = fixture();
    let finish!: (value: Awaited<ReturnType<typeof service.previewContent.readFile>>) => void;
    let sourceSignal!: AbortSignal;
    const read = vi.spyOn(service.previewContent, "readFile").mockImplementation((_target, options) => {
      sourceSignal = options!.signal!;
      return new Promise(resolve => { finish = resolve; });
    });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "preview", generation: "v1", register: value => { api = value; } });
    const captured = api.application.previewContent.readFile;
    const pending = captured({ root: "/workspace", path: "file.txt" });
    const rejected = expect(pending).rejects.toThrow();
    await host.activateGeneration({ pluginId: "preview", generation: "v2", register: () => {} });
    await rejected;
    expect(sourceSignal.aborted).toBe(true);
    await expect(captured({ root: "/workspace", path: "other.txt" })).rejects.toThrow();
    expect(read).toHaveBeenCalledTimes(1);
    finish({ root: "/workspace", path: "file.txt", absolute_path: "/workspace/file.txt", size_bytes: 1, mtime_ms: 0, sha256: "digest", binary: false, truncated: false });
  });

  it("disposes subscriptions and fences old application handles even after same-code reactivation", async () => {
    const { host, service, listeners } = fixture();
    let old!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => { old = api; } });
    old.application.subscribe(() => {});
    expect(listeners.size).toBe(1);
    await old.application.invoke("composer.set-draft", { text: "hello" });
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: () => {} });
    expect(listeners.size).toBe(0);
    await expect(old.application.invoke("composer.submit", {})).rejects.toThrow(/no longer active/);
    expect(service.invoke).toHaveBeenCalledTimes(1);
  });

  it.each(["user", "project"])("composes manifest-named slots for %s installations and removes only their owner", async (scope) => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: `plugin:${scope}:shell`, manifestId: "shell", generation: "v1", register: api => {
      api.registerUISlot({ id: "body", cardinality: "many" });
      api.registerApplicationShell({ id: "ui.shell", render: ({ slots }) => <main><slots.Slot name="shell/body" /></main> });
    } });
    await host.activateGeneration({ pluginId: `plugin:${scope}:panel`, manifestId: "panel", generation: "v1", register: api => {
      api.registerUISlot({ id: "children", cardinality: "many" });
      api.registerUIContribution({ id: "panel", target: "shell/body", render: ({ slots }) => <section>Panel<slots.Slot name="panel/children" /></section> });
    } });
    await host.activateGeneration({ pluginId: `plugin:${scope}:evidence`, manifestId: "evidence", generation: "v1", register: api => {
      api.registerUIContribution({ id: "panel", target: "panel/children", render: () => <span>Evidence</span> });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: `plugin:${scope}:shell`, generation: "v1", id: "ui.shell" }} />));
      expect(container.textContent).toBe("PanelEvidence");
      act(() => host.unload(`plugin:${scope}:evidence`));
      expect(container.textContent).toBe("Panel");
      act(() => host.unload(`plugin:${scope}:panel`));
      expect(container.textContent).toBe("");
    } finally { act(() => root.unmount()); }
  });

  it("rejects conflicting single-slot staging without disposing the previous provider", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "owner", generation: "v1", register: api => {
      api.registerUISlot({ id: "only", cardinality: "one" });
    } });
    const render = () => <span>One</span>;
    await host.activateGeneration({ pluginId: "one", generation: "v1", register: api => {
      api.registerUIContribution({ id: "view", target: "owner/only", render });
    } });
    await expect(host.activateGeneration({ pluginId: "two", generation: "v1", register: api => {
      api.registerUIContribution({ id: "view", target: "owner/only", render });
    } })).rejects.toThrow(/conflict/i);
    expect(host.getUIContributions("owner/only").map(item => item.pluginId)).toEqual(["one"]);
  });

  it("remounts a nested contribution when the same module fingerprint is reactivated", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => {
      api.registerUISlot({ id: "body", cardinality: "many" });
      api.registerApplicationShell({ id: "ui.shell", render: ({ slots }) => <slots.Slot name="shell/body" /> });
    } });
    const mounted = vi.fn();
    const cleanup = vi.fn();
    const View = () => {
      React.useEffect(() => { mounted(); return cleanup; }, []);
      return <span>Evidence</span>;
    };
    const activate = () => host.activateGeneration({ pluginId: "child", generation: "same-code", register: api => {
      api.registerUIContribution({ id: "evidence", target: "shell/body", render: View });
    } });
    await activate();
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "v1", id: "ui.shell" }} />));
      await act(async () => { await activate(); });
      expect(container.textContent).toBe("Evidence");
      expect(cleanup).toHaveBeenCalledTimes(1);
      expect(mounted).toHaveBeenCalledTimes(2);
    } finally { act(() => root.unmount()); }
  });
  it("retains a provider until mounted shell effects finish cleanup", async () => {
    const { host } = fixture();
    const order: string[] = [];
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => {
      api.registerCleanup(() => { order.push("provider"); });
      api.registerApplicationShell({ id: "ui.shell", render: () => {
        React.useEffect(() => () => { order.push("view"); }, []);
        return <main>Mounted</main>;
      } });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "v1", id: "ui.shell" }} />));
    let cleanup: Promise<void> | undefined;
    act(() => { cleanup = host.unloadAndWait("shell"); });
    await cleanup;
    expect(order).toEqual(["view", "provider"]);
    act(() => root.unmount());
  });

});
