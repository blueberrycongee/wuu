import { createContext, useContext, useMemo, type ReactNode } from "react";
import type { ApplicationServiceV1, UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";

type ConversationControls = { application: ApplicationServiceV1; slots: UICompositionV1 };
const ConversationControlsContext = createContext<ConversationControls | undefined>(undefined);

/** Selected application and composition authority; no conversation state is copied here. */
export function ConversationControlsProvider({ application, slots, children }: ConversationControls & { children: ReactNode }): JSX.Element {
  const value = useMemo(() => ({ application, slots }), [application, slots]);
  return <ConversationControlsContext.Provider value={value}>{children}</ConversationControlsContext.Provider>;
}

export function useConversationControls(): ConversationControls {
  const controls = useContext(ConversationControlsContext);
  if (!controls) throw new Error("Conversation controls require the selected application and composition");
  return controls;
}


const ConversationTurnIdentityContext = createContext<{ threadId?: string; turnId?: string }>({});
export function ConversationTurnIdentityProvider({ threadId, turnId, children }: { threadId?: string; turnId: string; children: ReactNode }): JSX.Element {
  const identity = useMemo(() => ({ threadId, turnId }), [threadId, turnId]);
  return <ConversationTurnIdentityContext.Provider value={identity}>{children}</ConversationTurnIdentityContext.Provider>;
}
export function useConversationTurnIdentity() { return useContext(ConversationTurnIdentityContext); }
