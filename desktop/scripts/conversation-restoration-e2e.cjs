// Production UI + selected-provider lifecycle, native input, synthetic read failures.
// Build the renderer first. No real sessions, credentials, engines, or model calls.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { app, BrowserWindow, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_RESTORATION_OUTPUT || path.join(desktop, 'out/e2e/conversation-restoration'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-conversation-restoration-'));
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { HOME: fixture, WUU_RESIZE_E2E_CWD: fixture, WUU_RESIZE_E2E_LOCALE: 'en-US' });
const report = { status: 'running', fixture: 'production renderer, sandboxed synthetic bridge, native input', checks: [], resumes: [], writes: [], console: [], screenshots: [] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let win, rejectRetired, recovered = false;
const evaluate = (fn, input) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(input)})`);
async function until(fn, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) { const value = await fn(); if (value) return value; await pause(25); }
  throw new Error(`Timed out: ${label}`);
}
async function click(selector, text) {
  const point = await until(() => evaluate(({ selector, text }) => {
    for (const el of document.querySelectorAll(selector)) {
      if (el.closest('[inert]') || el.disabled || text && !el.textContent.includes(text)) continue;
      el.scrollIntoView({ block: 'nearest' });
      const r = el.getBoundingClientRect(), x = Math.round(r.x + r.width / 2), y = Math.round(r.y + r.height / 2);
      if (!r.height || !el.contains(document.elementFromPoint(x, y))) continue;
      el.addEventListener('click', event => { window.restorationTrustedClicks = (window.restorationTrustedClicks || 0) + Number(event.isTrusted); }, { once: true });
      return { x, y };
    }
  }, { selector, text }), `native click ${selector} ${text || ''}`);
  win.focus(); win.webContents.focus();
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
}
const composer = '[data-main-conversation-composer] textarea';
async function ready() { await until(() => evaluate(selector => {
  const input = document.querySelector(selector);
  return input && !input.disabled && !input.readOnly && !input.closest('[inert]') && !document.querySelector('.view-switch-loading');
}, composer), 'usable conversation and composer'); }
async function capture(name) {
  win.webContents.sendInputEvent({ type: 'mouseMove', x: 1100, y: 650 });
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await pause(350);
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, `${name}.txt`), await evaluate(() => document.body.innerText));
  report.screenshots.push(`${name}.png`); save();
}
ipcMain.handle('test:restoration-resume', async (_event, request) => {
  report.resumes.push(request); save();
  if (request.id === 'unavailable' && request.graph === 'restoration-original') await new Promise((_resolve, reject) => { rejectRetired = reject; });
  if (request.id === 'unavailable' && !recovered) throw new Error('Synthetic thread execution authority unavailable after plugin deletion');
});
ipcMain.handle('test:restoration-write', (_event, method, args) => {
  report.writes.push({ method, args }); save(); throw new Error('This read-only fixture must never submit');
});
async function run() {
  fs.mkdirSync(output, { recursive: true });
  const preload = path.join(fixture, 'preload.cjs');
  await require('esbuild').build({ entryPoints: [path.join(__dirname, 'resize-e2e-preload.cjs')], outfile: preload,
    bundle: true, platform: 'node', format: 'cjs', external: ['electron'],
    define: { 'process.env.WUU_RENDERER_E2E_EXTENSION': JSON.stringify(path.join(__dirname, 'conversation-restoration-e2e-preload.cjs')) } });
  await app.whenReady();
  win = new BrowserWindow({ width: 1280, height: 900, show: true, webPreferences: { preload, sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  win.webContents.session.webRequest.onBeforeRequest(({ url }, done) => done({ cancel: /^https?:/.test(url) }));
  win.webContents.on('console-message', event => { if (event.level === 'warning' || event.level === 'error') report.console.push(event.message); });
  const renderer = path.join(desktop, 'out/renderer/index.html');
  report.rendererSha256 = createHash('sha256').update(fs.readFileSync(renderer)).digest('hex');
  report.runnerSha256 = createHash('sha256').update(fs.readFileSync(__filename)).digest('hex');
  const assets = path.join(desktop, 'out/renderer/assets');
  const bundle = createHash('sha256');
  for (const file of fs.readdirSync(assets).filter(file => file.endsWith('.js')).sort()) bundle.update(file).update(fs.readFileSync(path.join(assets, file)));
  report.rendererAssetsSha256 = bundle.digest('hex');
  await win.loadFile(renderer); await ready();
  await click(composer); await win.webContents.insertText('Preserved unsent draft');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === 'Preserved unsent draft', composer), 'native draft entry');
  await click('.thread-row-main', 'Unavailable conversation');
  await until(() => rejectRetired, 'original pending destination');
  win.webContents.send('test:restoration-graph', false);
  await until(() => report.resumes.some(row => row.graph !== 'restoration-original'), 'successor history read');
  await ready();
  await until(() => evaluate(() => Boolean(document.querySelector('[role="alert"]'))), 'localized failure notice');
  const local = await evaluate(selector => ({ text: document.body.innerText, draft: document.querySelector(selector)?.value,
    rows: [...document.querySelectorAll('.thread-row-main')].map(el => el.textContent), notice: document.querySelector('[role="alert"]')?.textContent }), composer);
  assert.equal(local.draft, 'Preserved unsent draft');
  assert.ok(local.rows.some(row => row.includes('Healthy conversation')));
  assert.ok(!local.text.includes('Open recovery'));
  assert.ok(!local.notice.includes('Synthetic'), 'technical error is retained in diagnostics, not the notice');
  assert.equal(report.writes.length, 0);
  report.checks.push('Failed destination keeps sidebar, selected provider and unsent draft usable without replaying writes');
  await capture('localized-failure');
  rejectRetired(new Error('Retired synthetic history failure'));
  await pause(100);
  await click('.thread-row-main', 'Healthy conversation');
  await until(() => evaluate(() => document.body.innerText.includes('healthy history remains accessible')), 'healthy conversation');
  await ready();
  report.checks.push('Retired failure cannot replace the successor or prevent healthy navigation');
  recovered = true;
  await click('.thread-row-main', 'Unavailable conversation');
  await until(() => evaluate(() => document.body.innerText.includes('unavailable history remains accessible')), 'explicit retry');
  await ready();
  assert.equal(report.resumes.filter(row => row.id === 'unavailable').length, 3, 'one old read, one restoration, one explicit retry');
  assert.equal(report.writes.length, 0);
  assert.ok(await evaluate(() => window.restorationTrustedClicks >= 4), 'native input reached the actual controls');
  report.checks.push('Explicit retry reads the same selected authority; no creation, turn start or queue replay');
  await capture('explicit-retry');
  win.webContents.send('test:restoration-graph', true);
  await until(() => evaluate(() => document.body.innerText.includes('Open recovery')), 'genuine selected configuration failure');
  report.checks.push('Genuine selected application configuration failure still exposes global recovery');
  await capture('root-failure');
  report.status = 'passed'; save(); console.log(`PASS conversation restoration: ${output}`); app.exit(0);
}
run().catch(async error => { report.status = 'failed'; report.error = String(error.stack); await capture('failure').catch(() => {}); save(); console.error(error); app.exit(1); });
