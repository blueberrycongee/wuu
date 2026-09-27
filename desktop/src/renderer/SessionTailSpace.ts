import { type RefObject, useCallback, useLayoutEffect, useRef } from "react";
import { conversationDisclosureHeight } from "./ConversationDisclosure";

/** A submission reserves a minimum content extent, not permanent bottom padding. */
export function useSessionTailSpace({
  threadID, enabled, preserveOnThreadChange, viewportRef, contentRef, getRestorationOffset,
}: {
  threadID?: string;
  enabled: boolean;
  preserveOnThreadChange: boolean;
  viewportRef: RefObject<HTMLElement | null>;
  contentRef: RefObject<HTMLElement | null>;
  getRestorationOffset: () => number;
}) {
  const space = useRef(0);
  const extent = useRef(0);
  const reservedNaturalHeight = useRef(0);
  const reservedDisclosureHeight = useRef(0);
  const savedExtents = useRef(new Map<string, { extent: number; natural: number; disclosure: number }>());
  const layoutScope = useRef<{ threadID?: string; enabled: boolean } | undefined>(undefined);
  const restoredOffset = useRef(0);
  const apply = useCallback((height: number) => {
    if (height === space.current) return;
    // Layout rounds fractional padding while the motion keeps a continuous
    // position. Feeding that measurement noise back into an inherited token
    // causes another style/layout pass on every frame. Preserve the applied
    // value below half a CSS pixel, but always clear a consumed reservation.
    if (height > 0 && space.current > 0 && Math.abs(height - space.current) < 0.5) return;
    space.current = height;
    // The reservation changes on stream frames. An inherited variable would
    // restyle every turn below it; padding changes only the wrapper's box.
    const content = contentRef.current;
    if (content) content.style.paddingBottom = height > 0 ? `${height}px` : "";
  }, [contentRef]);
  const naturalHeight = useCallback(() => {
    // scrollHeight is floored at clientHeight, so it cannot measure short
    // first turns. The content wrapper includes the tail but not that floor.
    const lead = Math.max(0, Number.parseFloat(contentRef.current?.style.paddingTop || "0") || 0);
    return Math.max(0, (contentRef.current?.getBoundingClientRect().height ?? 0) - space.current - lead);
  }, [contentRef]);
  const syncLayout = useCallback(() => {
    // Child layout effects may report stream frames before our thread restore.
    // Never save the outgoing reservation under the incoming thread's ID.
    if (!enabled || layoutScope.current?.threadID !== threadID || !layoutScope.current?.enabled) return;
    const natural = extent.current ? naturalHeight() : 0;
    // Opening existing details may temporarily fill the gap. Retain the
    // reservation until non-disclosure growth fills it, so closing restores
    // only the space that genuine output or browsing has not consumed.
    if (extent.current && natural >= extent.current) {
      const outputHeight = natural - conversationDisclosureHeight(contentRef.current) + reservedDisclosureHeight.current;
      if (outputHeight >= extent.current) extent.current = 0;
    }
    apply(Math.max(0, extent.current - natural));
    if (threadID) {
      // Keep the output baseline after the gap is consumed. A thread switch
      // must not turn attachment-only growth into response growth.
      if (extent.current || savedExtents.current.has(threadID)) {
        savedExtents.current.set(threadID, { extent: extent.current, natural: reservedNaturalHeight.current, disclosure: reservedDisclosureHeight.current });
      }
    }
  }, [apply, contentRef, enabled, naturalHeight, threadID]);

  const reserve = useCallback((targetScrollTop: number, messageHeight: number) => {
    if (!enabled) return;
    const viewportHeight = viewportRef.current?.clientHeight ?? 0;
    // Reserve only the actual visible gap. Extra padding beyond it would delay
    // following until output had already disappeared below the viewport.
    extent.current = targetScrollTop + viewportHeight;
    reservedNaturalHeight.current = naturalHeight() - messageHeight;
    reservedDisclosureHeight.current = conversationDisclosureHeight(contentRef.current);
    if (threadID) savedExtents.current.set(threadID, { extent: extent.current, natural: reservedNaturalHeight.current, disclosure: reservedDisclosureHeight.current });
    syncLayout();
  }, [contentRef, enabled, naturalHeight, syncLayout, threadID, viewportRef]);

  const consume = useCallback((distance: number) => {
    if (distance <= 0 || !extent.current) return;
    // Only remove space already scrolled out of view; never clamp the user's
    // viewport to a new bottom or accidentally re-arm following.
    const node = viewportRef.current;
    const offscreen = node ? Math.max(0, node.scrollHeight - node.clientHeight - node.scrollTop - 24) : 0;
    extent.current -= Math.min(distance, offscreen, space.current);
    syncLayout();
  }, [syncLayout, viewportRef]);

  const ensureRange = useCallback((targetScrollTop: number) => {
    const node = viewportRef.current;
    if (!enabled || !node) return;
    // Track the placement, including an earlier receipt collapsing. Keeping
    // the old extent after its target moved would leave extra offscreen space
    // and delay resuming follow. This only runs while placement owns scrolling.
    extent.current = targetScrollTop + node.clientHeight;
    syncLayout();
  }, [enabled, syncLayout, viewportRef]);
  // Intrinsic attachment resizing is not response growth and must not take
  // over the viewport just because it consumed the remaining blank space.
  const filled = useCallback((messageHeight: number) => extent.current === 0 && space.current === 0 &&
    naturalHeight() - conversationDisclosureHeight(contentRef.current) + reservedDisclosureHeight.current - messageHeight >
      reservedNaturalHeight.current + 1, [contentRef, naturalHeight]);

  const discard = useCallback((targetThreadID?: string) => {
    if (targetThreadID) savedExtents.current.delete(targetThreadID);
    if (targetThreadID !== layoutScope.current?.threadID) return;
    extent.current = 0;
    reservedNaturalHeight.current = 0;
    reservedDisclosureHeight.current = 0;
    apply(0);
  }, [apply]);

  useLayoutEffect(() => {
    if (layoutScope.current?.threadID === threadID && layoutScope.current?.enabled === enabled) return;
    layoutScope.current = { threadID, enabled };
    restoredOffset.current = 0;
    if (!preserveOnThreadChange) {
      const saved = threadID ? savedExtents.current.get(threadID) : undefined;
      // First-query lead is in-flight motion on the outgoing thread, not a
      // saved reservation. Drop it before measuring so the incoming pane does
      // not inherit a composer-sized padding-top for one layout.
      contentRef.current?.style.removeProperty("padding-top");
      // Rebase before measuring growth: a different history window moves the
      // submission without producing output or creating new trailing space.
      const offset = getRestorationOffset();
      restoredOffset.current = offset;
      extent.current = saved?.extent ? Math.max(0, saved.extent + offset) : 0;
      reservedNaturalHeight.current = Math.max(0, (saved?.natural ?? 0) + offset);
      reservedDisclosureHeight.current = saved?.disclosure ?? 0;
    }
    if (!enabled) {
      extent.current = 0;
      apply(0);
    }
    // Incoming content inherits outgoing padding. Measure before replacing it,
    // and restore the extent before the parent restores scrollTop.
    syncLayout();
  });

  // Completion and status removal do not own the submission's reading space.
  // Content growth consumes it even when auto-follow has been paused.
  useLayoutEffect(syncLayout);

  return { reserve, ensureRange, filled, consume, syncLayout, discard, restoredOffset };
}
