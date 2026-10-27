import { useLayoutEffect, useRef, type RefObject } from "react";
import type { PendingViewSwitch } from "./ViewSwitchState";

const MAX_RESTORATION_FRAMES = 8;

/** Keep restoration covered through real layout and its first stable paint. */
export function useConversationSwitchReady({
  visible = true,
  pendingViewSwitch,
  activeThreadID,
  viewportRef,
  settleLayout,
  finishViewSwitch,
}: {
  visible?: boolean;
  pendingViewSwitch: PendingViewSwitch | undefined;
  activeThreadID: string | undefined;
  viewportRef: RefObject<HTMLElement | null>;
  settleLayout: () => void;
  finishViewSwitch: (requestID: number) => boolean;
}): void {
  const settleLayoutRef = useRef(settleLayout);
  settleLayoutRef.current = settleLayout;
  const requestID = pendingViewSwitch?.restoreRequestID;
  const targetID = pendingViewSwitch?.targetID;
  const kind = pendingViewSwitch?.kind;
  useLayoutEffect(() => {
    // Only thread addresses name a history. Verified workspace/runtime/tab
    // destinations can present a blank draft, and their IDs are not thread IDs.
    if (requestID === undefined || kind === "thread" && targetID !== activeThreadID) return;
    // Covered routes cannot provide visible conversation geometry. Finish
    // runtime recovery without waiting for the hidden or unmounted viewport.
    if (!visible) {
      finishViewSwitch(requestID);
      return;
    }
    let previous = "";
    let stableFrames = 0;
    let measuredFrames = 0;
    let frame = 0;
    const check = (): void => {
      measuredFrames += 1;
      const viewport = viewportRef.current;
      if (!viewport) {
        // A selected surface may remount after the resumed data is published.
        // Missing geometry must not strand navigation behind its loading cover.
        if (measuredFrames >= MAX_RESTORATION_FRAMES) finishViewSwitch(requestID);
        else frame = window.requestAnimationFrame(check);
        return;
      }
      settleLayoutRef.current();
      const geometry = [viewport.clientWidth, viewport.clientHeight, viewport.scrollHeight, viewport.scrollTop].join(":");
      stableFrames = geometry === previous ? stableFrames + 1 : 0;
      previous = geometry;
      // The first callback precedes paint; two unchanged frames also allow
      // nested React commits and resize-observer scroll corrections to settle.
      // Live output or external assets may keep changing height. The snapshot
      // and snapped folds are already committed; bound only this layout phase,
      // never the preceding resume, and reveal their latest restored geometry.
      if (stableFrames >= 2 || measuredFrames >= MAX_RESTORATION_FRAMES) {
        finishViewSwitch(requestID);
        return;
      }
      frame = window.requestAnimationFrame(check);
    };
    frame = window.requestAnimationFrame(check);
    return () => window.cancelAnimationFrame(frame);
  }, [activeThreadID, finishViewSwitch, kind, requestID, targetID, viewportRef, visible]);
}
