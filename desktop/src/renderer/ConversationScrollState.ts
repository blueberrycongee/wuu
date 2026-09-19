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
  latestFollowScrollTop,
  maxScrollTop,
  observeAutoFollowResizeTargets,
  selectionIntersectsNode,
  sessionTailSpacePx,
  setAutoFollowOverflowAnchor,
} from "./AutoFollowScroll";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";
import { markSessionSwitch } from "./SessionSwitchPerformance";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { createMessageScrollMotion, messageMotionTime } from "./MessageScrollMotion";
import { useSessionTailSpace } from "./SessionTailSpace";
import { useMessageArrivalMotion } from "./useMessageArrivalMotion";

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

const SUBMITTED_CONTEXT_FRACTION = 0.2;
const SUBMITTED_CONTEXT_MIN_FRACTION = 0.16;
const SUBMITTED_CONTEXT_MAX_FRACTION = 0.24;
const SUBMITTED_MESSAGE_MIN_FRACTION = 0.25;
const SUBMITTED_MESSAGE_MAX_FRACTION = 0.35;

function clampFraction(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

/**
 * Short messages leave room for the response in the upper reading band.
 * Tall folded cards (including attachments) keep their beginning visible;
 * response space must never be bought by clipping the submitted content.
 */
export function submittedMessageScrollTop(
  viewport: HTMLElement,
  message: HTMLElement,
  clamp = true,
): number {
  const { targetTop } = submittedMessagePlacement(viewport, message);
  return clamp ? clampScrollTop(viewport, targetTop) : targetTop;
}

function submittedMessagePlacement(viewport: HTMLElement, message: HTMLElement) {
  const viewportRect = viewport.getBoundingClientRect();
  const scrollTop = viewport.scrollTop;
  const messageRect = message.getBoundingClientRect();
  const viewportHeight = viewport.clientHeight;
  const messageTop = messageRect.top;
  const messageBottom = Number.isFinite(messageRect.bottom)
    ? messageRect.bottom
    : messageTop + (Number.isFinite(messageRect.height) ? messageRect.height : 0);
  const messageHeight = Math.max(0, messageBottom - messageTop);
  const context = clampFraction(
    SUBMITTED_CONTEXT_FRACTION,
    SUBMITTED_CONTEXT_MIN_FRACTION,
    SUBMITTED_CONTEXT_MAX_FRACTION,
  );
  const messageBand = clampFraction(
    context + messageHeight / Math.max(1, viewportHeight),
    SUBMITTED_MESSAGE_MIN_FRACTION,
    SUBMITTED_MESSAGE_MAX_FRACTION,
  );
  const bottomTarget = scrollTop + messageBottom - viewportRect.top - viewportHeight * messageBand;
  const topLimit = scrollTop + messageTop - viewportRect.top - viewportHeight * 0.08;
  const screenTop = messageTop - viewportRect.top;
  return {
    targetTop: Math.max(0, Math.min(bottomTarget, topLimit)),
    documentTop: scrollTop + screenTop,
    screenTop,
    messageHeight,
  };
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

type SubmissionScrollPhase = "pending" | "placing" | "holding";
type ConversationScrollMode = "following" | "paused" | SubmissionScrollPhase;

export type ConversationScrollSnapshot = {
  scrollTop: number;
  autoFollow: boolean;
  submissionPhase?: SubmissionScrollPhase;
  submittedMessageID?: string;
};

export function useConversationScrollState({
  activeThreadID,
  activePane,
  splitConversation,
  primaryTurns,
  secondaryTurns,
  emptyConversation,
  initialized,
  running = false,
  statusClusterNode = null,
  nativeScrollBounce = window.wuu?.platform === "darwin" && window.wuu?.hostKind !== "web",
}: {
  activeThreadID?: string;
  activePane: ConversationPaneID;
  splitConversation: boolean;
  primaryTurns?: Turn[];
  secondaryTurns?: Turn[];
  emptyConversation: boolean;
  initialized: boolean;
  running?: boolean;
  statusClusterNode?: HTMLElement | null;
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
  /** Position this submission once its optimistic bubble has mounted. */
  requestSubmittedQueryScroll: (messageID: string) => void;
  /** Queue/steer inputs are positioned only when their exact source materializes. */
  requestDeferredQueryScroll: (sourceID: string) => void;
  acknowledgeSubmittedMessage: (pendingID: string, messageID: string) => void;
  discardSubmittedMessage: (messageID: string) => void;
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
  const submissionRef = useRef<{ messageID: string; threadID?: string; animate: boolean } | undefined>(undefined);
  const deferredSubmissionRef = useRef(new Set<string>());
  // Exactly one owner can write scrollTop. Geometry alone cannot transfer
  // ownership: a submission's padded bottom is not the bottom of its output.
  const scrollModeRef = useRef<ConversationScrollMode>("following");
  function isFollowing(): boolean { return scrollModeRef.current === "following"; }
  function submissionPhase(): SubmissionScrollPhase | undefined {
    const mode = scrollModeRef.current;
    return mode === "following" || mode === "paused" ? undefined : mode;
  }
  const { reconcile: reconcileArrivals, acknowledge: acknowledgeArrival, cancel: cancelArrivals } = useMessageArrivalMotion();
  const previousThreadRef = useRef(activeThreadID);
  const [dockComposerNode, setDockComposerNode] = useState<HTMLElement | null>(null);
  const dockComposerRef = useCallback((node: HTMLElement | null) => {
    setDockComposerNode(node);
  }, []);
  const dockComposerHeightRef = useRef(0);
  const setAutoFollow = useCallback((next: boolean): void => {
    scrollModeRef.current = next ? "following" : "paused";
  }, []);
  const lastConversationScrollTopRef = useRef(0);
  const programmaticScrollTopRef = useRef<number | undefined>(undefined);
  const suppressAutoFollowRearmRef = useRef(false);
  const smoothAutoFollowRef = useRef(false);
  const submittedScrollFrameRef = useRef<number | undefined>(undefined);
  const reflowSubmittedMotionRef = useRef<(() => void) | undefined>(undefined);
  const positionSubmittedMessageRef = useRef<((animate: boolean) => boolean) | undefined>(undefined);
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
      autoFollow,
      submissionPhase: submissionPhase(),
      submittedMessageID: submissionRef.current?.messageID,
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
    reflowSubmittedMotionRef.current = undefined;
    if (smoothAutoFollowRef.current) suppressAutoFollowRearmRef.current = false;
    smoothAutoFollowRef.current = false;
    if (submittedScrollFrameRef.current !== undefined) {
      window.cancelAnimationFrame(submittedScrollFrameRef.current);
      submittedScrollFrameRef.current = undefined;
    }
    if (scrollModeRef.current === "placing") scrollModeRef.current = "holding";
  }, []);

  const markUserScrollAwayIntent = useCallback((startTop?: number): void => {
    deferredSubmissionRef.current.clear();
    cancelSubmittedQueryScroll();
    if (submissionPhase()) setAutoFollow(false);
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
    const nextAutoFollow = autoFollow;
    if (!submissionPhase()) setAutoFollow(nextAutoFollow);
    setAutoFollowOverflowAnchor(node, nextAutoFollow || Boolean(submissionPhase()));
    rememberActiveThreadScrollSnapshot(node, nextAutoFollow);
    if (moved && options.revealScrollbar) {
      showConversationScrollbar(node);
    }
  }

  const adoptingSubmission = Boolean(submissionRef.current && !submissionRef.current.threadID && activeThreadID &&
    primaryTurns?.some(turn => turn.items.some(item => item.id === submissionRef.current?.messageID)));
  const submittedMessage = useCallback(() => {
    const viewport = conversationViewport();
    return Array.from(viewport?.querySelectorAll<HTMLElement>("[data-user-message-id]") ?? [])
      .find(node => node.dataset.userMessageId === submissionRef.current?.messageID && !node.closest('[aria-hidden="true"]'));
  }, [activePane, splitConversation]);
  const reconcileSubmittedArrival = useCallback(() => {
    const message = splitConversation ? undefined : submittedMessage();
    const element = message?.querySelector<HTMLElement>("[data-message-arrival]");
    reconcileArrivals(element && submissionRef.current
      ? [{ id: submissionRef.current.messageID, element, own: true, fresh: submissionRef.current.animate }]
      : []);
  }, [reconcileArrivals, splitConversation, submittedMessage]);
  const { reserve: reserveTailSpace, ensureRange: ensureTailRange, filled: tailFilled, consume: consumeTailSpace, syncLayout: syncTailLayout, discard: discardTailSpace } = useSessionTailSpace({
    threadID: activeThreadID,
    enabled: initialized && !emptyConversation && !splitConversation,
    preserveOnThreadChange: adoptingSubmission,
    paneRef: conversationPaneRef,
    viewportRef: conversationScrollRef,
    contentRef: scrollContentRef,
  });

  const scrollConversationToBottom = useCallback((): void => {
    if (previousThreadRef.current !== activeThreadID) return;
    // Active placement owns the screen-space trajectory. Once it has settled,
    // preserve the held scroll position if a larger viewport clamps its range.
    if (scrollModeRef.current === "placing") {
      reflowSubmittedMotionRef.current?.();
    } else if (scrollModeRef.current === "holding") {
      const ownedTop = lastConversationScrollTopRef.current;
      ensureTailRange(ownedTop);
      const viewport = conversationViewport();
      if (viewport && Math.abs(viewport.scrollTop - ownedTop) > 1) {
        // Unlike a new programmatic scroll, this must not cancel placement.
        viewport.scrollTop = ownedTop;
        programmaticScrollTopRef.current = clampScrollTop(viewport, viewport.scrollTop);
      }
    }
    // Child-only mounts must start their entrance with placement, not on a
    // later parent render after the bubble has already painted at full opacity.
    reconcileSubmittedArrival();
    syncTailLayout();
    // Cached/windowed turns can mount in a child-only commit. The parent
    // layout effect is not guaranteed to run when the exact bubble appears.
    if (scrollModeRef.current === "pending") positionSubmittedMessageRef.current?.(true);
    const node = conversationViewport();
    if (node && scrollModeRef.current === "holding" &&
      tailFilled(submittedMessage()?.getBoundingClientRect().height ?? 0)) {
      setAutoFollow(true);
    }
    if (!node || !isFollowing()) {
      return;
    }
    // The submit animation reads the live bottom itself. A native smooth
    // scroll restarted on every collapsing-card resize never gets up to speed.
    if (smoothAutoFollowRef.current) return;
    // Content/layout following is not a user scroll. In particular, keyboard
    // animation must not repeatedly reveal the scrollbar as the viewport shrinks.
    applyProgrammaticScroll(
      node,
      latestFollowScrollTop(node, sessionTailSpacePx(conversationPaneRef.current ?? node)),
      true,
    );
  }, [
    activePane,
    activeThreadID,
    clearUserScrollAwayIntent,
    setAutoFollow,
    splitConversation,
    syncTailLayout,
    tailFilled,
    submittedMessage,
    ensureTailRange,
    reconcileSubmittedArrival,
  ]);

  // A draft can be remounted into a real thread during the same animation.
  // Frame callbacks keep their deadline but must use the current scope's
  // layout/snapshot callbacks, not closures belonging to the outgoing draft.
  const submissionFrameCallbacks = useRef({ ensureTailRange, scrollConversationToBottom, rememberActiveThreadScrollSnapshot });
  useLayoutEffect(() => {
    submissionFrameCallbacks.current = { ensureTailRange, scrollConversationToBottom, rememberActiveThreadScrollSnapshot };
  });

  const acknowledgeSubmittedMessage = useCallback((pendingID: string, messageID: string) => {
    for (const snapshot of threadScrollSnapshotsRef.current.values()) {
      if (snapshot.submittedMessageID === pendingID) snapshot.submittedMessageID = messageID;
    }
    if (submissionRef.current?.messageID !== pendingID) return;
    acknowledgeArrival(pendingID, messageID);
    submissionRef.current.messageID = messageID;
  }, [acknowledgeArrival]);

  const discardSubmittedMessage = useCallback((messageID: string) => {
    deferredSubmissionRef.current.delete(messageID);
    for (const [threadID, snapshot] of threadScrollSnapshotsRef.current) {
      if (snapshot.submittedMessageID !== messageID) continue;
      snapshot.submissionPhase = undefined;
      snapshot.submittedMessageID = undefined;
      discardTailSpace(threadID);
    }
    if (submissionRef.current?.messageID !== messageID) return;
    discardTailSpace(submissionRef.current.threadID);
    cancelSubmittedQueryScroll();
    cancelArrivals();
    submissionRef.current = undefined;
    setAutoFollow(false);
    const node = conversationViewport();
    if (node) {
      setAutoFollowOverflowAnchor(node, false);
      rememberActiveThreadScrollSnapshot(node, false);
    }
  }, [activePane, activeThreadID, splitConversation, discardTailSpace, cancelSubmittedQueryScroll, cancelArrivals, setAutoFollow]);

  const positionSubmittedMessage = useCallback((animate = false): boolean => {
    if (splitConversation || scrollModeRef.current !== "pending") return false;
    let viewport = conversationViewport();
    if (!viewport) return false;
    const message = submittedMessage();
    if (!message) return false;
    scrollModeRef.current = "placing";
    setAutoFollowOverflowAnchor(viewport, true);
    const placement = submittedMessagePlacement(viewport, message);
    const targetTop = placement.targetTop;
    reserveTailSpace(targetTop, placement.messageHeight);
    const startTop = clampScrollTop(viewport, viewport.scrollTop);
    if (!animate || !submissionRef.current?.animate || prefersReducedMotion() || Math.abs(targetTop - startTop) <= 1) {
      viewport.scrollTop = targetTop;
      programmaticScrollTopRef.current = clampScrollTop(viewport, viewport.scrollTop);
      lastConversationScrollTopRef.current = programmaticScrollTopRef.current;
      scrollModeRef.current = "holding";
      rememberActiveThreadScrollSnapshot(viewport, false);
      return true;
    }

    const duration = motionDurationMs("--query-scroll-duration", 360);
    const screenStart = placement.documentTop - startTop;
    const sample = createMessageScrollMotion(screenStart, placement.documentTop - targetTop, duration);
    let lastFrameTime: number | undefined;
    let animatedMessage = message;
    const paint = (now: number | undefined): void => {
      if (scrollModeRef.current !== "placing") return;
      viewport = conversationViewport();
      if (!viewport) {
        scrollModeRef.current = "pending";
        return;
      }
      // Animate the bubble's position in the reading viewport, not scrollTop.
      // Reflow moves its document anchor; compensate that displacement directly
      // so it cannot become a second visible movement or a delayed correction.
      if (animatedMessage.dataset.userMessageId !== submissionRef.current?.messageID || !viewport.contains(animatedMessage)) {
        const replacement = submittedMessage();
        if (!replacement) {
          scrollModeRef.current = "pending";
          return;
        }
        animatedMessage = replacement;
      }
      const live = submittedMessagePlacement(viewport, animatedMessage);
      const { position, done } = now === undefined
        ? { position: screenStart, done: false }
        : sample(now, live.documentTop - live.targetTop);
      const top = live.documentTop - position;
      submissionFrameCallbacks.current.ensureTailRange(Math.max(top, live.targetTop));
      viewport.scrollTop = top;
      programmaticScrollTopRef.current = clampScrollTop(viewport, viewport.scrollTop);
      lastConversationScrollTopRef.current = programmaticScrollTopRef.current;
      if (done) {
        scrollModeRef.current = "holding";
        reflowSubmittedMotionRef.current = undefined;
      }
      submissionFrameCallbacks.current.rememberActiveThreadScrollSnapshot(viewport, false);
    };
    reflowSubmittedMotionRef.current = () => paint(messageMotionTime() ?? lastFrameTime);
    const step = (now: number): void => {
      submittedScrollFrameRef.current = undefined;
      lastFrameTime = now;
      paint(now);
      if (scrollModeRef.current === "placing") {
        submittedScrollFrameRef.current = window.requestAnimationFrame(step);
      } else if (scrollModeRef.current === "holding") {
        submissionFrameCallbacks.current.scrollConversationToBottom();
      }
    };
    submittedScrollFrameRef.current = window.requestAnimationFrame(step);
    return true;
  }, [activePane, activeThreadID, ensureTailRange, reserveTailSpace, scrollConversationToBottom, splitConversation, submittedMessage]);

  useLayoutEffect(() => { positionSubmittedMessageRef.current = positionSubmittedMessage; });

  const requestSubmittedQueryScroll = useCallback((messageID: string, fromQueue = false): void => {
    if (!fromQueue) deferredSubmissionRef.current.clear();
    // Collaboration keeps its existing bottom-follow behavior. Ordinary
    // sessions have a different lifecycle: the submitted message is the
    // reading anchor, even when the user had previously browsed history.
    if (splitConversation && !isFollowing()) return;
    cancelSubmittedQueryScroll();
    submissionRef.current = { messageID, threadID: activeThreadID, animate: true };
    clearUserScrollAwayIntent();
    cancelBottomOverscroll(conversationViewport());
    selectionPausedAutoFollowRef.current = false;
    scrollModeRef.current = splitConversation ? "following" : "pending";
    const node = conversationViewport();
    if (!node) {
      return;
    }
    if (!splitConversation) {
      // Do not animate to the bottom first. The optimistic user message (or
      // the first committed turn) will be positioned at the reading anchor
      // below, and stream growth must not get a chance to steal that anchor.
      suppressAutoFollowRearmRef.current = false;
      setAutoFollowOverflowAnchor(node, true);
      rememberActiveThreadScrollSnapshot(node, false);
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
      if (!smoothAutoFollowRef.current || !isFollowing()) return;
      startedAt ??= now;
      const progress = duration > 0 ? Math.min(1, (now - startedAt) / duration) : 1;
      // CSS uses cubic-bezier(1/3, 1, 2/3, 1): linear time, cubic ease-out.
      const eased = 1 - (1 - progress) ** 3;
      // Share one deadline with the diff receipt's exit, even while its height
      // and the optimistic turn change. Layout signals must not restart easing.
      const targetTop = latestFollowScrollTop(
        node,
        sessionTailSpacePx(conversationPaneRef.current ?? node),
      );
      node.scrollTop = startTop + (targetTop - startTop) * eased;
      programmaticScrollTopRef.current = clampScrollTop(node, node.scrollTop);
      lastConversationScrollTopRef.current = programmaticScrollTopRef.current;
      rememberActiveThreadScrollSnapshot(node, true);
      if (progress < 1) {
        submittedScrollFrameRef.current = window.requestAnimationFrame(step);
      } else {
        applyProgrammaticScroll(node, targetTop, true, { revealScrollbar: true });
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

  const requestDeferredQueryScroll = useCallback((sourceID: string): void => {
    if (!activeThreadID || splitConversation) return;
    // Pending composer entries are not conversation bubbles. Keep the current
    // reading policy until the server publishes the matching user message.
    deferredSubmissionRef.current.add(sourceID);
  }, [activeThreadID, splitConversation]);

  useLayoutEffect(() => {
    deferredSubmissionRef.current.clear();
    if (!adoptingSubmission) cancelSubmittedQueryScroll();
  }, [activeThreadID, activePane, splitConversation, cancelSubmittedQueryScroll]);
  useLayoutEffect(() => cancelSubmittedQueryScroll, [cancelSubmittedQueryScroll]);

  useEffect(() => {
    const media = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    const reduce = () => { if (media?.matches) cancelSubmittedQueryScroll(); };
    const hide = () => { if (document.hidden) cancelSubmittedQueryScroll(); };
    media?.addEventListener("change", reduce);
    document.addEventListener("visibilitychange", hide);
    return () => { media?.removeEventListener("change", reduce); document.removeEventListener("visibilitychange", hide); };
  }, [cancelSubmittedQueryScroll]);

  const scheduleLiveResizeScroll = useCallback((): void => {
    if (
      liveResizeScrollFrameRef.current !== undefined ||
      !isFollowing()
    ) {
      return;
    }
    liveResizeScrollFrameRef.current = window.requestAnimationFrame(() => {
      liveResizeScrollFrameRef.current = undefined;
      const node = conversationViewport();
      if (
        !node ||
        !isWindowResizing() ||
        !isFollowing()
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
    if (previousThreadRef.current !== activeThreadID) return;
    syncTailLayout();
    if (!activeThreadID) {
      return;
    }
    if (!isFollowing() && !submissionPhase()) {
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
  }, [activeThreadID, scheduleLiveResizeScroll, scrollConversationToBottom, syncTailLayout]);

  useEffect(() => {
    resizeSettleStreamScrollRef.current?.cancel();
    resizeSettleStreamScrollRef.current = null;
  }, [scrollConversationToBottom]);

  const enableConversationAutoFollow = useCallback((): void => {
    deferredSubmissionRef.current.clear();
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
    deferredSubmissionRef.current.clear();
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
        autoFollow: isFollowing(),
        submissionPhase: submissionPhase(),
        submittedMessageID: submissionRef.current?.messageID,
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
      cancelSubmittedQueryScroll();
      scrollModeRef.current = snapshot.submissionPhase === "placing" ? "pending" :
        snapshot.submissionPhase ?? (snapshot.autoFollow ? "following" : "paused");
      submissionRef.current = snapshot.submittedMessageID
        ? { messageID: snapshot.submittedMessageID, threadID: activeThreadID, animate: false }
        : undefined;
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
    if (submissionPhase()) {
      // Wheel/key/touch/scrollbar and content actions release ownership before
      // their scroll event. Native anchoring, clamping and coalesced rAF events
      // must not enable bottom-follow or consume the submission's reservation.
      if (scrollModeRef.current === "holding") {
        node.scrollTop = clampScrollTop(node, lastConversationScrollTopRef.current);
      }
      programmaticScrollTopRef.current = undefined;
      rememberActiveThreadScrollSnapshot(node, false);
      return;
    }
    if (isWindowResizing()) {
      if (isFollowing()) {
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
          isFollowing() &&
          !userScrollAwayIntentRef.current &&
          !atLatestScrollView(node, CONVERSATION_AUTO_SCROLL_THRESHOLD_PX)
        ) {
          scrollConversationToBottom();
          return;
        }
        rememberActiveThreadScrollSnapshot(
          node,
          isFollowing()
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
    let nextAutoFollow = isFollowing();
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
      isFollowing() &&
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
      nextAutoFollow = isFollowing();
    } else if (atLatestView) {
      suppressAutoFollowRearmRef.current = false;
      if (
        isFollowing() ||
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
    } else if (isFollowing() && !userScrollAwayIntent) {
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
    if ((scrolledUp || scrolledDown) && !layoutClamp && !nextAutoFollow && !splitConversation) {
      deferredSubmissionRef.current.clear();
      consumeTailSpace(Math.abs(previousScrollTop - node.scrollTop));
    }
    rememberActiveThreadScrollSnapshot(node, nextAutoFollow);
  }

  useLayoutEffect(() => {
    const node = conversationViewport();
    const threadChanged = previousThreadRef.current !== activeThreadID;
    previousThreadRef.current = activeThreadID;
    if (threadChanged && !adoptingSubmission) {
      submissionRef.current = undefined;
      setAutoFollow(true);
    }
    if (!activeThreadID || !node) {
      programmaticScrollTopRef.current = undefined;
      lastConversationScrollTopRef.current = 0;
      if (!submissionPhase()) setAutoFollow(true);
      return undefined;
    }

    if (adoptingSubmission && submissionRef.current) {
      submissionRef.current.threadID = activeThreadID;
      // The draft bubble moves into the thread pane without a bottom jump,
      // even if this short first turn has no scroll range yet.
      setAutoFollowOverflowAnchor(node, Boolean(submissionPhase()));
      programmaticScrollTopRef.current = clampScrollTop(node, node.scrollTop);
      lastConversationScrollTopRef.current = programmaticScrollTopRef.current;
      rememberActiveThreadScrollSnapshot(node, isFollowing());
      return;
    }
    markSessionSwitch(activeThreadID, "scroll-restore-start");
    const snapshot = threadScrollSnapshotsRef.current.get(activeThreadID);
    scrollModeRef.current = snapshot?.submissionPhase === "placing" ? "pending" :
      snapshot?.submissionPhase ?? (snapshot?.autoFollow === false ? "paused" : "following");
    if (snapshot?.submittedMessageID) {
      submissionRef.current = { messageID: snapshot.submittedMessageID, threadID: activeThreadID, animate: false };
    }
    if (snapshot && !snapshot.autoFollow) {
      applyProgrammaticScroll(node, snapshot.scrollTop, false);
      bottomOverscrollFromAwayRef.current = true;
      setNativeBottomOverscrollEnabled(node, true);
    } else {
      applyProgrammaticScroll(
        node,
        latestFollowScrollTop(node, sessionTailSpacePx(conversationPaneRef.current ?? node)),
        true,
      );
      setNativeBottomOverscrollEnabled(node, false);
    }
    markSessionSwitch(activeThreadID, "scroll-restore-end");
    return undefined;
  }, [activePane, activeThreadID, setAutoFollow, splitConversation]);

  // Runs after restoration and every commit, including deferred queue/steer
  // materialization. Never infer ownership from an arbitrary arriving message.
  useLayoutEffect(() => {
    const deferred = deferredSubmissionRef.current;
    let latestMessageID: string | undefined;
    if (deferred.size) {
      for (const turn of primaryTurns ?? []) {
        for (const item of turn.items) {
          if (item.type === "user_message" && item.source_id && deferred.delete(item.source_id)) {
            latestMessageID = item.id;
          }
        }
      }
    }
    // A batched server update has one visible destination; later queued inputs
    // retain their own intent until they materialize or the reader takes over.
    if (latestMessageID) requestSubmittedQueryScroll(latestMessageID, true);
    positionSubmittedMessage(true);
  });

  useLayoutEffect(reconcileSubmittedArrival);

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
      if (event.deltaY !== 0) deferredSubmissionRef.current.clear();
      if (event.deltaY !== 0 && (submittedScrollFrameRef.current !== undefined || submissionPhase())) disableConversationAutoFollow();
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
      if (submittedScrollFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
      cancelArrivals();
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
        // A scrollbar gesture owns the viewport before native scroll delivery.
        disableConversationAutoFollow();
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
      if (SCROLL_TOWARD_LATEST_KEYS.has(event.key)) deferredSubmissionRef.current.clear();
      if (SCROLL_TOWARD_LATEST_KEYS.has(event.key) || ((event.key === "Enter" || event.key === " ") &&
        event.target instanceof Element && event.target.closest('button, [role="button"], summary'))) {
        if (submittedScrollFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
      }
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        return;
      }
      if (SCROLL_AWAY_KEYS.has(event.key)) {
        if (nativeScrollBounce) {
          bottomOverscrollFromAwayRef.current = true;
          setNativeBottomOverscrollEnabled(node, true);
        }
        markUserScrollAwayIntent(clampScrollTop(node, node.scrollTop));
        disableConversationAutoFollow();
      } else if (SCROLL_TOWARD_LATEST_KEYS.has(event.key)) {
        selectionPausedAutoFollowRef.current = false;
      }
    };
    const handleTouchStart = (event: TouchEvent): void => {
      deferredSubmissionRef.current.clear();
      if (submittedScrollFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
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
        disableConversationAutoFollow();
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
    // Inspecting a submitted attachment interrupts its placement. Ordinary
    // content actions do not change reading ownership: a fold expanded at
    // the bottom must keep following, while an away viewport stays paused.
    const handleContentAction = (event: MouseEvent): void => {
      if (!(event.target instanceof Element) || !event.target.closest('button, [role="button"], summary, a, input, textarea, select, video, audio')) return;
      deferredSubmissionRef.current.clear();
      if (submittedScrollFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
      cancelArrivals();
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
    node.addEventListener("click", handleContentAction, true);
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
      node.removeEventListener("click", handleContentAction, true);
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
    const pane = conversationPaneRef.current;
    if (!pane) return;
    let frame = 0;
    const update = (): void => {
      const height = statusClusterNode?.getBoundingClientRect().height ?? 0;
      const gap = statusClusterNode
        ? cssPixelValue(window.getComputedStyle(statusClusterNode).getPropertyValue("--conversation-status-gap"))
        : 0;
      const value = `${height > 0 ? Math.ceil(height + gap) : 0}px`;
      if (pane.style.getPropertyValue("--conversation-status-space") === value) return;
      pane.style.setProperty("--conversation-status-space", value);
      scrollConversationToBottom();
    };
    update();
    if (!statusClusterNode || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(update);
    });
    observer.observe(statusClusterNode);
    return () => {
      observer.disconnect();
      window.cancelAnimationFrame(frame);
    };
  }, [statusClusterNode, scrollConversationToBottom]);

  useLayoutEffect(() => {
    const node = dockComposerNode;
    const pane = conversationPaneRef.current;
    let windowResizeHeight: ReturnType<
      typeof createWindowResizeSettleScheduler
    > | undefined;
    const applyHeight = (nextHeight: number, inputInset = nextHeight): void => {
      const nextValue = `${nextHeight}px`;
      const insetValue = `${inputInset}px`;
      if (
        dockComposerHeightRef.current === nextHeight &&
        pane?.style.getPropertyValue("--dock-composer-height") === nextValue &&
        pane?.style.getPropertyValue("--conversation-input-inset") === insetValue
      ) {
        return;
      }
      dockComposerHeightRef.current = nextHeight;
      pane?.style.setProperty("--dock-composer-height", nextValue);
      pane?.style.setProperty("--conversation-input-inset", insetValue);
      // The input edge changes the readable viewport. Settle its owned range
      // in the same frame, before queue removal can clamp a held submission.
      // The scroll policy leaves deliberate reading pauses untouched.
      scrollConversationToBottom();
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
      const input = node.querySelector<HTMLElement>(".composer-frame");
      const inputInset = input
        ? Math.max(0, Math.ceil(node.getBoundingClientRect().bottom - input.getBoundingClientRect().top))
        : nextHeight;
      applyHeight(nextHeight, inputInset);
    };

    windowResizeHeight = createWindowResizeSettleScheduler(updateHeight);
    updateHeight();
    // The height token changes ancestor layout. Applying it inside observer
    // delivery can invalidate the growing composer's own resize notifications.
    let heightFrame = 0;
    const resizeObserver = new ResizeObserver(() => {
      window.cancelAnimationFrame(heightFrame);
      heightFrame = window.requestAnimationFrame(updateHeight);
    });
    resizeObserver.observe(node);
    const frame = node.querySelector<HTMLElement>(".composer-frame");
    if (frame) {
      resizeObserver.observe(frame);
    }
    return () => {
      windowResizeHeight?.cancel();
      window.cancelAnimationFrame(heightFrame);
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
    requestDeferredQueryScroll,
    acknowledgeSubmittedMessage,
    discardSubmittedMessage,
  };
}
