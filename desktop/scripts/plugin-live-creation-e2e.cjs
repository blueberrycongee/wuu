// Run after building this checkout's core and desktop, using its Wuu Dev host.
// The local model scripts real agent tool calls; all profile and workspace data is disposable.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_PLUGIN_LIVE_OUTPUT || path.join(desktop, 'out/e2e/plugin-live-creation'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-plugin-live-'));
const home = path.join(fixture, 'home');
const project = path.join(fixture, 'project');
const userHome = path.join(fixture, 'user-home');
for (const dir of [output, home, project, userHome]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, {
  HOME: userHome, WUU_HOME: home, WUU_DESKTOP_CORE: path.join(desktop, 'build/bin/wuu-core'),
  WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0',
  WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1',
});
delete process.env.ELECTRON_RENDERER_URL;

const pluginFiles = {
  'live-plugin/plugin.json': JSON.stringify({ schemaVersion: 1, id: 'live-creation-fixture', name: 'Live creation fixture', version: '1.0.0', desktop: { entry: 'desktop.js' } }),
  'live-plugin/package.json': JSON.stringify({ name: 'live-creation-fixture', private: true, type: 'module', scripts: { build: 'node --check desktop.js' } }),
  'live-plugin/desktop.js': `export function activate(api) {
    api.registerUIContribution({ id: 'indicator', target: 'wuu.default-navigation/sidebar.footer',
      render: () => api.react.createElement('span', { 'data-live-creation': 'ready' }, 'Live plugin ready') });
  }\n`,
};
const report = { status: 'running', checks: [], requests: [], errors: [], limitations: [
  'Synthetic loopback model, disposable workspace and profile; no user account or paid model.',
  'Uses the current checkout Wuu Dev host and production main, preload, renderer and Go core.',
] };
let phase = 'startup', win, heldAnswer, detachedID;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const bounded = (promise, label, ms = 15000) => Promise.race([promise, delay(ms).then(() => { throw new Error(`Timed out: ${label}`); })]);
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await bounded(Promise.resolve().then(check), label, 5000)) return;
    await delay(50);
  }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, arg) => bounded(win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`), 'renderer response');
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ ...report, phase, fixture }, null, 2));
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created;
  win.webContents.on('console-message', event => {
    if (event.level === 'error') report.errors.push({ phase, message: event.message });
  });
});
function chunk(res, delta, finishReason) {
  res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta, finish_reason: finishReason || null }] })}\n\n`);
}
function finish(res, content) {
  if (content) chunk(res, { role: 'assistant', content });
  chunk(res, {}, 'stop');
  res.end('data: [DONE]\n\n');
}
function call(res, calls) {
  chunk(res, { role: 'assistant', tool_calls: calls.map((item, index) => ({ index, id: item.id, type: 'function', function: { name: item.name, arguments: JSON.stringify(item.args) } })) });
  chunk(res, {}, 'tool_calls');
  res.end('data: [DONE]\n\n');
}
const server = http.createServer((req, res) => {
  let raw = '';
  req.on('data', part => { raw += part; });
  req.on('end', () => {
    if (req.method === 'GET' || req.url.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] }));
      return;
    }
    const body = JSON.parse(raw || '{}');
    if (!body.stream) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Live plugin fixture' }, finish_reason: 'stop' }] }));
      return;
    }
    const messages = body.messages || [];
    const ids = new Set(messages.flatMap(message => (message.tool_calls || []).map(tool => tool.id)));
    const lastUser = [...messages].reverse().find(message => message.role === 'user');
    const userText = typeof lastUser?.content === 'string' ? lastUser.content : JSON.stringify(lastUser?.content || '');
    report.requests.push({ phase, user: userText.slice(0, 300), calls: [...ids], offeredTools: (body.tools || []).map(tool => tool.function?.name) });
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    if (userText.includes('<process_notification>')) return finish(res, 'Background work completed after plugin publication.');
    if (userText.includes('Continue after installation')) return finish(res, 'Follow-up conversation works.');
    if (userText.includes('Update the plugin')) {
      if (!ids.has('update-entry')) return call(res, [{ id: 'update-entry', name: 'write_file', args: { path: 'live-plugin/desktop.js', content: pluginFiles['live-plugin/desktop.js'].replace('Live plugin ready', 'Live plugin updated') } }]);
      if (!ids.has('update-discover')) return call(res, [{ id: 'update-discover', name: 'tool_search', args: { query: 'select:plugin_manager' } }]);
      if (!ids.has('update-apply')) return call(res, [{ id: 'update-apply', name: 'plugin_manager', args: { action: 'apply', path: 'live-plugin' } }]);
      return finish(res, 'Plugin update completed.');
    }
    if (!ids.has('start-detached')) return call(res, [{ id: 'start-detached', name: 'bash', args: { command: 'while [ ! -f finish-detached ]; do sleep 0.1; done', run_in_background: true } }]);
    if (!ids.has('detach-process')) {
      const result = messages.find(message => message.role === 'tool' && message.tool_call_id === 'start-detached');
      const processID = JSON.stringify(result?.content || '').match(/proc-[a-zA-Z0-9]+/)?.[0];
      if (!processID) { res.end('data: [DONE]\n\n'); report.errors.push({ phase, message: 'background process ID missing' }); return; }
      detachedID = processID;
      return call(res, [{ id: 'detach-process', name: 'process', args: { action: 'update', process_id: processID, completion_mode: 'detached' } }]);
    }
    if (!ids.has('start-background')) return call(res, [{ id: 'start-background', name: 'bash', args: { command: 'while [ ! -f finish-background ]; do sleep 0.1; done; printf background-complete', run_in_background: true } }]);
    if (!ids.has('write-manifest')) return call(res, Object.entries(pluginFiles).map(([file, content], index) => ({ id: ['write-manifest', 'write-package', 'write-entry'][index], name: 'write_file', args: { path: file, content } })));
    if (!ids.has('discover-plugin-manager')) return call(res, [{ id: 'discover-plugin-manager', name: 'tool_search', args: { query: 'select:plugin_manager' } }]);
    if (!ids.has('apply-plugin')) return call(res, [{ id: 'apply-plugin', name: 'plugin_manager', args: { action: 'apply', path: 'live-plugin' } }]);
    chunk(res, { role: 'assistant', content: 'Plugin publication returned.' });
    heldAnswer = res;
  });
});

const composer = '[data-main-conversation-composer] textarea';
async function send(text) {
  await until(() => evaluate(selector => Boolean(document.querySelector(selector) && !document.querySelector(selector).disabled), composer), 'editable composer');
  await evaluate(({ selector, text }) => {
    const input = document.querySelector(selector);
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  }, { selector: composer, text });
  await until(() => evaluate(() => !document.querySelector('[data-main-conversation-composer] .composer-send-button')?.disabled), 'send enabled');
  await evaluate(selector => document.querySelector(selector).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })), composer);
}
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', agent: { permission_mode: 'unconfined' },
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } },
    engines: { default_engine: 'wuu' }, skills: { enabled: false }, ptc: { enabled: false },
  }));
  const stamp = new Date().toISOString();
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Live plugin fixture', path: project, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.isDestroyed(), 'Wuu window');
  win.webContents.setBackgroundThrottling(false);
  await until(() => evaluate(() => Boolean(window.wuu?.onServerEvent)), 'server event bridge');
  await evaluate(() => {
    window.liveCreationCompleted = [];
    window.wuu.onServerEvent(event => {
      if (event.kind === 'notification' && event.message?.method === 'turn/completed') window.liveCreationCompleted.push(event.message.params);
    });
  });
  phase = 'agent-creates-plugin';
  await send('Create and display a plugin while background work continues.');
  await until(() => heldAnswer, 'agent published plugin and continued its turn', 60000);
  assert.ok(fs.existsSync(path.join(home, 'dev/generations/live-creation-fixture/receipt.json')), 'agent publication produced a development generation');
  report.checks.push('The agent authored the plugin and called plugin_manager while its turn was active');
  phase = 'live-render';
  await until(() => evaluate(() => document.querySelector('[data-live-creation="ready"]')?.textContent === 'Live plugin ready'), 'plugin is rendered without restart', 60000);
  assert.equal(await evaluate(() => document.body.innerText.includes('Plugin scope is no longer active (retired)')), false, 'normal generation replacement must not show a retirement error');
  fs.writeFileSync(path.join(output, 'plugin-rendered.png'), (await win.webContents.capturePage()).toPNG());
  report.checks.push('The new plugin rendered while the publishing turn was still running');
  fs.writeFileSync(path.join(project, 'finish-background'), 'finish\n');
  finish(heldAnswer);
  heldAnswer = undefined;
  phase = 'completion';
  await until(() => evaluate(() => document.body.innerText.includes('Background work completed after plugin publication.')), 'background completion continuation after replacement', 60000);
  await until(() => evaluate(() => window.liveCreationCompleted.length >= 2), 'publishing and background turns settled', 60000);
  report.checks.push('The background completion was delivered after generation replacement');
  phase = 'agent-updates-plugin';
  await send('Update the plugin');
  await until(() => evaluate(() => document.querySelector('[data-live-creation="ready"]')?.textContent === 'Live plugin updated'), 'updated plugin is rendered without restart', 60000);
  await until(() => evaluate(() => window.liveCreationCompleted.length >= 3), 'plugin update turn settled', 60000);
  report.checks.push('The agent updated the plugin and its new UI appeared without restart');
  phase = 'follow-up';
  await send('Continue after installation');
  await until(() => evaluate(() => document.body.innerText.includes('Follow-up conversation works.')), 'follow-up answer after replacement', 60000);
  await until(() => evaluate(() => window.liveCreationCompleted.length >= 4), 'follow-up turn settled', 60000);
  assert.equal(await evaluate(() => document.body.innerText.includes('Plugin scope is no longer active (retired)')), false);
  assert.equal(await evaluate(() => document.querySelector('[data-live-creation="ready"]')?.textContent), 'Live plugin updated');
  const workspaceDir = path.join(home, 'workspaces');
  const records = fs.readdirSync(workspaceDir).map(workspace => path.join(workspaceDir, workspace, 'runtime', 'processes', `${detachedID}.json`));
  const recordPath = records.find(file => fs.existsSync(file));
  assert.ok(recordPath, 'the detached process has a managed record');
  const detached = JSON.parse(fs.readFileSync(recordPath, 'utf8'));
  assert.equal(detached.status, 'running', 'the detached process survives publication and follow-up');
  process.kill(detached.pid, 0);
  report.checks.push('The detached managed process remained alive through both plugin publications and the follow-up');
  await delay(3500);
  const debugLog = fs.readFileSync(path.join(home, 'log', 'debug.log'), 'utf8');
  assert.equal(debugLog.includes('inference journal heartbeat update: generation is not accepting autonomous service calls'), false, 'retained journal must keep its heartbeat');
  assert.equal(debugLog.includes('prepare next admission for thread'), false, 'background completion must not enter an admission retry loop');
  win.webContents.sendInputEvent({ type: 'mouseMove', x: 10, y: 10 });
  await delay(200);
  fs.writeFileSync(path.join(output, 'follow-up.png'), (await win.webContents.capturePage()).toPNG());
  report.checks.push('A subsequent user message completed with the plugin still visible');
  assert.deepEqual(report.errors, [], 'renderer emitted no errors during publication');
  fs.writeFileSync(path.join(project, 'finish-detached'), 'finish\n');
  report.status = 'passed';
  save();
  console.log(`PASS live plugin creation; evidence ${output}`);
  server.close();
  app.quit();
}
const watchdog = setTimeout(() => { report.errors.push({ phase, message: 'watchdog timeout' }); report.status = 'failed'; save(); app.exit(1); }, 180000);
run().catch(async error => {
  report.status = 'failed'; report.errors.push({ phase, message: error.stack || String(error) }); save();
  fs.writeFileSync(path.join(project, 'finish-detached'), 'finish\n');
  fs.writeFileSync(path.join(project, 'finish-background'), 'finish\n');
  if (win && !win.isDestroyed()) fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG());
  console.error(error); server.close(); clearTimeout(watchdog); app.exit(1);
});
app.on('before-quit', () => clearTimeout(watchdog));
