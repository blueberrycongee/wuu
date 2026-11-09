// @vitest-environment jsdom
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { HeaderSnapshotV1, PresentationHost } from "../../shared/workbench";
import { PluginHost } from "./PluginHost";
import { HeaderPresentation, immutableHeaderSnapshot } from "./HeaderPresentation";
import { WorkbenchController } from "./Workbench";

let root: Root | undefined;
let container: HTMLElement | undefined;

afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  root = undefined;
  container = undefined;
  vi.restoreAllMocks();
});

describe("HeaderPresentation", () => {
  it("replaces the native root with an immutable public snapshot and updates live", async () => {
    const { host, controller } = setup();
    const snapshots: HeaderSnapshotV1[] = [];
    await host.activateGeneration({ pluginId: "header", generation: "one", register(api) {
      api.registerPresenter({
        id: "conversation-header",
        target: "header.conversation",
        render: ({ snapshot }) => {
          snapshots.push(snapshot as HeaderSnapshotV1);
          return <section data-plugin-header>{(snapshot as HeaderSnapshotV1).title}</section>;
        },
      });
    } });

    renderHeader({ host, controller, title: "First" });
    expect(container?.firstElementChild?.hasAttribute("data-plugin-header")).toBe(true);
    expect(container?.querySelector("[data-native-header]")).toBeNull();
    expect(Object.isFrozen(snapshots[0])).toBe(true);
    expect(Object.isFrozen(snapshots[0].tabs)).toBe(true);
    expect(Object.isFrozen(snapshots[0].tabs?.[0])).toBe(true);

    renderHeader({ host, controller, title: "Second" });
    expect(container?.textContent).toBe("Second");
    expect(snapshots.at(-1)?.activeTabId).toBe("tab-1");
  });

  it("advertises only backed actions and validates tab ids", async () => {
    const { host, controller } = setup();
    const onSelectTab = vi.fn();
    let presentationHost: PresentationHost | undefined;
    await host.activateGeneration({ pluginId: "actions", generation: "one", register(api) {
      api.registerPresenter({ id: "workspace-header", target: "header.workspace", render: ({ host: apiHost }) => {
        presentationHost = apiHost;
        return <span>plugin</span>;
      } });
    } });
    const snapshot = immutableHeaderSnapshot({
      scope: "workspace",
      tabs: [{ id: "known", title: "Known" }],
      activeTabId: "known",
    });
    act(() => root?.render(
      <HeaderPresentation
        host={host}
        controller={controller}
        snapshot={snapshot}
        fallback={<span>native</span>}
        onSelectTab={onSelectTab}
      />,
    ));

    await expect(presentationHost?.invoke("header.close-tab", { tabId: "known" })).rejects.toThrow("not supported");
    await expect(presentationHost?.invoke("header.select-tab", { tabId: "missing" })).rejects.toThrow("existing tabId");
    await expect(presentationHost?.invoke("header.select-tab", { tabId: "known" })).resolves.toBeUndefined();
    expect(onSelectTab).toHaveBeenCalledWith("known");
  });

  it("restores the exact native fallback on unload and presenter failure", async () => {
    const { host, controller } = setup();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const native = <h1 data-native-header>Native</h1>;
    act(() => root?.render(
      <HeaderPresentation
        host={host}
        controller={controller}
        snapshot={immutableHeaderSnapshot({ scope: "conversation", title: "Native" })}
        fallback={native}
      />,
    ));
    expect(container?.firstElementChild?.outerHTML).toBe('<h1 data-native-header="true">Native</h1>');

    await act(async () => host.activateGeneration({ pluginId: "broken", generation: "one", register(api) {
      api.registerPresenter({ id: "broken", target: "header.conversation", render: () => { throw new Error("broken"); } });
    } }));
    expect(container?.firstElementChild?.outerHTML).toBe('<h1 data-native-header="true">Native</h1>');

    await act(async () => host.activateGeneration({ pluginId: "broken", generation: "two", register(api) {
      api.registerPresenter({ id: "fixed", target: "header.conversation", render: () => <div data-fixed>Fixed</div> });
    } }));
    expect(container?.querySelector("[data-fixed]")?.textContent).toBe("Fixed");
    act(() => host.unload("broken"));
    expect(container?.firstElementChild?.outerHTML).toBe('<h1 data-native-header="true">Native</h1>');
    consoleError.mockRestore();
  });
});

function setup(): { host: PluginHost; controller: WorkbenchController } {
  const host = new PluginHost({ react: React });
  const controller = new WorkbenchController(host);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  return { host, controller };
}

function renderHeader({
  host,
  controller,
  title,
}: {
  host: PluginHost;
  controller: WorkbenchController;
  title: string;
}): void {
  const snapshot = immutableHeaderSnapshot({
    scope: "conversation",
    title,
    tabs: [{ id: "tab-1", title: "Tab" }],
    activeTabId: "tab-1",
  });
  act(() => root?.render(
    <HeaderPresentation
      host={host}
      controller={controller}
      snapshot={snapshot}
      fallback={<h1 data-native-header>Native</h1>}
    />,
  ));
}
