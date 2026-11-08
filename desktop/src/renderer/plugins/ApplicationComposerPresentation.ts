import { externalAgentActivityStatusSource } from "../ExternalAgentActivityStore";
import type { ComposerStatusItem, ComposerStatusState } from "../../../../packages/plugin-sdk/src/index";
import type { ApplicationComposerStatusSnapshotV1, ApplicationComposerStatusItemV1, ApplicationComposerPresentationV1, ApplicationComposerPresentationViewV1, ApplicationComposerInlineProviderV1 } from "../../../../packages/plugin-sdk/src/ui-composer-presentation";
import { activeTodoUpdateForThread, activeTurnAcceptsSteering, activeTurnForThread, activeTurnIsAnswerReady, isThreadRunning, latestContextUsageForThread, queryTextsForThread, requestedHandoffIntentForThread, type AppState } from "../AppState";
import { providerModelContextWindow } from "../RuntimeHelpers";
import type { InitializeResult } from "../../shared/protocol";
import { runtimeViewForConversation } from "../SessionRuntimeState";
import { turnTelemetryStore } from "../TurnTelemetryStore";
import type { RegisteredComposerStatusSourceV1 as RegisteredComposerStatusSource, UIContributionRegistryV1 as PluginHost, RegisteredComposerInlineCompletionProviderV1 as RegisteredComposerInlineCompletionProvider } from "../../../../packages/plugin-sdk/src/ui-contributions";

function immutable<T>(value: T): T {
  const result = structuredClone(value);
  const freeze = (item: unknown): void => { if (!item || typeof item !== "object" || Object.isFrozen(item)) return; Object.values(item).forEach(freeze); Object.freeze(item); };
  freeze(result); return result;
}

// Heavy configuration inputs (providers, extension inventory, permissions) are
// structurally shared across AppState updates, so one deep-frozen copy per
// source object is reused by every snapshot.
const frozenBySource = new WeakMap<object, unknown>();

function frozenCopy<T>(value: T): T {
  if (!value || typeof value !== "object") return value;
  const cached = frozenBySource.get(value);
  if (cached !== undefined) return cached as T;
  const copy = immutable(value);
  frozenBySource.set(value, copy);
  return copy;
}

/** Adapts existing owners; neither canonical composer state nor telemetry is duplicated. */
export class ApplicationComposerPresentation {
  private disposed = false;
  private readonly scopes = new Set<() => void>();
  // View snapshots are shared by all scopes so each consumer does not pay the
  // recomputation cost separately on every state notification.
  private readonly views = new Map<string, { state: AppState; key: string; heavy: readonly unknown[]; value: ApplicationComposerPresentationViewV1 }>();
  private skillRevisionCache: { inventory: unknown; count: unknown; epoch: number; value: string } | undefined;
  // The runtime projection is identity-stable across unrelated state updates,
  // so the published configuration keeps one frozen wrapper per projection and
  // consumers can memoize heavy clones (structuredClone, effect deps) on it.
  private readonly configurationCache = new WeakMap<InitializeResult, NonNullable<ApplicationComposerPresentationViewV1["configuration"]>>();
  constructor(private readonly host: { getState(): AppState; updateState(update: (state: AppState) => AppState): void; subscribeState(listener: () => void): () => void; getPluginHost(): PluginHost }) {}

  createScope(signal: AbortSignal): ApplicationComposerPresentationV1 {
    if (this.disposed) throw new Error("Composer presentation is disposed");
    signal.throwIfAborted(); let active = true;
    const subscriptions = new Set<() => void>();
    const statusViews = new Map<string | undefined, { key: string; value: ApplicationComposerStatusSnapshotV1 }>();
    const statusSources = () => [...this.host.getPluginHost().getComposerStatusSources(), externalAgentActivityStatusSource];
    const inlineCache = new WeakMap<readonly RegisteredComposerInlineCompletionProvider[], readonly ApplicationComposerInlineProviderV1[]>();
    const telemetryCache = new WeakMap<ReturnType<typeof turnTelemetryStore.getSnapshot>, ReturnType<ApplicationComposerPresentationV1["getTelemetry"]>>();
    let commandSource: ReturnType<PluginHost["getCommands"]> | undefined;
    let commands: ReturnType<ApplicationComposerPresentationV1["getCommands"]> = Object.freeze([]);
    const check = () => { signal.throwIfAborted(); if (!active || this.disposed) throw new Error("Composer presentation generation is retired"); };
    const listen = (start: (listener: () => void) => () => void, callback: () => void): (() => void) => {
      check(); const stop = start(() => { if (active && !this.disposed && !signal.aborted) callback(); });
      const off = () => { if (subscriptions.delete(off)) stop(); }; subscriptions.add(off); return off;
    };
    const retire = () => { if (!active) return; active = false; for (const off of [...subscriptions]) off(); statusViews.clear(); signal.removeEventListener("abort", retire); this.scopes.delete(retire); };
    this.scopes.add(retire); signal.addEventListener("abort", retire, { once: true });
    const owner = (ownerTabId: string) => {
      check(); const state = this.host.getState(); const tab = state.sessionTabs.find(tab => tab.id === ownerTabId && tab.kind !== "skills");
      if (!tab || tab.kind === "skills") throw new Error("Composer owner is no longer available");
      const thread = tab.kind === "thread" ? [state.thread, state.secondaryThread, ...state.threads].find(thread => thread?.id === tab.threadID) : undefined;
      return { state, tab, thread };
    };
    return Object.freeze({
      setRuntimeMenu: (ownerTabId: string, menu: "model" | null) => {
        const { state } = owner(ownerTabId);
        if (state.activeSessionTabID !== ownerTabId) throw new Error("Composer owner is no longer active");
        this.host.updateState(current => current.codexRuntimeMenu === menu ? current : { ...current, codexRuntimeMenu: menu });
      },
      getStatusSnapshot: (threadId?: string) => {
        check(); const context = Object.freeze({ threadId, mainConversation: true as const });
        const items: ApplicationComposerStatusItemV1[] = []; const seen = new Set<string>();
        for (const source of statusSources()) {
          let rows: readonly ComposerStatusItem[];
          try { rows = source.getSnapshot(context); } catch { continue; }
          if (!Array.isArray(rows)) continue;
          for (const row of rows) {
            const item = normalizeStatusItem(source, row);
            if (item && !seen.has(item.key)) { seen.add(item.key); items.push(item); }
          }
        }
        const state = this.host.getState();
        const thread = threadId ? [state.thread, state.secondaryThread, ...state.threads].find(thread => thread?.id === threadId) : undefined;
        const value = { items, todoUpdate: activeTodoUpdateForThread(thread) };
        const key = JSON.stringify(value); const previous = statusViews.get(threadId); if (previous?.key === key) return previous.value;
        const snapshot = immutable(value); statusViews.set(threadId, { key, value: snapshot }); return snapshot;
      },
      subscribeStatus: (threadId, callback) => listen(notify => {
        const context = Object.freeze({ threadId, mainConversation: true as const });
        let retired = false; let sourceCleanups: Array<() => void> = [];
        const clearSources = () => { const cleanups = sourceCleanups; sourceCleanups = []; for (const off of cleanups) off(); };
        const bindSources = () => {
          clearSources();
          for (const source of statusSources()) {
            let subscribed = true;
            try {
              const off = source.subscribe(context, () => { if (subscribed && !retired) notify(); });
              sourceCleanups.push(() => { subscribed = false; try { off(); } catch { /* Isolate untrusted cleanup so other sources are released. */ } });
            } catch { subscribed = false; }
          }
        };
        bindSources();
        const providersOff = this.host.getPluginHost().subscribeComposerStatusSources(() => { if (retired) return; bindSources(); notify(); });
        const stateOff = this.host.subscribeState(notify);
        return () => { retired = true; providersOff(); stateOff(); clearSources(); };
      }, callback),
      getSnapshot: (ownerTabId: string) => {
        check();
        return this.viewForOwner(ownerTabId);
      },
      subscribe: (callback: () => void) => {
        const stateOff = listen(listener => this.host.subscribeState(listener), callback);
        const pluginsOff = listen(listener => this.host.getPluginHost().subscribe(listener), callback);
        return () => { stateOff(); pluginsOff(); };
      },
      getCommands: () => {
        check(); const source = this.host.getPluginHost().getCommands();
        if (source !== commandSource) { commandSource = source; commands = Object.freeze(source.map(command => Object.freeze({ pluginId: command.pluginId, id: command.id }))); }
        return commands;
      },
      executeCommand: async input => {
        const { tab, thread } = owner(input.ownerTabId); const host = this.host.getPluginHost();
        const command = host.getCommands().find(command => command.pluginId === input.pluginId && command.id === input.id);
        if (!command) throw new Error("Composer command is no longer available");
        await command.execute(Object.freeze({ source: "slash-command", commandId: input.id, args: input.args, threadId: thread?.id, context: Object.freeze({ ...tab.context }) })); check();
      },
      getComposerInlineCompletionProviders: () => {
        check(); const host = this.host.getPluginHost(); const source = host.getComposerInlineCompletionProviders(); const previous = inlineCache.get(source); if (previous) return previous;
        const adapters = Object.freeze(source.map(provider => Object.freeze({
          provideCompletion: async (context: Parameters<typeof provider.provideCompletion>[0], requestSignal: AbortSignal) => {
            check(); if (!host.getComposerInlineCompletionProviders().includes(provider)) return null;
            requestSignal.throwIfAborted();
            const request = new AbortController();
            const cancelScope = () => request.abort(signal.reason);
            const cancelRequest = () => request.abort(requestSignal.reason);
            signal.addEventListener("abort", cancelScope, { once: true });
            requestSignal.addEventListener("abort", cancelRequest, { once: true });
            try {
              const result = await provider.provideCompletion(immutable(context), request.signal);
              if (request.signal.aborted) return null;
              check();
              return host.getComposerInlineCompletionProviders().includes(provider) ? result : null;
            } finally {
              signal.removeEventListener("abort", cancelScope);
              requestSignal.removeEventListener("abort", cancelRequest);
            }
          },
          onDidInteract: async (event: Parameters<NonNullable<typeof provider.onDidInteract>>[0]) => { check(); if (host.getComposerInlineCompletionProviders().includes(provider)) await provider.onDidInteract?.(immutable(event)); },
          subscribeRefresh: (callback: Parameters<typeof provider.subscribeRefresh>[0]) => {
            check(); const off = provider.subscribeRefresh(scope => { if (active && !this.disposed && !signal.aborted) callback(scope); });
            const stop = () => { if (subscriptions.delete(stop)) off(); }; subscriptions.add(stop); return stop;
          },
        })));
        inlineCache.set(source, adapters); return adapters;
      },
      subscribeComposerInlineCompletionProviders: callback => listen(listener => this.host.getPluginHost().subscribeComposerInlineCompletionProviders(listener), callback),
      getTelemetry: turnId => {
        check(); const source = turnTelemetryStore.getSnapshot(turnId);
        const previous = telemetryCache.get(source); if (previous) return previous;
        const snapshot = immutable(source); telemetryCache.set(source, snapshot); return snapshot;
      },
      subscribeTelemetry: callback => listen(turnTelemetryStore.subscribe, callback),
    } satisfies ApplicationComposerPresentationV1);
  }
  private viewForOwner(ownerTabId: string): ApplicationComposerPresentationViewV1 {
    const cached = this.views.get(ownerTabId);
    const state = this.host.getState();
    if (cached?.state === state) return cached.value;
    const tab = state.sessionTabs.find(tab => tab.id === ownerTabId && tab.kind !== "skills");
    if (!tab) {
      if (cached?.key === "unavailable") { cached.state = state; return cached.value; }
      const value = immutable({ ownerTabId, runtimeMenu: null, available: false, running: false, turnRunning: false, canSteer: false, readOnly: true,
        status: "", queryHistory: [], skillRevision: "", workspaceCapabilities: { create: false, open: false } });
      this.views.set(ownerTabId, { state, key: "unavailable", heavy: [], value }); return value;
    }
    const thread = tab.kind === "thread" ? [state.thread, state.secondaryThread, ...state.threads].find(thread => thread?.id === tab.threadID) : undefined;
    const initialized = runtimeViewForConversation(state.initialized, thread, activeTurnForThread(thread));
    const turn = activeTurnForThread(thread); const running = Boolean(thread && isThreadRunning(thread));
    const ceiling = providerModelContextWindow(initialized, thread?.model_provider || initialized?.provider, thread?.model || initialized?.model)
      || (!thread ? state.initialized?.advanced_settings?.context_window_tokens : undefined);
    const usage = latestContextUsageForThread(state, thread, { model: thread?.model || initialized?.model, contextWindowTokens: ceiling });
    const configuration = initialized && this.configurationFor(initialized);
    const skillsCount = initialized?.extension_trust?.main_session?.skills?.count;
    const inventory = initialized?.extension_inventory;
    // A committed inventory generation may carry a new skills payload with
    // byte-identical inventory records, so the epoch is part of the key.
    const epoch = state.extensionEpoch ?? 0;
    const revision = this.skillRevisionCache;
    const skillRevision = revision && revision.inventory === inventory && revision.count === skillsCount && revision.epoch === epoch
      ? revision.value
      : (this.skillRevisionCache = { inventory, count: skillsCount, epoch, value: JSON.stringify([skillsCount, epoch, inventory]) }).value;
    // Turn updates replace the turns array, so the cached query list comes back
    // with a new identity carrying the same entries. Reuse the published array
    // while the texts match or unrelated item traffic would rebuild the view.
    const rawQueryHistory = queryTextsForThread(thread);
    const previousQueryHistory = cached?.value.queryHistory;
    const queryHistory = previousQueryHistory
      && previousQueryHistory.length === rawQueryHistory.length
      && previousQueryHistory.every((text, index) => text === rawQueryHistory[index])
      ? previousQueryHistory : rawQueryHistory;
    const value: ApplicationComposerPresentationViewV1 = Object.freeze({ ownerTabId, runtimeMenu: state.codexRuntimeMenu ?? null, threadId: thread?.id, available: Boolean(initialized && (tab.kind === "draft" || thread)),
      running, turnRunning: running && !activeTurnIsAnswerReady(thread), canSteer: activeTurnAcceptsSteering(thread), readOnly: Boolean(thread?.read_only),
      status: state.status, turnId: turn?.id, configuration, contextUsage: immutable(usage && ceiling ? { ...usage, window: ceiling } : usage),
      queryHistory,
      requestedHandoffIntent: requestedHandoffIntentForThread(thread), skillRevision,
      workspaceCapabilities: immutable({ create: !window.wuu.unsupportedMethods?.includes("createBlankProject"), open: !window.wuu.unsupportedMethods?.includes("chooseProjectFolder") }) });
    const { queryHistory: _queryHistory, skillRevision: _skillRevision, configuration: valueConfiguration, ...keyRest } = value;
    const { providers: _providers, extension_inventory: _inventory, permissions: _permissions, ...configurationRest } = valueConfiguration ?? {};
    const key = JSON.stringify({ ...keyRest, configuration: valueConfiguration ? configurationRest : undefined });
    const heavy = [queryHistory, skillRevision, configuration?.providers, configuration?.extension_inventory, configuration?.permissions];
    if (cached && cached.key === key && cached.heavy.length === heavy.length && cached.heavy.every((item, index) => item === heavy[index])) {
      cached.state = state; return cached.value;
    }
    this.views.set(ownerTabId, { state, key, heavy, value }); return value;
  }

  private configurationFor(initialized: InitializeResult): NonNullable<ApplicationComposerPresentationViewV1["configuration"]> {
    const cached = this.configurationCache.get(initialized);
    if (cached) {
      return cached;
    }
    const value = Object.freeze({ provider: initialized.provider, model: initialized.model, effort: initialized.effort, variant: initialized.variant,
      speed: initialized.speed, permissions: frozenCopy(initialized.permissions), providers: frozenCopy(initialized.providers),
      features: immutable({ safe_mode: initialized.features?.safe_mode }),
      extension_inventory: frozenCopy(initialized.extension_inventory) });
    this.configurationCache.set(initialized, value);
    return value;
  }

  dispose(): void { if (this.disposed) return; this.disposed = true; this.views.clear(); for (const retire of [...this.scopes]) retire(); }
}

function normalizeStatusItem(
  source: RegisteredComposerStatusSource,
  item: ComposerStatusItem,
): ApplicationComposerStatusItemV1 | undefined {
  if (!item || typeof item !== "object") return undefined;
  const id = typeof item.id === "string" ? item.id.trim() : "";
  const label = typeof item.label === "string" ? item.label.trim() : "";
  if (!id || !label) return undefined;
  const action = item.action?.kind === "open-session" && typeof item.action.sessionId === "string"
    && item.action.sessionId.trim()
    ? Object.freeze({ kind: "open-session" as const, sessionId: item.action.sessionId.trim() })
    : undefined;
  return Object.freeze({
    key: `${source.pluginId}:${source.generation}:${source.id}:${id}`,
    id,
    label,
    state: normalizeStatusState(item.state),
    secondaryText: cleanOptionalText(item.secondaryText),
    tooltip: cleanOptionalText(item.tooltip),
    updatedAt: cleanOptionalText(item.updatedAt),
    action,
  });
}

function normalizeStatusState(value: unknown): ComposerStatusState | undefined {
  return value === "running" || value === "queued" || value === "waiting"
    || value === "error" || value === "idle"
    ? value
    : undefined;
}

function cleanOptionalText(value: string | undefined): string | undefined {
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}
