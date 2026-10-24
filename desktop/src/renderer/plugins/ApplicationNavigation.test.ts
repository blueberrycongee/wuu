import * as RuntimeLoader from "../RuntimeLoadState";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import { afterEach, describe, expect, it, vi } from "vitest";
import { initialState, type AppState } from "../AppState";
import type { ApplicationWebWorkspaceV1, WebWorkspaceInitializationStateV1, WebWorkspaceStateV1 } from "../../../../packages/plugin-sdk/src/ui-web-workspace";
import { createRibbonBrowserState } from "../RibbonBrowserState";
import { readNavigationRibbonPreferences, saveNavigationRibbonPreferences } from "../NavigationRibbonPreferences";
import { ApplicationNavigation } from "./ApplicationNavigation";

afterEach(() => localStorage.clear());
function webControls(values: Partial<ApplicationWebWorkspaceV1> = {}): ApplicationWebWorkspaceV1 {
  return { capabilities: { available: true, windowInsets: true, nativeOverlays: true }, limits: { tabs: 1, zoomMin: 0.25, zoomMax: 3 }, ...values } as ApplicationWebWorkspaceV1;
}
function fixture(controls = webControls()) {
  let state: AppState = { ...initialState, threads: [{ id: "t", preview: "", cwd: "/one", model_provider: "p", model: "m", status: "idle", created_at: "", updated_at: "", turns: [], forked_from_id: "parent", pinned: true }] };
  const cache = { getSnapshot: () => ({ workspaceThreadsByWorkspaceID: {}, cachedScratchThreads: [], requestedWorkspaceThreadIDs: new Set<string>() }),
    loadWorkspaceThreads: vi.fn(), cacheSidebarThreads: vi.fn(), updateCachedSidebarThread: vi.fn(),
    patchCachedSidebarThread: vi.fn(), updateCachedSidebarThreadPinned: vi.fn(), removeCachedSidebarThread: vi.fn() };
  const mutations = { toggleThreadPinned: vi.fn(), archiveThread: vi.fn(), deleteThread: vi.fn() };
  const listeners = new Set<() => void>();
  const updateState = (update: AppState | ((state: AppState) => AppState)) => { state = typeof update === "function" ? update(state) : update; for (const listener of listeners) listener(); };
  const navigation = new ApplicationNavigation({ getWebWorkspace: () => controls, getState: () => state,
    subscribeState: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, updateState, changed: vi.fn(), getPendingDraftTabIds: () => [], sidebarCache: cache, mutationActions: () => mutations });
  return { navigation, cache, mutations, get: () => state, set: updateState };
}

describe("public navigation domain", () => {
  it("closes only the addressed active plugin view and exposes region reveal requests", async () => {
    window.wuu = {} as typeof window.wuu;
    window.wuu.reportPluginDesktopActivation = vi.fn().mockResolvedValue(undefined);
    const { navigation } = fixture();
    await desktopPluginHost.activateGeneration({ pluginId: "close-test", generation: "one", graphGeneration: "graph", register(api) {
      api.registerViewType({ id: "page", title: "Page", render: () => null });
    } });
    try {
      const first = await desktopWorkbenchController.openPluginView("close-test", "page", { region: "primary" });
      expect(navigation.getSnapshot().activePluginView).toMatchObject({ id: first, closable: true });
      await expect(navigation.invoke("navigation.close-plugin", { viewId: "stale" })).rejects.toThrow();
      expect(navigation.getSnapshot().activePluginView?.id).toBe(first);
      await navigation.invoke("navigation.close-plugin", { viewId: first });
      expect(navigation.getSnapshot().activePluginView).toBeUndefined();
      await navigation.invoke("navigation.reveal-region", { region: "auxiliary" });
      expect(navigation.getSnapshot().regionVisibilityRequest).toEqual({ id: 1, region: "auxiliary" });
    } finally { await desktopPluginHost.unload("close-test"); }
  });

  it("repairs a missed foreground start from raw executor state and ignores a moved source", async () => {
    vi.useFakeTimers();
    const h = fixture(webControls({ capabilities: { available: false, windowInsets: false, nativeOverlays: false } }));
    const thread = h.get().threads[0];
    h.set(state => ({ ...state, initialized: {} as AppState["initialized"], activeContext: { kind: "no_project", cwd: "/one" }, thread }));
    let running!: (items: { thread_id: string }[]) => void;
    let finish!: (value: unknown) => void;
    const resume = vi.fn(() => new Promise(resolve => { finish = resolve; }));
    window.wuu = { onRunningThreadsChanged: (listener: typeof running) => { running = listener; return () => {}; }, resumeThread: resume } as unknown as typeof window.wuu;
    const stop = h.navigation.start();
    try {
      expect(desktopPluginHost.getActiveConversationThreadId()).toBe(thread.id);
      running([{ thread_id: thread.id }]);
      await vi.advanceTimersByTimeAsync(200);
      expect(resume).toHaveBeenCalledExactlyOnceWith(thread.id);
      h.set(state => ({ ...state, activeContext: { kind: "no_project", cwd: "/next" } }));
      finish({ thread: { ...thread, status: "in_progress", turns: [{ id: "late", status: "in_progress", items_view: "full", items: [] }] } });
      await vi.advanceTimersByTimeAsync(0);
      expect(h.get().thread?.status).toBe("idle");
    } finally { stop(); vi.useRealTimers(); }
    expect(desktopPluginHost.getActiveConversationThreadId()).toBeUndefined();
  });

  it("refreshes durable thread state on focus and visible heartbeat until host stop", async () => {
    vi.useFakeTimers(); vi.setSystemTime(new Date("2026-10-24T00:00:00Z"));
    const h = fixture(webControls({ capabilities: { available: false, windowInsets: false, nativeOverlays: false } }));
    const rows = [{ ...h.get().threads[0], title: "from phone" }];
    h.set(state => ({ ...state, initialized: {} as AppState["initialized"], activeContext: { kind: "no_project", cwd: "/one" } }));
    const refresh = vi.spyOn(RuntimeLoader, "loadThreadListRefresh").mockResolvedValue(rows);
    window.wuu = {} as typeof window.wuu;
    const stop = h.navigation.start();
    try {
      window.dispatchEvent(new Event("focus")); await vi.advanceTimersByTimeAsync(0);
      expect(refresh).toHaveBeenCalledOnce(); expect(h.get().threads[0].title).toBe("from phone");
      window.dispatchEvent(new Event("focus")); await vi.advanceTimersByTimeAsync(0); expect(refresh).toHaveBeenCalledOnce();
      await vi.advanceTimersByTimeAsync(60_000); expect(refresh).toHaveBeenCalledTimes(2);
      stop(); window.dispatchEvent(new Event("focus")); await vi.advanceTimersByTimeAsync(60_000);
      expect(refresh).toHaveBeenCalledTimes(2);
    } finally { stop(); refresh.mockRestore(); vi.useRealTimers(); }
  });

  it("publishes independent immutable rows preserving pin and fork identity", () => {
    const h = fixture();
    const view = h.navigation.getSnapshot();
    expect(view.threads[0]).toMatchObject({ id: "t", pinned: true, forked_from_id: "parent" });
    expect(Object.isFrozen(view.threads[0].turns)).toBe(true);
    expect(() => { (view.threads as unknown as unknown[]).pop(); }).toThrow();
    expect(h.get().threads).toHaveLength(1);
    expect(h.navigation.getSnapshot()).toBe(view);
  });
  it("uses the shared mutation owner and rejects stale thread identities", async () => {
    const h = fixture();
    await h.navigation.invoke("navigation.pin-thread", { threadId: "t", pinned: false });
    expect(h.mutations.toggleThreadPinned).toHaveBeenCalledWith(expect.objectContaining({ id: "t" }));
    await expect(h.navigation.invoke("navigation.delete-thread", { threadId: "missing" })).rejects.toThrow();
    expect(h.mutations.deleteThread).not.toHaveBeenCalled();
  });
  it("reconciles a folder assignment in both active and cached rows", async () => {
    const h = fixture();
    window.wuu = { updateThreadOrganization: vi.fn().mockResolvedValue({}) } as unknown as typeof window.wuu;
    await h.navigation.invoke("navigation.move-to-folder", { threadId: "t", folderId: "folder" });
    expect(h.get().threads[0].folder_id).toBe("folder");
    expect(h.cache.patchCachedSidebarThread).toHaveBeenCalledWith("t", expect.any(Function));
  });
  it("does not update read receipts for a failed folder mutation", async () => {
    const h = fixture();
    window.wuu = { updateThreadOrganization: vi.fn().mockRejectedValue(new Error("offline")) } as unknown as typeof window.wuu;
    const before = h.get();
    await expect(h.navigation.invoke("navigation.move-to-folder", { threadId: "t", folderId: "folder" })).rejects.toThrow("offline");
    expect(h.get()).toBe(before);
    expect(h.cache.patchCachedSidebarThread).not.toHaveBeenCalled();
  });
  it("publishes folders even when the independent thread refresh fails", async () => {
    const h = fixture();
    window.wuu = { listAllThreads: vi.fn().mockRejectedValue(new Error("threads offline")),
      getSessionOrganization: vi.fn().mockResolvedValue({ organization: { folders: [{ id: "folder", name: "Folder" }] } }),
    } as unknown as typeof window.wuu;
    await expect(h.navigation.invoke("navigation.refresh", {})).rejects.toThrow("threads offline");
    expect(h.navigation.getSnapshot().folders).toEqual([{ id: "folder", name: "Folder" }]);
    expect(h.cache.cacheSidebarThreads).not.toHaveBeenCalled();
  });
  it("retains folder edits when an older catalog refresh completes", async () => {
    const h = fixture();
    let finish!: (value: unknown) => void;
    window.wuu = { listAllThreads: vi.fn().mockResolvedValue({ threads: [] }),
      getSessionOrganization: vi.fn(() => new Promise(resolve => { finish = resolve; })),
      createSessionFolder: vi.fn().mockResolvedValue({ group: { id: "new", name: "New" } }),
    } as unknown as typeof window.wuu;
    const refresh = h.navigation.invoke("navigation.refresh", {});
    await h.navigation.invoke("navigation.create-folder", { name: "New" });
    finish({ organization: { folders: [] } });
    await refresh;
    expect(h.navigation.getSnapshot().folders).toEqual([{ id: "new", name: "New" }]);
  });

});

describe("public native navigation", () => {
  it("reopens through native history and reveals the authoritative current tab without selecting twice", async () => {
    const selectTab = vi.fn();
    const reopenTab = vi.fn().mockResolvedValue({ tabs: [{ id: "restored", title: "Restored", url: "https://example.com" }], activeTabId: "restored", canReopenTab: false });
    window.wuu = {} as typeof window.wuu;
    const controls = webControls({
      createTab: vi.fn().mockResolvedValue({ tabs: [{ id: "first", title: "First", url: "about:blank" }], activeTabId: "first", canReopenTab: true }),
      selectTab, reopenTab,
    });
    const { navigation } = fixture(controls);
    await navigation.invoke("navigation.create-browser-tab", {});
    expect(navigation.getSnapshot().browser?.canReopenTab).toBe(true);
    expect(navigation.getSnapshot().browser?.canCreate).toBe(false);
    await navigation.invoke("navigation.reopen-browser-tab", {});
    expect(reopenTab).toHaveBeenCalledTimes(1);
    expect(navigation.getSnapshot().activeWebsiteId).toBe("restored");
    expect(navigation.getSnapshot().browser?.canReopenTab).toBe(false);
    await navigation.invoke("navigation.show-native", {});
    expect(navigation.getSnapshot().activeWebsiteId).toBeUndefined();
    await navigation.invoke("navigation.reveal-browser", {});
    expect(navigation.getSnapshot().activeWebsiteId).toBe("restored");
    expect(selectTab).not.toHaveBeenCalled();
    expect(reopenTab).toHaveBeenCalledTimes(1);
  });

  it("preserves failed native reopen errors and does not reveal a missing browser", async () => {
    window.wuu = {} as typeof window.wuu;
    const { navigation } = fixture(webControls({ reopenTab: vi.fn().mockRejectedValue(new Error("native failure")) }));
    await navigation.invoke("navigation.reveal-browser", {});
    expect(navigation.getSnapshot().activeWebsiteId).toBeUndefined();
    await expect(navigation.invoke("navigation.reopen-browser-tab", {})).rejects.toThrow("native failure");
    expect(navigation.getSnapshot().activeWebsiteId).toBeUndefined();
  });

  it("prefers the native account window, preserves its error and closes inline state without reopening", async () => {
    const openAccountWindow = vi.fn().mockResolvedValue(undefined);
    window.wuu = { openAccountWindow } as unknown as typeof window.wuu;
    const { navigation } = fixture();
    await navigation.invoke("navigation.show-account", { open: true });
    expect(openAccountWindow).toHaveBeenCalledTimes(1);
    expect(navigation.getSnapshot().accountOpen).toBe(false);
    await navigation.invoke("navigation.show-account", { open: false });
    expect(openAccountWindow).toHaveBeenCalledTimes(1);
    openAccountWindow.mockRejectedValueOnce(new Error("window unavailable"));
    await expect(navigation.invoke("navigation.show-account", { open: true })).rejects.toThrow("window unavailable");
    expect(navigation.getSnapshot().accountOpen).toBe(false);
    delete window.wuu.openAccountWindow;
    await navigation.invoke("navigation.show-account", { open: true });
    expect(navigation.getSnapshot().accountOpen).toBe(true);
    await navigation.invoke("navigation.show-account", { open: false });
    expect(navigation.getSnapshot().accountOpen).toBe(false);
  });
});


describe("canonical injected ribbon owner", () => {
  it("migrates once through the selected public service and keeps newer events over initialization", async () => {
    window.wuu = undefined as unknown as typeof window.wuu;
    saveNavigationRibbonPreferences({websites:[{id:"website:old",url:"https://example.com",title:"Old"}],order:["session:one","website:old","session:two"]});
    let finish!: (value:WebWorkspaceInitializationStateV1)=>void;
    let emit!: (value:WebWorkspaceStateV1)=>void;
    const off=vi.fn();
    const initializeTabs=vi.fn(()=>new Promise<WebWorkspaceInitializationStateV1>(resolve=>{finish=resolve;}));
    const controls=webControls({initializeTabs,onState:listener=>{emit=listener;return off;}});
    const owner=createRibbonBrowserState(()=>controls);
    const changed=vi.fn(), onError=vi.fn(); owner.subscribe(changed);
    const stop=owner.start(onError);
    const initialized={id:"ribbon-browser",url:"https://example.com",title:"Old",loading:false,canGoBack:false,canGoForward:false,activeTabId:"child",tabs:[{id:"child",url:"https://example.com",title:"Child",loading:false,canGoBack:false,canGoForward:false,zoomFactor:1}],migratedTabGroups:{"website:old":["child"]}};
    emit({...initialized,title:"Newer event"}); finish(initialized);
    await vi.waitFor(()=>expect(owner.getSnapshot()?.title).toBe("Newer event"));
    expect(initializeTabs).toHaveBeenCalledExactlyOnceWith([{id:"website:old",url:"https://example.com/",title:"Old"}]);
    expect(readNavigationRibbonPreferences()).toMatchObject({order:["session:one","child","session:two"],browserTabsMigrated:true});
    stop(); expect(off).toHaveBeenCalledTimes(1);
    emit({...initialized,title:"after stop"}); expect(owner.getSnapshot()?.title).toBe("Newer event"); expect(onError).not.toHaveBeenCalled();
  });
  it("suppresses late stopped initialization without borrowing a global native fallback", async () => {
    const fallback=vi.fn(); window.wuu={webWorkspace:{initializeTabs:fallback}} as unknown as typeof window.wuu;
    let finish!: (value:WebWorkspaceInitializationStateV1)=>void;
    const off=vi.fn(), onError=vi.fn();
    const owner=createRibbonBrowserState(()=>webControls({initializeTabs:()=>new Promise(resolve=>{finish=resolve;}),onState:()=>off}));
    const stop=owner.start(onError);stop();
    finish({id:"ribbon-browser",url:"about:blank",title:"",loading:false,canGoBack:false,canGoForward:false});
    await Promise.resolve();await Promise.resolve();
    expect(owner.getSnapshot()).toBeUndefined();expect(off).toHaveBeenCalledTimes(1);expect(fallback).not.toHaveBeenCalled();
    expect(readNavigationRibbonPreferences().browserTabsMigrated).toBeUndefined();
  });
});
