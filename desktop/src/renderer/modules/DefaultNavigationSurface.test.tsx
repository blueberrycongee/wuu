import "../test-support/AbortSignal";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { PluginActionNotStartedError } from "../../shared/PluginLifecycle";
import { ApplicationController } from "../plugins/ApplicationController.test-support";
import { PluginHost } from "../plugins/PluginHost";
import { retainPresentationReads } from "../plugins/PresentationApplication";
import * as Toast from "../Toast";
import { DefaultNavigationSurface } from "./DefaultNavigationSurface";

vi.mock("../AppSidebar", async importOriginal => ({ ...await importOriginal<typeof import("../AppSidebar")>(),
  AppSidebar: ({ onStartNewThread, onStartNewThreadInWorkspace }: { onStartNewThread: () => void; onStartNewThreadInWorkspace: (id: string) => void }) => <>
    <button onClick={() => onStartNewThreadInWorkspace("project")}>New conversation</button>
    <button onClick={onStartNewThread}>New conversation here</button>
  </>,
}));

describe("navigation composer focus intent", () => {
  it.each([
    ["active", "same workspace"], ["active", "workspace changed"], ["active", "subsequent interaction"],
    ["workspace", "same workspace"], ["workspace", "workspace changed"], ["workspace", "subsequent interaction"],
  ] as const)("handles a not-yet-started %s draft request after replacement: %s", async (target, mode) => {
    window.wuu = {} as typeof window.wuu;
    const controller = new ApplicationController();
    let snapshot = { ...controller.getSnapshot(), workspaces: { ...controller.getSnapshot().workspaces,
      workspaces: [{ id: "project", name: "Project", root: "/project", available: true }] } };
    vi.spyOn(controller, "getSnapshot").mockImplementation(() => snapshot);
    const invoke = vi.spyOn(controller, "invoke").mockImplementation(async action => action === "draft.start" || action === "workspace.start-draft" ? { activated: true, tabId: "draft:next" } : undefined);
    const host = new PluginHost({ react: React, application: controller });
    let application!: ApplicationServiceV1;
    const activate = async (generation: string) => host.activateGeneration({ pluginId: "navigation-test", generation, register: api => {
      application = retainPresentationReads(api.application, api.signal);
    } });
    await activate("one");
    const element = document.createElement("div"); document.body.append(element);
    const root = createRoot(element);
    try {
      await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      host.unload("navigation-test");
      await act(async () => element.querySelectorAll("button")[target === "active" ? 1 : 0].click());
      if (mode === "subsequent interaction") document.body.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      if (mode === "workspace changed") snapshot = { ...snapshot, workspaces: { ...snapshot.workspaces, activeWorkspaceId: "other", activeRoot: "/other" } };
      await activate("two");
      await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      expect(invoke.mock.calls.filter(([action]) => action === (target === "active" ? "draft.start" : "workspace.start-draft"))).toHaveLength(mode === "same workspace" ? 1 : 0);
    } finally { act(() => root.unmount()); host.unload("navigation-test"); element.remove(); vi.restoreAllMocks(); }
  });

  it("does not retry a draft request that had already started", async () => {
    window.wuu = {} as typeof window.wuu;
    const controller = new ApplicationController();
    let finish!: (value: { tabId: string }) => void;
    const invoke = vi.spyOn(controller, "invoke").mockImplementation(async action => action === "draft.start" ? new Promise(resolve => { finish = resolve; }) : undefined);
    const host = new PluginHost({ react: React, application: controller });
    let application!: ApplicationServiceV1;
    const activate = async (generation: string) => host.activateGeneration({ pluginId: "navigation-test", generation, register: api => {
      application = retainPresentationReads(api.application, api.signal);
    } });
    await activate("one");
    const element = document.createElement("div"); document.body.append(element);
    const root = createRoot(element);
    try {
      await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      await act(async () => element.querySelectorAll("button")[1].click());
      expect(finish).toBeTypeOf("function");
      host.unload("navigation-test");
      await act(async () => finish({ tabId: "draft:next" }));
      await activate("two");
      await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      expect(invoke.mock.calls.filter(([action]) => action === "draft.start")).toHaveLength(1);
    } finally { act(() => root.unmount()); host.unload("navigation-test"); element.remove(); vi.restoreAllMocks(); }
  });

  it("does not create a second draft if only the subsequent focus request was retired", async () => {
    window.wuu = {} as typeof window.wuu;
    const controller = new ApplicationController();
    const invoke = vi.spyOn(controller, "invoke").mockImplementation(async action => {
      if (action === "draft.start") return { tabId: "draft:next" };
      if (action === "navigation.focus-composer") throw new PluginActionNotStartedError();
      return undefined;
    });
    const host = new PluginHost({ react: React, application: controller });
    let application!: ApplicationServiceV1;
    const activate = async (generation: string) => host.activateGeneration({ pluginId: "navigation-test", generation, register: api => { application = api.application; } });
    await activate("one");
    const element = document.createElement("div"); document.body.append(element);
    const root = createRoot(element);
    try {
      await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      const button = element.querySelectorAll("button")[1]; button.focus();
      await act(async () => button.click());
      await activate("two");
      await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      expect(invoke.mock.calls.filter(([action]) => action === "draft.start")).toHaveLength(1);
    } finally { act(() => root.unmount()); host.unload("navigation-test"); element.remove(); vi.restoreAllMocks(); }
  });

  it.each(["unchanged", "focus moved", "pointer interaction", "unmounted"])("preserves %s while a workspace switch is pending", async mode => {
    window.wuu = {} as typeof window.wuu;
    const controller = new ApplicationController();
    let finish!: (result: { activated: boolean; tabId: string }) => void;
    const invoke = vi.spyOn(controller, "invoke").mockImplementation(async action => {
      if (action === "workspace.start-draft") return new Promise(resolve => { finish = resolve; });
      return undefined;
    });
    let application!: ApplicationServiceV1;
    const host = new PluginHost({ react: React, application: controller });
    await host.activateGeneration({ pluginId: "navigation-test", generation: "one", register: api => { application = api.application; } });
    const element = document.createElement("div"); document.body.append(element);
    const other = document.createElement("button"); document.body.append(other);
    const root = createRoot(element);
    let mounted = true;
    try {
      act(() => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      const origin = element.querySelector("button")!; origin.focus();
      await act(async () => origin.click());
      if (mode === "focus moved") other.focus();
      if (mode === "pointer interaction") { document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })); origin.blur(); }
      if (mode === "unmounted") { act(() => root.unmount()); mounted = false; }
      await act(async () => finish({ activated: true, tabId: "draft:next" }));
      expect(invoke.mock.calls.filter(([action]) => action === "navigation.focus-composer")).toHaveLength(mode === "unchanged" ? 1 : 0);
    } finally { if (mounted) act(() => root.unmount()); host.unload("navigation-test"); element.remove(); other.remove(); }
  });
});

it.each(["retired", "failure"])("handles a pending navigation refresh after %s", async outcome => {
  window.wuu = {} as typeof window.wuu;
  const controller = new ApplicationController();
  const snapshot = controller.getSnapshot();
  vi.spyOn(controller, "getSnapshot").mockReturnValue({ ...snapshot, navigationWorkspace: { ...snapshot.navigationWorkspace, initialized: true } });
  let reject!: (error: Error) => void;
  vi.spyOn(controller, "invoke").mockImplementation(async action => {
    if (action === "navigation.refresh") return new Promise((_resolve, fail) => { reject = fail; });
    return undefined;
  });
  const toast = vi.spyOn(Toast, "showErrorToast");
  let application!: ApplicationServiceV1;
  const host = new PluginHost({ react: React, application: controller });
  await host.activateGeneration({ pluginId: "navigation-refresh", generation: "one", register: api => { application = api.application; } });
  const element = document.createElement("div"); document.body.append(element);
  const root = createRoot(element);
  try {
    await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
    expect(reject).toBeTypeOf("function");
    if (outcome === "retired") host.unload("navigation-refresh");
    await act(async () => reject(new Error("Refresh failed")));
    if (outcome === "retired") expect(toast).not.toHaveBeenCalled();
    else expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: "Refresh failed" }));
  } finally {
    act(() => root.unmount());
    host.unload("navigation-refresh");
    element.remove();
    vi.restoreAllMocks();
  }
});
