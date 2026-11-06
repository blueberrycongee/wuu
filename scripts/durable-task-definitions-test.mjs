#!/usr/bin/env node
// Persistence contract failures: missing definitions, downgrade, absent/throwing
// migrations, invalid migrated phases, CAS conflict, and stale host checkpoints.
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const source = resolve(process.env.WUU_DURABLE_SOURCE || 'examples/plugins/durable-driver');
const { build } = await import(pathToFileURL(join(source, 'node_modules/esbuild/lib/main.js')));
const directory = await mkdtemp(join(tmpdir(), 'durable-definitions-'));
try {
  const file = join(directory, 'definitions.mjs');
  await build({ absWorkingDir: source, entryPoints: ['task-definitions.ts'], bundle: true, platform: 'node', format: 'esm', outfile: file });
  const { TaskDefinitions, TaskRegistry } = await import(pathToFileURL(file));
  const state = { schema_version: 1, session_id: 'session', run_id: 'run', turn_id: 'turn', phase: 'ready', attempt: 1, restart_count: 0, round: 0 };
  const checkpoint = state => ({ contract_version: 1, driver_id: 'example.durable-driver', driver_version: '1.0.0', state });
  function storage(initial) {
    let doc = { schema_version: 1, session_id: 'session', tasks: [{ id: 'run', phase: 'ready' }], checkpoints: { run: checkpoint(initial) }, canceled: false };
    let revision = 1, conflicts = 0, commits = 0;
    return {
      get doc() { return structuredClone(doc); },
      get commits() { return commits; },
      conflict() { conflicts++; },
      host: { async call(_name, request) {
        if (request.method === 'read') return { revision, documents: [{ value: structuredClone(doc) }] };
        assert.equal(request.method, 'commit');
        const tx = request.params;
        if (conflicts) { conflicts--; revision++; throw Object.assign(new Error('conflict'), { code: 'state_conflict' }); }
        assert.equal(tx.expected_revision, revision);
        doc = structuredClone(tx.writes[0].value); revision++; commits++;
        return { revision };
      } },
    };
  }
  const definition = (overrides = {}) => ({ name: 'durable.run', version: 1, initial: () => ({ ...state }), phases: { ready: async context => ++context.effects, model_pending: async context => ++context.effects }, abort: async context => --context.effects, ...overrides });
  for (const [label, registered, stored, code] of [
    ['missing', undefined, state, 'missing_task'],
    ['downgrade', definition(), { ...state, task_name: 'durable.run', task_version: 2, task_input: {} }, 'task_too_old'],
    ['missing migration', definition({ version: 2 }), state, 'migration_failed'],
    ['throwing migration', definition({ version: 2, migrate() { throw new Error('bad'); } }), state, 'migration_failed'],
    ['invalid migrated phase', definition({ version: 2, migrate(input, cp) { return { input, checkpoint: { ...cp, phase: 'bad' } }; } }), state, 'migration_failed'],
    ['changed identity', definition({ version: 2, migrate(input, cp) { return { input, checkpoint: { ...cp, session_id: 'other' } }; } }), state, 'migration_failed'],
  ]) {
    const registry = new TaskDefinitions(); if (registered) registry.register(registered);
    const store = storage(stored), before = store.doc;
    const prior = structuredClone(stored);
    assert.equal(registry.blockedReason(checkpoint(stored)), code, `${label}: visible reason`);
    assert.deepEqual(stored, prior, `${label}: inspection mutated checkpoint`);
    assert.deepEqual(store.doc, before, `${label}: inspection persisted state`);
    assert.equal(store.commits, 0, `${label}: inspection committed migration`);
    await assert.rejects(registry.prepare(store.host, checkpoint(stored)), error => error.code === code, label);
    assert.deepEqual(store.doc, before, label); assert.equal(store.commits, 0, label);
  }
  const registry = new TaskDefinitions(); let migrations = 0;
  registry.register(definition({ version: 2, migrate(input, cp, from) {
    assert.equal(from, 1); migrations++; return { input: { migrated: true }, checkpoint: { ...cp, phase: "model_pending", round: cp.round + 1 } };
  } }));
  const store = storage(state); store.conflict();
  const prepared = await registry.prepare(store.host, checkpoint(state));
  assert.equal(prepared.state.task_version, 2); assert.equal(prepared.state.round, 1);
  assert.equal(store.doc.checkpoints.run.state.task_version, 2); assert.equal(store.doc.tasks[0].phase, "model_pending"); assert.equal(store.commits, 1); assert.equal(migrations, 2);
  const stale = await registry.prepare(store.host, checkpoint(state));
  assert.equal(stale.state.round, 1); assert.equal(store.commits, 1); assert.equal(migrations, 2);
  const context = { effects: 0 }; assert.equal(await registry.dispatch(prepared.state, context), 1);
  assert.equal(await registry.abort(prepared.state, context), 0);
  assert.equal(registry.initial('durable.run', {}).task_version, 2);
  // A second definition has its own payload and phases, and uses exactly the
  // registry dispatcher that the builtin driver adapter delegates to.
  const custom = new TaskRegistry();
  custom.register({ name: 'custom.delivery', version: 2,
    initial: input => ({ phase: 'pack', parcel: input.parcel, count: 0 }),
    migrate: (input, cp, from) => {
      assert.equal(from, 1);
      return { input: { parcel: input.legacyParcel }, checkpoint: { phase: 'pack', parcel: cp.box, count: 0 } };
    },
    phases: {
      pack: async context => ({ ...context.record, checkpoint: { ...context.record.checkpoint, phase: 'ship', count: 1 } }),
      ship: async context => ({ ...context.record, checkpoint: { ...context.record.checkpoint, count: 2 } }),
    },
    abort: async context => ({ parcel: context.record.checkpoint.parcel, canceled: true }),
  });
  let customRecord = custom.migrate({ name: 'custom.delivery', version: 1, input: { legacyParcel: 'book' }, checkpoint: { phase: 'legacy', box: 'book' } });
  assert.deepEqual(customRecord.input, { parcel: 'book' });
  customRecord = await custom.dispatch(customRecord, { record: customRecord });
  assert.equal(customRecord.checkpoint.phase, 'ship');
  customRecord = await custom.dispatch(customRecord, { record: customRecord });
  assert.equal(customRecord.checkpoint.count, 2);
  assert.deepEqual(await custom.abort(customRecord, { record: customRecord }), { parcel: 'book', canceled: true });
  assert.equal(custom.initial('custom.delivery', { parcel: 'new' }).checkpoint.phase, 'pack');
  console.log('Passed task definition and atomic migration persistence contracts.');
} finally { await rm(directory, { recursive: true, force: true }); }
