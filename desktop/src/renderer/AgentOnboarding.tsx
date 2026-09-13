import { ArrowRight, X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import type { ChannelAgentCreateParams, InitializeResult, NamedAgent, ProviderModelSummary, ProviderSummary } from "../shared/protocol";
import { AgentAvatarMark, randomAgentAvatarKey } from "./AgentAvatarMark";
import { ChannelComposer } from "./ChannelComposer";
import { effortLabel, providerModelEffortOptions, providerModelReasoningMode } from "./RuntimeHelpers";
import { MessageBubble, MessageBubbleRow } from "./MessageBubbleFlow";
import { SelectMenu } from "./SelectMenu";
import { LinuxWindowControls } from "./LinuxWindowControls";
import { useI18n } from "./i18n";
import "./styles/agent-onboarding.css";

export type AgentOnboardingDraft = {
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
  navigation?: ReactNode;
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
    name: "", role: "", avatarKey: randomAgentAvatarKey(), avatarImage: "",
    ...initialModel(initialized), requestId: newRequestID(),
  };
}

export function AgentOnboarding({ draft, onDraftChange, initialized, navigation, onCreate, onOpenConversation, onManageProviders, onClose }: AgentOnboardingProps): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState<"creating" | "opening" | null>(null);
  const [error, setError] = useState("");
  const panelRef = useRef<HTMLElement>(null);
  const draftRef = useRef(draft);
  const pendingRef = useRef(false);
  draftRef.current = draft;
  const providers = configuredProviders(initialized);
  const provider = providers.find((item) => item.name === draft.provider);
  const models = modelsFor(provider);
  const model = models.find((item) => item.id === draft.model);
  const reasoning = providerModelReasoningMode(provider, draft.model);
  const efforts = effortsFor(provider, draft.model);
  const locked = Boolean(busy || draft.createdAgent);
  const canCreate = Boolean(provider && model && efforts.includes(draft.effort));

  function update(patch: Partial<AgentOnboardingDraft>): void {
    if (pendingRef.current || draftRef.current.createdAgent) return;
    const current = draftRef.current;
    if (Object.entries(patch).every(([key, value]) => value === current[key as keyof AgentOnboardingDraft])) return;
    const next = { ...current, ...patch, requestId: newRequestID() };
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
    panelRef.current?.focus();
  }, []);

  useEffect(() => {
    if (error) panelRef.current?.querySelector<HTMLElement>('[data-action="submit"]')?.focus();
  }, [error]);

  async function submit(): Promise<void> {
    if (pendingRef.current) return;
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
          name: current.name.trim() || t("channels.newAgent"), role: current.role.trim(),
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
      onClose();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("agentOnboarding.unexpectedError"));
    } finally {
      pendingRef.current = false;
      setBusy(null);
    }
  }

  return <section className="channel-view channel-mode-rooms agent-onboarding" data-wuu-component="agent-onboarding" aria-label={t("channels.newAgent")} tabIndex={-1} ref={panelRef}>
    <header className="titlebar channel-room-header" data-wuu-component="conversation-titlebar">
      {navigation}
      <span className="channel-room-header-avatar"><AgentAvatarMark seed={draft.requestId} avatarKey={draft.avatarKey} /></span>
      <input className="agent-onboarding-name" name="agent-name" aria-label={t("agentOnboarding.name")} value={draft.name} placeholder={t("channels.newAgent")} onChange={(event) => update({ name: event.currentTarget.value })} disabled={locked} />
      <button type="button" className="icon-button" data-action="close" aria-label={t("agentOnboarding.cancel")} onClick={() => { if (!pendingRef.current) onClose(); }} disabled={Boolean(busy)}><X size={18} /></button>
      <LinuxWindowControls />
    </header>
    <div className="agent-onboarding-scroll">
      <MessageBubbleRow outgoing={false} className="channel-message agent" contentClassName="channel-message-content"
        avatar={<AgentAvatarMark seed={draft.requestId} avatarKey={draft.avatarKey} status={busy ? "thinking" : "idle"} />}
        meta={<div className="channel-message-meta"><strong className="agent-onboarding-author">{draft.name.trim() || t("channels.newAgent")}</strong></div>}>
        <MessageBubble outgoing={false} className="agent-onboarding-bubble">
          <form className="agent-onboarding-form" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
            <p className="agent-onboarding-prompt">{t(providers.length ? "agentOnboarding.selectBeforeChat" : "agentOnboarding.noProviders")}</p>
            {providers.length > 0 ? <>
              <SelectMenu dataField="agent-model" className="agent-onboarding-model" triggerClassName="agent-onboarding-model-trigger"
                value={`${draft.provider}\u0000${draft.model}`} ariaLabel={t("agentOnboarding.model")} placeholder={t("agentOnboarding.chooseModel")}
                disabled={locked} searchable flip groups={providers.map((item) => ({
                  label: item.name,
                  options: modelsFor(item).map((candidate) => ({ value: `${item.name}\u0000${candidate.id}`, label: candidate.display_name || candidate.id, keywords: [item.name, candidate.id] })),
                }))} onChange={(value) => {
                  const [providerName, modelID] = value.split("\u0000");
                  const nextProvider = providers.find((item) => item.name === providerName);
                  update({ provider: providerName, model: modelID, effort: modelEffort(nextProvider, modelID, providerName === draft.provider && modelID === draft.model ? draft.effort : "") });
                }} />
              {model && reasoning !== "off" ? <div className="agent-onboarding-effort">
                <span>{t("agentOnboarding.effort")}</span>
                <SelectMenu dataField="agent-effort" value={draft.effort} ariaLabel={t("agentOnboarding.effort")} disabled={locked}
                  options={efforts.map((value) => ({ value, label: reasoning === "toggle" ? t(value === "none" ? "agentOnboarding.thinkingOff" : "agentOnboarding.thinkingOn") : value ? effortLabel(value) : t("agentOnboarding.modelDefault") }))}
                  onChange={(value) => update({ effort: value })} />
              </div> : null}
              {!provider || !model || !efforts.includes(draft.effort) ? <p className="agent-onboarding-error" role="status">{t("agentOnboarding.selectionUnavailable")}</p> : null}
            </> : null}
            {error ? <div className="agent-onboarding-error" role="alert">{draft.createdAgent ? <strong>{t("agentOnboarding.openFailed")}</strong> : null}<span>{error}</span></div> : null}
            <footer className="agent-onboarding-footer">
              {onManageProviders ? <button type="button" className={providers.length ? "agent-onboarding-manage" : "agent-onboarding-submit"} data-action="manage-providers" onClick={() => { if (!pendingRef.current) onManageProviders(); }} disabled={locked}>{t(providers.length ? "agentOnboarding.manageProviders" : "agentOnboarding.addProvider")}</button> : null}
              {providers.length > 0 || draft.createdAgent ? <button type="submit" className="agent-onboarding-submit" data-action="submit" disabled={Boolean(busy) || (!draft.createdAgent && !canCreate)}>{busy === "creating" ? t("agentOnboarding.creating") : busy === "opening" ? t("agentOnboarding.opening") : draft.createdAgent ? t("agentOnboarding.openConversation") : t("agentOnboarding.startChat")} {!busy ? <ArrowRight size={14} /> : null}</button> : null}
            </footer>
          </form>
        </MessageBubble>
      </MessageBubbleRow>
    </div>
    <div className="channel-conversation-footer">
      <ChannelComposer draft="" placeholder={t("agentOnboarding.chooseModelFirst")} compact disabled sending={false} files={[]} images={[]} onPasteAttachmentFiles={() => {}} onRemoveFile={() => {}} onRemoveImage={() => {}} onChangeDraft={() => {}} onSend={() => {}} />
    </div>
  </section>;
}
