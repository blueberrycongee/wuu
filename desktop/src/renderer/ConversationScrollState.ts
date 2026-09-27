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
  SCROLL_AWAY_KEYS,
  SCROLL_TOWARD_LATEST_KEYS,
  USER_SCROLL_AWAY_INTENT_WINDOW_MS,
  atLatestScrollView,
  clampScrollTop,
  distanceFromLatestContent,
  eventTargetsNestedAutoFollowScroll,
  latestFollowScrollTop,
  maxScrollTop,
  observeAutoFollowResizeTargets,
  scrollTopForDistanceFromLatest,
  selectionIntersectsNode,
  setAutoFollowOverflowAnchor,
  setSubmitGlideActive,
} from "./AutoFollowScroll";
import { markScrollbarRevealSelfManaged, revealScrollbar } from "./ScrollbarReveal";
import { isWindowResizing } from "./WindowResizeState";
import { markSessionSwitch } from "./SessionSwitchPerformance";
import { messageMotionTime, motionDurationMs, motionEasing, prefersReducedMotion, subscribeReducedMotion, cubicBezier } from "./motion";
import { createScrollGlide, GLIDE_FOLLOW_HANDOFF_VIEWPORTS } from "./ScrollGlide";
import { useSessionTailSpace } from "./SessionTailSpace";
import { conversationDisclosureHeight, eventTargetsConversationDisclosure } from "./ConversationDisclosure";
import { useMessageArrivalMotion } from "./useMessageArrivalMotion";
import { captureReadingAnchor, readingAnchorScrollTop, type ConversationReadingAnchor } from "./ConversationReadingAnchor";
import { syncConversationRenderWindow } from "./ConversationRenderWindow";

// Tight threshold so the conversation only re-engages auto-follow when the
// user is effectively parked at the bottom. The previous 48px band let one
// mouse-wheel notch land inside the band and silently re-arm auto-follow,
// which made slow scroll-up get yanked back to the bottom mid-gesture.
const CONVERSATION_AUTO_SCROLL_THRESHOLD_PX = AUTO_FOLLOW_BOTTOM_THRESHOLD_PX;
const CONVERSATION_USER_SCROLL_INTENT_WINDOW_MS =
  USER_SCROLL_AWAY_INTENT_WINDOW_MS;
// Wider band for a reader moving back down toward latest. Output streamed
// while a wheel or scrollbar motion settles moves the bottom after that
// motion's destination was fixed, so an explicit return could land a few lines
// short and stay paused under a growing reply. Upward movement never re-arms.
const CONVERSATION_RETURN_TO_LATEST_PX = 96;
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
/** Hides the submitted turn's status on the content wrapper while its bubble
 * is being placed. The status is not part of what the user sent, so it enters
 * once the bubble arrives instead of travelling with it. */
const SUBMIT_PLACING_ATTR = "data-submit-placing";
/** Remaining placement distance at which the status enters. Below this the
 * glide only creeps, so the entrance overlaps the bubble settling. */
const SUBMIT_STATUS_REVEAL_PX = 8;

function clampFraction(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

function restoredScrollTop(
  node: HTMLElement,
  snapshot: { scrollTop: number; distanceFromLatest?: number; submittedMessageID?: string; readingAnchor?: ConversationReadingAnchor },
  submissionOffset = 0,
): number {
  if (snapshot.readingAnchor) {
    // A paused reader's place is content-relative. A hidden stream may grow
    // below the reader, history may grow above it, and a settled answer may
    // rewrite what lies between the reader and an older submission; only the
    // anchor's actual movement changes the restored offset.
    const anchored = readingAnchorScrollTop(node, snapshot.readingAnchor);
    if (anchored !== undefined) return anchored;
  }
  // A placed submission owns the reading frame: its tail reservation is rebased
  // to the incoming history window by the message's movement, so the saved
  // offset lives in that coordinate system. Distance-from-latest only serves
  // frozen snapshots.
  if (snapshot.submittedMessageID !== undefined) return snapshot.scrollTop + submissionOffset;
  if (snapshot.readingAnchor) return snapshot.scrollTop;
  return snapshot.distanceFromLatest === undefined
    ? snapshot.scrollTop
    : scrollTopForDistanceFromLatest(node, snapshot.distanceFromLatest);
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

type SubmitGlideAnchor = {
  documentTop: number;
  /** Screen position the glide approaches: documentTop minus the destination scroll. */
  targetScreen: number;
  viewportHeight: number;
  /** Latest-content scroll captured with this anchor, so later frames do not remeasure. */
  followTop: number;
};

function submitGlideAnchor(viewport: HTMLElement, message: HTMLElement): SubmitGlideAnchor {
  const live = submittedMessagePlacement(viewport, message);
  return {
    documentTop: live.documentTop,
    targetScreen: live.documentTop - live.targetTop,
    viewportHeight: viewport.clientHeight,
    followTop: latestFollowScrollTop(viewport),
  };
}

function submittedGroupHeight(message: HTMLElement): number {
  const messageRect = message.getBoundingClientRect();
  let bottom = messageRect.bottom;
  const turn = message.closest(".turn") ?? message.parentElement;
  if (turn) {
    for (const child of Array.from(turn.children)) {
      if (!(child instanceof HTMLElement) || child === message || message.contains(child)) continue;
      if (child.classList.contains("assistant-turn-shell") || child.hasAttribute("data-submitted-motion")) {
        const rect = child.getBoundingClientRect();
        if (Number.isFinite(rect.bottom)) bottom = Math.max(bottom, rect.bottom);
      }
    }
  }
  return Math.max(0, bottom - messageRect.top);
}

/**
 * Where a newly submitted turn would sit if it were still parked on the
 * composer. The first turn has no scroll range, so a lead spacer parks the
 * whole turn — bubble and in-progress timer — there, then the same glide
 * consumes that spacer. Do not add a second transform on the bubble.
 */
function submittedComposerScreenTop(
  viewport: HTMLElement,
  message: HTMLElement,
  composer: HTMLElement | null,
): number {
  const groupHeight = submittedGroupHeight(message);
  if (composer) {
    const viewportTop = viewport.getBoundingClientRect().top;
    const composerTop = composer.getBoundingClientRect().top;
    if (Number.isFinite(viewportTop) && Number.isFinite(composerTop)) {
      return composerTop - viewportTop - groupHeight;
    }
  }
  return viewport.clientHeight - groupHeight;
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
  distanceFromLatest: number;
  autoFollow: boolean;
  submissionPhase?: SubmissionScrollPhase;
  submittedMessageID?: string;
  readingAnchor?: ConversationReadingAnchor;
};

type ThreadScrollSnapshot = ConversationScrollSnapshot & { submittedMessageTop?: number };

export function useConversationScrollState({
  activeThreadID,
  activePane,
  splitConversation,
  primaryTurns,
  secondaryTurns,
  emptyConversation,
  initialized,
  running = false,
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
  statusClusterRef: (node: HTMLDivElement | null) => void;
  scheduleStreamScroll: () => void;
  handleConversationScroll: (scrolledNode?: HTMLElement) => void;
  enableConversationAutoFollow: () => void;
  /** Glide to the latest content and keep following it. */
  jumpToLatest: () => void;
  /** Position this submission once its optimistic bubble has mounted. */
  requestSubmittedQueryScroll: (messageID: string) => void;
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
  const submissionRef = useRef<{
    messageID: string;
    threadID?: string;
    animate: boolean;
    documentTop?: number;
    /** The thread's turns have contained this message ID since it was assigned. */
    inThread?: boolean;
  } | undefined>(undefined);
  // Exactly one owner can write scrollTop. Geometry alone cannot transfer
  // ownership: a submission's padded bottom is not the bottom of its output.
  const scrollModeRef = useRef<ConversationScrollMode>("following");
  const runningRef = useRef(running);
  runningRef.current = running;
  const [statusClusterNode, setStatusClusterNode] = useState<HTMLDivElement | null>(null);
  const statusClusterNodeRef = useRef<HTMLDivElement | null>(null);
  const statusClusterRef = useCallback((node: HTMLDivElement | null) => {
    // Ref attachment precedes layout effects; state alone still describes the
    // outgoing row when the incoming conversation restores its reading position.
    statusClusterNodeRef.current = node;
    setStatusClusterNode(node);
  }, []);
  function syncStreamFollowing(): void {
    // Scroll-linked fades and the live text wave repaint on every chunk
    // while the viewport is pinned to a running turn. The attribute lets
    // CSS drop those layers without a React render.
    const degrade = runningRef.current && scrollModeRef.current !== "paused";
    const root = document.documentElement;
    if (root.hasAttribute("data-stream-following") === degrade) return;
    if (degrade) root.setAttribute("data-stream-following", "");
    else root.removeAttribute("data-stream-following");
  }
  function writeScrollMode(mode: ConversationScrollMode): void {
    scrollModeRef.current = mode;
    syncStreamFollowing();
    // Any exit from placing (landing, cancellation, a missing pane) must
    // leave the status visible.
    scrollContentRef.current?.toggleAttribute(SUBMIT_PLACING_ATTR, mode === "placing");
    // Readers of the conversation scrollport skip geometry work for the
    // duration and catch up when this clears.
    setSubmitGlideActive(mode === "placing");
  }
  useEffect(() => {
    syncStreamFollowing();
    return () => {
      document.documentElement.removeAttribute("data-stream-following");
    };
  }, [running]);
  function isFollowing(): boolean { return scrollModeRef.current === "following"; }
  function submissionPhase(): SubmissionScrollPhase | undefined {
    const mode = scrollModeRef.current;
    return mode === "following" || mode === "paused" ? undefined : mode;
  }
  const { reconcile: reconcileArrivals, acknowledge: acknowledgeArrival, cancel: cancelArrivals } = useMessageArrivalMotion();
  const previousThreadRef = useRef(activeThreadID);
  // Layout from the pane swap clamps scrollTop and fires scroll before this
  // hook can restore the incoming snapshot. Handling that event would save
  // the clamp as the thread's reading position.
  const restoreScrollLockRef = useRef(false);
  if (previousThreadRef.current !== activeThreadID) {
    restoreScrollLockRef.current = true;
  }
  const [dockComposerNode, setDockComposerNode] = useState<HTMLElement | null>(null);
  const dockComposerRef = useCallback((node: HTMLElement | null) => {
    setDockComposerNode(node);
  }, []);
  const dockComposerHeightRef = useRef(0);
  const syncDockComposerGeometry = useCallback((): void => {
    const pane = conversationPaneRef.current;
    const nextHeight = dockComposerNode ? dockComposerVisualHeight(dockComposerNode) : 0;
    const input = dockComposerNode?.querySelector<HTMLElement>(".composer-frame");
    const inputInset = input && dockComposerNode
      ? Math.max(0, Math.ceil(dockComposerNode.getBoundingClientRect().bottom - input.getBoundingClientRect().top))
      : nextHeight;
    const heightValue = `${nextHeight}px`;
    const insetValue = `${inputInset}px`;
    if (dockComposerHeightRef.current === nextHeight &&
      pane?.style.getPropertyValue("--dock-composer-height") === heightValue &&
      pane?.style.getPropertyValue("--conversation-input-inset") === insetValue) return;
    dockComposerHeightRef.current = nextHeight;
    pane?.style.setProperty("--dock-composer-height", heightValue);
    pane?.style.setProperty("--conversation-input-inset", insetValue);
  }, [dockComposerNode]);
  /** Returns whether the reserved status-row space changed. */
  const syncConversationStatusSpace = useCallback((): boolean => {
    const pane = conversationPaneRef.current;
    const cluster = statusClusterNodeRef.current;
    if (!pane) return false;
    const height = cluster?.getBoundingClientRect().height ?? 0;
    const gap = cluster
      ? cssPixelValue(window.getComputedStyle(cluster).getPropertyValue("--conversation-status-gap"))
      : 0;
    const value = `${height > 0 ? Math.ceil(height + gap) : 0}px`;
    if (pane.style.getPropertyValue("--conversation-status-space") === value) return false;
    pane.style.setProperty("--conversation-status-space", value);
    return true;
  }, []);
  const setAutoFollow = useCallback((next: boolean): void => {
    // A later ownership change supersedes a pending gesture's restoration.
    if (pointerScrollGestureRef.current) {
      pointerScrollGestureRef.current.resumeScrollTop = undefined;
      pointerScrollGestureRef.current.followOnRelease = false;
    }
    writeScrollMode(next ? "following" : "paused");
  }, []);
  const lastConversationScrollTopRef = useRef(0);
  const lastDisclosureHeightRef = useRef(0);
  const programmaticScrollTopRef = useRef<number | undefined>(undefined);
  const suppressAutoFollowRearmRef = useRef(false);
  // A follow that animates toward the live bottom (jump to latest, the split
  // submit) re-reads its target each frame, so instant follow writes defer
  // to it until it lands.
  const followMotionRef = useRef(false);
  /** Frame of whichever programmatic motion owns the viewport; input cancels it. */
  const motionFrameRef = useRef<number | undefined>(undefined);
  const reflowSubmittedMotionRef = useRef<(() => void) | undefined>(undefined);
  const leadSpaceRef = useRef(0);
  const positionSubmittedMessageRef = useRef<((animate: boolean) => boolean) | undefined>(undefined);
  const selectionPausedAutoFollowRef = useRef(false);
  const pointerScrollGestureRef = useRef<
    {
      node: HTMLElement;
      scrollTop: number;
      scrollHeight: number;
      resumeScrollTop?: number;
      followOnRelease?: boolean;
    } | undefined
  >(undefined);
  const userScrollIntentRef = useRef<"away" | "latest" | undefined>(undefined);
  const userScrollIntentTimerRef = useRef<number | undefined>(undefined);
  const userScrollAwayStartTopRef = useRef<number | undefined>(undefined);
  const touchLastYRef = useRef<number | undefined>(undefined);
  const threadScrollSnapshotsRef = useRef(
    new Map<string, ThreadScrollSnapshot>()
  );
  const streamScrollFrameRef = useRef<number | undefined>(undefined);
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
    const node = splitConversation
      ? splitPaneRefs.current[activePane] ?? undefined
      : conversationScrollRef.current ?? undefined;
    if (node) {
      // The controller owns this node's reveal — it can tell a layout clamp
      // from content movement, which the global scroll listener cannot — so
      // the listener must leave the node alone.
      markScrollbarRevealSelfManaged(node);
    }
    return node;
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
    // Empty and workspace panes have no conversation to scroll; their gutter is
    // layout compensation, not overflow.
    if (
      node.classList.contains("empty-scroll-region") ||
      node.classList.contains("workspace-scroll-region")
    ) {
      return;
    }
    revealScrollbar(node);
  }

  function rememberThreadScrollSnapshot(
    threadID: string,
    node: HTMLElement,
    autoFollow: boolean,
    scrollTop?: number,
    distanceFromLatest?: number,
  ): void {
    const submission = submissionRef.current;
    // Animation frames already measured the anchor while placing the bubble.
    // Refresh it for ordinary scrolling without adding geometry reads per frame.
    if (submission && !autoFollow && scrollTop === undefined) {
      const message = submittedMessage();
      if (message) submission.documentTop = submittedMessagePlacement(node, message).documentTop;
    }
    const nextTop = scrollTop ?? clampScrollTop(node, node.scrollTop);
    threadScrollSnapshotsRef.current.set(threadID, {
      // Callers already scrolling inside a frame pass the offset they reached,
      // so this never re-measures the scroller while motion is in flight.
      scrollTop: nextTop,
      distanceFromLatest: distanceFromLatest ?? Math.max(0, latestFollowScrollTop(node) - nextTop),
      readingAnchor: !autoFollow && !submissionPhase() ? captureReadingAnchor(node) : undefined,
      autoFollow,
      submissionPhase: submissionPhase(),
      submittedMessageID: submissionRef.current?.messageID,
      submittedMessageTop: submission?.documentTop,
    });
  }

  function rememberActiveThreadScrollSnapshot(
    node: HTMLElement,
    autoFollow: boolean,
    scrollTop?: number,
    distanceFromLatest?: number,
  ): void {
    if (!activeThreadID) {
      return;
    }
    rememberThreadScrollSnapshot(activeThreadID, node, autoFollow, scrollTop, distanceFromLatest);
  }

  const clearUserScrollIntent = useCallback((): void => {
    userScrollIntentRef.current = undefined;
    userScrollAwayStartTopRef.current = undefined;
    touchLastYRef.current = undefined;
    if (userScrollIntentTimerRef.current !== undefined) {
      window.clearTimeout(userScrollIntentTimerRef.current);
      userScrollIntentTimerRef.current = undefined;
    }
  }, []);

  const applyLeadSpace = useCallback((px: number): void => {
    const next = px <= 1 ? 0 : px;
    leadSpaceRef.current = next;
    const content = scrollContentRef.current;
    if (!content) return;
    // This changes every animation frame. An inherited variable on the pane
    // invalidates the composer and every cached turn as well as the spacer.
    // Keep the lead in flow, but change only the content wrapper's box.
    const value = next === 0 ? "" : `${next}px`;
    if (content.style.paddingTop !== value) content.style.paddingTop = value;
  }, []);

  const cancelScrollMotion = useCallback((): void => {
    reflowSubmittedMotionRef.current = undefined;
    applyLeadSpace(0);
    if (followMotionRef.current) suppressAutoFollowRearmRef.current = false;
    followMotionRef.current = false;
    if (motionFrameRef.current !== undefined) {
      window.cancelAnimationFrame(motionFrameRef.current);
      motionFrameRef.current = undefined;
    }
    if (scrollModeRef.current === "placing") writeScrollMode("holding");
  }, [applyLeadSpace]);

  const markUserScrollIntent = useCallback((direction: "away" | "latest", startTop?: number): void => {
    cancelScrollMotion();
    if (submissionPhase()) setAutoFollow(false);
    userScrollIntentRef.current = direction;
    userScrollAwayStartTopRef.current = direction === "away" ? startTop : undefined;
    if (userScrollIntentTimerRef.current !== undefined) {
      window.clearTimeout(userScrollIntentTimerRef.current);
    }
    userScrollIntentTimerRef.current = window.setTimeout(() => {
      userScrollIntentRef.current = undefined;
      userScrollAwayStartTopRef.current = undefined;
      userScrollIntentTimerRef.current = undefined;
    }, CONVERSATION_USER_SCROLL_INTENT_WINDOW_MS);
  }, [cancelScrollMotion]);

  function applyProgrammaticScroll(
    node: HTMLElement,
    top: number,
    autoFollow: boolean,
    options: { revealScrollbar?: boolean } = {}
  ): void {
    pointerScrollGestureRef.current = undefined;
    cancelScrollMotion();
    clearUserScrollIntent();
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
    // Callers run inside frames that paint before this write's scroll event.
    if (moved) syncConversationRenderWindow(node);
    programmaticScrollTopRef.current = actualTop;
    lastConversationScrollTopRef.current = actualTop;
    lastDisclosureHeightRef.current = conversationDisclosureHeight(node);
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
  const submittedMessage = useCallback((messageID = submissionRef.current?.messageID) => {
    const viewport = conversationViewport();
    return Array.from(viewport?.querySelectorAll<HTMLElement>("[data-user-message-id]") ?? [])
      .find(node => node.dataset.userMessageId === messageID && !node.closest('[aria-hidden="true"]'));
  }, [activePane, splitConversation]);
  const reconcileSubmittedArrival = useCallback(() => {
    const message = splitConversation ? undefined : submittedMessage();
    const element = message?.querySelector<HTMLElement>("[data-message-arrival]");
    reconcileArrivals(element && submissionRef.current
      ? [{
        id: submissionRef.current.messageID,
        element,
        own: true,
        // A first-turn lead spacer is the entrance. An opacity fade on the
        // same bubble is a second motion and reads as the two fighting.
        fresh: submissionRef.current.animate && leadSpaceRef.current <= 1,
      }]
      : []);
  }, [reconcileArrivals, splitConversation, submittedMessage]);
  const getRestorationOffset = useCallback(() => {
    const snapshot = activeThreadID ? threadScrollSnapshotsRef.current.get(activeThreadID) : undefined;
    const node = conversationViewport();
    const message = snapshot?.submittedMessageID ? submittedMessage(snapshot.submittedMessageID) : undefined;
    if (!node || !message || snapshot?.submittedMessageTop === undefined) return 0;
    return submittedMessagePlacement(node, message).documentTop - snapshot.submittedMessageTop;
  }, [activeThreadID, activePane, splitConversation, submittedMessage]);
  const { reserve: reserveTailSpace, ensureRange: ensureTailRange, filled: tailFilled, consume: consumeTailSpace, syncLayout: syncTailLayout, discard: discardTailSpace, restoredOffset } = useSessionTailSpace({
    threadID: activeThreadID,
    enabled: initialized && !emptyConversation && !splitConversation,
    preserveOnThreadChange: adoptingSubmission,
    viewportRef: conversationScrollRef,
    contentRef: scrollContentRef,
    getRestorationOffset,
  });

  const scrollConversationToBottom = useCallback((): void => {
    if (previousThreadRef.current !== activeThreadID) return;
    const node = conversationViewport();
    const previousMax = node ? maxScrollTop(node) : 0;
    const restorePausedTop = node && scrollModeRef.current === "paused" &&
      !userScrollIntentRef.current && !pointerScrollGestureRef.current && !selectionPausedAutoFollowRef.current &&
      lastConversationScrollTopRef.current > previousMax && node.scrollTop >= previousMax - 1
      ? lastConversationScrollTopRef.current : undefined;
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
    if (node && restorePausedTop !== undefined && maxScrollTop(node) > previousMax) {
      // A closing disclosure can clamp against the zero-gap layout before the
      // reservation is restored. Repair it whether scroll or resize arrives first.
      node.scrollTop = clampScrollTop(node, restorePausedTop);
      programmaticScrollTopRef.current = node.scrollTop;
    }
    // Cached/windowed turns can mount in a child-only commit. The parent
    // layout effect is not guaranteed to run when the exact bubble appears.
    if (scrollModeRef.current === "pending") positionSubmittedMessageRef.current?.(true);
    if (node && scrollModeRef.current === "holding" &&
      tailFilled(submittedMessage()?.getBoundingClientRect().height ?? 0)) {
      setAutoFollow(true);
    }
    if (!node || !isFollowing()) {
      return;
    }
    // The submit animation reads the live bottom itself. A native smooth
    // scroll restarted on every collapsing-card resize never gets up to speed.
    if (followMotionRef.current) return;
    // Content/layout following is not a user scroll. In particular, keyboard
    // animation must not repeatedly reveal the scrollbar as the viewport shrinks.
    applyProgrammaticScroll(
      node,
      latestFollowScrollTop(node),
      true,
    );
  }, [
    activePane,
    activeThreadID,
    clearUserScrollIntent,
    setAutoFollow,
    splitConversation,
    syncTailLayout,
    tailFilled,
    submittedMessage,
    ensureTailRange,
    reconcileSubmittedArrival,
  ]);

  const pinConversationDuringWindowResize = useCallback((): void => {
    if (previousThreadRef.current !== activeThreadID) {
      return;
    }
    // Placement and the submit glide still own the viewport. A plain follow
    // only needs the new bottom; measuring every disclosure forces layout
    // again on each live resize frame.
    if (
      scrollModeRef.current === "placing" ||
      scrollModeRef.current === "holding" ||
      followMotionRef.current
    ) {
      scrollConversationToBottom();
      return;
    }
    const node = conversationViewport();
    if (!node || !isFollowing()) {
      return;
    }
    const top = latestFollowScrollTop(node);
    if (node.scrollTop !== top) {
      node.scrollTop = top;
    }
    programmaticScrollTopRef.current = node.scrollTop;
    lastConversationScrollTopRef.current = node.scrollTop;
    rememberActiveThreadScrollSnapshot(node, true, node.scrollTop, 0);
  }, [activePane, activeThreadID, scrollConversationToBottom, splitConversation]);

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
    submissionRef.current.inThread = false;
  }, [acknowledgeArrival]);

  const discardSubmittedMessage = useCallback((messageID: string) => {
    for (const [threadID, snapshot] of threadScrollSnapshotsRef.current) {
      if (snapshot.submittedMessageID !== messageID) continue;
      snapshot.submissionPhase = undefined;
      snapshot.submittedMessageID = undefined;
      discardTailSpace(threadID);
    }
    if (submissionRef.current?.messageID !== messageID) return;
    discardTailSpace(submissionRef.current.threadID);
    cancelScrollMotion();
    cancelArrivals();
    submissionRef.current = undefined;
    setAutoFollow(false);
    const node = conversationViewport();
    if (node) {
      setAutoFollowOverflowAnchor(node, false);
      rememberActiveThreadScrollSnapshot(node, false);
    }
  }, [activePane, activeThreadID, splitConversation, discardTailSpace, cancelScrollMotion, cancelArrivals, setAutoFollow]);

  const positionSubmittedMessage = useCallback((animate = false): boolean => {
    if (splitConversation || scrollModeRef.current !== "pending") return false;
    const viewport = conversationViewport();
    if (!viewport) return false;
    const message = submittedMessage();
    if (!message) return false;
    writeScrollMode("placing");
    setAutoFollowOverflowAnchor(viewport, true);
    const placement = submittedMessagePlacement(viewport, message);
    if (submissionRef.current) submissionRef.current.documentTop = placement.documentTop;
    const targetTop = placement.targetTop;
    reserveTailSpace(targetTop, placement.messageHeight);
    const startTop = clampScrollTop(viewport, viewport.scrollTop);
    const composerStart = submittedComposerScreenTop(viewport, message, dockComposerNode);
    const canScroll = Math.abs(targetTop - startTop) > 1;
    const lift = composerStart - placement.screenTop;
    const canLift = !canScroll && lift > 1;
    if (!animate || !submissionRef.current?.animate || prefersReducedMotion() || (!canScroll && !canLift)) {
      applyLeadSpace(0);
      viewport.scrollTop = targetTop;
      programmaticScrollTopRef.current = clampScrollTop(viewport, viewport.scrollTop);
      lastConversationScrollTopRef.current = programmaticScrollTopRef.current;
      writeScrollMode("holding");
      rememberActiveThreadScrollSnapshot(viewport, false);
      return true;
    }

    const glide = createScrollGlide();
    let lastFrameTime: number | undefined;
    const finishHold = (viewport: HTMLElement, placed: number): void => {
      writeScrollMode("holding");
      reflowSubmittedMotionRef.current = undefined;
      submissionFrameCallbacks.current.rememberActiveThreadScrollSnapshot(viewport, false, placed);
    };
    const revealStatusNear = (remaining: number): void => {
      const content = scrollContentRef.current;
      if (remaining > SUBMIT_STATUS_REVEAL_PX || !content?.hasAttribute(SUBMIT_PLACING_ATTR)) return;
      // Start the entrance in the same task that unhides the status, so it
      // never paints a frame at full opacity. Plain ease-out, not the
      // front-loaded --ease-out: this should surface, not pop.
      const landed = submittedMessage();
      const turn = landed?.closest(".turn") ?? landed?.parentElement;
      turn?.querySelector<HTMLElement>(":scope > .assistant-turn-shell")?.animate?.([
        { opacity: 0, transform: "translateY(4px)" },
        { opacity: 1, transform: "none" },
      ], {
        duration: motionDurationMs("--motion-slow", 280),
        easing: "ease-out",
        fill: "backwards",
      });
      content.removeAttribute(SUBMIT_PLACING_ATTR);
    };
    const armFrames = (
      paint: (now: number | undefined) => void,
      syncStart: boolean,
      markDirty: () => void,
    ): void => {
      // A React commit or resize retargets the same glide. Steady frames do
      // not: re-reading geometry there forces a layout of the whole thread.
      reflowSubmittedMotionRef.current = () => {
        markDirty();
        paint(messageMotionTime() ?? lastFrameTime);
      };
      if (syncStart) paint(undefined);
      const step = (now: number): void => {
        motionFrameRef.current = undefined;
        lastFrameTime = now;
        paint(now);
        if (scrollModeRef.current === "placing") {
          motionFrameRef.current = window.requestAnimationFrame(step);
        } else if (scrollModeRef.current === "holding") {
          submissionFrameCallbacks.current.scrollConversationToBottom();
        }
      };
      motionFrameRef.current = window.requestAnimationFrame(step);
    };

    if (canLift) {
      // One writer: consume a lead spacer. The bubble and in-progress timer
      // stay in document flow, so they cannot drift apart or fight a scroll.
      glide.start(lift);
      applyLeadSpace(lift);
      let liftViewportHeight = viewport.clientHeight;
      let liftFollowTop = latestFollowScrollTop(viewport);
      let liftDirty = false;
      const paint = (now: number | undefined): void => {
        if (scrollModeRef.current !== "placing") return;
        const viewport = conversationViewport();
        if (!viewport) {
          writeScrollMode("pending");
          applyLeadSpace(0);
          return;
        }
        if (liftDirty) {
          liftViewportHeight = viewport.clientHeight;
          liftFollowTop = latestFollowScrollTop(viewport);
          liftDirty = false;
        }
        const { position, done } = now === undefined
          ? { position: lift, done: false }
          : glide.step(now, 0, liftViewportHeight);
        // Padding is the motion. Do not read it back: the commanded spacer
        // is the position, and a geometry read would lay the thread out twice.
        revealStatusNear(position);
        applyLeadSpace(position);
        viewport.scrollTop = targetTop;
        programmaticScrollTopRef.current = targetTop;
        lastConversationScrollTopRef.current = targetTop;
        if (done) {
          applyLeadSpace(0);
          finishHold(viewport, targetTop);
          return;
        }
        submissionFrameCallbacks.current.rememberActiveThreadScrollSnapshot(
          viewport,
          false,
          targetTop,
          Math.max(0, liftFollowTop - targetTop),
        );
      };
      armFrames(paint, true, () => { liftDirty = true; });
      return true;
    }

    applyLeadSpace(0);
    const screenStart = placement.documentTop - startTop;
    glide.start(screenStart);
    let animatedMessage = message;
    let anchor: SubmitGlideAnchor = {
      documentTop: placement.documentTop,
      targetScreen: placement.documentTop - placement.targetTop,
      viewportHeight: viewport.clientHeight,
      followTop: latestFollowScrollTop(viewport),
    };
    let anchorDirty = false;
    let reservedRange: { target: number; clientHeight: number } | undefined;
    const paint = (now: number | undefined): void => {
      if (scrollModeRef.current !== "placing") return;
      const viewport = conversationViewport();
      if (!viewport) {
        writeScrollMode("pending");
        return;
      }
      // A remount keeps the trajectory. Geometry is refreshed only when the
      // anchor is dirty, so a steady frame does not walk the thread.
      if (animatedMessage.dataset.userMessageId !== submissionRef.current?.messageID || !viewport.contains(animatedMessage)) {
        const replacement = submittedMessage();
        if (!replacement) {
          writeScrollMode("pending");
          return;
        }
        animatedMessage = replacement;
        anchorDirty = true;
      }
      if (anchorDirty) {
        anchor = submitGlideAnchor(viewport, animatedMessage);
        if (submissionRef.current) submissionRef.current.documentTop = anchor.documentTop;
        anchorDirty = false;
      }
      const { position, done } = now === undefined
        ? { position: screenStart, done: false }
        : glide.step(now, anchor.targetScreen, anchor.viewportHeight);
      const top = anchor.documentTop - position;
      // Command the full range first: the reservation is what makes the target
      // reachable, and the browser clamps against it in the same write. The
      // reservation only needs re-syncing when its inputs move, and re-syncing
      // it walks every disclosure in the thread — do that on change, not on
      // every frame of a glide.
      const destinationTop = anchor.documentTop - anchor.targetScreen;
      const range = Math.max(top, destinationTop);
      if (
        !reservedRange ||
        reservedRange.clientHeight !== anchor.viewportHeight ||
        range > reservedRange.target + 0.5 ||
        done
      ) {
        reservedRange = { target: range, clientHeight: anchor.viewportHeight };
        submissionFrameCallbacks.current.ensureTailRange(range);
        anchor.followTop = latestFollowScrollTop(viewport);
      }
      viewport.scrollTop = top;
      syncConversationRenderWindow(viewport);
      // The reservation makes this offset reachable, so the commanded value
      // is the achieved one. Reading scrollTop back would force a second layout.
      programmaticScrollTopRef.current = top;
      lastConversationScrollTopRef.current = top;
      revealStatusNear(Math.abs(anchor.targetScreen - position));
      if (done) {
        finishHold(viewport, top);
        return;
      }
      submissionFrameCallbacks.current.rememberActiveThreadScrollSnapshot(
        viewport,
        false,
        top,
        Math.max(0, anchor.followTop - top),
      );
    };
    armFrames(paint, false, () => { anchorDirty = true; });
    return true;
  }, [activePane, activeThreadID, applyLeadSpace, dockComposerNode, ensureTailRange, reserveTailSpace, scrollConversationToBottom, splitConversation, submittedMessage]);

  useLayoutEffect(() => { positionSubmittedMessageRef.current = positionSubmittedMessage; });

  const requestSubmittedQueryScroll = useCallback((messageID: string): void => {
    // Split panes keep their existing bottom-follow behavior. Ordinary
    // sessions have a different lifecycle: the submitted message is the
    // reading anchor, even when the user had previously browsed history.
    if (splitConversation && !isFollowing()) return;
    pointerScrollGestureRef.current = undefined;
    cancelScrollMotion();
    submissionRef.current = { messageID, threadID: activeThreadID, animate: true };
    clearUserScrollIntent();
    cancelBottomOverscroll(conversationViewport());
    selectionPausedAutoFollowRef.current = false;
    writeScrollMode(splitConversation ? "following" : "pending");
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
    followMotionRef.current = smooth;
    suppressAutoFollowRearmRef.current = smooth;
    setAutoFollowOverflowAnchor(node, true);
    rememberActiveThreadScrollSnapshot(node, true);
    if (!smooth) {
      scrollConversationToBottom();
      return;
    }
    const startTop = clampScrollTop(node, node.scrollTop);
    const duration = motionDurationMs("--query-submit-duration", 220);
    // The same curve the CSS transition on the optimistic turn rides, so the
    // scroll and the height/opacity change it shares a deadline with agree.
    const easing = motionEasing("--query-submit-easing", cubicBezier(1 / 3, 1, 2 / 3, 1));
    let startedAt: number | undefined;
    const step = (now: number): void => {
      motionFrameRef.current = undefined;
      if (!followMotionRef.current || !isFollowing()) return;
      startedAt ??= now;
      const progress = duration > 0 ? Math.min(1, (now - startedAt) / duration) : 1;
      const eased = easing(progress);
      // Share one deadline with the diff receipt's exit, even while its height
      // and the optimistic turn change. Layout signals must not restart easing.
      const targetTop = latestFollowScrollTop(node);
      node.scrollTop = startTop + (targetTop - startTop) * eased;
      // The browser already clamped the write above; reading it back once and
      // reusing it avoids two more extent measurements per frame.
      const placed = node.scrollTop;
      programmaticScrollTopRef.current = placed;
      lastConversationScrollTopRef.current = placed;
      rememberActiveThreadScrollSnapshot(node, true, placed);
      if (progress < 1) {
        motionFrameRef.current = window.requestAnimationFrame(step);
      } else {
        applyProgrammaticScroll(node, targetTop, true, { revealScrollbar: true });
      }
    };
    motionFrameRef.current = window.requestAnimationFrame(step);
  }, [
    activePane,
    activeThreadID,
    clearUserScrollIntent,
    scrollConversationToBottom,
    cancelScrollMotion,
    setAutoFollow,
    splitConversation,
  ]);

  useLayoutEffect(() => {
    if (!adoptingSubmission) cancelScrollMotion();
  }, [activeThreadID, activePane, splitConversation, cancelScrollMotion]);
  useLayoutEffect(() => cancelScrollMotion, [cancelScrollMotion]);

  useEffect(() => {
    const stopReducedMotion = subscribeReducedMotion((reduced) => { if (reduced) cancelScrollMotion(); });
    const hide = () => { if (document.hidden) cancelScrollMotion(); };
    document.addEventListener("visibilitychange", hide);
    return () => { stopReducedMotion(); document.removeEventListener("visibilitychange", hide); };
  }, [cancelScrollMotion]);

  const scheduleStreamScroll = useCallback((): void => {
    if (previousThreadRef.current !== activeThreadID) return;
    syncTailLayout();
    if (!activeThreadID) {
      return;
    }
    if (!isFollowing() && !submissionPhase()) {
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
  }, [activeThreadID, scrollConversationToBottom, syncTailLayout]);

  const enableConversationAutoFollow = useCallback((): void => {
    cancelScrollMotion();
    suppressAutoFollowRearmRef.current = false;
    selectionPausedAutoFollowRef.current = false;
    cancelBottomOverscroll(conversationViewport());
    setAutoFollow(true);
    const node = conversationViewport();
    if (node) {
      setAutoFollowOverflowAnchor(node, true);
      rememberActiveThreadScrollSnapshot(node, true);
    }
  }, [activePane, activeThreadID, cancelScrollMotion, setAutoFollow, splitConversation]);

  const jumpToLatest = useCallback((): void => {
    const node = conversationViewport();
    if (!node) return;
    // Following starts now, not when the motion lands: output streamed
    // meanwhile moves the target, and a fixed destination would stop short of
    // it and leave the reader paused above the reply.
    enableConversationAutoFollow();
    if (prefersReducedMotion() || document.hidden) {
      scrollConversationToBottom();
      return;
    }
    const glide = createScrollGlide();
    glide.start(clampScrollTop(node, node.scrollTop));
    followMotionRef.current = true;
    const step = (now: number): void => {
      motionFrameRef.current = undefined;
      if (!followMotionRef.current || !isFollowing()) return;
      const target = latestFollowScrollTop(node);
      const { position, done } = glide.step(now, target, node.clientHeight);
      node.scrollTop = position;
      syncConversationRenderWindow(node);
      // The glide never passes its target, so the commanded offset is reached.
      programmaticScrollTopRef.current = position;
      lastConversationScrollTopRef.current = position;
      if (!done && target - position > node.clientHeight * GLIDE_FOLLOW_HANDOFF_VIEWPORTS) {
        motionFrameRef.current = window.requestAnimationFrame(step);
        return;
      }
      followMotionRef.current = false;
      applyProgrammaticScroll(node, target, true, { revealScrollbar: true });
    };
    motionFrameRef.current = window.requestAnimationFrame(step);
  }, [activePane, activeThreadID, enableConversationAutoFollow, scrollConversationToBottom, splitConversation]);

  const disableConversationAutoFollow = useCallback((): void => {
    cancelScrollMotion();
    suppressAutoFollowRearmRef.current = true;
    setAutoFollow(false);
    const node = conversationViewport();
    if (node) {
      setAutoFollowOverflowAnchor(node, false);
      rememberActiveThreadScrollSnapshot(node, false);
    }
  }, [activePane, activeThreadID, cancelScrollMotion, setAutoFollow, splitConversation]);

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
      const scrollTop = clampScrollTop(node, node.scrollTop);
      return {
        scrollTop,
        distanceFromLatest: Math.max(0, latestFollowScrollTop(node) - scrollTop),
        readingAnchor: !isFollowing() && !submissionPhase() ? captureReadingAnchor(node) : undefined,
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
      cancelScrollMotion();
      writeScrollMode(snapshot.submissionPhase === "placing" ? "pending" :
        snapshot.submissionPhase ?? (snapshot.autoFollow ? "following" : "paused"));
      submissionRef.current = snapshot.submittedMessageID
        ? { messageID: snapshot.submittedMessageID, threadID: activeThreadID, animate: false }
        : undefined;
      applyProgrammaticScroll(
        node,
        restoredScrollTop(node, snapshot),
        snapshot.autoFollow,
        {
          revealScrollbar: true,
        },
      );
    },
    [activePane, activeThreadID, setAutoFollow, splitConversation],
  );

  function handleConversationScroll(scrolledNode?: HTMLElement): void {
    const node = scrolledNode ?? conversationViewport();
    if (!node) return;
    syncConversationRenderWindow(node);
    if (restoreScrollLockRef.current) {
      return;
    }
    // The glide's own scrollTop writes fire this. Input cancels the glide
    // before the event, so measuring disclosures here laid the thread out
    // again on every frame of the send.
    if (scrollModeRef.current === "placing") return;
    const disclosureHeight = followMotionRef.current
      ? lastDisclosureHeightRef.current
      : conversationDisclosureHeight(node);
    const disclosureResized = Math.abs(disclosureHeight - lastDisclosureHeightRef.current) > 0.5;
    lastDisclosureHeightRef.current = disclosureHeight;
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
    if (disclosureResized && !userScrollIntentRef.current &&
      !pointerScrollGestureRef.current && !selectionPausedAutoFollowRef.current) {
      // Native anchoring can report a disclosure's layout scroll before the
      // resize observer. Preserve the owner and do not spend browsing space.
      // Paused readers keep the browser's anchor; followers track the new end.
      // Resizing may emit no scroll at all, so explicit input in either
      // direction must win over a stale disclosure-height baseline.
      programmaticScrollTopRef.current = undefined;
      scrollConversationToBottom();
      lastConversationScrollTopRef.current = clampScrollTop(node, node.scrollTop);
      rememberActiveThreadScrollSnapshot(node, isFollowing());
      return;
    }
    if (isWindowResizing()) {
      if (isFollowing()) {
        scheduleStreamScroll();
      } else {
        // Native anchoring may move a paused reader during reflow. Retain that
        // new offset without re-arming follow or consuming submission space;
        // otherwise a later session switch restores the pre-resize position.
        // The snapshot re-measures the submitted message with it: restore
        // corrects the offset by that message's movement, and a reflowed
        // offset against its pre-reflow position is off by the whole rewrap.
        programmaticScrollTopRef.current = undefined;
        lastConversationScrollTopRef.current = clampScrollTop(node, node.scrollTop);
        rememberActiveThreadScrollSnapshot(node, false);
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
          userScrollIntentRef.current !== "away" &&
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

    const readingSnapshot = activeThreadID ? threadScrollSnapshotsRef.current.get(activeThreadID) : undefined;
    if (!isFollowing() && readingSnapshot?.readingAnchor &&
      Math.abs(node.scrollTop - readingSnapshot.scrollTop) > 1) {
      const anchoredTop = readingAnchorScrollTop(node, readingSnapshot.readingAnchor);
      if (anchoredTop !== undefined && Math.abs(anchoredTop - node.scrollTop) <= 0.5) {
        // Native anchoring (or a history prepend) moved scrollTop but left the
        // content stationary. It is not a gesture toward latest, and must not
        // re-arm following or consume the reader's reserved space.
        lastConversationScrollTopRef.current = clampScrollTop(node, node.scrollTop);
        rememberActiveThreadScrollSnapshot(node, false);
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
    const userScrollAwayIntent = userScrollIntentRef.current === "away";
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
    const nearLatest = scrolledDown &&
      distanceFromLatestContent(node) <= CONVERSATION_RETURN_TO_LATEST_PX;
    const returnedToLatest = atLatestView || (nearLatest && userScrollIntentRef.current === "latest");
    const scrollAwayStartTop = userScrollAwayStartTopRef.current;
    const movedAboveUserIntentStart =
      userScrollAwayIntent &&
      scrollAwayStartTop !== undefined &&
      node.scrollTop < scrollAwayStartTop - 1;
    const movedAbovePreviousScroll = userScrollAwayIntent && scrolledUp;
    let nextAutoFollow = isFollowing();
    if (pointerGesture?.node === node) {
      // Returning to the bottom band does not release a held scrollbar.
      // Defer follow until pointerup so stream frames cannot fight the drag.
      if (scrolledUp || scrolledDown) {
        setAutoFollow(false);
        pointerGesture.followOnRelease = (atLatestView || nearLatest) && scrolledDown && !selectionPausedAutoFollowRef.current;
      }
      nextAutoFollow = false;
      setAutoFollowOverflowAnchor(node, false);
    } else if (
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
    } else if (returnedToLatest && suppressAutoFollowRearmRef.current) {
      // Query-history / turn-rail jumps are programmatic smooth scrolls.
      // The browser can emit an unchanged or tiny upward scroll event while
      // the viewport is still inside the bottom band. If that re-arms
      // auto-follow, the next scroll/layout signal yanks the viewport back to
      // the bottom before the jump reaches its target. Only an actual downward
      // move back to the latest content should clear this jump guard.
      if (followMotionRef.current) {
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
      // returned-to-latest case; this branch covers the in-between frames.
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
    } else if (returnedToLatest) {
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
      consumeTailSpace(Math.abs(previousScrollTop - node.scrollTop));
    }
    rememberActiveThreadScrollSnapshot(node, nextAutoFollow);
  }

  useLayoutEffect(() => {
    try {
      const node = conversationViewport();
      const threadChanged = previousThreadRef.current !== activeThreadID;
      // Read the saved position before any layout. The pane swap clamps
      // scrollTop, and that scroll event would otherwise replace this snapshot.
      const savedSnapshot = activeThreadID
        ? threadScrollSnapshotsRef.current.get(activeThreadID)
        : undefined;
      // Restore against the incoming viewport, not the outgoing draft's height.
      // Clamping first loses a paused reader's offset even if an observer later
      // repairs the composer inset.
      syncDockComposerGeometry();
      pointerScrollGestureRef.current = undefined;
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
      // The status row already shows the incoming session's items. Its observer
      // reports a frame late, so placing against the outgoing reservation would
      // move the whole session once the stale gap is released.
      syncConversationStatusSpace();
      let snapshot = savedSnapshot;
      const restorationOffset = restoredOffset.current;
      restoredOffset.current = 0;
      if (snapshot?.submittedMessageTop !== undefined && !submittedMessage(snapshot.submittedMessageID)) {
        // The saved anchor itself may have left the history window. Its absolute
        // extent cannot describe this layout; open at the latest content instead.
        discardTailSpace(activeThreadID);
        submissionRef.current = undefined;
        snapshot = undefined;
      }
      writeScrollMode(snapshot?.submissionPhase === "placing" ? "pending" :
        snapshot?.submissionPhase ?? (snapshot?.autoFollow === false ? "paused" : "following"));
      if (snapshot?.submittedMessageID) {
        submissionRef.current = { messageID: snapshot.submittedMessageID, threadID: activeThreadID, animate: false };
      }
      if (snapshot && !snapshot.autoFollow) {
        // Prefer the content anchor; submission reservations retain their own
        // coordinate system and fallback snapshots still support empty layouts.
        applyProgrammaticScroll(
          node,
          restoredScrollTop(node, snapshot, restorationOffset),
          false,
        );
        bottomOverscrollFromAwayRef.current = true;
        setNativeBottomOverscrollEnabled(node, true);
      } else {
        applyProgrammaticScroll(
          node,
          latestFollowScrollTop(node),
          true,
        );
        setNativeBottomOverscrollEnabled(node, false);
      }
      markSessionSwitch(activeThreadID, "scroll-restore-end");
      return undefined;
    } finally {
      restoreScrollLockRef.current = false;
    }
  }, [activePane, activeThreadID, setAutoFollow, splitConversation, syncConversationStatusSpace, syncDockComposerGeometry, restoredOffset, submittedMessage, discardTailSpace]);

  // Only a direct submission owns placement. Queue/steer materialization is
  // incoming content and must preserve the current following/reading policy.
  useLayoutEffect(() => {
    positionSubmittedMessage(true);
  });

  useLayoutEffect(reconcileSubmittedArrival);

  useLayoutEffect(() => {
    if (!activeThreadID) {
      return;
    }
    const submission = submissionRef.current;
    if (submission?.threadID === activeThreadID && submissionPhase()) {
      if (primaryTurns?.some(turn => turn.items.some(item => item.id === submission.messageID))) {
        submission.inThread = true;
      } else if (submission.inThread) {
        // A snapshot that drops the submitted message (a resync that replaced
        // the thread) leaves nothing to place or hold. Keeping its reading
        // frame would stop the conversation following what replaced it.
        cancelScrollMotion();
        submissionRef.current = undefined;
        discardTailSpace(activeThreadID);
        setAutoFollow(true);
      }
    }
    // Turn snapshots can add non-token content (for example a gray process
    // row). Re-anchor before paint so the bottom never flashes at old scrollTop.
    scrollConversationToBottom();
  }, [
    activeThreadID,
    cancelScrollMotion,
    discardTailSpace,
    primaryTurns,
    scrollConversationToBottom,
    secondaryTurns,
    setAutoFollow,
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
      if (event.deltaY !== 0 && (motionFrameRef.current !== undefined || submissionPhase())) disableConversationAutoFollow();
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        if (event.deltaY < 0) {
          markUserScrollIntent("away", clampScrollTop(node, node.scrollTop));
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
        markUserScrollIntent("away", clampScrollTop(node, node.scrollTop));
        // Take user control before the browser's later `scroll` event. During
        // streaming, an already queued auto-follow frame can otherwise run in
        // the wheel-to-scroll gap and write the viewport back to the bottom,
        // making trackpad and mouse-wheel movement feel sticky or resistant.
        disableConversationAutoFollow();
      } else if (deltaPx > 0) {
        markUserScrollIntent("latest");
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
      if (eventTargetsConversationDisclosure(event.target)) {
        clearUserScrollIntent();
        return;
      }
      if (motionFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
      cancelArrivals();
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        return;
      }
      if (event.target === node) {
        const resumeScrollTop = isFollowing() ? clampScrollTop(node, node.scrollTop) : undefined;
        pointerScrollGestureRef.current = {
          node,
          scrollTop: clampScrollTop(node, node.scrollTop),
          scrollHeight: node.scrollHeight,
        };
        markUserScrollIntent("away", clampScrollTop(node, node.scrollTop));
        // A scrollbar gesture owns the viewport before native scroll delivery.
        disableConversationAutoFollow();
        pointerScrollGestureRef.current.resumeScrollTop = resumeScrollTop;
      }
    };
    const handlePointerEnd = (event: PointerEvent): void => {
      const gesture = pointerScrollGestureRef.current;
      pointerScrollGestureRef.current = undefined;
      // A press on empty surface is not necessarily a scroll. Do not leave
      // following disabled when it ends without movement or another owner.
      if (event.type === "pointerup" && gesture?.node === node &&
        ((gesture.resumeScrollTop !== undefined &&
          Math.abs(clampScrollTop(node, node.scrollTop) - gesture.resumeScrollTop) <= 1) ||
          (gesture.followOnRelease && distanceFromLatestContent(node) <= CONVERSATION_RETURN_TO_LATEST_PX))) {
        enableConversationAutoFollow();
        scrollConversationToBottom();
      }
    };
    const handleSelectionChange = (): void => {
      if (selectionIntersectsNode(document.getSelection(), node)) {
        selectionPausedAutoFollowRef.current = true;
        disableConversationAutoFollow();
      }
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      if ((event.key === "Enter" || event.key === " ") && eventTargetsConversationDisclosure(event.target)) {
        clearUserScrollIntent();
        return;
      }
      if (SCROLL_TOWARD_LATEST_KEYS.has(event.key) || ((event.key === "Enter" || event.key === " ") &&
        event.target instanceof Element && event.target.closest('button, [role="button"], summary'))) {
        if (motionFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
      }
      if (eventTargetsNestedAutoFollowScroll(event.target, node)) {
        return;
      }
      if (SCROLL_AWAY_KEYS.has(event.key)) {
        if (nativeScrollBounce) {
          bottomOverscrollFromAwayRef.current = true;
          setNativeBottomOverscrollEnabled(node, true);
        }
        markUserScrollIntent("away", clampScrollTop(node, node.scrollTop));
        disableConversationAutoFollow();
      } else if (SCROLL_TOWARD_LATEST_KEYS.has(event.key)) {
        markUserScrollIntent("latest");
        selectionPausedAutoFollowRef.current = false;
      }
    };
    const handleTouchStart = (event: TouchEvent): void => {
      if (eventTargetsConversationDisclosure(event.target)) {
        clearUserScrollIntent();
        touchLastYRef.current = event.touches[0]?.clientY;
        return;
      }
      if (motionFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
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
          markUserScrollIntent("away", clampScrollTop(node, node.scrollTop));
        }
        touchLastYRef.current = currentY;
        return;
      }
      const currentY = event.touches[0]?.clientY;
      const previousY = touchLastYRef.current;
      if (currentY !== undefined && previousY !== undefined && currentY !== previousY && submissionPhase()) {
        disableConversationAutoFollow();
      }
      if (
        currentY !== undefined &&
        previousY !== undefined &&
        currentY > previousY
      ) {
        if (nativeScrollBounce) {
          bottomOverscrollFromAwayRef.current = true;
          setNativeBottomOverscrollEnabled(node, true);
        }
        markUserScrollIntent("away", clampScrollTop(node, node.scrollTop));
        disableConversationAutoFollow();
      } else if (
        currentY !== undefined &&
        previousY !== undefined &&
        currentY < previousY
      ) {
        markUserScrollIntent("latest");
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
      if (eventTargetsConversationDisclosure(event.target)) {
        clearUserScrollIntent();
        return;
      }
      if (!(event.target instanceof Element) || !event.target.closest('button, [role="button"], summary, a, input, textarea, select, video, audio')) return;
      if (motionFrameRef.current !== undefined || submissionPhase()) disableConversationAutoFollow();
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
    const resizeObserver = new ResizeObserver(() => {
      if (isWindowResizing()) {
        pinConversationDuringWindowResize();
        return;
      }
      refreshPointerScrollGestureLayout(node);
      // Observer delivery is already after layout and before paint. Deferring
      // to rAF here paints the new line wrapping with the previous scrollTop.
      scrollConversationToBottom();
    });
    observeAutoFollowResizeTargets(node, resizeObserver);
    return () => {
      resizeObserver.disconnect();
    };
  }, [
    activePane,
    activeThreadID,
    emptyConversation,
    initialized,
    primaryTurns,
    pinConversationDuringWindowResize,
    refreshPointerScrollGestureLayout,
    secondaryTurns,
    scrollConversationToBottom,
    splitConversation
  ]);

  useLayoutEffect(() => {
    if (!conversationPaneRef.current) return;
    let frame = 0;
    const update = (): void => {
      if (!syncConversationStatusSpace()) return;
      if (isWindowResizing()) {
        pinConversationDuringWindowResize();
        return;
      }
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
  }, [pinConversationDuringWindowResize, statusClusterNode, syncConversationStatusSpace, scrollConversationToBottom]);

  useLayoutEffect(() => {
    const node = dockComposerNode;
    const updateHeight = (): void => {
      syncDockComposerGeometry();
      if (isWindowResizing()) {
        pinConversationDuringWindowResize();
        return;
      }
      scrollConversationToBottom();
    };
    updateHeight();
    if (!node) return;
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
      window.cancelAnimationFrame(heightFrame);
      resizeObserver.disconnect();
    };
  }, [
    dockComposerNode,
    emptyConversation,
    initialized,
    pinConversationDuringWindowResize,
    scrollConversationToBottom,
    syncDockComposerGeometry
  ]);

  useEffect(() => {
    return () => {
      if (streamScrollFrameRef.current !== undefined) {
        window.cancelAnimationFrame(streamScrollFrameRef.current);
        streamScrollFrameRef.current = undefined;
      }
      cancelBottomOverscroll();
      clearUserScrollIntent();
    };
  }, [clearUserScrollIntent]);

  return {
    conversationScrollRef,
    scrollContentRef,
    splitPaneRefs,
    conversationPaneRef,
    dockComposerRef,
    dockComposerNode,
    statusClusterRef,
    scheduleStreamScroll,
    handleConversationScroll,
    enableConversationAutoFollow,
    jumpToLatest,
    disableConversationAutoFollow,
    captureConversationScrollPosition,
    restoreConversationScrollPosition,
    requestSubmittedQueryScroll,
    acknowledgeSubmittedMessage,
    discardSubmittedMessage,
  };
}
