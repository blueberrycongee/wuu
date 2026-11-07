import { useSettingsOperationScope } from "./SettingsOperationScope";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { isTouchWebShell } from "./ComposerFocus";
import { useCallback, useEffect, useId, useRef, useState, useSyncExternalStore } from "react";
import type {
  ExtensionInventoryRecord,
  PluginContributionDiagnostic,
  ExtensionSettingDescriptor,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import { SelectMenu } from "./SelectMenu";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { SettingsBadge } from "./SettingsRow";
import { Switch } from "./ui/Switch";
import { readRetainedSettingsData, writeRetainedSettingsData } from "./SettingsPageData";
import { AlertCircle, AlertTriangle } from "./WuuIcons";
import type { SettingsControlsConflictV1 as PluginContributionConflict, SettingsControlsDiagnosticV1 as PluginGenerationDiagnostic } from "../../../packages/plugin-sdk/src/ui-settings-controls";

type SettingValue = boolean | string | number;
const EMPTY_DIAGNOSTICS: readonly PluginGenerationDiagnostic[] = Object.freeze([]);

export function PluginSettingsEditor({
  plugin,
  title,
  workspaceRoot,
}: {
  plugin: ExtensionInventoryRecord;
  title?: string;
  workspaceRoot?: string;
}): JSX.Element | null {
  const scope = useSettingsOperationScope(JSON.stringify([plugin.id, plugin.fingerprint]));
  const controls = scope.controls;
  const { t } = useI18n();
  const settings = plugin.contributions?.settings ?? [];
  const approved = plugin.approval_state === "official" || plugin.approval_state === "granted";
  const fingerprint = plugin.fingerprint;
  const subscribe = useCallback((listener: () => void) => controls.subscribe(listener), [controls]);
  const conflicts = useSyncExternalStore(
    subscribe,
    () => controls.getConflicts(),
    () => controls.getConflicts(),
  ).filter((conflict) => conflict.candidates.some((candidate) => candidate.pluginId === plugin.id));
  const rendererDiagnostics = useSyncExternalStore(
    subscribe,
    () => fingerprint ? controls.getDiagnostics(plugin.id, fingerprint) : EMPTY_DIAGNOSTICS,
    () => EMPTY_DIAGNOSTICS,
  ).filter((diagnostic) => diagnostic.kind === "render" || diagnostic.kind === "runtime");
  const [runtimeDiagnostics, setRuntimeDiagnostics] = useState<readonly PluginContributionDiagnostic[]>([]);
  const [runtimeDiagnosticError, setRuntimeDiagnosticError] = useState("");

  useEffect(() => {
    let cancelled = false;
    if (!approved || plugin.enabled === false || !fingerprint || !controls.capabilities.diagnostics) {
      setRuntimeDiagnostics([]);
      setRuntimeDiagnosticError("");
      return;
    }
    setRuntimeDiagnostics([]);
    setRuntimeDiagnosticError("");
    void controls.getPluginDiagnostics({ id: plugin.id, fingerprint }).then((result) => {
      if (!cancelled && scope.active) setRuntimeDiagnostics(result.diagnostics);
    }).catch((loadError: unknown) => {
      if (!cancelled && scope.active && !isPluginLifecycleCancellation(loadError)) setRuntimeDiagnosticError(errorMessage(loadError, t("skills.pluginDiagnosticsFailed")));
    });
    return () => { cancelled = true; };
  }, [scope, approved, fingerprint, plugin.enabled, plugin.id]);

  if (!approved || plugin.enabled === false || !fingerprint || (settings.length === 0 && conflicts.length === 0 && rendererDiagnostics.length === 0 && runtimeDiagnostics.length === 0 && !runtimeDiagnosticError)) {
    return null;
  }

  const plainSettings = settings.filter((setting) => setting.type !== "secret");
  const secretSettings = settings.filter((setting) => setting.type === "secret");
  const hasNotices = conflicts.length > 0 || runtimeDiagnostics.length > 0 || rendererDiagnostics.length > 0 || Boolean(runtimeDiagnosticError);
  return (
    <section
      className="plugin-settings-editor"
      data-wuu-component="plugin-settings"
      data-wuu-plugin={plugin.id}
      aria-label={title ?? t("skills.pluginSettingsNamed", { name: plugin.name })}
    >
      {title ? <h3 className="plugin-settings-editor-title">{title}</h3> : null}
      {plainSettings.length > 0 || hasNotices ? <SettingsGroup className="plugin-settings-editor-body">
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
        <PluginNotice key={`${diagnostic.message}:${index}`} title={diagnostic.kind === "runtime" ? t("skills.pluginRuntimeRequestFailed") : t("skills.pluginContributionIsolated")} detail={diagnostic.message} />
      ))}
      {plainSettings.map((setting) => (
        <PluginSettingControl
          key={setting.id}
          pluginId={plugin.id}
          fingerprint={fingerprint}
          setting={setting}
          workspaceRoot={workspaceRoot}
        />
      ))}
      </SettingsGroup> : null}
      {secretSettings.length > 0 ? (
        <SettingsSection title={plainSettings.length > 0 || hasNotices ? t("skills.pluginCredentialsSection") : undefined}>
          <SettingsGroup>
            {secretSettings.map((setting) => (
              <PluginSecretControl
                key={setting.id}
                pluginId={plugin.id}
                fingerprint={fingerprint}
                setting={setting}
                workspaceRoot={workspaceRoot}
              />
            ))}
          </SettingsGroup>
        </SettingsSection>
      ) : null}
    </section>
  );
}

function PluginConflictControl({ conflict }: { conflict: PluginContributionConflict }): JSX.Element {
  const scope = useSettingsOperationScope(conflict.key);
  const controls = scope.controls;
  const { t } = useI18n();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const candidates = Array.from(new Map(
    conflict.candidates.map((candidate) => [candidate.pluginId, candidate] as const),
  ).values());

  useEffect(() => { setSaving(false); setError(""); }, [scope]);

  async function choose(pluginId: string): Promise<void> {
    if (!scope.active || saving || pluginId === conflict.winnerPluginId) return;
    setSaving(true);
    setError("");
    try {
      await controls.chooseConflict(conflict.key, pluginId);
    } catch (saveError) {
      if (scope.active && !isPluginLifecycleCancellation(saveError)) setError(errorMessage(saveError, t("skills.pluginConflictSaveFailed")));
    } finally {
      if (scope.active) setSaving(false);
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

function PluginSecretControl({ pluginId, fingerprint, setting, workspaceRoot }: {
  pluginId: string;
  fingerprint: string;
  setting: ExtensionSettingDescriptor;
  workspaceRoot?: string;
}): JSX.Element {
  const target = settingTarget(pluginId, setting, workspaceRoot);
  const scope = useSettingsOperationScope(JSON.stringify([target, fingerprint]));
  const controls = scope.controls;
  const { t } = useI18n();
  const controlId = useId();
  const secretDataKey = `plugin-secret:${fingerprint}:${target}`;
  const retainedConfigured = readRetainedSettingsData<boolean>(controls, secretDataKey);
  const [draft, setDraft] = useState("");
  const [draftTarget, setDraftTarget] = useState(target);
  const [loadedScope, setLoadedScope] = useState<typeof scope>();
  const [configured, setConfigured] = useState(() => retainedConfigured ?? false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const params = { id: pluginId, fingerprint, key: setting.id };

  useEffect(() => { setDraft(""); setDraftTarget(target); }, [target]);
  useEffect(() => {
    let cancelled = false;
    setConfigured(readRetainedSettingsData<boolean>(controls, secretDataKey) ?? false);
    setSaving(false); setError(""); setLoadedScope(undefined);
    setLoading(true);
    void controls.getPluginSetting(params).then((result) => {
      if (!cancelled && scope.active) {
        setConfigured(result.configured === true);
        writeRetainedSettingsData(controls, secretDataKey, result.configured === true);
      }
    }).catch((failure: unknown) => {
      if (!cancelled && scope.active && !isPluginLifecycleCancellation(failure)) setError(errorMessage(failure, t("skills.pluginSettingLoadFailed")));
    }).finally(() => {
      if (!cancelled && scope.active) { setLoading(false); setLoadedScope(scope); }
    });
    return () => { cancelled = true; };
  }, [controls, scope, pluginId, fingerprint, setting.id, secretDataKey]);

  async function save(value: string): Promise<void> {
    if (!scope.active || loadedScope !== scope || loading || saving) return;
    setSaving(true);
    setError("");
    // Never retain a submitted token for display, retry or ordinary settings.
    setDraft("");
    try {
      const result = await controls.setPluginSetting({ ...params, value });
      if (scope.active) {
        setConfigured(result.configured === true);
        writeRetainedSettingsData(controls, secretDataKey, result.configured === true);
      }
    } catch (failure) {
      if (scope.active && !isPluginLifecycleCancellation(failure)) setError(errorMessage(failure, t("skills.pluginSettingSaveFailed")));
    } finally {
      if (scope.active) setSaving(false);
    }
  }

  const statusId = `${controlId}-status`;
  return (
    <div className="settings-row settings-row-block" data-wuu-component="settings-row" data-setting-key={setting.id} data-setting-type="secret">
      <div className="plugin-setting-heading settings-row-label">
        <label className="settings-row-label-title" htmlFor={controlId}>{setting.title}</label>
        {setting.description ? <span className="settings-row-label-description">{setting.description}</span> : null}
        <span className="settings-row-label-description">
          {t("skills.pluginSecretStorage")}
          {setting.credential_origins?.length ? ` ${t("skills.pluginSecretOrigins", { origins: setting.credential_origins.join(", ") })}` : null}
        </span>
      </div>
      <div className="plugin-setting-control settings-row-control settings-row-control-block">
        <input id={controlId} className="settings-input" type="password" autoComplete="new-password" autoCapitalize="none" spellCheck={false} maxLength={16 * 1024}
          value={draftTarget === target ? draft : ""} disabled={loadedScope !== scope || loading || saving} aria-describedby={statusId}
          placeholder={configured ? t("skills.pluginSecretReplace") : t("skills.pluginSecretEnter")}
          onChange={(event) => { if (scope.active && loadedScope === scope) setDraft(event.currentTarget.value); }}
          onKeyDown={(event) => {
            if (event.key === "Enter" && draft) { event.preventDefault(); void save(draft); }
          }} />
        <button type="button" className="settings-button" disabled={loadedScope !== scope || loading || saving || !draft} onClick={() => void save(draft)}>{t("common.save")}</button>
        {configured ? (
          <button type="button" className="settings-button settings-button-ghost" disabled={loadedScope !== scope || loading || saving} onClick={() => void save("")}>{t("common.remove")}</button>
        ) : null}
      </div>
      <div id={statusId} className={`plugin-setting-status${error ? " is-error" : ""}`} aria-live="polite">
        {error || (saving ? t("skills.pluginSettingSaving") : loading ? "" : configured ? t("skills.pluginSecretConfigured") : t("skills.pluginSecretMissing"))}
      </div>
    </div>
  );
}

function PluginSettingControl({
  pluginId,
  fingerprint,
  setting,
  workspaceRoot,
}: {
  pluginId: string;
  fingerprint: string;
  setting: ExtensionSettingDescriptor;
  workspaceRoot?: string;
}): JSX.Element {
  const target = settingTarget(pluginId, setting, workspaceRoot);
  const scope = useSettingsOperationScope(JSON.stringify([target, fingerprint]));
  const controls = scope.controls;
  const { locale, t } = useI18n();
  const controlId = useId();
  const valueDataKey = `plugin-value:${fingerprint}:${target}`;
  const retainedValue = readRetainedSettingsData<SettingValue>(controls, valueDataKey);
  const [draft, setDraft] = useState<SettingValue>(() => retainedValue ?? setting.default);
  const [draftTarget, setDraftTarget] = useState(target);
  const dirty = useRef(false);
  const [loadedScope, setLoadedScope] = useState<typeof scope>();
  const [committed, setCommitted] = useState<SettingValue>(() => retainedValue ?? setting.default);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    const retained = readRetainedSettingsData<SettingValue>(controls, valueDataKey);
    dirty.current = false;
    setDraftTarget(target); setDraft(retained ?? setting.default); setCommitted(retained ?? setting.default);
  }, [target]);
  useEffect(() => {
    let cancelled = false;
    setLoadedScope(undefined); setSaving(false); setSaved(false);
    void load();
    return () => {
      cancelled = true;
    };

    async function load(): Promise<void> {
      setLoading(true);
      setError("");
      try {
        const result = await controls.getPluginSetting({
          id: pluginId,
          fingerprint,
          key: setting.id,
        });
        if (!cancelled && scope.active) {
          setLoadedScope(scope);
          if (!dirty.current) setDraft(result.value);
          setCommitted(result.value);
          writeRetainedSettingsData(controls, valueDataKey, result.value);
        }
      } catch (loadError) {
        if (!cancelled && scope.active && !isPluginLifecycleCancellation(loadError)) {
          setError(errorMessage(loadError, t("skills.pluginSettingLoadFailed")));
        }
      } finally {
        if (!cancelled && scope.active) setLoading(false);
      }
    }
  }, [scope, fingerprint, locale, pluginId, setting.id, valueDataKey]);

  async function save(value: SettingValue): Promise<void> {
    if (!scope.active || loadedScope !== scope || loading || saving || Object.is(value, committed)) return;
    setSaving(true);
    setError("");
    setSaved(false);
    try {
      const result = await controls.setPluginSetting({
        id: pluginId,
        fingerprint,
        key: setting.id,
        value,
      });
      if (!scope.active) return;
      dirty.current = false;
      setDraft(result.value);
      setCommitted(result.value);
      setSaved(true);
      writeRetainedSettingsData(controls, valueDataKey, result.value);
    } catch (saveError) {
      if (scope.active && !isPluginLifecycleCancellation(saveError)) setError(errorMessage(saveError, t("skills.pluginSettingSaveFailed")));
    } finally {
      if (scope.active) setSaving(false);
    }
  }

  async function retry(): Promise<void> {
    if (!scope.active || loading || saving) return;
    if (loadedScope === scope && !Object.is(draft, committed)) {
      await save(draft);
      return;
    }
    setLoading(true);
    setError("");
    try {
      const result = await controls.getPluginSetting({ id: pluginId, fingerprint, key: setting.id });
      if (!scope.active) return;
      setLoadedScope(scope);
      if (!dirty.current) setDraft(result.value);
      setCommitted(result.value);
      writeRetainedSettingsData(controls, valueDataKey, result.value);
    } catch (retryError) {
      if (scope.active && !isPluginLifecycleCancellation(retryError)) setError(errorMessage(retryError, t("skills.pluginSettingLoadFailed")));
    } finally {
      if (scope.active) setLoading(false);
    }
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
        {/* User scope and live apply are the defaults; only a narrower reach or a
          * deferred effect is worth a badge. */}
        {setting.scope === "workspace" || setting.apply === "restart" ? (
          <span className="settings-row-label-heading">
            <label className="settings-row-label-title" htmlFor={controlId}>{setting.title}</label>
            <span className="settings-row-badges">
              {setting.scope === "workspace" ? <SettingsBadge>{t("skills.pluginSettingWorkspace")}</SettingsBadge> : null}
              {setting.apply === "restart" ? <SettingsBadge>{t("skills.pluginSettingRestart")}</SettingsBadge> : null}
            </span>
          </span>
        ) : (
          <label className="settings-row-label-title" htmlFor={controlId}>{setting.title}</label>
        )}
        {showDescription ? (
          <span id={descriptionId} className="settings-row-label-description">{setting.description}</span>
        ) : null}
      </div>
      <div className="plugin-setting-control settings-row-control">
        {setting.type === "boolean" ? (
          <Switch
            id={controlId}
            checked={Boolean(draftTarget === target ? draft : setting.default)}
            disabled={loadedScope !== scope || loading || saving}
            aria-describedby={describedBy}
            onClick={() => {
              if (!scope.active || loadedScope !== scope) return;
              const value = !Boolean(draftTarget === target ? draft : setting.default);
              dirty.current = true;
              setDraft(value);
              void save(value);
            }}
          />
        ) : setting.type === "enum" ? (
          <SelectMenu
            id={controlId}
            triggerClassName="settings-select-trigger"
            value={String(draftTarget === target ? draft : setting.default)}
            disabled={loadedScope !== scope || loading || saving}
            ariaLabel={setting.title}
            align="right"
            options={(setting.enum ?? []).map((option) => ({ value: option, label: option }))}
            onChange={(value) => {
              if (!scope.active || loadedScope !== scope) return;
              dirty.current = true;
              setDraft(value);
              void save(value);
            }}
          />
        ) : (
          <input
            id={controlId}
            className={setting.type === "number" ? "settings-input settings-input-num" : "settings-input"}
            type={setting.type === "number" ? "number" : "text"}
            value={String(draftTarget === target ? draft : setting.default)}
            disabled={loadedScope !== scope || loading || saving}
            aria-describedby={describedBy}
            onChange={(event) => {
              if (!scope.active || loadedScope !== scope) return;
              const raw = event.currentTarget.value;
              dirty.current = true;
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
            <button type="button" className="settings-button settings-button-ghost" onClick={() => void retry()} disabled={loading || saving}>
              {t("skills.pluginSettingRetry")}
            </button>
          </>
        ) : saving ? t("skills.pluginSettingSaving") : setting.apply === "restart" && saved ? t("skills.pluginSettingRestartSaved") : null}
      </div>
    </div>
  );
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim() ? error.message : fallback;
}

// Provider generations renew authority; only a different setting meaning clears a draft.
function settingTarget(pluginId: string, setting: ExtensionSettingDescriptor, workspaceRoot?: string): string {
  return JSON.stringify([pluginId, setting.id, setting.type, setting.scope,
    setting.scope === "workspace" ? workspaceRoot : undefined,
    setting.enum, setting.credential_origins]);
}
