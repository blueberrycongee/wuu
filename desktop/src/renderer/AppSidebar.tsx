import { hostSupports } from "./HostCapabilities";
import { SidebarAccountMenu } from "./SidebarAccountMenu";
import { MobileSidebar } from "./MobileSidebar";
import {
  Archive,
  Bell,
  ChevronRight,
  Folder,
  FolderMinus,
  FolderOpen,
  FolderPlus,
  MessageSquare,
  MessageSquarePlus,
  MessagesSquare,
  Plus,
  Search,
  Workflow,
} from "./WuuIcons";
import {
  type PointerEvent as ReactPointerEvent,
  type DragEvent as ReactDragEvent,
  type CSSProperties,
  type Dispatch,
  type HTMLAttributes,
  type ReactNode,
  type RefObject,
  type SetStateAction,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  closestCenter,
  DndContext,
  DragOverlay,
  KeyboardSensor,
  useDroppable,
  useSensor,
  useSensors,
  type CollisionDetection,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { restrictToVerticalAxis } from "@dnd-kit/modifiers";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useDropAnimation, useSortableTransition } from "./SortableMotion";
import type { DesktopProject } from "../shared/protocol";
import {
  isScratchThread,
  threadBelongsToWorkspace,
  isThreadExecuting,
  isThreadUnread,
  sidebarThreadSortTime,
  type AppState,
  type ThreadSummary,
} from "./AppState";
import { SCRATCH_PSEUDO_PROJECT_ID } from "./AppState";
import { projectDirectory } from "./ProjectSessions";
import {
  OrganizationThreadList,
  WorkspaceGroup,
  type PendingConversation,
} from "./ThreadSidebar";
import { SidebarCollapseBody, SidebarSection } from "./SidebarSection";
import { SidebarNameDialog } from "./SidebarNameDialog";
import { ThreadContextMenu } from "./ThreadContextMenu";
import {
  SessionOrganizationProvider,
  SESSION_FOLDER_DRAG_MIME,
  useSessionOrganization,
  type SessionGroup,
} from "./SessionOrganization";
import {
  SidebarSectionDragPreview,
  SortableSidebarSection,
  reorderSidebarSections,
  type SidebarSectionHeaderInfo,
} from "./SortableSidebarSection";
export { reorderSidebarSections } from "./SortableSidebarSection";
import { SidebarPointerSensor } from "./SidebarPointerSensor";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import { PluginIcon } from "./PublicIcon";
import { SidebarBrand } from "./SidebarBrand";
import { useI18n } from "./i18n";
import {
  NavigationPresentation,
  type NavigationSourceNode,
} from "./plugins/NavigationPresentation";
import {
  desktopPluginHost,
  desktopWorkbenchController,
} from "./plugins/DesktopPluginRuntime";
import type { PluginHost } from "./plugins/PluginHost";
import type { WorkbenchController } from "./plugins/Workbench";
import { primaryViewNavigation } from "./plugins/PrimaryViewNavigation";
import { PluginSlot } from "./plugins/PluginSlot";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";

/**
 * Stable section identity keys for the new sidebar tree.
 *
 * - `SIDEBAR_SECTION_PINNED` is the legacy persisted collapse key. The visible
 *   pinned functional group now keeps its own persisted order/collapse state.
 * - `SCRATCH_PSEUDO_PROJECT_ID` ("__wuu_scratch__") is the 对话
 *   pseudo-project entry. It belongs to the workspace functional group and
 *   can be reordered with real projects.
 */
export const SIDEBAR_SECTION_PINNED = "__wuu_pinned__";

type AttentionRow = {
  thread: ThreadSummary;
  running: boolean;
  unread: boolean;
  time: number;
};

export type AttentionThreadPartition = {
  running: ThreadSummary[];
  unread: ThreadSummary[];
  recent: ThreadSummary[];
};

/**
 * Splits sidebar sessions into the attention view's three sections: everything
 * that is running, settled sessions with an unread answer, then recently
 * attended sessions that would otherwise drop out after being opened. Sort
 * keys come from `sidebarThreadSortTime` so a running row cannot shuffle while
 * its session streams.
 */
export function partitionAttentionThreads(
  threads: readonly ThreadSummary[],
  activeThreadID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
  stickyThreadIDs: ReadonlySet<string> = new Set(),
): AttentionThreadPartition {
  const rows = threads
    .filter((thread) => !thread.archived)
    .map((thread): AttentionRow => {
      const running = isThreadExecuting(thread);
      const unread = (
        !running &&
        thread.id !== activeThreadID &&
        thread.id !== pendingThreadID &&
        isThreadUnread(thread, lastViewedTurnByThreadID[thread.id])
      );
      return { thread, running, unread, time: sidebarThreadSortTime(thread, running) };
    });
  const byNewest = (left: AttentionRow, right: AttentionRow): number => (
    Number(Boolean(right.thread.pinned)) - Number(Boolean(left.thread.pinned))
    || right.time - left.time
  );
  const running = rows.filter((row) => row.running).sort(byNewest).map((row) => row.thread);
  const unread = rows.filter((row) => row.unread).sort(byNewest).map((row) => row.thread);
  return {
    running,
    unread,
    recent: rows
      .filter((row) => (
        !row.running &&
        !row.unread &&
        stickyThreadIDs.has(row.thread.id)
      ))
      .sort(byNewest)
      .map((row) => row.thread),
  };
}

export function attentionStickyThreadIDs(
  threads: readonly ThreadSummary[],
  activeThreadID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
  current: ReadonlySet<string> = new Set(),
): Set<string> {
  const next = new Set(current);
  if (activeThreadID) next.add(activeThreadID);
  if (pendingThreadID) next.add(pendingThreadID);
  for (const thread of threads) {
    if (thread.archived) continue;
    if (isThreadExecuting(thread) || isThreadUnread(thread, lastViewedTurnByThreadID[thread.id])) {
      next.add(thread.id);
    }
  }
  const liveIDs = new Set(threads.map((thread) => thread.id));
  for (const id of next) {
    if (!liveIDs.has(id)) next.delete(id);
  }
  return next;
}

const FOLDER_REMOVE_DROP_TARGET = "__wuu_remove_from_folder__";
const FOLDER_SORTABLE_PREFIX = "__wuu_folder_sort__:";
const PINNED_THREAD_SORTABLE_PREFIX = "__wuu_pinned_thread_sort__:";
const PINNED_APPEND_DROP_ID = "__wuu_pinned_append_drop__";
const PINNED_SESSION_DROP_TARGET = "__wuu_pinned_session_drop__";
const WORKSPACE_SESSION_DROP_TARGET = "__wuu_workspace_session_drop__";
const SIDEBAR_FUNCTIONAL_GROUP_ORDER_KEY = "wuu.desktop.sidebarFunctionalGroupOrder";
const SIDEBAR_COLLAPSED_FUNCTIONAL_GROUPS_KEY = "wuu.desktop.sidebarCollapsedFunctionalGroups";
const SIDEBAR_PINNED_ITEMS_KEY = "wuu.desktop.sidebarPinnedItems";
const LEGACY_SIDEBAR_PINNED_CONTAINERS_KEY = "wuu.desktop.sidebarPinnedContainers";
const SIDEBAR_FUNCTIONAL_GROUP_IDS = ["projects", "pinned", "folders", "workspace"] as const;
type SidebarFunctionalGroupID = (typeof SIDEBAR_FUNCTIONAL_GROUP_IDS)[number];
type SidebarPinnedItem = {
  kind: "thread" | "folder" | "workspace";
  id: string;
};

function pinnedItemSortableID(item: SidebarPinnedItem): string {
  if (item.kind === "thread") return `${PINNED_THREAD_SORTABLE_PREFIX}${item.id}`;
  if (item.kind === "folder") return `${FOLDER_SORTABLE_PREFIX}${item.id}`;
  return item.id;
}

function moveSidebarItem(
  order: string[],
  activeID: string,
  targetID: string,
  position: "before" | "after",
): string[] {
  if (activeID === targetID || !order.includes(activeID) || !order.includes(targetID)) return order;
  const next = order.filter((id) => id !== activeID);
  const targetIndex = next.indexOf(targetID);
  next.splice(targetIndex + (position === "after" ? 1 : 0), 0, activeID);
  return next.every((id, index) => id === order[index]) ? order : next;
}

function loadSidebarPinnedItems(): SidebarPinnedItem[] {
  try {
    const parsed: unknown = JSON.parse(
      window.localStorage.getItem(SIDEBAR_PINNED_ITEMS_KEY)
        ?? window.localStorage.getItem(LEGACY_SIDEBAR_PINNED_CONTAINERS_KEY)
        ?? "[]",
    );
    if (!Array.isArray(parsed)) return [];
    const seen = new Set<string>();
    return parsed.flatMap((value) => {
      if (!value || typeof value !== "object") return [];
      const candidate = value as Partial<SidebarPinnedItem>;
      if ((candidate.kind !== "thread"
        && candidate.kind !== "folder"
        && candidate.kind !== "workspace")
        || typeof candidate.id !== "string"
        || !candidate.id
      ) return [];
      const key = `${candidate.kind}:${candidate.id}`;
      if (seen.has(key)) return [];
      seen.add(key);
      return [{ kind: candidate.kind, id: candidate.id }];
    });
  } catch {
    return [];
  }
}

function loadSidebarFunctionalGroupOrder(): SidebarFunctionalGroupID[] {
  try {
    const parsed: unknown = JSON.parse(
      window.localStorage.getItem(SIDEBAR_FUNCTIONAL_GROUP_ORDER_KEY) ?? "[]",
    );
    if (!Array.isArray(parsed)) return [...SIDEBAR_FUNCTIONAL_GROUP_IDS];
    // Projects take Collaboration's saved place. Remove this mapping once no
    // saved order predates Collaboration's removal.
    const order = [...new Set(parsed.map((id) => id === "collaboration" ? "projects" : id).filter(
      (id): id is SidebarFunctionalGroupID =>
        typeof id === "string"
        && SIDEBAR_FUNCTIONAL_GROUP_IDS.includes(id as SidebarFunctionalGroupID),
    ))];
    // Keep saved relative positions when adding groups.
    return [...SIDEBAR_FUNCTIONAL_GROUP_IDS.filter((id) => !order.includes(id)), ...order];
  } catch {
    return [...SIDEBAR_FUNCTIONAL_GROUP_IDS];
  }
}

function persistSidebarFunctionalGroupOrder(order: SidebarFunctionalGroupID[]): void {
  try {
    window.localStorage.setItem(SIDEBAR_FUNCTIONAL_GROUP_ORDER_KEY, JSON.stringify(order));
  } catch (reason) {
    console.warn("sidebar functional group order persistence failed", reason);
  }
}

function loadCollapsedSidebarFunctionalGroups(): Set<SidebarFunctionalGroupID> {
  try {
    const parsed: unknown = JSON.parse(
      window.localStorage.getItem(SIDEBAR_COLLAPSED_FUNCTIONAL_GROUPS_KEY) ?? "[]",
    );
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.filter(
      (id): id is SidebarFunctionalGroupID =>
        typeof id === "string"
        && SIDEBAR_FUNCTIONAL_GROUP_IDS.includes(id as SidebarFunctionalGroupID),
    ));
  } catch {
    return new Set();
  }
}

function persistCollapsedSidebarFunctionalGroups(
  collapsedIDs: Set<SidebarFunctionalGroupID>,
): void {
  try {
    window.localStorage.setItem(
      SIDEBAR_COLLAPSED_FUNCTIONAL_GROUPS_KEY,
      JSON.stringify([...collapsedIDs]),
    );
  } catch (reason) {
    console.warn("collapsed sidebar functional groups persistence failed", reason);
  }
}

/**
 * Reconcile the persisted sidebar section order against the current
 * project list. Pure function so it is directly testable.
 *
 * Rules:
 *   1. Drop any stored key that is neither a real project id nor
 *      `SCRATCH_PSEUDO_PROJECT_ID` (including stale fixed or legacy section
 *      ids) once `workspaceIDs` is known. When `workspaceIDs` is empty the real
 *      project list has not been loaded yet, so stored keys are preserved —
 *      pruning them against an empty list would destroy the user's persisted
 *      workspace order on every launch.
 *   2. Append newly-seen project ids in `workspaceIDs` order.
 *   3. Ensure the scratch entry is present while preserving workspace order.
 */
export function reconcileSidebarSectionOrder(
  stored: string[] | undefined,
  workspaceIDs: string[],
): string[] {
  const workspaceIDsKnown = workspaceIDs.length > 0;
  const knownIDs = new Set<string>([
    SCRATCH_PSEUDO_PROJECT_ID,
    ...workspaceIDs,
  ]);
  const out: string[] = [];
  if (Array.isArray(stored)) {
    for (const key of stored) {
      if (typeof key !== "string" || key.length === 0) continue;
      if (key === SIDEBAR_SECTION_PINNED) continue;
      if (workspaceIDsKnown && !knownIDs.has(key)) continue;
      if (out.includes(key)) continue;
      out.push(key);
    }
  }
  for (const id of workspaceIDs) {
    if (!out.includes(id)) {
      out.push(id);
    }
  }
  if (!out.includes(SCRATCH_PSEUDO_PROJECT_ID)) {
    out.unshift(SCRATCH_PSEUDO_PROJECT_ID);
  }
  if (
    stored &&
    stored.length === out.length &&
    stored.every((key, index) => key === out[index])
  ) {
    return stored;
  }
  return out;
}

/**
 * Pure reorder helper: returns the order with `activeId` moved to the
 * position of `overId`. Pure so the drag-end logic is testable without
 * mounting the DOM or stubbing dnd-kit. Behavior contract:
 *   - overId is null / empty / equal to activeId → returns the input
 *     unchanged. dnd-kit occasionally emits "no over" when the pointer
 *     releases outside any droppable; we treat that as a no-op.
 *   - Either id absent from the order → returns the input unchanged.
 *     This preserves the persisted order when something exotic happens
 *     (e.g. the active section was removed from the reconcile result
 *     while the drag was in flight).
 */
// Context that lets SidebarSection — a shared component used by both
// the non-sortable pinned section and the sortable 对话 / 工作区
// sections — pick up the dnd-kit activator listeners when it's inside a
// SortableSection. Default value is null so non-sortable callsites
// (the pinned section) fall through to the no-drag-handle path and the
// header still works as a plain toggle.
//
// The context itself lives in SidebarSection so the consumer side
// (SidebarSection) and provider side (SortableSection, below) share a
// single import without a circular type dependency. SidebarSection
// also exports the hook SidebarSection uses to read the handle.

export function AppSidebar({
  state,
  sidebarWorkspaces,
  activeWorkspaceID,
  pinnedThreads,
  activeThreadID: nativeActiveThreadID,
  pendingThreadID,
  pendingWorkspaceID,
  collapsedSidebarSectionIDs,
  collapsedFolderIDs,
  setCollapsedFolderIDs,
  expandedSidebarSectionIDs,
  loadingWorkspaceThreadIDs,
  workspaceThreadsByWorkspaceID,
  workspaceMenuOpen,
  workspaceMenuRef,
  searchOpen,
  sectionOrder,
  onStartNewThread,
  onOpenSkillsTab,
  onToggleConversationSearch,
  pendingConversations = [],
  onSelectPendingConversation,
  onSelectThread: selectNativeThread,
  onTogglePinned,
  onArchiveThread,
  onDeleteThread,
  onRenameThread,
  onToggleWorkspaceMenu,
  onCreateWorkspace,
  onOpenWorkspaceFolder,
  onToggleSidebarSectionCollapsed,
  onFocusWorkspace,
  onStartNewThreadInWorkspace,
  onSelectWorkspaceThread: selectNativeWorkspaceThread,
  onRemoveWorkspace,
  onRelocateWorkspace,
  onCreateProject,
  onAdoptIntoProject,
  onReorderSections,
  onPointerEnter,
  onPointerLeave,
  onOpenSettings,
  onOpenAccount,
  onMarkThreadsViewed,
  unreadViewOpen,
  onToggleUnreadView,
  attentionStickyIDs: attentionStickyIDsProp,
  onAttentionStickyIDsChange,
  pluginHost = desktopPluginHost,
  workbenchController = desktopWorkbenchController,
  mobileNavigation = false,
  drawerVisible = false,
  sidebarVisible = true,
  sidebarCollapsed = false,
  onNavigateAway,
  onToggleSidebar,
}: {
  state: AppState;
  // The sidebar renders scratch conversations through the same WorkspaceList
  // path as real projects, so App.tsx prepends a synthetic DesktopProject
  // (id = SCRATCH_PSEUDO_PROJECT_ID) into this array. The original
  // state.projects list is unchanged; sidebarWorkspaces is what the sidebar
  // actually shows.
  sidebarWorkspaces: DesktopProject[];
  pendingConversations?: readonly PendingConversation[];
  onSelectPendingConversation?: (id: string) => void;
  activeWorkspaceID?: string;
  pinnedThreads: ThreadSummary[];
  activeThreadID?: string;
  pendingThreadID?: string;
  pendingWorkspaceID?: string;
  collapsedSidebarSectionIDs: Set<string>;
  collapsedFolderIDs: ReadonlySet<string>;
  setCollapsedFolderIDs: Dispatch<SetStateAction<Set<string>>>;
  expandedSidebarSectionIDs: Set<string>;
  loadingWorkspaceThreadIDs?: ReadonlySet<string>;
  workspaceThreadsByWorkspaceID: Record<string, ThreadSummary[]>;
  workspaceMenuOpen: boolean;
  workspaceMenuRef: RefObject<HTMLDivElement | null>;
  searchOpen: boolean;
  // Order of reorderable sections. The pinned section is rendered first
  // (fixed position) and is NOT included. Each key maps to either
  // SCRATCH_PSEUDO_PROJECT_ID or a real project id.
  sectionOrder: string[];
  onStartNewThread: () => void;
  onOpenSkillsTab: () => void;
  onToggleConversationSearch: () => void;
  onSelectThread: (id: string) => void;
  onTogglePinned: (thread: ThreadSummary) => void;
  onArchiveThread: (thread: ThreadSummary) => void;
  onDeleteThread: (thread: ThreadSummary) => void;
  onRenameThread: (thread: ThreadSummary, title: string) => void;
  onToggleWorkspaceMenu: () => void;
  onCreateWorkspace: () => void;
  onOpenWorkspaceFolder: () => void;
  onToggleSidebarSectionCollapsed: (id: string) => void;
  onFocusWorkspace?: (id: string) => void;
  onStartNewThreadInWorkspace: (id: string) => void;
  onSelectWorkspaceThread: (workspaceID: string, threadID: string) => void;
  onRemoveWorkspace: (id: string) => void;
  onRelocateWorkspace: (id: string) => void;
  // Open a project draft, in the given workspace or the active one.
  onCreateProject?: (workspaceID?: string) => void;
  // The user dropped a conversation onto a project.
  onAdoptIntoProject?: (projectID: string, threadID: string) => void;
  // Fires when the user drops a reorderable sidebar section in a new
  // position. The next array is the FULL sectionOrder with the moved
  // entry swapped into place. App.tsx persists this via the same
  // sidebarSectionOrder effect that already exists. Optional so test
  // harnesses and other consumers can render the sidebar without
  // wiring persistence — production callers (App.tsx) always provide it.
  onReorderSections?: (nextOrder: string[]) => void;
  onPointerEnter?: () => void;
  onPointerLeave?: (event: ReactPointerEvent<HTMLElement>) => void;
  onOpenSettings: (page?: "providers" | "usage") => void;
  onOpenAccount?: () => void;
  onMarkThreadsViewed: (threads: readonly ThreadSummary[]) => void;
  // The bell view is a sidebar navigation mode, so App owns the flag: faces
  // that replace this component wholesale (settings, account) must bring the
  // user back to the view they left, not to the session list.
  unreadViewOpen: boolean;
  onToggleUnreadView: () => void;
  attentionStickyIDs?: ReadonlySet<string>;
  onAttentionStickyIDsChange?: (ids: Set<string>) => void;
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
  mobileNavigation?: boolean;
  drawerVisible?: boolean;
  sidebarVisible?: boolean;
  sidebarCollapsed?: boolean;
  onNavigateAway?: () => void;
  onToggleSidebar?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [attentionStickyIDsState, setAttentionStickyIDsState] = useState<Set<string>>(() => new Set());
  const attentionStickyIDs = attentionStickyIDsProp ?? attentionStickyIDsState;
  const attentionStickyIDsRef = useRef(attentionStickyIDs);
  attentionStickyIDsRef.current = attentionStickyIDs;

  function commitAttentionStickyIDs(next: Set<string>): void {
    if (sameStringSet(attentionStickyIDsRef.current, next)) return;
    if (onAttentionStickyIDsChange) {
      onAttentionStickyIDsChange(next);
      return;
    }
    setAttentionStickyIDsState(next);
  }
  const organizationSourceThreads = useMemo(() => {
    const byID = new Map<string, ThreadSummary>();
    for (const threads of Object.values(workspaceThreadsByWorkspaceID)) {
      for (const thread of threads) byID.set(thread.id, thread);
    }
    for (const thread of pinnedThreads) byID.set(thread.id, thread);
    return [...byID.values()];
  }, [pinnedThreads, workspaceThreadsByWorkspaceID]);
  const organization = useSessionOrganization(organizationSourceThreads);
  const [pinnedItems, setPinnedItems] = useState<SidebarPinnedItem[]>(
    loadSidebarPinnedItems,
  );
  const [functionalGroupOrder, setFunctionalGroupOrder] = useState<SidebarFunctionalGroupID[]>(
    loadSidebarFunctionalGroupOrder,
  );
  const [collapsedFunctionalGroupIDs, setCollapsedFunctionalGroupIDs] =
    useState<Set<SidebarFunctionalGroupID>>(loadCollapsedSidebarFunctionalGroups);
  const [draggingFunctionalGroupID, setDraggingFunctionalGroupID] =
    useState<SidebarFunctionalGroupID>();
  const [groupContextMenu, setGroupContextMenu] = useState<{
    group: SessionGroup;
    pinned: boolean;
    x: number;
    y: number;
  } | null>(null);
  const [groupNameDialog, setGroupNameDialog] = useState<
    | { action: "create"; thread?: ThreadSummary }
    | { action: "rename"; group: SessionGroup }
    | null
  >(null);
  const [groupName, setGroupName] = useState("");
  const [groupNamePending, setGroupNamePending] = useState(false);
  const [folderDragThreadID, setFolderDragThreadID] = useState<string>();
  const [folderDropTargetID, setFolderDropTargetID] = useState<string>();
  const folderDropTargetIDRef = useRef<string | undefined>(undefined);
  const folderDragThread = folderDragThreadID
    ? organizationSourceThreads.find((thread) => thread.id === folderDragThreadID)
    : undefined;
  const folderDragCanRemove = Boolean(
    folderDragThreadID
      && (folderDragThread?.pinned || organization.folderByThreadID[folderDragThreadID]),
  );
  const hasRuntimeContext = Boolean(state.activeContext);
  // The scratch pseudo project is "active" when the runtime context is in
  // no-project mode (i.e. the user is viewing a scratch conversation).
  // Active state is passed into WorkspaceList so the row highlights even though
  // it has no DesktopProject entry in state.projects.
  const sidebarScratchPseudoActive = state.activeContext?.kind === "no_project";
  const declaredPluginNavigationEntries = useSyncExternalStore(
    (listener) => pluginHost.subscribe(listener),
    () => pluginHost.getNavigationEntries(),
    () => pluginHost.getNavigationEntries(),
  );
  const workbenchSnapshot = useSyncExternalStore(
    workbenchController.subscribe,
    workbenchController.getSnapshot,
    workbenchController.getSnapshot,
  );
  const activePluginMainView = workbenchSnapshot.views.find(
    (view) => view.id === workbenchSnapshot.activeViewByRegion.primary,
  );
  const activeThreadID = activePluginMainView ? undefined : nativeActiveThreadID;
  const pluginNavigationEntries = useMemo(
    () => primaryViewNavigation(declaredPluginNavigationEntries, workbenchSnapshot),
    [declaredPluginNavigationEntries, workbenchSnapshot],
  );
  const activateNative = useCallback((action: () => void): void => {
    workbenchController.deactivateRegion("primary");
    action();
  }, [workbenchController]);
  const onSelectThread = useCallback((id: string) => {
    activateNative(() => selectNativeThread(id));
  }, [activateNative, selectNativeThread]);
  const onSelectWorkspaceThread = useCallback((workspaceID: string, threadID: string) => {
    activateNative(() => selectNativeWorkspaceThread(workspaceID, threadID));
  }, [activateNative, selectNativeWorkspaceThread]);
  const openPluginNavigation = useCallback((pluginId: string, viewTypeId: string, instanceId?: string): void => {
    if (instanceId) {
      workbenchController.activateView(instanceId);
      return;
    }
    void workbenchController.openPluginView(pluginId, viewTypeId, {
      region: "primary",
      persistence: "durable",
      reveal: true,
    }).catch(() => workbenchController.deactivateRegion("primary"));
  }, [workbenchController]);

  // Drag-and-drop reorder wiring for the reorderable sections. The 6px
  // activation distance lets plain clicks on the header (collapse toggle)
  // and on threads inside the section pass through without triggering a
  // drag — matches SessionTabs so the two surfaces share a feel.
  const sensors = useSensors(
    useSensor(SidebarPointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const dropAnimation = useDropAnimation();
  const [draggingSectionID, setDraggingSectionID] = useState<string | undefined>();
  const [sidebarSortIndicator, setSidebarSortIndicator] = useState<{
    id: string;
    position: "before" | "after";
  }>();
  const sidebarDragStartClientYRef = useRef<number | undefined>(undefined);
  // Each SortableSection calls back into this registry on every render
  // with its id → header info. The DragOverlay reads from this map to
  // render the right header for the currently dragged section. Using a
  // ref (not state) avoids an extra render when a child mounts and
  // keeps the lookup cheap during high-frequency drag pointer moves.
  const sectionHeaderInfoByIDRef = useRef<Map<string, SidebarSectionHeaderInfo>>(new Map());
  const registerSectionHeaderInfo = useCallback(
    (id: string, info: SidebarSectionHeaderInfo | null): void => {
      if (info === null) {
        sectionHeaderInfoByIDRef.current.delete(id);
      } else {
        sectionHeaderInfoByIDRef.current.set(id, info);
      }
    },
    [],
  );
  const draggingSectionInfo = draggingSectionID
    ? sectionHeaderInfoByIDRef.current.get(draggingSectionID)
    : undefined;
  const folderSortableIDs = organization.folders.map((folder) => `${FOLDER_SORTABLE_PREFIX}${folder.id}`);
  const availableFolderIDs = new Set(organization.folders.map((folder) => folder.id));
  const availableWorkspaceIDs = new Set(sidebarWorkspaces.map((project) => project.id));
  const availablePinnedThreadIDs = new Set(pinnedThreads.map((thread) => thread.id));
  const validPinnedItems = pinnedItems.filter((entry) => {
    if (entry.kind === "thread") return availablePinnedThreadIDs.has(entry.id);
    if (entry.kind === "folder") return availableFolderIDs.has(entry.id);
    return availableWorkspaceIDs.has(entry.id);
  });
  const knownPinnedThreadIDs = new Set(
    validPinnedItems.filter((entry) => entry.kind === "thread").map((entry) => entry.id),
  );
  for (const thread of pinnedThreads) {
    if (!knownPinnedThreadIDs.has(thread.id)) {
      validPinnedItems.push({ kind: "thread", id: thread.id });
    }
  }
  const pinnedItemSortableIDs = validPinnedItems.map(pinnedItemSortableID);
  const pinnedItemBySortableID = new Map(
    validPinnedItems.map((entry) => [pinnedItemSortableID(entry), entry]),
  );
  const validPinnedContainers = validPinnedItems.filter(
    (entry): entry is SidebarPinnedItem & { kind: "folder" | "workspace" } =>
      entry.kind === "folder" || entry.kind === "workspace",
  );
  const pinnedFolderIDs = new Set(
    validPinnedContainers.filter((entry) => entry.kind === "folder").map((entry) => entry.id),
  );
  const pinnedWorkspaceIDs = new Set(
    validPinnedContainers.filter((entry) => entry.kind === "workspace").map((entry) => entry.id),
  );
  const visibleFolderSortableIDs = folderSortableIDs.filter(
    (id) => !pinnedFolderIDs.has(id.slice(FOLDER_SORTABLE_PREFIX.length)),
  );
  const visibleWorkspaceSectionOrder = sectionOrder.filter(
    (id) => !pinnedWorkspaceIDs.has(id),
  );

  function updatePinnedItems(
    updater: (current: SidebarPinnedItem[]) => SidebarPinnedItem[],
  ): void {
    setPinnedItems((current) => {
      const next = updater(current);
      window.localStorage.setItem(SIDEBAR_PINNED_ITEMS_KEY, JSON.stringify(next));
      return next;
    });
  }

  function pinContainer(
    entry: SidebarPinnedItem,
    targetSortableID?: string,
    position: "before" | "after" = "after",
  ): void {
    updatePinnedItems((current) => {
      const next = [...current];
      for (const existing of validPinnedItems) {
        if (!next.some((candidate) =>
          candidate.kind === existing.kind && candidate.id === existing.id
        )) next.push(existing);
      }
      const alreadyPinned = next.some(
        (candidate) => candidate.kind === entry.kind && candidate.id === entry.id,
      );
      const target = targetSortableID
        ? pinnedItemBySortableID.get(targetSortableID)
        : undefined;
      if (!target) return alreadyPinned ? next : [...next, entry];
      const withoutEntry = next.filter(
        (candidate) => candidate.kind !== entry.kind || candidate.id !== entry.id,
      );
      const targetIndex = withoutEntry.findIndex(
        (candidate) => candidate.kind === target.kind && candidate.id === target.id,
      );
      if (targetIndex === -1) return [...withoutEntry, entry];
      withoutEntry.splice(targetIndex + (position === "after" ? 1 : 0), 0, entry);
      return withoutEntry;
    });
    setCollapsedFunctionalGroupIDs((current) => {
      if (!current.has("pinned")) return current;
      const next = new Set(current);
      next.delete("pinned");
      persistCollapsedSidebarFunctionalGroups(next);
      return next;
    });
  }

  function unpinContainer(entry: SidebarPinnedItem): void {
    updatePinnedItems((current) => current.filter(
      (candidate) => candidate.kind !== entry.kind || candidate.id !== entry.id,
    ));
  }

  function toggleThreadPinned(thread: ThreadSummary): void {
    if (thread.pinned) {
      unpinContainer({ kind: "thread", id: thread.id });
    } else {
      pinContainer({ kind: "thread", id: thread.id });
    }
    onTogglePinned(thread);
  }

  function functionalGroupForSortableID(id: string | undefined): SidebarFunctionalGroupID | undefined {
    if (!id) return undefined;
    if (SIDEBAR_FUNCTIONAL_GROUP_IDS.includes(id as SidebarFunctionalGroupID)) {
      return id as SidebarFunctionalGroupID;
    }
    if (id === PINNED_APPEND_DROP_ID) return "pinned";
    if (pinnedItemBySortableID.has(id)) return "pinned";
    if (id.startsWith(FOLDER_SORTABLE_PREFIX)) return "folders";
    if (sectionOrder.includes(id)) return "workspace";
    return undefined;
  }

  const sidebarCollisionDetection: CollisionDetection = (args) => {
    const activeID = String(args.active.id);
    const activePinnedEntry = pinnedItemBySortableID.get(activeID);
    const allowed = (candidateID: string): boolean => {
      if (SIDEBAR_FUNCTIONAL_GROUP_IDS.includes(activeID as SidebarFunctionalGroupID)) {
        return SIDEBAR_FUNCTIONAL_GROUP_IDS.includes(candidateID as SidebarFunctionalGroupID);
      }
      if (activePinnedEntry) {
        const returnsToEmptySource = activePinnedEntry.kind === "folder"
          ? visibleFolderSortableIDs.length === 0 && candidateID === "folders"
          : activePinnedEntry.kind === "workspace"
            ? visibleWorkspaceSectionOrder.length === 0 && candidateID === "workspace"
            : candidateID === "folders" || candidateID === "workspace";
        return returnsToEmptySource
          || candidateID === PINNED_APPEND_DROP_ID
          || pinnedItemBySortableID.has(candidateID)
          || (activePinnedEntry.kind === "folder"
            ? visibleFolderSortableIDs.includes(candidateID)
            : activePinnedEntry.kind === "workspace"
              ? visibleWorkspaceSectionOrder.includes(candidateID)
              : false);
      }
      if (activeID.startsWith(FOLDER_SORTABLE_PREFIX)) {
        return candidateID === "folders"
          || candidateID === PINNED_APPEND_DROP_ID
          || pinnedItemBySortableID.has(candidateID)
          || (candidateID.startsWith(FOLDER_SORTABLE_PREFIX)
            && !pinnedItemBySortableID.has(candidateID));
      }
      if (sectionOrder.includes(activeID)) {
        return candidateID === "workspace"
          || candidateID === PINNED_APPEND_DROP_ID
          || pinnedItemBySortableID.has(candidateID)
          || (sectionOrder.includes(candidateID)
            && !pinnedItemBySortableID.has(candidateID));
      }
      return true;
    };
    return closestCenter({
      ...args,
      droppableContainers: args.droppableContainers.filter(
        (container) => allowed(String(container.id)),
      ),
    });
  };

  function handleSidebarDragStart(event: DragStartEvent): void {
    const activeID = String(event.active.id);
    const activatorClientY = (event.activatorEvent as MouseEvent).clientY;
    sidebarDragStartClientYRef.current = typeof activatorClientY === "number"
      ? activatorClientY
      : undefined;
    if (SIDEBAR_FUNCTIONAL_GROUP_IDS.includes(activeID as SidebarFunctionalGroupID)) {
      setDraggingFunctionalGroupID(activeID as SidebarFunctionalGroupID);
    } else {
      setDraggingSectionID(activeID);
    }
  }

  function handleSidebarDragOver(event: DragOverEvent): void {
    const activeID = String(event.active.id);
    const overID = event.over ? String(event.over.id) : undefined;
    const activePinnedEntry = pinnedItemBySortableID.get(activeID);
    const activePinned = activePinnedEntry !== undefined;
    const activeCanEnterPinned = activeID.startsWith(FOLDER_SORTABLE_PREFIX)
      || sectionOrder.includes(activeID);
    if ((activePinned || activeCanEnterPinned) && overID === PINNED_APPEND_DROP_ID) {
      setSidebarSortIndicator({ id: PINNED_APPEND_DROP_ID, position: "after" });
      return;
    }
    const returnTargets = activePinnedEntry?.kind === "folder"
      ? visibleFolderSortableIDs
      : activePinnedEntry?.kind === "workspace"
        ? visibleWorkspaceSectionOrder
        : undefined;
    const returnGroup = activePinnedEntry?.kind === "folder"
      ? "folders"
      : activePinnedEntry?.kind === "workspace"
        ? "workspace"
        : undefined;
    if (overID && returnTargets?.includes(overID)) {
      const overRect = event.over?.rect;
      const pointerY = sidebarDragStartClientYRef.current === undefined
        ? undefined
        : sidebarDragStartClientYRef.current + event.delta.y;
      const position = overRect && pointerY !== undefined
        ? pointerY < overRect.top + overRect.height / 2 ? "before" : "after"
        : "before";
      setSidebarSortIndicator({ id: overID, position });
      return;
    }
    if (returnTargets?.length === 0 && overID === returnGroup && returnGroup) {
      setSidebarSortIndicator({ id: returnGroup, position: "after" });
      return;
    }
    if ((!activePinned && !activeCanEnterPinned)
      || !overID
      || !pinnedItemBySortableID.has(overID)
    ) {
      setSidebarSortIndicator(undefined);
      return;
    }
    if (!activePinned) {
      const activeRect = event.active.rect.current.translated;
      const overRect = event.over?.rect;
      const pointerY = sidebarDragStartClientYRef.current === undefined
        ? undefined
        : sidebarDragStartClientYRef.current + event.delta.y;
      const position = overRect && (pointerY !== undefined || activeRect)
        ? (pointerY ?? (activeRect ? activeRect.top + activeRect.height / 2 : overRect.top))
            < overRect.top + overRect.height / 2
          ? "before"
          : "after"
        : "before";
      setSidebarSortIndicator({ id: overID, position });
      return;
    }
    const activeIndex = pinnedItemSortableIDs.indexOf(activeID);
    const overIndex = pinnedItemSortableIDs.indexOf(overID);
    if (activeIndex === -1 || overIndex === -1 || activeIndex === overIndex) {
      setSidebarSortIndicator(undefined);
      return;
    }
    setSidebarSortIndicator({
      id: overID,
      position: activeIndex < overIndex ? "after" : "before",
    });
  }

  function handleSidebarDragEnd(event: DragEndEvent): void {
    const activeID = String(event.active.id);
    const overID = event.over ? String(event.over.id) : undefined;
    const activeGroup = functionalGroupForSortableID(activeID);
    const overGroup = functionalGroupForSortableID(overID);

    if (SIDEBAR_FUNCTIONAL_GROUP_IDS.includes(activeID as SidebarFunctionalGroupID)) {
      const next = reorderSidebarSections(functionalGroupOrder, activeID, overGroup);
      if (next !== functionalGroupOrder) {
        const nextOrder = next as SidebarFunctionalGroupID[];
        setFunctionalGroupOrder(nextOrder);
        persistSidebarFunctionalGroupOrder(nextOrder);
      }
    } else if (activeGroup === "pinned") {
      const entry = pinnedItemBySortableID.get(activeID);
      const returnGroup = entry?.kind === "folder"
        ? "folders"
        : entry?.kind === "workspace"
          ? "workspace"
          : undefined;
      if (entry?.kind === "thread" && (overGroup === "folders" || overGroup === "workspace")) {
        const thread = pinnedThreads.find((candidate) => candidate.id === entry.id);
        if (thread) toggleThreadPinned(thread);
      } else if (entry && overGroup === returnGroup) {
        if (entry.kind === "folder" && overID && visibleFolderSortableIDs.includes(overID)) {
          const next = moveSidebarItem(
            folderSortableIDs,
            activeID,
            overID,
            sidebarSortIndicator?.id === overID ? sidebarSortIndicator.position : "before",
          );
          if (next !== folderSortableIDs) {
            void organization.reorderFolders(next.map((id) => id.slice(FOLDER_SORTABLE_PREFIX.length)));
          }
        } else if (entry.kind === "workspace" && overID && visibleWorkspaceSectionOrder.includes(overID)) {
          const next = moveSidebarItem(
            sectionOrder,
            activeID,
            overID,
            sidebarSortIndicator?.id === overID ? sidebarSortIndicator.position : "before",
          );
          if (next !== sectionOrder) onReorderSections?.(next);
        }
        unpinContainer(entry);
      } else if (entry && overGroup === "pinned") {
        const nextIDs = overID === PINNED_APPEND_DROP_ID
          ? [...pinnedItemSortableIDs.filter((id) => id !== activeID), activeID]
          : reorderSidebarSections(pinnedItemSortableIDs, activeID, overID);
        if (nextIDs !== pinnedItemSortableIDs) {
          const byID = pinnedItemBySortableID;
          updatePinnedItems(() => nextIDs.flatMap((id) => {
            const nextEntry = byID.get(id);
            return nextEntry ? [nextEntry] : [];
          }));
        }
      }
    } else if (activeID.startsWith(FOLDER_SORTABLE_PREFIX)) {
      const folderID = activeID.slice(FOLDER_SORTABLE_PREFIX.length);
      if (overGroup === "pinned") {
        pinContainer(
          { kind: "folder", id: folderID },
          overID,
          sidebarSortIndicator && sidebarSortIndicator.id === overID
            ? sidebarSortIndicator.position
            : "after",
        );
      } else if (overGroup === "folders") {
        const next = reorderSidebarSections(folderSortableIDs, activeID, overID);
        if (next !== folderSortableIDs) {
          void organization.reorderFolders(next.map((id) => id.slice(FOLDER_SORTABLE_PREFIX.length)));
        }
      }
    } else if (sectionOrder.includes(activeID)) {
      if (overGroup === "pinned") {
        pinContainer(
          { kind: "workspace", id: activeID },
          overID,
          sidebarSortIndicator && sidebarSortIndicator.id === overID
            ? sidebarSortIndicator.position
            : "after",
        );
      } else if (overGroup === "workspace") {
        const next = reorderSidebarSections(sectionOrder, activeID, overID);
        if (next !== sectionOrder) onReorderSections?.(next);
      }
    }

    setDraggingFunctionalGroupID(undefined);
    setDraggingSectionID(undefined);
    setSidebarSortIndicator(undefined);
    sidebarDragStartClientYRef.current = undefined;
  }

  function handleSidebarDragCancel(): void {
    setDraggingFunctionalGroupID(undefined);
    setDraggingSectionID(undefined);
    setSidebarSortIndicator(undefined);
    sidebarDragStartClientYRef.current = undefined;
  }
  function toggleFunctionalGroupCollapsed(groupID: SidebarFunctionalGroupID): void {
    setCollapsedFunctionalGroupIDs((current) => {
      const next = new Set(current);
      if (next.has(groupID)) next.delete(groupID); else next.add(groupID);
      persistCollapsedSidebarFunctionalGroups(next);
      return next;
    });
  }
  const pinnedRows = pinnedThreads;
  const hasPinnedRows = validPinnedItems.length > 0;
  const pinnedHeadingDropTargetID = pinnedItemSortableIDs[0] ?? PINNED_APPEND_DROP_ID;
  const pinnedHasRunning = pinnedRows.some((thread) => isThreadExecuting(thread));
  const pinnedHasUnread = pinnedRows.some((thread) =>
    sidebarThreadUnread(
      thread,
      state.lastViewedTurnByThreadID,
    ),
  );
  const allSidebarThreads = useMemo(() => {
    const byID = new Map<string, ThreadSummary>();
    for (const threads of Object.values(workspaceThreadsByWorkspaceID)) {
      for (const thread of threads) byID.set(thread.id, thread);
    }
    for (const thread of pinnedRows) byID.set(thread.id, thread);
    return [...byID.values()];
  }, [pinnedRows, workspaceThreadsByWorkspaceID]);
  useEffect(() => {
    if (!unreadViewOpen) {
      commitAttentionStickyIDs(new Set());
      return;
    }
    commitAttentionStickyIDs(attentionStickyThreadIDs(
      organizationSourceThreads,
      activeThreadID,
      pendingThreadID,
      state.lastViewedTurnByThreadID,
      attentionStickyIDsRef.current,
    ));
  }, [
    activeThreadID,
    organizationSourceThreads,
    pendingThreadID,
    state.lastViewedTurnByThreadID,
    unreadViewOpen,
  ]);
  const attentionThreads = useMemo(() => partitionAttentionThreads(
    organizationSourceThreads,
    activeThreadID,
    pendingThreadID,
    state.lastViewedTurnByThreadID,
    attentionStickyIDs,
  ), [
    activeThreadID,
    attentionStickyIDs,
    organizationSourceThreads,
    pendingThreadID,
    state.lastViewedTurnByThreadID,
  ]);
  const runningThreads = attentionThreads.running;
  const unreadThreads = attentionThreads.unread;
  const recentThreads = attentionThreads.recent;
  const attentionCount = runningThreads.length + unreadThreads.length;
  const projectIndex = useMemo(
    () => projectDirectory(allSidebarThreads, state.lastViewedTurnByThreadID, activeThreadID),
    [activeThreadID, allSidebarThreads, state.lastViewedTurnByThreadID],
  );
  // Projects list apart from workspaces unless the user pinned or filed them.
  const projectRows = useMemo(() => projectIndex.projects
    .filter((thread) => !thread.pinned && !organization.folderByThreadID[thread.id])
    .sort((a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || a.id.localeCompare(b.id)),
  [organization.folderByThreadID, projectIndex]);
  const visibleWorkspaceThreadsByWorkspaceID = useMemo(() => {
    const next: Record<string, ThreadSummary[]> = {};
    const byID = new Map(allSidebarThreads.map((thread) => [thread.id, thread]));
    const projectIDs = new Set(projectIndex.projects.map((thread) => thread.id));
    const projects = sidebarWorkspaces.filter((project) => project.id !== SCRATCH_PSEUDO_PROJECT_ID);
    for (const [workspaceID, threads] of Object.entries(workspaceThreadsByWorkspaceID)) {
      const project = projects.find((candidate) => candidate.id === workspaceID);
      // Cached buckets can overlap while a fork's workspace metadata refreshes.
      // Classify the same session snapshot in every bucket before publishing nodes.
      next[workspaceID] = threads.map((thread) => byID.get(thread.id) ?? thread).filter(
        (thread) => !thread.pinned && !organization.folderByThreadID[thread.id] &&
          !projectIDs.has(thread.id) && !projectIndex.managedSessionIDs.has(thread.id) && (
          workspaceID === SCRATCH_PSEUDO_PROJECT_ID
            ? isScratchThread(thread, projects)
            : project !== undefined && threadBelongsToWorkspace(thread, project)
        ),
      );
    }
    return next;
  }, [allSidebarThreads, organization.folderByThreadID, projectIndex, workspaceThreadsByWorkspaceID, sidebarWorkspaces]);
  const folderThreadsByID = useMemo(() => {
    const next: Record<string, ThreadSummary[]> = {};
    for (const folder of organization.folders) next[folder.id] = [];
    for (const thread of allSidebarThreads) {
      const folderID = organization.folderByThreadID[thread.id];
      // Pinning changes where an item is shown in the sidebar, not its domain
      // ownership. Keep the membership, but do not duplicate pinned sessions
      // inside their source folder.
      if (folderID && next[folderID] && !thread.archived && !thread.pinned) {
        next[folderID].push(thread);
      }
    }
    return next;
  }, [allSidebarThreads, organization.folderByThreadID, organization.folders]);
  function createFolder(thread?: ThreadSummary): void {
    setGroupName("");
    setGroupNameDialog({ action: "create", thread });
  }

  function renameFolder(group: SessionGroup): void {
    setGroupName(group.name);
    setGroupNameDialog({ action: "rename", group });
  }

  function closeGroupNameDialog(): void {
    setGroupNameDialog(null);
    setGroupName("");
  }

  async function submitGroupNameDialog(): Promise<void> {
    if (!groupNameDialog || groupNamePending) return;
    const name = groupName.trim();
    if (!name) return;
    setGroupNamePending(true);
    try {
      if (groupNameDialog.action === "create") {
        await organization.createFolder(name, groupNameDialog.thread?.id);
      } else {
        await organization.renameFolder(groupNameDialog.group.id, name);
      }
      closeGroupNameDialog();
    } catch {
      // The organization controller keeps the dialog open and reports the
      // concrete failure through the product-wide toast surface.
    } finally {
      setGroupNamePending(false);
    }
  }

  function acceptsSessionFolderDrag(event: ReactDragEvent<HTMLElement>): boolean {
    return folderDragThreadID !== undefined
      || Array.from(event.dataTransfer.types ?? []).includes(SESSION_FOLDER_DRAG_MIME);
  }

  function updateFolderDropTarget(folderID?: string): void {
    if (folderDropTargetIDRef.current === folderID) return;
    folderDropTargetIDRef.current = folderID;
    setFolderDropTargetID(folderID);
  }

  function dragSessionOverFolder(event: ReactDragEvent<HTMLElement>, folderID: string): void {
    if (!acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    updateFolderDropTarget(folderID);
  }

  function leaveSessionFolderTarget(event: ReactDragEvent<HTMLElement>, folderID: string): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
    if (folderDropTargetIDRef.current === folderID) updateFolderDropTarget();
  }

  function dropSessionIntoFolder(event: ReactDragEvent<HTMLElement>, folderID: string): void {
    if (!acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    const threadID = event.dataTransfer.getData(SESSION_FOLDER_DRAG_MIME) || folderDragThreadID;
    updateFolderDropTarget();
    setFolderDragThreadID(undefined);
    if (threadID) {
      setCollapsedFolderIDs((current) => {
        if (!current.has(folderID)) return current;
        const next = new Set(current);
        next.delete(folderID);
        return next;
      });
      const thread = allSidebarThreads.find((candidate) => candidate.id === threadID);
      if (thread?.pinned) toggleThreadPinned(thread);
      void organization.moveThreadToFolder(threadID, folderID);
    }
  }

  function dragSessionOverPinned(
    event: ReactDragEvent<HTMLElement>,
    targetSortableID: string,
    fixedPosition?: "before" | "after",
  ): void {
    if (!acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = "move";
    updateFolderDropTarget(PINNED_SESSION_DROP_TARGET);
    const rect = event.currentTarget.getBoundingClientRect();
    const position = fixedPosition
      ?? (event.clientY < rect.top + rect.height / 2 ? "before" : "after");
    setSidebarSortIndicator({ id: targetSortableID, position });
  }

  function leavePinnedSessionTarget(
    event: ReactDragEvent<HTMLElement>,
    targetSortableID: string,
  ): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
    setSidebarSortIndicator((current) => current?.id === targetSortableID ? undefined : current);
  }

  function dropSessionIntoPinned(
    event: ReactDragEvent<HTMLElement>,
    targetSortableID: string,
    fixedPosition?: "before" | "after",
  ): void {
    if (!acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    event.stopPropagation();
    const threadID = event.dataTransfer.getData(SESSION_FOLDER_DRAG_MIME) || folderDragThreadID;
    const rect = event.currentTarget.getBoundingClientRect();
    const position = fixedPosition
      ?? (event.clientY < rect.top + rect.height / 2 ? "before" : "after");
    updateFolderDropTarget();
    setFolderDragThreadID(undefined);
    setSidebarSortIndicator(undefined);
    if (!threadID) return;
    const thread = allSidebarThreads.find((candidate) => candidate.id === threadID);
    if (thread) {
      pinContainer({ kind: "thread", id: thread.id }, targetSortableID, position);
      if (!thread.pinned) onTogglePinned(thread);
    }
    setCollapsedFunctionalGroupIDs((current) => {
      if (!current.has("pinned")) return current;
      const next = new Set(current);
      next.delete("pinned");
      persistCollapsedSidebarFunctionalGroups(next);
      return next;
    });
  }

  function pinnedSessionDropProps(
    targetSortableID: string,
    fixedPosition?: "before" | "after",
  ): HTMLAttributes<HTMLElement> {
    return {
      onDragEnter: (event) => dragSessionOverPinned(event, targetSortableID, fixedPosition),
      onDragOver: (event) => dragSessionOverPinned(event, targetSortableID, fixedPosition),
      onDragLeave: (event) => leavePinnedSessionTarget(event, targetSortableID),
      onDrop: (event) => dropSessionIntoPinned(event, targetSortableID, fixedPosition),
    };
  }

  function dragSessionOverWorkspace(event: ReactDragEvent<HTMLElement>): void {
    if (!acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    updateFolderDropTarget(WORKSPACE_SESSION_DROP_TARGET);
  }

  function dropSessionIntoWorkspace(event: ReactDragEvent<HTMLElement>): void {
    if (!acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    const threadID = event.dataTransfer.getData(SESSION_FOLDER_DRAG_MIME) || folderDragThreadID;
    updateFolderDropTarget();
    setFolderDragThreadID(undefined);
    if (!threadID) return;
    const thread = allSidebarThreads.find((candidate) => candidate.id === threadID);
    if (thread?.pinned) toggleThreadPinned(thread);
  }

  function dragSessionOverFolderRemoval(event: ReactDragEvent<HTMLElement>): void {
    if (!folderDragCanRemove || !acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    updateFolderDropTarget(FOLDER_REMOVE_DROP_TARGET);
  }

  function dropSessionOutOfFolder(event: ReactDragEvent<HTMLElement>): void {
    if (!folderDragCanRemove || !acceptsSessionFolderDrag(event)) return;
    event.preventDefault();
    const threadID = event.dataTransfer.getData(SESSION_FOLDER_DRAG_MIME) || folderDragThreadID;
    updateFolderDropTarget();
    setFolderDragThreadID(undefined);
    if (!threadID) return;
    const thread = allSidebarThreads.find((candidate) => candidate.id === threadID);
    if (thread?.pinned) {
      onTogglePinned(thread);
    } else {
      void organization.moveThreadToFolder(threadID);
    }
  }

  const organizationActions = {
    ...organization,
    togglePinned: (thread: ThreadSummary, _fallback: (thread: ThreadSummary) => void) => {
      toggleThreadPinned(thread);
    },
    moveToFolder: (thread: ThreadSummary, folderID?: string) => {
      organization.moveThreadToFolder(thread.id, folderID);
    },
    createFolderForThread: (thread: ThreadSummary) => createFolder(thread),
    folderDragThreadID,
    startFolderDrag: (threadID: string) => {
      setFolderDragThreadID(threadID);
      updateFolderDropTarget();
    },
    endFolderDrag: () => {
      setFolderDragThreadID(undefined);
      updateFolderDropTarget();
      setSidebarSortIndicator(undefined);
    },
  };

  function renderFolderSection(folder: SessionGroup, pinned: boolean): JSX.Element {
    const collapsed = collapsedFolderIDs.has(folder.id);
    const sortableID = `${FOLDER_SORTABLE_PREFIX}${folder.id}`;
    const folderThreads = folderThreadsByID[folder.id] ?? [];
    const folderHasRunning = folderThreads.some((thread) => isThreadExecuting(thread));
    const folderHasUnread = folderThreads.some((thread) => sidebarThreadUnread(
      thread,
      state.lastViewedTurnByThreadID,
    ));
    return (
      <SortableSidebarSection
        id={sortableID}
        className={`project-section session-folder-drop-target${folderDropTargetID === folder.id ? " drop-active" : ""}`}
        key={sortableID}
        ariaLabel={folder.name}
        headerInfo={{ label: folder.name, iconKind: "project", CollapsedIcon: Folder, ExpandedIcon: FolderOpen }}
        registerHeaderInfo={registerSectionHeaderInfo}
        sortIndicator={sidebarSortIndicator?.id === sortableID
          ? sidebarSortIndicator.position
          : undefined}
        containerProps={pinned
          ? pinnedSessionDropProps(sortableID)
          : {
              onDragEnter: (event) => dragSessionOverFolder(event, folder.id),
              onDragOver: (event) => dragSessionOverFolder(event, folder.id),
              onDragLeave: (event) => leaveSessionFolderTarget(event, folder.id),
              onDrop: (event) => dropSessionIntoFolder(event, folder.id),
            }}
      >
        <SidebarSection
          expanded={!collapsed}
          iconKind="project"
          CollapsedIcon={Folder}
          ExpandedIcon={FolderOpen}
          label={folder.name}
          ariaLabel={t(collapsed ? "sidebar.expandSection" : "sidebar.collapseSection", { section: folder.name })}
          title={t(collapsed ? "sidebar.expandSection" : "sidebar.collapseSection", { section: folder.name })}
          running={folderHasRunning}
          unread={folderHasUnread}
          onToggle={() => setCollapsedFolderIDs((current) => {
            const next = new Set(current);
            if (next.has(folder.id)) next.delete(folder.id); else next.add(folder.id);
            return next;
          })}
          onContextMenu={(event) => {
            event.preventDefault();
            setGroupContextMenu({ group: folder, pinned, x: event.clientX, y: event.clientY });
          }}
        >
          {folderThreads.length > 0 ? (
            <OrganizationThreadList
              threads={folderThreads}
              projectSummaries={projectIndex.summaries}
              activeID={activeThreadID}
              pendingThreadID={pendingThreadID}
              lastViewedTurnByThreadID={state.lastViewedTurnByThreadID}
              onSelect={onSelectThread}
              onTogglePinned={onTogglePinned}
              onArchive={onArchiveThread}
              onDelete={onDeleteThread}
              onRename={onRenameThread}
            />
          ) : null}
        </SidebarSection>
      </SortableSidebarSection>
    );
  }

  function renderWorkspaceSection(project: DesktopProject, pinned: boolean): JSX.Element {
    const isScratchPseudo = project.id === SCRATCH_PSEUDO_PROJECT_ID;
    const sectionAriaLabel = isScratchPseudo
      ? t("sidebar.workspace")
      : t("sidebar.workspaceNamed", { name: project.name });
    return (
      <SortableSidebarSection
        key={project.id}
        id={project.id}
        className="project-section"
        ariaLabel={sectionAriaLabel}
        headerInfo={{
          label: isScratchPseudo ? t("sidebar.conversations") : project.name,
          iconKind: isScratchPseudo ? "conversation" : "project",
          CollapsedIcon: isScratchPseudo ? MessageSquare : Folder,
          ExpandedIcon: isScratchPseudo ? MessagesSquare : FolderOpen,
        }}
        registerHeaderInfo={registerSectionHeaderInfo}
        sortIndicator={sidebarSortIndicator?.id === project.id
          ? sidebarSortIndicator.position
          : undefined}
        containerProps={pinned ? pinnedSessionDropProps(project.id) : undefined}
      >
        <WorkspaceGroup
          project={project}
          pendingConversations={pendingConversations.filter((pending) => pending.context.kind === "project"
            ? pending.context.project_id === project.id
            : isScratchPseudo)}
          activeSessionTabID={state.activeSessionTabID}
          onSelectPendingConversation={onSelectPendingConversation}
          activeID={activeWorkspaceID ?? state.activeProjectId}
          pendingWorkspaceID={pendingWorkspaceID}
          expandedSidebarSectionIDs={expandedSidebarSectionIDs}
          loadingWorkspaceThreadIDs={loadingWorkspaceThreadIDs}
          threadsByWorkspaceID={visibleWorkspaceThreadsByWorkspaceID}
          activeThreadID={activeThreadID}
          pendingThreadID={pendingThreadID}
          lastViewedTurnByThreadID={state.lastViewedTurnByThreadID}
          scratchPseudoWorkspaceID={SCRATCH_PSEUDO_PROJECT_ID}
          scratchPseudoActive={sidebarScratchPseudoActive}
          onToggleSidebarSectionCollapsed={onToggleSidebarSectionCollapsed}
          onFocusWorkspace={onFocusWorkspace}
          onStartNewThread={onStartNewThreadInWorkspace}
          onSelectThread={onSelectWorkspaceThread}
          onToggleThreadPinned={onTogglePinned}
          onArchiveThread={onArchiveThread}
          onDeleteThread={onDeleteThread}
          onRenameThread={onRenameThread}
          onRemoveWorkspace={onRemoveWorkspace}
          onRelocateWorkspace={onRelocateWorkspace}
          onCreateProject={onCreateProject}
          workspacePinned={pinned}
          onToggleWorkspacePinned={(id) => pinned
            ? unpinContainer({ kind: "workspace", id })
            : pinContainer({ kind: "workspace", id })}
        />
      </SortableSidebarSection>
    );
  }

  // Top-level actions stay above every group: new conversation, search, and the
  // plugin catalog entry.
  const primaryNavigationNodes = useMemo<readonly NavigationSourceNode[]>(() => {
    const nodes: NavigationSourceNode[] = [
      {
        id: "command:new-conversation",
        kind: "command",
        label: t("sidebar.newConversation"),
        icon: "message-square-plus",
        disabled: !hasRuntimeContext,
        onActivate: () => activateNative(onStartNewThread),
      },
      {
        id: "command:search-conversations",
        kind: "command",
        label: t("sidebar.searchConversations"),
        icon: "search",
        active: searchOpen,
        disabled: !hasRuntimeContext,
        onActivate: () => activateNative(onToggleConversationSearch),
      },
      {
        id: "command:skills",
        kind: "command",
        label: t("skills.sectionSkills"),
        icon: "plugin-blocks",
        disabled: !hasRuntimeContext,
        onActivate: () => activateNative(onOpenSkillsTab),
      },
    ];
    return Object.freeze(nodes);
  }, [
    activateNative, hasRuntimeContext, onOpenSkillsTab, onStartNewThread,
    onToggleConversationSearch, searchOpen, t,
  ]);

  // Plugin navigation belongs to the primary navigation: it renders directly
  // below the top-level actions, so its entries never hide below the
  // functional groups.
  const pluginNavigationNodes = useMemo<readonly NavigationSourceNode[]>(() => {
    if (pluginNavigationEntries.length === 0) return [];
    const nodes: NavigationSourceNode[] = [{
      id: "section:plugins",
      kind: "section",
      label: t("skills.sectionPlugins"),
      icon: "plugin-blocks",
      depth: 0,
    }];
    for (const entry of pluginNavigationEntries) {
      nodes.push({
        id: `plugin:${entry.pluginId}:${entry.id}`,
        kind: "command",
        parentId: "section:plugins",
        depth: 1,
        label: entry.title,
        icon: entry.icon && "name" in entry.icon ? entry.icon.name : "plugin-blocks",
        active: activePluginMainView !== undefined && activePluginMainView.id === entry.instanceId,
        onActivate: () => openPluginNavigation(entry.pluginId, entry.view, entry.instanceId),
      });
    }
    return Object.freeze(nodes);
  }, [activePluginMainView, openPluginNavigation, pluginNavigationEntries, t]);

  const navigationNodes = useMemo<readonly NavigationSourceNode[]>(() => {
    const nodes: NavigationSourceNode[] = [];
    const functionalGroupNodes: Record<SidebarFunctionalGroupID, NavigationSourceNode[]> = {
      projects: [],
      pinned: [],
      folders: [],
      workspace: [],
    };
    if (projectRows.length > 0) {
      functionalGroupNodes.projects.push({ id: "section:projects", kind: "section", label: t("sidebar.projects"), depth: 0 });
      for (const thread of projectRows) {
        const summary = projectIndex.summaries.get(thread.id);
        functionalGroupNodes.projects.push({
          ...threadNavigationNode(
            thread,
            "section:projects",
            activeThreadID,
            state.lastViewedTurnByThreadID,
            () => onSelectThread(thread.id),
            () => toggleThreadPinned(thread),
            1,
          ),
          running: summary?.running,
          unread: summary?.unread,
        });
      }
    }
    if (hasPinnedRows) {
      functionalGroupNodes.pinned.push({
        id: "section:pinned",
        kind: "section",
        label: t("sidebar.pinned"),
        icon: "pin",
        depth: 0,
        running: pinnedHasRunning,
        unread: pinnedHasUnread,
      });
      for (const entry of validPinnedItems) {
        if (entry.kind === "thread") {
          const thread = pinnedRows.find((candidate) => candidate.id === entry.id);
          if (thread) {
            functionalGroupNodes.pinned.push(threadNavigationNode(
              thread,
              "section:pinned",
              activeThreadID,
              state.lastViewedTurnByThreadID,
              () => onSelectThread(thread.id),
              () => toggleThreadPinned(thread),
              1,
            ));
          }
          continue;
        }
        const folder = entry.kind === "folder"
          ? organization.folders.find((candidate) => candidate.id === entry.id)
          : undefined;
        const project = entry.kind === "workspace"
          ? sidebarWorkspaces.find((candidate) => candidate.id === entry.id)
          : undefined;
        const label = folder?.name ?? project?.name;
        if (!label) continue;
        const parentID = `pinned-${entry.kind}:${entry.id}`;
        const threads = folder
          ? (folderThreadsByID[folder.id] ?? [])
          : (visibleWorkspaceThreadsByWorkspaceID[entry.id] ?? []);
        functionalGroupNodes.pinned.push({
          id: parentID,
          kind: "project",
          label,
          parentId: "section:pinned",
          depth: 1,
          icon: "folder",
          running: threads.some((thread) => isThreadExecuting(thread)),
          unread: threads.some((thread) => sidebarThreadUnread(
            thread,
            state.lastViewedTurnByThreadID,
          )),
          disabled: project ? onFocusWorkspace === undefined : undefined,
          onActivate: project && onFocusWorkspace
            ? () => onFocusWorkspace(project.id)
            : undefined,
        });
        for (const thread of threads) {
          functionalGroupNodes.pinned.push(threadNavigationNode(
            thread,
            parentID,
            activeThreadID,
            state.lastViewedTurnByThreadID,
            () => project
              ? onSelectWorkspaceThread(project.id, thread.id)
              : onSelectThread(thread.id),
            () => toggleThreadPinned(thread),
          ));
        }
      }
    }
    if (organization.folders.some((folder) => !pinnedFolderIDs.has(folder.id))) {
      functionalGroupNodes.folders.push({ id: "section:folders", kind: "section", label: t("sidebar.folders"), icon: "folder", depth: 0 });
      for (const folder of organization.folders) {
        if (pinnedFolderIDs.has(folder.id)) continue;
        const parentID = `folder:${folder.id}`;
        const folderThreads = folderThreadsByID[folder.id] ?? [];
        functionalGroupNodes.folders.push({
          id: parentID,
          kind: "project",
          label: folder.name,
          parentId: "section:folders",
          depth: 1,
          icon: "folder",
          running: folderThreads.some((thread) => isThreadExecuting(thread)),
          unread: folderThreads.some((thread) => sidebarThreadUnread(
            thread,
            state.lastViewedTurnByThreadID,
          )),
        });
        for (const thread of folderThreads) {
          functionalGroupNodes.folders.push(threadNavigationNode(
            thread,
            parentID,
            activeThreadID,
            state.lastViewedTurnByThreadID,
            () => onSelectThread(thread.id),
            () => toggleThreadPinned(thread),
          ));
        }
      }
    }

    if (sectionOrder.length > 0) {
      functionalGroupNodes.workspace.push({
        id: "section:workspace",
        kind: "section",
        label: t("sidebar.workspace"),
        depth: 0,
      });
      for (const workspaceID of visibleWorkspaceSectionOrder) {
        const project = sidebarWorkspaces.find((candidate) => candidate.id === workspaceID);
        if (!project) continue;
        const threads = (visibleWorkspaceThreadsByWorkspaceID[workspaceID] ?? []).filter(
          (thread) => !thread.pinned,
        );
        const isScratch = workspaceID === SCRATCH_PSEUDO_PROJECT_ID;
        const workspaceActive = (isScratch
          ? sidebarScratchPseudoActive
          : workspaceID === (activeWorkspaceID ?? state.activeProjectId)) &&
          !threads.some((thread) => thread.id === activeThreadID);
        functionalGroupNodes.workspace.push({
          id: `project:${workspaceID}`,
          kind: "project",
          label: isScratch ? t("sidebar.conversations") : project.name,
          parentId: "section:workspace",
          depth: 1,
          icon: isScratch ? "messages" : "folder",
          active: workspaceActive,
          unread: threads.some((thread) => isThreadUnread(
            thread,
            state.lastViewedTurnByThreadID[thread.id],
          )),
          running: threads.some((thread) => isThreadExecuting(thread)),
          disabled: onFocusWorkspace === undefined,
          onActivate: onFocusWorkspace === undefined
            ? undefined
            : () => onFocusWorkspace(workspaceID),
        });
        for (const pending of pendingConversations) {
          if (pending.context.kind === "project" ? pending.context.project_id !== workspaceID : !isScratch) continue;
          functionalGroupNodes.workspace.push({
            id: pending.id, kind: "thread", parentId: `project:${workspaceID}`, depth: 2,
            label: pending.title, running: true, active: pending.id === state.activeSessionTabID,
            onActivate: () => onSelectPendingConversation?.(pending.id),
          });
        }
        for (const thread of threads) {
          functionalGroupNodes.workspace.push(threadNavigationNode(
            thread,
            `project:${workspaceID}`,
            activeThreadID,
            state.lastViewedTurnByThreadID,
            () => onSelectWorkspaceThread(workspaceID, thread.id),
            () => toggleThreadPinned(thread),
          ));
        }
      }
    }
    for (const groupID of functionalGroupOrder) {
      nodes.push(...functionalGroupNodes[groupID]);
    }
    nodes.push({
      id: "command:settings",
      kind: "command",
      label: t("sidebar.settings"),
      icon: "settings",
      disabled: !state.initialized,
      onActivate: () => activateNative(onOpenSettings),
    });
    return Object.freeze(nodes);
  }, [
    pendingConversations, onSelectPendingConversation, state.activeSessionTabID,
    activateNative, activeWorkspaceID, activeThreadID,
    hasPinnedRows,
    onOpenSettings,
    onSelectWorkspaceThread, onFocusWorkspace, onSelectThread,
    onTogglePinned, pendingThreadID, pinnedHasRunning,
    pinnedHasUnread, pinnedRows, validPinnedItems,
    projectRows, projectIndex, visibleWorkspaceThreadsByWorkspaceID,
    folderThreadsByID, functionalGroupOrder, organization.folders, pinnedFolderIDs,
    sidebarWorkspaces, sidebarScratchPseudoActive, visibleWorkspaceSectionOrder,
    state.activeProjectId, state.initialized, state.lastViewedTurnByThreadID, t,
  ]);

  const nativeSidebar = (
    <aside
      className="sidebar"
      data-wuu-component="sidebar"
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
    >
      <div className="sidebar-content">
        <div className="traffic-spacer">
          {onToggleSidebar ? (
            <button
              className="icon-button side-panel-toggle-button sidebar-toggle-button sidebar-collapse-toggle"
              data-wuu-component="sidebar-toggle"
              type="button"
              aria-label={t(sidebarCollapsed ? "app.expandLeftSidebar" : "app.collapseLeftSidebar")}
              aria-pressed={!sidebarCollapsed}
              onClick={onToggleSidebar}
            >
              <SidePanelToggleIcon side="left" open={!sidebarCollapsed} />
            </button>
          ) : null}
        </div>
        <SidebarBrand
          unreadViewOpen={unreadViewOpen}
          unreadCount={attentionCount}
          onToggleUnreadView={onToggleUnreadView}
          onClearUnread={() => onMarkThreadsViewed(unreadThreads)}
        />
        {unreadViewOpen ? (
          <section
            className="sidebar-unread-view scrollbar-hidden"
            data-scroll-fade=""
            aria-label={t("sidebar.attentionConversations")}
          >
            {runningThreads.length > 0 ? (
              <AttentionSection
                headingID="sidebar-running-heading"
                title={t("sidebar.runningConversations")}
                count={runningThreads.length}
              >
                {runningThreads.map((thread) => (
                  <AttentionThreadRow
                    key={thread.id}
                    thread={thread}
                    running
                    active={thread.id === activeThreadID}
                    pending={thread.id === pendingThreadID}
                    onSelect={onSelectThread}
                  />
                ))}
              </AttentionSection>
            ) : null}
            {unreadThreads.length > 0 ? (
              <AttentionSection
                headingID="sidebar-unread-heading"
                title={t("sidebar.unreadConversations")}
                count={unreadThreads.length}
              >
                {unreadThreads.map((thread) => (
                  <AttentionThreadRow
                    key={thread.id}
                    thread={thread}
                    unread
                    onSelect={onSelectThread}
                  />
                ))}
              </AttentionSection>
            ) : null}
            {recentThreads.length > 0 ? (
              <AttentionSection
                headingID="sidebar-recent-heading"
                title={t("sidebar.recentActivity")}
                count={recentThreads.length}
              >
                {recentThreads.map((thread) => (
                  <AttentionThreadRow
                    key={thread.id}
                    thread={thread}
                    active={thread.id === activeThreadID}
                    pending={thread.id === pendingThreadID}
                    onSelect={onSelectThread}
                  />
                ))}
              </AttentionSection>
            ) : null}
            {runningThreads.length === 0 && unreadThreads.length === 0 && recentThreads.length === 0 ? (
              <div className="sidebar-unread-empty" role="status">
                <Bell aria-hidden="true" />
                <span>{t("sidebar.attentionEmpty")}</span>
              </div>
            ) : null}
          </section>
        ) : (
          <>
        <nav className="primary-nav" aria-label={t("sidebar.mainNavigation")}>
          <button
            className="nav-item"
            onClick={() => activateNative(onStartNewThread)}
            disabled={!hasRuntimeContext}
          >
            <MessageSquarePlus className="icon-lg" />
            <span>{t("sidebar.newConversation")}</span>
          </button>
          <button
            className="nav-item conversation-search-trigger"
            type="button"
            aria-haspopup="dialog"
            aria-expanded={searchOpen}
            onClick={() => activateNative(onToggleConversationSearch)}
            disabled={!hasRuntimeContext}
          >
            <Search className="icon-lg" />
            <span>{t("sidebar.searchConversations")}</span>
          </button>
          <button
            className="nav-item"
            onClick={() => activateNative(onOpenSkillsTab)}
            disabled={!hasRuntimeContext}
          >
            <PluginBlocksIcon className="icon-lg" />
            <span>{t("skills.sectionSkills")}</span>
          </button>
        </nav>

        <div className="sidebar-main scrollbar-hidden" data-scroll-fade="">
          {pluginNavigationEntries.length > 0 ? (
            <section
              className="sidebar-functional-group plugin-navigation-group"
              aria-label={t("skills.sectionPlugins")}
              data-wuu-component="plugin-navigation"
            >
              <div className="sidebar-functional-heading">
                <span className="sidebar-functional-heading-label">{t("skills.sectionPlugins")}</span>
              </div>
              <div className="sidebar-functional-group-collapse">
                <div className="sidebar-functional-group-body">
                  {pluginNavigationEntries.map((entry) => {
                    const active = activePluginMainView !== undefined && activePluginMainView.id === entry.instanceId;
                    return (
                      <button
                        key={`${entry.pluginId}:${entry.id}`}
                        type="button"
                        className={`nav-item plugin-navigation-item${active ? " active" : ""}`}
                        data-wuu-component="plugin-navigation-item"
                        data-wuu-plugin={entry.pluginId}
                        aria-current={active ? "page" : undefined}
                        title={entry.description || entry.title}
                        onClick={() => openPluginNavigation(entry.pluginId, entry.view, entry.instanceId)}
                      >
                        <PluginIcon icon={entry.icon} pluginId={entry.pluginId} fingerprint={entry.generation} className="icon-lg" />
                        <span>{entry.title}</span>
                      </button>
                    );
                  })}
                </div>
              </div>
            </section>
          ) : null}
          <DndContext
            sensors={sensors}
            collisionDetection={sidebarCollisionDetection}
            modifiers={[restrictToVerticalAxis]}
            onDragStart={handleSidebarDragStart}
            onDragOver={handleSidebarDragOver}
            onDragEnd={handleSidebarDragEnd}
            onDragCancel={handleSidebarDragCancel}
          >
            <SortableContext
              items={functionalGroupOrder}
              strategy={verticalListSortingStrategy}
            >
              {functionalGroupOrder.map((groupID) => groupID === "projects" ? (
                <SortableFunctionalGroup
                  key={groupID}
                  id={groupID}
                  ariaLabel={t("sidebar.projects")}
                  itemDropIndicator={sidebarSortIndicator?.id === groupID
                    ? sidebarSortIndicator.position
                    : undefined}
                  headingLabel={t("sidebar.projects")}
                  collapsed={collapsedFunctionalGroupIDs.has(groupID)}
                  collapseLabel={t(
                    collapsedFunctionalGroupIDs.has(groupID)
                      ? "sidebar.expandSection"
                      : "sidebar.collapseSection",
                    { section: t("sidebar.projects") },
                  )}
                  onToggleCollapsed={() => toggleFunctionalGroupCollapsed(groupID)}
                  action={onCreateProject ? (
                    <button
                      className="sidebar-functional-action"
                      type="button"
                      aria-label={t("projects.newProject")}
                      title={t("projects.newProject")}
                      disabled={!hasRuntimeContext}
                      onClick={() => activateNative(() => onCreateProject())}
                    >
                      <Plus aria-hidden="true" />
                    </button>
                  ) : undefined}
                >
                  <div className="sidebar-functional-group-body">
                    {projectRows.length > 0 ? (
                      <OrganizationThreadList
                        threads={projectRows}
                        projectSummaries={projectIndex.summaries}
                        activeID={activeThreadID}
                        pendingThreadID={pendingThreadID}
                        lastViewedTurnByThreadID={state.lastViewedTurnByThreadID}
                        onSelect={onSelectThread}
                        onTogglePinned={onTogglePinned}
                        onArchive={onArchiveThread}
                        onDelete={onDeleteThread}
                        onRename={onRenameThread}
                        onDropSession={onAdoptIntoProject
                          ? (project, threadID) => onAdoptIntoProject(project.id, threadID)
                          : undefined}
                      />
                    ) : onCreateProject ? (
                      // With no project yet, the group offers its first one.
                      <button
                        className="nav-item project-new-item"
                        type="button"
                        disabled={!hasRuntimeContext}
                        onClick={() => activateNative(() => onCreateProject())}
                      >
                        <Workflow className="icon-lg" />
                        <span>{t("projects.newProject")}</span>
                      </button>
                    ) : null}
                  </div>
                </SortableFunctionalGroup>
              ) : groupID === "pinned" ? (
                <SortableFunctionalGroup
                  key={groupID}
                  id={groupID}
                  ariaLabel={t("sidebar.pinned")}
                  headingClassName={!hasPinnedRows && folderDropTargetID === PINNED_SESSION_DROP_TARGET
                    ? "drop-active"
                    : ""}
                  headingProps={{
                    onDragEnter: (event) => dragSessionOverPinned(
                      event,
                      pinnedHeadingDropTargetID,
                      hasPinnedRows ? "before" : "after",
                    ),
                    onDragOver: (event) => dragSessionOverPinned(
                      event,
                      pinnedHeadingDropTargetID,
                      hasPinnedRows ? "before" : "after",
                    ),
                    onDragLeave: (event) => leavePinnedSessionTarget(event, pinnedHeadingDropTargetID),
                    onDrop: (event) => dropSessionIntoPinned(
                      event,
                      pinnedHeadingDropTargetID,
                      hasPinnedRows ? "before" : "after",
                    ),
                  }}
                  headingLabel={t("sidebar.pinned")}
                  collapsed={collapsedFunctionalGroupIDs.has(groupID)}
                  collapseLabel={t(
                    collapsedFunctionalGroupIDs.has(groupID)
                      ? "sidebar.expandSection"
                      : "sidebar.collapseSection",
                    { section: t("sidebar.pinned") },
                  )}
                  onToggleCollapsed={() => toggleFunctionalGroupCollapsed(groupID)}
                  collapseDisabled={folderDragThreadID !== undefined}
                  dragDisabled={folderDragThreadID !== undefined}
                >
                  <SortableContext items={pinnedItemSortableIDs} strategy={verticalListSortingStrategy}>
                    <div
                      className="sidebar-functional-group-body session-folder-list"
                      data-folder-dragging={folderDragThreadID !== undefined || undefined}
                    >
                      {validPinnedItems.map((entry) => {
                        if (entry.kind === "thread") {
                          const thread = pinnedRows.find((candidate) => candidate.id === entry.id);
                          return thread ? (
                            <SortablePinnedThreadItem
                              key={pinnedItemSortableID(entry)}
                              id={pinnedItemSortableID(entry)}
                              sortIndicator={sidebarSortIndicator?.id === pinnedItemSortableID(entry)
                                ? sidebarSortIndicator.position
                                : undefined}
                              containerProps={pinnedSessionDropProps(pinnedItemSortableID(entry))}
                            >
                              <OrganizationThreadList
                                threads={[thread]}
                                projectSummaries={projectIndex.summaries}
                                activeID={activeThreadID}
                                pendingThreadID={pendingThreadID}
                                lastViewedTurnByThreadID={state.lastViewedTurnByThreadID}
                                onSelect={onSelectThread}
                                onTogglePinned={onTogglePinned}
                                onArchive={onArchiveThread}
                                onDelete={onDeleteThread}
                                onRename={onRenameThread}
                              />
                            </SortablePinnedThreadItem>
                          ) : null;
                        }
                        if (entry.kind === "folder") {
                          const folder = organization.folders.find((candidate) => candidate.id === entry.id);
                          return folder ? renderFolderSection(folder, true) : null;
                        }
                        const project = sidebarWorkspaces.find((candidate) => candidate.id === entry.id);
                        return project ? renderWorkspaceSection(project, true) : null;
                      })}
                      <PinnedAppendDropZone
                        nativeDropActive={sidebarSortIndicator?.id === PINNED_APPEND_DROP_ID}
                        containerProps={pinnedSessionDropProps(PINNED_APPEND_DROP_ID, "after")}
                      />
                    </div>
                  </SortableContext>
                </SortableFunctionalGroup>
              ) : groupID === "folders" ? (
                <SortableFunctionalGroup
                  key={groupID}
                  id={groupID}
                  ariaLabel={t("sidebar.folders")}
                  itemDropIndicator={sidebarSortIndicator?.id === groupID
                    ? sidebarSortIndicator.position
                    : undefined}
                  headingClassName={`sidebar-folder-heading${folderDragCanRemove ? " remove-drop-available" : ""}${folderDropTargetID === FOLDER_REMOVE_DROP_TARGET ? " drop-active" : ""}`}
                  headingProps={{
                    "data-folder-remove-drop": folderDragCanRemove || undefined,
                    onDragEnter: dragSessionOverFolderRemoval,
                    onDragOver: dragSessionOverFolderRemoval,
                    onDragLeave: (event) => leaveSessionFolderTarget(event, FOLDER_REMOVE_DROP_TARGET),
                    onDrop: dropSessionOutOfFolder,
                  }}
                  headingLabelClassName="sidebar-folder-heading-label"
                  headingLabel={(
                    <>
                      {folderDragCanRemove ? <FolderMinus aria-hidden="true" /> : null}
                      {t(folderDragThread?.pinned
                        ? "sidebar.unpin"
                        : folderDragCanRemove
                          ? "sidebar.removeFromFolderDrop"
                          : "sidebar.folders")}
                    </>
                  )}
                  collapsed={collapsedFunctionalGroupIDs.has(groupID)}
                  collapseLabel={t(
                    collapsedFunctionalGroupIDs.has(groupID)
                      ? "sidebar.expandSection"
                      : "sidebar.collapseSection",
                    { section: t("sidebar.folders") },
                  )}
                  onToggleCollapsed={() => toggleFunctionalGroupCollapsed(groupID)}
                  collapseDisabled={folderDragCanRemove}
                  dragDisabled={folderDragCanRemove}
                  action={!folderDragCanRemove ? (
                    <button
                      className="sidebar-functional-action"
                      type="button"
                      aria-label={t("sidebar.newFolder")}
                      title={t("sidebar.newFolder")}
                      onClick={() => createFolder()}
                    >
                      <Plus aria-hidden="true" />
                    </button>
                  ) : undefined}
                >
                  <div
                    className="sidebar-functional-group-body session-folder-list"
                    data-folder-dragging={folderDragThreadID !== undefined || undefined}
                  >
                    <SortableContext items={visibleFolderSortableIDs} strategy={verticalListSortingStrategy}>
                      {organization.folders
                        .filter((folder) => !pinnedFolderIDs.has(folder.id))
                        .map((folder) => renderFolderSection(folder, false))}
                    </SortableContext>
                  </div>
                </SortableFunctionalGroup>
              ) : (
                <SortableFunctionalGroup
                  key={groupID}
                  id={groupID}
                  ariaLabel={t("sidebar.workspace")}
                  itemDropIndicator={sidebarSortIndicator?.id === groupID
                    ? sidebarSortIndicator.position
                    : undefined}
                  headingClassName={folderDropTargetID === WORKSPACE_SESSION_DROP_TARGET ? "drop-active" : ""}
                  headingProps={{
                    onDragEnter: dragSessionOverWorkspace,
                    onDragOver: dragSessionOverWorkspace,
                    onDragLeave: (event) => leaveSessionFolderTarget(event, WORKSPACE_SESSION_DROP_TARGET),
                    onDrop: dropSessionIntoWorkspace,
                  }}
                  headingLabel={t("sidebar.workspace")}
                  collapsed={collapsedFunctionalGroupIDs.has(groupID)}
                  collapseLabel={t(
                    collapsedFunctionalGroupIDs.has(groupID)
                      ? "sidebar.expandSection"
                      : "sidebar.collapseSection",
                    { section: t("sidebar.workspace") },
                  )}
                  onToggleCollapsed={() => toggleFunctionalGroupCollapsed(groupID)}
                  action={(
                    <div className="sidebar-add-workspace" ref={workspaceMenuRef}>
                      <button
                        className="sidebar-functional-action"
                        type="button"
                        aria-label={t("sidebar.addWorkspace")}
                        title={t("sidebar.addWorkspace")}
                        aria-haspopup="menu"
                        aria-expanded={workspaceMenuOpen}
                        onClick={onToggleWorkspaceMenu}
                      >
                        <Plus aria-hidden="true" />
                      </button>
                      {workspaceMenuOpen ? (
                        <div className="project-add-menu" role="menu">
                          <button role="menuitem" disabled={!hostSupports("createBlankProject")} onClick={onCreateWorkspace}>
                            <FolderPlus className="icon-xl" />
                            <span>{t("sidebar.newBlankWorkspace")}</span>
                          </button>
                          <button role="menuitem" disabled={!hostSupports("chooseProjectFolder")} onClick={onOpenWorkspaceFolder}>
                            <FolderOpen className="icon-xl" />
                            <span>{t("sidebar.useExistingFolder")}</span>
                          </button>
                        </div>
                      ) : null}
                    </div>
                  )}
                >
                  {visibleWorkspaceSectionOrder.length > 0 ? (
                    <SortableContext
                      items={visibleWorkspaceSectionOrder}
                      strategy={verticalListSortingStrategy}
                    >
                      <div className="sidebar-functional-group-body">
                        {visibleWorkspaceSectionOrder.map((key) => {
                          const project = sidebarWorkspaces.find((candidate) => candidate.id === key);
                          return project ? renderWorkspaceSection(project, false) : null;
                        })}
                      </div>
                    </SortableContext>
                  ) : null}
                </SortableFunctionalGroup>
              ))}
            </SortableContext>
            <DragOverlay dropAnimation={dropAnimation}>
              {draggingFunctionalGroupID ? (
                <div className="sidebar-functional-group-drag-overlay">
                  <span>
                    {t(
                      draggingFunctionalGroupID === "projects"
                        ? "sidebar.projects"
                        : draggingFunctionalGroupID === "pinned"
                        ? "sidebar.pinned"
                        : draggingFunctionalGroupID === "folders"
                          ? "sidebar.folders"
                          : "sidebar.workspace",
                    )}
                  </span>
                </div>
              ) : draggingSectionInfo ? (
                <SidebarSectionDragPreview info={draggingSectionInfo} />
              ) : null}
            </DragOverlay>
          </DndContext>
        </div>
        <PluginSlot
          host={pluginHost}
          id="sidebar.primary"
          context={Object.freeze({
            initialized: Boolean(state.initialized),
            hasActiveThread: activeThreadID !== undefined,
            workspaceCount: sidebarWorkspaces.length,
          })}
        />
          </>
        )}
        <div className="sidebar-settings">
          <PluginSlot
            host={pluginHost}
            id="sidebar.footer"
            context={Object.freeze({ initialized: Boolean(state.initialized) })}
          />
          {sidebarVisible && <SidebarAccountMenu
            disabled={!state.initialized}
            onOpenAccount={onOpenAccount ? () => activateNative(onOpenAccount) : undefined}
            onOpenSettings={(page) => activateNative(() => onOpenSettings(page))}
          />}
        </div>
        {groupContextMenu ? (
          <ThreadContextMenu
            x={groupContextMenu.x}
            y={groupContextMenu.y}
            items={[
              {
                label: t(groupContextMenu.pinned ? "sidebar.unpin" : "sidebar.pin"),
                onSelect: () => groupContextMenu.pinned
                  ? unpinContainer({ kind: "folder", id: groupContextMenu.group.id })
                  : pinContainer({ kind: "folder", id: groupContextMenu.group.id }),
              },
              { separator: true },
              {
                label: t("sidebar.renameGroup"),
                onSelect: () => {
                  renameFolder(groupContextMenu.group);
                },
              },
              { separator: true },
              {
                label: t("sidebar.deleteGroup"),
                onSelect: () => {
                  if (!window.confirm(t("sidebar.deleteGroupConfirmation"))) return;
                  if (groupContextMenu.pinned) {
                    unpinContainer({ kind: "folder", id: groupContextMenu.group.id });
                  }
                  organization.deleteFolder(groupContextMenu.group.id);
                },
              },
            ]}
            onClose={() => setGroupContextMenu(null)}
          />
        ) : null}
        <SidebarNameDialog
          open={groupNameDialog !== null}
          title={groupName}
          onTitleChange={setGroupName}
          onSubmit={() => { void submitGroupNameDialog(); }}
          onClose={closeGroupNameDialog}
          dialogTitle={groupNameDialog?.action === "rename"
            ? t("sidebar.renameFolder")
            : t("sidebar.newFolder")}
          dialogTitleId="session-organization-name-title"
          fieldLabel={t("sidebar.folderNamePrompt")}
          fieldAriaLabel={t("sidebar.folderNamePrompt")}
          placeholder={t("sidebar.folderNamePrompt")}
          icon={Folder}
          submitLabel={t(groupNameDialog?.action === "rename" ? "common.save" : "common.create")}
          cancelLabel={t("common.cancel")}
          submitDisabled={groupNamePending || groupName.trim().length === 0 || (
            groupNameDialog?.action === "rename" && groupName.trim() === groupNameDialog.group.name.trim()
          )}
        />
      </div>
    </aside>
  );
  const organizedSidebar = (
    <SessionOrganizationProvider value={organizationActions}>
      {mobileNavigation ? <MobileSidebar
        onNavigateAway={onNavigateAway}
        visible={drawerVisible}
        state={state}
        sidebarWorkspaces={sidebarWorkspaces}
        activeThreadID={activeThreadID}
        pendingThreadID={pendingThreadID}
        workspaceThreadsByWorkspaceID={workspaceThreadsByWorkspaceID}
        loadingWorkspaceThreadIDs={loadingWorkspaceThreadIDs}
        expandedSidebarSectionIDs={expandedSidebarSectionIDs}
        onToggleSidebarSectionCollapsed={onToggleSidebarSectionCollapsed}
        onStartNewThreadInWorkspace={onStartNewThreadInWorkspace}
        onSelectWorkspaceThread={onSelectWorkspaceThread}
        onTogglePinned={toggleThreadPinned}
        onArchiveThread={onArchiveThread}
        onRenameThread={onRenameThread}
        onDeleteThread={onDeleteThread}
        onRemoveWorkspace={onRemoveWorkspace}
        onRelocateWorkspace={onRelocateWorkspace}
        onFocusWorkspace={onFocusWorkspace}
        onCreateWorkspace={onCreateWorkspace}
        onOpenWorkspaceFolder={onOpenWorkspaceFolder}
        commands={[...primaryNavigationNodes, ...pluginNavigationNodes, ...navigationNodes]}
      /> : nativeSidebar}
    </SessionOrganizationProvider>
  );
  return (
    <NavigationPresentation
      nodes={[
        ...primaryNavigationNodes,
        ...pluginNavigationNodes,
        ...navigationNodes,
      ]}
      fallback={organizedSidebar}
    />
  );
}

function PinnedAppendDropZone({
  nativeDropActive,
  containerProps,
}: {
  nativeDropActive?: boolean;
  containerProps?: HTMLAttributes<HTMLElement>;
}): JSX.Element {
  const { setNodeRef, isOver } = useDroppable({ id: PINNED_APPEND_DROP_ID });
  return (
    <div
      {...containerProps}
      ref={setNodeRef}
      className="pinned-append-drop-zone"
      data-drop-over={isOver || nativeDropActive || undefined}
      aria-hidden="true"
    />
  );
}

function SortablePinnedThreadItem({
  id,
  sortIndicator,
  containerProps,
  children,
}: {
  id: string;
  sortIndicator?: "before" | "after";
  containerProps?: HTMLAttributes<HTMLElement>;
  children: ReactNode;
}): JSX.Element {
  const {
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
    isOver,
  } = useSortable({ id, disabled: { draggable: true }, transition: useSortableTransition() });
  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };
  return (
    <div
      {...containerProps}
      ref={setNodeRef}
      className="pinned-sortable-thread"
      data-dragging={isDragging || undefined}
      data-drop-over={isOver || undefined}
      data-sort-indicator={sortIndicator}
      style={style}
      {...listeners}
    >
      {children}
    </div>
  );
}

function SortableFunctionalGroup({
  id,
  ariaLabel,
  itemDropIndicator,
  headingClassName = "",
  headingProps,
  headingLabel,
  headingLabelClassName = "",
  action,
  collapsed,
  collapseLabel,
  onToggleCollapsed,
  collapseDisabled = false,
  dragDisabled = false,
  children,
}: {
  id: SidebarFunctionalGroupID;
  ariaLabel: string;
  itemDropIndicator?: "before" | "after";
  headingClassName?: string;
  headingProps?: HTMLAttributes<HTMLDivElement> & {
    "data-folder-remove-drop"?: boolean;
  };
  headingLabel: ReactNode;
  headingLabelClassName?: string;
  action?: ReactNode;
  collapsed: boolean;
  collapseLabel: string;
  onToggleCollapsed: () => void;
  collapseDisabled?: boolean;
  dragDisabled?: boolean;
  children: ReactNode;
}): JSX.Element {
  const {
    listeners,
    setNodeRef,
    setActivatorNodeRef,
    transform,
    transition,
    isDragging,
    isOver,
  } = useSortable({ id, disabled: dragDisabled, transition: useSortableTransition() });
  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };
  return (
    <section
      ref={setNodeRef}
      className="sidebar-functional-group sidebar-functional-group-sortable"
      aria-label={ariaLabel}
      data-functional-group-id={id}
      data-dragging={isDragging || undefined}
      data-drop-over={isOver || undefined}
      data-item-drop-indicator={itemDropIndicator}
      data-drag-disabled={dragDisabled || undefined}
      style={style}
    >
      <div
        {...headingProps}
        ref={setActivatorNodeRef}
        onPointerDown={(event) => listeners?.onPointerDown?.(event)}
        className={`sidebar-functional-heading ${headingClassName}`.trim()}
      >
        <button
          className="sidebar-functional-heading-toggle"
          type="button"
          aria-expanded={!collapsed}
          aria-label={collapseLabel}
          title={collapseLabel}
          disabled={collapseDisabled}
          onClick={() => {
            if (!isDragging) onToggleCollapsed();
          }}
        >
          <span
            className={`sidebar-functional-heading-label ${headingLabelClassName}`.trim()}
          >
            {headingLabel}
          </span>
          <ChevronRight
            className="sidebar-functional-heading-chevron"
            data-expanded={!collapsed || undefined}
            aria-hidden="true"
          />
        </button>
        {action ? (
          <div
            className="sidebar-functional-heading-action"
            onPointerDown={(event) => event.stopPropagation()}
          >
            {action}
          </div>
        ) : null}
      </div>
      <SidebarCollapseBody
        className="sidebar-functional-group-collapse"
        expanded={!collapsed}
      >
        {children}
      </SidebarCollapseBody>
    </section>
  );
}

function threadNavigationNode(
  thread: ThreadSummary,
  parentId: string,
  activeThreadID: string | undefined,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
  onActivate: () => void,
  onTogglePinned: () => void,
  depth = 2,
): NavigationSourceNode {
  return {
    id: `thread:${thread.id}`,
    kind: "thread",
    label: sidebarThreadLabel(thread),
    parentId,
    depth,
    active: thread.id === activeThreadID,
    pinned: Boolean(thread.pinned),
    unread: isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]),
    running: isThreadExecuting(thread),
    onActivate,
    onTogglePinned,
  };
}

function sidebarThreadLabel(thread: ThreadSummary): string {
  return thread.title?.trim() || thread.preview?.trim() || thread.id;
}

function sidebarThreadUnread(
  thread: ThreadSummary,
  lastViewedTurnByThreadID: Readonly<Record<string, string>>,
): boolean {
  return isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
}

function sameStringSet(left: ReadonlySet<string>, right: ReadonlySet<string>): boolean {
  if (left.size !== right.size) return false;
  for (const value of left) {
    if (!right.has(value)) return false;
  }
  return true;
}

function AttentionSection({
  headingID,
  title,
  count,
  children,
}: {
  headingID: string;
  title: string;
  count: number;
  children: ReactNode;
}): JSX.Element {
  return (
    <section className="sidebar-attention-section" aria-labelledby={headingID}>
      <header className="sidebar-unread-heading" id={headingID}>
        <span>{title}</span>
        <span className="sidebar-unread-count" aria-live="polite">
          {count}
        </span>
      </header>
      <div className="sidebar-unread-list">{children}</div>
    </section>
  );
}

function AttentionThreadRow({
  thread,
  running = false,
  unread = false,
  active = false,
  pending = false,
  onSelect,
}: {
  thread: ThreadSummary;
  running?: boolean;
  unread?: boolean;
  active?: boolean;
  pending?: boolean;
  onSelect: (id: string) => void;
}): JSX.Element {
  const label = sidebarThreadLabel(thread);
  const className = [
    "thread-row",
    "sidebar-session-row",
    "sidebar-unread-row",
    running ? "running" : "",
    unread ? "has-unread" : "",
    active ? "active" : "",
    pending ? "pending-switch" : "",
  ].filter(Boolean).join(" ");
  return (
    <div className={className} aria-current={active ? "page" : undefined}>
      {running ? <span className="thread-row-spinner" aria-hidden="true" /> : null}
      <button
        className="thread-row-main"
        type="button"
        aria-busy={running || undefined}
        aria-label={label}
        onClick={() => onSelect(thread.id)}
      >
        <span className="thread-row-title">{label}</span>
      </button>
    </div>
  );
}
