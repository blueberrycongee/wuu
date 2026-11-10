/*
 * TruncatedText.
 *
 * Single-line (or line-clamped) text that reveals its full content on
 * hover — but ONLY when CSS truncation is actually hiding part of it.
 * The old pattern put `title={fullText}` on every truncated element,
 * which meant the hint fired even when nothing was hidden, and a parent
 * element's `title` leaked the same popup onto every child.
 *
 * Truncation is measured against the element's own box (scrollWidth vs
 * clientWidth, with the scrollHeight variant for line-clamped blocks)
 * and re-measured on resize and text change, so responsive layouts arm
 * and disarm the tooltip as the available width changes.
 */
import { type ElementType, type ReactNode, useLayoutEffect, useRef, useState } from "react";
import { Tooltip } from "./Tooltip";
import { observeAppearance } from "./AppearancePreferences";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";

/* scrollReveal sweeps at this speed, clamped so short reveals don't crawl
 * and long titles stay readable instead of whipping past. */
const SCROLL_REVEAL_PX_PER_SECOND = 40;
const SCROLL_REVEAL_MIN_SECONDS = 1.2;
const SCROLL_REVEAL_MAX_SECONDS = 12;

export function TruncatedText({
  text,
  className,
  as = "span",
  children,
  nativeTooltip = false,
  fade = false,
  scrollReveal = false,
  measureKey,
  measureRef,
  ...rest
}: {
  text: string;
  /** Optional styled rendering of the same text, measured as one text box. */
  children?: ReactNode;
  /** Use native overlays when the text is beside an embedded browser. */
  nativeTooltip?: boolean;
  /** Fade the trailing edge only while the text is actually truncated. */
  fade?: boolean;
  /**
   * Publish the overflow geometry and wrap the text in a runner so hover CSS
   * can slide the clipped tail into view. The transform lives on the child
   * because transforms never feed back into scrollWidth — animating the box
   * itself would corrupt the measurement that armed it.
   */
  scrollReveal?: boolean;
  /** Optional render-state key for animated children that change their width. */
  measureKey?: string;
  /** Optional access to the measured element for callers coordinating animation. */
  measureRef?: (element: HTMLElement | null) => void;
  className?: string;
  /** Host tag for the text box. Keeps block-level semantics (div/h2/strong). */
  as?: "span" | "div" | "strong" | "h2";
} & Record<string, unknown>): JSX.Element {
  const ref = useRef<HTMLElement | null>(null);
  const [truncated, setTruncated] = useState(false);
  // scrollReveal arms on horizontal overflow alone; a line-clamped block has
  // nothing to sweep sideways.
  const [scrollable, setScrollable] = useState(false);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) {
      return;
    }
    const readTruncation = (): boolean => (
      element.scrollWidth > element.clientWidth + 1 ||
      element.scrollHeight > element.clientHeight + 1
    );
    const measure = (): void => {
      if (isWindowResizing()) {
        settle.schedule();
        return;
      }
      sync();
    };
    const sync = (): void => {
      if (scrollReveal) {
        const overflow = Math.max(0, element.scrollWidth - element.clientWidth);
        const seconds = Math.min(
          Math.max(overflow / SCROLL_REVEAL_PX_PER_SECOND, SCROLL_REVEAL_MIN_SECONDS),
          SCROLL_REVEAL_MAX_SECONDS,
        );
        element.style.setProperty("--scroll-reveal-distance", `${-overflow}px`);
        element.style.setProperty("--scroll-reveal-duration", `${seconds}s`);
        setScrollable(overflow > 1);
      }
      setTruncated(readTruncation());
    };
    const settle = createWindowResizeSettleScheduler(sync);
    measure();
    // Font changes can alter text overflow without changing the grid cell's box.
    const stopObservingAppearance = observeAppearance(measure);
    document.fonts?.addEventListener("loadingdone", measure);
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(element);
    return () => {
      settle.cancel();
      observer?.disconnect();
      stopObservingAppearance();
      document.fonts?.removeEventListener("loadingdone", measure);
    };
  }, [text, measureKey, scrollReveal]);

  const Tag = as as ElementType;
  return (
    <Tooltip asChild content={text} disabled={!truncated} native={nativeTooltip}>
      <Tag
        ref={(element: HTMLElement | null) => {
          ref.current = element;
          measureRef?.(element);
        }}
        className={className}
        data-text={text}
        data-truncated={fade && truncated ? "true" : undefined}
        data-scroll-reveal={scrollReveal && scrollable ? "true" : undefined}
        {...rest}
      >
        {scrollReveal ? (
          <span className="truncated-text-runner">{children ?? text}</span>
        ) : (
          children ?? text
        )}
      </Tag>
    </Tooltip>
  );
}
