// Build the core and desktop first, then run with Electron. Real production
// main/preload/renderer with packaged guards, disposable data, and no inference.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.join(desktop, 'out/e2e/desktop-zoom');
fs.mkdirSync(output, { recursive: true });
const fixture = fs.mkdtempSync(path.join(output, 'fixture-'));
const home = path.join(fixture, 'home');
const project = path.join(fixture, 'zoom-project');
fs.mkdirSync(home);
fs.mkdirSync(project);
app.setPath('userData', path.join(fixture, 'profile'));
// Exercise the release menu/shortcut policy without producing a signed bundle.
Object.defineProperty(app, 'isPackaged', { get: () => true });
Object.assign(process.env, {
  HOME: home, WUU_HOME: home, CODEX_HOME: path.join(home, '.codex'),
  WUU_DESKTOP_CORE: path.join(desktop, 'build/bin/wuu-core'),
  WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_SAFE_MODE: '1',
});
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
  default_provider: 'fixture',
  providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic', model: 'fixture' } },
  engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
}));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({
  projects: [{ id: 'zoom-project', name: 'Zoom acceptance', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }],
  active_context: { kind: 'project', project_id: 'zoom-project', cwd: project },
}));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn, arg) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await evaluate(win, fn, arg)) return;
    await delay(20);
  }
  throw new Error(`Timed out: ${fn}`);
}
const modifier = process.platform === 'darwin' ? 'meta' : 'control';
async function key(win, keyCode, modifiers = [modifier]) {
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
}
async function zoom(win, keyCode, expected, modifiers) {
  await key(win, keyCode, modifiers);
  await waitFor(win, expected => document.querySelector('.desktop-zoom-readout')?.textContent === `${expected}%`, expected);
  assert.ok(Math.abs(win.webContents.getZoomFactor() - expected / 100) < 0.001);
}
let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('Zoom E2E timed out'); app.exit(1); }, 120000);
const results = [];
import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href).then(async () => {
  while (!main) await delay(20);
  await waitFor(main, () => document.querySelector('.composer textarea'));
  main.show(); main.focus();
  await evaluate(main, () => document.querySelector('.composer textarea').focus());
  await zoom(main, '0', 100);
  await zoom(main, '=', 110);
  await zoom(main, '+', 120, [modifier, 'shift']);
  await zoom(main, '-', 110);
  await zoom(main, '0', 100);
  // Plain typing must still reach a focused composer.
  await key(main, '=', []);
  await waitFor(main, () => document.querySelector('.composer textarea').value.includes('='));
  assert.equal(main.webContents.getZoomFactor(), 1);
  for (let i = 0; i < 15; i++) await key(main, '=');
  await waitFor(main, () => document.querySelector('.desktop-zoom-readout')?.textContent === '200%');
  assert.ok(Math.abs(main.webContents.getZoomFactor() - 2) < 0.001);
  for (let i = 0; i < 25; i++) await key(main, '-');
  await waitFor(main, () => document.querySelector('.desktop-zoom-readout')?.textContent === '50%');
  assert.ok(Math.abs(main.webContents.getZoomFactor() - 0.5) < 0.001);
  await zoom(main, '0', 100);
  await zoom(main, '=', 110);
  // Recovery/reload uses the same production preload and persisted preference.
  await main.loadFile(path.join(desktop, 'out/renderer/index.html'));
  await waitFor(main, () => document.querySelector('.composer textarea'));
  assert.ok(Math.abs(main.webContents.getZoomFactor() - 1.1) < 0.001);
  assert.equal(await evaluate(main, () => !!document.querySelector('.desktop-zoom-readout')), false);
  results.push('packaged shortcuts, typing, bounds, reset, reload persistence');

  for (const theme of ['light', 'dark']) {
    for (const size of [14, 20]) {
      for (const width of [1440, 820]) {
        main.setSize(width, 900);
        await evaluate(main, ({ theme, size }) => {
          document.documentElement.dataset.theme = theme;
          document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`);
          document.documentElement.style.setProperty('--appearance-scale', String(size / 14));
        }, { theme, size });
        await zoom(main, '0', 100);
        await zoom(main, '=', 110);
        await evaluate(main, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        const rect = await evaluate(main, () => {
          const node = document.querySelector('.desktop-zoom-readout');
          const box = node.getBoundingClientRect();
          return { x: box.x, y: box.y, width: box.width, height: box.height, viewportWidth: innerWidth, viewportHeight: innerHeight,
            hit: document.elementFromPoint(innerWidth / 2, innerHeight / 2)?.className,
            fontSize: getComputedStyle(node).fontSize };
        });
        assert.ok(Math.abs(rect.x + rect.width / 2 - rect.viewportWidth / 2) < 1);
        assert.ok(Math.abs(rect.y + rect.height / 2 - rect.viewportHeight / 2) < 1);
        assert.notEqual(rect.hit, 'desktop-zoom-readout', 'Readout must not intercept clicks');
        const name = `${theme}-${size}px-${width}`;
        fs.writeFileSync(path.join(output, `${name}.png`), (await main.webContents.capturePage()).toPNG());
        results.push({ name, ...rect });
      }
    }
  }
  await waitFor(main, () => !document.querySelector('.desktop-zoom-readout'));
  results.push('readout auto-dismissed');
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ electron: process.versions.electron, results }, null, 2));
  console.log('PASS desktop zoom:', output);
  clearTimeout(timeout);
  app.quit();
}).catch(error => { console.error(error); clearTimeout(timeout); app.exit(1); });
