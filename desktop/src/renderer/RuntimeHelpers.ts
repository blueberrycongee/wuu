import type { CodexModelSummary, GitStatusResult, InitializeResult, ProviderModelSummary, ProviderSummary } from "../shared/protocol";
import { translateCurrent as t } from "./i18n";

export function effectiveModelSpeed(speed?: string, defaultSpeed?: string): string | undefined {
  return speed || defaultSpeed;
}

export function providerIsCodex(initialized: InitializeResult, providerName: string): boolean {
  const summary = initialized.providers?.find((provider) => provider.name === providerName);
  const type = (summary?.type ?? providerName).trim().toLowerCase().replaceAll("_", "-");
  return type === "openai-codex" || type === "codex-subscription" || type === "chatgpt-codex";
}

export function isCodexProvider(initialized: InitializeResult): boolean {
  return providerIsCodex(initialized, initialized.provider);
}

export function displayCodexModelName(model?: CodexModelSummary): string {
  return model?.display_name || model?.slug || "GPT";
}

export function shortCodexModelLabel(model: string): string {
  return model.replace(/^gpt-/i, "");
}

export function codexEffortLabel(effort: string): string {
  switch (effort) {
    case "":
      return "Default";
    case "none":
      return "None";
    case "minimal":
      return "Minimal";
    case "low":
      return "Low";
    case "medium":
      return "Medium";
    case "high":
      return "High";
    case "xhigh":
      return "Extra high";
    case "max":
      return "Max";
    case "ultra":
      return "Ultra";
    default:
      return effort;
  }
}

export function effortLabel(effort: string): string {
  return codexEffortLabel(effort);
}

export function variantLabel(variant: string): string {
  return codexEffortLabel(variant);
}

const EFFORT_RANK: Record<string, number> = {
  "": 0,
  none: 1,
  minimal: 2,
  low: 3,
  medium: 4,
  high: 5,
  xhigh: 6,
  max: 7,
  ultra: 8,
};

function effortRank(effort: string): number {
  return EFFORT_RANK[effort] ?? 100;
}

// Keep discrete effort controls in increasing intensity so the leftmost stop
// is the weakest level and Extra high / Max sit on the right.
export function orderedEffortOptions(options: string[]): string[] {
  const unique: string[] = [];
  for (const option of options) {
    if (!unique.includes(option)) unique.push(option);
  }
  return unique.sort((left, right) => {
    const leftRank = effortRank(left);
    const rightRank = effortRank(right);
    if (leftRank !== rightRank) return leftRank - rightRank;
    return left.localeCompare(right);
  });
}

export function providerModelDisplayName(model?: ProviderModelSummary): string {
  return model?.display_name || model?.id || "model";
}

// Resolve the ceiling for the exact provider/model shown in a conversation.
// Workspace advanced settings describe the default runtime and can belong to a
// different model after a conversation switch. Provider summaries already carry
// the backend-budgeted ceilings (including channel clamps), so this helper only
// picks the smaller published input limit when one is present.
export function providerModelContextWindow(
  initialized: InitializeResult | undefined,
  providerName: string | undefined,
  modelID: string | undefined,
): number | undefined {
  const providerKey = providerName?.trim().toLowerCase();
  const modelKey = modelID?.trim().toLowerCase();
  if (!providerKey || !modelKey) {
    return undefined;
  }
  const provider = initialized?.providers?.find(
    (item) => item.name.trim().toLowerCase() === providerKey,
  );
  const model = provider?.models?.find(
    (item) => item.id.trim().toLowerCase() === modelKey,
  );
  const contextWindow = model?.capabilities?.context_window ?? 0;
  const inputLimit = model?.capabilities?.input_limit ?? 0;
  const effectiveWindow =
    inputLimit > 0 && (contextWindow <= 0 || inputLimit < contextWindow)
      ? inputLimit
      : contextWindow;
  return effectiveWindow > 0 ? effectiveWindow : undefined;
}

export function providerModelVariantOptions(
  provider: ProviderSummary | undefined,
  modelID: string,
  _currentVariant: string
): string[] {
  const model = provider?.models?.find((item) => item.id === modelID);
  const variants = (model?.variants ?? []).map((item) => item.id).filter(Boolean);
  const supported = variants.length > 0 ? variants : (model?.supported_efforts ?? []).filter(Boolean);
  const options = ["", ...supported];
  // When the model supports thinking but exposes no adjustable levels,
  // still let the user explicitly turn thinking off via "none" instead
  // of silently locking them to the model's default behavior.
  if (supported.length === 0 && model?.capabilities?.reasoning === true && !options.includes("none")) {
    options.push("none");
  }
  return orderedEffortOptions(options);
}

export function providerModelReasoningMode(
  provider: ProviderSummary | undefined,
  modelID: string
): "off" | "toggle" | "levels" {
  const model = provider?.models?.find((item) => item.id === modelID);
  if (!model) {
    return "off";
  }
  const variants = (model.variants ?? []).map((item) => item.id).filter(Boolean);
  const supported = variants.length > 0 ? variants : (model.supported_efforts ?? []).filter(Boolean);
  if (supported.length > 0) {
    return "levels";
  }
  return model.capabilities?.reasoning === true ? "toggle" : "off";
}

export function providerModelEffortOptions(
  provider: ProviderSummary | undefined,
  modelID: string,
  currentEffort: string
): string[] {
  return providerModelVariantOptions(provider, modelID, currentEffort);
}

export function normalizedEffortForProviderModel(
  currentEffort: string,
  provider: ProviderSummary | undefined,
  modelID: string
): string {
  if (!currentEffort) {
    return "";
  }
  const model = provider?.models?.find((item) => item.id === modelID);
  const supported = model?.supported_efforts ?? [];
  if (supported.length === 0 || supported.includes(currentEffort)) {
    return currentEffort;
  }
  if (model?.default_effort && supported.includes(model.default_effort)) {
    return model.default_effort;
  }
  return "";
}

export function normalizedVariantForProviderModel(
  currentVariant: string,
  provider: ProviderSummary | undefined,
  modelID: string
): string {
  if (!currentVariant) {
    return "";
  }
  const model = provider?.models?.find((item) => item.id === modelID);
  const variants = (model?.variants ?? []).map((item) => item.id).filter(Boolean);
  const supported = variants.length > 0 ? variants : model?.supported_efforts ?? [];
  if (supported.includes(currentVariant) ||
      (supported.length === 0 && model?.capabilities?.reasoning === true && currentVariant === "none")) {
    return currentVariant;
  }
  if (model?.default_variant && supported.includes(model.default_variant)) {
    return model.default_variant;
  }
  if (model?.default_effort && supported.includes(model.default_effort)) {
    return model.default_effort;
  }
  return "";
}

export function codexEffortOptions(model: CodexModelSummary | undefined, currentEffort: string): string[] {
  const defaults = ["low", "medium", "high", "xhigh"];
  const supported = (model?.supported_reasoning?.length ? model.supported_reasoning : defaults).filter(Boolean);
  const options = ["", ...supported];
  if (currentEffort && !options.includes(currentEffort)) {
    options.push(currentEffort);
  }
  return orderedEffortOptions(options);
}

export function normalizedEffortForModel(currentEffort: string, model: CodexModelSummary): string {
  if (currentEffort === "") {
    return "";
  }
  const supported = model.supported_reasoning ?? [];
  if (supported.length === 0 || supported.includes(currentEffort)) {
    return currentEffort;
  }
  if (model.default_reasoning_level && supported.includes(model.default_reasoning_level)) {
    return model.default_reasoning_level;
  }
  return "";
}

export function pullRequestUnavailableReason(gitStatus?: GitStatusResult): string {
  if (!gitStatus?.is_repo) {
    return t("runtime.pr.notRepository");
  }
  if (!gitStatus.gh_available) {
    return t("runtime.pr.githubCliMissing");
  }
  if (gitStatus.detached || !gitStatus.branch) {
    return t("runtime.pr.namedBranchRequired");
  }
  if (gitStatus.default_branch && gitStatus.branch === gitStatus.default_branch) {
    return t("runtime.pr.createFeatureBranch");
  }
  if (gitStatus.dirty_count > 0) {
    return t("runtime.pr.commitChangesFirst");
  }
  return "";
}

export function humanizeBranchTitle(branch: string): string {
  return branch
    .split(/[/-]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toLocaleUpperCase() + part.slice(1))
    .join(" ");
}
