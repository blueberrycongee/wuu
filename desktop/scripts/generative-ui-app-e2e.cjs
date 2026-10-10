// Real App/main/preload/app-server/native-helper acceptance. Only the HTTP
// model is synthetic; plugin discovery, tool execution, history and storage
// use production paths in an isolated home. No user key or model is used.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const childProcess = require('node:child_process');
const { syncBuiltinESMExports } = require('node:module');
const { app, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const root = path.dirname(desktop);
const output = path.resolve(process.env.WUU_GENUI_APP_OUTPUT || path.join(desktop, 'out/e2e/generative-ui-app'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-genui-app-'));
const home = path.join(fixture, 'home');
const project = path.join(fixture, 'project');
for (const dir of [home, project, output, path.join(fixture, 'user')]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.HOME = path.join(fixture, 'user');
process.env.USERPROFILE = process.env.HOME;
process.env.CODEX_HOME = path.join(process.env.HOME, '.codex');
process.env.XDG_CONFIG_HOME = path.join(fixture, 'config');
process.env.XDG_DATA_HOME = path.join(fixture, 'data');
for (const name of Object.keys(process.env)) if (/(?:API_KEY|ACCESS_TOKEN|AUTH_TOKEN|SECRET|PASSWORD)$/.test(name)) delete process.env[name];
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = path.resolve(process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'));
process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER = path.resolve(process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER || path.join(desktop, 'build/bin/wuu-generative-ui-plugin'));
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
const subject = 'plugin:bundled:generative-ui';
const spec = {
  version: 1, title: 'Generated UI in Wuu', fallback: 'Design: 12 tasks; Engineering: 24 tasks. Local form only. ![never-load](https://example.invalid/pixel)',
  blocks: [
    { id: 'teams', type: 'table', title: 'Team progress', columns: [{ key: 'team', label: 'Team' }, { key: 'tasks', label: 'Tasks' }], rows: [['Design', 12], ['Engineering', 24]] },
    { id: 'progress', type: 'chart', title: 'Completed tasks', chartType: 'bar', points: [{ label: 'Design', value: 12 }, { label: 'Engineering', value: 24 }] },
    { id: 'draft', type: 'form', title: 'Local follow-up', fields: [{ id: 'owner', label: 'Owner', kind: 'text', initial: 'Design' }] },
  ],
};
const requests = [];
const rpc = [];
const checks = [];
const screenshots = [];
const errors = [];
const processes = [];
const spawn = childProcess.spawn;
childProcess.spawn = (...args) => {
  const child = spawn(...args);
  if (args[0] === process.env.WUU_DESKTOP_CORE) {
    const env = args[2]?.env || {};
    const entry = { command: args[0], args: args[1], helper: env.WUU_GENERATIVE_UI_PLUGIN_HELPER, home: env.WUU_HOME, safeMode: env.WUU_SAFE_MODE, stderr: '' };
    processes.push(entry);
    child.stderr?.on('data', chunk => { entry.stderr = (entry.stderr + chunk).slice(-16000); });
  }
  return child;
};
syncBuiltinESMExports();
let emittedTool = false;
let providerError;
// Observe real handlers and their results; do not replace the preload or IPC.
const handle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => handle(channel, async (...args) => {
  const entry = { channel, args: args.slice(1), completed: false };
  rpc.push(entry);
  try {
    const result = await handler(...args);
    entry.completed = true;
    if (/plugin-storage|extension-package|thread-resume|initialize/.test(channel)) entry.result = result;
    return result;
  } catch (error) { entry.error = String(error); throw error; }
});
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    try {
      const input = JSON.parse(body);
      // Runtime reminder messages can follow the actual user input. Route by
      // the last marked user request rather than the last role=user entry.
      const user = JSON.stringify((input.messages || []).findLast(message => message.role === 'user' && /GENUI_(DEFAULT_OFF|ENABLED|DISABLED_AGAIN)/.test(JSON.stringify(message.content)))?.content || '');
      const toolNames = (input.tools || []).map(tool => tool.function?.name || tool.name);
      const uiTools = toolNames.filter(name => /^plugin_generative_ui_render_ui_[a-f0-9]+$/.test(name));
      requests.push({ path: req.url, body: input, toolNames, latestUser: user });
      const delta = (value, finish = null) => `data: ${JSON.stringify({ id: 'genui-fixture', model: 'fixture', choices: [{ index: 0, delta: value, finish_reason: finish }] })}\n\n`;
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      if (!Array.isArray(input.tools)) {
        res.write(delta({ role: 'assistant', content: 'Generative UI acceptance' }));
      } else if (user.includes('GENUI_DEFAULT_OFF')) {
        assert.equal(uiTools.length, 0, 'Disabled-by-default tool must be absent from the actual provider request');
        assert.ok(!JSON.stringify(input.tools).includes('plugin_generative_ui_render_ui_'), 'Disabled plugin must also be absent from nested discovery');
        res.write(delta({ role: 'assistant', content: 'Default-off discovery verified.' }));
      } else if (user.includes('GENUI_ENABLED')) {
        assert.equal(uiTools.length, 1, 'User enablement exposes exactly one render_ui tool');
        if (!emittedTool) {
          emittedTool = true;
          res.write(delta({ role: 'assistant', tool_calls: [{ index: 0, id: 'genui-app-call', type: 'function', function: { name: uiTools[0], arguments: JSON.stringify({ spec }) } }] }));
          res.end(delta({}, 'tool_calls') + 'data: [DONE]\n\n');
          return;
        }
        assert.ok((input.messages || []).some(message => message.role === 'tool' && JSON.stringify(message.content).includes('Design: 12 tasks')), 'The real native tool result returned to the model loop');
        res.write(delta({ role: 'assistant', content: 'The interactive overview is ready.' }));
      } else if (user.includes('GENUI_DISABLED_AGAIN')) {
        assert.equal(uiTools.length, 0, 'Disabled tool is removed from subsequent actual provider requests');
        assert.ok(!JSON.stringify(input.tools).includes('plugin_generative_ui_render_ui_'), 'Disabled plugin must also be removed from nested discovery');
        res.write(delta({ role: 'assistant', content: 'Disabled-again discovery verified.' }));
      } else {
        // The App may request a conversation title independently of the turn.
        res.write(delta({ role: 'assistant', content: 'Generative UI acceptance' }));
      }
      res.end(delta({}, 'stop') + 'data: [DONE]\n\n');
    } catch (error) { providerError = error; res.end(); }
  });
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, arg) => main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function until(read, label, timeout = 45000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (providerError) throw providerError;
    const result = await read();
    if (result) return result;
    await delay(50);
  }
  throw new Error(`Timed out: ${label}`);
}
const wait = (fn, arg, label = String(fn)) => until(() => evaluate(fn, arg), label);
const click = selector => evaluate(target => {
  const node = document.querySelector(target);
  if (!node) throw new Error(`Missing ${target}`);
  node.click();
}, selector);
const type = (selector, value) => evaluate(({ selector, value }) => {
  const node = document.querySelector(selector);
  if (!node) throw new Error(`Missing ${selector}`);
  Object.getOwnPropertyDescriptor(node instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(node, value);
  node.dispatchEvent(new Event('input', { bubbles: true }));
}, { selector, value });
async function settle() {
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(a => a.playState === 'running' && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
}
async function capture(name, selector) {
  if (selector) await evaluate(target => document.querySelector(target).scrollIntoView({ block: 'center', behavior: 'instant' }), selector);
  await settle();
  fs.writeFileSync(path.join(output, name), (await main.webContents.capturePage()).toPNG());
  screenshots.push(name);
}
function pass(name, evidence) { checks.push({ name, passed: true, ...evidence }); console.log(`PASS ${name}`); }
async function submit(text, answer) {
  await wait(() => document.querySelector('.composer textarea') && !document.querySelector('.composer-stop-button'));
  await type('.composer textarea', text);
  await wait(() => { const button = document.querySelector('.composer-send-button'); return button && !button.disabled && button.getAttribute('aria-busy') !== 'true'; });
  const before = rpc.length;
  await click('.composer-send-button');
  await until(() => rpc.slice(before).some(entry => entry.channel === 'wuu:turn-start'), 'real turn-start IPC after composer submission');
  await wait(text => [...document.querySelectorAll('.session-flow')].some(node => node.textContent.includes(text)), answer);
  await wait(() => !document.querySelector('.composer-stop-button'));
}
const pluginSwitch = '[data-plugin="generative-ui"] [role="switch"]';
async function catalog() {
  await evaluate(() => {
    const button = [...document.querySelectorAll('.primary-nav button')].find(node => node.querySelector('[data-icon="plugin-blocks"]'));
    if (!button) throw new Error('Missing plugin catalog navigation');
    button.click();
  });
  await wait(() => document.querySelector('[data-tab="plugins"]'));
  await click('[data-tab="plugins"]');
  await wait(selector => document.querySelector(selector), pluginSwitch);
}
async function conversation() {
  await evaluate(() => document.querySelector('[data-section-id="genui-app"] button[aria-expanded="false"]')?.click());
  await wait(() => document.querySelector('[data-section-id="genui-app"] .sidebar-session-row .thread-row-main'));
  await click('[data-section-id="genui-app"] .sidebar-session-row .thread-row-main');
  await wait(() => document.querySelector('.composer textarea'));
}
const hash = filename => createHash('sha256').update(fs.readFileSync(filename)).digest('hex');
function stateDocuments() {
  const dir = path.join(home, 'plugin-storage/user');
  if (!fs.existsSync(dir)) return [];
  return fs.readdirSync(dir).filter(name => name.endsWith('.json')).map(name => JSON.parse(fs.readFileSync(path.join(dir, name), 'utf8'))).filter(doc => doc.plugin_id === subject);
}
let main;
app.on('browser-window-created', (_event, win) => {
  if (main) return;
  main = win;
  win.webContents.on('console-message', (_event, level, message) => { if (level >= 2) errors.push(message); });
});
const timeout = setTimeout(() => { console.error('Full App E2E timed out', fixture); app.exit(1); }, 240000);
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  // Preserve the tested plugin's actual manifest default, without a false override.
  const disabled = Object.fromEntries(fs.readdirSync(path.join(root, 'internal/plugin/bundled')).filter(name => name !== 'generative-ui').map(name => [`plugin:bundled:${name}`, true]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture',
    engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'synthetic-not-a-credential', model: 'fixture' } },
    extensions: { disabled },
  }));
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'genui-app', name: 'Generated UI acceptance', path: project, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'genui-app', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 14 }));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => main, 'main BrowserWindow');
  main.setSize(1280, 1000);
  main.show(); main.focus();
  await wait(() => document.querySelector('.composer textarea'));
  const initialization = await until(() => rpc.find(entry => entry.channel === 'wuu:initialize' && entry.completed), 'native initialization');
  assert.ok(initialization.result.extension_inventory.some(record => record.kind === 'plugin' && record.provenance?.plugin_id === 'generative-ui'), 'Real initialization must expose the disabled bundled plugin');
  await submit('GENUI_DEFAULT_OFF', 'Default-off discovery verified.');
  await catalog();
  assert.equal(await evaluate(selector => document.querySelector(selector).getAttribute('aria-checked'), pluginSwitch), 'false');
  await capture('01-default-disabled.png', '[data-plugin="generative-ui"]');
  pass('manifest default is off in real catalog and provider tool discovery');
  await click(pluginSwitch);
  await wait(selector => document.querySelector(selector)?.getAttribute('aria-checked') === 'true' && !document.querySelector(selector).disabled, pluginSwitch);
  assert.ok(rpc.some(entry => entry.channel === 'wuu:extension-package-update' && entry.completed && entry.args[0]?.action === 'enable'));
  await capture('02-user-enabled.png', '[data-plugin="generative-ui"]');
  await conversation();
  await submit('GENUI_ENABLED', 'The interactive overview is ready.');
  await wait(() => document.querySelector('[data-wuu-component="generated-ui"]')?.getAttribute('aria-busy') === 'false');
  assert.equal(await evaluate(() => document.querySelectorAll('[data-wuu-component="generated-ui"]').length), 1);
  assert.ok(await evaluate(() => document.querySelector('.session-flow [data-wuu-component="generated-ui"]')));
  pass('real model tool call reaches Go helper, persisted message flow, MIME registry and production renderer');
  await capture('03-app-interactive.png', '.plugin-genui-header');
  await type('[data-genui-block="teams"] input', 'Engineering');
  await type('[data-genui-block="draft"] input', 'Persisted via production storage');
  await click('[data-genui-block="draft"] button[type="submit"]');
  await wait(() => document.querySelector('.plugin-genui-form-preview')?.textContent.includes('Persisted via production storage'));
  await until(() => stateDocuments().some(doc => Object.values(doc.values).some(raw => raw.includes('Persisted via production storage') && raw.includes('Engineering'))), 'Go plugin-storage file persisted both edits');
  await until(() => rpc.filter(entry => entry.channel === 'wuu:plugin-storage-set' && entry.completed).length >= 2, 'storage RPC acknowledgements after atomic file writes');
  const writes = rpc.filter(entry => entry.channel === 'wuu:plugin-storage-set' && entry.completed);
  assert.ok(writes.length >= 2 && writes.every(entry => entry.args[0].scope === 'user'));
  await capture('04-app-persisted-form.png', '[data-genui-block="draft"]');
  pass('production storage IPC writes the edited filter and form into the Go user-scope store', { writes: writes.length });
  const beforeReload = rpc.length;
  const loaded = new Promise(resolve => main.webContents.once('did-finish-load', resolve));
  main.reload(); await loaded;
  await wait(() => document.querySelector('[data-genui-block="draft"] input')?.value === 'Persisted via production storage');
  assert.equal(await evaluate(() => document.querySelector('[data-genui-block="teams"] input').value), 'Engineering');
  assert.ok(rpc.slice(beforeReload).some(entry => entry.channel === 'wuu:plugin-storage-get' && entry.completed && entry.result?.value?.includes('Persisted via production storage')));
  assert.ok(rpc.slice(beforeReload).some(entry => entry.channel === 'wuu:thread-resume' && entry.completed && JSON.stringify(entry.result).includes('application/vnd.wuu.ui+json')), 'Historical resource came from the real app-server thread resume');
  await capture('05-app-reloaded.png', '[data-genui-block="draft"]');
  pass('full renderer reload restores native history and production plugin storage');
  await catalog(); await click(pluginSwitch);
  await wait(selector => document.querySelector(selector)?.getAttribute('aria-checked') === 'false' && !document.querySelector(selector).disabled, pluginSwitch);
  await conversation();
  await wait(() => !document.querySelector('[data-wuu-component="generated-ui"]') && [...document.querySelectorAll('.session-flow')].some(node => node.textContent.includes('![never-load]')));
  assert.equal(await evaluate(() => document.querySelectorAll('.session-flow img[src*="example.invalid"]').length), 0);
  await capture('06-app-disabled-fallback.png');
  await submit('GENUI_DISABLED_AGAIN', 'Disabled-again discovery verified.');
  pass('user disablement removes renderer and tool discovery while preserving literal historical fallback');
  // Re-enable the same historical widget without rerunning the model/tool.
  const calls = requests.length;
  await catalog(); await click(pluginSwitch);
  await wait(selector => document.querySelector(selector)?.getAttribute('aria-checked') === 'true' && !document.querySelector(selector).disabled, pluginSwitch);
  await conversation();
  await wait(() => document.querySelector('[data-genui-block="draft"] input')?.value === 'Persisted via production storage');
  assert.equal(requests.length, calls, 'Re-enabling history does not send a model request');
  await capture('07-app-reenabled-history.png', '[data-genui-block="draft"]');
  pass('re-enabled historical renderer retains local edits without model activity');
  const evidence = { recordedAt: new Date().toISOString(), scope: 'Real App, main, preload, app-server, Go helper, native history and plugin storage; loopback synthetic HTTP provider only.',
    hashes: { harness: hash(__filename), core: hash(process.env.WUU_DESKTOP_CORE), helper: hash(process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER), main: hash(path.join(desktop, 'out/main/index.js')), preload: hash(path.join(desktop, 'out/preload/index.cjs')) },
    checks, screenshots, processes, storage: stateDocuments(), requests, rpc, errors };
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(evidence, null, 2));
  console.log(`Full App generative UI E2E passed: ${output}`);
}
run().then(() => { clearTimeout(timeout); server.close(); app.quit(); }).catch(async error => {
  clearTimeout(timeout);
  fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: String(error.stack || error), fixture, checks, requests, rpc, errors, processes, binaries: { core: { path: process.env.WUU_DESKTOP_CORE, hash: hash(process.env.WUU_DESKTOP_CORE) }, helper: { path: process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER, hash: hash(process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER), mode: fs.statSync(process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER).mode } } }, null, 2));
  if (main) fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
  console.error(error); server.close(); app.exit(1);
});
