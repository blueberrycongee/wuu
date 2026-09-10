import {
  type KeyboardEvent as ReactKeyboardEvent,
  type MutableRefObject,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
  useCallback,
  useEffect,
  useRef,
  useState
} from "react";
import {
  LAYOUT_MOTION_CLASS,
  WINDOW_RESIZING_CLASS,
} from "./WindowResizeState";
import { motionDurationMs } from "./motion";
import { isTouchWebShell } from "./ComposerFocus";

export const SIDEBAR_MOTION_MS = motionDurationMs(
  "--sidebar-motion-duration",
  280,
);
export const SIDEBAR_DRAWER_EXIT_MS = motionDurationMs(
  "--sidebar-drawer-exit-duration",
  220,
);
export const RIGHT_PANEL_MOTION_MS = motionDurationMs(
  "--workspace-panel-motion-duration",
  280,
);
export const SIDEBAR_DEFAULT_WIDTH = 326;
// Keep enough horizontal room for one-line navigation labels and useful
// conversation titles. The rail becomes an overlay drawer below the compact
// breakpoint, so shrinking it further saves no canvas space and only makes
// its contents harder to scan.
export const SIDEBAR_MIN_WIDTH = 240;
export const SIDEBAR_MAX_WIDTH = 520;
// The desktop opens at 1280px. Below that design width, the whole sidebar
// range scales together; wider windows keep the familiar pixel sizes.
export const SIDEBAR_SCALE_REFERENCE_WINDOW_WIDTH = 1280;
// Fonts and icons do not scale with the window, so the readable floor must not
// scale down either. Narrow windows auto-collapse the rail into a drawer.
export const SIDEBAR_SCALED_MIN_WIDTH = SIDEBAR_MIN_WIDTH;
export const SIDEBAR_AUTO_COLLAPSE_WINDOW_WIDTH = 900;
// Below this width the renderer switches from multi-column/tab rails to
// single-surface navigation. Touch phones retain it in landscape; tablets and
// desktop windows use the available viewport width.
export const COMPACT_NAVIGATION_WINDOW_WIDTH = 760;
// Keep a small pull-past-minimum dead zone so resizing to the minimum width
// does not collapse the sidebar by accident.
const SIDEBAR_COLLAPSE_INTENT_PX = 32;
const SIDEBAR_COLLAPSE_WIDTH = SIDEBAR_MIN_WIDTH - SIDEBAR_COLLAPSE_INTENT_PX;
const SIDEBAR_STEP = 24;
const SIDEBAR_WIDTH_KEY = "wuu.desktop.sidebarWidth";
const SIDEBAR_COLLAPSED_KEY = "wuu.desktop.sidebarCollapsed";
export const WORKSPACE_RIGHT_PANEL_DEFAULT_WIDTH = 360;
export const WORKSPACE_RIGHT_PANEL_MIN_WIDTH = 300;
export const WORKSPACE_RIGHT_PANEL_MAX_WIDTH = 860;
export const WORKSPACE_RIGHT_PANEL_MAIN_MIN_WIDTH = 360;
export const WORKSPACE_CONVERSATION_SAFE_WIDTH = 440;
export const WORKSPACE_DOCKED_PANEL_SAFE_WIDTH = 320;
// The narrowest drag range the panel must always keep. Without it, a tight
// window collapses min === max and the panel can't be dragged at all.
export const WORKSPACE_RIGHT_PANEL_MIN_DRAG_RANGE = 120;
const WORKSPACE_RIGHT_PANEL_STEP = 32;

const WORKSPACE_RIGHT_PANEL_WIDTH_KEY = "wuu.desktop.workspaceRightPanelWidth";
// The fork/split view (源会话 | 分叉) is proportional rather than a fixed pixel
// panel, so its divider is stored as the left pane's share of the container
// width. Clamped away from the extremes so neither pane collapses to unusable.
export const CONVERSATION_SPLIT_DEFAULT_PERCENT = 50;
export const CONVERSATION_SPLIT_MIN_PERCENT = 20;
export const CONVERSATION_SPLIT_MAX_PERCENT = 80;
const CONVERSATION_SPLIT_STEP = 4;
const CONVERSATION_SPLIT_PERCENT_KEY = "wuu.desktop.conversationSplitLeftPercent";

type SidebarResizeSession = {
  startX: number;
  startWidth: number;
  currentWidth: number;
  // Tracks whether the sidebar collapsed during the current drag (either at
  // pointerdown or by crossing the collapse-intent width mid-drag). When true
  // and the user drags back above that width without releasing, the move
  // handler must route through applySidebarWidth so the collapsed React state
  // is cleared; otherwise applyLiveSidebarWidth would only mutate the
  // --sidebar-width inline style while the .sidebar-collapsed class stays on
  // .app-shell, leaving a white glass slab with no content visible.
  collapsedDuringDrag: boolean;
};

type RightPanelResizeSession = {
  startX: number;
  startWidth: number;
  currentWidth: number;
};

type SplitResizeSession = {
  startX: number;
  startPercent: number;
  // Captured at pointerdown so mid-drag moves can convert a pixel delta into a
  // percentage without measuring the container every frame.
  containerWidth: number;
  currentPercent: number;
};

type WindowPointerResizeController<Session> = {
  resizing: boolean;
  sessionRef: MutableRefObject<Session | null>;
  onMove: (event: PointerEvent, session: Session) => void;
  onEnd: (session: Session | null) => void;
};

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

function sidebarWindowScale(windowWidth: number): number {
  return Math.min(1, Math.max(0, windowWidth) / SIDEBAR_SCALE_REFERENCE_WINDOW_WIDTH);
}

function sidebarMinWidthForWindow(windowWidth: number): number {
  return Math.max(
    SIDEBAR_SCALED_MIN_WIDTH,
    Math.floor(SIDEBAR_MIN_WIDTH * sidebarWindowScale(windowWidth))
  );
}

function sidebarMaxWidthForWindow(windowWidth: number): number {
  return Math.max(
    sidebarMinWidthForWindow(windowWidth),
    Math.floor(SIDEBAR_MAX_WIDTH * sidebarWindowScale(windowWidth))
  );
}

function sidebarCollapseWidthForWindow(windowWidth: number): number {
  return Math.floor(SIDEBAR_COLLAPSE_WIDTH * sidebarWindowScale(windowWidth));
}

function sidebarShouldCollapse(width: number, windowWidth: number): boolean {
  return width <= sidebarCollapseWidthForWindow(windowWidth);
}

function sidebarShouldAutoCollapseForWindow(width: number): boolean {
  return width < SIDEBAR_AUTO_COLLAPSE_WINDOW_WIDTH;
}

export function clampSidebarWidthForWindow(width: number, windowWidth: number): number {
  const scale = sidebarWindowScale(windowWidth);
  return clamp(
    Math.floor(clamp(width, SIDEBAR_MIN_WIDTH, SIDEBAR_MAX_WIDTH) * scale),
    sidebarMinWidthForWindow(windowWidth),
    sidebarMaxWidthForWindow(windowWidth)
  );
}

function clampSidebarDisplayWidth(width: number, windowWidth: number): number {
  return clamp(
    width,
    sidebarMinWidthForWindow(windowWidth),
    sidebarMaxWidthForWindow(windowWidth)
  );
}

function sidebarPreferredWidthForDisplay(width: number, windowWidth: number): number {
  const scale = sidebarWindowScale(windowWidth);
  if (scale === 0) {
    return SIDEBAR_DEFAULT_WIDTH;
  }
  if (width <= sidebarMinWidthForWindow(windowWidth)) {
    return SIDEBAR_MIN_WIDTH;
  }
  return clamp(
    Math.round(clampSidebarDisplayWidth(width, windowWidth) / scale),
    SIDEBAR_MIN_WIDTH,
    SIDEBAR_MAX_WIDTH
  );
}

// Number(null) is 0, not NaN, so a missing key must be checked explicitly —
// otherwise a fresh profile boots with every panel clamped to its minimum.
function storedWidth(key: string, fallback: number, min: number, max: number): number {
  const raw = window.localStorage.getItem(key);
  if (raw === null) {
    return fallback;
  }
  const stored = Number(raw);
  if (!Number.isFinite(stored)) {
    return fallback;
  }
  return clamp(stored, min, max);
}

function initialSidebarWidth(): number {
  return storedWidth(SIDEBAR_WIDTH_KEY, SIDEBAR_DEFAULT_WIDTH, SIDEBAR_MIN_WIDTH, SIDEBAR_MAX_WIDTH);
}

function initialSidebarCollapsed(): boolean {
  return (
    window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "true" ||
    sidebarShouldAutoCollapseForWindow(window.innerWidth)
  );
}

function initialWorkspaceRightPanelWidth(): number {
  return storedWidth(
    WORKSPACE_RIGHT_PANEL_WIDTH_KEY,
    WORKSPACE_RIGHT_PANEL_DEFAULT_WIDTH,
    WORKSPACE_RIGHT_PANEL_MIN_WIDTH,
    WORKSPACE_RIGHT_PANEL_MAX_WIDTH
  );
}

function initialConversationSplitPercent(): number {
  return storedWidth(
    CONVERSATION_SPLIT_PERCENT_KEY,
    CONVERSATION_SPLIT_DEFAULT_PERCENT,
    CONVERSATION_SPLIT_MIN_PERCENT,
    CONVERSATION_SPLIT_MAX_PERCENT
  );
}

// Exported for unit testing: this pure helper is the single gate every
// right-panel width change (live drag, commit, keyboard, window-resize) passes
// through, so its clamp range determines whether the panel is draggable at all.
export function clampWorkspaceRightPanelWidth(width: number, sidebarWidth: number): number {
  const maxForWindow =
    typeof window === "undefined"
      ? WORKSPACE_RIGHT_PANEL_MAX_WIDTH
      : window.innerWidth - sidebarWidth - WORKSPACE_RIGHT_PANEL_MAIN_MIN_WIDTH;
  // Always keep the ceiling at least MIN_DRAG_RANGE above the floor. The old
  // formula was `max(MIN, min(MAX, maxForWindow))`, which on a tight window
  // (small maxForWindow) collapsed to exactly MIN — min === max — so the
  // resizer had zero travel and dragging did nothing. Guaranteeing the range
  // lets the conversation area dip below its *preferred* MAIN_MIN on narrow
  // windows, which is the correct trade for keeping the panel resizable.
  const maxWidth = clamp(
    maxForWindow,
    WORKSPACE_RIGHT_PANEL_MIN_WIDTH + WORKSPACE_RIGHT_PANEL_MIN_DRAG_RANGE,
    WORKSPACE_RIGHT_PANEL_MAX_WIDTH
  );
  return clamp(width, WORKSPACE_RIGHT_PANEL_MIN_WIDTH, maxWidth);
}

export function workspacePanelNeedsFocus(
  windowWidth: number,
  sidebarWidth: number,
): boolean {
  return (
    windowWidth - sidebarWidth <
    WORKSPACE_CONVERSATION_SAFE_WIDTH + WORKSPACE_DOCKED_PANEL_SAFE_WIDTH
  );
}

function useWindowPointerResize<Session>({
  resizing,
  sessionRef,
  onMove,
  onEnd,
}: WindowPointerResizeController<Session>): void {
  useEffect(() => {
    if (!resizing) {
      return;
    }

    function handlePointerMove(event: PointerEvent): void {
      const session = sessionRef.current;
      if (!session) {
        return;
      }
      onMove(event, session);
    }

    function handlePointerEnd(): void {
      const session = sessionRef.current;
      sessionRef.current = null;
      onEnd(session);
    }

    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerEnd);
    window.addEventListener("pointercancel", handlePointerEnd);
    return () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerEnd);
      window.removeEventListener("pointercancel", handlePointerEnd);
    };
  }, [onEnd, onMove, resizing, sessionRef]);
}

type LiveWidthWriter = {
  schedule: (width: number) => void;
  cancel: () => void;
  flush: () => void;
};

// Coalesces per-pointermove width updates into one write per animation frame
// so drags mutate a CSS variable instead of re-rendering the React tree.
function useLiveWidthWriter(write: (width: number) => void): LiveWidthWriter {
  const frameRef = useRef<number | undefined>(undefined);
  const pendingRef = useRef<number | undefined>(undefined);

  const cancel = useCallback((): void => {
    if (frameRef.current !== undefined) {
      window.cancelAnimationFrame(frameRef.current);
      frameRef.current = undefined;
    }
    pendingRef.current = undefined;
  }, []);

  const flush = useCallback((): void => {
    if (frameRef.current !== undefined) {
      window.cancelAnimationFrame(frameRef.current);
      frameRef.current = undefined;
    }
    const pendingWidth = pendingRef.current;
    pendingRef.current = undefined;
    if (pendingWidth !== undefined) {
      write(pendingWidth);
    }
  }, [write]);

  const schedule = useCallback(
    (nextWidth: number): void => {
      pendingRef.current = nextWidth;
      if (frameRef.current !== undefined) {
        return;
      }
      frameRef.current = window.requestAnimationFrame(() => {
        frameRef.current = undefined;
        const pendingWidth = pendingRef.current;
        pendingRef.current = undefined;
        if (pendingWidth !== undefined) {
          write(pendingWidth);
        }
      });
    },
    [write]
  );

  return { schedule, cancel, flush };
}

export function useAppLayoutState({
  layoutRootRef,
  settingsLayoutRootRef,
  onCloseProjectMenu
}: {
  layoutRootRef?: RefObject<HTMLElement | null>;
  // The settings shell mounts in place of the main app shell but shares the
  // same sidebar width/collapse state. Live drag writes must land on
  // whichever root is currently mounted, so both refs are consulted.
  settingsLayoutRootRef?: RefObject<HTMLElement | null>;
  onCloseProjectMenu: () => void;
}): {
  compactNavigation: boolean;
  sidebarWidth: number;
  sidebarCollapsed: boolean;
  setSidebarCollapsed: (collapsed: boolean) => void;
  resizingSidebar: boolean;
  sidebarAnimating: boolean;
  workspaceRightPanelWidth: number;
  clampedWorkspaceRightPanelWidth: number;
  resizingRightPanel: boolean;
  rightPanelOpen: boolean;
  rightPanelAnimating: boolean;
  effectiveSidebarWidth: number;
  workspaceRightPanelAutoGlobalized: boolean;
  workspaceRightPanelDockableWithoutSidebar: boolean;
  setRightPanelOpenWithMotion: (open: boolean) => void;
  animateRightPanelLayout: () => void;
  animateSidebarLayout: () => void;
  startSidebarResize: (event: ReactPointerEvent<HTMLDivElement>) => void;
  startRightPanelResize: (event: ReactPointerEvent<HTMLDivElement>) => void;
  handleRightPanelSeparatorKey: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
  resetWorkspaceRightPanelWidth: () => void;
  toggleSidebar: () => void;
  handleSidebarSeparatorKey: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
  splitLeftPercent: number;
  resizingSplit: boolean;
  startSplitResize: (event: ReactPointerEvent<HTMLDivElement>) => void;
  handleSplitSeparatorKey: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
  resetSplitPercent: () => void;
} {
  const [sidebarPreferredWidth, setSidebarPreferredWidth] = useState(initialSidebarWidth);
  const [windowWidth, setWindowWidth] = useState(() => window.innerWidth);
  const [sidebarCollapsed, setSidebarCollapsedState] = useState(initialSidebarCollapsed);
  const [resizingSidebar, setResizingSidebar] = useState(false);
  const [sidebarAnimating, setSidebarAnimating] = useState(false);
  const [workspaceRightPanelWidth, setWorkspaceRightPanelWidth] = useState(initialWorkspaceRightPanelWidth);
  const [resizingRightPanel, setResizingRightPanel] = useState(false);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const [rightPanelAnimating, setRightPanelAnimating] = useState(false);
  const [splitLeftPercent, setSplitLeftPercent] = useState(initialConversationSplitPercent);
  const [resizingSplit, setResizingSplit] = useState(false);
  const resizeSessionRef = useRef<SidebarResizeSession | null>(null);
  const rightPanelResizeSessionRef = useRef<RightPanelResizeSession | null>(null);
  const splitResizeSessionRef = useRef<SplitResizeSession | null>(null);
  const sidebarMotionTimerRef = useRef<number | undefined>(undefined);
  const rightPanelMotionTimerRef = useRef<number | undefined>(undefined);
  const sidebarAutoCollapsedRef = useRef(
    sidebarShouldAutoCollapseForWindow(window.innerWidth) &&
      window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) !== "true"
  );
  const setSidebarCollapsed = useCallback((collapsed: boolean): void => {
    sidebarAutoCollapsedRef.current = false;
    setSidebarCollapsedState(collapsed);
  }, []);
  const sidebarWidth = clampSidebarWidthForWindow(sidebarPreferredWidth, windowWidth);
  const screenShortSide = Math.min(window.screen.width, window.screen.height);
  const phoneNavigation = isTouchWebShell() && screenShortSide > 0 && screenShortSide < COMPACT_NAVIGATION_WINDOW_WIDTH;
  const compactNavigation = phoneNavigation || windowWidth < COMPACT_NAVIGATION_WINDOW_WIDTH;
  // Compact sidebars overlay the conversation even when Settings changes the
  // desktop docking preference. Never reserve a hidden desktop column.
  const effectiveSidebarWidth = compactNavigation || sidebarCollapsed ? 0 : sidebarWidth;
  // Auto-globalize the open right panel only when the window is too narrow to
  // dock conversation + panel even with the sidebar fully collapsed — i.e. we
  // measure the space WITHOUT the sidebar's width. Opening the sidebar no
  // longer shoves an already-docked panel into fullscreen focus: in the medium
  // band all three columns stay docked and the conversation column
  // (minmax(0,1fr)) absorbs the squeeze, dipping below its preferred safe
  // width. This keeps an explicit "open the sidebar" from silently globalizing
  // the right panel. The narrow floor (windowWidth < 760) still globalizes,
  // since conversation + panel cannot both fit there regardless of the sidebar.
  // (Previously this passed effectiveSidebarWidth, so opening the sidebar could
  // tip the layout over the threshold and auto-globalize the panel.)
  const workspaceRightPanelAutoGlobalized = phoneNavigation || workspacePanelNeedsFocus(
    windowWidth,
    0,
  );
  // Complement of the above by construction: if the panel can't dock without
  // the sidebar it auto-globalizes, and vice versa.
  const workspaceRightPanelDockableWithoutSidebar =
    !workspaceRightPanelAutoGlobalized;
  const clampedWorkspaceRightPanelWidth = clampWorkspaceRightPanelWidth(
    workspaceRightPanelWidth,
    effectiveSidebarWidth
  );
  const startSidebarMotion = useCallback((): void => {
    if (sidebarMotionTimerRef.current !== undefined) {
      window.clearTimeout(sidebarMotionTimerRef.current);
    }
    setSidebarAnimating(true);
    sidebarMotionTimerRef.current = window.setTimeout(() => {
      sidebarMotionTimerRef.current = undefined;
      setSidebarAnimating(false);
    }, SIDEBAR_MOTION_MS);
  }, []);

  const startRightPanelMotion = useCallback((): void => {
    if (rightPanelMotionTimerRef.current !== undefined) {
      window.clearTimeout(rightPanelMotionTimerRef.current);
    }
    setRightPanelAnimating(true);
    rightPanelMotionTimerRef.current = window.setTimeout(() => {
      rightPanelMotionTimerRef.current = undefined;
      setRightPanelAnimating(false);
    }, RIGHT_PANEL_MOTION_MS);
  }, []);

  // Only one shell root (main app or settings) is mounted at a time; write to
  // whichever exists so a drag started in either shell stays live.
  const sidebarLayoutRoots = useCallback((): HTMLElement[] => {
    const roots: HTMLElement[] = [];
    if (layoutRootRef?.current) {
      roots.push(layoutRootRef.current);
    }
    if (settingsLayoutRootRef?.current) {
      roots.push(settingsLayoutRootRef.current);
    }
    return roots;
  }, [layoutRootRef, settingsLayoutRootRef]);

  const writeLiveSidebarWidth = useCallback(
    (nextWidth: number): void => {
      const clampedWidth = clampSidebarDisplayWidth(nextWidth, windowWidth);
      for (const root of sidebarLayoutRoots()) {
        root.style.setProperty("--sidebar-width", `${clampedWidth}px`);
        root.style.setProperty("--sidebar-open-width", `${clampedWidth}px`);
      }
    },
    [sidebarLayoutRoots, windowWidth]
  );

  // A drag that ends collapsed leaves the live writer's clamped-to-minimum
  // --sidebar-open-width behind as an inline style. React only rewrites the
  // variable when the width state changes, so the stale 200px would size the
  // hover drawer and, after reopening, the sidebar content — while the shell
  // itself opens at the real width. Restore the remembered open width (only
  // that variable: --sidebar-width must stay 0 while collapsed).
  const restoreSidebarOpenWidth = useCallback(
    (nextWidth: number): void => {
      const clampedWidth = clampSidebarDisplayWidth(nextWidth, windowWidth);
      for (const root of sidebarLayoutRoots()) {
        root.style.setProperty("--sidebar-open-width", `${clampedWidth}px`);
      }
    },
    [sidebarLayoutRoots, windowWidth]
  );

  const writeLiveWorkspaceRightPanelWidth = useCallback(
    (nextWidth: number): void => {
      const root = layoutRootRef?.current;
      if (!root) {
        return;
      }
      const clampedWidth = clampWorkspaceRightPanelWidth(
        nextWidth,
        effectiveSidebarWidth
      );
      root.style.setProperty("--workspace-right-panel-width", `${clampedWidth}px`);
    },
    [effectiveSidebarWidth, layoutRootRef]
  );

  const writeLiveSplitPercent = useCallback(
    (nextPercent: number): void => {
      const root = layoutRootRef?.current;
      if (!root) {
        return;
      }
      const clampedPercent = clamp(
        nextPercent,
        CONVERSATION_SPLIT_MIN_PERCENT,
        CONVERSATION_SPLIT_MAX_PERCENT
      );
      root.style.setProperty("--conversation-split-left", `${clampedPercent}%`);
    },
    [layoutRootRef]
  );

  const sidebarLive = useLiveWidthWriter(writeLiveSidebarWidth);
  const rightPanelLive = useLiveWidthWriter(writeLiveWorkspaceRightPanelWidth);
  const splitLive = useLiveWidthWriter(writeLiveSplitPercent);

  const applySidebarWidth = useCallback(
    (nextWidth: number): void => {
      if (sidebarShouldCollapse(nextWidth, windowWidth)) {
        if (!sidebarCollapsed && !resizingSidebar) {
          startSidebarMotion();
        }
        onCloseProjectMenu();
        setSidebarCollapsed(true);
        // Same rule as toggleSidebar: a collapsed sidebar whose remembered
        // open width is the bare minimum reopens (hover drawer included) at
        // the comfortable default instead of the cramped 200px.
        setSidebarPreferredWidth((width) =>
          width <= SIDEBAR_MIN_WIDTH ? SIDEBAR_DEFAULT_WIDTH : width
        );
        return;
      }
      if (sidebarCollapsed && !resizingSidebar) {
        startSidebarMotion();
      }
      setSidebarCollapsed(false);
      setSidebarPreferredWidth(sidebarPreferredWidthForDisplay(nextWidth, windowWidth));
    },
    [onCloseProjectMenu, resizingSidebar, sidebarCollapsed, startSidebarMotion, windowWidth]
  );

  const applyWorkspaceRightPanelWidth = useCallback(
    (nextWidth: number): void => {
      setWorkspaceRightPanelWidth(clampWorkspaceRightPanelWidth(nextWidth, effectiveSidebarWidth));
    },
    [effectiveSidebarWidth]
  );

  const applySplitPercent = useCallback((nextPercent: number): void => {
    setSplitLeftPercent(
      clamp(nextPercent, CONVERSATION_SPLIT_MIN_PERCENT, CONVERSATION_SPLIT_MAX_PERCENT)
    );
  }, []);

  const setRightPanelOpenWithMotion = useCallback(
    (open: boolean): void => {
      if (rightPanelOpen !== open) {
        startRightPanelMotion();
      }
      setRightPanelOpen(open);
    },
    [rightPanelOpen, startRightPanelMotion]
  );

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_WIDTH_KEY, String(sidebarPreferredWidth));
    if (!sidebarAutoCollapsedRef.current) {
      window.localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(sidebarCollapsed));
    }
  }, [sidebarPreferredWidth, sidebarCollapsed]);

  useEffect(() => {
    window.localStorage.setItem(WORKSPACE_RIGHT_PANEL_WIDTH_KEY, String(workspaceRightPanelWidth));
  }, [workspaceRightPanelWidth]);

  useEffect(() => {
    window.localStorage.setItem(CONVERSATION_SPLIT_PERCENT_KEY, String(splitLeftPercent));
  }, [splitLeftPercent]);

  // This is unmount cleanup. The live writer objects are recreated during
  // render; depending on them would cancel panel motion timers mid-animation.
  useEffect(() => {
    return () => {
      if (sidebarMotionTimerRef.current !== undefined) {
        window.clearTimeout(sidebarMotionTimerRef.current);
      }
      if (rightPanelMotionTimerRef.current !== undefined) {
        window.clearTimeout(rightPanelMotionTimerRef.current);
      }
      sidebarLive.cancel();
      rightPanelLive.cancel();
      splitLive.cancel();
    };
  }, [sidebarLive.cancel, rightPanelLive.cancel, splitLive.cancel]);

  const handleSidebarResizeMove = useCallback(
    (event: PointerEvent, session: SidebarResizeSession): void => {
      const nextWidth = session.startWidth + event.clientX - session.startX;
      session.currentWidth = nextWidth;
      if (sidebarShouldCollapse(nextWidth, windowWidth)) {
        sidebarLive.cancel();
        applySidebarWidth(nextWidth);
        session.collapsedDuringDrag = true;
        return;
      }
      if (session.collapsedDuringDrag) {
        // We crossed the collapse-intent width earlier in this drag and never
        // released. Route through applySidebarWidth so setSidebarCollapsed
        // (false) clears the .sidebar-collapsed class; otherwise the live
        // inline --sidebar-width would expand the sidebar while its content
        // stayed opacity: 0, leaving a white glass slab.
        applySidebarWidth(nextWidth);
        session.collapsedDuringDrag = false;
        return;
      }
      if (nextWidth <= sidebarMinWidthForWindow(windowWidth)) {
        sidebarLive.cancel();
        applySidebarWidth(nextWidth);
        return;
      }
      sidebarLive.schedule(nextWidth);
    },
    [applySidebarWidth, sidebarLive, windowWidth]
  );

  const handleSidebarResizeEnd = useCallback(
    (session: SidebarResizeSession | null): void => {
      if (session) {
        if (session.currentWidth === session.startWidth) {
          sidebarLive.cancel();
        } else {
          if (session.currentWidth > sidebarCollapseWidthForWindow(windowWidth)) {
            sidebarLive.flush();
          } else {
            sidebarLive.cancel();
            const openPreference =
              sidebarPreferredWidth <= SIDEBAR_MIN_WIDTH
                ? SIDEBAR_DEFAULT_WIDTH
                : sidebarPreferredWidth;
            restoreSidebarOpenWidth(
              clampSidebarWidthForWindow(openPreference, windowWidth)
            );
          }
          applySidebarWidth(session.currentWidth);
        }
      }
      setResizingSidebar(false);
    },
    [
      applySidebarWidth,
      restoreSidebarOpenWidth,
      sidebarLive,
      sidebarPreferredWidth,
      windowWidth,
    ]
  );

  useWindowPointerResize({
    resizing: resizingSidebar,
    sessionRef: resizeSessionRef,
    onMove: handleSidebarResizeMove,
    onEnd: handleSidebarResizeEnd,
  });

  const handleRightPanelResizeMove = useCallback(
    (event: PointerEvent, session: RightPanelResizeSession): void => {
      const nextWidth = session.startWidth - (event.clientX - session.startX);
      session.currentWidth = nextWidth;
      rightPanelLive.schedule(nextWidth);
    },
    [rightPanelLive]
  );

  const handleRightPanelResizeEnd = useCallback((session: RightPanelResizeSession | null): void => {
    if (session) {
      rightPanelLive.flush();
      applyWorkspaceRightPanelWidth(session.currentWidth);
    } else {
      rightPanelLive.cancel();
    }
    setResizingRightPanel(false);
  }, [applyWorkspaceRightPanelWidth, rightPanelLive]);

  useWindowPointerResize({
    resizing: resizingRightPanel,
    sessionRef: rightPanelResizeSessionRef,
    onMove: handleRightPanelResizeMove,
    onEnd: handleRightPanelResizeEnd,
  });

  const handleSplitResizeMove = useCallback(
    (event: PointerEvent, session: SplitResizeSession): void => {
      const deltaPercent =
        session.containerWidth > 0
          ? ((event.clientX - session.startX) / session.containerWidth) * 100
          : 0;
      const nextPercent = session.startPercent + deltaPercent;
      session.currentPercent = nextPercent;
      splitLive.schedule(nextPercent);
    },
    [splitLive]
  );

  const handleSplitResizeEnd = useCallback(
    (session: SplitResizeSession | null): void => {
      if (session) {
        splitLive.flush();
        applySplitPercent(session.currentPercent);
      } else {
        splitLive.cancel();
      }
      setResizingSplit(false);
    },
    [applySplitPercent, splitLive]
  );

  useWindowPointerResize({
    resizing: resizingSplit,
    sessionRef: splitResizeSessionRef,
    onMove: handleSplitResizeMove,
    onEnd: handleSplitResizeEnd,
  });

  useEffect(() => {
    function handleResize(): void {
      const nextWindowWidth = window.innerWidth;
      setWindowWidth(nextWindowWidth);
      const autoCollapseSidebar =
        !resizingSidebar &&
        sidebarShouldAutoCollapseForWindow(nextWindowWidth);
      const restoreAutoCollapsedSidebar =
        !autoCollapseSidebar && sidebarAutoCollapsedRef.current;
      const nextEffectiveSidebarWidth = autoCollapseSidebar
        ? 0
        : sidebarCollapsed && !restoreAutoCollapsedSidebar
          ? 0
          : clampSidebarWidthForWindow(sidebarPreferredWidth, nextWindowWidth);
      if (autoCollapseSidebar) {
        if (!sidebarCollapsed) {
          sidebarAutoCollapsedRef.current = true;
          startSidebarMotion();
          onCloseProjectMenu();
        }
        setSidebarCollapsedState(true);
        setSidebarPreferredWidth((width) =>
          width <= SIDEBAR_MIN_WIDTH ? SIDEBAR_DEFAULT_WIDTH : width
        );
      } else if (restoreAutoCollapsedSidebar) {
        sidebarAutoCollapsedRef.current = false;
        startSidebarMotion();
        setSidebarCollapsedState(false);
      }
      setWorkspaceRightPanelWidth((current) =>
        clampWorkspaceRightPanelWidth(current, nextEffectiveSidebarWidth)
      );
    }

    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, [
    onCloseProjectMenu,
    resizingSidebar,
    sidebarCollapsed,
    sidebarPreferredWidth,
    startSidebarMotion,
  ]);

  // Sidebar / right-panel drags resize the conversation viewport every frame.
  // Reuse the window-resize deferred-scroll path so ResizeObserver consumers
  // (ConversationScrollState, AutoFollowScroll, ConversationTurnRail) stop
  // forcing layout + scrollTop writes on every pointermove.
  useEffect(() => {
    if (!resizingSidebar && !resizingRightPanel && !resizingSplit) {
      return undefined;
    }
    const root = document.documentElement;
    root.classList.add(WINDOW_RESIZING_CLASS);
    return () => {
      root.classList.remove(WINDOW_RESIZING_CLASS);
    };
  }, [resizingSidebar, resizingRightPanel, resizingSplit]);

  // Opening and closing structural panels changes the conversation viewport
  // on every CSS frame just like a live resize. Mark only the geometry phase
  // (without the window-resizing CSS that would cancel the transition) so
  // scroll followers and ResizeObserver consumers defer their layout reads
  // until the panel settles.
  useEffect(() => {
    if (!sidebarAnimating && !rightPanelAnimating) {
      return undefined;
    }
    const root = document.documentElement;
    root.classList.add(LAYOUT_MOTION_CLASS);
    return () => {
      root.classList.remove(LAYOUT_MOTION_CLASS);
    };
  }, [rightPanelAnimating, sidebarAnimating]);

  function startSidebarResize(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    resizeSessionRef.current = {
      startX: event.clientX,
      startWidth: sidebarCollapsed ? 0 : sidebarWidth,
      currentWidth: sidebarCollapsed ? 0 : sidebarWidth,
      collapsedDuringDrag: sidebarCollapsed
    };
    onCloseProjectMenu();
    setResizingSidebar(true);
  }

  function startRightPanelResize(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0 || !rightPanelOpen) {
      return;
    }
    event.preventDefault();
    rightPanelResizeSessionRef.current = {
      startX: event.clientX,
      startWidth: clampedWorkspaceRightPanelWidth,
      currentWidth: clampedWorkspaceRightPanelWidth
    };
    setResizingRightPanel(true);
  }

  function handleRightPanelSeparatorKey(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      applyWorkspaceRightPanelWidth(workspaceRightPanelWidth + WORKSPACE_RIGHT_PANEL_STEP);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      applyWorkspaceRightPanelWidth(workspaceRightPanelWidth - WORKSPACE_RIGHT_PANEL_STEP);
    } else if (event.key === "Home") {
      event.preventDefault();
      applyWorkspaceRightPanelWidth(WORKSPACE_RIGHT_PANEL_MAX_WIDTH);
    } else if (event.key === "End") {
      event.preventDefault();
      applyWorkspaceRightPanelWidth(WORKSPACE_RIGHT_PANEL_MIN_WIDTH);
    }
  }

  function resetWorkspaceRightPanelWidth(): void {
    applyWorkspaceRightPanelWidth(WORKSPACE_RIGHT_PANEL_DEFAULT_WIDTH);
  }

  function startSplitResize(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    const container = event.currentTarget.parentElement;
    const containerWidth = container?.clientWidth ?? window.innerWidth;
    splitResizeSessionRef.current = {
      startX: event.clientX,
      startPercent: splitLeftPercent,
      containerWidth,
      currentPercent: splitLeftPercent
    };
    setResizingSplit(true);
  }

  function handleSplitSeparatorKey(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      applySplitPercent(splitLeftPercent - CONVERSATION_SPLIT_STEP);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      applySplitPercent(splitLeftPercent + CONVERSATION_SPLIT_STEP);
    } else if (event.key === "Home") {
      event.preventDefault();
      applySplitPercent(CONVERSATION_SPLIT_MIN_PERCENT);
    } else if (event.key === "End") {
      event.preventDefault();
      applySplitPercent(CONVERSATION_SPLIT_MAX_PERCENT);
    }
  }

  function resetSplitPercent(): void {
    applySplitPercent(CONVERSATION_SPLIT_DEFAULT_PERCENT);
  }

  function toggleSidebar(): void {
    onCloseProjectMenu();
    startSidebarMotion();
    setSidebarCollapsed(!sidebarCollapsed);
    setSidebarPreferredWidth((width) =>
      width <= SIDEBAR_MIN_WIDTH ? SIDEBAR_DEFAULT_WIDTH : width
    );
  }

  function handleSidebarSeparatorKey(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      toggleSidebar();
      return;
    }
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      if (sidebarCollapsed) {
        return;
      }
      applySidebarWidth(sidebarWidth - SIDEBAR_STEP);
      return;
    }
    if (event.key === "ArrowRight") {
      event.preventDefault();
      if (sidebarCollapsed) {
        startSidebarMotion();
        setSidebarCollapsed(false);
        setSidebarPreferredWidth(SIDEBAR_DEFAULT_WIDTH);
        return;
      }
      applySidebarWidth(sidebarWidth + SIDEBAR_STEP);
    }
  }

  return {
    compactNavigation,
    sidebarWidth,
    sidebarCollapsed,
    setSidebarCollapsed,
    resizingSidebar,
    sidebarAnimating,
    workspaceRightPanelWidth,
    clampedWorkspaceRightPanelWidth,
    resizingRightPanel,
    rightPanelOpen,
    rightPanelAnimating,
    effectiveSidebarWidth,
    workspaceRightPanelAutoGlobalized,
    workspaceRightPanelDockableWithoutSidebar,
    setRightPanelOpenWithMotion,
    animateRightPanelLayout: startRightPanelMotion,
    animateSidebarLayout: startSidebarMotion,
    startSidebarResize,
    startRightPanelResize,
    handleRightPanelSeparatorKey,
    resetWorkspaceRightPanelWidth,
    toggleSidebar,
    handleSidebarSeparatorKey,
    splitLeftPercent,
    resizingSplit,
    startSplitResize,
    handleSplitSeparatorKey,
    resetSplitPercent
  };
}
