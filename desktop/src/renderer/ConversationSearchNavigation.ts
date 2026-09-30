import { useEffect, useRef, useState } from "react";
import type { Thread, ThreadSearchResultItem } from "../shared/protocol";
import { conversationSearchPattern } from "./ConversationSearchDisplay";
import { scrollToConversationMessage } from "./TurnViewHelpers";
import { showErrorToast } from "./Toast";

// Activation can render a cached snapshot before its resume completes. Keep the
// search address until that snapshot is reconciled, then reveal its actual item.
export function useConversationSearchNavigation({
  thread, switching, activateThread, disableAutoFollow,
}: {
  thread?: Thread;
  switching: boolean;
  activateThread: (id: string) => Promise<void>;
  disableAutoFollow: () => void;
}): (result: ThreadSearchResultItem, query: string) => void {
  const [target, setTarget] = useState<{
    result: ThreadSearchResultItem; query: string; ready: boolean;
  }>();
  const handled = useRef<typeof target>(undefined);
  const loadingPage = useRef("");
  const cancelJump = useRef<(() => void) | undefined>(undefined);

  // Streaming snapshots must not cancel retries while an old turn mounts.
  // Only a destination change or unmount abandons the pending jump.
  useEffect(() => () => cancelJump.current?.(), [target, thread?.id, switching]);

  useEffect(() => {
    if (!target?.ready || switching || handled.current === target) return;
    if (thread?.id !== target.result.thread.id) {
      handled.current = target;
      return;
    }
    const seq = target.result.message_seq;
    const turn = thread.turns.find(turn => turn.items.some(item => item.seq === seq));
    if (!turn) {
      const cursor = thread.history_cursor;
      if (cursor && window.wuu.loadEarlierThreadHistory && loadingPage.current !== cursor) {
        loadingPage.current = cursor;
        void window.wuu.loadEarlierThreadHistory(thread.id, cursor).catch(error => {
          handled.current = target;
          showErrorToast(error);
        });
      }
      if (!cursor) handled.current = target;
      return;
    }
    const items = turn.items.filter(item => item.seq === seq);
    const item = items.find(item =>
      (item.type === "user_message" || item.type === "agent_message") &&
      conversationSearchPattern(target.query)?.test(item.text ?? ""));
    // Technical records and metadata remain searchable, but do not pretend a
    // tool/reasoning hit is a match in the user's prompt or the final answer.
    if (!item) {
      handled.current = target;
      return;
    }
    disableAutoFollow();
    const frame = window.requestAnimationFrame(() => {
      disableAutoFollow();
      cancelJump.current = scrollToConversationMessage(turn.id, item);
      handled.current = target;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [target, thread, switching, disableAutoFollow]);

  return (result, query) => {
    loadingPage.current = "";
    const next = result.message_seq ? { result, query, ready: false } : undefined;
    setTarget(next);
    void activateThread(result.thread.id).then(() => {
      setTarget(current => current === next && current ? { ...current, ready: true } : current);
    });
  };
}
