// Real Electron main/preload/renderer and OS shortcuts against disposable data.
// Run after building core + desktop, under X11 with xdotool and a window manager.
// No model inference, user profile, or runtime API mocking is used.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn, execFile } = require('node:child_process');
const { promisify } = require('node:util');
const { pathToFileURL } = require('node:url');
const { createHash } = require('node:crypto');
const desktop = path.resolve(__dirname, '..');
const phases = ['lifecycle', 'restart', 'disabled-restart'];
const output = path.resolve(process.env.WUU_QUICK_ACCESS_OUTPUT || path.join(desktop, 'out/e2e/quick-access'));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const xdotool = (...args) => promisify(execFile)('xdotool', args, { timeout: 10000, killSignal: 'SIGKILL' });

async function runDriver() {
  assert.equal(process.platform, 'linux', 'This driver verifies X11; macOS needs native acceptance separately');
  assert.ok(process.env.DISPLAY, 'Run under Xvfb or an X11 desktop');
  console.log('Starting quick-access E2E driver');
  await xdotool('version');
  let activeChild;
  const stopChild = () => {
    if (!activeChild?.pid) return;
    try { process.kill(-activeChild.pid, 'SIGKILL'); } catch (error) {
      if (error.code !== 'ESRCH') throw error;
    }
  };
  process.once('exit', stopChild);
  process.once('SIGTERM', () => process.exit(143));
  process.once('SIGINT', () => process.exit(130));
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
  try {
    for (const phase of phases) {
      console.log(`Starting quick access ${phase}`);
      const log = fs.openSync(path.join(output, `${phase}.log`), 'w');
      try {
        await new Promise((resolve, reject) => {
          // A separate process group lets the watchdog stop Electron and its
          // descendants even if a native call blocks the Electron event loop.
          const child = spawn(require('electron'), ['--no-sandbox', __filename], {
            detached: true, stdio: ['ignore', 'pipe', 'pipe'],
            env: { ...process.env, HOME: home, WUU_HOME: home, CODEX_HOME: path.join(home, '.codex'),
              WUU_QUICK_ACCESS_FIXTURE: fixture, WUU_QUICK_ACCESS_OUTPUT: output, WUU_QUICK_ACCESS_PHASE: phase,
              WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'),
              WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_SAFE_MODE: '1' },
          });
          activeChild = child;
          let settled = false;
          const finish = error => {
            if (settled) return;
            settled = true;
            clearTimeout(timer);
            child.stdout.removeAllListeners('data');
            child.stderr.removeAllListeners('data');
            child.stdout.destroy();
            child.stderr.destroy();
            try { stopChild(); } catch (stopError) { error ||= stopError; }
            activeChild = undefined;
            if (error) reject(error); else resolve();
          };
          const timer = setTimeout(() => {
            const message = `${phase} exceeded its 150-second process deadline\n`;
            fs.writeSync(log, message);
            process.stderr.write(message);
            finish(new Error(message.trim()));
          }, 150000);
          child.stdout.on('data', chunk => { fs.writeSync(log, chunk); process.stdout.write(chunk); });
          child.stderr.on('data', chunk => { fs.writeSync(log, chunk); process.stderr.write(chunk); });
          child.once('error', finish);
          child.once('close', (code, signal) => finish(code === 0 ? undefined
            : new Error(`${phase} failed: ${signal || `exit ${code}`}`)));
        });
      } finally { fs.closeSync(log); }
    }
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({
      platform: process.platform, transport: 'X11 globalShortcut + xdotool', macOS: 'not run',
      mainSHA256: createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/main/index.js'))).digest('hex'),
      phases: phases.map(phase => JSON.parse(fs.readFileSync(path.join(output, `${phase}.json`), 'utf8'))),
    }, null, 2));
  } finally { fs.rmSync(fixture, { recursive: true, force: true }); }
}

if (!process.versions.electron) {
  runDriver().then(() => process.exit(0), error => { console.error(error); process.exit(1); });
  return;
}

const { app, BrowserWindow, globalShortcut } = require('electron');
// Electron retains runtime flags in argv; keep phase selection independent of them.
const phase = process.env.WUU_QUICK_ACCESS_PHASE;
if (!phases.includes(phase)) {
  console.error('Unknown quick-access E2E phase');
  app.exit(1);
  return;
}
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
  await xdotool('key', '--clearmodifiers', chord);
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
const record = result => { results.push(result); console.log(`PASS ${phase}: ${result}`); };
console.log(`Loading Wuu for ${phase}`);
const timeout = setTimeout(() => { console.error('Quick-access E2E timed out'); app.exit(1); }, 120000);
const fail = error => {
  console.error(error);
  try { fs.writeFileSync(path.join(output, `${phase}-failure.txt`), String(error.stack || error)); }
  finally { clearTimeout(timeout); app.exit(1); }
};
// Assertions in native quit callbacks must fail CI instead of opening an
// unattended Electron error dialog that blocks the main-process watchdog.
process.once('uncaughtException', fail);
import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href).then(async () => {
  await until(() => main, 'main window');
  console.log(`${phase}: main window created`);
  await waitFor(main, () => document.querySelector('.composer textarea'));
  console.log(`${phase}: composer ready`);
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
    record('invalid and occupied bindings preserve previous binding and independent owner');

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
    record('packaged reserved keys report errors without zoom/reload/devtools; normal guards and zoom remain active after recording');
    await beginRecording(main);
    // Let Electron handle X11 shortcut events while xdotool sends the chord.
    await xdotool('key', '--clearmodifiers', 'ctrl+shift+space');
    await waitFor(main, () => document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'false');
    assert.equal((await snapshot(main)).shortcut, original);
    record('the already-owned global shortcut can be recorded again');


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
    record('recording rejects plain keys, Escape cancels without closing settings, conflicts surface in UI');

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
    record('record, disable, reset and repeated native summon preserve unsent main draft');
    record('unavailable workspace does not navigate or replace the existing draft');

    await openSettings(main);
    await beginRecording(main);
    const popout = await createPopout(main);
    await waitFor(main, () => document.querySelector('[data-testid="quick-access-record"]').getAttribute('aria-pressed') === 'false');
    record('focus transfer cancels recording before another window receives input');
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
    record('live/new popouts on top; native minimized popout restore; closed target falls back without navigating settings');

    for (const [theme, size, width] of [['light', 14, 1180], ['dark', 20, 820]]) {
      main.setSize(width, 900);
      await evaluate(main, () => window.dispatchEvent(new CustomEvent('wuu:open-settings', { detail: { page: 'appearance' } })));
      await waitFor(main, () => document.querySelector('[data-testid="settings-message-flow-font-size-input"]'));
      await click(main, `[data-testid="settings-theme-${theme}"]`);
      await evaluate(main, () => document.querySelector('[data-testid="settings-message-flow-font-size-input"]').focus());
      await input(main, 'a', ['control']);
      await main.webContents.insertText(String(size));
      await input(main, 'Tab');
      await waitFor(main, size => Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--font-ui')) === size, size);
      await until(async () => await evaluate(main, () => window.wuu.getMessageFlowFontSize()) === size, 'UI size persisted');
      await openSettings(main);
      await evaluate(main, async () => {
        document.querySelector('[data-testid="settings-quick-access"]').scrollIntoView({ block: 'center' });
        await document.fonts.ready;
      });
      // The missing-workspace case intentionally shows a toast. Capture after
      // it and the zoom readout dismiss, and after live appearance transitions.
      await waitFor(main, ({ theme, size }) => document.documentElement.dataset.theme === theme
        && Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--font-ui')) === size
        && !document.querySelector('.archive-tip, .desktop-zoom-readout')
        && !document.getAnimations().some(animation => Number.isFinite(animation.effect.getComputedTiming().iterations)
          && (animation.pending || animation.playState === 'running')), { theme, size });
      await evaluate(main, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      const rendered = await evaluate(main, () => ({
        theme: document.documentElement.dataset.theme,
        uiFontSize: Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--font-ui')),
        viewport: { width: innerWidth, height: innerHeight },
      }));
      const name = `${theme}-${size}-${width}`;
      fs.writeFileSync(path.join(output, `${name}.json`), JSON.stringify(rendered, null, 2));
      fs.writeFileSync(path.join(output, `${name}.png`), (await main.webContents.capturePage()).toPNG());
      record(`captured ${theme} at ${rendered.uiFontSize}px in ${rendered.viewport.width}px viewport`);
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
    record('fresh process restores binding and popout preference; disable persists');
  } else {
    assert.equal((await snapshot(main)).shortcutStatus, 'disabled');
    assert.equal((await snapshot(main)).popOutAlwaysOnTop, false);
    assert.equal(globalShortcut.isRegistered('Control+Shift+Space'), false);
    record('fresh process retains disabled shortcut and disabled on-top preference');
  }
  const owned = (await snapshot(main)).shortcut;
  if (!globalShortcut.isRegistered('Control+Alt+F9')) assert.equal(globalShortcut.register('Control+Alt+F9', () => {}), true);
  let quitChecked = false;
  app.on('before-quit', () => {
    if (quitChecked) return;
    quitChecked = true;
    if (owned) assert.equal(globalShortcut.isRegistered(owned), false);
    assert.equal(globalShortcut.isRegistered('Control+Alt+F9'), true, 'Quit cleanup must not unregister another owner');
    record('quit unregisters only the quick-access binding');
  });
  app.once('will-quit', () => {
    fs.writeFileSync(path.join(output, `${phase}.json`), JSON.stringify({ phase, electron: process.versions.electron, results }, null, 2));
    console.log(`PASS quick access ${phase}`);
    clearTimeout(timeout);
  });
  app.quit();
}).catch(fail);
