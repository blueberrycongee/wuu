import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { createServer } from 'node:http';
import { createInterface } from 'node:readline';
import { once } from 'node:events';
import { mkdir, writeFile, mkdtemp, rm, access, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const exec = promisify(execFile);
const binary = process.env.WUU_PARITY_BINARY;
if (!binary) throw new Error('WUU_PARITY_BINARY must point to the integrated Wuu executable');
const providerPath = resolve(process.argv[2]);
const ordinaryPath = resolve(process.argv[3]);
const sessionsPath = resolve(process.argv[4]);
const promptPath = resolve(process.argv[5]);
const compactionPath = resolve(process.argv[6]);
const updatedToolsPath = resolve(process.argv[7]);
const titlePath = resolve(process.argv[8]);
const expectedTitle = process.argv[9];
let heldRequest;
let releaseHeldRequest;
let phase = 'ordinary';
let toolOwner = 'production-owner';
let windowRequests = 0;
const output = resolve(process.env.WUU_PARITY_OUTPUT ?? 'artifacts/parity-author');
await mkdir(output, {recursive: true});
for (const name of ['result.json','tools-result.json','compaction-result.json','generation-result.json','sessions-result.json','title-result.json']) await rm(join(output,name), {force:true});
await writeFile(join(output, 'binary.json'), JSON.stringify({path:resolve(binary),sha256:createHash('sha256').update(await readFile(binary)).digest('hex'),node:process.version,startedAt:new Date().toISOString()}, null, 2));
const temporary = await mkdtemp(join(tmpdir(), 'wuu-parity-production-'));
const transcript = [];
const calls = [];
let server;
let model;
let modelFailure;
const env = {PATH: process.env.PATH, HOME: temporary, WUU_HOME: join(temporary, 'home'), WUU_NODE_EXECUTABLE: process.execPath};
const configPath = join(env.WUU_HOME, 'config.json');

function startServer() {
  const child = spawn(binary, ['app-server', '--workdir', temporary, '--config', configPath], {env, stdio: ['pipe', 'pipe', 'pipe']});
  const pending = new Map();
  const notifications = [];
  const listeners = new Set();
  let sequence = 0;
  let ended;
  let stderr = '';
  const exit = once(child, 'exit');
  child.stderr.on('data', data => {stderr += String(data); transcript.push({stderr: String(data)});});
  createInterface({input: child.stdout}).on('line', line => {
    transcript.push({received: line});
    let frame;
    try { frame = JSON.parse(line); } catch { return; }
    if (frame.id) {
      const waiter = pending.get(String(frame.id));
      if (waiter) { pending.delete(String(frame.id)); clearTimeout(waiter.timer); frame.error ? waiter.reject(new Error(JSON.stringify(frame.error))) : waiter.resolve(frame.result); }
    } else { notifications.push(frame); for (const listener of listeners) listener(); }
  });
  child.on('exit', (code, signal) => {
    ended = new Error(`app-server exited (${code ?? signal}): ${stderr}`);
    for (const waiter of pending.values()) {clearTimeout(waiter.timer); waiter.reject(ended);}
    pending.clear(); for (const listener of listeners) listener();
  });
  return {
    request(method, params = {}) {
      if (ended) return Promise.reject(ended);
      return new Promise((resolve, reject) => {
        const id = String(++sequence);
        const timer = setTimeout(() => {pending.delete(id); reject(new Error(`${method} timed out`));}, 30000);
        pending.set(id, {resolve, reject, timer});
        const frame = {id, method, params}; transcript.push({sent: frame}); child.stdin.write(JSON.stringify(frame) + '\n');
      });
    },
    terminal() {
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => finish(new Error('turn terminal notification timed out')), 30000);
        const finish = (error, value) => {clearTimeout(timer); listeners.delete(check); error ? reject(error) : resolve(value);};
        const check = () => {
          const index = notifications.findIndex(frame => ['turn/completed', 'turn/error'].includes(frame.method));
          if (index >= 0) finish(null, notifications.splice(index, 1)[0]); else if (ended) finish(ended);
        };
        listeners.add(check); check();
      });
    },
    async close() {
      if (ended) return;
      child.stdin.end();
      const timer = setTimeout(() => child.kill('SIGKILL'), 5000);
      try { await exit; } finally { clearTimeout(timer); }
    },
  };
}

try {
  await mkdir(env.WUU_HOME);
  model = createServer(async (request, response) => {
    try {
      let body = ''; for await (const chunk of request) body += chunk;
      const payload = JSON.parse(body); calls.push(payload);
      if (phase === 'generation-old' && heldRequest) {
        const arrived = heldRequest; heldRequest = undefined; arrived();
        await new Promise(resolve => {releaseHeldRequest = resolve;});
      }
      const names = (payload.tools ?? []).map(tool => tool.function?.name);
      if (phase !== 'ordinary') {
        const system = payload.messages.filter(message => message.role === 'system' || message.role === 'developer').map(message => typeof message.content === 'string' ? message.content : message.content.map(part => part.text ?? '').join('')).join('\n');
        assert.equal(system, 'independent-prompt:main', 'host contaminated the complete selected prompt');
      }
      const selected = phase === 'window' ? 'new_context' : phase === 'ordinary' ? (payload.tools ?? []).find(tool => tool.function?.description === 'Independent ordinary contribution')?.function.name : 'author_echo';
      if (phase === 'ordinary') assert.ok(selected, 'default registry lost ordinary plugin contribution');
      else if (phase === 'window') assert.ok(names.includes('new_context'), 'default registry lost context transition tool');
      else assert.deepEqual(names, ['author_echo'], 'native tools leaked into replacement registry');
      const turnMessages = payload.messages.slice(payload.messages.findLastIndex(message => message.role === 'user') + 1);
      const toolResult = turnMessages.find(message => message.role === 'tool');
      let delta;
      if (phase === 'window') {
        windowRequests++;
        assert.ok(windowRequests <= 2, 'unexpected repeated inference after refused window');
        if (windowRequests === 2) {
          assert.ok(JSON.stringify(payload.messages).includes('independent policy refuses window'), 'selected refusal was not delivered to the model');
          assert.ok(JSON.stringify(payload.messages).includes('replacement verified'), 'refused window discarded prior history');
        }
        delta = windowRequests === 1 ? {role:'assistant',tool_calls:[{index:0,id:'window-call',type:'function',function:{name:'new_context',arguments:'{}'}}]} : {role:'assistant',content:'window refusal observed'};
      } else if (toolResult) {
        assert.ok(JSON.stringify(toolResult.content).includes(`${phase === 'ordinary' ? 'ordinary-owner' : toolOwner}:probe`), 'selected tool implementation did not execute');
        delta = {role: 'assistant', content: 'replacement verified'};
      } else {
        delta = {role: 'assistant', tool_calls: [{index: 0, id: 'independent-call', type: 'function', function: {name: selected, arguments: phase === 'ordinary' || phase === 'window' ? '{}' : '{"text":"probe"}'}}]};
      }
      response.writeHead(200, {'Content-Type': 'text/event-stream'});
      response.end(`data: ${JSON.stringify({choices: [{index: 0, delta, finish_reason: delta.tool_calls ? 'tool_calls' : 'stop'}]})}\n\ndata: [DONE]\n\n`);
    } catch (error) {modelFailure = error; response.writeHead(500); response.end(String(error));}
  });
  model.listen(0, '127.0.0.1'); await once(model, 'listening');
  const config = {default_provider: 'synthetic', providers: {synthetic: {type: 'openai-compatible', base_url: `http://127.0.0.1:${model.address().port}`, api_key: 'synthetic', model: 'test'}}, agent: {permission_mode: 'unconfined'}, skills: {enabled: false}, composition: {version: 1, services: [{service: 'agent.tools', major: 1, provider: 'wuu.tools.default'}, {service: 'agent.prompt', major: 1, provider: 'wuu.prompt.default'}]}};
  await writeFile(configPath, JSON.stringify(config));
  for (const args of [['plugin', 'install', ordinaryPath], ['plugin', 'approve', '--workdir', temporary, 'ordinary-tools']]) {
    const result = await exec(binary, args, {env, cwd: temporary}); transcript.push({command: args, stdout: result.stdout, stderr: result.stderr});
  }
  Object.assign(config, JSON.parse(await readFile(configPath, 'utf8')));
  server = startServer();
  await server.request('initialize', {protocol_version: 'wuu-app-server/v0.1', client: {name: 'ordinary-author-acceptance'}});
  const ordinaryThread = await server.request('thread/start');
  await server.request('thread/rename', {thread_id:ordinaryThread.thread.id,title:'Ordinary tool acceptance'});
  await server.request('turn/start', {thread_id: ordinaryThread.thread.id, prompt: 'Call the ordinary contribution.'});
  const ordinaryTerminal = await server.terminal();
  if (modelFailure) throw modelFailure;
  assert.equal(ordinaryTerminal.method, 'turn/completed');
  assert.equal(calls.length, 2);
  await server.close(); server = undefined;
  for (const args of [['plugin', 'install', providerPath], ['plugin', 'approve', '--workdir', temporary, 'independent-tools'], ['plugin', 'install', promptPath], ['plugin', 'approve', '--workdir', temporary, 'independent-prompt']]) {
    const result = await exec(binary, args, {env, cwd: temporary}); transcript.push({command: args, stdout: result.stdout, stderr: result.stderr});
  }
  Object.assign(config, JSON.parse(await readFile(configPath, 'utf8')));
  phase = 'selected';
  const factoryMarker = join(temporary, 'native-factory-started');
  const mcpProbe = join(temporary, 'mcp-probe.cjs');
  await writeFile(mcpProbe, `require('node:fs').writeFileSync(${JSON.stringify(factoryMarker)}, 'started'); process.exit(1);`);
  config.mcp_servers = {factory_probe: {command: process.execPath, args: [mcpProbe]}};
  config.composition = {version: 2, profile: 'headless', patches: [{modules: {'independent-title': false, 'wuu.tools.default': false, 'independent-tools': true, 'ordinary-tools': true, 'wuu.prompt.default': false, 'independent-prompt': true}, services: [{service: 'agent.tools', major: 1, provider: 'independent-tools'}, {service: 'agent.prompt', major: 1, provider: 'independent-prompt'}]}]};
  await writeFile(configPath, JSON.stringify(config));
  server = startServer();
  await server.request('initialize', {protocol_version: 'wuu-app-server/v0.1', client: {name: 'independent-author-acceptance'}});
  const created = await server.request('thread/start');
  const threadId = created.thread.id;
  await server.request('thread/rename', {thread_id:threadId,title:'Selected tool acceptance'});
  await server.request('turn/start', {thread_id: threadId, prompt: 'Call author_echo with text probe.'});
  const terminal = await server.terminal();
  if (modelFailure) throw modelFailure;
  assert.equal(terminal.method, 'turn/completed');
  assert.equal(calls.length, 4, 'expected one tool round and one completion per scenario');
  await assert.rejects(access(factoryMarker), error => error.code === 'ENOENT');
  await server.close(); server = undefined;
  const lifecycle = (await readFile(join(output, 'tool-lifecycle.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  const executed = lifecycle.find(event => event.event === 'invokeService' && event.method === 'execute');
  assert.ok(executed, 'provider process never executed selected tool');
  assert.ok(lifecycle.some(event => event.pid === executed.pid && event.event === 'shutdown'), 'executing provider did not receive shutdown');
  config.composition.patches[0].modules['independent-tools'] = false;
  await writeFile(configPath, JSON.stringify(config));
  server = startServer();
  await assert.rejects(server.request('initialize', {protocol_version: 'wuu-app-server/v0.1', client: {name: 'missing-provider-acceptance'}}), error => !error.message.includes('timed out') && /agent\.tools|independent-tools/.test(error.message));
  assert.equal(calls.length, 4, 'missing registry invoked a model or native fallback');
  await writeFile(join(output, 'tools-result.json'), JSON.stringify({passed: true, cases: ['default ordinary tool', 'independent registry ownership', 'default factory absence', 'complete independent prompt without default contamination', 'provider shutdown', 'missing selected provider'], modelRequests: calls.length}, null, 2));
  await server.close(); server = undefined;
  config.composition.patches[0].modules['independent-tools'] = true;
  config.composition.patches[0].services.push({service:'session.title',major:1,provider:'wuu.title.default'});
  await writeFile(configPath, JSON.stringify(config));
  for (const args of [['plugin','install',titlePath],['plugin','approve','--workdir',temporary,'independent-title']]) {
    const result = await exec(binary,args,{env,cwd:temporary}); transcript.push({command:args,stdout:result.stdout,stderr:result.stderr});
  }
  Object.assign(config, JSON.parse(await readFile(configPath, 'utf8')));
  config.composition.patches[0].modules['wuu.title.default'] = false;
  config.composition.patches[0].modules['independent-title'] = true;
  config.composition.patches[0].services.find(binding => binding.service === 'session.title').provider = 'independent-title';
  await writeFile(configPath,JSON.stringify(config));
  phase = 'title';
  const beforeTitle = calls.length;
  server = startServer();
  await server.request('initialize',{protocol_version:'wuu-app-server/v0.1',client:{name:'title-author-acceptance'}});
  const titleThread = await server.request('thread/start');
  const titleResult = await server.request('thread/regenerate-title',{thread_id:titleThread.thread.id});
  assert.equal(titleResult.CleanedTitle,expectedTitle);
  assert.equal(titleResult.PersistedTitle,expectedTitle);
  assert.equal(titleResult.Persisted,true);
  const titleListing = await server.request('thread/list',{summary_only:true});
  assert.equal(titleListing.threads.find(thread => thread.id === titleThread.thread.id)?.title,expectedTitle);
  await server.close(); server = undefined;
  assert.equal(calls.length,beforeTitle,'complete title policy invoked inference');
  config.composition.patches[0].modules['independent-title'] = false;
  config.composition.patches[0].services = config.composition.patches[0].services.filter(binding => binding.service !== 'session.title');
  await writeFile(configPath,JSON.stringify(config));
  phase = 'title-disabled';
  server = startServer();
  await server.request('initialize',{protocol_version:'wuu-app-server/v0.1',client:{name:'omitted-title-acceptance'}});
  const untitled = await server.request('thread/start');
  const omitted = await server.request('thread/regenerate-title',{thread_id:untitled.thread.id});
  assert.equal(omitted.Persisted,false);
  assert.equal(omitted.CleanedTitle,'');
  assert.equal(calls.length,beforeTitle,'omitted title policy invoked native inference');
  await server.request('turn/start',{thread_id:untitled.thread.id,prompt:'Call author_echo with text probe.'});
  const untitledTerminal = await server.terminal();
  if (modelFailure) throw modelFailure;
  assert.equal(untitledTerminal.method,'turn/completed',JSON.stringify(untitledTerminal));
  const untitledListing = await server.request('thread/list',{summary_only:true});
  assert.equal(untitledListing.threads.find(thread => thread.id === untitled.thread.id)?.title ?? '', '');
  await server.close(); server = undefined;
  assert.equal(calls.length,beforeTitle+2,'omitted title policy generated a background model request');
  await writeFile(join(output,'title-result.json'),JSON.stringify({passed:true,modelRequests:calls.length},null,2));
  // Return to the durable native session store containing the earlier completed turn.
  delete config.mcp_servers;
  config.composition = {version: 2, profile: 'headless', patches: [{modules: {'independent-title': false, 'independent-tools': false, 'ordinary-tools': true, 'independent-sessions': false, 'independent-prompt': true, 'wuu.prompt.default': false}, services: [{service:'agent.tools',major:1,provider:'wuu.tools.default'}, {service:'session.domain',major:1,provider:'wuu.session.default'}, {service:'agent.prompt',major:1,provider:'independent-prompt'}, {service:'agent.compaction',major:1,provider:'wuu.compaction.default'}]}]};
  await writeFile(configPath, JSON.stringify(config));
  for (const args of [['plugin', 'install', compactionPath], ['plugin', 'approve', '--workdir', temporary, 'independent-compaction']]) {
    const result = await exec(binary, args, {env, cwd: temporary}); transcript.push({command: args, stdout: result.stdout, stderr: result.stderr});
  }
  Object.assign(config, JSON.parse(await readFile(configPath, 'utf8')));
  config.composition.patches[0].modules['wuu.compaction.default'] = false;
  config.composition.patches[0].modules['independent-compaction'] = true;
  config.composition.patches[0].services.find(binding => binding.service === 'agent.compaction').provider = 'independent-compaction';
  await writeFile(configPath, JSON.stringify(config));
  server = startServer();
  await server.request('initialize', {protocol_version: 'wuu-app-server/v0.1', client: {name: 'compaction-author-acceptance'}});
  const previous = await server.request('thread/resume', {session_id: threadId});
  const completed = previous.thread.turns.filter(turn => turn.status === 'completed');
  assert.ok(completed.length > 0, 'compaction acceptance requires existing completed history');
  const assertRetained = async () => {
    const current = await server.request('thread/resume', {session_id: threadId});
    for (const turn of completed) assert.deepEqual(current.thread.turns.find(candidate => candidate.id === turn.id)?.items, turn.items, 'refused compaction changed prior completed history');
  };
  const beforeCompact = calls.length;
  await server.request('thread/compact/start', {thread_id: threadId});
  const compactTerminal = await server.terminal();
  assert.equal(compactTerminal.method, 'turn/error');
  assert.match(JSON.stringify(compactTerminal), /independent policy refuses window/);
  assert.equal(calls.length, beforeCompact, 'refused compaction invoked fallback inference');
  await assertRetained();
  phase = 'window';
  await server.request('turn/start', {thread_id: threadId, prompt: 'Call new_context now.'});
  const windowTerminal = await server.terminal();
  if (modelFailure) throw modelFailure;
  assert.equal(windowTerminal.method, 'turn/completed');
  assert.match(JSON.stringify(windowTerminal), /window refusal observed/);
  assert.equal(calls.length, beforeCompact + 2, 'unexpected inference beyond window request and error-aware continuation');
  await assertRetained();
  await server.close(); server = undefined;
  const policyCalls = (await readFile(join(output, 'compaction-lifecycle.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  assert.equal(policyCalls.filter(call => call.method === 'window' && call.code === 'author_policy_refused').length, 2, 'manual compact and new_context must both reach selected window policy');
  await writeFile(join(output, 'compaction-result.json'), JSON.stringify({passed:true, modelRequests:calls.length}, null, 2));
  config.composition = {version:2,profile:'headless',patches:[{modules:{'independent-title':false,'wuu.tools.default':false,'independent-tools':true,'independent-sessions':false,'wuu.prompt.default':false,'independent-prompt':true,'independent-compaction':false},services:[{service:'agent.tools',major:1,provider:'independent-tools'},{service:'agent.prompt',major:1,provider:'independent-prompt'},{service:'session.domain',major:1,provider:'wuu.session.default'},{service:'agent.compaction',major:1,provider:'wuu.compaction.default'}]}]};
  await writeFile(configPath, JSON.stringify(config));
  server = startServer();
  await server.request('initialize', {protocol_version:'wuu-app-server/v0.1',client:{name:'generation-author-acceptance'}});
  phase = 'generation-old';
  const oldGeneration = await server.request('thread/start');
  await server.request('thread/rename', {thread_id:oldGeneration.thread.id,title:'Retained generation acceptance'});
  const arrived = new Promise(resolve => {heldRequest = resolve;});
  const generationCalls = calls.length;
  await server.request('turn/start', {thread_id:oldGeneration.thread.id,prompt:'Call author_echo with text probe.'});
  let arrivalTimer;
  try {await Promise.race([arrived, new Promise((_, reject) => {arrivalTimer=setTimeout(() => reject(new Error('generation model barrier timed out')),30000);})]);}
  finally {clearTimeout(arrivalTimer);}
  const beforeUpdate = (await readFile(join(output, 'tool-lifecycle.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  const retainedPID = beforeUpdate.findLast(event => event.event === 'invokeService' && event.method === 'list')?.pid;
  assert.ok(retainedPID, 'old selected generation was not observed');
  const updateArgs = ['plugin','update','--json','independent-tools',updatedToolsPath];
  const staged = await exec(binary, updateArgs, {env,cwd:temporary});
  transcript.push({command:updateArgs,stdout:staged.stdout,stderr:staged.stderr});
  const pending = JSON.parse(staged.stdout);
  await server.request('extension/package/update', {id:'plugin:user:independent-tools',fingerprint:pending.fingerprint,action:'promote_update'});
  const duringUpdate = (await readFile(join(output, 'tool-lifecycle.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  assert.ok(!duringUpdate.some(event => event.pid === retainedPID && event.event === 'shutdown'), 'running generation was shut down during publication');
  releaseHeldRequest(); releaseHeldRequest = undefined;
  const oldTerminal = await server.terminal();
  if (modelFailure) throw modelFailure;
  assert.equal(oldTerminal.method,'turn/completed',JSON.stringify(oldTerminal));
  phase = 'generation-new';
  toolOwner = 'production-owner-v2';
  const newGeneration = await server.request('thread/start');
  await server.request('thread/rename', {thread_id:newGeneration.thread.id,title:'Replacement generation acceptance'});
  await server.request('turn/start',{thread_id:newGeneration.thread.id,prompt:'Call author_echo with text probe.'});
  const newTerminal = await server.terminal();
  if (modelFailure) throw modelFailure;
  assert.equal(newTerminal.method,'turn/completed',JSON.stringify(newTerminal));
  assert.equal(calls.length,generationCalls+4);
  await server.close(); server = undefined;
  const generationEvents = (await readFile(join(output, 'tool-lifecycle.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  for (const pid of new Set(generationEvents.filter(event => event.method === 'execute').map(event => event.pid))) assert.ok(generationEvents.some(event => event.pid === pid && event.event === 'shutdown'), `executing generation ${pid} was not shut down`);
  await writeFile(join(output, 'generation-result.json'), JSON.stringify({passed:true, modelRequests:calls.length}, null, 2));
  Object.assign(config, JSON.parse(await readFile(configPath, 'utf8')));
  config.composition = {version:2,profile:'headless',patches:[{modules:{'independent-title':false,'wuu.tools.default':false,'independent-tools':true,'wuu.prompt.default':false,'independent-prompt':true,'independent-compaction':false},services:[{service:'agent.tools',major:1,provider:'independent-tools'},{service:'agent.prompt',major:1,provider:'independent-prompt'}]}]};
  const beforeSessions = calls.length;
  await writeFile(configPath, JSON.stringify(config));
  for (const args of [['plugin', 'install', sessionsPath], ['plugin', 'approve', '--workdir', temporary, 'independent-sessions']]) {
    const result = await exec(binary, args, {env, cwd: temporary}); transcript.push({command: args, stdout: result.stdout, stderr: result.stderr});
  }
  Object.assign(config, JSON.parse(await readFile(configPath, 'utf8')));
  config.composition.patches[0].modules['wuu.session.default'] = false;
  config.composition.patches[0].modules['independent-sessions'] = true;
  config.composition.patches[0].services.push({service: 'session.domain', major: 1, provider: 'independent-sessions'});
  await writeFile(configPath, JSON.stringify(config));
  phase = 'sessions';
  server = startServer();
  await server.request('initialize', {protocol_version: 'wuu-app-server/v0.1', client: {name: 'session-author-acceptance'}});
  const seeded = await server.request('thread/resume', {session_id: 'author-seeded'});
  assert.ok(JSON.stringify(seeded).includes('provider-owned seeded answer'), 'uncached resume did not read independent history');
  const sessionCreated = await server.request('thread/start');
  const sessionID = sessionCreated.thread.id;
  const listing = await server.request('thread/list', {summary_only: true});
  assert.equal(listing.threads.find(thread => thread.id === sessionID)?.title, 'production-session-owner');
  await server.request('thread/rename', {thread_id: sessionID, title: 'Independent session'});
  await server.request('turn/start', {thread_id: sessionID, prompt: 'Call author_echo with text probe.'});
  const sessionTerminal = await server.terminal();
  if (modelFailure) throw modelFailure;
  assert.equal(sessionTerminal.method, 'turn/completed',JSON.stringify(sessionTerminal));
  const resumed = await server.request('thread/resume', {session_id: sessionID});
  assert.ok(JSON.stringify(resumed).includes('replacement verified'));
  const forked = await server.request('thread/fork', {thread_id: sessionID});
  assert.notEqual(forked.thread.id, sessionID);
  const forkResume = await server.request('thread/resume', {session_id: forked.thread.id});
  assert.ok(JSON.stringify(forkResume).includes('replacement verified'));
  assert.equal(calls.length, beforeSessions + 2);
  await server.close(); server = undefined;
  const sessionCalls = (await readFile(join(output, 'session-lifecycle.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  for (const method of ['CreateWithMetadata', 'CreateInitialized', 'LoadProviderHistorySnapshot', 'NewInferenceJournalRuntime', 'Journal.PrepareOperation', 'Journal.PrepareAttempt', 'Journal.UpsertSubmission', 'Journal.CompleteOperation', 'Journal.Close']) {
    assert.ok(sessionCalls.some(call => call.method === method && call.ok), `production session path did not invoke ${method}`);
  }
  await writeFile(join(output, 'sessions-result.json'), JSON.stringify({passed:true, modelRequests:calls.length}, null, 2));
  await writeFile(join(output, 'result.json'), JSON.stringify({passed: true, cases: ['tools production acceptance', 'complete independent title without inference and omitted title without fallback', 'independent session create/list/rename', 'synthetic model turn with real journal', 'history/fork/resume', 'selected forced compaction and window refusal preserve history without fallback', 'in-flight generation retention and subsequent replacement'], modelRequests: calls.length}, null, 2));
} catch (error) {
  await writeFile(join(output, 'result.json'), JSON.stringify({passed: false, phase, modelRequests:calls.length, error: error.stack}, null, 2)); throw error;
} finally {
  releaseHeldRequest?.();
  await server?.close();
  if (model) {model.closeAllConnections(); await new Promise(resolve => model.close(resolve));}
  await writeFile(join(output, 'protocol.json'), JSON.stringify(transcript, null, 2));
  await writeFile(join(output, 'synthetic-model.json'), JSON.stringify(calls, null, 2));
  await rm(temporary, {recursive: true, force: true});
}
