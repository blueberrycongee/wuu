import type { ReactNode } from "react";
import type { ThreadItem } from "../../shared/protocol";
import { useConversationControls, useConversationTurnIdentity } from "../ConversationControlsContext";
export { toConversationProcessSnapshot } from "./ApplicationSnapshots";

export interface ConversationProcessPresentationProps {
  processItems: readonly ThreadItem[];
  fallback: ReactNode;
}

export function ConversationProcessPresentation({ processItems, fallback }: ConversationProcessPresentationProps): JSX.Element {
  const { slots } = useConversationControls();
  const { threadId, turnId } = useConversationTurnIdentity();
  return <slots.Slot name="wuu.default-conversation/conversation.process"
    context={{ threadId, turnId, itemIds: processItems.map(item => item.id) }} fallback={fallback} />;
}
