import { ChevronRight, RefreshCw } from "./WuuIcons";
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

const BUILTIN_ENGINE = "wuu";

/**
 * Agent options inside the model services page: one row per agent, so each
 * agent appears exactly once. External agents are
 * auto-detected and stay listed even when unavailable — the row says why —
 * while the row's radio makes it the default. Binary path overrides and
 * explicit enable/disable live behind the row's own expand control.
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
  ): { text: string; selectable: boolean } => {
    if (engine?.enabled && engine.binary_ok) {
      const detail = engine.binary_path || t("settings.engineAutoBinary");
      return { text: `${t("settings.engineReady")} · ${detail}`, selectable: true };
    }
    if (engine && (binarySettings[engine.id]?.enabled === false || (engine.binary_ok && !engine.enabled))) {
      return { text: t("settings.engineDisabled"), selectable: false };
    }
    return { text: t("settings.engineNotInstalled"), selectable: false };
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
    state: { text: string; selectable: boolean },
    advanced?: ReactNode,
  ): JSX.Element => {
    const expanded = expandedId === id;
    const label = engineLabel(id, engineById(id));
    return (
      <div
        key={id}
        className="settings-engine-item"
        data-testid={`settings-engine-${id}-status`}
        aria-label={`${label} · ${state.text}`}
      >
        <div className="settings-row settings-engine-row">
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
            <span className="settings-engine-row-status">{state.text}</span>
          </label>
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
                className={`icon settings-engine-expand-chevron${expanded ? " open" : ""}`}
                aria-hidden="true"
              />
            </button>
          ) : null}
        </div>
        {advanced && expanded ? (
          <div className="settings-engine-advanced">{advanced}</div>
        ) : null}
      </div>
    );
  };

  return (
    <section
      className="settings-section settings-agent-section"
      data-wuu-component="settings-section"
      data-testid="settings-agent-engines"
    >
      <header className="settings-section-header settings-agent-header">
        <h2 className="settings-section-title">{t("settings.agentEngines")}</h2>
        <button
          className="settings-button settings-icon-button settings-engine-refresh"
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
      </header>
      <div className="settings-engine-body">
        {result === undefined ? (
          <div
            className="settings-engine-skeleton"
            role="status"
            aria-label={t("settings.engineDetecting")}
            aria-busy="true"
          >
            <div className="settings-row" aria-hidden="true">
              <div className="settings-row-label">
                <span className="settings-engine-skeleton-line settings-engine-skeleton-title" />
                <span className="settings-engine-skeleton-line settings-engine-skeleton-description" />
              </div>
              <span className="settings-engine-skeleton-control" />
            </div>
          </div>
        ) : (
          <div role="radiogroup" aria-label={t("settings.defaultEngine")}>
            {renderAgentRow(BUILTIN_ENGINE, {
              text: t("settings.engineBuiltin"),
              selectable: true,
            })}
            {engines.filter((engine) => engine.id !== BUILTIN_ENGINE).map((engine) => {
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
                  <small className="settings-muted-line settings-engine-detail">{statusLine(engine)}</small>
                  {engine.install_url ? (
                    <button className="settings-button" type="button" onClick={() => void window.wuu.openExternal(engine.install_url!).catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))}>
                      {t("settings.engineInstall")}
                    </button>
                  ) : null}
                  {engine.protocol === "acp" && engine.binary_ok && enabled ? (
                    <EngineAuthentication key={`${id}-${engineSettings?.binary_path ?? ""}`} engineID={id} />
                  ) : null}
                </>,
              );
            })}
          </div>
        )}
      </div>
      {error || loadError ? <small className="settings-muted-line">{error || loadError}</small> : null}
    </section>
  );
}
