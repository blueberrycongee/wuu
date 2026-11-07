// Build desktop first: npm --prefix desktop run build:core && npm --prefix desktop run build.
// Run: electron desktop/scripts/shell-skin-canvas-e2e.cjs
// Captures the real App (navigation-ribbon shell) in an isolated profile with the
// frame-placement examples/plugins/shell-skins plus a generated canvas-placement
// fixture skin, for each skin x theme x destination, and asserts the
// single-painter canvas contract through pixel sampling.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, nativeImage } = require('electron');

const desktop = path.resolve(__dirname, '..');
const repository = path.dirname(desktop);
const output = path.resolve(process.env.WUU_SHELL_SKIN_OUTPUT || path.join(repository, 'artifacts/shell-skin-canvas/after'));
const run = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-skin-canvas-'));
const home = path.join(run, 'home');
const workspace = path.join(run, 'workspace');
const core = path.resolve(process.env.WUU_SHELL_SKIN_CORE || path.join(desktop, 'build/bin/wuu-core'));
for (const dir of [output, home, workspace]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(run, 'profile'));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: core,
  WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0',
  WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;

const WIDTH = 1440, HEIGHT = 960;
const REPLY = 'Shell skin canvas fixture reply.';
const report = { status: 'running', output, fixture: run, captures: [], checks: [], errors: [], plugins: {}, limitations: [
  'Hidden window, synthetic openai-compatible model, reduced motion emulated. Conversation turns use the fixture provider.',
] };
let phase = 'setup', win;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const bounded = (promise, label, ms = 15000) => Promise.race([promise, sleep(ms).then(() => { throw new Error(`Timed out: ${label}`); })]);
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await bounded(Promise.resolve().then(check), label, 8000)) return; await sleep(50); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, arg) => bounded(win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`), 'renderer');
const click = async (selector, label = selector) => {
  const found = await evaluate(sel => { const el = document.querySelector(sel); if (el) el.click(); return Boolean(el); }, selector);
  if (!found) throw new Error(`Missing element to click: ${label}`);
};
function save() { fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ ...report, phase }, null, 2)); }
function check(name, condition, detail) {
  report.checks.push({ name, passed: Boolean(condition), detail });
  assert.ok(condition, `${name}: ${detail ?? ''}`);
}

app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created; created.hide();
  created.webContents.setBackgroundThrottling(false);
  created.webContents.on('console-message', event => {
    if (event.level === 'error') report.errors.push({ phase, message: event.message });
  });
});

// Minimal openai-compatible fixture: one assistant reply, fixed title.
const server = http.createServer((req, res) => {
  let raw = ''; req.on('data', part => { raw += part; }); req.on('end', () => {
    if (req.method === 'GET' || req.url.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] })); return;
    }
    const body = JSON.parse(raw || '{}');
    const text = (body.messages || []).filter(m => m.role === 'user')
      .map(m => typeof m.content === 'string' ? m.content : JSON.stringify(m.content || '')).join('\n');
    const content = text.includes('Generate a title') ? 'Canvas fixture conversation' : REPLY;
    if (!body.stream) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content }, finish_reason: 'stop' }] })); return;
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta: { role: 'assistant', content }, finish_reason: null }] })}\n\n`);
    res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`);
    res.end('data: [DONE]\n\n');
  });
});

async function shellReady() {
  await until(() => evaluate(() => Boolean(document.querySelector('[data-ribbon-id]') && document.querySelector('.primary-nav .nav-item'))), 'navigation shell', 60000);
}
async function settle() {
  await evaluate(() => Promise.all(document.getAnimations()
    .filter(a => Number.isFinite(a.effect?.getComputedTiming?.().endTime))
    .map(a => a.finished.catch(() => {}))).then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))));
}
const pixels = new Map();
async function capture(name) {
  await settle();
  const image = await win.webContents.capturePage();
  fs.writeFileSync(path.join(output, `${name}.png`), image.toPNG());
  const scale = image.getSize().width / win.getContentSize()[0];
  const bitmap = image.toBitmap();
  pixels.set(name, { scale, width: image.getSize().width, height: image.getSize().height,
    pixel(x, y) {
      const i = (Math.round(y * scale) * this.width + Math.round(x * scale)) * 4;
      return [bitmap[i], bitmap[i + 1], bitmap[i + 2]];
    },
    // Mean over one 4px cell so dot-screen samples do not depend on phase.
    at(x, y) {
      const sum = [0, 0, 0], step = 1 / scale, count = Math.round(4 * scale) ** 2;
      for (let dy = 0; dy < 4; dy += step) for (let dx = 0; dx < 4; dx += step) {
        const [b, g, r] = this.pixel(x + dx, y + dy); sum[0] += b; sum[1] += g; sum[2] += r;
      }
      return sum.map(v => Math.round(v / count));
    },
    row(x0, y, count) {
      const out = [];
      for (let x = x0; x < x0 + count; x++) out.push(this.pixel(x, y));
      return out;
    } });
  report.captures.push({ file: `${name}.png`, phase, canvas: await evaluate(() => document.querySelector('.conversation-pane')?.dataset.canvas) });
}
const at = (name, x, y) => pixels.get(name).at(x, y);
// Average window color in a 16px square at a client point. Plugin skin images
// taint the artwork canvas, so read the composited page instead.
async function artworkSample(x, y) {
  const image = await win.webContents.capturePage({ x, y, width: 16, height: 16 });
  const bitmap = image.toBitmap(), sum = [0, 0, 0];
  for (let i = 0; i < bitmap.length; i += 4) { sum[0] += bitmap[i + 2]; sum[1] += bitmap[i + 1]; sum[2] += bitmap[i]; }
  return sum.map(v => Math.round(v / (bitmap.length / 4)));
}
// A user picture: horizontal magenta-to-cyan gradient with a bright band.
function fixturePicture() {
  const width = 1600, height = 1000, bitmap = Buffer.alloc(width * height * 4);
  for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) {
    const t = x / (width - 1), band = Math.max(0, 1 - Math.abs(y - height * 0.62) / 90);
    const i = (y * width + x) * 4;
    bitmap[i] = Math.min(255, 170 * (1 - t) + 255 * t * 0.9 + 200 * band); // B
    bitmap[i + 1] = Math.min(255, 200 * t + 160 * band); // G
    bitmap[i + 2] = Math.min(255, 255 * (1 - t) + 200 * band); // R
    bitmap[i + 3] = 255;
  }
  return nativeImage.createFromBitmap(bitmap, { width, height }).toPNG();
}
async function chooseBackgroundFile(file) {
  const send = (method, params) => win.webContents.debugger.sendCommand(method, params);
  const { root } = await send('DOM.getDocument', { depth: 0 });
  const { nodeId } = await send('DOM.querySelector', { nodeId: root.nodeId, selector: '.background-file-picker input[type=file]' });
  assert.ok(nodeId, 'background file input must exist');
  await send('DOM.setFileInputFiles', { nodeId, files: [file] });
}
const backgroundRemove = () => evaluate(() => {
  const button = document.querySelector('.background-file-picker')?.parentElement?.querySelector('button.settings-button');
  if (button) button.click();
  return Boolean(button);
});
const backgroundSaved = () => evaluate(() => Boolean(document.querySelector('.background-file-picker')?.parentElement?.querySelector('button.settings-button'))
  && !document.querySelector('.background-file-picker.disabled'));
const dist = (a, b) => Math.max(...a.map((v, i) => Math.abs(v - b[i])));

const composer = '[data-main-conversation-composer] textarea';
async function sendTurn(text) {
  await until(() => evaluate(selector => { const i = document.querySelector(selector); return Boolean(i && !i.disabled && !i.readOnly && !i.closest('[inert]')); }, composer), 'editable composer', 60000);
  await evaluate(({ selector, text }) => { const i = document.querySelector(selector);
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(i, text);
    i.dispatchEvent(new Event('input', { bubbles: true })); }, { selector: composer, text });
  await until(() => evaluate(() => { const b = document.querySelector('[data-main-conversation-composer] .composer-send-button'); return Boolean(b && !b.disabled); }), 'send enabled');
  await evaluate(selector => document.querySelector(selector).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })), composer);
}
async function goHome() {
  // Leaving a primary plugin view restores the conversations sidebar first.
  if (await evaluate(() => Boolean(document.querySelector('[data-primary-plugin-view]')))) {
    await click('[data-ribbon-id="conversations"]');
    await until(() => evaluate(() => !document.querySelector('[data-primary-plugin-view]') && Boolean(document.querySelector('.primary-nav .nav-item'))), 'conversations section');
  }
  await click('.primary-nav .nav-item');
  await until(() => evaluate(() => Boolean(document.querySelector('.empty-home'))), 'empty home');
}
async function openPluginView(ribbonId) {
  await evaluate(id => [...document.querySelectorAll('[data-ribbon-id]')].find(n => n.dataset.ribbonId === id).click(), ribbonId);
  await until(() => evaluate(() => Boolean(document.querySelector('[data-primary-plugin-view]'))), 'primary plugin view');
}
async function openSettingsAppearance() {
  await goHome();
  await until(() => evaluate(() => Boolean(document.querySelector('[data-settings-entry="settings"]'))), 'settings entry');
  await click('[data-settings-entry="settings"]');
  await until(() => evaluate(() => Boolean(document.querySelector('.app-shell[data-settings-open]'))), 'settings open');
  await until(() => evaluate(() => Boolean(document.querySelector('.settings-nav [data-wuu-page="appearance"]'))), 'appearance nav item');
  await click('.settings-nav [data-wuu-page="appearance"]');
  await until(() => evaluate(() => Boolean(document.querySelector('.settings-page[data-wuu-page="appearance"]'))), 'appearance page');
}
async function closeSettings() {
  await evaluate(() => window.dispatchEvent(new Event('wuu:workbench-back')));
  await until(() => evaluate(() => !document.querySelector('.app-shell[data-settings-open]')), 'settings closed');
}
function writeFixturePlugin() {
  const dir = path.join(run, 'canvas-plugin');
  fs.mkdirSync(dir, { recursive: true });
  // Deterministic high-contrast artwork: vertical color bands over a dark base.
  const bands = ['#e05260', '#e8a33d', '#3fb984', '#2f7fe0', '#8a5ce0']
    .map((color, i) => `<rect x="${i * 240}" y="0" width="240" height="960" fill="${color}"/>`).join('');
  fs.writeFileSync(path.join(dir, 'art.svg'),
    `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="960"><rect width="1200" height="960" fill="#16283a"/>${bands}</svg>`);
  fs.writeFileSync(path.join(dir, 'desktop.js'), `
export function activate(api) {
  const React = api.react;
  const page = (canvas, label) => api.registerViewType({
    id: label.toLowerCase().replace(/\\s+/g, '-'), title: label, canvas,
    render: () => React.createElement(api.ui.Page, { title: label },
      React.createElement(api.ui.Card, null,
        React.createElement('p', { 'data-canvas-fixture': label }, label + ' card on the host canvas'))),
  });
  page(undefined, 'Fixture Opaque');
  page('transparent', 'Fixture Clear');
}
`);
  fs.writeFileSync(path.join(dir, 'plugin.json'), JSON.stringify({
    schemaVersion: 1, id: 'canvas-skin-fixture', name: 'Canvas Skin Fixture', version: '1.0.0',
    desktop: { entry: 'desktop.js' },
    contributes: {
      skins: [{
        id: 'deep-field', name: 'Deep Field', placement: 'canvas',
        light: { background: '#2a4d68', foreground: '#f2f8fb', image: 'art.svg', veil: 0.8, dots: { pitch: 4, size: 2.6 } },
        dark: { background: '#12293c', foreground: '#e2f0f5', image: 'art.svg', overlay: '#04101c66', veil: 0.8, dots: { pitch: 4, size: 2.6 } },
      }],
      navigation: [
        { id: 'opaque-nav', view: 'fixture-opaque', title: 'Fixture Opaque', icon: 'pulse', order: 80 },
        { id: 'clear-nav', view: 'fixture-clear', title: 'Fixture Clear', icon: 'pulse', order: 81 },
      ],
    },
  }));
  return dir;
}

async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', agent: { permission_mode: 'unconfined' },
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } },
    engines: { default_engine: 'wuu' }, skills: { enabled: false }, ptc: { enabled: false } }));
  const stamp = new Date().toISOString();
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Fixture', path: workspace, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'fixture', cwd: workspace } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));

  phase = 'install-plugins';
  for (const source of [path.join(repository, 'examples/plugins/shell-skins'), writeFixturePlugin(), path.join(repository, 'examples/plugins/deep-field')]) {
    const installed = spawnSync(core, ['plugin', 'install', '--json', source], { env: process.env, encoding: 'utf8', timeout: 30000 });
    fs.appendFileSync(path.join(output, 'plugin-install.log'), `${source}\n${installed.stdout}\n${installed.stderr}\n`);
    assert.equal(installed.status, 0, `plugin install must succeed: ${source}`);
  }

  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.isDestroyed(), 'Wuu window');
  win.setContentSize(WIDTH, HEIGHT);
  win.webContents.debugger.attach('1.3');
  await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
  await shellReady();

  const inventory = (await evaluate(() => window.wuu.initialize())).extension_inventory;
  fs.writeFileSync(path.join(output, 'inventory.json'), JSON.stringify(inventory.map(i => ({ id: i.id, kind: i.kind, state: i.state, plugin_id: i.provenance?.plugin_id, skins: (i.contributions?.skins || []).map(s => ({ id: s.id, placement: s.placement })) })), null, 2));
  const skinKey = pluginId => {
    const record = inventory.find(i => i.provenance?.plugin_id === pluginId);
    assert.ok(record, `${pluginId} must appear in inventory`);
    report.plugins[pluginId] = { id: record.id, state: record.state, fingerprint: record.fingerprint };
    return skin => `${record.provenance.plugin_id || record.id}:${skin}`;
  };
  const frame = skinKey('shell-skins-example');
  const canvas = skinKey('canvas-skin-fixture');
  const deepField = skinKey('deep-field');

  const skins = [
    { name: 'default', key: 'default' },
    { name: 'tide', key: frame('tide') },
    { name: 'deepfield', key: canvas('deep-field'), placement: 'canvas' },
  ];
  const destinations = {
    home: async name => { await goHome(); await capture(name); },
    conversation: async name => {
      await sendTurn(`Say hello for ${name}`);
      await until(() => evaluate(reply => document.body.innerText.includes(reply), REPLY), 'fixture reply', 60000);
      await capture(name);
    },
    automation: async name => { await openPluginView(ribbons.automation); await capture(name); },
    'fixture-opaque': async name => { await openPluginView(ribbons.fixtureOpaque); await capture(name); },
    'fixture-clear': async name => { await openPluginView(ribbons.fixtureClear); await capture(name); },
    'settings-appearance': async name => { await openSettingsAppearance(); await capture(name); await closeSettings(); },
  };
  const skinsDestination = {
    default: ['home', 'conversation', 'automation', 'settings-appearance'],
    tide: ['home', 'conversation', 'automation', 'settings-appearance'],
    deepfield: ['home', 'conversation', 'automation', 'fixture-opaque', 'fixture-clear', 'settings-appearance'],
  };

  const ribbons = {};
  const firstRibbon = await evaluate(() => [...document.querySelectorAll('[data-ribbon-id]')].map(n => n.dataset.ribbonId));
  ribbons.automation = firstRibbon.find(id => id.includes('automation'));
  ribbons.fixtureOpaque = firstRibbon.find(id => id.includes('opaque-nav'));
  ribbons.fixtureClear = firstRibbon.find(id => id.includes('clear-nav'));
  assert.ok(ribbons.automation, 'bundled automation primary view must exist');
  report.ribbonIds = firstRibbon;

  for (const skin of skins) {
    phase = `skin-${skin.name}`;
    await evaluate(key => window.localStorage.setItem('wuu.shell-skin', key), skin.key);
    win.webContents.reload();
    await shellReady();
    if (skin.placement === 'canvas') {
      await until(() => evaluate(() => document.documentElement.dataset.shellPlacement === 'canvas'), 'canvas placement applied', 30000);
      const afterRibbon = await evaluate(() => [...document.querySelectorAll('[data-ribbon-id]')].map(n => n.dataset.ribbonId));
      ribbons.fixtureOpaque = afterRibbon.find(id => id.includes('opaque-nav')) || ribbons.fixtureOpaque;
      ribbons.fixtureClear = afterRibbon.find(id => id.includes('clear-nav')) || ribbons.fixtureClear;
    } else if (skin.name !== 'default') {
      await until(() => evaluate(() => document.documentElement.hasAttribute('data-shell-skin')), 'skin applied', 30000);
    }
    if (skin.placement === 'canvas') {
      assert.ok(ribbons.fixtureOpaque && ribbons.fixtureClear, 'fixture navigation entries must appear in the ribbon');
    }
    for (const theme of ['light', 'dark']) {
      phase = `${skin.name}-${theme}`;
      await evaluate(t => window.wuu.setThemePreference(t), theme);
      await until(() => evaluate(t => document.documentElement.dataset.theme === t, theme), 'theme applied');
      for (const destination of skinsDestination[skin.name]) {
        phase = `${skin.name}-${theme}-${destination}`;
        await destinations[destination](`${skin.name}-${theme}-${destination}`);
      }
    }
    // Size/type variants for the canvas skin home.
    if (skin.placement === 'canvas') {
      phase = `${skin.name}-variants`;
      await evaluate(t => window.wuu.setThemePreference(t), 'dark');
      await until(() => evaluate(() => document.documentElement.dataset.theme === 'dark'), 'dark theme');
      await goHome();
      win.setContentSize(900, HEIGHT);
      await capture('deepfield-dark-home-w900');
      win.setContentSize(WIDTH, HEIGHT);
      await evaluate(() => document.documentElement.style.setProperty('--wuu-font-size-ui', '20px'));
      await capture('deepfield-dark-home-font20');
      await evaluate(() => document.documentElement.style.removeProperty('--wuu-font-size-ui'));
    }
  }

  phase = 'reduced-transparency';
  await evaluate(key => window.localStorage.setItem('wuu.shell-skin', key), canvas('deep-field'));
  win.webContents.reload();
  await shellReady();
  await until(() => evaluate(() => document.documentElement.dataset.shellPlacement === 'canvas'), 'canvas placement', 30000);
  await evaluate(t => window.wuu.setThemePreference(t), 'dark');
  await until(() => evaluate(() => document.documentElement.dataset.theme === 'dark'), 'dark theme');
  await goHome();
  await capture('deepfield-dark-reduced-transparency-off');
  await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [
    { name: 'prefers-reduced-motion', value: 'reduce' },
    { name: 'prefers-reduced-transparency', value: 'reduce' },
  ] });
  await settle();
  await capture('deepfield-dark-reduced-transparency');
  await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [
    { name: 'prefers-reduced-motion', value: 'reduce' },
    { name: 'prefers-reduced-transparency', value: '' },
  ] });

  phase = 'restore-default';
  await evaluate(() => window.localStorage.setItem('wuu.shell-skin', 'default'));
  win.webContents.reload();
  await shellReady();
  await evaluate(t => window.wuu.setThemePreference(t), 'dark');
  await until(() => evaluate(() => document.documentElement.dataset.theme === 'dark'), 'dark theme');
  await goHome();
  await capture('default-dark-home-restored');

  // Deep Field example: a declarative canvas skin. It styles the frame and
  // the background only; the welcome and content colors stay native.
  phase = 'deep-field-example';
  const deepFieldDir = path.join(path.dirname(output), 'deep-field');
  fs.mkdirSync(deepFieldDir, { recursive: true });
  const captureDF = async name => {
    win.webContents.focus();
    await capture(name);
    fs.renameSync(path.join(output, `${name}.png`), path.join(deepFieldDir, `${name}.png`));
    report.captures[report.captures.length - 1].file = `../deep-field/${name}.png`;
  };
  const selectSkin = key => evaluate(k => {
    window.localStorage.setItem('wuu.shell-skin', k);
    window.dispatchEvent(new StorageEvent('storage', { key: 'wuu.shell-skin' }));
  }, key);
  report.deepField = {};
  await selectSkin(deepField('deep-field'));
  await until(() => evaluate(() => document.documentElement.dataset.shellPlacement === 'canvas'), 'deep-field canvas placement', 30000);
  await goHome();
  report.deepField.nativeWelcome = await evaluate(() => Boolean(document.querySelector('.empty-home')));
  for (const theme of ['light', 'dark']) {
    await evaluate(t => window.wuu.setThemePreference(t), theme);
    await until(() => evaluate(t => document.documentElement.dataset.theme === t, theme), 'deep-field theme applied');
    await goHome();
    await captureDF(`deep-field-example-${theme}-home`);
    // A composer menu over the artwork uses the glass tier.
    if (await evaluate(() => { const b = document.querySelector('[data-main-conversation-composer] button[aria-haspopup="menu"]'); b?.click(); return Boolean(b); })) {
      await sleep(400);
      await captureDF(`deep-field-example-${theme}-menu`);
      await evaluate(() => document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })));
      await evaluate(() => document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true })));
      await sleep(300);
    }
    await sendTurn(`Say hello for deep-field ${theme}`);
    await until(() => evaluate(reply => document.body.innerText.includes(reply), REPLY), 'fixture reply', 60000);
    await captureDF(`deep-field-example-${theme}-conversation`);
    // The right panel shares the sidebar's frosted tier.
    await click('[data-wuu-component="right-sidebar-toggle"]', 'right panel toggle');
    await until(() => evaluate(() => Boolean(document.querySelector('.workspace-right-panel'))), 'right panel open');
    await sleep(600);
    await captureDF(`deep-field-example-${theme}-right-panel`);
    report.deepField[`rightPanel-${theme}`] = await evaluate(() => {
      const box = document.querySelector('.workspace-right-panel').getBoundingClientRect();
      return { x: Math.round(box.left + box.width / 2), y: Math.round(box.bottom - 40) };
    });
    await click('[data-wuu-component="right-sidebar-toggle"]', 'right panel toggle');
    await until(() => evaluate(() => document.querySelector('[data-wuu-component="right-sidebar-toggle"]')?.getAttribute('aria-pressed') !== 'true'), 'right panel closed');
    await openPluginView(ribbons.automation);
    await captureDF(`deep-field-example-${theme}-automation`);
  }
  await goHome();
  win.setContentSize(900, HEIGHT);
  await captureDF('deep-field-example-dark-home-w900');
  win.setContentSize(WIDTH, HEIGHT);
  await evaluate(() => document.documentElement.style.setProperty('--wuu-font-size-ui', '20px'));
  await captureDF('deep-field-example-dark-home-font20');
  await evaluate(() => document.documentElement.style.removeProperty('--wuu-font-size-ui'));

  // Appearance offers the picture only under skins that can show it, and
  // describes where it goes.
  const pictureRow = () => evaluate(() => {
    const picker = document.querySelector('.background-file-picker');
    return picker ? picker.closest('.settings-row').querySelector('.settings-row-label-description')?.textContent ?? '' : null;
  });
  const rows = report.deepField.pictureRow = {};
  await openSettingsAppearance();
  rows.order = await evaluate(() => [...document.querySelectorAll('.settings-page[data-wuu-page="appearance"] .settings-row')].map(row =>
    row.querySelector('.shell-skin-options') ? 'skin' : row.querySelector('.background-file-picker') ? 'picture' : row.querySelector('.settings-palette-trigger') ? 'palette' : null).filter(Boolean));
  rows.deepField = await pictureRow();
  await captureDF('deep-field-example-dark-settings');
  await selectSkin(frame('tide'));
  await until(async () => (await pictureRow()) === null, 'picture row hidden under tide');
  rows.tide = await pictureRow();
  await capture('tide-dark-settings-appearance-picture');
  await selectSkin('default');
  await until(async () => typeof (await pictureRow()) === 'string', 'picture row under default');
  rows.default = await pictureRow();
  await selectSkin(deepField('deep-field'));
  await until(async () => typeof (await pictureRow()) === 'string', 'picture row under deep-field');
  await closeSettings();

  // Your own picture through the real Appearance file input: Deep Field
  // paints it as cells; Remove returns to the bundled artwork.
  phase = 'deep-field-user-image';
  const picture = path.join(run, 'picture.png');
  fs.writeFileSync(picture, fixturePicture());
  const userImage = report.deepField.userImage = {};
  const paneFreeSample = { x: 1350, y: 860 };
  await goHome();
  await settle();
  userImage.bundled = await artworkSample(paneFreeSample.x, paneFreeSample.y);
  await openSettingsAppearance();
  await chooseBackgroundFile(picture);
  await until(backgroundSaved, 'picture saved', 30000);
  userImage.selectedAfterImport = await evaluate(() => window.localStorage.getItem('wuu.shell-skin'));
  await closeSettings();
  await goHome();
  await until(async () => {
    const sample = await artworkSample(paneFreeSample.x, paneFreeSample.y);
    return sample && sample[2] > sample[0] + 30;
  }, 'picture painted in the canvas', 30000);
  userImage.picture = await artworkSample(paneFreeSample.x, paneFreeSample.y);
  await captureDF('deep-field-example-dark-user-image');
  await evaluate(t => window.wuu.setThemePreference(t), 'light');
  await until(() => evaluate(() => document.documentElement.dataset.theme === 'light'), 'light theme');
  await sleep(1000);
  await captureDF('deep-field-example-light-user-image');
  await evaluate(t => window.wuu.setThemePreference(t), 'dark');
  await until(() => evaluate(() => document.documentElement.dataset.theme === 'dark'), 'dark theme');
  // A fixed-artwork skin keeps its artwork and the saved picture.
  await selectSkin(frame('tide'));
  await until(() => evaluate(() => !document.documentElement.dataset.shellPlacement || document.documentElement.dataset.shellPlacement === 'frame'), 'tide frame');
  await goHome();
  await sleep(500);
  await capture('tide-dark-home-with-picture');
  await selectSkin(deepField('deep-field'));
  await until(async () => {
    const sample = await artworkSample(paneFreeSample.x, paneFreeSample.y);
    return sample && dist(sample, userImage.picture) < 12;
  }, 'picture returns with deep-field', 30000);
  await openSettingsAppearance();
  assert.ok(await backgroundRemove(), 'background remove button');
  await until(async () => !(await evaluate(() => Boolean(document.querySelector('.background-file-picker')?.parentElement?.querySelector('button.settings-button')))), 'picture removed');
  await closeSettings();
  await goHome();
  await until(async () => {
    const sample = await artworkSample(paneFreeSample.x, paneFreeSample.y);
    return sample && dist(sample, userImage.bundled) < 12;
  }, 'bundled artwork restored', 30000);
  userImage.restored = await artworkSample(paneFreeSample.x, paneFreeSample.y);
  // Disable through the real CLI path: the default frame returns.
  const disabled = spawnSync(core, ['plugin', 'disable', 'deep-field'], { env: process.env, encoding: 'utf8', timeout: 30000 });
  fs.appendFileSync(path.join(output, 'plugin-install.log'), `disable deep-field\n${disabled.stdout}\n${disabled.stderr}\n`);
  assert.equal(disabled.status, 0, 'plugin disable must succeed');
  // The CLI writes the extension state; give the app's config watcher a moment
  // to publish the external change before reloading the renderer.
  await new Promise(resolve => setTimeout(resolve, 3000));
  win.webContents.reload();
  await shellReady();
  await goHome();
  report.deepField.afterDisable = await evaluate(() => ({
    placement: document.documentElement.dataset.shellPlacement ?? null,
    emptyHome: Boolean(document.querySelector('.empty-home')),
  }));

  phase = 'checks';
  const pane = { x: 720, y: 500 };
  // Far edge of the pane: artwork shows there in every transparent state and
  // no card, bubble or composer reaches it.
  const paneFree = { x: 1350, y: 860 };
  const sidebar = { x: 180, y: 600 };
  const ribbon = { x: 26, y: 300 };
  // The reduced-transparency capture is the opaque paper reference.
  const paper = at('deepfield-dark-reduced-transparency', paneFree.x, paneFree.y);

  // Canvas skin: sidebar, ribbon and opaque destinations stay on their
  // surfaces; only the pane interior shows artwork.
  check('deepfield home shows artwork', dist(at('deepfield-dark-home', paneFree.x, paneFree.y), paper) > 24,
    `pane ${at('deepfield-dark-home', paneFree.x, paneFree.y)} vs opaque ${paper}`);
  check('opaque destinations veil the artwork more than conversations',
    dist(at('deepfield-dark-automation', paneFree.x, paneFree.y), paper) < dist(at('deepfield-dark-conversation', paneFree.x, paneFree.y), paper),
    `automation ${at('deepfield-dark-automation', paneFree.x, paneFree.y)} conversation ${at('deepfield-dark-conversation', paneFree.x, paneFree.y)} paper ${paper}`);
  check('sidebar is a translucent panel over the same artwork',
    dist(at('deepfield-dark-home', sidebar.x, sidebar.y), at('deepfield-dark-reduced-transparency', sidebar.x, sidebar.y)) > 3,
    `${at('deepfield-dark-home', sidebar.x, sidebar.y)} vs ${at('deepfield-dark-reduced-transparency', sidebar.x, sidebar.y)}`);
  check('default-canvas fixture view stays opaque', dist(at('deepfield-dark-fixture-opaque', pane.x, pane.y), at('deepfield-dark-automation', pane.x, pane.y)) < 4,
    `${at('deepfield-dark-fixture-opaque', pane.x, pane.y)} vs ${at('deepfield-dark-automation', pane.x, pane.y)}`);
  check('transparent fixture view paints like the pane (single painter)',
    dist(at('deepfield-dark-fixture-clear', paneFree.x, paneFree.y), at('deepfield-dark-home', paneFree.x, paneFree.y)) < 6,
    `${at('deepfield-dark-fixture-clear', paneFree.x, paneFree.y)} vs ${at('deepfield-dark-home', paneFree.x, paneFree.y)}`);
  check('conversations veil the artwork that the home exposes',
    dist(at('deepfield-dark-conversation', paneFree.x, paneFree.y), at('deepfield-dark-home', paneFree.x, paneFree.y)) > 8,
    `${at('deepfield-dark-conversation', paneFree.x, paneFree.y)}`);
  check('ribbon stays on the skin frame color',
    dist(at('deepfield-dark-home', ribbon.x, ribbon.y), at('deepfield-dark-automation', ribbon.x, ribbon.y)) < 4,
    `${at('deepfield-dark-home', ribbon.x, ribbon.y)} vs ${at('deepfield-dark-automation', ribbon.x, ribbon.y)}`);

  // Dot screen: neighboring pixels at pitch spacing alternate between the gap
  // color and the artwork inside the transparent pane.
  // One of the rows within a pitch crosses the cells rather than a gap row.
  const alternations = [500, 501, 502, 503].map(y => {
    const row = pixels.get('deepfield-dark-home').row(390, y, 32);
    return row.slice(1).filter((px, i) => dist(px, row[i]) > 4).length;
  });
  check('dot screen alternates at pitch', Math.max(...alternations) >= 8, `alternations per row ${JSON.stringify(alternations)}`);

  check('reduced transparency restores opaque surfaces',
    dist(paper, at('default-dark-automation', paneFree.x, paneFree.y)) < 4
      && dist(at('deepfield-dark-reduced-transparency', sidebar.x, sidebar.y), at('default-dark-automation', sidebar.x, sidebar.y)) < 4
      && dist(at('deepfield-dark-reduced-transparency', paneFree.x, paneFree.y), at('deepfield-dark-reduced-transparency-off', paneFree.x, paneFree.y)) > 8,
    `${at('deepfield-dark-reduced-transparency', paneFree.x, paneFree.y)}`);

  // Restoring the default skin returns to baseline pixels.
  check('default skin restores baseline pixels',
    dist(at('default-dark-home-restored', paneFree.x, paneFree.y), at('default-dark-home', paneFree.x, paneFree.y)) < 1
      && dist(at('default-dark-home-restored', sidebar.x, sidebar.y), at('default-dark-home', sidebar.x, sidebar.y)) < 1,
    `${at('default-dark-home-restored', paneFree.x, paneFree.y)} vs ${at('default-dark-home', paneFree.x, paneFree.y)}`);

  // Frame skins keep painting like before: identical pane/sidebar samples
  // between default and tide content regions (artwork stays in the frame).
  check('frame skin keeps content regions identical',
    dist(at('tide-dark-automation', pane.x, pane.y), at('default-dark-automation', pane.x, pane.y)) < 1
      && dist(at('tide-dark-automation', sidebar.x, sidebar.y), at('default-dark-automation', sidebar.x, sidebar.y)) < 1,
    `tide ${at('tide-dark-automation', pane.x, pane.y)} vs default ${at('default-dark-automation', pane.x, pane.y)}`);

  const deepFieldReport = report.deepField, pictureReport = deepFieldReport.userImage, rowReport = deepFieldReport.pictureRow;
  check('deep-field keeps the native welcome', deepFieldReport.nativeWelcome === true, JSON.stringify(deepFieldReport.nativeWelcome));
  check('appearance offers the picture only under skins that show it',
    typeof rowReport.deepField === 'string' && rowReport.deepField.includes('Deep Field')
      && rowReport.tide === null && typeof rowReport.default === 'string' && rowReport.default !== rowReport.deepField,
    JSON.stringify(rowReport));
  check('appearance lists the skin before the palette',
    rowReport.order.join() === 'skin,picture,palette' || rowReport.order.join() === 'skin,picture',
    JSON.stringify(rowReport.order));
  check('deep-field keeps its skin selected when the user picks a picture',
    pictureReport.selectedAfterImport === deepField('deep-field'), JSON.stringify(pictureReport));
  check('deep-field paints the user picture, then restores its artwork on remove',
    pictureReport.picture && dist(pictureReport.picture, pictureReport.bundled) > 40 && dist(pictureReport.restored, pictureReport.bundled) < 12, JSON.stringify(pictureReport));
  check('a fixed-artwork skin keeps its own artwork while a picture is saved',
    dist(at('tide-dark-home-with-picture', ribbon.x, ribbon.y), at('tide-dark-home', ribbon.x, ribbon.y)) < 4,
    `${at('tide-dark-home-with-picture', ribbon.x, ribbon.y)} vs ${at('tide-dark-home', ribbon.x, ribbon.y)}`);
  check('disabling deep-field restores the default frame',
    deepFieldReport.afterDisable?.placement !== 'canvas' && deepFieldReport.afterDisable?.emptyHome === true,
    JSON.stringify(deepFieldReport.afterDisable));

  report.status = 'passed'; save();
  console.log(`PASS shell-skin canvas; evidence ${output}`);
  server.close(); app.quit();
}

const watchdog = setTimeout(() => { report.status = 'failed'; report.errors.push({ phase, message: 'watchdog timeout' }); save(); app.exit(1); }, 480000);
main().catch(async error => {
  report.status = 'failed'; report.errors.push({ phase, message: error.stack || String(error) });
  if (win && !win.isDestroyed()) { try { fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG()); } catch {} }
  save(); console.error(error); server.close(); clearTimeout(watchdog); app.exit(1);
});
app.on('before-quit', () => clearTimeout(watchdog));
