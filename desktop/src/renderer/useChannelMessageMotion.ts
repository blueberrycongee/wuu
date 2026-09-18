import { useCallback, useLayoutEffect, useRef, type RefObject } from "react";
import type { ChannelMessage } from "../shared/protocol";
import { useMessageArrivalMotion } from "./useMessageArrivalMotion";

export function useChannelMessageMotion(
  scrollRef: RefObject<HTMLDivElement | null>,
  roomID: string,
  ready: boolean,
  messages: readonly Pick<ChannelMessage, "id" | "seq">[],
  pendingID?: string,
  onArrival?: (localSend: boolean) => void,
): (pendingID: string, messageID: string) => void {
  const previous = useRef({ roomID, ready: false, seq: 0 });
  const localAcknowledgements = useRef(new Set<string>());
  const { reconcile, acknowledge, cancel } = useMessageArrivalMotion();
  const acknowledgeLocalSend = useCallback((pending: string, message: string) => {
    acknowledge(pending, message);
    localAcknowledgements.current.add(message);
  }, [acknowledge]);

  useLayoutEffect(() => {
    const last = previous.current;
    const baseline = last.roomID !== roomID || !last.ready || !ready;
    const seqByID = new Map(messages.map(message => [message.id, message.seq]));
    const arrivals = Array.from(scrollRef.current?.querySelectorAll<HTMLElement>("[data-message-id]") ?? []).map(element => {
      const id = element.dataset.messageId!;
      return { id, element, own: element.classList.contains("own"), fresh: id === pendingID || (seqByID.get(id) ?? 0) > last.seq };
    });
    const fresh = reconcile(arrivals, baseline);
    // Only a local send may resume a paused reader, not an incoming human row.
    // Include sends acknowledged before their optimistic row could mount.
    if (fresh.length) onArrival?.(fresh.some(arrival => arrival.id === pendingID || localAcknowledgements.current.has(arrival.id)));
    localAcknowledgements.current.clear();
    previous.current = { roomID, ready, seq: Math.max(baseline ? 0 : last.seq, ...messages.map(message => message.seq)) };
  }, [messages, pendingID, ready, roomID, scrollRef, reconcile, onArrival]);

  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (!node) return;
    const keydown = (event: KeyboardEvent) => {
      if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(event.key)) cancel();
    };
    node.addEventListener("wheel", cancel, { passive: true });
    node.addEventListener("pointerdown", cancel);
    node.addEventListener("touchstart", cancel, { passive: true });
    node.addEventListener("keydown", keydown);
    return () => {
      cancel();
      node.removeEventListener("wheel", cancel);
      node.removeEventListener("pointerdown", cancel);
      node.removeEventListener("touchstart", cancel);
      node.removeEventListener("keydown", keydown);
    };
  }, [cancel, ready, roomID, scrollRef]);

  return acknowledgeLocalSend;
}
