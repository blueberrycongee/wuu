import { ArchiveTip } from "../ArchiveTip";
import { UILayerPortal } from "../ui/layers/UILayerHost";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type PointerEvent } from "react";
import type { ApplicationPresentationAPI, ApplicationServiceV1, ApplicationSnapshotV1, UICompositionV1, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationNavigationViewV1 } from "../../../../packages/plugin-sdk/src/ui-navigation";
import { AppSidebar, reconcileSidebarSectionOrder, type SidebarNavigationService } from "../AppSidebar";
import { threadBelongsToWorkspace, type ThreadSummary } from "../AppState";
import { threadDisplayTitle } from "../ThreadTitles";
import type { DesktopProject } from "../../shared/protocol";
import { isPluginLifecycleCancellation, PluginActionNotStartedError } from "../../shared/PluginLifecycle";
import { usePresentedNavigation, reportPresentedNavigationError } from "../ConversationPresentationTransition";
import { showErrorToast } from "../Toast";
import { showApplicationActionError } from "../ApplicationActionError";
import { confirmAction } from "../ConfirmDialog";
import { useI18n } from "../i18n";

/** Only geometry and DOM pointer feedback belong to the containing shell. */
export interface DefaultNavigationLayout {
  workspaceSelectionEnabled?: boolean;
  sidebarVisible?: boolean;
  sidebarCollapsed?: boolean;
  onPointerEnter?: () => void;
  onPointerLeave?: (event: PointerEvent<HTMLElement>) => void;
  onRibbonPresenceChange?: (present: boolean) => void;
}
function readIDs(key: string, fallback: string[] = []): Set<string> {
  try { const stored = localStorage.getItem(key); const value: unknown = stored ? JSON.parse(stored) : fallback; return new Set(Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : fallback); }
  catch { return new Set(fallback); }
}
function useStoredIDs(key: string, fallback: string[] = []) {
  const [ids, setIDs] = useState(() => readIDs(key, fallback));
  useEffect(() => { localStorage.setItem(key, JSON.stringify([...ids])); }, [key, ids]);
  return [ids, setIDs] as const;
}

export function DefaultNavigationSurface({ application, slots, workspaceSelectionEnabled = false, ...layout }: DefaultNavigationLayout & { application: ApplicationServiceV1; slots: UICompositionV1 }): JSX.Element {
  const presentNavigation = usePresentedNavigation();
  // Field subscriptions keep the sidebar off stream-frame publications; none of
  // these fields carry projected stream text.
  const tabs = useSyncExternalStore(application.subscribe, () => application.getSnapshot().tabs);
  const phase = useSyncExternalStore(application.subscribe, () => application.getSnapshot().phase);
  const workspacesSnapshot = useSyncExternalStore(application.subscribe, () => application.getSnapshot().workspaces);
  const view = useSyncExternalStore(application.subscribe,
    () => (application.getSnapshot() as ApplicationSnapshotV1 & { navigationWorkspace: ApplicationNavigationViewV1 }).navigationWorkspace);
  const conversationThreadId = useSyncExternalStore(application.subscribe, () => application.getSnapshot().conversation.threadId);
  const navigationSnapshot = useSyncExternalStore(application.subscribe, () => application.getSnapshot().navigation);
  const settingsOpen = useSyncExternalStore(application.subscribe, () => application.getSnapshot().settings.open);
  // Compare the complete tab inventory, not just visible drafts: selecting an
  // existing draft must not look like creation. Initial/restored tabs stay still.
  const previousTabs = useRef({ application, items: tabs.items, phase });
  const previousTabIDs = new Set(previousTabs.current.items.map(tab => tab.id));
  const canAnimateNewTabs = previousTabs.current.application === application
    && previousTabs.current.phase === "ready" && phase === "ready";
  useLayoutEffect(() => {
    previousTabs.current = { application, items: tabs.items, phase };
  }, [application, tabs.items, phase]);
  const invoke = useMemo(() => application.invoke.bind(application) as SidebarNavigationService["invoke"], [application]);
  const { t } = useI18n();
  const run = useCallback((operation: Promise<unknown>) => { void operation.catch(reportPresentedNavigationError); }, []);
  const interactionVersion = useRef(0);
  useEffect(() => {
    const changed = () => { interactionVersion.current++; };
    document.addEventListener("pointerdown", changed, true);
    document.addEventListener("keydown", changed, true);
    return () => {
      changed();
      document.removeEventListener("pointerdown", changed, true);
      document.removeEventListener("keydown", changed, true);
    };
  }, []);
  const focusAfterNavigation = async (navigate: () => Promise<string | undefined>, gesture = { origin: document.activeElement, version: interactionVersion.current }) => {
    const tabId = await presentNavigation(navigate);
    // Keep the initiating gesture's intent across the async workspace switch.
    if (tabId && gesture.version === interactionVersion.current
      && (document.activeElement === gesture.origin || document.activeElement === document.body)) {
      await invoke("navigation.focus-composer", { tabId });
    }
  };
  type DraftTarget = { workspaceId: string };
  type DraftIntent = { application: ApplicationServiceV1; context: string; target: DraftTarget; targetContext: string; gesture: { origin: Element | null; version: number } };
  const [deferredDraft, setDeferredDraft] = useState<DraftIntent>();
  const draftContext = JSON.stringify([view.activeContextKind, workspacesSnapshot.activeWorkspaceId, workspacesSnapshot.activeRoot]);
  const targetContext = (target: DraftTarget): string => {
    const workspace = workspacesSnapshot.workspaces.find(row => row.id === target.workspaceId);
    return JSON.stringify([target.workspaceId, workspace?.root, workspace?.available]);
  };
  const startDraft = (target: DraftTarget, gesture = { origin: document.activeElement, version: interactionVersion.current }): void => {
    const intent = { application, context: draftContext, target, targetContext: targetContext(target), gesture };
    void focusAfterNavigation(async () => {
      try {
        const result = await application.invoke("workspace.start-draft", { workspaceId: target.workspaceId });
        return result.activated ? result.tabId : undefined;
      } catch (error) {
        // Retry only when the draft request itself never reached a writer.
        if (error instanceof PluginActionNotStartedError) { setDeferredDraft(intent); return undefined; }
        throw error;
      }
    }, gesture).catch(reportPresentedNavigationError);
  };
  useEffect(() => {
    if (!deferredDraft || deferredDraft.application === application) return;
    setDeferredDraft(undefined);
    if (deferredDraft.context !== draftContext || deferredDraft.targetContext !== targetContext(deferredDraft.target)
      || deferredDraft.gesture.version !== interactionVersion.current) return;
    startDraft(deferredDraft.target, deferredDraft.gesture);
  }, [application, deferredDraft, draftContext]);
  type ThreadTarget = { threadId: string; workspaceId?: string };
  type ThreadIntent = { application: ApplicationServiceV1; target: ThreadTarget; targetContext?: string; version: number };
  const [deferredThread, setDeferredThread] = useState<ThreadIntent>();
  const threadTargetContext = (target: ThreadTarget): string | undefined => target.workspaceId
    ? targetContext({ workspaceId: target.workspaceId }) : undefined;
  const selectThread = (target: ThreadTarget, version = interactionVersion.current): void => {
    const intent = { application, target, targetContext: threadTargetContext(target), version };
    run(presentNavigation(async () => {
      if (version !== interactionVersion.current) return;
      try {
        await application.invoke("navigation.show-native", {});
      } catch (error) {
        // No thread action has started; preserve the click across a provider handoff.
        if (isPluginLifecycleCancellation(error)) { setDeferredThread(intent); return; }
        throw error;
      }
      if (version !== interactionVersion.current) return;
      try {
        await application.invoke("thread.activate", target);
      } catch (error) {
        // The thread action never reached its writer, so a successor may carry it.
        if (error instanceof PluginActionNotStartedError) { setDeferredThread(intent); return; }
        throw error;
      }
    }));
  };
  useEffect(() => {
    if (!deferredThread || deferredThread.application === application) return;
    setDeferredThread(undefined);
    if (deferredThread.version !== interactionVersion.current
      || deferredThread.targetContext !== threadTargetContext(deferredThread.target)) return;
    selectThread(deferredThread.target, deferredThread.version);
  }, [application, deferredThread]);
  const archiveTipID = useRef(0);
  const [archiveTip, setArchiveTip] = useState<{ id: number; thread: ThreadSummary; error?: string; forceRetryable?: boolean; pending?: boolean }>();
  const archiveRequest = useRef(0);
  const pendingArchiveThreads = useRef(new Map<string, number>());
  useEffect(() => {
    setArchiveTip(undefined);
    pendingArchiveThreads.current.clear();
    return () => { archiveRequest.current++; };
  }, [application]);
  const [workspaceMenuOpen, setWorkspaceMenuOpen] = useState(false);
  const workspaceMenuRef = useRef<HTMLDivElement>(null);
  const [unreadViewOpen, setUnreadViewOpen] = useState(false);
  const [attentionStickyIDs, setAttentionStickyIDs] = useState(new Set<string>());
  const [collapsed, setCollapsed] = useStoredIDs("wuu.desktop.collapsedSidebarSectionIDs");
  const [expanded, setExpanded] = useStoredIDs("wuu.desktop.expandedSidebarSectionIDs", []);
  const [collapsedFolders, setCollapsedFolders] = useState(new Set<string>());
  const [order, setOrder] = useState(() => [...readIDs("wuu.desktop.sidebarSectionOrder")]);
  const rows = useMemo(() => structuredClone(view.threads) as ThreadSummary[], [view.threads]);
  const workspaces = useMemo<DesktopProject[]>(() => view.workspaces.map(row => ({ ...row })), [view.workspaces]);
  const sectionOrder = useMemo(() => reconcileSidebarSectionOrder(order, workspaces.map(row => row.id)), [order, workspaces]);
  const workspaceRows = useMemo(() => Object.fromEntries(workspaces.map(workspace => [workspace.id, rows.filter(row => !row.archived && threadBelongsToWorkspace(row, workspace))])), [rows, workspaces]);
  const defaultDraftWorkspaceID = view.selectedWorkspaceId && view.workspaces.some(row => row.id === view.selectedWorkspaceId && !row.missing)
    ? view.selectedWorkspaceId
    : view.workspaces.find(row => !row.missing)?.id;
  useEffect(() => { if (!view.initialized) return; const lifetime = new AbortController(); void invoke("navigation.refresh", {}, { signal: lifetime.signal }).catch(error => { if (!lifetime.signal.aborted) showApplicationActionError(error); }); return () => lifetime.abort(); }, [invoke, view.initialized]);
  const navigation = useMemo<SidebarNavigationService>(() => ({ view, invoke, slots, organization: {
    folders: view.folders.map(folder => ({ ...folder })),
    folderByThreadID: Object.fromEntries(rows.filter(row => row.folder_id).map(row => [row.id, row.folder_id!])),
    createFolder: async (name, threadID) => { try { await invoke("navigation.create-folder", { name, threadId: threadID }); } catch (error) { showErrorToast(error); throw error; } },
    renameFolder: async (id, name) => { try { await invoke("navigation.rename-folder", { folderId: id, name }); } catch (error) { showErrorToast(error); throw error; } },
    deleteFolder: id => invoke("navigation.delete-folder", { folderId: id }).catch(error => { showErrorToast(error); }),
    reorderFolders: folderIds => invoke("navigation.reorder-folders", { folderIds }).catch(error => { showErrorToast(error); }),
    moveThreadToFolder: (threadId, folderId) => invoke("navigation.move-to-folder", { threadId, folderId }).catch(error => { showErrorToast(error); }),
  } }), [view, invoke, rows, slots]);
  const toggleSection = (id: string) => {
    const opening = !expanded.has(id);
    setExpanded(current => { const next = new Set(current); if (opening) next.add(id); else next.delete(id); return next; });
    setCollapsed(current => { const next = new Set(current); if (opening) next.delete(id); else next.add(id); return next; });
    if (opening) run(invoke("navigation.load-workspace", { workspaceId: id }));
  };
  const archiveThread = async (thread: ThreadSummary, force = false) => {
    // The explicit confirmation admits one mutation, even before React has
    // painted the disabled button. A new provider never inherits this consent.
    if (pendingArchiveThreads.current.has(thread.id)) return;
    const request = ++archiveRequest.current;
    pendingArchiveThreads.current.set(thread.id, request);
    if (force) setArchiveTip(current => current?.thread.id === thread.id ? { ...current, pending: true } : current);
    try {
      const result = await invoke("navigation.archive-thread", { threadId: thread.id, force });
      if (request === archiveRequest.current) setArchiveTip({ id: ++archiveTipID.current, thread, ...(!result.ok ? { error: result.error, forceRetryable: !force && result.forceRetryable } : {}) });
    } catch (error) {
      // Only the provider's typed rejection can offer destructive confirmation.
      // A stale status must not turn retired scopes/storage errors into retries.
      if (request === archiveRequest.current) setArchiveTip({ id: ++archiveTipID.current, thread, error: error instanceof Error ? error.message : String(error) });
    } finally { if (pendingArchiveThreads.current.get(thread.id) === request) pendingArchiveThreads.current.delete(thread.id); }
  };
  return <>{archiveTip && <UILayerPortal layer="notice"><ArchiveTip key={archiveTip.id} threadTitle={threadDisplayTitle(archiveTip.thread, rows, t("app.thisConversation"))} errorMessage={archiveTip.error} pending={archiveTip.pending}
    onDismiss={() => setArchiveTip(undefined)} onViewArchive={() => { setArchiveTip(undefined); run(application.invoke("settings.open", { pageId: "archive" })); }}
    onForceArchive={archiveTip.forceRetryable ? () => run(archiveThread(archiveTip.thread, true)) : undefined} /></UILayerPortal>}<AppSidebar {...layout} navigation={navigation} initialized={view.initialized}
    activeContextKind={view.activeContextKind} activeSessionTabID={tabs.activeTabId} selectedWorkspaceID={view.selectedWorkspaceId}
    lastViewedTurnByThreadID={view.lastViewedTurnByThreadId}
    sidebarWorkspaces={workspaces} activeWorkspaceID={workspaceSelectionEnabled ? view.focusedWorkspaceId ?? view.selectedWorkspaceId : undefined}
    pendingConversations={view.pendingConversations.map(row => ({ ...row, context: { ...row.context }, title: row.title || t("tabs.newConversation") }))}
    draftConversations={tabs.items.filter(tab => tab.kind === "draft"
      && (tab.id === tabs.activeTabId || view.pendingConversations.some(row => row.id === tab.id))).map(tab => {
      const pending = view.pendingConversations.find(row => row.id === tab.id);
      // Draft input is not a session title; wait for the created thread's title.
      return { id: tab.id, title: t("tabs.newConversation"), pending: Boolean(pending), entering: canAnimateNewTabs && !previousTabIDs.has(tab.id) };
    })}
    onSelectPendingConversation={tabId => run(presentNavigation(() => application.invoke("tab.select", { tabId })))}
    pinnedThreads={rows.filter(row => row.pinned && !row.archived)} activeThreadID={conversationThreadId}
    pendingThreadID={tabs.pending?.kind === "thread" ? tabs.pending.targetId : undefined}
    pendingWorkspaceID={tabs.pending?.kind === "workspace" ? tabs.pending.targetId : undefined}
    collapsedSidebarSectionIDs={collapsed} expandedSidebarSectionIDs={expanded} collapsedFolderIDs={collapsedFolders} setCollapsedFolderIDs={setCollapsedFolders}
    loadingWorkspaceThreadIDs={new Set(view.loadingWorkspaceIds)} workspaceThreadsByWorkspaceID={workspaceRows}
    workspaceMenuOpen={workspaceMenuOpen} workspaceMenuRef={workspaceMenuRef} searchOpen={view.searchOpen} sectionOrder={sectionOrder}
    onStartNewThread={() => { if (defaultDraftWorkspaceID) startDraft({ workspaceId: defaultDraftWorkspaceID }); }}
    onOpenSkillsTab={() => run(application.invoke("navigation.show-section", { section: "catalog" }))}
    onToggleConversationSearch={() => run(invoke("navigation.show-search", { open: !view.searchOpen }))}
    onSelectThread={threadId => selectThread({ threadId })}
    onSelectWorkspaceThread={(workspaceId, threadId) => selectThread({ threadId, workspaceId })}
    onTogglePinned={thread => run(invoke("navigation.pin-thread", { threadId: thread.id, pinned: !thread.pinned }))}
    onArchiveThread={thread => run(archiveThread(thread))}
    onDeleteThread={thread => run(invoke("navigation.delete-thread", { threadId: thread.id }))}
    onRenameThread={(thread, title) => run(application.invoke("thread.rename", { threadId: thread.id, title }))}
    onToggleWorkspaceMenu={() => setWorkspaceMenuOpen(value => !value)}
    onCreateWorkspace={() => { setWorkspaceMenuOpen(false); run(application.invoke("workspace.create", { kind: "blank" })); }}
    onOpenWorkspaceFolder={() => { setWorkspaceMenuOpen(false); run(application.invoke("workspace.create", { kind: "folder" })); }}
    onToggleSidebarSectionCollapsed={toggleSection} onLoadWorkspaceThreads={workspace => invoke("navigation.load-workspace", { workspaceId: workspace.id })}
    onFocusWorkspace={workspaceSelectionEnabled ? workspaceId => run(presentNavigation(() => invoke("navigation.focus-workspace", { workspaceId }))) : undefined}
    onStartNewThreadInWorkspace={workspaceId => startDraft({ workspaceId })}
    onRemoveWorkspace={workspaceId => run((async () => { if (await confirmAction({ title: t("workspace.removeTitle", { name: view.workspaces.find(row => row.id === workspaceId)?.name ?? workspaceId }), message: t("workspace.removeConfirm"), confirmLabel: t("common.remove") })) await application.invoke("workspace.remove", { workspaceId }); })())}
    onRelocateWorkspace={workspaceId => run(application.invoke("workspace.relocate", { workspaceId }))}
    onReorderSections={next => { setOrder(next); localStorage.setItem("wuu.desktop.sidebarSectionOrder", JSON.stringify(next)); }}
    onOpenSettings={pageId => run(application.invoke("settings.open", pageId ? { pageId } : {}))}
    onMarkThreadsViewed={threads => run(invoke("navigation.mark-viewed", { threadIds: threads.map(row => row.id) }))}
    unreadViewOpen={unreadViewOpen} onToggleUnreadView={() => setUnreadViewOpen(value => !value)} attentionStickyIDs={attentionStickyIDs} onAttentionStickyIDsChange={setAttentionStickyIDs}
    onShowConversations={() => run(application.invoke("navigation.show-section", { section: "conversations" }))}
    catalogActive={navigationSnapshot.section === "catalog"} catalogSection={navigationSnapshot.catalogSection} settingsActive={settingsOpen}
    onLeaveSettings={() => run(application.invoke("settings.close", {}))}
    onSelectCatalogSection={section => run(application.invoke("catalog.select-section", { section }))}
    onOpenCatalogPlugin={pluginId => run(application.invoke("catalog.select-section", { section: "plugins" }).then(() => application.invoke("catalog.select-plugin", { pluginId })))}
    websites={view.browser?.tabs.map(tab => ({ ...tab }))} activeWebsiteId={view.activeWebsiteId}
    onNewTab={view.browser?.canCreate ? () => run(invoke("navigation.create-browser-tab", {})) : undefined}
    onSelectWebsite={tab => run(invoke("navigation.select-browser-tab", { tabId: tab.id }))}
    onCloseTab={tabId => run(invoke("navigation.close-browser-tab", { tabId }))}
    onPinTab={(tabId, pinned) => run(invoke("navigation.pin-browser-tab", { tabId, pinned }))}
    onMuteTab={(tabId, muted) => run(invoke("navigation.mute-browser-tab", { tabId, muted }))}
    onReorderTabs={tabIds => run(invoke("navigation.reorder-browser-tabs", { tabIds }))}
  /></>;
}

export function activatePresentation(api: ApplicationPresentationAPI): void {
  api.registerUISlot({ id: "sidebar.primary", cardinality: "many" });
  api.registerUISlot({ id: "sidebar.footer", cardinality: "many" });
  api.registerUIContribution({ id: "ui.navigation", target: "wuu.default-shell/application.navigation",
    render: ({ application, slots, context }: UISlotRenderProps) => <DefaultNavigationSurface application={application} slots={slots} {...context as DefaultNavigationLayout} /> });
}

// The legacy entry remains available to standalone authoring hosts.
export const activate = activatePresentation;
