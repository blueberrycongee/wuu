import { useEffect, useMemo, useState } from "react";
import { RefreshCw } from "./WuuIcons";
import type { EngineListResult, ProviderSummary, SubscriptionQuota } from "../shared/protocol";
import { EngineIcon } from "./EngineIcons";
import { EngineAuthentication } from "./EngineAuthentication";
import { SelectMenu } from "./SelectMenu";
import { useI18n } from "./i18n";
import {
  selectSubscriptionModel,
  subscriptionSources,
  type SubscriptionSource,
} from "./SubscriptionSources";

export function SubscriptionDashboard({
  inventory,
  providers,
  onSelectBuiltinModel,
}: {
  inventory?: EngineListResult;
  providers?: readonly ProviderSummary[];
  onSelectBuiltinModel: (provider: string, model: string) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [error, setError] = useState<"" | "settings.subscriptionRefreshFailed" | "settings.subscriptionModelFailed">("");
  const [pending, setPending] = useState("");
  const [revision, setRevision] = useState(0);
  const [loadedInventory, setLoadedInventory] = useState<EngineListResult>();
  // The mount effect always starts a load; begin busy so the first frame
  // already reserves the quota placeholders.
  const [refreshing, setRefreshing] = useState(true);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    let active = true;
    setRefreshing(true);
    setError("");
    void window.wuu.listEngines({ include_quota: true }).then((result) => {
      if (active) { setLoadedInventory(result); setNow(Date.now()); }
    }, () => {
      if (active) setError("settings.subscriptionRefreshFailed");
    }).finally(() => { if (active) setRefreshing(false); });
    return () => { active = false; };
  }, [refreshVersion]);
  const sources = useMemo(
    () => {
      // Keep live model selections from the parent; refresh owns only the
      // account snapshot and supplies inventory before the parent loads.
      const base = inventory ?? loadedInventory;
      const refreshed = base ? { ...base, engines: base.engines.map((engine) => {
        const snapshot = loadedInventory?.engines.find((item) => item.id === engine.id);
        return snapshot ? { ...snapshot, enabled: engine.enabled } : engine;
      }) } : undefined;
      return subscriptionSources(refreshed, providers ?? loadedInventory?.subscription_providers);
    },
    [loadedInventory, inventory, providers, revision],
  );

  // Quota arrives only with this dashboard's own snapshot. Until the first one
  // lands, reserve its block on runnable engines that advertise an account
  // allowance; the snapshot omits every other row's quota.
  const quotaLoading = refreshing && !loadedInventory;

  async function choose(source: SubscriptionSource, modelID: string): Promise<void> {
    if (!modelID || modelID === source.selectedModel || pending) return;
    setError("");
    setPending(source.key);
    try {
      if (source.kind === "builtin") {
        await onSelectBuiltinModel(source.id, modelID);
      } else {
        selectSubscriptionModel(source, modelID);
        setRevision((current) => current + 1);
      }
    } catch {
      setError("settings.subscriptionModelFailed");
    } finally {
      setPending("");
    }
  }

  return (
    <section className="settings-section settings-subscriptions" data-testid="settings-subscriptions" aria-busy={refreshing}>
      <header className="settings-page-header settings-subscription-header">
        <h1 className="settings-page-title">{t("settings.subscriptions")}</h1>
        <button type="button" className="settings-button settings-button-ghost settings-icon-button" disabled={refreshing} onClick={() => setRefreshVersion((value) => value + 1)} aria-label={t(refreshing ? "settings.subscriptionRefreshing" : "settings.subscriptionRefresh")} title={t("settings.subscriptionRefresh")}>
          <RefreshCw size={16} className={refreshing ? "settings-spin" : undefined} aria-hidden="true" />
        </button>
      </header>
      {sources.length === 0 ? (
        inventory || loadedInventory ? <p className="settings-muted-line">{t("settings.subscriptionsEmpty")}</p>
          : refreshing ? (
            <div className="settings-subscription-list settings-subscription-skeleton" role="status" aria-label={t("settings.engineDetecting")} aria-busy="true">
              {[0, 1, 2].map((item) => (
                <div key={item} className="settings-subscription" aria-hidden="true">
                  <div className="settings-subscription-row">
                    <div className="settings-subscription-main">
                      <span className="settings-usage-skeleton-line settings-subscription-skeleton-icon" />
                      <span className="settings-usage-skeleton-line settings-subscription-skeleton-text settings-subscription-skeleton-name" />
                    </div>
                    <div className="settings-subscription-actions">
                      <span className="settings-usage-skeleton-line settings-subscription-skeleton-model" />
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : null
      ) : (
        <div className="settings-subscription-list">
          {sources.map((source) => {
            const showAuthentication = source.engine?.protocol === "acp" && source.engine.enabled && source.engine.binary_ok
              && (source.login !== "ready" || source.catalogFailed);
            const status = source.login === "unavailable" ? "settings.subscriptionUnavailable"
              : source.catalogFailed ? "settings.subscriptionCatalogFailed"
                : source.login === "sign_in" ? "settings.subscriptionSignIn" : null;
            return (
              <article key={source.key} className="settings-subscription" data-testid={`subscription-${source.kind}-${source.id}`}>
                <div className="settings-subscription-row">
                  <div className="settings-subscription-main">
                    <span className="settings-engine-row-icon" aria-hidden="true"><EngineIcon engine={source.kind === "engine" ? source.id : "wuu"} /></span>
                    <div className="settings-subscription-identity">
                      <h2 className="settings-subscription-name">{source.label}</h2>
                      {status ? <p className="settings-subscription-status">{t(status)}</p> : null}
                    </div>
                  </div>
                  <div className="settings-subscription-actions">
                    {source.models.length > 0 ? (
                      <SelectMenu
                        triggerClassName="settings-subscription-model-trigger"
                        value={source.selectedModel}
                        disabled={!!pending || source.login === "unavailable"}
                        ariaLabel={`${source.label} ${t("settings.subscriptionModel")}`}
                        placeholder={t("settings.subscriptionModelUnset")}
                        onChange={(model) => void choose(source, model)}
                        options={source.models.map((model) => ({ value: model.id, label: model.label }))}
                        flip
                      />
                    ) : null}
                    {showAuthentication ? <EngineAuthentication engineID={source.id} compact onAuthenticated={() => setRefreshVersion((value) => value + 1)} /> : null}
                  </div>
                </div>
                {quotaLoading && source.engine?.enabled && source.engine.binary_ok && source.engine.capabilities?.includes("account-quota")
                  ? <QuotaSkeleton />
                  : <Quota quota={source.quota} now={now} />}
              </article>
            );
          })}
        </div>
      )}
      {error ? <p className="settings-subscription-status" role="alert">{t(error)}</p> : null}
    </section>
  );
}

// Mirrors Quota's layout line for line (usage, meter, reset). Two windows
// match the usual short and long allowance pair at every width.
function QuotaSkeleton(): JSX.Element {
  return <div className="settings-subscription-quota settings-subscription-skeleton" aria-hidden="true">
    {[0, 1].map((item) => (
      <div key={item} className="settings-subscription-window">
        <div className="settings-subscription-usage">
          <span className="settings-usage-skeleton-line settings-subscription-skeleton-text settings-subscription-skeleton-period" />
          <span className="settings-usage-skeleton-line settings-subscription-skeleton-text settings-subscription-skeleton-remaining" />
        </div>
        <span className="settings-usage-skeleton-line settings-subscription-skeleton-meter" />
        <span className="settings-usage-skeleton-line settings-subscription-skeleton-text settings-subscription-skeleton-reset" />
      </div>
    ))}
  </div>;
}

function Quota({ quota, now }: { quota?: SubscriptionQuota; now: number }): JSX.Element | null {
  const { t } = useI18n();
  const windows = quota?.status === "available" ? quota.windows?.filter((window) => Number.isFinite(window.used_percent) && window.used_percent >= 0) ?? [] : [];
  if (!windows.length) return quota?.status === "unavailable" ? <p className="settings-subscription-status">{t("settings.subscriptionAllowance")} · {t("settings.subscriptionQuotaUnavailable")}</p> : null;
  return <div className="settings-subscription-quota">
    {windows.map((window) => {
      const remaining = Math.max(0, 100 - window.used_percent);
      const minutes = window.window_minutes;
      const period = minutes ? minutes % 1440 === 0 ? t("settings.subscriptionDays", { count: minutes / 1440 }) : minutes % 60 === 0 ? t("settings.subscriptionHours", { count: minutes / 60 }) : t("settings.subscriptionMinutes", { count: minutes }) : t("settings.subscriptionAllowance");
      const label = [window.label, period].filter(Boolean).join(" · ");
      const reset = window.resets_at ? new Date(window.resets_at) : undefined;
      const checkedAt = quota?.checked_at ? Date.parse(quota.checked_at) : NaN;
      const expired = (reset && Number.isFinite(reset.getTime()) && reset.getTime() <= now) || (Number.isFinite(checkedAt) && now - checkedAt > 5 * 60_000);
      return <div key={window.id} className="settings-subscription-window" data-exhausted={!expired && remaining === 0}>
        <div className="settings-subscription-usage"><span>{label}</span><span className={expired ? undefined : "settings-subscription-remaining"}>{expired ? t("settings.subscriptionQuotaStale") : t("settings.subscriptionRemaining", { percent: new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(remaining) })}</span></div>
        {!expired ? <meter min={0} max={100} value={remaining} aria-label={label} aria-valuetext={t("settings.subscriptionRemaining", { percent: remaining })} /> : null}
        {reset && Number.isFinite(reset.getTime()) ? <span className="settings-subscription-reset">{t("settings.subscriptionResets", { time: reset.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) })}</span> : null}
      </div>;
    })}
  </div>;
}
