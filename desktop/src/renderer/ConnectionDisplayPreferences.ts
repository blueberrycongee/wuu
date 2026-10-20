import { useSyncExternalStore } from "react";

// Presentation only, scoped to this client/origin. Connection IDs remain the
// server's provider name or engine ID; aliases never participate in requests.
const storageKey = "wuu.connectionDisplay.v1";
const changeEvent = "wuu-connection-display-change";
export type ConnectionDisplayPreference = {
  alias?: string;
  modelOrder: string[];
  hiddenModels: string[];
};

function snapshot(): string {
  try { return localStorage.getItem(storageKey) ?? "{}"; }
  catch { return "{}"; }
}

function read(): Record<string, ConnectionDisplayPreference> {
  try {
    const value: unknown = JSON.parse(snapshot());
    if (!value || typeof value !== "object" || Array.isArray(value)) return {};
    return Object.fromEntries(Object.entries(value).filter(([, entry]) => entry && typeof entry === "object").map(([key, entry]) => [key, {
      alias: typeof entry.alias === "string" ? entry.alias : undefined,
      modelOrder: strings(entry.modelOrder),
      hiddenModels: strings(entry.hiddenModels),
    }]));
  } catch { return {}; }
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? [...new Set(value.filter((item): item is string => typeof item === "string" && !!item))] : [];
}

function get(key: string): ConnectionDisplayPreference {
  return read()[key] ?? { modelOrder: [], hiddenModels: [] };
}

function save(key: string, patch: Partial<ConnectionDisplayPreference>): void {
  const current = read();
  const next = { ...get(key), ...patch };
  // A failed write must surface to the settings caller, not claim persistence.
  localStorage.setItem(storageKey, JSON.stringify({ ...current, [key]: next }));
  window.dispatchEvent(new Event(changeEvent));
}

function orderModels<T extends { id: string }>(key: string, models: readonly T[]): T[] {
  const order = new Map(get(key).modelOrder.map((id, index) => [id, index]));
  return [...models].sort((a, b) => (order.get(a.id) ?? Infinity) - (order.get(b.id) ?? Infinity));
}

function visibleModels<T extends { id: string }>(key: string, models: readonly T[], retainedIDs: readonly string[] = []): T[] {
  const hidden = new Set(get(key).hiddenModels);
  return orderModels(key, models).filter(model => !hidden.has(model.id) || retainedIDs.includes(model.id));
}

export const connectionDisplayPreferences = {
  get,
  label: (key: string, fallback: string): string => get(key).alias || fallback,
  orderModels,
  visibleModels,
  setAlias: (key: string, alias: string): void => save(key, { alias: alias.trim() || undefined }),
  setModelOrder: (key: string, ids: readonly string[]): void => save(key, { modelOrder: strings(ids) }),
  moveModel: <T extends { id: string }>(key: string, models: readonly T[], id: string, direction: -1 | 1): void => {
    const ids = orderModels(key, models).map(model => model.id);
    const index = ids.indexOf(id);
    const next = index + direction;
    if (index < 0 || next < 0 || next >= ids.length) return;
    [ids[index], ids[next]] = [ids[next], ids[index]];
    save(key, { modelOrder: ids });
  },
  setModelVisible: (key: string, id: string, visible: boolean): void => {
    const hidden = new Set(get(key).hiddenModels);
    if (visible) hidden.delete(id); else hidden.add(id);
    save(key, { hiddenModels: [...hidden] });
  },
};

function subscribe(callback: () => void): () => void {
  const storage = (event: StorageEvent): void => {
    if (event.key === storageKey || event.key === null) callback();
  };
  window.addEventListener(changeEvent, callback);
  window.addEventListener("storage", storage);
  return () => {
    window.removeEventListener(changeEvent, callback);
    window.removeEventListener("storage", storage);
  };
}

export function useConnectionDisplayPreferences(): typeof connectionDisplayPreferences {
  useSyncExternalStore(subscribe, snapshot, () => "{}");
  return { ...connectionDisplayPreferences };
}
