import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationServiceV1, UICompositionV1 } from "../../../../packages/plugin-sdk/src/ui";
import { BrowserControlsProvider } from "../BrowserControlsContext";
import { ConversationControlsProvider } from "../ConversationControlsContext";

const Application = createContext<ApplicationServiceV1 | undefined>(undefined);
const Source = createContext<{ id: string; fingerprint: string; graph_generation: string } | undefined>(undefined);
const Lifetime = createContext<AbortSignal | undefined>(undefined);
// Independent transports reuse native message presentation, not the selected
// Coding conversation's contributed actions, completions or history authority.
const presentationOnly: UICompositionV1 = { Slot: ({ fallback }) => fallback ?? null };

export function PluginConversationScope({ application, signal, source, children }: {
  application: ApplicationServiceV1;
  signal: AbortSignal;
  source?: { id: string; fingerprint: string; graph_generation: string };
  children: ReactNode;
}): JSX.Element {
  return <Application.Provider value={application}><Source.Provider value={source}><Lifetime.Provider value={signal}>
    <BrowserControlsProvider service={application.browser}>
      <ConversationControlsProvider application={application} slots={presentationOnly} slotOwner="independent-conversation">
        {children}
      </ConversationControlsProvider>
    </BrowserControlsProvider>
  </Lifetime.Provider></Source.Provider></Application.Provider>;
}

export function usePluginConversationLifetime(): AbortSignal | undefined { return useContext(Lifetime); }

export function usePluginConversationSource() { return useContext(Source); }

export function usePluginConversationApplication() { return useContext(Application); }
