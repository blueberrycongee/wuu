import { useEffect, useRef } from "react";
import type { ActivitySession, BrowserBoundsRect } from "../shared/protocol";
import { submitGlideActive, subscribeSubmitGlide } from "./AutoFollowScroll";

// The workspace browser panel and the agent's page are the same tab. Painting
// is owned by the panel: it reports a rectangle while that tab is on screen.
// The agent does not open or close the panel; the floating card is the live
// preview until the user docks the page. A torn-down core still drops leftover
// activity UI here.

// ── Pure helpers (unit-tested) ─────────────────────────────────────────────

export type { BrowserBoundsRect };

// A browser activity is "visible" — its main-owned WebContentsView is overlaid
// on the window — only in the foreground-controlled state. Every renderer-side
// takeover behaviour keys off this single predicate.
export function isForegroundControlled(
  activity: ActivitySession | undefined,
): activity is ActivitySession {
  return (
    activity?.kind === "browser" && activity.state === "foreground_controlled"
  );
}

// Main keys agent views by (workdir, tabID). The tab id travels on the
// activity's `target` field (set to the browser tab id when the activity is
// acquired); fall back to the activity id so we always report a stable key.
export function browserTabIDForActivity(activity: ActivitySession): string {
  return activity.target && activity.target.length > 0
    ? activity.target
    : activity.id;
}

// The panel shows the agent's tab while that activity is alive, and a
// per-thread tab otherwise. Both are the same kind of page: one web contents
// the address bar can drive.
export function displayedBrowserTabID(
  activity: ActivitySession | undefined,
  threadID: string | undefined,
): string | undefined {
  if (activity?.kind === "browser" && activity.state !== "stopped") {
    return browserTabIDForActivity(activity);
  }
  if (threadID && threadID.length > 0) return `user:${threadID}`;
  return undefined;
}

export function roundRect(rect: BrowserBoundsRect): BrowserBoundsRect {
  return {
    x: Math.round(rect.x),
    y: Math.round(rect.y),
    width: Math.round(rect.width),
    height: Math.round(rect.height),
  };
}

export function isMeasurableRect(rect: BrowserBoundsRect): boolean {
  return rect.width > 0 && rect.height > 0;
}

// Report only when the (integer) rect actually moved or resized — an unchanged
// rect polled every animation frame must not spam IPC.
export function boundsChanged(
  previous: BrowserBoundsRect | undefined,
  next: BrowserBoundsRect,
): boolean {
  if (!previous) {
    return true;
  }
  return (
    previous.x !== next.x ||
    previous.y !== next.y ||
    previous.width !== next.width ||
    previous.height !== next.height
  );
}

// Prefer the inner host. Fall back to the frame when the host has no area
// so the page still has a rectangle to occupy.
export function pickBoundsRect(
  hostRect: BrowserBoundsRect | undefined,
  frameRect: BrowserBoundsRect | undefined,
): BrowserBoundsRect | undefined {
  if (hostRect && isMeasurableRect(hostRect)) {
    return hostRect;
  }
  if (frameRect && isMeasurableRect(frameRect)) {
    return frameRect;
  }
  return hostRect ?? frameRect;
}

export type ForegroundSnapshot = {
  threadID?: string;
  activityID?: string;
  state?: string;
};

// Decide whether an observed browser activity should force the browser panel
// open. Mirrors useThreadBrowserPreview's "switching threads only restores,
// never force-opens" discipline: a genuine foreground *transition* on the
// activity currently in view opens the panel; merely switching to a thread
// whose activity is already foreground does not.
export function computeForegroundPromotion(
  previous: ForegroundSnapshot,
  threadID: string | undefined,
  activity: ActivitySession | undefined,
): { open: boolean; snapshot: ForegroundSnapshot } {
  const snapshot: ForegroundSnapshot = {
    threadID,
    activityID: activity?.id,
    state: activity?.state,
  };
  if (previous.threadID !== threadID) {
    return { open: false, snapshot };
  }
  if (!isForegroundControlled(activity)) {
    return { open: false, snapshot };
  }
  const wasForeground =
    previous.activityID === activity.id &&
    previous.state === "foreground_controlled";
  return { open: !wasForeground, snapshot };
}

// The agent asked to stop showing the page. Closing is limited to that
// transition: handing control back, or switching threads, leaves the panel
// where the user put it.
export function computeForegroundRetreat(
  previous: ForegroundSnapshot,
  threadID: string | undefined,
  activity: ActivitySession | undefined,
): boolean {
  if (!threadID || previous.threadID !== threadID) return false;
  if (previous.state !== "foreground_controlled") return false;
  if (!activity || previous.activityID !== activity.id) return false;
  return activity.state === "background_controlled";
}

// ── DOM measurement (thin, not unit-tested — jsdom rects are all zero) ───────

function measureRect(element: Element | null): BrowserBoundsRect | undefined {
  if (!element) {
    return undefined;
  }
  const rect = element.getBoundingClientRect();
  return roundRect({
    x: rect.left,
    y: rect.top,
    width: rect.width,
    height: rect.height,
  });
}

function measureBrowserPanelRect(
  host: Element | null,
  frame: Element | null,
): BrowserBoundsRect | undefined {
  return pickBoundsRect(measureRect(host), measureRect(frame));
}

const BOUNDS_TRANSITION_PROPERTIES = new Set([
  "grid-template-columns",
  "transform",
  "width",
]);
const BOUNDS_TRANSITION_MAX_MS = 1000;

function transitionAffectsBrowserPanel(
  event: TransitionEvent,
  host: Element | null,
  frame: Element | null,
): boolean {
  if (!BOUNDS_TRANSITION_PROPERTIES.has(event.propertyName)) {
    return false;
  }
  const target = event.target;
  if (!(target instanceof Element)) {
    return false;
  }
  return [host, frame].some(
    (element) =>
      element !== null &&
      (target === element || target.contains(element) || element.contains(target)),
  );
}

// Keep the native WebContentsView aligned without a permanent frame loop.
// Resize/scroll changes schedule one measurement; the short rAF loop only
// runs while a geometry-changing CSS transition is active.
export function observeBrowserPanelBounds(
  report: (rect: BrowserBoundsRect) => void,
): () => void {
  let host: Element | null = null;
  let frame: Element | null = null;
  let rafHandle: number | undefined;
  let activeTransitions = 0;
  let transitionSafetyTimer: number | undefined;
  let lastRect: BrowserBoundsRect | undefined;
  let mountObserver: MutationObserver | undefined;
  const resizeObserver =
    typeof ResizeObserver === "undefined"
      ? undefined
      : new ResizeObserver(() => scheduleMeasure());

  const syncElements = (): void => {
    const nextHost = host?.isConnected
      ? host
      : document.querySelector(".workspace-browser-host");
    const nextFrame = frame?.isConnected
      ? frame
      : document.querySelector(".workspace-browser-frame");
    if (nextHost === host && nextFrame === frame) {
      return;
    }
    host = nextHost;
    frame = nextFrame;
    resizeObserver?.disconnect();
    if (host) {
      resizeObserver?.observe(host);
    }
    if (frame && frame !== host) {
      resizeObserver?.observe(frame);
    }
    if (host || frame) {
      mountObserver?.disconnect();
      mountObserver = undefined;
    }
  };

  const measureAndReport = (): void => {
    syncElements();
    const rect = measureBrowserPanelRect(host, frame);
    if (rect && boundsChanged(lastRect, rect)) {
      lastRect = rect;
      report(rect);
    }
  };

  function flushMeasure(): void {
    rafHandle = undefined;
    measureAndReport();
    if (activeTransitions > 0) {
      rafHandle = window.requestAnimationFrame(flushMeasure);
    }
  }

  function scheduleMeasure(): void {
    // Conversation send scrolls this window every frame. The browser panel
    // does not move with that glide; measuring it then walks the document.
    if (submitGlideActive()) return;
    if (rafHandle === undefined) {
      rafHandle = window.requestAnimationFrame(flushMeasure);
    }
  }

  const handleTransitionRun = (rawEvent: Event): void => {
    const event = rawEvent as TransitionEvent;
    syncElements();
    if (!transitionAffectsBrowserPanel(event, host, frame)) {
      return;
    }
    activeTransitions += 1;
    if (transitionSafetyTimer !== undefined) {
      window.clearTimeout(transitionSafetyTimer);
    }
    transitionSafetyTimer = window.setTimeout(() => {
      activeTransitions = 0;
      transitionSafetyTimer = undefined;
    }, BOUNDS_TRANSITION_MAX_MS);
    scheduleMeasure();
  };
  const handleTransitionStop = (rawEvent: Event): void => {
    const event = rawEvent as TransitionEvent;
    if (!transitionAffectsBrowserPanel(event, host, frame)) {
      return;
    }
    activeTransitions = Math.max(0, activeTransitions - 1);
    if (activeTransitions === 0 && transitionSafetyTimer !== undefined) {
      window.clearTimeout(transitionSafetyTimer);
      transitionSafetyTimer = undefined;
    }
    scheduleMeasure();
  };

  syncElements();
  if (!host && !frame && typeof MutationObserver !== "undefined") {
    mountObserver = new MutationObserver(() => {
      syncElements();
      scheduleMeasure();
    });
    mountObserver.observe(document.body, { childList: true, subtree: true });
  }
  window.addEventListener("resize", scheduleMeasure);
  window.addEventListener("scroll", scheduleMeasure, true);
  const unsubscribeGlide = subscribeSubmitGlide(scheduleMeasure);
  document.addEventListener("transitionrun", handleTransitionRun);
  document.addEventListener("transitionend", handleTransitionStop);
  document.addEventListener("transitioncancel", handleTransitionStop);
  scheduleMeasure();

  return () => {
    if (rafHandle !== undefined) {
      window.cancelAnimationFrame(rafHandle);
    }
    if (transitionSafetyTimer !== undefined) {
      window.clearTimeout(transitionSafetyTimer);
    }
    resizeObserver?.disconnect();
    mountObserver?.disconnect();
    unsubscribeGlide();
    window.removeEventListener("resize", scheduleMeasure);
    window.removeEventListener("scroll", scheduleMeasure, true);
    document.removeEventListener("transitionrun", handleTransitionRun);
    document.removeEventListener("transitionend", handleTransitionStop);
    document.removeEventListener("transitioncancel", handleTransitionStop);
  };
}

// ── Hook ─────────────────────────────────────────────────────────────────

export function useBrowserVisibility({
  onInvalidateWorkdir,
}: {
  activeThreadID?: string | undefined;
  activeBrowserActivity?: ActivitySession | undefined;
  onInvalidateWorkdir: (workdir: string) => void;
}): void {
  const onInvalidateWorkdirRef = useRef(onInvalidateWorkdir);

  useEffect(() => {
    onInvalidateWorkdirRef.current = onInvalidateWorkdir;
  }, [onInvalidateWorkdir]);

  // Server-exit fallback: when a core is torn down/evicted its Close-time
  // "stopped" events can be lost, leaving ghost browser activity UI hanging
  // forever. The invalidate signal lets us hard-clear that workdir locally.
  useEffect(() => {
    const subscribe = window.wuu.onBrowserInvalidate;
    if (typeof subscribe !== "function") {
      return undefined;
    }
    return subscribe((payload) => {
      if (payload && typeof payload.workdir === "string") {
        onInvalidateWorkdirRef.current(payload.workdir);
      }
    });
  }, []);
}
