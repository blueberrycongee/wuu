// Build desktop first: npm --prefix desktop run build:core && npm --prefix desktop run build.
// Run: electron desktop/scripts/artworks-settings-gallery-e2e.cjs
// Captures the Appearance settings row (three-card summary) and the second-level
// shell skin gallery it opens, in light and dark, zh-CN and en-US.
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
const output = path.resolve(process.env.WUU_ARTWORKS_GALLERY_OUTPUT || path.join(repository, 'artifacts/default-artwork/gallery-e2e'));
const run = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-artworks-gallery-'));
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
const report = { status: 'running', output, captures: [], checks: [], errors: [] };
let phase = 'setup', win;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const bounded = (promise, label, ms = 15000) => Promise.race([promise, sleep(ms).then(() => { throw new Error(`Timed out: ${label}`); })]);
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await bounded(Promise.resolve().then(check), label, 8000)) return; await sleep(50); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, arg) => bounded(win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`), 'renderer');
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

const server = http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] }));
});

async function shellReady() {
  await until(() => evaluate(() => Boolean(document.querySelector('[data-ribbon-id]') && document.querySelector('.primary-nav .nav-item'))), 'navigation shell', 60000);
}
async function settle() {
  await evaluate(() => Promise.all(document.getAnimations()
    .filter(a => Number.isFinite(a.effect?.getComputedTiming?.().endTime))
    .map(a => a.finished.catch(() => {}))).then(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))));
}
async function capture(name) {
  await settle();
  const image = await win.webContents.capturePage();
  fs.writeFileSync(path.join(output, `${name}.png`), image.toPNG());
  report.captures.push({ file: `${name}.png`, phase });
}
async function goHome() {
  if (await evaluate(() => Boolean(document.querySelector('[data-primary-plugin-view]')))) {
    await until(() => evaluate(() => !document.querySelector('[data-primary-plugin-view]') && Boolean(document.querySelector('.primary-nav .nav-item'))), 'conversations section');
  }
}
async function openSettingsAppearance() {
  await goHome();
  await until(() => evaluate(() => Boolean(document.querySelector('[data-settings-entry="settings"]'))), 'settings entry');
  await evaluate(() => document.querySelector('[data-settings-entry="settings"]').click());
  await until(() => evaluate(() => Boolean(document.querySelector('.settings-nav-item'))), 'settings nav');
  await evaluate(() => {
    const items = [...document.querySelectorAll('.settings-nav-item')];
    const target = items.find(item => /appearance|外观/i.test(item.textContent || ''));
    if (!target) throw new Error('appearance nav item not found: ' + items.map(i => i.textContent).join('|'));
    target.click();
  });
  await until(() => evaluate(() => Boolean(document.querySelector('.settings-skin-preview'))), 'skin row', 30000);
  await sleep(500);
}
async function openGallery() {
  await evaluate(() => {
    const cards = [...document.querySelectorAll('.settings-theme-card')];
    const more = cards.find(card => /更多|more/i.test(card.textContent || '') && !/收起|show less/i.test(card.textContent || ''));
    if (!more) throw new Error('more card not found');
    more.click();
  });
  await until(() => evaluate(() => Boolean(document.querySelector('.settings-skin-gallery'))), 'skin gallery', 10000);
  await sleep(500);
}
async function closeGallery() {
  await evaluate(() => document.querySelector('[data-testid="settings-skin-gallery-back"]')?.click());
  await until(() => evaluate(() => Boolean(document.querySelector('.settings-skin-preview')) && !document.querySelector('.settings-skin-gallery')), 'back to appearance', 10000);
  await sleep(300);
}

async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', agent: { permission_mode: 'unconfined' },
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } },
    engines: { default_engine: 'wuu' }, skills: { enabled: false }, ptc: { enabled: false } }));
  const stamp = new Date().toISOString();
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Fixture', path: workspace, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'fixture', cwd: workspace } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'zh-CN', theme: 'light' }));

  phase = 'install-plugins';
  const installed = spawnSync(core, ['plugin', 'install', '--json', path.join(repository, 'plugins/halftone/package')], { env: process.env, encoding: 'utf8', timeout: 30000 });
  fs.appendFileSync(path.join(output, 'plugin-install.log'), `${installed.stdout}\n${installed.stderr}\n`);
  assert.equal(installed.status, 0, 'halftone install must succeed');

  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.isDestroyed(), 'Wuu window');
  win.setContentSize(WIDTH, HEIGHT);
  win.webContents.debugger.attach('1.3');
  await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
  await shellReady();

  const inventory = (await evaluate(() => window.wuu.initialize())).extension_inventory;
  fs.writeFileSync(path.join(output, 'inventory.json'), JSON.stringify(inventory.map(i => ({ id: i.id, kind: i.kind, state: i.state, plugin_id: i.provenance?.plugin_id, skins: (i.contributions?.skins || []).map(s => s.id) })), null, 2));

  // Select one painting so the summary row shows the "current choice" card.
  const starry = inventory.flatMap(i => (i.contributions?.skins || []).map(s => ({ plugin: i, skin: s })))
    .find(entry => entry.skin.id === 'starry-night');
  assert.ok(starry, 'starry-night skin must be contributed');
  const starryKey = `${starry.plugin.provenance?.plugin_id || starry.plugin.id}:${starry.skin.id}`;

  for (const theme of ['light', 'dark']) {
    for (const language of ['zh-CN', 'en-US']) {
      phase = `settings-${theme}-${language}`;
      await evaluate(t => window.wuu.setThemePreference(t), theme);
      await until(() => evaluate(t => document.documentElement.dataset.theme === t, theme), 'theme applied');
      await evaluate(l => window.wuu.setLanguagePreference(l), language);
      await sleep(300);
      await openSettingsAppearance();
      // Summary row with the selected painting.
      await evaluate(key => window.localStorage.setItem('wuu.shell-skin', key), starryKey);
      win.webContents.reload();
      await shellReady();
      await openSettingsAppearance();
      await capture(`${theme}-summary-${language}`);
      // Gallery.
      await openGallery();
      await capture(`${theme}-gallery-${language}`);
      await evaluate(() => {
        const scroller = document.querySelector('.settings-scroll') || document.scrollingElement;
        if (scroller) scroller.scrollTop = scroller.scrollHeight;
      });
      await sleep(400);
      await capture(`${theme}-gallery-bottom-${language}`);
      // Select the default frame inside the gallery to show the active state change.
      await evaluate(() => {
        const cards = [...document.querySelectorAll('.settings-skin-gallery .settings-theme-card')];
        const first = cards[0];
        if (first) first.click();
      });
      await sleep(400);
      await capture(`${theme}-gallery-default-selected-${language}`);
      await closeGallery();
      check(`${phase}-gallery-flow`, true);
    }
  }
  report.status = 'done';
}

main().catch(err => { report.status = 'failed'; report.error = String(err && err.stack || err); console.error(err); })
  .finally(() => { save(); server.close(); setTimeout(() => app.exit(report.status === 'done' ? 0 : 1), 300); });
