// Persistence and concurrency failure cases are defined before the scheduler.
import assert from 'node:assert/strict';
import { test, after } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from '../../node_modules/esbuild/lib/main.js';
const temp = await mkdtemp(join(tmpdir(), 'wuu-routines-'));
await build({ entryPoints: [new URL('../src/user-routines.ts', import.meta.url).pathname], bundle: true, platform: 'node', format: 'esm', outfile: join(temp, 'scheduler.mjs'), alias: { '@wuu/plugin-sdk': new URL('../../../packages/plugin-sdk/src/index.ts', import.meta.url).pathname } });
const { UserRoutineScheduler, createUserAutomationPlugin } = await import(join(temp, 'scheduler.mjs'));
after(() => rm(temp, { recursive: true, force: true }));
class Host {
  values = new Map(); deliveries = new Map(); attempts = 0; failReply = false;
  async call(method, p) {
    const key = `${p.scope}:${p.key}`;
    if (method === 'host.storage.get') return { value: this.values.get(key) ?? null };
    if (method === 'host.storage.keys') return { keys: [...this.values.keys()].filter(k => k.startsWith(`${p.scope}:`)).map(k => k.slice(p.scope.length + 1)) };
    if (method === 'host.storage.compare_exchange') { const value = this.values.get(key) ?? null; if (value !== p.expected) return { swapped: false, value }; this.values.set(key, p.value); return { swapped: true, value: p.value }; }
    assert.equal(method, 'host.service.call'); assert.equal(p.service, 'automation.trigger');
    this.attempts++; this.deliveries.set(p.params.event_id, p.params);
    if (this.failReply) { this.failReply = false; throw new Error('reply lost'); }
    return { accepted: true };
  }
}
const create = (overrides = {}) => ({ binding_id: 'routine-one', request_id: 'create-one', expected_revision: 0, run_at: '2026-10-18T12:00:00Z', timezone: 'UTC', missed_policy: 'run_once', payload: { routine_id: 'one', routine_revision: 1 }, ...overrides });
test('stable upsert rejects conflicting stale writers and recovers identical retries', async () => {
  const host = new Host(), clock = () => new Date('2026-10-18T11:00:00Z'), scheduler = new UserRoutineScheduler(host, 'local', clock);
  const a = await scheduler.upsert(create()); const b = await scheduler.upsert(create()); assert.equal(a.revision, b.revision);
  await assert.rejects(scheduler.upsert(create({ request_id: 'different' })), /revision_conflict/);
  await assert.rejects(scheduler.upsert(create({ run_at: '2026-10-18T13:00:00Z' })), /request_id_conflict/);
});
test('simultaneous schedulers expose one producer-owned event and owner-scoped claims', async () => {
  const host = new Host(); let current = new Date('2026-10-18T11:00:00Z'); const clock = () => current;
  const first = new UserRoutineScheduler(host, 'local', clock), second = new UserRoutineScheduler(host, 'local', clock);
  await first.upsert(create(), 'consumer'); current = new Date('2026-10-18T12:01:00Z');
  await Promise.all([first.tick(), second.tick()]);
  const [binding] = await first.pending('consumer'); assert(binding.pending_event);
  const input = {binding_id:binding.binding_id,event_id:binding.pending_event.event_id,binding_revision:binding.revision};
  await assert.rejects(first.claim({...input,claim_id:'foreign'},'other'), /consumer/);
  const claims = await Promise.all([first.claim({...input,claim_id:'one'},'consumer'),second.claim({...input,claim_id:'two'},'consumer')]);
  assert.equal(claims.filter(value => value.claimed).length,1);
  const claim_id = claims[0].claimed?'one':'two';
  await first.acknowledge({...input,claim_id,accepted:true},'consumer');
  await first.acknowledge({...input,claim_id,accepted:true},'consumer');
  assert.equal((await first.pending('consumer')).length,0);
  assert.equal((await first.read(binding.binding_id)).status,'completed');
  assert.equal(host.attempts,0,'scheduler must not invoke a reverse consumer service');
});
test('lost acknowledgment and restart preserve event identity without redoing an effect', async () => {
  const host = new Host(); let current = new Date('2026-10-18T11:00:00Z'); const clock = () => current;
  let scheduler = new UserRoutineScheduler(host, 'local', clock); await scheduler.upsert(create()); current = new Date('2026-10-18T12:01:00Z'); await scheduler.tick();
  const [binding] = await scheduler.pending('fixture'), event = binding.pending_event;
  const input={binding_id:binding.binding_id,event_id:event.event_id,binding_revision:event.binding_revision,claim_id:'consumer-execution'};
  assert.equal((await scheduler.claim(input,'fixture')).claimed,true);
  await scheduler.acknowledge({...input,accepted:true},'fixture'); // Response deliberately lost.
  scheduler = new UserRoutineScheduler(host, 'local', clock);
  await scheduler.acknowledge({...input,accepted:true},'fixture');
  assert.equal((await scheduler.read(binding.binding_id)).last_event_id,event.event_id);
  assert.equal((await scheduler.pending('fixture')).length,0);
});
test('deferral preserves outbox identity and pause fences stale acknowledgments', async () => {
  const host = new Host(); let current = new Date('2026-10-18T11:00:00Z'); const clock=()=>current;
  const scheduler=new UserRoutineScheduler(host,'local',clock); const original=await scheduler.upsert(create());
  current=new Date('2026-10-18T12:00:00Z'); await scheduler.tick(); const [binding]=await scheduler.pending('fixture');
  const input={binding_id:binding.binding_id,event_id:binding.pending_event.event_id,binding_revision:binding.revision,claim_id:'one'};
  await scheduler.claim(input,'fixture'); await scheduler.acknowledge({...input,defer_until:'2026-10-18T12:01:00Z'},'fixture');
  assert.equal((await scheduler.pending('fixture')).length,0); current=new Date('2026-10-18T12:01:00Z');
  assert.equal((await scheduler.pending('fixture'))[0].pending_event.event_id,input.event_id);
  await scheduler.pause({binding_id:binding.binding_id,expected_revision:original.revision,request_id:'pause'});
  await assert.rejects(scheduler.acknowledge({...input,accepted:true},'fixture'),/stale|revision/);
});
test('pause revision removes due intent and resume never replays missed ticks', async () => {
  const host = new Host(); let current = new Date('2026-10-18T11:00:00Z'); const clock = () => current;
  const scheduler = new UserRoutineScheduler(host, 'local', clock); const original = await scheduler.upsert(create({ run_at: undefined, cron: '* * * * *' }));
  const paused = await scheduler.pause({ binding_id: original.binding_id, expected_revision: original.revision, request_id: 'pause-one' }); current = new Date('2026-10-18T12:01:00Z'); await scheduler.tick(); assert.equal(host.deliveries.size, 0);
  const resumed = await scheduler.upsert(create({ request_id: 'resume-one', expected_revision: paused.revision, run_at: undefined, cron: '* * * * *' })); assert.equal(resumed.next_run_at, '2026-10-18T12:02:00.000Z'); await scheduler.tick(); assert.equal(host.deliveries.size, 0);
});

test('schedule validation is read-only and uses the same schedule contract as upsert', async () => {
  const host = new Host(), clock = () => new Date('2026-10-18T11:00:00Z');
  const scheduler = new UserRoutineScheduler(host, 'local', clock);
  const binding = await scheduler.upsert(create());
  const before = new Map(host.values);
  for (const change of [{ cron: '99 9 * * *', run_at: undefined }, { timezone: 'Invalid/Zone' }, { run_at: 'not-a-date' }, { missed_policy: 'unknown' }]) {
    const input = create(change);
    assert.throws(() => scheduler.validate(input));
    await assert.rejects(scheduler.upsert({ ...input, request_id: 'invalid', expected_revision: binding.revision }));
    assert.deepEqual(host.values, before);
  }
  const schedule = scheduler.validate(create({ run_at: undefined, cron: '0 12 * * *' }));
  assert.equal(schedule.next_run_at, '2026-10-18T12:00:00.000Z');
  assert.deepEqual(host.values, before); assert.equal(host.attempts, 0);
  const plugin = createUserAutomationPlugin();
  const definition = plugin.initialize({ user_space_id: 'local' }, host);
  assert(definition.provided_services[0].methods.some(method => method.name === 'validate'));
  const result = await plugin.invokeService({ service: 'automation.routines', method: 'validate', caller: 'personal', params: { run_at: '2026-10-18T12:00:00Z', timezone: 'UTC' } });
  assert.equal(result.schedule.next_run_at, '2026-10-18T12:00:00.000Z');
  assert.deepEqual(host.values, before); assert.equal(host.attempts, 0);
});

// Admission failures: lifecycle authority leaks into background/foreground work,
// a foreign caller starts the scheduler, or cancellation leaves an active timer.
test('routine scheduling uses admitted background authority and drains on cancellation', async t => {
  const state = new Host(), plugin = createUserAutomationPlugin();
  const lifetime = new AbortController(), background = new AbortController();
  let run, fire, cleared = false, reads = 0;
  t.mock.method(globalThis, 'setInterval', callback => { fire = callback; return 1; });
  t.mock.method(globalThis, 'clearInterval', () => { cleared = true; });
  const scoped = { supports: () => true, async call(method, params) { reads++; return state.call(method, params); } };
  const lifecycle = { supports: () => true, async call(method, input) {
    assert.equal(method, 'host.service.call'); assert.equal(input.service, 'host.background');
    run = plugin.invokeService({ service: input.params.service, method: input.params.method, caller: 'scheduler-owner', params: {} }, scoped, { signal: background.signal });
    return { job_id: 'job', execution_id: 'background' };
  } };
  plugin.initialize({ plugin_id: 'scheduler-owner', user_space_id: 'local' }, lifecycle);
  await assert.rejects(plugin.invokeService({ service: 'automation.routines', method: 'run', caller: 'foreign', params: {} }, scoped, { signal: background.signal }), /owner/);
  await plugin.activate(lifecycle, { signal: lifetime.signal });
  await new Promise(setImmediate);
  assert(reads > 0, 'recovery reads use the background callback');
  background.abort(); await run;
  const count = reads; fire(); await new Promise(setImmediate);
  assert.equal(reads, count); assert.equal(cleared, true);
  // A canceled background does not revoke an independently admitted foreground call.
  await plugin.invokeService({service:'automation.routines',method:'list',caller:'personal',params:{}}, scoped, {signal:new AbortController().signal});
  assert(reads > count);
  lifetime.abort(); await plugin.shutdown();
});
