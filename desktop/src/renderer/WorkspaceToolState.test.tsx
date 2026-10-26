import { createScopedTestApplication } from "./test-support/ScopedApplication";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApplicationController } from "./plugins/ApplicationController";
import { useWorkspaceToolState } from "./WorkspaceToolState";

const cleanups: Array<() => void> = [];
afterEach(() => { act(() => cleanups.splice(0).forEach(cleanup => cleanup())); document.body.replaceChildren(); });
async function mount(controller: ApplicationController, globalized = false) {
  const { application, dispose } = await createScopedTestApplication(controller);
  let value!: ReturnType<typeof useWorkspaceToolState>;
  const setOpen = vi.fn();
  function Probe() {
    value = useWorkspaceToolState({ application, rightPanelOpen: true, rightPanelGlobalized: globalized, setRightPanelOpenWithMotion: setOpen });
    return null;
  }
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  let mounted = true;
  const unmount = () => { if (mounted) { root.unmount(); dispose(); mounted = false; } };
  cleanups.push(unmount);
  act(() => root.render(<Probe />));
  return { get: () => value, setOpen, unmount };
}

describe("application-owned workspace tabs", () => {
  it("preserves open resources and close fallback when the consumer is replaced", async () => {
    const application = new ApplicationController();
    const first = await mount(application);
    await act(async () => {
      first.get().openWorkspaceFileTab({ context: { kind: "no_project", cwd: "/tmp" }, path: "one.ts" });
      first.get().openWorkspaceFileTab({ context: { kind: "no_project", cwd: "/tmp" }, path: "two.ts" });
    });
    const active = first.get().workspaceActiveViewTabID!;
    act(() => first.unmount());
    const second = await mount(application);
    expect(second.get().workspaceViewTabs.filter(tab => tab.kind === "file")).toHaveLength(2);
    expect(second.get().workspaceActiveViewTabID).toBe(active);
    await act(async () => second.get().closeWorkspaceViewTab(active));
    expect(second.get().workspaceViewTabs.find(tab => tab.id === second.get().workspaceActiveViewTabID)).toMatchObject({ kind: "file", path: "one.ts" });
  });

  it("returns an expanded workspace to the conversation when its final tab closes", async () => {
    const application = new ApplicationController();
    const hook = await mount(application, true);
    await act(async () => hook.get().closeWorkspaceViewTabsWhere(() => true));
    expect(hook.get().workspaceConversationActive).toBe(true);
    expect(application.getSnapshot().previewWorkspace.tabs).toHaveLength(0);
    expect(hook.setOpen).not.toHaveBeenCalled();
  });
});
