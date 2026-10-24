import { describe, expect, it, vi } from "vitest";
import { initialState, type AppState } from "../AppState";
import { ApplicationNavigation } from "./ApplicationNavigation";

function fixture() {
  let state: AppState = { ...initialState, threads: [{ id: "t", preview: "", cwd: "/one", model_provider: "p", model: "m", status: "idle", created_at: "", updated_at: "", turns: [], forked_from_id: "parent", pinned: true }] };
  const cache = { getSnapshot: () => ({ workspaceThreadsByWorkspaceID: {}, cachedScratchThreads: [], requestedWorkspaceThreadIDs: new Set<string>() }),
    loadWorkspaceThreads: vi.fn(), cacheSidebarThreads: vi.fn(), updateCachedSidebarThread: vi.fn(),
    patchCachedSidebarThread: vi.fn(), updateCachedSidebarThreadPinned: vi.fn(), removeCachedSidebarThread: vi.fn() };
  const mutations = { toggleThreadPinned: vi.fn(), archiveThread: vi.fn(), deleteThread: vi.fn() };
  const navigation = new ApplicationNavigation({ getState: () => state,
    updateState: update => { state = typeof update === "function" ? update(state) : update; }, changed: vi.fn(), getPendingDraftTabIds: () => [], sidebarCache: cache, mutationActions: () => mutations });
  return { navigation, cache, mutations, get: () => state };
}

describe("public navigation domain", () => {
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
    window.wuu = { webWorkspace: {
      createTab: vi.fn().mockResolvedValue({ tabs: [{ id: "first", title: "First", url: "about:blank" }], activeTabId: "first", canReopenTab: true }),
      selectTab, reopenTab,
    } } as unknown as typeof window.wuu;
    const { navigation } = fixture();
    await navigation.invoke("navigation.create-browser-tab", {});
    expect(navigation.getSnapshot().browser?.canReopenTab).toBe(true);
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
    window.wuu = { webWorkspace: { reopenTab: vi.fn().mockRejectedValue(new Error("native failure")) } } as unknown as typeof window.wuu;
    const { navigation } = fixture();
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
