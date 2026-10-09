// Real Electron main/preload/renderer and OS shortcuts against disposable data.
// Run after building core + desktop, under X11 with xdotool and a window manager.
// No model inference, user profile, or runtime API mocking is used.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync, execFileSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { createHash } = require('node:crypto');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_QUICK_ACCESS_OUTPUT || path.join(desktop, 'out/e2e/quick-access'));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

if (!process.versions.electron) {
  assert.equal(process.platform, 'linux', 'This driver verifies X11; macOS needs native acceptance separately');
  assert.ok(process.env.DISPLAY, 'Run under Xvfb or an X11 desktop');
  execFileSync('xdotool', ['version']);
  fs.mkdirSync(output, { recursive: true });
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-quick-access-'));
  const home = path.join(fixture, 'home');
  const project = path.join(fixture, 'project');
  fs.mkdirSync(home); fs.mkdirSync(project);
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture',
    providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic', model: 'fixture' } },
    engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
  }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({
    projects: [{ id: 'quick-access', name: 'Quick access acceptance', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }],
    active_context: { kind: 'project', project_id: 'quick-access', cwd: project },
  }));
  let failed = false;
  try {
    for (const phase of ['lifecycle', 'restart', 'disabled-restart']) {
      const result = spawnSync(require('electron'), ['--no-sandbox', __filename, phase], {
        encoding: 'utf8', timeout: 150000,
        env: { ...process.env, HOME: home, WUU_HOME: home, CODEX_HOME: path.join(home, '.codex'),
          WUU_QUICK_ACCESS_FIXTURE: fixture, WUU_QUICK_ACCESS_OUTPUT: output,
          WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'),
          WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_SAFE_MODE: '1' },
      });
      fs.writeFileSync(path.join(output, `${phase}.log`), `${result.stdout || ''}${result.stderr || ''}`);
      process.stdout.write(result.stdout || ''); process.stderr.write(result.stderr || '');
      assert.equal(result.status, 0, `${phase} failed: ${result.error || result.signal || 'see log'}`);
    }
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({
      platform: process.platform, transport: 'X11 globalShortcut + xdotool', macOS: 'not run',
      mainSHA256: createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/main/index.js'))).digest('hex'),
      phases: ['lifecycle', 'restart', 'disabled-restart'].map(phase => JSON.parse(fs.readFileSync(path.join(output, `${phase}.json`), 'utf8'))),
    }, null, 2));
  } catch (error) { failed = true; console.error(error); }
  finally { fs.rmSync(fixture, { recursive: true, force: true }); }
  process.exit(failed ? 1 : 0);
}

const { app, BrowserWindow, globalShortcut } = require('electron');
const phase = process.argv[2];
const fixture = process.env.WUU_QUICK_ACCESS_FIXTURE;
const home = process.env.WUU_HOME;
const project = path.join(fixture, 'project');
const settingsPath = path.join(home, 'desktop-settings.json');
app.setPath('userData', path.join(fixture, 'profile'));
// The recorder must also work with release accelerator/security guards enabled.
Object.defineProperty(app, 'isPackaged', { get: () => true });
delete process.env.ELECTRON_RENDERER_URL;
const evaluate = (window, fn, arg) => window.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function until(check, label) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) { if (await check()) return; await delay(25); }
  throw new Error(`Timed out: ${label}`);
}
const waitFor = (window, fn, arg) => until(() => evaluate(window, fn, arg), String(fn));
const click = (window, selector) => evaluate(window, selector => document.querySelector(selector).click(), selector);
async function beginRecording(window) {
  window.show(); window.focus();
  await until(() => window.isFocused(), 'recorder window focused');
  await click(window, '[data-testid="quick-access-record"]');
  await waitFor(window, () => document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'true');
}
async function input(window, keyCode, modifiers = []) {
  window.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers });
  window.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
}
const snapshot = window => evaluate(window, () => window.wuu.getDesktopQuickAccess());
const update = (window, value) => evaluate(window, value => window.wuu.updateDesktopQuickAccess(value), value);
async function openSettings(window) {
  await evaluate(window, () => window.dispatchEvent(new CustomEvent('wuu:open-settings', { detail: { page: 'general' } })));
  await waitFor(window, () => document.querySelector('[data-testid="quick-access-record"]:not(:disabled)'));
}
async function back(window) {
  await click(window, '.settings-back-button');
  await waitFor(window, () => document.querySelector('.composer textarea'));
}
async function typeDraft(window, value) {
  window.show(); window.focus();
  await evaluate(window, () => document.querySelector('.composer textarea').focus());
  await window.webContents.insertText(value);
  await waitFor(window, value => document.querySelector('.composer textarea').value === value, value);
}
async function summon(window, chord) {
  execFileSync('xdotool', ['key', '--clearmodifiers', chord]);
  await until(() => window.isVisible() && !window.isMinimized() && window.isFocused(), 'native shortcut focused target');
}
async function createPopout(main) {
  const { windowID } = await evaluate(main, context => window.wuu.popOutSession({ kind: 'draft', context }),
    { kind: 'project', project_id: 'quick-access', cwd: project });
  const popout = BrowserWindow.getAllWindows().find(window => window.webContents.id === windowID);
  assert.ok(popout);
  await waitFor(popout, () => document.querySelector('.composer textarea'));
  return popout;
}
let main;
app.on('browser-window-created', (_event, window) => { main ||= window; });
const results = [];
const timeout = setTimeout(() => { console.error('Quick-access E2E timed out'); app.exit(1); }, 120000);
import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href).then(async () => {
  await until(() => main, 'main window');
  await waitFor(main, () => document.querySelector('.composer textarea'));
  if (phase === 'lifecycle') {
    assert.equal((await snapshot(main)).shortcutStatus, 'disabled');
    await typeDraft(main, 'Unsent main draft');
    await openSettings(main);
    await click(main, '[data-testid="quick-access-default"]');
    await until(async () => (await snapshot(main)).shortcutStatus === 'registered', 'default registered');
    const original = (await snapshot(main)).shortcut;
    assert.equal(globalShortcut.isRegistered(original), true);
    assert.equal((await update(main, { shortcut: 'A' })).error, 'invalid_shortcut');
    assert.equal(globalShortcut.isRegistered(original), true);
    assert.equal(globalShortcut.register('Control+Alt+F9', () => {}), true);
    assert.equal((await update(main, { shortcut: 'Control+Alt+F9' })).error, 'unavailable');
    assert.equal((await snapshot(main)).shortcut, original);
    assert.equal(globalShortcut.isRegistered(original), true);
    assert.equal(globalShortcut.isRegistered('Control+Alt+F9'), true);
    results.push('invalid and occupied bindings preserve previous binding and independent owner');

    await beginRecording(main);
    main.webContents.setZoomFactor(1.1);
    let navigations = 0;
    main.webContents.on('did-start-navigation', () => { navigations++; });
    for (const [keyCode, modifiers] of [
      ['0', ['control']], ['r', ['control']],
      ['i', ['control', 'shift']], ['F12', ['control']],
    ]) {
      await evaluate(main, () => {
        window.__quickAccessReserved = false;
        window.__quickAccessReservedCleanup?.();
        window.__quickAccessReservedCleanup = window.wuu.onDesktopQuickAccessRecorded(event => {
          if (event.error === 'reserved') window.__quickAccessReserved = true;
        });
      });
      await input(main, keyCode, modifiers);
      await waitFor(main, () => window.__quickAccessReserved === true
        && document.querySelector('[data-testid="settings-quick-access"] [role="alert"]'));
      assert.equal((await snapshot(main)).shortcut, original);
      assert.ok(Math.abs(main.webContents.getZoomFactor() - 1.1) < 0.0001, 'Reserved recording must not alter zoom');
      assert.equal(main.webContents.isDevToolsOpened(), false);
      assert.equal(navigations, 0, 'Reserved recording must not reload');
    }
    await input(main, 'Escape');
    await waitFor(main, () => document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'false');
    await input(main, '0', ['control']);
    await until(() => main.webContents.getZoomFactor() === 1, 'normal zoom still works after recording');
    await input(main, 'r', ['control']);
    await input(main, 'i', ['control', 'shift']);
    await evaluate(main, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal(navigations, 0);
    assert.equal(main.webContents.isDevToolsOpened(), false);
    results.push('packaged reserved keys report errors without zoom/reload/devtools; normal guards and zoom remain active after recording');
    await beginRecording(main);
    execFileSync('xdotool', ['key', '--clearmodifiers', 'ctrl+shift+space']);
    await waitFor(main, () => document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'false');
    assert.equal((await snapshot(main)).shortcut, original);
    results.push('the already-owned global shortcut can be recorded again');


    await beginRecording(main);
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'a' });
    main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'a' });
    await waitFor(main, () => document.querySelector('[data-testid="settings-quick-access"] [role="alert"]'));
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
    main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
    await waitFor(main, () => document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'false');
    await beginRecording(main);
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'F9', modifiers: ['control', 'alt'] });
    main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'F9', modifiers: ['control', 'alt'] });
    await waitFor(main, () => document.querySelector('[data-testid="settings-quick-access"] [role="alert"]')
      && document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'false');
    assert.equal((await snapshot(main)).shortcut, original);
    results.push('recording rejects plain keys, Escape cancels without closing settings, conflicts surface in UI');

    await beginRecording(main);
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: '8', modifiers: ['control', 'shift'] });
    main.webContents.sendInputEvent({ type: 'keyUp', keyCode: '8', modifiers: ['control', 'shift'] });
    await until(async () => (await snapshot(main)).shortcut === 'Control+Shift+8', 'recorded shortcut');
    assert.equal(globalShortcut.isRegistered(original), false);
    assert.equal(globalShortcut.isRegistered('Control+Shift+8'), true);
    await click(main, '[data-testid="quick-access-disable"]');
    await until(async () => (await snapshot(main)).shortcutStatus === 'disabled', 'disabled');
    assert.equal(globalShortcut.isRegistered('Control+Shift+8'), false);
    assert.equal(globalShortcut.isRegistered('Control+Alt+F9'), true);
    await click(main, '[data-testid="quick-access-default"]');
    await until(async () => (await snapshot(main)).shortcutStatus === 'registered', 'default restored');
    await click(main, '[data-testid="quick-access-default"]');
    assert.equal((await snapshot(main)).shortcutStatus, 'registered');
    await back(main);
    main.hide();
    await summon(main, 'ctrl+shift+space');
    assert.equal(await evaluate(main, () => document.querySelector('.composer textarea').value), 'Unsent main draft');
    fs.renameSync(project, `${project}-unavailable`);
    try {
      main.hide();
      await summon(main, 'ctrl+shift+space');
      assert.equal(await evaluate(main, () => document.querySelector('.composer textarea').value), 'Unsent main draft');
    } finally { fs.renameSync(`${project}-unavailable`, project); }
    results.push('record, disable, reset and repeated native summon preserve unsent main draft');
    results.push('unavailable workspace does not navigate or replace the existing draft');

    await openSettings(main);
    await beginRecording(main);
    const popout = await createPopout(main);
    await waitFor(main, () => document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'false');
    results.push('focus transfer cancels recording before another window receives input');
    await typeDraft(popout, 'Unsent popped-out draft');
    await openSettings(main);
    await click(main, '[data-testid="quick-access-on-top"]');
    await until(() => popout.isAlwaysOnTop(), 'existing popout on top');
    assert.equal(main.isAlwaysOnTop(), false);
    const secondPopout = await createPopout(main);
    assert.equal(secondPopout.isAlwaysOnTop(), true);
    secondPopout.close();
    popout.show(); popout.focus();
    await until(() => popout.isFocused(), 'popout focused');
    main.hide(); // Do not let the window manager focus main after minimizing the popout.
    popout.minimize();
    await until(() => popout.isMinimized(), 'popout minimized');
    await summon(popout, 'ctrl+shift+space');
    assert.equal(await evaluate(popout, () => document.querySelector('.composer textarea').value), 'Unsent popped-out draft');
    assert.equal((await evaluate(popout, () => window.wuu.popOutInit())).context.cwd, project);
    popout.close(); main.hide();
    await summon(main, 'ctrl+shift+space');
    assert.equal(await evaluate(main, () => !!document.querySelector('[data-testid="settings-quick-access"]')), true);
    results.push('live/new popouts on top; native minimized popout restore; closed target falls back without navigating settings');

    for (const [theme, size, width] of [['light', 14, 1180], ['dark', 20, 820]]) {
      main.setSize(width, 900);
      await evaluate(main, async ({ theme, size }) => {
        await window.wuu.setThemePreference(theme);
        await window.wuu.setMessageFlowFontSize(size);
        document.querySelector('[data-testid="settings-quick-access"]').scrollIntoView({ block: 'center' });
        await document.fonts.ready;
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      }, { theme, size });
      fs.writeFileSync(path.join(output, `${theme}-${size}-${width}.png`), (await main.webContents.capturePage()).toPNG());
    }
    await back(main);
    assert.equal(await evaluate(main, () => document.querySelector('.composer textarea').value), 'Unsent main draft');
    assert.equal((await evaluate(main, () => window.wuu.listThreads())).threads.length, 0, 'No message or session was created');
    assert.equal(JSON.parse(fs.readFileSync(settingsPath)).pop_out_always_on_top, true);
  } else if (phase === 'restart') {
    assert.equal((await snapshot(main)).shortcutStatus, 'registered');
    assert.equal(globalShortcut.isRegistered('Control+Shift+Space'), true);
    const popout = await createPopout(main);
    assert.equal(popout.isAlwaysOnTop(), true);
    await openSettings(main);
    await click(main, '[data-testid="quick-access-on-top"]');
    await until(() => !popout.isAlwaysOnTop(), 'live popout unpinned');
    await click(main, '[data-testid="quick-access-disable"]');
    await until(async () => (await snapshot(main)).shortcutStatus === 'disabled', 'shortcut disabled after restart');
    assert.equal(globalShortcut.isRegistered('Control+Shift+Space'), false);
    results.push('fresh process restores binding and popout preference; disable persists');
  } else {
    assert.equal((await snapshot(main)).shortcutStatus, 'disabled');
    assert.equal((await snapshot(main)).popOutAlwaysOnTop, false);
    assert.equal(globalShortcut.isRegistered('Control+Shift+Space'), false);
    results.push('fresh process retains disabled shortcut and disabled on-top preference');
  }
  const owned = (await snapshot(main)).shortcut;
  if (!globalShortcut.isRegistered('Control+Alt+F9')) assert.equal(globalShortcut.register('Control+Alt+F9', () => {}), true);
  let quitChecked = false;
  app.on('before-quit', () => {
    if (quitChecked) return;
    quitChecked = true;
    if (owned) assert.equal(globalShortcut.isRegistered(owned), false);
    assert.equal(globalShortcut.isRegistered('Control+Alt+F9'), true, 'Quit cleanup must not unregister another owner');
    results.push('quit unregisters only the quick-access binding');
    fs.writeFileSync(path.join(output, `${phase}.json`), JSON.stringify({ phase, electron: process.versions.electron, results }, null, 2));
    console.log(`PASS quick access ${phase}`);
    clearTimeout(timeout);
  });
  app.quit();
}).catch(error => {
  console.error(error);
  fs.writeFileSync(path.join(output, `${phase}-failure.txt`), String(error.stack || error));
  clearTimeout(timeout); app.exit(1);
});
