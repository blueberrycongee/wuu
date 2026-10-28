// Public-contract acceptance: external plugins inspect request-scoped live
// progress in the real app-server. Failure cases are ownership disclosure,
// stale/missing request identity, pending-input attribution, hidden content,
// unbounded output, stale terminal state and cancellation across sessions.
import assert from 'node:assert/strict';
import {execFile, spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {once} from 'node:events';
import {cp, mkdir, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from './public-package.mjs';

const exec = promisify(execFile);
const sdk = fileURLToPath(new URL('../', import.meta.url));
const root = resolve(sdk, '../..');
const binary = resolve(process.env.WUU_SESSION_PROGRESS_BINARY ?? join(root, '.tmp/bin/wuu'));
const output = resolve(process.env.WUU_SESSION_PROGRESS_OUTPUT ?? join(root, '.tmp/e2e/session-progress'));
await mkdir(output, {recursive: true});
const run = await mkdtemp(join(output, 'run-'));
const home = join(run, 'home'), workspace = join(run, 'workspace');
await mkdir(home); await mkdir(workspace);
const configPath = join(home, 'config.json');
const env = {PATH: process.env.PATH, HOME: run, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath};
const transcript = [], checks = [], samples = [];
const report = {passed: false, run, checks, samples,
  limitations: ['Deterministic public engine, not model-quality or GUI acceptance.', 'Replace-all snapshots are not event replay, delivery receipts or persisted recovery.']};
let server;
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const command = async args => {
  const reply = await exec(binary, args, {env, cwd: workspace});
  transcript.push({command: args, stdout: reply.stdout, stderr: reply.stderr});
  return reply.stdout;
};
function startServer() {
  const child = spawn(binary, ['app-server', '--workdir', workspace, '--config', configPath], {env, stdio: ['pipe', 'pipe', 'pipe']});
  const pending = new Map(), frames = [], listeners = new Set();
  const exited = once(child, 'exit');
  let sequence = 0, ended, stderr = '';
  child.stderr.on('data', bytes => {stderr += String(bytes); transcript.push({stderr: String(bytes)});});
  createInterface({input: child.stdout}).on('line', line => {
    transcript.push({received: line});
    let frame; try {frame = JSON.parse(line);} catch {return;}
    frames.push(frame);
    const waiter = pending.get(String(frame.id));
    if (waiter) {pending.delete(String(frame.id)); clearTimeout(waiter.timer); waiter.resolve(frame);}
    for (const listener of listeners) listener();
  });
  child.on('exit', (code, signal) => {
    ended = new Error(`app-server exited (${code ?? signal}): ${stderr}`);
    for (const waiter of pending.values()) {clearTimeout(waiter.timer); waiter.reject(ended);}
    pending.clear(); for (const listener of listeners) listener();
  });
  return {
    async raw(method, params = {}) {
      if (ended) throw ended;
      return new Promise((resolve, reject) => {
        const id = `progress-${++sequence}`;
        const timer = setTimeout(() => {pending.delete(id); reject(new Error(`${method} timed out: ${stderr}`));}, 30_000);
        pending.set(id, {resolve, reject, timer});
        const frame = {id, method, params}; transcript.push({sent: frame}); child.stdin.write(JSON.stringify(frame) + '\n');
      });
    },
    async request(method, params = {}) {
      const reply = await this.raw(method, params);
      assert(!reply.error, `${method}: ${JSON.stringify(reply.error)}`);
      return reply.result;
    },
    wait(predicate) {
      return new Promise((resolve, reject) => {
        const finish = (error, value) => {clearTimeout(timer); listeners.delete(check); error ? reject(error) : resolve(value);};
        const check = () => {const found = frames.find(predicate); if (found) finish(null, found); else if (ended) finish(ended);};
        const timer = setTimeout(() => finish(new Error('expected app-server event timed out')), 30_000);
        listeners.add(check); check();
      });
    },
    async close() {
      if (ended) return;
      child.stdin.end(); const timer = setTimeout(() => child.kill('SIGKILL'), 10_000);
      try {const [code, signal] = await exited; assert.equal(code, 0, `${signal}: ${stderr}`);} finally {clearTimeout(timer);}
    },
  };
}
function bounded(progress) {
  assert.equal(progress.version, 1);
  if (!progress.available) return;
  assert(progress.items.length <= 32, 'too many progress items');
  assert(Buffer.byteLength(JSON.stringify(progress)) <= 32768, 'progress exceeded JSON budget');
  assert.equal(typeof progress.truncated, 'boolean');
  for (const item of progress.items) {
    assert(['assistant', 'tool'].includes(item.type));
    assert(['in_progress', 'completed', 'failed'].includes(item.status));
    if (item.text !== undefined) {
      assert(Buffer.byteLength(item.text) <= 4096, 'text exceeded UTF-8 budget');
      assert(!item.text.includes('\uFFFD'), 'UTF-8 truncation split a character');
    }
  }
  const serialized = JSON.stringify(progress);
  for (const secret of ['PRIVATE_REASONING', 'PRIVATE_PROMPT', 'PRIVATE_TOOL_ARGS', 'PRIVATE_TOOL_RESULT']) {
    assert(!serialized.includes(secret), `progress exposed ${secret}`);
  }
}
try {
  report.binary = {path: binary, sha256: sha(await readFile(binary))};
  report.source_sha256 = sha(await readFile(fileURLToPath(import.meta.url)));
  const {project, tsc, packed, manifest} = await preparePublicSDK({sdk, temporary: run, tarball: process.env.WUU_SESSION_PROGRESS_SDK_TARBALL});
  for (const name of ['runtime.ts', 'projections.ts', 'fixture-engine.ts', 'fixture-observer.ts']) await cp(join(root, 'examples/plugins/session-progress', name), join(project, name));
  await exec(process.execPath, [tsc, 'runtime.ts', 'projections.ts', 'fixture-engine.ts', 'fixture-observer.ts', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--target', 'ES2023', '--strict', '--skipLibCheck'], {cwd: project});
  report.sdk = {name: manifest.name, version: manifest.version, tarball: packed, sha256: sha(await readFile(packed))};
  checks.push('example and deterministic engine compile outside checkout against packed public SDK only');
  const plugins = [
    {id: 'example.session-progress', source: 'fixture-observer.js', factory: 'createObservedProgressPlugin'},
    {id: 'example.session-progress-other', source: 'runtime.js', factory: 'createProgressPlugin'},
    {id: 'example.progress-engine', source: 'fixture-engine.js', factory: 'createProgressEngine'},
  ];
  for (const plugin of plugins) {
    const directory = join(run, plugin.id); await mkdir(directory);
    const wrapper = join(project, `${plugin.id}.mjs`);
    await writeFile(wrapper, `import {runJSONLRuntime} from '@wuu/plugin-sdk';\nimport {${plugin.factory}} from './${plugin.source}';\nawait runJSONLRuntime(${plugin.factory}(),{input:process.stdin,output:process.stdout});\n`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [wrapper, '--bundle', '--platform=node', '--format=esm', `--outfile=${join(directory, 'runtime.mjs')}`]);
    await writeFile(join(directory, 'plugin.json'), JSON.stringify({schema_version: 1, id: plugin.id, name: plugin.id, version: '1.0.0', runtime: {protocol: 'wuu-plugin-v1', command: process.execPath, args: ['runtime.mjs']}}));
    await command(['plugin', 'install', directory]);
  }
  const installed = JSON.parse(await readFile(configPath, 'utf8'));
  await writeFile(configPath, JSON.stringify({...installed, skills: {enabled: false}, composition: {version: 4, profile: 'minimal', patches: [{modules: Object.fromEntries(plugins.map(plugin => [plugin.id, true])), services: [{service: 'engine.wuu', major: 1, provider: 'example.progress-engine'}]}]}}, null, 2));
  server = startServer();
  const initialized = await server.request('initialize', {protocol_version: 'wuu-app-server/v0.1', client: {name: 'session-progress-public-sdk-acceptance'}});
  let inventory = initialized.extension_inventory;
  assert(Array.isArray(inventory), 'initialize omitted extension inventory');
  let requestSequence = 0;
  const invokeRaw = (id, method, input, requestID) => {
    const item = inventory.find(item => item.id === `plugin:user:${id}`);
    assert(item, `missing installed plugin ${id}`);
    return server.raw('plugin/client/request', {id: item.id, fingerprint: item.fingerprint, graph_generation: item.graph_generation, request_id: requestID ?? `client-${++requestSequence}`, method, input});
  };
  const invoke = async (id, method, input) => {
    const reply = await invokeRaw(id, method, input);
    assert(!reply.error, `${method}: ${JSON.stringify(reply.error)}`);
    return reply.result.result;
  };
  const owner = (method, input) => invoke(plugins[0].id, method, input);
  const engine = (method, input) => invoke(plugins[2].id, method, input);
  const inspect = input => owner('session.inspect', input);
  const waitText = text => server.wait(frame => !frame.id && frame.method === 'item/agentMessage/delta' && JSON.stringify(frame).includes(text));
  const started = await owner('session.start', {request_id: 'owned-A', prompt: 'PRIVATE_PROMPT active'});
  await waitText('public-progress:active');
  const identity = {session_id: started.session_id, request_id: 'owned-A'};
  const plain = await inspect(identity);
  assert.equal(plain.progress, undefined, 'legacy inspect disclosed progress without opt-in');
  const active = await inspect({...identity, include_progress: true});
  bounded(active.progress);
  assert(active.progress.available, JSON.stringify(active));
  assert.equal(active.progress.session_id, started.session_id);
  assert.equal(active.progress.request_id, 'owned-A');
  assert.equal(active.progress.turn_id, started.turn_id);
  assert.notEqual(active.turn.state, 'completed');
  assert(active.progress.items.some(item => item.type === 'assistant' && item.text?.includes('public-progress:active')));
  assert(active.progress.items.some(item => item.type === 'tool' && item.name === 'fixture_tool'));
  const bot = await owner('progress.bot', identity);
  const fleet = await owner('progress.fleet', identity);
  assert.equal(bot.available, true); assert.equal(fleet.available, true);
  assert.match(bot.text, /public-progress:active/);
  assert(fleet.activeTools.includes('fixture_tool'));
  samples.push({phase: 'before-terminal', active, bot, fleet});
  checks.push('same real active snapshot drives bot status-card and subagent fleet before terminal completion; opt-in preserves legacy response');


  const observing = invokeRaw(plugins[0].id, 'fixture.inspect-wait', {...identity, include_progress: true}, 'inspect-wait');
  // The fixture reports after submitting the terminal-wait inspect call. Host
  // callbacks may overlap: this marker does not prove waiter registration.
  const submissionDeadline = Date.now() + 10_000;
  for (;;) {
    const live = await server.request('plugin/executions/list');
    if (live.executions.some(row => row.plugin_id === plugins[0].id && row.message === 'progress-inspect-waiting')) break;
    assert(Date.now() < submissionDeadline, 'inspect wait was not submitted');
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  const observerItem = inventory.find(item => item.id === `plugin:user:${plugins[0].id}`);
  const cancelledObservation = await server.request('plugin/client/cancel', {
    id: observerItem.id, fingerprint: observerItem.fingerprint, graph_generation: observerItem.graph_generation, request_id: 'inspect-wait',
  });
  assert(cancelledObservation.cancelled);
  assert((await observing).error, 'cancelled observer returned success');
  const stillActive = await inspect({...identity, include_progress: true});
  assert.equal(stillActive.turn.state, 'running');
  assert(stillActive.progress.available, 'cancelling inspect cancelled its target');
  checks.push('cancelling a submitted terminal-wait observation leaves target generation running');

  const missing = await invokeRaw(plugins[0].id, 'session.inspect', {session_id: started.session_id, include_progress: true});
  assert(missing.error, 'include_progress accepted missing request_id');
  const stale = await inspect({...identity, request_id: 'not-submitted', include_progress: true});
  assert.equal(stale.progress.available, false);
  const foreign = await invokeRaw(plugins[1].id, 'session.inspect', {...identity, include_progress: true});
  if (!foreign.error) assert.equal(foreign.result.result.progress.available, false, 'other plugin observed private progress');
  const {thread} = await server.request('thread/start');
  const user = await inspect({session_id: thread.id, request_id: 'owned-A', include_progress: true});
  assert.equal(user.progress.available, false);
  assert.equal(user.progress.reason, 'session_not_owned');
  checks.push('missing, stale and foreign owner/request identities disclose no progress; user-owned sessions remain unavailable');

  const other = await owner('session.start', {request_id: 'owned-C', prompt: 'PRIVATE_PROMPT other'});
  await waitText('public-progress:other');
  const isolated = await inspect({session_id: other.session_id, request_id: 'owned-C', include_progress: true});
  bounded(isolated.progress); assert(isolated.progress.available);
  assert(!JSON.stringify(isolated.progress).includes('public-progress:active'));
  const pending = await owner('session.send', {session_id: started.session_id, request_id: 'owned-B', input: {prompt: 'PRIVATE_PROMPT queued'}, if_running: 'queue'});
  assert(pending.queue_id, JSON.stringify(pending));
  const queued = await inspect({session_id: started.session_id, request_id: 'owned-B', include_progress: true});
  assert.equal(queued.progress.available, false, 'queued B observed active A');
  await owner('session.cancel', {session_id: started.session_id, queue_id: pending.queue_id});
  checks.push('concurrent sessions stay isolated; queued request cannot observe another request turn');

  await engine('large-text', {tag: 'active'});
  const longText = await inspect({...identity, include_progress: true});
  bounded(longText.progress); assert(longText.progress.available);
  assert(longText.progress.items.some(item => item.type === 'assistant' && item.truncated), 'long assistant text was not marked truncated');
  samples.push({phase: 'bounded-cjk-text', snapshot: longText});
  await engine('burst', {tag: 'active'});
  const large = await inspect({...identity, include_progress: true});
  bounded(large.progress); assert(large.progress.available); assert(large.progress.truncated || large.progress.items.some(item => item.truncated));
  samples.push({phase: 'bounded-large-progress', snapshot: large});
  checks.push('large CJK output and many tool items remain inside public UTF-8/item/JSON budgets without private fields');

  await engine('complete', {tag: 'active'});
  const complete = await inspect({...identity, include_progress: true, wait: 'terminal', timeout_ms: 10_000});
  assert.equal(complete.turn.state, 'completed', JSON.stringify(complete));
  assert.match(complete.turn.final_output, /finished:active/);
  bounded(complete.progress);
  if (complete.progress.available) assert(complete.progress.items.every(item => item.status !== 'in_progress'), 'terminal snapshot retained active items');

  const next = await owner('session.send', {session_id: started.session_id, request_id: 'owned-D', input: {prompt: 'PRIVATE_PROMPT later'}});
  await server.wait(frame => !frame.id && frame.method === 'item/agentMessage/delta' && frame.params?.turn_id === next.turn_id && JSON.stringify(frame).includes('public-progress:later'));
  const old = await inspect({...identity, include_progress: true});
  assert.equal(old.turn.turn_id, started.turn_id, 'old request resolved to a newer turn');
  if (old.progress.available) {
    assert.equal(old.progress.turn_id, started.turn_id);
    assert(!JSON.stringify(old.progress).includes('public-progress:later'));
  }
  const current = await inspect({session_id: started.session_id, request_id: 'owned-D', include_progress: true});
  assert(current.progress.available); assert.equal(current.progress.turn_id, next.turn_id);
  await engine('complete', {tag: 'later'});
  await inspect({session_id: started.session_id, request_id: 'owned-D', wait: 'terminal', timeout_ms: 10_000});
  checks.push('an older completed request cannot observe a newer active turn in the same session');

  const cancel = await owner('session.cancel', {session_id: other.session_id, turn_id: other.turn_id});
  assert(cancel.cancelled);
  const canceled = await inspect({session_id: other.session_id, request_id: 'owned-C', include_progress: true, wait: 'terminal', timeout_ms: 10_000});
  assert(['cancelled', 'failed', 'interrupted'].includes(canceled.turn.state), JSON.stringify(canceled));
  bounded(canceled.progress);
  samples.push({phase: 'terminal', complete, canceled});
  checks.push('completion and cancellation settle independently; terminal observation does not return stale in-progress items');

  // Explicit compositions reject disabling a still-selected module. Remove it
  // in the disposable config and publish that selection with package disable/enable.
  const selectedConfig = JSON.parse(await readFile(configPath, 'utf8'));
  const removedConfig = structuredClone(selectedConfig);
  removedConfig.composition.patches[0].modules[plugins[0].id] = false;
  await writeFile(configPath, JSON.stringify(removedConfig, null, 2));
  const beforeDisable = inventory.find(item => item.id === `plugin:user:${plugins[0].id}`);
  const disabled = await server.request('extension/package/update', {id: beforeDisable.id, fingerprint: beforeDisable.fingerprint, action: 'disable'});
  const denied = await invokeRaw(plugins[0].id, 'progress.bot', identity);
  assert(denied.error, 'disabled plugin kept its client progress route');
  const disabledItem = disabled.extension_inventory.find(item => item.id === beforeDisable.id);
  await writeFile(configPath, JSON.stringify({...selectedConfig, plugins: JSON.parse(await readFile(configPath, 'utf8')).plugins}, null, 2));
  const enabled = await server.request('extension/package/update', {id: disabledItem.id, fingerprint: disabledItem.fingerprint, action: 'enable'});
  const staleClient = await invokeRaw(plugins[0].id, 'progress.bot', identity);
  assert(staleClient.error, 'stale generation kept its client route after re-enable');
  inventory = enabled.extension_inventory;
  const restored = await owner('progress.bot', identity);
  assert.equal(typeof restored.available, 'boolean');
  checks.push('module removal and disable revoke observation; re-enable/reselection require fresh generation identity');

  await server.close(); server = undefined;
  report.passed = true;
} catch (error) {
  report.error = error.stack ?? String(error);
  throw error;
} finally {
  try {await server?.close();} catch (error) {report.cleanup_error = String(error); report.passed = false;}
  await writeFile(join(run, 'report.json'), JSON.stringify(report, null, 2));
  await writeFile(join(run, 'transcript.jsonl'), transcript.map(frame => JSON.stringify(frame)).join('\n') + '\n');
  console.log(JSON.stringify({passed: report.passed, report: join(run, 'report.json'), checks: checks.length}));
}
