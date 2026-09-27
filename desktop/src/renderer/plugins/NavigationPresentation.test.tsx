// @vitest-environment jsdom
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NAVIGATION_ACTIONS, type NavigationSnapshotV1 } from "../../shared/workbench";
import { desktopPluginHost } from "./DesktopPluginRuntime";
import {
  createNavigationModel,
  NavigationPresentation,
  type NavigationSourceNode,
} from "./NavigationPresentation";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  desktopPluginHost.unload("test:navigation-presentation");
  container.remove();
});

describe("NavigationPresentation", () => {
  it("creates a representative ordered immutable snapshot without host callbacks", () => {
    const activate = vi.fn();
    const togglePin = vi.fn();
    const source: NavigationSourceNode[] = [
      { id: "command:new", kind: "command", label: "New", onActivate: activate },
      { id: "section:workspace", kind: "section", label: "Workspace", depth: 0 },
      { id: "project:p1", kind: "project", label: "Wuu", parentId: "section:workspace", depth: 1 },
      {
        id: "thread:t1", kind: "thread", label: "Build", parentId: "project:p1", depth: 2,
        active: true, pinned: false, unread: true, running: true,
        onActivate: activate, onTogglePinned: togglePin,
      },
      {
        id: "project:p2", kind: "project", label: "Design", parentId: "section:workspace", depth: 1,
        pinned: true, disabled: true,
      },
    ];

    const model = createNavigationModel(source);
    expect(model.snapshot.nodes.map(({ id }) => id)).toEqual([
      "command:new", "section:workspace", "project:p1", "thread:t1", "project:p2",
    ]);
    expect(model.snapshot.activeNodeId).toBe("thread:t1");
    expect(model.snapshot.nodes[3]).toMatchObject({
      parentId: "project:p1", active: true, pinned: false, unread: true, running: true,
    });
    expect(Object.isFrozen(model.snapshot)).toBe(true);
    expect(Object.isFrozen(model.snapshot.nodes)).toBe(true);
    expect(Object.isFrozen(model.snapshot.nodes[3])).toBe(true);
    expect(model.snapshot.nodes[3]).not.toHaveProperty("onActivate");
    expect(model.snapshot.nodes[3]).not.toHaveProperty("onTogglePinned");

    model.dispatchAction(NAVIGATION_ACTIONS.activateNode, { id: "thread:t1" });
    model.dispatchAction(NAVIGATION_ACTIONS.pinNode, { id: "thread:t1" });
    expect(activate).toHaveBeenCalledOnce();
    expect(togglePin).toHaveBeenCalledOnce();
    expect(() => model.dispatchAction(NAVIGATION_ACTIONS.activateNode, { id: "missing" }))
      .toThrow("Unknown navigation node id");
    expect(() => model.dispatchAction(NAVIGATION_ACTIONS.activateNode, { id: "project:p2" }))
      .toThrow("disabled");
  });

  it("replaces the complete root, updates live, and returns to fallback on unload", async () => {
    let observed: NavigationSnapshotV1 | undefined;
    await desktopPluginHost.activateGeneration({
      pluginId: "test:navigation-presentation",
      generation: "one",
      register(api) {
        api.registerPresenter({
          id: "root",
          target: "navigation.primary",
          render: ({ snapshot }) => {
            observed = snapshot as NavigationSnapshotV1;
            return <main data-navigation-root>{observed.nodes[0]?.label}</main>;
          },
        });
      },
    });
    const fallback = <aside data-native-sidebar>native</aside>;
    const render = (label: string): void => act(() => root.render(
      <NavigationPresentation
        nodes={[{ id: "command:new", kind: "command", label }]}
        fallback={fallback}
      />,
    ));

    render("First");
    expect(container.querySelector("[data-navigation-root]")?.textContent).toBe("First");
    expect(container.querySelector("[data-native-sidebar]")).toBeNull();
    render("Second");
    expect(observed?.nodes[0]?.label).toBe("Second");
    expect(container.querySelector("[data-navigation-root]")?.textContent).toBe("Second");
    act(() => desktopPluginHost.unload("test:navigation-presentation"));
    expect(container.querySelector("[data-native-sidebar]")?.textContent).toBe("native");
  });

  it("uses the exact native fallback when a presenter fails", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    await desktopPluginHost.activateGeneration({
      pluginId: "test:navigation-presentation",
      generation: "broken",
      register(api) {
        api.registerPresenter({
          id: "root",
          target: "navigation.primary",
          render: () => { throw new Error("broken navigation"); },
        });
      },
    });
    act(() => root.render(
      <NavigationPresentation
        nodes={[]}
        fallback={<aside data-native-sidebar>native</aside>}
      />,
    ));
    expect(container.querySelector("[data-native-sidebar]")?.textContent).toBe("native");
    consoleError.mockRestore();
  });
});
