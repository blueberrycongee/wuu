import { useEffect, useRef, useState } from "react";
import type {
  AutoModelConfig,
  ModelAliasSummary,
  ProviderSummary,
  RuntimeAdvancedSettingsUpdate,
} from "../shared/protocol";
import { SelectMenu } from "./SelectMenu";
import { useI18n } from "./i18n";
import { providerModelVariantOptions, variantLabel } from "./RuntimeHelpers";

const roles = ["classifier", "simple", "medium", "complex"] as const;
const selectionID = (selection: ModelAliasSummary): string =>
  JSON.stringify([selection.provider, selection.model]);

function initialConfig(providers: ProviderSummary[]): AutoModelConfig {
  const provider = providers.find((p) =>
    [p.model, ...(p.models ?? []).map((m) => m.id)].some((id) => id && id !== "wuu/auto"),
  );
  const selection = {
    provider: provider?.name ?? "",
    model: [provider?.model, ...(provider?.models ?? []).map((m) => m.id)]
      .find((id) => id && id !== "wuu/auto") ?? "",
  };
  return {
    enabled: false,
    default: false,
    classifier: { ...selection },
    simple: { ...selection },
    medium: { ...selection },
    complex: { ...selection },
  };
}

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
  const [draft, setDraft] = useState<AutoModelConfig>(() => value ?? initialConfig(providers));
  const confirmed = useRef(draft);
  const pending = useRef(false);
  const selectionsRef = useRef<HTMLDivElement>(null);
  const revealOnEnable = useRef(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    const next = value ?? initialConfig(providers);
    confirmed.current = next;
    if (!pending.current) setDraft(next);
  }, [value, providers]);
  useEffect(() => {
    if (!draft.enabled || !revealOnEnable.current) return;
    revealOnEnable.current = false;
    selectionsRef.current?.scrollIntoView({
      block: "nearest",
      inline: "nearest",
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth",
    });
  }, [draft.enabled]);
  const busy = disabled || saving;
  const labels = zh
    ? ["判断模型", "简单任务", "中等任务", "复杂任务"]
    : ["Classifier", "Simple tasks", "Medium tasks", "Complex tasks"];

  async function save(next: AutoModelConfig): Promise<void> {
    // Serialize full-policy writes so a slower response cannot undo a later choice.
    if (disabled || pending.current) return;
    pending.current = true;
    setSaving(true);
    setError("");
    setDraft(next);
    try {
      await onSave({ auto_model: next });
      confirmed.current = next;
    } catch (e) {
      revealOnEnable.current = false;
      setDraft(confirmed.current);
      const detail = e instanceof Error ? e.message : String(e);
      setError(zh ? `保存失败，已恢复原设置：${detail}` : `Could not save. Previous settings restored: ${detail}`);
    } finally {
      pending.current = false;
      setSaving(false);
    }
  }

  const groups = providers.map((provider) => {
    // Custom configured models need not appear in the provider's catalog.
    const modelIDs = new Set([
      provider.model,
      ...(provider.models ?? []).map((model) => model.id),
      ...roles.filter((key) => draft[key].provider === provider.name)
        .map((key) => draft[key].model),
    ]);
    return {
      label: provider.name,
      options: [...modelIDs].filter((id) => id && id !== "wuu/auto").map((id) => {
        const model = provider.models?.find((item) => item.id === id);
        const selection = { provider: provider.name, model: id };
        return {
          value: selectionID(selection),
          label: `${model?.display_name || id} · ${provider.name}`,
          selection,
        };
      }),
    };
  });
  const selections = groups.flatMap((group) => group.options);

  return (
    <section className="settings-section auto-model-settings" aria-busy={saving}>
      <header className="auto-model-heading">
        <div className="settings-section-header">
          <h2 className="settings-section-title">Auto</h2>
          <p className="settings-section-description">
            {zh
              ? "设置判断模型，根据任务复杂度动态选择执行模型。"
              : "Set a classifier to choose execution models dynamically based on task complexity."}
          </p>
        </div>
        <button
          className="settings-switch"
          type="button"
          role="switch"
          aria-label={zh ? "启用 Auto" : "Enable Auto"}
          aria-checked={draft.enabled}
          disabled={busy || (!draft.enabled && selections.length === 0)}
          onClick={() => {
            const next = {
              ...draft,
              enabled: !draft.enabled,
              default: !draft.enabled && draft.default,
            };
            // A service may have been removed while Auto was off. Initialize
            // only missing references so hidden fields cannot block re-enabling.
            if (next.enabled && selections[0]) {
              for (const key of roles) {
                if (!selections.some((option) => option.value === selectionID(next[key]))) {
                  next[key] = { ...selections[0].selection };
                }
              }
            }
            revealOnEnable.current = next.enabled;
            void save(next);
          }}
        >
          <span className="settings-switch-thumb" aria-hidden="true" />
        </button>
      </header>
      {selections.length === 0 ? (
        <p className="settings-muted-line">
          {zh ? "请先连接服务并添加模型。" : "Connect a service and add a model first."}
        </p>
      ) : draft.enabled ? (
        <div ref={selectionsRef} className="auto-model-selections">
          {roles.map((key, index) => {
            const selected = draft[key];
            const provider = providers.find((p) => p.name === selected.provider);
            const current = selected.variant || selected.effort || "";
            const variants = providerModelVariantOptions(provider, selected.model, current);
            const hasReasoning = Boolean(current) || variants.some(Boolean);
            return (
              <div className="settings-row auto-model-selection" key={key}>
                <span className="settings-row-label-title">{labels[index]}</span>
                <SelectMenu
                  triggerClassName="settings-select-trigger"
                  ariaLabel={`${labels[index]} ${zh ? "模型" : "model"}`}
                  value={selectionID(selected)}
                  placeholder={selected.model
                    ? `${selected.model} · ${selected.provider}`
                    : zh ? "选择模型" : "Select model"}
                  disabled={busy}
                  searchable
                  flip
                  searchPlaceholder={zh ? "搜索模型或服务" : "Search models or services"}
                  emptyMessage={zh ? "没有可选模型" : "No models available"}
                  groups={groups}
                  onChange={(id) => {
                    const option = selections.find((item) => item.value === id);
                    if (option && id !== selectionID(selected)) {
                      void save({ ...draft, [key]: { ...option.selection, effort: "", variant: "" } });
                    }
                  }}
                />
                <SelectMenu
                  triggerClassName="settings-select-trigger"
                  ariaLabel={`${labels[index]} ${zh ? "推理强度" : "reasoning"}`}
                  value={current}
                  disabled={busy || !hasReasoning}
                  flip
                  options={[...new Set(["", ...variants, current])].map((variant) => ({
                    value: variant,
                    label: variant ? variantLabel(variant) : zh ? "模型默认" : "Model default",
                  }))}
                  onChange={(variant) => {
                    if (variant !== current) {
                      void save({ ...draft, [key]: { ...selected, variant, effort: "" } });
                    }
                  }}
                />
              </div>
            );
          })}
        </div>
      ) : null}
      {error && <p role="alert" className="settings-error">{error}</p>}
    </section>
  );
}
