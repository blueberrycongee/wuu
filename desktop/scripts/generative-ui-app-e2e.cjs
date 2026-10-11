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
const actionSpec = {
  version: 2, title: 'Follow-up', fallback: 'Send owner',
  state: [{ id: 'owner', value: 'Design' }],
  body: [
    { id: 'ownerInput', type: 'input', label: 'Owner', state: 'owner' },
    { id: 'send', type: 'button', label: 'Send owner', onClick: [
      { type: 'sendMessage', text: { op: 'concat', args: ['GENUI_RESPONSE ', { op: 'state', key: 'owner' }] } },
    ] },
    { id: 'ownerForm', type: 'form', label: 'Send owner form', children: [
      { id: 'formOwner', type: 'input', label: 'Required owner', state: 'owner', required: true },
    ], onSubmit: [
      { type: 'sendMessage', text: { op: 'concat', args: ['GENUI_RESPONSE ', { op: 'state', key: 'owner' }] } },
    ] },
  ],
};
const requests = [];
const rpc = [];
const checks = [];
const screenshots = [];
const recordings = [];
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
let emittedAction = false;
let followupResponse;
let actionSource;
let providerError;
let liveStream;
let canceledStream;
const streamEvidence = [];
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
      // Providers can merge consecutive user messages after an interruption;
      // choose the final marker in that merged content, not the first match.
      const user = JSON.stringify((input.messages || []).findLast(message => message.role === 'user' && /GENUI_(DEFAULT_OFF|ENABLED|DISABLED_AGAIN|CANCEL|ACTION|RESPONSE)/.test(JSON.stringify(message.content)))?.content || '');
      const route = user.match(/GENUI_(DEFAULT_OFF|ENABLED|DISABLED_AGAIN|CANCEL|ACTION|RESPONSE)/g)?.at(-1);
      const toolNames = (input.tools || []).map(tool => tool.function?.name || tool.name);
      const uiTools = toolNames.filter(name => /^plugin_generative_ui_render_ui_[a-f0-9]+$/.test(name));
      requests.push({ path: req.url, body: input, toolNames, latestUser: user });
      const delta = (value, finish = null) => `data: ${JSON.stringify({ id: 'genui-fixture', model: 'fixture', choices: [{ index: 0, delta: value, finish_reason: finish }] })}\n\n`;
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      if (!Array.isArray(input.tools)) {
        res.write(delta({ role: 'assistant', content: 'Generative UI acceptance' }));
      } else if (route === 'GENUI_DEFAULT_OFF') {
        assert.equal(uiTools.length, 0, 'Disabled-by-default tool must be absent from the actual provider request');
        assert.ok(!JSON.stringify(input.tools).includes('plugin_generative_ui_render_ui_'), 'Disabled plugin must also be absent from nested discovery');
        res.write(delta({ role: 'assistant', content: 'Default-off discovery verified.' }));
      } else if (route === 'GENUI_ENABLED') {
        assert.equal(uiTools.length, 1, 'User enablement exposes exactly one render_ui tool');
        if (!emittedTool) {
          emittedTool = true;
          const independent = { version: 2, title: 'Independent streamed widget', fallback: 'A separate UI stream.', state: [], body: [{ id: 'independent', type: 'text', text: 'Independent stream content' }] };
          const argumentsByIndex = [spec, independent].map(value => JSON.stringify({ spec: value }));
          const firstBoundary = argumentsByIndex[0].indexOf(JSON.stringify(spec.blocks[0])) + JSON.stringify(spec.blocks[0]).length;
          const secondBoundary = argumentsByIndex[0].indexOf(JSON.stringify(spec.blocks[1])) + JSON.stringify(spec.blocks[1]).length;
          const independentBoundary = argumentsByIndex[1].length - 3;
          const append = (index, arguments) => res.write(delta({ tool_calls: [{ index, function: { arguments } }] }));
          // Both calls start before either receives arguments. This catches
          // providers or host layers that append deltas to the latest call.
          res.write(delta({ role: 'assistant', tool_calls: argumentsByIndex.map((_, index) => ({ index, id: `genui-app-call-${index}`, type: 'function', function: { name: uiTools[0], arguments: '' } })) }));
          liveStream = {
            first() {
              append(0, argumentsByIndex[0].slice(0, firstBoundary));
              append(1, argumentsByIndex[1].slice(0, independentBoundary));
            },
            second() { append(0, argumentsByIndex[0].slice(firstBoundary, secondBoundary)); },
            finish() {
              append(1, argumentsByIndex[1].slice(independentBoundary));
              append(0, argumentsByIndex[0].slice(secondBoundary));
              res.end(delta({}, 'tool_calls') + 'data: [DONE]\n\n');
              this.finished = true;
            },
            finished: false,
          };
          return;
        }
        assert.ok((input.messages || []).some(message => message.role === 'tool' && JSON.stringify(message.content).includes('Design: 12 tasks')), 'The real native tool result returned to the model loop');
        res.write(delta({ role: 'assistant', content: 'The interactive overview is ready.' }));
      } else if (route === 'GENUI_RESPONSE') {
        assert.ok(user.includes('Engineering follow-up'), 'The continuation includes the explicit action text');
        const providerText = JSON.parse(user);
        assert.equal(typeof providerText, 'string');
        let contextText = providerText;
        let delivery = 'inline';
        if (!contextText.includes('Submitted artifact context (JSON):')) {
          // The normal input-parts pipeline materializes pasted context as a
          // file. Verify the actual file offered to the model, not just IPC.
          assert.ok(providerText.includes('Attached text file "Interactive response"'));
          const contextPath = providerText.split('\n').find(line => line.startsWith(home + path.sep) && line.includes(`${path.sep}input-attachments${path.sep}`));
          assert.ok(contextPath, 'The provider can address the submitted context attachment');
          const resolved = fs.realpathSync(contextPath);
          assert.ok(resolved.startsWith(fs.realpathSync(home) + path.sep), 'Only the exact attachment within this isolated App home may be read');
          contextText = fs.readFileSync(resolved, 'utf8');
          delivery = 'attached-file';
        }
        assert.ok(contextText.includes('Submitted artifact context (JSON):') && contextText.includes('Engineering follow-up'), 'The continuation includes explicit values and host attribution');
        const context = JSON.parse(contextText.split('Submitted artifact context (JSON):')[1]);
        assert.equal(context.threadId, rpc.filter(entry => entry.channel === 'wuu:turn-start').at(-1).args[0]);
        for (const [key, value] of Object.entries(actionSource)) assert.equal(context[key], value, `Submitted source ${key} matches the original persisted UI`);
        assert.deepEqual(context.values.values, { owner: 'Engineering follow-up' }, 'Unrelated widget values do not leak into the submission');
        requests.at(-1).submittedContext = { delivery, context };
        followupResponse = () => res.end(delta({ role: 'assistant', content: 'Follow-up accepted for Engineering.' }) + delta({}, 'stop') + 'data: [DONE]\n\n');
        return;
      } else if (route === 'GENUI_ACTION') {
        assert.equal(uiTools.length, 1);
        if (!emittedAction) {
          emittedAction = true;
          res.write(delta({ role: 'assistant', tool_calls: [{ index: 0, id: 'genui-action-call', type: 'function', function: { name: uiTools[0], arguments: JSON.stringify({ spec: actionSpec }) } }] }));
          res.end(delta({}, 'tool_calls') + 'data: [DONE]\n\n');
          return;
        }
        res.write(delta({ role: 'assistant', content: 'The follow-up form is ready.' }));
      } else if (route === 'GENUI_CANCEL') {
        assert.equal(uiTools.length, 1);
        const canceled = { version: 2, title: 'Canceled stream', fallback: 'Incomplete preview only.', state: [], body: [{ id: 'canceled', type: 'text', text: 'This generation can be stopped.' }] };
        const partial = JSON.stringify({ spec: canceled }).slice(0, -3);
        res.write(delta({ role: 'assistant', tool_calls: [{ index: 0, id: 'genui-canceled-call', type: 'function', function: { name: uiTools[0], arguments: partial } }] }));
        canceledStream = { closed: false };
        res.once('close', () => { canceledStream.closed = true; });
        return;
      } else if (route === 'GENUI_DISABLED_AGAIN') {
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
let lastOperation = 'Start full App acceptance';
function withDeadline(promise, label, timeout = 20000) {
  if (label !== lastOperation) {
    lastOperation = label;
    fs.writeFileSync(path.join(output, 'progress.json'), JSON.stringify({ operation: label, at: new Date().toISOString() }));
  }
  let timer;
  return Promise.race([promise, new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`Timed out after ${timeout} ms: ${label}`)), timeout);
  })]).finally(() => clearTimeout(timer));
}
const evaluate = (fn, arg) => withDeadline(main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`), `Renderer evaluation: ${String(fn).slice(0, 180)}`);
const capturePage = () => withDeadline(main.webContents.capturePage(), 'Capture live App compositor');
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
async function trustedClick(selector) {
  const point = await evaluate(target => {
    const node = document.querySelector(target);
    if (!node) throw new Error(`Missing ${target}`);
    node.scrollIntoView({ block: 'center', behavior: 'instant' });
    const rect = node.getBoundingClientRect();
    return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
  }, selector);
  main.webContents.sendInputEvent({ type: 'mouseDown', ...point, button: 'left', clickCount: 1 });
  main.webContents.sendInputEvent({ type: 'mouseUp', ...point, button: 'left', clickCount: 1 });
}
async function trustedType(selector, value) {
  await evaluate(target => {
    const node = document.querySelector(target);
    if (!node) throw new Error(`Missing ${target}`);
    node.focus(); node.select();
  }, selector);
  if (value) await withDeadline(main.webContents.insertText(value), `Native text entry: ${selector}`);
  else {
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Backspace' });
    main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Backspace' });
  }
}
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
  fs.writeFileSync(path.join(output, name), (await capturePage()).toPNG());
  screenshots.push(name);
}
function pass(name, evidence) { checks.push({ name, passed: true, ...evidence }); console.log(`PASS ${name}`); }
function recordFrames(name) {
  if (process.env.WUU_GENUI_RECORD !== '1') return () => {};
  const directory = path.join(output, 'recordings', name);
  fs.mkdirSync(directory, { recursive: true });
  const recording = { name, directory: `recordings/${name}`, source: 'Electron live compositor frames', frames: [] };
  recordings.push(recording);
  const started = performance.now();
  let previous = -Infinity;
  main.webContents.beginFrameSubscription(false, image => {
    const elapsed = performance.now() - started;
    if (elapsed - previous < 100) return;
    previous = elapsed;
    const filename = `${String(recording.frames.length).padStart(5, '0')}.png`;
    fs.writeFileSync(path.join(directory, filename), image.toPNG());
    recording.frames.push({ filename, elapsedMs: elapsed });
  });
  return () => main.webContents.endFrameSubscription();
}
async function beginTurn(text) {
  await wait(() => document.querySelector('.composer textarea') && !document.querySelector('.composer-stop-button'));
  await type('.composer textarea', text);
  await wait(() => { const button = document.querySelector('.composer-send-button'); return button && !button.disabled && button.getAttribute('aria-busy') !== 'true'; });
  const before = rpc.length;
  await click('.composer-send-button');
  await until(() => rpc.slice(before).some(entry => entry.channel === 'wuu:turn-start'), 'real turn-start IPC after composer submission');
}
async function submit(text, answer) {
  await beginTurn(text);
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
const timeout = setTimeout(() => { void fail(new Error(`Full App E2E exceeded 240000 ms during ${lastOperation}`)); }, 240000);
async function run() {
  await withDeadline(new Promise(resolve => server.listen(0, '127.0.0.1', resolve)), 'Start isolated provider');
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
  await withDeadline(import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href), 'Import production App main');
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
  const stopStreamRecording = recordFrames('streaming-frames');
  await beginTurn('GENUI_ENABLED');
  await until(() => liveStream, 'held-open interleaved provider stream');
  liveStream.first();
  await wait(() => document.querySelector('[data-genui-block="teams"]') && document.querySelector('[data-genui-node="independent"]'));
  assert.equal(liveStream.finished, false, 'Both widgets are rendered while the provider response is still open');
  assert.ok(await evaluate(() => {
    const table = document.querySelector('[data-genui-block="teams"]');
    const other = document.querySelector('[data-genui-node="independent"]');
    window.__genuiStreamRoot = table.closest('[data-wuu-component="generated-ui"]');
    return table.textContent.includes('Engineering') && other.textContent.includes('Independent stream content')
      && !document.querySelector('[data-genui-block="progress"]')
      && [...document.querySelectorAll('[data-wuu-component="generated-ui"] input, [data-wuu-component="generated-ui"] button, [data-wuu-component="generated-ui"] select')].every(node => node.matches(':disabled'));
  }), 'Complete atomic prefixes have isolated contents and disabled controls');
  streamEvidence.push({ stage: 'first-prefix', providerFinished: liveStream.finished, at: new Date().toISOString() });
  await capture('02a-streaming-first-prefix.png', '[data-genui-block="teams"]');
  liveStream.second();
  await wait(() => document.querySelector('[data-genui-block="progress"]'));
  assert.equal(liveStream.finished, false);
  assert.ok(await evaluate(() => document.querySelector('[data-genui-block="teams"]').closest('[data-wuu-component="generated-ui"]') === window.__genuiStreamRoot), 'Appending a validated unit preserves widget identity');
  assert.equal(rpc.filter(entry => entry.channel === 'wuu:plugin-storage-set').length, 0, 'Input previews never persist speculative control state');
  streamEvidence.push({ stage: 'second-prefix', providerFinished: liveStream.finished, at: new Date().toISOString() });
  await capture('02b-streaming-second-prefix.png', '[data-genui-block="progress"]');
  liveStream.finish();
  await wait(() => [...document.querySelectorAll('.session-flow')].some(node => node.textContent.includes('The interactive overview is ready.')));
  await wait(() => !document.querySelector('.composer-stop-button'));
  await wait(() => document.querySelector('[data-wuu-component="generated-ui"]')?.getAttribute('aria-busy') === 'false');
  assert.equal(await evaluate(() => document.querySelectorAll('[data-wuu-component="generated-ui"]').length), 2);
  assert.ok(await evaluate(() => document.querySelector('.session-flow [data-wuu-component="generated-ui"]')));
  assert.ok(await evaluate(() => {
    const bars = [...document.querySelectorAll('.plugin-genui-chart rect')];
    return bars.length === 2 && [...document.querySelectorAll('.plugin-genui-x-categories span')].length === bars.length
      && [...document.querySelectorAll('.plugin-genui-x-categories span')].every((node, index) => {
        const label = node.getBoundingClientRect(), bar = bars[index].getBoundingClientRect();
        return Math.abs((label.left + label.right - bar.left - bar.right) / 2) < 1;
      });
  }), 'Production category labels align with their bar centers');
  pass('real model tool call reaches Go helper, persisted message flow, MIME registry and production renderer');
  pass('two interleaved model argument streams render atomic prefixes before tool completion', { streamEvidence });
  await capture('03-app-interactive.png', '.plugin-genui-header');
  stopStreamRecording();
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
  main.reload(); await withDeadline(loaded, 'Reload production App history');
  await wait(() => document.querySelector('[data-genui-block="draft"] input')?.value === 'Persisted via production storage');
  assert.equal(await evaluate(() => document.querySelector('[data-genui-block="teams"] input').value), 'Engineering');
  assert.ok(rpc.slice(beforeReload).some(entry => entry.channel === 'wuu:plugin-storage-get' && entry.completed && entry.result?.value?.includes('Persisted via production storage')));
  assert.ok(rpc.slice(beforeReload).some(entry => entry.channel === 'wuu:thread-resume' && entry.completed && JSON.stringify(entry.result).includes('application/vnd.wuu.ui+json')), 'Historical resource came from the real app-server thread resume');
  await capture('05-app-reloaded.png', '[data-genui-block="draft"]');
  pass('full renderer reload restores native history and production plugin storage');
  await beginTurn('GENUI_CANCEL');
  await wait(() => document.querySelector('[data-genui-node="canceled"]'));
  const writesBeforeCancel = rpc.filter(entry => entry.channel === 'wuu:plugin-storage-set').length;
  await capture('05a-before-stream-cancel.png', '[data-genui-node="canceled"]');
  await click('.composer-stop-button');
  await wait(() => !document.querySelector('.composer-stop-button'));
  await until(() => canceledStream?.closed, 'provider response closed after stop');
  await wait(() => {
    const preview = document.querySelector('[data-genui-node="canceled"]')?.closest('[data-wuu-component="generated-ui"]');
    return !preview || (preview.dataset.genuiPreview === 'true' && preview.getAttribute('aria-busy') === 'false'
      && [...preview.querySelectorAll('input,button,select,textarea')].every(node => node.matches(':disabled')));
  }, undefined, 'stopped UI is removed or retained as a settled read-only preview');
  assert.equal(rpc.filter(entry => entry.channel === 'wuu:plugin-storage-set').length, writesBeforeCancel);
  const cancelThreadId = rpc.filter(entry => entry.channel === 'wuu:turn-start').at(-1).args[0];
  const stoppedHistory = await until(async () => {
    const history = await evaluate(id => window.wuu.resumeThread(id), cancelThreadId);
    return history.thread.turns.at(-1).status === 'in_progress' ? undefined : history;
  }, 'durable interrupted turn after provider closure');
  const stoppedTurn = stoppedHistory.thread.turns.at(-1);
  assert.equal(stoppedTurn.status, 'interrupted');
  assert.ok(stoppedTurn.items.every(item => !(item.result_detail?.content || []).some(part => part.type === 'resource')),
    'Reloaded interrupted history contains no published speculative UI resource');
  pass('stopping an incomplete UI closes inference without persisting or enabling speculative output', { interruptedTurn: stoppedTurn.id });
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
  await submit('GENUI_ACTION', 'The follow-up form is ready.');
  const sendAction = '[data-genui-node="send"] button';
  await wait(selector => document.querySelector(selector) && !document.querySelector(selector).matches(':disabled'), sendAction);
  const actionOrigin = rpc.filter(entry => entry.channel === 'wuu:turn-start').at(-1).args[0];
  const actionHistory = await evaluate(id => window.wuu.resumeThread(id), actionOrigin);
  const sourceTurn = actionHistory.thread.turns.at(-1);
  const sourceItem = sourceTurn.items.find(item => item.result_detail?.content?.some(part => part.type === 'resource' && part.name === actionSpec.title));
  const sourcePart = sourceItem?.result_detail.content.find(part => part.type === 'resource' && part.name === actionSpec.title);
  assert.equal(sourceTurn.status, 'completed');
  assert.ok(sourceItem && sourcePart?.artifact?.ref, 'The action binds an actual completed native history resource');
  actionSource = { threadId: actionOrigin, turnId: sourceTurn.id, itemId: sourceItem.id, artifactId: sourcePart.artifact.ref, title: actionSpec.title };
  const beforeAction = rpc.filter(entry => entry.channel === 'wuu:turn-start').length;
  const stopActionRecording = recordFrames('continuation-frames');
  await trustedType('input[data-genui-node="ownerInput"]', 'Engineering follow-up');
  await type('.composer textarea', 'Unsent composer draft');
  await evaluate(() => {
    const picker = document.querySelector('.composer-file-input[type="file"]:not([capture])');
    if (!picker) throw new Error('Missing composer attachment picker');
    const transfer = new DataTransfer();
    transfer.items.add(new File(['Keep this attachment in my draft.'], 'draft-only.txt', { type: 'text/plain' }));
    picker.files = transfer.files;
    picker.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await wait(() => [...document.querySelectorAll('.composer-shell .composer-attachment-tray-item:not([data-exiting]) .composer-document-card')].some(node => node.textContent.includes('draft-only.txt')));
  // A script or restored effect cannot forge a real button gesture.
  await click(sendAction);
  await settle();
  assert.equal(rpc.filter(entry => entry.channel === 'wuu:turn-start').length, beforeAction);
  await trustedClick(sendAction);
  await until(() => followupResponse, 'explicit artifact action reaches the ordinary provider pipeline');
  await trustedClick(sendAction);
  const afterAction = rpc.filter(entry => entry.channel === 'wuu:turn-start');
  assert.equal(afterAction.length, beforeAction + 1, 'Repeated click while pending must not start or queue a second turn');
  assert.equal(afterAction.at(-1).args[0], actionOrigin, 'The host binds the originating thread');
  assert.ok(JSON.stringify(afterAction.at(-1).args).includes('Engineering follow-up'));
  assert.ok(!JSON.stringify(afterAction.at(-1).args).includes('draft-only.txt'), 'Content actions do not consume draft attachments');
  assert.ok(await evaluate(selector => document.querySelector(selector).matches(':disabled'), sendAction));
  assert.equal(await evaluate(() => document.querySelector('.composer textarea').value), 'Unsent composer draft');
  assert.ok(await evaluate(() => [...document.querySelectorAll('.composer-shell .composer-attachment-tray-item:not([data-exiting]) .composer-document-card')].some(node => node.textContent.includes('draft-only.txt'))));
  await capture('08-app-action-running.png', '[data-genui-node="send"]');
  followupResponse();
  await wait(() => [...document.querySelectorAll('.session-flow')].some(node => node.textContent.includes('Follow-up accepted for Engineering.')));
  await wait(() => !document.querySelector('.composer-stop-button'));
  assert.equal(await evaluate(() => document.querySelector('input[data-genui-node="ownerInput"]').value), 'Engineering follow-up');
  stopActionRecording();
  pass('explicit trusted UI action starts one origin-bound normal turn with values and retains local form, composer draft and attachment');
  const beforeActionReload = rpc.filter(entry => entry.channel === 'wuu:turn-start').length;
  const actionReloaded = new Promise(resolve => main.webContents.once('did-finish-load', resolve));
  main.reload(); await withDeadline(actionReloaded, 'Reload action history');
  await wait(() => document.querySelector('input[data-genui-node="ownerInput"]')?.value === 'Engineering follow-up');
  assert.equal(rpc.filter(entry => entry.channel === 'wuu:turn-start').length, beforeActionReload, 'Restoring action history must not replay submission');
  await capture('09-app-action-history.png', '[data-genui-node="send"]');
  pass('restored interactive action values do not replay agent requests');
  const form = '[data-genui-node="ownerForm"]';
  const formButton = `${form} button[type="submit"]`;
  const beforeForm = rpc.filter(entry => entry.channel === 'wuu:turn-start').length;
  await trustedType('input[data-genui-node="formOwner"]', '');
  await wait(() => document.querySelector('input[data-genui-node="formOwner"]').value === '');
  await trustedClick(formButton);
  await settle();
  assert.equal(rpc.filter(entry => entry.channel === 'wuu:turn-start').length, beforeForm, 'Invalid native forms cannot start a turn');
  await trustedType('input[data-genui-node="formOwner"]', 'Engineering follow-up');
  await wait(() => document.querySelector('input[data-genui-node="formOwner"]').validity.valid
    && document.querySelector('input[data-genui-node="formOwner"]').value === 'Engineering follow-up');
  await evaluate(target => document.querySelector(target).requestSubmit(), form);
  await settle();
  assert.equal(rpc.filter(entry => entry.channel === 'wuu:turn-start').length, beforeForm, 'requestSubmit without a native gesture cannot start a turn');
  await wait(selector => !document.querySelector(selector).matches(':disabled'), formButton);
  await evaluate(target => document.querySelector(target).addEventListener('click', event => event.preventDefault(), { once: true }), formButton);
  await trustedClick(formButton);
  await settle();
  assert.equal(rpc.filter(entry => entry.channel === 'wuu:turn-start').length, beforeForm, 'Canceling the submit-button default action cannot send');
  for (const method of ['click', 'enter']) {
    followupResponse = undefined;
    const before = rpc.filter(entry => entry.channel === 'wuu:turn-start').length;
    if (method === 'click') await trustedClick(formButton);
    else {
      await evaluate(() => document.querySelector('input[data-genui-node="formOwner"]').focus());
      main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Enter' });
      main.webContents.sendInputEvent({ type: 'char', keyCode: '\r' });
      main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Enter' });
    }
    await until(() => followupResponse, `native form ${method} reaches inference`);
    assert.equal(rpc.filter(entry => entry.channel === 'wuu:turn-start').length, before + 1);
    assert.equal(rpc.filter(entry => entry.channel === 'wuu:turn-start').at(-1).args[0], actionOrigin);
    followupResponse();
    await wait(() => !document.querySelector('.composer-stop-button'));
    await wait(selector => !document.querySelector(selector).matches(':disabled'), formButton);
  }
  await capture('10-app-native-form.png', form);
  pass('native form click and Enter send once while invalid, canceled and programmatic submissions remain inert');
  const evidence = { recordedAt: new Date().toISOString(), scope: 'Real App, main, preload, app-server, Go helper, native history and plugin storage; loopback synthetic HTTP provider only.',
    hashes: { harness: hash(__filename), core: hash(process.env.WUU_DESKTOP_CORE), helper: hash(process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER), main: hash(path.join(desktop, 'out/main/index.js')), preload: hash(path.join(desktop, 'out/preload/index.cjs')) },
    checks, screenshots, recordings, processes, storage: stateDocuments(), requests, rpc, errors };
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(evidence, null, 2));
  console.log(`Full App generative UI E2E passed: ${output}`);
}
let failing = false;
async function fail(error) {
  if (failing) return;
  failing = true;
  clearTimeout(timeout);
  console.error(error);
  const failedOperation = lastOperation;
  // Even diagnostics must terminate when Chromium's compositor or renderer is
  // unresponsive. Write the primary evidence before asking it for anything.
  const forceExit = setTimeout(() => app.exit(1), 30000);
  try {
    const binary = filename => {
      try { return { path: filename, hash: hash(filename), mode: fs.statSync(filename).mode }; }
      catch (inspectionError) { return { path: filename, error: String(inspectionError) }; }
    };
    fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: String(error.stack || error), failedOperation, fixture, checks, requests, rpc, errors, processes, recordings, binaries: { core: binary(process.env.WUU_DESKTOP_CORE), helper: binary(process.env.WUU_GENERATIVE_UI_PLUGIN_HELPER) } }, null, 2));
    if (main && !main.isDestroyed()) {
      try { fs.writeFileSync(path.join(output, 'failure.png'), (await capturePage()).toPNG()); }
      catch (captureError) { fs.writeFileSync(path.join(output, 'failure-capture.txt'), String(captureError.stack || captureError)); }
      try {
        const state = await withDeadline(main.webContents.executeJavaScript(`({ text: document.body.innerText, focused: document.hasFocus(), activeElement: document.activeElement?.outerHTML, inputs: [...document.querySelectorAll('[data-wuu-component="generated-ui"] input')].map(input => ({ node: input.dataset.genuiNode, value: input.value, valid: input.validity.valid, disabled: input.matches(':disabled') })) })`), 'Read failed App DOM', 5000);
        fs.writeFileSync(path.join(output, 'failure-state.json'), JSON.stringify(state, null, 2));
      } catch (stateError) { fs.writeFileSync(path.join(output, 'failure-state.json'), JSON.stringify({ error: String(stateError) })); }
    }
  } finally {
    clearTimeout(forceExit);
    server.close();
    app.exit(1);
  }
}
run().then(() => { clearTimeout(timeout); server.close(); app.quit(); }).catch(fail);
