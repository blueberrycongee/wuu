import { useCallback, useEffect, useRef } from "react";
import { messageMotionTime, motionCurve, motionDurationMs, prefersReducedMotion, subscribeReducedMotion } from "./motion";

type Arrival = { element: HTMLElement; animation?: Animation };
/** `settle`: the bubble rises in place instead of travelling from where it
 * was laid out, so it appears where nothing was and fades in from clear. */
type MessageArrival = { id: string; element: HTMLElement; own: boolean; fresh: boolean; settle?: boolean };

/** Entrance and optimistic-DOM handoff only; callers own history and scrolling. */
export function useMessageArrivalMotion() {
  const rows = useRef(new Map<string, Arrival>());
  const handoffs = useRef(new Map<string, Arrival>());
  const running = useRef(new Set<Animation>());
  const cancel = useCallback(() => {
    for (const animation of running.current) animation.cancel();
    running.current.clear();
  }, []);
  const acknowledge = useCallback((pending: string, message: string) => {
    const arrival = rows.current.get(pending);
    if (arrival) handoffs.current.set(message, arrival);
  }, []);

  const reconcile = useCallback((messages: readonly MessageArrival[], reset = false) => {
    const next = new Map<string, Arrival>();
    const freshArrivals: MessageArrival[] = [];
    const startTime = messageMotionTime();
    const arriveDuration = motionDurationMs("--motion-base", 180);
    const settleDuration = motionDurationMs("--motion-slow", 280);
    const easing = motionCurve("--ease-out", "cubic-bezier(0.33, 1, 0.68, 1)");
    const canAnimate = !reset && !document.hidden && !prefersReducedMotion() && arriveDuration > 0;
    for (const { id, element, own, fresh, settle } of messages) {
      const old = reset ? undefined : rows.current.get(id);
      if (old?.element === element) { next.set(id, old); continue; }
      const arrival: Arrival = { element };
      next.set(id, arrival);
      const source = reset ? undefined : handoffs.current.get(id) ?? old;
      if (!reset && !source && fresh) freshArrivals.push({ id, element, own, fresh, settle });
      const animation = source?.animation;
      const elapsed = animation && animation.playState !== "finished" && animation.playState !== "idle"
        && typeof animation.currentTime === "number" ? animation.currentTime : undefined;
      // A remount/acknowledgement continues a live entrance, never a finished
      // or cancelled one. An acknowledgement before the first paint is fresh.
      if (!canAnimate || !element.animate || (source ? elapsed === undefined : !fresh)) continue;
      const entrance = element.animate([
        { opacity: settle ? 0 : 0.85 },
        { opacity: 1 },
      ], { duration: settle ? settleDuration : arriveDuration, easing });
      if (elapsed !== undefined) entrance.currentTime = elapsed;
      else if (startTime !== undefined) entrance.startTime = startTime;
      arrival.animation = entrance;
      running.current.add(entrance);
      const release = () => running.current.delete(entrance);
      entrance.addEventListener("finish", release, { once: true });
      entrance.addEventListener("cancel", release, { once: true });
    }
    for (const [id, old] of rows.current) {
      if (next.get(id) !== old && old.animation) {
        old.animation.cancel();
        running.current.delete(old.animation);
      }
    }
    handoffs.current.clear();
    rows.current = next;
    return freshArrivals;
  }, []);

  useEffect(() => {
    const stopReducedMotion = subscribeReducedMotion((reduced) => { if (reduced) cancel(); });
    const hide = () => { if (document.hidden) cancel(); };
    document.addEventListener("visibilitychange", hide);
    return () => { cancel(); stopReducedMotion(); document.removeEventListener("visibilitychange", hide); };
  }, [cancel]);

  return { reconcile, acknowledge, cancel };
}
