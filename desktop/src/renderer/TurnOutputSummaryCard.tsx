import { Fragment, useId, type MouseEvent, type ReactNode } from "react";
import { ChevronRight } from "./WuuIcons";

import type { Turn } from "../shared/protocol";
import { turnIsAnswerReady } from "./AppState";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { useExitPresence } from "./useExitPresence";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";

export const TURN_OUTPUT_SUMMARY_BATCH_SIZE = 3;

export type TurnOutputSummaryRow = {
  key: string;
  name: string;
  tooltip?: string;
  icon?: ReactNode;
  subtitle?: ReactNode;
  trailing?: ReactNode;
  onOpen?: (event: MouseEvent<HTMLButtonElement>) => void;
  onContextMenu?: (event: MouseEvent<HTMLButtonElement>) => void;
  openLabel?: string;
  wrap?: (row: ReactNode) => ReactNode;
};

export function TurnOutputSummaryCard({
  icon,
  title,
  subtitle,
  trailing,
  onOpen,
  onContextMenu,
  openLabel,
  wrapOverview,
  rows,
  footer,
  component,
}: {
  icon?: ReactNode;
  title?: string;
  subtitle?: ReactNode;
  trailing?: ReactNode;
  onOpen?: (event: MouseEvent<HTMLButtonElement>) => void;
  onContextMenu?: (event: MouseEvent<HTMLButtonElement>) => void;
  openLabel?: string;
  wrapOverview?: (overview: ReactNode) => ReactNode;
  rows?: readonly TurnOutputSummaryRow[];
  footer?: ReactNode;
  component?: string;
}): JSX.Element {
  const descriptionId = useId();
  const multiple = (rows?.length ?? 0) > 0;
  const overviewInner = (
    <>
      <span className="turn-edit-summary-icon" aria-hidden="true">{icon}</span>
      <span className="turn-edit-summary-overview-copy">
        <strong className="turn-edit-summary-overview-title">{title}</strong>
        {subtitle ? <span id={descriptionId} className="turn-output-summary-description">{subtitle}</span> : null}
      </span>
      {trailing ? <span className="turn-edit-summary-overview-trailing">{trailing}</span> : null}
    </>
  );
  const overview = onOpen && !multiple ? (
    <button
      className="turn-edit-summary-overview is-clickable"
      type="button"
      aria-label={openLabel}
      aria-describedby={subtitle ? descriptionId : undefined}
      onClick={onOpen}
      onContextMenu={onContextMenu}
    >
      {overviewInner}
    </button>
  ) : (
    <div className="turn-edit-summary-overview">{overviewInner}</div>
  );

  return (
    <div
      className={`turn-edit-summary-card ${multiple ? "is-multiple" : "is-single"}${title === undefined ? " is-list-only" : ""}`}
      data-wuu-component={component}
    >
      {title === undefined ? null : wrapOverview ? wrapOverview(overview) : overview}
      {multiple ? (
        <div className="turn-output-summary-list turn-edit-summary-list">
          {rows!.map((row, index) => {
            const rowDescriptionId = `${descriptionId}-${index}`;
            const content = (
              <>
                {row.icon ? <span className="turn-edit-summary-icon" aria-hidden="true">{row.icon}</span> : null}
                <span className="turn-output-summary-file turn-edit-summary-file">
                  <span className="turn-output-summary-copy">
                    {row.tooltip ? (
                      <Tooltip content={row.tooltip}>
                        <span className="turn-output-summary-name turn-edit-summary-name">{row.name}</span>
                      </Tooltip>
                    ) : (
                      <span className="turn-output-summary-name turn-edit-summary-name">{row.name}</span>
                    )}
                    {row.subtitle ? <span id={rowDescriptionId} className="turn-output-summary-description">{row.subtitle}</span> : null}
                  </span>
                </span>
                {row.trailing}
              </>
            );
            const node = row.onOpen ? (
              <button
                className="turn-output-summary-row turn-edit-summary-row is-clickable"
                type="button"
                aria-label={row.openLabel}
                aria-describedby={row.subtitle ? rowDescriptionId : undefined}
                onClick={row.onOpen}
                onContextMenu={row.onContextMenu}
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
  totalCount,
  hiddenCount,
  nextCount,
  onShowMore,
}: {
  totalCount?: number;
  hiddenCount: number;
  nextCount: number;
  onShowMore: () => void;
}): JSX.Element {
  const { t, formatNumber } = useI18n();
  return (
    <div className="turn-edit-summary-more">
      <span>
        {totalCount !== undefined ? t(hiddenCount > 0 ? "artifacts.totalWithHidden" : "artifacts.total", {
          total: formatNumber(totalCount),
          hidden: formatNumber(hiddenCount),
        }) : t(hiddenCount === 1 ? "turnEdits.moreFileOne" : "turnEdits.moreFiles", {
          count: formatNumber(hiddenCount),
        })}
      </span>
      {hiddenCount > 0 ? <button
        className="turn-edit-summary-more-button"
        type="button"
        onClick={onShowMore}
      >
        {t(nextCount === 1 ? "turnEdits.showMoreOne" : "turnEdits.showMore", {
          count: formatNumber(nextCount),
        })}
      </button> : null}
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
  const [present] = useExitPresence(
    visible,
    () => prefersReducedMotion() ? 0 : motionDurationMs("--query-submit-duration", 220),
    onCollapseComplete,
  );

  if (!present) return null;
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
