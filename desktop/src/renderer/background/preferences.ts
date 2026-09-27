export const backgroundEffects = ["none", "dither", "halftone", "ascii", "scanlines"] as const;
export type BackgroundEffect = typeof backgroundEffects[number];
export interface BackgroundPreferences {
  image: Blob;
  imageID: string;
  name: string;
  effect: BackgroundEffect;
  opacity: number;
}

export function normalizeBackground(value: unknown): BackgroundPreferences | null {
  if (!value || typeof value !== "object") return null;
  const record = value as Partial<BackgroundPreferences>;
  if (!(record.image instanceof Blob) || !record.image.size || typeof record.imageID !== "string") return null;
  return {
    image: record.image,
    imageID: record.imageID,
    name: typeof record.name === "string" ? record.name.slice(0, 240) : "",
    effect: backgroundEffects.includes(record.effect as BackgroundEffect) ? record.effect! : "none",
    opacity: typeof record.opacity === "number" && Number.isFinite(record.opacity)
      ? Math.max(0.05, Math.min(0.3, record.opacity)) : 0.15,
  };
}

const eventName = "wuu-background-change";
// Blobs stay in IndexedDB, not quota-limited localStorage or remote settings.
function transaction<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore, result: (value: T) => void) => void): Promise<T> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open("wuu-background", 1);
    request.onupgradeneeded = () => request.result.createObjectStore("background");
    request.onerror = () => reject(request.error);
    request.onsuccess = () => {
      const db = request.result;
      const tx = db.transaction("background", mode);
      let result: T;
      tx.oncomplete = () => { db.close(); resolve(result); };
      tx.onabort = () => { db.close(); reject(tx.error ?? new Error("Background storage failed")); };
      try { run(tx.objectStore("background"), value => { result = value; }); }
      catch (error) { tx.abort(); reject(error); }
    };
  });
}

export function readBackground(): Promise<BackgroundPreferences | null> {
  return transaction("readonly", (store, result) => {
    store.get("current").onsuccess = event => result(normalizeBackground((event.target as IDBRequest).result));
  });
}

export async function updateBackground(update: (current: BackgroundPreferences | null) => BackgroundPreferences | null): Promise<void> {
  await transaction<void>("readwrite", (store, result) => {
    store.get("current").onsuccess = event => {
      const next = update(normalizeBackground((event.target as IDBRequest).result));
      if (next) store.put(normalizeBackground(next), "current");
      else store.delete("current");
      result();
    };
  });
  // Notify only after commit; a failed replacement must retain the previous image.
  window.dispatchEvent(new Event(eventName));
  if (typeof BroadcastChannel !== "undefined") {
    const channel = new BroadcastChannel(eventName);
    channel.postMessage(null);
    channel.close();
  }
}

export function observeBackground(callback: () => void): () => void {
  const channel = typeof BroadcastChannel !== "undefined" ? new BroadcastChannel(eventName) : null;
  if (channel) channel.onmessage = callback;
  window.addEventListener(eventName, callback);
  window.addEventListener("focus", callback);
  return () => {
    channel?.close();
    window.removeEventListener(eventName, callback);
    window.removeEventListener("focus", callback);
  };
}
