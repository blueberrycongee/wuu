import { useEffect, useLayoutEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import type { Thread, ThreadItem, ThreadSearchResultItem } from "../shared/protocol";
import { conversationSearchPattern } from "./ConversationSearchDisplay";
import { showErrorToast } from "./Toast";
import { updateThreadByID, updateTurnItem, type AppState } from "./AppState";

// Activation can render a cached snapshot before its resume completes. Keep the
// search address until that snapshot is reconciled, then reveal its actual item.
export function useConversationSearchNavigation({
  thread, switching, activateThread, captureConversationScrollIntent, jumpToConversationMessage, setAppState,
}: {
  thread?: Thread;
  switching: boolean;
  activateThread: (id: string) => Promise<void>;
  captureConversationScrollIntent: () => () => boolean;
  jumpToConversationMessage: (turnID: string, item: ThreadItem, query?: string) => () => void;
  setAppState: Dispatch<SetStateAction<AppState>>;
}): (result: ThreadSearchResultItem, query: string) => void {
  const [target, setTarget] = useState<{
    id: number; result: ThreadSearchResultItem; query: string; ready: boolean;
  }>();
  const nextTargetID = useRef(0);
  const targetIntent = useRef<{ id: number; isCurrent: () => boolean } | undefined>(undefined);
  const handled = useRef<typeof target>(undefined);
  const loadingPage = useRef("");
  const cancelJump = useRef<(() => void) | undefined>(undefined);
  const loadingContent = useRef("");
  const currentDestination = useRef({ target, threadID: thread?.id });
  currentDestination.current = { target, threadID: thread?.id };

  useLayoutEffect(() => {
    if (!target || targetIntent.current?.id !== target.id) targetIntent.current = undefined;
    // Bind on arrival, before activation or remote message content finishes.
    // Leaving and returning must not make an old search a new reading intent.
    if (target && !targetIntent.current && thread?.id === target.result.thread.id) {
      targetIntent.current = { id: target.id, isCurrent: captureConversationScrollIntent() };
    }
    if (targetIntent.current && !targetIntent.current.isCurrent()) {
      cancelJump.current?.();
      setTarget(undefined);
    }
  }, [target?.id, thread?.id, captureConversationScrollIntent]);

  // Streaming snapshots must not cancel retries while an old turn mounts.
  // Only a destination change or unmount abandons the pending jump.
  useEffect(() => () => cancelJump.current?.(), [target, thread?.id, switching]);

  useEffect(() => {
    if (!target?.ready || switching || handled.current === target) return;
    if (!targetIntent.current?.isCurrent()) { handled.current = target; return; }
    if (thread?.id !== target.result.thread.id) {
      handled.current = target;
      return;
    }
    const seq = target.result.message_seq;
    const turn = thread.turns.find(turn => turn.items.some(item => item.seq === seq &&
      (item.type === "user_message" || item.type === "agent_message")));
    if (!turn) {
      // A page can contain only the trailing tool items of an assistant
      // message. They share its sequence; its text can be on the prior page.
      const passedAddress = thread.turns.some(turn => turn.items.some(item =>
        item.seq !== undefined && seq !== undefined && item.seq < seq));
      const cursor = passedAddress ? undefined : thread.history_cursor;
      if (cursor && window.wuu.loadEarlierThreadHistory && loadingPage.current !== cursor) {
        loadingPage.current = cursor;
        void window.wuu.loadEarlierThreadHistory(thread.id, cursor).catch(error => {
          if (currentDestination.current.target !== target || currentDestination.current.threadID !== thread.id || !targetIntent.current?.isCurrent()) return;
          handled.current = target;
          showErrorToast(error);
        });
      }
      if (!cursor) handled.current = target;
      return;
    }
    const items = turn.items.filter(item => item.seq === seq);
    const preview = items.find(item =>
      (item.type === "user_message" || item.type === "agent_message") && item.remote_content_ref);
    if (preview?.remote_content_ref && window.wuu.readRemoteItem) {
      const ref = preview.remote_content_ref;
      if (loadingContent.current === ref) return;
      loadingContent.current = ref;
      const stillCurrent = () => currentDestination.current.target === target &&
        currentDestination.current.threadID === thread.id && Boolean(targetIntent.current?.isCurrent());
      // A full-text hit can occur beyond the history page's leading preview.
      // Hydrate only the addressed message and wait for its rendered snapshot.
      void window.wuu.readRemoteItem(ref).then(complete => {
        if (!stillCurrent()) return;
        if (complete.id !== preview.id || complete.type !== preview.type) {
          throw new Error("Content changed during download");
        }
        setAppState(current => updateThreadByID(current, thread.id, currentThread =>
          updateTurnItem(currentThread, turn.id, preview.id, item =>
            item.remote_content_ref === ref ? complete : item)));
      }).catch(error => {
        if (!stillCurrent()) return;
        handled.current = target;
        showErrorToast(error);
      });
      return;
    }
    const item = items.find(item =>
      (item.type === "user_message" || item.type === "agent_message") &&
      conversationSearchPattern(target.query)?.test(item.text ?? ""));
    // Technical records and metadata remain searchable, but do not pretend a
    // tool/reasoning hit is a match in the user's prompt or the final answer.
    if (!item) {
      handled.current = target;
      return;
    }
    // The controller reserves ownership now; its helper waits for item layout.
    cancelJump.current = jumpToConversationMessage(turn.id, item, target.query);
    handled.current = target;
  }, [target, thread, switching, jumpToConversationMessage, setAppState]);

  return (result, query) => {
    loadingPage.current = "";
    loadingContent.current = "";
    const next = result.message_seq ? { id: ++nextTargetID.current, result, query, ready: false } : undefined;
    setTarget(next);
    void activateThread(result.thread.id).then(() => {
      setTarget(current => current === next && current ? { ...current, ready: true } : current);
    });
  };
}
