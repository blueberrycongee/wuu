#!/usr/bin/env node
// Failure-first public runtime contracts: interrupted identity creation, concurrent
// default creation, lost main sessions, stale edits, unapproved specialists,
// durable dispatch, stale/duplicate completion, cancellation, and unknown outcomes.
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
      case 'calendar.personal': if (this.calendar) return this.calendar.call('service.invoke', { service, method, caller: 'personal', params: p }); throw new Error('calendar service unavailable');
      case 'automation.routines': {
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
      case 'host.session.history.read': assert(p.start_seq >= 1 || p.cursor, 'history read requires start_seq or cursor'); assert((p.limit ?? 0) <= 50, 'history limit exceeds host contract'); return { session_id: p.session_id, head_seq: (this.histories.get(p.session_id) ?? []).length, snapshot_seq: 20, records: (this.histories.get(p.session_id) ?? []).filter(r => (!p.start_seq || r.seq >= p.start_seq) && (!p.end_seq || r.seq <= p.end_seq)) };
      case 'host.session.history.search': assert(p.limit >= 1 && p.limit <= 25, 'history search limit exceeds public host maximum25'); assert.equal(p.view, 'conversation'); return { matches: [] };
      case 'host.session.attachment.read': {
        const record = (this.histories.get(p.session_id) ?? []).find(record => record.seq === p.seq);
        const attachment = record?.attachments?.find(item => item.kind === p.kind && item.index === p.index && item.sha256 === p.sha256);
        if (!attachment) throw new Error('attachment not found in selected session history');
        const encoded = Buffer.from('attached bytes').toString('base64'), offset = p.offset ?? 0;
        return { data: encoded.slice(offset, offset + 8), offset, total: encoded.length, content_type: attachment.mime_type, name: attachment.name, sha256: attachment.sha256 };
      }
      case 'host.session.cancel': {
        const turns = [...this.turns.values()].filter(t => t.session_id === p.session_id && (!p.turn_id || p.turn_id === t.turn_id) && (!p.queue_id || p.queue_id === t.queue_id));
        for (const turn of turns) if (!['completed', 'failed', 'discarded', 'interrupted'].includes(turn.state)) turn.state = 'interrupted';
        return { session_id: p.session_id, cancelled: turns.length > 0 };
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
    if (method === 'agents.send') this.host.userInputs.set(input.request_id, { prompt: input.prompt, attachments: input.attachments, execution_id: `x-${this.seq}` });
    return this.call('capability.invoke', { capability: 'plugin.client.request', input: { method, input }, execution_id: `x-${this.seq}` }).then(r => r.output.result).finally(() => { if (method === 'agents.send') this.host.userInputs.delete(input.request_id); });
  }
  tool(tool_id, args, session_id) { return this.call('tool.execute', { tool_id, arguments: args, session_id, thread_id: session_id, cwd: scratch, call_id: `c-${this.seq}`, execution_id: `e-${this.seq}` }).then(r => JSON.parse(r.result.content[0].text)); }
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
  const host = new Host(); let runtime = await new Runtime(host).start(); let main, specialist, task;
  await scenario('Coding initialization has no Personal tools, services, or writes', async () => {
    const codingHost = new Host(), coding = await new Runtime(codingHost).start('coding');
    assert.equal(coding.definition.tools?.length ?? 0, 0); assert.equal(coding.definition.provided_services?.length ?? 0, 0); assert.equal(codingHost.calls.length, 0); await coding.close();
  });
  await scenario('concurrent default requests bind exactly one persistent main session', async () => {
    const results = await Promise.all([runtime.client('agents.ensure_default'), runtime.client('agents.ensure_default')]);
    main = results[0].agent; assert.equal(main.main_session_id, results[1].agent.main_session_id); assert.equal(host.sessions.size, 1); assert.equal(main.kind, 'main');
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
  await scenario('missing bound session fails without silent replacement', async () => {
    const original = host.sessions.get(main.main_session_id); host.sessions.delete(main.main_session_id);
    await assert.rejects(runtime.client('agents.open', { agent_id: main.agent_id }), /recover|missing|unavailable/i); assert.equal(host.sessions.size, 0); host.sessions.set(main.main_session_id, original);
  });
  await scenario('specialist requires main proposal and explicit confirmation', async () => {
    const proposal = await runtime.tool('propose_specialist', { name: 'Fern', responsibility: 'Travel planning' }, main.main_session_id);
    assert.equal((await runtime.client('agents.list')).agents.length, 1);
    specialist = (await runtime.client('agents.confirm_specialist', { proposal_id: proposal.proposal.proposal_id, approved: true })).agent;
    const retry = (await runtime.client('agents.confirm_specialist', { proposal_id: proposal.proposal.proposal_id, approved: true })).agent; assert.equal(retry.main_session_id, specialist.main_session_id);
    await assert.rejects(runtime.tool('propose_specialist', { name: 'No', responsibility: 'No' }, specialist.main_session_id), /main/i);
  });
  await scenario('background dispatch is durable, hidden, and leaves main chat available', async () => {
    task = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'task-one', title: 'Plan supper', goal: 'Research a supper plan' })).task;
    const retry = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'task-one', title: 'Plan supper', goal: 'Research a supper plan' })).task;
    assert.equal(task.task_id, retry.task_id); assert.equal(host.sessions.get(task.execution.session_id).visibility, 'plugin');
    await runtime.client('agents.send', { agent_id: main.agent_id, request_id: 'chat-one', prompt: 'Hello meanwhile' });
    assert([...host.turns.values()].some(t => t.session_id === main.main_session_id));
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
  await scenario('waiting for the user survives terminal observation and repeated recovery', async () => {
    let waiting = (await runtime.client('tasks.create', { agent_id: main.agent_id, request_id: 'waiting-one', title: 'Choose a date', goal: 'Wait for the user to choose Sunday' })).task;
    await runtime.tool('report_task', { task_id: waiting.task_id, requirements_revision: waiting.requirements_revision, state: 'waiting_user', progress: 'Which Sunday works?' }, waiting.execution.session_id);
    await runtime.lifecycle(waiting, 'completed'); waiting = (await runtime.client('tasks.get', { task_id: waiting.task_id })).task;
    assert.equal(waiting.state, 'waiting_user'); assert.equal(waiting.reconciliation_required, false);
    const revision = waiting.revision; await runtime.lifecycle(waiting, 'completed');
    assert.equal((await runtime.client('tasks.get', { task_id: waiting.task_id })).task.revision, revision);
  });
  await scenario('memory correction forwards trusted session and excludes client routing fields', async () => {
    let forwarded;
    host.intercept = (service, p) => { if (service === 'memory.records') { forwarded = p; return { version: 1, record: { id: 'memory-one', revision: 2 } }; } };
    await runtime.client('memory.correct', { agent_id: main.agent_id, request_id: 'edit-one', id: 'memory-one', expected_revision: 1, content: 'Updated preference' });
    assert.equal(forwarded.session_id, main.main_session_id); assert.equal(forwarded.evidence, 'explicit'); assert.equal(forwarded.sources[0].kind, 'user_edit'); assert(!('request_id' in forwarded)); assert(!('agent_id' in forwarded)); host.intercept = undefined;
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
  await scenario('routine creation has one stable scheduler binding and repeated event delivers once', async () => {
    const args = { agent_id: main.agent_id, request_id: 'remind-one', title: 'Drink water', prompt: 'Remind me to drink water', kind: 'reminder', run_at: '2026-10-18T10:00:00Z', timezone: 'UTC' };
    const routine = (await runtime.client('routines.create', args)).routine;
    const retry = (await runtime.client('routines.create', args)).routine; assert.equal(routine.routine_id, retry.routine_id); assert.equal(host.bindings.size, 1);
    const binding = host.bindings.get(routine.binding.binding_id); binding.pending_event = { event_id: 'event-one', binding_revision: binding.revision, scheduled_at: '2026-10-18T10:00:00Z' };
    const params = { binding_id: binding.binding_id, ...binding.pending_event, payload: binding.payload };
    const fire = () => runtime.call('service.invoke', { service: 'automation.trigger', method: 'fire', caller: 'automation', params });
    const before = host.turns.size; assert((await fire()).defer_until); const reminder = [...host.turns.values()].at(-1); reminder.state = 'completed'; reminder.final_output = 'Time to drink water'; assert.equal((await fire()).accepted, true); await fire(); assert.equal(host.turns.size, before + 1);
    await runtime.client('routines.pause', { routine_id: routine.routine_id, expected_revision: (await runtime.client('routines.list')).routines[0].revision });
    binding.pending_event.event_id = 'late-event'; params.event_id = 'late-event'; await fire(); assert.equal(host.turns.size, before + 1);
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
