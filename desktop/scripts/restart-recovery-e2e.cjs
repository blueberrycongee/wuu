// Build with npm --prefix desktop run build:renderer, then run with Electron.
// Actual product renderer and native input; synthetic backend, no model calls.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { app, BrowserWindow, ipcMain, nativeTheme } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.join(desktop, 'out/e2e/restart-recovery');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-restart-recovery-'));
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_RESIZE_E2E_CWD = fixture;
process.env.WUU_RESIZE_E2E_LOCALE = 'zh-CN';
const report = { checks: [], screenshots: [], starts: [], dismissals: [], errors: [] };
let pending = [0, 1, 2], failStart = false, win;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function until(fn, label) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) { const value = await fn(); if (value) return value; await pause(25); }
  throw new Error(`Timed out: ${label}`);
}
async function click(selector) {
  const point = await evaluate(selector => {
    const element = document.querySelector(selector);
    const r = element.getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
  }, selector);
  win.focus(); win.webContents.focus();
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
}
async function capture(name) {
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await pause(250);
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  report.screenshots.push(`${name}.png`);
  const geometry = await evaluate(() => {
    const panel = document.querySelector('.restart-recovery-dialog');
    const r = panel.getBoundingClientRect();
    return { x: r.x, y: r.y, width: r.width, height: r.height, viewport: [innerWidth, innerHeight], overflow: panel.scrollWidth > panel.clientWidth };
  });
  assert.equal(geometry.overflow, false); assert(geometry.x >= 0 && geometry.y >= 0);
  assert(geometry.x + geometry.width <= geometry.viewport[0] + 1 && geometry.y + geometry.height <= geometry.viewport[1] + 1);
  report.checks.push({ name, geometry });
}
ipcMain.handle('test:recovery-list', () => pending);
ipcMain.handle('test:recovery-dismiss', (_event, ids) => { report.dismissals.push(ids); pending = pending.filter(index => !ids.includes(`recovery-${index}`)); });
ipcMain.handle('test:recovery-start', (_event, args) => {
  report.starts.push(args);
  if (failStart) throw new Error('Synthetic admission failure');
  pending = pending.filter(index => args[0] !== `recovery-${index}`);
  return { turn: { id: `${args[0]}-turn-0002`, status: 'in_progress', items: [], items_view: 'full' } };
});
async function run() {
  fs.mkdirSync(output, { recursive: true });
  const preload = path.join(fixture, 'preload.cjs');
  await require('esbuild').build({ entryPoints: [path.join(__dirname, 'resize-e2e-preload.cjs')], outfile: preload, bundle: true,
    platform: 'node', format: 'cjs', external: ['electron'], define: { 'process.env.WUU_RENDERER_E2E_EXTENSION': JSON.stringify(path.join(__dirname, 'restart-recovery-e2e-preload.cjs')) } });
  await app.whenReady(); nativeTheme.themeSource = 'light';
  win = new BrowserWindow({ width: 1200, height: 850, show: true, webPreferences: { preload, sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  win.webContents.session.webRequest.onBeforeRequest(({ url }, done) => done({ cancel: /^https?:/.test(url) }));
  win.webContents.on('console-message', event => { if (event.level === 'error') report.errors.push(event.message); });
  await win.loadFile(path.join(desktop, 'out/renderer/index.html'));
  await until(() => evaluate(() => document.querySelectorAll('.restart-recovery-row').length === 3), 'recovery dialog');
  assert.equal(report.starts.length, 0);
  assert(await evaluate(() => document.querySelector('.restart-recovery-dialog .settings-button-primary').disabled));
  assert.equal(await evaluate(() => document.querySelectorAll('.restart-recovery-row input:checked').length), 0);
  await capture('light-default');
  await click('.restart-recovery-row input');
  await until(() => evaluate(() => !document.querySelector('.restart-recovery-dialog .settings-button-primary').disabled), 'selection');
  await capture('light-selected');
  await evaluate(() => { document.documentElement.dataset.theme = 'dark'; });
  await capture('dark-selected');
  win.setContentSize(430, 720);
  await evaluate(() => { document.documentElement.style.setProperty('--font-ui', '20px'); document.documentElement.style.fontSize = '20px'; });
  await capture('narrow-large');
  win.setContentSize(1200, 850);
  await evaluate(() => { document.documentElement.dataset.theme = 'light'; document.documentElement.style.removeProperty('--font-ui'); document.documentElement.style.removeProperty('font-size'); });
  await click('.restart-recovery-dialog .settings-button-primary');
  await until(() => evaluate(() => !document.querySelector('.restart-recovery-dialog')), 'selected tasks accepted');
  assert.equal(report.starts.length, 1); assert.equal(report.starts[0][0], 'recovery-0');
  assert.equal(report.starts[0][1], ''); assert.equal(report.starts[0][9], 'recovery-0-turn-0001');
  assert.deepEqual(report.dismissals[0], ['recovery-1', 'recovery-2']);
  report.checks.push('No silent sends; selected thread continues in place; unselected work is deferred');
  win.webContents.send('test:recovery-refresh');
  await pause(100);
  assert.equal(await evaluate(() => Boolean(document.querySelector('.restart-recovery-dialog'))), false);
  pending = [0, 1, 2]; win.webContents.send('test:recovery-refresh');
  await until(() => evaluate(() => document.querySelectorAll('.restart-recovery-row').length === 3), 'second recovery batch');
  await click('.restart-recovery-dialog .settings-button-ghost');
  await until(() => evaluate(() => !document.querySelector('.restart-recovery-dialog')), 'cancel');
  assert.equal(report.starts.length, 1); assert.deepEqual(pending, []);
  report.checks.push('Not now leaves all work paused; refresh does not reopen dismissed work');
  pending = [2]; failStart = true; win.webContents.send('test:recovery-refresh');
  await until(() => evaluate(() => document.querySelectorAll('.restart-recovery-row').length === 1), 'failed task batch');
  await click('.restart-recovery-row input'); await click('.restart-recovery-dialog .settings-button-primary');
  await until(() => evaluate(() => document.body.innerText.includes('部分任务未能恢复')), 'failure toast');
  assert.equal(await evaluate(() => document.querySelectorAll('.restart-recovery-row input:checked').length), 1);
  failStart = false; await click('.restart-recovery-dialog .settings-button-primary');
  await until(() => evaluate(() => !document.querySelector('.restart-recovery-dialog')), 'retry');
  assert.equal(report.starts.at(-1)[7].project_id, 'website-demo');
  report.checks.push('Failed admission preserves selection and retries in its original workspace');
  report.status = 'passed';
}
run().catch(error => { report.status = 'failed'; report.failure = error.stack; process.exitCode = 1; }).finally(() => {
  fs.mkdirSync(output, { recursive: true }); fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report)); if (win && !win.isDestroyed()) win.destroy(); app.exit(process.exitCode || 0);
});
