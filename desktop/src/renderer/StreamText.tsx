import { useCallback, useSyncExternalStore } from "react";

/**
 * The set of fields on a thread item that can be streamed incrementally.
 * `text` is the visible assistant/user/reasoning body. `arguments` and
 * `result` carry the streamed JSON payload for tool calls.
 */
export type StreamTextField = "text" | "arguments" | "result";

type StreamTextListener = (value: string) => void;
const subscribeToNothing = (): (() => void) => () => {};

export type StreamTextStats = {
  entryCount: number;
  valueEntryCount: number;
  listenerCount: number;
  pendingEntryCount: number;
  totalValueLength: number;
};

interface StreamEntry {
  /** The current accumulated text. */
  value: string;
  /**
   * Deltas received since `value` was last joined. Tool output can arrive as
   * thousands of small chunks; concatenating onto the full string each time
   * copies the whole result on every chunk.
   */
  pendingChunks: string[] | undefined;
  /** Visible text retained across an empty replace until fresh text arrives. */
  displayValue: string | undefined;
  /** The text at the time the stream was first seeded. */
  seed: string;
  /** Whether this entry has an actual stream value, not only listeners. */
  hasValue: boolean;
  /** Latest value waiting to be published to subscribers on the next frame. */
  pendingValue: string | undefined;
  /** Changes whenever the accumulated value is replaced instead of appended. */
  replacementVersion: number;
  /** Subscribers for value changes. */
  valueListeners: Set<StreamTextListener>;
}

const STREAM_FIELD_KEYS = ["text", "arguments", "result"] as const satisfies readonly StreamTextField[];
// Keep high-rate provider deltas from forcing one React commit, Markdown parse,
// layout read, and feather-animation update per token. Ten visual updates per
// second remain fluid for text while leaving the renderer enough time to handle
// input and the rest of the desktop during very fast streams.
export const STREAM_TEXT_NOTIFY_INTERVAL_MS = 100;

/**
 * Append-only text store used by the renderer to coalesce server-streamed
 * deltas. Components read with `useSyncExternalStore`, which gives us
 * correct concurrent-mode tearing semantics and a single subscription per
 * component.
 */
class StreamTextStore {
  private entries = new Map<string, StreamEntry>();
  private dirtyEntries = new Set<StreamEntry>();
  private notifyScheduled = false;
  private lastNotifyAt = 0;
  private nextReplacementVersion = 1;

  key(turnID: string, itemID: string, field: StreamTextField): string {
    return `${turnID}\u0000${itemID}\u0000${field}`;
  }

  has(key: string): boolean {
    return this.entries.get(key)?.hasValue ?? false;
  }

  get(key: string): string {
    const entry = this.entries.get(key);
    if (!entry?.hasValue) {
      return "";
    }
    return entry.displayValue ?? this.materialize(entry);
  }

  seedValue(key: string): string {
    const entry = this.entries.get(key);
    return entry?.hasValue ? entry.seed : "";
  }

  replacementVersion(key: string): number {
    return this.entries.get(key)?.replacementVersion ?? 0;
  }

  /**
   * Seed an entry. Idempotent: subsequent seeds are no-ops so late-arriving
   * `item/started` notifications cannot clobber in-flight deltas.
   */
  seed(key: string, value: string): void {
    const existing = this.entries.get(key);
    if (existing?.hasValue) {
      return;
    }
    if (existing) {
      existing.value = value;
      existing.displayValue = undefined;
      existing.seed = value;
      existing.hasValue = true;
      existing.replacementVersion = this.nextReplacementVersion++;
      this.notifyValue(existing, value);
      return;
    }
    const entry: StreamEntry = {
      value,
      pendingChunks: undefined,
      displayValue: undefined,
      seed: value,
      hasValue: true,
      pendingValue: undefined,
      replacementVersion: this.nextReplacementVersion++,
      valueListeners: new Set()
    };
    this.entries.set(key, entry);
    this.notifyValue(entry, value);
  }

  /** Replace the value at a key. Used by `*\/replace` events and sync. */
  set(key: string, value: string): void {
    this.ensureEntry(key, { hasValue: true });
    const entry = this.entries.get(key);
    if (!entry) {
      return;
    }
    const valueChanged =
      !entry.hasValue ||
      entry.pendingChunks !== undefined ||
      entry.value !== value ||
      entry.displayValue !== undefined;
    entry.hasValue = true;
    entry.displayValue = undefined;
    entry.pendingChunks = undefined;
    if (!valueChanged) {
      return;
    }
    entry.value = value;
    entry.replacementVersion = this.nextReplacementVersion++;
    this.notifyValue(entry, value);
  }

  /**
   * Replace the accumulated stream text. An empty replace after visible text
   * is a retry/reset marker: reset the append base, but keep the old text
   * visible until the replacement stream produces fresh content.
   */
  replace(key: string, value: string): void {
    if (value !== "") {
      this.set(key, value);
      return;
    }
    this.ensureEntry(key, { hasValue: true });
    const entry = this.entries.get(key);
    if (!entry) {
      return;
    }
    const currentVisible = entry.displayValue ?? this.materialize(entry);
    entry.hasValue = true;
    entry.value = "";
    entry.pendingChunks = undefined;
    entry.seed = "";
    entry.replacementVersion = this.nextReplacementVersion++;
    if (currentVisible.length === 0) {
      entry.displayValue = undefined;
      return;
    }
    entry.displayValue = currentVisible;
  }

  /** Concatenate a delta. Used by `*\/delta` events. */
  append(key: string, delta: string): void {
    if (!delta) {
      return;
    }
    this.ensureEntry(key, { hasValue: true });
    const entry = this.entries.get(key);
    if (!entry) {
      return;
    }
    entry.hasValue = true;
    entry.displayValue = undefined;
    (entry.pendingChunks ??= []).push(delta);
    if (entry.valueListeners.size === 0) {
      return;
    }
    // A replacement published earlier in this frame must not hide the chunks
    // that arrived after it.
    entry.pendingValue = undefined;
    this.dirtyEntries.add(entry);
    this.scheduleNotify();
  }

  clearItem(turnID: string, itemID: string): void {
    for (const field of STREAM_FIELD_KEYS) {
      this.clearField(turnID, itemID, field);
    }
  }

  clearField(turnID: string, itemID: string, field: StreamTextField): void {
    this.deleteEntry(this.key(turnID, itemID, field));
  }

  clearTurn(turnID: string): void {
    const prefix = `${turnID}\u0000`;
    for (const key of Array.from(this.entries.keys())) {
      if (key.startsWith(prefix)) {
        this.deleteEntry(key);
      }
    }
  }

  stats(): StreamTextStats {
    let valueEntryCount = 0;
    let listenerCount = 0;
    let pendingEntryCount = 0;
    let totalValueLength = 0;
    for (const entry of this.entries.values()) {
      if (entry.hasValue) {
        valueEntryCount += 1;
        let chunkLength = 0;
        if (entry.pendingChunks) {
          for (const chunk of entry.pendingChunks) {
            chunkLength += chunk.length;
          }
        }
        totalValueLength += Math.max(
          entry.value.length + chunkLength,
          entry.displayValue?.length ?? 0,
        );
      }
      listenerCount += entry.valueListeners.size;
      if (
        entry.pendingValue !== undefined ||
        (entry.pendingChunks !== undefined && entry.pendingChunks.length > 0)
      ) {
        pendingEntryCount += 1;
      }
    }
    return {
      entryCount: this.entries.size,
      valueEntryCount,
      listenerCount,
      pendingEntryCount,
      totalValueLength,
    };
  }

  /**
   * Subscribe to value updates. Returns an unsubscribe function. Uses the
   * `useSyncExternalStore` snapshot/getServerState convention: we always
   * return the latest cached value, and React decides when to re-render.
   */
  subscribe(key: string, listener: StreamTextListener): () => void {
    this.ensureEntry(key);
    const entry = this.entries.get(key);
    if (!entry) {
      return () => undefined;
    }
    entry.valueListeners.add(listener);
    return () => {
      entry.valueListeners.delete(listener);
      this.deleteEntryIfIdle(key, entry);
    };
  }

  private ensureEntry(key: string, options?: { hasValue?: boolean }): void {
    const hasValue = options?.hasValue ?? false;
    if (this.entries.has(key)) {
      return;
    }
    this.entries.set(key, {
      value: "",
      pendingChunks: undefined,
      displayValue: undefined,
      seed: "",
      hasValue,
      pendingValue: undefined,
      replacementVersion: 0,
      valueListeners: new Set()
    });
  }

  /** Join queued deltas once. Repeated reads return the same string. */
  private materialize(entry: StreamEntry): string {
    const chunks = entry.pendingChunks;
    if (!chunks || chunks.length === 0) {
      return entry.value;
    }
    entry.pendingChunks = undefined;
    entry.value = chunks.length === 1 && entry.value.length === 0
      ? chunks[0]
      : entry.value + chunks.join("");
    return entry.value;
  }

  private notifyValue(entry: StreamEntry, value: string): void {
    if (entry.valueListeners.size === 0) {
      return;
    }
    entry.pendingValue = value;
    this.dirtyEntries.add(entry);
    this.scheduleNotify();
  }

  private scheduleNotify(): void {
    if (this.notifyScheduled) {
      return;
    }
    this.notifyScheduled = true;
    const flush = (): void => {
      this.notifyScheduled = false;
      this.lastNotifyAt = performance.now();
      this.flushNotifications();
    };
    const sinceLast = performance.now() - this.lastNotifyAt;
    if (
      sinceLast >= STREAM_TEXT_NOTIFY_INTERVAL_MS &&
      typeof window !== "undefined" &&
      typeof window.requestAnimationFrame === "function"
    ) {
      window.requestAnimationFrame(flush);
      return;
    }
    globalThis.setTimeout(
      flush,
      Math.max(0, STREAM_TEXT_NOTIFY_INTERVAL_MS - sinceLast),
    );
  }

  private flushNotifications(): void {
    const entries = Array.from(this.dirtyEntries);
    this.dirtyEntries.clear();
    for (const entry of entries) {
      const value = this.materialize(entry);
      entry.pendingValue = undefined;
      if (!entry.hasValue) {
        continue;
      }
      if (entry.valueListeners.size === 0) {
        continue;
      }
      this.notifyListeners(entry, value);
    }
  }

  private notifyListeners(entry: StreamEntry, value: string): void {
    for (const listener of entry.valueListeners) {
      listener(value);
    }
  }

  private deleteEntry(key: string): void {
    const entry = this.entries.get(key);
    if (!entry) {
      return;
    }
    this.dirtyEntries.delete(entry);
    entry.valueListeners.clear();
    this.entries.delete(key);
  }

  private deleteEntryIfIdle(key: string, entry: StreamEntry): void {
    if (entry.hasValue || entry.valueListeners.size > 0) {
      return;
    }
    this.dirtyEntries.delete(entry);
    this.entries.delete(key);
  }
}

export const streamTextStore = new StreamTextStore();

export function streamTextKey(turnID: string, itemID: string, field: StreamTextField): string {
  return streamTextStore.key(turnID, itemID, field);
}

/**
 * Hook that subscribes to a stream key. Returns the latest value or
 * `initialValue` if nothing has been written yet.
 */
export function useStreamedText(
  streamKey: string,
  initialValue = "",
  enabled = true,
): string {
  const subscribe = useCallback(
    (listener: StreamTextListener) => streamTextStore.subscribe(streamKey, listener),
    [streamKey],
  );
  const getSnapshot = useCallback(
    () =>
      enabled && streamTextStore.has(streamKey)
        ? streamTextStore.get(streamKey)
        : initialValue,
    [enabled, initialValue, streamKey],
  );
  return useSyncExternalStore(
    enabled ? subscribe : subscribeToNothing,
    getSnapshot,
    getSnapshot,
  );
}

export function useStreamedTextHasValue(
  streamKey: string,
  enabled = true,
): boolean {
  const subscribe = useCallback(
    (listener: StreamTextListener) => streamTextStore.subscribe(streamKey, listener),
    [streamKey],
  );
  const getSnapshot = useCallback(
    () => enabled && streamTextStore.has(streamKey),
    [enabled, streamKey],
  );
  return useSyncExternalStore(
    enabled ? subscribe : subscribeToNothing,
    getSnapshot,
    getSnapshot,
  );
}
