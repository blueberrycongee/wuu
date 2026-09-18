import { ArrowRight, Shuffle, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { ChannelRoomOnboarding, ChannelAgentCreateParams, InitializeResult, NamedAgent, ProviderModelSummary, ProviderSummary } from "../shared/protocol";
import { randomAgentAvatarKey } from "./AgentAvatarMark";
import { AgentOnboardingHistory } from "./AgentOnboardingHistory";
import { AgentOnboardingAvatar, AGENT_BUBBLE_DELAY_MS } from "./AgentOnboardingAvatar";
import { useChannelMessageMotion } from "./useChannelMessageMotion";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { ChannelComposer, type ChannelComposerHandle } from "./ChannelComposer";
import { providerModelVariantOptions } from "./RuntimeHelpers";
import { MessageBubble, MessageBubbleRow } from "./MessageBubbleFlow";
import { RuntimeModelMenu } from "./ComposerRuntimeMenus";
import { useI18n } from "./i18n";
import "./styles/agent-onboarding.css";

export type AgentOnboardingDraft = {
  step?: "model" | "name";
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
  onOpenConversation: (agent: NamedAgent, onboarding: ChannelRoomOnboarding) => Promise<void>;
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
  return providerModelVariantOptions(provider, modelID, "");
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
  const [intro, setIntro] = useState(() => prefersReducedMotion() ? 2 : 1);
  const [sentName, setSentName] = useState(draft.createdAgent?.name ?? "");
  const scrollRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<ChannelComposerHandle>(null);
  const step = draft.step ?? "model";
  const arrivals = useMemo(() => [
    ...(intro >= 2 ? [{ id: "model", seq: 1 }] : []),
    ...(step === "name" ? [{ id: "name", seq: 2 }] : []),
    ...(sentName ? [{ id: "answer", seq: 3 }] : []),
  ], [intro, step, sentName]);
  useChannelMessageMotion(scrollRef, "agent-onboarding", true, arrivals);
  useEffect(() => {
    if (prefersReducedMotion()) return;
    const bubble = window.setTimeout(() => setIntro(2), AGENT_BUBBLE_DELAY_MS);
    return () => { window.clearTimeout(bubble); };
  }, []);
  useEffect(() => {
    if (step === "name") composerRef.current?.focus();
    scrollRef.current?.scrollTo?.({ top: scrollRef.current.scrollHeight });
  }, [step, sentName]);
  const panelRef = useRef<HTMLElement>(null);
  const draftRef = useRef(draft);
  const pendingRef = useRef(false);
  draftRef.current = draft;
  const providers = configuredProviders(initialized);
  const provider = providers.find((item) => item.name === draft.provider);
  const models = modelsFor(provider);
  const model = models.find((item) => item.id === draft.model);
  const efforts = effortsFor(provider, draft.model);
  const locked = Boolean(busy || draft.createdAgent);
  const canCreate = Boolean(provider && model && efforts.includes(draft.effort));

  function update(patch: Partial<AgentOnboardingDraft>): void {
    if (pendingRef.current || draftRef.current.createdAgent) return;
    const current = draftRef.current;
    if (Object.entries(patch).every(([key, value]) => value === current[key as keyof AgentOnboardingDraft])) return;
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
    if (step === "name") composerRef.current?.focus();
    else panelRef.current?.focus();
  }, []);

  useEffect(() => {
    if (error) panelRef.current?.querySelector<HTMLElement>('[data-action="submit"]')?.focus();
  }, [error]);

  async function submit(): Promise<void> {
    if (pendingRef.current) return;
    if (!draft.createdAgent && !canCreate) return;
    const name = draftRef.current.name.trim() || t("channels.newAgent");
    setSentName(name);
    pendingRef.current = true;
    setError("");
    let agent = draftRef.current.createdAgent;
    try {
      if (!agent) {
        setBusy("creating");
        const current = draftRef.current;
        agent = await onCreate({
          request_id: current.requestId,
          name, role: current.role.trim(),
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
      // Let the shared outgoing entrance finish before replacing the setup stream.
      if (!prefersReducedMotion()) await new Promise(resolve => window.setTimeout(resolve, motionDurationMs("--motion-slow", 280)));
      await onOpenConversation(agent, {
        model_prompt: t("agentOnboarding.selectBeforeChat"),
        name_prompt: t("agentOnboarding.askName"),
        name: agent.name, provider: draftRef.current.provider,
        model: model?.display_name || draftRef.current.model, effort: draftRef.current.effort,
        avatar_key: draftRef.current.avatarKey,
      });
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
      <span className="agent-onboarding-name">{sentName || t("channels.newAgent")}</span>
      <button type="button" className="icon-button" data-action="close" aria-label={t("agentOnboarding.cancel")} onClick={() => { if (!pendingRef.current) onClose(); }} disabled={Boolean(busy)}><X size={18} /></button>
    </header>
    <div className="agent-onboarding-scroll" ref={scrollRef}>
      {step === "model" ? <div className="agent-onboarding-first-message">
      {intro >= 1 ? <div className="agent-onboarding-intro-avatar"><AgentOnboardingAvatar avatarKey={draft.avatarKey} /></div> : null}
      {intro >= 2 ? <MessageBubbleRow messageID="model" outgoing={false} className="channel-message agent" contentClassName="channel-message-content"

        >
        <MessageBubble outgoing={false} className="agent-onboarding-bubble">
          <form className="agent-onboarding-form" onSubmit={(event) => { event.preventDefault(); if (canCreate && !locked) update({ step: "name" }); }}>
            <p className="agent-onboarding-prompt">{t(providers.length ? "agentOnboarding.selectBeforeChat" : "agentOnboarding.noProviders")}</p>
            {providers.length > 0 ? <>
              <fieldset className="agent-onboarding-runtime" disabled={locked} inert={locked}>
                <RuntimeModelMenu
                  initialized={{ ...initialized!, providers, provider: draft.provider, model: draft.model, effort: draft.effort, variant: draft.effort }}
                  state={{ loading: false, error: "", models: [] }}
                  selectedProvider={draft.provider} selectedModel={draft.model} selectedVariant={draft.effort}
                  embedded hideEngine compactSummary hideHandoff engineOptions={[]} selectedEngine="wuu" engineLocked running={locked}
                  onSelectEngine={() => {}}
                  onSelectModel={(providerName, modelID, effort) => {
                    const nextProvider = providers.find(item => item.name === providerName);
                    update({ provider: providerName, model: modelID, effort: effort ?? modelEffort(nextProvider, modelID) });
                  }}
                  onSelectEffort={(effort) => update({ effort })}
                />
              </fieldset>
              {!provider || !model || !efforts.includes(draft.effort) ? <p className="agent-onboarding-error" role="status">{t("agentOnboarding.selectionUnavailable")}</p> : null}
            </> : null}

            <footer className="agent-onboarding-footer">
              {onManageProviders ? <button type="button" className={providers.length ? "agent-onboarding-manage" : "agent-onboarding-submit"} data-action="manage-providers" onClick={() => { if (!pendingRef.current) onManageProviders(); }} disabled={locked}>{t(providers.length ? "agentOnboarding.manageProviders" : "agentOnboarding.addProvider")}</button> : null}
              {providers.length > 0 && step === "model" ? <button type="submit" className="agent-onboarding-submit" data-action="confirm-model" disabled={locked || !canCreate}>{t("agentOnboarding.useModel")} <ArrowRight size={14} /></button> : null}
            </footer>
          </form>
        </MessageBubble>
      </MessageBubbleRow> : null}
      </div> : <AgentOnboardingHistory onboarding={{
        model_prompt: t("agentOnboarding.selectBeforeChat"), name_prompt: t("agentOnboarding.askName"),
        name: sentName, provider: draft.provider, model: model?.display_name || draft.model,
        effort: draft.effort, avatar_key: draft.avatarKey,
      }} />}
      {error ? <div className="agent-onboarding-error" role="alert">{draft.createdAgent ? <strong>{t("agentOnboarding.openFailed")}</strong> : null}<span>{error}</span><button type="button" className="agent-onboarding-manage" data-action="submit" onClick={() => void submit()} disabled={Boolean(busy)}>{t("agentOnboarding.openConversation")}</button></div> : null}
    </div>
    {step === "name" ? <div className="channel-conversation-footer">
      <div className="agent-onboarding-name-actions">
        <button type="button" className="agent-onboarding-manage" data-action="edit-model" disabled={locked} onClick={() => update({ step: "model" })}>{t("slash.model.title")}</button>
        <button type="button" className="agent-onboarding-manage" data-action="random-name" disabled={locked} onClick={() => {
          const names = t("agentOnboarding.randomNames").split("|").filter(name => name !== draft.name);
          const value = crypto.getRandomValues(new Uint32Array(1))[0];
          update({ name: names[value % names.length] });
          composerRef.current?.focus();
        }}><Shuffle size={14} />{t("agentOnboarding.randomName")}</button>
      </div>
      <ChannelComposer allowAttachments={false} ref={composerRef} draft={draft.name} placeholder={t("agentOnboarding.namePlaceholder")} compact disabled={locked} sending={Boolean(busy)} files={[]} images={[]} onPasteAttachmentFiles={() => {}} onRemoveFile={() => {}} onRemoveImage={() => {}} onChangeDraft={(name) => update({ name })} onSend={() => void submit()} />
    </div> : null}
  </section>;
}
