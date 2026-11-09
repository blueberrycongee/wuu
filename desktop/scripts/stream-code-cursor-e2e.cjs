// Checks where the streaming cursor paints while a fenced code block is the
// last visible block of a live answer. The bar must sit right after the final
// code character, inside the code card, and settling must not move the card.
// Writes measurements and one screenshot per case under out/stream-code-cursor/.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { app, BrowserWindow } = require('electron');
const output = path.resolve(process.env.WUU_CODE_CURSOR_OUTPUT || path.join(__dirname, '../out/stream-code-cursor'));
fs.mkdirSync(output, { recursive: true });
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-code-cursor-'));
app.setPath('userData', profile);
app.on('window-all-closed', () => {});
const timeout = setTimeout(() => { console.error('Code cursor check timed out'); app.exit(1); }, 180_000);
const samples = ['open-code', 'open-code-newline', 'open-code-plain', 'open-code-empty', 'open-code-list', 'closed-code', 'closed-code-blank'];
let server;
let win;
let caseName;
const report = [];

app.whenReady().then(async () => {
  let origin = process.env.WUU_FIXTURE_ORIGIN;
  if (!origin) {
    const { createServer } = await import('vite');
    const { default: react } = await import('@vitejs/plugin-react');
    server = await createServer({
      configFile: false,
      root: path.resolve(__dirname, '..'),
      cacheDir: path.resolve(__dirname, '../node_modules/.vite/stream-code-cursor'),
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
  win = new BrowserWindow({ show: false, width: 1000, height: 700, webPreferences: { backgroundThrottling: false } });
  win.webContents.on('console-message', event => {
    if (event.level === 'error') console.error(event.message);
  });
  const evaluate = fn => win.webContents.executeJavaScript(`(${fn.toString()})()`);
  const measure = () => evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const surface = document.querySelector('[data-testid="reading-answer"] .streaming-markdown');
    const cursors = [...surface.querySelectorAll('.stream-cursor')];
    const cursor = cursors[0];
    const after = getComputedStyle(cursor, '::after');
    const slot = cursor.getBoundingClientRect();
    // The visible bar is the ::after box. Resolve its geometry from the slot
    // and computed insets so the check follows the stylesheet.
    const barLeft = slot.left + parseFloat(after.left);
    const barHeight = parseFloat(after.height);
    const barTop = after.top === 'auto'
      ? slot.bottom - parseFloat(after.bottom) - barHeight
      : slot.top + parseFloat(after.top);
    const blocks = surface.querySelectorAll('.rich-code-block');
    const block = blocks[blocks.length - 1];
    const pre = block.querySelector('pre');
    const code = pre.querySelector('code');
    const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
    let lastChar;
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const index = node.data.search(/\S\s*$/);
      if (index >= 0) lastChar = { node, index };
    }
    let charRect;
    if (lastChar) {
      const range = document.createRange();
      range.setStart(lastChar.node, lastChar.index);
      range.setEnd(lastChar.node, lastChar.index + 1);
      const rect = range.getBoundingClientRect();
      charRect = { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom };
    }
    const box = element => {
      const rect = element.getBoundingClientRect();
      return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, height: rect.height };
    };
    const preStyle = getComputedStyle(pre);
    return {
      cursorCount: cursors.length,
      cursorInPre: pre.contains(cursor),
      cursorInCode: code.contains(cursor),
      bar: { left: barLeft, top: barTop, bottom: barTop + barHeight },
      charRect,
      preContentLeft: pre.getBoundingClientRect().left + parseFloat(preStyle.paddingLeft) + parseFloat(preStyle.borderLeftWidth),
      block: box(block),
      pre: box(pre),
      surface: box(surface),
      codeText: code.textContent,
    };
  });
  const setLive = live => evaluate(new Function(`
    const toggle = document.querySelector('input[aria-label="流式状态"]');
    if (toggle.checked !== ${live}) toggle.click();
  `));
  const screenshot = () => win.webContents.capturePage().then(image => fs.writeFileSync(path.join(output, `${caseName}.png`), image.toPNG()));

  for (const width of [1000, 420]) for (const size of [15, 20]) for (const theme of ['light', 'dark']) for (const stream of samples) {
    caseName = `${stream}-${width}-${size}-${theme}`;
    win.setContentSize(width, 700);
    const params = new URLSearchParams({ surface: 'stream', live: '1', size, theme, stream });
    await win.loadURL(`${origin}/dev/message-flow-reading/index.html?${params}`);
    const live = await measure();
    await screenshot();
    await setLive(false);
    const settled = await measure();
    report.push({ caseName, live, settled });

    assert.equal(live.cursorCount, 1, `${caseName}: one cursor`);
    assert.equal(live.cursorInPre, true, `${caseName}: cursor belongs to the trailing code block`);
    assert.equal(live.cursorInCode, false, `${caseName}: cursor stays outside the copied code element`);
    assert.ok(!live.codeText.includes('\uE000'), `${caseName}: no cursor sentinel in code`);
    const tolerance = 1.5;
    if (live.charRect) {
      const gap = live.bar.left - live.charRect.right;
      assert.ok(gap >= 0 && gap <= 4 + tolerance, `${caseName}: bar follows the last code character (gap ${gap.toFixed(2)}px)`);
      const center = (live.bar.top + live.bar.bottom) / 2;
      assert.ok(center >= live.charRect.top && center <= live.charRect.bottom,
        `${caseName}: bar shares the last code line (bar ${live.bar.top.toFixed(1)}-${live.bar.bottom.toFixed(1)}, char ${live.charRect.top.toFixed(1)}-${live.charRect.bottom.toFixed(1)})`);
    } else {
      const gap = live.bar.left - live.preContentLeft;
      assert.ok(gap >= 0 && gap <= 4 + tolerance, `${caseName}: empty block starts the bar at the code inset (gap ${gap.toFixed(2)}px)`);
    }
    assert.ok(live.bar.top >= live.pre.top && live.bar.bottom <= live.pre.bottom + tolerance,
      `${caseName}: bar stays inside the code surface`);
    assert.ok(Math.abs(live.block.height - settled.block.height) < 0.5, `${caseName}: settling keeps the code card height`);
    assert.ok(Math.abs(live.surface.height - settled.surface.height) < 0.5, `${caseName}: settling keeps the message height`);
  }
  fs.writeFileSync(path.join(output, 'report.json'), `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Checked ${report.length} code cursor cases; artifacts in ${output}`);
}).then(async () => {
  clearTimeout(timeout);
  await server?.close();
  app.exit(0);
}).catch(async error => {
  console.error(error);
  if (report.length || caseName) fs.writeFileSync(path.join(output, 'report.json'), `${JSON.stringify(report, null, 2)}\n`);
  await server?.close();
  app.exit(1);
});
