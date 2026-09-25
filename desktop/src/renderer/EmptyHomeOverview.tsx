import { type CSSProperties, memo, useEffect, useState } from "react";
import type { UsageOverviewResponse } from "../shared/protocol";
import { useI18n } from "./i18n";
import {
  buildUsageHeatmap,
  formatCompactUsageNumber,
  type UsageHeatmapCell,
} from "./UsageActivity";

type HeatmapWeek = {
  days: UsageHeatmapCell[];
  month?: string;
};

/**
 * Usage summary under the empty conversation greeting. It renders nothing
 * until the host answers: an older host or a failed read leaves the greeting
 * alone instead of claiming zero usage. A store with no usage yet is a real
 * answer and shows zero totals with an empty heatmap. Memoized because the
 * home re-renders while the user types a draft.
 */
export const EmptyHomeOverview = memo(function EmptyHomeOverview(): JSX.Element | null {
  const { locale, t, formatNumber } = useI18n();
  const [usage, setUsage] = useState<UsageOverviewResponse>();

  useEffect(() => {
    const api = window.wuu as Partial<typeof window.wuu>;
    if (typeof api.getUsageOverview !== "function") {
      return;
    }
    let cancelled = false;
    void api
      .getUsageOverview({ timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })
      .then((response) => {
        if (!cancelled) {
          setUsage(response);
        }
      })
      // The settings usage page reports load failures; here the greeting
      // simply stays on its own.
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  if (!usage) {
    return null;
  }
  const { metrics } = usage;
  const tokens =
    metrics.input_tokens + metrics.output_tokens + metrics.cache_creation_tokens + metrics.cache_read_tokens;
  // Newest week first: the heatmap drops whole older weeks when it narrows.
  const weeks = heatmapWeeks(buildUsageHeatmap(usage.days), locale).reverse();

  return (
    <section className="empty-home-overview" aria-label={t("emptyHome.usage")}>
      <dl className="empty-home-stats">
        <div>
          <dt>{t("emptyHome.sessions")}</dt>
          <dd>{formatNumber(usage.total_sessions)}</dd>
        </div>
        <div>
          <dt>{t("emptyHome.tokens")}</dt>
          <dd>{formatCompactUsageNumber(tokens, locale)}</dd>
        </div>
        <div>
          <dt>{t("emptyHome.activeDays")}</dt>
          <dd>{formatNumber(metrics.active_days)}</dd>
        </div>
      </dl>
      <div className="empty-home-heatmap" role="img" aria-label={t("emptyHome.activityHeatmap")}>
        <div
          className="empty-home-heatmap-weeks"
          style={{ "--empty-home-heatmap-weeks": weeks.length } as CSSProperties}
        >
          {weeks.map((week) => (
            <div className="empty-home-heatmap-week" key={week.days[0].date}>
              <span className="empty-home-heatmap-month">{week.month}</span>
              {week.days.map((day) => (
                <i key={day.date} data-level={day.level} />
              ))}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
});

// Label the week holding a month's first day, so labels stay four or five
// weeks apart. The two newest weeks go unlabeled: a label there would run
// past the right edge.
function heatmapWeeks(cells: UsageHeatmapCell[], locale: string): HeatmapWeek[] {
  const monthFormat = new Intl.DateTimeFormat(locale, { month: "short" });
  const weeks: HeatmapWeek[] = [];
  for (let index = 0; index < cells.length; index += 7) {
    const days = cells.slice(index, index + 7);
    const first = days.find((day) => day.date.endsWith("-01"));
    weeks.push({
      days,
      month: first ? monthFormat.format(new Date(`${first.date}T12:00:00`)) : undefined,
    });
  }
  for (const week of weeks.slice(-2)) {
    week.month = undefined;
  }
  return weeks;
}
