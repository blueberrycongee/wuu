import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

// Cached conversation panes stay mounted so tab switches can reuse their DOM,
// but hidden panes must not keep consuming high-rate presentation updates.
// `content-visibility: hidden` only suppresses layout and paint; without this
// boundary, background streams still parse Markdown and run timers on the
// renderer main thread.
const ConversationRenderActivityContext = createContext(true);

// True only for the commit that shows a cached conversation again. The
// following paint releases it, after transitions have already been suppressed
// for that frame.
const ConversationRevealSnapContext = createContext(false);

export function ConversationRenderActivityProvider({
  active,
  children,
}: {
  active: boolean;
  children: ReactNode;
}): JSX.Element {
  const wasActive = useRef(active);
  const activating = active && !wasActive.current;
  if (activating) {
    wasActive.current = true;
  } else if (!active) {
    wasActive.current = false;
  }
  const [, setRevealRelease] = useState(0);
  useEffect(() => {
    if (!activating) {
      return;
    }
    // The snap class has to survive the first paint. Releasing it here, in a
    // passive effect, is the first commit after that paint.
    setRevealRelease((generation) => generation + 1);
  }, [activating]);
  return (
    <ConversationRenderActivityContext.Provider value={active}>
      <ConversationRevealSnapContext.Provider value={activating}>
        <div
          className={
            activating
              ? "conversation-render-activity is-reveal-snap"
              : "conversation-render-activity"
          }
          style={{ display: "contents" }}
        >
          {children}
        </div>
      </ConversationRevealSnapContext.Provider>
    </ConversationRenderActivityContext.Provider>
  );
}

export function useConversationRenderActive(): boolean {
  return useContext(ConversationRenderActivityContext);
}

/** True while the first paint of a revealed cached conversation is committing. */
export function useConversationRevealSnap(): boolean {
  return useContext(ConversationRevealSnapContext);
}

/** True on the commit that makes a cached conversation visible again. */
export function useConversationBecameRenderActive(): boolean {
  const active = useConversationRenderActive();
  const wasActive = useRef(active);
  const becameActive = active && !wasActive.current;
  wasActive.current = active;
  return becameActive;
}
