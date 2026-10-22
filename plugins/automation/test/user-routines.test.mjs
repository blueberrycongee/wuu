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
test('simultaneous schedulers use one durable due event and stable callback identity', async () => {
  const host = new Host(); let current = new Date('2026-10-18T11:00:00Z'); const clock = () => current;
  const first = new UserRoutineScheduler(host, 'local', clock), second = new UserRoutineScheduler(host, 'local', clock);
  await first.upsert(create()); current = new Date('2026-10-18T12:01:00Z');
  await Promise.all([first.tick(), second.tick()]); assert.equal(host.deliveries.size, 1);
  await first.tick(); assert.equal(host.attempts, 1); assert.equal((await first.read('routine-one')).status, 'completed');
});
test('callback reply loss remains durable and retries the same event after restart', async () => {
  const host = new Host(); let current = new Date('2026-10-18T11:00:00Z'); const clock = () => current;
  let scheduler = new UserRoutineScheduler(host, 'local', clock); await scheduler.upsert(create()); current = new Date('2026-10-18T12:01:00Z'); host.failReply = true; await scheduler.tick();
  assert.equal((await scheduler.read('routine-one')).pending_event.status, 'pending'); scheduler = new UserRoutineScheduler(host, 'local', clock); await scheduler.tick();
  assert.equal(host.deliveries.size, 1); assert.equal((await scheduler.read('routine-one')).status, 'completed');
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
