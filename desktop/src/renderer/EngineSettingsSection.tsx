import { ArrowUp, ArrowDown, ChevronRight, ExternalLink, RefreshCw } from "./WuuIcons";
import { useCallback, useId, useMemo, useState } from "react";
import type {
  EngineInfo,
  EngineListResult,
  EngineUpdateParams,
  EngineBinarySettings,
} from "../shared/protocol";
import { clearDraftEngineMemory } from "./DraftEngineMemory";
import { EngineIcon } from "./EngineIcons";
import { ConnectionTitle } from "./ConnectionTitle";
import { useConnectionDisplayPreferences } from "./ConnectionDisplayPreferences";
import { EngineAuthentication } from "./EngineAuthentication";
import { selectSubscriptionModel, subscriptionSources } from "./SubscriptionSources";
import { ConnectionPage } from "./ConnectionPage";
import { ConnectionUsageSummary } from "./ConnectionUsageSummary";
import { SubscriptionDashboard } from "./SubscriptionDashboard";
import { engineLabel } from "./EngineDisplay";
import { useI18n } from "./i18n";
import { SelectMenu, type SelectMenuOption } from "./SelectMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsPageHeader, SettingsSection } from "./SettingsSection";

const BUILTIN_ENGINE = "wuu";

type AgentRowState = { text: string; selectable: boolean };
type Translate = ReturnType<typeof useI18n>["t"];

function externalSettings(result: EngineListResult | undefined): Record<string, EngineBinarySettings | undefined> {
  const { default_engine: _default, ...binarySettings } = result?.settings ?? {};
  return binarySettings;
}

// Availability of an external agent. Unavailable agents stay listed instead of
// silently leaving the choice set; the row's switch or its group says why.
function externalState(
  engine: EngineInfo | undefined,
  binarySettings: Record<string, EngineBinarySettings | undefined>,
  t: Translate,
): AgentRowState {
  if (engine?.enabled && engine.binary_ok) {
    return { text: t("settings.engineReady"), selectable: true };
  }
  if (engine && (binarySettings[engine.id]?.enabled === false || (engine.binary_ok && !engine.enabled))) {
    return { text: t("settings.engineDisabled"), selectable: false };
  }
  return { text: t("settings.engineNotInstalled"), selectable: false };
}

/**
 * The agent new conversations start with, as one labeled picker. Every
 * installed agent is listed, so a switched-off one says why it cannot be
 * picked instead of silently leaving the choice set.
 */
export function DefaultEngineRow({
  result,
  onUpdate,
}: {
  result?: EngineListResult;
  onUpdate: (params: EngineUpdateParams) => Promise<EngineListResult>;
}): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const binarySettings = externalSettings(result);
  const defaultEngine = result?.settings?.default_engine ?? BUILTIN_ENGINE;
  const engines = result?.engines ?? [];
  const options: SelectMenuOption[] = [
    { value: BUILTIN_ENGINE, label: engineLabel(BUILTIN_ENGINE, engines.find((e) => e.id === BUILTIN_ENGINE)), hint: t("settings.engineBuiltin") },
    ...engines.filter((engine) => engine.id !== BUILTIN_ENGINE && engine.binary_ok).map((engine) => {
      const state = externalState(engine, binarySettings, t);
      return {
        value: engine.id,
        label: engineLabel(engine.id, engine),
        hint: state.selectable ? undefined : state.text,
        disabled: !state.selectable,
      };
    }),
  ];

  async function select(id: string): Promise<void> {
    if (id === defaultEngine) return;
    // The composer remembers the last agent picked there. An explicit default
    // change here is the newer decision, so drop that memory instead of
    // letting it mask the setting.
    clearDraftEngineMemory();
    setBusy(true);
    setError("");
    try {
      await onUpdate({ default_engine: id });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingsRow title={t("settings.newConversationAgent")} error={error || undefined}>
      <SelectMenu
        triggerClassName="settings-select-trigger"
        ariaLabel={t("settings.defaultEngine")}
        dataTestid="settings-default-engine"
        value={defaultEngine}
        options={options}
        // Until detection lands only the built-in agent is known.
        disabled={busy || result === undefined}
        flip
        onChange={(id) => void select(id)}
      />
    </SettingsRow>
  );
}

/** External runtime connections, with parent-owned list/detail navigation. */
export function EngineSettingsSection({
  result,
  loadError = "",
  quotaLoading = false,
  quotaFailed = false,
  quotaNow,
  onRefresh,
  onUpdate,
  embedded = false,
  showAll = false,
  selectedEngineID,
  onSelectEngine,
  onCloseEngine,
}: {
  result?: EngineListResult;
  loadError?: string;
  quotaLoading?: boolean;
  quotaFailed?: boolean;
  quotaNow?: number;
  onRefresh: () => Promise<EngineListResult | undefined>;
  onUpdate: (params: EngineUpdateParams) => Promise<EngineListResult>;
  embedded?: boolean;
  showAll?: boolean;
  selectedEngineID?: string;
  onSelectEngine: (id: string) => void;
  onCloseEngine: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const usageID = useId();
  const display = useConnectionDisplayPreferences();
  const [accountRevision, setAccountRevision] = useState(0);
  const [, setSelectionRevision] = useState(0);
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [pathDrafts, setPathDrafts] = useState<Record<string, string>>({});
  const [accountSnapshots, setAccountSnapshots] = useState<Record<string, EngineInfo>>({});
  const engines = useMemo(() => result?.engines ?? [], [result]);
  const binarySettings = externalSettings(result);
  const selected = engines.find(engine => engine.id === selectedEngineID && engine.id !== BUILTIN_ENGINE);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    setError("");
    try { await onRefresh(); }
    catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    finally { setRefreshing(false); }
  }, [onRefresh]);

  const save = useCallback(async (params: EngineUpdateParams) => {
    setBusy(true);
    setError("");
    try { await onUpdate(params); }
    catch (e) { setError(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(false); }
  }, [onUpdate]);

  const enabled = (engine: EngineInfo) => binarySettings[engine.id]?.enabled
    ?? (binarySettings[engine.id]?.binary_path ? true : engine.enabled);
  const external = engines.filter(engine => engine.id !== BUILTIN_ENGINE);
  const visible = showAll ? external : external.filter(engine => enabled(engine)
    && (engine.binary_ok || Boolean(binarySettings[engine.id]?.binary_path) || binarySettings[engine.id]?.enabled === true
      || result?.settings?.default_engine === engine.id || engine.latest_request || engine.quota
      || accountSnapshots[`${engine.id}:${binarySettings[engine.id]?.binary_path ?? ""}`]));
  // Account observations belong to an executable configuration. Preserve them
  // while disabled, but never reuse one after a path override changes.
  const accountSnapshot = (engine: EngineInfo) => {
    const saved = accountSnapshots[`${engine.id}:${binarySettings[engine.id]?.binary_path ?? ""}`];
    return saved && (!engine.binary_path || !saved.binary_path || engine.binary_path === saved.binary_path) ? saved : undefined;
  };
  const quotaFor = (engine: EngineInfo): EngineInfo["quota"] => {
    const cached = accountSnapshot(engine)?.quota;
    let quota = cached ?? engine.quota;
    if (cached && engine.quota) {
      const cachedAt = Date.parse(cached.observed_at ?? cached.checked_at);
      const inventoryAt = Date.parse(engine.quota.observed_at ?? engine.quota.checked_at);
      // A detail refresh follows the overview snapshot; prefer it when the source
      // omits timestamps, while allowing a newer overview refresh to supersede it.
      quota = inventoryAt > cachedAt ? engine.quota : cached;
    }
    return quotaFailed && quota?.status === "available" ? { ...quota, status: "stale" } : quota;
  };
  const status = (engine: EngineInfo) => !enabled(engine) ? t("settings.engineDisabled")
    : !engine.binary_ok ? t("settings.engineNotInstalled")
      : quotaFor(engine)?.status === "sign_in" ? t("settings.subscriptionSignIn")
      : engine.error || engine.models_error ? t("settings.subscriptionCatalogFailed") : t("settings.engineReady");
  const close = () => { setError(""); onCloseEngine(); };
  const refreshButton = <button
    className="settings-button settings-button-ghost settings-icon-button"
    type="button" disabled={busy || refreshing} aria-busy={refreshing}
    aria-label={t("settings.engineRefresh")} title={t("settings.engineRefresh")}
    data-testid="settings-engine-refresh" onClick={() => void refresh()}
  ><RefreshCw className={`icon${refreshing ? " settings-spin" : ""}`} aria-hidden="true" /></button>;

  const selectedSettings = selected ? binarySettings[selected.id] : undefined;
  const accountKey = selected ? `${selected.id}:${selectedSettings?.binary_path ?? ""}` : "";
  const snapshot = selected ? accountSnapshot(selected) : undefined;

  const observedEngine = selected ? { ...selected, ...(snapshot && enabled(selected) ? snapshot : {}), enabled: selected.enabled, quota: quotaFor(selected) } : undefined;
  const modelSource = observedEngine ? subscriptionSources({ engines: [observedEngine] }, [], observedEngine.id)[0] : undefined;
  const displayKey = selected ? `engine:${selected.id}` : "";
  const models = display.orderModels(displayKey, (observedEngine?.models ?? []).map(model => ({ id: model.id, label: model.display_name?.trim() || model.id })));
  const headerStatus = selected && enabled(selected) && selected.binary_ok && !selected.error && !selected.models_error
    && quotaFor(selected)?.status !== "sign_in" && modelSource?.login !== "ready"
    ? t("settings.subscriptionUnknown") : selected ? status(selected) : "";
  const hiddenModels = display.get(displayKey).hiddenModels;
  const updateDisplay = (change: () => void) => {
    try { change(); setError(""); }
    catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  };
  const refreshAccount = async () => {
    const loaded = await window.wuu.listEngines({ include_quota: true });
    const observed = loaded.engines.find(engine => engine.id === selected?.id);
    if (observed) setAccountSnapshots(current => ({ ...current, [accountKey]: observed }));
    setAccountRevision(value => value + 1);
  };

  if (selected) return <ConnectionPage key={selected.id}
    title={<ConnectionTitle connectionKey={displayKey} fallback={engineLabel(selected.id, selected)} disabled={busy}
      suffix={selected.cli_version ? <span className="connection-engine-version">{selected.cli_version}</span> : undefined} />}
    icon={<EngineIcon engine={selected.id} />}
    subtitle={<>{quotaFor(selected)?.plan?.trim() ? <span>{t("connection.subscriptionPlan", { provider: selected.id === "codex" ? "ChatGPT" : selected.id === "claude" ? "Claude" : engineLabel(selected.id, selected), plan: quotaFor(selected)!.plan! })}</span> : null}
      <span className="connection-status-pill" data-attention={headerStatus !== t("settings.engineReady")}>{headerStatus}</span></>}
    actions={<>
      {selected.binary_ok && (selected.update_url || selected.install_url) ? <button className="settings-button" type="button" data-testid="engine-update-guide"
        title={t("settings.engineUpdateGuideHint")} disabled={busy}
        onClick={() => void window.wuu.openExternal((selected.update_url || selected.install_url)!).catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))}>
        {t("settings.engineUpdateGuide")}
      </button> : null}
      <EngineAuthentication key={`auth-${accountKey}`} entry engineID={selected.id} protocol={selected.protocol}
        disabled={busy || !enabled(selected) || !selected.binary_ok} onReuseLocal={refreshAccount} onAuthenticated={refreshAccount} />
    </>}
    onClose={close} closeDisabled={busy}>
    <SettingsSection title={t("settings.engineRuntime")}>
      <SettingsRow title={t("settings.engineEnable")}>
        <button type="button" className="settings-switch" role="switch" aria-checked={enabled(selected)}
          aria-label={`${engineLabel(selected.id, selected)} ${t(enabled(selected) ? "settings.engineDisable" : "settings.engineEnable")}`}
          data-testid={`settings-engine-${selected.id}-enabled`} disabled={busy}
          onClick={() => void save({ [selected.id]: { enabled: !enabled(selected) } })}>
          <span className="settings-switch-thumb" aria-hidden="true" />
        </button>
      </SettingsRow>
      <SettingsRow title={t("settings.engineBinaryPath")} block>
        <input key={`path-${selected.id}-${selectedSettings?.binary_path ?? ""}`} className="settings-input"
          type="text" aria-label={`${engineLabel(selected.id, selected)} ${t("settings.engineBinaryPath")}`}
          data-testid={`settings-engine-${selected.id}-path`} placeholder={selected.binary_path || t("settings.engineAutoBinary")}
          value={pathDrafts[accountKey] ?? selectedSettings?.binary_path ?? ""} spellCheck={false} disabled={busy}
          onChange={event => setPathDrafts(current => ({ ...current, [accountKey]: event.target.value }))} />
        <button type="button" className="settings-button"
          data-testid={`settings-engine-${selected.id}-path-save`}
          disabled={busy || (pathDrafts[accountKey] ?? selectedSettings?.binary_path ?? "").trim() === (selectedSettings?.binary_path ?? "")}
          onClick={() => void save({ [selected.id]: { binary_path: (pathDrafts[accountKey] ?? "").trim() } })}>
          {t("common.save")}
        </button>
      </SettingsRow>
      {!selected.binary_ok ? <SettingsRow title={t("settings.engineInstall")}
        description={selected.error || t("settings.engineNotInstalledHint")}>
        {selected.install_url ? <button className="settings-button" type="button"
          onClick={() => void window.wuu.openExternal(selected.install_url!).catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))}>
          {t("settings.engineInstall")}<ExternalLink className="icon" aria-hidden="true" />
        </button> : null}
      </SettingsRow> : null}
      {error || loadError ? <p className="settings-error" role="alert">{error || loadError}</p> : null}
      {selected.binary_ok && selected.error ? <p className="settings-engine-reason">{selected.error}</p> : null}
    </SettingsSection>
    <SettingsSection title={t("settings.engineModels")}>
      {models.length ? <div className="connection-model-table" role="table" aria-label={t("settings.engineModels")}>
        <div className="connection-model-row connection-model-head" role="row">
          <span role="columnheader">{t("settings.engineModelName")}</span><span role="columnheader">{t("settings.engineModelID")}</span>
          <span role="columnheader">{t("settings.engineModelEnabled")}</span><span role="columnheader" className="sr-only">{t("settings.engineModelOrder")}</span>
        </div>
        {models.map((model, index) => <div key={model.id} className="connection-model-row" role="row" data-model-id={model.id}>
          <span className="connection-model-name" role="cell">{model.label}</span>
          <span className="connection-model-id" role="cell">{model.id}</span>
          <span className="connection-model-enabled" role="cell"><button type="button" className="settings-switch" role="switch"
            aria-label={`${model.label} ${t("settings.engineModelEnabled")}`} aria-checked={!hiddenModels.includes(model.id)} disabled={busy}
            onClick={() => updateDisplay(() => display.setModelVisible(displayKey, model.id, hiddenModels.includes(model.id)))}>
            <span className="settings-switch-thumb" aria-hidden="true" />
          </button></span>
          <span className="connection-model-order" role="cell">
            <button type="button" className="settings-button settings-button-ghost settings-icon-button" disabled={busy || index === 0}
              aria-label={t("settings.engineModelUp", { name: model.label })} onClick={() => updateDisplay(() => display.moveModel(displayKey, models, model.id, -1))}><ArrowUp className="icon" /></button>
            <button type="button" className="settings-button settings-button-ghost settings-icon-button" disabled={busy || index === models.length - 1}
              aria-label={t("settings.engineModelDown", { name: model.label })} onClick={() => updateDisplay(() => display.moveModel(displayKey, models, model.id, 1))}><ArrowDown className="icon" /></button>
          </span>
        </div>)}
      </div> : <p className="settings-muted-line">{t("settings.subscriptionModelUnset")}</p>}
      {observedEngine?.models_error ? <p className="settings-error" role="alert">{observedEngine.models_error}</p> : null}
    </SettingsSection>
    <details className="connection-engine-advanced">
      <summary>{t("settings.engineAdvanced")}</summary>
      <SettingsRow title={t("settings.engineModelCurrent")}>
        {refreshButton}
        {modelSource && models.length ? <SelectMenu triggerClassName="settings-select-trigger"
          ariaLabel={t("settings.engineModelCurrent")} value={modelSource.selectedModel} disabled={busy || !enabled(selected) || !selected.binary_ok}
          options={display.visibleModels(displayKey, models, [modelSource.selectedModel]).map(model => ({ value: model.id, label: model.label }))}
          placeholder={t("settings.subscriptionModelUnset")} onChange={id => { selectSubscriptionModel(modelSource, id); setSelectionRevision(value => value + 1); }} /> : null}
      </SettingsRow>
      <SubscriptionDashboard key={`account-${accountKey}-${selected.binary_path ?? ""}-${accountRevision}`} detailOnly
        scope={{ kind: "engine", id: selected.id }} inventory={result}
        initialInventory={snapshot ? { engines: [snapshot] } : undefined}
        onInventoryLoaded={loaded => {
          const observed = loaded.engines.find(engine => engine.id === selected.id);
          if (observed) setAccountSnapshots(current => ({ ...current, [accountKey]: observed }));
        }} providers={[]} onSelectBuiltinModel={async () => {}} />
    </details>
  </ConnectionPage>;

  return <>
    {!embedded ? <SettingsPageHeader title={t("settings.agents")} /> : null}
    <SettingsSection title={t("provider.connectionEngines")} actions={embedded ? undefined : refreshButton} testID="settings-engine-connections">
      {error || loadError ? <p className="settings-error" role="alert">{error || loadError}</p> : null}
      {result === undefined ? <div className="settings-engine-skeleton" role="status" aria-label={t("settings.engineDetecting")} aria-busy="true">
        <div className="settings-engine-row" aria-hidden="true"><span className="settings-engine-skeleton-line settings-engine-skeleton-title" /></div>
      </div> : visible.length ? <div className="connection-summary-list">
        {visible.map(engine => {
          const quota = quotaFor(engine);
          return <button key={engine.id} type="button"
            className="connection-summary-row"
            data-testid={`settings-engine-${engine.id}-status`}
            aria-label={`${display.label(`engine:${engine.id}`, engineLabel(engine.id, engine))} · ${status(engine)}`}
            aria-describedby={`${usageID}-${engine.id} ${usageID}-${engine.id}-quota`}
            onClick={() => { setError(""); onSelectEngine(engine.id); }}>
            <span className="connection-summary-icon" aria-hidden="true"><EngineIcon engine={engine.id} /></span>
            <span className="connection-summary-copy" id={`${usageID}-${engine.id}`}>
              <span className="connection-summary-name">{display.label(`engine:${engine.id}`, engineLabel(engine.id, engine))}</span>
              <span className="connection-summary-meta"><span>{quota?.plan?.trim() || t("provider.localEngine")}</span><span aria-hidden="true"> · </span>
                <span className="connection-summary-status">{status(engine)}</span>
              </span>
            </span>
            <ConnectionUsageSummary id={`${usageID}-${engine.id}-quota`} quota={quota} loading={quotaLoading || refreshing} now={quotaNow} />
            <ChevronRight className="icon" aria-hidden="true" />
          </button>;
        })}
      </div> : <p className="settings-muted-line">{t("provider.engineConnectionsEmpty")}</p>}
    </SettingsSection>
  </>;
}
