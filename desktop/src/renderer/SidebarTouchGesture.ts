import { useEffect, type RefObject } from "react";
import { isTouchWebShell } from "./ComposerFocus";
import type { SidebarDrawerPhase } from "./SidebarDrawerState";

// Leave controls and horizontal scrollers (code, editors, terminal) in charge
// of their own gestures inside the message flow.
function ownsGesture(target: Element, shell: HTMLElement, closing: boolean): boolean {
  if (target.closest('input, textarea, select, [contenteditable], [role="dialog"], [role="slider"], .monaco-editor, .xterm')) {
    return true;
  }
  // Session rows allow closing drags; message actions keep their own gestures.
  if (!closing && target.closest("button, a")) return true;
  for (let node: Element | null = target; node && node !== shell; node = node.parentElement) {
    if (node.scrollWidth > node.clientWidth && /auto|scroll/.test(getComputedStyle(node).overflowX)) {
      return true;
    }
  }
  return false;
}

export function useSidebarTouchGesture(
  shellRef: RefObject<HTMLDivElement | null>,
  enabled: boolean,
  phase: SidebarDrawerPhase,
  open: () => void,
  close: () => void,
): void {
  useEffect(() => {
    const shell = shellRef.current;
    if (!enabled || !shell || !isTouchWebShell()) return;

    const sidebar = shell.querySelector<HTMLElement>(".sidebar");
    if (!sidebar) return;
    const wasOpen = phase === "open";
    let gesture: {
      id: number; x: number; y: number; horizontal: boolean;
      width: number; openDistance: number; position: number; lastX: number; lastTime: number; velocity: number;
    } | null = null;
    let settleTimer: number | undefined;
    let suppressClickUntil = 0;
    const clearVisual = (): void => {
      window.clearTimeout(settleTimer);
      settleTimer = undefined;
      delete shell.dataset.sidebarTouch;
      shell.style.removeProperty("--sidebar-touch-offset");
      shell.style.removeProperty("--sidebar-touch-progress");
    };
    const paint = (position: number, width: number): void => {
      shell.style.setProperty("--sidebar-touch-offset", `${position - width}px`);
      shell.style.setProperty("--sidebar-touch-progress", String(position / width));
    };
    const settle = (toOpen: boolean): void => {
      if (!gesture?.horizontal) { gesture = null; return; }
      const width = gesture.width;
      gesture = null;
      suppressClickUntil = performance.now() + 500;
      // Commit the dragged position before enabling the release transition.
      sidebar.getBoundingClientRect();
      shell.dataset.sidebarTouch = "settling";
      paint(toOpen ? width : 0, width);
      const duration = matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 180;
      settleTimer = window.setTimeout(() => {
        settleTimer = undefined;
        if (toOpen !== wasOpen) {
          // Leave the override in place until React commits the new phase.
          if (toOpen) open(); else close();
        } else clearVisual();
      }, duration);
    };
    const cancel = (): void => settle(wasOpen);
    const reset = (): void => { gesture = null; clearVisual(); };
    const start = (event: TouchEvent): void => {
      if (gesture) cancel();
      if (settleTimer !== undefined || (phase !== "closed" && phase !== "open")) return;
      suppressClickUntil = 0;
      if (event.defaultPrevented || event.touches.length !== 1 || !isTouchWebShell()) return;
      const touch = event.touches[0];
      const region = wasOpen
        ? ".sidebar, .compact-session-switcher-backdrop"
        : ".scroll-region, .conversation-split-body, .side-thread-panel__body";
      if (!(event.target instanceof Element) ||
        !event.target.closest(region) || ownsGesture(event.target, shell, wasOpen)) return;
      const width = sidebar.getBoundingClientRect().width;
      if (!width) return;
      gesture = {
        id: touch.identifier, x: touch.clientX, y: touch.clientY, horizontal: false,
        // A right-hand thumb has little travel left near the screen edge.
        // Keep opening reachable there without treating tiny movements as swipes.
        openDistance: Math.max(32, Math.min(64, (window.innerWidth - touch.clientX) / 2)),
        width, position: wasOpen ? width : 0, lastX: touch.clientX, lastTime: event.timeStamp, velocity: 0,
      };
    };
    const move = (event: TouchEvent): void => {
      if (!gesture) return;
      const touch = event.touches[0];
      if (event.defaultPrevented || event.touches.length !== 1 || touch.identifier !== gesture.id || !event.cancelable) {
        cancel();
        return;
      }
      const dx = touch.clientX - gesture.x;
      const dy = Math.abs(touch.clientY - gesture.y);
      if (!gesture.horizontal) {
        if (Math.max(Math.abs(dx), dy) < 10) return;
        const forward = wasOpen ? -dx : dx;
        if (forward <= 0 || dy > forward * 1.5) { gesture = null; return; }
        // Thumb arcs can start slightly more vertical than horizontal. Give
        // that ambiguous start a short observation window before native
        // scrolling takes ownership. Clearly vertical motion stays native.
        if (forward < dy) {
          if (Math.max(forward, dy) >= 20) gesture = null;
          else event.preventDefault();
          return;
        }
        // Keep this direction until release, like a drawer drag: a short drag
        // can be abandoned without turning its tail into a page scroll.
        gesture.horizontal = true;
        shell.dataset.sidebarTouch = "dragging";
      }
      event.preventDefault();
      const elapsed = event.timeStamp - gesture.lastTime;
      if (touch.clientX !== gesture.lastX) {
        gesture.velocity = elapsed > 0 ? (touch.clientX - gesture.lastX) / elapsed : 0;
        gesture.lastX = touch.clientX;
        gesture.lastTime = event.timeStamp;
      }
      gesture.position = Math.max(0, Math.min(gesture.width, (wasOpen ? gesture.width : 0) + dx));
      paint(gesture.position, gesture.width);
    };
    const end = (event: TouchEvent): void => {
      if (!gesture?.horizontal) { gesture = null; return; }
      if (event.defaultPrevented || event.touches.length) { cancel(); return; }
      const touch = Array.from(event.changedTouches).find((item) => item.identifier === gesture!.id);
      if (!touch) { cancel(); return; }
      if (event.cancelable) event.preventDefault();
      const velocity = event.timeStamp - gesture.lastTime <= 100 ? gesture.velocity : 0;
      const distance = Math.abs(touch.clientX - gesture.x);
      const threshold = wasOpen ? gesture.width / 2 : gesture.openDistance;
      settle(Math.abs(velocity) >= 0.5 && distance >= 32 ? velocity > 0 : gesture.position >= threshold);
    };
    const click = (event: MouseEvent): void => {
      if (performance.now() < suppressClickUntil) {
        event.preventDefault();
        event.stopPropagation();
      }
    };

    shell.addEventListener("touchstart", start, { passive: true });
    // Cancel scrolling only after a horizontal drawer gesture is established.
    shell.addEventListener("touchmove", move, { passive: false });
    shell.addEventListener("touchend", end, { passive: false });
    shell.addEventListener("touchcancel", cancel);
    shell.addEventListener("click", click, true);
    window.addEventListener("resize", reset);
    return () => {
      reset();
      shell.removeEventListener("touchstart", start);
      shell.removeEventListener("touchmove", move);
      shell.removeEventListener("touchend", end);
      shell.removeEventListener("touchcancel", cancel);
      shell.removeEventListener("click", click, true);
      window.removeEventListener("resize", reset);
    };
  }, [shellRef, enabled, phase, open, close]);
}
