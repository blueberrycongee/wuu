import { isTouchWebShell } from "./ComposerFocus";
import { useEffect, useId, useState, useSyncExternalStore } from "react";
import type {
  ExtensionInventoryRecord,
  PluginContributionDiagnostic,
  ExtensionSettingDescriptor,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import { SelectMenu } from "./SelectMenu";
import { AlertCircle, AlertTriangle } from "./WuuIcons";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { PluginContributionConflict, PluginGenerationDiagnostic } from "./plugins/PluginHost";

type SettingValue = boolean | string | number;
const EMPTY_DIAGNOSTICS: readonly PluginGenerationDiagnostic[] = Object.freeze([]);

export function PluginSettingsEditor({
  plugin,
  title,
}: {
  plugin: ExtensionInventoryRecord;
  title?: string;
}): JSX.Element | null {
  const { t } = useI18n();
  const settings = plugin.contributions?.settings ?? [];
  const approved = plugin.approval_state === "official" || plugin.approval_state === "granted";
  const fingerprint = plugin.fingerprint;
  const conflicts = useSyncExternalStore(
    (listener) => desktopPluginHost.subscribe(listener),
    () => desktopPluginHost.getConflicts(),
    () => desktopPluginHost.getConflicts(),
  ).filter((conflict) => conflict.candidates.some((candidate) => candidate.pluginId === plugin.id));
  const rendererDiagnostics = useSyncExternalStore(
    (listener) => desktopPluginHost.subscribe(listener),
    () => fingerprint ? desktopPluginHost.getGenerationDiagnostics(plugin.id, fingerprint) : EMPTY_DIAGNOSTICS,
    () => EMPTY_DIAGNOSTICS,
  ).filter((diagnostic) => diagnostic.kind === "render");
  const [runtimeDiagnostics, setRuntimeDiagnostics] = useState<readonly PluginContributionDiagnostic[]>([]);
  const [runtimeDiagnosticError, setRuntimeDiagnosticError] = useState("");

  useEffect(() => {
    let cancelled = false;
    if (!approved || plugin.enabled === false || !fingerprint || !window.wuu.getPluginDiagnostics) {
      setRuntimeDiagnostics([]);
      setRuntimeDiagnosticError("");
      return;
    }
    setRuntimeDiagnostics([]);
    setRuntimeDiagnosticError("");
    void window.wuu.getPluginDiagnostics({ id: plugin.id, fingerprint }).then((result) => {
      if (!cancelled) setRuntimeDiagnostics(result.diagnostics);
    }).catch((loadError: unknown) => {
      if (!cancelled) setRuntimeDiagnosticError(errorMessage(loadError, t("skills.pluginDiagnosticsFailed")));
    });
    return () => { cancelled = true; };
  }, [approved, fingerprint, plugin.enabled, plugin.id]);

  if (!approved || plugin.enabled === false || !fingerprint || (settings.length === 0 && conflicts.length === 0 && rendererDiagnostics.length === 0 && runtimeDiagnostics.length === 0 && !runtimeDiagnosticError)) {
    return null;
  }

  return (
    <section
      className="plugin-settings-editor"
      data-wuu-component="plugin-settings"
      data-wuu-plugin={plugin.id}
      aria-label={title ?? t("skills.pluginSettingsNamed", { name: plugin.name })}
    >
      {title ? <h3 className="plugin-settings-editor-title">{title}</h3> : null}
      <div className="plugin-settings-editor-body">
      {conflicts.map((conflict) => (
        <PluginConflictControl key={conflict.key} conflict={conflict} />
      ))}
      {runtimeDiagnostics.map((diagnostic) => (
        <PluginNotice
          key={`runtime:${diagnostic.contribution}`}
          title={t("skills.pluginContributionIsolated")}
          detail={t("skills.pluginContributionDetail", { contribution: diagnostic.contribution, message: diagnostic.message })}
        />
      ))}
      {runtimeDiagnosticError ? <PluginNotice tone="danger" title={runtimeDiagnosticError} /> : null}
      {rendererDiagnostics.map((diagnostic, index) => (
        <PluginNotice key={`${diagnostic.message}:${index}`} title={t("skills.pluginContributionIsolated")} detail={diagnostic.message} />
      ))}
      {settings.map((setting) => (
        <PluginSettingControl
          key={setting.id}
          pluginId={plugin.id}
          fingerprint={fingerprint}
          setting={setting}
        />
      ))}
      </div>
    </section>
  );
}

function PluginConflictControl({ conflict }: { conflict: PluginContributionConflict }): JSX.Element {
  const { t } = useI18n();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const candidates = Array.from(new Map(
    conflict.candidates.map((candidate) => [candidate.pluginId, candidate] as const),
  ).values());

  async function choose(pluginId: string): Promise<void> {
    if (saving || pluginId === conflict.winnerPluginId) return;
    setSaving(true);
    setError("");
    try {
      const preferences = await window.wuu.setPluginConflictPreference(conflict.key, pluginId);
      desktopPluginHost.setConflictPreferences(preferences);
    } catch (saveError) {
      setError(errorMessage(saveError, t("skills.pluginConflictSaveFailed")));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="settings-row plugin-notice" data-conflict-key={conflict.key}>
      <div className="settings-row-label">
        <span className="settings-row-label-title plugin-notice-title">
          <AlertTriangle className="icon-sm" aria-hidden="true" />
          {t("skills.pluginConflictTitle")}
        </span>
        <span className="settings-row-label-description">{t("skills.pluginConflictTarget", {
          kind: conflict.kind === "surface" ? t("skills.pluginConflictSurface") : t("skills.pluginConflictRenderer"),
          target: conflict.target,
        })}</span>
      </div>
      <div className="settings-row-control">
        <SelectMenu
          triggerClassName="settings-select-trigger"
          ariaLabel={t("skills.pluginConflictUse")}
          value={conflict.winnerPluginId}
          disabled={saving}
          align="right"
          options={candidates.map((candidate) => ({
            value: candidate.pluginId,
            label: candidate.title ? `${candidate.title} (${candidate.pluginId})` : candidate.pluginId,
          }))}
          onChange={(pluginId) => void choose(pluginId)}
        />
      </div>
      {error ? <div className="settings-row-error settings-error" role="alert">{error}</div> : null}
    </div>
  );
}

// Something about the plugin that needs a look, as a row of its group: the
// symbol and title carry the state, the detail says which contribution.
function PluginNotice({ title, detail, tone = "warning" }: { title: string; detail?: string; tone?: "warning" | "danger" }): JSX.Element {
  const Icon = tone === "danger" ? AlertCircle : AlertTriangle;
  return (
    <div className="settings-row plugin-notice" role={tone === "danger" ? "alert" : "status"}>
      <div className="settings-row-label">
        <span className="settings-row-label-title plugin-notice-title" data-tone={tone}>
          <Icon className="icon-sm" aria-hidden="true" />
          {title}
        </span>
        {detail ? <span className="settings-row-label-description">{detail}</span> : null}
      </div>
    </div>
  );
}

function PluginSettingControl({
  pluginId,
  fingerprint,
  setting,
}: {
  pluginId: string;
  fingerprint: string;
  setting: ExtensionSettingDescriptor;
}): JSX.Element {
  const { locale, t } = useI18n();
  const controlId = useId();
  const [draft, setDraft] = useState<SettingValue>(setting.default);
  const [committed, setCommitted] = useState<SettingValue>(setting.default);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void load();
    return () => {
      cancelled = true;
    };

    async function load(): Promise<void> {
      setLoading(true);
      setError("");
      try {
        const result = await window.wuu.getPluginSetting({
          id: pluginId,
          fingerprint,
          key: setting.id,
        });
        if (!cancelled) {
          setDraft(result.value);
          setCommitted(result.value);
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(errorMessage(loadError, t("skills.pluginSettingLoadFailed")));
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
  }, [fingerprint, locale, pluginId, setting.id]);

  async function save(value: SettingValue): Promise<void> {
    if (saving || Object.is(value, committed)) return;
    setSaving(true);
    setError("");
    setSaved(false);
    try {
      const result = await window.wuu.setPluginSetting({
        id: pluginId,
        fingerprint,
        key: setting.id,
        value,
      });
      setDraft(result.value);
      setCommitted(result.value);
      setSaved(true);
    } catch (saveError) {
      setError(errorMessage(saveError, t("skills.pluginSettingSaveFailed")));
    } finally {
      setSaving(false);
    }
  }

  function retry(): void {
    if (!loading && !Object.is(draft, committed)) {
      void save(draft);
      return;
    }
    setLoading(true);
    setError("");
    void window.wuu
      .getPluginSetting({ id: pluginId, fingerprint, key: setting.id })
      .then((result) => {
        setDraft(result.value);
        setCommitted(result.value);
      })
      .catch((retryError: unknown) => {
        setError(errorMessage(retryError, t("skills.pluginSettingLoadFailed")));
      })
      .finally(() => setLoading(false));
  }

  const descriptionId = `${controlId}-description`;
  const statusId = `${controlId}-status`;
  const showDescription = Boolean(setting.description) && !isTouchWebShell();
  const describedBy = showDescription ? `${descriptionId} ${statusId}` : statusId;

  return (
    <div
      className="plugin-setting settings-row"
      data-wuu-component="settings-row"
      data-setting-key={setting.id}
      data-setting-type={setting.type}
    >
      <div className="plugin-setting-heading settings-row-label">
        <label className="settings-row-label-title" htmlFor={controlId}>{setting.title}</label>
        {showDescription ? (
          <span id={descriptionId} className="settings-row-label-description">{setting.description}</span>
        ) : null}
        {/* User scope is the default; only a narrower reach is worth saying. */}
        {setting.scope === "workspace" ? (
          <span className="settings-row-label-description">{t("skills.pluginSettingWorkspace")}</span>
        ) : null}
      </div>
      <div className="plugin-setting-control settings-row-control">
        {setting.type === "boolean" ? (
          <input
            id={controlId}
            className="plugin-setting-switch"
            type="checkbox"
            role="switch"
            checked={Boolean(draft)}
            disabled={loading || saving}
            aria-describedby={describedBy}
            onChange={(event) => {
              const value = event.currentTarget.checked;
              setDraft(value);
              void save(value);
            }}
          />
        ) : setting.type === "enum" ? (
          <SelectMenu
            id={controlId}
            triggerClassName="settings-select-trigger"
            value={String(draft)}
            disabled={loading || saving}
            ariaLabel={setting.title}
            align="right"
            options={(setting.enum ?? []).map((option) => ({ value: option, label: option }))}
            onChange={(value) => {
              setDraft(value);
              void save(value);
            }}
          />
        ) : (
          <input
            id={controlId}
            className={setting.type === "number" ? "settings-input settings-input-num" : "settings-input"}
            type={setting.type === "number" ? "number" : "text"}
            value={String(draft)}
            disabled={loading || saving}
            aria-describedby={describedBy}
            onChange={(event) => {
              const raw = event.currentTarget.value;
              setDraft(setting.type === "number" ? Number(raw) : raw);
              setSaved(false);
            }}
            onBlur={() => void save(draft)}
          />
        )}
      </div>
      <div id={statusId} className={`plugin-setting-status${error ? " is-error" : ""}`} aria-live="polite">
        {error ? (
          <>
            <span className="plugin-setting-status-message">
              <AlertCircle className="icon-sm" aria-hidden="true" />
              {error}
            </span>
            <button type="button" className="settings-button settings-button-ghost" onClick={retry} disabled={loading || saving}>
              {t("skills.pluginSettingRetry")}
            </button>
          </>
        ) : saving ? t("skills.pluginSettingSaving") : setting.apply === "restart" ? (
          saved ? t("skills.pluginSettingRestartSaved") : t("skills.pluginSettingRestart")
        ) : null}
      </div>
    </div>
  );
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim() ? error.message : fallback;
}
