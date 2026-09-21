import { createContext, useContext, useRef, type ReactNode } from "react";

// Cached conversation panes stay mounted so tab switches can reuse their DOM,
// but hidden panes must not keep consuming high-rate presentation updates.
// `content-visibility: hidden` only suppresses layout and paint; without this
// boundary, background streams still parse Markdown and run timers on the
// renderer main thread.
const ConversationRenderActivityContext = createContext(true);

export function ConversationRenderActivityProvider({
  active,
  children,
}: {
  active: boolean;
  children: ReactNode;
}): JSX.Element {
  return (
    <ConversationRenderActivityContext.Provider value={active}>
      {children}
    </ConversationRenderActivityContext.Provider>
  );
}

export function useConversationRenderActive(): boolean {
  return useContext(ConversationRenderActivityContext);
}

/** True on the commit that makes a cached conversation visible again. */
export function useConversationBecameRenderActive(): boolean {
  const active = useConversationRenderActive();
  const wasActive = useRef(active);
  const becameActive = active && !wasActive.current;
  wasActive.current = active;
  return becameActive;
}
