// Build desktop first: npm --prefix desktop run build:core && npm --prefix desktop run build.
// Run: electron desktop/scripts/artworks-skin-e2e.cjs
// Captures the real App in an isolated profile: the bundled Artworks frame
// skins (no install needed) and the workspace Halftone canvas skins, each in
// light and dark themes on the home destination.
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
const output = path.resolve(process.env.WUU_ARTWORKS_OUTPUT || path.join(repository, 'artifacts/default-artwork/e2e'));
const run = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-artworks-'));
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
const report = { status: 'running', output, captures: [], checks: [], errors: [], plugins: {} };
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
  const skinKey = pluginId => {
    const record = inventory.find(i => i.provenance?.plugin_id === pluginId);
    assert.ok(record, `${pluginId} must appear in inventory`);
    report.plugins[pluginId] = { id: record.id, state: record.state, fingerprint: record.fingerprint };
    return skin => `${record.provenance.plugin_id || record.id}:${skin}`;
  };
  const artworks = skinKey('artworks');
  const halftone = skinKey('halftone');

  const skins = [
    ...['nihonbashi-snow', 'hira-evening-snow', 'cherry-branch', 'snowy-sparrows',
        'starry-night', 'moonlit-street', 'crimea-sea', 'glasgow-docks']
      .map(id => ({ name: `art-${id}`, key: artworks(id) })),
    ...['halftone-sheer', 'halftone', 'halftone-clear']
      .map(id => ({ name: id, key: halftone(id), placement: 'canvas' })),
  ];

  for (const skin of skins) {
    phase = `skin-${skin.name}`;
    await evaluate(key => window.localStorage.setItem('wuu.shell-skin', key), skin.key);
    win.webContents.reload();
    await shellReady();
    if (skin.placement === 'canvas') {
      await until(() => evaluate(() => document.documentElement.dataset.shellPlacement === 'canvas'), 'canvas placement applied', 30000);
    } else {
      await until(() => evaluate(() => document.documentElement.hasAttribute('data-shell-skin')), 'skin applied', 30000);
    }
    for (const theme of ['light', 'dark']) {
      phase = `${skin.name}-${theme}`;
      await evaluate(t => window.wuu.setThemePreference(t), theme);
      await until(() => evaluate(t => document.documentElement.dataset.theme === t, theme), 'theme applied');
      await goHome();
      await capture(`${skin.name}-${theme}-home`);
    }
    check(`${skin.name}-skin-applied`, true);
  }
  report.status = 'done';
}

main().catch(err => { report.status = 'failed'; report.error = String(err && err.stack || err); })
  .finally(() => { save(); server.close(); setTimeout(() => app.exit(report.status === 'done' ? 0 : 1), 300); });
