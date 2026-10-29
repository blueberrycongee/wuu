// Failure-first persistent change-cursor contracts with synthetic snapshots only.
import assert from 'node:assert/strict';
import { after, test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from '../../node_modules/esbuild/lib/main.js';
const temp = await mkdtemp(join(tmpdir(), 'wuu-calendar-changes-')); after(() => rm(temp, { recursive: true, force: true }));
await build({ entryPoints: [new URL('../src/index.ts', import.meta.url).pathname], bundle: true, platform: 'node', format: 'esm', outfile: join(temp, 'calendar.mjs'), alias: { '@wuu/plugin-sdk': new URL('../../../packages/plugin-sdk/src/index.ts', import.meta.url).pathname } });
const { PersonalCalendarPlugin, GoogleCalendarReader } = await import(join(temp, 'calendar.mjs'));
const now = () => new Date('2026-10-18T12:00:00Z');
const query = { calendar_id: 'primary', time_min: '2026-10-18T00:00:00Z', time_max: '2026-10-19T00:00:00Z', timezone: 'UTC' };
const grant = { user_space_id: 'space', account_id: 'account', access_token: 'synthetic-token', expires_at: '2026-10-18T13:00:00Z', scopes: ['https://www.googleapis.com/auth/calendar.readonly'] };
const event = { id: 'dinner', status: 'confirmed', summary: 'Synthetic dinner', etag: '1', updated: '2026-10-18T11:00:00Z', start: { dateTime: '2026-10-18T18:00:00Z' }, end: { dateTime: '2026-10-18T19:00:00Z' }, attendees: [{ email: 'guest@example.test', responseStatus: 'needsAction' }] };
class Host { values = new Map(); async call(method, p) { const key = `${p.scope}:${p.key}`; if (method === 'host.storage.get') return { value: this.values.get(key) ?? null }; assert.equal(method, 'host.storage.compare_exchange'); const value = this.values.get(key) ?? null; if (value !== p.expected) return { swapped: false, value }; this.values.set(key, p.value); return { swapped: true, value: p.value }; } }
async function fixture({ host = new Host(), state = { items: [structuredClone(event)], account: grant, status: 200, requests: 0 } } = {}) {
  const plugin = new PersonalCalendarPlugin({ clock: now, reader: new GoogleCalendarReader({ clock: now, authorize: async () => state.account, fetch: async () => { state.requests++; return new Response(JSON.stringify({ timeZone: 'UTC', items: state.items }), { status: state.status }); } }) });
  plugin.initialize({ product_domain: 'personal', user_space_id: 'space' }, host, { signal: new AbortController().signal }); await plugin.activate(host);
  const call = (method, params = {}) => plugin.invokeService({ service: 'calendar.personal', method, params, caller: 'kernel' }, host);
  return { state, host, plugin, call, refresh: (input = query) => call('refresh', input), changes: (input = {}) => call('changes', { ...query, ...input }) };
}

test('first verified snapshot establishes a baseline and metadata-only refreshes never create changes', async () => {
  const f = await fixture(); assert.equal((await f.changes()).available, false);
  await f.refresh(); const baseline = await f.changes(); assert.equal(baseline.available, true); assert.equal(baseline.baseline, true); assert.deepEqual(baseline.changes, []); assert.ok(baseline.source.generation);
  f.state.items[0].etag = '2'; f.state.items[0].updated = '2026-10-18T12:00:00Z'; await f.refresh(); const unchanged = await f.changes({ cursor: baseline.cursor, account_id: 'account', generation: baseline.source.generation });
  assert.equal(unchanged.cursor, baseline.cursor); assert.deepEqual(unchanged.changes, []); assert.equal(unchanged.baseline, false);
});

test('meaningful changes have stable delivery IDs across repeated reads and host restart', async () => {
  const f = await fixture(); await f.refresh(); const base = await f.changes();
  f.state.items[0].summary = 'Updated dinner'; f.state.items[0].attendees[0].responseStatus = 'accepted'; await f.refresh();
  const delta = await f.changes({ cursor: base.cursor }); assert.equal(delta.changes.length, 1); assert.equal(delta.changes[0].kind, 'updated'); assert.equal(delta.changes[0].calendar_event_id, 'dinner'); assert.notEqual(delta.changes[0].event_id, 'dinner'); assert.equal(delta.changes[0].after.attendees[0].response_status, 'accepted');
  const next = await fixture({ host: f.host, state: f.state }); const replay = await next.changes({ cursor: base.cursor }); assert.equal(replay.changes[0].event_id, delta.changes[0].event_id); assert.equal(replay.cursor, delta.cursor);
  assert.deepEqual((await next.changes({ cursor: delta.cursor })).changes, []);
});

test('cancellation and removal distinguish verified tombstones from disappearance outside a window', async () => {
  const f = await fixture(); await f.refresh(); const base = await f.changes(); f.state.items = [{ id: 'dinner', status: 'cancelled' }]; await f.refresh();
  const cancel = await f.changes({ cursor: base.cursor }); assert.equal(cancel.changes[0].kind, 'cancelled');
  f.state.items = [event]; await f.refresh(); const readded = await f.changes({ cursor: cancel.cursor }); assert.equal(readded.changes[0].kind, 'created');
  f.state.items = []; await f.refresh(); const removed = await f.changes({ cursor: readded.cursor }); assert.equal(removed.changes[0].kind, 'removed');
});

test('failed refresh blocks delivery without advancing cursor or discarding undelivered changes', async () => {
  const f = await fixture(); await f.refresh(); const base = await f.changes(); f.state.items[0].summary = 'Changed'; await f.refresh(); f.state.status = 503; await f.refresh();
  const blocked = await f.changes({ cursor: base.cursor }); assert.equal(blocked.available, false); assert.equal(blocked.cursor, undefined); assert.deepEqual(blocked.changes, []);
  f.state.status = 200; await f.refresh(); assert.equal((await f.changes({ cursor: base.cursor })).changes.length, 1);
});

test('pinned account mismatch stops transport and every scope reset invalidates the old cursor', async () => {
  const f = await fixture(); await f.refresh(); const base = await f.changes(), requests = f.state.requests;
  f.state.account = { ...grant, account_id: 'other' }; await f.refresh({ ...query, account_id: 'account' }); assert.equal(f.state.requests, requests);
  f.state.account = grant; await f.refresh(); const changed = await f.changes({ cursor: base.cursor }); assert.equal(changed.reset_required, true); assert.equal(changed.available, false);
  const fresh = await f.changes(); await f.call('clear-cache'); await f.refresh(); assert.equal((await f.changes({ cursor: fresh.cursor })).reset_required, true);
  const beforeReconnect = await f.changes(); await f.call('disconnect'); await f.refresh({ ...query, reconnect: true }); assert.equal((await f.changes({ cursor: beforeReconnect.cursor })).reset_required, true);
});

test('different query windows and expired bounded history require an explicit fresh baseline', async () => {
  const f = await fixture(); await f.refresh(); const base = await f.changes();
  for (let i = 0; i < 130; i++) { f.state.items[0].summary = `Synthetic revision ${i}`; await f.refresh(); }
  const expired = await f.changes({ cursor: base.cursor }); assert.equal(expired.reset_required, true); assert.equal(expired.reason, 'cursor_expired'); assert.deepEqual(expired.changes, []);
  const latest = await f.changes(); const other = { ...query, time_max: '2026-10-20T00:00:00Z' }; await f.refresh(other); const wrongWindow = await f.call('changes', { ...other, cursor: latest.cursor }); assert.equal(wrongWindow.reset_required, true);
  const wrongAccount = await f.call('changes', { ...other, account_id: 'wrong' }); assert.equal(wrongAccount.available, false); assert.equal(wrongAccount.reason, 'account_scope_mismatch');
});

test('Today and Routine windows preserve independent cursors, scoped status and stable connection generation across restart', async () => {
  const f = await fixture(), today = { ...query, time_max: '2026-10-20T00:00:00Z' };
  await f.refresh(); const routineBase = await f.changes();
  const todaySnapshot = await f.refresh(today); assert.equal(todaySnapshot.source.generation, routineBase.source.generation);
  const todayBase = await f.call('changes', today);
  assert.equal((await f.changes({ cursor: routineBase.cursor })).available, true);
  assert.equal((await f.call('status', query)).source.time_max, routineBase.source.time_max);
  f.state.items[0].summary = 'Updated across both windows'; await f.refresh(today); await f.refresh();
  const routineDelta = await f.changes({ cursor: routineBase.cursor }); const todayDelta = await f.call('changes', { ...today, cursor: todayBase.cursor });
  assert.equal(routineDelta.changes.length, 1); assert.equal(todayDelta.changes.length, 1);
  assert.notEqual(routineDelta.changes[0].event_id, todayDelta.changes[0].event_id, 'delivery identity includes its query window');
  const restart = await fixture({ host: f.host, state: f.state });
  assert.deepEqual(await restart.changes({ cursor: routineBase.cursor }), routineDelta);
  assert.deepEqual(await restart.call('changes', { ...today, cursor: todayBase.cursor }), todayDelta);
});

test('a refresh failure in one window neither stales another nor is repaired by refreshing another', async () => {
  const f = await fixture(), today = { ...query, time_max: '2026-10-20T00:00:00Z' };
  await f.refresh(); const base = await f.changes(); await f.refresh(today); f.state.status = 503; await f.refresh(today);
  assert.equal((await f.changes({ cursor: base.cursor })).available, true);
  assert.equal((await f.call('status', today)).freshness, 'stale');
  f.state.status = 200; await f.refresh();
  assert.equal((await f.call('changes', today)).available, false); assert.equal((await f.call('status', today)).freshness, 'stale');
});

test('window capacity rejects new work without network or eviction, while account resets invalidate every window', async () => {
  const f = await fixture(), windows = Array.from({ length: 8 }, (_, i) => ({ ...query, calendar_id: `calendar-${i}` }));
  const bases = [];
  for (const window of windows) { await f.refresh(window); bases.push(await f.call('changes', window)); }
  const requests = f.state.requests; await assert.rejects(f.refresh({ ...query, calendar_id: 'overflow' }), /window_capacity_exceeded/); assert.equal(f.state.requests, requests);
  for (let i = 0; i < windows.length; i++) { const unchanged = await f.call('changes', { ...windows[i], cursor: bases[i].cursor }); assert.equal(unchanged.available, true); assert.equal(unchanged.cursor, bases[i].cursor); }
  await f.refresh({ ...windows[0], reconnect: true });
  for (let i = 0; i < windows.length; i++) { await f.refresh(windows[i]); const invalid = await f.call('changes', { ...windows[i], cursor: bases[i].cursor }); assert.equal(invalid.available, false); assert.equal(invalid.reset_required, true); }
  const beforeSwitch = await f.call('changes', windows[1]); f.state.account = { ...grant, account_id: 'different-account' }; await f.refresh(windows[0]);
  assert.equal((await f.call('agenda', windows[1])).freshness, 'empty'); await f.refresh(windows[1]); assert.equal((await f.call('changes', { ...windows[1], cursor: beforeSwitch.cursor })).reset_required, true);
  await f.call('disconnect'); for (const window of windows) assert.equal((await f.call('agenda', window)).freshness, 'empty');
});

test('legacy single-window persisted cursors migrate without losing or replaying verified changes', async () => {
  const f = await fixture(); await f.refresh(); const base = await f.changes(); f.state.items[0].summary = 'Pending legacy change'; await f.refresh();
  const expected = await f.changes({ cursor: base.cursor });
  const [key, raw] = [...f.host.values][0], stored = JSON.parse(raw);
  // Materialize the previously shipped single-window representation.
  if (stored.windows) { const window = Object.values(stored.windows)[0]; stored.cache = window.cache; stored.feed = window.feed; delete stored.windows; delete stored.selected_window; }
  f.host.values.set(key, JSON.stringify(stored));
  const restart = await fixture({ host: f.host, state: f.state }); assert.deepEqual(await restart.changes({ cursor: base.cursor }), expected);
  await restart.refresh({ ...query, time_max: '2026-10-20T00:00:00Z' }); assert.deepEqual(await restart.changes({ cursor: base.cursor }), expected);
});

test('complete persisted state stays below the actual host 1 MiB value limit and capacity failure is recoverable', async () => {
  class BoundedHost extends Host {
    async call(method, params) {
      if (method === 'host.storage.compare_exchange' && Buffer.byteLength(params.value) > 1024 * 1024) throw new Error('state value exceeds 1048576 byte limit');
      return super.call(method, params);
    }
  }
  const f = await fixture({ host: new BoundedHost() });
  f.state.items = Array.from({ length: 80 }, (_, i) => ({ ...structuredClone(event), id: `large-${i}`, summary: 'x'.repeat(8000) }));
  await f.refresh(); const baseline = await f.changes(); assert.equal(baseline.available, true);
  const today = { ...query, time_max: '2026-10-20T00:00:00Z' };
  const full = await f.refresh(today); assert.equal(full.state, 'error'); assert.equal(full.error_code, 'window_storage_capacity_exceeded'); assert.equal(full.freshness, 'empty');
  assert.equal((await f.changes({ cursor: baseline.cursor })).available, true);
  assert.ok([...f.host.values.values()].every(value => Buffer.byteLength(value) <= 1024 * 1024));
  f.state.items = [event]; assert.equal((await f.refresh(today)).state, 'ready');
  const restart = await fixture({ host: f.host, state: f.state }); assert.equal((await restart.changes({ cursor: baseline.cursor })).available, true);
});

test('host document capacity rejection restores the previous cache as stale instead of leaving a refreshing claim', async () => {
  class CapacityHost extends Host {
    failNext = false;
    async call(method, params) {
      if (method === 'host.storage.compare_exchange' && this.failNext && JSON.parse(params.value).state === 'ready') { this.failNext = false; throw new Error('plugin storage document exceeds the supported limit'); }
      return super.call(method, params);
    }
  }
  const host = new CapacityHost(), f = await fixture({ host }); await f.refresh(); const baseline = await f.changes();
  f.state.items[0].summary = 'Not yet persisted'; host.failNext = true;
  const failed = await f.refresh(); assert.equal(failed.state, 'error'); assert.equal(failed.error_code, 'cache_persistence_failed'); assert.equal(failed.freshness, 'stale'); assert.equal(failed.events[0].summary, event.summary);
  assert.equal((await f.changes({ cursor: baseline.cursor })).available, false);
  await f.refresh(); assert.equal((await f.changes({ cursor: baseline.cursor })).changes.length, 1);
});
