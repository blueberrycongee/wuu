import { useCallback, useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import type { ChannelMessage } from "../shared/protocol";
import { motionDurationMs, prefersReducedMotion } from "./motion";

type Arrival = { element: HTMLElement; animation?: Animation };

export function useChannelMessageMotion(
  scrollRef: RefObject<HTMLDivElement | null>,
  roomID: string,
  ready: boolean,
  messages: readonly ChannelMessage[],
  pendingID?: string,
): (pendingID: string, messageID: string) => void {
  const previous = useRef({ roomID, ready: false, seq: 0, rows: new Map<string, Arrival>() });
  const handoffs = useRef(new Map<string, Animation | undefined>());
  const running = useRef(new Set<Animation>());

  useLayoutEffect(() => {
    const last = previous.current;
    const baseline = last.roomID !== roomID || !last.ready || !ready;
    const canAnimate = !baseline && !document.hidden && !prefersReducedMotion();
    const seqByID = new Map(messages.map(message => [message.id, message.seq]));
    const rows = new Map<string, Arrival>();
    const duration = motionDurationMs("--motion-slow", 280);
    const easing = getComputedStyle(document.documentElement).getPropertyValue("--ease-out").trim() || "cubic-bezier(0.16, 1, 0.3, 1)";
    for (const element of scrollRef.current?.querySelectorAll<HTMLElement>("[data-message-id]") ?? []) {
      const id = element.dataset.messageId!;
      const old = last.rows.get(id);
      if (!baseline && old?.element === element) { rows.set(id, old); continue; }
      const arrival: Arrival = { element };
      rows.set(id, arrival);
      const acknowledged = handoffs.current.has(id);
      const source = handoffs.current.get(id);
      // A fast acknowledgement replaces the optimistic row before its entrance
      // finishes. Continue from that frame instead of blinking or replaying it.
      const elapsed = source && source.playState !== "finished" && source.playState !== "idle"
        && typeof source.currentTime === "number" ? source.currentTime : undefined;
      const fresh = id === pendingID || (seqByID.get(id) ?? 0) > last.seq;
      if (!canAnimate || duration <= 0 || !element.animate || (acknowledged ? elapsed === undefined : !fresh)) continue;
      const own = element.classList.contains("own");
      const animation = element.animate([
        { opacity: 0, transform: own ? "translateY(12px) scale(.985)" : "translateY(8px)", transformOrigin: own ? "right bottom" : "left top" },
        { opacity: 1, transform: "none", transformOrigin: own ? "right bottom" : "left top" },
      ], { duration, easing });
      if (elapsed !== undefined) animation.currentTime = elapsed;
      arrival.animation = animation;
      running.current.add(animation);
      const release = () => running.current.delete(animation);
      animation.addEventListener("finish", release, { once: true });
      animation.addEventListener("cancel", release, { once: true });
    }
    for (const [id, old] of last.rows) {
      if (rows.get(id) !== old) { old.animation?.cancel(); if (old.animation) running.current.delete(old.animation); }
    }
    handoffs.current.clear();
    previous.current = { roomID, ready, seq: Math.max(baseline ? 0 : last.seq, ...messages.map(message => message.seq)), rows };
  }, [messages, pendingID, ready, roomID, scrollRef]);

  useEffect(() => {
    const cancel = () => { for (const animation of running.current) animation.cancel(); running.current.clear(); };
    const media = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    const reduce = () => { if (media?.matches) cancel(); };
    const hide = () => { if (document.hidden) cancel(); };
    media?.addEventListener("change", reduce);
    document.addEventListener("visibilitychange", hide);
    return () => { cancel(); media?.removeEventListener("change", reduce); document.removeEventListener("visibilitychange", hide); };
  }, []);

  return useCallback((pending: string, message: string) => {
    const arrival = previous.current.rows.get(pending);
    if (arrival) handoffs.current.set(message, arrival.animation);
  }, []);
}
