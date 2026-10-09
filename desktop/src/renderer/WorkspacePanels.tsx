import {
  type CSSProperties,
  type DragEvent as ReactDragEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
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
import { useDropAnimation, useSortableTransition } from "./SortableMotion";
import { motionDurationMs } from "./motion";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";
import {
  ChevronDown,
  FileDiff,
  FileText,
  FolderOpen,
  Globe,
  GripHorizontal,
  LayoutGrid,
  LoaderCircle,
  Maximize2,
  MessageCircle,
  Minimize2,
  PanelLeft,
  PanelLeftOpen,
  PanelRight,
  PanelRightOpen,
  Plus,
  ShieldCheck,
  Terminal,
  Project,
  X,
} from "./WuuIcons";
import type { ActivitySession, BrowserDockTarget, GitStatusResult, RuntimeContext, Thread } from "../shared/protocol";
import {
  formatWorkspaceFileTarget,
  parseWorkspaceFileTarget,
  resolveWorkspaceFileTarget,
} from "./LinkTargets";
import { TurnFileDiffPanel } from "./TurnFileDiffPanel";
import { ArtifactPreview } from "./ArtifactOutputs";
import { WorkspaceBrowserPanel } from "./WorkspaceBrowserPanel";
import {
  WorkspaceFilePreview,
  WorkspaceFileTree,
  type WorkspaceFileDirtyState,
} from "./WorkspaceFiles";
import { WorkspaceReviewPanel } from "./WorkspaceReviewPanels";
import { ProjectPanel } from "./ProjectPanels";
import { confirmAction } from "./ConfirmDialog";
import { WorkspacePanelLoading } from "./LoadingViews";
import { WorkspacePanelEmpty } from "./WorkspacePanelEmpty";
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
import { useWorkspaceBrowserNavigationRequest } from "./WorkspaceBrowserNavigation";

type WorkspaceTerminalPanelComponent = typeof import("./WorkspaceTerminalPanel").WorkspaceTerminalPanel;

let cachedWorkspaceTerminalPanel: WorkspaceTerminalPanelComponent | undefined;
let workspaceTerminalPanelPromise: Promise<WorkspaceTerminalPanelComponent> | undefined;

function loadWorkspaceTerminalPanel(): Promise<WorkspaceTerminalPanelComponent> {
  if (cachedWorkspaceTerminalPanel) {
    return Promise.resolve(cachedWorkspaceTerminalPanel);
  }
  workspaceTerminalPanelPromise ??= import("./WorkspaceTerminalPanel").then(
    (module) => {
      cachedWorkspaceTerminalPanel = module.WorkspaceTerminalPanel;
      module.preloadWorkspaceTerminalRuntime();
      return module.WorkspaceTerminalPanel;
    },
    (error: unknown) => {
      workspaceTerminalPanelPromise = undefined;
      throw error;
    },
  );
  return workspaceTerminalPanelPromise;
}

export type WorkspacePanelView = "files" | "review" | "terminal" | "browser";

const WORKSPACE_TOOL_ITEMS: Array<{
  id: WorkspacePanelView;
  titleKey: TranslationKey;
}> = [
  { id: "files", titleKey: "workspace.tool.files" },
  { id: "review", titleKey: "workspace.tool.review" },
  { id: "terminal", titleKey: "workspace.tool.terminal" },
  { id: "browser", titleKey: "workspace.tool.browser" },
];

export const WORKSPACE_FILE_TREE_DEFAULT_WIDTH = 320;
export const WORKSPACE_FILE_TREE_MIN_WIDTH = 180;
export const WORKSPACE_FILE_TREE_MAX_WIDTH = 480;
// Code and prose need this much beside the tree; a narrower panel gives the
// document the whole width and keeps the tree one step away.
export const WORKSPACE_FILE_CONTENT_MIN_WIDTH = 360;
const WORKSPACE_FILE_TREE_COLLAPSE_THRESHOLD = 140;
const WORKSPACE_FILE_TREE_WIDTH_STEP = 24;
const WORKSPACE_FILE_TREE_WIDTH_KEY = "wuu.desktop.fileTreeWidth";
const WORKSPACE_FILE_TREE_SIDE_KEY = "wuu.desktop.fileTreeSide";
const WORKSPACE_FILE_TREE_VISIBLE_KEY = "wuu.desktop.fileTreeVisible";
const WORKSPACE_PANEL_PREWARM_TIMEOUT_MS = 2_000;
const WORKSPACE_CONVERSATION_TAB_ID = "conversation";

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
  onResumeTab = onSelectTab,
  onCloseTab,
  onDirtyFileTabsChange,
  onReorderTabs,
  onOpenFile,
  globalized,
  conversationTab,
  sideThreadContent,
  sideThreadRunning = false,
  sheetPhase = "docked",
  onToggleGlobalize,
  canExitGlobalized = true,
  browserActivity,
  browserDockTarget,
  browserOverlaySuppressed = false,
  onBrowserUserInteraction,
  focusedComposer,
  fileRefreshKey,
  pluginHost,
  workbenchController,
}: {
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
  onResumeTab?: (id: string) => void;
  onCloseTab: (id: string) => void;
  onDirtyFileTabsChange?: (dirty: boolean) => void;
  onReorderTabs: (activeID: string, overID: string) => void;
  onOpenFile: (path: string, sourceContext?: RuntimeContext) => void;
  globalized: boolean;
  conversationTab?: { title: string; active: boolean; onSelect: () => void };
  sideThreadContent?: ReactNode;
  sideThreadRunning?: boolean;
  // Globalize-sheet phase from App's phase machine; drives the data-sheet
  // attribute that promotes the panel to a full-window sheet in CSS.
  sheetPhase?: "docked" | "arming" | "open" | "exiting" | "docking";
  onToggleGlobalize: () => void;
  canExitGlobalized?: boolean;
  browserActivity?: ActivitySession;
  browserDockTarget?: BrowserDockTarget;
  browserOverlaySuppressed?: boolean;
  onBrowserUserInteraction?: () => void | Promise<void>;
  focusedComposer?: ReactNode;
  fileRefreshKey?: string;
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
}): JSX.Element {
  const { t } = useI18n();
  const browserNavigation = useWorkspaceBrowserNavigationRequest();
  const effectivePluginHost = pluginHost ?? desktopPluginHost;
  const pluginTools = useSyncExternalStore(
    (listener) => effectivePluginHost.subscribe(listener),
    () => effectivePluginHost.getWorkspaceTools(),
    () => effectivePluginHost.getWorkspaceTools(),
  );
  const conversationActive = Boolean(conversationTab?.active);
  const selectedTabID = conversationActive ? WORKSPACE_CONVERSATION_TAB_ID : activeTabID;
  const visibleTabs = tabs;
  const activeTab = activeTabID ? visibleTabs.find((tab) => tab.id === activeTabID) : undefined;
  const terminalTabOpen = tabs.some((tab) => tab.kind === "terminal");
  // Latch after the terminal has been shown so a later tool tab does not
  // tear the pty down. Closing the terminal tab releases it.
  const [terminalMounted, setTerminalMounted] = useState(false);
  if (activeTab?.kind === "terminal" && !terminalMounted) {
    setTerminalMounted(true);
  } else if (!terminalTabOpen && terminalMounted) {
    setTerminalMounted(false);
  }
  const fileTabs = tabs.filter((tab): tab is WorkspaceFileViewTab => tab.kind === "file");
  const showingPicker = !activeTab || activeTab.kind === "new";
  const [dirtyFileTabIDs, setDirtyFileTabIDs] = useState<Set<string>>(() => new Set());
  const enterReady = useStripEnterReady();
  const tabEntries = useTabExitRetention(visibleTabs, (tab) => tab.id);
  const [draggingTabID, setDraggingTabID] = useState<string | undefined>(undefined);
  const [draggingTabWidth, setDraggingTabWidth] = useState<number | undefined>(undefined);
  const [fileTreeWidth, setFileTreeWidth] = useState(initialWorkspaceFileTreeWidth);
  const [fileTreeSide, setFileTreeSide] = useState<WorkspaceFileTreeSide>(initialWorkspaceFileTreeSide);
  const [fileTreeVisible, setFileTreeVisible] = useState(initialWorkspaceFileTreeVisible);
  // Too narrow for the tree beside a document: the document takes the panel
  // and the tree stays available through the Files tab.
  const [fileSplitStacked, setFileSplitStacked] = useState(false);
  const stackedFileView = fileSplitStacked && activeTab?.kind === "file";
  // The saved visibility is untouched; a stacked document only sets it aside.
  const fileTreeDocked = fileTreeVisible && !stackedFileView;
  // The Files tab is the tree itself; beside a document the tree follows the
  // saved choice.
  const fileTreeShown = activeTab?.kind === "files" || fileTreeDocked;
  const fileTreeBesideDocument = activeTab?.kind === "file" && fileTreeDocked;
  const moveFileTreeLabel = t(fileTreeSide === "right" ? "workspace.moveFileTreeLeft" : "workspace.moveFileTreeRight");
  const [draggingFileTree, setDraggingFileTree] = useState(false);
  const [fileTreeDropSide, setFileTreeDropSide] = useState<WorkspaceFileTreeSide | undefined>(undefined);
  const [bodyPrewarmed, setBodyPrewarmed] = useState(false);
  const fileTreePreferredWidthRef = useRef(fileTreeWidth);
  const fileTreeVisibleRef = useRef(fileTreeVisible);
  const fileTreeDockDragRef = useRef(false);
  const [resizingFileSplit, setResizingFileSplit] = useState(false);
  const fileSplitRef = useRef<HTMLDivElement>(null);
  const fileContentRef = useRef<HTMLElement>(null);
  const documentComposerRef = useRef<HTMLDivElement>(null);
  const artifactContentRef = useRef<HTMLDivElement>(null);
  const fileTreeRef = useRef<HTMLElement>(null);
  const fileTreeDragPreviewRef = useRef<HTMLDivElement>(null);
  const fileSplitResizeRef = useRef<{ startX: number; startTreeWidth: number } | null>(null);
  const tabSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
  const dropAnimation = useDropAnimation();
  const draggingTab = draggingTabID ? tabs.find((tab) => tab.id === draggingTabID) : undefined;
  const addButtonRef = useRef<HTMLButtonElement>(null);
  const { requestFocusRestoration, tabListRef } = useTabCloseFocusRestoration(
    selectedTabID,
    [...(conversationTab ? [WORKSPACE_CONVERSATION_TAB_ID] : []), ...visibleTabs.map((tab) => tab.id)],
    addButtonRef,
  );

  useLayoutEffect(() => {
    const content = activeTab?.kind === "artifact" ? artifactContentRef.current : fileContentRef.current;
    const composer = documentComposerRef.current;
    if (!content || !composer) return;
    // Document annotations must remain above the floating composer's actual
    // footprint, including attachments and expanded input at larger font sizes.
    const measure = () => content.style.setProperty(
      "--workspace-document-composer-inset", `${composer.getBoundingClientRect().height + 16}px`,
    );
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(composer);
    return () => {
      observer?.disconnect();
      content.style.removeProperty("--workspace-document-composer-inset");
    };
  }, [Boolean(focusedComposer), activeTab?.kind, activeTab?.id, open, present]);
  // Keep the active tab visible after selection, opening and pane resizing.
  // Tabs retain their label/close lanes instead of collapsing to icons.
  useLayoutEffect(() => {
    const strip = tabListRef.current;
    if (!strip || !selectedTabID) {
      return undefined;
    }
    const reveal = (): void => {
      const tab = strip.querySelector<HTMLElement>(".workspace-tool-tab.active");
      if (!tab) {
        return;
      }
      const stripBox = strip.getBoundingClientRect();
      const tabBox = tab.getBoundingClientRect();
      if (tabBox.left < stripBox.left) {
        strip.scrollLeft -= stripBox.left - tabBox.left;
      } else if (tabBox.right > stripBox.right) {
        strip.scrollLeft += tabBox.right - stripBox.right;
      }
    };
    reveal();
    const timer = window.setTimeout(reveal, motionDurationMs("--motion-base", 180));
    const settle = createWindowResizeSettleScheduler(reveal);
    const observer = new ResizeObserver(() => {
      if (isWindowResizing()) settle.schedule();
      else reveal();
    });
    observer.observe(strip);
    const activeElement = strip.querySelector<HTMLElement>(".workspace-tool-tab.active");
    if (activeElement) observer.observe(activeElement);
    return () => {
      settle.cancel();
      window.clearTimeout(timer);
      observer.disconnect();
    };
  }, [selectedTabID, Boolean(conversationTab), tabListRef, visibleTabs.length]);

  useEffect(() => {
    if (!prewarm && !open) {
      return undefined;
    }
    return scheduleIdleTask(() => {
      void loadWorkspaceTerminalPanel().catch(() => undefined);
    }, WORKSPACE_PANEL_PREWARM_TIMEOUT_MS);
  }, [open, prewarm]);

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

  // The split only exists once the body mounts; a panel that starts closed must
  // attach the fit observer when it opens, not keep the stored width forever.
  const bodyMounted = present || bodyPrewarmed || fileTabs.length > 0;

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
      setFileSplitStacked(panelWidth < WORKSPACE_FILE_TREE_MIN_WIDTH + WORKSPACE_FILE_CONTENT_MIN_WIDTH);
      setFileTreeWidth((current) => {
        const next = clampWorkspaceFileTreeWidth(
          fileTreePreferredWidthRef.current,
          panelWidth,
        );
        return next === current ? current : next;
      });
    }

    const settle = createWindowResizeSettleScheduler(fitFileTreeToPanel);
    const observer = new ResizeObserver(() => {
      if (isWindowResizing()) {
        settle.schedule();
        return;
      }
      fitFileTreeToPanel();
    });
    observer.observe(splitElement);
    fitFileTreeToPanel();
    return () => {
      settle.cancel();
      observer.disconnect();
    };
  }, [bodyMounted]);

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

  function startFileTreeDockDrag(event: ReactDragEvent<HTMLElement>): void {
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

  function handleFileTreeDockKeyDown(event: ReactKeyboardEvent<HTMLElement>): void {
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

  async function requestCloseTab(tab: WorkspaceViewTab): Promise<void> {
    if (
      tab.kind === "file" &&
      dirtyFileTabIDs.has(tab.id) &&
      !(await confirmAction({
        title: t("workspace.unsavedCloseTitle"),
        message: t("workspace.unsavedCloseConfirm", { name: workspaceViewTabLabel(tab) }),
        confirmLabel: t("workspace.discardChanges"),
        tone: "danger",
      }))
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

  const headerTabs = visibleTabs.map((tab) => {
    const busy = (tab.kind === "terminal" && terminalThread?.status === "in_progress") ||
      (tab.kind === "browser" && browserActivity?.state === "active") ||
      (tab.kind === "side-thread" && sideThreadRunning);
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
    title: conversationActive ? conversationTab?.title : activeTab ? workspaceViewTabLabel(activeTab) : t("workspace.artifactsAndTools"),
    subtitle: !conversationActive && (activeTab?.kind === "file" || activeTab?.kind === "diff") ? activeTab.path : undefined,
    tabs: conversationTab ? [{ id: WORKSPACE_CONVERSATION_TAB_ID, title: conversationTab.title, kind: "conversation" }, ...headerTabs] : headerTabs,
    activeTabId: selectedTabID,
    busy: headerTabs.some((tab) => tab.busy) || undefined,
    dirty: headerTabs.some((tab) => tab.dirty) || undefined,
  });

  return (
    <aside
      className={`workspace-right-panel${showingPicker ? " tools" : " detail"}${activeTab?.kind === "review" ? " review" : ""}${activeTab?.kind === "diff" ? " diff" : ""}${activeTab?.kind === "files" || activeTab?.kind === "file" ? " files" : ""}${activeTab?.kind === "terminal" ? " terminal" : ""}${activeTab?.kind === "browser" ? " browser" : ""}${focusedComposer ? " document-focus" : ""}`}
      data-wuu-component="workspace-panel"
      data-wuu-view={conversationActive ? "conversation" : activeTab?.kind ?? "picker"}
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
            activeViewKind: conversationActive ? "conversation" : activeTab?.kind,
          })}
        />
        <HeaderPresentation
          snapshot={headerSnapshot}
          host={pluginHost}
          controller={workbenchController}
          onSelectTab={(tabId) => {
            if (tabId === WORKSPACE_CONVERSATION_TAB_ID) conversationTab?.onSelect();
            else onSelectTab(tabId);
          }}
          onCloseTab={(tabId) => {
            const tab = tabs.find((candidate) => candidate.id === tabId);
            if (tab) void requestCloseTab(tab);
          }}
          fallback={(
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
              data-scroll-fade="inline"
              onKeyDown={handleTabListKeyDown}
            >
              {conversationTab ? (
                <div
                  className={`workspace-tool-tab workspace-conversation-tab${conversationActive ? " active" : ""}`}
                  data-wuu-component="workspace-conversation-tab"
                  data-wuu-active={conversationActive ? "true" : "false"}
                >
                  <Tooltip content={conversationTab.title}>
                    <button
                      className="workspace-tool-tab-main"
                      type="button"
                      role="tab"
                      aria-selected={conversationActive}
                      aria-label={conversationTab.title}
                      tabIndex={conversationActive ? 0 : -1}
                      disabled={!open}
                      onClick={conversationTab.onSelect}
                    >
                      <MessageCircle className="icon" />
                      <span>{conversationTab.title}</span>
                    </button>
                  </Tooltip>
                </div>
              ) : null}
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
                const active = tab.id === selectedTabID;
                return (
                  <SortableWorkspaceViewTab
                    key={tab.id}
                    tab={tab}
                    active={active}
                    dirty={tab.kind === "file" && dirtyFileTabIDs.has(tab.id)}
                    busy={tab.kind === "side-thread" && sideThreadRunning}
                    open={open}
                    reorderable={visibleTabs.length > 1}
                    onSelect={() => onSelectTab(tab.id)}
                    onClose={() => void requestCloseTab(tab)}
                    onDoubleClick={() => void requestCloseTab(tab)}
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
            <DragOverlay dropAnimation={dropAnimation}>
              {draggingTab ? (
                <WorkspaceViewTabPreview
                  tab={draggingTab}
                  active={draggingTab.id === selectedTabID}
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
          className="icon-button workspace-panel-add"
          type="button"
          aria-label={t("workspace.newPage")}
          disabled={!open}
          onClick={onShowTools}
        >
          <Plus />
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
            </>
          )}
        />
      </div>
      {bodyMounted ? (
        <>
          <div
            className={`workspace-panel-body${showingPicker ? " picker" : ""}`}
            inert={conversationActive}
            aria-hidden={conversationActive || undefined}
          >
            <div
              className={`workspace-files-split${resizingFileSplit ? " resizing" : ""}${fileTreeShown ? "" : " tree-hidden"}${draggingFileTree ? " tree-dragging" : ""}`}
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
                ref={fileContentRef}
                data-wuu-component="workspace-file-content"
                aria-label={t("workspace.fileContent")}
              >
                <div className="workspace-files-content-body">
                  {fileTabs.map((tab) => (
                    <WorkspaceFileResource
                      active={open && !conversationActive && activeTab?.kind === "file" && tab.id === activeFileTabID}
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
                </div>
                {focusedComposer && activeTab?.kind === "file" ? (
                  <div ref={documentComposerRef} className="workspace-document-composer" data-testid="workspace-document-composer">
                    {focusedComposer}
                  </div>
                ) : null}
              </section>
              <div
                className="workspace-files-resizer"
                hidden={!fileTreeBesideDocument}
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
                className={`workspace-files-tree${fileTreeBesideDocument ? " dockable" : ""}`}
                data-wuu-component="workspace-file-tree"
                aria-label={t("workspace.fileTree")}
                hidden={!fileTreeShown}
                ref={fileTreeRef}
              >
                {/* Docking sides only mean something beside a document: the
                    button moves the tree across, and can also be dragged. */}
                {fileTreeBesideDocument ? (
                  <Tooltip content={moveFileTreeLabel} side="bottom">
                    <button
                      className="workspace-file-tree-drag-handle"
                      type="button"
                      draggable
                      aria-label={moveFileTreeLabel}
                      onClick={() => moveFileTree(fileTreeSide === "right" ? "left" : "right")}
                      onDragStart={startFileTreeDockDrag}
                      onDragEnd={finishFileTreeDockDrag}
                      onKeyDown={handleFileTreeDockKeyDown}
                    >
                      {fileTreeSide === "right" ? <PanelLeft className="icon" /> : <PanelRight className="icon" />}
                    </button>
                  </Tooltip>
                ) : null}
                <WorkspaceFileTree
                  activeContext={workspaceContext}
                  open={
                    open &&
                    fileTreeShown &&
                    (activeTab?.kind === "files" || activeTab?.kind === "file")
                  }
                  selectedFilePath={selectedFilePath}
                  onOpenFile={onOpenFile}
                />
              </section>
              {activeTab?.kind === "file" && !fileTreeDocked ? (
                <button
                  className={`icon-button workspace-file-tree-reveal ${fileTreeSide}`}
                  type="button"
                  aria-label={t("workspace.showFileTree")}
                  title={t("workspace.showFileTree")}
                  onClick={() => (stackedFileView ? onOpenTool("files") : setFileTreeVisibility(true))}
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
            {tabs.some((tab) => tab.kind === "browser") ? (
              <div
                className="workspace-panel-content-swap"
                hidden={activeTab?.kind !== "browser"}
                aria-hidden={activeTab?.kind !== "browser"}
              >
                <WorkspaceBrowserPanel
                  visible={open && !conversationActive && activeTab?.kind === "browser"}
                  threadID={terminalThread?.id}
                  activeContext={activeContext}
                  activity={browserActivity}
                  dockTarget={browserDockTarget}
                  requestedURL={browserNavigation}
                  overlaySuppressed={browserOverlaySuppressed}
                  onUserInteraction={onBrowserUserInteraction}
                />
              </div>
            ) : null}
            {terminalMounted && terminalTabOpen ? (
              <div
                className="workspace-panel-content-swap"
                hidden={activeTab?.kind !== "terminal"}
                aria-hidden={activeTab?.kind !== "terminal"}
              >
                <WorkspaceTerminalHost
                  active={open && !conversationActive && activeTab?.kind === "terminal"}
                  activeContext={workspaceContext}
                  thread={terminalThread}
                />
              </div>
            ) : null}
            {tabs.some((tab) => tab.kind === "side-thread") ? (
              <div
                className="workspace-side-thread-content"
                hidden={activeTab?.kind !== "side-thread"}
                inert={!open || conversationActive || activeTab?.kind !== "side-thread"}
              >
                {sideThreadContent}
              </div>
            ) : null}
            {activeTab?.kind === "files" || activeTab?.kind === "file" || activeTab?.kind === "browser" || activeTab?.kind === "terminal" || activeTab?.kind === "side-thread" ? null : (
              <div
                className="workspace-panel-content-swap"
                key={activeTab?.id ?? "picker"}
              >
                {showingPicker ? (
                  <WorkspaceToolPicker
                    pluginTools={pluginTools}
                    tabs={tabs}
                    onResumeTab={onResumeTab}
                    onSelectTool={onOpenTool}
                    onSelectPluginTool={onOpenPluginTool}
                  />
                ) : activeTab.kind === "diff" ? (
                  <TurnFileDiffPanel
                    selection={activeTab.selection}
                    onClose={() => onCloseTab(activeTab.id)}
                  />
                ) : activeTab.kind === "artifact" ? (
                  <div className="workspace-artifact-document" ref={artifactContentRef}>
                  <ArtifactPreview
                    active={open && !conversationActive}
                    artifact={activeTab.artifact}
                    motion={activeTab.motion}
                    cwd={activeTab.cwd}
                    mode="panel"
                    onClose={() => onCloseTab(activeTab.id)}
                  />
                  {focusedComposer ? (
                    <div ref={documentComposerRef} className="workspace-document-composer" data-testid="workspace-document-composer">
                      {focusedComposer}
                    </div>
                  ) : null}
                  </div>
                ) : activeTab.kind === "review" ? (
                  <WorkspaceReviewPanel
                    gitStatus={gitStatus}
                    workspaceRoot={workspaceContext?.cwd}
                    onOpenFile={onOpenFile}
                  />
                ) : activeTab.kind === "project" ? (
                  <ProjectPanel projectID={activeTab.projectID} />
                ) : activeTab.kind === "plugin" && workbenchController ? (
                  <PluginViewContent
                    controller={workbenchController}
                    pluginId={activeTab.pluginId}
                    viewTypeId={activeTab.viewTypeId}
                    region="auxiliary"
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

function WorkspaceTerminalHost({
  active,
  activeContext,
  thread,
}: {
  active: boolean;
  activeContext?: RuntimeContext;
  thread?: Thread;
}): JSX.Element {
  const [Panel, setPanel] = useState<WorkspaceTerminalPanelComponent | undefined>(
    () => cachedWorkspaceTerminalPanel,
  );
  useEffect(() => {
    if (Panel) {
      return undefined;
    }
    let cancelled = false;
    void loadWorkspaceTerminalPanel().then(
      (loaded) => {
        if (!cancelled) {
          setPanel(() => loaded);
        }
      },
      () => undefined,
    );
    return () => {
      cancelled = true;
    };
  }, [Panel]);
  if (!Panel) {
    return <WorkspacePanelLoading />;
  }
  return <Panel active={active} activeContext={activeContext} thread={thread} />;
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
  onOpenFile: (path: string, sourceContext?: RuntimeContext) => void;
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
      tab.context,
    );
  }, [onOpenFile, tab.path, tab.context]);

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
  busy,
  open,
  reorderable,
  onSelect,
  onClose,
  onDoubleClick
}: {
  tab: WorkspaceViewTab;
  active: boolean;
  dirty: boolean;
  busy: boolean;
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
    disabled: !reorderable,
    transition: useSortableTransition(),
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
      data-wuu-tab-kind={tab.kind}
      data-wuu-state={isDragging ? "dragging" : undefined}
    >
      {/* Keep the full resource path available when its title is truncated. */}
      <Tooltip content={tooltip}>
        <button
          ref={setActivatorNodeRef}
          className="workspace-tool-tab-main"
          type="button"
          {...dragAttributes}
          {...listeners}
          role="tab"
          aria-selected={active}
          aria-busy={busy || undefined}
          aria-label={dirty ? t("workspace.tabUnsaved", { label }) : label}
          tabIndex={active ? 0 : -1}
          disabled={!open}
          onClick={onSelect}
          onDoubleClick={onDoubleClick}
        >
        {busy ? <LoaderCircle className="icon control-busy-icon" aria-hidden="true" /> : <WorkspaceViewTabIcon tab={tab} className="icon" />}
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
      data-wuu-tab-kind={tab.kind}
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
  pluginTools,
  tabs,
  onResumeTab,
  onSelectTool,
  onSelectPluginTool,
}: {
  pluginTools: readonly RegisteredPluginViewEntry[];
  tabs: readonly WorkspaceViewTab[];
  onResumeTab: (id: string) => void;
  onSelectTool: (view: WorkspacePanelView) => void;
  onSelectPluginTool: (entry: RegisteredPluginViewEntry) => void;
}): JSX.Element {
  const { t } = useI18n();
  const resumableTabs = tabs.filter((tab) =>
    tab.kind === "file" || tab.kind === "diff" || tab.kind === "artifact",
  ).slice(-4).reverse();
  // The list moves like a menu: arrows walk the tools, Home and End jump.
  function handleKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    const items = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(".workspace-tool-menu-item, .workspace-tool-menu-more > summary"))
      .filter((item) => item.tagName === "SUMMARY" || !item.closest("details:not([open])"));
    const index = items.indexOf(document.activeElement as HTMLElement);
    const target =
      event.key === "ArrowDown" ? items[(index + 1) % items.length]
        : event.key === "ArrowUp" ? items[(index - 1 + items.length) % items.length]
          : event.key === "Home" ? items[0]
            : event.key === "End" ? items.at(-1)
              : undefined;
    if (target) {
      event.preventDefault();
      target.focus();
    }
  }
  return (
    <div
      className="workspace-tool-menu"
      aria-label={t("workspace.tools")}
      data-wuu-component="workspace-tool-picker"
      onKeyDown={handleKeyDown}
    >
      <section className="workspace-tool-menu-section" aria-label={t("workspace.tools")}>
        <h2>{t("workspace.tools")}</h2>
        <div className="workspace-tool-menu-list">
          {WORKSPACE_TOOL_ITEMS.map((item) => (
            <button
              key={item.id}
              className="workspace-tool-menu-item"
              data-wuu-component="workspace-tool"
              data-wuu-tool={item.id}
              type="button"
              onClick={() => onSelectTool(item.id)}
            >
              <span className="workspace-tool-menu-icon" aria-hidden="true">
                <WorkspaceToolIcon view={item.id} className="icon" />
              </span>
              <span className="workspace-tool-menu-copy">
                <strong>{t(item.titleKey)}</strong>
              </span>
            </button>
          ))}
        </div>
        {pluginTools.length > 0 ? (
          <details className="workspace-tool-menu-more">
            <summary>
              <span className="workspace-tool-menu-icon" aria-hidden="true"><LayoutGrid className="icon" /></span>
              <span>{t("workspace.moreTools")}</span>
              <ChevronDown className="icon workspace-tool-menu-chevron" aria-hidden="true" />
            </summary>
            <div className="workspace-tool-menu-list">
              {pluginTools.map((item) => {
                const tabID = `plugin:${item.pluginId}:${item.id}`;
                return (
                  <button
                    key={tabID}
                    className="workspace-tool-menu-item"
                    data-wuu-component="workspace-tool"
                    data-wuu-plugin={item.pluginId}
                    type="button"
                    onClick={() => onSelectPluginTool(item)}
                  >
                    <span className="workspace-tool-menu-icon" aria-hidden="true">
                      <PluginIcon icon={item.icon} pluginId={item.pluginId} fingerprint={item.generation} className="icon" />
                    </span>
                    <span className="workspace-tool-menu-copy">
                      <strong>{item.title}</strong>
                      {item.description ? <span>{item.description}</span> : null}
                    </span>
                  </button>
                );
              })}
            </div>
          </details>
        ) : null}
      </section>
      {resumableTabs.length > 0 ? (
        <section className="workspace-tool-menu-section" aria-label={t("workspace.continueViewing")}>
          <h2>{t("workspace.continueViewing")}</h2>
          <div className="workspace-tool-menu-list">
            {resumableTabs.map((tab) => (
              <button
                key={tab.id}
                type="button"
                className="workspace-tool-menu-item workspace-tool-menu-resume"
                data-wuu-component="workspace-resume-tab"
                title={workspaceViewTabTooltip(tab)}
                onClick={() => onResumeTab(tab.id)}
              >
                <span className="workspace-tool-menu-icon" aria-hidden="true"><WorkspaceViewTabIcon tab={tab} className="icon" /></span>
                <span className="workspace-tool-menu-copy">
                  <strong>{workspaceViewTabLabel(tab)}</strong>
                  {tab.kind === "file" || tab.kind === "diff" ? <span>{tab.path}</span> : null}
                </span>
              </button>
            ))}
          </div>
        </section>
      ) : null}
    </div>
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
  if (tab.kind === "new") return translateCurrent("workspace.newPage");
  if (tab.kind === "side-thread") return translateCurrent("sideThread.title");
  return tab.kind === "diff" || tab.kind === "file" || tab.kind === "plugin" || tab.kind === "artifact" || tab.kind === "project"
    ? tab.title
    : translateCurrent(workspaceToolFor(tab.kind).titleKey);
}

function workspaceViewTabTooltip(tab: WorkspaceViewTab): string {
  if (tab.kind === "new") return translateCurrent("workspace.newPage");
  if (tab.kind === "side-thread") return translateCurrent("sideThread.title");
  if (tab.kind === "plugin" || tab.kind === "artifact" || tab.kind === "project") return tab.title;
  return tab.kind === "diff" || tab.kind === "file"
    ? tab.path
    : translateCurrent(workspaceToolFor(tab.kind).titleKey);
}

function WorkspaceViewTabIcon({ tab, className }: { tab: WorkspaceViewTab; className?: string }): JSX.Element {
  if (tab.kind === "new") return <LayoutGrid className={className} />;
  if (tab.kind === "side-thread") return <MessageCircle className={className} />;
  if (tab.kind === "diff") {
    return <FileDiff className={className} />;
  }
  if (tab.kind === "project") {
    return <Project className={className} />;
  }
  if (tab.kind === "file" || tab.kind === "artifact") {
    return <FileText className={className} />;
  }
  if (tab.kind === "plugin") {
    return <PluginIcon icon={tab.icon} pluginId={tab.pluginId} fingerprint={tab.fingerprint} className={className} />;
  }
  return <WorkspaceToolIcon view={tab.kind} className={className} />;
}
