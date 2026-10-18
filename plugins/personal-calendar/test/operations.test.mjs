// Failure-first write-boundary contracts. All accounts, events, grants and HTTP
// exchanges below are synthetic. No OAuth consent or real Calendar writes occur.
import assert from 'node:assert/strict';
import { after, test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from '../../node_modules/esbuild/lib/main.js';
const dir = await mkdtemp(join(tmpdir(), 'wuu-calendar-writes-')); after(() => rm(dir, { recursive: true, force: true }));
await build({ entryPoints: [new URL('../src/index.ts', import.meta.url).pathname], bundle: true, platform: 'node', format: 'esm', outfile: join(dir, 'calendar.mjs'), alias: { '@wuu/plugin-sdk': new URL('../../../packages/plugin-sdk/src/index.ts', import.meta.url).pathname } });
const { PersonalCalendarPlugin, GoogleCalendarWriter } = await import(join(dir, 'calendar.mjs'));
const now = () => new Date('2026-10-18T12:00:00Z');
const grant = { user_space_id: 'one', account_id: 'account-one', access_token: 'synthetic-write-token', expires_at: '2026-10-18T13:00:00Z', scopes: ['https://www.googleapis.com/auth/calendar.events'] };
const fields = { summary: 'Synthetic dinner', location: 'Synthetic venue', start: { date_time: '2026-10-18T18:00:00-04:00', timezone: 'America/New_York' }, end: { date_time: '2026-10-18T19:00:00-04:00', timezone: 'America/New_York' }, attendees: [{ email: 'guest@example.test', optional: false }], send_updates: 'all' };
const proposal = (overrides = {}) => ({ operation_id: 'dinner-create', kind: 'create', account_id: 'account-one', calendar_id: 'primary', task_id: 'dinner-task', requirements_revision: 1, fields, session_id: 'session-one', ...overrides });
const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
class Host {
  values = new Map(); taskRevision = 1; taskState = 'running'; taskCancelRequested = false;
  async call(method, p) {
    if (method === 'host.session.context') return { session_id: p.session_id, user_space_id: 'one', product_domain: 'personal', agent_id: 'agent-one', parent_session_id: '' };
    if (method === 'host.service.call') { assert.equal(p.service, 'personal.agents'); assert.equal(p.method, 'tasks.get'); return { task: { task_id: 'dinner-task', user_space_id: 'one', agent_id: 'agent-one', requirements_revision: this.taskRevision, state: this.taskState, cancel_requested: this.taskCancelRequested } }; }
    if (p.key !== undefined) assert.ok(Buffer.byteLength(p.key) <= 128, 'host.storage rejects keys longer than 128 bytes');
    const key = `${p.scope}:${p.key}`;
    if (method === 'host.storage.keys') return { keys: [...this.values.keys()].filter(key => key.startsWith(`${p.scope}:`)).map(key => key.slice(p.scope.length + 1)) };
    if (method === 'host.storage.get') return { value: this.values.get(key) ?? null };
    assert.equal(method, 'host.storage.compare_exchange'); const current = this.values.get(key) ?? null; if (current !== p.expected) return { swapped: false, value: current }; this.values.set(key, p.value); return { swapped: true, value: p.value };
  }
}
class Provider {
  events = new Map(); requests = []; writes = 0; mode = 'normal'; busy = []; pause;
  fetch = async (url, init) => {
    const target = new URL(url); this.requests.push({ url: target.toString(), method: init.method, headers: init.headers, body: init.body });
    assert.equal(target.origin, 'https://www.googleapis.com'); assert.equal(init.redirect, 'error'); assert.equal(target.searchParams.has('access_token'), false);
    const id = decodeURIComponent(target.pathname.split('/').at(-1));
    if (init.method === 'GET' && id === 'events') return json({ items: this.busy, timeZone: 'America/New_York' });
    if (init.method === 'GET') return this.events.has(id) ? json(this.events.get(id)) : json({}, 404);
    this.writes++;
    if (this.pause) { this.pause.started.resolve(); await this.pause.release.promise; }
    const body = JSON.parse(init.body);
    if (init.method === 'POST') {
      if (this.events.has(body.id)) return json({}, 409);
      this.events.set(body.id, { ...body, etag: '"created"', status: 'confirmed' });
    } else {
      assert.equal(init.method, 'PATCH'); const current = this.events.get(id); if (!current) return json({}, 404);
      if (init.headers['If-Match'] !== current.etag || this.mode === 'etag-conflict') return json({}, 412);
      this.events.set(id, { ...current, ...body, etag: '"updated"', status: 'confirmed' });
    }
    if (this.mode === 'lost-reply') throw new Error('synthetic-write-token response lost');
    if (this.mode === 'bad-proof') { const event = this.events.get(body.id ?? id); event.summary = 'Different event'; }
    return json(this.events.get(body.id ?? id));
  };
}
async function fixture({ host = new Host(), provider = new Provider(), authorization = async () => grant, writer = true } = {}) {
  const plugin = new PersonalCalendarPlugin({ clock: now, ...(writer ? { writer: new GoogleCalendarWriter({ authorize: authorization, fetch: provider.fetch, clock: now }) } : {}) });
  const lifetime = new AbortController(); plugin.initialize({ product_domain: 'personal', user_space_id: 'one' }, host, { signal: lifetime.signal }); await plugin.activate();
  const service = (method, params = {}) => plugin.invokeService({ service: 'calendar.personal', method, params, caller: 'kernel' });
  const client = async (method, input = {}) => (await plugin.invokeCapability({ capability: 'plugin.client.request', input: { method: `calendar.${method}`, input } })).output.result;
  const confirm = op => client('operations.confirm', { operation_id: op.operation_id, expected_revision: op.revision, preview_digest: op.preview_digest });
  return { host, provider, plugin, lifetime, service, client, confirm };
}

test('readonly default cannot propose writes; models and services cannot confirm even with forged booleans', async () => {
  const bare = await fixture({ writer: false }); await assert.rejects(bare.service('operations.propose', proposal()), /unconnected|not_configured/);
  const f = await fixture(); const op = await f.service('operations.propose', proposal()); assert.equal(op.status, 'proposed'); assert.equal(f.provider.writes, 0);
  assert.equal(op.agent_id, 'agent-one'); assert.equal(op.session_id, 'session-one'); assert.equal(op.user_space_id, 'one');
  await assert.rejects(f.service('operations.confirm', { operation_id: op.operation_id, expected_revision: op.revision, preview_digest: op.preview_digest, confirmed: true }), /client/);
  await assert.rejects(f.plugin.executeTool({ tool_id: 'calendar_confirm', session_id: 'session-one', arguments: { confirmed: true } }), /unknown/);
  await assert.rejects(f.confirm({ ...op, preview_digest: 'forged' }), /preview|confirmation/); assert.equal(f.provider.writes, 0);
});

test('immutable operation ID rejects changed targets and exact confirmation verifies POST with separate GET proof', async () => {
  const f = await fixture(); const op = await f.service('operations.propose', proposal());
  const retry = await f.service('operations.propose', proposal()); assert.equal(retry.preview_digest, op.preview_digest);
  await assert.rejects(f.service('operations.propose', proposal({ fields: { ...fields, summary: 'Other dinner' } })), /conflict/);
  const result = await f.confirm(op); assert.equal(result.status, 'completed'); assert.equal(result.result.source.account_id, 'account-one'); assert.equal(result.result.source.verified_by, 'events.get'); assert.equal(f.provider.writes, 1);
  const post = f.provider.requests.find(r => r.method === 'POST'); const body = JSON.parse(post.body); assert.match(body.id, /^[a-v0-9]{5,1024}$/); assert.equal(new URL(post.url).searchParams.get('sendUpdates'), 'all');
  assert.ok(f.provider.requests.findIndex(r => r.method === 'GET' && r.url.includes(body.id)) < f.provider.requests.indexOf(post)); assert.ok(f.provider.requests.at(-1).method === 'GET');
  assert.equal((await f.confirm(op)).status, 'completed'); assert.equal(f.provider.writes, 1); assert.equal(JSON.stringify([...f.host.values]).includes(grant.access_token), false);
});

test('lost write acknowledgement persists uncertainty and restart reconciles without another mutation', async () => {
  const provider = new Provider(); provider.mode = 'lost-reply'; const f = await fixture({ provider }); const op = await f.service('operations.propose', proposal());
  const uncertain = await f.confirm(op); assert.equal(uncertain.status, 'uncertain'); assert.equal(uncertain.reconciliation_required, true); assert.equal(provider.writes, 1); assert.equal(JSON.stringify(uncertain).includes(grant.access_token), false);
  const next = await fixture({ provider, host: f.host }); const completed = await next.service('operations.reconcile', { operation_id: op.operation_id }); assert.equal(completed.status, 'completed'); assert.equal(provider.writes, 1);
  assert.equal((await next.confirm(op)).status, 'completed'); assert.equal(provider.writes, 1);
});

test('unverified write response cannot complete and reconciliation of absent/mismatched data never retries a write', async () => {
  const f = await fixture(); const op = await f.service('operations.propose', proposal()); f.provider.mode = 'bad-proof';
  const uncertain = await f.confirm(op); assert.equal(uncertain.status, 'uncertain'); assert.equal(uncertain.result, undefined);
  await f.service('operations.reconcile', { operation_id: op.operation_id }); assert.equal(f.provider.writes, 1);
  f.provider.events.clear(); assert.equal((await f.service('operations.reconcile', { operation_id: op.operation_id })).status, 'uncertain'); assert.equal(f.provider.writes, 1);
});

test('account, task revision and new availability conflicts invalidate confirmation before any write', async () => {
  let account = grant; const f = await fixture({ authorization: async () => account }); const op = await f.service('operations.propose', proposal()); account = { ...grant, account_id: 'different' };
  assert.equal((await f.confirm(op)).status, 'failed'); assert.equal(f.provider.writes, 0);
  const changed = await fixture(); const stale = await changed.service('operations.propose', proposal()); changed.host.taskRevision = 2; await assert.rejects(changed.confirm(stale), /requirements|revision/); assert.equal(changed.provider.writes, 0);
  const busy = await fixture(); const available = await busy.service('operations.propose', proposal()); busy.provider.busy = [{ id: 'new-conflict', status: 'confirmed', start: { dateTime: fields.start.date_time }, end: { dateTime: fields.end.date_time } }];
  assert.equal((await busy.confirm(available)).status, 'failed'); assert.equal(busy.provider.writes, 0);
});

test('reschedule uses the original event and ETag with GET-before-PATCH and If-Match', async () => {
  const f = await fixture(); const original = { id: 'existing', etag: '"v1"', status: 'confirmed', summary: fields.summary, location: fields.location, start: { dateTime: fields.start.date_time, timeZone: fields.start.timezone }, end: { dateTime: fields.end.date_time, timeZone: fields.end.timezone }, attendees: fields.attendees, extendedProperties: { private: { preserve: 'this' } } };
  f.provider.events.set('existing', original);
  const changed = { ...fields, start: { ...fields.start, date_time: '2026-10-19T18:00:00-04:00' }, end: { ...fields.end, date_time: '2026-10-19T19:00:00-04:00' } };
  const op = await f.service('operations.propose', proposal({ operation_id: 'dinner-reschedule', kind: 'reschedule', event_id: 'existing', original_event_etag: '"v1"', fields: changed }));
  assert.equal(op.original_event.fields.send_updates, undefined, 'a provider read cannot establish the original notification setting');
  const result = await f.confirm(op); assert.equal(result.status, 'completed'); assert.equal(result.result.event.id, 'existing');
  const patch = f.provider.requests.find(r => r.method === 'PATCH'); assert.equal(patch.headers['If-Match'], '"v1"'); const body = JSON.parse(patch.body); assert.equal(body.summary, undefined); assert.equal(body.attendees, undefined); assert.equal(body.extendedProperties.private.preserve, 'this'); assert.equal(f.provider.writes, 1);
});

test('incomplete attendee reads cannot authorize a reschedule or verify a remote write', async () => {
  const f = await fixture(); f.provider.events.set('existing', { id: 'existing', etag: '"v1"', status: 'confirmed', summary: fields.summary, location: fields.location, start: { dateTime: fields.start.date_time, timeZone: fields.start.timezone }, end: { dateTime: fields.end.date_time, timeZone: fields.end.timezone }, attendees: [], attendeesOmitted: true });
  await assert.rejects(f.service('operations.propose', proposal({ kind: 'reschedule', event_id: 'existing', original_event_etag: '"v1"', fields: { ...fields, attendees: [] } })), /attendees_incomplete/);
  assert.equal(f.provider.writes, 0);
  const provider = new Provider(), original = provider.fetch;
  provider.fetch = async (url, init) => { const response = await original(url, init); if (provider.writes && init.method === 'GET') return json({ ...await response.json(), attendeesOmitted: true }); return response; };
  const created = await fixture({ provider }); const op = await created.service('operations.propose', proposal());
  assert.equal((await created.confirm(op)).status, 'uncertain'); assert.equal(provider.writes, 1);
});

test('stale update ETag and attempted attendee edits fail without overwriting remote events', async () => {
  const f = await fixture(); f.provider.events.set('existing', { id: 'existing', etag: '"v1"', status: 'confirmed', summary: fields.summary, location: fields.location, start: { dateTime: fields.start.date_time, timeZone: fields.start.timezone }, end: { dateTime: fields.end.date_time, timeZone: fields.end.timezone }, attendees: fields.attendees });
  const desired = proposal({ kind: 'reschedule', event_id: 'existing', original_event_etag: '"v1"' });
  await assert.rejects(f.service('operations.propose', { ...desired, fields: { ...fields, attendees: [] } }), /reschedule/);
  const op = await f.service('operations.propose', desired); f.provider.events.get('existing').etag = '"v2"'; const failed = await f.confirm(op); assert.equal(failed.status, 'failed'); assert.equal(f.provider.writes, 0);
});

test('simultaneous confirmations perform one mutation; local cancellation never claims to undo a remote write', async () => {
  const f = await fixture(); const op = await f.service('operations.propose', proposal()); f.provider.pause = { started: deferred(), release: deferred() };
  const one = f.confirm(op); await f.provider.pause.started.promise; const two = f.confirm(op); const running = await f.service('operations.get', { operation_id: op.operation_id });
  const cancelled = await f.client('operations.cancel', { operation_id: op.operation_id, expected_revision: running.revision }); assert.equal(cancelled.status, 'uncertain'); assert.equal(cancelled.cancel_requested, true); f.provider.pause.release.resolve(); await Promise.all([one, two]); assert.equal(f.provider.writes, 1);
  const reconciled = await f.service('operations.reconcile', { operation_id: op.operation_id }); assert.equal(reconciled.status, 'completed'); assert.equal(reconciled.cancel_requested, true);
});

test('failed proof reads after an accepted mutation remain uncertain rather than definite failures', async () => {
  const provider = new Provider(), original = provider.fetch;
  provider.fetch = async (url, init) => init.method === 'GET' && provider.writes > 0 ? json({}, 503) : original(url, init);
  const f = await fixture({ provider }); const op = await f.service('operations.propose', proposal()); const result = await f.confirm(op);
  assert.equal(result.status, 'uncertain'); assert.equal(result.reconciliation_required, true); assert.equal(provider.writes, 1);
});

test('simultaneous first confirmations are idempotent before the mutation claim', async () => {
  const f = await fixture(); const op = await f.service('operations.propose', proposal());
  const results = await Promise.all([f.confirm(op), f.confirm(op), f.confirm(op)]);
  assert.equal(f.provider.writes, 1); assert.ok(results.every(result => ['confirmed', 'running', 'completed'].includes(result.status)));
});

test('restart of an in-flight operation marks uncertainty and never starts a second write', async () => {
  const f = await fixture(); const op = await f.service('operations.propose', proposal()); f.provider.pause = { started: deferred(), release: deferred() };
  const running = f.confirm(op); await f.provider.pause.started.promise; f.lifetime.abort();
  const replacement = await fixture({ host: f.host, provider: f.provider }); const recovered = await replacement.service('operations.get', { operation_id: op.operation_id }); assert.equal(recovered.status, 'uncertain');
  assert.equal((await replacement.confirm(op)).status, 'uncertain'); assert.equal(f.provider.writes, 1);
  f.provider.pause.release.resolve(); await running;
  assert.equal((await replacement.service('operations.reconcile', { operation_id: op.operation_id })).status, 'completed'); assert.equal(f.provider.writes, 1);
});

test('cancelled proposals and readonly grants cannot cause remote mutations', async () => {
  const f = await fixture(); const op = await f.service('operations.propose', proposal());
  assert.equal((await f.client('operations.cancel', { operation_id: op.operation_id, expected_revision: op.revision })).status, 'cancelled');
  await assert.rejects(f.confirm(op), /revision|cancel/); assert.equal(f.provider.writes, 0);
  const readonly = await fixture({ authorization: async () => ({ ...grant, scopes: ['https://www.googleapis.com/auth/calendar.readonly'] }) });
  await assert.rejects(readonly.service('operations.propose', proposal()), /write_scope_required/); assert.equal(readonly.provider.requests.length, 0);
});

test('pending cancellation and a changed task revision during preflight fence the actual write', async () => {
  const cancelled = await fixture(); const op = await cancelled.service('operations.propose', proposal()); cancelled.host.taskCancelRequested = true;
  await assert.rejects(cancelled.confirm(op), /requirements|scope|cancel/); assert.equal(cancelled.provider.writes, 0);
  const host = new Host(), provider = new Provider(), original = provider.fetch; let reads = 0;
  provider.fetch = async (url, init) => { const response = await original(url, init); if (init.method === 'GET' && new URL(url).pathname.endsWith('/events') && ++reads === 2) host.taskRevision = 2; return response; };
  const changed = await fixture({ host, provider }); const prior = await changed.service('operations.propose', proposal());
  assert.equal((await changed.confirm(prior)).status, 'failed'); assert.equal(provider.writes, 0);
});
