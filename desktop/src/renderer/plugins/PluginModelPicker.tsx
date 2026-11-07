import type { PluginUIModelPickerProps } from "../../shared/workbench";
import { useI18n } from "../i18n";
import { variantLabel } from "../RuntimeHelpers";
import { SelectMenu, type SelectMenuGroup, type SelectMenuOption } from "../SelectMenu";

// Matches the model field on the Model services page.
const SEARCH_THRESHOLD = 8;
const ALIAS_PREFIX = "alias:";
const modelKey = (provider: string, model: string): string => JSON.stringify([provider, model]);

/** Public api.ui.ModelPicker: the settings model menu plus a reasoning-level menu. */
export function PluginModelPicker({ models, aliases = {}, value, onChange, disabled = false, className }: PluginUIModelPickerProps): JSX.Element {
  const { t } = useI18n();
  const alias = value && "alias" in value ? value.alias : undefined;
  const direct = value && !("alias" in value) ? value : undefined;
  const selected = direct && models.find((entry) => entry.provider === direct.provider && entry.model === direct.model);
  const aliasNames = Object.keys(aliases).sort();

  const groups: SelectMenuGroup[] = [...new Set(models.map((entry) => entry.provider))].map((provider) => ({
    label: provider,
    options: models.filter((entry) => entry.provider === provider).map((entry) => ({
      value: modelKey(entry.provider, entry.model), label: entry.label, keywords: [entry.model, entry.provider],
    })),
  }));
  if (aliasNames.length) {
    groups.push({ label: t("plugins.modelPicker.aliases"), options: aliasNames.map((name) => ({
      value: ALIAS_PREFIX + name, label: name,
      hint: models.find((entry) => entry.provider === aliases[name].provider && entry.model === aliases[name].model)?.label ?? aliases[name].model,
    })) });
  }
  // A saved choice that is no longer configured stays visible until the user picks another.
  if (direct && !selected) {
    groups.push({ options: [{ value: modelKey(direct.provider, direct.model), label: t("plugins.modelPicker.unavailable", { model: direct.model, provider: direct.provider }), disabled: true }] });
  }
  if (alias !== undefined && !aliases[alias]) {
    groups.push({ options: [{ value: ALIAS_PREFIX + alias, label: t("plugins.modelPicker.unavailable", { model: alias, provider: t("plugins.modelPicker.aliases") }), disabled: true }] });
  }

  const aliasTarget = alias !== undefined ? aliases[alias] : undefined;
  const level = aliasTarget ? aliasTarget.variant || aliasTarget.effort || "" : direct?.variant ?? "";
  const levels = selected?.variants ?? [];
  const levelOptions: SelectMenuOption[] = [
    { value: "", label: t("plugins.modelPicker.defaultLevel") },
    ...levels.map((id) => ({ value: id, label: variantLabel(id) })),
  ];
  if (level && !levels.includes(level)) levelOptions.push({ value: level, label: variantLabel(level), disabled: true });

  const current = alias !== undefined ? ALIAS_PREFIX + alias : direct ? modelKey(direct.provider, direct.model) : "";
  return (
    <div className={className ? `plugin-ui-model-picker ${className}` : "plugin-ui-model-picker"} data-wuu-component="plugin-ui-model-picker">
      <SelectMenu
        className="plugin-ui-model-picker-model"
        triggerClassName="settings-select-trigger"
        ariaLabel={t("runtime.model")}
        placeholder={t("runtime.selectModel")}
        value={current}
        groups={groups}
        disabled={disabled || groups.length === 0}
        searchable={models.length + aliasNames.length > SEARCH_THRESHOLD}
        searchPlaceholder={t("runtime.searchModels")}
        emptyMessage={t("runtime.noMatchingModels")}
        flip
        onChange={(next) => {
          if (next === current) return;
          if (next.startsWith(ALIAS_PREFIX)) return onChange({ alias: next.slice(ALIAS_PREFIX.length) });
          const [provider, model] = JSON.parse(next) as [string, string];
          onChange({ provider, model, variant: "" });
        }}
      />
      <SelectMenu
        className="plugin-ui-model-picker-level"
        triggerClassName="settings-select-trigger"
        ariaLabel={t("runtime.reasoningEffort")}
        value={level}
        options={levelOptions}
        disabled={disabled || !selected || levels.length === 0}
        flip
        onChange={(variant) => direct && onChange({ provider: direct.provider, model: direct.model, variant })}
      />
    </div>
  );
}
