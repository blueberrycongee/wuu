import { useLayoutEffect, useRef, type RefObject } from "react";
import type { PendingViewSwitch } from "./ViewSwitchState";
import { CONVERSATION_MEDIA_DISCOVERY_MS, CONVERSATION_MEDIA_SETTLE_MS, hasPendingConversationMedia, settleConversationMedia, waitForConversationMedia } from "./ConversationMediaSettle";

const MAX_RESTORATION_FRAMES = 8;

/** Keep restoration covered through real layout and its first stable paint. */
export function useConversationSwitchReady({
  visible = true,
  mediaExpected = false,
  pendingViewSwitch,
  activeThreadID,
  viewportRef,
  settleLayout,
  finishViewSwitch,
}: {
  visible?: boolean;
  /** The resumed thread contains images that may mount after its first layout. */
  mediaExpected?: boolean;
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
    let mediaSettled = false;
    let cancelled = false;
    let frame = 0;
    const startedAt = performance.now();
    const finish = (): void => {
      if (!cancelled) finishViewSwitch(requestID);
    };
    const check = (): void => {
      if (cancelled) return;
      measuredFrames += 1;
      const viewport = viewportRef.current;
      if (!viewport) {
        // A selected surface may remount after the resumed data is published.
        // Missing geometry must not strand navigation behind its loading cover.
        if (measuredFrames >= MAX_RESTORATION_FRAMES) finish();
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
      if (stableFrames >= 2 && kind === "thread" && mediaExpected && !mediaSettled) {
        // Layout is settled, but the resumed thread's image components can
        // mount in a later nested commit and then decode into their reserved
        // rows. Allow that commit to arrive, then keep the cover until images
        // near the viewport have loaded and decoded. Both waits are bounded so
        // an unavailable remote source cannot hold the conversation.
        mediaSettled = true;
        const discovery = Math.max(0, CONVERSATION_MEDIA_DISCOVERY_MS - (performance.now() - startedAt));
        void waitForConversationMedia(viewport, discovery).then(() => {
          if (cancelled) return;
          if (!hasPendingConversationMedia(viewport)) {
            settleLayoutRef.current();
            finish();
            return;
          }
          const remaining = Math.max(0, CONVERSATION_MEDIA_SETTLE_MS - (performance.now() - startedAt));
          void settleConversationMedia(viewport, remaining).then(() => {
            if (cancelled) return;
            settleLayoutRef.current();
            finish();
          });
        });
        return;
      }
      if (stableFrames >= 2 || measuredFrames >= MAX_RESTORATION_FRAMES) {
        finish();
        return;
      }
      frame = window.requestAnimationFrame(check);
    };
    frame = window.requestAnimationFrame(check);
    return () => {
      cancelled = true;
      window.cancelAnimationFrame(frame);
    };
  }, [activeThreadID, finishViewSwitch, kind, mediaExpected, requestID, targetID, viewportRef, visible]);
}
