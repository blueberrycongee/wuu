// Production Personal UI with synthetic tasks. From the repository root:
// node desktop/scripts/build-personal-fixture.cjs
// node desktop/scripts/personal-task-scroll-e2e.cjs
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');
if (!process.versions.electron) {
  const result = require('node:child_process').spawnSync(require('electron'), [__filename], { stdio: 'inherit', timeout: 60000 });
  process.exit(result.status ?? 1);
}
const { app, BrowserWindow, session } = require('electron');
const output = path.resolve(process.env.WUU_TASK_SCROLL_OUTPUT || path.join(__dirname, '../out/personal-task-scroll'));
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-task-scroll-'));
app.setPath('userData', profile);
fs.mkdirSync(output, { recursive: true });
const report = { boundary: 'Production renderer, synthetic tasks, real Electron wheel and keyboard input', scenes: [] };
let win;
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
const settle = () => evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function until(fn) {
  await evaluate(fn => new Promise((resolve, reject) => {
    const check = new Function(`return (${fn})()`);
    const deadline = performance.now() + 10000;
    const tick = () => { if (check()) resolve(); else if (performance.now() > deadline) reject(new Error(fn)); else requestAnimationFrame(tick); };
    tick();
  }), String(fn));
}
async function click(selector) {
  await evaluate(selector => { const element = document.querySelector(selector); if (!element) throw new Error(selector); element.click(); }, selector);
  await settle();
}
async function geometry() {
  return evaluate(() => {
    const content = document.querySelector('[data-task-detail] .mosaic-detail-content');
    return {
      content: { ...content.getBoundingClientRect().toJSON(), top: content.scrollTop, height: content.clientHeight, total: content.scrollHeight },
      header: document.querySelector('.mosaic-detail-head').getBoundingClientRect().toJSON(),
      actions: document.querySelector('.mosaic-detail-actions').getBoundingClientRect().toJSON(),
      outerTop: document.querySelector('.mosaic-main > .plugin-conversation > .plugin-conversation-scroll').scrollTop,
      viewportHeight: innerHeight,
    };
  });
}
async function capture(name) {
  await settle();
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
}
app.whenReady().then(async () => {
  session.defaultSession.webRequest.onBeforeRequest((details, callback) => callback({ cancel: !['file:', 'data:', 'devtools:'].some(prefix => details.url.startsWith(prefix)) }));
  win = new BrowserWindow({ width: 1100, height: 900, show: true, webPreferences: { contextIsolation: true, sandbox: true, backgroundThrottling: false } });
  for (const scene of [
    { id: 'light', width: 1100, height: 800, query: {} },
    { id: 'dark-large-short', width: 680, height: 540, query: { theme: 'dark', font: '20' } },
  ]) {
    win.setContentSize(scene.width, scene.height);
    await win.loadFile(path.join(__dirname, '../out/personal-fixture/index.html'), { query: scene.query });
    await until(() => Boolean(window.mosaicFixture && document.querySelector('.mosaic-profile-trigger')));
    await evaluate(() => {
      const task = window.mosaicFixture.state().tasks.find(task => task.task_id === 'dinner');
      task.goal = Array.from({ length: 30 }, (_, i) => `Requirement ${i + 1}: 核对月份天数，列出独立依据并解释结果。`).join('\n\n');
      task.result = { summary: 'END OF TASK RESULT: 31 days.' };
      task.title = 'Long task scroll fixture';
      task.revision++;
      window.mosaicFixture.host.publishHostEvent({ type: 'routine-state-changed' });
    });
    await click('[data-action="toggle-personal-panel"]');
    await until(() => document.querySelector('.mosaic-sidebar')?.dataset.panelPhase === 'open');
    await until(() => document.querySelector('[data-task-id="dinner"]')?.textContent.includes('Long task scroll fixture'));
    await click('[data-task-id="dinner"]');
    await click('[data-task-detail] .user-message-expand-toggle');
    await until(() => document.querySelector('[data-task-detail]')?.textContent.includes('Requirement 30'));
    const before = await geometry();
    report.scenes.push({ scene, before });
    await capture(`${scene.id}-before`);
    assert(before.content.total > before.content.height + 100, 'Long task content must have a bounded scroll viewport');
    const x = Math.round(before.content.x + before.content.width / 2), y = Math.round(before.content.y + Math.min(60, before.content.height / 2));
    win.webContents.sendInputEvent({ type: 'mouseMove', x, y });
    win.webContents.sendInputEvent({ type: 'mouseWheel', x, y, deltaY: -400, deltaX: 0, canScroll: true });
    await until(() => document.querySelector('.mosaic-detail-content').scrollTop > 0);
    const afterWheel = await geometry();
    assert.equal(afterWheel.outerTop, before.outerTop, 'Wheel input must stay inside task details');
    await evaluate(() => document.querySelector('.mosaic-detail-content').focus());
    win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'End' });
    win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'End' });
    await until(() => { const e = document.querySelector('.mosaic-detail-content'); return e.scrollTop + e.clientHeight >= e.scrollHeight - 2; });
    const end = await geometry();
    assert.equal(end.header.y, before.header.y, 'Back control must stay fixed');
    assert.equal(end.actions.y, before.actions.y, 'Return-to-chat action must stay fixed');
    assert(end.actions.bottom <= end.viewportHeight, 'Return-to-chat action must stay visible');
    await capture(`${scene.id}-end`);
    Object.assign(report.scenes.at(-1), { afterWheel, end });
    await click('[data-action="close-detail"]');
    await until(() => !document.querySelector('[data-task-detail]'));
    await click('[data-task-id="reading"]');
    const short = await geometry();
    assert(short.actions.bottom <= short.viewportHeight, 'Short task actions must also remain reachable');
    await click('[data-action="open-main-chat"]');
    assert(await evaluate(() => document.activeElement.matches('.mosaic-main textarea')));
  }
  report.passed = true;
}).catch(async error => { report.passed = false; report.error = error.stack; if (win) await capture('failure').catch(() => {}); console.error(error); }).finally(() => {
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  win?.destroy();
  fs.rmSync(profile, { recursive: true, force: true });
  app.exit(report.passed ? 0 : 1);
});
