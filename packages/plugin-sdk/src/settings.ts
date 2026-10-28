import type { PluginSettingsAPI, RuntimeHost } from "./index.js";

export interface PluginSettingsSnapshot {
  /** Opaque content token. Compare for equality; never order revisions. */
  readonly revision: string;
  readonly values: Readonly<Record<string, unknown>>;
  /** Configured content revisions for independently committed scopes. */
  readonly scopeRevisions: Readonly<Record<string, string>>;
  /** Local keys saved for the next generation, not effective in this one. */
  readonly restartRequired: readonly string[];
}

export interface PluginSettingsReader extends PluginSettingsAPI {
  readonly current: PluginSettingsSnapshot | null;
  /** Capture once at the start of an operation and pass the immutable value on. */
  capture(): Promise<PluginSettingsSnapshot>;
  /** Read, validate, then atomically publish; a failed read keeps current intact. */
  refresh(): Promise<PluginSettingsSnapshot>;
  /** Notifications occur after successful refreshes, including the first one.
   * Unchanged revisions do not notify. Disposal and lifetime abort remove listeners.
   * Listener failures reject refresh after publication; they do not undo it.
   */
  subscribe(listener: (snapshot: PluginSettingsSnapshot) => void): () => void;
  /** Optional polling for live configuration, scoped to the supplied lifetime.
   * Polls coalesce with capture/refresh. Errors reject without discarding current;
   * abort rejects with the lifetime reason. No watcher is started automatically.
   */
  watch(intervalMs?: number): Promise<void>;
}

/** Require host.settings.list using requireKernelService("host.settings.list").
 * Use the host passed to the current execution for next-operation semantics.
 * Background readers must use the generation lifecycle signal, not a tool signal.
 * Old get/list services and PluginSettingsAPI implementations remain unchanged;
 * this helper requires the additive snapshot metadata and rejects older hosts.
 */
export function createPluginSettings(host: RuntimeHost, options: {
  signal: AbortSignal;
  scope?: "user" | "workspace";
  /** Optional cross-field constraints, run before publishing the full candidate. */
  validate?: (values: Readonly<Record<string, unknown>>) => void;
}): PluginSettingsReader {
  const { signal } = options;
  const listeners = new Set<(snapshot: PluginSettingsSnapshot) => void>();
  let current: PluginSettingsSnapshot | null = null;
  let pending: Promise<PluginSettingsSnapshot> | undefined;
  const retire = () => listeners.clear();
  if (signal.aborted) retire();
  else signal.addEventListener("abort", retire, { once: true });

  const refresh = (): Promise<PluginSettingsSnapshot> => {
    if (signal.aborted) return Promise.reject(signal.reason);
    if (pending) return pending;
    pending = (async () => {
      const result = await host.call("host.settings.list", options.scope ? { scope: options.scope } : {}, { signal });
      signal.throwIfAborted();
      if (!result.revision || !result.scope_revisions || !Array.isArray(result.restart_required)) {
        throw new Error("host.settings.list does not support configuration snapshots");
      }
      const next: PluginSettingsSnapshot = Object.freeze({
        revision: result.revision,
        values: Object.freeze({ ...result.entries }),
        scopeRevisions: Object.freeze({ ...result.scope_revisions }),
        restartRequired: Object.freeze([...result.restart_required]),
      });
      options.validate?.(next.values);
      signal.throwIfAborted();
      if (current?.revision === next.revision) return current;
      current = next;
      const failures: unknown[] = [];
      for (const listener of [...listeners]) {
        if (signal.aborted) break;
        if (!listeners.has(listener)) continue;
        try { listener(next); } catch (error) { failures.push(error); }
      }
      if (failures.length) throw new AggregateError(failures, "Settings were published but a change listener failed");
      return next;
    })().finally(() => { pending = undefined; });
    return pending;
  };
  return {
    get current() { return current; },
    capture: refresh,
    refresh,
    async get(key) { return (await refresh()).values[key]; },
    async getAll() { return { ...(await refresh()).values }; },
    subscribe(listener) {
      signal.throwIfAborted();
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    async watch(intervalMs = 1000) {
      if (!Number.isFinite(intervalMs) || intervalMs < 100 || intervalMs > 60_000) {
        throw new RangeError("settings watch interval must be between 100 and 60000 ms");
      }
      while (true) {
        signal.throwIfAborted();
        await refresh();
        await new Promise<void>((resolve, reject) => {
          const abort = () => { clearTimeout(timer); reject(signal.reason); };
          const timer = setTimeout(() => { signal.removeEventListener("abort", abort); resolve(); }, intervalMs);
          signal.addEventListener("abort", abort, { once: true });
          if (signal.aborted) abort();
        });
      }
    },
  };
}
