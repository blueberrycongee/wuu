import { useLayoutEffect, useRef, type RefObject } from "react";
import type { ChannelMessage } from "../shared/protocol";
import { useMessageArrivalMotion } from "./useMessageArrivalMotion";

export function useChannelMessageMotion(
  scrollRef: RefObject<HTMLDivElement | null>,
  roomID: string,
  ready: boolean,
  messages: readonly Pick<ChannelMessage, "id" | "seq">[],
  pendingID?: string,
): (pendingID: string, messageID: string) => void {
  const previous = useRef({ roomID, ready: false, seq: 0 });
  const { reconcile, acknowledge } = useMessageArrivalMotion();

  useLayoutEffect(() => {
    const last = previous.current;
    const baseline = last.roomID !== roomID || !last.ready || !ready;
    const seqByID = new Map(messages.map(message => [message.id, message.seq]));
    const arrivals = Array.from(scrollRef.current?.querySelectorAll<HTMLElement>("[data-message-id]") ?? []).map(element => {
      const id = element.dataset.messageId!;
      return { id, element, own: element.classList.contains("own"), fresh: id === pendingID || (seqByID.get(id) ?? 0) > last.seq };
    });
    reconcile(arrivals, baseline);
    previous.current = { roomID, ready, seq: Math.max(baseline ? 0 : last.seq, ...messages.map(message => message.seq)) };
  }, [messages, pendingID, ready, roomID, scrollRef, reconcile]);

  return acknowledge;
}
