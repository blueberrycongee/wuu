#!/usr/bin/env node
// Failure-first public runtime contracts: interrupted identity creation, concurrent
// default creation, lost main sessions, stale edits, unapproved specialists,
// durable dispatch, stale/duplicate completion, cancellation, and unknown outcomes.
// Calendar proposal visibility must stay main-scoped and never proxy confirmation
// or cancellation through Personal, including forged client routing parameters.
// Personal-only memory must retain main/worker authority, evidence validation and
// correction routing without an external memory service or duplicate provider.
// Run against source in an isolated JSONL subprocess; fixture implements only host services.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createRequire } from 'node:module';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url));
const { build } = createRequire(join(root, 'plugins/package.json'))('esbuild');
const scratch = await mkdtemp(join(tmpdir(), 'wuu-personal-'));
const evidencePath = resolve(process.env.WUU_PERSONAL_E2E_OUTPUT ?? join(root, '.tmp/e2e/personal-runtime'));
const evidence = { scenarios: [], frames: [], node: process.version, started_at: new Date().toISOString() };
const runtimePath = join(scratch, 'runtime.mjs');
const calendarRuntimePath = join(scratch, 'calendar-runtime.mjs');
const active = new Set();
let UserRoutineScheduler;
class Host {
  bindings = new Map(); storage = new Map(); sessions = new Map(); turns = new Map(); histories = new Map(); calls = []; intercept;
  userInputs = new Map();
  async call(frame) {
    assert.equal(frame.method, 'host.service.call');
    const { service, method, params: p = {} } = frame.params;
    this.calls.push({ service, method, params: p });
    if (this.intercept) { const result = await this.intercept(service, p); if (result !== undefined) return result; }
    const key = `${p.scope}:${p.key}`;
    switch (service) {
      case 'host.file-lock': return method === 'acquire' ? { lock_id: 'personal-fixture-lock' } : { released: true };
      case 'calendar.personal': if (this.calendar) return this.calendar.call('service.invoke', { service, method, caller: 'personal', params: p }); throw new Error('calendar service unavailable');
      case 'automation.routines': {
        if (method === 'validate') return { schedule: new UserRoutineScheduler({ call() { throw new Error('validation must not access storage'); } }, 'validation').validate(p) };
        if (method === 'read') return { binding: this.bindings.get(p.binding_id) };
        const current = this.bindings.get(p.binding_id);
        if (current?.request_id === p.request_id) return { binding: current };
        assert.equal(current?.revision ?? 0, p.expected_revision);
        const binding = { ...current, ...p, revision: (current?.revision ?? 0) + 1, status: method === 'upsert' ? 'active' : method === 'pause' ? 'paused' : 'stopped', next_run_at: p.run_at };
        this.bindings.set(p.binding_id, binding); return { binding };
      }
      case 'host.storage.get': return { value: this.storage.get(key) ?? null };
      case 'host.storage.keys': return { keys: [...this.storage.keys()].filter(k => k.startsWith(`${p.scope}:`)).map(k => k.slice(p.scope.length + 1)) };
      case 'host.storage.compare-exchange': {
        const value = this.storage.get(key) ?? null;
        if (value !== p.expected) return { swapped: false, value };
        if (p.value === null) this.storage.delete(key); else this.storage.set(key, p.value);
        return { swapped: true, value: p.value };
      }
      case 'host.session.create': {
        const session_id = `s-${p.request_id}`, existing = this.sessions.get(session_id);
        if (!existing) this.sessions.set(session_id, { ...p, session_id, state: 'idle' });
        return { session_id, created: !existing };
      }
      case 'host.session.send': {
        assert(this.sessions.has(p.session_id));
        if (p.user_input_request_id) {
          const proof = this.userInputs.get(p.user_input_request_id);
          assert.equal(proof?.prompt, p.input.prompt, 'trusted client input proof missing or mismatched');
          assert.equal(frame.params.execution_id, proof?.execution_id, 'trusted client execution context missing or mismatched');
          assert.deepEqual(proof?.attachments, p.input.attachments, 'trusted client attachments missing or mismatched');
          assert.deepEqual(proof?.relation, p.relation, 'trusted client relation missing or mismatched');
        }
        if (p.presentation?.kind === 'session_message') assert(p.presentation.text && p.presentation.related_session_id && p.presentation.related_session_id !== p.session_id, 'session_message requires a distinct source session');
        if (!this.turns.has(p.request_id)) this.turns.set(p.request_id, { request_id: p.request_id, session_id: p.session_id, turn_id: `t-${p.request_id}`, state: 'running' });
        return this.turns.get(p.request_id);
      }
      case 'host.session.inspect': {
        const session = this.sessions.get(p.session_id); if (!session) throw new Error('session not found');
        const turn = p.request_id ? this.turns.get(p.request_id) : [...this.turns.values()].filter(t => t.session_id === p.session_id && (!p.turn_id || t.turn_id === p.turn_id)).at(-1);
        return { session: { ...session, state: turn?.state ?? 'idle' }, ...(turn ? { turn } : {}) };
      }
      case 'host.session.context': {
        const session = this.sessions.get(p.session_id); if (!session) throw new Error('session not found');
        return { session_id: p.session_id, user_space_id: session.user_space_id, product_domain: session.product_domain, agent_id: session.agent_id, parent_session_id: session.parent_session_id };
      }
      case 'host.session.history.read': {
        assert(p.start_seq >= 1 || p.cursor, 'history read requires start_seq or cursor'); assert((p.limit ?? 0) <= 50, 'history limit exceeds host contract');
        const records = this.histories.get(p.session_id) ?? [], snapshot = p.snapshot_seq ?? records.at(-1)?.seq ?? 0;
        const matching = records.filter(r => (!p.start_seq || r.seq >= p.start_seq) && (!p.end_seq || r.seq <= p.end_seq) && r.seq <= snapshot);
        return { session_id: p.session_id, head_seq: snapshot, snapshot_seq: snapshot, records: matching.slice(0, p.limit ?? 20), has_more: matching.length > (p.limit ?? 20) };
      }
      case 'host.session.history.search': assert(p.limit >= 1 && p.limit <= 25, 'history search limit exceeds public host maximum25'); assert.equal(p.view, 'conversation'); return { matches: [] };
      case 'host.session.attachment.read': {
        const record = (this.histories.get(p.session_id) ?? []).find(record => record.seq === p.seq);
        const attachment = record?.attachments?.find(item => item.kind === p.kind && item.index === p.index && item.sha256 === p.sha256);
        if (!attachment) throw new Error('attachment not found in selected session history');
        const encoded = Buffer.from('attached bytes').toString('base64'), offset = p.offset ?? 0;
        return { data: encoded.slice(offset, offset + 8), offset, total: encoded.length, content_type: attachment.mime_type, name: attachment.name, sha256: attachment.sha256 };
      }
      case 'host.session.cancel': {
        const turns = [...this.turns.values()].filter(t => t.session_id === p.session_id && (!p.turn_id || p.turn_id === t.turn_id) && (!p.queue_id || p.queue_id === t.queue_id) && !['completed', 'failed', 'discarded', 'interrupted'].includes(t.state));
        for (const turn of turns) if (!['completed', 'failed', 'discarded', 'interrupted'].includes(turn.state)) turn.state = 'interrupted';
        return { session_id: p.session_id, cancelled: turns.some(turn => turn.state === 'interrupted') };
      }
      default: throw new Error(`unavailable service ${service}.${method}`);
    }
  }
}
class Runtime {
  pending = new Map(); seq = 0;
  constructor(host, path = runtimePath, pluginID = 'personal') {
    this.host = host;
    this.pluginID = pluginID;
    this.process = spawn(process.execPath, [path], { stdio: ['pipe', 'pipe', 'pipe'] }); active.add(this);
    this.stderr = ''; this.process.stderr.on('data', b => { this.stderr += b; });
    this.process.on('exit', () => { active.delete(this); for (const p of this.pending.values()) p.reject(new Error(this.stderr || 'runtime closed')); });
    createInterface({ input: this.process.stdout }).on('line', line => {
      const frame = JSON.parse(line); evidence.frames.push(frame);
      if (frame.method) host.call(frame).then(result => this.send({ id: frame.id, result }), error => this.send({ id: frame.id, error: { code: 'fixture_error', message: error.message } }));
      else { const p = this.pending.get(frame.id); if (p) { this.pending.delete(frame.id); if (frame.error) p.reject(new Error(frame.error.message)); else p.resolve(frame.result); } }
    });
  }
  send(frame) { this.process.stdin.write(`${JSON.stringify(frame)}\n`); }
  call(method, params) {
    const id = `r-${++this.seq}`;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`timeout ${method}: ${this.stderr}`)), 15000);
      this.pending.set(id, { resolve: v => { clearTimeout(timer); resolve(v); }, reject: e => { clearTimeout(timer); reject(e); } });
      this.send({ id, method, params });
    });
  }
  client(method, input = {}) {
    if (method === 'agents.send') this.host.userInputs.set(input.request_id, { prompt: input.prompt, attachments: input.attachments, relation: input.relation, execution_id: `x-${this.seq}` });
    return this.call('capability.invoke', { capability: 'plugin.client.request', input: { method, input }, execution_id: `x-${this.seq}` }).then(r => r.output.result).finally(() => { if (method === 'agents.send') this.host.userInputs.delete(input.request_id); });
  }
  tool(tool_id, args, session_id, context = {}) { return this.call('tool.execute', { tool_id, arguments: args, session_id, thread_id: session_id, cwd: scratch, call_id: `c-${this.seq}`, execution_id: `e-${this.seq}`, ...context }).then(r => JSON.parse(r.result.content[0].text)); }
  lifecycle(task, state, final_output = 'unverified final output') { return this.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { thread_id: task.execution.session_id, request_id: task.execution.request_id, state, final_output }, execution_id: `l-${this.seq}` }); }
  async start(domain = 'personal') {
    this.definition = await this.call('initialize', { protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 1, plugin_id: this.pluginID, plugin_root: scratch, project_root: scratch, wuu_home: scratch, product_domain: domain, user_space_id: 'local', supported_host_services: ['host.service.call', 'host.storage.get', 'host.storage.keys', 'host.storage.compare_exchange', 'host.session.create', 'host.session.send', 'host.session.inspect', 'host.session.context', 'host.session.cancel', 'host.session.history.read', 'host.session.history.search', 'host.session.attachment.read'] });
    for (const descriptor of this.definition.provided_services ?? []) {
      assert.match(descriptor.name, /^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$/);
      for (const method of descriptor.methods) { assert.match(method.name, /^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$/); assert.match(method.input_schema, /^[a-z][a-z0-9-]*(\.[a-z0-9-]+)+$/); assert.match(method.output_schema, /^[a-z][a-z0-9-]*(\.[a-z0-9-]+)+$/); }
    }
    await this.call('activate'); return this;
  }
  async close() { await this.call('shutdown'); this.process.stdin.end(); }
}
async function scenario(name, fn) { await fn(); evidence.scenarios.push({ name, passed: true }); console.log(`PASS ${name}`); }
try {
  await build({ stdin: { contents: `import plugin from '${join(root, 'plugins/personal/src/index.ts')}'; import {runJSONLRuntime} from '@wuu/plugin-sdk'; await runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout});`, resolveDir: root, loader: 'ts' }, alias: { '@wuu/plugin-sdk': join(root, 'packages/plugin-sdk/src/index.ts') }, bundle: true, platform: 'node', target: 'node22.7', format: 'esm', outfile: runtimePath });
  await build({ stdin: { contents: `import {PersonalCalendarPlugin} from '${join(root, 'plugins/personal-calendar/src/index.ts')}'; import {runJSONLRuntime} from '@wuu/plugin-sdk'; let host; class CalendarFixture extends PersonalCalendarPlugin { initialize(params,value,lifecycle){host=value;return super.initialize(params,value,lifecycle)} } const plugin=new CalendarFixture({reader:{async read(space,query){const {value}=await host.call('host.storage.get',{scope:'user',key:'fixture.calendar.snapshot'});const data=JSON.parse(value);return {source:{...query,provider:'google-calendar',account_id:'integration-account',user_space_id:space,fetched_at:new Date().toISOString(),read_only:true},events:data.events,cancelled_events:data.cancelled_events||[]}}}}); await runJSONLRuntime(plugin,{input:process.stdin,output:process.stdout});`, resolveDir: root, loader: 'ts' }, alias: { '@wuu/plugin-sdk': join(root, 'packages/plugin-sdk/src/index.ts') }, bundle: true, platform: 'node', target: 'node22.7', format: 'esm', outfile: calendarRuntimePath });
  const schedulerPath = join(scratch, 'scheduler.mjs');
  await build({ entryPoints: [join(root, 'plugins/automation/src/user-routines.ts')], alias: { '@wuu/plugin-sdk': join(root, 'packages/plugin-sdk/src/index.ts') }, bundle: true, platform: 'node', format: 'esm', outfile: schedulerPath });
  ({ UserRoutineScheduler } = await import(schedulerPath));
  const host = new Host(); let runtime = await new Runtime(host).start(); let main, specialist, task;
  await scenario('trusted message sources, quoted output, task selection and mutation retries retain one matter', async () => {
    const h = new Host(), r = await new Runtime(h).start();
    const a = (await r.client('agents.ensure_default')).agent, sid = a.main_session_id;
    h.histories.set(sid, [{ seq: 1, role: 'user', origin: 'user', content: 'Task A and task B' }, { seq: 2, role: 'user', origin: 'user', content: 'Later unrelated input' }]);
    const source = { session_id: sid, seq: 1 };
    let aTask = (await r.tool('create_task', { request_id: 'bound-a', title: 'A', goal: 'Original A', message_seq: 2 }, sid, { source_message: source })).task;
    const bTask = (await r.tool('create_task', { request_id: 'bound-b', title: 'B', goal: 'Original B' }, sid, { source_message: source })).task;
    assert.equal(aTask.source.message_seq, 1); assert.equal(bTask.source.message_seq, 1);
    const forgedSource = (await r.client('tasks.create', { agent_id: a.agent_id, request_id: 'unbound-client', title: 'Unbound', goal: 'Must not claim another message', message_seq: 2 })).task;
    assert.equal(forgedSource.source.message_seq, undefined);
    assert.equal((await r.client('agents.history', { agent_id: a.agent_id })).records[1].relation, undefined);
    const projected = await r.client('agents.history', { agent_id: a.agent_id });
    assert.deepEqual(projected.records[0].relation.metadata.task_links.map(link => link.task_id).sort(), [aTask.task_id, bTask.task_id].sort());
    const link = { task_id: aTask.task_id, requirements_revision: 1, execution_session_id: aTask.execution.session_id };
    const relation = { reply_to: source, metadata: { task_links: [link] } };
    await r.client('agents.send', { agent_id: a.agent_id, request_id: 'quote-a', prompt: '  Explain this result\n', relation });
    assert.deepEqual(h.calls.filter(call => call.service === 'host.session.send').at(-1).params.relation, relation);
    assert.equal((await r.client('tasks.get', { task_id: aTask.task_id })).task.requirements_revision, 1, 'quoting never automatically reruns a task');
    h.histories.get(sid).push({ seq: 4, role: 'tool', attachments: [{ kind: 'artifact', index: 0, sha256: 'a'.repeat(64), name: 'result.pdf' }] });
    await r.client('agents.send', { agent_id: a.agent_id, request_id: 'quote-artifact', prompt: 'Explain this file', relation: { reply_to: { session_id: sid, seq: 4 } } });
    h.histories.get(sid).pop();
    await assert.rejects(r.client('agents.send', { agent_id: a.agent_id, request_id: 'quote-a', prompt: 'Different text', relation }), /request_id_conflict/);
    await assert.rejects(r.client('agents.send', { agent_id: a.agent_id, request_id: 'cross-session', prompt: 'Reply', relation: { reply_to: { session_id: bTask.execution.session_id, seq: 1 } } }), /reply|conversation/);
    await assert.rejects(r.client('agents.send', { agent_id: a.agent_id, request_id: 'forged-task', prompt: 'Reply', relation: { metadata: { task_links: [{ task_id: 'task-not-owned', requirements_revision: 1 }] } } }), /task/);
    const edit = { task_id: aTask.task_id, expected_revision: aTask.revision, goal: 'Revised A' };
    aTask = (await r.tool('update_task', edit, sid, { source_message: { session_id: sid, seq: 2 }, message_relation: relation })).task;
    const repeated = (await r.tool('update_task', edit, sid, { source_message: { session_id: sid, seq: 2 }, message_relation: relation })).task;
    assert.equal(repeated.task_id, aTask.task_id); assert.equal(repeated.requirements_revision, 2);
    assert.equal((await r.client('tasks.get', { task_id: bTask.task_id })).task.requirements_revision, 1);
    const context = { source_message: { session_id: sid, seq: 2 }, message_relation: relation };
    await assert.rejects(r.tool('update_task', { task_id: bTask.task_id, expected_revision: bTask.revision, goal: 'Wrong sibling' }, sid, context), /target|relation/);
    let waiting = (await r.tool('report_task', { task_id: aTask.task_id, requirements_revision: 2, state: 'waiting_user', progress: 'Which date?' }, aTask.execution.session_id)).task;
    const delivered = h.calls.filter(call => call.service === 'host.session.send' && call.params.cause === 'personal.task.question').at(-1).params;
    assert.equal(delivered.relation.metadata.task_links[0].question_request_id, waiting.question.request_id);
    assert.deepEqual(delivered.relation.reply_to, source);
    h.histories.get(sid).push({ seq: 3, role: 'assistant', content: 'Which date?', relation: delivered.relation });
    const history = await r.client('agents.history', { agent_id: a.agent_id });
    assert.deepEqual(history.records.at(-1).relation, delivered.relation);
    const answerRelation = { reply_to: { session_id: sid, seq: 3 }, metadata: delivered.relation.metadata };
    await r.client('agents.send', { agent_id: a.agent_id, request_id: 'answer-a', prompt: 'Sunday', relation: answerRelation });
    waiting = (await r.tool('resume_task', { task_id: waiting.task_id, expected_revision: waiting.revision, answer: 'Sunday' }, sid, { source_message: { session_id: sid, seq: 2 }, message_relation: answerRelation })).task;
    assert.equal(waiting.requirements_revision, 3);
    await assert.rejects(r.client('agents.send', { agent_id: a.agent_id, request_id: 'stale-question', prompt: 'Monday', relation: answerRelation }), /question|stale/);
    await assert.rejects(r.client('agents.send', { agent_id: a.agent_id, request_id: 'stripped-question', prompt: 'Monday', relation: { reply_to: answerRelation.reply_to, metadata: { task_links: [{ task_id: waiting.task_id, requirements_revision: 2 }] } } }), /question|relation|stale/);
    await assert.rejects(r.tool('update_task', { task_id: bTask.task_id, expected_revision: bTask.revision, goal: 'Bypass target' }, sid, { source_message: { session_id: sid, seq: 2 }, message_relation: { reply_to: { session_id: sid, seq: 3 }, metadata: { task_links: [] } } }), /question|target|relation/);
    await r.close();
    const restarted = await new Runtime(h).start();
    const restored = await restarted.client('agents.history', { agent_id: a.agent_id });
    assert.deepEqual(restored.records.at(-1).relation, delivered.relation);
    await restarted.close();
  });
  await scenario('idempotent update and resume retries release task serialization before delivery reconciliation', async () => {
    for (const tool of ['update_task', 'resume_task']) for (const state of ['waiting_user', 'completed']) {
      const h = new Host(), r = await new Runtime(h).start();
      const a = (await r.client('agents.ensure_default')).agent, sid = a.main_session_id;
      h.histories.set(sid, [{ seq: 1, role: 'user', origin: 'user', content: 'Continue with the changed requirements' }]);
      let task = (await r.client('tasks.create', { agent_id: a.agent_id, request_id: 'retry', title: 'Retry contract', goal: 'Original' })).task;
      const args = { task_id: task.task_id, expected_revision: task.revision, goal: 'Changed requirements' };
      const context = { source_message: { session_id: sid, seq: 1 } };
      task = (await r.tool(tool, args, sid, context)).task;
      if (state === 'waiting_user') {
        await r.tool('report_task', { task_id: task.task_id, requirements_revision: 2, state, progress: 'Which date?' }, task.execution.session_id);
      } else {
        h.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified result' }]);
        await r.tool('complete_task', { task_id: task.task_id, requirements_revision: 2, summary: 'Verified result', evidence: [{ seq: 1, description: 'Result evidence' }] }, task.execution.session_id);
        h.turns.get(task.execution.request_id).state = 'completed';
        await r.lifecycle(task, 'completed');
      }
      const before = (await r.client('tasks.get', { task_id: task.task_id })).task;
      assert.equal(before.delivery.status, 'submitted');
      const sends = h.calls.filter(call => call.service === 'host.session.send').length;
      const repeated = (await r.tool(tool, args, sid, context)).task;
      assert.equal(repeated.requirements_revision, 2); assert.equal(repeated.state, state);
      assert.equal(repeated.delivery.request_id, before.delivery.request_id);
      assert.equal(h.calls.filter(call => call.service === 'host.session.send').length, sends);
      // A replay racing a stop must not monopolize the task's transition queue.
      const [replay, stopped] = await Promise.all([
        r.tool(tool, args, sid, context),
        r.client('tasks.stop', { task_id: task.task_id, expected_revision: repeated.revision }),
      ]);
      assert.equal(replay.task.requirements_revision, 2);
      assert.equal(stopped.task.state, state === 'completed' ? 'completed' : 'stopped');
      await r.close();
    }
  });
  await scenario('periodic recovery survives a transient host failure and retries on the next tick', async () => {
    const h = new Host(), r = await new Runtime(h).start();
    const a = (await r.client('agents.ensure_default')).agent;
    let failEnumeration, recovered;
    const failedTick = new Promise(resolve => { failEnumeration = resolve; });
    const dispatched = new Promise(resolve => { recovered = resolve; });
    let failSend = true, failKeys = true;
    h.intercept = (service, params) => {
      if (service === 'host.session.send' && params.cause === 'personal.task') {
        if (failSend) { failSend = false; throw new Error('temporary dispatch failure'); }
        recovered();
      }
      if (service === 'host.storage.keys' && failKeys) {
        failKeys = false; failEnumeration(); throw new Error('temporary recovery storage failure');
      }
    };
    // Creation has no enumeration; its durable outbox waits for recovery.
    const task = (await r.client('tasks.create', { agent_id: a.agent_id, request_id: 'recover-tick', title: 'Recovery', goal: 'Retry dispatch after the host recovers' })).task;
    assert.equal(task.reconciliation_required, true);
    let timeout;
    const deadline = new Promise((_, reject) => { timeout = setTimeout(() => reject(new Error('periodic recovery did not retry within two ticks')), 40_000); });
    try {
      const exited = new Promise((_, reject) => r.process.once('exit', code => reject(new Error(`Personal runtime exited during recovery: ${code}; ${r.stderr}`))));
      await Promise.race([failedTick, deadline, exited]);
      await Promise.race([dispatched, deadline, exited]);
      const current = (await r.client('tasks.get', { task_id: task.task_id })).task;
      assert.equal(current.execution.request_id, task.execution.request_id);
      assert.equal(r.process.exitCode, null);
      assert.match(r.stderr, /Personal background recovery failed.*temporary recovery storage failure/);
    } finally { clearTimeout(timeout); }
    await r.close();
  });
  await scenario('Coding initialization has no Personal tools, services, or writes', async () => {
    const codingHost = new Host(), coding = await new Runtime(codingHost).start('coding');
    assert.equal(coding.definition.tools?.length ?? 0, 0); assert.equal(coding.definition.provided_services?.length ?? 0, 0);
    await assert.rejects(coding.client('routines.history', { routine_id: 'foreign' }), /Personal|available|active/i);
    assert.equal(codingHost.calls.length, 0); await coding.close();
  });
  await scenario('concurrent default requests bind exactly one persistent main session', async () => {
    const results = await Promise.all([runtime.client('agents.ensure_default'), runtime.client('agents.ensure_default')]);
    main = results[0].agent; assert.equal(main.main_session_id, results[1].agent.main_session_id); assert.equal(host.sessions.size, 1); assert.equal(main.kind, 'main');
  });
  await scenario('memory authority follows stored main binding, not names or parent absence', async () => {
    const authority = session_id => runtime.call('service.invoke', { service: 'personal.agents', method: 'agents.memory-context', caller: 'memory', params: { session_id } });
    assert.equal((await authority(main.main_session_id)).role, 'main');
    const original = host.sessions.get(main.main_session_id);
    host.sessions.set('unbound-root', { ...original, session_id: 'unbound-root', parent_session_id: undefined });
    host.sessions.set('worker', { ...original, session_id: 'worker', parent_session_id: main.main_session_id });
    host.sessions.set('unknown-agent', { ...original, session_id: 'unknown-agent', agent_id: 'unknown' });
    host.sessions.set('foreign-memory', { ...original, session_id: 'foreign-memory', user_space_id: 'foreign' });
    assert.equal((await authority('unbound-root')).role, 'background');
    assert.equal((await authority('worker')).role, 'background');
    await assert.rejects(authority('unknown-agent'), /agent|found/);
    await assert.rejects(authority('foreign-memory'), /bound|Personal/);
    await assert.rejects(authority('missing-session'), /found/);
    for (const id of ['unbound-root', 'worker', 'unknown-agent', 'foreign-memory']) host.sessions.delete(id);
  });
  await scenario('Personal alone owns memory tools, pre-step, worker evidence and main acceptance', async () => {
    const h = new Host(), p = await new Runtime(h).start();
    const agent = (await p.client('agents.ensure_default')).agent;
    assert.equal(p.definition.provided_services.filter(service => service.name === 'memory.records').length, 1);
    const sources = [{ kind: 'message', ref: 'integration-source', occurred_at: new Date(Date.now() - 60000).toISOString() }];
    const accepted = (await p.tool('personal_memory_create', { key: 'integration-core', expected_revision: 0, content: 'Prefers concise replies', evidence: 'explicit', sources, tier: 'core' }, agent.main_session_id)).record;
    assert.equal(accepted.status, 'active'); assert.equal(accepted.tier, 'core');
    const sourceTime = new Date(Date.now() - 120000).toISOString();
    h.histories.set(agent.main_session_id, [{ seq: 1, role: 'user', origin: 'user', content: 'I usually schedule meetings before lunch', at: sourceTime }]);
    const task = (await p.tool('create_task', { request_id: 'memory-worker', title: 'Research', goal: 'Find relevant context' }, agent.main_session_id, { source_message: { session_id: agent.main_session_id, seq: 1 } })).task;
    assert.equal(task.source.message_binding, 'host_turn');
    const worker = task.execution.session_id;
    const ref = `personal-message:${encodeURIComponent(task.source.session_id)}:${task.source.message_seq}`;
    const mainStep = await p.call('capability.invoke', { capability: 'agent.pre_step', input: { session_id: agent.main_session_id, source_message: { session_id: agent.main_session_id, seq: 1 }, messages: [] } });
    const exposed = mainStep.output.append_messages.flatMap(message => message.content.match(/personal-message:[^\s]+:[1-9][0-9]*/g) ?? []);
    assert(exposed.includes(ref), 'ordinary main input must expose usable host-bound source identity without a reply relation');
    const candidate = (await p.tool('personal_memory_create', { key: 'integration-candidate', expected_revision: 0, content: 'May prefer morning meetings', evidence: 'inferred', sources: [{ ...sources[0], ref }] }, worker)).record;
    assert.equal(candidate.status, 'candidate'); assert.equal(candidate.sources[0].occurred_at, sourceTime);
    const reviewed = await p.tool('personal_memory_evidence', { id: candidate.id }, agent.main_session_id);
    assert.equal(reviewed.sources[0].content, 'I usually schedule meetings before lunch');
    const admitted = (await p.tool('personal_memory_accept', { id: candidate.id, expected_revision: reviewed.record.revision }, agent.main_session_id)).record;
    assert.equal(admitted.status, 'active'); assert.equal(admitted.evidence, 'inferred');
    assert.deepEqual(admitted.sources, candidate.sources); assert.equal(admitted.source_at, sourceTime);
    const resolve = (sourceRef, session_id = agent.main_session_id) => p.call('service.invoke', { service: 'personal.agents', method: 'agents.memory-evidence', caller: 'fixture', params: { session_id, ref: sourceRef } });
    assert.equal((await resolve(ref, worker)).content, undefined, 'workers receive provenance metadata, not source text');
    for (const [i, override] of [{ role: 'assistant' }, { origin: 'plugin' }, { hidden: true }, { read_only: true }, { truncated: true }, { at: 'not-a-date' }, { at: new Date(Date.now() + 3600000).toISOString() }].entries()) {
      const seq = i + 2;
      h.histories.get(agent.main_session_id).push({ seq, role: 'user', origin: 'user', content: 'Not valid evidence', at: sourceTime, ...override });
      await assert.rejects(resolve(`personal-message:${encodeURIComponent(agent.main_session_id)}:${seq}`), /evidence|timestamp/);
    }
    for (const bad of [`personal-message:${encodeURIComponent(agent.main_session_id)}:01`, `personal-message:${encodeURIComponent(agent.main_session_id)}:9007199254740992`, 'personal-message:%73ource:1', 'personal-message:%ZZ:1']) await assert.rejects(resolve(bad), /source|URI/);
    const mainContext = h.sessions.get(agent.main_session_id);
    h.sessions.set('unowned-source', { ...mainContext, session_id: 'unowned-source' });
    h.sessions.set('foreign-source', { ...mainContext, session_id: 'foreign-source', user_space_id: 'other' });
    h.sessions.set('other-identity', { ...mainContext, session_id: 'other-identity', agent_id: 'another-agent' });
    for (const session of ['unowned-source', 'foreign-source', 'other-identity']) await assert.rejects(resolve(`personal-message:${session}:1`), /owned|outside/);
    h.histories.set(worker, [{ seq: 1, role: 'user', origin: 'user', content: 'Original task reply', at: sourceTime }]);
    assert.equal((await resolve(`personal-message:${encodeURIComponent(worker)}:1`)).content, 'Original task reply');

    await assert.rejects(p.tool('personal_memory_forget', { id: accepted.id, expected_revision: 1 }, worker), /main/);
    const forgotten = await p.client('memory.forget', { agent_id: agent.agent_id, id: accepted.id, expected_revision: 1 });
    assert.equal(forgotten.record.status, 'forgotten');
    assert(!h.calls.some(call => call.service === 'memory.records' || call.service === 'personal.agents'), 'built-in memory must not route authority or persistence through another plugin');
    await p.close();
  });
  await scenario('restart, persona edit, and history retain immutable main binding', async () => {
    await runtime.close(); runtime = await new Runtime(host).start();
    const restored = (await runtime.client('agents.ensure_default')).agent; assert.equal(restored.main_session_id, main.main_session_id);
    main = (await runtime.client('agents.update', { agent_id: main.agent_id, expected_revision: main.revision, name: 'River' })).agent;
    await assert.rejects(runtime.client('agents.update', { agent_id: main.agent_id, expected_revision: main.revision - 1, name: 'Stale' }), /revision|conflict/i);
    const prompt = await runtime.call('capability.invoke', { capability: 'agent.pre_step', input: { session_id: main.main_session_id, step_index: 0, messages: [] } }); assert.match(JSON.stringify(prompt), /River/);
    host.histories.set(main.main_session_id, [{ seq: 1, role: 'user', content: 'Hello' }, { seq: 2, role: 'tool', tool_result: 'private' }, { seq: 3, role: 'assistant', content: 'Hi', reasoning_content: 'hidden' }]);
    const history = await runtime.client('agents.history', { agent_id: main.agent_id }); assert.equal(history.records.length, 2); assert(!JSON.stringify(history).includes('private')); assert(!JSON.stringify(history).includes('hidden'));
  });
  await scenario('main name edits validate Unicode, survive restart, and preserve identity and unrelated fields', async () => {
    const before = (await runtime.client('agents.list')).agents[0];
    const renamed = (await runtime.client('agents.update', { agent_id: before.agent_id, expected_revision: before.revision, name: '  🍄  ' })).agent;
    assert.equal(renamed.name, '🍄');
    const unchanged = ({ name, revision, updated_at, ...rest }) => rest;
    assert.deepEqual(unchanged(renamed), unchanged(before));
    assert.equal(renamed.revision, before.revision + 1);
    await runtime.close(); runtime = await new Runtime(host).start();
    assert.deepEqual((await runtime.client('agents.ensure_default')).agent, renamed);
    for (const name of ['', ' \n\t ', 'x'.repeat(65), '🍄'.repeat(65), 42, null]) {
      await assert.rejects(runtime.client('agents.update', { agent_id: renamed.agent_id, expected_revision: renamed.revision, name }), /name/);
      assert.deepEqual((await runtime.client('agents.list')).agents[0], renamed);
    }
    const boundary = (await runtime.client('agents.update', { agent_id: renamed.agent_id, expected_revision: renamed.revision, name: '🍄'.repeat(64) })).agent;
    assert.equal(Array.from(boundary.name).length, 64);
    main = (await runtime.client('agents.update', { agent_id: boundary.agent_id, expected_revision: boundary.revision, name: 'River' })).agent;
  });
  await scenario('name save failures leave durable identity unchanged and competing edits cannot overwrite the winner', async () => {
    const before = (await runtime.client('agents.list')).agents[0];
    host.intercept = service => { if (service === 'host.storage.compare-exchange') throw new Error('name storage unavailable'); };
    await assert.rejects(runtime.client('agents.update', { agent_id: before.agent_id, expected_revision: before.revision, name: 'Not saved' }), /storage unavailable/);
    host.intercept = undefined;
    assert.deepEqual((await runtime.client('agents.list')).agents[0], before);
    let release, reached;
    const held = new Promise(resolve => { release = resolve; });
    const entered = new Promise(resolve => { reached = resolve; });
    host.intercept = async (service, params) => {
      if (service === 'host.storage.compare-exchange' && JSON.parse(params.value).name === 'Losing edit') { reached(); await held; }
    };
    const losing = runtime.client('agents.update', { agent_id: before.agent_id, expected_revision: before.revision, name: 'Losing edit' });
    const rejected = assert.rejects(losing, /revision_conflict/);
    await entered;
    const winner = (await runtime.client('agents.update', { agent_id: before.agent_id, expected_revision: before.revision, name: 'River' })).agent;
    release(); await rejected; host.intercept = undefined;
    assert.equal(winner.revision, before.revision + 1);
    assert.deepEqual((await runtime.client('agents.list')).agents[0], winner);
    await runtime.close(); runtime = await new Runtime(host).start();
    main = (await runtime.client('agents.ensure_default')).agent;
    assert.deepEqual(main, winner);
  });
  await scenario('missing bound session fails without silent replacement', async () => {
    const original = host.sessions.get(main.main_session_id); host.sessions.delete(main.main_session_id);
    await assert.rejects(runtime.client('agents.open', { agent_id: main.agent_id }), /recover|missing|unavailable/i); assert.equal(host.sessions.size, 0); host.sessions.set(main.main_session_id, original);
  });
  await scenario('single main surface preserves historical identities without reopening or creating specialists', async () => {
    specialist = { ...main, agent_id: 'historical-specialist', kind: 'specialist', name: 'Fern', main_session_id: 'historical-conversation' };
    const mainKey = [...host.storage.keys()].find(key => key.endsWith(`agent.${main.agent_id}`));
    host.storage.set(mainKey.replace(main.agent_id, specialist.agent_id), JSON.stringify(specialist));
    host.sessions.set(specialist.main_session_id, { session_id: specialist.main_session_id, product_domain: 'personal', user_space_id: 'local', agent_id: specialist.agent_id, state: 'idle' });
    host.histories.set(specialist.main_session_id, [{ seq: 1, role: 'user', content: 'Historical private conversation' }]);
    assert.deepEqual((await runtime.client('agents.list')).agents.map(agent => agent.agent_id), [main.agent_id]);
    await assert.rejects(runtime.tool('propose_specialist', { name: 'New', responsibility: 'Travel' }, main.main_session_id), /unknown/);
    await assert.rejects(runtime.client('agents.confirm_specialist', { proposal_id: 'old', approved: true }), /unknown/);
    for (const method of ['agents.open', 'agents.history', 'agents.send']) await assert.rejects(runtime.client(method, { agent_id: specialist.agent_id, request_id: 'old-chat', prompt: 'Hello' }), /main Personal conversation/);
    assert.equal(host.histories.get(specialist.main_session_id)[0].content, 'Historical private conversation');
    assert.equal(JSON.parse(host.storage.get(mainKey.replace(main.agent_id, specialist.agent_id))).agent_id, specialist.agent_id);
  });
  await scenario('main history retains exact human text and artifacts without generated user bubbles', async () => {
    const prompt = '  Personal identity revision supplied by the user\n  ', at = '2026-10-18T08:30:00Z';
    const attachment = { kind: 'artifact', index: 0, name: 'result.txt', mime_type: 'text/plain', size: 1, sha256: 'a'.repeat(64) };
    for (const agent of [main]) {
      const previous = host.histories.get(agent.main_session_id);
      const records = [
        { seq: 1, role: 'user', origin: 'user', content: prompt, at },
        { seq: 2, role: 'user', content: 'legacy user input' },
        { seq: 3, role: 'assistant', content: 'visible answer', provider_item_id: 'provider-visible', engine_turn_id: 'engine-visible' },
        { seq: 4, role: 'user', content: prompt, hidden: true, read_only: true, origin: 'plugin' },
        { seq: 5, role: 'user', content: 'generated task result', display_content: 'task title', origin: 'plugin', read_only: true },
        { seq: 6, role: 'user', content: 'internal context', origin: 'internal' },
        { seq: 7, role: 'user', content: 'legacy read-only context', read_only: true },
        { seq: 8, role: 'assistant', content: 'hidden response', hidden: true },
        { seq: 9, role: 'tool', content: 'private tool data', attachments: [attachment] },
        { seq: 10, role: 'tool', hidden: true, attachments: [attachment] },
      ];
      host.histories.set(agent.main_session_id, records);
      const history = await runtime.client('agents.history', { agent_id: agent.agent_id });
      assert.deepEqual(history.records.map(record => record.seq), [1, 2, 3, 9]);
      assert.equal(history.records[0].content, prompt); assert.equal(history.records[0].at, at); assert.equal(history.records[2].provider_item_id, 'provider-visible'); assert.equal(history.records[2].engine_turn_id, 'engine-visible');
      assert.equal(history.records[3].role, 'assistant'); assert.equal(history.records[3].content, ''); assert.deepEqual(history.records[3].attachments, [attachment]);
      const original = await runtime.client('agents.history', { agent_id: agent.agent_id, start_seq: 1, end_seq: 1 });
      assert.equal(original.records[0].content, prompt);
      const hidden = await runtime.client('agents.history', { agent_id: agent.agent_id, start_seq: 4, end_seq: 4 });
      assert.deepEqual(hidden.records, []);
      assert.deepEqual(host.histories.get(agent.main_session_id), records, 'projection must not rewrite durable/provider history');
      if (previous) host.histories.set(agent.main_session_id, previous); else host.histories.delete(agent.main_session_id);
    }
  });
  await scenario('hidden-only recent history recovers an older visible page within its original snapshot', async () => {
    const previous = host.histories.get(main.main_session_id);
    const records = [{ seq: 1, role: 'user', origin: 'user', content: 'original question' }, { seq: 2, role: 'assistant', content: 'original answer' }, ...Array.from({ length: 120 }, (_, index) => ({ seq: index + 3, role: 'user', origin: 'plugin', hidden: true, content: 'internal context' }))];
    host.histories.set(main.main_session_id, records);
    let initialRead = true;
    host.intercept = (service, p) => {
      if (service !== 'host.session.history.read') return;
      if (initialRead) {
        initialRead = false;
        records.push({ seq: 123, role: 'assistant', content: 'later append' });
        return { session_id: p.session_id, head_seq: 122, snapshot_seq: 122, records: [records[0]], has_more: true };
      }
      assert.equal(p.snapshot_seq, 122, 'backward recovery must stay on the original snapshot');
    };
    const history = await runtime.client('agents.history', { agent_id: main.agent_id });
    assert.deepEqual(history.records.map(record => record.seq), [1, 2]); assert.equal(history.snapshot_seq, 122); assert.equal(history.first_seq, 1); assert.equal(history.has_earlier, false);
    host.intercept = undefined;
    host.histories.set(main.main_session_id, records.slice(2, -1));
    const empty = await runtime.client('agents.history', { agent_id: main.agent_id });
    assert.deepEqual(empty.records, []); assert.equal(empty.first_seq, 1); assert.equal(empty.has_earlier, false);
    if (previous) host.histories.set(main.main_session_id, previous); else host.histories.delete(main.main_session_id);
  });
  // A hidden audit tail must not trigger unbounded work on every chat poll.
  // Budget exhaustion must leave an exact earlier-page boundary for the client.
  await scenario('hidden history recovery stops at its scan budget and can continue through earlier messages', async () => {
    const previous = host.histories.get(main.main_session_id);
    const records = [{ seq: 1, role: 'user', origin: 'user', content: 'earlier question' }, { seq: 2, role: 'assistant', content: 'earlier answer' }, ...Array.from({ length: 250 }, (_, index) => ({ seq: index + 3, role: 'user', origin: 'plugin', hidden: true, content: 'internal context' }))];
    host.histories.set(main.main_session_id, records);
    const before = host.calls.length;
    const history = await runtime.client('agents.history', { agent_id: main.agent_id });
    const reads = host.calls.slice(before).filter(call => call.service === 'host.session.history.read');
    assert.equal(reads.length, 5, 'at most four physical pages plus one snapshot lookup');
    assert.deepEqual(history.records, []); assert.equal(history.first_seq, 53); assert.equal(history.has_earlier, true);
    assert.equal(history.snapshot_seq, 252);
    assert(reads.slice(1).every(call => call.params.snapshot_seq === history.snapshot_seq));
    records.push({ seq: 253, role: 'assistant', content: 'later append' });
    const earlier = await runtime.client('agents.history', { agent_id: main.agent_id, end_seq: history.first_seq - 1 });
    assert.deepEqual(earlier.records.map(record => record.seq), [1, 2]);
    assert.equal(earlier.first_seq, 1); assert.equal(earlier.has_earlier, false);
    const narrowBefore = host.calls.length;
    const narrow = await runtime.client('agents.history', { agent_id: main.agent_id, end_seq: 252, limit: 1 });
    assert.equal(host.calls.slice(narrowBefore).filter(call => call.service === 'host.session.history.read').length, 5);
    assert.deepEqual(narrow.records, []); assert.equal(narrow.first_seq, 249); assert.equal(narrow.has_earlier, true);
    if (previous) host.histories.set(main.main_session_id, previous); else host.histories.delete(main.main_session_id);
  });
  await scenario('reply history resolves ancestors outside the main page and paginates the whole discussion', async () => {
    const previous = host.histories.get(main.main_session_id);
    const ref = seq => ({ reply_to: { session_id: main.main_session_id, seq } });
    const rows = [{ seq: 1, role: 'assistant', content: 'discussion root' },
      ...Array.from({ length: 120 }, (_, i) => ({ seq: i + 2, role: 'assistant', content: 'unrelated' })),
      { seq: 122, role: 'user', content: 'follow up', relation: ref(1) },
      { seq: 123, role: 'assistant', content: 'answer', relation: ref(1) },
      { seq: 124, role: 'user', content: 'later question', relation: ref(123) },
      { seq: 125, role: 'assistant', content: 'later answer', relation: ref(123) }];
    host.histories.set(main.main_session_id, rows);
    try {
      const latest = await runtime.client('agents.history', { agent_id: main.agent_id, reply_seq: 125, limit: 2 });
      assert.equal(latest.reply_root_seq, 1);
      assert.deepEqual(latest.records.map(r => r.seq), [124, 125]);
      assert.equal(latest.has_earlier, true);
      const earlier = await runtime.client('agents.history', { agent_id: main.agent_id, reply_seq: 1, end_seq: latest.first_seq - 1, limit: 2 });
      assert.deepEqual(earlier.records.map(r => r.seq), [122, 123]);
      const oldest = await runtime.client('agents.history', { agent_id: main.agent_id, reply_seq: 123, end_seq: earlier.first_seq - 1, limit: 2 });
      assert.deepEqual(oldest.records.map(r => r.seq), [1]);
      assert.equal(oldest.has_earlier, false);
      await assert.rejects(runtime.client('agents.history', { agent_id: main.agent_id, reply_seq: 999 }), /reply.*not found/i);
      assert.deepEqual(host.histories.get(main.main_session_id), rows);
    } finally { if (previous) host.histories.set(main.main_session_id, previous); else host.histories.delete(main.main_session_id); }
  });
  await scenario('background dispatch is durable, hidden, and leaves main chat available', async () => {
    task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'task-one', title: 'Plan supper', goal: 'Research a supper plan' })).task;
    const retry = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'task-one', title: 'Plan supper', goal: 'Research a supper plan' })).task;
    assert.equal(task.task_id, retry.task_id); assert.equal(host.sessions.get(task.execution.session_id).visibility, 'plugin');
    await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'chat-one', prompt: 'Hello meanwhile' });
    assert([...host.turns.values()].some(t => t.session_id === main.main_session_id));
  });
  await scenario('stop reply requires its frozen turn identity and stale retries leave newer replies running', async () => {
    const first = await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'stale-stop-first', prompt: 'First reply' });
    host.turns.get([...host.turns.keys()].find(key => host.turns.get(key).turn_id === first.turn_id)).state = 'completed';
    const second = await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'stale-stop-second', prompt: 'New reply' });
    await assert.rejects(runtime.client('agents.stop_reply', { agent_id: main.agent_id }), /turn_id/);
    const stale = await runtime.client('agents.stop_reply', { agent_id: main.agent_id, turn_id: first.turn_id });
    assert.equal(stale.cancelled, false); assert.equal([...host.turns.values()].find(turn => turn.turn_id === second.turn_id).state, 'running');
    const stopped = await runtime.client('agents.stop_reply', { agent_id: main.agent_id, turn_id: second.turn_id }); assert.equal(stopped.cancelled, true);
    const third = await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'stale-stop-third', prompt: 'Another reply' });
    const retry = await runtime.client('agents.stop_reply', { agent_id: main.agent_id, turn_id: second.turn_id });
    assert.equal(retry.cancelled, false); assert.equal([...host.turns.values()].find(turn => turn.turn_id === third.turn_id).state, 'running');
  });
  await scenario('terminal turn without verified result remains pending reconciliation', async () => {
    await runtime.lifecycle(task, 'completed'); task = (await runtime.client('tasks.get', { task_id: task.task_id })).task;
    assert.notEqual(task.state, 'completed'); assert.equal(task.reconciliation_required, true);
  });
  await scenario('revised requirements reject old completion and deliver verified result once', async () => {
    const previous = task;
    task = (await runtime.client('tasks.update', { task_id: task.task_id, expected_revision: task.revision, goal: 'Research vegetarian supper' })).task;
    await runtime.lifecycle(previous, 'completed'); assert.notEqual((await runtime.client('tasks.get', { task_id: task.task_id })).task.state, 'completed');
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: '{"result":"verified menu source"}' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: task.requirements_revision, summary: 'Vegetarian supper options', evidence: [{ seq: 1, description: 'Menu source' }] }, task.execution.session_id);
    await runtime.lifecycle(task, 'completed'); await runtime.lifecycle(task, 'completed');
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal(task.state, 'completed');
    assert.equal([...host.turns.values()].filter(t => t.request_id === task.delivery.request_id).length, 1);
  });
  await scenario('stop is durable and late completion cannot restart or notify', async () => {
    let stopped = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'stop-one', title: 'Long lookup', goal: 'Look up items' })).task;
    stopped = (await runtime.client('tasks.stop', { task_id: stopped.task_id, expected_revision: stopped.revision })).task;
    assert.equal(stopped.state, 'stopped'); await runtime.lifecycle(stopped, 'completed');
    assert.equal((await runtime.client('tasks.get', { task_id: stopped.task_id })).task.state, 'stopped');
  });
  await scenario('dispatch acknowledgement loss reconciles without duplicate execution', async () => {
    let lost = false; host.intercept = (service, p) => { if (service === 'host.session.send' && p.cause === 'personal.task' && !lost) { lost = true; host.turns.set(p.request_id, { ...p, turn_id: `t-${p.request_id}`, state: 'running' }); throw new Error('lost acknowledgement'); } };
    const pending = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'lost-one', title: 'Recover', goal: 'Recover dispatch' })).task;
    assert.equal(pending.reconciliation_required, true); host.intercept = undefined;
    await runtime.close(); runtime = await new Runtime(host).start();
    const recovered = (await runtime.client('tasks.get', { task_id: pending.task_id })).task; assert.equal(recovered.execution.request_id, pending.execution.request_id); assert.equal(recovered.state, 'running');
  });
  await scenario('stop during interrupted session creation never sends or restarts a task', async () => {
    let entered, release;
    const reached = new Promise(resolve => { entered = resolve; });
    const held = new Promise(resolve => { release = resolve; });
    host.intercept = async (service, p) => { if (service === 'host.session.create' && p.parent_session_id) { entered(); await held; } };
    const creating = runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'create-stop', title: 'Interrupt creation', goal: 'Do not start after stop' });
    await reached;
    const pending = (await runtime.client('tasks.list')).tasks.find(t => t.request_id === 'create-stop');
    await runtime.client('tasks.stop', { task_id: pending.task_id, expected_revision: pending.revision });
    release(); const settled = (await creating).task; host.intercept = undefined;
    assert.equal(settled.state, 'stopped'); assert(!host.turns.has(settled.execution.request_id));
  });
  await scenario('requirements revision during creation cannot stop or complete the replacement', async () => {
    let entered, release, first = true;
    const reached = new Promise(resolve => { entered = resolve; });
    const held = new Promise(resolve => { release = resolve; });
    host.intercept = async (service, p) => { if (service === 'host.session.create' && p.parent_session_id && first) { first = false; entered(); await held; } };
    const creating = runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'create-edit', title: 'Revise creation', goal: 'Old requirement' });
    await reached;
    const pending = (await runtime.client('tasks.list')).tasks.find(t => t.request_id === 'create-edit');
    const edited = (await runtime.client('tasks.update', { task_id: pending.task_id, expected_revision: pending.revision, goal: 'New requirement' })).task;
    release(); await creating; host.intercept = undefined;
    const current = (await runtime.client('tasks.get', { task_id: pending.task_id })).task;
    assert.equal(current.state, 'running'); assert.equal(current.requirements_revision, 2); assert.equal(current.execution.request_id, edited.execution.request_id);
    assert(!host.turns.has(pending.execution.request_id));
  });
  await scenario('interrupted main creation retries the original stable session request', async () => {
    const isolated = new Host(); let once = true;
    isolated.intercept = (service, p) => { if (service === 'host.session.create' && once) { once = false; const session_id = `s-${p.request_id}`; isolated.sessions.set(session_id, { ...p, session_id, state: 'idle' }); throw new Error('creation response lost'); } };
    let r = await new Runtime(isolated).start(); await assert.rejects(r.client('agents.ensure_default'), /lost/); await r.close();
    isolated.intercept = undefined; r = await new Runtime(isolated).start(); const restored = (await r.client('agents.ensure_default')).agent;
    assert.equal(isolated.sessions.size, 1); assert(isolated.sessions.has(restored.main_session_id)); await r.close();
  });
  await scenario('question outbox survives lost acknowledgement, main answer resumes same task, targeted stop leaves siblings running', async () => {
    let waiting = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'waiting-one', title: 'Choose a date', goal: 'Wait for the user to choose Sunday' })).task;
    const sibling = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'waiting-independent', title: 'Independent goal', goal: 'Keep this separate' })).task;
    assert.notEqual(waiting.task_id, sibling.task_id);
    let lost = false;
    host.intercept = (service, p) => {
      if (service === 'host.session.send' && p.cause === 'personal.task.question' && !lost) {
        lost = true; host.turns.set(p.request_id, { request_id: p.request_id, session_id: p.session_id, turn_id: `t-${p.request_id}`, state: 'running' }); throw new Error('question acknowledgement lost');
      }
    };
    waiting = (await runtime.tool('report_task', { task_id: waiting.task_id, requirements_revision: waiting.requirements_revision, state: 'waiting_user', progress: 'Which Sunday works?' }, waiting.execution.session_id)).task;
    assert.equal(waiting.state, 'waiting_user'); assert.equal(waiting.question.text, 'Which Sunday works?');
    const deliveryID = waiting.delivery.request_id;
    assert.equal(host.turns.get(deliveryID).session_id, main.main_session_id);
    host.intercept = undefined; await runtime.lifecycle(waiting, 'completed');
    await runtime.close(); runtime = await new Runtime(host).start();
    waiting = (await runtime.client('tasks.get', { task_id: waiting.task_id })).task;
    assert.equal(waiting.delivery.request_id, deliveryID);
    assert.equal(host.calls.filter(call => call.service === 'host.session.send' && call.params.request_id === deliveryID).length, 1);
    await assert.rejects(runtime.tool('resume_task', { task_id: waiting.task_id, expected_revision: waiting.revision }, main.main_session_id), /answer/);
    const old = waiting;
    waiting = (await runtime.tool('resume_task', { task_id: waiting.task_id, expected_revision: waiting.revision, answer: 'Next Sunday, October 25' }, main.main_session_id)).task;
    assert.equal(waiting.task_id, old.task_id); assert.equal(waiting.requirements_revision, old.requirements_revision + 1); assert.match(waiting.goal, /October 25/); assert.equal(waiting.question, undefined);
    assert.equal(host.turns.get(deliveryID).state, 'interrupted');
    await assert.rejects(runtime.tool('report_task', { task_id: waiting.task_id, requirements_revision: old.requirements_revision, state: 'running', progress: 'stale' }, old.execution.session_id), /stale|current/);
    await assert.rejects(runtime.tool('stop_task', { expected_revision: waiting.revision }, main.main_session_id), /task_id/);
    await runtime.tool('stop_task', { task_id: waiting.task_id, expected_revision: waiting.revision }, main.main_session_id);
    assert.equal((await runtime.client('tasks.get', { task_id: sibling.task_id })).task.state, 'running');
    assert.equal(host.turns.get(sibling.execution.request_id).state, 'running');
  });
  await scenario('question delivery waits for durable output and recovers without a stale error', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'question-receipt-race', title: 'Receipt race', goal: 'Ask before proceeding' })).task;
    task = (await runtime.tool('report_task', { task_id: task.task_id, requirements_revision: 1, state: 'waiting_user', progress: 'Which date?' }, task.execution.session_id)).task;
    const receipt = host.turns.get(task.delivery.request_id);
    receipt.state = 'completed';
    let durable = false;
    host.intercept = (service, params) => {
      if (service !== 'host.session.inspect' || params.request_id !== receipt.request_id) return;
      return { session: host.sessions.get(main.main_session_id), turn: { ...receipt, ...(durable ? { final_output: 'Which date?' } : {}) }, ...(params.wait === 'terminal' && !durable ? { timed_out: true } : {}) };
    };
    try {
      task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task;
      assert.equal(task.delivery.status, 'submitted', 'live completed snapshot is not proof of empty output');
      assert.equal(task.error, undefined);
      durable = true;
      task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task;
      assert.equal(task.delivery.status, 'delivered'); assert.equal(task.error, undefined);
    } finally { host.intercept = undefined; }
    let recovered = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'question-receipt-recovery', title: 'Recover receipt', goal: 'Ask once' })).task;
    recovered = (await runtime.tool('report_task', { task_id: recovered.task_id, requirements_revision: 1, state: 'waiting_user', progress: 'Which date?' }, recovered.execution.session_id)).task;
    const input = { thread_id: main.main_session_id, request_id: recovered.delivery.request_id, state: 'completed' };
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input });
    recovered = (await runtime.client('tasks.get', { task_id: recovered.task_id })).task;
    assert.equal(recovered.delivery.status, 'failed'); assert.match(recovered.error, /no visible response/);
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { ...input, final_output: 'Which date?' } });
    recovered = (await runtime.client('tasks.get', { task_id: recovered.task_id })).task;
    assert.equal(recovered.delivery.status, 'delivered'); assert.equal(recovered.error, undefined);
  });
  await scenario('failed question delivery retries explicitly without repeating execution and stop cancels only its question', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'question-retry', title: 'Retry question', goal: 'Ask before proceeding' })).task;
    task = (await runtime.tool('report_task', { task_id: task.task_id, requirements_revision: 1, state: 'waiting_user', progress: 'Which city?' }, task.execution.session_id)).task;
    const failedID = task.delivery.request_id;
    host.turns.get(failedID).state = 'failed';
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { thread_id: main.main_session_id, request_id: failedID, state: 'failed', error: 'model unavailable' } });
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task;
    assert.equal(task.delivery.status, 'failed');
    const executionSends = host.calls.filter(call => call.service === 'host.session.send' && call.params.request_id === task.execution.request_id).length;
    task = (await runtime.tool('retry_task_delivery', { task_id: task.task_id, expected_revision: task.revision }, main.main_session_id)).task;
    assert.notEqual(task.delivery.request_id, failedID); assert.equal(task.previous_deliveries.at(-1).request_id, failedID); assert.equal(task.delivery.attempt, 1);
    assert.equal(host.calls.filter(call => call.service === 'host.session.send' && call.params.request_id === task.execution.request_id).length, executionSends);
    await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'unrelated-main-reply', prompt: 'Another topic' });
    const foreground = [...host.turns.values()].at(-1);
    task = (await runtime.tool('stop_task', { task_id: task.task_id, expected_revision: task.revision }, main.main_session_id)).task;
    assert.equal(task.state, 'stopped'); assert.equal(foreground.state, 'running'); assert.equal(host.turns.get(task.previous_deliveries.at(-1).request_id).state, 'interrupted');
  });
  await scenario('historical task result and execution failure return to the unique main conversation', async () => {
    let legacy = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'legacy-task-route', title: 'Historical task', goal: 'Preserve provenance' })).task;
    const key = [...host.storage.keys()].find(key => key.endsWith(`task.${legacy.task_id}`));
    legacy.agent_id = specialist.agent_id; host.storage.set(key, JSON.stringify(legacy)); host.sessions.get(legacy.execution.session_id).agent_id = specialist.agent_id;
    host.histories.set(legacy.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'verified legacy result' }]);
    await runtime.tool('complete_task', { task_id: legacy.task_id, requirements_revision: 1, summary: 'Preserved legacy result', evidence: [{ seq: 1, description: 'Legacy evidence' }] }, legacy.execution.session_id);
    await runtime.lifecycle(legacy, 'completed'); legacy = (await runtime.client('tasks.get', { task_id: legacy.task_id })).task;
    assert.equal(legacy.agent_id, specialist.agent_id); assert.equal(host.turns.get(legacy.delivery.request_id).session_id, main.main_session_id);
    assert((await runtime.client('tasks.list', { agent_id: main.agent_id })).tasks.some(task => task.task_id === legacy.task_id));
    let failed = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'failure-route', title: 'Failed lookup', goal: 'Tell the user if execution fails' })).task;
    await runtime.lifecycle(failed, 'failed'); failed = (await runtime.client('tasks.get', { task_id: failed.task_id })).task;
    assert.equal(failed.state, 'failed'); assert.equal(failed.delivery.kind, 'failure'); assert.equal(host.turns.get(failed.delivery.request_id).session_id, main.main_session_id);
  });
  await scenario('pending legacy result migrates to main only after its old queued delivery is cancelled', async () => {
    let legacy = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'legacy-outbox-route', title: 'Existing outbox', goal: 'Preserve its pending delivery' })).task;
    const key = [...host.storage.keys()].find(key => key.endsWith(`task.${legacy.task_id}`));
    const oldRequest = 'personal-result-legacy-pending';
    legacy = { ...legacy, agent_id: specialist.agent_id, state: 'completed', execution: { ...legacy.execution, finished_state: 'completed' }, result: { summary: 'Historical result', evidence: [], requirements_revision: 1, verified_at: new Date().toISOString() }, delivery: { request_id: oldRequest, status: 'submitted' } };
    host.storage.set(key, JSON.stringify(legacy));
    host.turns.set(oldRequest, { request_id: oldRequest, session_id: specialist.main_session_id, turn_id: 'legacy-delivery-turn', state: 'queued' });
    legacy = (await runtime.client('tasks.reconcile', { task_id: legacy.task_id })).task;
    assert.equal(host.turns.get(oldRequest).state, 'interrupted'); assert.equal(legacy.previous_deliveries[0].session_id, specialist.main_session_id);
    assert.notEqual(legacy.delivery.request_id, oldRequest); assert.equal(host.turns.get(legacy.delivery.request_id).session_id, main.main_session_id);
    assert.equal((await runtime.client('agents.ensure_default')).agent.main_session_id, main.main_session_id);
  });
  await scenario('Calendar proposal list stays main-scoped and exposes no Personal confirmation or cancellation route', async () => {
    const methods = runtime.definition.provided_services.find(service => service.name === 'personal.agents').methods.map(method => method.name);
    assert(methods.includes('calendar.operations.list'));
    assert(!methods.includes('calendar.operations.confirm')); assert(!methods.includes('calendar.operations.cancel'));
    const operations = [{ operation_id: 'pending-calendar-proposal', status: 'proposed', revision: 1, preview_digest: 'immutable-preview' }];
    let forwarded;
    host.intercept = (service, params) => { if (service === 'calendar.personal') { forwarded = params; return { operations }; } };
    assert.deepEqual(await runtime.client('calendar.operations.list', { agent_id: main.agent_id, session_id: specialist.main_session_id }), { operations });
    assert.equal(forwarded.session_id, main.main_session_id); assert(!('agent_id' in forwarded));
    assert.equal(host.calls.filter(call => call.service === 'calendar.personal').at(-1).method, 'operations.list');
    await assert.rejects(runtime.client('calendar.operations.list', { agent_id: specialist.agent_id }), /main Personal conversation/);
    const callsBefore = host.calls.filter(call => call.service === 'calendar.personal').length;
    for (const method of ['calendar.operations.confirm', 'calendar.operations.cancel']) {
      await assert.rejects(runtime.client(method, { operation_id: operations[0].operation_id, expected_revision: 1, preview_digest: 'immutable-preview', confirmed: true }), /unknown Personal client method/);
      await assert.rejects(runtime.call('service.invoke', { service: 'personal.agents', method, caller: 'personal-calendar', params: { session_id: main.main_session_id, confirmed: true } }), /unknown Personal client method/);
    }
    assert.equal(host.calls.filter(call => call.service === 'calendar.personal').length, callsBefore);
    host.intercept = undefined;
    await assert.rejects(runtime.client('calendar.operations.list'), /calendar service unavailable/);
    const calendar = await new Runtime(host, calendarRuntimePath, 'personal-calendar').start(); host.calendar = calendar;
    assert.deepEqual(await runtime.client('calendar.operations.list'), { operations: [] });
    assert.deepEqual(await runtime.call('service.invoke', { service: 'personal.agents', method: 'calendar.operations.list', caller: 'personal-calendar', params: {} }), { operations: [] });
    for (const method of ['operations.confirm', 'operations.cancel']) {
      await assert.rejects(calendar.call('service.invoke', { service: 'calendar.personal', method, caller: 'personal', params: { session_id: main.main_session_id, expected_revision: 1, confirmed: true } }), /trusted user client capability/);
    }
    await calendar.close(); host.calendar = undefined;
  });
  await scenario('memory correction uses the bound main session and ignores forged client routing fields', async () => {
    const original = (await runtime.tool('personal_memory_create', { key: 'correction-routing', expected_revision: 0, content: 'Original preference', evidence: 'explicit', sources: [{ kind: 'message', ref: 'correction-source', occurred_at: new Date(Date.now() - 60000).toISOString() }] }, main.main_session_id)).record;
    const corrected = (await runtime.client('memory.correct', { agent_id: main.agent_id, request_id: 'edit-one', id: original.id, expected_revision: 1, content: 'Updated preference' })).record;
    assert.equal(corrected.updated_by_agent_id, main.agent_id); assert.equal(corrected.evidence, 'explicit'); assert.equal(corrected.sources[0].kind, 'user_edit');
    const routed = (await runtime.client('memory.correct', { agent_id: main.agent_id, session_id: 'forged-main', request_id: 'edit-two', id: original.id, expected_revision: 2, content: 'Bound correction' })).record;
    assert.equal(routed.updated_by_agent_id, main.agent_id);
    assert.equal((await runtime.client('memory.read', { agent_id: main.agent_id, id: original.id })).record.revision, 3);
  });
  await scenario('queued result discarded on shutdown is retried using its original delivery identity', async () => {
    let queued = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'queued-result', title: 'Queued result', goal: 'Verify result delivery recovery' })).task;
    host.histories.set(queued.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'verified outcome' }]);
    await runtime.tool('complete_task', { task_id: queued.task_id, requirements_revision: 1, summary: 'Verified result', evidence: [{ seq: 1, description: 'Outcome' }] }, queued.execution.session_id);
    host.intercept = (service, p) => { if (service === 'host.session.send' && p.cause === 'personal.task.result') { const turn = { request_id: p.request_id, session_id: p.session_id, state: 'queued' }; host.turns.set(p.request_id, turn); return turn; } };
    await runtime.lifecycle(queued, 'completed'); host.intercept = undefined;
    queued = (await runtime.client('tasks.get', { task_id: queued.task_id })).task;
    const deliveryID = queued.delivery.request_id; host.turns.set(deliveryID, { request_id: deliveryID, session_id: main.main_session_id, state: 'discarded', retryable: true });
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { thread_id: main.main_session_id, request_id: deliveryID, state: 'discarded', retryable: true } });
    host.intercept = (service, p) => { if (service === 'host.session.send' && p.request_id === deliveryID) { const turn = { request_id: deliveryID, session_id: p.session_id, state: 'running' }; host.turns.set(deliveryID, turn); return turn; } };
    await runtime.client('tasks.reconcile', { task_id: queued.task_id }); host.intercept = undefined;
    assert.equal(host.turns.get(deliveryID).state, 'running'); assert.equal((await runtime.client('tasks.get', { task_id: queued.task_id })).task.delivery.request_id, deliveryID);
  });
  await scenario('conversational routine update keeps its binding and cancels obsolete reminder delivery', async () => {
    let routine = (await runtime.tool('create_routine', { request_id: 'routine-edit', title: 'Lunch reminder', prompt: 'Lunch at noon', run_at: '2026-10-19T12:00:00Z', timezone: 'UTC' }, main.main_session_id)).routine;
    const bindingID = routine.binding.binding_id;
    const binding = host.bindings.get(bindingID); binding.pending_event = { event_id: 'edit-event', binding_revision: binding.revision };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: bindingID, binding_revision: binding.revision, event_id: 'edit-event', payload: binding.payload } });
    const reminder = [...host.turns.values()].find(turn => turn.request_id.startsWith('personal-reminder-') && turn.session_id === main.main_session_id);
    routine = (await runtime.tool('manage_routine', { action: 'update', routine_id: routine.routine_id, expected_revision: routine.revision, run_at: '2026-10-19T13:00:00Z', prompt: 'Lunch at one' }, main.main_session_id)).routine;
    assert.equal(routine.binding.binding_id, bindingID); assert.equal(routine.config_revision, 2); assert.equal(routine.run_at, '2026-10-19T13:00:00Z'); assert.equal(host.bindings.get(bindingID).run_at, routine.run_at); assert.equal(reminder.state, 'interrupted');
    await assert.rejects(runtime.tool('manage_routine', { action: 'update', routine_id: routine.routine_id, expected_revision: routine.revision - 1, prompt: 'stale update' }, main.main_session_id), /revision/);
    routine = (await runtime.tool('manage_routine', { action: 'pause', routine_id: routine.routine_id, expected_revision: routine.revision }, main.main_session_id)).routine;
    routine = (await runtime.tool('manage_routine', { action: 'update', routine_id: routine.routine_id, expected_revision: routine.revision, prompt: 'Still paused' }, main.main_session_id)).routine;
    assert.equal(routine.status, 'paused'); assert.equal(host.bindings.get(bindingID).status, 'paused');
  });
  await scenario('routine kind changes cancel original queued and running work using persisted event identity', async () => {
    for (const kind of ['reminder', 'task']) for (const state of ['queued', 'running']) {
      const h = new Host(), r = await new Runtime(h).start();
      const a = (await r.client('agents.ensure_default')).agent;
      let routine = (await r.client('routines.create', { agent_id: a.agent_id, request_id: 'kind-switch', title: 'Switch kind', prompt: 'Old work', kind, cron: '0 12 * * *', timezone: 'UTC' })).routine;
      const binding = h.bindings.get(routine.binding.binding_id);
      binding.pending_event = { event_id: 'kind-switch-event', binding_revision: binding.revision };
      await r.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
      const execution = [...h.turns.values()][0]; execution.state = state;
      const unrelated = { request_id: 'unrelated', session_id: a.main_session_id, turn_id: 'unrelated-turn', state: 'running' }; h.turns.set(unrelated.request_id, unrelated);
      // Old persisted events lack a kind; their exact request/session is still authoritative.
      if (kind === 'reminder' && state === 'queued') for (const [key, value] of h.storage) {
        const record = JSON.parse(value);
        if (record.event_id === 'kind-switch-event') { delete record.kind; h.storage.set(key, JSON.stringify(record)); }
      }
      routine = (await r.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id);
      const updated = (await r.client('routines.update', { routine_id: routine.routine_id, expected_revision: routine.revision, kind: kind === 'reminder' ? 'task' : 'reminder' })).routine;
      assert.equal(execution.state, 'interrupted'); assert.equal(updated.cancellation_pending, false); assert.equal(unrelated.state, 'running');
      assert(h.calls.some(call => call.service === 'host.session.cancel' && call.params.session_id === execution.session_id));
      await r.close();
    }
  });
  await scenario('invalid routine schedules and unavailable validation preserve existing configuration and execution', async () => {
    const h = new Host(), r = await new Runtime(h).start();
    const a = (await r.client('agents.ensure_default')).agent;
    const args = { agent_id: a.agent_id, request_id: 'valid-schedule', title: 'Preserved', prompt: 'Keep running', kind: 'reminder', cron: '0 12 * * *', timezone: 'UTC' };
    let routine = (await r.client('routines.create', args)).routine;
    const binding = h.bindings.get(routine.binding.binding_id);
    binding.pending_event = { event_id: 'validation-event', binding_revision: binding.revision };
    await r.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    routine = (await r.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id);
    const before = new Map(h.storage), beforeBindings = JSON.stringify([...h.bindings]);
    for (const change of [{ cron: '99 9 * * *' }, { timezone: 'Invalid/Zone' }, { run_at: 'not-a-date', cron: undefined }]) {
      await assert.rejects(r.client('routines.create', { ...args, ...change, request_id: 'invalid-create' }));
      await assert.rejects(r.client('routines.update', { routine_id: routine.routine_id, expected_revision: routine.revision, ...change }));
      assert.deepEqual(h.storage, before); assert.equal(JSON.stringify([...h.bindings]), beforeBindings);
      assert.equal([...h.turns.values()][0].state, 'running');
    }
    // A future revision must not let preflight race a newer writer and merge stale config.
    h.intercept = (service, params) => {
      if (service === 'automation.routines' && !params.binding_id && params.cron === '0 13 * * *') {
        const entry = [...h.storage].find(([, value]) => JSON.parse(value).routine_id === routine.routine_id && JSON.parse(value).config_revision);
        h.storage.set(entry[0], JSON.stringify({ ...routine, revision: routine.revision + 1, title: 'Concurrent writer' }));
      }
    };
    await assert.rejects(r.client('routines.update', { routine_id: routine.routine_id, expected_revision: routine.revision + 1, cron: '0 13 * * *' }), /revision_conflict/);
    assert.deepEqual(h.storage, before); h.intercept = undefined;
    // Older/offline providers cannot preflight: reject before changing an active responsibility.
    h.intercept = service => { if (service === 'automation.routines') throw new Error('unknown routine method'); };
    await assert.rejects(r.client('routines.update', { routine_id: routine.routine_id, expected_revision: routine.revision, cron: '0 13 * * *' }), /unknown routine method/);
    assert.deepEqual(h.storage, before); assert.equal([...h.turns.values()][0].state, 'running'); h.intercept = undefined;
    routine = (await r.client('routines.pause', { routine_id: routine.routine_id, expected_revision: routine.revision })).routine;
    // Historical invalid schedules must not resume or overwrite the paused binding.
    for (const change of [{ cron: '99 9 * * *' }, { timezone: 'Invalid/Zone' }, { run_at: 'not-a-date', cron: undefined }]) {
      const entry = [...h.storage].find(([, value]) => JSON.parse(value).routine_id === routine.routine_id && JSON.parse(value).config_revision);
      const invalid = { ...routine, ...change }; h.storage.set(entry[0], JSON.stringify(invalid));
      const pausedStorage = new Map(h.storage), pausedBindings = JSON.stringify([...h.bindings]);
      await assert.rejects(r.client('routines.resume', { routine_id: routine.routine_id, expected_revision: invalid.revision }));
      assert.deepEqual(h.storage, pausedStorage); assert.equal(JSON.stringify([...h.bindings]), pausedBindings);
      h.storage.set(entry[0], JSON.stringify(routine));
    }
    await r.close();
  });
  await scenario('routine creation has one stable scheduler binding and repeated event delivers once', async () => {
    const args = { agent_id: main.agent_id, request_id: 'remind-one', title: 'Drink water', prompt: 'Remind me to drink water', kind: 'reminder', run_at: '2026-10-18T10:00:00Z', timezone: 'UTC' };
    const routine = (await runtime.client('routines.create', args)).routine;
    const retry = (await runtime.client('routines.create', args)).routine; assert.equal(routine.routine_id, retry.routine_id); assert(host.bindings.has(routine.binding.binding_id));
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'event-one', binding_revision: binding.revision, scheduled_at: '2026-10-18T10:00:00Z' };
    const params = { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload };
    const fire = () => runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params });
    const before = host.turns.size; assert((await fire()).defer_until); const reminder = [...host.turns.values()].at(-1); reminder.state = 'completed'; reminder.final_output = 'Time to drink water'; assert.equal((await fire()).accepted, true); await fire(); assert.equal(host.turns.size, before + 1);
    await runtime.client('routines.pause', { routine_id: routine.routine_id, expected_revision: (await runtime.client('routines.list')).routines[0].revision });
    binding.pending_event.event_id = 'late-event'; params.event_id = 'late-event'; await fire(); assert.equal(host.turns.size, before + 1);
  });
  // History must distinguish dispatch from completion, survive replacement, retain
  // failures, avoid invented legacy timestamps, and isolate each routine's runs.
  await scenario('routine history preserves actual outcomes and task executions across restart', async () => {
    const routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'history-reminder', title: 'History', prompt: 'A reminder', kind: 'reminder', cron: '0 12 * * *', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id);
    const fire = async event_id => {
      binding.pending_event = { event_id, binding_revision: binding.revision, scheduled_at: '2026-10-18T12:00:00Z' };
      return runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    };
    await assert.rejects(runtime.client('routines.history', { routine_id: 'missing' }), /not found/);
    assert.deepEqual((await runtime.client('routines.history', { routine_id: routine.routine_id })).events, []);
    await fire('history-failed');
    let history = await runtime.client('routines.history', { routine_id: routine.routine_id });
    assert.equal(history.events.length, 1); assert.equal(history.events[0].state, 'running');
    assert.equal(history.events[0].kind, 'reminder'); assert.equal(history.events[0].scheduled_at, binding.pending_event.scheduled_at);
    const turn = host.turns.get(history.events[0].request_id); turn.state = 'failed'; turn.error = 'Synthetic failure'; turn.completed_at = new Date().toISOString();
    await fire('history-failed');
    history = await runtime.client('routines.history', { routine_id: routine.routine_id });
    assert.equal(history.events[0].status, 'accepted'); assert.equal(history.events[0].state, 'failed'); assert.equal(history.events[0].error, turn.error);
    assert(history.events[0].created_at); assert.equal(history.events[0].completed_at, turn.completed_at);
    host.turns.delete(turn.request_id);
    await runtime.close(); runtime = await new Runtime(host).start();
    assert.deepEqual((await runtime.client('routines.history', { routine_id: routine.routine_id })).events, history.events);
    const eventKey = [...host.storage.keys()].find(key => JSON.parse(host.storage.get(key)).event_id === 'history-failed');
    const event = JSON.parse(host.storage.get(eventKey));
    for (const key of ['state', 'created_at', 'updated_at', 'scheduled_at', 'completed_at', 'error', 'kind']) delete event[key];
    host.storage.set(eventKey, JSON.stringify(event));
    history = await runtime.client('routines.history', { routine_id: routine.routine_id });
    assert.equal(history.events[0].state, 'unknown'); assert.equal(history.events[0].created_at, undefined);
    const taskRoutine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'history-task', title: 'Task history', prompt: 'Do work', kind: 'task', run_at: '2026-10-18T12:00:00Z', timezone: 'UTC' })).routine;
    const taskBinding = host.bindings.get(taskRoutine.binding.binding_id); taskBinding.pending_event = { event_id: 'history-task-event', binding_revision: taskBinding.revision };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: taskBinding.binding_id, ...taskBinding.pending_event, payload: taskBinding.payload } });
    const taskHistory = await runtime.client('routines.history', { routine_id: taskRoutine.routine_id });
    assert.equal(taskHistory.tasks.length, 1); assert.equal(taskHistory.tasks[0].routine_id, taskRoutine.routine_id);
    assert.equal(taskHistory.tasks[0].state, 'running'); assert.equal(taskHistory.events[0].state, 'dispatched');
    assert.equal(taskHistory.events[0].kind, 'task'); assert.deepEqual(history.tasks, []);
  });
  // Bound reads and inspections even when most records belong to other routines;
  // malformed rows, forged cursors and foreign session identities must fail safely.
  await scenario('routine history pages bounded scans and fences malformed or foreign records', async () => {
    const h = new Host(), r = await new Runtime(h).start();
    try {
      const a = (await r.client('agents.ensure_default')).agent;
      const routine = (await r.client('routines.create', { agent_id: a.agent_id, request_id: 'paged-history', title: 'Paged history', prompt: 'Reminder', cron: '0 12 * * *', timezone: 'UTC' })).routine;
      await assert.rejects(r.client('routines.history', { routine_id: routine.routine_id, agent_id: 'foreign-agent' }), /main Personal/);
      const prefix = [...h.storage.keys()].find(key => key.endsWith(`routine.${routine.routine_id}`)).replace(`routine.${routine.routine_id}`, 'event.');
      for (let i = 0; i < 25; i++) h.storage.set(`${prefix}a${String(i).padStart(3, '0')}`, JSON.stringify({ event_id: `unrelated-${i}`, routine_id: 'other', request_id: 'unused', status: 'accepted' }));
      h.storage.set(`${prefix}b-broken`, '{broken');
      const own = { event_id: 'owned', routine_id: routine.routine_id, routine_revision: routine.config_revision, kind: 'reminder', request_id: 'owned-request', session_id: a.main_session_id, status: 'pending' };
      h.storage.set(`${prefix}c-owned`, JSON.stringify(own));
      h.turns.set(own.request_id, { request_id: own.request_id, session_id: a.main_session_id, state: 'running' });
      h.storage.set(`${prefix}d-foreign`, JSON.stringify({ ...own, event_id: 'foreign', request_id: 'foreign-request', session_id: 'foreign-session' }));
      h.sessions.set('foreign-session', { session_id: 'foreign-session', product_domain: 'personal', user_space_id: 'other-space', agent_id: 'other-agent' });
      h.turns.set('foreign-request', { request_id: 'foreign-request', session_id: 'foreign-session', state: 'completed', final_output: 'Must not leak' });
      let cursor, pages = [], skipped = 0;
      do {
        const before = h.calls.length;
        const page = await r.client('routines.history', { routine_id: routine.routine_id, limit: 2, ...(cursor ? { cursor } : {}) });
        const calls = h.calls.slice(before);
        assert(page.scanned <= 10); assert(calls.filter(call => call.service === 'host.session.inspect').length <= 2);
        assert(calls.filter(call => call.service === 'host.storage.get').length <= 14);
        assert.equal(page.order, 'storage_scan'); assert(page.events.length + page.tasks.length <= 2);
        if (!pages.length) { assert.equal(page.events.length, 0); assert.equal(page.has_more, true); await assert.rejects(r.client('routines.history', { routine_id: routine.routine_id, cursor: 'garbage' }), /cursor/); }
        pages.push(page); skipped += page.skipped_records; cursor = page.cursor;
        assert.equal(Boolean(cursor), page.has_more);
      } while (cursor);
      assert.equal(skipped, 1); assert.equal(pages.flatMap(page => page.events).length, 2);
      const foreign = pages.flatMap(page => page.events).find(event => event.event_id === 'foreign');
      assert.equal(foreign.state, 'unknown'); assert.equal(foreign.output, undefined);
      assert(!h.calls.some(call => call.service === 'host.session.inspect' && call.params.session_id === 'foreign-session'));
      const other = (await r.client('routines.create', { agent_id: a.agent_id, request_id: 'other-history', title: 'Other', prompt: 'Other', cron: '0 12 * * *', timezone: 'UTC' })).routine;
      await assert.rejects(r.client('routines.history', { routine_id: other.routine_id, cursor: pages[0].cursor }), /cursor/);
      const session = h.sessions.get(a.main_session_id); session.product_domain = 'coding';
      const last = await r.client('routines.history', { routine_id: routine.routine_id, cursor: pages.at(-2).cursor, limit: 2 });
      assert.equal(last.events.find(event => event.event_id === 'owned').state, 'unknown');
    } finally { await r.close(); }
  });
  await scenario('existing legacy reminder outbox moves to main without creating a new routine', async () => {
    let routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'legacy-reminder', title: 'Legacy reminder', prompt: 'Keep this reminder', kind: 'reminder', run_at: '2026-10-19T12:00:00Z', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id), eventID = 'legacy-reminder-event'; binding.pending_event = { event_id: eventID, binding_revision: binding.revision };
    const fire = () => runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, binding_revision: binding.revision, event_id: eventID, payload: binding.payload } });
    await fire();
    const routineKey = [...host.storage.keys()].find(key => key.endsWith(`routine.${routine.routine_id}`));
    routine.agent_id = specialist.agent_id; host.storage.set(routineKey, JSON.stringify(routine));
    const eventKey = [...host.storage.keys()].find(key => { const record = JSON.parse(host.storage.get(key)); return record.event_id === eventID; });
    const event = JSON.parse(host.storage.get(eventKey)); delete event.session_id; host.storage.set(eventKey, JSON.stringify(event)); host.turns.get(event.request_id).session_id = specialist.main_session_id;
    await fire();
    const migrated = JSON.parse(host.storage.get(eventKey));
    assert.equal(host.turns.get(event.request_id).state, 'interrupted'); assert.notEqual(migrated.request_id, event.request_id); assert.equal(host.turns.get(migrated.request_id).session_id, main.main_session_id); assert.equal(migrated.previous_delivery.session_id, specialist.main_session_id);
    const existing = (await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id);
    assert.equal(existing.binding.binding_id, binding.binding_id); assert.equal(existing.agent_id, specialist.agent_id);
  });
  await scenario('quiet hours defer routine callbacks without sending or starting work', async () => {
    const settings = (await runtime.client('settings.get')).settings;
    await runtime.client('settings.update', { expected_revision: settings.revision, quiet_hours: { enabled: true, start: '00:00', end: '23:59', timezone: 'UTC' }, notifications: true });
    const routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'quiet-one', title: 'Quiet reminder', prompt: 'Wait until quiet hours finish', kind: 'reminder', run_at: '2026-10-18T10:00:00Z', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'quiet-event', binding_revision: binding.revision, scheduled_at: '2026-10-18T10:00:00Z' };
    const before = host.turns.size; const response = await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    assert(response.defer_until); assert.equal(host.turns.size, before);
  });
  await scenario('routine result with no meaningful change stays silent', async () => {
    const settings = (await runtime.client('settings.get')).settings;
    await runtime.client('settings.update', { expected_revision: settings.revision, quiet_hours: { enabled: false, start: '22:00', end: '08:00', timezone: 'UTC' }, notifications: true });
    const routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'check-one', title: 'Check changes', prompt: 'Notify only about meaningful changes', kind: 'task', run_at: '2026-10-18T10:00:00Z', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'no-change-event', binding_revision: binding.revision, scheduled_at: '2026-10-18T10:00:00Z' };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    const task = (await runtime.client('tasks.list')).tasks.find(t => t.routine_id === routine.routine_id); assert(task);
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'No changes in the checked source' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'No meaningful changes', meaningful: false, evidence: [{ seq: 1, description: 'Checked source' }] }, task.execution.session_id);
    const before = host.calls.filter(c => c.service === 'host.session.send' && c.params.cause === 'personal.task.result').length;
    await runtime.lifecycle(task, 'completed'); assert.equal(host.calls.filter(c => c.service === 'host.session.send' && c.params.cause === 'personal.task.result').length, before);
  });
  await scenario('queued task discarded for host restart retries its original dispatch', async () => {
    let pending = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'retry-queued', title: 'Retry queued', goal: 'Preserve dispatch over restart' })).task;
    host.turns.set(pending.execution.request_id, { request_id: pending.execution.request_id, session_id: pending.execution.session_id, state: 'discarded', retryable: true });
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { thread_id: pending.execution.session_id, request_id: pending.execution.request_id, state: 'discarded', retryable: true } });
    host.intercept = (service, p) => { if (service === 'host.session.send' && p.request_id === pending.execution.request_id) { const turn = { request_id: p.request_id, session_id: p.session_id, state: 'running' }; host.turns.set(p.request_id, turn); return turn; } };
    const resumed = (await runtime.client('tasks.reconcile', { task_id: pending.task_id })).task; host.intercept = undefined;
    assert.equal(resumed.state, 'running'); assert.equal(resumed.execution.request_id, pending.execution.request_id); assert.equal(resumed.execution.session_id, pending.execution.session_id);
  });
  await scenario('routine result queued for delivery is not suppressed by its own dedupe hash', async () => {
    const routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'changed-one', title: 'Watch changes', prompt: 'Report meaningful changes', kind: 'task', run_at: '2026-10-18T10:00:00Z', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'changed-event', binding_revision: binding.revision, scheduled_at: '2026-10-18T10:00:00Z' };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    let task = (await runtime.client('tasks.list')).tasks.find(t => t.routine_id === routine.routine_id);
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'New result confirmed' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'Important new result', evidence: [{ seq: 1, description: 'Changed source' }] }, task.execution.session_id);
    host.intercept = (service, p) => { if (service === 'host.session.send' && p.cause === 'personal.task.result') { const turn = { request_id: p.request_id, session_id: p.session_id, state: 'queued' }; host.turns.set(p.request_id, turn); return turn; } };
    await runtime.lifecycle(task, 'completed'); host.intercept = undefined; task = (await runtime.client('tasks.get', { task_id: task.task_id })).task;
    const delivery = task.delivery.request_id; host.turns.set(delivery, { request_id: delivery, session_id: main.main_session_id, state: 'discarded', retryable: true });
    host.intercept = (service, p) => { if (service === 'host.session.send' && p.request_id === delivery) { const turn = { request_id: delivery, session_id: p.session_id, state: 'running' }; host.turns.set(delivery, turn); return turn; } };
    await runtime.client('tasks.reconcile', { task_id: task.task_id }); host.intercept = undefined;
    assert.equal(host.turns.get(delivery).state, 'running');
  });
  await scenario('failed responsible-agent reply is surfaced instead of claiming delivered', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'delivery-failure', title: 'Delivery failure', goal: 'Keep verified result available' })).task;
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified result' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'Useful result', evidence: [{ seq: 1, description: 'Evidence' }] }, task.execution.session_id);
    await runtime.lifecycle(task, 'completed'); task = (await runtime.client('tasks.get', { task_id: task.task_id })).task;
    assert.equal(task.delivery.status, 'submitted');
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { thread_id: main.main_session_id, request_id: task.delivery.request_id, state: 'failed', error: 'model unavailable' } });
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal(task.delivery.status, 'failed'); assert.match(task.error, /reply|deliver/i); assert.equal(task.result.summary, 'Useful result');
  });
  await scenario('failed cancellation of old requirements blocks replacement and remains recoverable', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'old-cancel-fail', title: 'Revise safely', goal: 'Original requirement' })).task;
    const oldSession = task.execution.session_id;
    host.intercept = (service, p) => { if (service === 'host.session.cancel' && p.session_id === oldSession) throw new Error('cancel connection lost'); };
    task = (await runtime.client('tasks.update', { task_id: task.task_id, expected_revision: task.revision, goal: 'Replacement requirement' })).task;
    assert.equal(task.state, 'waiting_external'); assert.equal(task.previous_executions.at(-1).cancel_pending, true); assert(!host.turns.has(task.execution.request_id));
    const retry = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'old-cancel-fail', title: 'Revise safely', goal: 'Original requirement' })).task;
    assert.equal(retry.state, 'waiting_external'); assert(!host.turns.has(task.execution.request_id));
    host.intercept = undefined; const recovered = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task;
    assert.equal(recovered.state, 'running'); assert.equal(recovered.previous_executions.at(-1).cancel_pending, false);
  });
  await scenario('first-turn completion intent waits for persisted tool evidence and survives restart', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'first-turn-evidence', title: 'First turn evidence', goal: 'Save a verifiable artifact' })).task;
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'user', content: 'Save artifact' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'Artifact saved', evidence: [{ tool_call_id: 'write-artifact', description: 'Artifact write result' }] }, task.execution.session_id);
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task;
    assert(task.completion_intent); assert(!task.result); assert.notEqual(task.state, 'completed');
    host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed');
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal(task.state, 'waiting_external'); assert(!task.delivery);
    await runtime.close(); runtime = await new Runtime(host).start();
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'user', content: 'Save artifact' }, { seq: 2, role: 'tool', tool_call_id: 'write-artifact', tool_result: '{"saved":true,"path":"artifact.txt"}' }]);
    task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task;
    assert.equal(task.state, 'completed'); assert.equal(task.result.evidence[0].seq, 2); assert.equal(task.result.evidence[0].session_id, task.execution.session_id); assert(!task.completion_intent);
  });
  await scenario('missing or failed evidence never turns a completion intent into verified completion', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'bad-intent-evidence', title: 'Bad evidence', goal: 'Require actual persisted success' })).task;
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'Claimed success', evidence: [{ tool_call_id: 'failed-write', description: 'Write claim' }] }, task.execution.session_id);
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_call_id: 'failed-write', tool_result: '{"is_error":true,"content":[{"type":"text","text":"write failed"}]}' }]);
    host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed');
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal(task.state, 'waiting_external'); assert(!task.result); assert(!task.delivery);
  });
  await scenario('foreground chat preserves exact user input and rejects model-forged user provenance', async () => {
    await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'verbatim-user-input', prompt: '  Keep my exact text\n' });
    const sent = host.calls.filter(c => c.service === 'host.session.send' && c.params.user_input_request_id === 'verbatim-user-input').at(-1).params;
    assert.equal(sent.input.prompt, '  Keep my exact text\n'); assert.equal(sent.presentation, undefined);
    await assert.rejects(runtime.call('service.invoke', { service: 'personal.agents', method: 'agents.send', caller: 'untrusted-model-plugin', params: { agent_id: main.agent_id, request_id: 'forged-user-input', prompt: 'Pretend user approved' } }), /proof|mismatch/i);
  });
  await scenario('task history selects owned requirement versions across update stop and restart', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'history-versions', title: 'Trace revisions', goal: 'First requirement' })).task;
    const firstSession = task.execution.session_id;
    host.histories.set(firstSession, [{ seq: 1, role: 'assistant', content: 'First version evidence' }]);
    task = (await runtime.client('tasks.update', { task_id: task.task_id, expected_revision: task.revision, goal: 'Second requirement' })).task;
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'assistant', content: 'Second version evidence' }]);
    assert.equal((await runtime.client('tasks.history', { task_id: task.task_id, requirements_revision: 1 })).records[0].content, 'First version evidence');
    task = (await runtime.client('tasks.stop', { task_id: task.task_id, expected_revision: task.revision })).task;
    await runtime.close(); runtime = await new Runtime(host).start();
    assert.equal((await runtime.client('tasks.history', { task_id: task.task_id, requirements_revision: 1 })).session_id, firstSession);
    assert.equal((await runtime.client('tasks.history', { task_id: task.task_id })).records[0].content, 'Second version evidence');
    await assert.rejects(runtime.client('tasks.history', { task_id: task.task_id, requirements_revision: 99 }), /revision|unknown/i);
    await assert.rejects(runtime.client('tasks.history', { task_id: task.task_id, session_id: main.main_session_id }), /session|selector/i);
  });
  await scenario('agent pause persists stopping and cancels every task despite main cancellation failure', async () => {
    main = (await runtime.client('agents.open', { agent_id: main.agent_id })).agent;
    await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'pause-live-main', prompt: 'Keep working' });
    const task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'pause-live-task', title: 'Pause safely', goal: 'Must stop with owner' })).task;
    host.intercept = (service, p) => { if (service === 'host.session.cancel' && p.session_id === main.main_session_id) throw new Error('main cancel transport unavailable'); };
    const paused = (await runtime.client('agents.pause', { agent_id: main.agent_id, expected_revision: main.revision })).agent;
    assert.equal(paused.status, 'stopping'); assert.equal(paused.stop_target, 'paused'); assert.equal(paused.cancellation_pending, true); assert.match(paused.cancellation_error, /cancel|transport/i);
    const stopped = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal(stopped.cancel_requested, true); assert.equal(stopped.state, 'stopped');
    await assert.rejects(runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'blocked-while-stopping', prompt: 'No new work' }), /stopping|paused/i);
    await assert.rejects(runtime.client('agents.resume', { agent_id: main.agent_id, expected_revision: paused.revision }), /stopping|cancellation/i);
    await runtime.close(); host.intercept = undefined; runtime = await new Runtime(host).start();
    main = (await runtime.client('agents.open', { agent_id: main.agent_id })).agent;
    assert.equal(main.status, 'paused'); assert.equal(main.cancellation_pending, false);
    main = (await runtime.client('agents.resume', { agent_id: main.agent_id, expected_revision: main.revision })).agent;
    assert.equal(main.status, 'active'); assert.equal((await runtime.client('tasks.get', { task_id: task.task_id })).task.state, 'stopped');
  });
  await scenario('task resume retains and reconciles cancellation of obsolete queued result delivery', async () => {
    let task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'stale-result-on-resume', title: 'Revise result safely', goal: 'First version' })).task;
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified first version' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'First version result', evidence: [{ seq: 1, description: 'Persisted first result' }] }, task.execution.session_id);
    host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed');
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task;
    const oldDelivery = task.delivery.request_id, queueID = `q-${oldDelivery}`;
    host.turns.set(oldDelivery, { request_id: oldDelivery, session_id: main.main_session_id, state: 'queued', queue_id: queueID });
    await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'unrelated-main-work', prompt: 'Continue our other conversation' });
    const unrelated = [...host.turns.values()].find(t => t.session_id === main.main_session_id && t.request_id !== oldDelivery && t.state === 'running');
    host.intercept = (service, p) => { if (service === 'host.session.cancel' && p.queue_id === queueID) throw new Error('delivery cancel unavailable'); };
    task = (await runtime.client('tasks.resume', { task_id: task.task_id, expected_revision: task.revision, goal: 'Replacement version' })).task;
    assert.equal(task.state, 'waiting_external'); assert.equal(task.previous_deliveries[0].cancel_pending, true); assert(!host.turns.has(task.execution.request_id));
    await runtime.close(); runtime = await new Runtime(host).start();
    assert(!host.turns.has(task.execution.request_id)); host.intercept = undefined;
    task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task;
    assert.equal(task.state, 'running'); assert.equal(task.previous_deliveries[0].cancel_pending, false); assert.equal(host.turns.get(oldDelivery).state, 'interrupted'); assert.equal(unrelated.state, 'running');
  });
  await scenario('Calendar source baselines silently then durably deduplicates meaningful changes into responsible tasks', async () => {
    const source = { kind: 'calendar', account_id: 'calendar-account', generation: 'source-generation-1', calendar_id: 'primary', time_min: new Date().toISOString(), time_max: new Date(Date.now() + 86400000).toISOString(), timezone: 'UTC' };
    const provenance = { ...source, provider: 'google-calendar', user_space_id: 'local', fetched_at: new Date().toISOString(), read_only: true };
    const change = { event_id: 'meaningful-change-1', calendar_event_id: 'meeting-1', kind: 'updated', before: { id: 'meeting-1', summary: 'Meeting', start: '10:00' }, after: { id: 'meeting-1', summary: 'Meeting', start: '11:00' }, occurred_at: new Date().toISOString(), source: provenance };
    let reads = 0;
    host.intercept = (service, p) => { if (service === 'calendar.personal') return p.cursor ? { available: true, baseline: false, reset_required: false, cursor: 'cursor-1', changes: [change], source: provenance } : { available: true, baseline: true, reset_required: false, cursor: 'cursor-0', changes: [], source: provenance, state: 'ready', freshness: 'fresh' }; };
    let routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'calendar-source', title: 'Watch meeting changes', prompt: 'Explain meaningful schedule changes', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC', source })).routine;
    const fire = async eventID => { const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: eventID, binding_revision: binding.revision, scheduled_at: new Date().toISOString() }; return runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } }); };
    await fire('calendar-tick-1'); routine = (await runtime.client('routines.list')).routines.find(r => r.routine_id === routine.routine_id);
    assert.equal(routine.source_cursor, 'cursor-0'); assert.equal((await runtime.client('tasks.list')).tasks.filter(t => t.routine_id === routine.routine_id).length, 0);
    await fire('calendar-tick-2'); await fire('calendar-tick-2'); await fire('calendar-tick-3');
    const tasks = (await runtime.client('tasks.list')).tasks.filter(t => t.routine_id === routine.routine_id); assert.equal(tasks.length, 1); assert.equal(tasks[0].agent_id, main.agent_id); assert.equal(tasks[0].source.calendar.event_id, change.event_id);
    routine = (await runtime.client('routines.list')).routines.find(r => r.routine_id === routine.routine_id); assert.equal(routine.source_cursor, 'cursor-1'); assert(!routine.source_pending);
    const complete = async task => { host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified distinct calendar event' }]); await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'A meeting changed', evidence: [{ seq: 1, description: 'Distinct change evidence' }] }, task.execution.session_id); host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed'); return (await runtime.client('tasks.get', { task_id: task.task_id })).task; };
    const firstResult = await complete(tasks[0]);
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { thread_id: main.main_session_id, request_id: firstResult.delivery.request_id, state: 'completed', final_output: 'A meeting changed' } });
    const nextChange = { ...change, event_id: 'meaningful-change-2', calendar_event_id: 'meeting-2', after: { id: 'meeting-2', summary: 'Another meeting', start: '12:00' } };
    host.intercept = service => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', available: true, baseline: false, reset_required: false, cursor: 'cursor-2', changes: [nextChange], source: provenance } : undefined;
    await fire('calendar-tick-4'); const nextTask = (await runtime.client('tasks.list')).tasks.find(task => task.routine_id === routine.routine_id && task.source.calendar.event_id === nextChange.event_id);
    assert.equal((await complete(nextTask)).delivery.status, 'submitted', 'distinct Calendar events must not be suppressed merely because summaries match');
    host.intercept = undefined;
  });
  await scenario('Calendar source account reset blocks model work and pause resume discards old pending events', async () => {
    const source = { kind: 'calendar', account_id: 'expected-account', generation: 'expected-generation', calendar_id: 'primary', time_min: new Date().toISOString(), time_max: new Date(Date.now() + 86400000).toISOString(), timezone: 'UTC' };
    let routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'calendar-reset', title: 'Bound source', prompt: 'Watch only the requested calendar', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC', source })).routine;
    host.intercept = (service) => { if (service === 'calendar.personal') return { state: 'ready', freshness: 'fresh', available: false, reset_required: true, reason: 'account_generation_changed', changes: [], source: { ...source, account_id: 'different-account', user_space_id: 'local' } }; };
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'reset-tick', binding_revision: binding.revision, scheduled_at: new Date().toISOString() };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    routine = (await runtime.client('routines.list')).routines.find(r => r.routine_id === routine.routine_id); assert.equal(routine.source_blocked, true); assert.match(routine.last_error, /account|generation|reset/i); assert.equal((await runtime.client('tasks.list')).tasks.filter(t => t.routine_id === routine.routine_id).length, 0);
    const paused = (await runtime.client('routines.pause', { routine_id: routine.routine_id, expected_revision: routine.revision })).routine;
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    routine = (await runtime.client('routines.resume', { routine_id: paused.routine_id, expected_revision: paused.revision })).routine; assert.equal(routine.source_cursor, undefined); assert.equal(routine.source_blocked, false);
    host.intercept = undefined;
  });
  await scenario('Calendar cursor interruption replays one durable task and expired windows stop without a model', async () => {
    const source = { kind: 'calendar', account_id: 'recovery-account', generation: 'recovery-generation', calendar_id: 'primary', time_min: new Date().toISOString(), time_max: new Date(Date.now() + 86400000).toISOString(), timezone: 'UTC' };
    const provenance = { ...source, provider: 'google-calendar', user_space_id: 'local', fetched_at: new Date().toISOString(), read_only: true };
    const change = { event_id: 'recover-change', calendar_event_id: 'event-recover', kind: 'created', after: { id: 'event-recover', summary: 'New meeting' }, occurred_at: new Date().toISOString(), source: provenance };
    let routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'calendar-cursor-recovery', title: 'Recover source', prompt: 'Review changes', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC', source })).routine;
    let failCursor = true;
    host.intercept = (service, p) => {
      if (service === 'calendar.personal') return { state: 'ready', freshness: 'fresh', available: true, baseline: !p.cursor, reset_required: false, cursor: p.cursor ? 'cursor-next' : 'cursor-start', changes: p.cursor ? [change] : [], source: provenance };
      if (service === 'host.storage.compare-exchange' && p.value) { const next = JSON.parse(p.value); if (failCursor && next.routine_id === routine.routine_id && next.source_cursor === 'cursor-next' && !next.source_pending) { failCursor = false; throw new Error('cursor acknowledgement interrupted'); } }
    };
    const fire = async eventID => { const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: eventID, binding_revision: binding.revision, scheduled_at: new Date().toISOString() }; await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } }); };
    await fire('cursor-tick-1'); await fire('cursor-tick-2');
    routine = (await runtime.client('routines.list')).routines.find(r => r.routine_id === routine.routine_id); assert.equal(routine.source_cursor, 'cursor-start'); assert.equal(routine.source_pending.cursor, 'cursor-next');
    await runtime.close(); runtime = await new Runtime(host).start(); await fire('cursor-tick-3');
    routine = (await runtime.client('routines.list')).routines.find(r => r.routine_id === routine.routine_id); assert.equal(routine.source_cursor, 'cursor-next'); assert(!routine.source_pending); assert.equal((await runtime.client('tasks.list')).tasks.filter(t => t.routine_id === routine.routine_id).length, 1);
    const expiredCancellation = { ...change, event_id: 'past-cancellation', kind: 'cancelled', before: { id: 'past-event', end: { date_time: new Date(Date.now() - 3600000).toISOString(), timezone: 'UTC' } }, after: { id: 'past-event', status: 'cancelled' } };
    host.intercept = service => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', available: true, baseline: false, reset_required: false, cursor: 'cursor-after-expired', changes: [expiredCancellation], source: provenance } : undefined;
    await fire('cursor-past-cancellation'); assert.equal((await runtime.client('tasks.list')).tasks.filter(t => t.routine_id === routine.routine_id).length, 1);
    let task = (await runtime.client('tasks.list')).tasks.find(t => t.routine_id === routine.routine_id);
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified calendar assessment' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'The meeting change matters', evidence: [{ seq: 1, description: 'Assessment evidence' }] }, task.execution.session_id);
    host.intercept = service => service === 'calendar.personal' ? { state: 'refreshing', freshness: 'stale', source: provenance } : undefined;
    host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed');
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal(task.delivery.status, 'pending'); assert.notEqual((await runtime.client('routines.list')).routines.find(r => r.routine_id === routine.routine_id).source_blocked, true);
    host.intercept = service => service === 'calendar.personal' ? { state: 'ready', source: provenance } : undefined;
    task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task; assert.equal(task.delivery.status, 'pending', 'missing source freshness must defer rather than claim delivery eligibility');
    host.intercept = service => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', source: provenance } : undefined;
    task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task; assert.equal(task.delivery.status, 'submitted');
    host.intercept = undefined;
    const expired = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'expired-calendar-window', title: 'Expired source', prompt: 'Should not execute', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC', source: { ...source, time_min: new Date(Date.now() - 7200000).toISOString(), time_max: new Date(Date.now() - 3600000).toISOString() } })).routine;
    const binding = host.bindings.get(expired.binding.binding_id); binding.pending_event = { event_id: 'expired-tick', binding_revision: binding.revision, scheduled_at: new Date().toISOString() };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    assert.equal((await runtime.client('routines.list')).routines.find(r => r.routine_id === expired.routine_id).status, 'completed'); assert.equal(host.bindings.get(binding.binding_id).status, 'stopped'); assert.equal((await runtime.client('tasks.list')).tasks.filter(t => t.routine_id === expired.routine_id).length, 0);
  });
  await scenario('attachment-only chat preserves trusted refs and renders safe artifact history with scoped chunk reads', async () => {
    const sha256 = 'a'.repeat(64), attachment = { sha256, filename: 'agenda.txt', media_type: 'text/plain' };
    await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'attachment-only', prompt: '', attachments: [attachment] });
    const sent = host.calls.filter(call => call.service === 'host.session.send' && call.params.user_input_request_id === 'attachment-only').at(-1).params;
    assert.equal(sent.input.prompt, ''); assert.deepEqual(sent.input.attachments, [attachment]);
    host.histories.set(main.main_session_id, [{ seq: 1, role: 'user', attachments: [{ kind: 'file', index: 0, name: 'agenda.txt', mime_type: 'text/plain', size: 14, sha256 }] }, { seq: 2, role: 'tool', content: 'hidden tool internals', tool_result: 'private raw payload', attachments: [{ kind: 'artifact', index: 0, name: 'result.txt', mime_type: 'text/plain', size: 14, sha256 }] }]);
    const history = await runtime.client('agents.history', { agent_id: main.agent_id }); assert.equal(history.records.length, 2); assert.equal(history.records[0].attachments[0].name, 'agenda.txt'); assert.equal(history.records[1].attachments[0].name, 'result.txt'); assert(!JSON.stringify(history).includes('hidden tool')); assert(!JSON.stringify(history).includes('private raw'));
    const first = await runtime.client('agents.attachments.read', { agent_id: main.agent_id, seq: 2, kind: 'artifact', index: 0, sha256 });
    const next = await runtime.client('agents.attachments.read', { agent_id: main.agent_id, seq: 2, kind: 'artifact', index: 0, sha256, offset: first.data.length }); assert.equal(next.offset, first.data.length); assert.equal(first.sha256, sha256);
    await assert.rejects(runtime.client('agents.attachments.read', { agent_id: main.agent_id, seq: 2, kind: 'artifact', index: 0, sha256, session_id: specialist.main_session_id }), /session|scope/i);
    await assert.rejects(runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'attachment-bytes-forgery', prompt: '', attachments: [{ ...attachment, data: 'privatebytes' }] }), /reference|bytes|attachment/i);
  });
  await scenario('actual Calendar and Personal runtimes integrate silent baseline meaningful change and restart dedupe', async () => {
    const host = new Host(); let calendar = await new Runtime(host, calendarRuntimePath, 'personal-calendar').start(); host.calendar = calendar;
    let personal = await new Runtime(host).start(); const agent = (await personal.client('agents.ensure_default')).agent;
    const query = { calendar_id: 'primary', time_min: new Date().toISOString(), time_max: new Date(Date.now() + 86400000).toISOString(), timezone: 'UTC' };
    const event = { id: 'integrated-meeting', status: 'confirmed', summary: 'Design review', start: { date_time: new Date(Date.now() + 3600000).toISOString(), timezone: 'UTC' }, end: { date_time: new Date(Date.now() + 7200000).toISOString(), timezone: 'UTC' } };
    host.storage.set('user:fixture.calendar.snapshot', JSON.stringify({ events: [event] }));
    const snapshot = await calendar.client('calendar.refresh', query); assert(snapshot.source.generation);
    let routine = (await personal.client('routines.create', { agent_id: agent.agent_id, request_id: 'integrated-calendar', title: 'Calendar review', prompt: 'Explain important schedule changes', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC', source: { ...snapshot.source, kind: 'calendar' } })).routine;
    const fire = async eventID => { const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: eventID, binding_revision: binding.revision, scheduled_at: new Date().toISOString() }; await personal.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } }); };
    await fire('integrated-tick-1'); assert.equal((await personal.client('tasks.list')).tasks.length, 0);
    const todayQuery = { ...query, time_max: new Date(Date.now() + 6 * 3600000).toISOString() };
    await calendar.client('calendar.refresh', todayQuery);
    host.storage.set('user:fixture.calendar.snapshot', JSON.stringify({ events: [{ ...event, summary: 'Moved design review', start: { ...event.start, date_time: new Date(Date.now() + 5400000).toISOString() } }] }));
    await fire('integrated-tick-2'); const tasks = (await personal.client('tasks.list')).tasks; assert.equal(tasks.length, 1); assert.equal(tasks[0].source.calendar.calendar_event_id, event.id); assert.equal(tasks[0].source.calendar.kind, 'updated');
    await calendar.client('calendar.refresh', todayQuery);
    host.histories.set(tasks[0].execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified calendar change assessment' }]);
    await personal.tool('complete_task', { task_id: tasks[0].task_id, requirements_revision: 1, summary: 'The design review moved', evidence: [{ seq: 1, description: 'Assessment of the sourced calendar change' }] }, tasks[0].execution.session_id);
    host.turns.get(tasks[0].execution.request_id).state = 'completed'; await personal.lifecycle(tasks[0], 'completed');
    assert.equal((await personal.client('tasks.get', { task_id: tasks[0].task_id })).task.delivery.status, 'submitted');
    await personal.close(); await calendar.close(); calendar = await new Runtime(host, calendarRuntimePath, 'personal-calendar').start(); host.calendar = calendar; personal = await new Runtime(host).start();
    await fire('integrated-tick-3'); assert.equal((await personal.client('tasks.list')).tasks.length, 1);
    await personal.close(); await calendar.close();
  });
  await scenario('pausing a task Routine durably cancels descendants and waits for confirmation before resume', async () => {
    let routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'pause-task-routine', title: 'Pause recurring work', prompt: 'Execute only while responsibility is active', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'pause-task-event', binding_revision: binding.revision, scheduled_at: new Date().toISOString() };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    const task = (await runtime.client('tasks.list')).tasks.find(task => task.routine_id === routine.routine_id); assert(task);
    routine = (await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id);
    host.intercept = (service, p) => { if (service === 'host.session.cancel' && p.session_id === task.execution.session_id) throw new Error('descendant cancellation unavailable'); };
    routine = (await runtime.client('routines.pause', { routine_id: routine.routine_id, expected_revision: routine.revision })).routine;
    assert.equal(routine.status, 'stopping'); assert.equal(routine.stop_target, 'paused'); assert.equal(routine.cancellation_pending, true); assert.equal((await runtime.client('tasks.get', { task_id: task.task_id })).task.cancel_requested, true);
    await assert.rejects(runtime.client('routines.resume', { routine_id: routine.routine_id, expected_revision: routine.revision }), /stopping|cancellation/i);
    await runtime.close(); host.intercept = undefined; runtime = await new Runtime(host).start();
    routine = (await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id); assert.equal(routine.status, 'paused'); assert.equal(routine.cancellation_pending, false); assert.equal((await runtime.client('tasks.get', { task_id: task.task_id })).task.state, 'stopped');
    await runtime.client('routines.resume', { routine_id: routine.routine_id, expected_revision: routine.revision }); assert.equal((await runtime.client('tasks.get', { task_id: task.task_id })).task.state, 'stopped');
  });
  await scenario('Routine stop intent fences retryable task dispatch after a crash before descendant cancellation', async () => {
    let routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'routine-crash-fence', title: 'Crash fence', prompt: 'Do not retry after stopping', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'routine-crash-event', binding_revision: binding.revision, scheduled_at: new Date().toISOString() };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    const task = (await runtime.client('tasks.list')).tasks.find(t => t.routine_id === routine.routine_id); routine = (await runtime.client('routines.list')).routines.find(r => r.routine_id === routine.routine_id);
    let intentWritten = false;
    host.intercept = (service, p) => { if (service === 'host.storage.compare-exchange' && p.value) { const next = JSON.parse(p.value); if (next.routine_id === routine.routine_id && next.status === 'stopping') intentWritten = true; } if (service === 'host.storage.keys' && intentWritten) throw new Error('simulated crash after stop intent'); };
    await assert.rejects(runtime.client('routines.pause', { routine_id: routine.routine_id, expected_revision: routine.revision }), /simulated crash/i); host.intercept = undefined;
    assert.equal((await runtime.client('tasks.get', { task_id: task.task_id })).task.cancel_requested, false);
    host.turns.get(task.execution.request_id).state = 'discarded'; host.turns.get(task.execution.request_id).retryable = true;
    const before = host.calls.filter(call => call.service === 'host.session.send' && call.params.request_id === task.execution.request_id).length;
    await runtime.close(); runtime = await new Runtime(host).start();
    assert.equal(host.calls.filter(call => call.service === 'host.session.send' && call.params.request_id === task.execution.request_id).length, before); assert.equal((await runtime.client('tasks.get', { task_id: task.task_id })).task.state, 'stopped');
  });
  await scenario('a proven Calendar source block survives transient sibling diagnostics and fences old work', async () => {
    const source = { kind: 'calendar', account_id: 'blocked-account', generation: 'blocked-generation', calendar_id: 'primary', time_min: new Date().toISOString(), time_max: new Date(Date.now() + 86400000).toISOString(), timezone: 'UTC' };
    const provenance = { ...source, provider: 'google-calendar', user_space_id: 'local', fetched_at: new Date().toISOString(), read_only: true };
    const changes = [1, 2, 3].map(index => ({ event_id: `blocked-change-${index}`, calendar_event_id: `blocked-event-${index}`, kind: 'updated', after: { id: `blocked-event-${index}`, summary: 'Changed event' }, occurred_at: new Date().toISOString(), source: provenance }));
    host.intercept = (service, p) => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', available: true, baseline: !p.cursor, cursor: p.cursor ? 'blocked-cursor-1' : 'blocked-cursor-0', changes: p.cursor ? changes : [], source: provenance } : undefined;
    const routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'monotonic-source-block', title: 'Bound account work', prompt: 'Assess calendar changes', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC', source })).routine;
    const fire = async eventID => { const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: eventID, binding_revision: binding.revision }; await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } }); };
    await fire('blocked-tick-1'); await fire('blocked-tick-2');
    const tasks = (await runtime.client('tasks.list')).tasks.filter(task => task.routine_id === routine.routine_id); assert.equal(tasks.length, 3);
    const complete = async task => { host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified assessment' }]); await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'Assessment ready', evidence: [{ seq: 1, description: 'Verified assessment' }] }, task.execution.session_id); host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed'); };
    host.intercept = service => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', source: { ...provenance, generation: 'different-generation' } } : undefined;
    await complete(tasks[0]); assert.equal((await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id).source_blocked, true);
    host.intercept = service => service === 'calendar.personal' ? { state: 'unconnected', freshness: 'empty' } : undefined;
    await complete(tasks[1]); assert.equal((await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id).source_blocked, true, 'transient diagnostics must not remove a proven source block');
    host.turns.get(tasks[2].execution.request_id).state = 'discarded'; host.turns.get(tasks[2].execution.request_id).retryable = true;
    const before = host.calls.filter(call => call.service === 'host.session.send' && call.params.request_id === tasks[2].execution.request_id).length;
    const stopped = (await runtime.client('tasks.reconcile', { task_id: tasks[2].task_id })).task;
    assert.equal(stopped.state, 'stopped'); assert.equal(host.calls.filter(call => call.service === 'host.session.send' && call.params.request_id === tasks[2].execution.request_id).length, before);
    host.intercept = undefined;
  });
  await scenario('queued Routine replies retain honest delivery receipts after notifications or quiet hours change', async () => {
    for (const policy of ['disabled', 'quiet']) {
      let settings = (await runtime.client('settings.get')).settings;
      await runtime.client('settings.update', { expected_revision: settings.revision, notifications: true, quiet_hours: { enabled: false, start: '22:00', end: '08:00', timezone: 'UTC' } });
      const routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: `queued-policy-${policy}`, title: 'Queued result policy', prompt: 'Assess requested changes', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC' })).routine;
      const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: `queued-policy-event-${policy}`, binding_revision: binding.revision };
      await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
      let task = (await runtime.client('tasks.list')).tasks.find(item => item.routine_id === routine.routine_id);
      host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified result' }]);
      await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: `Requested result ${policy}`, evidence: [{ seq: 1, description: 'Persisted result' }] }, task.execution.session_id);
      host.intercept = (service, p) => { if (service === 'host.session.send' && p.cause === 'personal.task.result') { const turn = { request_id: p.request_id, session_id: p.session_id, state: 'queued', queue_id: `policy-${policy}` }; host.turns.set(p.request_id, turn); return turn; } };
      host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed'); host.intercept = undefined;
      task = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal(task.delivery.status, 'submitted');
      settings = (await runtime.client('settings.get')).settings;
      const hour = new Date().getUTCHours(), hh = value => String((value + 24) % 24).padStart(2, '0');
      await runtime.client('settings.update', { expected_revision: settings.revision, notifications: policy !== 'disabled', quiet_hours: { enabled: policy === 'quiet', start: `${hh(hour - 1)}:00`, end: `${hh(hour + 1)}:00`, timezone: 'UTC' } });
      task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task; assert.equal(task.delivery.status, 'submitted', 'an admitted reply is not suppressed merely because push preferences changed');
      const receipt = host.turns.get(task.delivery.request_id); receipt.state = 'completed'; receipt.final_output = 'Visible result retained in history';
      task = (await runtime.client('tasks.reconcile', { task_id: task.task_id })).task; assert.equal(task.delivery.status, 'delivered');
    }
  });
  await scenario('attention decisions verify main-session ownership and current notification preferences fail closed', async () => {
    assert(runtime.definition.capabilities.some(capability => capability.id === 'session.attention' && capability.kind === 'decision' && capability.version === 1));
    const input = { session_id: main.main_session_id, agent_id: main.agent_id, turn_id: 'persisted-main-turn', message_seq: 2, outcome: 'completed', user_space_id: 'local', product_domain: 'personal' };
    host.turns.set('personal-chat-attention-test', { request_id: 'personal-chat-attention-test', session_id: main.main_session_id, turn_id: input.turn_id, state: 'completed', final_output: 'Persisted direct response' });
    const attention = async changes => (await runtime.call('capability.invoke', { capability: 'session.attention', input: { ...input, ...changes } })).output;
    assert.deepEqual(await attention({}), { allowed: false }, 'current quiet interval applies to all main replies');
    let settings = (await runtime.client('settings.get')).settings;
    await runtime.client('settings.update', { expected_revision: settings.revision, notifications: false, quiet_hours: { ...settings.quiet_hours, enabled: false } });
    assert.deepEqual(await attention({ outcome: 'failed' }), { allowed: false });
    settings = (await runtime.client('settings.get')).settings;
    await runtime.client('settings.update', { expected_revision: settings.revision, notifications: true, quiet_hours: { ...settings.quiet_hours, enabled: false } });
    assert.deepEqual(await attention({}), { allowed: true, batch_window_seconds: 0 }); assert.deepEqual(await attention({ outcome: 'failed' }), { allowed: true, batch_window_seconds: 0 });
    for (const mismatch of [{ product_domain: 'coding' }, { user_space_id: 'another-space' }, { agent_id: specialist.agent_id }, { session_id: 'unknown-session' }, { outcome: 'interrupted' }]) assert.deepEqual(await attention(mismatch), { allowed: false });
    const internal = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'attention-internal', title: 'Internal attention denied', goal: 'Remain a background executor' })).task;
    assert.deepEqual(await attention({ session_id: internal.execution.session_id }), { allowed: false });
    host.intercept = service => { if (service === 'host.storage.get') throw new Error('preference storage unavailable'); };
    assert.deepEqual(await attention({}), { allowed: false }); host.intercept = undefined;
    const pause = (await runtime.client('agents.pause', { agent_id: main.agent_id, expected_revision: (await runtime.client('agents.open', { agent_id: main.agent_id })).agent.revision })).agent;
    assert.deepEqual(await attention({}), { allowed: false }); await runtime.client('agents.resume', { agent_id: main.agent_id, expected_revision: pause.revision });
    await assert.rejects(runtime.client('session.attention', input), /unknown Personal client method/);
  });
  await scenario('late result attention is denied after Routine pause or superseded requirements even if cancellation fails', async () => {
    const attention = async turnID => (await runtime.call('capability.invoke', { capability: 'session.attention', input: { session_id: main.main_session_id, agent_id: main.agent_id, turn_id: turnID, outcome: 'completed', user_space_id: 'local', product_domain: 'personal' } })).output;
    for (const policy of ['routine-pause', 'superseded']) {
      let routine;
      let task;
      if (policy === 'routine-pause') {
        routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'late-attention-routine', title: 'Stop pending attention', prompt: 'Assess requested changes', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC' })).routine;
        const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'late-attention-event', binding_revision: binding.revision };
        await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
        task = (await runtime.client('tasks.list')).tasks.find(item => item.routine_id === routine.routine_id);
      } else task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'late-attention-version', title: 'Versioned notification', goal: 'First requirements version' })).task;
      host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified old result' }]);
      await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: `Result ${policy}`, evidence: [{ seq: 1, description: 'Verified result' }] }, task.execution.session_id);
      const turnID = `late-result-${policy}`, queueID = `late-queue-${policy}`;
      host.intercept = (service, p) => { if (service === 'host.session.send' && p.cause === 'personal.task.result') { const turn = { request_id: p.request_id, session_id: p.session_id, state: 'queued', turn_id: turnID, queue_id: queueID }; host.turns.set(p.request_id, turn); return turn; } };
      host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed'); host.intercept = undefined;
      task = (await runtime.client('tasks.get', { task_id: task.task_id })).task; assert.equal((await attention(turnID)).allowed, true);
      const deliveryID = task.delivery.request_id;
      host.intercept = (service, p) => { if (service === 'host.session.cancel' && p.queue_id === queueID) throw new Error('queued cancellation unavailable'); };
      if (routine) {
        routine = (await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id);
        const paused = (await runtime.client('routines.pause', { routine_id: routine.routine_id, expected_revision: routine.revision })).routine; assert.equal(paused.cancellation_pending, true);
      } else {
        task = (await runtime.client('tasks.resume', { task_id: task.task_id, expected_revision: task.revision, goal: 'New requirements supersede the old result' })).task;
        assert.equal(task.requirements_revision, 2); assert.equal(task.previous_deliveries[0].cancel_pending, true);
      }
      host.intercept = undefined; const receipt = host.turns.get(deliveryID); receipt.state = 'completed'; receipt.final_output = 'Late old result';
      assert.deepEqual(await attention(turnID), { allowed: false });
      await runtime.close(); runtime = await new Runtime(host).start(); assert.deepEqual(await attention(turnID), { allowed: false });
    }
  });
  await scenario('paused reminder attention remains denied after explicit Routine resume', async () => {
    let routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'late-reminder-attention', title: 'Old reminder', prompt: 'Remind only while requested', kind: 'reminder', cron: '*/5 * * * *', timezone: 'UTC' })).routine;
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'old-reminder-event', binding_revision: binding.revision };
    await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } });
    const receipt = [...host.turns.values()].filter(turn => turn.request_id.startsWith('personal-reminder-')).at(-1);
    const attention = async () => (await runtime.call('capability.invoke', { capability: 'session.attention', input: { session_id: main.main_session_id, agent_id: main.agent_id, turn_id: receipt.turn_id, outcome: 'completed', user_space_id: 'local', product_domain: 'personal' } })).output;
    assert.equal((await attention()).allowed, true);
    routine = (await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id);
    routine = (await runtime.client('routines.pause', { routine_id: routine.routine_id, expected_revision: routine.revision })).routine;
    receipt.state = 'completed'; receipt.final_output = 'Late reminder'; assert.deepEqual(await attention(), { allowed: false });
    await runtime.client('routines.resume', { routine_id: routine.routine_id, expected_revision: routine.revision }); assert.deepEqual(await attention(), { allowed: false });
  });
  await scenario('notification aggregation settings validate bounds and expose only current permitted attention policy', async () => {
    let settings = (await runtime.client('settings.get')).settings; assert.equal(settings.notification_batch_seconds, 0);
    const update = notification_batch_seconds => runtime.client('settings.update', { expected_revision: settings.revision, notifications: true, quiet_hours: settings.quiet_hours, notification_batch_seconds });
    for (const value of [-1, 901, 1.5, '60']) await assert.rejects(update(value), /batch|integer|900/i);
    settings = (await update(60)).settings; assert.equal(settings.notification_batch_seconds, 60);
    settings = (await runtime.client('settings.update', { expected_revision: settings.revision, notifications: true, quiet_hours: settings.quiet_hours })).settings; assert.equal(settings.notification_batch_seconds, 60, 'omission preserves saved policy');
    const input = { session_id: main.main_session_id, agent_id: main.agent_id, turn_id: 'persisted-main-turn', outcome: 'completed', user_space_id: 'local', product_domain: 'personal' };
    assert.deepEqual((await runtime.call('capability.invoke', { capability: 'session.attention', input })).output, { allowed: true, batch_window_seconds: 60 });
    settings = (await update(0)).settings; assert.equal(settings.notification_batch_seconds, 0);
    assert.deepEqual((await runtime.call('capability.invoke', { capability: 'session.attention', input })).output, { allowed: true, batch_window_seconds: 0 });
  });
  await scenario('agent history retains persisted timestamps and search forwards bounded time cursor filters within its owner', async () => {
    const at = '2026-10-18T08:30:00Z';
    host.histories.set(main.main_session_id, [{ seq: 1, role: 'user', content: 'Dated conversation', at }]);
    assert.equal((await runtime.client('agents.history', { agent_id: main.agent_id })).records[0].at, at);
    host.intercept = (service, p) => {
      if (service !== 'host.session.history.search') return;
      assert.equal(p.session_id, main.main_session_id); assert.equal(p.query, 'conversation'); assert.equal(p.time_min, '2026-10-18T00:00:00Z'); assert.equal(p.time_max, '2026-10-19T00:00:00Z'); assert.equal(p.cursor, 'owned-history-cursor'); assert.equal(p.limit, 25); assert.equal(p.view, 'conversation');
      return { session_id: p.session_id, head_seq: 20, snapshot_seq: 20, matches: [{ seq: 1, role: 'user', excerpt: 'Dated conversation', at }], has_more: true, next_cursor: 'next-owned-cursor' };
    };
    const search = await runtime.client('agents.search', { agent_id: main.agent_id, query: 'conversation', time_min: '2026-10-18T00:00:00Z', time_max: '2026-10-19T00:00:00Z', cursor: 'owned-history-cursor', limit: 1000, session_id: 'untrusted-ignored-session' });
    assert.equal(search.matches[0].at, at); assert.equal(search.next_cursor, 'next-owned-cursor'); assert.equal(search.has_more, true); host.intercept = undefined;
    await runtime.client('agents.search', { agent_id: main.agent_id, query: 'conversation' });
  });
  await scenario('queued Calendar result attention revalidates live source authority on completion and replay without self dedupe', async () => {
    const source = { kind: 'calendar', account_id: 'attention-calendar-account', generation: 'attention-generation-a', calendar_id: 'primary', time_min: new Date().toISOString(), time_max: new Date(Date.now() + 86400000).toISOString(), timezone: 'UTC' };
    const provenance = { ...source, provider: 'google-calendar', user_space_id: 'local', fetched_at: new Date().toISOString(), read_only: true };
    const change = { event_id: 'attention-calendar-change', calendar_event_id: 'attention-calendar-event', kind: 'updated', after: { id: 'attention-calendar-event', summary: 'Changed meeting' }, occurred_at: new Date().toISOString(), source: provenance };
    host.intercept = (service, p) => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', available: true, baseline: !p.cursor, cursor: p.cursor ? 'attention-cursor-1' : 'attention-cursor-0', changes: p.cursor ? [change] : [], source: provenance } : undefined;
    const routine = (await runtime.client('routines.create', { agent_id: main.agent_id, request_id: 'attention-source-fence', title: 'Watch current account', prompt: 'Assess calendar changes', kind: 'task', cron: '*/5 * * * *', timezone: 'UTC', source })).routine;
    const fire = async eventID => { const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: eventID, binding_revision: binding.revision }; await runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params: { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload } }); };
    await fire('attention-source-tick-1'); await fire('attention-source-tick-2');
    let task = (await runtime.client('tasks.list')).tasks.find(item => item.routine_id === routine.routine_id);
    host.histories.set(task.execution.session_id, [{ seq: 1, role: 'tool', tool_result: 'Verified calendar assessment' }]);
    await runtime.tool('complete_task', { task_id: task.task_id, requirements_revision: 1, summary: 'The meeting changed', evidence: [{ seq: 1, description: 'Verified assessment' }] }, task.execution.session_id);
    host.turns.get(task.execution.request_id).state = 'completed'; await runtime.lifecycle(task, 'completed');
    task = (await runtime.client('tasks.get', { task_id: task.task_id })).task;
    const receipt = host.turns.get(task.delivery.request_id); receipt.state = 'completed'; receipt.final_output = 'The meeting changed';
    await runtime.call('capability.invoke', { capability: 'agent.turn.lifecycle', input: { thread_id: main.main_session_id, request_id: receipt.request_id, state: 'completed', final_output: receipt.final_output } });
    const attention = async () => (await runtime.call('capability.invoke', { capability: 'session.attention', input: { session_id: main.main_session_id, agent_id: main.agent_id, turn_id: receipt.turn_id, outcome: 'completed', user_space_id: 'local', product_domain: 'personal' } })).output;
    assert.equal((await attention()).allowed, true, 'recording delivery must not make its own attention look like a duplicate result');
    let releaseStatus, enteredStatus;
    const heldStatus = new Promise(resolve => { releaseStatus = resolve; });
    const entered = new Promise(resolve => { enteredStatus = resolve; });
    let holdOnce = true;
    host.intercept = async service => {
      if (service !== 'calendar.personal') return;
      if (holdOnce) { holdOnce = false; enteredStatus(); await heldStatus; }
      return { state: 'ready', freshness: 'fresh', source: provenance };
    };
    const heldAttention = attention(); await entered;
    const currentAgent = (await runtime.client('agents.list')).agents.find(agent => agent.agent_id === main.agent_id);
    await runtime.client('agents.update', { agent_id: main.agent_id, expected_revision: currentAgent.revision, name: currentAgent.name, communication: 'Changed while attention awaits Calendar status' });
    releaseStatus();
    assert.deepEqual(await heldAttention, { allowed: false }, 'a callback must not outlive a concurrent authority mutation');

    for (const transient of [{ state: 'refreshing', freshness: 'stale', source: provenance }, { state: 'ready', source: provenance }]) {
      host.intercept = service => service === 'calendar.personal' ? transient : undefined;
      assert.deepEqual(await attention(), { allowed: false }); assert.notEqual((await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id).source_blocked, true);
    }
    host.intercept = service => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', source: provenance } : undefined;
    assert.equal((await attention()).allowed, true);
    host.intercept = service => service === 'calendar.personal' ? { state: 'ready', freshness: 'fresh', source: { ...provenance, generation: 'attention-generation-b' } } : undefined;
    assert.deepEqual(await attention(), { allowed: false }); assert.equal((await runtime.client('routines.list')).routines.find(item => item.routine_id === routine.routine_id).source_blocked, true);
    await runtime.close(); runtime = await new Runtime(host).start(); assert.deepEqual(await attention(), { allowed: false }); host.intercept = undefined;
  });
  await runtime.close(); evidence.passed = true;
} catch (error) { evidence.passed = false; evidence.error = error.stack; throw error; }
finally { for (const runtime of active) runtime.process.kill(); await mkdir(evidencePath, { recursive: true }); await writeFile(join(evidencePath, 'evidence.json'), JSON.stringify(evidence, null, 2)); await rm(scratch, { recursive: true, force: true }); }
