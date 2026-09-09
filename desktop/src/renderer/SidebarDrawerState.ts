import { isTouchWebShell } from "./ComposerFocus";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type MutableRefObject,
  type RefObject,
} from "react";

export const SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS = 240;

export type SidebarDrawerPhase = "closed" | "open" | "closing" | "docking";

export type SidebarDrawerStateController = {
  sidebarDrawerPhase: SidebarDrawerPhase;
  sidebarHoverZoneRef: MutableRefObject<HTMLDivElement | null>;
  cancelSidebarDrawerOpen: () => void;
  openSidebarDrawer: () => void;
  openSidebarDrawerNow: () => void;
  scheduleSidebarDrawerOpen: () => void;
  closeSidebarDrawer: () => void;
  scheduleSidebarDrawerCloseFromPointerLeave: (event?: Event) => void;
  syncSidebarDrawerHover: () => void;
};

// The drawer controller serves both shells: the main app (`aside.sidebar`)
// and the settings page (`aside.settings-sidebar`). Pointer-hover checks must
// match either rail, otherwise moving the pointer from the hover zone into
// the revealed drawer counts as "left the drawer" and closes it.
const SIDEBAR_RAIL_SELECTOR = ".sidebar, .settings-sidebar";
const SIDEBAR_DRAWER_HOVER_TRIGGER_SELECTOR =
  ".sidebar-hover-zone, .sidebar-toggle-button";

export function useSidebarDrawerState({
  appShellRef,
  sidebarCollapsed,
  resizingSidebar,
  motionMs,
  dockingMotionMs = motionMs,
  hoverOpenDelayMs = SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS,
  closeOnWindowResize = false,
}: {
  appShellRef: RefObject<HTMLDivElement | null>;
  sidebarCollapsed: boolean;
  resizingSidebar: boolean;
  motionMs: number;
  dockingMotionMs?: number;
  hoverOpenDelayMs?: number;
  closeOnWindowResize?: boolean;
}): SidebarDrawerStateController {
  const [sidebarDrawerPhase, setSidebarDrawerPhase] =
    useState<SidebarDrawerPhase>("closed");
  const sidebarDrawerOpenTimerRef = useRef<number | undefined>(undefined);
  const sidebarDrawerCloseTimerRef = useRef<number | undefined>(undefined);
  const sidebarHoverZoneRef = useRef<HTMLDivElement | null>(null);
  const sidebarHoverZoneActiveRef = useRef(false);
  const sidebarPointerPositionRef = useRef<{ x: number; y: number } | null>(
    null,
  );
  const sidebarDrawerSuppressedRef = useRef(false);
  const sidebarDrawerPointerLeaveTimerRef = useRef<number | undefined>(undefined);
  const sidebarWasCollapsedRef = useRef(sidebarCollapsed);

  const clearSidebarDrawerCloseTimer = useCallback((): void => {
    if (sidebarDrawerCloseTimerRef.current !== undefined) {
      window.clearTimeout(sidebarDrawerCloseTimerRef.current);
      sidebarDrawerCloseTimerRef.current = undefined;
    }
  }, []);

  const clearSidebarDrawerOpenTimer = useCallback((): void => {
    if (sidebarDrawerOpenTimerRef.current !== undefined) {
      window.clearTimeout(sidebarDrawerOpenTimerRef.current);
      sidebarDrawerOpenTimerRef.current = undefined;
    }
  }, []);

  const clearSidebarDrawerPointerLeaveTimer = useCallback((): void => {
    if (sidebarDrawerPointerLeaveTimerRef.current !== undefined) {
      window.clearTimeout(sidebarDrawerPointerLeaveTimerRef.current);
      sidebarDrawerPointerLeaveTimerRef.current = undefined;
    }
  }, []);

  const cancelSidebarDrawerOpen = useCallback((): void => {
    sidebarHoverZoneActiveRef.current = false;
    clearSidebarDrawerOpenTimer();
  }, [clearSidebarDrawerOpenTimer]);

  const rememberSidebarPointerPosition = useCallback((event: Event): void => {
    if (!(event instanceof MouseEvent)) {
      return;
    }
    sidebarPointerPositionRef.current = {
      x: event.clientX,
      y: event.clientY,
    };
  }, []);

  const sidebarDrawerPointerHovered = useCallback((): boolean | undefined => {
    const point = sidebarPointerPositionRef.current;
    if (!point) {
      return undefined;
    }
    const sidebar = appShellRef.current?.querySelector(SIDEBAR_RAIL_SELECTOR);
    const hoverTriggers = appShellRef.current?.querySelectorAll(
      SIDEBAR_DRAWER_HOVER_TRIGGER_SELECTOR,
    );
    for (const element of [sidebar, ...(hoverTriggers ?? [])]) {
      if (!element) continue;
      const rect = element.getBoundingClientRect();
      if (
        rect.width > 0 &&
        rect.height > 0 &&
        point.x >= rect.left &&
        point.x <= rect.right &&
        point.y >= rect.top &&
        point.y <= rect.bottom
      ) {
        return true;
      }
    }
    if (typeof document.elementFromPoint !== "function") {
      return undefined;
    }
    const target = document.elementFromPoint(point.x, point.y);
    if (!target) {
      return undefined;
    }
    return Boolean(
      (sidebar && sidebar.contains(target)) ||
        [...(hoverTriggers ?? [])].some((trigger) => trigger.contains(target)),
    );
  }, [appShellRef]);

  const blurSidebarFocus = useCallback((): void => {
    const sidebar = appShellRef.current?.querySelector(SIDEBAR_RAIL_SELECTOR);
    const active = document.activeElement;
    if (sidebar && active instanceof HTMLElement && sidebar.contains(active)) {
      active.blur();
    }
  }, [appShellRef]);

  const openSidebarDrawer = useCallback((): void => {
    if (resizingSidebar || sidebarDrawerSuppressedRef.current) {
      return;
    }
    if (sidebarCollapsed && sidebarDrawerPointerHovered() === false) {
      return;
    }
    clearSidebarDrawerOpenTimer();
    clearSidebarDrawerCloseTimer();
    if (sidebarCollapsed) {
      setSidebarDrawerPhase("open");
    }
  }, [
    clearSidebarDrawerCloseTimer,
    clearSidebarDrawerOpenTimer,
    resizingSidebar,
    sidebarCollapsed,
    sidebarDrawerPointerHovered,
  ]);

  const openSidebarDrawerNow = useCallback((): void => {
    if (resizingSidebar || !sidebarCollapsed) {
      return;
    }
    clearSidebarDrawerOpenTimer();
    clearSidebarDrawerCloseTimer();
    setSidebarDrawerPhase("open");
  }, [
    clearSidebarDrawerCloseTimer,
    clearSidebarDrawerOpenTimer,
    resizingSidebar,
    sidebarCollapsed,
  ]);

  const scheduleSidebarDrawerOpen = useCallback((): void => {
    sidebarHoverZoneActiveRef.current = true;
    if (
      resizingSidebar ||
      sidebarDrawerSuppressedRef.current ||
      !sidebarCollapsed
    ) {
      return;
    }
    clearSidebarDrawerOpenTimer();
    sidebarDrawerOpenTimerRef.current = window.setTimeout(() => {
      sidebarDrawerOpenTimerRef.current = undefined;
      if (
        !sidebarHoverZoneActiveRef.current ||
        resizingSidebar ||
        sidebarDrawerSuppressedRef.current ||
        sidebarDrawerPointerHovered() === false
      ) {
        return;
      }
      clearSidebarDrawerCloseTimer();
      setSidebarDrawerPhase("open");
    }, hoverOpenDelayMs);
  }, [
    clearSidebarDrawerCloseTimer,
    clearSidebarDrawerOpenTimer,
    hoverOpenDelayMs,
    resizingSidebar,
    sidebarCollapsed,
    sidebarDrawerPointerHovered,
  ]);

  const closeSidebarDrawer = useCallback((): void => {
    cancelSidebarDrawerOpen();
    clearSidebarDrawerCloseTimer();
    clearSidebarDrawerPointerLeaveTimer();
    blurSidebarFocus();
    if (!sidebarCollapsed || resizingSidebar) {
      setSidebarDrawerPhase("closed");
      return;
    }
    setSidebarDrawerPhase("closing");
    sidebarDrawerCloseTimerRef.current = window.setTimeout(() => {
      sidebarDrawerCloseTimerRef.current = undefined;
      setSidebarDrawerPhase("closed");
    }, motionMs);
  }, [
    blurSidebarFocus,
    cancelSidebarDrawerOpen,
    clearSidebarDrawerCloseTimer,
    clearSidebarDrawerPointerLeaveTimer,
    motionMs,
    resizingSidebar,
    sidebarCollapsed,
  ]);

  const scheduleSidebarDrawerCloseFromPointerLeave = useCallback((event?: Event): void => {
    // Touch browsers also synthesize mouseout after taps and layout changes.
    // Mobile drawers close through navigation, gestures or their backdrop.
    if (isTouchWebShell()) return;
    // Touch release/cancel produces pointerleave even when a drawer drag is
    // returning to its open position. It is not a mouse leaving the rail.
    if (event && "pointerType" in event && event.pointerType === "touch") return;
    if (event) {
      rememberSidebarPointerPosition(event);
    }
    clearSidebarDrawerPointerLeaveTimer();
    // Sidebar geometry settles after the globalized panel and drawer swap
    // stacking contexts. Check the real pointer location on the next task,
    // so a synthetic leave caused by that swap does not close the drawer.
    // Prefer the leave event's relatedTarget when it is available: it tells
    // us where the pointer is actually going, which avoids the boundary
    // rounding problem where the pointer's reported position is still inside
    // the sidebar rect even though the user has moved outside the floating
    // layer.
    sidebarDrawerPointerLeaveTimerRef.current = window.setTimeout(() => {
      sidebarDrawerPointerLeaveTimerRef.current = undefined;

      const relatedTarget = event instanceof MouseEvent ? event.relatedTarget : null;
      if (relatedTarget && relatedTarget instanceof Element) {
        const sidebar = appShellRef.current?.querySelector(SIDEBAR_RAIL_SELECTOR);
        const hoverTriggers = appShellRef.current?.querySelectorAll(
          SIDEBAR_DRAWER_HOVER_TRIGGER_SELECTOR,
        );
        const isMovingToHoverTarget = Boolean(
          (sidebar &&
            (relatedTarget === sidebar || sidebar.contains(relatedTarget))) ||
            [...(hoverTriggers ?? [])].some(
              (trigger) =>
                relatedTarget === trigger || trigger.contains(relatedTarget),
            ),
        );
        if (!isMovingToHoverTarget) {
          closeSidebarDrawer();
        }
        return;
      }

      if (sidebarDrawerPointerHovered() === false) {
        closeSidebarDrawer();
      }
    }, 0);
  }, [
    appShellRef,
    clearSidebarDrawerPointerLeaveTimer,
    closeSidebarDrawer,
    rememberSidebarPointerPosition,
    sidebarDrawerPointerHovered,
  ]);

  const syncSidebarDrawerHover = useCallback((): void => {
    if (isTouchWebShell()) return;
    if (!sidebarCollapsed || sidebarDrawerPhase !== "open") {
      return;
    }
    if (sidebarDrawerPointerHovered() === false) {
      closeSidebarDrawer();
    }
  }, [
    closeSidebarDrawer,
    sidebarCollapsed,
    sidebarDrawerPhase,
    sidebarDrawerPointerHovered,
  ]);

  useEffect(() => {
    function handlePointerEvent(event: Event): void {
      if ("pointerType" in event && event.pointerType === "touch") return;
      rememberSidebarPointerPosition(event);
      syncSidebarDrawerHover();
    }
    window.addEventListener("pointermove", handlePointerEvent, true);
    window.addEventListener("pointerover", handlePointerEvent, true);
    window.addEventListener("pointerdown", handlePointerEvent, true);
    window.addEventListener("mousemove", handlePointerEvent, true);
    window.addEventListener("mouseover", handlePointerEvent, true);
    window.addEventListener("mousedown", handlePointerEvent, true);
    return () => {
      window.removeEventListener("pointermove", handlePointerEvent, true);
      window.removeEventListener("pointerover", handlePointerEvent, true);
      window.removeEventListener("pointerdown", handlePointerEvent, true);
      window.removeEventListener("mousemove", handlePointerEvent, true);
      window.removeEventListener("mouseover", handlePointerEvent, true);
      window.removeEventListener("mousedown", handlePointerEvent, true);
    };
  }, [rememberSidebarPointerPosition, syncSidebarDrawerHover]);

  useLayoutEffect(() => {
    const sidebarWasCollapsed = sidebarWasCollapsedRef.current;
    sidebarWasCollapsedRef.current = sidebarCollapsed;

    if (sidebarCollapsed) {
      if (!sidebarWasCollapsed) {
        cancelSidebarDrawerOpen();
        clearSidebarDrawerCloseTimer();
        setSidebarDrawerPhase("closed");
        return;
      }
      if (sidebarDrawerPhase === "docking") {
        clearSidebarDrawerCloseTimer();
        setSidebarDrawerPhase("closed");
      }
      return;
    }
    if (sidebarDrawerPhase === "closed" || sidebarDrawerPhase === "docking") {
      return;
    }

    cancelSidebarDrawerOpen();
    clearSidebarDrawerCloseTimer();
    if (!sidebarWasCollapsed) {
      setSidebarDrawerPhase("closed");
      return;
    }

    // Keep a pinned hover drawer on its current visual layer while the grid
    // opens underneath it. Once both occupy the same rectangle, the overlay
    // positioning can disappear without a second visible motion.
    setSidebarDrawerPhase("docking");
    sidebarDrawerCloseTimerRef.current = window.setTimeout(() => {
      sidebarDrawerCloseTimerRef.current = undefined;
      setSidebarDrawerPhase("closed");
    }, dockingMotionMs);
  }, [
    cancelSidebarDrawerOpen,
    clearSidebarDrawerCloseTimer,
    dockingMotionMs,
    sidebarCollapsed,
    sidebarDrawerPhase,
  ]);

  useEffect(() => {
    if (!sidebarCollapsed || resizingSidebar) {
      cancelSidebarDrawerOpen();
    }
  }, [cancelSidebarDrawerOpen, resizingSidebar, sidebarCollapsed]);

  // Shells that cannot preserve their drawer geometry across a native resize
  // can opt into resetting it. The main conversation shell keeps its drawer
  // stable during resize, while settings recomputes against the new bounds.
  useEffect(() => {
    if (!closeOnWindowResize) {
      return undefined;
    }
    function handleWindowResize(): void {
      cancelSidebarDrawerOpen();
      clearSidebarDrawerCloseTimer();
      clearSidebarDrawerPointerLeaveTimer();
      setSidebarDrawerPhase("closed");
    }

    window.addEventListener("resize", handleWindowResize);
    return () => window.removeEventListener("resize", handleWindowResize);
  }, [
    cancelSidebarDrawerOpen,
    closeOnWindowResize,
    clearSidebarDrawerCloseTimer,
    clearSidebarDrawerPointerLeaveTimer,
  ]);

  useEffect(() => {
    if (!sidebarCollapsed || sidebarDrawerPhase !== "open") {
      return undefined;
    }
    function handleWindowMouseOut(event: MouseEvent): void {
      if (event.relatedTarget === null) {
        scheduleSidebarDrawerCloseFromPointerLeave(event);
      }
    }
    window.addEventListener("mouseout", handleWindowMouseOut);
    window.addEventListener("blur", closeSidebarDrawer);
    return () => {
      window.removeEventListener("mouseout", handleWindowMouseOut);
      window.removeEventListener("blur", closeSidebarDrawer);
    };
  }, [
    closeSidebarDrawer,
    scheduleSidebarDrawerCloseFromPointerLeave,
    sidebarCollapsed,
    sidebarDrawerPhase,
  ]);

  useEffect(() => {
    if (!sidebarCollapsed) {
      return undefined;
    }
    function handleWindowMouseOut(event: MouseEvent): void {
      if (event.relatedTarget === null) {
        cancelSidebarDrawerOpen();
      }
    }
    window.addEventListener("mouseout", handleWindowMouseOut);
    window.addEventListener("blur", cancelSidebarDrawerOpen);
    return () => {
      window.removeEventListener("mouseout", handleWindowMouseOut);
      window.removeEventListener("blur", cancelSidebarDrawerOpen);
    };
  }, [cancelSidebarDrawerOpen, sidebarCollapsed]);

  useEffect(() => {
    if (resizingSidebar) {
      sidebarDrawerSuppressedRef.current = true;
      return undefined;
    }
    if (!sidebarDrawerSuppressedRef.current) {
      return undefined;
    }
    if (!sidebarCollapsed) {
      sidebarDrawerSuppressedRef.current = false;
      return undefined;
    }
    function handlePointerMove(event: PointerEvent): void {
      const zone = sidebarHoverZoneRef.current?.getBoundingClientRect();
      if (!zone || event.clientX > zone.right || event.clientX < zone.left) {
        sidebarDrawerSuppressedRef.current = false;
        window.removeEventListener("pointermove", handlePointerMove);
      }
    }
    window.addEventListener("pointermove", handlePointerMove);
    return () => window.removeEventListener("pointermove", handlePointerMove);
  }, [resizingSidebar, sidebarCollapsed]);

  useEffect(() => {
    if (!resizingSidebar || !sidebarCollapsed) {
      return;
    }
    const sidebar = appShellRef.current?.querySelector(SIDEBAR_RAIL_SELECTOR);
    const active = document.activeElement;
    if (sidebar && active instanceof HTMLElement && sidebar.contains(active)) {
      active.blur();
    }
  }, [appShellRef, resizingSidebar, sidebarCollapsed]);

  useEffect(
    () => () => {
      clearSidebarDrawerOpenTimer();
      clearSidebarDrawerCloseTimer();
      clearSidebarDrawerPointerLeaveTimer();
    },
    [
      clearSidebarDrawerCloseTimer,
      clearSidebarDrawerOpenTimer,
      clearSidebarDrawerPointerLeaveTimer,
    ],
  );

  return {
    sidebarDrawerPhase,
    sidebarHoverZoneRef,
    cancelSidebarDrawerOpen,
    openSidebarDrawer,
    openSidebarDrawerNow,
    scheduleSidebarDrawerOpen,
    closeSidebarDrawer,
    scheduleSidebarDrawerCloseFromPointerLeave,
    syncSidebarDrawerHover,
  };
}
