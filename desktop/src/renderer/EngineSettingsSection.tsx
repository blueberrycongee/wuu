import { ChevronRight, ExternalLink, RefreshCw } from "./WuuIcons";
import { useCallback, useMemo, useState, type ReactNode } from "react";
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
import { SettingsPageHeader, SettingsSection } from "./SettingsSection";

const BUILTIN_ENGINE = "wuu";

type AgentRowState = { text: string; selectable: boolean };

/**
 * The Agent settings page: one row per agent, so each agent appears exactly
 * once. Detected agents share the default radio group, each with its switch on
 * the row; the path override (and sign-in for ACP agents) opens under the
 * row's disclosure. An agent Wuu cannot find waits in a second group: its row
 * opens to say why once, with its install link and path override, instead of
 * repeating an install icon on every row.
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

  // A detected agent: the radio picks the default, the switch turns it on or
  // off, and the disclosure holds its path override.
  const renderDetectedRow = (
    id: string,
    state: AgentRowState,
    control?: ReactNode,
    advanced?: ReactNode,
  ): JSX.Element => {
    const expanded = expandedId === id;
    const engine = engineById(id);
    const label = engineLabel(id, engine);
    return (
      <div
        key={id}
        className="settings-engine-item"
        data-testid={`settings-engine-${id}-status`}
        aria-label={`${label} · ${state.text}`}
      >
        <div className="settings-engine-row">
          <input
            id={`settings-engine-radio-${id}`}
            className="settings-engine-radio"
            type="radio"
            name="settings-default-engine"
            checked={defaultEngine === id}
            disabled={busy || !state.selectable}
            data-testid={`settings-engine-${id}-radio`}
            onChange={() => selectDefault(id)}
          />
          <label
            className="settings-engine-row-main"
            htmlFor={`settings-engine-radio-${id}`}
          >
            <span className="settings-engine-row-icon" aria-hidden="true">
              <EngineIcon engine={id} />
            </span>
            <span className="settings-engine-row-name">{label}</span>
          </label>
          {control}
          {advanced ? (
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
          ) : (
            // Keeps switches aligned with rows that have a disclosure.
            <span className="settings-engine-expand" aria-hidden="true" />
          )}
        </div>
        {advanced && expanded ? (
          <div className="settings-engine-advanced">{advanced}</div>
        ) : null}
      </div>
    );
  };

  const renderDetected = (engine: EngineInfo): JSX.Element => {
    const { id } = engine;
    const enabled = binarySettings[id]?.enabled !== false;
    const label = engineLabel(id, engine);
    return renderDetectedRow(
      id,
      externalState(engine),
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
      </button>,
      <>
        {pathInput(engine, engine.binary_path || t("settings.engineAutoBinary"))}
        {enabled ? (
          <EngineAuthentication key={`${id}-${binarySettings[id]?.binary_path ?? ""}`} engineID={id} protocol={engine.protocol ?? id} binaryPath={engine.binary_path} />
        ) : null}
      </>,
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
          {/* The radio's footprint keeps both groups on one icon column. */}
          <span className="settings-engine-radio is-placeholder" aria-hidden="true" />
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
        className="settings-section settings-agent-section"
        data-wuu-component="settings-section"
        data-testid="settings-agent-engines"
      >
        <div className="settings-group settings-engine-body">
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
            <div role="radiogroup" aria-label={t("settings.defaultEngine")}>
              {renderDetectedRow(BUILTIN_ENGINE, {
                text: t("settings.engineBuiltin"),
                selectable: true,
              })}
              {detected.map((engine) => renderDetected(engine))}
            </div>
          )}
        </div>
        {error || loadError ? <p className="settings-error" role="alert">{error || loadError}</p> : null}
      </section>
      {missing.length > 0 ? (
        <SettingsSection title={t("settings.engineNotInstalled")} testID="settings-agent-missing">
          <div className="settings-group settings-engine-body">
            {missing.map((engine) => renderMissing(engine))}
          </div>
        </SettingsSection>
      ) : null}
    </>
  );
}
