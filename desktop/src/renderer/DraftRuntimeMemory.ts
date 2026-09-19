import type { InitializeResult, ProviderSummary } from "../shared/protocol";
import { activeThreadForState, type AppState } from "./AppState";
import type { PermissionMode } from "./ComposerTypes";
import { clearRecentSelections, readRecentSelections, rememberRecentSelection } from "./RecentSelectionMemory";
import { normalizedVariantForProviderModel } from "./RuntimeHelpers";

// Wuu provider/model/effort picks from the composer, most recent first. Existing
// conversations keep their own pinned selection; this memory seeds a brand-new
// conversation and restores the model plus effort a provider was last used with,
// so a user who just chose TokenHub / GPT-5.6 / High does not have to repeat that
// trio on the next tab, after a relaunch, or after visiting another provider.
//
// One list per provider/model pair serves both lookups. Older builds stored a
// single object under the same key; that payload still parses as one entry.
const DRAFT_RUNTIME_MEMORY_KEY = "wuu.desktop.lastDraftRuntime";

// Last permission mode the user picked in the composer. The composer only
// pins the mode on the target conversation (workspace defaults live in
// Settings), so without this memory every new conversation silently fell
// back to the workspace default and the user had to re-pick the mode per
// session. Kept separate from the provider/model memory because it stays
// valid regardless of which provider catalog is currently offered.
const DRAFT_PERMISSION_MEMORY_KEY = "wuu.desktop.lastDraftPermissionMode";
const DRAFT_APPROVE_FOR_ME_MEMORY_KEY = "wuu.desktop.lastDraftApproveForMe";

export type DraftRuntimeMemory = {
  provider: string;
  model: string;
  effort: string;
};

const PERMISSION_MODES = new Set<PermissionMode>(["standard", "read_only", "unconfined"]);

export function readDraftPermissionMemory(): PermissionMode | undefined {
  try {
    const raw = window.localStorage.getItem(DRAFT_PERMISSION_MEMORY_KEY);
    if (!raw) return undefined;
    const mode = raw.trim();
    return PERMISSION_MODES.has(mode as PermissionMode) ? (mode as PermissionMode) : undefined;
  } catch {
    return undefined;
  }
}

export function writeDraftPermissionMemory(mode: string): void {
  const next = mode.trim();
  if (!PERMISSION_MODES.has(next as PermissionMode)) {
    clearDraftPermissionMemory();
    return;
  }
  try {
    window.localStorage.setItem(DRAFT_PERMISSION_MEMORY_KEY, next);
  } catch {
    // A denied/quota-limited write should not break the selection; the
    // current window still applies it through app state.
  }
}

export function clearDraftPermissionMemory(): void {
  try {
    window.localStorage.removeItem(DRAFT_PERMISSION_MEMORY_KEY);
  } catch {
    // Nothing to recover: the next read falls back to the workspace default.
  }
}

export function readDraftApproveForMeMemory(): boolean | undefined {
  try {
    const raw = window.localStorage.getItem(DRAFT_APPROVE_FOR_ME_MEMORY_KEY);
    if (raw === "1") return true;
    if (raw === "0") return false;
    return undefined;
  } catch {
    return undefined;
  }
}

export function writeDraftApproveForMeMemory(enabled: boolean): void {
  try {
    window.localStorage.setItem(DRAFT_APPROVE_FOR_ME_MEMORY_KEY, enabled ? "1" : "0");
  } catch {
    // A denied write should not break the current-window selection.
  }
}

export function clearDraftApproveForMeMemory(): void {
  try {
    window.localStorage.removeItem(DRAFT_APPROVE_FOR_ME_MEMORY_KEY);
  } catch {
    // The next read falls back to the workspace default.
  }
}

export function readDraftRuntimeMemory(): DraftRuntimeMemory | undefined {
  return recentDraftRuntimeSelections()[0];
}

export function writeDraftRuntimeMemory(memory: DraftRuntimeMemory): void {
  const provider = memory.provider.trim();
  const model = memory.model.trim();
  if (!provider || !model) {
    clearDraftRuntimeMemory();
    return;
  }
  rememberRecentSelection(
    DRAFT_RUNTIME_MEMORY_KEY,
    parseDraftRuntimeMemory,
    draftRuntimeMemoryIdentity,
    { provider, model, effort: memory.effort },
  );
}

export function clearDraftRuntimeMemory(): void {
  clearRecentSelections(DRAFT_RUNTIME_MEMORY_KEY);
}

function recentDraftRuntimeSelections(): DraftRuntimeMemory[] {
  return readRecentSelections(DRAFT_RUNTIME_MEMORY_KEY, parseDraftRuntimeMemory);
}

function parseDraftRuntimeMemory(value: unknown): DraftRuntimeMemory | undefined {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return undefined;
  }
  const record = value as Partial<Record<keyof DraftRuntimeMemory, unknown>>;
  const provider = typeof record.provider === "string" ? record.provider.trim() : "";
  const model = typeof record.model === "string" ? record.model.trim() : "";
  if (!provider || !model) return undefined;
  return {
    provider,
    model,
    effort: typeof record.effort === "string" ? record.effort : "",
  };
}

// Provider/model pairs are remembered independently so an effort tuned for one
// model is still there after the same provider picked a different model.
function draftRuntimeMemoryIdentity(memory: DraftRuntimeMemory): string {
  return JSON.stringify([memory.provider, memory.model]);
}

/**
 * Remembered Wuu selection to seed a new conversation with, validated against
 * the live provider catalog. Returns undefined when nothing is remembered or
 * the remembered provider/model is not currently offered.
 *
 * A stale entry is left in storage so a temporarily missing provider can
 * recover the preference once it is configured again.
 */
export function resolveDraftRuntimeMemory(
  initialized: InitializeResult | undefined,
): DraftRuntimeMemory | undefined {
  const memory = readDraftRuntimeMemory();
  if (!memory || !initialized) return undefined;
  const provider = initialized.providers?.find((item) => item.name === memory.provider);
  if (!provider) return undefined;
  return runtimeWithinCatalog(provider, memory);
}

export function applyDraftRuntimeMemory(
  initialized: InitializeResult,
): InitializeResult {
  const remembered = resolveDraftRuntimeMemory(initialized);
  const rememberedMode = readDraftPermissionMemory();
  const rememberedApproveForMe = readDraftApproveForMeMemory();
  let next = initialized;
  if (remembered) {
    next = {
      ...next,
      provider: remembered.provider,
      model: remembered.model,
      variant: remembered.effort,
      effort: remembered.effort,
    };
  }
  if (rememberedMode && rememberedMode !== next.permissions?.mode) {
    next = {
      ...next,
      permissions: { ...next.permissions, mode: rememberedMode },
    };
  }
  if (rememberedApproveForMe !== undefined && rememberedApproveForMe !== Boolean(next.permissions?.approve_for_me)) {
    next = {
      ...next,
      permissions: { ...next.permissions, approve_for_me: rememberedApproveForMe },
    };
  }
  return next;
}

export function lastEffortForRuntimeModel(provider: string, model: string): string | undefined {
  const remembered = recentDraftRuntimeSelections().find(
    (entry) => entry.provider === provider.trim() && entry.model === model.trim(),
  );
  return remembered?.effort;
}

/**
 * Model the composer last used with this provider. The caller still validates it
 * against the provider's live catalog; a provider with no memory (or a model that
 * disappeared) falls back to the provider's configured model.
 */
export function lastModelForProvider(provider: string): string | undefined {
  const name = provider.trim();
  if (!name) return undefined;
  return recentDraftRuntimeSelections().find((entry) => entry.provider === name)?.model;
}

export function seedDraftRuntimeFromMemory(state: AppState): AppState {
  if (!state.initialized || activeThreadForState(state)) return state;
  const next = applyDraftRuntimeMemory(state.initialized);
  if (
    next.provider === state.initialized.provider
    && next.model === state.initialized.model
    && (next.variant ?? "") === (state.initialized.variant ?? "")
    && (next.effort ?? "") === (state.initialized.effort ?? "")
    && (next.permissions?.mode ?? "") === (state.initialized.permissions?.mode ?? "")
    && Boolean(next.permissions?.approve_for_me) === Boolean(state.initialized.permissions?.approve_for_me)
  ) {
    return state;
  }
  return { ...state, initialized: next };
}

function runtimeWithinCatalog(
  provider: ProviderSummary,
  memory: DraftRuntimeMemory,
): DraftRuntimeMemory | undefined {
  const models = provider.models ?? [];
  // A provider that reports no catalog keeps the remembered values: they were
  // valid when picked and the provider still accepts them.
  if (models.length === 0) {
    if (provider.model && provider.model !== memory.model) return undefined;
    return memory;
  }
  if (!models.some((item) => item.id === memory.model)) return undefined;
  return {
    provider: memory.provider,
    model: memory.model,
    effort: normalizedVariantForProviderModel(memory.effort, provider, memory.model),
  };
}
