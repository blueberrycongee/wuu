import { useCallback, useSyncExternalStore, type ReactNode } from "react";
import { streamTextKey, streamTextStore } from "../StreamText";
import { useI18n } from "../i18n";
import { toConversationProcessSnapshot } from "./ApplicationSnapshots";
import type { ThreadItem } from "../../shared/protocol";
import { useConversationControls, useConversationTurnIdentity } from "../ConversationControlsContext";

export interface ConversationProcessPresentationProps {
  processItems: readonly ThreadItem[];
  streaming: boolean;
  active: boolean;
  fallback: ReactNode;
}

export function ConversationProcessPresentation({ processItems, streaming, active, fallback }: ConversationProcessPresentationProps): JSX.Element {
  const { slots, slotOwner } = useConversationControls();
  const { threadId, turnId } = useConversationTurnIdentity();
  const { locale } = useI18n();
  const subscribe = useCallback((listener: () => void) => {
    const releases = streaming && turnId ? processItems.filter(item => item.type === "reasoning")
      .map(item => streamTextStore.subscribe(streamTextKey(turnId, item.id, "text"), listener)) : [];
    return () => releases.forEach(release => release());
  }, [processItems, streaming, turnId]);
  const revision = useCallback(() => turnId ? streamTextStore.getTurnRevision(turnId) : 0, [turnId]);
  useSyncExternalStore(subscribe, revision, revision);
  const items = processItems.map(item => {
    if (!streaming || !turnId || item.type !== "reasoning") return item;
    const key = streamTextKey(turnId, item.id, "text");
    return streamTextStore.has(key) ? { ...item, text: streamTextStore.get(key) } : item;
  });
  const snapshot = toConversationProcessSnapshot(items, streaming, active, locale);
  // Compaction notices are not represented by this payload; keep them visible.
  if (!snapshot) return <>{fallback}</>;
  return <slots.Slot name={`${slotOwner}/conversation.process`}
    context={{ threadId, turnId, itemIds: processItems.map(item => item.id), snapshot }} fallback={fallback} />;
}
