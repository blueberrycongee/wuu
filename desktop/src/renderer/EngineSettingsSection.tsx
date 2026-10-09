import { ChevronRight, ExternalLink, RefreshCw } from "./WuuIcons";
import { useCallback, useMemo, useState } from "react";
import type {
  EngineInfo,
  EngineListResult,
  EngineUpdateParams,
  EngineBinarySettings,
} from "../shared/protocol";
import { clearDraftEngineMemory } from "./DraftEngineMemory";
import { EngineIcon } from "./EngineIcons";
import { EngineAuthentication } from "./EngineAuthentication";
import { engineLabel } from "./EngineDisplay";
import { useI18n } from "./i18n";
import { SelectMenu, type SelectMenuOption } from "./SelectMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsPageHeader, SettingsSection } from "./SettingsSection";

const BUILTIN_ENGINE = "wuu";

type AgentRowState = { text: string; selectable: boolean };

/**
 * The Agent settings page. The default agent is one labeled picker, so the
 * choice reads as a setting rather than a column of unlabeled radios. Each
 * installed external agent then appears once, with its switch on the row and
 * its path override (and sign-in for ACP agents) under the row's disclosure.
 * An agent Wuu cannot find waits in a last group: its row opens to say why
 * once, with its install link and path override.
 */
export function EngineSettingsSection({
  result,
  loadError = "",
  onRefresh,
  onUpdate,
}: {
  result?: EngineListResult;
  loadError?: string;
  onRefresh: () => Promise<EngineListResult | undefined>;
  onUpdate: (params: EngineUpdateParams) => Promise<EngineListResult>;
}): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    setError("");
    try {
      await onRefresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setRefreshing(false);
    }
  }, [onRefresh]);

  const save = useCallback(async (params: EngineUpdateParams) => {
    setBusy(true);
    setError("");
    try {
      await onUpdate(params);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [onUpdate]);

  const engines = useMemo(() => result?.engines ?? [], [result]);
  const settings = result?.settings;
  const { default_engine: _default, ...externalSettings } = settings ?? {};
  const binarySettings: Record<string, EngineBinarySettings | undefined> = externalSettings;
  const defaultEngine = settings?.default_engine ?? BUILTIN_ENGINE;
  const engineById = useCallback(
    (id: string): EngineInfo | undefined => engines.find((e) => e.id === id),
    [engines],
  );

  const selectDefault = useCallback(
    (id: string) => {
      if (id === defaultEngine) return;
      // The composer remembers the last agent picked there. An explicit
      // default change here is the newer decision, so drop that memory
      // instead of letting it mask the setting.
      clearDraftEngineMemory();
      void save({ default_engine: id });
    },
    [defaultEngine, save],
  );

  // Row availability for an external agent. Unavailable agents stay listed
  // instead of silently leaving the choice set; the row's switch or its group
  // says why.
  const externalState = (
    engine: EngineInfo | undefined,
  ): AgentRowState => {
    if (engine?.enabled && engine.binary_ok) {
      return { text: t("settings.engineReady"), selectable: true };
    }
    if (engine && (binarySettings[engine.id]?.enabled === false || (engine.binary_ok && !engine.enabled))) {
      return { text: t("settings.engineDisabled"), selectable: false };
    }
    return { text: t("settings.engineNotInstalled"), selectable: false };
  };

  const toggleExpanded = (id: string) => setExpandedId((current) => (current === id ? null : id));

  const pathInput = (engine: EngineInfo, placeholder: string): JSX.Element => {
    const engineSettings = binarySettings[engine.id];
    return (
      <input
        key={`${engine.id}-${engineSettings?.binary_path ?? ""}`}
        className="settings-input"
        type="text"
        aria-label={`${engineLabel(engine.id, engine)} ${t("settings.engineBinaryPath")}`}
        data-testid={`settings-engine-${engine.id}-path`}
        placeholder={placeholder}
        defaultValue={engineSettings?.binary_path ?? ""}
        spellCheck={false}
        disabled={busy}
        onBlur={(event) => {
          const next = event.currentTarget.value.trim();
          if (next !== (engineSettings?.binary_path ?? "")) {
            void save({ [engine.id]: { binary_path: next } });
          }
        }}
      />
    );
  };

  // An installed agent: the switch turns it on or off, and the disclosure
  // holds its path override.
  const renderDetected = (engine: EngineInfo): JSX.Element => {
    const { id } = engine;
    const enabled = binarySettings[id]?.enabled !== false;
    const expanded = expandedId === id;
    const label = engineLabel(id, engine);
    return (
      <div
        key={id}
        className="settings-engine-item"
        data-testid={`settings-engine-${id}-status`}
        aria-label={`${label} · ${externalState(engine).text}`}
      >
        <div className="settings-engine-row">
          <span className="settings-engine-row-main">
            <span className="settings-engine-row-icon" aria-hidden="true">
              <EngineIcon engine={id} />
            </span>
            <span className="settings-engine-row-name">{label}</span>
          </span>
          <button
            className="settings-switch"
            type="button"
            role="switch"
            aria-checked={enabled}
            aria-label={`${label} ${enabled ? t("settings.engineDisable") : t("settings.engineEnable")}`}
            data-testid={`settings-engine-${id}-enabled`}
            disabled={busy}
            onClick={() => void save({ [id]: { enabled: !enabled } })}
          >
            <span className="settings-switch-thumb" aria-hidden="true" />
          </button>
          <button
            className="settings-engine-expand"
            type="button"
            aria-expanded={expanded}
            aria-label={`${label} ${t("settings.engineAdvanced")}`}
            data-testid={`settings-engine-${id}-advanced-toggle`}
            onClick={() => toggleExpanded(id)}
          >
            <ChevronRight className={`icon settings-disclosure-chevron${expanded ? " open" : ""}`} aria-hidden="true" />
          </button>
        </div>
        {expanded ? (
          <div className="settings-engine-advanced">
            {pathInput(engine, engine.binary_path || t("settings.engineAutoBinary"))}
            {engine.protocol === "acp" && enabled ? (
              <EngineAuthentication key={`${id}-${binarySettings[id]?.binary_path ?? ""}`} engineID={id} />
            ) : null}
          </div>
        ) : null}
      </div>
    );
  };

  // An agent Wuu cannot find: the whole row opens it, and the reason, the
  // install link and the path override live inside, said once.
  const renderMissing = (engine: EngineInfo): JSX.Element => {
    const { id } = engine;
    const expanded = expandedId === id;
    const label = engineLabel(id, engine);
    return (
      <div
        key={id}
        className="settings-engine-item"
        data-testid={`settings-engine-${id}-status`}
        aria-label={`${label} · ${t("settings.engineNotInstalled")}`}
      >
        <div className="settings-engine-row">
          <button
            className="settings-engine-row-main settings-engine-row-toggle"
            type="button"
            aria-expanded={expanded}
            data-testid={`settings-engine-${id}-advanced-toggle`}
            onClick={() => toggleExpanded(id)}
          >
            <span className="settings-engine-row-icon" aria-hidden="true">
              <EngineIcon engine={id} />
            </span>
            <span className="settings-engine-row-name">{label}</span>
            <span className="settings-engine-expand" aria-hidden="true">
              <ChevronRight className={`icon settings-disclosure-chevron${expanded ? " open" : ""}`} />
            </span>
          </button>
        </div>
        {expanded ? (
          <div className="settings-engine-advanced">
            <p className="settings-engine-reason">{engine.error || t("settings.engineNotInstalledHint")}</p>
            <div className="settings-engine-path-row">
              {pathInput(engine, t("settings.engineBinaryPathPlaceholder"))}
              {engine.install_url ? (
                <button
                  className="settings-button"
                  type="button"
                  onClick={() => void window.wuu.openExternal(engine.install_url!).catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))}
                >
                  {t("settings.engineInstall")}
                  <ExternalLink className="icon" aria-hidden="true" />
                </button>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>
    );
  };

  const external = engines.filter((engine) => engine.id !== BUILTIN_ENGINE);
  const detected = external.filter((engine) => engine.binary_ok);
  const missing = external.filter((engine) => !engine.binary_ok);
  // Every installed agent is listed so a switched-off one says why it cannot
  // be picked instead of silently leaving the choice set.
  const defaultOptions: SelectMenuOption[] = [
    { value: BUILTIN_ENGINE, label: engineLabel(BUILTIN_ENGINE, engineById(BUILTIN_ENGINE)), hint: t("settings.engineBuiltin") },
    ...detected.map((engine) => {
      const state = externalState(engine);
      return {
        value: engine.id,
        label: engineLabel(engine.id, engine),
        hint: state.selectable ? undefined : state.text,
        disabled: !state.selectable,
      };
    }),
  ];

  return (
    <>
      <SettingsPageHeader
        title={t("settings.agents")}
        description={t("settings.agentsDescription")}
        actions={
          <button
            className="settings-button settings-button-ghost settings-icon-button"
            type="button"
            disabled={busy || refreshing}
            aria-busy={refreshing}
            aria-label={t("settings.engineRefresh")}
            title={t("settings.engineRefresh")}
            data-testid="settings-engine-refresh"
            onClick={() => void refresh()}
          >
            <RefreshCw className={`icon${refreshing ? " settings-spin" : ""}`} aria-hidden="true" />
          </button>
        }
      />
      <section
        className="settings-section"
        data-wuu-component="settings-section"
        data-testid="settings-agent-engines"
      >
        <SettingsGroup>
          {result === undefined ? (
            <div
              className="settings-engine-skeleton"
              role="status"
              aria-label={t("settings.engineDetecting")}
              aria-busy="true"
            >
              <div className="settings-engine-row" aria-hidden="true">
                <span className="settings-engine-skeleton-line settings-engine-skeleton-title" />
                <span className="settings-engine-skeleton-line settings-engine-skeleton-description" />
              </div>
            </div>
          ) : (
            <SettingsRow title={t("settings.defaultEngine")}>
              <SelectMenu
                triggerClassName="settings-select-trigger"
                ariaLabel={t("settings.defaultEngine")}
                dataTestid="settings-default-engine"
                value={defaultEngine}
                options={defaultOptions}
                disabled={busy}
                flip
                onChange={selectDefault}
              />
            </SettingsRow>
          )}
        </SettingsGroup>
        {error || loadError ? <p className="settings-error" role="alert">{error || loadError}</p> : null}
      </section>
      {detected.length > 0 ? (
        <SettingsSection title={t("settings.engineInstalled")} testID="settings-agent-installed">
          <SettingsGroup>
            {detected.map((engine) => renderDetected(engine))}
          </SettingsGroup>
        </SettingsSection>
      ) : null}
      {missing.length > 0 ? (
        <SettingsSection title={t("settings.engineNotInstalled")} testID="settings-agent-missing">
          <SettingsGroup>
            {missing.map((engine) => renderMissing(engine))}
          </SettingsGroup>
        </SettingsSection>
      ) : null}
    </>
  );
}
