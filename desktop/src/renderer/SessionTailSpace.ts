import { type RefObject, useCallback, useLayoutEffect, useRef, useState } from "react";
import { prefersReducedMotion } from "./motion";

// Tuning knobs for the temporary ordinary-session reading clearance.
// Leave most of the viewport below the latest user message so its dynamic
// upper-band reading anchor does not clamp to the bottom when appended.
export const SESSION_TAIL_SPACE_RATIO = 0.67;
export const SESSION_TAIL_SPACE_MIN_PX = 240;
export const SESSION_TAIL_SPACE_MAX_PX = 520;

export function sessionTailSpaceForViewport(viewportHeight: number): number {
  const proportional = Math.round(viewportHeight * SESSION_TAIL_SPACE_RATIO);
  return Math.min(SESSION_TAIL_SPACE_MAX_PX, Math.max(SESSION_TAIL_SPACE_MIN_PX, proportional));
}

/** Session-only clearance. Scroll ownership remains with ConversationScrollState. */
export function useSessionTailSpace({
  threadID, running, enabled, paneRef, viewportRef, followLayout,
}: {
  threadID?: string;
  running: boolean;
  enabled: boolean;
  paneRef: RefObject<HTMLElement | null>;
  viewportRef: RefObject<HTMLElement | null>;
  followLayout: () => void;
}) {
  const [statusNode, statusClusterRef] = useState<HTMLDivElement | null>(null);
  const space = useRef(0);
  const floor = useRef(0);
  const frame = useRef(0);
  const layoutThread = useRef(threadID);
  const follow = useRef(followLayout);
  follow.current = () => {
    // The parent restores the incoming thread after these layout effects.
    // Do not save the outgoing viewport as the incoming thread's snapshot.
    if (layoutThread.current === threadID) followLayout();
  };
  const cancel = useCallback(() => {
    window.cancelAnimationFrame(frame.current);
    frame.current = 0;
  }, []);
  const apply = useCallback((height: number) => {
    space.current = height;
    paneRef.current?.style.setProperty("--session-tail-space", `${height}px`);
  }, [paneRef]);
  const settle = useCallback((target: number, shouldFollow = true) => {
    cancel();
    const from = space.current;
    if (target === from) return;
    if (target >= from || prefersReducedMotion()) {
      apply(target);
      if (shouldFollow) follow.current();
      return;
    }
    let start: number | undefined;
    const step = (now: number) => {
      start ??= now;
      const progress = Math.min(1, (now - start) / 220);
      apply(target + (from - target) * (1 - progress) ** 3);
      if (shouldFollow) follow.current();
      frame.current = progress < 1 ? window.requestAnimationFrame(step) : 0;
    };
    frame.current = window.requestAnimationFrame(step);
  }, [apply, cancel]);

  const reserve = useCallback(() => {
    if (!enabled) return;
    cancel();
    // Reserve once for the run. Stream ticks only follow layout and must not
    // restore clearance that the user has already consumed by scrolling.
    const viewportHeight = viewportRef.current?.clientHeight ?? 0;
    apply(Math.max(floor.current, sessionTailSpaceForViewport(viewportHeight)));
  }, [apply, cancel, enabled, viewportRef]);

  const consume = useCallback((distance: number) => {
    if (distance <= 0) return;
    cancel();
    // Only remove space already scrolled out of view; never clamp the user's
    // viewport to a new bottom or accidentally re-arm following.
    const node = viewportRef.current;
    const offscreen = node ? Math.max(0, node.scrollHeight - node.clientHeight - node.scrollTop - 24) : 0;
    apply(Math.max(floor.current, space.current - Math.min(distance, offscreen)));
    // Completion must retire temporary space without re-anchoring the
    // viewport. ConversationScrollState owns the user's final reading place.
    if (!running) settle(floor.current, false);
  }, [apply, cancel, viewportRef, running, settle]);

  useLayoutEffect(() => {
    cancel();
    floor.current = 0;
    apply(0);
    paneRef.current?.style.removeProperty("--session-status-clip");
    viewportRef.current?.removeAttribute("data-session-status-clearance");
    return cancel;
  }, [threadID, enabled, apply, cancel, paneRef, viewportRef]);

  useLayoutEffect(() => {
    if (!enabled) return;
    const measure = () => {
      // Popovers are absolute descendants: reserve the capsule row, not an
      // opened TODO card. Keep a separate chrome band even while browsing:
      // clipping paint does not resize the viewport or change scroll ownership.
      const next = statusNode?.isConnected ? Math.ceil(statusNode.getBoundingClientRect().height) + 16 : 0;
      paneRef.current?.style.setProperty("--session-status-clip", next ? `calc(var(--dock-composer-height, 0px) + ${next}px)` : "0px");
      viewportRef.current?.toggleAttribute("data-session-status-clearance", next > 0);
      const previous = floor.current;
      floor.current = next;
      if (space.current < next) {
        cancel();
        apply(next);
        follow.current();
      } else if (next < previous && !running) {
        settle(next, running);
      }
    };
    measure();
    if (!statusNode) return;
    const observer = new ResizeObserver(measure);
    observer.observe(statusNode);
    return () => observer.disconnect();
  }, [threadID, enabled, statusNode, running, apply, cancel, settle, paneRef, viewportRef]);

  useLayoutEffect(() => {
    if (!enabled) return;
    if (running) {
      reserve();
      follow.current();
    } else {
      settle(floor.current, running);
    }
  }, [threadID, enabled, running, reserve, settle]);

  useLayoutEffect(() => { layoutThread.current = threadID; }, [threadID]);

  return { statusClusterRef, statusClusterNode: statusNode, reserve, consume };
}
