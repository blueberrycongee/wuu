import type { ReactNode } from "react";
import type { ThreadItem } from "../../shared/protocol";
import { useConversationControls, useConversationTurnIdentity } from "../ConversationControlsContext";

export interface ConversationProcessPresentationProps {
  processItems: readonly ThreadItem[];
  fallback: ReactNode;
}

export function ConversationProcessPresentation({ processItems, fallback }: ConversationProcessPresentationProps): JSX.Element {
  const { slots, slotOwner } = useConversationControls();
  const { threadId, turnId } = useConversationTurnIdentity();
  return <slots.Slot name={`${slotOwner}/conversation.process`}
    context={{ threadId, turnId, itemIds: processItems.map(item => item.id) }} fallback={fallback} />;
}
