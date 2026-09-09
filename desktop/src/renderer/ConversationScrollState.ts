import {
  type MutableRefObject,
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState
} from "react";
import type { Turn } from "../shared/protocol";
import type { ConversationPaneID } from "./AppState";
import {
  AUTO_FOLLOW_BOTTOM_THRESHOLD_PX,
  AUTO_FOLLOW_SCROLLBAR_HIDE_DELAY_MS,
  SCROLL_AWAY_KEYS,
  SCROLL_TOWARD_LATEST_KEYS,
  USER_SCROLL_AWAY_INTENT_WINDOW_MS,
  atLatestScrollView,
  clampScrollTop,
  eventTargetsNestedAutoFollowScroll,
  maxScrollTop,
  observeAutoFollowResizeTargets,
  selectionIntersectsNode,
  setAutoFollowOverflowAnchor,
} from "./AutoFollowScroll";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";
import { markSessionSwitch } from "./SessionSwitchPerformance";
import { motionDurationMs, prefersReducedMotion } from "./motion";

// Tight threshold so the conversation only re-engages auto-follow when the
// user is effectively parked at the bottom. The previous 48px band let one
// mouse-wheel notch land inside the band and silently re-arm auto-follow,
// which made slow scroll-up get yanked back to the bottom mid-gesture.
const CONVERSATION_AUTO_SCROLL_THRESHOLD_PX = AUTO_FOLLOW_BOTTOM_THRESHOLD_PX;
const CONVERSATION_SCROLLBAR_HIDE_DELAY_MS = AUTO_FOLLOW_SCROLLBAR_HIDE_DELAY_MS;
const CONVERSATION_USER_SCROLL_AWAY_INTENT_WINDOW_MS =
  USER_SCROLL_AWAY_INTENT_WINDOW_MS;
export function wheelDeltaPixels(
  event: WheelEvent,
  viewportHeight: number,
): number {
  if (event.deltaMode === 1) {
    return event.deltaY * 16;
  }
  if (event.deltaMode === 2) {
    return event.deltaY * Math.max(1, viewportHeight);
  }
  return event.deltaY;
}

function cssPixelValue(value: string): number {
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

/**
 * Height used by the conversation-adjacent floating chrome. This includes
 * accessory drawers (such as queued messages) and the visual lift applied to
 * an expanded composer, not just the input frame itself.
 */
export function dockComposerVisualHeight(node: HTMLElement): number {
  const layoutHeight = Math.ceil(node.getBoundingClientRect().height);
  const frame = node.querySelector<HTMLElement>(".composer-frame");
  if (!frame) {
    return layoutHeight;
  }
  const expandedOffset = cssPixelValue(
    frame.style.getPropertyValue("--composer-expanded-offset") ||
      window.getComputedStyle(frame).getPropertyValue("--composer-expanded-offset")
  );
  return layoutHeight + expandedOffset;
}

export type ConversationScrollSnapshot = {
  scrollTop: number;
  autoFollow: boolean;
};

export function useConversationScrollState({
  activeThreadID,
  activePane,
  splitConversation,
  primaryTurns,
  secondaryTurns,
  emptyConversation,
  initialized,
  nativeScrollBounce = window.wuu?.platform === "darwin" && window.wuu?.hostKind !== "web",
}: {
  activeThreadID?: string;
  activePane: ConversationPaneID;
  splitConversation: boolean;
  primaryTurns?: Turn[];
  secondaryTurns?: Turn[];
  emptyConversation: boolean;
  initialized: boolean;
  /** Use the macOS/AppKit rubber band instead of synthesizing wheel motion. */
  nativeScrollBounce?: boolean;
}): {
  conversationScrollRef: RefObject<HTMLDivElement | null>;
  /** Wrapper inside the conversation viewport. */
  scrollContentRef: RefObject<HTMLDivElement | null>;
  splitPaneRefs: MutableRefObject<Record<ConversationPaneID, HTMLElement | null>>;
  conversationPaneRef: RefObject<HTMLElement | null>;
  dockComposerRef: (node: HTMLElement | null) => void;
  /**
   * The live dock-composer element (set by dockComposerRef). Exposed so the
   * "跳到最新" pill can anchor itself just above the composer by direct
   * measurement — see JumpToLatestPill's anchored mode.
   */
  dockComposerNode: HTMLElement | null;
  scheduleStreamScroll: () => void;
  handleConversationScroll: (scrolledNode?: HTMLElement) => void;
  enableConversationAutoFollow: () => void;
  /** Smoothly move the current conversation to its new bottom on submit. */
  requestSubmittedQueryScroll: () => void;
  /**
   * Pause auto-follow so a programmatic scroll (e.g. query-history
   * jump) doesn't get pulled back to the bottom by the next stream
   * tick. Auto-follow resumes naturally once the user scrolls back
   * near the bottom.
   */
  disableConversationAutoFollow: () => void;
  /**
   * Snapshot the current scrollTop + auto-follow state so a later call to
   * `restoreConversationScrollPosition` can return the viewport to the
   * exact position the user came from. Used by the history-message edit
   * flow (capture on edit start, restore on cancel).
   */
  captureConversationScrollPosition: () => ConversationScrollSnapshot | undefined;
  restoreConversationScrollPosition: (snapshot: ConversationScrollSnapshot) => void;
} {
  const conversationScrollRef = useRef<HTMLDivElement | null>(null);
  const splitPaneRefs = useRef<Record<ConversationPaneID, HTMLElement | null>>({
    primary: null,
    secondary: null
  });
  const conversationPaneRef = useRef<HTMLElement | null>(null);
  const [dockComposerNode, setDockComposerNode] = useState<HTMLElement | null>(null);
  const dockComposerRef = useCallback((node: HTMLElement | null) => {
    setDockComposerNode(node);
  }, []);
  const dockComposerHeightRef = useRef(0);
  const conversationAutoFollowRef = useRef(true);
  const setAutoFollow = useCallback((next: boolean): void => {
    conversationAutoFollowRef.current = next;
  }, []);
  const lastConversationScrollTopRef = useRef(0);
  const programmaticScrollTopRef = useRef<number | undefined>(undefined);
  const suppressAutoFollowRearmRef = useRef(false);
  const smoothAutoFollowRef = useRef(false);
  const submittedScrollFrameRef = useRef<number | undefined>(undefined);
  const selectionPausedAutoFollowRef = useRef(false);
  const pointerScrollGestureRef = useRef<
    { node: HTMLElement; scrollTop: number; scrollHeight: number } | undefined
  >(undefined);
  const userScrollAwayIntentRef = useRef(false);
  const userScrollAwayIntentTimerRef = useRef<number | undefined>(undefined);
  const userScrollAwayStartTopRef = useRef<number | undefined>(undefined);
  const touchLastYRef = useRef<number | undefined>(undefined);
  const threadScrollSnapshotsRef = useRef(
    new Map<string, ConversationScrollSnapshot>()
  );
  const streamScrollFrameRef = useRef<number | undefined>(undefined);
  const liveResizeScrollFrameRef = useRef<number | undefined>(undefined);
  const resizeSettleStreamScrollRef = useRef<ReturnType<
    typeof createWindowResizeSettleScheduler
  > | null>(null);
  const conversationScrollbarHideTimerRef = useRef<number | undefined>(undefined);
  const scrollContentRef = useRef<HTMLDivElement | null>(null);
  const bottomOverscrollFromAwayRef = useRef(false);

  const refreshPointerScrollGestureLayout = useCallback((node: HTMLElement): void => {
    const gesture = pointerScrollGestureRef.current;
    if (gesture?.node !== node) {
      return;
    }
    gesture.scrollTop = clampScrollTop(node, node.scrollTop);
    gesture.scrollHeight = node.scrollHeight;
  }, []);

  function conversationViewport(): HTMLElement | undefined {
    if (splitConversation) {
      return splitPaneRefs.current[activePane] ?? undefined;
    }
    return conversationScrollRef.current ?? undefined;
  }

  function setNativeBottomOverscrollEnabled(
    node: HTMLElement,
    enabled: boolean,
  ): void {
    if (!nativeScrollBounce) {
      return;
    }
    node.style.overscrollBehaviorY = enabled ? "contain" : "none";
  }

  function cancelBottomOverscroll(node?: HTMLElement): void {
    bottomOverscrollFromAwayRef.current = false;
    if (node) {
      setNativeBottomOverscrollEnabled(node, false);
    }
  }

  function showConversationScrollbar(node: HTMLElement): void {
    if (
      node.classList.contains("empty-scroll-region") ||
      node.classList.contains("workspace-scroll-region") ||
      node.scrollHeight <= node.clientHeight
    ) {
      return;
    }
    node.classList.add("scrollbar-visible");
    if (conversationScrollbarHideTimerRef.current !== undefined) {
      window.clearTimeout(conversationScrollbarHideTimerRef.current);
    }
    conversationScrollbarHideTimerRef.current = window.setTimeout(() => {
      conversationScrollbarHideTimerRef.current = undefined;
      node.classList.remove("scrollbar-visible");
    }, CONVERSATION_SCROLLBAR_HIDE_DELAY_MS);
  }

  function rememberThreadScrollSnapshot(
    threadID: string,
    node: HTMLElement,
    autoFollow: boolean
  ): void {
    threadScrollSnapshotsRef.current.set(threadID, {
      scrollTop: clampScrollTop(node, node.scrollTop),
      autoFollow: node.scrollHeight <= node.clientHeight ? true : autoFollow
    });
  }

  function rememberActiveThreadScrollSnapshot(
    node: HTMLElement,
    autoFollow: boolean
  ): void {
    if (!activeThreadID) {
      return;
    }
    rememberThreadScrollSnapshot(activeThreadID, node, autoFollow);
  }

  const clearUserScrollAwayIntent = useCallback((): void => {
    userScrollAwayIntentRef.current = false;
    userScrollAwayStartTopRef.current = undefined;
    touchLastYRef.current = undefined;
    if (userScrollAwayIntentTimerRef.current !== undefined) {
      window.clearTimeout(userScrollAwayIntentTimerRef.current);
      userScrollAwayIntentTimerRef.current = undefined;
    }
  }, []);

  const cancelSubmittedQueryScroll = useCallback((): void => {
    if (smoothAutoFollowRef.current) suppressAutoFollowRearmRef.current = false;
    smoothAutoFollowRef.current = false;
    if (submittedScrollFrameRef.current !== undefined) {
      window.cancelAnimationFrame(submittedScrollFrameRef.current);
      submittedScrollFrameRef.current = undefined;
    }
  }, []);

  const markUserScrollAwayIntent = useCallback((startTop?: number): void => {
    cancelSubmittedQueryScroll();
    userScrollAwayIntentRef.current = true;
    if (startTop !== undefined) {
      userScrollAwayStartTopRef.current = startTop;
    }
    if (userScrollAwayIntentTimerRef.current !== undefined) {
      window.clearTimeout(userScrollAwayIntentTimerRef.current);
    }
    userScrollAwayIntentTimerRef.current = window.setTimeout(() => {
      userScrollAwayIntentRef.current = false;
      userScrollAwayStartTopRef.current = undefined;
      userScrollAwayIntentTimerRef.current = undefined;
    }, CONVERSATION_USER_SCROLL_AWAY_INTENT_WINDOW_MS);
  }, [cancelSubmittedQueryScroll]);

  function applyProgrammaticScroll(
    node: HTMLElement,
    top: number,
    autoFollow: boolean,
    options: { revealScrollbar?: boolean } = {}
  ): void {
    cancelSubmittedQueryScroll();
    clearUserScrollAwayIntent();
    cancelBottomOverscroll(node);
    suppressAutoFollowRearmRef.current = false;
    selectionPausedAutoFollowRef.current = false;
    const targetTop = clampScrollTop(node, top);
    const moved = node.scrollTop !== targetTop;
    if (moved) node.scrollTop = targetTop;
    const actualTop = clampScrollTop(node, node.scrollTop);
    if (Math.abs(node.scrollTop - actualTop) > 1) {
      node.scrollTop = actualTop;
    }
    programmaticScrollTopRef.current = actualTop;
    lastConversationScrollTopRef.current = actualTop;
    const nextAutoFollow =
      node.scrollHeight <= node.clientHeight ? true : autoFollow;
    setAutoFollow(nextAutoFollow);
    setAutoFollowOverflowAnchor(node, nextAutoFollow);
    rememberActiveThreadScrollSnapshot(node, nextAutoFollow);
    if (moved && options.revealScrollbar) {
      showConversationScrollbar(node);
    }
  }

  const scrollConversationToBottom = useCallback((): void => {
    const node = conversationViewport();
    if (!node || !conversationAutoFollowRef.current) {
      return;
    }
    // The submit animation reads the live bottom itself. A native smooth
    // scroll restarted on every collapsing-card resize never gets up to speed.
    if (smoothAutoFollowRef.current) return;
    // Content/layout following is not a user scroll. In particular, keyboard
    // animation must not repeatedly reveal the scrollbar as the viewport shrinks.
    applyProgrammaticScroll(node, node.scrollHeight, true);
  }, [
    activePane,
    activeThreadID,
    clearUserScrollAwayIntent,
    setAutoFollow,
    splitConversation,
  ]);

  const requestSubmittedQueryScroll = useCallback((): void => {
    cancelSubmittedQueryScroll();
    clearUserScrollAwayIntent();
    cancelBottomOverscroll(conversationViewport());
    selectionPausedAutoFollowRef.current = false;
    setAutoFollow(true);
    const node = conversationViewport();
    if (!node) {
      return;
    }
    const smooth = !prefersReducedMotion();
    smoothAutoFollowRef.current = smooth;
    suppressAutoFollowRearmRef.current = smooth;
    setAutoFollowOverflowAnchor(node, true);
    rememberActiveThreadScrollSnapshot(node, true);
    if (!smooth) {
      scrollConversationToBottom();
      return;
    }
    const startTop = clampScrollTop(node, node.scrollTop);
    const duration = motionDurationMs("--query-submit-duration", 220);
    let startedAt: number | undefined;
    const step = (now: number): void => {
      submittedScrollFrameRef.current = undefined;
      if (!smoothAutoFollowRef.current || !conversationAutoFollowRef.current) return;
      startedAt ??= now;
      const progress = duration > 0 ? Math.min(1, (now - startedAt) / duration) : 1;
      // CSS uses cubic-bezier(1/3, 1, 2/3, 1): linear time, cubic ease-out.
      const eased = 1 - (1 - progress) ** 3;
      // Share one deadline with the diff receipt's exit, even while its height
      // and the optimistic turn change. Layout signals must not restart easing.
      node.scrollTop = startTop + (maxScrollTop(node) - startTop) * eased;
      programmaticScrollTopRef.current = clampScrollTop(node, node.scrollTop);
      lastConversationScrollTopRef.current = programmaticScrollTopRef.current;
      rememberActiveThreadScrollSnapshot(node, true);
      if (progress < 1) {
        submittedScrollFrameRef.current = window.requestAnimationFrame(step);
      } else {
        applyProgrammaticScroll(node, node.scrollHeight, true, { revealScrollbar: true });
      }
    };
    submittedScrollFrameRef.current = window.requestAnimationFrame(step);
  }, [
    activePane,
    activeThreadID,
    clearUserScrollAwayIntent,
    scrollConversationToBottom,
    cancelSubmittedQueryScroll,
    setAutoFollow,
    splitConversation,
  ]);

  useLayoutEffect(() => cancelSubmittedQueryScroll,
    [activeThreadID, activePane, splitConversation, cancelSubmittedQueryScroll]);

  const scheduleLiveResizeScroll = useCallback((): void => {
    if (
      liveResizeScrollFrameRef.current !== undefined ||
      !conversationAutoFollowRef.current
    ) {
      return;
    }
    liveResizeScrollFrameRef.current = window.requestAnimationFrame(() => {
      liveResizeScrollFrameRef.current = undefined;
      const node = conversationViewport();
      if (
        !node ||
        !isWindowResizing() ||
        !conversationAutoFollowRef.current
      ) {
        return;
      }
      // Chromium clamps oversized scroll targets to the real bottom. Using a
      // sentinel avoids reading scrollHeight/clientHeight during live resize,
      // and rAF coalescing caps the work at one scroll write per paint.
      node.scrollTop = Number.MAX_SAFE_INTEGER;
      lastConversationScrollTopRef.current = node.scrollTop;
    });
  }, [activePane, splitConversation]);

  const scheduleStreamScroll = useCallback((): void => {
    if (!activeThreadID) {
      return;
    }
    if (!conversationAutoFollowRef.current) {
      return;
    }
    if (isWindowResizing()) {
      scheduleLiveResizeScroll();
      if (!resizeSettleStreamScrollRef.current) {
        resizeSettleStreamScrollRef.current =
          createWindowResizeSettleScheduler(scrollConversationToBottom);
      }
      resizeSettleStreamScrollRef.current.schedule();
      return;
    }
    if (streamScrollFrameRef.current !== undefined) {
      return;
    }
    streamScrollFrameRef.current = window.requestAnimationFrame(() => {
      scrollConversationToBottom();
      streamScrollFrameRef.current = window.requestAnimationFrame(() => {
        streamScrollFrameRef.current = undefined;
        scrollConversationToBottom();
      });
    });
  }, [scheduleLiveResizeScroll, scrollConversationToBottom]);

  useEffect(() => {
    resizeSettleStreamScrollRef.current?.cancel();
    resizeSettleStreamScrollRef.current = null;
  }, [scrollConversationToBottom]);

  const enableConversationAutoFollow = useCallback((): void => {
    cancelSubmittedQueryScroll();
    suppressAutoFollowRearmRef.current = false;
    selectionPausedAutoFollowRef.current = false;
    cancelBottomOverscroll(conversationViewport());
    setAutoFollow(true);
    const node = conversationViewport();
    if (node) {
      setAutoFollowOverflowAnchor(node, true);
      rememberActiveThreadScrollSnapshot(node, true);
    }
  }, [activePane, activeThreadID, cancelSubmittedQueryScroll, setAutoFollow, splitConversation]);

  const disableConversationAutoFollow = useCallback((): void => {
    cancelSubmittedQueryScroll();
    suppressAutoFollowRearmRef.current = true;
    setAutoFollow(false);
    const node = conversationViewport();
    if (node) {
      setAutoFollowOverflowAnchor(node, false);
      rememberActiveThreadScrollSnapshot(node, false);
    }
  }, [activePane, activeThreadID, cancelSubmittedQueryScroll, setAutoFollow, splitConversation]);

  // Snapshot the user's current scroll state so a later call to
  // restoreConversationScrollPosition can return the viewport to exactly
  // where they were. Used by the history-message edit flow: capture on
  // edit start, restore on cancel so the user is parked back where they
  // came from instead of being yanked to the bottom by the resize
  // observer when the inline editor swaps back to a bubble.
  const captureConversationScrollPosition = useCallback(
    (): ConversationScrollSnapshot | undefined => {
      const node = conversationViewport();
      if (!node) {
        return undefined;
      }
      return {
        scrollTop: clampScrollTop(node, node.scrollTop),
        autoFollow: conversationAutoFollowRef.current,
      };
    },
    [activePane, splitConversation],
  );

  const restoreConversationScrollPosition = useCallback(
    (snapshot: ConversationScrollSnapshot): void => {
      const node = conversationViewport();
      if (!node) {
        return;
      }
      applyProgrammaticScroll(node, snapshot.scrollTop, snapshot.autoFollow, {
        revealScrollbar: true,
      });
    },
    [activePane, activeThreadID, setAutoFollow, splitConversation],
  );

  function handleConversationScroll(scrolledNode?: HTMLElement): void {
    const node = scrolledNode ?? conversationViewport();
    if (!node) {
      return;
    }
    if (isWindowResizing()) {
      if (conversationAutoFollowRef.current) {
        scheduleStreamScroll();
      }
      return;
    }
    const programmaticTop = programmaticScrollTopRef.current;
    if (programmaticTop !== undefined) {
      programmaticScrollTopRef.current = undefined;
      if (Math.abs(node.scrollTop - programmaticTop) <= 1) {
        lastConversationScrollTopRef.current = clampScrollTop(
          node,
          node.scrollTop
        );
        if (
          conversationAutoFollowRef.current &&
          !userScrollAwayIntentRef.current &&
          !atLatestScrollView(node, CONVERSATION_AUTO_SCROLL_THRESHOLD_PX)
        ) {
          scrollConversationToBottom();
          return;
        }
        rememberActiveThreadScrollSnapshot(
          node,
          conversationAutoFollowRef.current
        );
        return;
      }
    }

    // Position-driven and intent-driven auto-follow.
    //
    // The previous logic re-armed auto-follow whenever the user landed
    // inside the bottom band (distanceFromBottom <= 16px), regardless of
    // scroll direction. That created a dead zone: any wheel-up landing
    // inside the band left auto-follow engaged, so the next stream tick
    // (or `onCollapseComplete` re-anchor after a fold shrink) yanked
    // scrollTop back to scrollHeight and the user felt the scroll as
    // "resistant" — most visibly during model output but universally
    // any time something triggered `scheduleStreamScroll` while the user
    // was inside the band.
    //
    // User intent overrides position: any user-initiated upward scroll
    // disarms auto-follow, regardless of how small the delta is. But
    // do not derive that solely from `previousScrollTop`: when the
    // conversation was hidden while content streamed, the old scrollTop can
    // be lower than the remounted bottom, making the first user scroll-up
    // look like a downward move.
    //
    // Still, only disarm after the viewport has actually left the absolute
    // bottom. A nested reasoning/process scroll can emit an upward wheel
    // without moving the outer conversation; that should keep following.
    //
    // layout-driven scrollTop clamps (for example a completed process fold
    // shrinking above the viewport) can also move scrollTop upward while
    // the viewport is still at the latest content. Those must keep
    // auto-follow armed; otherwise the next streaming or settle frame will
    // stop sticking to the bottom even though the user never scrolled away.
    const pointerGesture = pointerScrollGestureRef.current;
    if (selectionPausedAutoFollowRef.current && pointerGesture?.node === node) {
      if (node.scrollHeight !== pointerGesture.scrollHeight) {
        pointerGesture.scrollTop = clampScrollTop(node, node.scrollTop);
        pointerGesture.scrollHeight = node.scrollHeight;
      } else if (node.scrollTop > pointerGesture.scrollTop) {
        selectionPausedAutoFollowRef.current = false;
      }
    }

    const previousScrollTop = lastConversationScrollTopRef.current;
    const scrolledUp = node.scrollTop < previousScrollTop;
    const scrolledDown = node.scrollTop > previousScrollTop;
    const userScrollAwayIntent = userScrollAwayIntentRef.current;
    lastConversationScrollTopRef.current = clampScrollTop(node, node.scrollTop);

    // Native clamping can emit scroll before ResizeObserver gets a chance to
    // record a programmatic target (notably when the keyboard is dismissed).
    const layoutClamp = scrolledUp && !userScrollAwayIntent &&
      node.scrollTop >= maxScrollTop(node) - 1;
    if ((scrolledUp || scrolledDown) && !layoutClamp) {
      showConversationScrollbar(node);
    }

    const atLatestView = atLatestScrollView(
      node,
      CONVERSATION_AUTO_SCROLL_THRESHOLD_PX
    );
    const scrollAwayStartTop = userScrollAwayStartTopRef.current;
    const movedAboveUserIntentStart =
      userScrollAwayIntent &&
      scrollAwayStartTop !== undefined &&
      node.scrollTop < scrollAwayStartTop - 1;
    const movedAbovePreviousScroll = userScrollAwayIntent && scrolledUp;
    let nextAutoFollow = conversationAutoFollowRef.current;
    if (
      (movedAboveUserIntentStart || movedAbovePreviousScroll) &&
      node.scrollTop < maxScrollTop(node) - 1
    ) {
      suppressAutoFollowRearmRef.current = false;
      userScrollAwayStartTopRef.current = undefined;
      nextAutoFollow = false;
      setAutoFollow(false);
      setAutoFollowOverflowAnchor(node, false);
    } else if (
      conversationAutoFollowRef.current &&
      scrolledUp &&
      !atLatestView &&
      node.scrollTop < maxScrollTop(node) - 1
    ) {
      // Native scrollbar drags and some platform scroll paths can arrive as
      // a bare scroll event. If the viewport moved upward away from latest
      // content, treat it as user control even without a prior wheel/key/touch.
      suppressAutoFollowRearmRef.current = false;
      nextAutoFollow = false;
      setAutoFollow(false);
      setAutoFollowOverflowAnchor(node, false);
    } else if (atLatestView && suppressAutoFollowRearmRef.current) {
      // Query-history / turn-rail jumps are programmatic smooth scrolls.
      // The browser can emit an unchanged or tiny upward scroll event while
      // the viewport is still inside the bottom band. If that re-arms
      // auto-follow, the next scroll/layout signal yanks the viewport back to
      // the bottom before the jump reaches its target. Only an actual downward
      // move back to the latest content should clear this jump guard.
      if (smoothAutoFollowRef.current) {
        // Reaching the old bottom must not finish the submit animation before
        // React inserts the optimistic turn or the diff receipt finishes exiting.
        nextAutoFollow = true;
        setAutoFollow(true);
        setAutoFollowOverflowAnchor(node, true);
      } else if (scrolledDown && !selectionPausedAutoFollowRef.current) {
        suppressAutoFollowRearmRef.current = false;
        nextAutoFollow = true;
        setAutoFollow(true);
        setAutoFollowOverflowAnchor(node, true);
      } else {
        nextAutoFollow = false;
        setAutoFollow(false);
        setAutoFollowOverflowAnchor(node, false);
      }
    } else if (suppressAutoFollowRearmRef.current) {
      // A programmatic jump is in flight, and the viewport has not yet
      // reached the bottom band. The previous branch already handled the
      // atLatestView case; this branch covers the in-between frames.
      //
      // The smooth animation produces a stream of `scrolledDown` scroll
      // events as the viewport glides to the bottom. If we let branch 4
      // fire here, it would call `applyProgrammaticScroll(..., true)`
      // and instantly snap the scroll back to scrollHeight, breaking the
      // animation. Instead, track the position only and let the flag be
      // cleared by the atLatestView branch above when the animation
      // actually lands at the bottom (or by the user-initiated branches
      // when the user takes manual control).
      //
      // Keep the existing auto-follow value until the animation lands.
      nextAutoFollow = conversationAutoFollowRef.current;
    } else if (atLatestView) {
      suppressAutoFollowRearmRef.current = false;
      if (
        conversationAutoFollowRef.current ||
        scrolledDown ||
        node.scrollHeight <= node.clientHeight
      ) {
        nextAutoFollow = true;
        setAutoFollow(true);
        setAutoFollowOverflowAnchor(node, true);
      } else {
        // A layout shrink can clamp an already-away viewport to the new max
        // scrollTop without user intent. Keep the user's away state unless
        // they actively scroll down to latest or content no longer scrolls.
        nextAutoFollow = false;
        setAutoFollow(false);
        setAutoFollowOverflowAnchor(node, false);
      }
    } else if (conversationAutoFollowRef.current && !userScrollAwayIntent) {
      suppressAutoFollowRearmRef.current = false;
      if (isWindowResizing()) {
        nextAutoFollow = true;
        scheduleStreamScroll();
        rememberActiveThreadScrollSnapshot(node, nextAutoFollow);
        return;
      }
      // A raw scroll event that leaves the latest view is ambiguous: it can
      // be a scrollbar drag, a platform scroll path without wheel/key/touch
      // preflight, or a stale baseline after the conversation remounts. The
      // durable auto-follow signals already call scrollConversationToBottom
      // directly (stream frames, turn snapshots, resize observers, fold
      // collapse), so do not yank the viewport back from this fallback path.
      nextAutoFollow = false;
      setAutoFollow(false);
      setAutoFollowOverflowAnchor(node, false);
    } else {
      suppressAutoFollowRearmRef.current = false;
    }
    if (nativeScrollBounce && scrolledUp) {
      bottomOverscrollFromAwayRef.current = true;
      setNativeBottomOverscrollEnabled(node, true);
    }
    rememberActiveThreadScrollSnapshot(node, nextAutoFollow);
  }

  useLayoutEffect(() => {
    const node = conversationViewport();
    if (!activeThreadID || !node) {
      programmaticScrollTopRef.current = undefined;
      lastConversationScrollTopRef.current = 0;
      setAutoFollow(true);
      return undefined;
    }

    markSessionSwitch(activeThreadID, "scroll-restore-start");
    const snapshot = threadScrollSnapshotsRef.current.get(activeThreadID);
    if (snapshot && !snapshot.autoFollow) {
      applyProgrammaticScroll(node, snapshot.scrollTop, false);
      bottomOverscrollFromAwayRef.current = true;
      setNativeBottomOverscrollEnabled(node, true);
    } else {
      applyProgrammaticScroll(node, node.scrollHeight, true);
      setNativeBottomOverscrollEnabled(node, false);
    }
    markSessionSwitch(activeThreadID, "scroll-restore-end");
    return undefined;
  }, [activePane, activeThreadID, setAutoFollow, splitConversation]);

  useLayoutEffect(() => {
    if (!activeThreadID) {
      return;
    }
    // Turn snapshots can add non-token content (for example a gray process
    // row). Re-anchor before paint so the bottom never flashes at old scrollTop.
    if (isWindowResizing()) {
      scheduleStreamScroll();
      return;
    }
    scrollConversationToBottom();
  }, [
    activeThreadID,
    primaryTurns,
    scheduleStreamScroll,
    scrollConversationToBottom,
    secondaryTurns,
  ]);

  useLayoutEffect(() => {
    const node = conversationViewport();
    if (!node) {
      return undefined;
    }
    setNativeBottomOverscrollEnabled(
      node,
      bottomOverscrollFromAwayRef.current,
    );
    const handleWheel = (event: WheelEvent): void => {
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        if (event.deltaY < 0) {
          markUserScrollAwayIntent(clampScrollTop(node, node.scrollTop));
        }
        return;
      }
      const deltaPx = wheelDeltaPixels(event, node.clientHeight);
      if (deltaPx < 0) {
        cancelBottomOverscroll(node);
        if (nativeScrollBounce) {
          bottomOverscrollFromAwayRef.current = true;
          setNativeBottomOverscrollEnabled(node, true);
        }
        markUserScrollAwayIntent(clampScrollTop(node, node.scrollTop));
        // Take user control before the browser's later `scroll` event. During
        // streaming, an already queued auto-follow frame can otherwise run in
        // the wheel-to-scroll gap and write the viewport back to the bottom,
        // making trackpad and mouse-wheel movement feel sticky or resistant.
        disableConversationAutoFollow();
      } else if (deltaPx > 0) {
        selectionPausedAutoFollowRef.current = false;
      }
    };
    const handleNativeScrollEnd = (): void => {
      if (
        nativeScrollBounce &&
        bottomOverscrollFromAwayRef.current &&
        atLatestScrollView(node, 1)
      ) {
        bottomOverscrollFromAwayRef.current = false;
        setNativeBottomOverscrollEnabled(node, false);
      }
    };
    const handlePointerDown = (event: PointerEvent): void => {
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        return;
      }
      if (event.target === node) {
        pointerScrollGestureRef.current = {
          node,
          scrollTop: clampScrollTop(node, node.scrollTop),
          scrollHeight: node.scrollHeight,
        };
        markUserScrollAwayIntent(clampScrollTop(node, node.scrollTop));
      }
    };
    const handlePointerEnd = (): void => {
      pointerScrollGestureRef.current = undefined;
    };
    const handleSelectionChange = (): void => {
      if (selectionIntersectsNode(document.getSelection(), node)) {
        selectionPausedAutoFollowRef.current = true;
        disableConversationAutoFollow();
      }
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        return;
      }
      if (SCROLL_AWAY_KEYS.has(event.key)) {
        if (nativeScrollBounce) {
          bottomOverscrollFromAwayRef.current = true;
          setNativeBottomOverscrollEnabled(node, true);
        }
        markUserScrollAwayIntent(clampScrollTop(node, node.scrollTop));
      } else if (SCROLL_TOWARD_LATEST_KEYS.has(event.key)) {
        selectionPausedAutoFollowRef.current = false;
      }
    };
    const handleTouchStart = (event: TouchEvent): void => {
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        touchLastYRef.current = event.touches[0]?.clientY;
        return;
      }
      touchLastYRef.current = event.touches[0]?.clientY;
    };
    const handleTouchMove = (event: TouchEvent): void => {
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        const currentY = event.touches[0]?.clientY;
        const previousY = touchLastYRef.current;
        if (
          currentY !== undefined &&
          previousY !== undefined &&
          currentY > previousY
        ) {
          markUserScrollAwayIntent(clampScrollTop(node, node.scrollTop));
        }
        touchLastYRef.current = currentY;
        return;
      }
      const currentY = event.touches[0]?.clientY;
      const previousY = touchLastYRef.current;
      if (
        currentY !== undefined &&
        previousY !== undefined &&
        currentY > previousY
      ) {
        if (nativeScrollBounce) {
          bottomOverscrollFromAwayRef.current = true;
          setNativeBottomOverscrollEnabled(node, true);
        }
        markUserScrollAwayIntent(clampScrollTop(node, node.scrollTop));
      } else if (
        currentY !== undefined &&
        previousY !== undefined &&
        currentY < previousY
      ) {
        selectionPausedAutoFollowRef.current = false;
      }
      touchLastYRef.current = currentY;
    };
    const handleTouchEnd = (): void => {
      touchLastYRef.current = undefined;
    };
    node.addEventListener("wheel", handleWheel, { passive: true });
    node.addEventListener("scrollend", handleNativeScrollEnd);
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
      node.removeEventListener("wheel", handleWheel);
      node.removeEventListener("scrollend", handleNativeScrollEnd);
      node.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("pointerup", handlePointerEnd);
      window.removeEventListener("pointercancel", handlePointerEnd);
      document.removeEventListener("selectionchange", handleSelectionChange);
      node.removeEventListener("touchstart", handleTouchStart);
      node.removeEventListener("touchmove", handleTouchMove);
      node.removeEventListener("touchend", handleTouchEnd);
      node.removeEventListener("touchcancel", handleTouchEnd);
      node.removeEventListener("keydown", handleKeyDown);
      node.style.removeProperty("overscroll-behavior-y");
    };
  });

  useLayoutEffect(() => {
    const node = conversationViewport();
    if (!node || typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const windowResizeScroll = createWindowResizeSettleScheduler(() => {
      // The resize has settled. Commit the real scrollTop so the scrollbar
      // position, the "jump to latest" pill, and the turn-rail anchor line
      // up with the new viewport without moving text during the live drag.
      scrollConversationToBottom();
    });
    const resizeObserver = new ResizeObserver(() => {
      refreshPointerScrollGestureLayout(node);
      if (isWindowResizing()) {
        cancelBottomOverscroll(node);
        scheduleLiveResizeScroll();
        windowResizeScroll.schedule();
        return;
      }
      scrollConversationToBottom();
    });
    observeAutoFollowResizeTargets(node, resizeObserver);
    window.addEventListener("resize", scheduleLiveResizeScroll);
    return () => {
      windowResizeScroll.cancel();
      resizeObserver.disconnect();
      window.removeEventListener("resize", scheduleLiveResizeScroll);
    };
  }, [
    activePane,
    activeThreadID,
    emptyConversation,
    initialized,
    primaryTurns,
    refreshPointerScrollGestureLayout,
    secondaryTurns,
    scrollConversationToBottom,
    scheduleLiveResizeScroll,
    splitConversation
  ]);

  useLayoutEffect(() => {
    const node = dockComposerNode;
    const pane = conversationPaneRef.current;
    let windowResizeHeight: ReturnType<
      typeof createWindowResizeSettleScheduler
    > | undefined;
    const applyHeight = (nextHeight: number): void => {
      const nextValue = `${nextHeight}px`;
      if (
        dockComposerHeightRef.current === nextHeight &&
        pane?.style.getPropertyValue("--dock-composer-height") === nextValue
      ) {
        return;
      }
      const wasVisible = dockComposerHeightRef.current > 0;
      const isVisible = nextHeight > 0;
      const visibilityChanged = wasVisible !== isVisible;
      dockComposerHeightRef.current = nextHeight;
      pane?.style.setProperty("--dock-composer-height", nextValue);
      // Only re-scroll on a visibility transition (composer hidden → visible
      // or vice versa), not on every continuous resize from typing or focus
      // changes. Continuous resize firing scrollConversationToBottom used to
      // fight the user whenever they tried to scroll up.
      if (visibilityChanged && isVisible && conversationAutoFollowRef.current) {
        scrollConversationToBottom();
      }
    };

    if (!node) {
      applyHeight(0);
      return;
    }

    const updateHeight = (): void => {
      if (isWindowResizing()) {
        windowResizeHeight?.schedule();
        return;
      }
      const nextHeight = dockComposerVisualHeight(node);
      applyHeight(nextHeight);
    };

    windowResizeHeight = createWindowResizeSettleScheduler(updateHeight);
    updateHeight();
    const resizeObserver = new ResizeObserver(updateHeight);
    resizeObserver.observe(node);
    const frame = node.querySelector<HTMLElement>(".composer-frame");
    if (frame) {
      resizeObserver.observe(frame);
    }
    return () => {
      windowResizeHeight?.cancel();
      resizeObserver.disconnect();
    };
  }, [
    dockComposerNode,
    emptyConversation,
    initialized,
    scrollConversationToBottom
  ]);

  useEffect(() => {
    return () => {
      if (streamScrollFrameRef.current !== undefined) {
        window.cancelAnimationFrame(streamScrollFrameRef.current);
        streamScrollFrameRef.current = undefined;
      }
      if (liveResizeScrollFrameRef.current !== undefined) {
        window.cancelAnimationFrame(liveResizeScrollFrameRef.current);
        liveResizeScrollFrameRef.current = undefined;
      }
      resizeSettleStreamScrollRef.current?.cancel();
      resizeSettleStreamScrollRef.current = null;
      if (conversationScrollbarHideTimerRef.current !== undefined) {
        window.clearTimeout(conversationScrollbarHideTimerRef.current);
      }
      cancelBottomOverscroll();
      clearUserScrollAwayIntent();
    };
  }, [clearUserScrollAwayIntent]);

  return {
    conversationScrollRef,
    scrollContentRef,
    splitPaneRefs,
    conversationPaneRef,
    dockComposerRef,
    dockComposerNode,
    scheduleStreamScroll,
    handleConversationScroll,
    enableConversationAutoFollow,
    disableConversationAutoFollow,
    captureConversationScrollPosition,
    restoreConversationScrollPosition,
    requestSubmittedQueryScroll,
  };
}
