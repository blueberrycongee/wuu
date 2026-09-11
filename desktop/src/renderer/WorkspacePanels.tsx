import {
  lazy,
  Suspense,
  type CSSProperties,
  type DragEvent as ReactDragEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { createPortal } from "react-dom";
import {
  closestCenter,
  DndContext,
  DragOverlay,
  PointerSensor,
  useSensor,
  useSensors,
  type DragCancelEvent,
  type DragEndEvent,
  type DragStartEvent
} from "@dnd-kit/core";
import { restrictToHorizontalAxis } from "@dnd-kit/modifiers";
import { horizontalListSortingStrategy, SortableContext, useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  ArrowLeft,
  FileDiff,
  FileText,
  FolderOpen,
  Globe,
  GripHorizontal,
  Maximize2,
  Minimize2,
  PanelLeftOpen,
  PanelRightOpen,
  Plus,
  ShieldCheck,
  Terminal,
  X,
} from "lucide-react";
import type { ActivitySession, GitStatusResult, RuntimeContext, Thread } from "../shared/protocol";
import {
  formatWorkspaceFileTarget,
  parseWorkspaceFileTarget,
  resolveWorkspaceFileTarget,
} from "./LinkTargets";
import { TurnFileDiffPanel } from "./TurnFileDiffPanel";
import { WorkspaceBrowserPanel } from "./WorkspaceBrowserPanel";
import {
  WorkspaceFilePreview,
  WorkspaceFileTree,
  WorkspacePanelEmpty,
  type WorkspaceFileDirtyState,
} from "./WorkspaceFiles";
import { WorkspaceReviewPanel } from "./WorkspaceReviewPanels";
import { ViewSwitchLoading } from "./LoadingViews";
import type { WorkspaceFileViewTab, WorkspaceViewTab } from "./WorkspaceViewTabs";
import { handleTabListKeyDown, useTabCloseFocusRestoration } from "./TabKeyboardNavigation";
import { useStripEnterReady, useTabExitRetention } from "./TabMotion";
import { translateCurrent, useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import type { TranslationKey } from "./i18n/resources/zh-CN";
import { HeaderPresentation, immutableHeaderSnapshot } from "./plugins/HeaderPresentation";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { PluginHost, RegisteredPluginViewEntry } from "./plugins/PluginHost";
import { PluginIcon } from "./PublicIcon";
import { PluginSlot } from "./plugins/PluginSlot";
import type { WorkbenchController } from "./plugins/Workbench";
import { PluginViewContent } from "./plugins/Workbench";
import { ENABLE_EMBEDDED_BROWSER } from "./FeatureFlags";

const WorkspaceTerminalPanel = lazy(() => import("./WorkspaceTerminalPanel").then((module) => ({
  default: module.WorkspaceTerminalPanel,
})));

export type WorkspacePanelView = "files" | "review" | "terminal" | "browser";

const WORKSPACE_TOOL_ITEMS: Array<{
  id: WorkspacePanelView;
  titleKey: TranslationKey;
  subtitleKey: TranslationKey;
}> = [
  { id: "files", titleKey: "workspace.tool.files", subtitleKey: "workspace.tool.filesDescription" },
  { id: "review", titleKey: "workspace.tool.review", subtitleKey: "workspace.tool.reviewDescription" },
  { id: "terminal", titleKey: "workspace.tool.terminal", subtitleKey: "workspace.tool.terminalDescription" },
  ...(ENABLE_EMBEDDED_BROWSER
    ? [{ id: "browser" as const, titleKey: "workspace.tool.browser" as const, subtitleKey: "workspace.tool.browserDescription" as const }]
    : []),
];

export const WORKSPACE_FILE_TREE_DEFAULT_WIDTH = 320;
export const WORKSPACE_FILE_TREE_MIN_WIDTH = 180;
export const WORKSPACE_FILE_TREE_MAX_WIDTH = 480;
export const WORKSPACE_FILE_CONTENT_MIN_WIDTH = 240;
const WORKSPACE_FILE_TREE_COLLAPSE_THRESHOLD = 140;
const WORKSPACE_FILE_TREE_WIDTH_STEP = 24;
const WORKSPACE_FILE_TREE_WIDTH_KEY = "wuu.desktop.fileTreeWidth";
const WORKSPACE_FILE_TREE_SIDE_KEY = "wuu.desktop.fileTreeSide";
const WORKSPACE_FILE_TREE_VISIBLE_KEY = "wuu.desktop.fileTreeVisible";
const WORKSPACE_PANEL_PREWARM_TIMEOUT_MS = 2_000;

type WorkspaceFileTreeSide = "left" | "right";

function scheduleIdleTask(callback: () => void, timeoutMs: number): () => void {
  if (typeof window.requestIdleCallback === "function") {
    const idleCallbackID = window.requestIdleCallback(callback, { timeout: timeoutMs });
    return () => window.cancelIdleCallback(idleCallbackID);
  }
  const timeoutID = window.setTimeout(callback, Math.min(timeoutMs, 500));
  return () => window.clearTimeout(timeoutID);
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

export function clampWorkspaceFileTreeWidth(
  width: number,
  panelWidth = Number.POSITIVE_INFINITY,
): number {
  if (!Number.isFinite(panelWidth) || panelWidth <= 0) {
    return clamp(width, WORKSPACE_FILE_TREE_MIN_WIDTH, WORKSPACE_FILE_TREE_MAX_WIDTH);
  }
  const maxForPanel = Math.max(
    WORKSPACE_FILE_TREE_MIN_WIDTH,
    Math.min(WORKSPACE_FILE_TREE_MAX_WIDTH, panelWidth - WORKSPACE_FILE_CONTENT_MIN_WIDTH),
  );
  return clamp(width, WORKSPACE_FILE_TREE_MIN_WIDTH, maxForPanel);
}

function initialWorkspaceFileTreeWidth(): number {
  if (typeof window === "undefined") {
    return WORKSPACE_FILE_TREE_DEFAULT_WIDTH;
  }
  const stored = Number(window.localStorage.getItem(WORKSPACE_FILE_TREE_WIDTH_KEY));
  if (!Number.isFinite(stored) || stored <= 0) {
    return WORKSPACE_FILE_TREE_DEFAULT_WIDTH;
  }
  return clampWorkspaceFileTreeWidth(stored);
}

function initialWorkspaceFileTreeSide(): WorkspaceFileTreeSide {
  if (typeof window === "undefined") {
    return "right";
  }
  return window.localStorage.getItem(WORKSPACE_FILE_TREE_SIDE_KEY) === "left" ? "left" : "right";
}

function initialWorkspaceFileTreeVisible(): boolean {
  if (typeof window === "undefined") {
    return true;
  }
  return window.localStorage.getItem(WORKSPACE_FILE_TREE_VISIBLE_KEY) !== "false";
}

export function WorkspaceRightPanel({
  compactNavigation = false,
  open,
  present,
  prewarm = false,
  tabs,
  activeTabID,
  activeFileTabID,
  activeContext,
  workspaceContext,
  terminalThread,
  gitStatus,
  selectedFilePath,
  onSelectTab,
  onOpenTool,
  onOpenPluginTool = () => undefined,
  onShowTools,
  onCloseTab,
  onDirtyFileTabsChange,
  onReorderTabs,
  onOpenFile,
  onClose,
  globalized,
  sheetPhase = "docked",
  onToggleGlobalize,
  canExitGlobalized = true,
  browserActivity,
  onBrowserActivityTakeover,
  onBrowserActivityRelease,
  onBrowserActivityStop,
  focusedComposer,
  fileRefreshKey,
  pluginHost,
  workbenchController,
}: {
  compactNavigation?: boolean;
  open: boolean;
  present: boolean;
  prewarm?: boolean;
  tabs: WorkspaceViewTab[];
  activeTabID: string | undefined;
  activeFileTabID?: string;
  // activeContext is the pinned project/no_project context — used for the
  // browser tab (its "current project" hint text, not a filesystem root).
  // workspaceContext follows the active thread's own cwd (e.g. a worktree
  // fork) when it differs from activeContext, and roots the file tree and
  // terminal; see workspacePanelContext in AppState.ts.
  activeContext?: RuntimeContext;
  workspaceContext?: RuntimeContext;
  terminalThread?: Thread;
  gitStatus?: GitStatusResult;
  selectedFilePath?: string;
  onSelectTab: (id: string) => void;
  onOpenTool: (view: WorkspacePanelView) => void;
  onOpenPluginTool?: (entry: RegisteredPluginViewEntry) => void;
  onShowTools: () => void;
  onCloseTab: (id: string) => void;
  onDirtyFileTabsChange?: (dirty: boolean) => void;
  onReorderTabs: (activeID: string, overID: string) => void;
  onOpenFile: (path: string) => void;
  onClose: () => void;
  globalized: boolean;
  // Globalize-sheet phase from App's phase machine; drives the data-sheet
  // attribute that promotes the panel to a full-window sheet in CSS.
  sheetPhase?: "docked" | "arming" | "open" | "exiting" | "docking";
  onToggleGlobalize: () => void;
  canExitGlobalized?: boolean;
  browserActivity?: ActivitySession;
  onBrowserActivityTakeover?: () => void;
  onBrowserActivityRelease?: () => void;
  onBrowserActivityStop?: () => void;
  focusedComposer?: ReactNode;
  fileRefreshKey?: string;
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
}): JSX.Element {
  const { t } = useI18n();
  const effectivePluginHost = pluginHost ?? desktopPluginHost;
  const pluginTools = useSyncExternalStore(
    (listener) => effectivePluginHost.subscribe(listener),
    () => effectivePluginHost.getWorkspaceTools(),
    () => effectivePluginHost.getWorkspaceTools(),
  );
  const activeTab = activeTabID ? tabs.find((tab) => tab.id === activeTabID) : undefined;
  const fileTabs = tabs.filter((tab): tab is WorkspaceFileViewTab => tab.kind === "file");
  const visibleTabs = tabs;
  const showingPicker = !activeTab;
  const [dirtyFileTabIDs, setDirtyFileTabIDs] = useState<Set<string>>(() => new Set());
  const enterReady = useStripEnterReady();
  const tabEntries = useTabExitRetention(visibleTabs, (tab) => tab.id);
  const [draggingTabID, setDraggingTabID] = useState<string | undefined>(undefined);
  const [draggingTabWidth, setDraggingTabWidth] = useState<number | undefined>(undefined);
  const [fileTreeWidth, setFileTreeWidth] = useState(initialWorkspaceFileTreeWidth);
  const [fileTreeSide, setFileTreeSide] = useState<WorkspaceFileTreeSide>(initialWorkspaceFileTreeSide);
  const [fileTreeVisible, setFileTreeVisible] = useState(initialWorkspaceFileTreeVisible);
  const [draggingFileTree, setDraggingFileTree] = useState(false);
  const [fileTreeDropSide, setFileTreeDropSide] = useState<WorkspaceFileTreeSide | undefined>(undefined);
  const [bodyPrewarmed, setBodyPrewarmed] = useState(false);
  const fileTreePreferredWidthRef = useRef(fileTreeWidth);
  const fileTreeVisibleRef = useRef(fileTreeVisible);
  const fileTreeDockDragRef = useRef(false);
  const [resizingFileSplit, setResizingFileSplit] = useState(false);
  const fileSplitRef = useRef<HTMLDivElement>(null);
  const fileTreeRef = useRef<HTMLElement>(null);
  const fileTreeDragPreviewRef = useRef<HTMLDivElement>(null);
  const fileSplitResizeRef = useRef<{ startX: number; startTreeWidth: number } | null>(null);
  const tabSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
  const draggingTab = draggingTabID ? tabs.find((tab) => tab.id === draggingTabID) : undefined;
  const addButtonRef = useRef<HTMLButtonElement>(null);
  const { requestFocusRestoration, tabListRef } = useTabCloseFocusRestoration(
    activeTabID,
    visibleTabs.map((tab) => tab.id),
    addButtonRef,
  );

  useEffect(() => {
    if (!prewarm || bodyPrewarmed || open) {
      return undefined;
    }
    return scheduleIdleTask(() => setBodyPrewarmed(true), WORKSPACE_PANEL_PREWARM_TIMEOUT_MS);
  }, [bodyPrewarmed, open, prewarm]);

  useEffect(() => {
    onDirtyFileTabsChange?.(dirtyFileTabIDs.size > 0);
  }, [dirtyFileTabIDs, onDirtyFileTabsChange]);

  useEffect(() => {
    window.localStorage.setItem(
      WORKSPACE_FILE_TREE_WIDTH_KEY,
      String(fileTreePreferredWidthRef.current),
    );
  }, []);

  useEffect(() => {
    const split = fileSplitRef.current;
    if (!split || typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const splitElement = split;

    function fitFileTreeToPanel(): void {
      const panelWidth = splitElement.getBoundingClientRect().width;
      if (panelWidth <= 0) {
        return;
      }
      setFileTreeWidth((current) => {
        const next = clampWorkspaceFileTreeWidth(
          fileTreePreferredWidthRef.current,
          panelWidth,
        );
        return next === current ? current : next;
      });
    }

    const observer = new ResizeObserver(fitFileTreeToPanel);
    observer.observe(splitElement);
    fitFileTreeToPanel();
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("resizing-workspace-file-split", resizingFileSplit);
    if (!resizingFileSplit) {
      return () => root.classList.remove("resizing-workspace-file-split");
    }

    function handlePointerMove(event: PointerEvent): void {
      const session = fileSplitResizeRef.current;
      if (!session) {
        return;
      }
      const panelWidth = fileSplitRef.current?.getBoundingClientRect().width;
      const nextWidth =
        session.startTreeWidth +
        (event.clientX - session.startX) * (fileTreeSide === "left" ? 1 : -1);
      if (nextWidth < WORKSPACE_FILE_TREE_COLLAPSE_THRESHOLD) {
        setFileTreeVisibility(false);
        return;
      }
      setFileTreeVisibility(true);
      setPreferredFileTreeWidth(nextWidth, panelWidth);
    }

    function finishResize(): void {
      fileSplitResizeRef.current = null;
      setResizingFileSplit(false);
    }

    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", finishResize);
    window.addEventListener("pointercancel", finishResize);
    return () => {
      root.classList.remove("resizing-workspace-file-split");
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", finishResize);
      window.removeEventListener("pointercancel", finishResize);
    };
  }, [fileTreeSide, resizingFileSplit]);

  function resizeFileTreeBy(delta: number): void {
    const panelWidth = fileSplitRef.current?.getBoundingClientRect().width;
    setPreferredFileTreeWidth(fileTreeWidth + delta, panelWidth);
  }

  function setPreferredFileTreeWidth(width: number, panelWidth?: number): void {
    const preferredWidth = clampWorkspaceFileTreeWidth(width);
    fileTreePreferredWidthRef.current = preferredWidth;
    window.localStorage.setItem(WORKSPACE_FILE_TREE_WIDTH_KEY, String(preferredWidth));
    setFileTreeWidth(clampWorkspaceFileTreeWidth(preferredWidth, panelWidth));
  }

  function startFileSplitResize(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    fileSplitResizeRef.current = { startX: event.clientX, startTreeWidth: fileTreeWidth };
    setResizingFileSplit(true);
  }

  function handleFileSplitKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      resizeFileTreeBy(
        fileTreeSide === "right" ? WORKSPACE_FILE_TREE_WIDTH_STEP : -WORKSPACE_FILE_TREE_WIDTH_STEP,
      );
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      resizeFileTreeBy(
        fileTreeSide === "left" ? WORKSPACE_FILE_TREE_WIDTH_STEP : -WORKSPACE_FILE_TREE_WIDTH_STEP,
      );
    } else if (event.key === "Home") {
      event.preventDefault();
      resizeFileTreeBy(WORKSPACE_FILE_TREE_MAX_WIDTH);
    } else if (event.key === "End") {
      event.preventDefault();
      setFileTreeVisibility(false);
    }
  }

  function resetFileTreeWidth(): void {
    const panelWidth = fileSplitRef.current?.getBoundingClientRect().width;
    setPreferredFileTreeWidth(WORKSPACE_FILE_TREE_DEFAULT_WIDTH, panelWidth);
  }

  function moveFileTree(side: WorkspaceFileTreeSide): void {
    window.localStorage.setItem(WORKSPACE_FILE_TREE_SIDE_KEY, side);
    setFileTreeSide(side);
  }

  function setFileTreeVisibility(visible: boolean): void {
    if (fileTreeVisibleRef.current === visible) {
      return;
    }
    fileTreeVisibleRef.current = visible;
    window.localStorage.setItem(WORKSPACE_FILE_TREE_VISIBLE_KEY, String(visible));
    setFileTreeVisible(visible);
  }

  function startFileTreeDockDrag(event: ReactDragEvent<HTMLDivElement>): void {
    const dragPreview = fileTreeDragPreviewRef.current;
    if (!dragPreview) {
      return;
    }
    const rect = dragPreview.getBoundingClientRect();
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("application/x-wuu-file-tree", fileTreeSide);
    event.dataTransfer.setDragImage(dragPreview, rect.width / 2, rect.height / 2);
    fileTreeDockDragRef.current = true;
    setDraggingFileTree(true);
    setFileTreeDropSide(fileTreeSide);
  }

  function fileTreeSideAt(clientX: number): WorkspaceFileTreeSide {
    const rect = fileSplitRef.current?.getBoundingClientRect();
    if (!rect) {
      return fileTreeSide;
    }
    return clientX < rect.left + rect.width / 2 ? "left" : "right";
  }

  function handleFileTreeDockDragOver(event: ReactDragEvent<HTMLDivElement>): void {
    if (!fileTreeDockDragRef.current) {
      return;
    }
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    setFileTreeDropSide(fileTreeSideAt(event.clientX));
  }

  function finishFileTreeDockDrag(): void {
    fileTreeDockDragRef.current = false;
    setDraggingFileTree(false);
    setFileTreeDropSide(undefined);
  }

  function dropFileTree(event: ReactDragEvent<HTMLDivElement>): void {
    if (!fileTreeDockDragRef.current) {
      return;
    }
    event.preventDefault();
    moveFileTree(fileTreeSideAt(event.clientX));
    finishFileTreeDockDrag();
  }

  function handleFileTreeDockKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      moveFileTree(event.key === "ArrowLeft" ? "left" : "right");
    }
  }

  function startTabDrag(event: DragStartEvent): void {
    setDraggingTabID(String(event.active.id));
    setDraggingTabWidth(event.active.rect.current.initial?.width);
  }

  function endTabDrag(event: DragEndEvent): void {
    const activeID = String(event.active.id);
    const overID = event.over ? String(event.over.id) : undefined;
    if (overID && activeID !== overID) {
      onReorderTabs(activeID, overID);
    }
    finishTabDrag();
  }

  function cancelTabDrag(_event: DragCancelEvent): void {
    finishTabDrag();
  }

  function finishTabDrag(): void {
    setDraggingTabID(undefined);
    setDraggingTabWidth(undefined);
  }

  const updateFileDirtyState = useCallback((tabID: string, dirty: boolean): void => {
    setDirtyFileTabIDs((current) => {
      if (current.has(tabID) === dirty) {
        return current;
      }
      const next = new Set(current);
      if (dirty) {
        next.add(tabID);
      } else {
        next.delete(tabID);
      }
      return next;
    });
  }, []);

  function requestCloseTab(tab: WorkspaceViewTab): void {
    if (
      tab.kind === "file" &&
      dirtyFileTabIDs.has(tab.id) &&
      !window.confirm(t("workspace.unsavedCloseConfirm"))
    ) {
      return;
    }
    setDirtyFileTabIDs((current) => {
      if (!current.has(tab.id)) {
        return current;
      }
      const next = new Set(current);
      next.delete(tab.id);
      return next;
    });
    requestFocusRestoration();
    onCloseTab(tab.id);
  }

  const headerTabs = tabs.map((tab) => {
    const busy = (tab.kind === "terminal" && terminalThread?.status === "in_progress") ||
      (tab.kind === "browser" && browserActivity?.state === "active");
    return {
      id: tab.id,
      title: workspaceViewTabLabel(tab),
      subtitle: tab.kind === "file" || tab.kind === "diff" ? tab.path : undefined,
      kind: tab.kind,
      busy: busy || undefined,
      dirty: (tab.kind === "file" && dirtyFileTabIDs.has(tab.id)) || undefined,
    };
  });
  const headerSnapshot = immutableHeaderSnapshot({
    scope: "workspace",
    title: activeTab ? workspaceViewTabLabel(activeTab) : t("workspace.artifactsAndTools"),
    subtitle: activeTab?.kind === "file" || activeTab?.kind === "diff" ? activeTab.path : undefined,
    tabs: compactNavigation ? undefined : headerTabs,
    activeTabId: compactNavigation ? undefined : activeTabID,
    busy: headerTabs.some((tab) => tab.busy) || undefined,
    dirty: headerTabs.some((tab) => tab.dirty) || undefined,
  });

  return (
    <aside
      className={`workspace-right-panel${compactNavigation ? " compact-navigation" : ""}${activeTab ? " detail" : " tools"}${activeTab?.kind === "review" ? " review" : ""}${activeTab?.kind === "diff" ? " diff" : ""}${activeTab?.kind === "files" || activeTab?.kind === "file" ? " files" : ""}${activeTab?.kind === "terminal" ? " terminal" : ""}${focusedComposer && activeTab?.kind === "file" ? " document-focus" : ""}`}
      data-wuu-component="workspace-panel"
      data-wuu-view={activeTab?.kind ?? "picker"}
      data-sheet={
        sheetPhase === "exiting"
          ? "parked"
          : sheetPhase === "docked"
            ? undefined
            : sheetPhase
      }
      aria-hidden={!open}
      inert={!open}
    >
      <div className="workspace-panel-tabbar" data-wuu-component="workspace-panel-header">
        <PluginSlot
          host={pluginHost ?? desktopPluginHost}
          id="workspace.header"
          context={Object.freeze({
            scope: "workspace",
            open,
            globalized,
            activeViewKind: activeTab?.kind,
          })}
        />
        <HeaderPresentation
          snapshot={headerSnapshot}
          host={pluginHost}
          controller={workbenchController}
          onSelectTab={onSelectTab}
          onCloseTab={(tabId) => {
            const tab = tabs.find((candidate) => candidate.id === tabId);
            if (tab) requestCloseTab(tab);
          }}
          fallback={compactNavigation ? (
            <>
              {globalized ? <span className="workspace-panel-sidebar-hit-hole" aria-hidden="true" /> : null}
              <button
                className="icon-button workspace-panel-back"
                type="button"
                aria-label={t("common.back")}
                disabled={!open}
                onClick={() => {
                  if (!activeTab) {
                    onClose();
                    return;
                  }
                  if (activeTab.kind === "file") {
                    onOpenTool("files");
                    return;
                  }
                  onShowTools();
                }}
              >
                <ArrowLeft className="icon" />
              </button>
              <div className="workspace-panel-compact-heading">
                <strong>
                  {activeTab ? workspaceViewTabLabel(activeTab) : t("workspace.artifactsAndTools")}
                </strong>
                {activeTab?.kind === "file" || activeTab?.kind === "diff" ? (
                  <span>{activeTab.path}</span>
                ) : null}
              </div>
              {activeTab ? (
                <button
                  className="icon-button workspace-panel-close-tab"
                  type="button"
                  aria-label={t("workspace.closeTab", {
                    label: workspaceViewTabLabel(activeTab),
                  })}
                  disabled={!open}
                  onClick={() => requestCloseTab(activeTab)}
                >
                  <X className="icon" />
                </button>
              ) : (
                <span className="workspace-panel-compact-action-slot" aria-hidden="true" />
              )}
            </>
          ) : (
            <>
        {globalized ? (
          <span className="workspace-panel-sidebar-hit-hole" aria-hidden="true" />
        ) : null}
        <DndContext
          sensors={tabSensors}
          collisionDetection={closestCenter}
          modifiers={[restrictToHorizontalAxis]}
          onDragStart={startTabDrag}
          onDragEnd={endTabDrag}
          onDragCancel={cancelTabDrag}
        >
          <SortableContext items={visibleTabs.map((tab) => tab.id)} strategy={horizontalListSortingStrategy}>
            <div
              ref={tabListRef}
              className="workspace-panel-tabs"
              role="tablist"
              aria-label={t("workspace.artifactsAndTools")}
              data-enter-ready={enterReady ? "" : undefined}
              onKeyDown={handleTabListKeyDown}
            >
              {tabEntries.map((entry) => {
                const tab = entry.tab;
                if (entry.closing) {
                  // Exit retention (TabMotion.ts): inert collapsing ghost so
                  // the neighbours slide over instead of jumping.
                  return (
                    <div
                      key={`closing-${tab.id}`}
                      className="workspace-tool-tab closing"
                      aria-hidden="true"
                      data-wuu-component="workspace-tool-tab"
                      data-wuu-state="closing"
                    >
                      <span className="workspace-tool-tab-main">
                        <WorkspaceViewTabIcon tab={tab} className="icon" />
                        <span>{workspaceViewTabLabel(tab)}</span>
                      </span>
                    </div>
                  );
                }
                const active = tab.id === activeTabID;
                return (
                  <SortableWorkspaceViewTab
                    key={tab.id}
                    tab={tab}
                    active={active}
                    dirty={tab.kind === "file" && dirtyFileTabIDs.has(tab.id)}
                    open={open}
                    reorderable={visibleTabs.length > 1}
                    onSelect={() => onSelectTab(tab.id)}
                    onClose={() => requestCloseTab(tab)}
                    onDoubleClick={() => requestCloseTab(tab)}
                  />
                );
              })}
            </div>
          </SortableContext>
          {/* Portaled to <body>: the overlay is position:fixed and dnd-kit
            * places it in viewport coordinates, but .workspace-right-panel
            * has transform/will-change/contain — any of which makes it the
            * containing block for fixed descendants, so an in-panel overlay
            * renders offset by the panel's own position ("drifts" the moment
            * the drag starts). The session strip doesn't need this because
            * no ancestor of it is transformed. React portals keep context,
            * so DndContext still drives the overlay. */}
          {createPortal(
            <DragOverlay dropAnimation={{ duration: 150, easing: "cubic-bezier(0.16, 1, 0.3, 1)" }}>
              {draggingTab ? (
                <WorkspaceViewTabPreview
                  tab={draggingTab}
                  active={draggingTab.id === activeTabID}
                  dirty={dirtyFileTabIDs.has(draggingTab.id)}
                  width={draggingTabWidth}
                />
              ) : null}
            </DragOverlay>,
            document.body,
          )}
        </DndContext>
        <span className="workspace-panel-tabbar-spacer" />
        <button
          ref={addButtonRef}
          className={`icon-button workspace-panel-add${showingPicker ? " active" : ""}`}
          type="button"
          aria-label={t("workspace.chooseTool")}
          aria-pressed={showingPicker}
          disabled={!open}
          onClick={onShowTools}
        >
          <Plus className="icon-lg" />
        </button>
        <button
          className={`icon-button workspace-panel-globalize${globalized ? " active" : ""}`}
          type="button"
          aria-label={
            globalized && !canExitGlobalized
              ? t("workspace.tooNarrowToDock")
              : globalized
                ? t("workspace.exitFullPanel")
                : t("workspace.expandFullPanel")
          }
          title={
            globalized && !canExitGlobalized
              ? t("workspace.tooNarrowToDock")
              : globalized
                ? t("workspace.exitFullPanel")
                : t("workspace.expandFullPanel")
          }
          aria-pressed={globalized}
          disabled={!open || (globalized && !canExitGlobalized)}
          onClick={onToggleGlobalize}
        >
          {globalized ? <Minimize2 className="icon" /> : <Maximize2 className="icon" />}
        </button>
        <button
          className="icon-button workspace-panel-close"
          type="button"
          aria-label={t("workspace.closeRightPanel")}
          disabled={!open}
          onClick={onClose}
        >
          <X className="icon" />
        </button>
            </>
          )}
        />
      </div>
      {present || bodyPrewarmed || fileTabs.length > 0 ? (
        <>
          <div className={`workspace-panel-body${activeTab ? "" : " picker"}`}>
            <div
              className={`workspace-files-split${resizingFileSplit ? " resizing" : ""}${fileTreeVisible ? "" : " tree-hidden"}${draggingFileTree ? " tree-dragging" : ""}`}
              data-wuu-component="workspace-files"
              data-tree-drop-side={fileTreeDropSide}
              data-tree-side={fileTreeSide}
              hidden={activeTab?.kind !== "files" && activeTab?.kind !== "file"}
              ref={fileSplitRef}
              style={{ "--workspace-file-tree-width": `${fileTreeWidth}px` } as CSSProperties}
              onDragOver={handleFileTreeDockDragOver}
              onDrop={dropFileTree}
            >
              <section
                className="workspace-files-content"
                data-wuu-component="workspace-file-content"
                aria-label={t("workspace.fileContent")}
              >
                <div className="workspace-files-content-body">
                  {fileTabs.map((tab) => (
                    <WorkspaceFileResource
                      active={open && activeTab?.kind === "file" && tab.id === activeFileTabID}
                      key={tab.id}
                      onDirtyChange={updateFileDirtyState}
                      onOpenFile={onOpenFile}
                      tab={tab}
                      refreshKey={
                        open && activeTab?.kind === "file" && tab.id === activeFileTabID
                          ? fileRefreshKey
                          : undefined
                      }
                    />
                  ))}
                  {activeTab?.kind === "files" ? (
                    <WorkspacePanelEmpty
                      title={t("workspace.selectFile")}
                      hint={t("workspace.selectFileDescription")}
                      icon={<FileText size={24} />}
                    />
                  ) : null}
                </div>
                {focusedComposer && activeTab?.kind === "file" ? (
                  <div className="workspace-document-composer" data-testid="workspace-document-composer">
                    {focusedComposer}
                  </div>
                ) : null}
              </section>
              <div
                className="workspace-files-resizer"
                hidden={!fileTreeVisible}
                role="separator"
                aria-label={t("workspace.resizeFileContentTree")}
                aria-orientation="vertical"
                aria-valuemin={WORKSPACE_FILE_TREE_MIN_WIDTH}
                aria-valuemax={WORKSPACE_FILE_TREE_MAX_WIDTH}
                aria-valuenow={Math.round(fileTreeWidth)}
                tabIndex={0}
                onPointerDown={startFileSplitResize}
                onDoubleClick={resetFileTreeWidth}
                onKeyDown={handleFileSplitKeyDown}
              />
              <section
                className="workspace-files-tree"
                data-wuu-component="workspace-file-tree"
                aria-label={t("workspace.fileTree")}
                hidden={!compactNavigation && !fileTreeVisible}
                ref={fileTreeRef}
              >
                <div
                  className="workspace-file-tree-drag-handle"
                  role="button"
                  tabIndex={0}
                  draggable
                  aria-label={t("workspace.dragFileTree")}
                  title={t("workspace.dragFileTree")}
                  onDragStart={startFileTreeDockDrag}
                  onDragEnd={finishFileTreeDockDrag}
                  onKeyDown={handleFileTreeDockKeyDown}
                >
                  <GripHorizontal size={15} strokeWidth={1.8} />
                </div>
                <WorkspaceFileTree
                  activeContext={workspaceContext}
                  open={
                    open &&
                    (compactNavigation || fileTreeVisible) &&
                    (activeTab?.kind === "files" || activeTab?.kind === "file")
                  }
                  selectedFilePath={selectedFilePath}
                  onOpenFile={onOpenFile}
                />
              </section>
              {!fileTreeVisible ? (
                <button
                  className={`icon-button workspace-file-tree-reveal ${fileTreeSide}`}
                  type="button"
                  aria-label={t("workspace.showFileTree")}
                  title={t("workspace.showFileTree")}
                  onClick={() => setFileTreeVisibility(true)}
                >
                  {fileTreeSide === "left" ? (
                    <PanelLeftOpen className="icon" />
                  ) : (
                    <PanelRightOpen className="icon" />
                  )}
                </button>
              ) : null}
            </div>
            {createPortal(
              <div
                ref={fileTreeDragPreviewRef}
                className="workspace-file-tree-drag-preview"
                aria-hidden="true"
              >
                <span className="workspace-file-tree-drag-preview-icon">
                  <FolderOpen size={17} strokeWidth={1.8} />
                </span>
                <span>{t("workspace.fileTree")}</span>
                <GripHorizontal size={14} strokeWidth={1.7} />
              </div>,
              document.body,
            )}
            {activeTab?.kind === "files" || activeTab?.kind === "file" ? null : (
              <div
                className="workspace-panel-content-swap"
                key={activeTab?.id ?? "picker"}
              >
                {!activeTab ? (
                  <WorkspaceToolPicker
                    tabs={tabs}
                    pluginTools={pluginTools}
                    onSelectTool={onOpenTool}
                    onSelectPluginTool={onOpenPluginTool}
                  />
                ) : activeTab.kind === "diff" ? (
                  <TurnFileDiffPanel
                    selection={activeTab.selection}
                    onClose={() => onCloseTab(activeTab.id)}
                  />
                ) : activeTab.kind === "review" ? (
                  <WorkspaceReviewPanel
                    gitStatus={gitStatus}
                    workspaceRoot={workspaceContext?.cwd}
                  />
                ) : activeTab.kind === "terminal" ? (
                  <Suspense fallback={<ViewSwitchLoading />}>
                    <WorkspaceTerminalPanel
                      activeContext={workspaceContext}
                      thread={terminalThread}
                    />
                  </Suspense>
                ) : activeTab.kind === "browser" ? (
                  <WorkspaceBrowserPanel
                    open={open}
                    activeContext={activeContext}
                    activity={browserActivity}
                    onActivityTakeover={onBrowserActivityTakeover}
                    onActivityRelease={onBrowserActivityRelease}
                    onActivityStop={onBrowserActivityStop}
                  />
                ) : activeTab.kind === "plugin" && workbenchController ? (
                  <PluginViewContent
                    controller={workbenchController}
                    pluginId={activeTab.pluginId}
                    viewTypeId={activeTab.viewTypeId}
                    context={Object.freeze({ region: "workspace", tabId: activeTab.id })}
                  />
                ) : null}
              </div>
            )}
          </div>
        </>
      ) : null}
    </aside>
  );
}

function WorkspaceFileResource({
  active,
  onDirtyChange,
  onOpenFile,
  tab,
  refreshKey,
}: {
  active: boolean;
  onDirtyChange: (tabID: string, dirty: boolean) => void;
  onOpenFile: (path: string) => void;
  tab: WorkspaceFileViewTab;
  refreshKey?: string;
}): JSX.Element {
  const handleDirtyChange = useCallback(
    (state: WorkspaceFileDirtyState) => onDirtyChange(tab.id, state.dirty),
    [onDirtyChange, tab.id],
  );
  const handleOpenFile = useCallback((reference: string) => {
    const target = parseWorkspaceFileTarget(reference);
    onOpenFile(
      target
        ? formatWorkspaceFileTarget(resolveWorkspaceFileTarget(tab.path, target))
        : reference,
    );
  }, [onOpenFile, tab.path]);

  return (
    <div
      className={`workspace-file-resource${active ? " active" : ""}`}
      data-workspace-tab-id={tab.id}
      hidden={!active}
    >
      <WorkspaceFilePreview
        active={active}
        activeContext={tab.context}
        anchor={tab.anchor}
        editorResourceID={tab.id}
        selection={tab.selection}
        refreshKey={refreshKey}
        selectedFilePath={tab.path}
        onOpenRightPanel={() => {}}
        onOpenFile={handleOpenFile}
        onDirtyChange={handleDirtyChange}
      />
    </div>
  );
}

function SortableWorkspaceViewTab({
  tab,
  active,
  dirty,
  open,
  reorderable,
  onSelect,
  onClose,
  onDoubleClick
}: {
  tab: WorkspaceViewTab;
  active: boolean;
  dirty: boolean;
  open: boolean;
  reorderable: boolean;
  onSelect: () => void;
  onClose: () => void;
  // Parity with session tabs: double-click closes (through the dirty-file
  // confirm guard upstream).
  onDoubleClick: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const { attributes, listeners, setActivatorNodeRef, setNodeRef, transform, transition, isDragging } = useSortable({
    id: tab.id,
    disabled: !reorderable
  });
  const { role: _dragRole, ...dragAttributes } = attributes;
  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition
  };
  const label = workspaceViewTabLabel(tab);
  const tooltip = workspaceViewTabTooltip(tab);
  return (
    <div
      ref={setNodeRef}
      className={`workspace-tool-tab${active ? " active" : ""}${dirty ? " dirty" : ""}${reorderable ? " can-reorder" : ""}${
        isDragging ? " dragging" : ""
      }`}
      style={style}
      aria-grabbed={isDragging || undefined}
      data-wuu-component="workspace-tool-tab"
      data-wuu-active={active ? "true" : "false"}
      data-wuu-state={isDragging ? "dragging" : undefined}
    >
      <Tooltip content={tooltip} disabled={tooltip === label}>
        <button
          ref={setActivatorNodeRef}
          className="workspace-tool-tab-main"
          type="button"
          {...dragAttributes}
          {...listeners}
          role="tab"
          aria-selected={active}
          aria-label={dirty ? t("workspace.tabUnsaved", { label }) : label}
          tabIndex={active ? 0 : -1}
          disabled={!open}
          onClick={onSelect}
          onDoubleClick={onDoubleClick}
        >
        <WorkspaceViewTabIcon tab={tab} className="icon" />
        <span>{label}</span>
        {dirty ? <span className="workspace-tab-dirty-indicator" aria-hidden="true" /> : null}
      </button>
      </Tooltip>
      <button
        className="workspace-tool-tab-close"
        type="button"
        draggable={false}
        data-wuu-component="workspace-tool-tab-close"
        aria-label={t("workspace.closeTab", { label })}
        disabled={!open}
        onClick={(event) => {
          event.stopPropagation();
          onClose();
        }}
      >
        <X className="icon-xs" />
      </button>
    </div>
  );
}

function WorkspaceViewTabPreview({
  tab,
  active,
  dirty,
  width
}: {
  tab: WorkspaceViewTab;
  active: boolean;
  dirty: boolean;
  width?: number;
}): JSX.Element {
  const label = workspaceViewTabLabel(tab);
  return (
    <div
      className={`workspace-tool-tab workspace-tool-tab-drag-overlay${active ? " active" : ""}${dirty ? " dirty" : ""}`}
      style={width ? { width } : undefined}
      data-wuu-component="workspace-tool-tab"
      data-wuu-active={active ? "true" : "false"}
      data-wuu-state="dragging"
    >
      <div className="workspace-tool-tab-main">
        <WorkspaceViewTabIcon tab={tab} className="icon" />
        <span>{label}</span>
        {dirty ? <span className="workspace-tab-dirty-indicator" aria-hidden="true" /> : null}
      </div>
      <div className="workspace-tool-tab-close" aria-hidden="true" data-wuu-component="workspace-tool-tab-close">
        <X className="icon-xs" />
      </div>
    </div>
  );
}

function WorkspaceToolPicker({
  tabs,
  pluginTools,
  onSelectTool,
  onSelectPluginTool,
}: {
  tabs: WorkspaceViewTab[];
  pluginTools: readonly RegisteredPluginViewEntry[];
  onSelectTool: (view: WorkspacePanelView) => void;
  onSelectPluginTool: (entry: RegisteredPluginViewEntry) => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <div
      className="workspace-tool-menu"
      aria-label={t("workspace.tools")}
      data-wuu-component="workspace-tool-picker"
    >
      {WORKSPACE_TOOL_ITEMS.filter((item) => item.id !== "browser" || typeof window.wuu?.reportBrowserBounds === "function").map((item) => (
        <button
          key={item.id}
          className={`workspace-tool-menu-item${tabs.some((tab) => tab.kind === item.id) ? " active" : ""}`}
          data-wuu-component="workspace-tool"
          type="button"
          onClick={() => onSelectTool(item.id)}
        >
          <span className="workspace-tool-menu-icon" aria-hidden="true">
            <WorkspaceToolIcon view={item.id} className="icon-xl" />
          </span>
          <span className="workspace-tool-menu-copy">
            <strong>{t(item.titleKey)}</strong>
          </span>
        </button>
      ))}
      {pluginTools.map((item) => {
        const tabID = `plugin:${item.pluginId}:${item.id}`;
        return (
          <button
            key={tabID}
            className={`workspace-tool-menu-item${tabs.some((tab) => tab.id === tabID) ? " active" : ""}`}
            data-wuu-component="workspace-tool"
            data-wuu-plugin={item.pluginId}
            type="button"
            onClick={() => onSelectPluginTool(item)}
          >
            <span className="workspace-tool-menu-icon" aria-hidden="true">
              <PluginIcon icon={item.icon} pluginId={item.pluginId} fingerprint={item.generation} className="icon-xl" />
            </span>
            <span className="workspace-tool-menu-copy">
              <strong>{item.title}</strong>
              {item.description ? <span>{item.description}</span> : null}
            </span>
          </button>
        );
      })}
    </div>
  );
}

export function WorkspaceBottomPanel({
  open,
  selectedView,
  onSelectTool,
  onClose
}: {
  open: boolean;
  selectedView: WorkspacePanelView;
  onSelectTool: (view: WorkspacePanelView) => void;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <section className="workspace-bottom-panel" aria-hidden={!open}>
      <div className="workspace-bottom-header">
        <div className="workspace-bottom-title">{t("workspace.toolsShort")}</div>
        <button
          className="icon-button workspace-panel-close"
          type="button"
          aria-label={t("workspace.closeBottomPanel")}
          disabled={!open}
          onClick={onClose}
        >
          <X className="icon" />
        </button>
      </div>
      {open ? (
        <div
          className="workspace-tool-grid"
          aria-label={t("workspace.tools")}
        >
          {WORKSPACE_TOOL_ITEMS.filter((item) => item.id !== "browser" || typeof window.wuu?.reportBrowserBounds === "function").map((item) => (
            <button
              key={item.id}
              className={`workspace-tool-card${item.id === selectedView ? " active" : ""}`}
              data-wuu-component="workspace-tool"
              type="button"
              onClick={() => onSelectTool(item.id)}
            >
              <WorkspaceToolIcon view={item.id} className="workspace-tool-card-icon" />
              <strong>{t(item.titleKey)}</strong>
              <span>{t(item.subtitleKey)}</span>
            </button>
          ))}
        </div>
      ) : null}
    </section>
  );
}

export function WorkspaceToolIcon({ view, className }: { view: WorkspacePanelView; className?: string }): JSX.Element {
  switch (view) {
    case "files":
      return <FolderOpen className={className} />;
    case "review":
      return <ShieldCheck className={className} />;
    case "terminal":
      return <Terminal className={className} />;
    case "browser":
      return <Globe className={className} />;
  }
}

function workspaceToolFor(view: WorkspacePanelView): (typeof WORKSPACE_TOOL_ITEMS)[number] {
  return WORKSPACE_TOOL_ITEMS.find((item) => item.id === view) ?? WORKSPACE_TOOL_ITEMS[0];
}

function workspaceViewTabLabel(tab: WorkspaceViewTab): string {
  return tab.kind === "diff" || tab.kind === "file" || tab.kind === "plugin"
    ? tab.title
    : translateCurrent(workspaceToolFor(tab.kind).titleKey);
}

function workspaceViewTabTooltip(tab: WorkspaceViewTab): string {
  if (tab.kind === "plugin") return tab.title;
  return tab.kind === "diff" || tab.kind === "file"
    ? tab.path
    : translateCurrent(workspaceToolFor(tab.kind).titleKey);
}

function WorkspaceViewTabIcon({ tab, className }: { tab: WorkspaceViewTab; className?: string }): JSX.Element {
  if (tab.kind === "diff") {
    return <FileDiff className={className} />;
  }
  if (tab.kind === "file") {
    return <FileText className={className} />;
  }
  if (tab.kind === "plugin") {
    return <PluginIcon icon={tab.icon} pluginId={tab.pluginId} fingerprint={tab.fingerprint} className={className} />;
  }
  return <WorkspaceToolIcon view={tab.kind} className={className} />;
}
