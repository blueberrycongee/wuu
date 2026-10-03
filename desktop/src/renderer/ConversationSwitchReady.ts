import { useLayoutEffect, useRef, type RefObject } from "react";
import type { PendingViewSwitch } from "./ViewSwitchState";

const MAX_RESTORATION_FRAMES = 8;

/** Keep restoration covered through real layout and its first stable paint. */
export function useConversationSwitchReady({
  pendingViewSwitch,
  activeThreadID,
  viewportRef,
  settleLayout,
  finishViewSwitch,
}: {
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
  useLayoutEffect(() => {
    if (requestID === undefined || targetID !== activeThreadID) return;
    let previous = "";
    let stableFrames = 0;
    let measuredFrames = 0;
    let frame = 0;
    const check = (): void => {
      const viewport = viewportRef.current;
      if (!viewport) return;
      settleLayoutRef.current();
      const geometry = [viewport.clientWidth, viewport.clientHeight, viewport.scrollHeight, viewport.scrollTop].join(":");
      stableFrames = geometry === previous ? stableFrames + 1 : 0;
      previous = geometry;
      measuredFrames += 1;
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
  }, [activeThreadID, finishViewSwitch, requestID, targetID, viewportRef]);
}
