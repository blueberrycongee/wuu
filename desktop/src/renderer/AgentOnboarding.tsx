import { ArrowLeft, ArrowRight, ChevronDown, X } from "lucide-react";
import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent } from "react";
import type { ChannelAgentCreateParams, InitializeResult, NamedAgent, ProviderModelSummary, ProviderSummary } from "../shared/protocol";
import { AgentAvatarCreator } from "./AgentAvatarCreator";
import { AGENT_AVATAR_SHAPES, AgentAvatarMark, agentAvatarConfig, randomAgentAvatarKey, serializeAgentAvatarConfig } from "./AgentAvatarMark";
import { AVATAR_HUES } from "./DefaultAvatar";
import { effortLabel, providerModelEffortOptions, providerModelReasoningMode } from "./RuntimeHelpers";
import { SelectMenu } from "./SelectMenu";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import { squareAvatarImageFromFile } from "./avatarImage";
import "./styles/agent-onboarding.css";

export type AgentOnboardingDraft = {
  step: "identity" | "model";
  name: string;
  role: string;
  avatarKey: string;
  avatarImage: string;
  provider: string;
  model: string;
  effort: string;
  requestId: string;
  createdAgent?: NamedAgent;
};

type AgentOnboardingProps = {
  draft: AgentOnboardingDraft;
  onDraftChange: (draft: AgentOnboardingDraft) => void;
  initialized?: InitializeResult;
  onCreate: (params: ChannelAgentCreateParams) => Promise<NamedAgent>;
  onOpenConversation: (agent: NamedAgent) => Promise<void>;
  onManageProviders?: () => void;
  onClose: () => void;
};

function newRequestID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  // LAN web clients may not be secure contexts, but getRandomValues is available.
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function configuredProviders(initialized?: InitializeResult): ProviderSummary[] {
  // The host resolves API keys, OAuth and local credential stores into this flag.
  return initialized?.providers?.filter((provider) => provider.api_key_configured === true) ?? [];
}

function modelsFor(provider?: ProviderSummary): ProviderModelSummary[] {
  const models = provider?.models?.filter((model) => model.id) ?? [];
  return provider?.model && !models.some((model) => model.id === provider.model)
    ? [{ id: provider.model }, ...models]
    : models;
}

function modelEffort(provider: ProviderSummary | undefined, modelID: string, preferred = ""): string {
  const options = effortsFor(provider, modelID);
  if (preferred && options.includes(preferred)) return preferred;
  const model = modelsFor(provider).find((item) => item.id === modelID);
  const declared = model?.default_variant || model?.default_effort || "";
  return options.includes(declared) ? declared : options[0] ?? "";
}

function effortsFor(provider: ProviderSummary | undefined, modelID: string): string[] {
  const options = providerModelEffortOptions(provider, modelID, "");
  return providerModelReasoningMode(provider, modelID) === "levels" ? options.filter(Boolean) : options;
}

function initialModel(initialized?: InitializeResult): Pick<AgentOnboardingDraft, "provider" | "model" | "effort"> {
  const providers = configuredProviders(initialized);
  const provider = providers.find((item) => item.name === initialized?.provider) ?? providers[0];
  const models = modelsFor(provider);
  const model = provider?.name === initialized?.provider && models.some((item) => item.id === initialized.model)
    ? initialized.model
    : models.find((item) => item.id === provider?.model)?.id ?? models[0]?.id ?? "";
  const preferred = provider?.name === initialized?.provider && model === initialized.model
    ? initialized.variant || initialized.effort || ""
    : "";
  return { provider: provider?.name ?? "", model, effort: modelEffort(provider, model, preferred) };
}

export function createAgentOnboardingDraft(initialized?: InitializeResult): AgentOnboardingDraft {
  return {
    step: "identity", name: "", role: "", avatarKey: randomAgentAvatarKey(), avatarImage: "",
    ...initialModel(initialized), requestId: newRequestID(),
  };
}

const TEMPLATES = ["code", "research", "writing"] as const;
const COLORS = AVATAR_HUES.filter((_, index) => index % 2 === 0);

export function AgentOnboarding({ draft, onDraftChange, initialized, onCreate, onOpenConversation, onManageProviders, onClose }: AgentOnboardingProps): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState<"creating" | "opening" | null>(null);
  const [error, setError] = useState("");
  const [imageBusy, setImageBusy] = useState(false);
  const [customizing, setCustomizing] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const imageInputRef = useRef<HTMLInputElement>(null);
  const draftRef = useRef(draft);
  const pendingRef = useRef(false);
  const completedRef = useRef(false);
  draftRef.current = draft;
  const providers = configuredProviders(initialized);
  const provider = providers.find((item) => item.name === draft.provider);
  const models = modelsFor(provider);
  const model = models.find((item) => item.id === draft.model);
  const reasoning = providerModelReasoningMode(provider, draft.model);
  const efforts = effortsFor(provider, draft.model);
  const config = agentAvatarConfig(draft.avatarKey);
  const locked = Boolean(busy || imageBusy || draft.createdAgent);
  const canCreate = Boolean(draft.name.trim() && draft.role.length <= 280 && provider && model && efforts.includes(draft.effort));

  function update(patch: Partial<AgentOnboardingDraft>): void {
    if (pendingRef.current || draftRef.current.createdAgent) return;
    const current = draftRef.current;
    const contentChanged = Object.entries(patch).some(([key, value]) => key !== "step" && value !== current[key as keyof AgentOnboardingDraft]);
    const next = { ...current, ...patch, requestId: contentChanged ? newRequestID() : current.requestId };
    draftRef.current = next;
    onDraftChange(next);
    setError("");
  }

  useEffect(() => {
    if (draft.provider || draft.createdAgent || pendingRef.current) return;
    const selection = initialModel(initialized);
    if (selection.provider) update(selection);
  }, [initialized, draft.provider, draft.createdAgent]);

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return () => { if (!completedRef.current && previous?.isConnected) previous.focus(); };
  }, []);

  useEffect(() => {
    const panel = panelRef.current;
    const target = panel?.querySelector<HTMLElement>(draft.step === "identity" ? '[name="agent-name"]:not(:disabled)' : '[data-field="agent-provider"]:not(:disabled)')
      ?? panel?.querySelector<HTMLElement>('[data-action="manage-providers"]:not(:disabled), [data-action="submit"]:not(:disabled)')
      ?? panel?.querySelector<HTMLElement>('[data-action="close"]:not(:disabled)');
    (target ?? panel)?.focus();
  }, [draft.step]);

  useEffect(() => {
    // Disabling the focused submit button can otherwise move focus behind the dialog.
    if (busy || imageBusy) panelRef.current?.focus();
    else if (error) panelRef.current?.querySelector<HTMLElement>(draft.step === "identity" ? '[data-action="upload-image"]' : '[data-action="submit"]')?.focus();
  }, [busy, imageBusy, error, draft.step]);

  function close(): void {
    if (!pendingRef.current) onClose();
  }

  async function uploadImage(file: File): Promise<void> {
    if (pendingRef.current || draftRef.current.createdAgent) return;
    pendingRef.current = true;
    setImageBusy(true);
    setError("");
    try {
      const avatarImage = await squareAvatarImageFromFile(file);
      pendingRef.current = false;
      update({ avatarImage });
    } catch {
      setError(t("agentOnboarding.imageError"));
    } finally {
      pendingRef.current = false;
      setImageBusy(false);
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    event.stopPropagation();
    if (event.defaultPrevented) return;
    if (event.key === "Escape") {
      event.preventDefault();
      close();
    }
    if (event.key !== "Tab" || (event.target instanceof Element && event.target.closest("[data-floating-menu-owner]"))) return;
    const controls = Array.from(panelRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]') ?? [])
      .filter((element) => !element.closest("details:not([open])") || element.tagName === "SUMMARY");
    const first = controls[0];
    const last = controls.at(-1);
    if (!first) {
      event.preventDefault(); panelRef.current?.focus();
      return;
    }
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault(); last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault(); first?.focus();
    }
  }

  async function submit(): Promise<void> {
    if (pendingRef.current) return;
    if (draft.step === "identity") {
      if (draft.name.trim() && draft.role.length <= 280) update({ step: "model" });
      return;
    }
    if (!draft.createdAgent && !canCreate) return;
    pendingRef.current = true;
    setError("");
    let agent = draftRef.current.createdAgent;
    try {
      if (!agent) {
        setBusy("creating");
        const current = draftRef.current;
        agent = await onCreate({
          request_id: current.requestId,
          name: current.name.trim(), role: current.role.trim(),
          avatar_key: current.avatarKey, avatar_image: current.avatarImage || undefined,
          engine_override: "wuu", provider_override: current.provider,
          model_override: current.model, effort_override: current.effort,
        });
        // Persist the successful identity before opening its conversation can fail.
        const next = { ...current, createdAgent: agent };
        draftRef.current = next;
        onDraftChange(next);
      }
      setBusy("opening");
      await onOpenConversation(agent);
      completedRef.current = true;
      onClose();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("agentOnboarding.unexpectedError"));
    } finally {
      pendingRef.current = false;
      setBusy(null);
    }
  }

  return <UILayerPortal layer="dialog">
    <div className="agent-onboarding" data-wuu-component="agent-onboarding" role="dialog" aria-modal="true" aria-labelledby="agent-onboarding-title" tabIndex={-1} onKeyDown={handleKeyDown} ref={panelRef}>
      <header className="agent-onboarding-header">
        <h1 id="agent-onboarding-title">{t("agentOnboarding.title")}</h1>
        <ol className="agent-onboarding-progress" aria-label={t("agentOnboarding.steps")}>
          <li aria-current={draft.step === "identity" ? "step" : undefined}>{t("agentOnboarding.identityStep")}</li>
          <li aria-hidden="true"><ArrowRight size={12} /></li>
          <li aria-current={draft.step === "model" ? "step" : undefined}>{t("agentOnboarding.modelStep")}</li>
        </ol>
        <button type="button" className="agent-onboarding-close" data-action="close" aria-label={t("agentOnboarding.cancel")} onClick={close} disabled={Boolean(busy || imageBusy)}><X size={18} /></button>
      </header>
      <div className="agent-onboarding-scroll">
        <form className="agent-onboarding-form" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
          <div className="agent-onboarding-preview">
            <AgentAvatarMark seed="new-agent-preview" avatarKey={draft.avatarKey} avatarImage={draft.avatarImage} status={busy ? "thinking" : error ? "failed" : "idle"} />
            <h2>{draft.name.trim() || t("agentOnboarding.previewName")}</h2>
            {draft.step === "model" && draft.role.trim() ? <p>{draft.role}</p> : null}
          </div>
          <div className="agent-onboarding-step" key={draft.step} data-step={draft.step}>
            {draft.step === "identity" ? <>
              <label className="agent-onboarding-field"><span>{t("agentOnboarding.name")}</span><input name="agent-name" value={draft.name} onChange={(event) => update({ name: event.target.value })} placeholder={t("agentOnboarding.namePlaceholder")} autoComplete="off" disabled={locked} required /></label>
              <label className="agent-onboarding-field"><span>{t("agentOnboarding.role")}<small>{draft.role.length}/280</small></span><textarea name="agent-role" value={draft.role} onChange={(event) => update({ role: event.target.value })} placeholder={t("agentOnboarding.rolePlaceholder")} maxLength={280} rows={3} disabled={locked} /></label>
              <div className="agent-onboarding-templates" role="group" aria-label={t("agentOnboarding.templates")}>
                {TEMPLATES.map((template) => <button type="button" key={template} data-template={template} disabled={locked} onClick={() => update({ name: t(`agentOnboarding.template.${template}.name`), role: t(`agentOnboarding.template.${template}.role`) })}>{t(`agentOnboarding.template.${template}.label`)}</button>)}
              </div>
              <fieldset className="agent-onboarding-appearance" disabled={locked}>
                <legend>{t("agentOnboarding.appearance")}</legend>
                <div className="agent-onboarding-shapes" role="group" aria-label={t("agentOnboarding.shape")}>
                  {AGENT_AVATAR_SHAPES.map((shape) => {
                    const avatarKey = serializeAgentAvatarConfig({ ...config, shape: shape.id });
                    return <button type="button" key={shape.id} data-shape={shape.id} aria-label={t(`agentOnboarding.shape.${shape.id}`)} aria-pressed={!draft.avatarImage && config.shape === shape.id} onClick={() => update({ avatarKey, avatarImage: "" })}><AgentAvatarMark seed={`shape-${shape.id}`} avatarKey={avatarKey} /></button>;
                  })}
                </div>
                <div className="agent-onboarding-colors" role="group" aria-label={t("agentOnboarding.color")}>
                  {COLORS.map((hue, index) => <button type="button" key={hue} data-hue={hue} aria-label={t("agentOnboarding.colorOption", { number: index + 1 })} aria-pressed={!draft.avatarImage && config.hue === hue} style={{ "--agent-onboarding-hue": hue } as CSSProperties} onClick={() => update({ avatarKey: serializeAgentAvatarConfig({ ...config, hue }), avatarImage: "" })} />)}
                </div>
                <details className="agent-onboarding-customize" open={customizing}>
                  <summary onClick={(event) => { event.preventDefault(); setCustomizing((current) => !current); }}>{t("agentOnboarding.customize")}<ChevronDown size={13} /></summary>
                  {customizing ? <><div className="agent-onboarding-photo-actions">
                    <input ref={imageInputRef} type="file" accept="image/png,image/jpeg,image/webp" hidden disabled={locked} aria-label={t("agentOnboarding.uploadImage")} onChange={(event) => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void uploadImage(file); }} />
                    <button type="button" data-action="upload-image" disabled={locked} onClick={() => imageInputRef.current?.click()}>{t(imageBusy ? "agentOnboarding.preparingImage" : "agentOnboarding.uploadImage")}</button>
                    {draft.avatarImage ? <button type="button" data-action="remove-image" disabled={locked} onClick={() => update({ avatarImage: "" })}>{t("agentOnboarding.removeImage")}</button> : null}
                  </div>
                  <AgentAvatarCreator seed="new-agent-preview" avatarKey={draft.avatarKey} avatarImage={draft.avatarImage} showShapes={false} onChange={(avatarKey) => update({ avatarKey, avatarImage: "" })} />
                  </> : null}
                </details>
              </fieldset>
            </> : <>
              {providers.length > 0 ? <>
                <div className="agent-onboarding-field"><label htmlFor="agent-onboarding-provider">{t("agentOnboarding.provider")}</label><SelectMenu id="agent-onboarding-provider" dataField="agent-provider" value={draft.provider} ariaLabel={t("agentOnboarding.provider")} placeholder={t("agentOnboarding.chooseProvider")} disabled={locked} options={providers.map((item) => ({ value: item.name, label: item.name }))} onChange={(value) => {
                  const nextProvider = providers.find((item) => item.name === value);
                  const nextModel = modelsFor(nextProvider).find((item) => item.id === nextProvider?.model)?.id ?? modelsFor(nextProvider)[0]?.id ?? "";
                  update({ provider: value, model: nextModel, effort: modelEffort(nextProvider, nextModel) });
                }} /></div>
                <div className="agent-onboarding-field"><label htmlFor="agent-onboarding-model">{t("agentOnboarding.model")}</label><SelectMenu id="agent-onboarding-model" dataField="agent-model" value={draft.model} ariaLabel={t("agentOnboarding.model")} placeholder={t("agentOnboarding.chooseModel")} disabled={locked || !models.length} searchable flip options={models.map((item) => ({ value: item.id, label: item.display_name || item.id, hint: item.display_name && item.display_name !== item.id ? item.id : undefined }))} onChange={(value) => update({ model: value, effort: modelEffort(provider, value) })} /></div>
                <div className="agent-onboarding-field"><label htmlFor="agent-onboarding-effort">{t("agentOnboarding.effort")}</label><SelectMenu id="agent-onboarding-effort" dataField="agent-effort" value={draft.effort} ariaLabel={t("agentOnboarding.effort")} disabled={locked || !model || reasoning === "off"} options={efforts.map((value) => ({ value, label: reasoning === "off" ? t("agentOnboarding.effortUnavailable") : reasoning === "toggle" ? t(value === "none" ? "agentOnboarding.thinkingOff" : "agentOnboarding.thinkingOn") : value ? effortLabel(value) : t("agentOnboarding.modelDefault") }))} onChange={(value) => update({ effort: value })} /></div>
                {!provider || !model || !efforts.includes(draft.effort) ? <p className="agent-onboarding-error" role="status">{t("agentOnboarding.selectionUnavailable")}</p> : null}
                <p className="agent-onboarding-model-note">{t("agentOnboarding.modelNote")}</p>
              </> : <p className="agent-onboarding-provider-empty">{t("agentOnboarding.noProviders")}</p>}
              {onManageProviders ? <button type="button" className="agent-onboarding-manage" data-action="manage-providers" onClick={() => { if (!pendingRef.current) onManageProviders(); }} disabled={locked}>{t(providers.length ? "agentOnboarding.manageProviders" : "agentOnboarding.addProvider")}<ArrowRight size={14} /></button> : null}
            </>}
          </div>
          {error ? <div className="agent-onboarding-error" role="alert">{draft.createdAgent ? <strong>{t("agentOnboarding.openFailed")}</strong> : null}<span>{error}</span></div> : null}
          <footer className="agent-onboarding-footer">
            {draft.step === "model" ? <button type="button" className="agent-onboarding-back" data-action="back" disabled={locked} onClick={() => update({ step: "identity" })}><ArrowLeft size={14} />{t("agentOnboarding.back")}</button> : <span />}
            <button type="submit" className="agent-onboarding-submit" data-action="submit" disabled={Boolean(busy || imageBusy) || (draft.step === "identity" ? !draft.name.trim() || draft.role.length > 280 : !draft.createdAgent && !canCreate)}>{busy === "creating" ? t("agentOnboarding.creating") : busy === "opening" ? t("agentOnboarding.opening") : draft.createdAgent ? t("agentOnboarding.openConversation") : draft.step === "identity" ? t("agentOnboarding.continue") : t("agentOnboarding.create")} {!busy ? <ArrowRight size={14} /> : null}</button>
          </footer>
        </form>
      </div>
    </div>
  </UILayerPortal>;
}
