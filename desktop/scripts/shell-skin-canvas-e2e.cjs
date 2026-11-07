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
const { app } = require('electron');

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
    at(x, y) {
      const i = (Math.round(y * scale) * this.width + Math.round(x * scale)) * 4;
      return [bitmap[i], bitmap[i + 1], bitmap[i + 2]];
    },
    row(x0, y, count) {
      const out = [];
      for (let x = x0; x < x0 + count; x++) out.push(this.at(x, y));
      return out;
    } });
  report.captures.push({ file: `${name}.png`, phase, canvas: await evaluate(() => document.querySelector('.conversation-pane')?.dataset.canvas) });
}
const at = (name, x, y) => pixels.get(name).at(x, y);
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
  // The welcome slot is replaceable: accept either the default empty home or
  // a contributed welcome (deep-field renders .deep-field-welcome instead).
  await until(() => evaluate(() => Boolean(document.querySelector('.empty-home, .deep-field-welcome'))), 'empty home');
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
      await until(() => evaluate(() => document.documentElement.dataset.shellPlacement === 'canvas'
        && document.documentElement.hasAttribute('data-shell-dots')), 'canvas placement applied', 30000);
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

  // Deep Field example: canvas skin + paired themes through the persisted
  // extension-theme mechanism, captures in a sibling directory.
  phase = 'deep-field-example';
  const deepFieldDir = path.join(path.dirname(output), 'deep-field');
  fs.mkdirSync(deepFieldDir, { recursive: true });
  const captureDF = async name => {
    win.webContents.focus();
    const image = await win.webContents.capturePage();
    fs.writeFileSync(path.join(deepFieldDir, `${name}.png`), image.toPNG());
    report.captures.push({ name, file: `../deep-field/${name}.png` });
    return image;
  };
  const priorThemeKeys = await evaluate(() => ({
    dark: window.localStorage.getItem('wuu.extension-theme.dark'),
    light: window.localStorage.getItem('wuu.extension-theme.light'),
  }));
  // Seed threads with title shapes that broke layout: a very long CJK title
  // and a long space-free URL. Then verify the native welcome is untouched
  // while the default skin is selected, and that selecting the deep-field
  // skin installs the welcome live (and back).
  await sendTurn('帮我梳理一下这个仓库里所有和外壳皮肤、画布摆放、点阵蒙版、玻璃材质、半透明遮罩、主题令牌、插件贡献、生命周期治理以及渲染器组合模型相关的实现细节和调用链，输出一份带章节目录的完整中文说明文档，并给出每一步对应的源码文件路径和关键符号名');
  await until(() => evaluate(reply => document.body.innerText.includes(reply), REPLY), 'fixture reply', 60000);
  await goHome();
  await sendTurn('https://example.com/some/extremely/long/path/that/has/no/spaces/and/keeps/going/and/going/and/going/and/going/and/going/and/going/and/going/and/going/to/overflow/the/card/edge/definitely/without/fail');
  await until(() => evaluate(reply => document.body.innerText.includes(reply), REPLY), 'fixture reply', 60000);
  await goHome();
  report.deepField = report.deepField || {};
  report.deepField.nativeWelcome = await evaluate(() => ({
    replaced: Boolean(document.querySelector('.deep-field-welcome')),
    emptyHome: Boolean(document.querySelector('.empty-home')),
  }));
  // Select/deselect without reload: the app publishes shell-skin changes
  // through the storage event the plugin subscribes to.
  const selectDeepFieldSkin = key => evaluate(k => {
    window.localStorage.setItem('wuu.shell-skin', k);
    window.dispatchEvent(new StorageEvent('storage', { key: 'wuu.shell-skin' }));
  }, key);
  await evaluate(keys => {
    window.localStorage.setItem('wuu.extension-theme.dark', keys.dark);
    window.localStorage.setItem('wuu.extension-theme.light', keys.light);
  }, { dark: 'deep-field:deep-field', light: 'deep-field:deep-field-light' });
  await selectDeepFieldSkin(deepField('deep-field'));
  await until(() => evaluate(() => Boolean(document.querySelector('.deep-field-welcome'))), 'welcome installs on skin select', 15000);
  report.deepField.liveInstall = true;
  await selectDeepFieldSkin('default');
  await until(() => evaluate(() => !document.querySelector('.deep-field-welcome') && Boolean(document.querySelector('.empty-home'))), 'welcome removes on skin deselect', 15000);
  report.deepField.liveRemove = true;
  await selectDeepFieldSkin(deepField('deep-field'));
  await until(() => evaluate(() => Boolean(document.querySelector('.deep-field-welcome'))), 'welcome reinstalls', 15000);
  await until(() => evaluate(() => document.documentElement.dataset.shellPlacement === 'canvas'), 'deep-field canvas placement', 30000);
  // Each measured layout needs a long-title row on screen; the newest thread
  // is always listed, so send a long-title turn immediately before measuring.
  const seedLongTitle = async tag => {
    await goHome();
    await sendTurn(`${'长'.repeat(30)}${tag} https://example.com/${'segment/'.repeat(24)}end`);
    await until(() => evaluate(reply => document.body.innerText.includes(reply), REPLY), 'fixture reply', 60000);
    await goHome();
  };
  for (const theme of ['light', 'dark']) {
    await evaluate(t => window.wuu.setThemePreference(t), theme);
    await until(() => evaluate(t => document.documentElement.dataset.theme === t, theme), 'deep-field theme applied');
    await seedLongTitle(`${theme} measurement`);
    report.deepField = report.deepField || {};
    report.deepField[`welcome-${theme}`] = await evaluate(() => ({
      welcome: Boolean(document.querySelector('.deep-field-welcome')),
      sessionsCard: Boolean(document.querySelector('.deep-field-card')),
      usageCard: Boolean(document.querySelector('.empty-home-overview')),
      rows: document.querySelectorAll('.deep-field-session').length,
      untitledRows: [...document.querySelectorAll('.deep-field-session-title')]
        .filter(n => /Untitled|未命名/.test(n.textContent)).length,
    }));
    report.deepField[`layout-${theme}`] = await evaluate(() => {
      const card = document.querySelector('.deep-field-card');
      if (!card) return { card: false, rows: 0 };
      const contentRight = card.getBoundingClientRect().right - (parseFloat(getComputedStyle(card).paddingRight) || 0);
      return {
        card: true,
        contentRight,
        rows: [...document.querySelectorAll('.deep-field-session')].map(row => {
          const title = row.querySelector('.deep-field-session-title');
          const time = row.querySelector('.deep-field-session-time');
          return {
            rowRight: row.getBoundingClientRect().right,
            timeRight: time.getBoundingClientRect().right,
            ellipsized: title.scrollWidth > title.clientWidth,
            text: title.textContent.slice(0, 60),
            scroll: title.scrollWidth,
          };
        }),
      };
    });
    await captureDF(`deep-field-example-${theme}-home`);
    await sendTurn(`Say hello for deep-field ${theme}`);
    await until(() => evaluate(reply => document.body.innerText.includes(reply), REPLY), 'fixture reply', 60000);
    await captureDF(`deep-field-example-${theme}-conversation`);
    await openPluginView(ribbons.automation);
    await captureDF(`deep-field-example-${theme}-automation`);
  }
  // Session row click activates the thread.
  await evaluate(t => window.wuu.setThemePreference(t), 'dark');
  await goHome();
  const clicked = await evaluate(() => {
    const row = document.querySelector('.deep-field-session[data-deep-field-thread]');
    if (!row) return null;
    row.click();
    return row.dataset.deepFieldThread;
  });
  assert.ok(clicked, 'deep-field welcome must list a recent session row');
  await until(() => evaluate(() => !document.querySelector('.deep-field-welcome')), 'thread activated from session row', 15000);
  report.deepField.clickedThread = clicked;
  // Variants: 900px wide and UI font 20.
  await seedLongTitle('nine hundred pixel measurement');
  win.setContentSize(900, HEIGHT);
  await settle();
  report.deepField['layout-w900'] = await evaluate(() => {
    const card = document.querySelector('.deep-field-card');
    if (!card) return { card: false, rows: [] };
    const contentRight = card.getBoundingClientRect().right - (parseFloat(getComputedStyle(card).paddingRight) || 0);
    return {
      card: true,
      contentRight,
      rows: [...document.querySelectorAll('.deep-field-session')].map(row => {
        const title = row.querySelector('.deep-field-session-title');
        const time = row.querySelector('.deep-field-session-time');
        return {
          rowRight: row.getBoundingClientRect().right,
          timeRight: time.getBoundingClientRect().right,
          ellipsized: title.scrollWidth > title.clientWidth,
        };
      }),
    };
  });
  await captureDF('deep-field-example-dark-home-w900');
  win.setContentSize(WIDTH, HEIGHT);
  await evaluate(() => document.documentElement.style.setProperty('--wuu-font-size-ui', '20px'));
  await captureDF('deep-field-example-dark-home-font20');
  await evaluate(() => document.documentElement.style.removeProperty('--wuu-font-size-ui'));
  // Disable through the real CLI path: default frame + default welcome return.
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
    skin: document.documentElement.hasAttribute('data-shell-skin'),
    welcomeReplaced: Boolean(document.querySelector('.deep-field-welcome')),
    emptyHome: Boolean(document.querySelector('.empty-home')),
  }));
  await evaluate(keys => {
    if (keys.dark === null) window.localStorage.removeItem('wuu.extension-theme.dark');
    else window.localStorage.setItem('wuu.extension-theme.dark', keys.dark);
    if (keys.light === null) window.localStorage.removeItem('wuu.extension-theme.light');
    else window.localStorage.setItem('wuu.extension-theme.light', keys.light);
  }, priorThemeKeys);

  phase = 'checks';
  const pane = { x: 720, y: 500 };
  // Far edge of the pane: artwork shows there in every transparent state and
  // no card, bubble or composer reaches it.
  const paneFree = { x: 1350, y: 860 };
  const sidebar = { x: 180, y: 600 };
  const ribbon = { x: 26, y: 300 };

  // Canvas skin: sidebar, ribbon and opaque destinations stay on their
  // surfaces; only the pane interior shows artwork.
  check('deepfield home shows artwork', dist(at('deepfield-dark-home', pane.x, pane.y), at('deepfield-dark-home', sidebar.x, sidebar.y)) > 24,
    `pane ${at('deepfield-dark-home', pane.x, pane.y)} vs sidebar ${at('deepfield-dark-home', sidebar.x, sidebar.y)}`);
  check('automation stays opaque', dist(at('deepfield-dark-automation', pane.x, pane.y), at('deepfield-dark-automation', sidebar.x, sidebar.y)) < 12,
    `pane ${at('deepfield-dark-automation', pane.x, pane.y)} vs sidebar ${at('deepfield-dark-automation', sidebar.x, sidebar.y)}`);
  check('default-canvas fixture view stays opaque', dist(at('deepfield-dark-fixture-opaque', pane.x, pane.y), at('deepfield-dark-automation', pane.x, pane.y)) < 4,
    `${at('deepfield-dark-fixture-opaque', pane.x, pane.y)} vs ${at('deepfield-dark-automation', pane.x, pane.y)}`);
  check('transparent fixture view paints like the pane (single painter)',
    dist(at('deepfield-dark-fixture-clear', paneFree.x, paneFree.y), at('deepfield-dark-home', paneFree.x, paneFree.y)) < 6,
    `${at('deepfield-dark-fixture-clear', paneFree.x, paneFree.y)} vs ${at('deepfield-dark-home', paneFree.x, paneFree.y)}`);
  check('conversation veil sits between transparent and opaque',
    dist(at('deepfield-dark-conversation', paneFree.x, paneFree.y), at('deepfield-dark-home', paneFree.x, paneFree.y)) > 8
      && dist(at('deepfield-dark-conversation', paneFree.x, paneFree.y), at('deepfield-dark-automation', paneFree.x, paneFree.y)) > 8,
    `${at('deepfield-dark-conversation', paneFree.x, paneFree.y)}`);
  check('ribbon stays on the skin frame color',
    dist(at('deepfield-dark-home', ribbon.x, ribbon.y), at('deepfield-dark-automation', ribbon.x, ribbon.y)) < 4,
    `${at('deepfield-dark-home', ribbon.x, ribbon.y)} vs ${at('deepfield-dark-automation', ribbon.x, ribbon.y)}`);

  // Dot screen: neighboring pixels at pitch spacing alternate between the gap
  // color and the artwork inside the transparent pane.
  const row = pixels.get('deepfield-dark-home').row(390, 500, 32);
  const alternations = row.slice(1).filter((px, i) => dist(px, row[i]) > 4).length;
  check('dot screen alternates at pitch', alternations >= 8, `row ${JSON.stringify(row)} alternations=${alternations}`);

  check('reduced transparency restores the opaque veil',
    dist(at('deepfield-dark-reduced-transparency', paneFree.x, paneFree.y), at('deepfield-dark-automation', paneFree.x, paneFree.y)) < 6
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

  // Deep Field example: welcome replacement rendered, session row activated a
  // thread, and disabling the plugin restored the default frame + welcome.
  check('deep-field welcome renders both cards',
    report.deepField['welcome-dark']?.welcome && report.deepField['welcome-dark']?.sessionsCard
      && report.deepField['welcome-light']?.welcome && report.deepField['welcome-light']?.sessionsCard,
    JSON.stringify({ dark: report.deepField['welcome-dark'], light: report.deepField['welcome-light'] }));
  // Usage data is optional in the fixture; when present both cards must
  // render, and when absent the sessions card still renders alone.
  check('deep-field welcome pairs usage card when present',
    ['welcome-dark', 'welcome-light'].every(key => {
      const welcome = report.deepField[key];
      return welcome?.sessionsCard === true && (welcome?.usageCard !== true || welcome?.welcome === true);
    }),
    JSON.stringify({ dark: report.deepField['welcome-dark'], light: report.deepField['welcome-light'] }));
  check('deep-field welcome is gated on its own skin',
    report.deepField.nativeWelcome?.replaced === false && report.deepField.nativeWelcome?.emptyHome === true
      && report.deepField.liveInstall === true && report.deepField.liveRemove === true,
    JSON.stringify(report.deepField.nativeWelcome));
  check('deep-field session rows stay inside the card',
    ['layout-light', 'layout-dark', 'layout-w900'].every(key => {
      const layout = report.deepField[key];
      return layout?.card && layout.rows.every(row =>
        row.rowRight <= layout.contentRight + 1 && row.timeRight <= layout.contentRight + 1);
    }),
    JSON.stringify({ light: report.deepField['layout-light'], dark: report.deepField['layout-dark'], w900: report.deepField['layout-w900'] }));
  // Each measured layout is seeded with a fresh long-title thread right
  // before measurement, so every layout must show an ellipsized row.
  check('deep-field long titles are ellipsized',
    ['layout-light', 'layout-dark', 'layout-w900'].every(key =>
      (report.deepField[key]?.rows ?? []).some(row => row.ellipsized)),
    JSON.stringify({ light: report.deepField['layout-light'], dark: report.deepField['layout-dark'], w900: report.deepField['layout-w900'] }));
  check('deep-field lists no draft or child threads',
    (report.deepField['welcome-dark']?.untitledRows ?? 1) === 0
      && (report.deepField['welcome-dark']?.rows ?? 0) <= 3,
    JSON.stringify(report.deepField['welcome-dark']));
  check('deep-field session row activates the thread', Boolean(report.deepField.clickedThread), report.deepField.clickedThread);
  check('disabling deep-field restores default frame and welcome',
    report.deepField.afterDisable?.placement !== 'canvas'
      && report.deepField.afterDisable?.welcomeReplaced === false
      && report.deepField.afterDisable?.emptyHome === true,
    JSON.stringify(report.deepField.afterDisable));

  report.status = 'passed'; save();
  console.log(`PASS shell-skin canvas; evidence ${output}`);
  server.close(); app.quit();
}

const watchdog = setTimeout(() => { report.status = 'failed'; report.errors.push({ phase, message: 'watchdog timeout' }); save(); app.exit(1); }, 300000);
main().catch(async error => {
  report.status = 'failed'; report.errors.push({ phase, message: error.stack || String(error) });
  if (win && !win.isDestroyed()) { try { fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG()); } catch {} }
  save(); console.error(error); server.close(); clearTimeout(watchdog); app.exit(1);
});
app.on('before-quit', () => clearTimeout(watchdog));
