import { Fragment, useEffect, useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";

import type { Turn } from "../shared/protocol";
import { turnIsAnswerReady } from "./AppState";
import { motionDurationMs } from "./motion";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";

export const TURN_OUTPUT_SUMMARY_BATCH_SIZE = 3;

export type TurnOutputSummaryRow = {
  key: string;
  name: string;
  tooltip?: string;
  trailing?: ReactNode;
  onOpen?: () => void;
  openLabel?: string;
  wrap?: (row: ReactNode) => ReactNode;
};

export function TurnOutputSummaryCard({
  icon,
  title,
  subtitle,
  trailing,
  onOpen,
  openLabel,
  wrapOverview,
  rows,
  footer,
  component,
}: {
  icon: ReactNode;
  title: string;
  subtitle?: ReactNode;
  trailing?: ReactNode;
  onOpen?: () => void;
  openLabel?: string;
  wrapOverview?: (overview: ReactNode) => ReactNode;
  rows?: readonly TurnOutputSummaryRow[];
  footer?: ReactNode;
  component?: string;
}): JSX.Element {
  const multiple = (rows?.length ?? 0) > 0;
  const overviewInner = (
    <>
      <span className="turn-edit-summary-icon" aria-hidden="true">{icon}</span>
      <span className="turn-edit-summary-overview-copy">
        <strong className="turn-edit-summary-overview-title">{title}</strong>
        {subtitle}
      </span>
      {trailing ? <span className="turn-edit-summary-overview-trailing">{trailing}</span> : null}
    </>
  );
  const overview = onOpen && !multiple ? (
    <button
      className="turn-edit-summary-overview is-clickable"
      type="button"
      aria-label={openLabel}
      onClick={onOpen}
    >
      {overviewInner}
    </button>
  ) : (
    <div className="turn-edit-summary-overview">{overviewInner}</div>
  );

  return (
    <div
      className={`turn-edit-summary-card ${multiple ? "is-multiple" : "is-single"}`}
      data-wuu-component={component}
    >
      {wrapOverview ? wrapOverview(overview) : overview}
      {multiple ? (
        <div className="turn-output-summary-list turn-edit-summary-list">
          {rows!.map((row) => {
            const content = (
              <>
                <span className="turn-output-summary-file turn-edit-summary-file">
                  {row.tooltip ? (
                    <Tooltip content={row.tooltip}>
                      <span className="turn-output-summary-name turn-edit-summary-name">{row.name}</span>
                    </Tooltip>
                  ) : (
                    <span className="turn-output-summary-name turn-edit-summary-name">{row.name}</span>
                  )}
                </span>
                {row.trailing}
              </>
            );
            const node = row.onOpen ? (
              <button
                className="turn-output-summary-row turn-edit-summary-row is-clickable"
                type="button"
                aria-label={row.openLabel}
                onClick={row.onOpen}
              >
                {content}
              </button>
            ) : (
              <div className="turn-output-summary-row turn-edit-summary-row">{content}</div>
            );
            return <Fragment key={row.key}>{row.wrap ? row.wrap(node) : node}</Fragment>;
          })}
          {footer}
        </div>
      ) : null}
    </div>
  );
}

export function TurnOutputSummaryChevron(): JSX.Element {
  return <ChevronRight className="icon" aria-hidden="true" />;
}

export function TurnOutputSummaryMore({
  hiddenCount,
  nextCount,
  onShowMore,
}: {
  hiddenCount: number;
  nextCount: number;
  onShowMore: () => void;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  return (
    <div className="turn-edit-summary-more">
      <span>
        {t(hiddenCount === 1 ? "turnEdits.moreFileOne" : "turnEdits.moreFiles", {
          count: formatNumber(hiddenCount),
        })}
      </span>
      <button
        className="turn-edit-summary-more-button"
        type="button"
        onClick={onShowMore}
      >
        {t(nextCount === 1 ? "turnEdits.showMoreOne" : "turnEdits.showMore", {
          count: formatNumber(nextCount),
        })}
      </button>
    </div>
  );
}

export function TurnOutputSummaryPresentation({
  visible,
  onCollapseComplete,
  children,
}: {
  visible: boolean;
  onCollapseComplete?: () => void;
  children: ReactNode;
}): JSX.Element | null {
  const [retained, setRetained] = useState(visible);

  useEffect(() => {
    if (visible) {
      setRetained(true);
      return;
    }
    if (!retained) return;
    const finish = (): void => {
      setRetained(false);
      onCollapseComplete?.();
    };
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) {
      finish();
      return;
    }
    const timer = window.setTimeout(finish, motionDurationMs("--query-submit-duration", 220));
    return () => window.clearTimeout(timer);
  }, [visible, retained, onCollapseComplete]);

  if (!visible && !retained) return null;
  return (
    <div
      className={`turn-edit-presentation${visible ? "" : " is-exiting"}`}
      inert={!visible}
      aria-hidden={!visible || undefined}
    >
      <div className="turn-edit-presentation-body">{children}</div>
    </div>
  );
}

export function turnOutputSummaryVisible(turn: Turn, isLatestTurn: boolean): boolean {
  return isLatestTurn && (turn.status !== "in_progress" || turnIsAnswerReady(turn));
}
