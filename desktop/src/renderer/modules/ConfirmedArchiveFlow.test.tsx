import "../test-support/AbortSignal";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ServerEvent, Thread, WuuDesktopApi } from "../../shared/protocol";
import { PluginHost, type PluginGenerationApi } from "../plugins/PluginHost";
import { activate } from "../plugins/DefaultApplicationProvider";
import * as RuntimeLoader from "../RuntimeLoadState";
import { DefaultNavigationSurface } from "./DefaultNavigationSurface";

vi.mock("../AppSidebar", async importOriginal => ({ ...await importOriginal<typeof import("../AppSidebar")>(),
  AppSidebar: ({ onArchiveThread, navigation }: { onArchiveThread: (thread: unknown) => void; navigation: { view: { threads: unknown[] } } }) =>
    <button data-archive-target onClick={() => onArchiveThread(navigation.view.threads[0])}>Archive target</button>,
}));

afterEach(() => { vi.restoreAllMocks(); localStorage.clear(); sessionStorage.clear(); });

// Escaped confirmation contract: DOM clicks pass through the real navigation
// module, selected business provider and the public mutation action.
it.each(["running", "completed while confirming", "switched draft", "retired provider", "storage rejected", "replaced while pending", "stale completed replay"])("admits one confirmed archive and preserves history/drafts: %s", async mode => {
  const context = { kind: "no_project" as const, cwd: "/fixture" };
  const thread: Thread = { id: "archive-target", cwd: context.cwd, title: "Target history", preview: "", status: "in_progress", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", turns: [{ id: "turn", status: "in_progress", items_view: "full", items: [{ id: "answer", type: "agent_message", text: "Keep this historical answer" }] }] };
  const initialized = { provider: "fixture", model: "fixture", providers: [], extension_inventory: [] };
  let finish!: (value: unknown) => void;
  const archive = vi.fn(() => new Promise(resolve => { finish = resolve; }));
  const listeners = new Set<(event: ServerEvent) => void>();
  window.wuu = { listProjects: vi.fn().mockResolvedValue({ projects: [], active_context: context }),
    onServerEvent: (listener: (event: ServerEvent) => void) => { listeners.add(listener); return () => listeners.delete(listener); }, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
    listAllThreads: vi.fn().mockResolvedValue({ threads: [thread] }), getSessionOrganization: vi.fn().mockResolvedValue({ organization: { folders: [] } }),
    archiveThread: archive,
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: context, projects: [], thread, threads: [thread], initialized: initialized as never, status: "ready" });
  const host = new PluginHost({ react: React });
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let application!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "navigation", generation: "one", register: api => { application = api.application; } });
  const element = document.createElement("div"); document.body.append(element);
  const root = createRoot(element);
  try {
    await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
    await act(async () => element.querySelector<HTMLButtonElement>("[data-archive-target]")!.click());
    const confirmation = document.querySelector<HTMLButtonElement>("[role=alert] .archive-tip-action");
    expect(confirmation).not.toBeNull();
    expect(archive).not.toHaveBeenCalled();
    if (mode === "retired provider") {
      const retired = application;
      act(() => root.render(null));
      await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
      await host.activateGeneration({ pluginId: "business", generation: "two", register });
      await host.activateGeneration({ pluginId: "navigation", generation: "two", register: api => { application = api.application; } });
      await act(async () => root.render(<DefaultNavigationSurface application={application} slots={{ Slot: () => null }} />));
      await act(async () => confirmation!.click());
      await expect(retired.invoke("navigation.archive-thread", { threadId: thread.id, force: true })).rejects.toThrow();
      expect(archive).not.toHaveBeenCalled();
      expect(document.querySelector("[role=alert] .archive-tip-action")).toBeNull();
      return;
    }
    if (mode === "storage rejected") {
      archive.mockRejectedValueOnce(new Error("Selected storage unavailable"));
      await act(async () => confirmation!.click());
      expect(archive).toHaveBeenCalledExactlyOnceWith(thread.id, true, true);
      expect(document.querySelector("[role=alert]")?.textContent).toContain("Selected storage unavailable");
      expect(document.querySelector("[role=alert] .archive-tip-action")).toBeNull();
      expect(application.getSnapshot().settings.archivedThreads).toEqual([]);
      return;
    }
    if (mode === "completed while confirming") {
      thread.status = "idle"; thread.turns[0].status = "completed";
      await act(async () => application.invoke("navigation.refresh", {}));
    }
    if (mode === "switched draft") {
      await act(async () => application.invoke("draft.start", {}));
      await act(async () => application.invoke("composer.set-draft", { text: "New unrelated draft" }));
    }
    await act(async () => { confirmation!.click(); confirmation!.click(); });
    expect(archive).toHaveBeenCalledExactlyOnceWith(thread.id, true, true);
    expect(document.querySelector<HTMLButtonElement>("[role=alert] .archive-tip-action")?.disabled).toBe(true);
    if (mode === "replaced while pending") {
      act(() => root.render(null));
      await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
      await host.activateGeneration({ pluginId: "business", generation: "two", register });
      await host.activateGeneration({ pluginId: "navigation", generation: "two", register: api => { application = api.application; } });
      await application.invoke("draft.start", {});
      await application.invoke("composer.set-draft", { text: "Replacement-owned draft" });
      const before = { local: { ...localStorage }, session: { ...sessionStorage } };
      await act(async () => finish({ thread: { ...thread, archived: true, status: "idle" } }));
      expect(application.getSnapshot().composer.draftText).toBe("Replacement-owned draft");
      expect({ local: { ...localStorage }, session: { ...sessionStorage } }).toEqual(before);
      return;
    }
    await act(async () => finish({ thread: { ...thread, archived: true, status: "idle" } }));
    if (mode === "stale completed replay") {
      await act(async () => listeners.forEach(listener => listener({ workdir: context.cwd, kind: "notification", message: {
        method: "thread/updated", params: { thread: { ...thread, status: "idle", turns: thread.turns.map(turn => ({ ...turn, status: "completed" })) } },
      } })));
    }
    expect(application.getSnapshot().settings.archivedThreads.map(row => row.id)).toContain(thread.id);
    expect(application.getSnapshot().navigationWorkspace.threads.some(row => row.id === thread.id && !row.archived)).toBe(false);
    if (mode === "switched draft") expect(application.getSnapshot().composer.draftText).toBe("New unrelated draft");
    expect(thread.turns[0].items[0].text).toBe("Keep this historical answer");
    if (mode === "stale completed replay") {
      archive.mockResolvedValueOnce({ thread: { ...thread, archived: false, status: "idle" } });
      await act(async () => application.invoke("thread.archive", { threadId: thread.id, archived: false }));
      expect(application.getSnapshot().settings.archivedThreads).toEqual([]);
      expect(application.getSnapshot().navigationWorkspace.threads.some(row => row.id === thread.id && !row.archived)).toBe(true);
    }
  } finally {
    act(() => root.unmount()); element.remove();
    await host.unloadAndWait("navigation"); await host.unloadAndWait("business");
  }
});
