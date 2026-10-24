import { useCallback, useEffect, useLayoutEffect, useRef, useSyncExternalStore } from "react";
import type { Thread, ThreadItem } from "../shared/protocol";
import type { ConversationSearchApplication } from "./ConversationSearchState";
import { showErrorToast } from "./Toast";

/** The host locates and hydrates; this shell reserves only its own reading intent. */
export function useConversationSearchNavigation({
  application, thread, switching, captureConversationScrollIntent, jumpToConversationMessage,
}: {
  application: ConversationSearchApplication;
  thread?: Thread;
  switching: boolean;
  captureConversationScrollIntent: (options?: { forHandoff?: boolean }) => () => boolean;
  jumpToConversationMessage: (turnID: string, item: ThreadItem, query?: string) => () => void;
}): void {
  const target = useSyncExternalStore(application.subscribe, () => application.getSnapshot().search.target);
  const intent = useRef<{ id: string; isCurrent: () => boolean } | undefined>(undefined);
  const cancelJump = useRef<(() => void) | undefined>(undefined);
  const finish = useCallback((targetId: string) => {
    void application.invoke("search.complete-target", { targetId }).catch(showErrorToast);
  }, [application]);
  const cancelPendingJump = useCallback(() => {
    cancelJump.current?.();
    cancelJump.current = undefined;
  }, []);

  useLayoutEffect(() => {
    if (!target || intent.current?.id !== target.id) intent.current = undefined;
    if (target && !intent.current && thread?.id === target.threadId) {
      cancelPendingJump();
      intent.current = { id: target.id, isCurrent: captureConversationScrollIntent() };
    }
    if (target && intent.current && !intent.current.isCurrent()) {
      cancelPendingJump();
      finish(target.id);
    }
  }, [target, thread?.id, captureConversationScrollIntent, cancelPendingJump, finish]);

  useEffect(() => {
    // Publish reader cancellation before a replacement shell can bind a new
    // viewport to the same pending address. The viewport handles input first.
    const checkReadingIntent = () => {
      const current = intent.current;
      if (current && !current.isCurrent()) finish(current.id);
    };
    const events = ["wheel", "pointerdown", "keydown", "touchstart"] as const;
    for (const event of events) window.addEventListener(event, checkReadingIntent, { passive: true });
    return () => { for (const event of events) window.removeEventListener(event, checkReadingIntent); };
  }, [finish]);

  useEffect(() => cancelPendingJump, [thread?.id, switching, cancelPendingJump]);
  useEffect(() => {
    if (!target) return;
    if (target.phase === "failed") {
      if (target.error) showErrorToast(new Error(target.error));
      finish(target.id);
      return;
    }
    if (target.phase === "not-found") { finish(target.id); return; }
    if (target.phase !== "ready" || switching) return;
    if (!intent.current?.isCurrent() || thread?.id !== target.threadId) { finish(target.id); return; }
    const turn = thread.turns.find(turn => turn.id === target.turnId);
    const item = turn?.items.find(item => item.id === target.itemId);
    if (!turn || !item) return;
    // Reserve the local layout retry before acknowledging the durable address.
    cancelJump.current = jumpToConversationMessage(turn.id, item, target.query);
    finish(target.id);
  }, [target, thread, switching, jumpToConversationMessage, finish]);
}
