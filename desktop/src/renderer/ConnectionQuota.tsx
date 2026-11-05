import { createContext, useContext, useEffect, useSyncExternalStore, type ReactNode } from "react";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import type { EngineListResult } from "../shared/protocol";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useSettingsControls } from "./SettingsControlsContext";

const FRESH_FOR_MS = 60_000;
type QuotaState = { snapshot?: EngineListResult; loading: boolean; failed: boolean };

function createQuotaStore(controls: ApplicationSettingsControlsV1) {
  let state: QuotaState = { loading: false, failed: false };
  let checkedAt = 0;
  let request: Promise<EngineListResult> | undefined;
  const listeners = new Set<() => void>();
  const publish = (next: QuotaState) => { state = next; listeners.forEach(listener => listener()); };
  const load = (force = false): Promise<EngineListResult | undefined> => {
    if (!force) {
      if (request) return request;
      if (Date.now() - checkedAt < FRESH_FOR_MS) return Promise.resolve(state.snapshot);
    }
    publish({ ...state, loading: true });
    const pending = controls.listEngines({ include_quota: true, include_activity: false });
    request = pending;
    void pending.then(snapshot => {
      if (request !== pending) return;
      checkedAt = Date.now();
      request = undefined;
      publish({ snapshot, loading: false, failed: false });
    }, cause => {
      if (request !== pending) return;
      request = undefined;
      // Back off automatic retries, while keeping manual retry available.
      checkedAt = isPluginLifecycleCancellation(cause) ? 0 : Date.now();
      publish({ ...state, loading: false, failed: !isPluginLifecycleCancellation(cause) });
    });
    return pending;
  };
  return {
    getSnapshot: () => state,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    load,
    refresh: () => load(true),
    invalidate: () => { checkedAt = 0; request = undefined; publish({ loading: false, failed: false }); },
  };
}

type QuotaStore = ReturnType<typeof createQuotaStore>;
// The selected controls own the authority; the key fences runtime and account
// changes. Keep only the current context, without persisting account data.
const stores = new WeakMap<ApplicationSettingsControlsV1, { key: string; store: QuotaStore }>();
function quotaStore(controls: ApplicationSettingsControlsV1, key: string): QuotaStore {
  const cached = stores.get(controls);
  if (cached?.key === key) return cached.store;
  const store = createQuotaStore(controls);
  stores.set(controls, { key, store });
  return store;
}
const ConnectionQuotaContext = createContext<QuotaStore | null>(null);

export function ConnectionQuotaProvider({ cacheKey, children }: { cacheKey: string; children: ReactNode }): JSX.Element {
  const controls = useSettingsControls();
  return <ConnectionQuotaContext.Provider value={quotaStore(controls, cacheKey)}>{children}</ConnectionQuotaContext.Provider>;
}

export function useConnectionQuota(enabled = true) {
  const controls = useSettingsControls();
  const inherited = useContext(ConnectionQuotaContext);
  const store = inherited ?? quotaStore(controls, "");
  const state = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
  useEffect(() => {
    if (enabled) void store.load().catch(() => { /* The shared snapshot exposes refresh failures. */ });
  }, [store, enabled]);
  return { ...state, refresh: store.refresh, invalidate: store.invalidate };
}
