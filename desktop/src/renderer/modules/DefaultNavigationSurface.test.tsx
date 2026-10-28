import "../test-support/AbortSignal";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { ApplicationController } from "../plugins/ApplicationController.test-support";
import { PluginHost } from "../plugins/PluginHost";
import * as Toast from "../Toast";
import { DefaultNavigationSurface } from "./DefaultNavigationSurface";

vi.mock("../AppSidebar", async importOriginal => ({ ...await importOriginal<typeof import("../AppSidebar")>(),
  AppSidebar: ({ onStartNewThreadInWorkspace }: { onStartNewThreadInWorkspace: (id: string) => void }) =>
    <button onClick={() => onStartNewThreadInWorkspace("project")}>New conversation</button>,
}));

describe("navigation composer focus intent", () => {
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
