import { useCallback, useEffect, useRef } from "react";
import { cubicBezier, messageMotionTime, prefersReducedMotion, subscribeReducedMotion } from "./motion";

// Motionbook's chatgpt-dot-send study: https://github.com/blueberrycongee/motionbook/tree/main/examples/chatgpt-dot-send
// Keep the travel and shape clocks shared with the history placement underneath it.
export const SEND_TRAVEL_MS = 600;
export const SEND_SHAPE_MS = 190;
export const SEND_SHAPE_CURVE = "cubic-bezier(0.73, 0.42, 0.54, 1)";
const shapeEase = cubicBezier(0.73, 0.42, 0.54, 1);
const compressEase = cubicBezier(0.65, 0, 0.35, 1);
const recoverEase = cubicBezier(0.19, 0, 0.48, 1);

type Placement = { origin: DOMRect; offset: number; animation: Animation };
type Flight = { x: number; y: number; extra: number; width: number; height: number; offset: number };
type Arrival = { element: HTMLElement; animation?: Animation; flight?: Flight; stop?: () => void };
type MessageArrival = { id: string; element: HTMLElement; fresh: boolean; placement?: Placement };

/** Entrance and optimistic-DOM handoff only; callers own history and scrolling. */
export function useMessageArrivalMotion() {
  const rows = useRef(new Map<string, Arrival>());
  const handoffs = useRef(new Map<string, Arrival>());
  const running = useRef(new Set<() => void>());
  const cancel = useCallback(() => {
    for (const stop of running.current) stop();
    running.current.clear();
  }, []);
  const acknowledge = useCallback((pending: string, message: string) => {
    const arrival = rows.current.get(pending);
    if (arrival) handoffs.current.set(message, arrival);
  }, []);

  const reconcile = useCallback((messages: readonly MessageArrival[], reset = false) => {
    const next = new Map<string, Arrival>();
    const canAnimate = !reset && !document.hidden && !prefersReducedMotion();
    for (const { id, element, fresh, placement } of messages) {
      const old = reset ? undefined : rows.current.get(id);
      if (old?.element === element) { next.set(id, old); continue; }
      const arrival: Arrival = { element };
      next.set(id, arrival);
      const source = reset ? undefined : handoffs.current.get(id) ?? old;
      if (source?.element === element) { next.set(id, source); continue; }
      const animation = source?.animation;
      const elapsed = animation && animation.playState !== "finished" && animation.playState !== "idle"
        && typeof animation.currentTime === "number" ? animation.currentTime : undefined;
      // A remount continues the same flight; history and completed flights never replay.
      if (!canAnimate || !element.animate || (source ? elapsed === undefined : !fresh)) continue;
      const bubble = element.querySelector<HTMLElement>(".user-message");
      let flight = source?.flight;
      if (!flight && placement && element.offsetWidth > 0) {
        const body = element.getBoundingClientRect();
        const zoom = body.width / element.offsetWidth;
        const origin = placement.origin;
        flight = {
          x: (origin.right - body.right) / zoom,
          // Tall cards still leave from the input's top, never from above the viewport.
          y: (origin.top - body.top) / zoom + placement.offset,
          extra: bubble ? Math.max(0, (origin.width - bubble.getBoundingClientRect().width) / zoom) : 0,
          width: body.width / zoom,
          height: body.height / zoom,
          offset: placement.offset,
        };
      }
      arrival.flight = flight;
      const tracks: Animation[] = [];
      const startTime = source?.animation?.startTime ?? placement?.animation.startTime ?? messageMotionTime();
      const play = (target: HTMLElement, frames: Keyframe[], options: KeyframeAnimationOptions): Animation => {
        const track = target.animate(frames, options);
        if (elapsed !== undefined) track.currentTime = elapsed;
        else if (typeof startTime === "number") track.startTime = startTime;
        tracks.push(track);
        return track;
      };
      const restore: Array<() => void> = [];
      if (flight) {
        element.setAttribute("data-send-flight", "");
        restore.push(() => element.removeAttribute("data-send-flight"));
        const { x, y, width, height, extra, offset } = flight;
        // Distance-dependent damping limits overshoot to about 3 CSS pixels.
        const distance = Math.hypot(x, y);
        const log = -Math.log(Math.min(3 / Math.max(3.01, distance), 0.99));
        const damping = log / Math.hypot(Math.PI, log);
        const omega = Math.sqrt(140);
        const frequency = omega * Math.sqrt(1 - damping * damping);
        const spring = (ms: number): number => 1 - Math.exp(-damping * omega * ms / 1000)
          * (Math.cos(frequency * ms / 1000) + damping * omega / frequency * Math.sin(frequency * ms / 1000));
        let naturalMs = 0;
        while (naturalMs < 2000) {
          naturalMs += 50;
          const displacement = Math.abs(1 - spring(naturalMs)) * 100;
          const speed = Math.abs((spring(naturalMs + 1) - spring(naturalMs)) * 100000);
          if (displacement <= 0.5 && speed <= 15) break;
        }
        const size = Math.min(1, Math.max(width, height) / 360);
        const frames = Array.from({ length: 61 }, (_, index): Keyframe => {
          const phase = index / 60;
          const travel = index === 60 ? 1 : spring(naturalMs * phase);
          const compression = phase < 1 / 3 ? compressEase(phase * 3)
            : phase < 0.9 ? 1 - recoverEase((phase - 1 / 3) / (0.9 - 1 / 3)) : 0;
          // Cancel the history's FLIP on this child, so its path is independent of scroll distance.
          const history = offset * (1 - shapeEase(Math.min(1, phase * SEND_TRAVEL_MS / SEND_SHAPE_MS)));
          return {
            offset: phase,
            transform: `translate(${x * (1 - travel)}px, ${y * (1 - travel) - history}px) scale(${1 - Math.min(0.5, 18 * size / width) * compression}, ${1 - Math.min(0.5, 4 * size / height) * compression})`,
            transformOrigin: "right top",
          };
        });
        arrival.animation = play(element, frames, { duration: SEND_TRAVEL_MS, easing: "linear" });
        if (bubble && extra > 0 && (elapsed ?? 0) < SEND_SHAPE_MS) {
          const style = getComputedStyle(bubble);
          const properties: Record<string, string> = {
            "--send-background": style.background,
            "--send-border": style.border,
            "--send-shadow": style.boxShadow,
            "--send-inset": `-${style.borderTopWidth} -${style.borderRightWidth} -${style.borderBottomWidth} -${style.borderLeftWidth}`,
          };
          for (const [key, value] of Object.entries(properties)) bubble.style.setProperty(key, value);
          bubble.setAttribute("data-send-shaping", "");
          const clearShape = () => {
            bubble.removeAttribute("data-send-shaping");
            for (const key of Object.keys(properties)) bubble.style.removeProperty(key);
          };
          restore.push(clearShape);
          // Only the decorative surface changes width. Text keeps its final wrapping throughout.
          const shape = play(bubble, [{ left: `${-extra - parseFloat(style.borderLeftWidth)}px` }, { left: `-${style.borderLeftWidth}` }], {
            duration: SEND_SHAPE_MS, easing: SEND_SHAPE_CURVE, pseudoElement: "::before",
          });
          for (const child of Array.from(bubble.children)) {
            if (child instanceof HTMLElement) play(child, [{ translate: `${-extra}px 0` }, { translate: "0px 0px" }], {
              duration: SEND_SHAPE_MS, easing: SEND_SHAPE_CURVE,
            });
          }
          shape.addEventListener("finish", clearShape, { once: true });
        }
      } else {
        arrival.animation = play(element, [{ opacity: 0.85 }, { opacity: 1 }], { duration: 180, easing: "ease-out" });
      }
      const stop = () => {
        for (const track of tracks) track.cancel();
        for (const release of restore) release();
        running.current.delete(stop);
      };
      arrival.stop = stop;
      running.current.add(stop);
      arrival.animation.addEventListener("finish", stop, { once: true });
    }
    const retained = new Set(next.values());
    for (const old of rows.current.values()) if (!retained.has(old)) old.stop?.();
    handoffs.current.clear();
    rows.current = next;
  }, []);

  useEffect(() => {
    const stopReducedMotion = subscribeReducedMotion((reduced) => { if (reduced) cancel(); });
    const hide = () => { if (document.hidden) cancel(); };
    document.addEventListener("visibilitychange", hide);
    return () => { cancel(); stopReducedMotion(); document.removeEventListener("visibilitychange", hide); };
  }, [cancel]);

  return { reconcile, acknowledge, cancel };
}
