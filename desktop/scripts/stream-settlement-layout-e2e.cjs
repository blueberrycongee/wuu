const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { app, BrowserWindow } = require('electron');
const output = path.resolve(process.env.WUU_SETTLEMENT_OUTPUT || path.join(__dirname, '../out/stream-settlement'));
fs.mkdirSync(output, { recursive: true });
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-settlement-'));
app.setPath('userData', profile);
app.on('window-all-closed', () => {});
const timeout = setTimeout(() => { console.error('Settlement check timed out'); app.exit(1); }, 180_000);
let server;
let win;
let caseName;
const report = [];

app.whenReady().then(async () => {
  // Own the server and dependency cache so this gate needs neither a running
  // desktop nor its profile, and cannot invalidate the desktop's Vite modules.
  let origin = process.env.WUU_FIXTURE_ORIGIN;
  if (!origin) {
    const { createServer } = await import('vite');
    const { default: react } = await import('@vitejs/plugin-react');
    server = await createServer({
      configFile: false,
      root: path.resolve(__dirname, '..'),
      cacheDir: path.resolve(__dirname, '../node_modules/.vite/stream-settlement'),
      plugins: [react()],
      optimizeDeps: { entries: ['dev/message-flow-reading/index.html'] },
      resolve: { dedupe: ['react', 'react-dom'] },
      server: {
        host: '127.0.0.1', port: 0,
        watch: { ignored: ['**/out/**', '**/build/**'] },
        fs: { allow: [path.resolve(__dirname, '../..')] },
      },
    });
    await server.listen();
    origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  }
  const evaluate = fn => win.webContents.executeJavaScript(`(${fn.toString()})()`);
  const wait = async predicate => {
    for (let attempt = 0; attempt < 400; attempt++) {
      if (await evaluate(predicate)) return;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error(`Timed out waiting for ${caseName}`);
  };
  const measure = () => evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const turn = document.querySelector('[data-turn-id="fixture-lifecycle"]');
    const top = turn.getBoundingClientRect().top;
    const rect = element => {
      const bounds = element.getBoundingClientRect();
      return { top: bounds.top - top, height: bounds.height, left: bounds.left, width: bounds.width };
    };
    const content = turn.querySelector('.agent-text');
    const actions = turn.querySelector('.agent-message-actions');
    return {
      height: turn.getBoundingClientRect().height,
      actionHeight: actions?.getBoundingClientRect().height ?? 0,
      followingTop: document.querySelector('[data-testid="following-turn"]').getBoundingClientRect().top - top,
      content: rect(content),
      blocks: [...content.querySelectorAll('p, h1, h2, h3, ul, ol, li, blockquote, pre, table')].map(element => ({
        tag: element.tagName, text: element.textContent, ...rect(element),
      })),
    };
  });
  const screenshot = name => win.webContents.capturePage().then(image => fs.writeFileSync(path.join(output, `${caseName}-${name}.png`), image.toPNG()));

  for (const width of [1000, 420]) for (const size of [14, 15, 20]) for (const theme of ['light', 'dark']) {
    for (const lateTerminal of [true, false]) for (const content of ['prose', 'markdown']) {
      caseName = `${width}-${size}-${theme}-${lateTerminal ? 'late' : 'early'}-${content}`;
      // Give each navigation its own renderer so the matrix does not retain
      // previous fixture documents and their Vite/React runtime state.
      if (win) win.destroy();
      win = new BrowserWindow({ show: false, width, height: 900, webPreferences: { backgroundThrottling: false } });
      win.setContentSize(width, 900);
      win.webContents.on('console-message', event => {
        if (event.level === 'error') console.error(event.message);
      });
      win.webContents.on('render-process-gone', (_event, details) => {
        console.error(`${caseName}: renderer exited`, details);
      });
      win.webContents.on('did-fail-load', (_event, code, description, url) => {
        console.error(`${caseName}: load failed`, { code, description, url });
      });
      const params = new URLSearchParams({ surface: 'lifecycle', live: '1', size, theme, content });
      if (lateTerminal) params.set('lateTerminal', '1');
      await win.loadURL(`${origin}/dev/message-flow-reading/?${params}`);
      await wait(() => !!document.querySelector('[data-turn-id="fixture-lifecycle"] [data-stream-state="streaming"]'));
      assert.equal(await evaluate(() => !!document.querySelector('.turn-process-entry-commentary .agent-block')), lateTerminal);
      const live = await measure();
      if (size >= 15) await screenshot('live');
      await evaluate(() => document.querySelector('[aria-label="流式状态"]').click());
      await wait(() => !!document.querySelector('.turn-answer-body [data-stream-state="settled"]') && !!document.querySelector('.turn-answer-body .agent-message-actions button'));
      const settled = await measure();
      if (size >= 15) await screenshot('settled');
      report.push({ width, size, theme, lateTerminal, content, live, settled });
      fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
      assert.ok(live.blocks.length >= 4, `${caseName}: missing reading content`);
      assert.equal(settled.blocks.length, live.blocks.length, `${caseName}: block count changed`);
      if (content === 'prose') assert.equal(live.blocks.length, 4);
      const pairs = [[live.content, settled.content], ...live.blocks.map((block, index) => [block, settled.blocks[index]])];
      for (const [before, after] of pairs) {
        assert.equal(after.tag, before.tag, `${caseName}: block type changed`);
        assert.equal(after.text, before.text, `${caseName}: text changed`);
        for (const key of ['top', 'height', 'left', 'width']) {
          assert.ok(Math.abs(before[key] - after[key]) < 1,
            `${caseName}: ${before.tag || 'content'} ${key} moved from ${before[key]} to ${after[key]}`);
        }
      }
      // Provisional commentary has no action placeholder by design. Only the
      // newly visible actions may grow that turn; an already classified answer
      // reserves them, so its following content must not move at all.
      const actionGrowth = settled.actionHeight - live.actionHeight;
      assert.ok(settled.actionHeight > 0, `${caseName}: missing answer actions`);
      if (lateTerminal) assert.equal(live.actionHeight, 0);
      else assert.ok(Math.abs(actionGrowth) < 1, `${caseName}: action slot changed`);
      for (const key of ['height', 'followingTop']) {
        assert.ok(Math.abs(settled[key] - live[key] - actionGrowth) < 1,
          `${caseName}: ${key} changed beyond the action row`);
      }
      console.log(`PASS settlement ${caseName}`);
    }
  }
}).catch(async error => {
  console.error(error);
  if (win && !win.isDestroyed()) {
    fs.writeFileSync(path.join(output, `${caseName || 'startup'}-failure.png`), (await win.webContents.capturePage()).toPNG());
  }
  process.exitCode = 1;
}).finally(async () => {
  if (win && !win.isDestroyed()) win.destroy();
  await server?.close();
  fs.rmSync(profile, { recursive: true, force: true });
  clearTimeout(timeout);
  app.exit(process.exitCode || 0);
});
