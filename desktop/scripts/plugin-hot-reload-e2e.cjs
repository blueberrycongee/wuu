const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawn, spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');

// Current checkout's Electron, Go core, renderer, local model and temporary data.
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_PLUGIN_HOT_RELOAD_OUTPUT || path.join(desktop, 'out/e2e/plugin-hot-reload'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-token-speed-check-'));
const wuuData = path.join(fixture, 'home');
const project = path.join(fixture, 'project');
const source = path.join(project, 'token-speed');
const core = path.join(desktop, 'build/bin/wuu-core');
const trigger = path.join(project, 'publish-now');
const runningInput = process.env.WUU_PLUGIN_RELOAD_RUNNING_INPUT || '';
if (!['', 'guide', 'queue'].includes(runningInput)) throw new Error('WUU_PLUGIN_RELOAD_RUNNING_INPUT must be guide or queue');
for (const dir of [output, wuuData, project, source]) fs.mkdirSync(dir, { recursive: true });
fs.writeFileSync(path.join(source, 'plugin.json'), JSON.stringify({ schemaVersion: 1, id: 'token-speed', name: 'Token speed fixture', version: '1.0.0', desktop: { entry: 'desktop.js' } }));
fs.writeFileSync(path.join(source, 'package.json'), JSON.stringify({ type: 'module', private: true, scripts: { build: 'node --check desktop.js' } }));
const plugin = `const version = 'initial';
export function activate(api) {
  const listeners = new Set(); let revision = 0;
  api.onHostEvent(event => {
    if (event?.kind !== 'notification' || event.message?.method !== 'item/agentMessage/delta') return;
    revision++; for (const listener of listeners) listener();
  });
  api.registerCleanup(() => listeners.clear());
  function Speed({ context }) {
    const count = api.react.useSyncExternalStore(listener => { listeners.add(listener); return () => listeners.delete(listener); }, () => revision, () => revision);
    return context.mainConversation ? api.react.createElement('span', { 'data-wuu-component': 'token-speed-fixture', 'data-test-generation': version,
      'data-state': context.running && count ? 'streaming' : 'idle' }, version) : null;
  }
  api.registerUIContribution({ id: 'speed', target: 'wuu.default-composer/composer.above', render: ({ context }) => api.react.createElement(Speed, { context }) });
}
`;
const updatedPlugin = plugin.replace("const version = 'initial';", "const version = 'updated';");
fs.writeFileSync(path.join(source, 'desktop.js'), plugin);
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: wuuData, WUU_DESKTOP_CORE: core,
  WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
if (process.env.WUU_PLUGIN_RELOAD_RENDERER_URL) process.env.ELECTRON_RENDERER_URL = process.env.WUU_PLUGIN_RELOAD_RENDERER_URL;
else delete process.env.ELECTRON_RENDERER_URL;
const report = { status: 'running', phases: [], errors: [], exceptions: [], activations: [], inputs: [], fixture, limitations: [
  'Temporary Wuu data, synthetic model and DOM input events; external CLI publication and the current desktop and Go core.',
] };
let phase = 'setup', win, heldAnswer, heldOther, firstFingerprint, gateArmed = false, gateReached = false, releaseGate, gate;
function armGate(fingerprint) {
  firstFingerprint = fingerprint;
  gateReached = false; gateArmed = true;
  gate = new Promise(resolve => { releaseGate = resolve; });
}
const originalHandle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => originalHandle(channel, async (...args) => {
  if (channel === 'wuu:plugin-desktop-activation') report.activations.push(args[1]);
  const receipt = path.join(wuuData, 'dev/generations/token-speed/receipt.json');
  if (channel === 'wuu:project-list' && gateArmed && fs.existsSync(receipt) && JSON.parse(fs.readFileSync(receipt, 'utf8')).fingerprint !== firstFingerprint) { gateReached = true; await gate; }
  if (!['wuu:turn-steer', 'wuu:turn-queue'].includes(channel)) return handler(...args);
  const result = await handler(...args);
  report.inputs.push({ channel, threadId: args[1] });
  return result;
});
const streamTimers = new Map();
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const bounded = (promise, label, ms = 15000) => Promise.race([promise, sleep(ms).then(() => { throw new Error(`Timed out: ${label}`); })]);
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await bounded(Promise.resolve().then(check), label, 5000)) return; await sleep(40); }
  throw new Error(`Timed out: ${label}`);
}
function save() { fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ ...report, phase }, null, 2)); }
const evaluate = (fn, arg) => bounded(win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`), 'renderer');
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created; created.hide();
  created.webContents.setBackgroundThrottling(false);
  created.webContents.on('console-message', event => {
    if (event.level === 'error') report.errors.push({ phase, message: event.message });
  });
  try {
    const d = created.webContents.debugger;
    d.attach('1.3');
    d.on('message', (_e, method, params) => { if (method === 'Runtime.exceptionThrown') report.exceptions.push({ phase, details: params.exceptionDetails }); });
    void d.sendCommand('Runtime.enable').catch(error => report.errors.push({ phase, message: `Debugger enable: ${error.message}` }));
  } catch (error) { report.errors.push({ phase, message: `Debugger attach: ${error.message}` }); }
});
function chunk(res, delta, reason) { res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta, finish_reason: reason || null }] })}\n\n`); }
function finish(res, content) { if (streamTimers.has(res)) { clearInterval(streamTimers.get(res)); streamTimers.delete(res); } if (content) chunk(res, { role: 'assistant', content }); chunk(res, {}, 'stop'); res.end('data: [DONE]\n\n'); }
function hold(res, initial) { chunk(res, { role: 'assistant', content: initial }); const timer = setInterval(() => { if (!res.writableEnded) chunk(res, { content: '.' }); }, 80); streamTimers.set(res, timer); res.once('close', () => { clearInterval(timer); streamTimers.delete(res); }); }
function call(res, name, id, args) { chunk(res, { role: 'assistant', tool_calls: [{ index: 0, id, type: 'function', function: { name, arguments: JSON.stringify(args) } }] }); chunk(res, {}, 'tool_calls'); res.end('data: [DONE]\n\n'); }
const shellQuote = value => `'${value.replace(/'/g, "'\\''")}'`;
const server = http.createServer((req, res) => {
  let raw = ''; req.on('data', part => { raw += part; }); req.on('end', () => {
    if (req.method === 'GET' || req.url.endsWith('/models')) { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] })); return; }
    const body = JSON.parse(raw || '{}');
    if (!body.stream) { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Fixture' }, finish_reason: 'stop' }] })); return; }
    const messages = body.messages || [];
    const ids = new Set(messages.flatMap(message => (message.tool_calls || []).map(tool => tool.id)));
    const userText = messages.filter(message => message.role === 'user').map(message => typeof message.content === 'string' ? message.content : JSON.stringify(message.content || '')).join('\n');
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    if (userText.includes('Generate a title')) return finish(res, 'Fixture conversation');
    if (userText.includes('Draft edited while reloading')) return finish(res, 'Follow-up works after plugin publication.');
    if (userText.includes('<process_notification>')) return finish(res, 'Background process completed.');
    if (userText.includes('A conversation requested during reload')) return finish(res, 'Deferred conversation works.');
    if (userText.includes('A new conversation after reload')) return finish(res, 'New conversation works after reload.');
    if (userText.includes('Keep a parallel conversation active')) { hold(res, 'Parallel conversation is active.'); heldOther = res; return; }
    if (!ids.has('write-speed')) return call(res, 'write_file', 'write-speed', { path: 'token-speed/desktop.js', content: updatedPlugin });
    if (!ids.has('publish-speed')) return call(res, 'bash', 'publish-speed', { command: `while [ ! -f ${shellQuote(trigger)} ]; do sleep 0.05; done; exec ${shellQuote(core)} plugin dev --watch=false ${shellQuote(source)}`, run_in_background: true });
    hold(res, 'Publishing the token speed plugin.'); heldAnswer = res;
  });
});
const composer = '[data-main-conversation-composer] textarea';
async function typeDraft(text) {
  await until(() => evaluate(selector => { const input = document.querySelector(selector); return Boolean(input && !input.disabled && !input.readOnly && !input.closest('[inert]')); }, composer), 'editable composer', 60000);
  await evaluate(({ selector, text }) => { const input = document.querySelector(selector); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, text); input.dispatchEvent(new Event('input', { bubbles: true })); }, { selector: composer, text });
  await until(() => evaluate(() => { const send = document.querySelector('[data-main-conversation-composer] .composer-send-button'); return Boolean(send && !send.disabled); }), 'send enabled after draft', 30000);
}
async function submit(queue = false) { await evaluate(({ selector, queue }) => document.querySelector(selector).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', metaKey: queue, bubbles: true, cancelable: true })), { selector: composer, queue }); }
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(wuuData, 'config.json'), JSON.stringify({ default_provider: 'fixture', agent: { permission_mode: 'unconfined' }, providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } }, engines: { default_engine: 'wuu' }, skills: { enabled: false }, ptc: { enabled: false } }));
  const stamp = new Date().toISOString();
  fs.writeFileSync(path.join(wuuData, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Fixture', path: project, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(wuuData, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  const initial = spawnSync(core, ['plugin', 'dev', '--watch=false', source], { cwd: project, env: process.env, encoding: 'utf8', timeout: 30000 });
  fs.writeFileSync(path.join(output, 'initial-publication.log'), `${initial.stdout}\n${initial.stderr}`);
  assert.equal(initial.status, 0, 'initial external CLI publication');
  const receiptPath = path.join(wuuData, 'dev/generations/token-speed/receipt.json');
  firstFingerprint = JSON.parse(fs.readFileSync(receiptPath, 'utf8')).fingerprint;
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.isDestroyed(), 'Wuu Dev window');
  await until(() => evaluate(() => Boolean(document.querySelector('[data-wuu-component="token-speed-fixture"]'))), 'input-side plugin mounted', 60000);
  report.phases.push('Input-side streaming plugin mounted in the composer');
  phase = 'parallel-conversation';
  await typeDraft('Keep a parallel conversation active.'); await submit();
  await until(() => heldOther, 'parallel conversation running', 30000);
  await until(() => evaluate(() => Boolean(document.querySelector('[data-wuu-component=\"token-speed-fixture\"][data-state=\"streaming\"]'))), 'token speed receives live stream', 30000);
  await evaluate(() => document.querySelector('.primary-nav .nav-item').click());
  await until(() => evaluate(() => Boolean(document.querySelector('.empty-home'))), 'new draft for plugin update', 30000);
  phase = 'agent-updates'; armGate(firstFingerprint);
  await typeDraft('Update the token speed plugin while this conversation is running.'); await submit();
  await until(() => heldAnswer, 'publishing agent is running', 30000);
  if (!runningInput) {
    await evaluate(() => document.querySelector('.primary-nav .nav-item').click());
    await until(() => evaluate(() => Boolean(document.querySelector('.empty-home'))), 'draft before plugin publication', 30000);
  }
  await typeDraft('Before reload');
  await evaluate(selector => { const input = document.querySelector(selector); input.focus(); window.handoffEditor = input; }, composer);
  await until(() => evaluate(() => Object.keys(sessionStorage).some(k => sessionStorage.getItem(k).includes('Before reload'))), 'initial draft persisted', 5000);
  report.phases.push('Initial draft was persisted before reload');
  fs.writeFileSync(trigger, 'go');
  await until(() => JSON.parse(fs.readFileSync(receiptPath, 'utf8')).fingerprint !== firstFingerprint, 'external CLI published changed plugin', 60000);
  report.phases.push('Agent updated the input-side plugin using an external development command');
  phase = 'held-handoff-focus';
  await until(() => gateReached, 'replacement project-list request held', 30000);
  await evaluate(({ selector, text }) => { const input = document.querySelector(selector); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, text); input.dispatchEvent(new Event('input', { bubbles: true })); }, {selector: composer, text: 'Draft edited while reloading'});
  await evaluate(() => window.dispatchEvent(new Event('focus')));
  await sleep(300);
  if (runningInput) {
    await submit(runningInput === 'queue');
    // Let the retired scope reject the click before releasing the successor.
    await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  }
  const retired = report.exceptions.filter(entry => JSON.stringify(entry).includes('Plugin scope is no longer active'));
  const before = report.activations.filter(a => a.id.includes('wuu.default-application') && a.phase === 'registration' && a.status === 'active').length;
  gateArmed = false; releaseGate();
  await until(() => report.activations.filter(a => a.id.includes('wuu.default-application') && a.phase === 'registration' && a.status === 'active').length > before, 'successor application active', 30000);
  report.phases.push(`Focus during handoff produced ${retired.length} retired-scope exception(s)`);
  phase = 'new-draft-after-publication';
  const value = await evaluate(selector => document.querySelector(selector)?.value, composer);
  report.phases.push(`Draft after handoff: ${value}`);
  if (!runningInput) assert.equal(value, 'Draft edited while reloading', 'draft retains edits typed during provider handoff');
  assert.equal(await evaluate(selector => document.querySelector(selector) === window.handoffEditor && document.activeElement === window.handoffEditor, composer), true, 'focused editor survives the handoff');
  await until(() => evaluate(() => Boolean(document.querySelector('[data-wuu-component=\"token-speed-fixture\"][data-test-generation=\"updated\"]'))), 'updated token-speed is rendered', 60000);
  if (runningInput) {
    phase = 'running-conversation-input';
    await until(() => evaluate(() => document.querySelector('.composer-pending-drawer')?.textContent.includes('Draft edited while reloading')), 'running input admitted', 15000);
    const expected = runningInput === 'guide' ? 'wuu:turn-steer' : 'wuu:turn-queue';
    assert.equal(report.inputs.filter(input => input.channel === expected).length, 1, 'input is admitted exactly once');
    fs.writeFileSync(path.join(output, 'running-input-pending.png'), (await win.webContents.capturePage()).toPNG());
    if (heldAnswer && !heldAnswer.writableEnded) finish(heldAnswer, 'Plugin update completed.');
    if (heldOther && !heldOther.writableEnded) finish(heldOther, 'Parallel conversation completed.');
    await until(() => evaluate(() => document.body.innerText.includes('Follow-up works after plugin publication.')), 'model processes running input', 30000);
    assert.equal(report.exceptions.some(entry => JSON.stringify(entry).includes('Plugin scope is no longer active')), false, 'no retired-scope exception after the handoff');
    assert.deepEqual(report.errors, [], 'renderer diagnostics remain available without errors');
    fs.writeFileSync(path.join(output, 'running-input-completed.png'), (await win.webContents.capturePage()).toPNG());
    report.phases.push(`${runningInput} clicked during handoff was admitted and processed in the running conversation`);
    report.status = 'passed'; save(); console.log(`PASS ${runningInput} during plugin reload; evidence ${output}`);
    server.close(); app.quit(); return;
  }
  await until(() => evaluate(() => { const b = document.querySelector('[data-main-conversation-composer] .composer-send-button'); return Boolean(b && !b.disabled); }), 'send enabled after handoff', 30000);
  await until(() => evaluate(() => Object.keys(sessionStorage).some(k => sessionStorage.getItem(k).includes('Draft edited while reloading'))), 'handoff draft persisted', 5000);
  report.phases.push('Draft edits persisted after handoff');
  assert.equal(retired.length, 0, 'focus during handoff does not call a retired application scope');

  await submit();
  await until(() => evaluate(() => document.body.innerText.includes('Follow-up works after plugin publication.')), 'new conversation completes', 60000);
  await evaluate(() => document.querySelector('.primary-nav .nav-item').click());
  await until(() => evaluate(() => Boolean(document.querySelector('.empty-home'))), 'new conversation after reload', 30000);
  await typeDraft('A new conversation after reload');
  await submit();
  await until(() => evaluate(() => document.body.innerText.includes('New conversation works after reload.')), 'new conversation completes after reload', 60000);
  await typeDraft('Previous conversation draft');
  await until(() => evaluate(() => Object.keys(sessionStorage).some(k => sessionStorage.getItem(k).includes('Previous conversation draft'))), 'previous draft persisted', 5000);
  phase = 'new-conversation-during-handoff';
  const currentFingerprint = JSON.parse(fs.readFileSync(receiptPath, 'utf8')).fingerprint;
  fs.writeFileSync(path.join(source, 'desktop.js'), updatedPlugin.replace("const version = 'updated';", "const version = 'second';"));
  armGate(currentFingerprint);
  const published = new Promise((resolve, reject) => {
    const child = spawn(core, ['plugin', 'dev', '--watch=false', source], { cwd: project, env: process.env, stdio: 'ignore' });
    child.once('error', reject);
    child.once('exit', code => code === 0 ? resolve() : reject(new Error(`Second publication failed: ${code}`)));
  });
  await until(() => gateReached, 'second provider handoff held', 60000);
  await evaluate(({ selector, text }) => { const input = document.querySelector(selector); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, text); input.dispatchEvent(new Event('input', { bubbles: true })); }, { selector: composer, text: 'Previous draft edited during reload' });
  await sleep(100);
  await evaluate(() => document.querySelector('.primary-nav .nav-item').click());
  const activeBefore = report.activations.filter(a => a.id.includes('wuu.default-application') && a.phase === 'registration' && a.status === 'active').length;
  gateArmed = false; releaseGate();
  await published;
  await until(() => report.activations.filter(a => a.id.includes('wuu.default-application') && a.phase === 'registration' && a.status === 'active').length > activeBefore, 'second successor application active', 30000);
  await until(() => evaluate(() => Boolean(document.querySelector('.empty-home'))), 'conversation requested during handoff opens', 10000);
  await until(() => evaluate(() => Object.keys(sessionStorage).some(k => sessionStorage.getItem(k).includes('Previous draft edited during reload'))), 'previous conversation draft preserved', 5000);
  await typeDraft('A conversation requested during reload');
  await submit();
  await until(() => evaluate(() => document.body.innerText.includes('Deferred conversation works.')), 'conversation requested during handoff completes', 60000);
  report.phases.push('A new conversation requested during handoff opened and sent after the successor became active');
  if (heldAnswer && !heldAnswer.writableEnded) finish(heldAnswer, 'Plugin update completed.');
  if (heldOther && !heldOther.writableEnded) finish(heldOther, 'Parallel conversation completed.');
  await sleep(1500);
  assert.equal(report.exceptions.some(entry => JSON.stringify(entry).includes('Plugin scope is no longer active')), false, 'no retired-scope exception after the handoff');
  assert.deepEqual(report.errors, [], 'renderer diagnostics remain available without errors');

  fs.writeFileSync(path.join(output, 'follow-up.png'), (await win.webContents.capturePage()).toPNG());
  report.phases.push('Updated token-speed fixture rendered and the new draft sent a follow-up while two conversations were actively streaming');
  report.status = 'passed'; save(); console.log(`PASS plugin hot reload with draft handoff; evidence ${output}`);
  server.close(); app.quit();
}
const watchdog = setTimeout(() => { report.status = 'failed'; report.errors.push({ phase, message: 'watchdog timeout' }); save(); app.exit(1); }, 180000);
run().catch(async error => {
  report.status = 'failed'; report.errors.push({ phase, message: error.stack || String(error) });
  if (win && !win.isDestroyed()) { try { fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG()); } catch {} }
  save(); console.error(error); server.close(); clearTimeout(watchdog); app.exit(1);
});
app.on('before-quit', () => clearTimeout(watchdog));
