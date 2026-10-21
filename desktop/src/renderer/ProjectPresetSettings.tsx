import { useState } from "react";
import type { InitializeResult, ProjectPresetConfig, ProjectPresetMode, RuntimeAdvancedSettingsUpdate } from "../shared/protocol";
import { DEFAULT_PROJECT_PRESET, PROJECT_PRESET_MODES, PROJECT_PRESET_ROLES } from "./ProjectPresetModes";
import { providerModelVariantOptions, variantLabel } from "./RuntimeHelpers";
import { SelectMenu, type SelectMenuOption } from "./SelectMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { useI18n } from "./i18n";

export function ProjectPresetSettings({ initialized, running, onSave }: {
  initialized: InitializeResult;
  running: boolean;
  onSave: (settings: RuntimeAdvancedSettingsUpdate) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [mode, setMode] = useState<ProjectPresetMode>(DEFAULT_PROJECT_PRESET);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<{ mode: ProjectPresetMode; role: keyof ProjectPresetConfig; message: string }>();
  const presets = initialized.project_presets ?? {};
  const preset = presets[mode] ?? {};
  const modelOptions: SelectMenuOption[] = [];
  for (const provider of initialized.providers ?? []) {
    for (const id of new Set([provider.model, ...(provider.models ?? []).map(model => model.id)].filter(Boolean))) {
      const model = provider.models?.find(model => model.id === id);
      modelOptions.push({ value: JSON.stringify([provider.name, id]), label: model?.display_name || id, hint: provider.name });
    }
  }
  for (const selection of Object.values(preset)) {
    if (!selection?.provider || !selection.model) continue;
    const value = JSON.stringify([selection.provider, selection.model]);
    if (!modelOptions.some(option => option.value === value)) modelOptions.push({ value, label: `${selection.provider} / ${selection.model}` });
  }
  async function saveRole(role: keyof ProjectPresetConfig, selection: NonNullable<ProjectPresetConfig[typeof role]>): Promise<void> {
    setSaving(true);
    setError(undefined);
    try { await onSave({ project_presets: { ...presets, [mode]: { ...preset, [role]: selection } } }); }
    catch (cause) { setError({ mode, role, message: cause instanceof Error ? cause.message : t("settings.saveFailed") }); }
    finally { setSaving(false); }
  }
  return <SettingsSection title={t("settings.projectPresets")} testID="settings-project-presets">
    <div className="theme-segmented project-preset-tabs" role="group" aria-label={t("projects.mode")}>
      {PROJECT_PRESET_MODES.map(value => <button key={value} type="button" data-testid={`project-preset-${value}`}
        aria-pressed={mode === value} disabled={saving} onClick={() => setMode(value)}>{value}</button>)}
    </div>
    <div className="settings-form"><SettingsGroup>
      {PROJECT_PRESET_ROLES.map(({ id: role, label: labelKey }) => {
        const selection = preset[role];
        const value = selection?.provider && selection.model ? JSON.stringify([selection.provider, selection.model]) : "";
        const label = t(labelKey);
        const provider = initialized.providers?.find(provider => provider.name === selection?.provider);
        const effort = selection?.variant || selection?.effort || "";
        const efforts = providerModelVariantOptions(provider, selection?.model ?? "", effort);
        if (!efforts.includes(effort)) efforts.push(effort);
        return <SettingsRow key={role} title={label} block error={error?.mode === mode && error.role === role ? error.message : undefined}>
          <div className="project-role-controls">
            <SelectMenu triggerClassName="settings-select-trigger" ariaLabel={label} dataTestid={`project-preset-${role}-model`}
              value={value} options={modelOptions} placeholder={t("projects.notConfigured")} searchable flip disabled={running || saving}
              onChange={next => {
                if (next === value) return;
                const [provider, model] = JSON.parse(next) as [string, string];
                void saveRole(role, { provider, model });
              }} />
            <SelectMenu triggerClassName="settings-select-trigger" ariaLabel={`${label} · ${t("projects.reasoning")}`}
              dataTestid={`project-preset-${role}-effort`} value={effort} options={efforts.map(value => ({ value, label: variantLabel(value) }))}
              flip disabled={running || saving || !value} onChange={next => {
                if (next === effort) return;
                // Clear both fields together so a removed variant cannot revive an older effort.
                void saveRole(role, { ...selection, effort: next, variant: next });
              }} />
          </div>
        </SettingsRow>;
      })}
    </SettingsGroup></div>
  </SettingsSection>;
}
