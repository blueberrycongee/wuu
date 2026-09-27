// Full renderer + native Electron key events; the preload uses synthetic data.
// Contracts: opt-in persistence, draft preservation, input/IME/modal ownership,
// sequence cancellation, scrolling, and navigation through existing actions.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow, Menu } = require("electron");

const desktop = path.resolve(__dirname, "..");
const evidence = fs.mkdtempSync(path.join(desktop, "out", "vim-navigation-e2e-"));
app.setPath("userData", path.join(evidence, "profile"));
process.env.WUU_RESIZE_E2E_TURNS = "3";
process.env.WUU_RESIZE_E2E_CWD = path.resolve(desktop, "..");

app.whenReady().then(async () => {
  const timeout = setTimeout(() => app.exit(1), 180000);
  const win = new BrowserWindow({ width: 1180, height: 850, show: true,
    webPreferences: { preload: path.join(__dirname, "resize-e2e-preload.cjs"), sandbox: false, backgroundThrottling: false } });
  Menu.setApplicationMenu(null);
  const evaluate = (fn) => win.webContents.executeJavaScript(`(${fn})()`);
  const waitFor = async (fn) => {
    const end = Date.now() + 20000;
    while (!await evaluate(fn)) {
      if (Date.now() > end) throw new Error(`Timed out: ${fn}`);
      await new Promise(resolve => setTimeout(resolve, 30));
    }
  };
  const key = async (keyCode, modifiers = []) => {
    win.webContents.sendInputEvent({ type: "keyDown", keyCode, modifiers });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
    await evaluate(async () => { await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); });
  };
  const normal = () => evaluate(() => document.activeElement?.blur());
  const leader = async (suffix) => { await normal(); await key("Space"); await key(suffix); };
  try {
    if (process.env.WUU_VIM_E2E_URL) await win.loadURL(process.env.WUU_VIM_E2E_URL);
    else await win.loadFile(path.join(desktop, "out/renderer/index.html"));
    win.focus();
    await waitFor(() => Boolean(document.querySelector('.scroll-region')));
    console.log('Vim E2E: renderer ready');
    assert.equal(await evaluate(() => Boolean(document.querySelector('[data-vim-mode]'))), false);
    await normal(); await key('/');
    assert.equal(await evaluate(() => Boolean(document.querySelector('.conversation-search-dialog'))), false, 'Vim navigation is opt-in');
    await evaluate(() => document.querySelector('.sidebar-account-trigger').click());
    await waitFor(() => Boolean(document.querySelector('[data-settings-page="providers"]')));
    await evaluate(() => document.querySelector('[data-settings-page="providers"]').click());
    await waitFor(() => Boolean(document.querySelector('.settings-shell')));
    await evaluate(() => [...document.querySelectorAll('.settings-nav button')].find(el => /Keyboard|快捷键/.test(el.textContent)).click());
    await waitFor(() => Boolean(document.querySelector('[data-testid="vim-navigation-toggle"]')));
    await evaluate(() => document.querySelector('[data-testid="vim-navigation-toggle"]').click());
    await evaluate(() => document.querySelector('.settings-back-button').click());
    await waitFor(() => Boolean(document.querySelector('[data-vim-mode]')));
    console.log('Vim E2E: enabled through settings');
    await normal();
    await key('g'); await key('g');
    await waitFor(() => document.querySelector('.scroll-region').scrollTop === 0);
    await key('j');
    assert.ok(await evaluate(() => document.querySelector('.scroll-region').scrollTop > 0));
    await key('g', ['shift']);
    await waitFor(() => { const el = document.querySelector('.scroll-region'); return el.scrollHeight - el.clientHeight - el.scrollTop < 2; });
    await key('u', ['control']);
    assert.ok(await evaluate(() => { const el = document.querySelector('.scroll-region'); return el.scrollHeight - el.clientHeight - el.scrollTop > 100; }));
    const beforeRepeat = await evaluate(() => document.querySelector('.scroll-region').scrollTop);
    await key('g');
    await evaluate(() => document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'g', repeat: true, bubbles: true, cancelable: true })));
    assert.equal(await evaluate(() => document.querySelector('.scroll-region').scrollTop), beforeRepeat, 'holding g does not act as gg');
    await key('Escape');
    console.log('Vim E2E: reading keys passed');
    await key('i');
    await waitFor(() => document.activeElement?.tagName === 'TEXTAREA');
    await win.webContents.insertText('draft j k gg / untouched');
    const protectedInput = await evaluate(() => {
      const input = document.activeElement;
      const event = new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true });
      input.dispatchEvent(event);
      return { value: input.value, prevented: event.defaultPrevented, search: Boolean(document.querySelector('.conversation-search-dialog')) };
    });
    assert.deepEqual(protectedInput, { value: 'draft j k gg / untouched', prevented: false, search: false });
    await key('Escape');
    assert.notEqual(await evaluate(() => document.activeElement?.tagName), 'TEXTAREA');
    assert.equal(await evaluate(() => {
      const event = new KeyboardEvent('keydown', { key: '/', isComposing: true, bubbles: true, cancelable: true });
      document.body.dispatchEvent(event); return event.defaultPrevented;
    }), false);
    await key('/');
    await waitFor(() => Boolean(document.querySelector('.conversation-search-dialog')));
    assert.equal(await evaluate(() => {
      const event = new KeyboardEvent('keydown', { key: 'j', bubbles: true, cancelable: true });
      document.body.dispatchEvent(event); return event.defaultPrevented;
    }), false, 'modal owns the keyboard even if focus moves outside');
    await key('Escape');
    await waitFor(() => !document.querySelector('.conversation-search-dialog'));
    await normal();
    await key('g');
    await evaluate(() => {
      document.querySelector('textarea').focus();
      document.querySelector('textarea').blur();
    });
    await key('t');
    assert.equal(await evaluate(() => document.querySelector('textarea')?.value), 'draft j k gg / untouched', 'focus change cancels a pending prefix');
    await key('Space'); await key('Escape'); await key('n');
    assert.equal(await evaluate(() => document.querySelector('textarea')?.value), 'draft j k gg / untouched');
    await key('Space');
    await waitFor(() => Boolean(document.querySelector('.vim-prefix-hints')));
    await waitFor(() => !document.querySelector('.vim-prefix-hints'));
    await key('n');
    assert.equal(await evaluate(() => document.querySelector('textarea')?.value), 'draft j k gg / untouched', 'expired prefixes do not execute');
    console.log('Vim E2E: input, IME, modal, and prefix boundaries passed');
    await leader('n');
    await waitFor(() => document.activeElement?.tagName === 'TEXTAREA' && document.activeElement.value === '');
    await key('Escape');
    await key('g'); await key('T', ['shift']);
    await waitFor(() => document.querySelector('textarea')?.value === 'draft j k gg / untouched');
    await evaluate(() => document.querySelector('[aria-label="Open related session"], [aria-label="打开关联会话"]')?.click());
    await waitFor(() => Boolean(document.querySelector('.conversation-split-pane.active')));
    await normal(); await key('i');
    await waitFor(() => document.activeElement?.closest('.conversation-split-pane')?.classList.contains('active'));
    await key('Escape');
    assert.notEqual(await evaluate(() => document.activeElement?.tagName), 'TEXTAREA', 'Escape leaves the split composer');
    await evaluate(() => document.querySelectorAll('.conversation-split-pane')[1].dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })));
    await normal(); await key('i');
    await waitFor(() => document.activeElement?.closest('.conversation-split-pane') === document.querySelectorAll('.conversation-split-pane')[1]);
    await key('Escape');
    await evaluate(() => document.querySelector('.conversation-split-close').click());
    await waitFor(() => !document.querySelector('.conversation-split-pane'));
    console.log('Vim E2E: draft preservation and split focus passed');
    await leader('f');
    await waitFor(() => Boolean(document.querySelector('.workspace-right-panel')));
    await leader('t');
    await waitFor(() => Boolean(document.querySelector('.xterm')));
    await evaluate(() => document.querySelector('.xterm-helper-textarea').focus());
    assert.equal(await evaluate(() => {
      const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
      const input = document.activeElement; input.dispatchEvent(event); return document.activeElement === input;
    }), true, 'terminal Escape stays in the terminal');
    await leader('w');
    await normal(); await key('?');
    await waitFor(() => Boolean(document.querySelector('[data-testid="vim-navigation-toggle"]')));
    console.log('Vim E2E: workspace tools passed');
    const screenshots = [];
    for (const [theme, width, font] of [['light', 1180, 14], ['dark', 760, 20]]) {
      win.setContentSize(width, 850);
      await win.webContents.executeJavaScript(`document.documentElement.dataset.theme = '${theme}'; document.documentElement.style.setProperty('--font-ui', '${font}px')`);
      await evaluate(async () => {
        await document.fonts.ready;
        for (let frame = 0; frame < 3; frame++) {
          document.getAnimations().filter(animation => animation.effect.getComputedTiming().iterations !== Infinity).forEach(animation => animation.finish());
          await new Promise(requestAnimationFrame);
        }
      });
      const file = `shortcuts-${theme}-${width}-${font}.png`;
      fs.writeFileSync(path.join(evidence, file), (await win.webContents.capturePage()).toPNG());
      screenshots.push(file);
      assert.ok(await evaluate(() => [...document.querySelectorAll('.vim-key')].every(el => el.getBoundingClientRect().right <= innerWidth)), 'shortcut labels fit the viewport');
    }
    await evaluate(() => document.querySelector('.settings-back-button').click());
    await normal(); await key('Space');
    await evaluate(async () => {
      document.getAnimations().filter(animation => animation.effect.getComputedTiming().iterations !== Infinity).forEach(animation => animation.finish());
      await new Promise(requestAnimationFrame);
    });
    fs.writeFileSync(path.join(evidence, 'leader-hints.png'), (await win.webContents.capturePage()).toPNG());
    screenshots.push('leader-hints.png');
    await win.reload();
    await waitFor(() => Boolean(document.querySelector('[data-vim-mode]')));
    await normal(); await key('?');
    await waitFor(() => Boolean(document.querySelector('[data-testid="vim-navigation-toggle"]')));
    await evaluate(() => document.querySelector('[data-testid="vim-navigation-toggle"]').click());
    await win.reload();
    await waitFor(() => Boolean(document.querySelector('.scroll-region')));
    assert.equal(await evaluate(() => Boolean(document.querySelector('[data-vim-mode]'))), false, 'disabling Vim mode survives reload');
    fs.writeFileSync(path.join(evidence, 'result.json'), JSON.stringify({ passed: true, screenshots }, null, 2));
    console.log(`PASS Vim navigation E2E: ${evidence}`);
    clearTimeout(timeout); win.destroy(); app.quit();
  } catch (error) {
    console.error(error);
    fs.writeFileSync(path.join(evidence, 'failure.png'), (await win.webContents.capturePage()).toPNG());
    console.error(`Evidence: ${evidence}`); app.exit(1);
  }
});
