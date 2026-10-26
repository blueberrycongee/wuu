import type { SetStateAction } from "react";
import type { ApplicationNavigationActionMapV1 as Actions, ApplicationNavigationViewV1, NavigationReadonly } from "../../../../packages/plugin-sdk/src/ui-navigation";
import type { DesktopProject, Thread } from "../../shared/protocol";
import type { ApplicationWebWorkspaceV1 } from "../../../../packages/plugin-sdk/src/ui-web-workspace";
import { activeThreadForState, activeThreadIDForState, upsertThread, markThreadSummariesViewed, mergeSidebarThread, summarizeThreadsForSidebar, presentationRunningThreadIDs, reconcileListedThreadState, isThreadRunning, updateThreadByID, type AppState, type ThreadSummary } from "../AppState";
import { createRibbonBrowserState } from "../RibbonBrowserState";
import { parseSessionOrganization } from "../SessionOrganization";
import { loadThreadListRefresh, resumeThreadForRefresh } from "../RuntimeLoadState";
import { writeThreadReadState } from "../ThreadReadState";
import { hostSupports } from "../HostCapabilities";
import { ENABLE_ACCOUNT } from "../FeatureFlags";
import { pluginToggle } from "../extensionPackageState";
import { loadPluginIconURL } from "../PublicIcon";
import type { UIContributionRegistryV1, ApplicationWorkbenchV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { primaryViewNavigation } from "./PrimaryViewNavigation";
import { visibleWorkbenchView } from "./Workbench";
import type { ThreadMutationActions } from "../ThreadMutationActions";

const actions = new Set<keyof Actions>([
  "navigation.refresh", "navigation.load-workspace", "navigation.mark-viewed", "navigation.pin-thread", "navigation.archive-thread", "navigation.delete-thread", "navigation.adopt-thread",
  "navigation.create-folder", "navigation.rename-folder", "navigation.delete-folder", "navigation.reorder-folders", "navigation.move-to-folder",
  "navigation.close-plugin", "navigation.reveal-region", "navigation.open-plugin", "navigation.show-native", "navigation.show-search", "navigation.show-account", "navigation.focus-workspace", "navigation.focus-composer",
  "navigation.reveal-thread", "navigation.open-external", "navigation.load-icon", "navigation.account-status", "navigation.account-logout", "navigation.install-update",
  "navigation.reopen-browser-tab", "navigation.reveal-browser", "navigation.create-browser-tab", "navigation.select-browser-tab", "navigation.close-browser-tab", "navigation.pin-browser-tab", "navigation.mute-browser-tab", "navigation.reorder-browser-tabs",
]);
export function isApplicationNavigationAction(action: string): action is keyof Actions { return actions.has(action as keyof Actions); }
function immutable<T>(value: T): NavigationReadonly<T> {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => { if (!item || typeof item !== "object") return; Object.values(item).forEach(freeze); Object.freeze(item); };
  freeze(copy); return copy as NavigationReadonly<T>;
}

/** Receives the controller's existing cache and mutation owner; never a second session registry. */
export class ApplicationNavigation {
  private folders: { id: string; name: string }[] = [];
  private readonly browser: ReturnType<typeof createRibbonBrowserState>;
  private update: ApplicationNavigationViewV1["update"] = { kind: "unsupported" };
  private activeWebsiteId?: string;
  private regionVisibilityRequest?: ApplicationNavigationViewV1["regionVisibilityRequest"];
  private searchOpen = false;
  private accountOpen = false;
  private focusedWorkspaceId?: string;
  private composerFocusRequest?: { id: number; tabId: string };
  private snapshot?: ApplicationNavigationViewV1;
  private snapshotKey = "";
  private folderRevision = 0;
  private runningThreadIds: ReadonlySet<string> = new Set();
  private folderQueue: Promise<unknown> = Promise.resolve();
  constructor(private readonly host: {
    getWebWorkspace: () => ApplicationWebWorkspaceV1;
    getContributions: () => UIContributionRegistryV1;
    getWorkbench: () => ApplicationWorkbenchV1;
    getState: () => AppState;
    subscribeState: (listener: () => void) => () => void;
    updateState: (update: SetStateAction<AppState>) => void;
    changed: () => void;
    sidebarCache: {
      getSnapshot: () => { workspaceThreadsByWorkspaceID: Record<string, Thread[]>; cachedScratchThreads: Thread[]; requestedWorkspaceThreadIDs: ReadonlySet<string> };
      loadWorkspaceThreads: (project: DesktopProject) => Promise<void>;
      cacheSidebarThreads: (threads: Thread[], requested?: { projects: Record<string, Thread[]>; scratch: Thread[] }) => void;
      updateCachedSidebarThread: (thread: Thread) => void;
      patchCachedSidebarThread: (id: string, update: (thread: Thread) => Thread) => void;
      updateCachedSidebarThreadPinned: (id: string, pinned: boolean) => void;
      removeCachedSidebarThread: (id: string) => void;
    };
    getPendingDraftTabIds: () => readonly string[];
    mutationActions: () => Pick<ThreadMutationActions, "toggleThreadPinned" | "archiveThread" | "deleteThread">;
  }) { this.browser = createRibbonBrowserState(host.getWebWorkspace); }

  private threads(): Thread[] {
    const state = this.host.getState();
    const cache = this.host.sidebarCache.getSnapshot();
    const rows = new Map<string, Thread>();
    for (const thread of [...cache.cachedScratchThreads, ...Object.values(cache.workspaceThreadsByWorkspaceID).flat(), ...state.threads, ...[state.thread, state.secondaryThread].filter((row): row is Thread => Boolean(row))]) {
      const previous = rows.get(thread.id);
      rows.set(thread.id, previous ? mergeSidebarThread(previous, thread) : thread);
    }
    return [...rows.values()];
  }
  private thread(id: string): ThreadSummary {
    const thread = summarizeThreadsForSidebar(this.threads()).find(row => row.id === id);
    if (!thread) throw new Error(`Unknown navigation thread: ${id}`);
    return thread;
  }
  getSnapshot(): ApplicationNavigationViewV1 {
    const state = this.host.getState();
    const workbench = this.host.getWorkbench().getSnapshot();
    const active = visibleWorkbenchView(workbench, "primary");
    const browser = this.browser.getSnapshot();
    const threads = this.threads().filter(row => !row.ephemeral);
    const rows = summarizeThreadsForSidebar(threads, presentationRunningThreadIDs(threads, this.runningThreadIds));
    const view = {
      initialized: Boolean(state.initialized), activeContextKind: state.activeContext?.kind, selectedWorkspaceId: state.activeProjectId,
      workspaces: state.projects, threads: rows,
      loadingWorkspaceIds: [...this.host.sidebarCache.getSnapshot().requestedWorkspaceThreadIDs],
      pendingConversations: this.host.getPendingDraftTabIds().flatMap(id => { const tab = state.sessionTabs.find(tab => tab.id === id); return tab && tab.kind !== "skills" ? [{ id, title: tab.prompt, context: tab.context }] : []; }),
      lastViewedTurnByThreadId: state.lastViewedTurnByThreadID, folders: this.folders,
      plugins: (state.initialized?.extension_inventory ?? []).filter(row => row.kind === "plugin" || row.kind === "skill").map(row => ({
        id: row.id, name: row.name, pluginId: row.provenance.plugin_id ?? row.id, fingerprint: row.fingerprint ?? "", icon: row.icon, enabled: pluginToggle(row).on, kind: row.kind as "plugin" | "skill",
      })),
      destinations: primaryViewNavigation(this.host.getContributions().getNavigationEntries(), workbench),
      regionVisibilityRequest: this.regionVisibilityRequest,
      activePluginView: active && { closable: !this.host.getContributions().getNavigationEntries().some(entry => entry.pluginId === active.view.pluginId && entry.view === active.view.viewTypeId)
        || workbench.views.filter(view => view.region === "primary" && view.pluginId === active.view.pluginId && view.viewTypeId === active.view.viewTypeId).length > 1, id: active.view.id, viewTypeId: active.view.viewTypeId, context: active.view.context, pluginId: active.view.pluginId, title: active.definition.title, webPage: workbench.webPageViewIds.includes(active.view.id) },
      canRevealThread: hostSupports("revealSession"),
      canCreateWorkspace: hostSupports("createBlankProject"), canChooseWorkspace: hostSupports("chooseProjectFolder"),
      projectAgentEnabled: Boolean(state.initialized?.features?.project_agent), accountAvailable: ENABLE_ACCOUNT && hostSupports("remoteAccount") && hostSupports("getRemoteControlSnapshot"),
      update: this.update, browser: browser && { tabs: browser.tabs ?? [], activeTabId: browser.activeTabId, canCreate: (browser.tabs?.length ?? 0) < this.host.getWebWorkspace().limits.tabs, canReopenTab: Boolean(browser.canReopenTab) },
      activeWebsiteId: this.activeWebsiteId && browser?.tabs?.some(row => row.id === this.activeWebsiteId) ? this.activeWebsiteId : undefined,
      searchOpen: this.searchOpen, accountOpen: this.accountOpen, focusedWorkspaceId: this.focusedWorkspaceId, composerFocusRequest: this.composerFocusRequest,
    };
    const key = JSON.stringify(view);
    if (key !== this.snapshotKey || !this.snapshot || this.snapshot.activePluginView?.context !== active?.view.context) {
      this.snapshotKey = key;
      // Page-local stores use the workbench's frozen context as their instance key.
      // Copy data rows, but retain that object for the page and its contextual UI.
      this.snapshot = Object.freeze({
        ...immutable({ ...view, activePluginView: undefined }),
        activePluginView: view.activePluginView && Object.freeze(view.activePluginView),
      }) as ApplicationNavigationViewV1;
    }
    return this.snapshot;
  }
  start(): () => void {
    let active = true;
    let updateEvent = false;
    let runningEvent = false;
    const changed = () => { if (active) this.host.changed(); };
    const off = [this.host.getContributions().subscribe(changed), this.host.getWorkbench().subscribe(changed), this.browser.subscribe(() => {
      // Native keyboard commands can select or close a tab without invoking a
      // navigation action. Follow that selection while the browser is foreground.
      if (this.activeWebsiteId) this.activeWebsiteId = this.browser.getSnapshot()?.activeTabId;
      changed();
    }), this.browser.start(error => this.host.updateState(state => ({ ...state, status: String(error) })))];
    let repairTimer: number | undefined;
    let repairSignature = "";
    let repairVersion = 0;
    let repairInFlight = "";
    const reconcileForeground = () => {
      if (!active) return;
      const state = this.host.getState();
      const thread = activeThreadForState(state);
      const threadId = thread?.id;
      // Card authors target their explicit conversation; layout does not mutate registry-global focus.
      const cwd = state.activeContext?.cwd;
      const running = Boolean(thread && isThreadRunning(thread));
      const hasRunningTurn = Boolean(thread?.turns.some(turn => turn.status === "in_progress"));
      const executorRunning = Boolean(threadId && this.runningThreadIds.has(threadId));
      const signature = JSON.stringify([cwd, threadId, running, hasRunningTurn, executorRunning]);
      if (signature === repairSignature) return;
      repairSignature = signature;
      const version = ++repairVersion;
      if (repairTimer !== undefined) window.clearTimeout(repairTimer);
      repairTimer = undefined;
      if (!thread || !threadId || !cwd || (!running && !executorRunning) || (hasRunningTurn && executorRunning)) return;
      const key = `${cwd}\0${threadId}`;
      repairTimer = window.setTimeout(() => {
        repairTimer = undefined;
        if (repairInFlight === key) return;
        repairInFlight = key;
        void resumeThreadForRefresh(thread).then(thread => {
          if (!active || version !== repairVersion || !thread) return;
          this.host.updateState(current => current.activeContext?.cwd === cwd && activeThreadIDForState(current) === threadId
            ? reconcileListedThreadState(current, upsertThread(current.threads, thread)) : current);
        }).catch(() => {
          // The focus/visibility heartbeat retries transient owner-resume failures.
        }).finally(() => { if (repairInFlight === key) repairInFlight = ""; });
      }, 200);
    };
    off.push(this.host.subscribeState(reconcileForeground));
    reconcileForeground();
    let lastRefreshAt = 0;
    const refreshThreads = async () => {
      const captured = this.host.getState();
      const now = Date.now();
      if (!captured.initialized || now - lastRefreshAt < 10_000) return;
      lastRefreshAt = now;
      try {
        const rows = await loadThreadListRefresh(captured);
        if (!active || this.host.getState().activeContext !== captured.activeContext) return;
        this.host.sidebarCache.cacheSidebarThreads(rows);
        this.host.updateState(current => current.initialized && current.activeContext === captured.activeContext
          ? reconcileListedThreadState(current, rows) : current);
      } catch {
        // Another focus event or heartbeat retries shared-store pickup.
      }
    };
    const onFocus = () => { void refreshThreads(); };
    const onVisibility = () => { if (document.visibilityState === "visible") void refreshThreads(); };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibility);
    const heartbeat = window.setInterval(onVisibility, 60_000);
    off.push(() => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
      window.clearInterval(heartbeat);
      if (repairTimer !== undefined) window.clearTimeout(repairTimer);
    });
    const applyRunning = (items: { thread_id: string }[]) => {
      if (!active) return;
      this.runningThreadIds = new Set(items.map(row => row.thread_id));
      reconcileForeground();
      changed();
      const captured = this.host.getState();
      if (this.threads().some(thread => isThreadRunning(thread) && !this.runningThreadIds.has(thread.id))) {
        void loadThreadListRefresh(captured).then(rows => {
          if (!active) return;
          this.host.sidebarCache.cacheSidebarThreads(rows);
          this.host.updateState(state => state.activeContext === captured.activeContext ? reconcileListedThreadState(state, rows) : state);
        }).catch(() => {});
      }
    };
    if (window.wuu.onRunningThreadsChanged) off.push(window.wuu.onRunningThreadsChanged(items => { runningEvent = true; applyRunning(items); }));
    void window.wuu.getRunningThreadsSnapshot?.().then(items => { if (!runningEvent) applyRunning(items); }).catch(() => {});

    if (window.wuu.onDesktopUpdateChanged) off.push(window.wuu.onDesktopUpdateChanged(value => { updateEvent = true; this.update = value; changed(); }));
    void window.wuu.getDesktopUpdateState?.().then(value => { if (active && !updateEvent) { this.update = value; changed(); } }).catch(() => {});
    return () => { active = false; off.reverse().forEach(stop => stop()); };
  }
  private assignFolder(threadId: string, folderId?: string): void {
    const patch = (thread: Thread) => ({ ...thread, folder_id: folderId || undefined });
    this.host.sidebarCache.patchCachedSidebarThread(threadId, patch);
    this.host.updateState(state => updateThreadByID(state, threadId, patch));
  }
  private revealBrowser(tabId: string | undefined): void {
    // Publish the browser destination before notifying plugin-view subscribers.
    this.activeWebsiteId = tabId;
    this.accountOpen = false;
    this.host.getWorkbench().deactivateRegion("primary");
  }
  async invoke<A extends keyof Actions>(action: A, input: Actions[A]["input"], signal?: AbortSignal): Promise<Actions[A]["result"]> {
    signal?.throwIfAborted();
    let result: unknown;
    switch (action) {
      case "navigation.refresh": {
        const requested = this.host.sidebarCache.getSnapshot();
        const folderRevision = this.folderRevision;
        const [threads, folders] = await Promise.allSettled([
          Promise.resolve().then(() => window.wuu.listAllThreads()),
          Promise.resolve().then(() => window.wuu.getSessionOrganization()),
        ]);
        signal?.throwIfAborted();
        // Organization and thread listings are independent; preserve each
        // successful result while still surfacing a failed refresh to callers.
        if (threads.status === "fulfilled") this.host.sidebarCache.cacheSidebarThreads(threads.value.threads, { projects: requested.workspaceThreadsByWorkspaceID, scratch: requested.cachedScratchThreads });
        if (folders.status === "fulfilled" && folderRevision === this.folderRevision) this.folders = parseSessionOrganization(folders.value.organization).folders;
        if (threads.status === "rejected") { this.host.changed(); throw threads.reason; }
        if (folders.status === "rejected") { this.host.changed(); throw folders.reason; }
        break;
      }
      case "navigation.load-workspace": {
        const { workspaceId } = input as Actions["navigation.load-workspace"]["input"];
        const project = this.host.getState().projects.find(row => row.id === workspaceId);
        if (!project) throw new Error(`Unknown workspace: ${workspaceId}`);
        await this.host.sidebarCache.loadWorkspaceThreads(project); break;
      }
      case "navigation.mark-viewed": {
        const { threadIds } = input as Actions["navigation.mark-viewed"]["input"];
        const rows = threadIds.map(id => this.thread(id));
        this.host.updateState(state => markThreadSummariesViewed(state, rows));
        writeThreadReadState(this.host.getState().lastViewedTurnByThreadID); break;
      }
      case "navigation.pin-thread": {
        const { threadId, pinned } = input as Actions["navigation.pin-thread"]["input"];
        const thread = this.thread(threadId);
        if (Boolean(thread.pinned) !== pinned) await this.host.mutationActions().toggleThreadPinned(thread); break;
      }
      case "navigation.archive-thread": {
        const { threadId, force } = input as Actions["navigation.archive-thread"]["input"];
        result = await this.host.mutationActions().archiveThread(this.thread(threadId), { force }); break;
      }
      case "navigation.delete-thread": await this.host.mutationActions().deleteThread(this.thread((input as Actions["navigation.delete-thread"]["input"]).threadId)); break;
      case "navigation.adopt-thread": {
        const { threadId, projectId } = input as Actions["navigation.adopt-thread"]["input"];
        const response = await window.wuu.projectSession!({ action: "adopt", project_id: projectId, session_id: threadId });
        this.host.sidebarCache.updateCachedSidebarThread(response.thread); break;
      }
      case "navigation.create-folder": {
        const { name, threadId } = input as Actions["navigation.create-folder"]["input"];
        const { group } = await window.wuu.createSessionFolder(name);
        this.folderRevision++;
        this.folders = [...this.folders, { id: group.id, name: group.name }];
        this.host.changed();
        if (threadId) { await window.wuu.updateThreadOrganization(threadId, group.id); this.assignFolder(threadId, group.id); } break;
      }
      case "navigation.rename-folder": {
        const { folderId, name } = input as Actions["navigation.rename-folder"]["input"];
        const { group } = await window.wuu.renameSessionFolder(folderId, name);
        this.folderRevision++;
        this.folders = this.folders.map(row => row.id === folderId ? { id: group.id, name: group.name } : row); break;
      }
      case "navigation.delete-folder": {
        const { folderId } = input as Actions["navigation.delete-folder"]["input"];
        await window.wuu.deleteSessionFolder(folderId);
        this.folderRevision++;
        this.folders = this.folders.filter(row => row.id !== folderId);
        for (const thread of this.threads()) if (thread.folder_id === folderId) this.assignFolder(thread.id); break;
      }
      case "navigation.reorder-folders": {
        const { folderIds } = input as Actions["navigation.reorder-folders"]["input"];
        const request = this.folderQueue.catch(() => {}).then(async () => {
          const { organization } = await window.wuu.reorderSessionFolders([...folderIds]);
          this.folderRevision++;
          this.folders = parseSessionOrganization(organization).folders; this.host.changed();
        });
        this.folderQueue = request; await request; break;
      }
      case "navigation.move-to-folder": {
        const { threadId, folderId } = input as Actions["navigation.move-to-folder"]["input"];
        await window.wuu.updateThreadOrganization(threadId, folderId ?? ""); this.assignFolder(threadId, folderId); break;
      }
      case "navigation.close-plugin": {
        const { viewId } = input as Actions["navigation.close-plugin"]["input"];
        const active = this.getSnapshot().activePluginView;
        if (!active || active.id !== viewId || !active.closable) throw new Error("Plugin view is no longer closable");
        await this.host.getWorkbench().closeView(viewId); break;
      }
      case "navigation.reveal-region": {
        const { region } = input as Actions["navigation.reveal-region"]["input"];
        this.regionVisibilityRequest = { id: (this.regionVisibilityRequest?.id ?? 0) + 1, region };
        if (region === "primary") { this.activeWebsiteId = undefined; this.accountOpen = false; }
        break;
      }
      case "navigation.open-plugin": {
        const { pluginId, viewTypeId, instanceId } = input as Actions["navigation.open-plugin"]["input"];
        this.activeWebsiteId = undefined;
        if (instanceId) this.host.getWorkbench().activateView(instanceId);
        else await this.host.getWorkbench().openPluginView(pluginId, viewTypeId, { region: "primary", persistence: "durable", reveal: true }); break;
      }
      case "navigation.show-native": this.host.getWorkbench().deactivateRegion("primary"); this.activeWebsiteId = undefined; this.accountOpen = false; break;
      case "navigation.show-search": this.searchOpen = (input as Actions["navigation.show-search"]["input"]).open; break;
      case "navigation.show-account": {
        const { open } = input as Actions["navigation.show-account"]["input"];
        if (open && window.wuu.openAccountWindow) await window.wuu.openAccountWindow();
        else this.accountOpen = open;
        break;
      }
      case "navigation.focus-composer": {
        const { tabId } = input as Actions["navigation.focus-composer"]["input"];
        if (this.host.getState().activeSessionTabID === tabId) this.composerFocusRequest = { id: (this.composerFocusRequest?.id ?? 0) + 1, tabId };
        break;
      }
      case "navigation.focus-workspace": this.focusedWorkspaceId = (input as Actions["navigation.focus-workspace"]["input"]).workspaceId; break;
      case "navigation.reveal-thread": await window.wuu.revealSession((input as Actions["navigation.reveal-thread"]["input"]).threadId); break;
      case "navigation.open-external": await window.wuu.openExternal((input as Actions["navigation.open-external"]["input"]).url); break;
      // Reads must not publish: consumers request these values while rendering
      // navigation, so a notification would trigger another identical read.
      case "navigation.load-icon": {
        const value = input as Actions["navigation.load-icon"]["input"];
        return await loadPluginIconURL(value.pluginId, value.fingerprint, value.path) as Actions[A]["result"];
      }
      case "navigation.account-status": {
        const account = await window.wuu.remoteAccount!("status");
        return { username: account.username, display_name: account.display_name, server: account.server } as Actions[A]["result"];
      }
      case "navigation.account-logout": { const account = await window.wuu.remoteAccount!("logout"); result = { localLogoutOnly: (account as { localLogoutOnly?: boolean }).localLogoutOnly }; window.dispatchEvent(new Event("wuu:account-changed")); break; }
      case "navigation.install-update": if (this.update.kind === "ready") await window.wuu.restartDesktopUpdate!(); else await window.wuu.downloadDesktopUpdate!(); break;
      case "navigation.reopen-browser-tab": this.revealBrowser((await this.browser.reopenTab()).activeTabId); break;
      case "navigation.reveal-browser": this.revealBrowser(this.browser.getSnapshot()?.activeTabId); break;
      case "navigation.create-browser-tab": this.revealBrowser((await this.browser.createTab()).activeTabId); break;
      case "navigation.select-browser-tab": { const { tabId } = input as Actions["navigation.select-browser-tab"]["input"]; await this.browser.selectTab(tabId); this.revealBrowser(tabId); break; }
      case "navigation.close-browser-tab": { const { tabId } = input as Actions["navigation.close-browser-tab"]["input"]; const next = await this.browser.closeTab(tabId); if (this.activeWebsiteId === tabId) this.activeWebsiteId = next.activeTabId; break; }
      case "navigation.pin-browser-tab": { const { tabId, pinned } = input as Actions["navigation.pin-browser-tab"]["input"]; await this.browser.pinTab(tabId, pinned); break; }
      case "navigation.mute-browser-tab": { const { tabId, muted } = input as Actions["navigation.mute-browser-tab"]["input"]; await this.browser.muteTab(tabId, muted); break; }
      case "navigation.reorder-browser-tabs": await this.browser.reorderTabs([...(input as Actions["navigation.reorder-browser-tabs"]["input"]).tabIds]); break;
      default: throw new Error(`Unsupported navigation command: ${action}`);
    }
    this.host.changed();
    return result as Actions[A]["result"];
  }
}
