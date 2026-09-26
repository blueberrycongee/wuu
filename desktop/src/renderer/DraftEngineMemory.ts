import type { EngineInfo, EngineListResult } from "../shared/protocol";
import { clearRecentSelections, readRecentSelections, rememberRecentSelection } from "./RecentSelectionMemory";

// Engine/model/effort picks from the composer, most recent first. Engine binding
// is a thread-creation decision, so the draft selection is per-thread state that
// resets constantly. Without a persisted memory a user who works in Codex/Claude
// Code has to re-pick the agent on every new tab and every relaunch, while the
// built-in wuu provider/model choice survives because it lives in the server
// config. One entry per engine/model pair keeps each external engine's model and
// effort apart, so switching agents does not overwrite the other one's choice.
//
// Older builds stored a single object under the same key; that payload still
// parses as one entry.
const DRAFT_ENGINE_MEMORY_KEY = "wuu.desktop.lastDraftEngine";

export type DraftEngineMemory = {
  engine: string;
  // Empty means "use the engine default". The composer already resolves an
  // empty model/effort through the engine catalog default, so a model that
  // disappeared degrades to the default instead of pinning a dead id.
  model: string;
  effort: string;
  speed?: string;
};

export function readDraftEngineMemory(): DraftEngineMemory | undefined {
  return recentDraftEngineSelections()[0];
}

export function writeDraftEngineMemory(memory: DraftEngineMemory): void {
  const engine = memory.engine.trim();
  if (!engine) {
    clearDraftEngineMemory();
    return;
  }
  rememberRecentSelection(
    DRAFT_ENGINE_MEMORY_KEY,
    parseDraftEngineMemory,
    draftEngineMemoryIdentity,
    { engine, model: memory.model, effort: memory.effort, ...(memory.speed === undefined ? {} : { speed: memory.speed }) },
  );
}

export function clearDraftEngineMemory(): void {
  clearRecentSelections(DRAFT_ENGINE_MEMORY_KEY);
}

function recentDraftEngineSelections(): DraftEngineMemory[] {
  return readRecentSelections(DRAFT_ENGINE_MEMORY_KEY, parseDraftEngineMemory);
}

function parseDraftEngineMemory(value: unknown): DraftEngineMemory | undefined {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return undefined;
  }
  const record = value as Partial<Record<keyof DraftEngineMemory, unknown>>;
  const engine = typeof record.engine === "string" ? record.engine.trim() : "";
  if (!engine) return undefined;
  return {
    engine,
    model: typeof record.model === "string" ? record.model : "",
    effort: typeof record.effort === "string" ? record.effort : "",
    ...(typeof record.speed === "string" ? { speed: record.speed } : {}),
  };
}

function draftEngineMemoryIdentity(memory: DraftEngineMemory): string {
  return JSON.stringify([memory.engine, memory.model]);
}

export function lastEffortForEngineModel(engine: string, model: string): string | undefined {
  return recentDraftEngineSelections().find(
    (entry) => entry.engine === engine.trim() && entry.model === model.trim(),
  )?.effort;
}

/**
 * Remembered selection to seed a new conversation with, validated against the
 * live engine inventory. Returns undefined when nothing is remembered or the
 * remembered engine is not currently offered, in which case the caller keeps
 * the normal settings-default fallback.
 *
 * A stale entry is deliberately left in storage: an external CLI that is
 * missing right now (PATH not ready, reinstall in flight) should recover the
 * preference once it is detected again rather than lose it.
 */
export function resolveDraftEngineMemory(
  inventory: EngineListResult | undefined,
): DraftEngineMemory | undefined {
  const memory = readDraftEngineMemory();
  if (!memory) return undefined;
  // An explicit wuu pick overrides an external default engine and needs no
  // detection, so it applies before the inventory arrives.
  if (memory.engine === "wuu") {
    return { engine: "wuu", model: "", effort: "" };
  }
  const engine = engineForMemory(memory.engine, inventory);
  if (!engine) return undefined;
  const runtime = runtimeWithinCatalog(engine, memory.model, memory.effort)
    ?? { model: "", effort: "" };
  return { engine: memory.engine, ...runtime, ...(memory.speed === undefined ? {} : { speed: engine.models?.find(model => model.id === runtime.model)?.fast_mode ? memory.speed : "" }) };
}

/**
 * Model and effort the composer last used with this engine. Used when the user
 * switches back to an engine inside the draft picker: the engine keeps its own
 * child selection instead of falling back to the catalog default. Returns
 * undefined when the engine has no memory or the remembered model is no longer
 * offered, so the caller applies its default selection.
 */
export function rememberedEngineRuntime(
  engineID: string,
  inventory: EngineListResult | undefined,
): { model: string; effort: string; speed?: string } | undefined {
  const id = engineID.trim();
  if (!id || id === "wuu") return undefined;
  const memory = recentDraftEngineSelections().find((entry) => entry.engine === id);
  if (!memory) return undefined;
  const engine = engineForMemory(id, inventory);
  if (!engine) return undefined;
  const runtime = runtimeWithinCatalog(engine, memory.model, memory.effort);
  return runtime ? { ...runtime, ...(memory.speed === undefined ? {} : { speed: engine.models?.find(model => model.id === runtime.model)?.fast_mode ? memory.speed : "" }) } : undefined;
}

function engineForMemory(
  engineID: string,
  inventory: EngineListResult | undefined,
): EngineInfo | undefined {
  if (!engineID || engineID === "wuu" || !inventory) return undefined;
  const engine = inventory.engines.find((item) => item.id === engineID);
  if (!engine?.enabled || !engine.binary_ok) return undefined;
  return engine;
}

// Drop a model/effort the engine no longer reports so the caller's default
// fallback takes over. Efforts are checked against the resolved model because
// the supported set is per-model. An engine that reports no catalog
// (models_error) keeps the remembered values: they were valid when picked and
// the engine still accepts them.
function runtimeWithinCatalog(
  engine: EngineInfo,
  model: string,
  effort: string,
): { model: string; effort: string } | undefined {
  const models = engine.models ?? [];
  if (models.length === 0) {
    return engine.models_error ? { model, effort } : { model: "", effort: "" };
  }
  const rememberedModel = models.find((item) => item.id === model);
  if (!rememberedModel) return undefined;
  return {
    model,
    effort: (rememberedModel.supported_efforts ?? []).includes(effort) ? effort : "",
  };
}
