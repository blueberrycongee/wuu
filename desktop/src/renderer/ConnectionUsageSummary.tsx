import type { ReactNode } from "react";
import type { SubscriptionQuota } from "../shared/protocol";
import { useI18n } from "./i18n";

/** A read-only account snapshot. Fetching and cache identity belong to the connection owner. */
export function ConnectionUsageSummary({ id, quota, loading = false, now = Date.now(), compact = false, fallback }: {
  id?: string;
  compact?: boolean;
  fallback?: ReactNode;
  quota?: SubscriptionQuota;
  loading?: boolean;
  now?: number;
}): JSX.Element | null {
  const { t, formatNumber, formatDate } = useI18n();
  const empty = compact ? <span id={id} className="connection-usage-fallback" aria-busy={loading}>
    {loading ? <span className="connection-summary-pending">{t("settings.subscriptionRefreshing")}</span>
      : quota?.status === "unavailable" ? t("settings.subscriptionQuotaUnavailable") : fallback}
  </span> : null;
  if (!quota || (quota.status !== "available" && quota.status !== "stale")) return empty;

  const expired = quota.status === "stale" || (quota.expires_at !== undefined && Date.parse(quota.expires_at) <= now);
  const statusKey = quota.status === "stale" ? "settings.subscriptionQuotaRefreshFailed"
    : expired ? "settings.subscriptionQuotaStale" : undefined;
  // Labels and reset dates alone are not usage observations.
  const windows = (quota.windows ?? []).filter(window => window.unlimited || window.display?.trim()
    || (typeof window.used_percent === "number" && Number.isFinite(window.used_percent) && window.used_percent >= 0));
  const balances = (quota.balances ?? []).filter(balance => balance.amount.trim() && balance.currency.trim());
  if (!windows.length && !balances.length) return empty;

  return <span id={id} className={`connection-usage-summary${compact ? " connection-usage-inline" : ""}`} aria-busy={loading}>
    {statusKey ? <span className={`connection-usage-state${compact ? " sr-only" : ""}`} title={t(statusKey)}>{t(compact ? "settings.subscriptionQuotaStale" : statusKey)}</span> : null}
    {windows.map(window => {
      // The protocol reports consumed percent; invalid values must not invent remaining capacity.
      const remaining = typeof window.used_percent === "number" && Number.isFinite(window.used_percent) && window.used_percent >= 0
        ? Math.max(0, 100 - window.used_percent) : undefined;
      const minutes = window.window_minutes;
      const period = minutes && Number.isFinite(minutes) && minutes > 0
        ? minutes % 1440 === 0 ? `${formatNumber(minutes / 1440)}d`
          : minutes % 60 === 0 ? `${formatNumber(minutes / 60)}h` : `${formatNumber(minutes)}m` : undefined;
      const periodOnly = /^(?:\d+ (?:hours|days|minutes)|Primary|Secondary|Daily|Weekly|Weekly allowance|Rolling window|Requests|Allowance)$/i.test(window.label ?? "");
      const label = periodOnly && period ? undefined : window.label;
      const scope = [...new Set([window.model, window.scope].filter(value => value && value.toLowerCase() !== "all models"))].join(" · ");
      const details = [label, scope].filter(Boolean).join(" · ");
      const resetMillis = window.resets_at ? Date.parse(window.resets_at) : NaN;
      const pastReset = Number.isFinite(resetMillis) && resetMillis <= now;
      const lastKnown = expired || pastReset;
      const readout = window.unlimited ? t(lastKnown && !compact ? "settings.subscriptionLastKnownUnlimited" : "settings.subscriptionUnlimited")
        : remaining === undefined ? undefined
          : t(lastKnown && !compact ? "settings.subscriptionLastKnownRemaining" : "settings.subscriptionRemaining", { percent: formatNumber(remaining, { maximumFractionDigits: 1 }) });
      const resetTitle = Number.isFinite(resetMillis) ? t("settings.subscriptionResetTime", {
        time: formatDate(resetMillis, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZoneName: "short" }),
      }) : undefined;
      const meter = !lastKnown && !window.unlimited && remaining !== undefined ? <meter min={0} max={100} value={remaining}
        data-warning={remaining <= 20} data-exhausted={remaining === 0}
        aria-label={[period, details].filter(Boolean).join(" · ") || t("settings.subscriptionAllowance")} aria-valuetext={readout} /> : null;
      if (compact) return <span className="connection-usage-compact-window" key={window.id} data-stale={lastKnown || undefined}
        title={[statusKey ? t(statusKey) : undefined, period, readout, details, window.display, resetTitle].filter(Boolean).join(" · ")}>
        <span className="connection-usage-compact-label">{lastKnown
          ? [period, t("settings.subscriptionQuotaStale")].filter(Boolean).join(" · ")
          : t("settings.subscriptionPeriodRemaining", { period: period ?? label ?? "" }).trim()}</span>
        {meter}
        <span className="connection-usage-compact-value" data-warning={!lastKnown && remaining !== undefined && remaining <= 20}
          data-exhausted={!lastKnown && remaining === 0} aria-label={readout}>
          {remaining !== undefined && !window.unlimited ? `${formatNumber(remaining, { maximumFractionDigits: 1 })}%` : readout ?? window.display}
        </span>
      </span>;
      return <span className="connection-usage-window" key={window.id}>
        {readout || period ? <span className="connection-usage-heading">
          {readout ? <span className="connection-usage-value" aria-label={readout}>
            {!lastKnown && !window.unlimited && remaining !== undefined ? `${formatNumber(remaining, { maximumFractionDigits: 1 })}%` : readout}
          </span> : null}
          {!lastKnown && !window.unlimited && remaining !== undefined
            ? <span className="connection-usage-period">{t("settings.subscriptionPeriodRemaining", { period: period ?? "" }).trim()}</span>
            : period ? <span className="connection-usage-period">{period}</span> : null}
        </span> : null}
        {meter}
        {details ? <span className="connection-usage-scope">{details}</span> : null}
        {window.display ? <span className="connection-usage-display">{window.display}</span> : null}
        {pastReset && !expired && !readout ? <span className="connection-usage-state">{t("settings.subscriptionQuotaStale")}</span> : null}
        {resetTitle ? <time className="connection-usage-reset" dateTime={window.resets_at} title={resetTitle} aria-label={resetTitle}>
          {t("settings.subscriptionResets", { time: formatDate(resetMillis, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) })}
        </time> : null}
      </span>;
    })}
    {balances.length ? <span className="connection-usage-balance">{compact ? <span>{t("settings.subscriptionKindBalance")}</span> : <>{t("settings.subscriptionKindBalance")}{" · "}</>}
      <span>{balances.map(balance => `${balance.amount} ${balance.currency}`).join(" · ")}</span>
    </span> : null}
  </span>;
}
