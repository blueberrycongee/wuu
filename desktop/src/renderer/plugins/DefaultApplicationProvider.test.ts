import "../test-support/AbortSignal";
import * as React from "react";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi } from "./PluginHost";
import { activate } from "./DefaultApplicationProvider";
import * as RuntimeLoader from "../RuntimeLoadState";
import type { WuuDesktopApi } from "../../shared/protocol";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";

const hosts: PluginHost[] = [];
afterEach(async () => { for (const host of hosts.splice(0)) { await host.unloadAndWait("shell"); await host.unloadAndWait("business"); } vi.restoreAllMocks(); });

// Public boundary failure cases: no host controller, dormant unselected package,
// stopped checkpoint carries unsent text, and disable retires captured handles.
it("loads the default business through registration, transfers unsent drafts, and disables without a fallback", async () => {
  const context = { kind: "no_project" as const, cwd: "/fixture" };
  const off = vi.fn();
  window.wuu = { listProjects: vi.fn().mockResolvedValue({ projects: [], active_context: context }),
    onServerEvent: () => off, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: context, projects: [], threads: [],
    initialized: { provider: "fixture", model: "fixture", providers: [], extension_inventory: [] } as never, status: "ready" });
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  expect(window.wuu.listProjects).not.toHaveBeenCalled();
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let application!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { application = api.application; } });
  await application.invoke("composer.set-draft", { text: "unsent draft" });
  await application.invoke("settings.open", { pageId: "appearance" });
  await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
  await host.activateGeneration({ pluginId: "business", generation: "two", register });
  expect(() => application.getSnapshot()).toThrow();
  await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { application = api.application; } });
  expect(application.getSnapshot().composer.draftText).toBe("unsent draft");
  expect(application.getSnapshot().settings).toMatchObject({ open: true, activePageId: "appearance" });
  await host.unloadAndWait("business");
  expect(() => application.getSnapshot()).toThrow();
  expect(off).toHaveBeenCalled();
});

it.each([false, true])("transfers pending input before replacing the writer and ignores its late delivery (failure=%s)", async failed => {
  const context = { kind: "no_project" as const, cwd: "/fixture" };
  const thread = { id: "pending-thread", cwd: context.cwd, title: "Pending", preview: "", status: "idle", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", turns: [] };
  let resolve!: (value: unknown) => void; let reject!: (error: Error) => void;
  const delivery = new Promise((yes, no) => { resolve = yes; reject = no; });
  window.wuu = { listProjects: vi.fn().mockResolvedValue({ projects: [], active_context: context }),
    onServerEvent: () => () => {}, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
    startThread: vi.fn().mockResolvedValue({ thread }), startTurn: vi.fn(() => delivery),
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: context, projects: [], threads: [],
    initialized: { provider: "fixture", model: "fixture", providers: [{ name: "fixture", type: "openai-compatible", model: "fixture", api_key_configured: true }], extension_inventory: [] } as never, status: "ready" });
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  await app.invoke("composer.set-draft", { text: "pending original" });
  const owner = app.getSnapshot().drafts.primary.ownerTabId!;
  expect(await app.invoke("composer.submit", { ownerTabId: owner })).toMatchObject({ accepted: true });
  await vi.waitFor(() => expect(window.wuu.startTurn).toHaveBeenCalledOnce());
  await app.invoke("composer.set-draft", { text: "newer input" });
  await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
  await host.activateGeneration({ pluginId: "business", generation: "two", register });
  await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
  expect(app.getSnapshot().tabs.items.map(tab => tab.draftText)).toEqual(expect.arrayContaining(["pending original", "newer input"]));
  const before = app.getSnapshot().tabs;
  const persisted = { local: { ...window.localStorage }, session: { ...window.sessionStorage } };
  if (failed) reject(new Error("late rejected delivery"));
  else resolve({ turn: { id: "accepted-late", status: "completed", items_view: "full", items: [] } });
  await new Promise(done => setTimeout(done, 0));
  expect(app.getSnapshot().tabs).toEqual(before);
  expect({ local: { ...window.localStorage }, session: { ...window.sessionStorage } }).toEqual(persisted);
});

it.each([{ failed: false, repeated: false, cancelled: false, settled: false }, { failed: true, repeated: false, cancelled: false, settled: false }, { failed: false, repeated: true, cancelled: false, settled: false }, { failed: true, repeated: true, cancelled: false, settled: false }, { failed: false, repeated: false, cancelled: true, settled: false }, { failed: false, repeated: false, cancelled: false, settled: true }, { failed: false, repeated: false, cancelled: false, settled: false, ready: true }])("preserves latest navigation across replacement ($failed, ABA=$repeated, cancelled=$cancelled, settled=$settled, ready=$ready)", async ({ failed, repeated, cancelled, settled, ready }) => {
  const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
  const target = { kind: "project" as const, project_id: "b", cwd: "/b" };
  const projects = [{ id: "a", name: "A", path: "/a", created_at: "", updated_at: "" }, { id: "b", name: "B", path: "/b", created_at: "", updated_at: "" }];
  const thread = { id: "target", cwd: "/b", project_id: "b", title: "Target history", preview: "", status: "idle", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", turns: [{ id: "turn", status: "completed", items: [] }] };
  const initialized = { provider: "fixture", model: "fixture", providers: [], extension_inventory: [] };
  let selected = source as typeof source | typeof target;
  let finish!: (value: unknown) => void; let reject!: (error: Error) => void;
  const oldResume = new Promise((resolve, fail) => { finish = resolve; reject = fail; });
  const resume = vi.fn().mockReturnValueOnce(oldResume);
  if (repeated) resume.mockReturnValueOnce(oldResume).mockReturnValueOnce(oldResume);
  resume.mockResolvedValue({ thread,
    held_user_messages: [{ id: "queued", thread_id: thread.id, origin: "queue", prompt: "held queue" }],
    pending_user_messages: [{ id: "guided", thread_id: thread.id, origin: "steer", prompt: "pending guide" },
      { id: "uncertain", thread_id: thread.id, origin: "queue", prompt: "unknown delivery", delivery_unconfirmed: true }],
  });
  window.wuu = { listProjects: vi.fn(async () => ({ projects, active_context: selected })), resumeThread: resume,
    onServerEvent: () => () => {}, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
    startThread: vi.fn(), startTurn: vi.fn(), queueTurn: vi.fn(),
  } as unknown as WuuDesktopApi;
  const select = vi.spyOn(RuntimeLoader, "selectRuntimeContext").mockImplementation(async context => {
    selected = context as typeof selected; return { projects, active_context: selected };
  });
  vi.spyOn(RuntimeLoader, "loadRuntimeConfiguration").mockImplementation(async workspace => ({ projects, activeContext: workspace.active_context, initialized: initialized as never }));
  vi.spyOn(RuntimeLoader, "loadRuntimeThreadList").mockResolvedValue([thread as never]);
  vi.spyOn(RuntimeLoader, "loadRuntime").mockImplementation(async (_workspace, options) => {
    // Account for the same physical history read as the real startup loader.
    const resumed = options?.resumeLatestThread !== false && options?.resumeThreadID === thread.id
      ? await window.wuu.resumeThread(thread.id) : undefined;
    return { activeContext: selected, activeProjectId: selected.project_id, projects, thread: resumed?.thread, threads: [thread as never, { ...thread, id: "source", cwd: "/a", project_id: "a" } as never], initialized: initialized as never, status: "ready" };
  });
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  await app.invoke("composer.set-draft", { text: "retained source draft" });
  const oldApp = app;
  const navigating = [app.invoke("thread.activate", { threadId: thread.id, workspaceId: "b" }).catch(error => error)];
  await vi.waitFor(() => expect(resume).toHaveBeenCalledOnce());
  const firstIntent = app.getSnapshot().tabs.pending?.intentId;
  expect(firstIntent).toBeTruthy();
  if (repeated) {
    navigating.push(app.invoke("thread.activate", { threadId: "source", workspaceId: "a" }).catch(error => error));
    await vi.waitFor(() => expect(resume).toHaveBeenCalledTimes(2));
    navigating.push(app.invoke("thread.activate", { threadId: thread.id, workspaceId: "b" }).catch(error => error));
    await vi.waitFor(() => expect(resume).toHaveBeenCalledTimes(3));
    expect(app.getSnapshot().tabs.pending?.intentId).not.toBe(firstIntent);
  }
  if (ready) {
    finish({ thread }); await Promise.all(navigating);
    expect(app.getSnapshot().tabs.pending?.contentReady).toBe(true);
  }
  if (settled) {
    finish({ thread }); await Promise.all(navigating);
    await app.invoke("navigation.reveal-complete", { requestId: app.getSnapshot().tabs.pending!.requestId! });
    expect(app.getSnapshot().tabs.pending).toBeUndefined();
  }
  if (cancelled) {
    await app.invoke("navigation.cancel", {});
    finish({ thread }); await Promise.all(navigating);
    expect(app.getSnapshot().tabs.pending).toBeUndefined();
  }
  const latestIntent = app.getSnapshot().tabs.pending?.intentId;
  const priorSelections = select.mock.calls.length;
  const priorResumes = resume.mock.calls.length;
  await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
  await host.activateGeneration({ pluginId: "business", generation: "two", register });
  await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
  expect(resume.mock.calls.length - priorResumes).toBe(cancelled ? 0 : 1);
  expect(app.getSnapshot().tabs.pending?.intentId).toBe(latestIntent);
  expect(app.getSnapshot().conversation.threadId).toBe(cancelled ? undefined : thread.id);
  expect(app.getSnapshot().conversation.turns).toHaveLength(cancelled ? 0 : 1);
  if (!cancelled && !settled) {
    expect(app.getSnapshot().composerPending).toMatchObject([{ threadId: thread.id,
      queued: [{ id: "queued", text: "held queue" }, { id: "uncertain", text: "unknown delivery", deliveryUnconfirmed: true }],
      guides: [{ id: "guided", text: "pending guide" }],
    }]);
  }
  expect(app.getSnapshot().tabs.items.some(tab => tab.draftText === "retained source draft")).toBe(true);
  expect(() => oldApp.getSnapshot()).toThrow();
  const settledTabs = app.getSnapshot().tabs;
  if (failed) reject(new Error("retired resume failed")); else finish({ thread });
  await Promise.all(navigating);
  expect(app.getSnapshot().tabs).toEqual(settledTabs);
  expect(selected).toEqual(cancelled ? source : target);
  expect(select.mock.calls.slice(priorSelections).every(([context]) => context.cwd === target.cwd)).toBe(true);
  expect(window.wuu.startThread).not.toHaveBeenCalled(); expect(window.wuu.startTurn).not.toHaveBeenCalled(); expect(window.wuu.queueTurn).not.toHaveBeenCalled();
});

it("carries workspace presentation identity without replaying workspace or draft actions", async () => {
  const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
  const target = { kind: "project" as const, project_id: "b", cwd: "/b" };
  const projects = [source, target].map(context => ({ id: context.project_id, name: context.project_id, path: context.cwd, created_at: "", updated_at: "" }));
  const initialized = { provider: "fixture", model: "fixture", providers: [], extension_inventory: [] };
  let selected = source;
  let finish!: (value: never) => void;
  const oldLoad = new Promise<never>(resolve => { finish = resolve; });
  const loaded = () => ({ activeContext: selected, activeProjectId: selected.project_id, projects, threads: [], initialized: initialized as never, status: "ready" });
  window.wuu = { listProjects: vi.fn(async () => ({ projects, active_context: selected })),
    selectProject: vi.fn(async () => { selected = target; return { projects, active_context: selected }; }),
    onServerEvent: () => () => {}, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }), startThread: vi.fn(), startTurn: vi.fn(),
  } as unknown as WuuDesktopApi;
  const load = vi.spyOn(RuntimeLoader, "loadRuntime").mockImplementationOnce(async () => loaded()).mockReturnValueOnce(oldLoad).mockImplementation(async () => loaded());
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  await app.invoke("composer.set-draft", { text: "source stays saved" });
  const switching = app.invoke("workspace.select", { workspaceId: "b", resumeLatestThread: false }).catch(error => error);
  await vi.waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  const intentId = app.getSnapshot().tabs.pending?.intentId;
  expect(intentId).toBeTruthy();
  await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
  await host.activateGeneration({ pluginId: "business", generation: "two", register });
  await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
  expect(app.getSnapshot().tabs.pending).toMatchObject({ kind: "workspace", targetId: "b", intentId, contentReady: true });
  expect(app.getSnapshot().workspaces.activeRoot).toBe("/b");
  expect(app.getSnapshot().tabs.items.some(tab => tab.draftText === "source stays saved")).toBe(true);
  expect(window.wuu.selectProject).toHaveBeenCalledOnce();
  expect(window.wuu.startThread).not.toHaveBeenCalled(); expect(window.wuu.startTurn).not.toHaveBeenCalled();
  finish(loaded() as never); await switching;
  expect(app.getSnapshot().workspaces.activeRoot).toBe("/b");
});

it.each(["changed", "same", "missing", "rejected", "returned", "resume-rejected"] as const)("fences a cross-workspace history read at the selected application graph (%s)", async mode => {
  const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
  const target = { kind: "project" as const, project_id: "b", cwd: "/b" };
  const projects = [source, target].map(context => ({ id: context.project_id, name: context.project_id, path: context.cwd, created_at: "", updated_at: "" }));
  let selected = source;
  const initialization = (graph: string) => ({ provider: "fixture", model: "fixture", providers: [],
    application_surfaces: { "ui.application": { plugin_id: "business", generation: "one", provider_id: "ui.application" } },
    extension_inventory: [{ id: "business", fingerprint: "one", graph_generation: graph }],
  });
  const thread = { id: "destination", cwd: target.cwd, project_id: "b", title: "Destination", preview: "", status: "idle", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", turns: [] };
  const resume = vi.fn().mockResolvedValue({ thread });
  window.wuu = { listProjects: vi.fn(async () => ({ projects, active_context: selected })), resumeThread: resume,
    onServerEvent: () => () => {}, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "selectRuntimeContext").mockImplementation(async context => { selected = context as typeof selected; return { projects, active_context: selected }; });
  vi.spyOn(RuntimeLoader, "loadRuntime").mockImplementation(async () => ({ activeContext: selected, projects, threads: [thread as never], initialized: initialization(selected === source || mode === "same" ? "source" : "target") as never }));
  vi.spyOn(RuntimeLoader, "loadRuntimeThreadList").mockResolvedValue([thread as never]);
  let configure!: () => void;
  vi.spyOn(RuntimeLoader, "loadRuntimeConfiguration").mockImplementation(async workspace => {
    if (workspace.active_context?.cwd === target.cwd) await new Promise<void>(resolve => { configure = resolve; });
    if (mode === "rejected" && workspace.active_context?.cwd === target.cwd) throw new Error("configuration unavailable");
    return { activeContext: workspace.active_context, projects,
      initialized: (mode === "missing" ? { ...initialization("target"), application_surfaces: {} } : initialization(mode === "same" ? "source" : "target")) as never };
  });
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", graphGeneration: "source", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  await app.invoke("composer.set-draft", { text: "keep source draft" });
  const navigation = app.invoke("thread.activate", { threadId: thread.id, workspaceId: "b" }).catch(error => error);
  await vi.waitFor(() => expect(configure).toBeDefined());
  expect(resume).not.toHaveBeenCalled();
  configure();
  const outcome = await navigation;
  if (mode === "same") {
    expect(resume).toHaveBeenCalledExactlyOnceWith(thread.id);
    expect(app.getSnapshot().conversation.threadId).toBe(thread.id);
  } else {
    expect(resume).not.toHaveBeenCalled();
    if (mode === "rejected") {
      expect(String(outcome)).toContain("configuration unavailable");
      expect(selected).toEqual(source);
    } else {
      expect(String(outcome)).toContain("retired");
      if (mode === "returned") {
        // A user can return before asynchronous graph activation retires this
        // owner. Restore transport selection, not just the still-visible source.
        vi.mocked(RuntimeLoader.loadRuntimeConfiguration).mockResolvedValue({ activeContext: source, projects, initialized: initialization("source") as never });
        resume.mockImplementation(async id => {
          expect(selected).toEqual(source);
          return { thread: { ...thread, id, cwd: source.cwd, project_id: "a" } };
        });
        await app.invoke("thread.activate", { threadId: "origin", workspaceId: "a" });
        expect(resume).toHaveBeenCalledExactlyOnceWith("origin");
        expect(app.getSnapshot().conversation.threadId).toBe("origin");
      }
      if (mode === "changed" || mode === "resume-rejected") {
        // The successor owns the only selected-generation history read.
        vi.mocked(RuntimeLoader.loadRuntimeConfiguration).mockResolvedValue({ activeContext: target, projects, initialized: initialization("target") as never });
        if (mode === "resume-rejected") {
          resume.mockRejectedValue(new Error("selected history unavailable"));
          await expect(host.activateGeneration({ pluginId: "business", generation: "one", graphGeneration: "target", register })).rejects.toThrow("selected history unavailable");
          expect(resume).toHaveBeenCalledExactlyOnceWith(thread.id);
          expect(host.applicationProviders.current).toBeUndefined();
          expect(host.applicationProviders.failure).toContain("previous writer remains stopped");
          return;
        }
        await host.activateGeneration({ pluginId: "business", generation: "one", graphGeneration: "target", register });
        await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
        expect(resume).toHaveBeenCalledExactlyOnceWith(thread.id);
        expect(app.getSnapshot().conversation.threadId).toBe(thread.id);
      }
    }
  }
  expect(app.getSnapshot().tabs.items.some(tab => tab.draftText === "keep source draft")).toBe(true);
});

it.each([false, true])("restores an exact pending destination before an unrelated archive catalog settles (failure=%s)", async failure => {
  const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
  const target = { kind: "project" as const, project_id: "b", cwd: "/b" };
  const projects = [source, target].map(context => ({ id: context.project_id, name: context.project_id, path: context.cwd, created_at: "", updated_at: "" }));
  const thread = { id: "destination", cwd: target.cwd, project_id: "b", title: "Destination", preview: "", status: "idle", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", turns: [] };
  let selected = source;
  let blockArchives = false;
  const diagnostic = vi.spyOn(console, "warn").mockImplementation(() => {});
  let releaseArchives!: () => void;
  const archives = new Promise<void>(resolve => { releaseArchives = resolve; });
  const resume = vi.fn().mockResolvedValue({ thread, held_user_messages: [{ id: "held", thread_id: thread.id, origin: "queue", prompt: "retained queue" }] });
  window.wuu = {
    listProjects: vi.fn(async () => ({ projects, active_context: selected })),
    selectProject: vi.fn(async id => { selected = id === "b" ? target : source; return { projects, active_context: selected }; }),
    initialize: vi.fn(async () => ({ provider: "fixture", model: "fixture", providers: [], extension_inventory: [], workspace_root: selected.cwd })),
    listThreads: vi.fn(async () => ({ threads: [thread] })),
    listArchivedThreads: vi.fn(async () => {
      if (!blockArchives) return { threads: [] };
      await archives;
      if (failure) throw new Error("archive catalog offline");
      return { threads: [{ ...thread, id: "archived-history", archived: true }] };
    }),
    resumeThread: resume, onServerEvent: () => () => {}, listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
  } as unknown as WuuDesktopApi;
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  await app.invoke("composer.set-draft", { text: "source draft" });
  await app.invoke("thread.activate", { threadId: thread.id, workspaceId: "b" });
  expect(resume).toHaveBeenCalledOnce();
  blockArchives = true;
  const priorCatalogCalls = vi.mocked(window.wuu.listArchivedThreads).mock.calls.length;
  await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
  const replacing = host.activateGeneration({ pluginId: "business", generation: "two", register });
  try {
    await vi.waitFor(() => expect(resume).toHaveBeenCalledTimes(2));
    await replacing;
    await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
    expect(app.getSnapshot().conversation.threadId).toBe(thread.id);
    expect(app.getSnapshot().composerPending).toMatchObject([{ threadId: thread.id, queued: [{ id: "held", text: "retained queue" }] }]);
    expect(app.getSnapshot().tabs.items.some(tab => tab.draftText === "source draft")).toBe(true);
    expect(window.wuu.listArchivedThreads).toHaveBeenCalledTimes(priorCatalogCalls + 1);
    releaseArchives();
    if (failure) {
      await new Promise(resolve => setTimeout(resolve, 0));
      expect(app.getSnapshot().phase).toBe("ready");
      expect(app.getSnapshot().conversation.threadId).toBe(thread.id);
      expect(diagnostic).toHaveBeenCalledWith("Background thread catalog refresh failed", expect.objectContaining({ message: "archive catalog offline" }));
    } else {
      await vi.waitFor(() => expect(app.getSnapshot().conversationWorkspace.threads.some(row => row.id === "archived-history")).toBe(true));
    }
  } finally {
    releaseArchives();
    await replacing;
  }
});

it.each(["activation", "graph", "paging", "hydration", "graph-cancelled", "graph-newer"] as const)("transfers only the selected search address across application replacement (%s)", async phase => {
  const handoff = phase.startsWith("graph");
  const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
  const target = { kind: "project" as const, project_id: "b", cwd: "/b" };
  const projects = [source, target].map(context => ({ id: context.project_id, name: context.project_id, path: context.cwd, created_at: "", updated_at: "" }));
  let selected = source;
  const initialization = () => ({ provider: "fixture", model: "fixture", providers: [],
    application_surfaces: { "ui.application": { plugin_id: "business", generation: "one", provider_id: "ui.application" } },
    extension_inventory: [{ id: "business", fingerprint: "one", graph_generation: handoff && selected.cwd === target.cwd ? "target" : "source" }],
  });
  const answer = { id: "addressed-answer", seq: 10, type: "agent_message" as const, text: "exact historical needle" };
  const history = { id: "historical-turn", status: "completed" as const, items_view: "full" as const, items: [answer] };
  const thread = { id: "search-destination", cwd: target.cwd, project_id: "b", title: "Destination", preview: "", status: "idle", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", history_cursor: "older-page", turns: phase === "hydration"
    ? [{ ...history, items: [{ ...answer, text: "Preview", remote_content_ref: "content:addressed-answer" }] }]
    : [{ id: "tail", status: "completed", items: [{ id: "latest", seq: 100, type: "agent_message", text: "Latest answer" }] }] };
  let releaseResume!: (value: unknown) => void;
  const retiredResume = new Promise(resolve => { releaseResume = resolve; });
  const resume = vi.fn().mockResolvedValue({ thread });
  if (phase === "activation") resume.mockReturnValueOnce(retiredResume);
  let releaseOldContent!: (value: unknown) => void;
  const oldContent = new Promise(resolve => { releaseOldContent = resolve; });
  const content = vi.fn().mockResolvedValue(answer);
  if (phase === "hydration") content.mockReturnValueOnce(oldContent);
  const listeners = new Set<(event: unknown) => void>();
  window.wuu = { listProjects: vi.fn(async () => ({ projects, active_context: selected })), resumeThread: resume,
    searchThreads: vi.fn().mockResolvedValue({ results: [{ thread, message_seq: 10, snippet: answer.text }] }),
    loadEarlierThreadHistory: vi.fn().mockResolvedValue(undefined), readRemoteItem: content,
    onServerEvent: (listener: (event: unknown) => void) => { listeners.add(listener); return () => listeners.delete(listener); },
    listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }), startThread: vi.fn(), startTurn: vi.fn(),
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "selectRuntimeContext").mockImplementation(async context => { selected = context as typeof selected; return { projects, active_context: selected }; });
  vi.spyOn(RuntimeLoader, "loadRuntimeConfiguration").mockImplementation(async workspace => ({ activeContext: workspace.active_context, projects, initialized: initialization() as never }));
  vi.spyOn(RuntimeLoader, "loadRuntimeThreadList").mockResolvedValue([thread as never]);
  vi.spyOn(RuntimeLoader, "loadRuntime").mockImplementation(async (_workspace, options) => ({ activeContext: selected, projects,
    thread: options?.resumeLatestThread !== false && options?.resumeThreadID === thread.id ? (await resume()).thread : undefined,
    threads: [thread as never], initialized: initialization() as never }));
  const host = new PluginHost({ react: React }); hosts.push(host);
  const register = (api: PluginGenerationApi) => activate(api as unknown as Parameters<typeof activate>[0]);
  await host.activateGeneration({ pluginId: "business", generation: "one", graphGeneration: "source", register });
  await host.selectApplicationBinding({ pluginId: "business", generation: "one", id: "ui.application" });
  let app!: ApplicationServiceV1;
  await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { app = api.application; } });
  await app.invoke("search.run", { query: "historical needle" });
  const locating = app.invoke("search.locate", { resultId: app.getSnapshot().search.results[0].resultId }).catch(error => error);
  const targetId = app.getSnapshot().search.target!.id;
  if (phase === "activation") await vi.waitFor(() => expect(resume).toHaveBeenCalledOnce());
  else await locating;
  if (handoff) expect(resume).not.toHaveBeenCalled();
  if (phase === "paging") expect(window.wuu.loadEarlierThreadHistory).toHaveBeenCalledExactlyOnceWith(thread.id, "older-page");
  if (phase === "hydration") expect(content).toHaveBeenCalledExactlyOnceWith("content:addressed-answer");
  let expectedThread = thread;
  if (phase === "graph-cancelled") await app.invoke("search.cancel", { targetId });
  if (phase === "graph-newer") {
    expectedThread = { ...thread, id: "newer-selection", cwd: source.cwd, project_id: "a" };
    resume.mockResolvedValue({ thread: expectedThread });
    await app.invoke("thread.activate", { threadId: expectedThread.id, workspaceId: "a" });
  }
  const oldApp = app;
  if (handoff) await host.activateGeneration({ pluginId: "business", generation: "one", graphGeneration: "target", register });
  else {
    await host.selectApplicationBinding({ pluginId: "business", generation: "two", id: "ui.application" });
    await host.activateGeneration({ pluginId: "business", generation: "two", register });
  }
  await host.activateGeneration({ pluginId: "shell", generation: "two", register: api => { app = api.application; } });
  expect(app.getSnapshot().conversation.threadId).toBe(expectedThread.id);
  if (phase === "graph-cancelled" || phase === "graph-newer") {
    expect(app.getSnapshot().search.target).toBeUndefined();
    expect(window.wuu.loadEarlierThreadHistory).not.toHaveBeenCalled();
    expect(content).not.toHaveBeenCalled();
    expect(() => oldApp.getSnapshot()).toThrow();
    return;
  }
  expect(app.getSnapshot().search.target).toMatchObject({ id: targetId, threadId: thread.id, messageSeq: 10, query: "historical needle" });
  expect(resume).toHaveBeenCalledTimes(handoff ? 1 : 2);
  if (phase !== "hydration") {
    expect(window.wuu.loadEarlierThreadHistory).toHaveBeenLastCalledWith(thread.id, "older-page");
    for (const listener of [...listeners]) listener({ kind: "notification", workdir: target.cwd, message: { method: "thread/historyLoaded", params: {
      thread_id: thread.id, cursor: "older-page", turns: [history],
    } } });
  }
  await vi.waitFor(() => expect(app.getSnapshot().search.target).toMatchObject({ id: targetId, phase: "ready", turnId: history.id, itemId: answer.id }));
  const accepted = app.getSnapshot().conversation;
  releaseResume({ thread }); releaseOldContent({ ...answer, text: "retired historical needle" }); await locating;
  await Promise.resolve(); await Promise.resolve();
  expect(app.getSnapshot().conversation).toEqual(accepted);
  expect(() => oldApp.getSnapshot()).toThrow();
  await app.invoke("search.complete-target", { targetId });
  expect(app.getSnapshot().search.target).toBeUndefined();
  expect(window.wuu.startThread).not.toHaveBeenCalled(); expect(window.wuu.startTurn).not.toHaveBeenCalled();
});
