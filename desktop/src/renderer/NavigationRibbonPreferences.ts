import { RIBBON_COMPACT_WIDTH, RIBBON_MAX_WIDTH } from "../shared/navigationRibbon";
import { useMemo, useSyncExternalStore } from "react";

export interface RibbonWebsite {
  id: string;
  reopenedFrom?: string;
  title: string;
  url: string;
  pinned?: boolean;
  audible?: boolean;
  muted?: boolean;
  loading?: boolean;
  favicon?: string;
  startPage?: boolean;
}

export interface NavigationRibbonPreferences {
  websites: RibbonWebsite[];
  order: string[];
  browserTabsMigrated?: true;
  width?: number;
}

const storageKey = "wuu.navigation-ribbon.v1";
const changeEvent = "wuu-navigation-ribbon-change";

export function normalizeWebsiteURL(input: string): string | null {
  const value = input.trim();
  if (!value || /[\u0000-\u001f\u007f]/.test(value)) return null;
  try {
    const hostWithPort = /^(?:localhost|[^\s/:]+\.[^\s/:]+|\[[\da-f:]+\]):\d+(?:[/#?]|$)/i.test(value);
    const hasScheme = /^[a-z][a-z\d+.-]*:/i.test(value) && !hostWithPort;
    const localHost = /^(?:localhost|127(?:\.\d{1,3}){3}|\[::1\])(?::\d+)?(?:[/#?]|$)/i.test(value);
    const url = new URL(hasScheme ? value : `${localHost ? "http" : "https"}://${value}`);
    if (!["https:", "http:"].includes(url.protocol) || !url.hostname || url.username || url.password) return null;
    return url.href;
  } catch {
    return null;
  }
}

function normalizePreferences(value: unknown): NavigationRibbonPreferences {
  const input = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const websites: RibbonWebsite[] = [];
  const ids = new Set<string>();
  if (Array.isArray(input.websites)) {
    for (const candidate of input.websites) {
      if (!candidate || typeof candidate !== "object") continue;
      const site = candidate as Record<string, unknown>;
      if (typeof site.id !== "string" || !/^website:[a-z\d_-]+$/i.test(site.id) || ids.has(site.id)) continue;
      const url = typeof site.url === "string" ? normalizeWebsiteURL(site.url) : null;
      if (!url) continue;
      const title = typeof site.title === "string" ? site.title.trim().slice(0, 80) : "";
      websites.push({ id: site.id, title: title || new URL(url).hostname, url });
      ids.add(site.id);
    }
  }
  return {
    websites,
    ...(typeof input.width === "number" && Number.isFinite(input.width) ? { width: Math.max(RIBBON_COMPACT_WIDTH, Math.min(RIBBON_MAX_WIDTH, input.width)) } : {}),
    ...(input.browserTabsMigrated === true ? { browserTabsMigrated: true as const } : {}),
    order: Array.isArray(input.order)
      ? [...new Set(input.order.filter((id): id is string => typeof id === "string" && id.length > 0))]
      : [],
  };
}

function readSnapshot(): string | null {
  try { return window.localStorage.getItem(storageKey); }
  catch { return null; }
}

function parseSnapshot(snapshot: string | null): NavigationRibbonPreferences {
  try { return normalizePreferences(JSON.parse(snapshot ?? "null")); }
  catch { return { websites: [], order: [] }; }
}

export function readNavigationRibbonPreferences(): NavigationRibbonPreferences {
  return parseSnapshot(readSnapshot());
}

export function saveNavigationRibbonPreferences(value: NavigationRibbonPreferences): void {
  // Persist before notifying consumers so a failed write never looks saved.
  window.localStorage.setItem(storageKey, JSON.stringify(normalizePreferences(value)));
  window.dispatchEvent(new Event(changeEvent));
}

function subscribe(listener: () => void): () => void {
  const onStorage = (event: StorageEvent) => {
    if (event.key === storageKey || event.key === null) listener();
  };
  window.addEventListener(changeEvent, listener);
  window.addEventListener("storage", onStorage);
  return () => {
    window.removeEventListener(changeEvent, listener);
    window.removeEventListener("storage", onStorage);
  };
}

export function useNavigationRibbonPreferences(): NavigationRibbonPreferences {
  const snapshot = useSyncExternalStore(subscribe, readSnapshot, () => null);
  return useMemo(() => parseSnapshot(snapshot), [snapshot]);
}

/** Reorder visible destinations without discarding temporarily unloaded plugins. */
export function saveRibbonOrder(visibleOrder: string[]): void {
  const preferences = readNavigationRibbonPreferences();
  const visible = new Set(visibleOrder);
  let index = 0;
  const order = preferences.order.map((id) => visible.has(id) ? visibleOrder[index++] : id);
  order.push(...visibleOrder.slice(index));
  saveNavigationRibbonPreferences({ ...preferences, order });
}
