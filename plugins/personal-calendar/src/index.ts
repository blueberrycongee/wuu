import { AsyncLocalStorage } from "node:async_hooks";
import { ConnectedCalendarReader } from "./connected-calendar.js";
import { callPersonalHost, decodeCalendarConnection } from "./host-bridge.js";
import { createHash, randomUUID } from 'node:crypto';
import { requireKernelService, type RuntimePlugin, type RuntimeHost, type RuntimeInitializeParams, type RuntimeInitializeResult, type RuntimeLifecycleContext, type RuntimeExecutionContext, type ServiceInvokeParams, type CapabilityInvokeParams } from '@wuu/plugin-sdk';
import { CalendarReadError, GoogleCalendarReader, calendarQuery, type CalendarReader, type CalendarQuery, type CalendarSnapshot } from './google-calendar.js';
import { CalendarOperations } from './operations.js';
import { advanceCalendarFeed, calendarChanges, calendarWindowKey, type CalendarFeed } from './changes.js';
import type { CalendarWriter, WriteTarget } from './google-writer.js';
export * from './google-calendar.js';
export * from './google-writer.js';
const service = 'calendar.personal';
const methods = ['status', 'agenda', 'refresh', 'changes', 'clear-cache', 'disconnect', 'operations.propose', 'operations.get', 'operations.list', 'operations.reconcile'];
type State = 'unconnected' | 'reconnect_required' | 'error' | 'ready' | 'refreshing';
// host.storage values are limited to 1 MiB. Reserve space for refresh claims,
// cancellation/error metadata and migration without evicting any cached cursor.
const maxWindows = 8, maxStoredBytes = 1024 * 1024, metadataReserveBytes = 8 * 1024;
interface CachedWindow { state: State; cache?: CalendarSnapshot; feed?: CalendarFeed; last_success_at?: string; error_code?: string }
interface Stored {
  version: 1; user_space_id: string; owner: string; revision: number; disabled: boolean; state: State;
  account_id?: string; last_success_at?: string; error_code?: string; cache?: CalendarSnapshot;
  source_epoch?: string; feed?: CalendarFeed;
  windows?: Record<string, CachedWindow>; selected_window?: string;
  refresh?: { id: string; query: CalendarQuery };
}
export interface PersonalCalendarOptions { reader?: CalendarReader; writer?: CalendarWriter; clock?: () => Date }
const object = (raw: unknown): Record<string, unknown> => {
  if (raw === undefined || raw === null) return {};
  if (typeof raw !== 'object' || Array.isArray(raw)) throw new Error('invalid calendar parameters');
  return raw as Record<string, unknown>;
};
const sameQuery = (a: CalendarQuery, b: CalendarQuery) => a.calendar_id === b.calendar_id && a.time_min === b.time_min && a.time_max === b.time_max && a.timezone === b.timezone && (b.account_id === undefined || a.account_id === b.account_id);
/** User-host-only Calendar service. Stores normalized read results, never grants.
 * Default runtime is deliberately unconnected until an account-bound adapter is
 * installed; neither plugin configuration nor model input can supply credentials. */
export class PersonalCalendarPlugin implements RuntimePlugin {
  readonly concurrentServices = [service];
  readonly concurrentCapabilities = ['plugin.client.request'];
  private host!: RuntimeHost;
  private space = ''; private key = ''; private owner = randomUUID();
  private available = false; private active = false; private abort = new AbortController();
  private callScope = new AsyncLocalStorage<{ host: RuntimeHost; sessionID: string }>();
  private reader: CalendarReader; private clock: () => Date;
  private writer?: CalendarWriter; private operations?: CalendarOperations;
  private inFlight?: { query: CalendarQuery; promise: Promise<unknown>; abort: AbortController };
  constructor(options: PersonalCalendarOptions = {}) { this.reader = options.reader ?? new ConnectedCalendarReader({
      status: async signal => decodeCalendarConnection(await this.connectedCall('status', {}, signal)),
      authorize: async signal => decodeCalendarConnection(await this.connectedCall('authorize', {}, signal)),
      eventsPage: (connectionID, query, signal) => { const { account_id: _account, ...bounded } = query; return this.connectedCall('events', { connection_id: connectionID, query: bounded }, signal); },
    }); this.writer = options.writer; this.clock = options.clock ?? (() => new Date()); }
  private connectedCall(method: string, input: unknown, signal: AbortSignal) {
    const scope = this.callScope.getStore();
    if (!scope) throw new Error('Calendar read requires an admitted execution scope');
    return callPersonalHost(scope.host, `calendar.read.${method}`, input, scope.sessionID, signal);
  }
  initialize(params: RuntimeInitializeParams, host: RuntimeHost, lifecycle: RuntimeLifecycleContext): RuntimeInitializeResult {
    this.host = host; this.space = params.user_space_id?.trim() ?? '';
    this.available = params.product_domain === 'personal' && Boolean(this.space);
    this.key = `calendar.v1.${createHash('sha256').update(this.space).digest('hex')}`;
    if (lifecycle.signal.aborted) this.abort.abort(); else lifecycle.signal.addEventListener('abort', () => { this.active = false; this.abort.abort(); this.operations?.stopOutstanding(); }, { once: true });
    if (!this.available) return { protocol_version: 3, provided_services: [], capabilities: [], tools: [] };
    this.operations = new CalendarOperations({ host, space: this.space, owner: this.owner, signal: this.abort.signal, writer: this.writer, clock: this.clock,
      guard: async () => { await this.current(); },
      validateTarget: async target => { await this.validateOperationTarget(target); },
      beforeMutation: async () => { await this.update(current => { if (current!.disabled) throw new Error('calendar disconnected'); return { ...current!, state: 'error', error_code: 'calendar_write_pending', refresh: undefined, windows: Object.fromEntries(Object.entries(current!.windows!).map(([key, window]) => [key, { ...window, state: 'error', error_code: 'calendar_write_pending' }])) }; }); this.inFlight?.abort.abort(); },
    });
    return { protocol_version: 3, lifecycle_version: 1,
      required_services: [...(['host.storage.get', 'host.storage.keys', 'host.storage.compare_exchange', 'host.session.context'] as const).map(requireKernelService), { name: 'personal.agents', major_version: 1, required: false }, { name: 'host.personal', major_version: 1, required: true }],
      provided_services: [{ name: service, version: '1.0.0', methods: methods.map(name => ({ name, input_schema: `calendar.personal.${name}.request.v1`, output_schema: 'calendar.personal.snapshot.v1' })) }],
      capabilities: [{ id: 'plugin.client.request', kind: 'decision', version: 1 }],
      // Keep existing calendar state and client reconciliation available without
      // exposing a native Calendar tool surface to the model.
      tools: [],
    };
  }
  async activate(): Promise<void> {
    if (!this.available || this.abort.signal.aborted) return;
    await this.update(current => {
      const state = current ?? this.empty(), windows = { ...state.windows };
      // Migrate the single-window representation without replacing its cursor or
      // journal. Unsupported/absent old cursors still require a fresh baseline.
      const legacyKey = state.cache ? calendarWindowKey(state.cache.source) : undefined;
      if (!state.windows && legacyKey) windows[legacyKey] = { cache: state.cache, feed: state.feed, state: state.state, last_success_at: state.last_success_at, error_code: state.error_code };
      if (state.refresh) { const key = calendarWindowKey(state.refresh.query); if (windows[key]) windows[key] = { ...windows[key], state: 'error', error_code: 'refresh_interrupted' }; }
      return { ...state, windows, selected_window: state.selected_window ?? legacyKey, cache: undefined, feed: undefined, source_epoch: state.source_epoch ?? state.cache?.source.generation ?? randomUUID(), owner: this.owner, refresh: undefined, state: state.state === 'refreshing' ? 'error' : state.state, error_code: state.state === 'refreshing' ? 'refresh_interrupted' : state.error_code };
    }, true);
    this.active = true;
    if (this.writer) await this.operations!.activate();
  }
  shutdown(): void { this.active = false; this.abort.abort(); this.operations?.stopOutstanding(); }
  private empty(): Stored { return { version: 1, user_space_id: this.space, owner: this.owner, revision: 0, disabled: false, state: 'unconnected', source_epoch: randomUUID(), windows: {} }; }
  private assertActive(): void { if (!this.available || !this.active || this.abort.signal.aborted) throw new Error('Personal calendar is unavailable'); }
  private async stored(): Promise<{ raw: string | null; state?: Stored }> {
    const { value } = await this.host.call('host.storage.get', { scope: 'user', key: this.key }, { signal: this.abort.signal });
    if (!value) return { raw: value };
    let state: Stored;
    try { state = JSON.parse(value); } catch { throw new Error('invalid calendar storage'); }
    if (state.version !== 1 || state.user_space_id !== this.space || !Number.isInteger(state.revision) || typeof state.owner !== 'string' || typeof state.disabled !== 'boolean' || !['unconnected', 'reconnect_required', 'error', 'ready', 'refreshing'].includes(state.state)) throw new Error('invalid calendar storage scope');
    if (state.cache && (state.cache.source?.user_space_id !== this.space || state.cache.source.account_id !== state.account_id)) throw new Error('invalid calendar cache scope');
    if (state.windows) {
      if (typeof state.windows !== 'object' || Array.isArray(state.windows) || Object.keys(state.windows).length > maxWindows) throw new Error('invalid calendar window storage');
      for (const [key, window] of Object.entries(state.windows)) if (window.cache && (window.cache.source?.user_space_id !== this.space || window.cache.source.account_id !== state.account_id || window.cache.source.generation !== undefined && window.cache.source.generation !== state.source_epoch || calendarWindowKey(window.cache.source) !== key)) throw new Error('invalid calendar window scope');
    }
    return { raw: value, state };
  }
  private async update(change: (state?: Stored) => Stored, claim = false): Promise<Stored> {
    for (let attempt = 0; attempt < 16; attempt++) {
      if (!claim) this.assertActive();
      const current = await this.stored();
      if (!claim && current.state?.owner !== this.owner) throw new Error('calendar runtime generation replaced');
      const next = change(current.state);
      if (next === current.state) return next;
      next.revision = (current.state?.revision ?? 0) + 1;
      const value = JSON.stringify(next);
      if (Buffer.byteLength(value) > maxStoredBytes) throw new Error('calendar window_storage_capacity_exceeded');
      const result = await this.host.call('host.storage.compare_exchange', { scope: 'user', key: this.key, expected: current.raw, value }, { signal: this.abort.signal });
      if (result.swapped) return next;
    }
    throw new Error('calendar revision conflict');
  }
  private view(state: Stored, query?: CalendarQuery) {
    const window = state.windows?.[query ? calendarWindowKey(query) : state.selected_window ?? ''];
    const cache = window?.cache && (!query || sameQuery(window.cache.source, query)) ? window.cache : undefined;
    const observedState = window?.state ?? state.state;
    const age = cache ? this.clock().getTime() - Date.parse(cache.source.fetched_at) : Infinity;
    const freshness = cache ? observedState === 'ready' && age >= 0 && age <= 5 * 60_000 ? 'fresh' : 'stale' : 'empty';
    return { state: observedState, read_only: !this.writer, freshness, account_id: state.account_id, last_success_at: window?.last_success_at, error_code: window?.error_code ?? (!window ? state.error_code : undefined), cached_windows: Object.keys(state.windows ?? {}).length, max_cached_windows: maxWindows,
      events: cache?.events ?? [], cancelled_events: cache?.cancelled_events ?? [], source: cache?.source };
  }
  private async current(query?: CalendarQuery) {
    return this.view(await this.authorizedState(), query);
  }
  private async authorizedState(): Promise<Stored> {
    this.assertActive(); const { state } = await this.stored();
    if (!state || state.owner !== this.owner) throw new Error('calendar runtime generation replaced');
    const sources = Object.values(state.windows ?? {}).flatMap(window => window.cache ? [window.cache.source] : []);
    if (!sources.length || !this.reader.validateCache) return state;
    try {
      await this.reader.validateCache(this.space, sources, this.abort.signal);
      this.assertActive();
      // If another request replaces the cache while authority was checked, do
      // not release that unverified generation. Its caller may retry.
      const current = await this.stored();
      if (current.state?.owner !== this.owner || current.state.revision !== state.revision) throw new Error('calendar cache changed during authorization');
      return current.state;
    } catch (error) {
      const failure = error instanceof CalendarReadError ? error : new CalendarReadError('error', 'authorization_unverified');
      const updated = await this.update(current => {
        const retain = failure.state === 'error';
        return { ...current!, state: failure.state, error_code: failure.code, refresh: undefined,
          windows: retain ? Object.fromEntries(Object.entries(current!.windows ?? {}).map(([key, window]) => [key, { ...window, state: 'error', error_code: failure.code }])) : {},
          source_epoch: retain ? current!.source_epoch : randomUUID(), account_id: retain ? current!.account_id : undefined, last_success_at: retain ? current!.last_success_at : undefined };
      });
      this.inFlight?.abort.abort();
      return updated;
    }
  }
  private async session(sessionID: unknown) {
    if (typeof sessionID !== 'string' || !sessionID) throw new Error('Personal calendar requires a verified session scope');
    const context = await this.host.call('host.session.context', { session_id: sessionID }, { signal: this.abort.signal });
    if (context.product_domain !== 'personal' || context.user_space_id !== this.space) throw new Error('Personal calendar session scope mismatch');
    return context;
  }
  private async validateOperationTarget(target: { task_id: string; requirements_revision: number } & WriteTarget): Promise<void> {
    const context = await this.session(target.session_id);
    if (context.agent_id !== target.agent_id || target.user_space_id !== this.space) throw new Error('operation actor scope mismatch');
    const response = await this.host.call('host.service.call', { service: 'personal.agents', method: 'tasks.get', params: { task_id: target.task_id, session_id: target.session_id } }) as { task?: { task_id: string; user_space_id: string; agent_id: string; requirements_revision: number; state: string; cancel_requested?: boolean } };
    const task = response.task;
    if (!task || task.task_id !== target.task_id || task.user_space_id !== this.space || task.agent_id !== target.agent_id || task.requirements_revision !== target.requirements_revision || task.cancel_requested || ['stopped', 'failed', 'completed'].includes(task.state)) throw new Error('operation task requirements revision or scope changed');
  }
  async invokeService(call: ServiceInvokeParams, _host?: RuntimeHost, execution?: RuntimeExecutionContext): Promise<unknown> {
    this.assertActive(); if (call.service !== service) throw new Error('unknown calendar service');
    const input = object(call.params);
    if (call.caller !== 'kernel') {
      if (['disconnect', 'clear-cache'].includes(call.method) || input.reconnect === true) await callPersonalHost(_host ?? this.host, 'owner.check', {}, undefined, execution?.signal);
      else await this.session(input.session_id);
    }
    return this.callScope.run({ host: _host ?? this.host, sessionID: typeof input.session_id === 'string' ? input.session_id : '' }, () => this.request(call.method, input, execution?.signal));
  }
  async invokeCapability(call: CapabilityInvokeParams, _host?: RuntimeHost, execution?: RuntimeExecutionContext): Promise<{ output: unknown }> {
    this.assertActive(); if (call.capability !== 'plugin.client.request') throw new Error('unknown calendar capability');
    const input = object(call.input); if (typeof input.method !== 'string') throw new Error('invalid calendar method');
    const params = object(input.input), method = input.method.replace(/^calendar\./, "");
    await callPersonalHost(_host ?? this.host, 'owner.check', {}, undefined, execution?.signal);
    return this.callScope.run({ host: _host ?? this.host, sessionID: typeof params.session_id === 'string' ? params.session_id : '' }, async () => ({ output: { result: await this.request(method, params, execution?.signal, true) } }));
  }
  private async request(method: string, input: Record<string, unknown>, executionSignal?: AbortSignal, client = false): Promise<unknown> {
    this.assertActive();
    executionSignal?.throwIfAborted();
    if (method.startsWith('operations.')) {
      const signal = executionSignal ? AbortSignal.any([this.abort.signal, executionSignal]) : this.abort.signal;
      if (method === 'operations.confirm' || method === 'operations.cancel') {
        if (!client) throw new Error('Calendar confirmation and cancellation require the trusted user client capability');
        if (!Number.isInteger(input.expected_revision) || (input.expected_revision as number) < 1) throw new Error('invalid expected_revision');
        if (method === 'operations.cancel') return this.operations!.cancel(String(input.operation_id), input.expected_revision as number);
        if ((await this.stored()).state?.disabled) throw new Error('calendar disconnected');
        return this.operations!.confirm(String(input.operation_id), input.expected_revision as number, String(input.preview_digest), signal);
      }
      if (method === 'operations.propose') {
        if ((await this.stored()).state?.disabled) throw new Error('calendar disconnected');
        const context = await this.session(input.session_id);
        return this.operations!.propose(input, { user_space_id: this.space, agent_id: context.agent_id, session_id: context.session_id }, signal);
      }
      if (method === 'operations.get') return this.operations!.get(String(input.operation_id));
      if (method === 'operations.list') return { operations: await this.operations!.list() };
      if (method === 'operations.reconcile') return this.operations!.reconcile(String(input.operation_id), signal);
      throw new Error('unknown calendar operation method');
    }
    if (method === 'status') return this.current(input.calendar_id === undefined ? undefined : calendarQuery(input));
    if (method === 'agenda') return this.current(calendarQuery(input));
    if (method === 'changes') {
      const query = calendarQuery(input), state = await this.authorizedState();
      if (!state || state.owner !== this.owner) throw new Error('calendar runtime generation replaced');
      const window = state.windows?.[calendarWindowKey(query)], view = this.view(state, query);
      return calendarChanges(window?.feed, window?.cache, query, input, view.state, view.freshness === 'fresh');
    }
    if (method === 'disconnect' || method === 'clear-cache') {
      const state = await this.update(current => ({ ...current!, windows: {}, selected_window: undefined, source_epoch: randomUUID(), refresh: undefined, last_success_at: undefined, error_code: undefined, account_id: method === 'disconnect' ? undefined : current!.account_id, disabled: method === 'disconnect' || (input.reconnect === true ? false : current!.disabled), state: method === 'disconnect' ? 'unconnected' : current!.account_id ? 'ready' : 'unconnected' }));
      this.inFlight?.abort.abort();
      if (method === 'disconnect') this.operations?.stopOutstanding();
      return this.view(state);
    }
    if (method !== 'refresh') throw new Error('unknown calendar method');
    const query = calendarQuery(input);
    // Reserve synchronously before persistence awaits so concurrent callers share
    // the entire claim, read and commit rather than merely the network request.
    if (this.inFlight) { if (!sameQuery(this.inFlight.query, query) || this.inFlight.query.account_id !== query.account_id || input.reconnect === true) throw new Error('calendar refresh busy'); return this.inFlight.promise; }
    const abort = new AbortController();
    const signals = [this.abort.signal, abort.signal]; if (executionSignal) signals.push(executionSignal);
    const promise = this.refresh(query, input.reconnect === true, AbortSignal.any(signals)); this.inFlight = { query, promise, abort };
    try { return await promise; } finally { if (this.inFlight?.promise === promise) this.inFlight = undefined; }
  }
  private async refresh(query: CalendarQuery, reconnect: boolean, signal: AbortSignal): Promise<unknown> {
    const id = randomUUID(), key = calendarWindowKey(query);
    const claimed = await this.update(current => {
      if (current!.disabled && !reconnect) return current!;
      const windows = reconnect ? {} : current!.windows!;
      // A caller must explicitly clear/reset when capacity is exhausted. A new
      // Today window must never evict a journal used by an active responsibility.
      if (!windows[key] && Object.keys(windows).length >= maxWindows) throw new Error('calendar window_capacity_exceeded');
      return { ...current!, ...(reconnect ? { source_epoch: randomUUID() } : {}), windows: { ...windows, [key]: { ...windows[key], state: 'refreshing', error_code: undefined } }, selected_window: key, disabled: false, state: 'refreshing', error_code: undefined, refresh: { id, query } };
    });
    if (claimed.disabled) return this.view(claimed, query);
    let snapshot: CalendarSnapshot | undefined, failure: CalendarReadError | undefined;
    try { snapshot = await this.reader.read(this.space, query, signal); }
    catch (error) { failure = error instanceof CalendarReadError ? error : new CalendarReadError('error', 'provider_unavailable'); }
    // Stop/reload/clear is a fence even when a transport ignores cancellation.
    const current = await this.stored();
    if (!this.active || this.abort.signal.aborted || current.state?.owner !== this.owner || current.state.refresh?.id !== id) return this.view(current.state ?? this.empty(), query);
    let finished: Stored;
    try { finished = await this.update(state => {
      if (state!.refresh?.id !== id) return state!;
      if (snapshot) {
        if (snapshot.source.user_space_id !== this.space || !sameQuery(snapshot.source, query)) throw new Error('calendar provider scope mismatch');
        const accountChanged = state!.account_id !== undefined && state!.account_id !== snapshot.source.account_id;
        const epoch = accountChanged ? randomUUID() : state!.source_epoch!, windows = accountChanged ? {} : state!.windows!;
        const previous = windows[key], advanced = advanceCalendarFeed(previous?.feed, previous?.cache, snapshot, epoch);
        const window: CachedWindow = { state: 'ready', cache: advanced.snapshot, feed: advanced.feed, last_success_at: snapshot.source.fetched_at };
        const next: Stored = { ...state!, revision: state!.revision + 1, state: 'ready', refresh: undefined, windows: { ...windows, [key]: window }, source_epoch: epoch, account_id: snapshot.source.account_id, last_success_at: snapshot.source.fetched_at, error_code: undefined };
        if (Buffer.byteLength(JSON.stringify(next)) > maxStoredBytes - metadataReserveBytes) {
          return { ...state!, state: 'error', refresh: undefined, windows: { ...windows, [key]: { ...previous, state: 'error', error_code: 'window_storage_capacity_exceeded' } }, source_epoch: epoch, account_id: snapshot.source.account_id, error_code: 'window_storage_capacity_exceeded' };
        }
        return next;
      }
      const retain = failure!.state === 'error' && (!failure!.account_id || failure!.account_id === state!.account_id);
      return { ...state!, state: failure!.state, refresh: undefined, error_code: failure!.code, windows: retain ? { ...state!.windows, [key]: { ...state!.windows![key], state: 'error', error_code: failure!.code } } : {}, source_epoch: retain ? state!.source_epoch : randomUUID(), account_id: failure!.account_id ?? (retain ? state!.account_id : undefined), last_success_at: retain ? state!.last_success_at : undefined };
    }); } catch {
      // A host-wide storage limit (including other operation-ledger keys) may
      // reject an otherwise valid value. Re-read to distinguish a lost CAS ack
      // from a rejected commit, and retain only an explicitly stale old window.
      finished = await this.update(state => {
        if (state!.refresh?.id !== id) return state!;
        const account = snapshot?.source.account_id ?? failure?.account_id;
        const clear = failure && failure.state !== 'error' || account !== undefined && account !== state!.account_id;
        return { ...state!, state: 'error', error_code: 'cache_persistence_failed', refresh: undefined, windows: clear ? {} : { ...state!.windows, [key]: { ...state!.windows![key], state: 'error', error_code: 'cache_persistence_failed' } }, source_epoch: clear ? randomUUID() : state!.source_epoch, account_id: clear ? account : state!.account_id, last_success_at: clear ? undefined : state!.last_success_at };
      });
    }
    return this.view(finished, query);
  }
}
export default new PersonalCalendarPlugin();
