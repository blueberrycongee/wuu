import { useEffect, useMemo, useState } from "react";
import { RefreshCw } from "lucide-react";
import type { EngineListResult, ProviderSummary, SubscriptionQuota } from "../shared/protocol";
import { EngineIcon } from "./EngineIcons";
import { EngineAuthentication } from "./EngineAuthentication";
import { SelectMenu } from "./SelectMenu";
import { useI18n } from "./i18n";
import {
  selectSubscriptionModel,
  subscriptionSources,
  type SubscriptionLogin,
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
  const [error, setError] = useState("");
  const [pending, setPending] = useState("");
  const [revision, setRevision] = useState(0);
  const [loadedInventory, setLoadedInventory] = useState<EngineListResult>();
  const [refreshing, setRefreshing] = useState(false);
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
    }, (cause: unknown) => {
      if (active) setError(cause instanceof Error ? cause.message : String(cause));
    }).finally(() => { if (active) setRefreshing(false); });
    return () => { active = false; };
  }, [refreshVersion]);
  const sources = useMemo(
    () => {
      // Keep live model selections from the parent; refresh owns only the
      // account/usage snapshot and supplies inventory before the parent loads.
      const base = inventory ?? loadedInventory;
      const refreshed = base ? { ...base, engines: base.engines.map((engine) => {
        const snapshot = loadedInventory?.engines.find((item) => item.id === engine.id);
        return snapshot ? { ...engine, ...snapshot, enabled: engine.enabled } : engine;
      }) } : undefined;
      const refreshedProviders = providers?.map((provider) => {
        const snapshot = loadedInventory?.subscription_providers?.find((item) => item.name === provider.name);
        return snapshot ? { ...provider, local_usage: snapshot.local_usage, latest_request: snapshot.latest_request } : provider;
      }) ?? loadedInventory?.subscription_providers;
      return subscriptionSources(refreshed, refreshedProviders);
    },
    [loadedInventory, inventory, providers, revision],
  );

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
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setPending("");
    }
  }

  return (
    <section className="settings-section" data-testid="settings-subscriptions" aria-busy={refreshing}>
      <header className="settings-section-header settings-subscription-header">
        <h2 className="settings-section-title">{t("settings.subscriptions")}</h2>
        <button type="button" className="settings-button" disabled={refreshing} onClick={() => setRefreshVersion((value) => value + 1)} aria-label={t("settings.subscriptionRefresh")}>
          <RefreshCw size={16} aria-hidden="true" />
          {t(refreshing ? "settings.subscriptionRefreshing" : "settings.subscriptionRefresh")}
        </button>
      </header>
      {sources.length === 0 ? (
        <p className="settings-muted-line">{inventory ? t("settings.subscriptionsEmpty") : t("settings.engineDetecting")}</p>
      ) : (
        <div className="settings-subscription-list">
          {sources.map((source) => (
            <article key={source.key} className="settings-subscription" data-testid={`subscription-${source.kind}-${source.id}`}>
              <div className="settings-subscription-main">
                {source.kind === "engine" ? <span className="settings-engine-row-icon" aria-hidden="true"><EngineIcon engine={source.id} /></span> : null}
                <span className="settings-subscription-name">{source.label}</span>
                <span className={`settings-subscription-login settings-subscription-login-${source.login}`}>{t(loginKey(source.login))}</span>
              </div>
              <div className="settings-subscription-model">
                <span>{source.kind === "builtin" ? "Wuu" : source.engine?.protocol === "acp" ? "ACP" : "CLI"}</span>
                {source.models.length > 0 ? (
                  <SelectMenu
                    triggerClassName="settings-select-trigger"
                    value={source.selectedModel}
                    disabled={!!pending || source.login === "unavailable"}
                    ariaLabel={`${source.label} ${t("settings.subscriptionModel")}`}
                    placeholder={t("settings.subscriptionModelUnset")}
                    onChange={(model) => void choose(source, model)}
                    options={source.models.map((model) => ({ value: model.id, label: model.label }))}
                    flip
                  />
                ) : (
                  <span>{source.selectedModel || t("runtime.engineDefaultModel")}</span>
                )}
              </div>
              <Quota quota={source.quota} now={now} />
              {source.localUsage?.reported_turns ? <div className="settings-subscription-usage" title={t("settings.subscriptionLocalUsageHint")}>
                <span>{t("settings.subscriptionLocalUsage")}</span>
                <span>{t("settings.subscriptionTokens", {
                  input: new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(source.localUsage.input_tokens + source.localUsage.cache_creation_tokens + source.localUsage.cache_read_tokens),
                  output: new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(source.localUsage.output_tokens),
                })}</span>
              </div> : null}
              <details className="settings-subscription-details">
                <summary>{t("settings.subscriptionDetails")}</summary>
                {source.detail ? <p>{source.detail}</p> : null}
                {source.latest ? <>
                  <p>{t(source.latest.error ? "settings.subscriptionRequestError" : "settings.subscriptionRequest", {
                    status: t(source.latest.status === "completed" ? "channels.sessions.state.completed" : source.latest.status === "failed" ? "channels.sessions.state.failed" : source.latest.status === "interrupted" ? "channels.sessions.state.interrupted" : "settings.subscriptionUnknown"),
                    model: source.latest.model || t("settings.unknownModel"),
                    error: source.latest.error || "",
                  })}</p>
                  {source.latest.at && Number.isFinite(Date.parse(source.latest.at)) ? <time dateTime={source.latest.at}>{new Date(source.latest.at).toLocaleString()}</time> : null}
                  <p>{source.latest.usage_reported ? t("settings.subscriptionRequestUsage", {
                    input: (source.latest.input_tokens ?? 0) + (source.latest.cache_creation_tokens ?? 0) + (source.latest.cache_read_tokens ?? 0),
                    output: source.latest.output_tokens ?? 0,
                  }) : t("settings.subscriptionUsageUnknown")}</p>
                </> : <p>{t("settings.subscriptionNoRequest")}</p>}
                {source.engine?.protocol === "acp" && source.engine.enabled && source.engine.binary_ok ? <EngineAuthentication engineID={source.id} /> : null}
              </details>
            </article>
          ))}
        </div>
      )}
      {error ? <p className="settings-muted-line" role="alert">{error}</p> : null}
    </section>
  );
}

function Quota({ quota, now }: { quota?: SubscriptionQuota; now: number }): JSX.Element | null {
  const { t } = useI18n();
  const windows = quota?.status === "available" ? quota.windows?.filter((window) => Number.isFinite(window.used_percent) && window.used_percent >= 0) ?? [] : [];
  if (!windows.length) return <div className="settings-subscription-usage"><span>{t("settings.subscriptionAllowance")}</span><span>{t(quota?.status === "unavailable" ? "settings.subscriptionQuotaUnavailable" : "settings.subscriptionQuotaUnknown")}</span></div>;
  return <div className="settings-subscription-quota">
    {windows.map((window) => {
      const remaining = Math.max(0, 100 - window.used_percent);
      const minutes = window.window_minutes;
      const period = minutes ? minutes % 1440 === 0 ? t("settings.subscriptionDays", { count: minutes / 1440 }) : minutes % 60 === 0 ? t("settings.subscriptionHours", { count: minutes / 60 }) : t("settings.subscriptionMinutes", { count: minutes }) : t("settings.subscriptionAllowance");
      const label = [window.label, period].filter(Boolean).join(" · ");
      const reset = window.resets_at ? new Date(window.resets_at) : undefined;
      const checkedAt = quota?.checked_at ? Date.parse(quota.checked_at) : NaN;
      const expired = (reset && Number.isFinite(reset.getTime()) && reset.getTime() <= now) || (Number.isFinite(checkedAt) && now - checkedAt > 5 * 60_000);
      return <div key={window.id} className="settings-subscription-window" data-exhausted={remaining === 0}>
        <div className="settings-subscription-usage"><span>{label}</span><span>{expired ? t("settings.subscriptionQuotaStale") : t("settings.subscriptionRemaining", { percent: new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(remaining) })}</span></div>
        {!expired ? <meter min={0} max={100} value={remaining} aria-label={label} aria-valuetext={t("settings.subscriptionRemaining", { percent: remaining })} /> : null}
        {reset && Number.isFinite(reset.getTime()) ? <span className="settings-subscription-reset">{t("settings.subscriptionResets", { time: reset.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) })}</span> : null}
      </div>;
    })}
  </div>;
}

function loginKey(login: SubscriptionLogin): "settings.subscriptionReady" | "settings.subscriptionSignIn" | "settings.subscriptionUnavailable" | "settings.subscriptionUnknown" {
  switch (login) {
    case "ready":
      return "settings.subscriptionReady";
    case "sign_in":
      return "settings.subscriptionSignIn";
    case "unavailable":
      return "settings.subscriptionUnavailable";
    default:
      return "settings.subscriptionUnknown";
  }
}
