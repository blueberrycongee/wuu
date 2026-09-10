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
    const backdrop = shell.querySelector<HTMLElement>(".compact-session-switcher-backdrop");
    const closeButton = shell.querySelector<HTMLElement>(".compact-session-switcher-close");
    const surfaces = [sidebar, backdrop, closeButton].filter((node): node is HTMLElement => !!node);
    const wasOpen = phase === "open";
    let gesture: {
      id: number; x: number; y: number; horizontal: boolean;
      interrupted: boolean; originOpen: boolean; startPosition: number;
      width: number; openDistance: number; position: number; lastX: number; lastTime: number; velocity: number;
    } | null = null;
    let settleTimer: number | undefined;
    let settleTarget = wasOpen;
    let frame: number | undefined;
    let suppressClickUntil = 0;
    const cancelFrame = (): void => {
      if (frame !== undefined) cancelAnimationFrame(frame);
      frame = undefined;
    };
    const clearVisual = (): void => {
      cancelFrame();
      window.clearTimeout(settleTimer);
      settleTimer = undefined;
      delete shell.dataset.sidebarTouch;
      for (const surface of surfaces) {
        surface.style.removeProperty("transform");
        surface.style.removeProperty("opacity");
        surface.style.removeProperty("transition-duration");
      }
    };
    const paint = (position: number, width: number): void => {
      // Only compositor properties on the moving surfaces change per frame.
      // Inherited variables on the shell invalidate the entire conversation.
      sidebar.style.transform = `translate3d(${position - width}px, 0, 0)`;
      if (closeButton) closeButton.style.transform = sidebar.style.transform;
      if (backdrop) backdrop.style.opacity = String(position / width);
    };
    const settle = (toOpen: boolean, velocity = 0): void => {
      if (!gesture || (!gesture.horizontal && !gesture.interrupted)) { gesture = null; return; }
      const { width, position } = gesture;
      cancelFrame();
      paint(position, width);
      gesture = null;
      suppressClickUntil = performance.now() + 500;
      // Commit the dragged position before enabling the release transition.
      sidebar.getBoundingClientRect();
      const remaining = Math.abs((toOpen ? width : 0) - position);
      const duration = matchMedia("(prefers-reduced-motion: reduce)").matches || remaining < 1
        ? 0
        : Math.round(Math.max(80, Math.min(240, remaining / Math.max(0.8, Math.abs(velocity)))));
      shell.dataset.sidebarTouch = "settling";
      for (const surface of surfaces) surface.style.transitionDuration = `${duration}ms`;
      paint(toOpen ? width : 0, width);
      settleTarget = toOpen;
      settleTimer = window.setTimeout(() => {
        settleTimer = undefined;
        if (toOpen !== wasOpen) {
          // Leave the override in place until React commits the new phase.
          if (toOpen) open(); else close();
        } else clearVisual();
      }, duration);
    };
    const cancel = (): void => settle(gesture?.originOpen ?? wasOpen);
    const reset = (): void => { gesture = null; clearVisual(); };
    const start = (event: TouchEvent): void => {
      if (gesture) cancel();
      if (phase !== "closed" && phase !== "open") return;
      suppressClickUntil = 0;
      if (event.defaultPrevented || event.touches.length !== 1 || !isTouchWebShell()) return;
      const touch = event.touches[0];
      const interrupted = settleTimer !== undefined;
      const originOpen = interrupted ? settleTarget : wasOpen;
      const region = interrupted
        ? ".sidebar, .compact-session-switcher-backdrop, .scroll-region, .conversation-split-body, .side-thread-panel__body"
        : wasOpen
        ? ".sidebar, .compact-session-switcher-backdrop"
        : ".scroll-region, .conversation-split-body, .side-thread-panel__body";
      if (!(event.target instanceof Element) ||
        !event.target.closest(region) || ownsGesture(event.target, shell,
          !!event.target.closest(".sidebar, .compact-session-switcher-backdrop"))) return;
      const rect = sidebar.getBoundingClientRect();
      const width = rect.width;
      if (!width) return;
      const position = interrupted
        ? Math.max(0, Math.min(width, rect.right - shell.getBoundingClientRect().left))
        : wasOpen ? width : 0;
      if (interrupted) {
        window.clearTimeout(settleTimer);
        settleTimer = undefined;
        shell.dataset.sidebarTouch = "dragging";
        for (const surface of surfaces) surface.style.removeProperty("transition-duration");
        paint(position, width);
      }
      gesture = {
        id: touch.identifier, x: touch.clientX, y: touch.clientY, horizontal: false,
        interrupted, originOpen, startPosition: position,
        // A right-hand thumb has little travel left near the screen edge.
        // Keep opening reachable there without treating tiny movements as swipes.
        openDistance: Math.max(32, Math.min(64, (window.innerWidth - touch.clientX) / 2)),
        width, position, lastX: touch.clientX, lastTime: event.timeStamp, velocity: 0,
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
        const forward = gesture.interrupted ? Math.abs(dx) : wasOpen ? -dx : dx;
        if (forward <= 0 || dy > forward * 1.5) { cancel(); return; }
        // Thumb arcs can start slightly more vertical than horizontal. Give
        // that ambiguous start a short observation window before native
        // scrolling takes ownership. Clearly vertical motion stays native.
        if (forward < dy) {
          if (Math.max(forward, dy) >= 20) cancel();
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
      gesture.position = Math.max(0, Math.min(gesture.width, gesture.startPosition + dx));
      if (frame === undefined) frame = requestAnimationFrame(() => {
        frame = undefined;
        if (gesture) paint(gesture.position, gesture.width);
      });
    };
    const end = (event: TouchEvent): void => {
      if (!gesture?.horizontal) { cancel(); return; }
      if (event.defaultPrevented || event.touches.length) { cancel(); return; }
      const touch = Array.from(event.changedTouches).find((item) => item.identifier === gesture!.id);
      if (!touch) { cancel(); return; }
      if (event.cancelable) event.preventDefault();
      const velocity = event.timeStamp - gesture.lastTime <= 100 ? gesture.velocity : 0;
      const distance = Math.abs(touch.clientX - gesture.x);
      const threshold = gesture.interrupted || wasOpen ? gesture.width / 2 : gesture.openDistance;
      settle(Math.abs(velocity) >= 0.5 && distance >= 32 ? velocity > 0 : gesture.position >= threshold, velocity);
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
