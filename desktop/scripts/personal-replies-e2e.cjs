// Production Personal renderer, synthetic transport. Pair with mosaic-host-e2e.mjs
// for real persistence/routing. Failure cases: lost ancestors, nested reply,
// missing pages, cross-discussion leakage, and drafts lost when reopening.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { app, BrowserWindow } = require('electron');
const output = path.resolve(__dirname, '../out/personal-replies-e2e');
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
const report = { scope: 'Real Electron renderer with synthetic transport; no live model', cases: [] };
let win;
const evaluate = fn => win.webContents.executeJavaScript(`(${fn})()`, true);
async function until(fn) {
  const end = Date.now() + 10000;
  while (Date.now() < end) {
    if (await evaluate(fn)) return;
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  throw new Error(`Timed out: ${fn}`);
}
app.whenReady().then(async () => {
  win = new BrowserWindow({ width: 1100, height: 800, show: true, webPreferences: { contextIsolation: true } });
  await win.loadFile(path.resolve(__dirname, '../out/personal-fixture/index.html'), { query: { replies: '1' } });
  await until(() => document.querySelector('[data-wuu-plugin="personal"]'));
  await evaluate(() => document.querySelector('[data-wuu-plugin="personal"]').click());
  await until(() => document.querySelector('.mosaic-main [data-action="reply-message"]'));
  await evaluate(() => document.querySelector('.mosaic-main [data-action="reply-message"]').click());
  await until(() => document.querySelector('.mosaic-replies [data-role]'));
  assert.equal(await evaluate(() => document.querySelector('.mosaic-replies [data-action="reply-message"]') !== null), false);
  assert.equal(await evaluate(() => document.querySelector('.mosaic-replies').textContent.includes('UNRELATED DISCUSSION')), false);
  for (let i = 0; i < 4; i++) {
    await evaluate(() => { const node = document.querySelector('.mosaic-replies .plugin-conversation-scroll'); node.scrollTop = 0; node.dispatchEvent(new Event('scroll')); });
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  await until(() => document.querySelector('.mosaic-replies').textContent.includes('DISCUSSION ROOT'));
  assert.equal(await evaluate(() => document.querySelectorAll('.mosaic-replies [data-role]').length), 25);
  report.cases.push('Canonical root and all 25 messages loaded from later target; unrelated discussion excluded; no nested Reply');
  await evaluate(() => {
    const input = document.querySelector('.mosaic-replies textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Retained discussion draft');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await evaluate(() => document.querySelector('[data-action="close-replies"]').click());
  await evaluate(() => document.querySelector('.mosaic-main [data-action="reply-message"]').click());
  await until(() => document.querySelector('.mosaic-replies textarea')?.value === 'Retained discussion draft');
  await evaluate(() => document.querySelector('.mosaic-replies .composer-send-button').click());
  await until(() => document.querySelector('.mosaic-replies').textContent.includes('Retained discussion draft'));
  await until(() => document.querySelector('.mosaic-replies').textContent.includes('这是合成演示回复'));
  report.cases.push('Draft survives reopen; user send and assistant response remain in discussion');
  // Finish history loads before measuring wheel movement: prepending a page
  // correctly restores its reading anchor and changes scrollTop independently.
  for (let i = 0; i < 4; i++) {
    await evaluate(() => { const node = document.querySelector('.mosaic-replies .plugin-conversation-scroll'); node.scrollTop = 0; node.dispatchEvent(new Event('scroll')); });
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  await until(() => document.querySelectorAll('.mosaic-replies [data-role]').length === 27);
  win.focus(); win.webContents.focus();
  await evaluate(() => {
    const node = document.querySelector('.mosaic-replies .plugin-conversation-scroll');
    node.scrollTop = node.scrollHeight / 2;
    return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  const wheel = await evaluate(() => {
    const node = document.querySelector('.mosaic-replies .plugin-conversation-scroll');
    node.dataset.wheelStart = String(node.scrollTop);
    const rect = node.getBoundingClientRect();
    return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2), top: node.scrollTop };
  });
  win.webContents.sendInputEvent({ type: 'mouseMove', x: wheel.x, y: wheel.y });
  win.webContents.sendInputEvent({ type: 'mouseWheel', x: wheel.x, y: wheel.y, deltaX: 0, deltaY: 250, canScroll: true });
  await until(() => { const node = document.querySelector('.mosaic-replies .plugin-conversation-scroll'); return node.scrollTop < Number(node.dataset.wheelStart); });
  report.cases.push('Native vertical mouse wheel moves the reply viewport');
  for (const [name, width, height] of [['wide', 1100, 800], ['narrow', 620, 740]]) {
    win.setSize(width, height);
    await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const geometry = await evaluate(() => {
      const panel = document.querySelector('.mosaic-replies').getBoundingClientRect();
      const scroll = document.querySelector('.mosaic-replies .plugin-conversation-scroll');
      return { width: innerWidth, left: panel.left, right: panel.right, viewport: scroll.clientHeight, content: scroll.scrollHeight };
    });
    assert(geometry.left >= 0 && geometry.right <= geometry.width && geometry.viewport > 0 && geometry.content > geometry.viewport);
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
    report.cases.push({ name, geometry });
  }
  report.status = 'passed';
}).catch(error => { report.status = 'failed'; report.error = error.stack; console.error(error); }).finally(() => {
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
  app.exit(report.status === 'passed' ? 0 : 1);
});
