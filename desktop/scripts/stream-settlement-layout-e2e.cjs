const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { app, BrowserWindow } = require('electron');
const output = path.resolve(__dirname, '../out/stream-settlement');
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1000, height: 900 });
  const report = [];
  for (const width of [1000, 420]) for (const size of [14, 20]) for (const theme of ['light', 'dark']) {
    win.setContentSize(width, 900);
    await win.loadURL(`${process.env.WUU_FIXTURE_ORIGIN || 'http://localhost:5173'}/dev/message-flow-reading/?surface=lifecycle&lateTerminal=1&size=${size}&theme=${theme}`);
    const evaluate = fn => win.webContents.executeJavaScript(`(${fn.toString()})()`);
    const wait = async predicate => {
      for (let attempt = 0; attempt < 200; attempt++) {
        if (await evaluate(predicate)) return;
        await new Promise(resolve => setTimeout(resolve, 25));
      }
      throw new Error('Timed out waiting for settlement fixture');
    };
    await wait(() => !!document.querySelector('.turn-process-entry-commentary .agent-block'));
    const measure = () => evaluate(async () => {
      await document.fonts.ready;
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const turn = document.querySelector('[data-turn-id="fixture-lifecycle"]');
      const top = turn.getBoundingClientRect().top;
      return {
        height: turn.getBoundingClientRect().height,
        paragraphs: [...turn.querySelectorAll('.agent-block p')].map(p => {
          const rect = p.getBoundingClientRect();
          return { top: rect.top - top, height: rect.height, left: rect.left, width: rect.width };
        }),
      };
    });
    const live = await measure();
    if (width === 1000 && size === 14 && theme === 'light') fs.writeFileSync(path.join(output, 'live.png'), (await win.webContents.capturePage()).toPNG());
    await evaluate(() => document.querySelector('[aria-label="流式状态"]').click());
    await wait(() => !!document.querySelector('.turn-answer-body .agent-message-actions button'));
    const settled = await measure();
    if (width === 1000 && size === 14 && theme === 'light') fs.writeFileSync(path.join(output, 'settled.png'), (await win.webContents.capturePage()).toPNG());
    report.push({width, size, theme, live, settled});
    fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
    assert.equal(live.paragraphs.length, 4);
    assert.equal(settled.paragraphs.length, 4);
    for (let i = 0; i < 4; i++) for (const key of ['top', 'height', 'left', 'width']) {
      assert.ok(Math.abs(live.paragraphs[i][key] - settled.paragraphs[i][key]) < 1,
        `Paragraph ${i} ${key} moved: ${JSON.stringify(report.at(-1))}`);
    }
    assert.ok(Math.abs(live.height - settled.height) < 1, `Turn height changed: ${JSON.stringify(report.at(-1))}`);
    console.log(`PASS settlement ${width}/${size}/${theme}`);
  }
  win.close(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
