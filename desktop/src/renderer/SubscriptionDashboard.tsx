import { useEffect, useMemo, useState } from "react";
import { MoreHorizontal, RefreshCw } from "./WuuIcons";
import type { EngineListResult, ProviderSummary } from "../shared/protocol";
import { EngineIcon } from "./EngineIcons";
import { EngineAuthentication } from "./EngineAuthentication";
import { ServiceMark, serviceIdentity } from "./ModelServicesPage";
import { SelectMenu } from "./SelectMenu";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { useI18n } from "./i18n";
import { SettingsPageHeader } from "./SettingsSection";
import {
  hasBuiltInSubscriptionControls,
  isCodexSubscription,
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
  const { t, formatDate } = useI18n();
  const [error, setError] = useState<"" | "settings.subscriptionRefreshFailed" | "settings.subscriptionModelFailed">("");
  const [authProviders, setAuthProviders] = useState<ProviderSummary[]>([]);
  const [authError, setAuthError] = useState(false);
  const [checkedProvider, setCheckedProvider] = useState("");
  const [pending, setPending] = useState("");
  const [revision, setRevision] = useState(0);
  const [menu, setMenu] = useState<{ sourceKey: string; x: number; y: number } | null>(null);
  const [loadedInventory, setLoadedInventory] = useState<EngineListResult>();
  // The mount effect always starts a load; begin busy so the first frame
  // already reserves the quota placeholders.
  const [refreshing, setRefreshing] = useState(true);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [now, setNow] = useState(Date.now);
  useEffect(() => { setAuthProviders([]); setCheckedProvider(""); }, [providers]);
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
      const currentProviders = (providers ?? loadedInventory?.subscription_providers ?? []).map((current) => {
        const snapshot = loadedInventory?.subscription_providers?.find((item) => item.name === current.name);
        const provider = snapshot ? { ...snapshot, ...current, quota: snapshot.quota ?? current.quota } : current;
        const checked = authProviders.find((item) => item.name === provider.name);
        return checked ? { ...provider, reuse_codex_credentials: checked.reuse_codex_credentials,
          codex_credential_source: checked.codex_credential_source, api_key_configured: checked.api_key_configured,
          quota: provider.quota } : provider;
      });
      return subscriptionSources(refreshed, currentProviders);
    },
    [loadedInventory, inventory, providers, revision, authProviders],
  );

  // Group presentation only. Every connection keeps its own account snapshot,
  // model selection and credential actions, including accounts at one vendor.
  const groups = new Map<string, SubscriptionSource[]>();
  for (const source of sources) {
    const key = source.provider ? `service:${serviceIdentity(source.provider, t).label}` : source.key;
    const group = groups.get(key);
    if (group) group.push(source);
    else groups.set(key, [source]);
  }
  const menuSource = sources.find((source) => source.key === menu?.sourceKey);

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

  async function checkCodexLogin(source: SubscriptionSource, useLocal: boolean): Promise<void> {
    if (pending) return;
    setPending(source.key);
    setAuthError(false);
    setCheckedProvider("");
    try {
      if (useLocal) {
        const result = await window.wuu.useCodexCredentials(source.id);
        setAuthProviders(result.providers);
      }
      const result = await window.wuu.loadCodexModels(source.id);
      setAuthProviders(result.providers);
      setCheckedProvider(source.id);
    } catch {
      setAuthError(true);
    } finally {
      setPending("");
      setLoadedInventory(undefined);
      setRefreshVersion((value) => value + 1);
    }
  }

  return (
    <section className="settings-section settings-subscriptions" data-testid="settings-subscriptions" aria-busy={refreshing}>
      <SettingsPageHeader
        title={t("settings.subscriptions")}
        actions={
          <button type="button" className="settings-button settings-button-ghost settings-icon-button" disabled={refreshing} onClick={() => setRefreshVersion((value) => value + 1)} aria-label={t(refreshing ? "settings.subscriptionRefreshing" : "settings.subscriptionRefresh")} title={t("settings.subscriptionRefresh")}>
            <RefreshCw className={refreshing ? "icon settings-spin" : "icon"} aria-hidden="true" />
          </button>
        }
      />
      {sources.length === 0 ? (
        inventory || loadedInventory ? <p className="settings-muted-line">{t("settings.subscriptionsEmpty")}</p>
          : refreshing ? (
            <div className="settings-subscription-list settings-subscription-skeleton" role="status" aria-label={t("settings.engineDetecting")} aria-busy="true">
              {[0, 1, 2].map((item) => (
                <div key={item} className="settings-subscription" aria-hidden="true">
                  <div className="settings-subscription-main">
                    <span className="settings-usage-skeleton-line settings-subscription-skeleton-icon" />
                    <span className="settings-usage-skeleton-line settings-subscription-skeleton-text settings-subscription-skeleton-name" />
                  </div>
                  <QuotaSkeleton />
                </div>
              ))}
            </div>
          ) : null
      ) : (
        <div className="settings-subscription-list">
          {[...groups].map(([key, accounts]) => {
            const first = accounts[0];
            const identity = first.provider ? serviceIdentity(first.provider, t) : undefined;
            return (
              <article key={key} className="settings-subscription" aria-label={identity?.label ?? first.label}>
                {accounts.map((source, index) => {
                  const showAuthentication = source.engine?.protocol === "acp" && source.engine.enabled && source.engine.binary_ok
                    && (source.login !== "ready" || source.catalogFailed);
                  const status = source.login === "unavailable" ? "settings.subscriptionUnavailable"
                    : source.catalogFailed ? "settings.subscriptionCatalogFailed"
                      : source.login === "sign_in" && source.quota?.status !== "sign_in" ? "settings.subscriptionSignIn" : null;
                  const observed = source.quota?.status === "available" || source.quota?.status === "stale";
                  const account = observed ? source.quota?.account : undefined;
                  const serviceNamedConnection = source.id.toLowerCase() === identity?.label.toLowerCase();
                  const accountName = account?.label || (source.provider && (accounts.length > 1 || !serviceNamedConnection) ? source.id : undefined);
                  const origin = account?.source;
                  const observedAt = source.quota?.observed_at ?? source.quota?.checked_at;
                  const observedMillis = observedAt ? Date.parse(observedAt) : NaN;
                  const credentialSource = origin || source.provider?.codex_credential_source;
                  const credential = credentialSource ?
                    ["codex-cli", "codex-cli-readonly"].includes(credentialSource) ? "Codex CLI"
                      : credentialSource === "claude-code" ? "Claude Code"
                        : credentialSource === "grok-cli" ? "Grok CLI"
                          : credentialSource === "wuu-auth-store" ? "Wuu"
                            : ["explicit", "configured", "anthropic-oauth", "deepseek", "openrouter", "kimi-code", "zhipu"].includes(credentialSource) ? t("settings.subscriptionCredentialConfigured")
                              : credentialSource
                    : source.provider?.reuse_codex_credentials ? "Codex CLI"
                      : source.provider?.api_key_configured ? t("settings.subscriptionCredentialApiKey") : undefined;
                  const sourceLabel = [source.provider && accountName !== source.id && !serviceNamedConnection ? source.id : undefined, credential].filter(Boolean).join(" · ");
                  const observedLabel = Number.isFinite(observedMillis) ? t(source.quota?.observed_at ? "settings.subscriptionObserved" : "settings.subscriptionChecked", { age: formatAge(observedMillis, now, t) }) : undefined;
                  const showObservationAge = !sourceLabel && source.quota?.status === "available" && !(source.quota.expires_at && Date.parse(source.quota.expires_at) <= now);
                  const observationTitle = Number.isFinite(observedMillis) ? formatDate(observedMillis, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZoneName: "short" }) : undefined;
                  const showModel = source.models.length > 0 && (source.kind === "engine" || (source.provider && hasBuiltInSubscriptionControls(source.provider)));
                  const controls = <div className="settings-subscription-controls">
                    {showModel ? <SelectMenu
                      triggerClassName="settings-subscription-model-trigger"
                      value={source.selectedModel}
                      disabled={!!pending || source.login === "unavailable"}
                      ariaLabel={`${source.label} ${t("settings.subscriptionModel")}`}
                      placeholder={t("settings.subscriptionModelUnset")}
                      onChange={(model) => void choose(source, model)}
                      options={source.models.map((model) => ({ value: model.id, label: model.label }))}
                      flip
                    /> : null}
                    {isCodexSubscription(source.provider?.type) ? <button
                      type="button"
                      className="settings-button settings-button-ghost settings-icon-button settings-subscription-more"
                      data-testid="subscription-account-actions"
                      aria-label={pending === source.key ? t("settings.codexChecking") : t("provider.moreActions", { name: accountName ?? source.label })}
                      aria-haspopup="menu"
                      aria-expanded={menu?.sourceKey === source.key}
                      disabled={!!pending}
                      onClick={(event) => {
                        const bounds = event.currentTarget.getBoundingClientRect();
                        setMenu({ sourceKey: source.key, x: bounds.right, y: bounds.bottom + 4 });
                      }}
                    >{pending === source.key ? <RefreshCw className="icon settings-spin" aria-hidden="true" /> : <MoreHorizontal className="icon" aria-hidden="true" />}</button> : null}
                  </div>;
                  const attribution = [sourceLabel && t("settings.subscriptionAccountSource", { source: sourceLabel }), observedLabel, observationTitle].filter(Boolean).join(" · ");
                  return <section key={source.key} className="settings-subscription-source" title={attribution} data-testid={`subscription-${source.kind}-${source.id}`}>
                    {index === 0 ? <div className="settings-subscription-main">
                      {identity ? <ServiceMark identity={identity} />
                        : <span className="settings-engine-row-icon" aria-hidden="true"><EngineIcon engine={first.id} /></span>}
                      <h2 className="settings-subscription-name">{identity?.label ?? first.label}</h2>
                      {accounts.length === 1 ? controls : null}
                    </div> : null}
                    {accountName || (observed && source.quota?.plan) || accounts.length > 1 ? <div className="settings-subscription-account" title={attribution}>
                      <span className="settings-subscription-account-name">{accountName || sourceLabel}</span>

                      {observed && source.quota?.plan ? <span className="settings-subscription-plan">{source.quota.plan}</span> : null}
                      {accounts.length > 1 ? controls : null}
                    </div> : null}
                    {sourceLabel ? <span className="sr-only">{t("settings.subscriptionAccountSource", { source: sourceLabel })}</span> : null}
                    {status ? <p className="settings-subscription-status">{t(status)}</p> : null}
                    {quotaLoading && source.engine?.enabled && source.engine.binary_ok && source.engine.capabilities?.includes("account-quota")
                      ? <QuotaSkeleton />
                      : <Quota source={source} now={now} />}
                    {(observedLabel && showObservationAge) || showAuthentication ? <div className="settings-subscription-actions">
                      {observedLabel && showObservationAge ? <time className="settings-subscription-age" dateTime={observedAt} title={observationTitle}>{observedLabel}</time> : null}
                      {showAuthentication ? <EngineAuthentication engineID={source.id} compact onAuthenticated={() => setRefreshVersion((value) => value + 1)} /> : null}
                    </div> : null}
                    {checkedProvider === source.id ? <p className="settings-subscription-status" role="status">{t("settings.codexLoginVerified")}</p> : null}
                  </section>;
                })}
              </article>
            );
          })}
        </div>
      )}
      {menu && menuSource ? <ThreadContextMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)} items={[
        { label: t("settings.codexUseLocal"), disabled: !!pending, onSelect: () => checkCodexLogin(menuSource, true) },
        { label: t("settings.codexCheckLogin"), disabled: !!pending, onSelect: () => checkCodexLogin(menuSource, false) },
      ]} /> : null}
      {authError ? <p className="settings-subscription-status" role="alert">{t("settings.subscriptionLoginFailed")}</p> : null}
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

function Quota({ source, now }: { source: SubscriptionSource; now: number }): JSX.Element | null {
  const { t, formatNumber, formatDate } = useI18n();
  const quota = source.quota;
  if (!quota) return null;
  const statusKey = quota.status === "stale" ? "settings.subscriptionQuotaRefreshFailed"
    : quota.status === "unavailable" ? "settings.subscriptionQuotaUnavailable"
      : quota.status === "sign_in" ? "settings.subscriptionSignIn"
        : quota.status === "unsupported" ? "settings.subscriptionQuotaUnsupported" : null;
  const expiredSnapshot = quota.status === "available" && quota.expires_at !== undefined && Date.parse(quota.expires_at) <= now;
  const windows = quota.status === "available" || quota.status === "stale" ? quota.windows ?? [] : [];
  const balances = quota.status === "available" || quota.status === "stale" ? quota.balances ?? [] : [];

  const observation = Date.parse(quota.observed_at ?? quota.checked_at);
  const observationAge = Number.isFinite(observation) ? t(quota.observed_at ? "settings.subscriptionObserved" : "settings.subscriptionChecked", { age: formatAge(observation, now, t) }) : "";
  return <section className="settings-subscription-quota" aria-label={t(quota.kind === "plan" ? "settings.subscriptionKindPlan" : quota.kind === "balance" ? "settings.subscriptionKindBalance" : quota.kind === "subscription" ? "settings.subscriptionKindSubscription" : "settings.subscriptionAllowance")}>
    {statusKey || expiredSnapshot ? <p className="settings-subscription-quota-state" role="status">{[t(statusKey ?? "settings.subscriptionQuotaStale"), (quota.status === "stale" || expiredSnapshot) && observationAge].filter(Boolean).join(" · ")}</p> : null}
    {windows.length ? <div className="settings-subscription-windows">
      {windows.map((window) => {
        const percent = window.used_percent;
        const hasPercent = typeof percent === "number" && Number.isFinite(percent) && percent >= 0;
        const unlimited = window.unlimited === true;
        const remaining = hasPercent ? Math.max(0, 100 - percent) : undefined;
        const minutes = window.window_minutes;
        const period = minutes && minutes > 0 ? minutes % 1440 === 0 ? t("settings.subscriptionDays", { count: minutes / 1440 }) : minutes % 60 === 0 ? t("settings.subscriptionHours", { count: minutes / 60 }) : t("settings.subscriptionMinutes", { count: minutes }) : undefined;
        const periodOnly = /^(?:\d+ (?:hours|days|minutes)|Primary|Secondary|Daily|Weekly|Weekly allowance|Rolling window|Requests|Allowance)$/i.test(window.label ?? "");
        const label = (periodOnly && period ? period : [window.label, period].filter(Boolean).join(" · ")) || t("settings.subscriptionAllowance");
        const resetMillis = window.resets_at ? Date.parse(window.resets_at) : NaN;
        const expired = quota.status === "stale" || (quota.expires_at !== undefined && Date.parse(quota.expires_at) <= now)
          || (Number.isFinite(resetMillis) && resetMillis <= now);
        const readout = unlimited ? t(expired ? "settings.subscriptionLastKnownUnlimited" : "settings.subscriptionUnlimited")
          : remaining === undefined ? t("settings.subscriptionQuotaUnknown")
            : expired ? t("settings.subscriptionLastKnownRemaining", { percent: formatNumber(remaining, { maximumFractionDigits: 1 }) })
              : t("settings.subscriptionRemaining", { percent: formatNumber(remaining, { maximumFractionDigits: 1 }) });
        const resetTitle = Number.isFinite(resetMillis) ? t("settings.subscriptionResetTime", { time: formatDate(resetMillis, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZoneName: "short" }) }) : undefined;
        const resetMinutes = Math.max(1, Math.ceil((resetMillis - now) / 60_000));
        const resetDuration = resetMinutes < 60 ? t("settings.subscriptionMinutes", { count: resetMinutes })
          : resetMinutes < 1440 ? [t("settings.subscriptionHours", { count: Math.floor(resetMinutes / 60) }), resetMinutes % 60 ? t("settings.subscriptionMinutes", { count: resetMinutes % 60 }) : ""].filter(Boolean).join(" ")
            : [t("settings.subscriptionDays", { count: Math.floor(resetMinutes / 1440) }), Math.floor(resetMinutes % 1440 / 60) ? t("settings.subscriptionHours", { count: Math.floor(resetMinutes % 1440 / 60) }) : ""].filter(Boolean).join(" ");
        const knownLabels = [source.id, source.label, source.selectedModel, source.models.find((model) => model.id === source.selectedModel)?.label].map((value) => value?.toLowerCase());
        const scope = [window.model, window.scope].filter((value) => value && value.toLowerCase() !== "all models" && !knownLabels.includes(value.toLowerCase())).join(" · ");
        return <div key={window.id} className="settings-subscription-window" title={[window.model, window.scope, resetTitle].filter(Boolean).join(" · ")} data-exhausted={!expired && remaining === 0}>
          <div className="settings-subscription-window-heading">
            <div className="settings-subscription-usage"><span>{label}</span><span className="settings-subscription-remaining">{readout}</span></div>
            {window.display ? <p className="settings-subscription-window-display">{window.display}</p> : null}
            {scope ? <p className="settings-subscription-window-scope">{scope}</p> : null}
          </div>
          {!expired && !unlimited && remaining !== undefined ? <meter min={0} max={100} value={remaining} aria-label={label} aria-valuetext={readout} /> : <span aria-hidden="true" />}
          <span className="settings-subscription-reset">{window.resets_at && resetTitle ? <time className={resetMillis <= now ? "sr-only" : undefined} dateTime={window.resets_at} title={resetTitle} aria-label={resetTitle}>{resetMillis <= now
            ? t("settings.subscriptionResetTime", { time: formatDate(resetMillis, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) })
            : t("settings.subscriptionResetsIn", { time: resetDuration })}</time> : null}</span>
        </div>;
      })}
    </div> : null}
    {quota.status === "available" && !windows.length && !balances.length ? <p className="settings-subscription-empty">{t("settings.subscriptionQuotaUnknown")}</p> : null}
    {balances.length ? <div className="settings-subscription-balances" aria-label={t("settings.subscriptionBalances")}>
      <div className="settings-subscription-balance">
        <span>{t("settings.subscriptionKindBalance")}</span>
        <span className="settings-subscription-balance-amount">{balances.map((balance, index) => <span key={`${balance.currency}-${index}`}>
          {index > 0 ? " · " : ""}{balance.amount} {balance.currency}
        </span>)}</span>
      </div>
    </div> : null}
    {(quota.status === "available" || quota.status === "stale") && quota.reset_credits !== undefined
      ? <div className="settings-subscription-terms">{t("settings.subscriptionResetCredits", { count: quota.reset_credits })}</div> : null}
  </section>;
}

function formatAge(observedAt: number, now: number, t: (key: "settings.subscriptionAgeMinutes" | "settings.subscriptionAgeHours" | "settings.subscriptionAgeDays", options?: Record<string, string | number>) => string): string {
  const minutes = Math.floor(Math.max(0, now - observedAt) / 60_000);
  if (minutes < 60) return t("settings.subscriptionAgeMinutes", { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return t("settings.subscriptionAgeHours", { count: hours });
  return t("settings.subscriptionAgeDays", { count: Math.floor(hours / 24) });
}
