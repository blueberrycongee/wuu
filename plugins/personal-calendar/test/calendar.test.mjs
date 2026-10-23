// Synthetic protocol fixtures only. These contracts precede implementation;
// they do not validate a real Google account, OAuth consent, or remote writes.
// Failure cases: cross-domain reads, token/URL leakage, partial page success,
// stale refresh presentation, account changes, and late work after clear/stop/reload.
import assert from 'node:assert/strict';
import { after, test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from '../../node_modules/esbuild/lib/main.js';
const dir = await mkdtemp(join(tmpdir(), 'wuu-calendar-contract-'));
after(() => rm(dir, { recursive: true, force: true }));
await build({ entryPoints: [new URL('../src/index.ts', import.meta.url).pathname], bundle: true, platform: 'node', format: 'esm', outfile: join(dir, 'calendar.mjs'), alias: { '@wuu/plugin-sdk': new URL('../../../packages/plugin-sdk/src/index.ts', import.meta.url).pathname } });
const { PersonalCalendarPlugin, GoogleCalendarReader } = await import(join(dir, 'calendar.mjs'));
const query = { calendar_id: 'primary', time_min: '2026-10-18T00:00:00Z', time_max: '2026-10-19T00:00:00Z', timezone: 'America/New_York' };
const current = () => new Date('2026-10-18T12:00:00Z');
const authorization = { user_space_id: 'user-one', account_id: 'synthetic-account', access_token: 'synthetic-secret-token', expires_at: '2026-10-18T13:00:00Z', scopes: ['https://www.googleapis.com/auth/calendar.readonly'] };
const timed = { id: 'meeting', status: 'confirmed', summary: 'Synthetic dinner', start: { dateTime: '2026-10-18T17:00:00-04:00', timeZone: 'America/New_York' }, end: { dateTime: '2026-10-18T18:00:00-04:00', timeZone: 'America/New_York' }, recurringEventId: 'weekly', originalStartTime: { dateTime: '2026-10-18T17:00:00-04:00' } };
const page = (items = [timed], extra = {}) => new Response(JSON.stringify({ timeZone: 'America/New_York', items, ...extra }), { status: 200 });
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
class Host {
  values = new Map(); calls = []; context = { session_id: 'personal-session', product_domain: 'personal', user_space_id: 'user-one', agent_id: 'main', parent_session_id: '' };
  async call(method, p) {
    this.calls.push([method, p]);
    if (method === 'host.session.context') return this.context;
    if (method === 'host.service.call' && p.service === 'host.personal') { if (p.params.operation === 'owner.check') throw new Error('calendar connection changes require the user client'); return {ok:true,value:{product_domain:'personal',provider:'google-calendar',user_space_id:'user-one',state:'unavailable',selected_calendars:[],error_code:'oauth_client_not_configured'}}; }
    const key = `${p.scope}:${p.key}`;
    if (method === 'host.storage.get') return { value: this.values.get(key) ?? null };
    assert.equal(method, 'host.storage.compare_exchange');
    const value = this.values.get(key) ?? null;
    if (value !== p.expected) return { swapped: false, value };
    this.values.set(key, p.value); return { swapped: true, value: p.value };
  }
}
async function fixture({ fetch = async () => page(), authorize = async () => authorization, host = new Host(), domain = 'personal' } = {}) {
  const reader = new GoogleCalendarReader({ authorize, fetch, clock: current });
  const plugin = new PersonalCalendarPlugin({ reader, clock: current });
  const lifetime = new AbortController();
  const definition = plugin.initialize({ protocol_version: 3, plugin_id: 'personal-calendar', plugin_root: '/', project_root: '', wuu_home: '/synthetic', user_space_id: 'user-one', product_domain: domain, data_root: '/synthetic/personal' }, host, { signal: lifetime.signal });
  await plugin.activate();
  const call = (method, params = {}, caller = 'kernel') => plugin.invokeService({ service: 'calendar.personal', method, params, caller });
  return { plugin, host, call, definition, lifetime };
}

test('unconnected runtime exposes honest read-only capability; Coding does no storage or transport work', async () => {
  const missing = await fixture({ authorize: undefined });
  // Constructor default is exercised separately from the fixture default authorization.
  const bare = new PersonalCalendarPlugin({ clock: current }); const host = new Host();
  bare.initialize({ product_domain: 'personal', user_space_id: 'user-one' }, host, { signal: new AbortController().signal }); await bare.activate();
  const status = await bare.invokeService({ service: 'calendar.personal', method: 'refresh', params: query, caller: 'kernel' });
  assert.equal(status.state, 'unconnected'); assert.equal(status.read_only, true); assert.equal(status.freshness, 'empty');
  const coding = await fixture({ domain: 'coding', fetch: () => assert.fail('Coding contacted Calendar') });
  assert.equal(coding.definition.provided_services.length, 0); assert.equal(coding.host.calls.length, 0);
  await assert.rejects(coding.call('agenda', query), /unavailable/);
  assert.deepEqual(missing.definition.tools, []);
});

test('read adapter fixes the official endpoint, sends GET only, preserves dates and recurring cancellation provenance', async () => {
  const requests = [];
  const allDay = { id: 'holiday', status: 'confirmed', summary: 'Synthetic day', start: { date: '2026-10-18' }, end: { date: '2026-10-20' } };
  const cancelled = { id: 'cancelled-instance', status: 'cancelled', recurringEventId: 'weekly', originalStartTime: { dateTime: '2026-10-19T17:00:00-04:00' } };
  const f = await fixture({ fetch: async (url, init) => { requests.push([url.toString(), init]); return requests.length === 1 ? page([allDay], { nextPageToken: 'opaque/page?token' }) : page([timed, cancelled]); } });
  const data = await f.call('refresh', query);
  assert.equal(data.state, 'ready'); assert.equal(data.freshness, 'fresh'); assert.equal(data.events.length, 2);
  assert.equal(data.events[0].all_day, true); assert.equal(data.events[0].end.date, '2026-10-20');
  assert.equal(data.events[1].start.date_time, timed.start.dateTime); assert.equal(data.events[1].recurring_event_id, 'weekly');
  assert.equal(data.cancelled_events[0].id, 'cancelled-instance'); assert.equal(data.cancelled_events[0].status, 'cancelled');
  assert.equal(data.source.user_space_id, 'user-one'); assert.equal(data.source.account_id, authorization.account_id); assert.equal(data.source.calendar_id, 'primary');
  for (const [url, init] of requests) { const parsed = new URL(url); assert.equal(parsed.origin, 'https://www.googleapis.com'); assert.equal(init.method, 'GET'); assert.equal(init.redirect, 'error'); assert.equal(parsed.searchParams.get('singleEvents'), 'true'); assert.equal(parsed.searchParams.get('showDeleted'), 'true'); assert.equal(url.includes(authorization.access_token), false); }
  assert.equal(JSON.stringify([...f.host.values]).includes(authorization.access_token), false); assert.equal(JSON.stringify(data).includes(authorization.access_token), false);
});

test('invalid ranges, timezone and path injection fail before authorization or network', async () => {
  const f = await fixture({ authorize: () => assert.fail('invalid input reached authorization'), fetch: () => assert.fail('invalid input reached transport') });
  for (const change of [{ time_min: '2026-10-18' }, { time_max: '2026-10-17T00:00:00Z' }, { time_max: '2027-10-19T00:00:00Z' }, { timezone: 'Invalid/Zone' }, { calendar_id: '../private' }, { calendar_id: 'https://evil.example/x' }]) await assert.rejects(f.call('refresh', { ...query, ...change }), /invalid|range/);
});

test('session-bound reads reject Coding and foreign user spaces, including forged input identity', async () => {
  const f = await fixture(); await f.call('refresh', query);
  for (const context of [{ ...f.host.context, product_domain: 'coding' }, { ...f.host.context, user_space_id: 'other-user' }]) {
    f.host.context = context;
    await assert.rejects(f.call('agenda', { ...query, session_id: 'personal-session', user_space_id: 'user-one' }, 'personal'), /scope/);
  }
  await assert.rejects(f.call('disconnect', {}, 'personal'), /user client/);
});

test('refresh failures preserve last success only as stale; auth denial and changed accounts remove cached personal data', async () => {
  let mode = 'ok'; let account = authorization;
  const f = await fixture({ authorize: async () => account, fetch: async () => mode === 'ok' ? page() : new Response('synthetic-secret-token provider detail', { status: mode === 'auth' ? 401 : 503 }) });
  const good = await f.call('refresh', query); mode = 'server';
  const failed = await f.call('refresh', query); assert.equal(failed.state, 'error'); assert.equal(failed.freshness, 'stale'); assert.equal(failed.last_success_at, good.last_success_at); assert.equal(failed.events.length, 1); assert.equal(JSON.stringify(failed).includes(authorization.access_token), false);
  account = { ...authorization, account_id: 'different-account' }; const switched = await f.call('refresh', query); assert.equal(switched.events.length, 0); assert.equal(switched.source, undefined);
  mode = 'ok'; await f.call('refresh', query); mode = 'auth'; const revoked = await f.call('refresh', query); assert.equal(revoked.state, 'reconnect_required'); assert.equal(revoked.events.length, 0); assert.equal(JSON.stringify([...f.host.values]).includes('Synthetic dinner'), false);
});

test('partial page failure never commits a partial fresh snapshot or loops repeated pagination', async () => {
  let mode = 'ok', count = 0;
  const f = await fixture({ fetch: async () => { count++; if (mode === 'ok') return page(); return count % 2 === 0 ? page([], { nextPageToken: 'same' }) : page([], { nextPageToken: 'same' }); } });
  await f.call('refresh', query); mode = 'cycle'; const data = await f.call('refresh', query);
  assert.equal(data.state, 'error'); assert.equal(data.freshness, 'stale'); assert.equal(data.events.length, 1); assert.equal(data.error_code, 'invalid_response'); assert.ok(count <= 4);
});

test('concurrent same-query refresh shares work; clear-cache fences late provider results', async () => {
  const started = deferred(), release = deferred(); let count = 0;
  const f = await fixture({ fetch: async () => { count++; started.resolve(); await release.promise; return page(); } });
  const one = f.call('refresh', query); await started.promise; const two = f.call('refresh', query);
  await assert.rejects(f.call('refresh', { ...query, calendar_id: 'another' }), /busy/);
  const empty = await f.call('clear-cache'); assert.equal(empty.freshness, 'empty'); release.resolve();
  await Promise.all([one, two]); assert.equal(count, 1); assert.equal((await f.call('agenda', query)).events.length, 0);
});

test('disconnect fences reads across restart and requires explicit client reconnection', async () => {
  let count = 0; const f = await fixture({ fetch: async () => { count++; return page(); } });
  await f.call('refresh', query); const disconnected = await f.call('disconnect'); assert.equal(disconnected.state, 'unconnected');
  const restored = await fixture({ host: f.host, fetch: async () => { count++; return page(); } });
  assert.equal((await restored.call('refresh', query)).state, 'unconnected'); assert.equal(count, 1);
  assert.equal((await restored.call('refresh', { ...query, reconnect: true })).state, 'ready'); assert.equal(count, 2);
});

test('reload owns the generation; stopped or replaced runtimes cannot repopulate cached events', async () => {
  const started = deferred(), release = deferred(); const f = await fixture({ fetch: async () => { started.resolve(); await release.promise; return page(); } });
  const pending = f.call('refresh', query); await started.promise;
  const replacement = await fixture({ host: f.host }); release.resolve(); await pending;
  assert.equal((await replacement.call('agenda', query)).events.length, 0);
  await assert.rejects(f.call('refresh', query), /generation|unavailable/);
  f.lifetime.abort(); await f.plugin.shutdown();
  await assert.rejects(f.call('agenda', query), /unavailable/);
});

test('cache window mismatches are empty and aged successful data is explicitly stale', async () => {
  const f = await fixture(); await f.call('refresh', query);
  const mismatch = await f.call('agenda', { ...query, calendar_id: 'other' }); assert.equal(mismatch.events.length, 0); assert.equal(mismatch.freshness, 'empty'); assert.equal(mismatch.source, undefined);
  const plugin = new PersonalCalendarPlugin({ reader: new GoogleCalendarReader({ authorize: async () => authorization, clock: current }), clock: () => new Date('2026-10-18T12:20:00Z') });
  plugin.initialize({ product_domain: 'personal', user_space_id: 'user-one' }, f.host, { signal: new AbortController().signal }); await plugin.activate();
  const aged = await plugin.invokeService({ service: 'calendar.personal', method: 'agenda', params: query, caller: 'kernel' }); assert.equal(aged.freshness, 'stale'); assert.equal(aged.events.length, 1);
});

test('disconnect cancels current transport and prevents further pagination immediately', async () => {
  const started = deferred(), release = deferred(); let transportSignal; let requests = 0;
  const f = await fixture({ fetch: async (_url, init) => { requests++; transportSignal = init.signal; started.resolve(); await release.promise; return page([timed], { nextPageToken: 'second-page' }); } });
  const pending = f.call('refresh', query); await started.promise; await f.call('disconnect');
  assert.equal(transportSignal.aborted, true); release.resolve(); await pending;
  assert.equal(requests, 1); assert.equal((await f.call('agenda', query)).events.length, 0);
});

test('expired or foreign grants never reach transport; malformed and oversized responses remain errors', async () => {
  for (const grant of [{ ...authorization, user_space_id: 'another-user' }, { ...authorization, expires_at: '2026-10-18T11:59:59Z' }, { ...authorization, scopes: [] }]) {
    const f = await fixture({ authorize: async () => grant, fetch: () => assert.fail('invalid grant reached provider') });
    assert.equal((await f.call('refresh', query)).state, 'reconnect_required');
  }
  for (const response of [() => page([{ ...timed, start: { date: '2026-02-31' }, end: { date: '2026-03-04' } }]), () => new Response('x'.repeat(2 * 1024 * 1024 + 1)), () => page([], { nextPageToken: 42 })]) {
    const f = await fixture({ fetch: response }); const result = await f.call('refresh', query); assert.equal(result.state, 'error'); assert.equal(result.events.length, 0);
  }
});

test('second-page HTTP failure does not replace a previously complete snapshot', async () => {
  let requests = 0, failing = false;
  const f = await fixture({ fetch: async () => { requests++; return !failing ? page() : requests === 2 ? page([{ ...timed, id: 'partial' }], { nextPageToken: 'next' }) : new Response('', { status: 503 }); } });
  await f.call('refresh', query); failing = true;
  const result = await f.call('refresh', query); assert.equal(result.freshness, 'stale'); assert.equal(result.events[0].id, 'meeting'); assert.equal(result.events.length, 1);
});

test('cancelled service execution stops its refresh transport without marking cached data fresh', async () => {
  const started = deferred(), release = deferred(); let transportSignal;
  const f = await fixture({ fetch: async (_url, init) => { transportSignal = init.signal; started.resolve(); await release.promise; return page(); } });
  const execution = new AbortController();
  const pending = f.plugin.invokeService({ service: 'calendar.personal', method: 'refresh', caller: 'personal', params: { ...query, session_id: 'personal-session' } }, f.host, { executionId: 'synthetic-execution', signal: execution.signal });
  await started.promise; execution.abort(); assert.equal(transportSignal.aborted, true); release.resolve();
  const result = await pending; assert.equal(result.state, 'error'); assert.equal(result.freshness, 'empty');
});
