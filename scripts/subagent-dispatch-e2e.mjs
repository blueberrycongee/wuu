#!/usr/bin/env node
// Real app-server, plugin generations, persisted state and loopback provider.
// No real credentials or user home. Build WUU_E2E_CORE before running.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { EventEmitter, once } from 'node:events';
import { mkdir, mkdtemp, readFile, readdir, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { dirname, join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { DatabaseSync } from 'node:sqlite';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const binary = resolve(root, process.env.WUU_E2E_CORE ?? 'bin/wuu');
const output = resolve(root, process.env.WUU_SUBAGENT_E2E_OUTPUT ?? '.tmp/e2e/subagent-dispatch');
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, 'run-'));
const home = join(scratch, 'state'), packageRoot = join(scratch, 'package');
await mkdir(home); await mkdir(packageRoot);
const env = { PATH: process.env.PATH, HOME: scratch, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
const configPath = join(home, 'config.json');
const events = new EventEmitter(), messages = [], providerCalls = [], publications = [];
const result = { status: 'failed', scratch, checks: [], limitations: ['Synthetic model responses; real app-server, plugin reload, storage, locks and session delivery. No desktop or reasoning-quality claim.'] };
let child, held, sequence = 0, stderr = '';
function wait(event, history, predicate, label) {
  const found = history.find(predicate); if (found) return Promise.resolve(found);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off(event, listener); reject(new Error(`Timed out: ${label}`)); }, 30000);
    const listener = value => { if (!predicate(value)) return; clearTimeout(timer); events.off(event, listener); resolve(value); };
    events.on(event, listener);
  });
}
async function rpc(method, params = {}) {
  const id = `dispatch-${++sequence}`;
  child.stdin.write(JSON.stringify({ id, method, params }) + '\n');
  const message = await wait('message', messages, message => message.id === id, method);
  assert(!message.error, `${method}: ${JSON.stringify(message.error)}`);
  return message.result;
}
async function start() {
  child = spawn(binary, ['app-server', '--workdir', scratch, '--config', configPath], { env, stdio: ['pipe', 'pipe', 'pipe'] });
  child.stderr.setEncoding('utf8').on('data', data => { stderr += data; });
  createInterface({ input: child.stdout }).on('line', line => {
    try { const message = JSON.parse(line); messages.push(message); events.emit('message', message); }
    catch { stderr += `\nnon-JSON stdout: ${line}`; }
  });
  const initialized = await rpc('initialize', { protocol_version: 'wuu-app-server/v0.1', client: { name: 'subagent-dispatch-e2e' } });
  const plugin = initialized.extension_inventory.find(item => item.provenance?.plugin_id === 'subagent' && item.package_source === 'dev');
  assert.equal(plugin?.runtime_state, 'active', JSON.stringify(initialized.extension_inventory));
  return plugin;
}
async function stop() {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, 'exit');
  const timer = setTimeout(() => child.kill('SIGKILL'), 10000);
  child.stdin.end();
  try { const [code, signal] = await exited; assert.equal(signal, null, stderr); assert.equal(code, 0, stderr); }
  finally { clearTimeout(timer); }
}
async function publish(bytes) {
  await writeFile(join(packageRoot, 'runtime.mjs'), bytes);
  const run = spawnSync(binary, ['plugin', 'dev', '--watch=false', packageRoot], { env, encoding: 'utf8', timeout: 30000 });
  publications.push({ status: run.status, stdout: run.stdout, stderr: run.stderr });
  assert.equal(run.status, 0, run.stderr);
}
async function allFiles(path) {
  const entries = await readdir(path, { withFileTypes: true });
  return (await Promise.all(entries.map(item => item.isDirectory() ? allFiles(join(path, item.name)) : [join(path, item.name)]))).flat();
}
async function ledger() {
  const files = (await allFiles(home)).filter(path => path.includes('plugin-storage') && path.endsWith('.json'));
  for (const path of files) {
    const doc = JSON.parse(await readFile(path, 'utf8'));
    if (doc.values['tasks.v3']) return { path, index: JSON.parse(doc.values['tasks.v3']) };
  }
  return undefined;
}
async function until(predicate, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) { const value = await predicate(); if (value) return value; await new Promise(resolve => setTimeout(resolve, 25)); }
  throw new Error(`Timed out: ${label}`);
}
function text(content) { return typeof content === 'string' ? content : content?.map(part => part.text ?? '').join('') ?? ''; }
function reply(response, body, content) {
  const tool = content[0].type === 'tool_use';
  if (!body.stream) { response.writeHead(200, { 'Content-Type': 'application/json' }); response.end(JSON.stringify({ id: 'fixture', type: 'message', role: 'assistant', model: body.model, content, stop_reason: tool ? 'tool_use' : 'end_turn', usage: { input_tokens: 11, output_tokens: 3 } })); return; }
  response.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const emit = (type, fields = {}) => response.write(`event: ${type}\ndata: ${JSON.stringify({ type, ...fields })}\n\n`);
  emit('message_start', { message: { id: 'fixture', type: 'message', role: 'assistant', model: body.model, content: [], usage: { input_tokens: 11, output_tokens: 0 } } });
  content.forEach((block, index) => {
    emit('content_block_start', { index, content_block: block.type === 'tool_use' ? { ...block, input: {} } : { type: 'text', text: '' } });
    emit('content_block_delta', { index, delta: block.type === 'tool_use' ? { type: 'input_json_delta', partial_json: JSON.stringify(block.input) } : { type: 'text_delta', text: block.text } });
    emit('content_block_stop', { index });
  });
  emit('message_delta', { delta: { stop_reason: tool ? 'tool_use' : 'end_turn' }, usage: { output_tokens: 3 } });
  emit('message_stop'); response.end();
}
const provider = createServer(async (request, response) => {
  try {
    let raw = ''; for await (const chunk of request) raw += chunk;
    const body = JSON.parse(raw);
    const prompt = [...(body.messages ?? [])].reverse().map(item => text(item.content)).find(value => value.startsWith('dispatch-e2e:'))?.match(/^dispatch-e2e:[a-z-]+/)?.[0] ?? 'auxiliary';
    const hasResult = body.messages?.some(item => Array.isArray(item.content) && item.content.some(block => block.type === 'tool_result'));
    const hasLateResult = body.messages?.some(item => Array.isArray(item.content) && item.content.some(block => block.type === 'tool_result' && block.tool_use_id === 'late-tool'));
    const call = { prompt, body }; providerCalls.push(call); events.emit('provider', call);
    if (prompt === 'dispatch-e2e:held-child') {
      held = () => reply(response, body, [{ type: 'text', text: 'Held child completed' }]);
      events.emit('held'); return;
    }
    if (prompt === 'dispatch-e2e:held-parent' && hasResult && !hasLateResult) {
      const name = body.tools?.find(tool => tool.name.includes('spawn_agent'))?.name;
      reply(response, body, [{ type: 'tool_use', id: 'late-tool', name, input: { name: 'late_child', description: 'Late retained child', prompt: 'dispatch-e2e:late-child' } }]); return;
    }
    if (['dispatch-e2e:held-parent', 'dispatch-e2e:cold-parent'].includes(prompt) && !hasResult) {
      const name = body.tools?.find(tool => tool.name.includes('spawn_agent'))?.name;
      assert(name, 'Subagent spawn tool must be visible to the real provider request');
      const cold = prompt.includes('cold');
      reply(response, body, [{ type: 'tool_use', id: cold ? 'cold-tool' : 'held-tool', name, input: {
        name: cold ? 'recover_child' : 'held_child', description: cold ? 'Recover child' : 'Hold child',
        prompt: cold ? 'dispatch-e2e:recovered-child' : 'dispatch-e2e:held-child',
        ...(cold ? { model: 'recovery' } : { run_in_background: false }),
      } }]); return;
    }
    reply(response, body, [{ type: 'text', text: prompt === 'dispatch-e2e:recovered-child' ? 'Recovered child completed' : prompt === 'dispatch-e2e:late-child' ? 'Late child completed' : 'Parent observed result' }]);
  } catch (error) { response.writeHead(500); response.end(String(error)); }
});
await new Promise(resolve => provider.listen(0, '127.0.0.1', resolve));
try {
  result.core_sha256 = createHash('sha256').update(await readFile(binary)).digest('hex');
  const current = await readFile(join(root, 'internal/plugin/bundled/subagent/runtime.mjs'));
  const baseline = process.env.WUU_SUBAGENT_BASELINE_RUNTIME ? await readFile(process.env.WUU_SUBAGENT_BASELINE_RUNTIME) : Buffer.from(current + '\n// First acceptance generation.\n');
  result.runtime_sha256 = createHash('sha256').update(current).digest('hex');
  result.baseline_sha256 = createHash('sha256').update(baseline).digest('hex');
  await writeFile(join(packageRoot, 'plugin.json'), JSON.stringify({ schemaVersion: 1, id: 'subagent', name: 'Subagent', version: '0.1.0', runtime: { protocol: 'wuu-plugin-v1', command: 'node', args: ['runtime.mjs'], timeout: 30 } }));
  await writeFile(join(packageRoot, 'package.json'), JSON.stringify({ name: 'subagent', version: '0.1.0', scripts: { build: 'node -e ""' } }));
  await writeFile(configPath, JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'anthropic', base_url: `http://127.0.0.1:${provider.address().port}`, api_key: 'synthetic-no-real-key', model: 'fixture' } }, agent: { permission_mode: 'standard' }, skills: { enabled: false }, ptc: { enabled: false } }));
  await publish(baseline);
  const initial = await start();
  const parent = (await rpc('thread/start', { provider: 'fixture', model: 'fixture' })).thread;
  const first = (await rpc('turn/start', { thread_id: parent.id, prompt: 'dispatch-e2e:held-parent' })).turn;
  await until(() => held, 'foreground child request');
  const publicationStart = messages.length;
  await publish(current);
  await wait('message', messages.slice(publicationStart), message => message.method === 'plugin/inventory/changed' && message.params.extension_inventory.some(item => item.provenance?.plugin_id === 'subagent' && item.fingerprint !== initial.fingerprint && item.runtime_state === 'active'), 'replacement generation');
  const other = (await rpc('thread/start', { provider: 'fixture', model: 'fixture' })).thread;
  const cold = (await rpc('turn/start', { thread_id: other.id, prompt: 'dispatch-e2e:cold-parent' })).turn;
  await wait('message', messages, message => message.method === 'turn/completed' && message.params.turn.id === cold.id, 'rejected child launch');
  const before = await ledger();
  const pending = before.index.dispatches.find(item => !item.result && !item.cancelled && item.record.name === 'recover_child');
  assert(pending, 'Failed host create must leave the original durable intent');
  held();
  await wait('message', messages, message => message.method === 'turn/completed' && message.params.turn.id === first.id, 'retained foreground generation');
  assert((await ledger()).index.dispatches.some(item => item.id === pending.id && !item.result), 'Retained foreground save must not erase the replacement dispatch');
  result.checks.push('same-ID hot reload preserves a failed new dispatch while the retained foreground generation writes its result');
  await wait('message', messages, message => message.params?.thread_id === parent.id && JSON.stringify(message).includes('Late child completed'), 'late retained-generation child delivery');
  result.checks.push('a child launched by the retained generation after publication still delivers to its parent');
  await stop();
  const config = JSON.parse(await readFile(configPath, 'utf8'));
  config.agent.model_aliases = { recovery: { provider: 'fixture', model: 'fixture' } };
  await writeFile(configPath, JSON.stringify(config));
  const restartStart = providerCalls.length;
  await start();
  await wait('provider', providerCalls.slice(restartStart), call => call.prompt === 'dispatch-e2e:recovered-child', 'cold-start child recovery after router bind');
  const accepted = await until(async () => (await ledger()).index.dispatches.find(item => item.id === pending.id && item.result && item.settled), 'terminal recovered dispatch receipt');
  const db = new DatabaseSync(join(home, 'sessions/sessions.sqlite3'), { readOnly: true });
  try {
    const children = db.prepare('SELECT id, parent_id, creation_request_id FROM sessions WHERE parent_id = ?').all(other.id);
    assert.equal(children.length, 1); assert.equal(children[0].parent_id, other.id);
    assert.equal(children[0].id, accepted.result.record.session_id);
    assert.equal(children[0].creation_request_id, pending.create.request_id);
    result.recovered_child = children[0];
  } finally { db.close(); }
  assert.equal(providerCalls.filter(call => call.prompt === 'dispatch-e2e:recovered-child').length, 1);
  result.checks.push('real cold start waits for router binding and recovers the original intent into exactly one child and one model turn');
  await stop();
  await start();
  const finalLedger = await ledger();
  assert(finalLedger.index.dispatches.find(item => item.id === pending.id)?.result);
  assert.equal(providerCalls.filter(call => call.prompt === 'dispatch-e2e:recovered-child').length, 1);
  result.checks.push('a second restart preserves the accepted terminal receipt without replaying the child');
  await stop();
  result.status = 'passed';
} catch (error) { result.error = error.stack; process.exitCode = 1; }
finally {
  await stop().catch(error => { result.cleanup_error = String(error); process.exitCode = 1; });
  provider.closeAllConnections(); await new Promise(resolve => provider.close(resolve));
  await writeFile(join(output, 'result.json'), JSON.stringify(result, null, 2));
  await writeFile(join(output, 'protocol.json'), JSON.stringify(messages, null, 2));
  await writeFile(join(output, 'provider.json'), JSON.stringify(providerCalls, null, 2));
  await writeFile(join(output, 'publications.json'), JSON.stringify(publications, null, 2));
  await writeFile(join(output, 'stderr.log'), stderr);
  console.log(JSON.stringify(result, null, 2));
}
