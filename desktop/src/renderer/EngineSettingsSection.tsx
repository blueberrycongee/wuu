import { ChevronRight, Download, RefreshCw } from "./WuuIcons";
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
import { SettingsPageHeader, SettingsSection, SettingsStatus, type SettingsStatusTone } from "./SettingsSection";

const BUILTIN_ENGINE = "wuu";

type AgentRowState = { text: string; tone: SettingsStatusTone; selectable: boolean };

/**
 * The Agent settings page: one row per agent, so each agent appears exactly
 * once. Detected agents share the default radio group; an agent Wuu cannot
 * find waits in a second group under one heading instead of repeating why on
 * every row. Binary path overrides and explicit enable/disable live behind
 * the row's own expand control.
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

  // Row availability for an external agent. Unavailable agents stay listed —
  // the status explains why — instead of silently leaving the choice set.
  const externalState = (
    engine: EngineInfo | undefined,
  ): AgentRowState => {
    if (engine?.enabled && engine.binary_ok) {
      return { text: t("settings.engineReady"), tone: "success", selectable: true };
    }
    if (engine && (binarySettings[engine.id]?.enabled === false || (engine.binary_ok && !engine.enabled))) {
      return { text: t("settings.engineDisabled"), tone: "neutral", selectable: false };
    }
    return { text: t("settings.engineNotInstalled"), tone: "neutral", selectable: false };
  };

  // Placeholder detail for the path override input: the resolved binary
  // path, or why detection failed.
  const statusLine = (engine: EngineInfo | undefined): string => {
    if (!engine) return "";
    if (!engine.binary_ok) {
      return engine.error || t("settings.engineNotInstalledHint");
    }
    return engine.binary_path || t("settings.engineAutoBinary");
  };

  const renderAgentRow = (
    id: string,
    state: AgentRowState,
    advanced?: ReactNode,
    missing = false,
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
          {/* A missing agent keeps the radio's footprint so both groups
            * share one icon column. */}
          <input
            id={`settings-engine-radio-${id}`}
            className={`settings-engine-radio${missing ? " is-placeholder" : ""}`}
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
          {/* Ready agents need no label: the enabled radio says it. */}
          {state.selectable || missing ? null : <SettingsStatus tone={state.tone}>{state.text}</SettingsStatus>}
          {missing && engine?.install_url ? (
            <button
              className="settings-button settings-button-ghost settings-icon-button"
              type="button"
              aria-label={`${t("settings.engineInstall")} ${label}`}
              title={t("settings.engineInstall")}
              onClick={() => void window.wuu.openExternal(engine.install_url!).catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))}
            >
              <Download className="icon" aria-hidden="true" />
            </button>
          ) : null}
          {advanced ? (
            <button
              className="settings-engine-expand"
              type="button"
              aria-expanded={expanded}
              aria-label={`${label} ${t("settings.engineAdvanced")}`}
              data-testid={`settings-engine-${id}-advanced-toggle`}
              onClick={() =>
                setExpandedId((current) => (current === id ? null : id))
              }
            >
              <ChevronRight
                className={`icon settings-disclosure-chevron${expanded ? " open" : ""}`}
                aria-hidden="true"
              />
            </button>
          ) : (
            // Keeps status labels aligned with rows that have a disclosure.
            <span className="settings-engine-expand" aria-hidden="true" />
          )}
        </div>
        {advanced && expanded ? (
          <div className="settings-engine-advanced">{advanced}</div>
        ) : null}
      </div>
    );
  };

  const external = engines.filter((engine) => engine.id !== BUILTIN_ENGINE);
  const detected = external.filter((engine) => engine.binary_ok);
  const missing = external.filter((engine) => !engine.binary_ok);
  const renderExternal = (engine: EngineInfo, missingBinary = false): JSX.Element => {
    const { id } = engine;
    const engineSettings = binarySettings[id];
    const enabled = engineSettings?.enabled !== false;
    const label = engineLabel(id, engine);
    return renderAgentRow(
      id,
      externalState(engine),
      <>
        <div className="settings-engine-path-row">
          <input
            key={`${id}-${engineSettings?.binary_path ?? ""}`}
            className="settings-input"
            type="text"
            aria-label={`${label} ${t("settings.engineBinaryPath")}`}
            data-testid={`settings-engine-${id}-path`}
            placeholder={statusLine(engine) || t("settings.engineBinaryPathPlaceholder")}
            defaultValue={engineSettings?.binary_path ?? ""}
            disabled={busy}
            onBlur={(event) => {
              const next = event.currentTarget.value.trim();
              if (next !== (engineSettings?.binary_path ?? "")) {
                void save({ [id]: { binary_path: next } });
              }
            }}
          />
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
        </div>
        {!engine.binary_ok ? (
          <small className="settings-muted-line settings-engine-detail">{statusLine(engine)}</small>
        ) : null}
        {engine.protocol === "acp" && engine.binary_ok && enabled ? (
          <EngineAuthentication key={`${id}-${engineSettings?.binary_path ?? ""}`} engineID={id} />
        ) : null}
      </>,
      missingBinary,
    );
  };

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
              {renderAgentRow(BUILTIN_ENGINE, {
                text: t("settings.engineBuiltin"),
                tone: "neutral",
                selectable: true,
              })}
              {detected.map((engine) => renderExternal(engine))}
            </div>
          )}
        </div>
        {error || loadError ? <p className="settings-error" role="alert">{error || loadError}</p> : null}
      </section>
      {missing.length > 0 ? (
        <SettingsSection title={t("settings.engineNotInstalled")} testID="settings-agent-missing">
          <div className="settings-group settings-engine-body">
            {missing.map((engine) => renderExternal(engine, true))}
          </div>
        </SettingsSection>
      ) : null}
    </>
  );
}
