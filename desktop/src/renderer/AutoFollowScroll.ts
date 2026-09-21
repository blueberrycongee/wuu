import {
  type MutableRefObject,
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
} from "react";
import { isWindowResizing } from "./WindowResizeState";
import { createScrollGlide } from "./ScrollGlide";
import { prefersReducedMotion } from "./motion";
import { markScrollbarRevealSelfManaged, revealScrollbar } from "./ScrollbarReveal";

export const AUTO_FOLLOW_BOTTOM_THRESHOLD_PX = 16;
export const USER_SCROLL_AWAY_INTENT_WINDOW_MS = 300;
export const AUTO_FOLLOW_NESTED_SCROLL_ATTR = "data-wuu-nested-scroll";
export const AUTO_FOLLOW_NESTED_SCROLL_SELECTOR = `[${AUTO_FOLLOW_NESTED_SCROLL_ATTR}]`;
export const SCROLL_AWAY_KEYS = new Set(["ArrowUp", "PageUp", "Home"]);
export const SCROLL_TOWARD_LATEST_KEYS = new Set(["ArrowDown", "PageDown", "End"]);

export function maxScrollTop(node: HTMLElement): number {
  return Math.max(0, node.scrollHeight - node.clientHeight);
}

export function clampScrollTop(node: HTMLElement, top: number): number {
  return Math.max(0, Math.min(top, maxScrollTop(node)));
}

export function distanceFromBottom(node: HTMLElement): number {
  return Math.max(0, node.scrollHeight - node.scrollTop - node.clientHeight);
}

/** Submission reservation stored on the conversation pane, not chrome padding. */
export function sessionTailSpacePx(from?: HTMLElement | null): number {
  let node: HTMLElement | null | undefined = from;
  while (node) {
    const declared = node.style.getPropertyValue("--session-tail-space");
    if (declared) {
      const parsed = Number.parseFloat(declared);
      return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
    }
    node = node.parentElement;
  }
  return 0;
}

/**
 * Bottom of the latest *content*, excluding unconsumed submission tail.
 * Following `scrollHeight` would park the viewport in that empty reservation.
 */
export function latestFollowScrollTop(
  node: HTMLElement,
  tailSpace = sessionTailSpacePx(node),
): number {
  return clampScrollTop(node, maxScrollTop(node) - Math.max(0, tailSpace));
}

export function atLatestScrollView(
  node: HTMLElement,
  threshold = AUTO_FOLLOW_BOTTOM_THRESHOLD_PX,
  tailSpace = sessionTailSpacePx(node),
): boolean {
  return (
    node.scrollHeight <= node.clientHeight ||
    node.scrollTop >= latestFollowScrollTop(node, tailSpace) - threshold
  );
}

/** Distance from the latest content, excluding unconsumed submission tail. */
export function distanceFromLatestContent(
  node: HTMLElement,
  tailSpace = sessionTailSpacePx(node),
): number {
  return Math.max(0, latestFollowScrollTop(node, tailSpace) - node.scrollTop);
}

export function scrollTopForDistanceFromLatest(
  node: HTMLElement,
  distance: number,
  tailSpace = sessionTailSpacePx(node),
): number {
  return clampScrollTop(node, latestFollowScrollTop(node, tailSpace) - Math.max(0, distance));
}

/**
 * Hidden cached panes skip layout, so estimated heights can still be live on
 * the first in-flow pass. Force the last few turns to their real size before
 * restoring scroll.
 */
export function measureLatestConversationTurns(node: HTMLElement, count = 5): void {
  const turns = node.querySelectorAll<HTMLElement>(".turn");
  const start = Math.max(0, turns.length - count);
  for (let index = start; index < turns.length; index += 1) {
    void turns[index].offsetHeight;
  }
}

/**
 * Lay out the incoming conversation before scroll restore. Turns outside the
 * forced tail can still be on the 260px estimate. Record the real heights
 * while they are forced visible; the later skip reuses those sizes instead of
 * shifting the viewport.
 */
export function measureActiveConversationForRestore(node: HTMLElement): void {
  const pane = node.querySelector<HTMLElement>(
    '.cached-conversation-pane[data-active="true"]',
  );
  if (!pane) {
    measureLatestConversationTurns(node);
    return;
  }
  const turns = [...pane.querySelectorAll<HTMLElement>(".turn")];
  if (turns.length === 0) return;
  pane.classList.add("is-reveal-measure");
  const heights = turns.map((turn) => turn.offsetHeight);
  pane.classList.remove("is-reveal-measure");
  turns.forEach((turn, index) => {
    const height = heights[index];
    if (!height) return;
    const next = `auto ${height}px`;
    if (turn.style.containIntrinsicBlockSize !== next) {
      turn.style.containIntrinsicBlockSize = next;
    }
  });
}

export function eventTargetsNestedAutoFollowScroll(
  target: EventTarget | null,
  root: HTMLElement,
): boolean {
  if (!(target instanceof Element)) {
    return false;
  }
  const nested = target.closest(AUTO_FOLLOW_NESTED_SCROLL_SELECTOR);
  return Boolean(nested && nested !== root);
}

export function selectionIntersectsNode(
  selection: Selection | null,
  node: Node,
): boolean {
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
    return false;
  }
  for (let index = 0; index < selection.rangeCount; index += 1) {
    try {
      if (selection.getRangeAt(index).intersectsNode(node)) {
        return true;
      }
    } catch {
      // Streaming reconciliation can detach a range between event delivery and inspection.
    }
  }
  return false;
}

export function setAutoFollowOverflowAnchor(
  node: HTMLElement,
  autoFollow: boolean,
): void {
  node.style.overflowAnchor = autoFollow ? "none" : "auto";
}

const SUBMIT_GLIDE_ATTR = "data-submit-glide";
const SUBMIT_GLIDE_EVENT = "wuu-submit-glide";

/** True while a submitted query is gliding into its reading position. */
export function submitGlideActive(): boolean {
  return document.documentElement.hasAttribute(SUBMIT_GLIDE_ATTR);
}

/**
 * The glide writes scrollTop on every frame. Scroll-linked readers (the turn
 * rail, the jump pill, history preload) must not measure the thread on those
 * frames; they catch up from the event fired when the glide ends.
 */
export function setSubmitGlideActive(active: boolean): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  if (root.hasAttribute(SUBMIT_GLIDE_ATTR) === active) return;
  if (active) root.setAttribute(SUBMIT_GLIDE_ATTR, "");
  else root.removeAttribute(SUBMIT_GLIDE_ATTR);
  document.dispatchEvent(new CustomEvent(SUBMIT_GLIDE_EVENT, { detail: active }));
}

export function subscribeSubmitGlide(onSettle: () => void): () => void {
  const handle = (event: Event): void => {
    if (!(event instanceof CustomEvent) || event.detail === true) return;
    onSettle();
  };
  document.addEventListener(SUBMIT_GLIDE_EVENT, handle);
  return () => document.removeEventListener(SUBMIT_GLIDE_EVENT, handle);
}

export function observeAutoFollowResizeTargets(
  node: HTMLElement,
  observer: ResizeObserver,
): void {
  observer.observe(node);
  for (const child of Array.from(node.children)) {
    if (child instanceof HTMLElement) {
      observer.observe(child);
    }
  }
}

export function useAutoFollowScrollContainer({
  bottomThreshold = AUTO_FOLLOW_BOTTOM_THRESHOLD_PX,
  observeKey,
  open,
  openScrollDelayMs = 0,
}: {
  bottomThreshold?: number;
  observeKey?: string;
  open?: boolean;
  openScrollDelayMs?: number;
} = {}): {
  scrollRef: RefObject<HTMLDivElement | null>;
  autoFollowRef: MutableRefObject<boolean>;
  scrollToBottom: (options?: {
    force?: boolean;
    revealScrollbar?: boolean;
    animate?: boolean;
  }) => void;
  pauseAutoFollow: () => void;
  scheduleScrollToBottom: () => void;
  handleScrollFrame: () => void;
} {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const autoFollowRef = useRef(true);
  const selectionPausedAutoFollowRef = useRef(false);
  const pointerScrollGestureRef = useRef<
    { node: HTMLElement; scrollTop: number; scrollHeight: number; resumeScrollTop?: number } | undefined
  >(undefined);
  const lastScrollTopRef = useRef(0);
  const programmaticScrollTopRef = useRef<number | undefined>(undefined);
  const userScrollAwayIntentRef = useRef(false);
  const userScrollAwayIntentTimerRef = useRef<number | undefined>(undefined);
  const touchLastYRef = useRef<number | undefined>(undefined);
  const rafRef = useRef<number | undefined>(undefined);
  const motionFrameRef = useRef<number | undefined>(undefined);
  const cancelMotion = useCallback(() => {
    if (motionFrameRef.current !== undefined) window.cancelAnimationFrame(motionFrameRef.current);
    motionFrameRef.current = undefined;
  }, []);

  const setAutoFollow = useCallback((next: boolean): void => {
    // A later ownership change supersedes a pending click's restoration.
    if (pointerScrollGestureRef.current) pointerScrollGestureRef.current.resumeScrollTop = undefined;
    if (!next) cancelMotion();
    autoFollowRef.current = next;
    const node = scrollRef.current;
    if (node) {
      setAutoFollowOverflowAnchor(node, next);
    }
  }, [cancelMotion]);

  const pauseAutoFollow = useCallback((): void => {
    setAutoFollow(false);
  }, [setAutoFollow]);

  const refreshPointerScrollGestureLayout = useCallback((node: HTMLElement): void => {
    const gesture = pointerScrollGestureRef.current;
    if (gesture?.node !== node) {
      return;
    }
    gesture.scrollTop = clampScrollTop(node, node.scrollTop);
    gesture.scrollHeight = node.scrollHeight;
  }, []);

  const clearUserScrollAwayIntent = useCallback((): void => {
    userScrollAwayIntentRef.current = false;
    touchLastYRef.current = undefined;
    if (userScrollAwayIntentTimerRef.current !== undefined) {
      window.clearTimeout(userScrollAwayIntentTimerRef.current);
      userScrollAwayIntentTimerRef.current = undefined;
    }
  }, []);

  const markUserScrollAwayIntent = useCallback((): void => {
    userScrollAwayIntentRef.current = true;
    if (userScrollAwayIntentTimerRef.current !== undefined) {
      window.clearTimeout(userScrollAwayIntentTimerRef.current);
    }
    userScrollAwayIntentTimerRef.current = window.setTimeout(() => {
      userScrollAwayIntentRef.current = false;
      userScrollAwayIntentTimerRef.current = undefined;
    }, USER_SCROLL_AWAY_INTENT_WINDOW_MS);
  }, []);

  const scrollToBottom = useCallback(
    (options: { force?: boolean; revealScrollbar?: boolean; animate?: boolean } = {}): void => {
      const node = scrollRef.current;
      if (!node || (!options.force && !autoFollowRef.current)) {
        return;
      }
      if (options.force) {
        selectionPausedAutoFollowRef.current = false;
        setAutoFollow(true);
      }
      clearUserScrollAwayIntent();
      // Resize delivery and message reconciliation retarget the same motion;
      // only an explicit nonanimated jump may take over its scroll writes.
      if (motionFrameRef.current !== undefined) {
        if (!options.force || options.animate) return;
        cancelMotion();
      }
      const targetTop = maxScrollTop(node);
      if (options.animate && !document.hidden && !prefersReducedMotion() && Math.abs(node.scrollTop - targetTop) > 1) {
        const glide = createScrollGlide();
        glide.start(node.scrollTop);
        const step = (now: number): void => {
          motionFrameRef.current = undefined;
          if (scrollRef.current !== node || !autoFollowRef.current) return;
          // The target is re-read every frame, so content that arrives during
          // the arrival extends the same trajectory instead of restarting it.
          const { position, done } = glide.step(now, maxScrollTop(node), node.clientHeight);
          node.scrollTop = position;
          // The glide never passes its target, so the commanded offset is the
          // achieved one — no read-back to pay for on every frame.
          programmaticScrollTopRef.current = position;
          lastScrollTopRef.current = position;
          if (!done) motionFrameRef.current = window.requestAnimationFrame(step);
        };
        motionFrameRef.current = window.requestAnimationFrame(step);
        return;
      }
      const moved = node.scrollTop !== targetTop;
      if (moved) node.scrollTop = node.scrollHeight;
      programmaticScrollTopRef.current = node.scrollTop;
      lastScrollTopRef.current = node.scrollTop;
      if (moved && options.revealScrollbar) {
        revealScrollbar(node);
      }
    },
    [cancelMotion, clearUserScrollAwayIntent, setAutoFollow],
  );

  const scheduleScrollToBottom = useCallback((): void => {
    const node = scrollRef.current;
    if (!node || !autoFollowRef.current || rafRef.current !== undefined) {
      return;
    }
    rafRef.current = window.requestAnimationFrame(() => {
      rafRef.current = undefined;
      scrollToBottom();
    });
  }, [scrollToBottom]);

  const handleScrollFrame = useCallback((): void => {
    const node = scrollRef.current;
    if (!node) {
      return;
    }
    const programmaticTop = programmaticScrollTopRef.current;
    if (programmaticTop !== undefined) {
      programmaticScrollTopRef.current = undefined;
      if (Math.abs(node.scrollTop - programmaticTop) <= 1) {
        lastScrollTopRef.current = clampScrollTop(node, node.scrollTop);
        return;
      }
    }

    const pointerGesture = pointerScrollGestureRef.current;
    if (selectionPausedAutoFollowRef.current && pointerGesture?.node === node) {
      if (node.scrollHeight !== pointerGesture.scrollHeight) {
        pointerGesture.scrollTop = clampScrollTop(node, node.scrollTop);
        pointerGesture.scrollHeight = node.scrollHeight;
      } else if (node.scrollTop > pointerGesture.scrollTop) {
        selectionPausedAutoFollowRef.current = false;
      }
    }

    const scrolledUp = node.scrollTop < lastScrollTopRef.current;
    const scrolledDown = node.scrollTop > lastScrollTopRef.current;
    const userScrollAwayIntent = userScrollAwayIntentRef.current;
    lastScrollTopRef.current = clampScrollTop(node, node.scrollTop);

    // A native bottom clamp may arrive before the resize observer's follow.
    const layoutClamp = scrolledUp && !userScrollAwayIntent &&
      node.scrollTop >= maxScrollTop(node) - 1;
    if ((scrolledUp || scrolledDown) && !layoutClamp) {
      revealScrollbar(node);
    }

    if (scrolledUp && userScrollAwayIntent) {
      setAutoFollow(false);
      return;
    }
    if (
      autoFollowRef.current &&
      scrolledUp &&
      !atLatestScrollView(node, bottomThreshold)
    ) {
      // Native scrollbar drags and some platform scroll paths arrive without
      // a preceding wheel, key, or touch event. An upward move away from the
      // latest content is still enough evidence that the user took control.
      setAutoFollow(false);
      return;
    }
    if (
      atLatestScrollView(node, bottomThreshold) &&
      !selectionPausedAutoFollowRef.current &&
      // A larger viewport can clamp history to the bottom without the user
      // returning to latest. Match the main conversation's rearm policy.
      (autoFollowRef.current || scrolledDown || node.scrollHeight <= node.clientHeight)
    ) {
      setAutoFollow(true);
      return;
    }
    if (autoFollowRef.current && !userScrollAwayIntent) {
      scheduleScrollToBottom();
    }
  }, [
    bottomThreshold,
    scheduleScrollToBottom,
    setAutoFollow,
  ]);

  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (!node) {
      return undefined;
    }
    // This controller decides for itself when a reveal is warranted: it can
    // tell a layout clamp from content movement, which the global listener
    // cannot.
    markScrollbarRevealSelfManaged(node);
    setAutoFollowOverflowAnchor(node, autoFollowRef.current);
    const interruptMotion = (): void => {
      if (motionFrameRef.current !== undefined) setAutoFollow(false);
    };

    const handleScroll = (): void => {
      handleScrollFrame();
    };
    const handleWheel = (event: WheelEvent): void => {
      event.stopPropagation();
      if (event.deltaY !== 0) interruptMotion();
      if (event.deltaY < 0) {
        markUserScrollAwayIntent();
        // Disarm before the browser emits `scroll`. A queued resize or message
        // update can otherwise run in that gap and pull the viewport back down.
        setAutoFollow(false);
      } else if (event.deltaY > 0) {
        selectionPausedAutoFollowRef.current = false;
      }
    };
    const handlePointerDown = (event: PointerEvent): void => {
      event.stopPropagation();
      interruptMotion();
      if (event.target === node) {
        const resumeScrollTop = autoFollowRef.current ? clampScrollTop(node, node.scrollTop) : undefined;
        pointerScrollGestureRef.current = {
          node,
          scrollTop: clampScrollTop(node, node.scrollTop),
          scrollHeight: node.scrollHeight,
        };
        markUserScrollAwayIntent();
        // Yield before a queued follow or resize can overwrite native scrolling.
        setAutoFollow(false);
        pointerScrollGestureRef.current.resumeScrollTop = resumeScrollTop;
      }
    };
    const handlePointerEnd = (event: PointerEvent): void => {
      const gesture = pointerScrollGestureRef.current;
      pointerScrollGestureRef.current = undefined;
      // The surface also receives plain clicks. Restore only the following
      // state this press suspended, never history reading or a cancelled drag.
      if (event.type === "pointerup" && gesture?.node === node &&
        gesture.resumeScrollTop !== undefined &&
        Math.abs(clampScrollTop(node, node.scrollTop) - gesture.resumeScrollTop) <= 1) {
        setAutoFollow(true);
        scrollToBottom();
      }
    };
    const handleSelectionChange = (): void => {
      if (selectionIntersectsNode(document.getSelection(), node)) {
        selectionPausedAutoFollowRef.current = true;
        setAutoFollow(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      event.stopPropagation();
      if (SCROLL_AWAY_KEYS.has(event.key) || SCROLL_TOWARD_LATEST_KEYS.has(event.key) || event.key === " ") interruptMotion();
      if (SCROLL_AWAY_KEYS.has(event.key)) {
        markUserScrollAwayIntent();
        setAutoFollow(false);
      } else if (SCROLL_TOWARD_LATEST_KEYS.has(event.key)) {
        selectionPausedAutoFollowRef.current = false;
      }
    };
    const handleTouchStart = (event: TouchEvent): void => {
      event.stopPropagation();
      interruptMotion();
      touchLastYRef.current = event.touches[0]?.clientY;
    };
    const handleTouchMove = (event: TouchEvent): void => {
      event.stopPropagation();
      const currentY = event.touches[0]?.clientY;
      const previousY = touchLastYRef.current;
      if (
        currentY !== undefined &&
        previousY !== undefined &&
        currentY > previousY
      ) {
        markUserScrollAwayIntent();
        setAutoFollow(false);
      } else if (
        currentY !== undefined &&
        previousY !== undefined &&
        currentY < previousY
      ) {
        selectionPausedAutoFollowRef.current = false;
      }
      touchLastYRef.current = currentY;
    };
    const handleTouchEnd = (event: TouchEvent): void => {
      event.stopPropagation();
      touchLastYRef.current = undefined;
    };

    node.addEventListener("scroll", handleScroll, { passive: true });
    node.addEventListener("wheel", handleWheel, { passive: true });
    node.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("pointerup", handlePointerEnd);
    window.addEventListener("pointercancel", handlePointerEnd);
    document.addEventListener("selectionchange", handleSelectionChange);
    node.addEventListener("touchstart", handleTouchStart, { passive: true });
    node.addEventListener("touchmove", handleTouchMove, { passive: true });
    node.addEventListener("touchend", handleTouchEnd);
    node.addEventListener("touchcancel", handleTouchEnd);
    node.addEventListener("keydown", handleKeyDown);
    return () => {
      node.removeEventListener("scroll", handleScroll);
      node.removeEventListener("wheel", handleWheel);
      node.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("pointerup", handlePointerEnd);
      window.removeEventListener("pointercancel", handlePointerEnd);
      document.removeEventListener("selectionchange", handleSelectionChange);
      node.removeEventListener("touchstart", handleTouchStart);
      node.removeEventListener("touchmove", handleTouchMove);
      node.removeEventListener("touchend", handleTouchEnd);
      node.removeEventListener("touchcancel", handleTouchEnd);
      node.removeEventListener("keydown", handleKeyDown);
    };
  }, [handleScrollFrame, markUserScrollAwayIntent, observeKey, open, scrollToBottom, setAutoFollow]);

  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (!node || typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const resizeObserver = new ResizeObserver(() => {
      refreshPointerScrollGestureLayout(node);
      if (isWindowResizing()) cancelMotion();
      // Layout has already resolved. Correct before this paint rather than
      // scheduling a second frame that leaves the text trailing the viewport.
      scrollToBottom();
    });
    observeAutoFollowResizeTargets(node, resizeObserver);
    return () => {
      resizeObserver.disconnect();
    };
  }, [cancelMotion, observeKey, open, refreshPointerScrollGestureLayout, scrollToBottom]);

  useLayoutEffect(() => cancelMotion, [cancelMotion, observeKey, open]);

  useEffect(() => {
    const media = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    const settle = () => {
      if (motionFrameRef.current === undefined || (!document.hidden && !media?.matches)) return;
      cancelMotion();
      scrollToBottom();
    };
    media?.addEventListener("change", settle);
    document.addEventListener("visibilitychange", settle);
    return () => {
      media?.removeEventListener("change", settle);
      document.removeEventListener("visibilitychange", settle);
    };
  }, [cancelMotion, scrollToBottom]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    selectionPausedAutoFollowRef.current = false;
    setAutoFollow(true);
    lastScrollTopRef.current = 0;
    const timer = window.setTimeout(() => {
      scrollToBottom({ force: true, revealScrollbar: true });
    }, openScrollDelayMs);
    return () => {
      window.clearTimeout(timer);
    };
  }, [open, openScrollDelayMs, scrollToBottom, setAutoFollow]);

  useEffect(() => {
    return () => {
      if (rafRef.current !== undefined) {
        window.cancelAnimationFrame(rafRef.current);
      }
      clearUserScrollAwayIntent();
    };
  }, [clearUserScrollAwayIntent]);

  return useMemo(
    () => ({
      scrollRef,
      autoFollowRef,
      scrollToBottom,
      pauseAutoFollow,
      scheduleScrollToBottom,
      handleScrollFrame,
    }),
    [handleScrollFrame, pauseAutoFollow, scheduleScrollToBottom, scrollToBottom],
  );
}
