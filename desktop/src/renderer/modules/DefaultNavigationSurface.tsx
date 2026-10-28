import { ArchiveTip } from "../ArchiveTip";
import { UILayerPortal } from "../ui/layers/UILayerHost";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type PointerEvent } from "react";
import type { ApplicationPresentationAPI, ApplicationServiceV1, ApplicationSnapshotV1, UICompositionV1, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationNavigationViewV1 } from "../../../../packages/plugin-sdk/src/ui-navigation";
import { AppSidebar, reconcileSidebarSectionOrder, type SidebarNavigationService } from "../AppSidebar";
import { SCRATCH_PSEUDO_PROJECT_ID, isScratchThread, threadBelongsToWorkspace, type ThreadSummary } from "../AppState";
import type { DesktopProject } from "../../shared/protocol";
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
  const snapshot = useSyncExternalStore(application.subscribe, application.getSnapshot);
  // The controller owns this SDK extension; no private state or reducer is accepted.
  const view = (snapshot as ApplicationSnapshotV1 & { navigationWorkspace: ApplicationNavigationViewV1 }).navigationWorkspace;
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
  const focusAfterNavigation = async (navigate: () => Promise<string | undefined>) => {
    const origin = document.activeElement;
    const version = interactionVersion.current;
    const tabId = await presentNavigation(navigate);
    // Keep the initiating gesture's intent across the async workspace switch.
    if (tabId && version === interactionVersion.current
      && (document.activeElement === origin || document.activeElement === document.body)) {
      await invoke("navigation.focus-composer", { tabId });
    }
  };
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
  const [expanded, setExpanded] = useStoredIDs("wuu.desktop.expandedSidebarSectionIDs", collapsed.has(SCRATCH_PSEUDO_PROJECT_ID) ? [] : [SCRATCH_PSEUDO_PROJECT_ID]);
  const [collapsedFolders, setCollapsedFolders] = useState(new Set<string>());
  const [order, setOrder] = useState(() => [...readIDs("wuu.desktop.sidebarSectionOrder")]);
  const rows = useMemo(() => structuredClone(view.threads) as ThreadSummary[], [view.threads]);
  const workspaces = useMemo<DesktopProject[]>(() => [{ id: SCRATCH_PSEUDO_PROJECT_ID, name: t("sidebar.conversations"), path: "", created_at: "", updated_at: "" }, ...view.workspaces.map(row => ({ ...row }))], [view.workspaces, t]);
  const sectionOrder = useMemo(() => reconcileSidebarSectionOrder(order, workspaces.map(row => row.id)), [order, workspaces]);
  const workspaceRows = useMemo(() => Object.fromEntries(workspaces.map(workspace => [workspace.id, rows.filter(row => !row.archived && (workspace.id === SCRATCH_PSEUDO_PROJECT_ID ? isScratchThread(row, [...view.workspaces]) : threadBelongsToWorkspace(row, workspace)))])), [rows, workspaces, view.workspaces]);
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
    if (opening && id !== SCRATCH_PSEUDO_PROJECT_ID) run(invoke("navigation.load-workspace", { workspaceId: id }));
  };
  const createProject = async (workspaceId?: string) => {
    const targetId = workspaceId ?? (view.activeContextKind === "project" ? view.selectedWorkspaceId : undefined) ?? view.workspaces.find(row => !row.missing)?.id;
    const workspace = view.workspaces.find(row => row.id === targetId);
    if (!workspace || workspace.missing) throw new Error(t("projects.needsWorkspace"));
    const result = await application.invoke("workspace.start-draft", { workspaceId: workspace.id });
    if (result.activated) {
      await application.invoke("draft.set-project-mode", { project: true });
      return result.tabId;
    }
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
  return <>{archiveTip && <UILayerPortal layer="notice"><ArchiveTip key={archiveTip.id} threadTitle={archiveTip.thread.title?.trim() || t("app.thisConversation")} errorMessage={archiveTip.error} pending={archiveTip.pending}
    onDismiss={() => setArchiveTip(undefined)} onViewArchive={() => { setArchiveTip(undefined); run(application.invoke("settings.open", { pageId: "archive" })); }}
    onForceArchive={archiveTip.forceRetryable ? () => run(archiveThread(archiveTip.thread, true)) : undefined} /></UILayerPortal>}<AppSidebar {...layout} navigation={navigation} initialized={view.initialized}
    activeContextKind={view.activeContextKind} activeSessionTabID={snapshot.tabs.activeTabId} selectedWorkspaceID={view.selectedWorkspaceId}
    lastViewedTurnByThreadID={view.lastViewedTurnByThreadId}
    sidebarWorkspaces={workspaces} activeWorkspaceID={workspaceSelectionEnabled ? view.focusedWorkspaceId ?? view.selectedWorkspaceId : undefined}
    pendingConversations={view.pendingConversations.map(row => ({ ...row, context: { ...row.context }, title: row.title || t("tabs.newConversation") }))}
    draftConversations={snapshot.tabs.items.filter(tab => tab.kind === "draft"
      && (tab.id === snapshot.tabs.activeTabId || view.pendingConversations.some(row => row.id === tab.id))).map(tab => {
      const pending = view.pendingConversations.find(row => row.id === tab.id);
      // Draft input is not a session title; wait for the created thread's title.
      return { id: tab.id, title: t("tabs.newConversation"), pending: Boolean(pending) };
    })}
    onSelectPendingConversation={tabId => run(presentNavigation(() => application.invoke("tab.select", { tabId })))}
    pinnedThreads={rows.filter(row => row.pinned && !row.archived)} activeThreadID={snapshot.conversation.threadId}
    pendingThreadID={snapshot.tabs.pending?.kind === "thread" ? snapshot.tabs.pending.targetId : undefined}
    pendingWorkspaceID={snapshot.tabs.pending?.kind === "workspace" ? snapshot.tabs.pending.targetId : undefined}
    collapsedSidebarSectionIDs={collapsed} expandedSidebarSectionIDs={expanded} collapsedFolderIDs={collapsedFolders} setCollapsedFolderIDs={setCollapsedFolders}
    loadingWorkspaceThreadIDs={new Set(view.loadingWorkspaceIds)} workspaceThreadsByWorkspaceID={workspaceRows}
    workspaceMenuOpen={workspaceMenuOpen} workspaceMenuRef={workspaceMenuRef} searchOpen={view.searchOpen} sectionOrder={sectionOrder}
    onStartNewThread={() => run(focusAfterNavigation(async () => (await application.invoke("draft.start", {})).tabId))}
    onOpenSkillsTab={() => run(application.invoke("navigation.show-section", { section: "catalog" }))}
    onToggleConversationSearch={() => run(invoke("navigation.show-search", { open: !view.searchOpen }))}
    onSelectThread={threadId => run(presentNavigation(() => application.invoke("thread.activate", { threadId })))}
    onSelectWorkspaceThread={(workspaceId, threadId) => run(presentNavigation(() => application.invoke("thread.activate", { threadId, workspaceId: workspaceId === SCRATCH_PSEUDO_PROJECT_ID ? undefined : workspaceId })))}
    onTogglePinned={thread => run(invoke("navigation.pin-thread", { threadId: thread.id, pinned: !thread.pinned }))}
    onArchiveThread={thread => run(archiveThread(thread))}
    onDeleteThread={thread => run(invoke("navigation.delete-thread", { threadId: thread.id }))}
    onRenameThread={(thread, title) => run(application.invoke("thread.rename", { threadId: thread.id, title }))}
    onToggleWorkspaceMenu={() => setWorkspaceMenuOpen(value => !value)}
    onCreateWorkspace={() => { setWorkspaceMenuOpen(false); run(application.invoke("workspace.create", { kind: "blank" })); }}
    onOpenWorkspaceFolder={() => { setWorkspaceMenuOpen(false); run(application.invoke("workspace.create", { kind: "folder" })); }}
    onToggleSidebarSectionCollapsed={toggleSection} onLoadWorkspaceThreads={workspace => invoke("navigation.load-workspace", { workspaceId: workspace.id })}
    onFocusWorkspace={workspaceSelectionEnabled ? workspaceId => run(presentNavigation(() => invoke("navigation.focus-workspace", { workspaceId }))) : undefined}
    onStartNewThreadInWorkspace={workspaceId => run(focusAfterNavigation(async () => {
      const result = await application.invoke("workspace.start-draft", { workspaceId: workspaceId === SCRATCH_PSEUDO_PROJECT_ID ? undefined : workspaceId });
      return result.activated ? result.tabId : undefined;
    }))}
    onRemoveWorkspace={workspaceId => run((async () => { if (await confirmAction({ title: t("workspace.removeTitle", { name: view.workspaces.find(row => row.id === workspaceId)?.name ?? workspaceId }), message: t("workspace.removeConfirm"), confirmLabel: t("common.remove") })) await application.invoke("workspace.remove", { workspaceId }); })())}
    onRelocateWorkspace={workspaceId => run(application.invoke("workspace.relocate", { workspaceId }))}
    onCreateProject={view.projectAgentEnabled ? workspaceId => run(focusAfterNavigation(() => createProject(workspaceId))) : undefined}
    onAdoptIntoProject={view.projectAgentEnabled ? (projectId, threadId) => run(invoke("navigation.adopt-thread", { projectId, threadId })) : undefined}
    onReorderSections={next => { setOrder(next); localStorage.setItem("wuu.desktop.sidebarSectionOrder", JSON.stringify(next)); }}
    onOpenSettings={pageId => run(application.invoke("settings.open", { pageId }))}
    onOpenAccount={() => run(invoke("navigation.show-account", { open: true }))}
    onMarkThreadsViewed={threads => run(invoke("navigation.mark-viewed", { threadIds: threads.map(row => row.id) }))}
    unreadViewOpen={unreadViewOpen} onToggleUnreadView={() => setUnreadViewOpen(value => !value)} attentionStickyIDs={attentionStickyIDs} onAttentionStickyIDsChange={setAttentionStickyIDs}
    onShowConversations={() => run(application.invoke("navigation.show-section", { section: "conversations" }))}
    catalogActive={snapshot.navigation.section === "catalog"} catalogSection={snapshot.navigation.catalogSection} settingsActive={snapshot.settings.open}
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
