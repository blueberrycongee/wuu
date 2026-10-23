// Failure-first contracts for the token-free host bridge. Synthetic data only.
import assert from 'node:assert/strict';
import { test, after } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from '../../node_modules/esbuild/lib/main.js';
const dir = await mkdtemp(join(tmpdir(), 'wuu-connected-calendar-'));
after(() => rm(dir, { recursive: true, force: true }));
await build({ entryPoints: [new URL('../src/connected-calendar.ts', import.meta.url).pathname], bundle: true, platform: 'node', format: 'esm', outfile: join(dir, 'connected.mjs') });
const { ConnectedCalendarReader, tomorrowQuery, calendarAgendaTool, executeCalendarAgenda } = await import(join(dir, 'connected.mjs'));
const query = { calendar_id: 'fixture@example.invalid', time_min: '2026-11-01T04:00:00Z', time_max: '2026-11-02T05:00:00Z', timezone: 'America/New_York' };
const connection = { connection_id: 'c1', user_space_id: 'space', product_domain: 'personal', provider: 'google-calendar', account_id: 'subject', state: 'connected', selected_calendars: [query.calendar_id] };
const event = { id: 'instance', status: 'confirmed', summary: '<script>fixture</script>', start: { dateTime: '2026-11-01T09:00:00-05:00' }, end: { dateTime: '2026-11-01T10:00:00-05:00' }, recurringEventId: 'series', originalStartTime: { dateTime: '2026-11-01T08:00:00-05:00' } };
const signal = () => new AbortController().signal;
function fixture(pages) { let count = 0; const host = { status: async () => connection, eventsPage: async (id, q) => { assert.equal(id, 'c1'); return pages[count++]; } }; return { reader: new ConnectedCalendarReader(host), count: () => count }; }
test('tomorrow uses civil midnight across both DST boundaries and validates zones', () => {
 const fall = tomorrowQuery(query.calendar_id, 'America/New_York', new Date('2026-10-31T16:00:00Z')); assert.equal(Date.parse(fall.time_min), Date.parse(query.time_min)); assert.equal(Date.parse(fall.time_max), Date.parse(query.time_max));
 const spring = tomorrowQuery(query.calendar_id, 'America/New_York', new Date('2026-03-07T16:00:00Z')); assert.equal(Date.parse(spring.time_max)-Date.parse(spring.time_min), 23*3600000);
 assert.throws(() => tomorrowQuery(query.calendar_id, 'Fake/Zone'), /timezone/);
});
test('pages normalize recurring moves, cancellations, exclusive all-day end and deduplicate repeated records', async () => {
 const cancelled = { id: 'removed', status: 'cancelled', recurringEventId: 'series', originalStartTime: { date: '2026-11-01' } };
 const day = { id: 'day', status: 'confirmed', start: { date: '2026-11-01' }, end: { date: '2026-11-02' } };
 const f = fixture([{ items: [event, day], nextPageToken: 'page2' }, { items: [event, cancelled] }]); const snapshot = await f.reader.read('space', query, signal()); assert.equal(snapshot.events.length, 2); assert.equal(snapshot.cancelled_events.length, 1); assert.equal(snapshot.events[1].end.date, '2026-11-02'); assert.equal(snapshot.events[0].recurring_event_id, 'series'); assert.equal(snapshot.events[0].original_start_time.date_time, event.originalStartTime.dateTime); assert.equal(f.count(), 2);
});
test('foreign scope, unselected calendar, pagination cycle and partial failure are rejected without snapshots', async () => {
 const f = fixture([{ items: [], nextPageToken: 'same' }, { items: [], nextPageToken: 'same' }]); await assert.rejects(f.reader.read('other', query, signal()), /scope/); assert.equal(f.count(), 0); await assert.rejects(f.reader.read('space', { ...query, calendar_id: 'other' }, signal()), /selected/); await assert.rejects(f.reader.read('space', query, signal()), /pagination/);
 const partial = fixture([{ items: [event], nextPageToken: 'next' }, null]); await assert.rejects(partial.reader.read('space', query, signal()), /response/);
});
test('fresh reads share in-flight requests and disconnect fences late results', async () => {
 let release; const blocked = new Promise(r => release = r); let count = 0, state = connection;
 const host = { status: async () => state, eventsPage: async () => { count++; await blocked; return { items: [event] }; } }; const reader = new ConnectedCalendarReader(host); const one = reader.read('space', query, signal()), two = reader.read('space', query, signal()); await new Promise(setImmediate); assert.equal(count, 1); state = { ...connection, state: 'unconnected' }; release(); await Promise.all([assert.rejects(one, e => e.state === 'unconnected'), assert.rejects(two, e => e.state === 'unconnected')]);
});
test('model tool resolves session identity and exposes agenda only', async () => {
 assert.equal(calendarAgendaTool.id, 'calendar_agenda'); let called = false;
 const host = { call: async (method, p) => { if (method === 'host.session.context') return { product_domain: 'coding', user_space_id: 'space' }; called = true; throw new Error('should not reach service'); } };
 await assert.rejects(executeCalendarAgenda(host, 'space', { tool_id: 'calendar_agenda', session_id: 's', arguments: query }), /scope/); assert.equal(called, false);
 host.call = async (method, p) => method === 'host.session.context' ? { product_domain: 'personal', user_space_id: 'space' } : { state: 'ready', events: [event], source: query };
 const answer = await executeCalendarAgenda(host, 'space', { tool_id: 'calendar_agenda', session_id: 's', arguments: query }); assert.equal(answer.result.content[0].type, 'text'); assert.equal(JSON.parse(answer.result.content[0].text).events.length, 1);
 await assert.rejects(executeCalendarAgenda(host, 'space', { tool_id: 'calendar_delete', session_id: 's', arguments: query }), /unknown/);
});

// Regression: a host-side auth/selection change must clear the existing CAS
// cache on the first failed refresh, rather than retaining old events as stale.
await build({ stdin: { contents: "export { PersonalCalendarPlugin } from './plugins/personal-calendar/src/index.ts'; export { ConnectedCalendarReader } from './plugins/personal-calendar/src/connected-calendar.ts';", resolveDir: new URL('../../../', import.meta.url).pathname }, bundle: true, platform: 'node', format: 'esm', outfile: join(dir, 'plugin.mjs'), alias: { '@wuu/plugin-sdk': new URL('../../../packages/plugin-sdk/src/index.ts', import.meta.url).pathname } });
const { PersonalCalendarPlugin, ConnectedCalendarReader: IntegratedReader } = await import(join(dir, 'plugin.mjs'));
test('host auth failure and calendar deselection immediately clear existing CAS event cache', async () => {
 for (const mode of ['revoked-during-read', 'deselected', 'changed-account']) {
  let state = connection, error = false;
  const bridge = { status: async () => state, authorize: async () => state, eventsPage: async () => {
   if (error) { state = { ...connection, state: 'needs_reauth', error_code: 'authorization_denied' }; throw new Error('calendar: authorization_denied'); }
   return { items: [event] };
  } };
  const values = new Map();
  const host = { call: async (method, p) => {
   if (method === 'host.storage.get') return { value: values.get(p.key) ?? null };
   if (method === 'host.storage.compare_exchange') { const value = values.get(p.key) ?? null; if (p.expected !== value) return { swapped: false, value }; values.set(p.key, p.value); return { swapped: true, value: p.value }; }
   if (method === 'host.session.context') return { product_domain: 'personal', user_space_id: 'space', session_id: 's' };
   throw new Error('unexpected fixture call');
  } };
  const plugin = new PersonalCalendarPlugin({ reader: new IntegratedReader(bridge) });
  plugin.initialize({ product_domain: 'personal', user_space_id: 'space' }, host, { signal: signal() }); await plugin.activate();
  const refresh = () => plugin.invokeService({ service: 'calendar.personal', method: 'refresh', params: { ...query, session_id: 's' }, caller: 'personal' });
  assert.equal((await refresh()).events.length, 1);
  if (mode === 'revoked-during-read') error = true;
  if (mode === 'deselected') state = { ...connection, selected_calendars: [] };
  if (mode === 'changed-account') state = { ...connection, account_id: 'different-subject', connection_id: 'different-connection' };
  const result = await (mode === 'changed-account' ? plugin.invokeService({ service: 'calendar.personal', method: 'refresh', params: { ...query, account_id: 'subject', session_id: 's' }, caller: 'personal' }) : refresh());
  assert.equal(result.events.length, 0, mode); assert.equal(result.freshness, 'empty', mode);
  assert.equal(JSON.stringify([...values.values()]).includes(event.summary), false, mode);
  plugin.shutdown();
 }
});

test('typed authorization denial clears every cached window even when secure status persistence fails', async () => {
 for (const mode of ['refresh-denial', 'cache-only-denial', 'new-runtime-denial', 'network', 'account-changed-network', 'selection-changed-network']) {
  let denied = false, metadata = connection;
  const failure = () => Object.assign(new Error('sanitized host failure'), { code: mode.includes('network') ? 'provider_unavailable' : 'authorization_denied' });
  const bridge = { status: async () => metadata, authorize: async () => { if (denied) throw failure(); return metadata; }, eventsPage: async () => { if (denied) throw failure(); return { items: [event] }; } };
  const values = new Map();
  const host = { call: async (method, p) => {
   if (method === 'host.storage.get') return { value: values.get(p.key) ?? null };
   if (method === 'host.storage.compare_exchange') { const value = values.get(p.key) ?? null; if (p.expected !== value) return { swapped: false, value }; values.set(p.key, p.value); return { swapped: true, value: p.value }; }
   if (method === 'host.session.context') return { product_domain: 'personal', user_space_id: 'space', session_id: 's' };
   throw new Error('unexpected fixture call');
  } };
  const create = async () => { const p = new PersonalCalendarPlugin({ reader: new IntegratedReader(bridge) }); p.initialize({ product_domain: 'personal', user_space_id: 'space' }, host, { signal: signal() }); await p.activate(); return p; };
  let plugin = await create();
  const request = (method, q) => plugin.invokeService({ service: 'calendar.personal', method, params: { ...q, session_id: 's' }, caller: 'personal' });
  const next = { ...query, time_min: '2026-11-02T05:00:00Z', time_max: '2026-11-03T05:00:00Z' };
  assert.equal((await request('refresh', query)).events.length, 1); assert.equal((await request('refresh', next)).events.length, 1);
  denied = true;
  if (mode === 'account-changed-network') metadata = { ...connection, connection_id: 'new-connection', account_id: 'new-account' };
  if (mode === 'selection-changed-network') metadata = { ...connection, selected_calendars: [] };
  if (mode === 'new-runtime-denial') { plugin.shutdown(); plugin = await create(); }
  if (mode === 'refresh-denial' || mode === 'network') {
   const first = await request('refresh', query);
   assert.equal(first.events.length, mode === 'network' ? 1 : 0, mode);
   if (mode === 'network') assert.equal(first.freshness, 'stale');
  }
  const cached = await request('agenda', next);
  assert.equal(cached.events.length, mode === 'network' ? 1 : 0, mode);
  assert.notEqual(cached.freshness, 'fresh', mode);
  if (mode !== 'network') assert.equal(JSON.stringify([...values.values()]).includes(event.summary), false, mode);
  plugin.shutdown();
 }
});
