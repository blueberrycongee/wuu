// Compiled real component with a minimal DOM port; synthetic data and no network.
import assert from 'node:assert/strict';
import { test, after } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from '../../node_modules/esbuild/lib/main.js';
const dir = await mkdtemp(join(tmpdir(), 'calendar-view-contract-'));
after(() => rm(dir, { recursive: true, force: true }));
await build({ entryPoints: [new URL('../src/connection-view.ts', import.meta.url).pathname], bundle: true, platform: 'node', format: 'esm', outfile: join(dir, 'view.mjs') });
const { mountCalendarConnection } = await import(join(dir, 'view.mjs'));
class Element extends EventTarget {
 constructor(tag) { super(); this.tagName = tag; this.children = []; this.attributes = {}; this.classList = { add() {} }; }
 set textContent(value) { this.text = value; this.children = []; }
 get textContent() { return (this.text ?? '') + this.children.map(c => c.textContent).join(''); }
 append(...children) { this.children.push(...children); }
 replaceChildren(...children) { this.text = ''; this.children = children; }
 setAttribute(key, value) { this.attributes[key] = value; }
 all(predicate) { return [this, ...this.children.flatMap(c => c.all(predicate))].filter(predicate); }
}
globalThis.document = { createElement: tag => new Element(tag) };
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise(setImmediate); };
const deferred = () => { let resolve; return { promise: new Promise(r => resolve = r), resolve: value => resolve(value) }; };
const initial = { connection_id: 'A', user_space_id: 'space', product_domain: 'personal', provider: 'google-calendar', account_id: 'account-A', state: 'connected', selected_calendars: ['calendar-A'], selection_revision: 'selection-A' };
function fixture() {
 const root = new Element('div'); let connection = structuredClone(initial), agendaCalls = 0; const selects = [];
 const host = { status: async () => structuredClone(connection), calendars: async id => [{ id: `calendar-${id}`, summary: `Calendar ${id}`, timeZone: 'UTC' }], select: async (id, ids) => { selects.push([id, ids]); connection = { ...connection, selected_calendars: ids }; }, startAuthorization: async () => { connection = { ...connection, state: 'connected' }; }, disconnect: async () => ({ local_deleted: true, revocation: 'not_requested' }), agenda: async query => { agendaCalls++; return { freshness: 'fresh', source: { ...query, user_space_id: 'space', account_id: connection.account_id, connection_id: connection.connection_id, provider: 'google-calendar', read_only: true, fetched_at: '2026-10-23T00:00:00Z' }, events: [{ id: 'event-A', summary: 'Account A private event', all_day: true }], cancelled_events: [] }; } };
 const view = mountCalendarConnection(root, host, { timezone: 'UTC', now: () => new Date('2026-10-23T00:00:00Z') });
 const click = async text => { const button = root.all(e => e.tagName === 'button' && e.textContent === text)[0]; assert.ok(button, text); assert.equal(button.disabled, false); button.dispatchEvent(new Event('click')); await flush(); };
 const ask = async () => { root.all(e => e.tagName === 'form')[0].dispatchEvent(new Event('submit', { cancelable: true })); await flush(); };
 return { root, host, view, click, ask, selects, set: state => connection = { ...connection, ...state }, calls: () => agendaCalls };
}
test('refresh clears snapshots on identity, selection, and authorization changes without resurrection', async () => {
 for (const change of [{ connection_id: 'B', account_id: 'account-B', selected_calendars: ['calendar-B'] }, { selected_calendars: [] }, { selection_revision: 'new-selection' }, { state: 'needs_reauth' }]) {
  const f = fixture(); await flush(); await f.ask(); assert.match(f.root.textContent, /Account A private event/);
  f.set(change); await f.click('刷新状态');
  assert.doesNotMatch(f.root.textContent, /Account A private event/);
  f.set({ state: 'connected' }); await f.view.refresh(); await flush();
  assert.doesNotMatch(f.root.textContent, /Account A private event/); assert.equal(f.calls(), 1); f.view.destroy();
 }
});
test('failed calendar loading for another account cannot leave previous account choices actionable', async () => {
 const f = fixture(); await flush(); f.set({ connection_id: 'B', account_id: 'account-B', selected_calendars: ['calendar-B'] });
 f.host.calendars = async () => { throw new Error('synthetic outage'); }; await f.click('刷新状态');
 assert.doesNotMatch(f.root.textContent, /Calendar A/); assert.equal(f.selects.length, 0); f.view.destroy();
});
test('external invalidation during a busy reload supersedes old calendar results', async () => {
 const f = fixture(); await flush(); await f.ask(); const pending = deferred(); f.host.calendars = () => pending.promise;
 const button = f.root.all(e => e.tagName === 'button' && e.textContent === '刷新状态')[0]; button.dispatchEvent(new Event('click')); await flush();
 f.set({ state: 'needs_reauth' }); const refreshed = f.view.refresh(); await flush();
 pending.resolve([{ id: 'calendar-A', summary: 'Calendar A', timeZone: 'UTC' }]); await refreshed; await flush();
 assert.match(f.root.textContent, /需要重新授权/); assert.doesNotMatch(f.root.textContent, /Account A private event|Calendar A/); f.view.destroy();
});
test('ordinary network errors preserve same-owner prior results with an explicit stale notice', async () => {
 const f = fixture(); await flush(); await f.ask(); f.host.agenda = async () => { throw new Error('temporary outage'); }; await f.ask();
 assert.match(f.root.textContent, /Account A private event/); assert.ok(f.root.all(e => e.attributes.role === 'alert').length); f.view.destroy();
});
test('late agenda result cannot restore data after invalidation or destroy', async () => {
 for (const action of ['invalidate', 'destroy']) {
  const f = fixture(); await flush(); const pending = deferred(); const original = f.host.agenda;
  f.host.agenda = async query => { const result = await original(query); await pending.promise; return result; };
  const reading = f.ask(); await flush();
  if (action === 'destroy') f.view.destroy(); else { f.set({ state: 'needs_reauth' }); await f.view.refresh(); }
  pending.resolve(); await reading; await flush();
  assert.doesNotMatch(f.root.textContent, /Account A private event/);
  if (action === 'destroy') assert.equal(f.root.textContent, ''); else assert.match(f.root.textContent, /需要重新授权/);
  f.view.destroy();
 }
});
test('same-owner refresh retains a valid snapshot, and stale detached choices cannot select another account', async () => {
 const f = fixture(); await flush(); await f.ask();
 const oldChoice = f.root.all(e => e.tagName === 'input' && e.type === 'checkbox')[0];
 await f.click('刷新状态'); assert.match(f.root.textContent, /Account A private event/);
 f.set({ connection_id: 'B', account_id: 'account-B', selected_calendars: ['calendar-B'] }); await f.view.refresh();
 oldChoice.checked = true; oldChoice.dispatchEvent(new Event('change')); await flush(); assert.equal(f.selects.length, 0);
 f.view.destroy();
});

test('late authorization cleanup failure describes its operation without disabling a newer grant', async () => {
 for (const failure of [{ code: 'credential_cleanup_failed' }, { code: 'cleanup_failed' }, { code: 'provider_unavailable', persistence_failed: true }]) {
  for (const state of ['connected', 'needs_reauth']) {
   const f=fixture();await flush();f.set({state:'unconnected',selected_calendars:[]});await f.view.refresh();
   let reject;f.host.startAuthorization=()=>new Promise((_,r)=>{reject=r;});
   await f.click('连接 Google Calendar');
   f.set({state,connection_id:'B',account_id:'account-B',selected_calendars:['calendar-B'],selection_revision:'selection-B'});
   reject(failure);await flush();
   const alerts=f.root.all(e=>e.attributes.role==='alert');assert.ok(alerts.length,'cleanup fact stays visible');
   // This is the reported false state assertion, not a wording snapshot.
   assert.ok(alerts.every(e=>!e.textContent.includes('连接已停用')),'old operation must not claim current grant disabled');
   assert.equal(f.root.all(e=>e.tagName==='form').length,state==='connected'?1:0);
   if(state==='needs_reauth')assert.ok(f.root.all(e=>e.attributes.role==='status').some(e=>e.textContent.includes('需要重新授权')));
   f.view.destroy();
  }
 }
});
