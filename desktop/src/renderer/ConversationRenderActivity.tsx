import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type SyntheticEvent,
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

// An inactive subtree may still refresh live text at this slower cadence. It
// keeps the work of a later reveal bounded without per-frame updates.
const ConversationRenderCatchUpContext = createContext<number | undefined>(undefined);

// Closed disclosures sit in visible panes and can open at any moment.
const CLOSED_DISCLOSURE_CATCH_UP_MS = 1000;

export function ConversationRenderActivityProvider({
  active,
  restoring = false,
  catchUpMs,
  children,
}: {
  active: boolean;
  restoring?: boolean;
  catchUpMs?: number;
  children: ReactNode;
}): JSX.Element {
  const [reveal, setReveal] = useState({ active, snapping: restoring });
  if (reveal.active !== active) {
    setReveal({ active, snapping: active });
  }
  const snapping = active && (restoring || reveal.snapping || !reveal.active);
  useEffect(() => {
    if (!snapping || restoring) return;
    let frame = window.requestAnimationFrame(() => {
      // The first callback runs before paint. Release on the next frame so
      // caught-up folds have committed without a height transition.
      frame = window.requestAnimationFrame(() => {
        setReveal(current => ({ ...current, snapping: false }));
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [snapping, restoring]);
  return (
    <ConversationRenderActivityContext.Provider value={active}>
      <ConversationRenderCatchUpContext.Provider value={active ? undefined : catchUpMs}>
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
      </ConversationRenderCatchUpContext.Provider>
    </ConversationRenderActivityContext.Provider>
  );
}

export function useConversationRenderActive(): boolean {
  return useContext(ConversationRenderActivityContext);
}

/** Refresh interval for live text while this subtree is inactive, if any. */
export function useConversationRenderCatchUpMs(): number | undefined {
  return useContext(ConversationRenderCatchUpContext);
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

/**
 * Render activity for a disclosure body. A closed body is not painted, so its
 * streams only catch up occasionally. A summary click activates the body
 * before the native toggle opens it, so the first open frame is current.
 */
export function useDisclosureRenderActive(open: boolean): {
  active: boolean;
  catchUpMs?: number;
  onSummaryClick: (event: SyntheticEvent<HTMLElement>) => void;
} {
  const paneActive = useConversationRenderActive();
  const [opening, setOpening] = useState(false);
  if (open && opening) setOpening(false);
  return {
    active: paneActive && (open || opening),
    catchUpMs: paneActive ? CLOSED_DISCLOSURE_CATCH_UP_MS : undefined,
    onSummaryClick: event => {
      if (!open && !event.defaultPrevented) setOpening(true);
    },
  };
}
