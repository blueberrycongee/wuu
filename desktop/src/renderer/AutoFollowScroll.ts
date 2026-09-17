import {
  type MutableRefObject,
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
} from "react";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";

export const AUTO_FOLLOW_BOTTOM_THRESHOLD_PX = 16;
export const AUTO_FOLLOW_SCROLLBAR_HIDE_DELAY_MS = 700;
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

export function atLatestScrollView(
  node: HTMLElement,
  threshold = AUTO_FOLLOW_BOTTOM_THRESHOLD_PX,
): boolean {
  return (
    node.scrollHeight <= node.clientHeight ||
    distanceFromBottom(node) <= threshold
  );
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
  }) => void;
  pauseAutoFollow: () => void;
  scheduleScrollToBottom: () => void;
  handleScrollFrame: () => void;
} {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const autoFollowRef = useRef(true);
  const selectionPausedAutoFollowRef = useRef(false);
  const pointerScrollGestureRef = useRef<
    { node: HTMLElement; scrollTop: number; scrollHeight: number } | undefined
  >(undefined);
  const lastScrollTopRef = useRef(0);
  const programmaticScrollTopRef = useRef<number | undefined>(undefined);
  const userScrollAwayIntentRef = useRef(false);
  const userScrollAwayIntentTimerRef = useRef<number | undefined>(undefined);
  const touchLastYRef = useRef<number | undefined>(undefined);
  const rafRef = useRef<number | undefined>(undefined);
  const scrollbarHideTimerRef = useRef<number | undefined>(undefined);

  const setAutoFollow = useCallback((next: boolean): void => {
    autoFollowRef.current = next;
    const node = scrollRef.current;
    if (node) {
      setAutoFollowOverflowAnchor(node, next);
    }
  }, []);

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

  const showScrollbar = useCallback((node: HTMLElement): void => {
    if (node.scrollHeight <= node.clientHeight) {
      return;
    }
    node.classList.add("scrollbar-visible");
    if (scrollbarHideTimerRef.current !== undefined) {
      window.clearTimeout(scrollbarHideTimerRef.current);
    }
    scrollbarHideTimerRef.current = window.setTimeout(() => {
      scrollbarHideTimerRef.current = undefined;
      node.classList.remove("scrollbar-visible");
    }, AUTO_FOLLOW_SCROLLBAR_HIDE_DELAY_MS);
  }, []);

  const scrollToBottom = useCallback(
    (options: { force?: boolean; revealScrollbar?: boolean } = {}): void => {
      const node = scrollRef.current;
      if (!node || (!options.force && !autoFollowRef.current)) {
        return;
      }
      if (options.force) {
        selectionPausedAutoFollowRef.current = false;
        setAutoFollow(true);
      }
      clearUserScrollAwayIntent();
      const targetTop = maxScrollTop(node);
      const moved = node.scrollTop !== targetTop;
      if (moved) node.scrollTop = node.scrollHeight;
      programmaticScrollTopRef.current = node.scrollTop;
      lastScrollTopRef.current = node.scrollTop;
      if (moved && options.revealScrollbar) {
        showScrollbar(node);
      }
    },
    [clearUserScrollAwayIntent, setAutoFollow, showScrollbar],
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
      showScrollbar(node);
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
    showScrollbar,
  ]);

  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (!node) {
      return undefined;
    }
    setAutoFollowOverflowAnchor(node, autoFollowRef.current);

    const handleScroll = (): void => {
      handleScrollFrame();
    };
    const handleWheel = (event: WheelEvent): void => {
      event.stopPropagation();
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
      if (event.target === node) {
        pointerScrollGestureRef.current = {
          node,
          scrollTop: clampScrollTop(node, node.scrollTop),
          scrollHeight: node.scrollHeight,
        };
        markUserScrollAwayIntent();
      }
    };
    const handlePointerEnd = (): void => {
      pointerScrollGestureRef.current = undefined;
    };
    const handleSelectionChange = (): void => {
      if (selectionIntersectsNode(document.getSelection(), node)) {
        selectionPausedAutoFollowRef.current = true;
        setAutoFollow(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      event.stopPropagation();
      if (SCROLL_AWAY_KEYS.has(event.key)) {
        markUserScrollAwayIntent();
      } else if (SCROLL_TOWARD_LATEST_KEYS.has(event.key)) {
        selectionPausedAutoFollowRef.current = false;
      }
    };
    const handleTouchStart = (event: TouchEvent): void => {
      event.stopPropagation();
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
  }, [handleScrollFrame, markUserScrollAwayIntent, observeKey, open, setAutoFollow]);

  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (!node || typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const windowResizeScroll = createWindowResizeSettleScheduler(scrollToBottom);
    let liveResizeFrame: number | undefined;
    const scheduleLiveResizeScroll = (): void => {
      if (!autoFollowRef.current || liveResizeFrame !== undefined) return;
      liveResizeFrame = window.requestAnimationFrame(() => {
        liveResizeFrame = undefined;
        if (!isWindowResizing() || !autoFollowRef.current) return;
        // Match the main conversation: keep the bottom anchored during the
        // drag, not only after it settles. Chromium clamps this target without
        // needing scrollHeight/clientHeight reads on every resize frame.
        node.scrollTop = Number.MAX_SAFE_INTEGER;
        programmaticScrollTopRef.current = node.scrollTop;
        lastScrollTopRef.current = node.scrollTop;
      });
    };
    const resizeObserver = new ResizeObserver(() => {
      refreshPointerScrollGestureLayout(node);
      if (isWindowResizing()) {
        scheduleLiveResizeScroll();
        windowResizeScroll.schedule();
        return;
      }
      scrollToBottom();
    });
    observeAutoFollowResizeTargets(node, resizeObserver);
    window.addEventListener("resize", scheduleLiveResizeScroll);
    return () => {
      window.removeEventListener("resize", scheduleLiveResizeScroll);
      if (liveResizeFrame !== undefined) window.cancelAnimationFrame(liveResizeFrame);
      windowResizeScroll.cancel();
      resizeObserver.disconnect();
    };
  }, [observeKey, open, refreshPointerScrollGestureLayout, scrollToBottom]);

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
      if (scrollbarHideTimerRef.current !== undefined) {
        window.clearTimeout(scrollbarHideTimerRef.current);
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
