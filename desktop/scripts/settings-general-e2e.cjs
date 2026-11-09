// Real renderer/main/preload/Go persistence with a disposable Wuu home.
// No account, external provider requests, or user configuration is used.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_SETTINGS_GENERAL_OUTPUT || path.join(desktop, 'out/e2e/settings-general'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-settings-general-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project'), userHome = path.join(fixture, 'user-home');
for (const dir of [output, home, project, userHome]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.HOME = userHome;
process.env.WUU_DESKTOP_CORE ||= path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
const configPath = path.join(home, 'config.json');
const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
fs.writeFileSync(configPath, JSON.stringify({ default_provider: 'fixture', engines,
  providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'fixture-only', model: 'fixture' } },
  agent: { git_attribution_enabled: true, disable_auto_compact: true, compact_threshold_pct: 0.6, compact_keep_recent_tokens: 12000 },
  ptc: { enabled: false },
}));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Settings review', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'zh-CN', theme: 'light' }));
let win;
app.on('browser-window-created', (_event, created) => { win ||= created; });
const report = { status: 'running', sourceCommit: process.env.WUU_E2E_SOURCE_COMMIT || '', checks: [], screenshots: [], errors: [] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
async function until(check, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) { if (await check()) return; await delay(40); }
  throw new Error(`Timed out: ${label}`);
}
async function openGeneral(page = 'general') {
  await evaluate(page => window.dispatchEvent(new CustomEvent('wuu:open-settings', { detail: { page } })), page);
  await until(() => evaluate(() => !!document.querySelector('[data-testid="settings-git-attribution"]')), 'General attribution switch');
}
async function clickAttribution() {
  const point = await evaluate(() => {
    const button = document.querySelector('[data-testid="settings-git-attribution"]');
    button.scrollIntoView({ block: 'center' });
    const r = button.getBoundingClientRect(), x = r.x + r.width / 2, y = r.y + r.height / 2;
    return { x: Math.round(x), y: Math.round(y), hit: button.contains(document.elementFromPoint(x, y)), disabled: button.disabled };
  });
  assert(point.hit && !point.disabled, 'The switch owns its pointer target');
  for (const type of ['mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, x: point.x, y: point.y });
}
function freshCoreSnapshot() {
  const processResult = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--workdir', project], { env: process.env, input: '{"id":"readback","method":"initialize"}\n', encoding: 'utf8', timeout: 30000 });
  assert.equal(processResult.status, 0, processResult.stderr);
  const response = processResult.stdout.split('\n').filter(Boolean).map(line => JSON.parse(line)).find(item => item.id === 'readback');
  assert.ok(response?.result, processResult.stdout);
  return response.result;
}
const timeout = setTimeout(() => fail(new Error('Settings acceptance timed out')), 150000);
async function run() {
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => !!win, 'main window');
  win.webContents.on('console-message', details => { if (details.level === 3) report.errors.push(details.message); });
  await until(() => evaluate(() => !!document.querySelector('.app-shell')), 'application shell');
  await openGeneral();
  const before = await evaluate(async () => (await window.wuu.initialize()).general_settings.git_attribution_enabled);
  assert.equal(before, true);
  assert.equal(await evaluate(() => !!document.querySelector('[data-wuu-page="advanced"].settings-nav-item')), false);
  await clickAttribution();
  await until(() => evaluate(() => document.querySelector('[data-testid="settings-git-attribution"]')?.getAttribute('aria-checked') === 'false'), 'saved attribution state');
  assert.equal(JSON.parse(fs.readFileSync(configPath, 'utf8')).agent.git_attribution_enabled, false);
  assert.equal(freshCoreSnapshot().general_settings.git_attribution_enabled, false);
  report.checks.push('General switch saves through real preload/main/Go to config.json', 'A newly started core reads the saved value');
  await new Promise(resolve => { win.webContents.once('did-finish-load', resolve); win.webContents.reload(); });
  await until(() => evaluate(() => !!document.querySelector('.app-shell')), 'reloaded shell');
  await openGeneral('advanced');
  assert.equal(await evaluate(() => document.querySelector('[data-testid="settings-git-attribution"]').getAttribute('aria-checked')), 'false');
  report.checks.push('Renderer reload preserves the value; legacy advanced destination resolves to General');
  await clickAttribution();
  await until(() => evaluate(() => document.querySelector('[data-testid="settings-git-attribution"]')?.getAttribute('aria-checked') === 'true'), 'restored attribution state');
  const persisted = JSON.parse(fs.readFileSync(configPath, 'utf8'));
  assert.equal(persisted.agent.disable_auto_compact, true);
  assert.equal(persisted.agent.compact_threshold_pct, 0.6);
  assert.equal(persisted.agent.compact_keep_recent_tokens, 12000);
  assert.equal(persisted.ptc.enabled, false);
  // External configuration edits still reach the next runtime without the removed UI.
  persisted.agent.compact_threshold_pct = 0.55;
  fs.writeFileSync(configPath, JSON.stringify(persisted));
  const restarted = freshCoreSnapshot();
  assert.equal(restarted.advanced_settings.compact_threshold_pct, 0.55);
  assert.equal(restarted.general_settings.ptc.enabled, false);
  report.checks.push('Attribution updates preserve advanced fields', 'Direct config edit is read by a fresh core');
  for (const [theme, font, width] of [['light', 15, 1280], ['dark', 15, 1280], ['light', 20, 720], ['dark', 20, 720]]) {
    win.setContentSize(width, 900);
    await until(() => evaluate(width => innerWidth === width, width), 'viewport resize');
    await evaluate(({ theme, font }) => { document.documentElement.dataset.theme = theme; document.documentElement.style.setProperty('--font-ui', `${font}px`); }, { theme, font });
    await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
    await delay(500);
    const geometry = await evaluate(() => {
      const button = document.querySelector('[data-testid="settings-git-attribution"]');
      // The general page scrolls: bring the switch into the viewport so the
      // geometry check and the screenshot cover it.
      button.scrollIntoView({ block: "center" });
      const r = button.getBoundingClientRect();
      return { viewport: innerWidth, documentWidth: document.documentElement.scrollWidth, x: r.x, right: r.right, y: r.y, bottom: r.bottom, height: innerHeight, nav: [...document.querySelectorAll('.settings-nav-item')].map(item => item.textContent.trim()) };
    });
    assert(geometry.documentWidth <= geometry.viewport && geometry.x >= 0 && geometry.right <= width && geometry.y >= 0 && geometry.bottom <= geometry.height, JSON.stringify(geometry));
    const file = `settings-general-${theme}-${font}px-${width}.png`;
    const capture = await win.webContents.capturePage();
    assert(capture.getSize().width > 100, 'Real display capture is required');
    fs.writeFileSync(path.join(output, file), capture.toPNG());
    report.screenshots.push({ file, ...geometry });
  }
  report.status = 'passed'; save(); clearTimeout(timeout);
  console.log(JSON.stringify(report, null, 2)); app.exit(0);
}
function fail(error) { report.status = 'failed'; report.errors.push(String(error.stack || error)); save(); console.error(error); clearTimeout(timeout); app.exit(1); }
run().catch(fail);
