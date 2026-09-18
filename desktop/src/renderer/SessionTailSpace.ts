import { type RefObject, useCallback, useLayoutEffect, useRef } from "react";

/** A submission reserves a minimum content extent, not permanent bottom padding. */
export function useSessionTailSpace({
  threadID, enabled, preserveOnThreadChange, paneRef, viewportRef, contentRef,
}: {
  threadID?: string;
  enabled: boolean;
  preserveOnThreadChange: boolean;
  paneRef: RefObject<HTMLElement | null>;
  viewportRef: RefObject<HTMLElement | null>;
  contentRef: RefObject<HTMLElement | null>;
}) {
  const space = useRef(0);
  const extent = useRef(0);
  const reservedNaturalHeight = useRef(0);
  const savedExtents = useRef(new Map<string, { extent: number; natural: number }>());
  const layoutScope = useRef<{ threadID?: string; enabled: boolean } | undefined>(undefined);
  const apply = useCallback((height: number) => {
    if (height === space.current) return;
    space.current = height;
    paneRef.current?.style.setProperty("--session-tail-space", `${height}px`);
  }, [paneRef]);
  const naturalHeight = useCallback(() => {
    // scrollHeight is floored at clientHeight, so it cannot measure short
    // first turns. The content wrapper includes the tail but not that floor.
    return Math.max(0, (contentRef.current?.getBoundingClientRect().height ?? 0) - space.current);
  }, [contentRef]);
  const syncLayout = useCallback(() => {
    // Child layout effects may report stream frames before our thread restore.
    // Never save the outgoing reservation under the incoming thread's ID.
    if (!enabled || layoutScope.current?.threadID !== threadID || !layoutScope.current?.enabled) return;
    const natural = extent.current ? naturalHeight() : 0;
    if (natural >= extent.current) extent.current = 0;
    apply(Math.max(0, extent.current - natural));
    if (threadID) {
      // Keep the output baseline after the gap is consumed. A thread switch
      // must not turn attachment-only growth into response growth.
      if (extent.current || savedExtents.current.has(threadID)) {
        savedExtents.current.set(threadID, { extent: extent.current, natural: reservedNaturalHeight.current });
      }
    }
  }, [apply, enabled, naturalHeight, threadID]);

  const reserve = useCallback((targetScrollTop: number, messageHeight: number) => {
    if (!enabled) return;
    const viewportHeight = viewportRef.current?.clientHeight ?? 0;
    // Reserve only the actual visible gap. Extra padding beyond it would delay
    // following until output had already disappeared below the viewport.
    extent.current = targetScrollTop + viewportHeight;
    reservedNaturalHeight.current = naturalHeight() - messageHeight;
    if (threadID) savedExtents.current.set(threadID, { extent: extent.current, natural: reservedNaturalHeight.current });
    syncLayout();
  }, [enabled, naturalHeight, syncLayout, threadID, viewportRef]);

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
  const filled = useCallback((messageHeight: number) => space.current === 0 &&
    naturalHeight() - messageHeight > reservedNaturalHeight.current + 1, [naturalHeight]);

  const discard = useCallback((targetThreadID?: string) => {
    if (targetThreadID) savedExtents.current.delete(targetThreadID);
    if (targetThreadID !== layoutScope.current?.threadID) return;
    extent.current = 0;
    reservedNaturalHeight.current = 0;
    apply(0);
  }, [apply]);

  useLayoutEffect(() => {
    if (layoutScope.current?.threadID === threadID && layoutScope.current?.enabled === enabled) return;
    layoutScope.current = { threadID, enabled };
    if (!preserveOnThreadChange) {
      const saved = threadID ? savedExtents.current.get(threadID) : undefined;
      extent.current = saved?.extent ?? 0;
      reservedNaturalHeight.current = saved?.natural ?? 0;
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

  return { reserve, ensureRange, filled, consume, syncLayout, discard };
}
