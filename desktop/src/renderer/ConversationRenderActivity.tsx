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

// A reveal includes nested layout commits and the first painted frame. Releasing
// in a passive effect is too early: React can flush it before that first paint.
const ConversationRevealSnapContext = createContext(false);

export function ConversationRenderActivityProvider({
  active,
  children,
}: {
  active: boolean;
  children: ReactNode;
}): JSX.Element {
  const [reveal, setReveal] = useState({ active, snapping: false });
  if (reveal.active !== active) {
    setReveal({ active, snapping: active });
  }
  const snapping = active && (reveal.snapping || !reveal.active);
  useEffect(() => {
    if (!snapping) return;
    let frame = window.requestAnimationFrame(() => {
      // The first callback runs before paint. Release on the next frame so
      // caught-up folds have committed without a height transition.
      frame = window.requestAnimationFrame(() => {
        setReveal(current => ({ ...current, snapping: false }));
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [snapping]);
  return (
    <ConversationRenderActivityContext.Provider value={active}>
      <ConversationRevealSnapContext.Provider value={snapping}>
        <div
          className={
            snapping
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
