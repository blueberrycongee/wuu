import { useLayoutEffect, useRef } from "react";
import type {
  HTMLAttributes,
  ReactNode,
  Ref,
  SyntheticEvent,
} from "react";
import { ChevronRight } from "./WuuIcons";

/**
 * Generic "process row → expandable fold" primitive shared by every
 * process-region surface (tool activity rows, context-compaction notices).
 *
 * It owns the shared interaction contract of the fold:
 *
 *   * a clickable summary row with a chevron that rotates when open;
 *   * a bounded body (`process-surface-body`) that caps the expanded area
 *     at the shared height limit and scrolls as one container — an open
 *     fold can never push the conversation past one screen of activity;
 *   * a `disabled` state that renders the exact same read-only row the
 *     caller already used (no chevron, no toggle) so surfaces can switch
 *     between interactive and static without changing their markup.
 *
 * The component is controlled: the caller keeps the `open` state so
 * streaming surfaces can decide when an open fold resets (e.g. when
 * details disappear) without the DOM `details` element being the source
 * of truth.
 */
export type ProcessSurfaceFoldProps = {
  /** Row content rendered inside the clickable `<summary>`. */
  summary: ReactNode;
  /** Independent controls beside the disclosure, never inside its summary. */
  summaryAccessory?: ReactNode;
  /** Content rendered inside the bounded body once the fold is open. */
  children?: ReactNode;
  /**
   * Optional content rendered between the summary row and the body
   * (e.g. error blocks that should stay outside the scroll container).
   */
  header?: ReactNode;
  /** When true the fold renders as a static row with no toggle. */
  disabled?: boolean;
  open: boolean;
  onToggle: (event: SyntheticEvent<HTMLDetailsElement>) => void;
  onSummaryClick?: (event: SyntheticEvent<HTMLElement>) => void;
  className?: string;
  /** Extra classes for the clickable summary row (live/streaming states). */
  rowClassName?: string;
  /** Forwarded to the bounded body container (scroll refs, attrs). */
  bodyRef?: Ref<HTMLDivElement>;
  bodyProps?: HTMLAttributes<HTMLDivElement> & Record<string, unknown>;
};

export function ProcessSurfaceFold({
  summary,
  summaryAccessory,
  children,
  header,
  disabled = false,
  open,
  onToggle,
  onSummaryClick,
  className = "",
  rowClassName = "",
  bodyRef,
  bodyProps,
}: ProcessSurfaceFoldProps): JSX.Element {
  const hasDetails = !disabled;
  const layoutRef = useRef<HTMLDivElement>(null);
  const accessoryRef = useRef<HTMLDivElement>(null);
  const hasAccessory = Boolean(summaryAccessory);
  useLayoutEffect(() => {
    if (!hasAccessory) return;
    const measure = () => {
      const width = accessoryRef.current?.getBoundingClientRect().width ?? 0;
      layoutRef.current?.style.setProperty("--process-accessory-width", `${width}px`);
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    if (accessoryRef.current) observer?.observe(accessoryRef.current);
    return () => observer?.disconnect();
  }, [hasAccessory]);
  const handleToggle = (event: SyntheticEvent<HTMLDetailsElement>): void => {
    if (!hasDetails) {
      event.currentTarget.open = false;
      return;
    }
    onToggle(event);
  };
  const handleSummaryClick = (event: SyntheticEvent<HTMLElement>): void => {
    if (!hasDetails) {
      event.preventDefault();
      onSummaryClick?.(event);
    }
  };
  const fold = (
    <details
      className={`process-surface-fold${hasDetails ? " has-details" : " no-details"}${
        open ? " expanded" : " collapsed"
      }${className ? ` ${className}` : ""}`}
      open={hasDetails && open}
      onToggle={handleToggle}
    >
      <summary
        className={`process-surface-row${rowClassName ? ` ${rowClassName}` : ""}`}
        onClick={handleSummaryClick}
      >
        {summary}
        {hasDetails ? (
          <ChevronRight
            className="process-surface-chevron icon-xs"
            aria-hidden
          />
        ) : null}
      </summary>
      {header}
      {hasDetails ? (
        <div
          className="process-surface-body"
          data-scroll-fade="compact"
          ref={bodyRef}
          {...bodyProps}
        >
          {children}
        </div>
      ) : null}
    </details>
  );
  return hasAccessory ? (
    <div ref={layoutRef} className="process-surface-inline-controls">
      {fold}
      <div ref={accessoryRef} className="process-surface-summary-accessory">{summaryAccessory}</div>
    </div>
  ) : fold;
}
