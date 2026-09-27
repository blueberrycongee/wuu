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
import { type ElementType, useLayoutEffect, useRef, useState } from "react";
import { Tooltip } from "./Tooltip";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";

export function TruncatedText({
  text,
  className,
  as = "span",
  ...rest
}: {
  text: string;
  className?: string;
  /** Host tag for the text box. Keeps block-level semantics (div/h2/strong). */
  as?: "span" | "div" | "strong" | "h2";
} & Record<string, unknown>): JSX.Element {
  const ref = useRef<HTMLElement | null>(null);
  const [truncated, setTruncated] = useState(false);

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
      setTruncated(readTruncation());
    };
    const settle = createWindowResizeSettleScheduler(() => {
      setTruncated(readTruncation());
    });
    measure();
    if (typeof ResizeObserver === "undefined") {
      return;
    }
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => {
      settle.cancel();
      observer.disconnect();
    };
  }, [text]);

  const Tag = as as ElementType;
  return (
    <Tooltip content={text} disabled={!truncated}>
      <Tag ref={ref} className={className} data-text={text} {...rest}>
        {text}
      </Tag>
    </Tooltip>
  );
}
