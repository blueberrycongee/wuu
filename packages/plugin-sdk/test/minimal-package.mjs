// Public-contract acceptance: compile an independent engine against a packed SDK,
// install it normally, and drive the real app-server through its JSONL API.
// Failure cases: hidden model/tool initialization, unavailable selected provider,
// generation replacement redirecting admitted work, cancellation and lost cleanup.
import assert from 'node:assert/strict';
import {execFile, spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {once} from 'node:events';
import {createServer} from 'node:http';
import {cp, mkdir, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {promisify} from 'node:util';
import {preparePublicSDK} from './public-package.mjs';

const exec = promisify(execFile);
const authorSDK = fileURLToPath(new URL('../', import.meta.url));
const repo = resolve(authorSDK, '../..');
const sdk = resolve(process.env.WUU_MINIMAL_SDK_ROOT ?? authorSDK);
const binary = resolve(process.env.WUU_MINIMAL_BINARY ?? join(repo, 'bin/wuu'));
const output = resolve(process.env.WUU_MINIMAL_OUTPUT ?? join(repo, '.tmp/e2e/minimal-composition'));
await mkdir(output, {recursive: true});
const run = await mkdtemp(join(output, 'run-'));
const home = join(run, 'home'), workspace = join(run, 'workspace');
await mkdir(home); await mkdir(workspace);
const env = {PATH: process.env.PATH, HOME: run, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath};
const transcript = [], checks = [];
const configPath = join(home, 'config.json');
const lifecyclePath = join(run, 'engine-lifecycle.jsonl');
await writeFile(lifecyclePath, '');
let server, modelTrap, guiRelay;
let modelRequests = 0;
let result = {passed: false, run, checks, limitations: ['Deterministic independent engine and DOM-rendered default composer; no model inference, Electron or pixel-layout acceptance.']};
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
async function command(args) {
  const reply = await exec(binary, args, {env, cwd: workspace});
  transcript.push({command: args, ...reply});
  return reply.stdout;
}

function startServer() {
  const child = spawn(binary, ['app-server', '--workdir', workspace, '--config', configPath], {env, stdio: ['pipe', 'pipe', 'pipe']});
  const pending = new Map(), frames = [], listeners = new Set();
  const exited = once(child, 'exit');
  let sequence = 0, ended, stderr = '';
  child.stderr.on('data', data => {stderr += String(data); transcript.push({stderr: String(data)});});
  createInterface({input: child.stdout}).on('line', line => {
    transcript.push({received: line});
    let frame; try {frame = JSON.parse(line);} catch {return;}
    frames.push(frame);
    const waiter = pending.get(String(frame.id));
    if (waiter) {pending.delete(String(frame.id)); clearTimeout(waiter.timer); frame.error ? waiter.reject(new Error(JSON.stringify(frame.error))) : waiter.resolve(frame.result);}
    for (const listener of listeners) listener();
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
        const id = `minimal-${++sequence}`;
        const timer = setTimeout(() => {pending.delete(id); reject(new Error(`${method} timed out: ${stderr}`));}, 30000);
        pending.set(id, {resolve, reject, timer});
        const frame = {id, method, params}; transcript.push({sent: frame}); child.stdin.write(JSON.stringify(frame) + '\n');
      });
    },
    wait(predicate) {
      return new Promise((resolve, reject) => {
        const finish = (error, value) => {clearTimeout(timer); listeners.delete(check); error ? reject(error) : resolve(value);};
        const check = () => {const found = frames.find(predicate); if (found) finish(null, found); else if (ended) finish(ended);};
        const timer = setTimeout(() => finish(new Error('expected app-server event timed out')), 30000);
        listeners.add(check); check();
      });
    },
    async close() {
      if (ended) return;
      child.stdin.end();
      const timer = setTimeout(() => child.kill('SIGKILL'), 10000);
      try {const [code, signal] = await exited; assert.equal(code, 0, `shutdown: ${signal}: ${stderr}`);} finally {clearTimeout(timer);}
    },
  };
}
const initialize = () => server.request('initialize', {protocol_version: 'wuu-app-server/v0.1', client: {name: 'minimal-public-sdk-acceptance'}});
const terminal = (turn) => server.wait(frame => ['turn/completed', 'turn/error'].includes(frame.method) && frame.params?.turn?.id === turn.id);
const lifecycle = async () => (await readFile(lifecyclePath, 'utf8')).trim().split('\n').filter(Boolean).map(line => JSON.parse(line));

try {
  result.binary = {path: binary, sha256: sha(await readFile(binary))};
  result.source = {sha256: sha(await readFile(fileURLToPath(import.meta.url)))};
  const {project, tsc, packed, manifest} = await preparePublicSDK({sdk, temporary: run, tarball: process.env.WUU_MINIMAL_SDK_TARBALL});
  for (const name of ['engine', 'composition']) await cp(join(repo, `examples/compositions/minimal/${name}.ts`), join(project, `${name}.ts`));
  await exec(process.execPath, [tsc, 'engine.ts', 'composition.ts', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--target', 'ES2023', '--strict', '--skipLibCheck'], {cwd: project});
  result.sdk = {name: manifest.name, version: manifest.version, sha256: sha(await readFile(packed)), tarball: packed};
  checks.push('independent engine and composition compile against packed public SDK exports');
  const {config} = await import(pathToFileURL(join(project, 'composition.js')));
  await writeFile(configPath, JSON.stringify({...config, composition: {version: config.composition.version, profile: 'minimal'}}, null, 2));
  await writeFile(join(run, 'composition.json'), JSON.stringify(config.composition, null, 2));
  const selection = JSON.parse(await command(['composition', 'resolve', '--file', join(run, 'composition.json')]));
  assert.equal(selection.profile, 'minimal');
  assert(!config.providers && !config.default_provider, 'fixture contains a dummy model connection');
  const packages = [];
  for (const [index, label] of ['first', 'second'].entries()) {
    const directory = join(run, `engine-v${index + 1}`); await mkdir(directory); packages.push(directory);
    const wrapper = join(project, `runtime-v${index + 1}.mjs`);
    await writeFile(wrapper, `import {appendFileSync} from 'node:fs';\nimport {runJSONLRuntime} from '@wuu/plugin-sdk';\nimport {createEchoEngine} from './engine.js';\nconst record=(event,data)=>appendFileSync(${JSON.stringify(lifecyclePath)},JSON.stringify({event,pid:process.pid,...data})+'\\n');\nconst engine=createEchoEngine(${JSON.stringify(label)},record);\nconst invoke=engine.invokeService;\nengine.invokeService=async(...args)=>{const result=await invoke(...args);return args[0].method==='descriptor'?{...result,requires_host_model:${index === 1}}:result;};\nawait runJSONLRuntime(engine,{input:process.stdin,output:process.stdout});\n`);
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [wrapper, '--bundle', '--platform=node', '--format=esm', `--outfile=${join(directory, 'runtime.mjs')}`]);
    await writeFile(join(directory, 'plugin.json'), JSON.stringify({schema_version: 1, id: 'example.echo-engine', name: 'Independent echo engine', version: `${index + 1}.0.0`, runtime: {protocol: 'wuu-plugin-v1', command: process.execPath, args: ['runtime.mjs']}}));
  }
  await command(['plugin', 'install', packages[0]]);
  const installedConfig = JSON.parse(await readFile(configPath, 'utf8'));
  await writeFile(configPath, JSON.stringify({...installedConfig, ...config}, null, 2));
  server = startServer();
  const initialized = await initialize();
  const graph = await server.request('plugin/registry/introspect');
  await writeFile(join(run, 'minimal-graph.json'), JSON.stringify(graph, null, 2));
  const modules = graph.composition.modules.map(module => module.id);
  assert.deepEqual([...modules].sort(), [...selection.modules].sort(), 'runtime silently added unrelated product modules');
  for (const absent of ['wuu.default-loop', 'wuu.tools.default', 'wuu.model.native', 'wuu.engines.native', 'wuu.credentials.os', 'wuu.settings.local', 'wuu.artifacts.local', 'wuu.index.trace', 'wuu.default-shell']) assert(!modules.includes(absent), `${absent} leaked into minimal`);
  const route = graph.services.find(service => service.service === 'engine.wuu');
  assert.equal(route?.provider, 'example.echo-engine', JSON.stringify(route));
  const {thread} = await server.request('thread/start');
  await server.request('thread/rename', {thread_id: thread.id, title: 'Minimal public engine'});
  const {turn} = await server.request('turn/start', {thread_id: thread.id, prompt: 'hello'});
  const completed = await terminal(turn);
  assert.equal(completed.method, 'turn/completed', JSON.stringify(completed));
  assert.match(JSON.stringify(completed), /first:hello/);
  assert.equal(completed.params.index_error, undefined, 'omitted index was treated as an uncertain write');
  const history = await server.request('thread/resume', {session_id: thread.id});
  assert.match(JSON.stringify(history), /first:hello/);
  checks.push('real config entry starts only explicit modules; session/message persists without a model connection or default execution providers');

  const {turn: held} = await server.request('turn/start', {thread_id: thread.id, prompt: '/wait'});
  await server.wait(frame => !frame.id && JSON.stringify(frame).includes('first:waiting'));
  const oldPID = (await lifecycle()).findLast(event => event.event === 'run' && event.prompt === '/wait').pid;
  // The unchanged default renderer sends against the real selected engine. Its
  // second generation advertises a host-model prerequisite, which the same
  // draft must immediately honor without interrupting the admitted old run.
  let promoted = false;
  guiRelay = createServer(async (request, response) => {
    try {
      let body = ''; for await (const chunk of request) body += chunk;
      const {method, params} = JSON.parse(body);
      let result;
      if (method === 'fixture/initialize') result = initialized;
      else if (method === 'fixture/terminal') result = await terminal(params.turn);
      else if (method === 'fixture/promote') {
        assert(!promoted, 'duplicate fixture promotion'); promoted = true;
        const pending = JSON.parse(await command(['plugin', 'update', '--json', 'example.echo-engine', packages[1]]));
        result = await server.request('extension/package/update', {id: 'plugin:user:example.echo-engine', fingerprint: pending.fingerprint, action: 'promote_update'});
      } else {
        assert(['engine/list', 'thread/start', 'turn/start'].includes(method), `unexpected renderer method ${method}`);
        result = await server.request(method, params);
      }
      response.setHeader('Content-Type', 'application/json'); response.end(JSON.stringify({result}));
    } catch (error) { response.setHeader('Content-Type', 'application/json'); response.end(JSON.stringify({error: String(error)})); }
  });
  await new Promise(resolve => guiRelay.listen(0, '127.0.0.1', resolve));
  const guiLog = join(run, 'default-composer.log');
  try {
    const {stdout, stderr} = await exec(process.execPath, [join(repo, 'desktop/node_modules/vitest/vitest.mjs'), 'run', 'src/renderer/modules/DefaultComposerSurface.test.tsx', '-t', 'sends through the default composer using the selected real engine capability', '--maxWorkers=1', '--minWorkers=1'], {
      cwd: join(repo, 'desktop'), env: {...process.env, WUU_MINIMAL_GUI_ENDPOINT: `http://127.0.0.1:${guiRelay.address().port}`}, timeout: 60000,
    });
    await writeFile(guiLog, stdout + stderr);
  } catch (error) { await writeFile(guiLog, (error.stdout ?? '') + (error.stderr ?? '')); throw error; }
  finally { await new Promise(resolve => guiRelay.close(resolve)); guiRelay = undefined; }
  assert(promoted, 'default renderer acceptance did not exercise capability replacement');
  checks.push('default ComposerSurface/Runtime/Submission sends with real model-free engine.wuu metadata and no host provider; replacement requirement blocks fresh send while preserving draft');
  assert(!(await lifecycle()).some(event => event.pid === oldPID && event.event === 'shutdown'), 'publication closed admitted generation');
  const updatedGraph = await server.request('plugin/registry/introspect');
  assert.notEqual(updatedGraph.graph_generation, graph.graph_generation);
  const {thread: fresh} = await server.request('thread/start');
  await server.request('thread/rename', {thread_id: fresh.id, title: 'Replacement engine'});
  const {turn: replacement} = await server.request('turn/start', {thread_id: fresh.id, prompt: 'new generation'});
  const updated = await terminal(replacement);
  assert.equal(updated.method, 'turn/completed', JSON.stringify(updated));
  assert.match(JSON.stringify(updated), /second:new generation/);
  assert(!(await lifecycle()).some(event => event.pid === oldPID && event.event === 'shutdown'));
  await server.request('turn/interrupt', {thread_id: thread.id});
  await terminal(held);
  await server.close(); server = undefined;
  const events = await lifecycle();
  assert(events.some(event => event.pid === oldPID && event.event === 'canceled'), 'cancellation redirected to the new provider');
  for (const pid of new Set(events.filter(event => event.event === 'run').map(event => event.pid))) {
    const own = events.filter(event => event.pid === pid);
    assert(own.some(event => event.event === 'close'), 'engine session cleanup was not called');
    assert.equal(own.at(-1).event, 'shutdown', 'provider shut down before dependent cleanup');
  }
  checks.push('replacement uses new provider while admitted old run retains its generation; cancellation and close drain before shutdown');

  server = startServer(); await initialize();
  const restored = await server.request('thread/resume', {session_id: fresh.id});
  assert.match(JSON.stringify(restored), /second:new generation/);
  // The same independent engine contract also replaces the native engine in
  // the full product. A trap is local and deterministic; any inference is a bug.
  await server.close(); server = undefined;
  const savedMinimal = JSON.parse(await readFile(configPath, 'utf8'));
  modelTrap = createServer((_request, response) => {modelRequests++; response.writeHead(500); response.end('unexpected model call');});
  await new Promise(resolve => modelTrap.listen(0, '127.0.0.1', resolve));
  const product = {...savedMinimal, default_provider: 'unused', providers: {unused: {type: 'openai-compatible', base_url: `http://127.0.0.1:${modelTrap.address().port}`, model: 'unused', api_key: 'synthetic'}},
    composition: {...config.composition, profile: 'headless', patches: [{modules: {'example.echo-engine': true, 'wuu.engines.native': false}, services: [{service: 'engine.wuu', major: 1, provider: 'example.echo-engine'}]}]}};
  await writeFile(configPath, JSON.stringify(product, null, 2));
  server = startServer(); await initialize();
  const fullGraph = await server.request('plugin/registry/introspect');
  await writeFile(join(run, 'headless-graph.json'), JSON.stringify(fullGraph, null, 2));
  assert(fullGraph.composition.modules.some(module => module.id === 'wuu.default-loop'));
  const compatibility = {...product.composition, profile: 'compatibility', profiles: {compatibility: {bundles: ['wuu.base']}}};
  await writeFile(join(run, 'base-compatibility.json'), JSON.stringify(compatibility));
  const legacyBase = JSON.parse(await command(['composition', 'resolve', '--file', join(run, 'base-compatibility.json')]));
  assert.deepEqual([...legacyBase.modules].sort(), fullGraph.composition.modules.map(module => module.id).sort(), 'public complete-base bundle lost its session host');
  assert.equal(fullGraph.services.find(service => service.service === 'engine.wuu')?.provider, 'example.echo-engine');
  const {thread: fullThread} = await server.request('thread/start');
  await server.request('thread/rename', {thread_id: fullThread.id, title: 'Same public engine in full product'});
  const {turn: fullTurn} = await server.request('turn/start', {thread_id: fullThread.id, prompt: 'same contract'});
  const fullResult = await terminal(fullTurn);
  assert.equal(fullResult.method, 'turn/completed', JSON.stringify(fullResult));
  assert.match(JSON.stringify(fullResult), /second:same contract/);
  await server.close(); server = undefined;
  // This frozen input is the pre-resource-policy v3 headless graph, not a
  // module list derived from the catalog under test. Keep old files writable
  // by old executables while exposing each compatibility addition in the graph.
  const frozenPath = join(repo, 'internal/composition/testdata/v3-headless.json');
  const frozen = await readFile(frozenPath);
  result.legacyComposition = {path: frozenPath, sha256: sha(frozen)};
  const legacy = {...product, composition: {...JSON.parse(frozen), patches: product.composition.patches}};
  const legacySource = JSON.stringify(legacy, null, 2);
  await writeFile(configPath, legacySource);
  server = startServer(); await initialize();
  const legacyGraph = await server.request('plugin/registry/introspect');
  await writeFile(join(run, 'legacy-v3-graph.json'), JSON.stringify(legacyGraph, null, 2));
  for (const id of ['wuu.resource-access', 'wuu.resource-policy.default']) {
    const module = legacyGraph.composition.modules.find(module => module.id === id);
    assert.equal(module?.source, 'migration:v3-to-v4', JSON.stringify(module));
  }
  assert(!legacyGraph.composition.modules.some(module => module.id.startsWith('wuu.session-sharing')));
  const {thread: legacyThread} = await server.request('thread/start');
  await server.request('thread/rename', {thread_id: legacyThread.id, title: 'Frozen version 3 selection'});
  const {turn: legacyTurn} = await server.request('turn/start', {thread_id: legacyThread.id, prompt: 'old explicit config'});
  const legacyResult = await terminal(legacyTurn);
  assert.equal(legacyResult.method, 'turn/completed', JSON.stringify(legacyResult));
  assert.match(JSON.stringify(legacyResult), /second:old explicit config/);
  await server.close(); server = undefined;
  assert.equal(await readFile(configPath, 'utf8'), legacySource, 'compatibility resolution rewrote old source');
  checks.push('frozen explicit v3 composition retains real sessions with observable auth migration, no unrelated providers and no config rewrite');

  for (const {composition, error} of [
    {composition: {...legacy.composition, version: 4}, error: /access_denied.*policy-failed/},
    {composition: {...legacy.composition, patches: [...legacy.composition.patches, {modules: {'wuu.resource-policy.default': false}}]}, error: /requires service resource\.authorization@1 with no provider/},
    {composition: {...legacy.composition, patches: [...legacy.composition.patches, {modules: {'wuu.resource-access': false}}]}, error: /access_denied.*policy-failed/},
  ]) {
    await writeFile(configPath, JSON.stringify({...legacy, composition}));
    server = startServer();
    await assert.rejects(initialize(), error);
    await server.close(); server = undefined;
  }
  checks.push('current v4 omission and each explicit v3 auth exclusion fail closed without migration repair');

  // A public external policy replaces the migration default. Its explicit
  // config/read denial proves owner authority did not bypass its decision.
  const policyPackage = join(run, 'compatibility-policy'); await mkdir(policyPackage);
  const policyWrapper = join(project, 'compatibility-policy.mjs');
  await writeFile(policyWrapper, `import {runJSONLRuntime,resourceAuthorizationProvider} from '@wuu/plugin-sdk';
const policy=resourceAuthorizationProvider(request=>({allow:request.owner&&request.action!=='config/read'}));
await runJSONLRuntime({initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[policy.descriptor]}),invokeService:request=>policy.invoke(request)},{input:process.stdin,output:process.stdout});`);
  await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [policyWrapper, '--bundle', '--platform=node', '--format=esm', `--outfile=${join(policyPackage, 'runtime.mjs')}`]);
  await writeFile(join(policyPackage, 'plugin.json'), JSON.stringify({schema_version: 1, id: 'example.compatibility-policy', name: 'Independent compatibility policy', version: '1.0.0', runtime: {protocol: 'wuu-plugin-v1', command: process.execPath, args: ['runtime.mjs']}}));
  await writeFile(configPath, JSON.stringify(savedMinimal));
  await command(['plugin', 'install', policyPackage]);
  savedMinimal.extensions = JSON.parse(await readFile(configPath, 'utf8')).extensions;
  const replacementPolicy = {...legacy, extensions: savedMinimal.extensions, composition: {...legacy.composition, patches: [...legacy.composition.patches, {
    modules: {'example.compatibility-policy': true}, services: [{service: 'resource.authorization', major: 1, provider: 'example.compatibility-policy'}],
  }]}};
  await writeFile(configPath, JSON.stringify(replacementPolicy));
  server = startServer(); await initialize();
  const policyGraph = await server.request('plugin/registry/introspect');
  assert(!policyGraph.composition.modules.some(module => module.id === 'wuu.resource-policy.default'));
  assert.equal(policyGraph.services.find(service => service.service === 'resource.authorization')?.provider, 'example.compatibility-policy');
  await server.request('thread/start');
  await assert.rejects(server.request('config/read'), /access_denied/);
  await server.close(); server = undefined;
  checks.push('old v3 explicit external resource policy overrides migration; its owner denial is enforced without native fallback');
  assert.equal(modelRequests, 0, 'full product bypassed the selected engine');
  await new Promise(resolve => modelTrap.close(resolve)); modelTrap = undefined;
  checks.push('full headless product routes through the same public engine contract, without native inference fallback');
  await writeFile(configPath, JSON.stringify(savedMinimal, null, 2));
  await command(['plugin', 'disable', 'example.echo-engine']);
  const countBefore = (await lifecycle()).length;
  server = startServer();
  await assert.rejects(initialize(), /unavailable|excluded|disabled/);
  await server.close(); server = undefined;
  assert.equal((await lifecycle()).length, countBefore, 'disabled provider was started');
  checks.push('restart preserves messages; disabling selected provider rejects startup without native fallback');
  result.passed = true;
} catch (error) {
  result.error = String(error.stack ?? error);
  console.error(result.error);
  process.exitCode = 1;
} finally {
  if (guiRelay) await new Promise(resolve => guiRelay.close(resolve));
  if (modelTrap) await new Promise(resolve => modelTrap.close(resolve));
  result.modelRequests = modelRequests;
  try {await server?.close();} catch (error) {result.cleanupError = String(error); result.passed = false; process.exitCode = 1;}
  await writeFile(join(run, 'protocol.json'), JSON.stringify(transcript, null, 2));
  await writeFile(join(run, 'result.json'), JSON.stringify(result, null, 2));
  console.log(`${result.passed ? 'PASS' : 'FAIL'} minimal composition acceptance: ${join(run, 'result.json')}`);
}
