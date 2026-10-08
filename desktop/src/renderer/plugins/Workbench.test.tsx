import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ExtensionInventoryRecord } from "../../shared/protocol";
import {
  STATUS_ACTIONS,
  type PresentationHost,
  type StatusSnapshotV1,
} from "../../shared/workbench";
import { RichContent } from "../RichContent";
import { desktopPluginHost } from "./DesktopPluginRuntime";
import { DesktopWorkbench, visibleWorkbenchView, WorkbenchController } from "./Workbench";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";

describe("WorkbenchController", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.dataset.theme = "dark";
  });

  afterEach(() => {
    window.localStorage.clear();
    document.documentElement.removeAttribute("style");
    document.documentElement.removeAttribute("data-theme");
  });

  it("updates service handles without publishing an unchanged snapshot", () => {
    const host = new PluginHost({ react: React });
    const controller = new WorkbenchController(host);
    const listener = vi.fn();
    const unsubscribe = controller.subscribe(listener);
    const services = { openSettings: vi.fn() };

    controller.updateServices(services);

    expect(controller.services).toBe(services);
    expect(listener).not.toHaveBeenCalled();
    unsubscribe();
    controller.dispose();
  });

  it("maps views to every semantic region and persists only durable state", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:views",
      generation: "one",
      register(api) {
        api.registerViewType({
          id: "views.dashboard",
          title: "Dashboard",
          defaultRegion: "primary",
          persistence: "durable",
          render: () => <div>Dashboard</div>,
        });
        api.registerViewPlacement({
          id: "default-dashboard",
          region: "navigation",
          view: "views.dashboard",
        });
      },
    });
    const controller = new WorkbenchController(host);
    controller.setAvailablePluginIds(new Set(["user:views"]));

    for (const region of ["navigation", "primary", "auxiliary", "inspector", "settings", "overlay"] as const) {
      await controller.openView("views.dashboard", { region, context: { region } });
    }

    expect(new Set(controller.getSnapshot().views.map((view) => view.region))).toEqual(
      new Set(["navigation", "primary", "auxiliary", "inspector", "settings", "overlay"]),
    );
    expect(controller.getSnapshot().views).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: "placement:user:views:default-dashboard", region: "navigation" }),
    ]));

    const restored = new WorkbenchController(host);
    restored.setAvailablePluginIds(new Set(["user:views"]));
    expect(restored.getSnapshot().views.every((view) => view.persistence === "durable")).toBe(true);
    expect(restored.getSnapshot().views.map((view) => view.region)).toEqual(
      expect.arrayContaining(["navigation", "primary", "auxiliary", "inspector", "settings", "overlay"]),
    );
    controller.dispose();
    restored.dispose();
  });

  it("asks the shell to reveal the placement region when opening a view", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:views",
      generation: "one",
      register(api) {
        api.registerViewType({ id: "views.dashboard", title: "Dashboard", render: () => null });
      },
    });
    const requestRegionVisible = vi.fn();
    const controller = new WorkbenchController(host);
    controller.updateServices({ requestRegionVisible });

    await controller.openView("views.dashboard", { region: "auxiliary" });
    expect(requestRegionVisible).toHaveBeenCalledWith("auxiliary");

    // Revealing an already-open view also asks for the region.
    requestRegionVisible.mockClear();
    await controller.openView("views.dashboard", { region: "auxiliary" });
    expect(requestRegionVisible).toHaveBeenCalledWith("auxiliary");
    controller.dispose();
  });

  it("reveals an existing plugin View and can hide its region without destroying state", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "product",
      generation: "one",
      register(api) {
        api.registerViewType({ id: "dashboard", title: "Dashboard", render: () => null });
      },
    });
    const controller = new WorkbenchController(host);

    const first = await controller.openPluginView("product", "dashboard", { region: "primary" });
    const revealed = await controller.openPluginView("product", "dashboard", { region: "primary" });
    expect(revealed).toBe(first);
    expect(controller.getSnapshot().views).toHaveLength(1);
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")?.view.id).toBe(first);

    controller.deactivateRegion("primary");
    expect(controller.getSnapshot().views).toHaveLength(1);
    expect(controller.getSnapshot().activeViewByRegion.primary).toBe(first);
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")).toBeUndefined();

    expect(await controller.openPluginView("product", "dashboard", { region: "primary" })).toBe(first);
    expect(controller.getSnapshot().activeViewByRegion.primary).toBe(first);
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")?.view.id).toBe(first);
    controller.dispose();
  });

  it("uses placement priority only for the initial default and preserves user dismissal", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:priority",
      generation: "one",
      register(api) {
        api.registerViewType({
          id: "views.low",
          title: "Low",
          persistence: "durable",
          render: () => null,
        });
        api.registerViewType({
          id: "views.high",
          title: "High",
          persistence: "durable",
          render: () => null,
        });
        api.registerViewPlacement({
          id: "low",
          view: "views.low",
          region: "primary",
          priority: 10,
        });
        api.registerViewPlacement({
          id: "high",
          view: "views.high",
          region: "primary",
          priority: 20,
        });
      },
    });
    const controller = new WorkbenchController(host);
    controller.setAvailablePluginIds(new Set(["user:priority"]));

    const initial = controller.getSnapshot();
    const high = initial.views.find((view) => view.viewTypeId === "views.high");
    expect(initial.activeViewByRegion.primary).toBe(high?.id);
    expect(high).toBeDefined();
    if (!high) throw new Error("expected high-priority View placement");

    await controller.closeView(high.id);
    expect(controller.getSnapshot().activeViewByRegion.primary).toBe(
      controller.getSnapshot().views.find((view) => view.viewTypeId === "views.low")?.id,
    );

    const restored = new WorkbenchController(host);
    restored.setAvailablePluginIds(new Set(["user:priority"]));
    expect(restored.getSnapshot().views.some((view) => view.viewTypeId === "views.high")).toBe(false);
    controller.dispose();
    restored.dispose();
  });

  it("exposes controlled commands, settings, and plugin-namespaced storage", async () => {
    const execute = vi.fn((input?: unknown) => ({ accepted: input }));
    const getSetting = vi.fn((_pluginId: string, _generation: string, key: string) => key === "density" ? "compact" : null);
    const stored = new Map<string, string>();
    const getStorage = vi.fn(async (pluginId: string, _generation: string, key: string, scope: string) => stored.get(`${pluginId}:${scope}:${key}`) ?? null);
    const setStorage = vi.fn(async (pluginId: string, _generation: string, key: string, value: string, scope: string) => { stored.set(`${pluginId}:${scope}:${key}`, value); });
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:actions",
      generation: "one",
      register(api) {
        api.registerViewType({ id: "actions.view", title: "Actions", render: () => null });
        api.registerCommand({ id: "actions.run", title: "Run", execute });
        api.registerRenderer({
          id: "actions.low",
          category: "document",
          match: "text/plain",
          priority: 1,
          render: () => null,
        });
        api.registerRenderer({
          id: "actions.high",
          category: "document",
          match: "text/plain",
          priority: 10,
          render: () => null,
        });
      },
    });
    const controller = new WorkbenchController(host, { getSetting, getStorage, setStorage });
    const instanceId = await controller.openView("actions.view");
    const view = controller.getSnapshot().views.find((candidate) => candidate.id === instanceId);
    expect(view).toBeDefined();
    if (!view) return;
    const api = controller.createViewHostAPI(view);

    await api.setStorage("panel.mode", "focused");
    expect(await api.getStorage("panel.mode")).toBe("focused");
    expect(await api.getSetting("density")).toBe("compact");
    expect(getStorage).toHaveBeenCalledWith("user:actions", "one", "panel.mode", "workspace");
    expect(await api.executeCommand("actions.run", 7)).toEqual({ accepted: 7 });
    expect(execute).toHaveBeenCalledWith(7);
    expect(controller.getRenderer("document", "text/plain")?.id).toBe("actions.high");
    await expect(api.getStorage("../private")).rejects.toThrow("storage key is invalid");

    await host.activateGeneration({
      pluginId: "user:other",
      generation: "one",
      register(api) {
        api.registerViewType({ id: "other.view", title: "Other", render: () => null });
      },
    });
    const otherId = await controller.openView("other.view");
    const other = controller.getSnapshot().views.find((candidate) => candidate.id === otherId);
    expect(other).toBeDefined();
    if (other) expect(await controller.createViewHostAPI(other).getStorage("panel.mode")).toBeNull();
    host.unload("user:actions");
    await expect(api.getSetting("density")).rejects.toThrow("no longer active");
    controller.dispose();
  });

  it("keeps colliding view IDs bound to their plugin across placements, persistence, and reloads", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:alpha",
      generation: "one",
      register: (api) => registerCollidingGeneration(api, "alpha", "one"),
    });
    await host.activateGeneration({
      pluginId: "user:beta",
      generation: "one",
      register: (api) => registerCollidingGeneration(api, "beta", "one"),
    });
    const availablePlugins = new Set(["user:alpha", "user:beta"]);
    const controller = new WorkbenchController(host);
    controller.setAvailablePluginIds(availablePlugins);

    expect(controller.getSnapshot().views).toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: "placement:user:alpha:default-shared",
        pluginId: "user:alpha",
        generation: "one",
      }),
      expect.objectContaining({
        id: "placement:user:beta:default-shared",
        pluginId: "user:beta",
        generation: "one",
      }),
    ]));
    await expect(controller.openView("shared.view")).rejects.toThrow("view type is ambiguous");

    const alphaLauncherId = await controller.openView("alpha.launcher");
    const alphaLauncher = controller.getSnapshot().views.find((view) => view.id === alphaLauncherId);
    expect(alphaLauncher).toBeDefined();
    if (!alphaLauncher) return;
    const alphaHost = controller.createViewHostAPI(alphaLauncher);
    await alphaHost.openView("shared.view", { region: "primary" });
    await alphaHost.openView("beta.unique", { region: "inspector" });
    expect(controller.getSnapshot().views).toEqual(expect.arrayContaining([
      expect.objectContaining({ viewTypeId: "shared.view", pluginId: "user:alpha", region: "primary" }),
      expect.objectContaining({ viewTypeId: "beta.unique", pluginId: "user:beta", region: "inspector" }),
    ]));

    const restored = new WorkbenchController(host);
    restored.setAvailablePluginIds(availablePlugins);
    expect(restored.getSnapshot().views.filter((view) => view.viewTypeId === "shared.view")).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ pluginId: "user:alpha", generation: "one" }),
        expect.objectContaining({ pluginId: "user:beta", generation: "one" }),
      ]),
    );

    await host.activateGeneration({
      pluginId: "user:alpha",
      generation: "two",
      register: (api) => registerCollidingGeneration(api, "alpha", "two"),
    });
    const reloadedSharedViews = restored.getSnapshot().views.filter((view) =>
      view.viewTypeId === "shared.view");
    expect(new Set(reloadedSharedViews
      .filter((view) => view.pluginId === "user:alpha")
      .map((view) => view.generation))).toEqual(new Set(["two"]));
    expect(new Set(reloadedSharedViews
      .filter((view) => view.pluginId === "user:beta")
      .map((view) => view.generation))).toEqual(new Set(["one"]));

    controller.dispose();
    restored.dispose();
  });

  it("reconciles generation resources without retaining stale views, renderers, tokens, or status", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:reload",
      generation: "one",
      register: (api) => registerGeneration(api, "one"),
    });
    const controller = new WorkbenchController(host);
    controller.setAvailablePluginIds(new Set(["user:reload"]));
    const instanceId = await controller.openView("reload.view", { region: "primary" });
    expect(document.documentElement.style.getPropertyValue("--wuu-color-accent")).toBe("one");
    expect(document.documentElement.style.getPropertyValue("--wuu-font-family-ui")).toBe("one-ui");
    expect(document.documentElement.style.getPropertyValue("--wuu-syntax-keyword")).toBe("one-keyword");
    expect(controller.getRenderer("document", "text/demo")?.generation).toBe("one");
    expect(host.getStatusItems().map((item) => item.label)).toEqual(["one"]);

    await host.activateGeneration({
      pluginId: "user:reload",
      generation: "two",
      register: (api) => registerGeneration(api, "two"),
    });
    expect(controller.getSnapshot().views.find((view) => view.id === instanceId)?.generation).toBe("two");
    expect(controller.getRenderer("document", "text/demo")?.generation).toBe("two");
    expect(document.documentElement.style.getPropertyValue("--wuu-color-accent")).toBe("two");
    expect(document.documentElement.style.getPropertyValue("--wuu-font-family-ui")).toBe("two-ui");
    expect(host.getStatusItems().map((item) => item.label)).toEqual(["two"]);

    host.unload("user:reload");
    expect(controller.getSnapshot().views).toEqual([]);
    expect(controller.getRenderer("document", "text/demo")).toBeUndefined();
    expect(document.documentElement.style.getPropertyValue("--wuu-color-accent")).toBe("");
    expect(document.documentElement.style.getPropertyValue("--wuu-font-family-ui")).toBe("");
    expect(document.documentElement.style.getPropertyValue("--wuu-syntax-keyword")).toBe("");
    expect(host.getStatusItems()).toEqual([]);
    controller.dispose();
  });
});

describe("DesktopWorkbench product path", () => {
  let root: Root;
  let container: HTMLDivElement;

  beforeEach(() => {
    window.localStorage.clear();
    container = document.createElement("div");
    container.innerHTML = '<aside class="sidebar"></aside><main class="conversation-pane"></main>';
    document.body.appendChild(container);
    const workbenchRoot = document.createElement("div");
    workbenchRoot.dataset.workbenchRoot = "true";
    container.appendChild(workbenchRoot);
    root = createRoot(workbenchRoot);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    document.querySelectorAll(".plugin-workbench-status").forEach((item) => item.remove());
  });

  it("renders default View placements and restores built-in UI after unload", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:product",
      generation: "one",
      register(api) {
        api.registerViewType({ id: "product.view", title: "Product", render: () => <div>Plugin product view</div> });
        api.registerViewPlacement({
          id: "product-main",
          region: "primary",
          view: "product.view",
        });
        api.registerStatusItem({ id: "ready", label: "Plugin ready" });
      },
    });

    await act(async () => root.render(
      <DesktopWorkbench host={host} inventory={[inventoryPlugin("user:product")]} />,
    ));
    expect(container.querySelector(".conversation-pane")?.textContent).toContain("Plugin product view");
    expect(document.body.textContent).toContain("Plugin ready");

    await act(async () => host.unload("user:product"));
    expect(container.querySelector(".conversation-pane")?.textContent).not.toContain("Plugin product view");
    expect(document.body.textContent).not.toContain("Plugin ready");
  });

  it("passes the active locale and registered plugin translations to Views", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:localized-view",
      generation: "one",
      register(api) {
        api.registerLocale({
          id: "localized-view-zh",
          locale: "zh-CN",
          entries: { "localized.title": "本地化视图" },
        });
        api.registerViewType({
          id: "localized.view",
          title: "Localized",
          render: ({ locale, translate }) => <div>{locale}:{translate("localized.title")}</div>,
        });
        api.registerViewPlacement({ id: "localized-main", region: "primary", view: "localized.view" });
      },
    });

    await act(async () => root.render(
      <DesktopWorkbench host={host} inventory={[inventoryPlugin("user:localized-view")]} />,
    ));

    expect(container.querySelector(".conversation-pane")?.textContent).toContain("zh-CN:本地化视图");
  });

  it("replaces the complete status root with a sanitized immutable snapshot and controlled actions", async () => {
    const host = new PluginHost({ react: React });
    const execute = vi.fn();
    let presentedSnapshot: StatusSnapshotV1 | undefined;
    let presentationHost: PresentationHost | undefined;
    await host.activateGeneration({
      pluginId: "user:status-presenter",
      generation: "one",
      register(api) {
        api.registerPresenter({
          id: "status",
          target: "app.status",
          render({ snapshot, host: presenterHost }) {
            presentedSnapshot = snapshot as StatusSnapshotV1;
            presentationHost = presenterHost;
            return <section data-status-replacement>Wuu status</section>;
          },
        });
      },
    });

    await act(async () => root.render(<DesktopWorkbench host={host} />));
    expect(document.body.querySelector("[data-status-replacement]")?.parentElement).toBe(document.body);
    expect(document.body.querySelector(".plugin-workbench-status")).toBeNull();

    await act(async () => host.activateGeneration({
      pluginId: "user:status-source",
      generation: "one",
      register(api) {
        api.registerCommand({ id: "status.open", title: "Open", execute });
        api.registerStatusItem({
          id: "runtime",
          label: "Wuu running",
          icon: "pulse",
          tooltip: "Private tooltip",
          command: "status.open",
          priority: 12,
        });
      },
    }));

    expect(presentedSnapshot?.contractVersion).toBe(1);
    expect(presentedSnapshot?.items).toHaveLength(1);
    const item = presentedSnapshot?.items[0];
    expect(item).toEqual({
      id: JSON.stringify(["user:status-source", "runtime"]),
      label: "Wuu running",
      icon: "pulse",
      busy: false,
      disabled: false,
      actionId: STATUS_ACTIONS.activateItem,
    });
    expect(Object.isFrozen(presentedSnapshot)).toBe(true);
    expect(Object.isFrozen(presentedSnapshot?.items)).toBe(true);
    expect(Object.isFrozen(item)).toBe(true);
    expect(Object.keys(item ?? {})).not.toEqual(expect.arrayContaining([
      "pluginId", "generation", "command", "tooltip", "priority", "order",
    ]));

    await expect(presentationHost?.invoke(STATUS_ACTIONS.activateItem, { id: item?.id })).resolves.toBeUndefined();
    expect(execute).toHaveBeenCalledOnce();
    await expect(presentationHost?.invoke(STATUS_ACTIONS.activateItem, {})).rejects.toThrow("valid item id");
    await expect(presentationHost?.invoke(STATUS_ACTIONS.activateItem, { id: "missing" })).rejects.toThrow("not available");
    expect(execute).toHaveBeenCalledOnce();

    await act(async () => host.unload("user:status-source"));
    expect(presentedSnapshot?.items).toEqual([]);
  });

  it("keeps native additive status behavior through live registration, presenter failure, and unload", async () => {
    const host = new PluginHost({ react: React });
    const execute = vi.fn();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    await act(async () => root.render(<DesktopWorkbench host={host} />));
    expect(document.body.querySelector(".plugin-workbench-status")).toBeNull();

    await act(async () => host.activateGeneration({
      pluginId: "user:native-status",
      generation: "one",
      register(api) {
        api.registerCommand({ id: "status.run", title: "Run", execute });
        api.registerStatusItem({ id: "ready", label: "Wuu ready", icon: "check", tooltip: "Ready", command: "status.run" });
        api.registerStatusItem({ id: "passive", label: "Wuu idle" });
      },
    }));
    const nativeRoot = document.body.querySelector(".plugin-workbench-status");
    expect(nativeRoot?.getAttribute("role")).toBe("status");
    expect(nativeRoot?.textContent).toBe("Wuu idlecheckWuu ready");
    const readyButton = [...(nativeRoot?.querySelectorAll<HTMLButtonElement>("button") ?? [])]
      .find((button) => button.textContent === "checkWuu ready");
    expect(readyButton?.title).toBe("Ready");
    act(() => readyButton?.click());
    expect(execute).toHaveBeenCalledOnce();

    await act(async () => host.activateGeneration({
      pluginId: "user:broken-status",
      generation: "one",
      register(api) {
        api.registerPresenter({ id: "broken", target: "app.status", render: () => { throw new Error("status failed"); } });
      },
    }));
    expect(document.body.querySelector(".plugin-workbench-status")?.textContent).toBe("Wuu idlecheckWuu ready");
    expect(host.getGenerationDiagnostics("user:broken-status", "one")).toHaveLength(1);

    await act(async () => host.unload("user:broken-status"));
    expect(document.body.querySelector(".plugin-workbench-status")?.textContent).toBe("Wuu idlecheckWuu ready");
    await act(async () => host.unload("user:native-status"));
    expect(document.body.querySelector(".plugin-workbench-status")).toBeNull();
    consoleError.mockRestore();
  });

  it("renders colliding layout view IDs with each plugin's own definition", async () => {
    const host = new PluginHost({ react: React });
    await host.activateGeneration({
      pluginId: "user:alpha",
      generation: "one",
      register: (api) => registerCollidingGeneration(api, "alpha", "one"),
    });
    await host.activateGeneration({
      pluginId: "user:beta",
      generation: "one",
      register: (api) => registerCollidingGeneration(api, "beta", "one"),
    });

    await act(async () => root.render(
      <DesktopWorkbench
        host={host}
        inventory={[inventoryPlugin("user:alpha"), inventoryPlugin("user:beta")]}
      />,
    ));
    expect(container.querySelector(".sidebar")?.textContent).toContain("alpha");
    expect(container.querySelector(".conversation-pane")?.textContent).toContain("beta");
  });

  it("keeps settings, disable, and built-in UI escape actions available after a render failure", async () => {
    const host = new PluginHost({ react: React });
    const openSettings = vi.fn();
    const disablePlugin = vi.fn();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    await host.activateGeneration({
      pluginId: "user:broken",
      generation: "one",
      register(api) {
        api.registerViewType({
          id: "broken.view",
          title: "Broken",
          render: () => { throw new Error("render failed"); },
        });
        api.registerViewPlacement({
          id: "broken-main",
          region: "primary",
          view: "broken.view",
        });
      },
    });

    await act(async () => root.render(
      <DesktopWorkbench
        host={host}
        inventory={[inventoryPlugin("user:broken")]}
        services={{ openSettings, disablePlugin }}
      />,
    ));
    const buttons = [...container.querySelectorAll<HTMLButtonElement>(".plugin-workbench-error button")];
    expect(buttons.map((button) => button.textContent)).toEqual([
      "Use default UI",
      "Open settings",
      "Disable plugin",
    ]);
    act(() => buttons[1]?.click());
    act(() => buttons[2]?.click());
    expect(openSettings).toHaveBeenCalledOnce();
    expect(disablePlugin).toHaveBeenCalledWith("user:broken");
    act(() => buttons[0]?.click());
    expect(container.querySelector(".plugin-workbench-error")).toBeNull();
    consoleError.mockRestore();
  });

  it("uses registered message renderers and releases them with their generation", async () => {
    await desktopPluginHost.activateGeneration({
      pluginId: "user:renderer",
      generation: "one",
      register(api) {
        api.registerRenderer({
          id: "markdown",
          category: "message",
          match: "text/markdown",
          render: ({ content }) => <div>Plugin markdown: {String(content)}</div>,
        });
      },
    });

    await act(async () => root.render(<RichContent text="hello" />));
    expect(container.textContent).toContain("Plugin markdown: hello");

    await act(async () => desktopPluginHost.unload("user:renderer"));
    expect(container.textContent).not.toContain("Plugin markdown");
    expect(container.textContent).toContain("hello");
  });
});

function registerGeneration(api: PluginGenerationApi, label: string): void {
  api.registerViewType({
    id: "reload.view",
    title: `Reload ${label}`,
    persistence: "durable",
    render: () => <div>{label}</div>,
  });
  api.registerRenderer({
    id: "reload.renderer",
    category: "document",
    match: "text/demo",
    render: () => <div>{label}</div>,
  });
  api.registerThemeTokens({
    theme: "dark",
    base: "dark",
    tokens: {
      "--wuu-color-accent": label,
      "--wuu-font-family-ui": `${label}-ui`,
    },
    syntax: { "--wuu-syntax-keyword": `${label}-keyword` },
  });
  api.registerStatusItem({ id: "reload.status", label });
}

function registerCollidingGeneration(
  api: PluginGenerationApi,
  label: "alpha" | "beta",
  generation: string,
): void {
  api.registerViewType({
    id: "shared.view",
    title: `${label} ${generation}`,
    persistence: "durable",
    render: () => <div>{label}</div>,
  });
  api.registerViewType({
    id: `${label}.launcher`,
    title: `${label} launcher`,
    render: () => null,
  });
  if (label === "beta") {
    api.registerViewType({
      id: "beta.unique",
      title: "Beta unique",
      persistence: "durable",
      render: () => null,
    });
  }
  api.registerViewPlacement({
    id: "default-shared",
    region: label === "alpha" ? "navigation" : "auxiliary",
    view: "shared.view",
  });
}

function inventoryPlugin(id: string): ExtensionInventoryRecord {
  return {
    id,
    name: id,
    kind: "plugin",
    provenance: { kind: "plugin", source: "user", scope: "user" },
    state: "granted",
    approval_state: "granted",
    enabled: true,
  };
}

describe("destination routing", () => {
  it("activates sidebar and primary atomically, remembers instances, and falls back on unload", async () => {
    const host = new PluginHost({ react: React });
    host.registerCoreNavigation({ destinations: [{ id: "conversations", title: "Conversations", icon: "message-square", primaryViewType: "core.conversations" }], ribbonItems: [] });
    const activated = vi.fn();
    const controller = new WorkbenchController(host, { onDestinationActivated: activated });
    const register = async (generation: string, primaryViewType = "page") => host.activateGeneration({ pluginId: "user:destination", generation, register(api) {
      api.registerViewType({ id: "page", title: "Page", render: () => <input defaultValue="draft" /> });
      api.registerViewType({ id: "other-page", title: "Other page", render: () => <p>Other</p> });
      api.registerViewType({ id: "tree", title: "Tree", render: () => <div>Tree</div> });
      api.registerDestination({ id: "work", title: "Work", icon: "plug", primaryViewType, sidebarViewType: "tree" });
      api.registerRibbonItem({ id: "work", title: "Work", icon: "plug", target: { kind: "destination", destinationId: "work" } });
      api.registerCommand({ id: "refresh", title: "Refresh", execute: () => "done" });
      api.registerRibbonItem({ id: "refresh", title: "Refresh", icon: "plug", target: { kind: "command", commandId: "refresh" } });
    } });
    await register("one");
    const snapshots: Array<ReturnType<typeof controller.getSnapshot>> = [];
    const unsubscribe = controller.subscribe(() => snapshots.push(controller.getSnapshot()));
    controller.activateDestination("user:destination", "work");
    expect(snapshots).toHaveLength(1);
    const first = controller.getSnapshot();
    expect(first.activeDestination?.id).toBe("work");
    expect(visibleWorkbenchView(first, "primary")?.view.viewTypeId).toBe("page");
    expect(visibleWorkbenchView(first, "navigation")?.view.viewTypeId).toBe("tree");
    controller.activateDestination("wuu.core", "conversations");
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")).toBeUndefined();
    expect(visibleWorkbenchView(controller.getSnapshot(), "navigation")).toBeUndefined();
    controller.activateDestination("user:destination", "work");
    expect(controller.getSnapshot().activeViewByRegion).toEqual(first.activeViewByRegion);
    await controller.activateRibbonItem("user:destination", "one", "refresh");
    expect(controller.getSnapshot().activeDestination?.id).toBe("work");
    controller.deactivateRegion("navigation");
    await host.activateGeneration({ pluginId: "unrelated", generation: "one", register() {} });
    expect(visibleWorkbenchView(controller.getSnapshot(), "navigation")).toBeUndefined();
    await register("two", "other-page");
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")?.view.viewTypeId).toBe("other-page");
    expect(visibleWorkbenchView(controller.getSnapshot(), "navigation")).toBeUndefined();
    expect(controller.getSnapshot().activeDestination?.generation).toBe("two");
    await expect(controller.activateRibbonItem("user:destination", "one", "work")).rejects.toThrow();
    host.unload("user:destination");
    expect(controller.getSnapshot().activeDestination?.pluginId).toBe("wuu.core");
    expect(activated).toHaveBeenLastCalledWith(expect.objectContaining({ pluginId: "wuu.core", id: "conversations" }));
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")).toBeUndefined();
    unsubscribe();
    controller.dispose();
  });
  it("restores destination-owned durable instances without merging destinations that share a view", async () => {
    window.localStorage.clear();
    const host = new PluginHost({ react: React });
    await host.activateGeneration({ pluginId: "durable", generation: "one", register(api) {
      api.registerViewType({ id: "page", title: "Page", persistence: "durable", render: () => null });
      for (const id of ["first", "second"]) api.registerDestination({ id, title: id, icon: "plug", primaryViewType: "page" });
    } });
    let controller = new WorkbenchController(host);
    controller.activateDestination("durable", "first");
    const first = controller.getSnapshot().activeViewByRegion.primary;
    controller.activateDestination("durable", "second");
    const second = controller.getSnapshot().activeViewByRegion.primary;
    expect(second).not.toBe(first);
    controller.dispose();
    controller = new WorkbenchController(host);
    controller.activateDestination("durable", "first");
    expect(controller.getSnapshot().activeViewByRegion.primary).toBe(first);
    controller.activateDestination("durable", "second");
    expect(controller.getSnapshot().activeViewByRegion.primary).toBe(second);
    expect(controller.getSnapshot().views).toHaveLength(2);
    await controller.closeView(second!);
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")).toBeUndefined();
    expect(controller.getSnapshot().activeDestination?.id).toBe("second");
    expect(controller.getSnapshot().views.some(view => view.id === first)).toBe(true);
    controller.dispose();
    window.localStorage.clear();
  });

  it("never reveals another destination when a primary view closes or disappears", async () => {
    window.localStorage.clear();
    const host = new PluginHost({ react: React });
    host.registerCoreNavigation({ destinations: [{ id: "conversations", title: "Conversations", icon: "message-square", primaryViewType: "core.conversations" }], ribbonItems: [] });
    let removeDetail!: () => void;
    await host.activateGeneration({ pluginId: "owner", generation: "one", register(api) {
      api.registerViewType({ id: "page", title: "Page", render: () => null });
      const detail = api.registerViewType({ id: "detail", title: "Detail", render: () => null });
      removeDetail = () => detail.dispose();
      for (const id of ["a", "b"]) api.registerDestination({ id, title: id, icon: "plug", primaryViewType: "page" });
    } });
    const controller = new WorkbenchController(host);
    controller.activateDestination("owner", "a");
    const a = controller.getSnapshot().activeViewByRegion.primary;
    controller.activateDestination("owner", "b");
    controller.activateDestination("owner", "a");
    await controller.openPluginView("owner", "detail");
    removeDetail();
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")?.view.id).toBe(a);
    controller.activateDestination("wuu.core", "conversations");
    const independent = await controller.openPluginView("owner", "page");
    await controller.closeView(independent);
    expect(visibleWorkbenchView(controller.getSnapshot(), "primary")).toBeUndefined();
    expect(controller.getSnapshot().activeDestination?.pluginId).toBe("wuu.core");
    controller.dispose();
    window.localStorage.clear();
  });

});
