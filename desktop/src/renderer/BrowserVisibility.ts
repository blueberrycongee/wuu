import { useEffect, useMemo, useRef } from "react";
import type { ActivitySession, BrowserBoundsRect } from "../shared/protocol";
import { submitGlideActive, subscribeSubmitGlide } from "./AutoFollowScroll";

// Renderer-side visibility takeover for agent-owned browser activities (M3).
//
// The agent's page lives in a main-owned WebContentsView (never inside the
// unstable WorkspaceBrowserPanel component). When it is promoted to the
// foreground the main process overlays that view on top of the window; this
// hook feeds main everything it needs from the renderer: where the browser
// panel sits (bounds), when a full-window overlay must temporarily hide the
// view (suppress), when to force the panel open (foreground promotion), and a
// local fallback that drops ghost activity UI when a core is torn down.
//
// The DOM/effect wiring is deliberately thin; every decision is factored into
// the pure helpers below so they can be unit-tested without a real webview.

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

// Prefer the inner host div (design §3.4). It goes `display:none` while the
// user webview shows the home page, so fall back to the always-present frame
// so the agent overlay still gets a real region to sit in.
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
  activeThreadID,
  activeBrowserActivity,
  overlaySuppressed,
  onOpenBrowser,
  onInvalidateWorkdir,
}: {
  activeThreadID: string | undefined;
  activeBrowserActivity: ActivitySession | undefined;
  overlaySuppressed: boolean;
  onOpenBrowser: () => void;
  onInvalidateWorkdir: (workdir: string) => void;
}): void {
  const onOpenBrowserRef = useRef(onOpenBrowser);
  const onInvalidateWorkdirRef = useRef(onInvalidateWorkdir);
  const foregroundSnapshotRef = useRef<ForegroundSnapshot>({});

  useEffect(() => {
    onOpenBrowserRef.current = onOpenBrowser;
  }, [onOpenBrowser]);
  useEffect(() => {
    onInvalidateWorkdirRef.current = onInvalidateWorkdir;
  }, [onInvalidateWorkdir]);

  // Foreground promotion: open the browser panel only on a genuine foreground
  // event, never on a plain thread switch (restore, not force).
  useEffect(() => {
    const { open, snapshot } = computeForegroundPromotion(
      foregroundSnapshotRef.current,
      activeThreadID,
      activeBrowserActivity,
    );
    foregroundSnapshotRef.current = snapshot;
    if (open) {
      onOpenBrowserRef.current();
    }
  }, [activeThreadID, activeBrowserActivity]);

  // Stable identity of the currently-visible agent view; only changes when the
  // target (workdir/tab) or its visibility actually changes, so the effects
  // below do not restart on every unrelated activity merge.
  const foregroundTarget = useMemo(() => {
    if (!isForegroundControlled(activeBrowserActivity)) {
      return undefined;
    }
    return {
      workdir: activeBrowserActivity.workdir,
      tabID: browserTabIDForActivity(activeBrowserActivity),
    };
  }, [
    activeBrowserActivity?.kind,
    activeBrowserActivity?.state,
    activeBrowserActivity?.workdir,
    activeBrowserActivity?.target,
    activeBrowserActivity?.id,
  ]);

  // Bounds reporter: event-driven while the panel is still, with short rAF
  // tracking only for geometry-changing CSS transitions.
  useEffect(() => {
    const report = window.wuu.reportBrowserBounds;
    if (!foregroundTarget || typeof report !== "function") {
      return undefined;
    }
    const { workdir, tabID } = foregroundTarget;
    return observeBrowserPanelBounds((rect) => report(workdir, tabID, rect));
  }, [foregroundTarget]);

  // Overlay suppression: hide the agent view while a full-window overlay is
  // open (native views float above DOM and would occlude the modal). Cleanup
  // always lifts suppression so the view is never left permanently hidden.
  useEffect(() => {
    const suppress = window.wuu.suppressBrowserOverlay;
    if (!foregroundTarget || typeof suppress !== "function") {
      return undefined;
    }
    const { workdir, tabID } = foregroundTarget;
    suppress(workdir, tabID, overlaySuppressed);
    return () => {
      suppress(workdir, tabID, false);
    };
  }, [foregroundTarget, overlaySuppressed]);

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
