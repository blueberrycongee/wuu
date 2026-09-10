import { isTouchWebShell } from "./ComposerFocus";
import { useEffect, useId, useState, useSyncExternalStore } from "react";
import type {
  ExtensionInventoryRecord,
  PluginContributionDiagnostic,
  ExtensionSettingDescriptor,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { PluginContributionConflict, PluginGenerationDiagnostic } from "./plugins/PluginHost";

type SettingValue = boolean | string | number;
const EMPTY_DIAGNOSTICS: readonly PluginGenerationDiagnostic[] = Object.freeze([]);

export function PluginSettingsEditor({
  plugin,
  variant = "card",
  title,
}: {
  plugin: ExtensionInventoryRecord;
  variant?: "card" | "page";
  title?: string;
}): JSX.Element | null {
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
      if (!cancelled) setRuntimeDiagnosticError(errorMessage(loadError, "无法读取插件诊断"));
    });
    return () => { cancelled = true; };
  }, [approved, fingerprint, plugin.enabled, plugin.id]);

  if (!approved || plugin.enabled === false || !fingerprint || (settings.length === 0 && conflicts.length === 0 && rendererDiagnostics.length === 0 && runtimeDiagnostics.length === 0 && !runtimeDiagnosticError)) {
    return null;
  }

  return (
    <section
      className={`plugin-settings-editor plugin-settings-editor-${variant}`}
      data-wuu-component="plugin-settings"
      data-wuu-plugin={plugin.id}
      aria-label={title ?? `${plugin.name} settings`}
    >
      {title ? <h3 className="plugin-settings-editor-title">{title}</h3> : null}
      {conflicts.map((conflict) => (
        <PluginConflictControl key={conflict.key} conflict={conflict} />
      ))}
      {runtimeDiagnostics.map((diagnostic) => (
        <div className="plugin-contribution-warning" role="status" key={`runtime:${diagnostic.contribution}`}>
          <strong>插件贡献已被隔离</strong>
          <span>{diagnostic.contribution}：{diagnostic.message}</span>
        </div>
      ))}
      {runtimeDiagnosticError ? <div className="plugin-contribution-warning" role="alert">{runtimeDiagnosticError}</div> : null}
      {rendererDiagnostics.map((diagnostic, index) => (
        <div className="plugin-contribution-warning" role="status" key={`${diagnostic.message}:${index}`}>
          <strong>插件贡献已被隔离</strong>
          <span>{diagnostic.message}</span>
        </div>
      ))}
      {settings.map((setting) => (
        <PluginSettingControl
          key={setting.id}
          pluginId={plugin.id}
          fingerprint={fingerprint}
          setting={setting}
        />
      ))}
    </section>
  );
}

function PluginConflictControl({ conflict }: { conflict: PluginContributionConflict }): JSX.Element {
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
      setError(errorMessage(saveError, "无法保存插件冲突选择"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="plugin-contribution-conflict" data-conflict-key={conflict.key}>
      <div>
        <strong>插件贡献冲突</strong>
        <span>{conflict.kind === "surface" ? "界面区域" : "内容呈现"}：{conflict.target}</span>
      </div>
      <label>
        使用
        <select
          value={conflict.winnerPluginId}
          disabled={saving}
          onChange={(event) => void choose(event.currentTarget.value)}
        >
          {candidates.map((candidate) => (
            <option value={candidate.pluginId} key={candidate.pluginId}>
              {candidate.title ? `${candidate.title} (${candidate.pluginId})` : candidate.pluginId}
            </option>
          ))}
        </select>
      </label>
      {error ? <span role="alert">{error}</span> : null}
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
      className="plugin-setting settings-row settings-row-block"
      data-wuu-component="settings-row"
      data-setting-key={setting.id}
    >
      <div className="plugin-setting-heading settings-row-label">
        <label className="settings-row-label-title" htmlFor={controlId}>{setting.title}</label>
        <span className="settings-row-label-description">
          {t(setting.scope === "workspace" ? "skills.pluginSettingWorkspace" : "skills.pluginSettingUser")}
        </span>
      </div>
      {showDescription && <p id={descriptionId}>{setting.description}</p>}
      <div className="plugin-setting-control settings-row-control-block">
        {setting.type === "boolean" ? (
          <input
            id={controlId}
            type="checkbox"
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
          <select
            id={controlId}
            value={String(draft)}
            disabled={loading || saving}
            aria-describedby={describedBy}
            onChange={(event) => {
              const value = event.currentTarget.value;
              setDraft(value);
              void save(value);
            }}
          >
            {(setting.enum ?? []).map((option) => <option key={option} value={option}>{option}</option>)}
          </select>
        ) : (
          <input
            id={controlId}
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
        <span className="plugin-setting-default">
          {t("skills.pluginSettingDefault", { value: String(setting.default) })}
        </span>
      </div>
      <div id={statusId} className={`plugin-setting-status${error ? " is-error" : ""}`} aria-live="polite">
        {error ? (
          <>
            <span>{error}</span>
            <button type="button" className="text-button" onClick={retry} disabled={loading || saving}>
              {t("skills.pluginSettingRetry")}
            </button>
          </>
        ) : saving ? t("skills.pluginSettingSaving") : setting.apply === "restart" ? (
          saved ? t("skills.pluginSettingRestartSaved") : t("skills.pluginSettingRestart")
        ) : saved ? t("skills.pluginSettingLiveSaved") : t("skills.pluginSettingLive")}
      </div>
    </div>
  );
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim() ? error.message : fallback;
}
