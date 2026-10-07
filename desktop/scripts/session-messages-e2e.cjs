// Real conversation renderers and split panes with isolated, synthetic history.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { app, BrowserWindow } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.join(desktop, 'out/e2e/session-messages');
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
app.whenReady().then(async () => {
  const timeout = setTimeout(() => app.exit(1), 120000);
  const results = [];
  let win;
  try {
    const { build } = await import('vite');
    const react = (await import('@vitejs/plugin-react')).default;
    await build({ configFile: false, root: path.join(desktop, 'dev/session-messages'), base: './',
      plugins: [react()], resolve: { dedupe: ['react', 'react-dom'] }, logLevel: 'warn',
      define: { __DESKTOP_VERSION__: JSON.stringify('fixture'), __DESKTOP_BUILD_DATE__: JSON.stringify('fixture'), __WUU_ACCOUNT_SERVER__: JSON.stringify('') },
      build: { outDir: path.join(output, 'renderer'), emptyOutDir: true } });
    win = new BrowserWindow({ width: 1180, height: 840, show: true, title: 'Wuu · Synthetic UI verification' });
    win.webContents.on('console-message', event => console.log('Renderer:', event.message));
    const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
    const ready = async () => evaluate(async () => {
      const deadline = Date.now() + 10000;
      while (!document.querySelector('.session-message-source')) {
        if (Date.now() > deadline) throw new Error('Missing session message entry');
        await new Promise(requestAnimationFrame);
      }
      await document.fonts.ready;
      await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame);
    });
    const capture = async name => fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
    for (const theme of ['light', 'dark']) for (const size of ['14', '20']) for (const width of [1180, 600]) {
      const name = `${theme}-${size}-${width}`;
      win.setContentSize(width, 820);
      await win.loadFile(path.join(output, 'renderer/index.html'), { query: { theme, size, lang: theme === 'light' ? 'en' : 'zh' } });
      await ready();
      app.focus({ steal: true }); win.focus(); win.webContents.focus();
      win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Tab' });
      win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Tab' });
      await evaluate(async () => { await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); });
      const before = await evaluate(async () => {
        const buttons = [...document.querySelectorAll('.session-message-source')];
        const button = buttons[0]; button.focus();
        await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame);
        const bounds = button.getBoundingClientRect();
        return { count: buttons.length, label: button.textContent, width: bounds.width, height: bounds.height,
          privateBody: document.body.innerText.includes('Private dispatch'),
          avatar: button.querySelector('.agent-avatar-mark')?.getBoundingClientRect().toJSON(),
          entry: bounds.toJSON(), outline: getComputedStyle(button).outlineStyle };
      });
      assert.equal(before.count, 2); assert.match(before.label, /5/); assert.equal(before.privateBody, false);
      assert.ok(before.avatar.width > 0 && before.avatar.right <= before.entry.right);
      assert.ok(before.width <= width - 32); assert.notEqual(before.outline, 'none');
      await capture(`${name}-compact`);
      await evaluate(() => document.querySelector('.session-message-source').click());
      await evaluate(async () => {
        while (!document.querySelector('.conversation-split-pane[data-thread-id="sidekick"]')) await new Promise(requestAnimationFrame);
        await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame);
      });
      const opened = await evaluate(() => {
        const source = document.querySelector('.session-message-source');
        const side = document.querySelector('.conversation-split-pane[data-thread-id="sidekick"]');
        const leadBody = document.querySelector('.conversation-split-pane[data-thread-id="lead"] .conversation-split-body');
        const sourceBounds = source.getBoundingClientRect();
        const bodyBounds = leadBody.getBoundingClientRect();
        const peer = side;
        const closeBounds = side.querySelector('.conversation-split-close').getBoundingClientRect();
        const sideSourceBounds = side.querySelector('.session-message-source').getBoundingClientRect();
        const sideBodyBounds = side.querySelector('.conversation-split-body').getBoundingClientRect();
        source.click();
        return { panes: document.querySelectorAll('.conversation-split-pane').length,
          closeBounds: closeBounds.toJSON(), sideSourceBounds: sideSourceBounds.toJSON(), sideBodyBounds: sideBodyBounds.toJSON(),
          side: side.innerText, retained: peer === document.querySelector('.conversation-split-pane[data-thread-id="sidekick"]'),
          sourceBounds: sourceBounds.toJSON(), sourceScroll: source.scrollWidth, sourceClient: source.clientWidth,
          sourceVisible: sourceBounds.left >= bodyBounds.left && sourceBounds.right + 5 <= bodyBounds.right,
          paneOverflow: [...document.querySelectorAll('.conversation-split-body')].some(body => body.scrollWidth > body.clientWidth + 1),
          bodyOverflow: document.documentElement.scrollWidth > innerWidth, privateBody: document.querySelector('.conversation-split-pane[data-thread-id="lead"]').innerText.includes('Private dispatch') };
      });
      assert.equal(opened.panes, 2); assert.match(opened.side, /Sidekick conversation/); assert.equal(opened.privateBody, false);
      assert.equal(opened.retained, true); assert.equal(opened.bodyOverflow, false);
      assert.equal(opened.sourceVisible, true, `Clipped entry: ${JSON.stringify(opened)}`);
      assert.equal(opened.paneOverflow, false, `Pane overflow: ${JSON.stringify(opened)}`);
      assert.ok(opened.sourceScroll <= opened.sourceClient + 1, `Entry overflow: ${JSON.stringify(opened)}`);
      assert.ok(opened.closeBounds.bottom <= opened.sideBodyBounds.top + 1, `Close covers the conversation: ${JSON.stringify(opened)}`);
      await capture(`${name}-opened`);
      await evaluate(() => {
        const body = document.querySelector('.conversation-split-pane[data-thread-id="sidekick"] .conversation-split-body');
        body.scrollTop = body.scrollHeight;
      });
      await evaluate(async () => { await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); });
      const scrolled = await evaluate(() => {
        const close = document.querySelector('.conversation-split-close');
        const body = document.querySelector('.conversation-split-pane[data-thread-id="sidekick"] .conversation-split-body');
        return { scrollTop: body.scrollTop, close: close.getBoundingClientRect().toJSON(),
          unobstructed: document.elementFromPoint(close.getBoundingClientRect().x + close.clientWidth / 2, close.getBoundingClientRect().y + close.clientHeight / 2)?.closest('button') === close };
      });
      assert.ok(scrolled.scrollTop > 0); assert.equal(scrolled.close.y, opened.closeBounds.y); assert.equal(scrolled.unobstructed, true);
      await capture(`${name}-scrolled`);
      await evaluate(() => document.querySelector('.conversation-split-close').click());
      await evaluate(async () => { await new Promise(requestAnimationFrame); });
      const closed = await evaluate(() => document.querySelectorAll('.conversation-split-pane').length);
      assert.equal(closed, 1);
      results.push({ name, before, opened, scrolled, closed });
    }
    win.setContentSize(600, 820);
    await win.loadFile(path.join(output, 'renderer/index.html'), { query: { theme: 'dark', size: '20', long: 'true' } });
    await ready();
    const long = await evaluate(() => {
      const name = document.querySelector('.session-message-name');
      return { truncated: name.scrollWidth > name.clientWidth, label: name.textContent,
        entryOverflow: document.querySelector('.session-message-source').scrollWidth > document.querySelector('.session-message-source').clientWidth };
    });
    assert.equal(long.truncated, true); assert.equal(long.entryOverflow, false);
    await evaluate(() => document.querySelector('.session-message-source').click());
    await evaluate(async () => { await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); });
    const title = await evaluate(() => {
      const title = document.querySelector('.conversation-split-title');
      const close = document.querySelector('.conversation-split-close');
      return { truncated: title.scrollWidth > title.clientWidth, separate: title.getBoundingClientRect().right < close.getBoundingClientRect().left };
    });
    assert.equal(title.truncated, true); assert.equal(title.separate, true);
    await capture('long-source'); results.push({ name: 'long-source', ...long, title });
    for (const kind of ['plain', 'empty']) {
      await win.loadFile(path.join(output, 'renderer/index.html'), { query: { theme: 'light', size: '14', [kind]: 'true' } });
      await ready();
      await evaluate(() => document.querySelector('.session-message-source').click());
      await evaluate(async () => { await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); });
      const pane = await evaluate(() => {
        const side = document.querySelector('.conversation-split-pane[data-thread-id="sidekick"]');
        const close = side.querySelector('.conversation-split-close').getBoundingClientRect();
        return { title: side.querySelector('.conversation-split-title').textContent.trim(),
          separate: close.bottom <= side.querySelector('.conversation-split-body').getBoundingClientRect().top + 1,
          overflow: side.scrollWidth > side.clientWidth + 1 };
      });
      assert.equal(pane.title, 'Sidekick'); assert.equal(pane.separate, true); assert.equal(pane.overflow, false);
      await capture(`${kind}-history`); results.push({ name: `${kind}-history`, ...pane });
    }
    await win.loadFile(path.join(output, 'renderer/index.html'), { query: { missing: 'true' } });
    await ready();
    const missing = await evaluate(() => {
      const button = document.querySelector('.session-message-source'); button.click();
      return { disabled: button.disabled, panes: document.querySelectorAll('.conversation-split-pane').length };
    });
    assert.equal(missing.disabled, true); assert.equal(missing.panes, 1);
    await capture('missing-source'); results.push({ name: 'missing-source', ...missing });
    console.log(`PASS: ${results.length} session message scenes. Evidence: ${output}`);
  } catch (error) { console.error(error); if (win) fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG()); process.exitCode = 1; }
  finally {
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(results, null, 2));
    clearTimeout(timeout); win?.destroy(); app.exit(process.exitCode || 0);
  }
});
