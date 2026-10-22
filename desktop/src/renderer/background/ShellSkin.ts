import { useEffect, useState, useSyncExternalStore } from "react";
import type { ExtensionInventoryRecord, ExtensionSkinDescriptor } from "../../shared/protocol";

export type AvailableShellSkin = ExtensionSkinDescriptor & { key: string; pluginId: string; fingerprint: string; pluginName: string };
const selectionKey = "wuu.shell-skin";
const listeners = new Set<() => void>();
let snapshot: { skins: AvailableShellSkin[]; selected: string } = { skins: [], selected: "custom" };

function publish(): void {
  snapshot = { ...snapshot, selected: window.localStorage.getItem(selectionKey) ?? "custom" };
  for (const listener of listeners) listener();
}

export function syncShellSkins(inventory: readonly ExtensionInventoryRecord[] | undefined, safeMode = false): void {
  snapshot = { ...snapshot, skins: safeMode ? [] : (inventory ?? []).flatMap(plugin => {
    if (plugin.kind !== "plugin" || plugin.enabled === false || !["active", "granted"].includes(plugin.state) || !plugin.fingerprint) return [];
    return (plugin.contributions?.skins ?? []).map(skin => ({ ...skin,
      key: `${plugin.provenance.plugin_id || plugin.id}:${skin.id}`,
      pluginId: plugin.id, fingerprint: plugin.fingerprint!, pluginName: plugin.name,
    }));
  }) };
  publish();
}

export function selectShellSkin(key: string): void {
  window.localStorage.setItem(selectionKey, key);
  publish();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  const changed = (event: StorageEvent) => { if (event.key === null || event.key === selectionKey) publish(); };
  window.addEventListener("storage", changed);
  publish();
  return () => { listeners.delete(listener); window.removeEventListener("storage", changed); };
}

export function useShellSkins() {
  return useSyncExternalStore(subscribe, () => snapshot);
}

const images = new Map<string, Promise<string>>();
export function useSkinImage(skin: AvailableShellSkin | undefined, mode: "light" | "dark") {
  const path = skin?.[mode].image;
  const key = skin && path ? `${skin.pluginId}:${skin.fingerprint}:${path}` : "";
  const [result, setResult] = useState({ key: "", url: "", error: false });
  useEffect(() => {
    if (!skin || !path) return;
    let active = true;
    let pending = images.get(key);
    if (!pending) {
      pending = (async () => {
        if (!window.wuu?.loadPluginSkinImage) throw new Error("Skin images are unavailable in this host");
        const loaded = await window.wuu.loadPluginSkinImage({ id: skin.pluginId, fingerprint: skin.fingerprint, path });
        if (loaded.id !== skin.pluginId || loaded.fingerprint !== skin.fingerprint || loaded.path !== path) throw new Error("Skin image identity mismatch");
        const image = new Image(); image.src = loaded.url;
        await image.decode();
        return loaded.url;
      })();
      images.set(key, pending);
      pending.catch(() => images.delete(key));
    }
    void pending.then(url => { if (active) setResult({ key, url, error: false }); }, () => { if (active) setResult({ key, url: "", error: true }); });
    return () => { active = false; };
  }, [key]);
  return result.key === key ? result : { key, url: "", error: false };
}
