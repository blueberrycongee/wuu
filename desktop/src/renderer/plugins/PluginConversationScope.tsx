import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationServiceV1, UICompositionV1 } from "../../../../packages/plugin-sdk/src/ui";
import { BrowserControlsProvider } from "../BrowserControlsContext";
import { ConversationControlsProvider } from "../ConversationControlsContext";

const Lifetime = createContext<AbortSignal | undefined>(undefined);
// Independent transports reuse native message presentation, not the selected
// Coding conversation's contributed actions, completions or history authority.
const presentationOnly: UICompositionV1 = { Slot: ({ fallback }) => fallback ?? null };

export function PluginConversationScope({ application, signal, children }: {
  application: ApplicationServiceV1;
  signal: AbortSignal;
  children: ReactNode;
}): JSX.Element {
  return <Lifetime.Provider value={signal}>
    <BrowserControlsProvider service={application.browser}>
      <ConversationControlsProvider application={application} slots={presentationOnly} slotOwner="independent-conversation">
        {children}
      </ConversationControlsProvider>
    </BrowserControlsProvider>
  </Lifetime.Provider>;
}

export function usePluginConversationLifetime(): AbortSignal | undefined { return useContext(Lifetime); }
