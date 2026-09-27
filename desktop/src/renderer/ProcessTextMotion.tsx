import {
  useLayoutEffect,
  useRef,
  useState,
  type JSX,
  type Ref,
} from "react";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import {
  useConversationBecameRenderActive,
  useConversationRenderActive,
} from "./ConversationRenderActivity";

// The exit copy's duration in turns.css (.process-text-motion-exit). Read
// when the text changes, so reduced motion turned on mid-session applies.
const processTextExitMs = (): number =>
  prefersReducedMotion() ? 0 : motionDurationMs("--motion-base", 180);
const WIDTH_CLEANUP_BUFFER_MS = 32;

export function AnimatedProcessText({
  text,
  className,
  ref,
}: {
  text: string;
  className?: string;
  ref?: Ref<HTMLSpanElement>;
}): JSX.Element {
  const previousText = useRef(text);
  const [exitingText, setExitingText] = useState<string | undefined>();
  const renderActive = useConversationRenderActive();
  const becameRenderActive = useConversationBecameRenderActive();
  const motionRef = useRef<HTMLSpanElement | null>(null);
  const exitRef = useRef<HTMLSpanElement | null>(null);
  const currentRef = useRef<HTMLSpanElement | null>(null);

  // Keep the outgoing copy mounted through the crossfade, but do it in a
  // layout effect so the enter/exit pair is committed before paint. If the
  // swap waited for a passive effect, the row would paint the new (shorter)
  // text at its new width for one frame and only then expand back out to the
  // old width for the fade — the exact long-to-short snap this component
  // exists to smooth out.
  useLayoutEffect(() => {
    if (previousText.current === text) {
      return undefined;
    }
    const previous = previousText.current;
    previousText.current = text;
    // Session-switch catch-up is a restore, not a live phase change. Keep the
    // new copy in place so the aggregated summary does not tween width after
    // the incoming conversation is already on screen.
    const exitMs = processTextExitMs();
    if (!renderActive || becameRenderActive || exitMs <= 0) {
      setExitingText(undefined);
      return undefined;
    }
    setExitingText(previous);
    const timeoutID = window.setTimeout(() => {
      setExitingText(undefined);
    }, exitMs);
    return () => window.clearTimeout(timeoutID);
  }, [becameRenderActive, renderActive, text]);

  // The crossfade stacks old and new copy in the same grid cell, so the
  // container's intrinsic width stays at the wider text while both copies
  // exist and then snaps to the shorter width when the outgoing copy is
  // removed. Tween that width change so a long summary that collapses into a
  // short one (for example "查看、编辑、搜索" into "查看") does not reflow in
  // a single frame.
  useLayoutEffect(() => {
    const exitMs = processTextExitMs();
    if (!exitingText || exitMs <= 0) {
      return undefined;
    }
    const containerEl = motionRef.current;
    const exitEl = exitRef.current;
    const currentEl = currentRef.current;
    if (!containerEl || !exitEl || !currentEl) {
      return undefined;
    }

    const exitWidth = exitEl.getBoundingClientRect().width;
    const currentWidth = currentEl.getBoundingClientRect().width;
    if (exitWidth <= 0 || currentWidth <= 0 || exitWidth === currentWidth) {
      return undefined;
    }

    // Freeze at the outgoing width, then animate down to the incoming width
    // with an inline transition. Keeping the transition scoped here avoids
    // animating unrelated reflows (for example a parent resize) whenever the
    // summary text stays the same.
    containerEl.style.transition = "none";
    containerEl.style.width = `${exitWidth}px`;
    void containerEl.offsetWidth;
    containerEl.style.transition = `width ${exitMs}ms var(--ease-out)`;
    containerEl.style.width = `${currentWidth}px`;

    const cleanup = (): void => {
      containerEl.style.width = "";
      containerEl.style.transition = "";
    };
    const cleanupTimeout = window.setTimeout(
      cleanup,
      exitMs + WIDTH_CLEANUP_BUFFER_MS,
    );
    return () => {
      window.clearTimeout(cleanupTimeout);
      cleanup();
    };
  }, [exitingText, text]);

  const setMotionRef = (node: HTMLSpanElement | null): void => {
    motionRef.current = node;
    if (typeof ref === "function") {
      ref(node);
    } else if (ref) {
      (ref as { current: HTMLSpanElement | null }).current = node;
    }
  };

  return (
    <span
      ref={setMotionRef}
      className={["process-text-motion", className].filter(Boolean).join(" ")}
      data-text={text}
      data-transitioning={exitingText ? "true" : undefined}
    >
      {exitingText ? (
        <span
          ref={exitRef}
          aria-hidden="true"
          className="process-text-motion-copy process-text-motion-exit"
        >
          {exitingText}
        </span>
      ) : null}
      <span
        ref={currentRef}
        className={`process-text-motion-copy process-text-motion-current${
          exitingText ? " process-text-motion-enter" : ""
        }`}
        key={text}
      >
        {text}
      </span>
    </span>
  );
}
