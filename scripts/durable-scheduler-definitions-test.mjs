#!/usr/bin/env node
// Real scheduler regressions: missing/newer definitions must fence persisted
// timers, child creation/input delivery, and cancellation until code is restored.
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const source = resolve(process.env.WUU_DURABLE_SOURCE || 'examples/plugins/durable-driver');
const { build } = await import(pathToFileURL(join(source, 'node_modules/esbuild/lib/main.js')));
const directory = await mkdtemp(join(tmpdir(), 'durable-scheduler-definitions-'));
try {
  const file = join(directory, 'scheduler.mjs');
  await build({ stdin: { contents: 'export { schedulerTick } from "./orchestration.ts"; export { TaskDefinitions } from "./task-definitions.ts"; export { digest, phases } from "./state.ts";', resolveDir: source, loader: 'ts' }, bundle: true, platform: 'node', format: 'esm', outfile: file });
  const { schedulerTick, TaskDefinitions, digest, phases } = await import(pathToFileURL(file));
  const signal = new AbortController().signal;
  for (const blocked of ['missing', 'newer']) for (const intent of ['timer', 'child', 'cancel']) {
    const session = `${blocked}-${intent}`, run = `${session}-run`, name = `fixture.${session}`;
    const now = new Date().toISOString();
    const state = { schema_version: 1, session_id: session, run_id: run, turn_id: 'original', phase: intent === 'cancel' ? 'completing' : 'waiting', attempt: 1, restart_count: 0, round: 1, task_name: name, task_version: blocked === 'newer' ? 2 : 1, task_input: {} };
    const cp = { contract_version: 1, driver_id: 'example.durable-driver', driver_version: '1.0.0', state };
    const common = { owner: 'example.durable-driver', session_id: session, run_id: run, attempt: 1, created_at: now, updated_at: now, checkpoint_ref: run, turn_ref: 'original' };
    const root = { ...common, id: run, kind: 'run', status: 'waiting', phase: 'waiting' };
    const call = { id: 'spawn', name: 'durable_subagent', arguments: JSON.stringify({ prompt: 'Only the host history holds this input.' }) };
    const child = { ...common, id: `${run}:child`, parent_id: run, kind: 'subagent', status: 'waiting', phase: 'waiting', request_id: `${run}:create`, intent_seq: 1, intent_digest: digest([call]), source_call_id: 'spawn' };
    const timer = { ...common, id: `${run}:sleep`, parent_id: run, kind: 'sleep', status: 'waiting', phase: 'sleeping', wake_at: '2000-01-01T00:00:00.000Z', request_id: `${run}:wake` };
    if (intent === 'cancel') {
      for (const task of [root, child]) Object.assign(task, { status: 'completing', phase: 'completing', canceled: true, completion: { status: 'aborted', error_code: 'task_canceled' } });
      child.child_session_id = `${session}-owned`;
      state.terminal_outcome = 'aborted';
    }
    const documents = new Map([[session, { schema_version: 1, session_id: session, tasks: [root, intent === 'timer' ? timer : child], checkpoints: { [run]: cp }, canceled: false }]]);
    const effects = { create: 0, send: 0, cancel: 0 };
    let revision = 1, childStarted = false;
    const empty = id => ({ schema_version: 1, session_id: id, tasks: [], checkpoints: {}, canceled: false });
    const host = { supports: () => true, async call(method, params) {
      if (method === 'host.service.call') {
        if (params.method === 'read') {
          const keys = params.params.keys;
          const values = keys ? keys.flatMap(key => documents.has(key) ? [{ value: structuredClone(documents.get(key)) }] : []) : [{ value: structuredClone(documents.get(session)) }];
          return { revision, documents: values };
        }
        assert.equal(params.method, 'commit');
        assert.equal(params.params.expected_revision, revision);
        for (const write of params.params.writes) documents.set(write.key, structuredClone(write.value));
        return { revision: ++revision };
      }
      if (method === 'host.session.list') return { sessions: [] };
      if (method === 'host.session.create') {
        effects.create++; const id = `${session}-owned`; documents.set(id, empty(id)); return { session_id: id };
      }
      if (method === 'host.session.history.read') return { records: [{ seq: 1, tool_calls: JSON.stringify([call]) }] };
      if (method === 'host.session.send') {
        effects.send++; if (params.session_id !== session) childStarted = true;
        return { state: 'accepted', session_id: params.session_id, receipt: { receipt_id: params.request_id, read_at: now } };
      }
      if (method === 'host.session.cancel') { effects.cancel++; return {}; }
      if (method === 'host.session.inspect') {
        if (params.session_id === session) return { session: { state: 'idle' }, receipt: { read_at: now } };
        return { session: { state: 'idle' }, ...(childStarted || intent === 'cancel' ? { turn: { state: 'completed', completed_at: now, final_output: 'Child finished.' } } : {}) };
      }
      throw new Error(`Unexpected host method ${method}`);
    } };
    const definition = version => ({ name, version, initial: () => state, phases: Object.fromEntries(phases.map(phase => [phase, async () => {}])), abort: async () => {} });
    let registry = new TaskDefinitions();
    if (blocked === 'newer') registry.register(definition(1));
    const prepare = checkpoint => registry.prepare(host, checkpoint);
    const before = structuredClone(documents.get(session));
    await schedulerTick(host, signal, prepare); await schedulerTick(host, signal, prepare);
    assert.deepEqual(effects, { create: 0, send: 0, cancel: 0 }, `${blocked}/${intent} effects before definition`);
    assert.deepEqual(documents.get(session), before, `${blocked}/${intent} mutated blocked intent`);
    registry = new TaskDefinitions(); registry.register(definition(state.task_version));
    await schedulerTick(host, signal, prepare); await schedulerTick(host, signal, prepare); await schedulerTick(host, signal, prepare);
    const expected = intent === 'timer' ? { create: 0, send: 1, cancel: 0 } : intent === 'child' ? { create: 1, send: 2, cancel: 0 } : { create: 0, send: 0, cancel: 1 };
    assert.deepEqual(effects, expected, `${blocked}/${intent} recovery duplicated effects`);
    const task = documents.get(session).tasks.find(task => task.id === (intent === 'timer' ? timer.id : child.id));
    assert.equal(task.status, intent === 'cancel' ? 'aborted' : 'completed');
  }
  console.log('Passed real scheduler missing/newer definition effect fences and exactly-once recovery.');
} finally { await rm(directory, { recursive: true, force: true }); }
