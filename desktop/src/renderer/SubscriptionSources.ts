import type {
  EngineInfo,
  EngineLatestRequest,
  EngineListResult,
  ProviderSummary,
  SubscriptionQuota,
  SubscriptionUsage,
} from "../shared/protocol";
import { readDraftEngineMemory, writeDraftEngineMemory } from "./DraftEngineMemory";

const BUILTIN_ENGINE = "wuu";

export type SubscriptionLogin = "ready" | "sign_in" | "unavailable" | "unknown";

export type SubscriptionSourceKind = "engine" | "builtin";

export type SubscriptionSource = {
  key: string;
  kind: SubscriptionSourceKind;
  id: string;
  label: string;
  login: SubscriptionLogin;
  detail: string;
  models: { id: string; label: string }[];
  selectedModel: string;
  latest?: EngineLatestRequest;
  quota?: SubscriptionQuota;
  localUsage?: SubscriptionUsage;
  engine?: EngineInfo;
  provider?: ProviderSummary;
};

export function subscriptionSources(
  inventory: EngineListResult | undefined,
  providers: readonly ProviderSummary[] | undefined,
): SubscriptionSource[] {
  const engines = (inventory?.engines ?? [])
    .filter((engine) => engine.id !== BUILTIN_ENGINE && (engine.binary_ok || engine.latest_request))
    .map((engine) => engineSource(engine));
  const builtin = (providers ?? [])
    .filter(isBuiltInSubscription)
    .map((provider) => providerSource(provider));
  return [...engines, ...builtin];
}

export function selectSubscriptionModel(source: SubscriptionSource, modelID: string): void {
  const model = modelID.trim();
  if (!model || source.kind !== "engine" || source.id === BUILTIN_ENGINE) return;
  const remembered = readDraftEngineMemory();
  const effort = remembered?.engine === source.id ? remembered.effort : "";
  writeDraftEngineMemory({ engine: source.id, model, effort });
}

function engineSource(engine: EngineInfo): SubscriptionSource {
  const models = (engine.models ?? []).map((model) => ({
    id: model.id,
    label: model.display_name?.trim() || model.id,
  }));
  const remembered = readDraftEngineMemory();
  const rememberedModel = remembered?.engine === engine.id ? remembered.model : "";
  const declaredDefault = engine.models?.find((model) => model.is_default)?.id ?? "";
  const selected = models.some((model) => model.id === rememberedModel)
    ? rememberedModel
    : models.some((model) => model.id === declaredDefault)
      ? declaredDefault
      : "";
  return {
    key: `engine:${engine.id}`,
    kind: "engine",
    id: engine.id,
    label: engine.display_name?.trim() || engine.id,
    login: engineLogin(engine),
    detail: engine.models_error || engine.error || "",
    models,
    selectedModel: selected,
    latest: engine.latest_request,
    quota: engine.quota,
    localUsage: engine.local_usage,
    engine,
  };
}

function engineLogin(engine: EngineInfo): SubscriptionLogin {
  if (!engine.binary_ok || !engine.enabled) return "unavailable";
  // Only an ACP catalog was read from an accepted session. Static CLI model
  // lists and Codex model/list do not establish an authenticated account.
  if (engine.protocol === "acp" && (engine.models ?? []).length > 0) return "ready";
  if (engine.models_error) return engine.protocol === "acp" ? "sign_in" : "unknown";
  return "unknown";
}

function providerSource(provider: ProviderSummary): SubscriptionSource {
  const models = uniqueModels(provider);
  return {
    key: `provider:${provider.name}`,
    kind: "builtin",
    id: provider.name,
    label: provider.name,
    login: provider.api_key_configured ? "ready" : "sign_in",
    detail: "",
    models,
    selectedModel: provider.model,
    latest: provider.latest_request,
    localUsage: provider.local_usage,
    provider,
  };
}

function uniqueModels(provider: ProviderSummary): { id: string; label: string }[] {
  const seen = new Set<string>();
  const models: { id: string; label: string }[] = [];
  for (const id of [provider.model, ...(provider.models ?? []).map((model) => model.id)]) {
    const trimmed = id.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    const declared = provider.models?.find((model) => model.id === trimmed);
    models.push({ id: trimmed, label: declared?.display_name?.trim() || trimmed });
  }
  return models;
}

function isBuiltInSubscription(provider: ProviderSummary): boolean {
  return provider.reuse_codex_credentials === true || isXAI(provider.type) || isGrokBuild(provider.type);
}

function isXAI(type: string | undefined): boolean {
  const normalized = normalize(type);
  return normalized === "xai-subscription" || normalized === "xai-oauth" || normalized === "grok-subscription" || normalized === "supergrok";
}

function isGrokBuild(type: string | undefined): boolean {
  const normalized = normalize(type);
  return normalized === "grok-build" || normalized === "xai-grok-build" || normalized === "grok-cli";
}

function normalize(type: string | undefined): string {
  return (type ?? "").trim().toLowerCase().replaceAll("_", "-");
}
