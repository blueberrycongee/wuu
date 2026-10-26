// Native input and the selected default UI providers through production
// main/preload/core. No message is sent and no inference request is permitted.
// Run after a desktop/core build; WUU_FOOTER_FONT=15|20, WUU_FOOTER_LONG_NAMES=1.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');
const desktop = path.resolve(process.env.WUU_FOOTER_DESKTOP || path.join(__dirname, '..'));
const font = Number(process.env.WUU_FOOTER_FONT || 15);
const longNames = process.env.WUU_FOOTER_LONG_NAMES === '1';
assert.ok([15, 20].includes(font));
const output = path.resolve(process.env.WUU_FOOTER_OUTPUT || path.join(desktop, `out/e2e/composer-footer-${font}${longNames ? '-long' : ''}`));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-composer-footer-'));
const home = path.join(fixture, 'home'), userHome = path.join(fixture, 'user-home'), project = path.join(fixture, 'project');
const projectName = longNames ? 'Project with a deliberately long readable workspace name' : 'ProjectX';
const branch = longNames ? 'feature/a-deliberately-long-branch-name-for-responsive-controls' : 'main';
for (const dir of [output, home, userHome, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: home, HOME: userHome, XDG_CACHE_HOME: path.join(userHome, '.cache'), CODEX_HOME: path.join(userHome, '.codex'), GROK_HOME: path.join(userHome, '.grok'), WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
const report = { status: 'running', source: process.env.WUU_E2E_SOURCE_COMMIT, font, longNames, checks: [], samples: [], railMotion: [], screenshots: [], input: [], clicks: [], inferenceRequests: [], sendIPC: [], errors: [], violations: [], limitations: ['Linux Electron native input, not physical macOS acceptance.', 'Synthetic local project and metadata endpoint. No message submission, model inference or real user data.'] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
let win;
const wideControlFonts = new Map();
const register = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => register(channel, async (event, ...args) => {
  if (/^wuu:(thread-start|turn-start|turn-queue)$/.test(channel)) { report.sendIPC.push(channel); throw new Error('Composer footer acceptance must not submit a message'); }
  return handler(event, ...args);
});
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created;
  win.webContents.on('console-message', event => { if (event.level === 'error') report.errors.push(event.message); });
});
const server = http.createServer((req, res) => {
  if (req.method === 'GET') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ data: [{ id: 'fixture' }] })); return; }
  report.inferenceRequests.push({ method: req.method, url: req.url }); save(); res.writeHead(500); res.end('Inference is not allowed in this layout fixture');
});
const composer = '[data-main-conversation-composer] textarea:not(.composer-response-selection-comment)';
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
async function until(check, label, timeout = 30000) { const end = Date.now() + timeout; while (Date.now() < end) { const value = await check(); if (value) return value; await delay(25); } throw new Error(`Timed out: ${label}`); }
function key(keyCode, modifiers = []) { for (const type of ['keyDown', 'keyUp']) win.webContents.sendInputEvent({ type, keyCode, modifiers }); }
async function click(selector) {
  const point = await until(() => evaluate(async selector => {
    const el = [...document.querySelectorAll(selector)].find(el => el.getBoundingClientRect().height && !el.closest('[inert]'));
    if (!el) return;
    const before = el.getBoundingClientRect();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const r = el.getBoundingClientRect();
    if (Math.abs(r.x - before.x) + Math.abs(r.y - before.y) > 0.5) return;
    const x = Math.round(r.x + r.width / 2), y = Math.round(r.y + r.height / 2);
    if (!el.contains(document.elementFromPoint(x, y))) return;
    el.addEventListener('click', event => { (window.footerNativeClicks ||= []).push({ selector, trusted: event.isTrusted }); }, { once: true });
    return { x, y };
  }, selector), `visible hit target ${selector}`);
  win.focus(); win.webContents.focus();
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
}
async function typeDraft(label) {
  await click(composer);
  await until(() => evaluate(selector => document.activeElement?.matches(selector), composer), 'native input focus');
  key('a', [process.platform === 'darwin' ? 'meta' : 'control']);
  const text = '输入检查 Native draft remains editable.';
  await evaluate(selector => {
    const el = document.querySelector(selector);
    el.addEventListener('input', event => { window.footerLastInput = { trusted: event.isTrusted, value: el.value }; }, { once: true });
  }, composer);
  await win.webContents.insertText(text);
  const state = await until(() => evaluate(({ selector, text }) => {
    const el = document.querySelector(selector), send = el?.closest('[data-main-conversation-composer]')?.querySelector('[data-wuu-component="composer-send"]');
    // The native value changes before React commits the derived Send state.
    if (el?.value !== text || !window.footerLastInput || !send || send.disabled) return;
    return { ...window.footerLastInput, focused: el === document.activeElement, disabled: el.disabled, readOnly: el.readOnly, sendEnabled: Boolean(send && !send.disabled) };
  }, { selector: composer, text }), 'native text reaches the composer');
  report.input.push({ label, ...state });
  assert.ok(state.trusted && state.focused && !state.disabled && !state.readOnly && state.sendEnabled, `${label}: empty grey placeholder must not be confused with a disabled editor`);
  key('a', [process.platform === 'darwin' ? 'meta' : 'control']); key('Backspace');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === '', composer), 'native draft clearing');
}
async function toggleRail(label) {
  await evaluate(() => {
    const footer = document.querySelector('.composer-footer-shell');
    if (!footer) throw new Error('Main composer footer is missing');
    window.footerRailMotion = [];
    const record = event => {
      if (event.target === footer && event.propertyName === 'height') window.footerRailMotion.push({
        type: event.type, elapsedMs: event.elapsedTime * 1000,
        height: footer.getBoundingClientRect().height,
      });
    };
    footer.addEventListener('transitionrun', record);
    footer.addEventListener('transitionend', record);
    window.stopFooterRailMotion = () => {
      footer.removeEventListener('transitionrun', record);
      footer.removeEventListener('transitionend', record);
    };
  });
  await click('[data-wuu-component="workspace-tool-rail-toggle"]');
  await geometry();
  const events = await evaluate(() => { window.stopFooterRailMotion(); return window.footerRailMotion; });
  report.railMotion.push({ label, events });
  assert(events.some(event => event.type === 'transitionrun')
    && events.some(event => event.type === 'transitionend' && event.elapsedMs > 0),
  `${label}: explicit tool-rail toggles must retain their footer-height animation`);
}
async function geometry() {
  return evaluate(() => new Promise((resolve, reject) => {
    let previous, stable = 0; const deadline = performance.now() + 10000;
    const rect = el => { if (!el) return null; const { x, y, width, height, right, bottom } = el.getBoundingClientRect(); return { x, y, width, height, right, bottom }; };
    function sample() {
      const dock = document.querySelector('[data-main-conversation-composer]'), footer = dock?.querySelector('.composer-footer'), input = dock?.querySelector('textarea');
      if (!dock || !input) return reject(new Error('Main composer is not visible'));
      const controls = [...(footer?.querySelectorAll('button') || [])].map(el => {
        const r = rect(el), text = el.querySelector('.hero-project-pill-text');
        const x = Math.round(r.x + r.width / 2), y = Math.round(r.y + r.height / 2);
        return { ...r, label: el.getAttribute('aria-label'), text: text?.textContent, textOverflow: text ? text.scrollWidth - text.clientWidth : 0, font: parseFloat(getComputedStyle(el).fontSize), disabled: el.disabled, hit: el.contains(document.elementFromPoint(x, y)), project: Boolean(el.closest('.composer-project-control')), branch: Boolean(el.closest('.composer-branch-control')) };
      });
      const rows = [];
      for (const control of controls) if (control.height && !rows.some(y => Math.abs(y - control.y) < 2)) rows.push(control.y);
      const value = { viewport: { width: innerWidth, height: innerHeight }, pageWidth: document.documentElement.scrollWidth, dpr: devicePixelRatio, pane: rect(document.querySelector('.conversation-pane')), panel: rect(document.querySelector('.workspace-right-panel')), shell: rect(dock.querySelector('.composer-shell')), editor: rect(dock.querySelector('.composer-editor')), footer: rect(footer), footerShell: rect(dock.querySelector('.composer-footer-shell')), workspace: rect(footer?.querySelector('.composer-workspace-bar')), controls, rows: rows.length, inputFont: parseFloat(getComputedStyle(input).fontSize), railControls: document.querySelectorAll('.workspace-composer-controls button').length, collapsed: Boolean(document.querySelector('.workspace-tool-rail-collapsed')) };
      const current = JSON.stringify(value);
      stable = previous === current ? stable + 1 : 0; previous = current;
      const moving = document.getAnimations().some(animation => animation.playState === 'running' && Number.isFinite(animation.effect?.getComputedTiming().endTime));
      if (stable >= 4 && !moving) return resolve(value);
      if (performance.now() > deadline) return reject(new Error(`Footer geometry did not settle: ${current}`));
      requestAnimationFrame(sample);
    }
    requestAnimationFrame(sample);
  }));
}
async function capture(label, expectation) {
  const sample = await geometry();
  report.samples.push({ label, expectation, ...sample });
  fs.writeFileSync(path.join(output, `${label}.png`), (await win.webContents.capturePage()).toPNG()); report.screenshots.push(`${label}.png`);
  const check = (condition, message) => { if (!condition) report.violations.push(`${label}: ${message}`); };
  check(sample.inputFont === font, `editor font remains ${font}px, got ${sample.inputFont}`);
  check(sample.pageWidth <= sample.viewport.width, 'page does not overflow horizontally');
  check(sample.editor.x >= sample.pane.x && sample.editor.right <= sample.pane.right + 1, 'editor stays inside conversation');
  if (expectation === 'rail') {
    check(sample.railControls >= 4, 'expanded tool rail owns workspace, branch and runtime controls');
    check(sample.controls.length === 0 && sample.footerShell?.height <= 1, 'expanded tool rail releases the footer');
  } else {
    const projectControl = sample.controls.find(control => control.project), branchControl = sample.controls.find(control => control.branch);
    check(Boolean(projectControl && branchControl), 'workspace and branch remain available in the collapsed-rail fallback');
    check(sample.rows <= 2, `footer uses at most two compact rows, got ${sample.rows}`);
    for (const control of sample.controls) {
      check(control.x >= sample.footer.x - 1 && control.right <= sample.footer.right + 1 && control.y >= sample.footer.y - 1 && control.bottom <= sample.footer.bottom + 1, `control remains inside footer: ${control.label}`);
      check(control.hit, `control remains hit-testable: ${control.label}`);
      if (control.project || control.branch) {
        const role = control.project ? 'project' : 'branch';
        if (expectation === 'wide') wideControlFonts.set(role, control.font);
        check(control.font === wideControlFonts.get(role), `narrow label keeps its wide-layout font: ${control.label} (${control.font}px)`);
      }
    }
    if (!longNames && projectControl && branchControl) {
      check(Math.abs(projectControl.y - branchControl.y) <= 1, 'short project and branch stay on the same row');
      check(projectControl.textOverflow <= 1 && branchControl.textOverflow <= 1, 'short labels remain fully readable');
    }
    if (expectation === 'wide') check(sample.rows === 1, 'wide layout keeps a single footer row');
  }
  save();
}
async function run() {
  for (const args of [['init', '-b', branch], ['-c', 'user.name=Footer Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '--allow-empty', '-m', 'Synthetic composer layout fixture']]) {
    const result = spawnSync('git', args, { cwd: project, encoding: 'utf8' }); assert.equal(result.status, 0, result.stderr);
  }
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])), providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } } }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: projectName, path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'zh-CN', theme: 'light', message_flow_font_size: font }));
  report.coreSHA256 = createHash('sha256').update(fs.readFileSync(process.env.WUU_DESKTOP_CORE)).digest('hex');
  report.rendererSHA256 = Object.fromEntries(fs.readdirSync(path.join(desktop, 'out/renderer/assets')).filter(file => /\.(js|css)$/.test(file)).map(file => [file, createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/renderer/assets', file))).digest('hex')]));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && evaluate(selector => Boolean(document.querySelector(selector)), composer), 'production composer');
  win.setContentSize(1400, 1000); win.show(); win.focus(); win.webContents.setBackgroundThrottling(false);
  win.webContents.debugger.attach('1.3');
  await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'no-preference' }] });
  await until(() => evaluate(() => Boolean(document.querySelector('.composer-branch-control'))), 'real Git branch metadata');
  // Keep the sidebar open, as in the escaped layout.
  if (await evaluate(() => document.querySelector('.app-shell').classList.contains('sidebar-collapsed'))) await click('[data-wuu-component="sidebar-toggle"]');
  if (await evaluate(() => !document.querySelector('.workspace-tool-rail-collapsed'))) await click('[data-wuu-component="workspace-tool-rail-toggle"]');
  for (const theme of ['light', 'dark']) {
    await evaluate(theme => window.wuu.setThemePreference(theme), theme);
    await until(() => evaluate(theme => document.documentElement.dataset.theme === theme, theme), 'applied theme');
    win.setContentSize(1400, 1000);
    await geometry();
    if (await evaluate(() => document.querySelector('.app-shell').classList.contains('right-panel-open'))) await click('[data-wuu-component="right-sidebar-toggle"]');
    await capture(`${theme}-wide-panel-closed`, 'wide'); await typeDraft(`${theme}-wide`);
    await click('[data-wuu-component="right-sidebar-toggle"]');
    await until(() => evaluate(() => Boolean(document.querySelector('.workspace-right-panel-resizer:not([inert])'))), 'docked right panel');
    // Stay just above the product's automatic full-panel threshold while
    // dragging the native separator to its maximum width to reproduce a <320px input.
    const sidebarWidth = await evaluate(() => document.querySelector('.sidebar')?.getBoundingClientRect().width || 0);
    win.setContentSize(Math.ceil(sidebarWidth + 764), 1000);
    await geometry();
    const separator = await evaluate(() => { const r = document.querySelector('.workspace-right-panel-resizer').getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) }; });
    win.webContents.sendInputEvent({ type: 'mouseMove', ...separator });
    win.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...separator });
    win.webContents.sendInputEvent({ type: 'mouseMove', button: 'left', x: Math.max(1, separator.x - 600), y: separator.y });
    win.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, x: Math.max(1, separator.x - 600), y: separator.y });
    const narrow = await geometry();
    assert.ok(narrow.shell.width <= 320 && narrow.shell.width >= 240, `fixture must reproduce the narrow pane, got ${narrow.shell.width}`);
    await capture(`${theme}-narrow-panel-open`, 'footer'); await typeDraft(`${theme}-narrow`);
    await toggleRail(`${theme}-expand`);
    await capture(`${theme}-narrow-rail-expanded`, 'rail'); await typeDraft(`${theme}-rail`);
    await toggleRail(`${theme}-collapse`);
    await capture(`${theme}-narrow-rail-recollapsed`, 'footer'); await typeDraft(`${theme}-recollapsed`);
  }
  report.clicks = await evaluate(() => window.footerNativeClicks || []);
  assert.ok(report.clicks.length > 0 && report.clicks.every(click => click.trusted), 'all control clicks are native trusted input');
  assert.deepEqual(report.inferenceRequests, []); assert.deepEqual(report.sendIPC, []);
  assert.deepEqual(report.violations, [], 'responsive footer geometry');
  report.status = 'passed'; report.checks.push('Real Git metadata; light/dark, wide/narrow, expanded/collapsed rail; unchanged font sizes; no clipping; native focus and draft input; no inference'); save();
  console.log(`PASS composer footer ${font}px${longNames ? ' long names' : ''}: ${output}`);
}
function finish(code) { server.closeAllConnections(); server.close(); app.once('will-quit', () => app.exit(code)); app.quit(); }
setTimeout(() => { report.status = 'failed'; report.error = 'watchdog expired'; save(); finish(1); }, 180000).unref();
run().then(() => finish(0), async error => { report.status = 'failed'; report.error = String(error.stack || error); if (win && !win.isDestroyed()) fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG()); save(); console.error(error); finish(1); });
