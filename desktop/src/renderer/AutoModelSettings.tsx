import { useEffect, useState } from "react";
import type {
  AutoModelConfig,
  ModelAliasSummary,
  ProviderSummary,
  RuntimeAdvancedSettingsUpdate,
} from "../shared/protocol";
import { SelectMenu } from "./SelectMenu";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";
import { providerModelVariantOptions, variantLabel } from "./RuntimeHelpers";

export function AutoModelSettings({
  value,
  providers,
  disabled,
  onSave,
}: {
  value?: AutoModelConfig;
  providers: ProviderSummary[];
  disabled: boolean;
  onSave: (value: RuntimeAdvancedSettingsUpdate) => Promise<void>;
}): JSX.Element {
  const { locale } = useI18n();
  const zh = locale === "zh-CN";
  const empty = (): AutoModelConfig => {
    const selection = {
      provider: providers[0]?.name ?? "",
      model: providers[0]?.model ?? "",
    };
    return {
      enabled: false,
      default: false,
      classifier: { ...selection },
      simple: { ...selection },
      medium: { ...selection },
      complex: { ...selection },
    };
  };
  const [draft, setDraft] = useState<AutoModelConfig>(value ?? empty());
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    setDraft(value ?? empty());
  }, [value]);
  const busy = disabled || saving;
  const labels = zh
    ? ["判断模型", "简单任务", "中等任务", "复杂任务"]
    : ["Classifier", "Simple tasks", "Medium tasks", "Complex tasks"];
  const keys = ["classifier", "simple", "medium", "complex"] as const;
  const change = (
    key: (typeof keys)[number],
    selection: ModelAliasSummary,
  ): void => setDraft((current) => ({ ...current, [key]: selection }));
  return (
    <section className="settings-section">
      <header className="settings-section-header">
        <h2 className="settings-section-title">Auto</h2>
        <p className="settings-section-description">
          {zh
            ? "按任务复杂度选择执行模型。每个新任务会先调用判断模型，产生额外耗时和用量。"
            : "Choose an execution model by task complexity. Each new task first calls the classifier, adding latency and usage."}
        </p>
      </header>
      <div className="settings-group">
        <SettingsRow title={zh ? "启用 Auto" : "Enable Auto"}>
          <button
            className="settings-switch"
            type="button"
            role="switch"
            aria-label={zh ? "启用 Auto" : "Enable Auto"}
            aria-checked={draft.enabled}
            disabled={busy}
            onClick={() =>
              setDraft({
                ...draft,
                enabled: !draft.enabled,
                default: !draft.enabled && draft.default,
              })
            }
          >
            <span className="settings-switch-thumb" aria-hidden="true" />
          </button>
        </SettingsRow>
        {draft.enabled && (
          <SettingsRow
            title={
              zh ? "新会话默认使用 Auto" : "Use Auto for new conversations"
            }
          >
            <button
              className="settings-switch"
              type="button"
              role="switch"
              aria-label={
                zh ? "新会话默认使用 Auto" : "Use Auto for new conversations"
              }
              aria-checked={draft.default}
              disabled={busy}
              onClick={() => setDraft({ ...draft, default: !draft.default })}
            >
              <span className="settings-switch-thumb" aria-hidden="true" />
            </button>
          </SettingsRow>
        )}
      </div>
      {draft.enabled && (
        <div className="auto-model-selections">
          {keys.map((key, index) => {
            const selected = draft[key];
            const provider = providers.find(
              (p) => p.name === selected.provider,
            );
            const models =
              provider?.models?.filter((m) => m.id !== "wuu/auto") ?? [];
            const variants = providerModelVariantOptions(
              provider,
              selected.model,
              selected.variant ?? "",
            );
            return (
              <fieldset
                key={key}
                className="auto-model-selection"
                disabled={busy}
              >
                <legend>{labels[index]}</legend>
                <label>
                  <span>{zh ? "服务" : "Provider"}</span>
                  <SelectMenu
                    triggerClassName="settings-select-trigger"
                    ariaLabel={`${labels[index]} ${zh ? "服务" : "provider"}`}
                    value={selected.provider}
                    disabled={busy}
                    options={providers.map((p) => ({
                      value: p.name,
                      label: p.name,
                    }))}
                    onChange={(value) => {
                      const p = providers.find((p) => p.name === value);
                      change(key, { provider: value, model: p?.model ?? "" });
                    }}
                  />
                </label>
                <label>
                  <span>{zh ? "模型" : "Model"}</span>
                  <SelectMenu
                    triggerClassName="settings-select-trigger"
                    ariaLabel={`${labels[index]} ${zh ? "模型" : "model"}`}
                    value={selected.model}
                    disabled={busy}
                    searchable
                    options={models.map((m) => ({
                      value: m.id,
                      label: m.display_name || m.id,
                    }))}
                    onChange={(value) =>
                      change(key, {
                        ...selected,
                        model: value,
                        effort: "",
                        variant: "",
                      })
                    }
                  />
                </label>
                {variants.some(Boolean) && (
                  <label>
                    <span>{zh ? "推理强度" : "Reasoning"}</span>
                    <SelectMenu
                      triggerClassName="settings-select-trigger"
                      ariaLabel={`${labels[index]} ${zh ? "推理强度" : "reasoning"}`}
                      value={selected.variant ?? selected.effort ?? ""}
                      disabled={busy}
                      options={[
                        { value: "", label: zh ? "模型默认" : "Model default" },
                        ...variants
                          .filter(Boolean)
                          .map((v) => ({ value: v, label: variantLabel(v) })),
                      ]}
                      onChange={(value) =>
                        change(key, { ...selected, variant: value, effort: "" })
                      }
                    />
                  </label>
                )}
              </fieldset>
            );
          })}
        </div>
      )}
      {error && (
        <p role="alert" className="settings-error">
          {error}
        </p>
      )}
      <button
        type="button"
        className="settings-button settings-button-primary"
        disabled={
          busy ||
          (draft.enabled &&
            keys.some(
              (k) =>
                !draft[k].provider ||
                !draft[k].model ||
                !providers.some((p) => p.name === draft[k].provider),
            ))
        }
        onClick={async () => {
          setSaving(true);
          setError("");
          try {
            await onSave({ auto_model: draft });
          } catch (e) {
            setError(e instanceof Error ? e.message : String(e));
          } finally {
            setSaving(false);
          }
        }}
      >
        {saving
          ? zh
            ? "保存中"
            : "Saving"
          : zh
            ? "保存 Auto 设置"
            : "Save Auto settings"}
      </button>
    </section>
  );
}
