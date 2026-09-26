import { hostSupports } from "./HostCapabilities";
import { Archive, Folder, FolderOpen, MessageSquare, MessageSquarePlus, MessagesSquare, Pin, Split } from "./WuuIcons";
import {
  type DragEvent as ReactDragEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { SidebarNameDialog } from "./SidebarNameDialog";
import type { DesktopProject, RuntimeContext } from "../shared/protocol";
import {
  copyToClipboard,
  ThreadContextMenu,
  type ThreadContextMenuItem,
} from "./ThreadContextMenu";
import { SidebarSection } from "./SidebarSection";
import { SESSION_FOLDER_DRAG_MIME, useSessionOrganizationActions } from "./SessionOrganization";
import { baseThreadTitle, threadShowsForkMarker } from "./ThreadTitles";
import { revealInFileManagerLabel } from "./platform";
import {
  isThreadExecuting,
  isThreadRunning,
  isThreadUnread,
  threadBelongsToWorkspace,
  type ThreadSummary,
} from "./AppState";
import { resolveLocalizedText, useI18n } from "./i18n";

function unpinnedThreads(threads: ThreadSummary[]): ThreadSummary[] {
  return threads.filter((thread) => !thread.pinned);
}

const PROJECT_THREAD_INITIAL_VISIBLE_COUNT = 5;
const PROJECT_THREAD_VISIBLE_INCREMENT = 10;
const SIDEBAR_THREAD_ORDER_KEY = "wuu.desktop.sidebarThreadOrderByWorkspace";
const PINNED_THREAD_ORDER_ID = "__wuu_pinned_threads__";

type SidebarThreadOrderByWorkspace = Record<string, string[]>;

function storedThreadOrder(workspaceID: string): string[] {
  try {
    const stored = window.localStorage.getItem(SIDEBAR_THREAD_ORDER_KEY);
    const parsed: unknown = stored ? JSON.parse(stored) : {};
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return [];
    }
    const order = (parsed as SidebarThreadOrderByWorkspace)[workspaceID];
    return Array.isArray(order)
      ? order.filter((id): id is string => typeof id === "string" && id.length > 0)
      : [];
  } catch {
    return [];
  }
}

function persistThreadOrder(workspaceID: string, order: string[]): void {
  try {
    const stored = window.localStorage.getItem(SIDEBAR_THREAD_ORDER_KEY);
    const parsed: unknown = stored ? JSON.parse(stored) : {};
    const current = parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? parsed as SidebarThreadOrderByWorkspace
      : {};
    window.localStorage.setItem(
      SIDEBAR_THREAD_ORDER_KEY,
      JSON.stringify({ ...current, [workspaceID]: order }),
    );
  } catch {
    // A blocked or full localStorage should not prevent in-memory reordering.
  }
}

type ThreadDropPosition = "before" | "after";

function moveThreadInOrder(
  order: string[],
  activeThreadID: string,
  targetThreadID: string,
  position: ThreadDropPosition,
): string[] {
  if (activeThreadID === targetThreadID) return order;
  const withoutActive = order.filter((id) => id !== activeThreadID);
  const targetIndex = withoutActive.indexOf(targetThreadID);
  if (targetIndex < 0 || withoutActive.length === order.length) return order;
  withoutActive.splice(targetIndex + (position === "after" ? 1 : 0), 0, activeThreadID);
  return withoutActive.every((id, index) => id === order[index]) ? order : withoutActive;
}

function reconcileThreadOrder(threads: ThreadSummary[], storedOrder: string[]): string[] {
  const threadIDs = new Set(threads.map((thread) => thread.id));
  const known = storedOrder.filter((id) => threadIDs.has(id));
  const knownIDs = new Set(known);
  const newIDs = threads
    .map((thread) => thread.id)
    .filter((id) => !knownIDs.has(id));
  return [...newIDs, ...known];
}

export function WorkspaceList({
  projects,
  activeID,
  pendingWorkspaceID,
  expandedSidebarSectionIDs,
  threadsByWorkspaceID,
  activeThreadID,
  pendingThreadID,
  lastViewedTurnByThreadID,
  scratchPseudoWorkspaceID,
  scratchPseudoActive,
  onToggleSidebarSectionCollapsed,
  onStartNewThread,
  onSelectThread,
  onToggleThreadPinned,
  onArchiveThread,
  onDeleteThread,
  onRenameThread,
}: {
  projects: DesktopProject[];
  activeID?: string;
  pendingWorkspaceID?: string;
  expandedSidebarSectionIDs: Set<string>;
  threadsByWorkspaceID: Record<string, ThreadSummary[]>;
  activeThreadID?: string;
  pendingThreadID?: string;
  lastViewedTurnByThreadID: Record<string, string>;
  // The scratch pseudo project lives at the top of the sidebar tree and
  // groups all no-project (scratch) conversations under one collapsible
  // header, just like a real project. App.tsx injects a synthetic
  // DesktopProject with this id; AppSidebar passes it down so the row can
  // render a chat-bubble icon instead of a folder and so the row's
  // "active" highlight can be driven by the runtime context kind
  // (no_project), which is not represented in DesktopProject itself.
  scratchPseudoWorkspaceID: string;
  scratchPseudoActive: boolean;
  onToggleSidebarSectionCollapsed: (id: string) => void;
  onStartNewThread: (id: string) => void;
  onSelectThread: (workspaceID: string, threadID: string) => void;
  onToggleThreadPinned: (thread: ThreadSummary) => void;
  onArchiveThread: (thread: ThreadSummary) => void;
  onDeleteThread: (thread: ThreadSummary) => void;
  onRenameThread?: (thread: ThreadSummary, title: string) => void;
  
}): JSX.Element {
  return (
    <div className="projects">
      {projects.map((project) => (
        <WorkspaceGroup
          key={project.id}
          project={project}
          activeID={activeID}
          pendingWorkspaceID={pendingWorkspaceID}
          expandedSidebarSectionIDs={expandedSidebarSectionIDs}
          threadsByWorkspaceID={threadsByWorkspaceID}
          activeThreadID={activeThreadID}
          pendingThreadID={pendingThreadID}
          lastViewedTurnByThreadID={lastViewedTurnByThreadID}
          scratchPseudoWorkspaceID={scratchPseudoWorkspaceID}
          scratchPseudoActive={scratchPseudoActive}
          onToggleSidebarSectionCollapsed={onToggleSidebarSectionCollapsed}
          onStartNewThread={onStartNewThread}
          onSelectThread={onSelectThread}
          onToggleThreadPinned={onToggleThreadPinned}
          onArchiveThread={onArchiveThread}
          onDeleteThread={onDeleteThread}
          onRenameThread={onRenameThread}
          
        />
      ))}
    </div>
  );
}

export type PendingConversation = {
  id: string;
  context: RuntimeContext;
  title: string;
};

/**
 * Single project (or scratch pseudo) collapsible group. AppSidebar renders
 * one of these per reorderable section key. Visual/behavioral parity with
 * the legacy `WorkspaceList` map: the same `project-row` anatomy, the same
 * `thread-list-collapse` unfurl animation, the same unread/active classes.
 */
export function WorkspaceGroup({
  project,
  activeID,
  pendingConversations = [],
  activeSessionTabID,
  onSelectPendingConversation,
  pendingWorkspaceID,
  expandedSidebarSectionIDs,
  loadingWorkspaceThreadIDs,
  threadsByWorkspaceID,
  activeThreadID,
  pendingThreadID,
  lastViewedTurnByThreadID,
  scratchPseudoWorkspaceID,
  scratchPseudoActive,
  onToggleSidebarSectionCollapsed,
  onFocusWorkspace,
  onStartNewThread,
  onSelectThread,
  onToggleThreadPinned,
  onArchiveThread,
  onDeleteThread,
  onRenameThread,
  
  onRemoveWorkspace,
  onRelocateWorkspace,
  workspacePinned = false,
  onToggleWorkspacePinned,
}: {
  project: DesktopProject;
  activeID?: string;
  pendingConversations?: readonly PendingConversation[];
  activeSessionTabID?: string;
  onSelectPendingConversation?: (id: string) => void;
  pendingWorkspaceID?: string;
  expandedSidebarSectionIDs: ReadonlySet<string>;
  loadingWorkspaceThreadIDs?: ReadonlySet<string>;
  threadsByWorkspaceID: Record<string, ThreadSummary[]>;
  activeThreadID?: string;
  pendingThreadID?: string;
  lastViewedTurnByThreadID: Record<string, string>;
  scratchPseudoWorkspaceID: string;
  scratchPseudoActive: boolean;
  onToggleSidebarSectionCollapsed: (id: string) => void;
  onFocusWorkspace?: (id: string) => void;
  onStartNewThread: (id: string) => void;
  onSelectThread: (workspaceID: string, threadID: string) => void;
  onToggleThreadPinned: (thread: ThreadSummary) => void;
  onArchiveThread: (thread: ThreadSummary) => void;
  onDeleteThread: (thread: ThreadSummary) => void;
  onRenameThread?: (thread: ThreadSummary, title: string) => void;
  
  // Remove a real workspace from the sidebar. Absent for the 对话 scratch
  // pseudo project (and never wired for 群聊 / Agents, which are separate
  // sections), so those can never be removed.
  onRemoveWorkspace?: (id: string) => void;
  // Point a real workspace at a new folder (keeping its stable id, so its
  // state and history reconnect). The remedy for a moved/deleted directory.
  onRelocateWorkspace?: (id: string) => void;
  workspacePinned?: boolean;
  onToggleWorkspacePinned?: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [visibleThreadCount, setVisibleThreadCount] = useState<number>(
    PROJECT_THREAD_INITIAL_VISIBLE_COUNT,
  );
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
  } | null>(null);

  function showMoreWorkspaceThreads(): void {
    setVisibleThreadCount(
      (current) => current + PROJECT_THREAD_VISIBLE_INCREMENT,
    );
  }

  function collapseWorkspaceThreads(): void {
    setVisibleThreadCount(PROJECT_THREAD_INITIAL_VISIBLE_COUNT);
  }

  const pendingWorkspace = pendingWorkspaceID === project.id;
  const loadingWorkspaceThreads = loadingWorkspaceThreadIDs?.has(project.id) ?? false;
  const isScratchPseudo = project.id === scratchPseudoWorkspaceID;
  // A real workspace whose directory was moved away or deleted. Its "新建会话"
  // affordance is disabled so no session can be created in a cwd that is gone.
  const isMissing = !isScratchPseudo && project.missing === true;
  const workspaceSelectionMode =
    !isScratchPseudo && !isMissing && Boolean(onFocusWorkspace);
  const activeWorkspace = isScratchPseudo
    ? scratchPseudoActive
    : project.id === activeID;
  // Expansion is purely manual: only the header toggle mutates it.
  // Selecting a session (from anywhere — 置顶 included) or switching the
  // active context never expands or collapses a section; `activeWorkspace`
  // above is kept solely for the header highlight.
  const expanded = expandedSidebarSectionIDs.has(project.id);
  // The scratch pseudo project trusts the threadsByWorkspaceID entry
  // directly: App.tsx already filtered scratch threads. Real
  // projects use stable workspace identity, falling back to paths for legacy
  // sessions, so local forks inside worktrees remain in their owning project.
  const sourceWorkspaceThreads = threadsByWorkspaceID[project.id];
  const unorderedWorkspaceThreads = useMemo(
    () =>
      unpinnedThreads(
        isScratchPseudo
          ? sourceWorkspaceThreads ?? []
          : (sourceWorkspaceThreads ?? []).filter((thread) => threadBelongsToWorkspace(thread, project)),
      ),
    [isScratchPseudo, project.id, project.path, sourceWorkspaceThreads],
  );
  const [threadOrder, setThreadOrder] = useState<string[]>(() =>
    storedThreadOrder(project.id),
  );
  const reconciledThreadOrder = useMemo(
    () => reconcileThreadOrder(unorderedWorkspaceThreads, threadOrder),
    [threadOrder, unorderedWorkspaceThreads],
  );
  const workspaceThreads = useMemo(() => {
    const threadsByID = new Map(
      unorderedWorkspaceThreads.map((thread) => [thread.id, thread]),
    );
    return reconciledThreadOrder
      .map((id) => threadsByID.get(id))
      .filter((thread): thread is ThreadSummary => thread !== undefined);
  }, [reconciledThreadOrder, unorderedWorkspaceThreads]);

  function reorderWorkspaceThreads(
    activeThreadID: string,
    overThreadID: string,
    position: ThreadDropPosition,
  ): void {
    const next = moveThreadInOrder(reconciledThreadOrder, activeThreadID, overThreadID, position);
    if (next === reconciledThreadOrder) return;
    setThreadOrder(next);
    persistThreadOrder(project.id, next);
  }
  const workspaceHasUnread = workspaceThreads.some((thread) =>
    workspaceThreadUnread(
      thread,
      activeThreadID,
      pendingThreadID,
      lastViewedTurnByThreadID,
    ),
  );
  const workspaceHasRunning = pendingConversations.length > 0 || workspaceThreads.some((thread) =>
    isThreadExecuting(thread),
  );
  const CollapsedIcon = isScratchPseudo ? MessageSquare : Folder;
  const ExpandedIcon = isScratchPseudo ? MessagesSquare : FolderOpen;
  return (
    <div
      className={`project-group${isMissing ? " project-group-missing" : ""}`}
      data-section-id={project.id}
      data-missing={isMissing || undefined}
    >
      <SidebarSection
        expanded={workspaceSelectionMode ? false : expanded}
        iconKind={isScratchPseudo ? "conversation" : "project"}
        CollapsedIcon={CollapsedIcon}
        ExpandedIcon={ExpandedIcon}
        label={project.name}
        ariaLabel={
          workspaceSelectionMode
            ? t("threadSidebar.openWorkspace", { name: project.name })
            : t(
                expanded
                  ? "threadSidebar.collapseWorkspace"
                  : "threadSidebar.expandWorkspace",
                {
                  name: project.name,
                  unread: workspaceHasUnread ? t("threadSidebar.hasUnread") : "",
                },
              )
        }
        title={
          workspaceSelectionMode
            ? t("threadSidebar.openWorkspace", { name: project.name })
            : t(
                expanded
                  ? "threadSidebar.collapseConversations"
                  : "threadSidebar.expandConversations",
              )
        }
        active={activeWorkspace}
        pending={pendingWorkspace}
        running={workspaceHasRunning}
        unread={workspaceHasUnread}
        loading={pendingWorkspace || loadingWorkspaceThreads}
        onToggle={() =>
          workspaceSelectionMode
            ? onFocusWorkspace?.(project.id)
            : onToggleSidebarSectionCollapsed(project.id)
        }
        onContextMenu={
          !onRemoveWorkspace && !onRelocateWorkspace && !onToggleWorkspacePinned
            ? undefined
            : isScratchPseudo && !onToggleWorkspacePinned
              ? undefined
              : (event) => {
                  event.preventDefault();
                  setContextMenu({ x: event.clientX, y: event.clientY });
                }
        }
        newItemButton={
          <button
            className="sidebar-row-icon-button project-row-new-thread"
            type="button"
            aria-label={t("threadSidebar.newInWorkspace", { name: project.name })}
            title={
              isMissing
                ? t("threadSidebar.missingWorkspace")
                : t("sidebar.newConversation")
            }
            disabled={isMissing || (isScratchPseudo && !hostSupports("createBlankProject"))}
            onClick={() => onStartNewThread(project.id)}
          >
            <MessageSquarePlus className="icon" />
          </button>
        }
        emptyNote={
          loadingWorkspaceThreads
            ? t("threadSidebar.loadingConversations")
            : undefined
        }
      >
        {pendingConversations.length > 0 || workspaceThreads.length > 0 ? (
          <>
            {pendingConversations.length > 0 ? (
              <div className="thread-list">
                {pendingConversations.map((pending) => (
                  <div key={pending.id} className={`thread-row sidebar-session-row running${pending.id === activeSessionTabID ? " active" : ""}`}>
                    <span className="thread-row-spinner" aria-hidden="true" />
                    <button
                      className="thread-row-main"
                      type="button"
                      aria-busy="true"
                      onClick={() => onSelectPendingConversation?.(pending.id)}
                    >
                      <ThreadRowTitle title={pending.title} />
                    </button>
                  </div>
                ))}
              </div>
            ) : null}
            {workspaceThreads.length === 0 ? null : (
              <ThreadList
                threads={workspaceThreads}
                activeID={activeThreadID}
                pendingThreadID={pendingThreadID}
                lastViewedTurnByThreadID={lastViewedTurnByThreadID}
                visibleCount={visibleThreadCount}
                onSelect={(threadID) => onSelectThread(project.id, threadID)}
                onTogglePinned={onToggleThreadPinned}
                onArchive={onArchiveThread}
                onDelete={onDeleteThread}
                onRename={onRenameThread}
                onReorder={reorderWorkspaceThreads}
                onShowMore={showMoreWorkspaceThreads}
                onCollapse={collapseWorkspaceThreads}
              />
            )}
          </>
        ) : null}
      </SidebarSection>
      {contextMenu ? (
        <ThreadContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={[
            ...(onToggleWorkspacePinned ? [{
              label: t(workspacePinned ? "sidebar.unpin" : "sidebar.pin"),
              onSelect: () => onToggleWorkspacePinned(project.id),
            }, ...(!isScratchPseudo ? [{ separator: true } as const] : [])] : []),
            ...(!isScratchPseudo ? [{
              label: t("threadSidebar.relocate"),
              disabled: !onRelocateWorkspace || !hostSupports("relocateProject"),
              onSelect: () => onRelocateWorkspace?.(project.id),
            }, {
              label: t("threadSidebar.removeWorkspace"),
              disabled: !onRemoveWorkspace || !hostSupports("removeProject"),
              onSelect: () => onRemoveWorkspace?.(project.id),
            }] : []),
          ]}
          onClose={() => setContextMenu(null)}
        />
      ) : null}
    </div>
  );
}

/**
 * Renders the dual collapsed/expanded icon pair that lives inside every
 * section header (project, 对话/chat scratch, pinned, agents). The pair is
 * always rendered together; CSS crossfades between them via
 * `.project-row-icon-state` rules in sidebar.css. We render both icons
 * unconditionally rather than swapping them so the cross-fade transition stays
 * smooth without a mount/unmount flicker.
 *
 * Every pair follows the same single → many / closed → open metaphor:
 * Agents uses UserRound → UsersRound, 对话 uses MessageSquare →
 * MessagesSquare, projects use Folder → FolderOpen; the pinned section
 * keeps one Pin glyph and rotates the expanded state -45deg via CSS.
 */
export function SectionRowIcon({
  collapsed,
  iconKind,
  CollapsedIcon,
  ExpandedIcon,
}: {
  collapsed: boolean;
  iconKind: string;
  CollapsedIcon: React.ComponentType<{ className?: string }>;
  ExpandedIcon: React.ComponentType<{ className?: string }>;
}): JSX.Element {
  return (
    <span
      className={`project-row-icon${collapsed ? "" : " expanded"}`}
      aria-hidden="true"
    >
      <CollapsedIcon
        className="icon-lg project-row-icon-state collapsed"
        data-project-icon-kind={iconKind}
        data-project-icon-state="collapsed"
      />
      <ExpandedIcon
        className="icon-lg project-row-icon-state expanded"
        data-project-icon-kind={iconKind}
        data-project-icon-state="expanded"
      />
    </span>
  );
}

function ThreadList({
  threads,
  activeID,
  pendingThreadID,
  lastViewedTurnByThreadID,
  visibleCount,
  onSelect,
  onTogglePinned,
  onArchive,
  onDelete,
  onRename,
  onReorder,
  onShowMore,
  onCollapse
}: {
  threads: ThreadSummary[];
  activeID?: string;
  pendingThreadID?: string;
  lastViewedTurnByThreadID: Record<string, string>;
  visibleCount: number;
  onSelect: (id: string) => void;
  onTogglePinned: (thread: ThreadSummary) => void;
  onArchive: (thread: ThreadSummary) => void;
  onDelete: (thread: ThreadSummary) => void;
  onRename?: (thread: ThreadSummary, title: string) => void;
  onReorder: (
    activeThreadID: string,
    overThreadID: string,
    position: ThreadDropPosition,
  ) => void;
  onShowMore: () => void;
  onCollapse: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [stickyVisibleThreadIDs, setStickyVisibleThreadIDs] = useState<
    Set<string>
  >(() => new Set());
  const visibleThreads = threads;
  const stickyVisibilityRevision = JSON.stringify(
    visibleThreads.map((thread) => [
      thread.id,
      importantThreadVisible(thread, activeID, pendingThreadID),
    ]),
  );
  useEffect(() => {
    const validIDs = new Set(visibleThreads.map((thread) => thread.id));
    setStickyVisibleThreadIDs((current) => {
      const next = new Set<string>();
      for (const id of current) {
        if (validIDs.has(id)) {
          next.add(id);
        }
      }
      for (const thread of visibleThreads) {
        if (importantThreadVisible(thread, activeID, pendingThreadID)) {
          next.add(thread.id);
        }
      }
      return sameStringSet(current, next) ? current : next;
    });
  }, [stickyVisibilityRevision]);
  const limitedThreads = limitedWorkspaceThreads(
    visibleThreads,
    visibleCount,
    activeID,
    pendingThreadID,
    lastViewedTurnByThreadID,
    stickyVisibleThreadIDs,
  );
  const hiddenCount = visibleThreads.length - limitedThreads.length;
  const expanded = visibleCount > PROJECT_THREAD_INITIAL_VISIBLE_COUNT;
  const showFooter = hiddenCount > 0 || expanded;

  function collapseVisibleThreads(): void {
    setStickyVisibleThreadIDs(new Set());
    onCollapse();
  }

  return (
    <div className="thread-list">
      <ThreadRows
        threads={limitedThreads}
        activeID={activeID}
        pendingThreadID={pendingThreadID}
        lastViewedTurnByThreadID={lastViewedTurnByThreadID}
        onSelect={onSelect}
        onTogglePinned={onTogglePinned}
        onArchive={onArchive}
        onDelete={onDelete}
        onRename={onRename}
        onReorder={onReorder}
      />
      {showFooter ? (
        <div className="thread-list-footer">
          {hiddenCount > 0 ? (
            <button className="thread-list-more" type="button" onClick={onShowMore}>
              {t("common.expand")}
            </button>
          ) : null}
          {expanded ? (
            <button
              className="thread-list-collapse-btn"
              type="button"
              onClick={collapseVisibleThreads}
              aria-label={t("threadSidebar.collapseExpanded")}
              title={t("common.collapse")}
            >
              {t("common.collapse")}
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function limitedWorkspaceThreads(
  threads: ThreadSummary[],
  visibleCount: number,
  activeID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Record<string, string> = {},
  stickyVisibleThreadIDs: ReadonlySet<string> = new Set(),
): ThreadSummary[] {
  const visibleIDs = new Set(threads.slice(0, Math.max(0, visibleCount)).map((thread) => thread.id));
  return threads.filter((thread) => {
    if (
      visibleIDs.has(thread.id) ||
      stickyVisibleThreadIDs.has(thread.id) ||
      importantThreadVisible(thread, activeID, pendingThreadID) ||
      workspaceThreadUnread(
        thread,
        activeID,
        pendingThreadID,
        lastViewedTurnByThreadID,
      )
    ) {
      return true;
    }
    return false;
  });
}

function sameStringSet(left: ReadonlySet<string>, right: ReadonlySet<string>): boolean {
  if (left.size !== right.size) {
    return false;
  }
  for (const value of left) {
    if (!right.has(value)) {
      return false;
    }
  }
  return true;
}

function importantThreadVisible(
  thread: ThreadSummary,
  activeID: string | undefined,
  pendingThreadID: string | undefined
): boolean {
  return (
    thread.id === activeID ||
    thread.id === pendingThreadID ||
    isThreadExecuting(thread)
  );
}

function workspaceThreadUnread(
  thread: ThreadSummary,
  activeID: string | undefined,
  pendingThreadID: string | undefined,
  lastViewedTurnByThreadID: Record<string, string>,
): boolean {
  return (
    !isThreadExecuting(thread) &&
    thread.id !== activeID &&
    thread.id !== pendingThreadID &&
    isThreadUnread(
      thread,
      lastViewedTurnByThreadID[thread.id],
    )
  );
}

function ThreadRows({
  threads,
  activeID,
  pendingThreadID,
  lastViewedTurnByThreadID,
  onSelect,
  onTogglePinned,
  onArchive,
  onDelete,
  onRename,
  onReorder,
}: {
  threads: ThreadSummary[];
  activeID?: string;
  pendingThreadID?: string;
  lastViewedTurnByThreadID: Record<string, string>;
  onSelect: (id: string) => void;
  onTogglePinned: (thread: ThreadSummary) => void;
  onArchive: (thread: ThreadSummary) => void;
  onDelete: (thread: ThreadSummary) => void;
  onRename?: (thread: ThreadSummary, title: string) => void;
  onReorder?: (
    activeThreadID: string,
    overThreadID: string,
    position: ThreadDropPosition,
  ) => void;
}): JSX.Element {
  const { t } = useI18n();
  const organization = useSessionOrganizationActions();
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
    thread: ThreadSummary;
    mode: "all" | "folder";
  } | null>(null);
  const [renameDialog, setRenameDialog] = useState<{
    thread: ThreadSummary;
    initialTitle: string;
  } | null>(null);
  const [renameTitle, setRenameTitle] = useState("");
  const [draggingThreadID, setDraggingThreadID] = useState<string>();
  const [threadSortIndicator, setThreadSortIndicator] = useState<{
    id: string;
    position: ThreadDropPosition;
  }>();

  function handleContextMenu(
    targetThread: ThreadSummary,
    e: { clientX: number; clientY: number; preventDefault: () => void }
  ): void {
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY, thread: targetThread, mode: "all" });
  }

  function togglePinned(thread: ThreadSummary): void {
    if (organization) {
      organization.togglePinned(thread, onTogglePinned);
    } else {
      onTogglePinned(thread);
    }
  }

  function folderOrganizationMenuItems(thread: ThreadSummary): ThreadContextMenuItem[] {
    if (!organization) return [];
    const items: ThreadContextMenuItem[] = [];
    if (organization.folderByThreadID[thread.id]) {
      items.push({
        label: t("threadSidebar.removeFromFolder"),
        onSelect: () => organization.moveToFolder(thread),
      });
    }
    for (const folder of organization.folders) {
      items.push({
        label: t("threadSidebar.moveToFolder", { name: folder.name }),
        disabled: organization.folderByThreadID[thread.id] === folder.id,
        onSelect: () => organization.moveToFolder(thread, folder.id),
      });
    }
    items.push({
      label: t("threadSidebar.newFolder"),
      onSelect: () => organization.createFolderForThread(thread),
    });
    return items;
  }

  function organizationMenuItems(thread: ThreadSummary): ThreadContextMenuItem[] {
    if (!organization) return [];
    const items: ThreadContextMenuItem[] = [{ separator: true }];
    items.push(...folderOrganizationMenuItems(thread));
    return items;
  }

  function startThreadDrag(thread: ThreadSummary, event: ReactDragEvent<HTMLDivElement>): void {
    const interactiveAction = (event.target as HTMLElement).closest(".thread-row-actions");
    if (interactiveAction) {
      event.preventDefault();
      return;
    }
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData(SESSION_FOLDER_DRAG_MIME, thread.id);
    event.dataTransfer.setDragImage(event.currentTarget, 24, event.currentTarget.offsetHeight / 2);
    setDraggingThreadID(thread.id);
    setThreadSortIndicator(undefined);
    organization?.startFolderDrag(thread.id);
  }

  function dragThreadOver(thread: ThreadSummary, event: ReactDragEvent<HTMLDivElement>): void {
    if (!onReorder || !draggingThreadID || draggingThreadID === thread.id) return;
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = "move";
    const rect = event.currentTarget.getBoundingClientRect();
    setThreadSortIndicator({
      id: thread.id,
      position: event.clientY < rect.top + rect.height / 2 ? "before" : "after",
    });
  }

  function leaveThreadDropTarget(threadID: string, event: ReactDragEvent<HTMLDivElement>): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
    setThreadSortIndicator((current) => current?.id === threadID ? undefined : current);
  }

  function dropThread(thread: ThreadSummary, event: ReactDragEvent<HTMLDivElement>): void {
    if (!onReorder || !draggingThreadID || draggingThreadID === thread.id) return;
    event.preventDefault();
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    const position = event.clientY < rect.top + rect.height / 2 ? "before" : "after";
    onReorder(draggingThreadID, thread.id, position);
    setThreadSortIndicator(undefined);
  }

  function endThreadDrag(): void {
    setDraggingThreadID(undefined);
    setThreadSortIndicator(undefined);
    organization?.endFolderDrag();
  }

  function editableThreadTitle(thread: ThreadSummary): string {
    return (
      thread.title?.trim() ||
      resolveLocalizedText(thread.preview?.trim() ?? "") ||
      ""
    );
  }

  function openRenameDialog(thread: ThreadSummary): void {
    if (!onRename) {
      return;
    }
    const title = editableThreadTitle(thread);
    setContextMenu(null);
    setRenameTitle(title);
    setRenameDialog({ thread, initialTitle: title });
  }

  function closeRenameDialog(): void {
    setRenameDialog(null);
    setRenameTitle("");
  }

  function submitRenameDialog(): void {
    if (!renameDialog) {
      return;
    }
    const trimmed = renameTitle.trim();
    if (trimmed && trimmed !== renameDialog.initialTitle.trim()) {
      onRename?.(renameDialog.thread, trimmed);
    }
    closeRenameDialog();
  }

  return (
    <>
      {threads.map((thread) => {
        const pendingSwitch = pendingThreadID === thread.id;
        const running = isThreadExecuting(thread);
        const title = baseThreadTitle(thread, threads);
        const forkMarker = threadShowsForkMarker(thread, threads);
        const unread =
          !running &&
          !pendingSwitch &&
          thread.id !== activeID &&
          isThreadUnread(
            thread,
            lastViewedTurnByThreadID[thread.id],
          );
        return (
          <div
            key={thread.id}
            className={`thread-row sidebar-session-row ${thread.id === activeID ? "active" : ""}${running ? " running" : ""}${
              pendingSwitch ? " pending-switch" : ""
            }${unread ? " has-unread" : ""}${draggingThreadID === thread.id ? " dragging" : ""}`}
            aria-current={thread.id === activeID ? "page" : undefined}
            draggable={Boolean(organization || onReorder)}
            data-draggable={Boolean(organization || onReorder) || undefined}
            data-sortable={Boolean(onReorder) || undefined}
            data-sort-indicator={threadSortIndicator?.id === thread.id
              ? threadSortIndicator.position
              : undefined}
            onContextMenu={(e) => handleContextMenu(thread, e)}
            onDragStart={(event) => startThreadDrag(thread, event)}
            onDragEnter={(event) => dragThreadOver(thread, event)}
            onDragOver={(event) => dragThreadOver(thread, event)}
            onDragLeave={(event) => leaveThreadDropTarget(thread.id, event)}
            onDrop={(event) => dropThread(thread, event)}
            onDragEnd={endThreadDrag}
          >
              {running ? (
                <span className="thread-row-spinner" aria-hidden="true" />
              ) : null}
              <button
                className="thread-row-main"
                type="button"
                aria-busy={pendingSwitch || running}
                aria-label={t("threadSidebar.threadStatus", {
                  title,
                  fork: forkMarker ? t("threadSidebar.forked") : "",
                  status: running
                    ? t("threadSidebar.responding")
                    : t("threadSidebar.completed"),
                })}
                onClick={() => onSelect(thread.id)}
                onDoubleClick={() => openRenameDialog(thread)}
              >
                <ThreadRowTitle title={title} />
              </button>
              {forkMarker ? (
                <Split
                  className="icon-sm thread-row-fork-icon"
                  aria-hidden="true"
                />
              ) : null}
              <div
                className="thread-row-actions"
                aria-label={t("threadSidebar.actions")}
              >
                <button
                  className={`sidebar-row-icon-button thread-row-action ${thread.pinned ? "active" : ""}`}
                  type="button"
                  aria-label={t(thread.pinned ? "sidebar.unpin" : "sidebar.pin")}
                  title={t(thread.pinned ? "sidebar.unpin" : "sidebar.pin")}
                  onClick={() => togglePinned(thread)}
                >
                  <Pin className="icon-sm" />
                </button>
                <button
                  className="sidebar-row-icon-button thread-row-action archive"
                  type="button"
                  aria-label={t("sidebar.archiveAction")}
                  title={t("sidebar.archiveAction")}
                  onClick={() => onArchive(thread)}
                >
                  <Archive className="icon-sm" />
                </button>
              </div>
          </div>
        );
      })}
      {contextMenu ? (
        <ThreadContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={contextMenu.mode === "folder" ? folderOrganizationMenuItems(contextMenu.thread) : [
            {
              label: t(contextMenu.thread.pinned ? "sidebar.unpin" : "sidebar.pin"),
              onSelect: () => togglePinned(contextMenu.thread),
            },
            ...organizationMenuItems(contextMenu.thread),
            { separator: true },
            {
              label: t("threadSidebar.rename"),
              disabled: !onRename,
              onSelect: () => openRenameDialog(contextMenu.thread),
            },
            {
              label: t("threadSidebar.copyWorkingDirectory"),
              onSelect: () => {
                void copyToClipboard(contextMenu.thread.cwd);
              },
            },
            {
              label: revealInFileManagerLabel(),
              disabled: !hostSupports("revealSession"),
              onSelect: () => {
                void window.wuu.revealSession(contextMenu.thread.id);
              },
            },
            {
              label: t("threadSidebar.copyConversationID"),
              onSelect: () => {
                void copyToClipboard(contextMenu.thread.id);
              },
            },
            { separator: true },
            {
              label: t("threadSidebar.delete"),
              // A running thread cannot be deleted (the server also rejects
              // it); disable the entry so the confirm dialog never promises
              // a deletion that will fail.
              disabled: isThreadRunning(contextMenu.thread),
              onSelect: () => {
                if (!window.confirm(t("threadSidebar.deleteConfirmation"))) {
                  return;
                }
                onDelete(contextMenu.thread);
              },
            },
          ]}
          onClose={() => setContextMenu(null)}
        />
      ) : null}
      <SidebarNameDialog
        open={renameDialog !== null}
        title={renameTitle}
        onTitleChange={setRenameTitle}
        onSubmit={submitRenameDialog}
        onClose={closeRenameDialog}
        dialogTitle={t("threadSidebar.rename")}
        dialogTitleId="thread-rename-title"
        fieldLabel={t("threadSidebar.title")}
        fieldAriaLabel={t("threadSidebar.title")}
        placeholder={t("threadSidebar.title")}
        icon={MessageSquare}
        submitLabel={t("common.save")}
        cancelLabel={t("common.cancel")}
      />
    </>
  );
}

export function PinnedThreadList({
  threads,
  activeID,
  pendingThreadID,
  lastViewedTurnByThreadID,
  onSelect,
  onTogglePinned,
  onArchive,
  onDelete,
  onRename,
  
}: {
  threads: ThreadSummary[];
  activeID?: string;
  pendingThreadID?: string;
  lastViewedTurnByThreadID: Record<string, string>;
  onSelect: (id: string) => void;
  onTogglePinned: (thread: ThreadSummary) => void;
  onArchive: (thread: ThreadSummary) => void;
  onDelete: (thread: ThreadSummary) => void;
  onRename?: (thread: ThreadSummary, title: string) => void;
  
}): JSX.Element {
  const [threadOrder, setThreadOrder] = useState<string[]>(() =>
    storedThreadOrder(PINNED_THREAD_ORDER_ID),
  );
  const reconciledThreadOrder = useMemo(
    () => reconcileThreadOrder(threads, threadOrder),
    [threadOrder, threads],
  );
  const threadsByID = new Map(threads.map((thread) => [thread.id, thread]));
  const orderedThreads = reconciledThreadOrder
    .map((id) => threadsByID.get(id))
    .filter((thread): thread is ThreadSummary => thread !== undefined);

  function reorderPinnedThreads(
    activeThreadID: string,
    overThreadID: string,
    position: ThreadDropPosition,
  ): void {
    const next = moveThreadInOrder(reconciledThreadOrder, activeThreadID, overThreadID, position);
    if (next === reconciledThreadOrder) return;
    setThreadOrder(next);
    persistThreadOrder(PINNED_THREAD_ORDER_ID, next);
  }

  return (
    <div className="pinned-thread-list">
      <ThreadRows
        threads={orderedThreads}
        activeID={activeID}
        pendingThreadID={pendingThreadID}
        lastViewedTurnByThreadID={lastViewedTurnByThreadID}
        onSelect={onSelect}
        onTogglePinned={onTogglePinned}
        onArchive={onArchive}
        onDelete={onDelete}
        onRename={onRename}
        onReorder={reorderPinnedThreads}
      />
    </div>
  );
}

export function OrganizationThreadList({
  threads,
  activeID,
  pendingThreadID,
  lastViewedTurnByThreadID,
  onSelect,
  onTogglePinned,
  onArchive,
  onDelete,
  onRename,
}: {
  threads: ThreadSummary[];
  activeID?: string;
  pendingThreadID?: string;
  lastViewedTurnByThreadID: Record<string, string>;
  onSelect: (id: string) => void;
  onTogglePinned: (thread: ThreadSummary) => void;
  onArchive: (thread: ThreadSummary) => void;
  onDelete: (thread: ThreadSummary) => void;
  onRename?: (thread: ThreadSummary, title: string) => void;
}): JSX.Element {
  return (
    <div className="pinned-thread-list">
      <ThreadRows
        threads={threads}
        activeID={activeID}
        pendingThreadID={pendingThreadID}
        lastViewedTurnByThreadID={lastViewedTurnByThreadID}
        onSelect={onSelect}
        onTogglePinned={onTogglePinned}
        onArchive={onArchive}
        onDelete={onDelete}
        onRename={onRename}
      />
    </div>
  );
}

/**
 * ThreadRowTitle renders the sidebar title with a soft crossfade whenever the
 * displayed text changes (typically when the LLM-generated title replaces the
 * fallback preview after the first turn completes).
 *
 * Design intent: the streaming-state visual and the post-stable visual should
 * not switch abruptly. A pure DOM-text swap reads as a flicker because the
 * fallback (first user query, often long) and the final title (short,
 * grammar-normalized) are visually very different. We crossfade between them
 * with a key remount so the user perceives a settle, not a snap.
 *
 * The first appearance of a title does not animate — only swaps after the
 * component has been mounted with prior content. This avoids the entire
 * sidebar fading in on project switch / cold boot, which would itself feel
 * like a loading state.
 */
export function ThreadRowTitle({ title }: { title: string }): JSX.Element {
  const previousTitleRef = useRef(title);
  const swapCountRef = useRef(0);
  if (previousTitleRef.current !== title) {
    previousTitleRef.current = title;
    swapCountRef.current += 1;
  }
  const hasSwapped = swapCountRef.current > 0;
  return (
    <span
      className="thread-row-title"
      data-title-swap={hasSwapped ? swapCountRef.current : undefined}
      key={swapCountRef.current}
    >
      {title}
    </span>
  );
}
