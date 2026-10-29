// Explicit synthetic E2E driver. The Go test owns all Google transport and
// credentials; this process receives only the token-free proposed host contract.
import assert from 'node:assert/strict';
import { createInterface } from 'node:readline';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from '../../../plugins/node_modules/esbuild/lib/main.js';
const temporary = await mkdtemp(join(tmpdir(), 'calendar-bridge-e2e-'));
const pending = new Map(); let sequence = 0;
const input = createInterface({ input: process.stdin });
input.on('line', text => { const message = JSON.parse(text); const waiter = pending.get(message.id); if (!waiter) throw new Error('unknown fixture response'); pending.delete(message.id); if (message.error) waiter.reject(Object.assign(new Error('Calendar host failure'), message.error)); else waiter.resolve(message.result); });
const rpc = (method, params = {}) => new Promise((resolve, reject) => { const id = ++sequence; pending.set(id, { resolve, reject }); process.stdout.write(JSON.stringify({ id, method, params }) + '\n'); });
try {
 await build({ stdin: { contents: "export { PersonalCalendarPlugin } from './plugins/personal-calendar/src/index.ts'; export { ConnectedCalendarReader, executeCalendarAgenda, tomorrowQuery } from './plugins/personal-calendar/src/connected-calendar.ts';", resolveDir: process.cwd() }, bundle: true, platform: 'node', format: 'esm', outfile: join(temporary, 'runtime.mjs'), alias: { '@wuu/plugin-sdk': join(process.cwd(), 'packages/plugin-sdk/src/index.ts') } });
 const { PersonalCalendarPlugin, ConnectedCalendarReader, executeCalendarAgenda, tomorrowQuery } = await import(join(temporary, 'runtime.mjs'));
 const connection = await rpc('fixture.connect'); assert.equal(connection.state, 'connected');
 const calendars = await rpc('calendars', { connection_id: connection.connection_id }); assert.equal(calendars.length, 1);
 await rpc('select', { connection_id: connection.connection_id, calendar_ids: [calendars[0].id] });
 const bridge = { status: () => rpc('status'), authorize: () => rpc('authorize'), eventsPage: (id, query) => rpc('eventsPage', { connection_id: id, query }) };
 const reader = new ConnectedCalendarReader(bridge); const storage = new Map(); let plugin;
 const host = { call: async (method, p) => {
  if (method === 'host.session.context') return { product_domain: p.session_id === 'coding-session' ? 'coding' : 'personal', user_space_id: 'fixture-space', session_id: p.session_id, agent_id: 'main' };
  if (method === 'host.storage.get') return { value: storage.get(p.key) ?? null };
  if (method === 'host.storage.compare_exchange') { const value = storage.get(p.key) ?? null; if (value !== p.expected) return { swapped: false, value }; storage.set(p.key, p.value); return { swapped: true, value: p.value }; }
  if (method === 'host.service.call') return plugin.invokeService({ ...p, caller: 'personal' });
  throw new Error('unsupported fixture host method');
 } };
 plugin = new PersonalCalendarPlugin({ reader });
 plugin.initialize({ product_domain: 'personal', user_space_id: 'fixture-space' }, host, { signal: new AbortController().signal }); await plugin.activate();
 const query = tomorrowQuery(calendars[0].id, 'America/New_York', new Date('2026-10-31T16:00:00Z'));
 const ask = async () => JSON.parse((await executeCalendarAgenda(host, 'fixture-space', { tool_id: 'calendar_agenda', session_id: 'fixture-session', arguments: query })).result.content[0].text);
 await rpc('fixture.mode', { mode: 'rate-limit-once' });
 const first = await ask(); assert.equal(first.state, 'ready'); assert.equal(first.events.length, 2); assert.equal(first.cancelled_events.length, 1);
 const cached = await plugin.invokeService({ service: 'calendar.personal', method: 'agenda', params: { ...query, session_id: 'fixture-session' }, caller: 'personal' });
 assert.equal(cached.freshness, 'fresh'); assert.equal(cached.events.length, 2);
 assert.equal(first.events.find(e => e.id === 'day').end.date, '2026-11-02'); assert.equal(first.events.find(e => e.id === 'instance').original_start_time.date_time, '2026-11-01T08:00:00-05:00');
 assert.equal(Date.parse(query.time_max) - Date.parse(query.time_min), 25 * 3600000);
 const beforeShared = await rpc('fixture.stats');
 const shared = await Promise.all([ask(), ask()]); assert.deepEqual(shared[0], shared[1]);
 const afterShared = await rpc('fixture.stats'); assert.equal(afterShared.event_calls - beforeShared.event_calls, 2, 'same-query requests must share the two-page read');
 await assert.rejects(executeCalendarAgenda(host, 'fixture-space', { tool_id: 'calendar_agenda', session_id: 'coding-session', arguments: query }), /scope/);
 await rpc('fixture.expire'); const refreshed = await ask(); assert.equal(refreshed.state, 'ready');
 await rpc('fixture.mode', { mode: 'timeout-once' }); assert.equal((await ask()).state, 'ready');
 await rpc('fixture.mode', { mode: 'recurring-cancelled' }); const changed = await ask(); assert.equal(changed.events.length, 1); assert.equal(changed.cancelled_events.length, 2);
 await rpc('fixture.mode', { mode: 'revoked' }); const revoked = await ask(); assert.equal(revoked.state, 'reconnect_required'); assert.equal(revoked.events.length, 0); assert.equal(revoked.freshness, 'empty');
 assert.equal(JSON.stringify([...storage.values()]).includes('Fixture meeting'), false);
 await plugin.invokeService({ service: 'calendar.personal', method: 'disconnect', params: {}, caller: 'kernel' });
 const disconnected = await rpc('disconnect', { connection_id: connection.connection_id }); assert.equal(disconnected.local_deleted, true); const afterDisconnect = await rpc('fixture.stats'); assert.equal((await ask()).events.length, 0); assert.equal((await rpc('fixture.stats')).event_calls, afterDisconnect.event_calls);
 const status = await rpc('status'); assert.equal(status.state, 'unconnected');
 for (const text of [JSON.stringify(first), JSON.stringify([...storage.values()]), JSON.stringify(connection)]) { assert.equal(text.includes('synthetic-go-only-access'), false); assert.equal(text.includes('synthetic-go-only-refresh'), false); }
 const report = { result: 'passed', external_google: false, system_keychain: false, model_inference: false, user_request: '查看明天日程', window: query, first_events: first.events.length, cancelled_events: first.cancelled_events.length, recurrence_change_events: changed.events.length, revoked_cache: revoked.freshness, local_deleted: disconnected.local_deleted, connection: status.state, checkpoints: ['signed OAuth + real loopback', 'explicit owned calendar selection', 'Go host to Node reader', 'existing plugin CAS cache', 'session-bound model tool execution', '429 retry and page deduplication', '25-hour DST tomorrow', 'refresh rotation', 'timeout retry', 'recurring cancellation', 'first-failure authorization cache purge', 'disconnect deletion', 'Coding denied', 'no token in plugin output'] };
 await writeFile(process.env.WUU_CALENDAR_E2E_REPORT, JSON.stringify(report, null, 2) + '\n');
 await rpc('fixture.finished'); plugin.shutdown();
} finally { input.close(); process.stdin.destroy(); await rm(temporary, { recursive: true, force: true }); }
