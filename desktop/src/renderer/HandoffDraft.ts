import type { RuntimePanelView } from "./ComposerRuntimeMenus";

export type HandoffTargetStatus = "pending" | "resolved" | "unverified" | "unavailable";

export type HandoffDraft = {
  raw: string;
  revision: number;
  providerId: string;
  modelId: string;
  variant: string;
  intent: string;
  status: HandoffTargetStatus;
  targetLabel: string;
  filterQuery: string;
  pickerView: RuntimePanelView;
};

export type HandoffCatalog = {
  providers: Array<{
    id: string;
    label: string;
    models: Array<{ id: string; label: string }>;
    allowCustomModel?: boolean;
  }>;
};

export function emptyHandoffDraft(revision = 1): HandoffDraft {
  return {
    raw: "",
    revision,
    providerId: "",
    modelId: "",
    variant: "",
    intent: "",
    status: "pending",
    targetLabel: "",
    filterQuery: "",
    pickerView: "summary",
  };
}

export function parseHandoffArgs(raw: string): {
  providerPrefix: string;
  modelPrefix: string;
  intent: string;
  complete: boolean;
  hasSlash: boolean;
  variant: string;
} {
  const text = raw.replace(/^\s+/, "");
  const remainder = stripOptionalModelToken(text);
  const [targetPart, ...intentParts] = remainder.startsWith("-- ")
    ? ["", remainder.slice(3)]
    : splitOnce(remainder, " -- ");
  const intent = intentParts.join(" -- ");
  const { token, rest, complete } = readQuotedOrBare(targetPart);
  if (token.includes(":")) {
    const [provider = "", model = "", variant = ""] = token.split(":");
    return {
      providerPrefix: decodeTargetPart(provider),
      modelPrefix: decodeTargetPart(model),
      variant: decodeTargetPart(variant),
      intent,
      complete: complete && rest.trim() === "",
      hasSlash: true,
    };
  }
  const slash = token.indexOf("/");
  if (slash < 0) {
    return { providerPrefix: token, modelPrefix: "", intent, variant: "", complete: false, hasSlash: false };
  }
  return {
    providerPrefix: token.slice(0, slash),
    modelPrefix: token.slice(slash + 1),
    intent,
    complete: complete && rest.trim() === "",
    hasSlash: true,
    variant: "",
  };
}

export function reduceHandoffDraft(raw: string, catalog: HandoffCatalog, revision: number): HandoffDraft {
  const parsed = parseHandoffArgs(raw);
  const providers = catalog.providers ?? [];
  const matchingProviders = providers.filter((provider) =>
    matchesPrefix(provider.id, parsed.providerPrefix) || matchesPrefix(provider.label, parsed.providerPrefix),
  );
  const provider = matchingProviders.find((item) => item.id.toLowerCase() === parsed.providerPrefix.toLowerCase())
    ?? (matchingProviders.length === 1 ? matchingProviders[0] : undefined);
  let status: HandoffTargetStatus = "pending";
  let providerId = provider?.id ?? "";
  let modelId = "";
  if (provider && parsed.modelPrefix) {
    const exact = provider.models.find((model) => model.id === parsed.modelPrefix)
      ?? provider.models.find((model) => model.id.toLowerCase() === parsed.modelPrefix.toLowerCase());
    if (exact) {
      modelId = exact.id;
      status = "resolved";
    } else if (provider.allowCustomModel) {
      modelId = parsed.modelPrefix;
      status = "unverified";
    } else {
      status = "unavailable";
    }
  }
  const pickerView = handoffPickerView(parsed, providerId, modelId);
  const targetLabel = providerId && modelId
    ? `${provider?.label ?? providerId} / ${modelId}`
    : provider?.label ?? parsed.providerPrefix;
  return {
    raw,
    revision,
    providerId,
    modelId,
    variant: parsed.variant,
    intent: parsed.intent,
    status,
    targetLabel,
    filterQuery: pickerView === "models" ? parsed.modelPrefix : parsed.providerPrefix,
    pickerView,
  };
}

export function canSubmitHandoffDraft(draft: HandoffDraft): boolean {
  return (draft.status === "resolved" || draft.status === "unverified") && Boolean(draft.providerId && draft.modelId);
}

export function handoffPromptFromIntent(intent: string): string {
  const trimmed = intent.trim();
  return trimmed ? `/handoff -- ${trimmed}` : "/handoff";
}

export function handoffPromptFromSelection(providerId: string, modelId: string, intent: string, variant = ""): string {
  const target = [providerId, modelId, variant].map((part) => encodeURIComponent(part).replaceAll("%2F", "/")).join(":");
  return `/handoff ${target} -- ${intent}`;
}

function decodeTargetPart(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function parseRequestedHandoffIntent(payload: string): string | null {
  const trimmed = payload.trim();
  if (!trimmed.startsWith("{")) {
    return null;
  }
  try {
    const parsed = JSON.parse(trimmed) as { awaiting_user_configuration?: unknown; intent?: unknown };
    if (parsed.awaiting_user_configuration !== true) {
      return null;
    }
    return typeof parsed.intent === "string" ? parsed.intent : "";
  } catch {
    return null;
  }
}

function handoffPickerView(
  parsed: { providerPrefix: string; modelPrefix: string; hasSlash: boolean },
  providerId: string,
  modelId: string,
): RuntimePanelView {
  if (modelId || parsed.hasSlash) {
    return "models";
  }
  if (parsed.providerPrefix) {
    return "providers";
  }
  return "summary";
}

function stripOptionalModelToken(text: string): string {
  if (text.toLowerCase() === "model") {
    return "";
  }
  return text.toLowerCase().startsWith("model ") ? text.slice("model ".length).replace(/^\s+/, "") : text;
}

function matchesPrefix(value: string, prefix: string): boolean {
  return Boolean(prefix) && value.toLowerCase().startsWith(prefix.toLowerCase());
}

function splitOnce(value: string, separator: string): string[] {
  const index = value.indexOf(separator);
  if (index < 0) {
    return [value];
  }
  return [value.slice(0, index), value.slice(index + separator.length)];
}

function readQuotedOrBare(value: string): { token: string; rest: string; complete: boolean } {
  const trimmed = value.trim();
  if (!trimmed.startsWith("\"")) {
    const [token, ...rest] = trimmed.split(/\s+/);
    return { token: token ?? "", rest: rest.join(" "), complete: true };
  }
  let escaped = false;
  let token = "";
  for (let index = 1; index < trimmed.length; index += 1) {
    const char = trimmed[index];
    if (escaped) {
      token += char;
      escaped = false;
      continue;
    }
    if (char === "\\") {
      escaped = true;
      continue;
    }
    if (char === "\"") {
      return { token, rest: trimmed.slice(index + 1), complete: true };
    }
    token += char;
  }
  return { token, rest: "", complete: false };
}
