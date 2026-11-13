// Production task renderer; synthetic transport measures list refreshes and revision races.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');
const { app, BrowserWindow } = require('electron');
const output = path.resolve(process.env.WUU_TASK_SCALE_OUTPUT || path.join(__dirname, '../out/personal-task-scale-e2e'));
const fixture = path.resolve(__dirname, '../out/personal-fixture/index.html');
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
const report = { scope: 'Production Electron task UI with synthetic transport; no real backend or model', source_commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: __dirname, encoding: 'utf8' }).trim(), fixture_sha256: crypto.createHash('sha256').update(fs.readFileSync(fixture)).digest('hex'), electron: process.versions.electron, cases: [] };
let win;
const evaluate = fn => win.webContents.executeJavaScript(`(${fn})()`, true);
async function until(fn) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) { if (await evaluate(fn)) return; await new Promise(resolve => setTimeout(resolve, 20)); }
  throw new Error(`Timed out: ${fn}`);
}
app.whenReady().then(async () => {
  win = new BrowserWindow({ width: 1280, height: 900, show: false, webPreferences: { contextIsolation: true, backgroundThrottling: false, offscreen: true } });
  for (const [name, width, theme, font] of [['light-wide', 1280, 'light', '15'], ['dark-narrow-large', 720, 'dark', '20']]) {
    win.setSize(width, 900);
    const start = performance.now();
    await win.loadFile(fixture, { query: { taskScale: '1024', theme, font } });
    await until(() => document.querySelector('[data-wuu-plugin="personal"]'));
    await evaluate(() => document.querySelector('[data-wuu-plugin="personal"]').click());
    await until(() => document.querySelector('[data-action="toggle-personal-panel"]'));
    await evaluate(() => document.querySelector('[data-action="toggle-personal-panel"]').click());
    await until(() => document.querySelector('[data-personal-panel][data-panel-phase="open"]'));
    await until(() => document.querySelector('[data-task-id="scale-0"]'));
    const initialMs = performance.now() - start;
    await evaluate(() => document.querySelector('[data-action="show-more-activity"]').click());
    await until(() => document.querySelectorAll('[data-task-id]').length === 1024);
    await evaluate(() => { const task = window.__taskScale.tasks[0]; task.title = 'Updated from another instance'; task.revision++; window.__taskScale.refresh(); });
    await until(() => document.querySelector('[data-task-id="scale-0"]').textContent.includes('Updated from another instance'));
    await evaluate(() => { window.__taskScale.expectedReads = window.__taskScale.reads + 1; window.__taskScale.tasks[0].revision--; window.__taskScale.tasks[0].title = 'Stale response'; window.__taskScale.refresh(); });
    await until(() => window.__taskScale.reads >= window.__taskScale.expectedReads);
    await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert(await evaluate(() => document.querySelector('[data-task-id="scale-0"]').textContent.includes('Updated from another instance')), 'older poll must not regress the displayed revision');
    await evaluate(() => { window.__taskScale.tasks.splice(1, 1); window.__taskScale.refresh(); });
    await until(() => !document.querySelector('[data-task-id="scale-1"]'));
    const layout = await evaluate(() => ({ rows: document.querySelectorAll('[data-task-id]').length, width: innerWidth, documentWidth: document.documentElement.scrollWidth, reads: window.__taskScale.reads }));
    assert.equal(layout.rows, 1023); assert(layout.documentWidth <= layout.width, JSON.stringify(layout));
    await evaluate(() => document.querySelector('[data-task-id="scale-1023"]').scrollIntoView({ block: 'center' }));
    await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert(await evaluate(() => { const row = document.querySelector('[data-task-id="scale-1023"]'); const rect = row.getBoundingClientRect(); return rect.width > 0 && rect.top >= 0 && rect.bottom <= innerHeight && !row.closest('[hidden],[inert]'); }), 'last task must be visible and interactive');
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
    await evaluate(() => document.querySelector('[data-task-id="scale-1023"]').click());
    await until(() => document.body.textContent.includes('Scale goal 1023'));
    fs.writeFileSync(path.join(output, `${name}-detail.png`), (await win.webContents.capturePage()).toPNG());
    report.cases.push({ name, tasks: 1024, initial_render_ms: initialMs, elapsed_ms: performance.now() - start, layout, passed: true });
  }
  report.passed = true;
}).catch(error => { report.passed = false; report.error = error.stack; process.exitCode = 1; console.error(error); }).finally(() => { fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2)); win?.destroy(); app.quit(); });
