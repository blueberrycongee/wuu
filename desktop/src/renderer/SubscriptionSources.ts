import { connectionDisplayPreferences as display } from "./ConnectionDisplayPreferences";
import type {
  EngineInfo,
  EngineListResult,
  ProviderSummary,
  SubscriptionQuota,
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
  catalogFailed: boolean;
  models: { id: string; label: string }[];
  selectedModel: string;
  quota?: SubscriptionQuota;
  engine?: EngineInfo;
  provider?: ProviderSummary;
};

export function subscriptionSources(
  inventory: EngineListResult | undefined,
  providers: readonly ProviderSummary[] | undefined,
  engineID?: string,
): SubscriptionSource[] {
  const engines = (inventory?.engines ?? [])
    .filter((engine) => engine.id !== BUILTIN_ENGINE && (engine.binary_ok || engine.latest_request || engine.quota || engine.id === engineID))
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
    label: display.label(`engine:${engine.id}`, engine.display_name?.trim() || engine.id),
    login: engineLogin(engine),
    catalogFailed: !!(engine.models_error || engine.error),
    models: display.visibleModels(`engine:${engine.id}`, models, [selected]),
    selectedModel: selected,
    quota: engine.quota,
    engine,
  };
}

function engineLogin(engine: EngineInfo): SubscriptionLogin {
  if (!engine.binary_ok || !engine.enabled) return "unavailable";
  // Model catalogs can be static or available before provider authentication.
  // Only a current account observation establishes an authenticated account.
  if (engine.quota?.status === "available" && engine.quota.account
    && (!engine.quota.expires_at || Date.parse(engine.quota.expires_at) > Date.now())) return "ready";
  // Catalog failures can be cancellations or transport errors, not proof that
  // credentials are missing. Authentication remains an explicit user action.
  return "unknown";
}

function providerSource(provider: ProviderSummary): SubscriptionSource {
  const models = uniqueModels(provider);
  return {
    key: `provider:${provider.name}`,
    kind: "builtin",
    id: provider.name,
    label: display.label(`provider:${provider.name}`, provider.name),
    login: provider.api_key_configured ? "ready" : "sign_in",
    catalogFailed: false,
    models: display.visibleModels(`provider:${provider.name}`, models, [provider.model]),
    selectedModel: provider.model,
    quota: provider.quota,
    provider,
  };
}

function uniqueModels(provider: ProviderSummary): { id: string; label: string }[] {
  const seen = new Set<string>();
  const models: { id: string; label: string }[] = [];
  const catalogIDs = (provider.models ?? []).map(model => model.id);
  const ids = catalogIDs.includes(provider.model) ? catalogIDs : [provider.model, ...catalogIDs];
  for (const id of ids) {
    const trimmed = id.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    const declared = provider.models?.find((model) => model.id === trimmed);
    models.push({ id: trimmed, label: declared?.display_name?.trim() || trimmed });
  }
  return models;
}

function isBuiltInSubscription(provider: ProviderSummary): boolean {
  return provider.quota !== undefined || hasBuiltInSubscriptionControls(provider);
}

export function hasBuiltInSubscriptionControls(provider: ProviderSummary): boolean {
  return isCodexSubscription(provider.type) || provider.reuse_codex_credentials === true || isXAI(provider.type) || isGrokBuild(provider.type);
}

function isXAI(type: string | undefined): boolean {
  const normalized = normalize(type);
  return normalized === "xai-subscription" || normalized === "xai-oauth" || normalized === "grok-subscription" || normalized === "supergrok";
}

export function isGrokBuild(type: string | undefined): boolean {
  const normalized = normalize(type);
  return normalized === "grok-build" || normalized === "xai-grok-build" || normalized === "grok-cli";
}

function normalize(type: string | undefined): string {
  return (type ?? "").trim().toLowerCase().replaceAll("_", "-");
}

export function isCodexSubscription(type: string | undefined): boolean {
  return ["openai-codex", "codex-subscription", "chatgpt-codex"].includes(normalize(type));
}
