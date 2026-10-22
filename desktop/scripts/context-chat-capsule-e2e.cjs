// Shipping main/preload/renderer/Go. An authored loopback provider is the only
// configured model; this never calls a paid service or reads a real profile.
// Workbench intentionally disables the floating chat (5c83a1a3). Verify that
// shipping contract here; enabled native capsule mechanics remain covered by
// browser-ribbon-e2e.cjs, without turning the feature back on for this test.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, webContents, nativeImage, screen, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = process.env.WUU_CAPSULE_OUTPUT || path.join(desktop, 'out/e2e/context-chat-capsule');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-capsule-'));
const home = path.join(fixture, 'wuu-home'), userHome = path.join(fixture, 'user-home'), project = path.join(fixture, 'project');
for (const directory of [output, home, userHome, project]) fs.mkdirSync(directory, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home; process.env.HOME = userHome; process.env.USERPROFILE = userHome;
process.env.WUU_ENABLE_BROWSER = '1'; process.env.WUU_SAFE_MODE = '1'; process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
process.env.WUU_DESKTOP_CORE ||= path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
for (const name of Object.keys(process.env)) if (/(?:API_KEY|ACCESS_TOKEN|AUTH_TOKEN|SECRET|PASSWORD)$/.test(name)) delete process.env[name];
const requests = [], checks = [], errors = [], screenshots = [];
let diagnostics;
let main, phase = 'startup';
let capsuleModel, capsulePaint;
const capsuleProtocol = [];
// Observe the shipping handlers without bypassing their token/generation checks.
const originalHandle = ipcMain.handle;
ipcMain.handle = function (channel, handler) {
  if (channel !== 'wuu:chrome-overlay-show') return originalHandle.call(this, channel, handler);
  const result = originalHandle.call(this, channel, (event, request) => {
    if (request.model?.kind === 'capsule') {
      capsuleModel = { token: request.token, generation: request.generation };
      capsuleProtocol.push({ type: 'model', ...capsuleModel, at: Date.now() });
    }
    return handler(event, request);
  });
  // Registration happens after app.whenReady(), not when the main import resolves.
  ipcMain.handle = originalHandle;
  return result;
};
ipcMain.on('wuu:chrome-overlay-painted', (event, token, generation) => {
  if (!event.sender.getURL().includes('surface=capsule')) return;
  capsulePaint = { token, generation };
  capsuleProtocol.push({ type: 'paint', token, generation, at: Date.now() });
});
ipcMain.on('wuu:chrome-overlay-command', (event, message) => {
  if (event.sender.getURL().includes('surface=capsule')) capsuleProtocol.push({
    type: 'command', command: message.command?.type, token: message.token,
    generation: message.generation, current: capsuleModel, at: Date.now(),
  });
});
const server = http.createServer((req, res) => {
  if (req.method === 'GET' && req.url.startsWith('/page')) {
    res.writeHead(200, { 'Content-Type': 'text/html' });
    return res.end('<!doctype html><title>Capsule fixture</title><h1>Visible capsule fixture</h1><p>Visible page reference.</p><input id="page-input" aria-label="Page field"><input type="password" value="secret-password"><textarea>secret-draft</textarea><p hidden>secret-hidden</p><p id="api-token">secret-token</p><p style="display:none">secret-display</p>');
  }
  if (req.method === 'GET') { if (req.url.startsWith('/v1/')) requests.push({ method: req.method, url: req.url }); res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] })); }
  let raw = '';
  req.on('data', chunk => raw += chunk); req.on('end', () => {
    const body = JSON.parse(raw); requests.push(body);
    if (!body.stream) { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ id: 'title', choices: [{ index: 0, message: { role: 'assistant', content: 'Capsule page conversation' }, finish_reason: 'stop' }] })); }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(`data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta: { role: 'assistant', content: 'The page context was received.' }, finish_reason: null }] })}\n\n`);
    res.write(`data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`);
    res.end('data: [DONE]\n\n');
  });
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (target, fn, value) => target.executeJavaScript(`(${fn})(${JSON.stringify(value)})`, true);
async function until(fn, label = phase) { const deadline = Date.now() + 30000; while (Date.now() < deadline) { const value = await fn(); if (value) return value; await delay(30); } throw new Error(`Timed out: ${label}`); }
async function shot(name) {
  const previousPhase = phase;
  phase = `${previousPhase}: capture ${name}`;
  const capsule = capsuleView();
  if (capsule?.getVisible()) await evaluate(capsule.webContents, async () => {
    const deadline = performance.now() + 3000;
    const running = () => document.getAnimations().filter(animation => animation.playState === 'running'
      && Number.isFinite(animation.effect?.getComputedTiming().endTime));
    // Hover/menu changes can cancel a CSS transition while capture awaits it.
    // Only that expected AbortError is recoverable; recheck replacement motion.
    for (;;) {
      let timer;
      try {
        await Promise.race([
          Promise.all(running().map(animation => animation.finished.catch(error => {
            if (error?.name !== 'AbortError') throw error;
          }))),
          new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('Capsule capture motion did not settle')), Math.max(0, deadline - performance.now())); }),
        ]);
      } finally { clearTimeout(timer); }
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      if (!running().length) break;
      if (performance.now() >= deadline) throw new Error('Capsule capture replacement motion did not settle');
    }
  });
  // BrowserWindow.capturePage omits WebContentsView children. Capture the real
  // X11 compositor so the fixture website and privileged capsule are visible.
  if (process.platform === 'darwin') {
    for (const view of main.contentView.children) if (view.webContents && view.getVisible()) await view.webContents.capturePage();
    const file = path.join(output, `${name}.png`);
    const capture = spawnSync('/usr/sbin/screencapture', ['-x', '-o', '-l', main.getMediaSourceId().split(':')[1], file], { encoding: 'utf8' });
    assert.equal(capture.status, 0, capture.stderr);
    assert.ok(!nativeImage.createFromPath(file).isEmpty());
    screenshots.push(`${name}.png`); phase = previousPhase; return;
  }
  const bounds = main.getBounds();
  const display = screen.getDisplayMatching(bounds);
  assert.equal(display.scaleFactor, 1, 'X11 acceptance uses physical-pixel window coordinates.');
  assert.ok(bounds.x >= display.bounds.x && bounds.y >= display.bounds.y
    && bounds.x + bounds.width <= display.bounds.x + display.bounds.width
    && bounds.y + bounds.height <= display.bounds.y + display.bounds.height,
  `The entire native window must fit its capture display: ${JSON.stringify({ bounds, display: display.bounds })}`);
  const capture = spawnSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'x11grab', '-video_size', `${bounds.width}x${bounds.height}`,
    '-i', `${process.env.DISPLAY}+${bounds.x},${bounds.y}`, '-frames:v', '1', '-f', 'image2pipe', '-vcodec', 'png', 'pipe:1'], { timeout: 12000, maxBuffer: 24 * 1024 * 1024 });
  assert.equal(capture.status, 0, String(capture.stderr || capture.error));
  assert.ok(!nativeImage.createFromBuffer(capture.stdout).isEmpty());
  fs.writeFileSync(path.join(output, `${name}.png`), capture.stdout); screenshots.push(`${name}.png`); phase = previousPhase;
}
const capsuleView = () => main.contentView.children.find(view => view.webContents && !view.webContents.isDestroyed()
  && view.webContents.getURL().endsWith('/chrome-overlay.html?surface=capsule'));
function report(passed) { fs.writeFileSync(path.join(output, 'capsule-protocol.json'), JSON.stringify(capsuleProtocol, null, 2)); fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed, platform: process.platform, executable: process.execPath, desktop, phase, checks, errors, screenshots, diagnostics,
  boundary: 'Shipping Electron main/preload/renderer and Go core; disposable profile and local synthetic HTTP model provider. Real Electron text input verifies the disabled floating-chat contract, including navigation and resize. Enabled capsule mechanics are covered separately by browser-ribbon-e2e.cjs. Screenshots capture the composed native window on the recorded platform.', requests: requests.length }, null, 2));
  fs.writeFileSync(path.join(output, 'provider-requests.json'), JSON.stringify(requests, null, 2)); }
const timeout = setTimeout(() => fail(new Error('Capsule acceptance timed out')), 150000);
app.on('browser-window-created', (_event, window) => { main ||= window; });
module.exports = (async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines, providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${port}/v1`, api_key: 'synthetic-not-a-credential', model: 'fixture' } } }));
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'repo', name: 'Capsule fixture', path: project, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'repo', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light' }));
  assert.equal(app.isReady(), false);
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  void run(port).catch(fail);
})().catch(fail);
async function assertDisabled(label) {
  assert.equal(await evaluate(main.webContents, () => Boolean(document.querySelector('[data-context-capsule]'))), false, `${label}: no shell capsule`);
  assert.equal(Boolean(capsuleView()?.getVisible()), false, `${label}: no native capsule`);
  assert.equal(capsuleProtocol.length, 0, `${label}: disabled feature must not publish native models, paints or commands`);
  assert.equal(requests.length, 0, `${label}: disabled feature must not contact the provider`);
}
async function run(port) {
  await until(() => main);
  const workArea = screen.getPrimaryDisplay().workArea;
  const width = Math.min(1280, workArea.width), height = Math.min(900, workArea.height);
  const bounds = { x: workArea.x + Math.floor((workArea.width - width) / 2), y: workArea.y + Math.floor((workArea.height - height) / 2), width, height };
  main.setBounds(bounds); main.show(); main.focus(); if (process.platform === 'darwin') app.focus({ steal: true });
  await until(() => { const actual = main.getBounds(); return Object.keys(bounds).every(key => actual[key] === bounds[key]); }, 'Native window fits the display work area');
  // Positive readiness must precede an absence assertion; an unloaded shell is
  // not evidence that the disabled feature respects the product contract.
  await until(() => evaluate(main.webContents, () => Boolean(window.wuu?.webWorkspace && document.querySelector('.sidebar-ribbon'))), 'Shipping shell is ready');
  phase = 'disabled internal capsule';
  await assertDisabled(phase);
  await shot('01-disabled-internal');
  checks.push('Ready internal Wuu view has no floating context chat or capsule IPC.');
  phase = 'hidden-page context boundary';
  const plugin = await evaluate(main.webContents, url => window.wuu.webWorkspace.open({ id: 'capsule-plugin-fixture', url }), `http://127.0.0.1:${port}/page`);
  assert.equal(typeof plugin.activeTabId, 'string', 'Non-tabbed plugin pages expose their stable browser target.');
  const hiddenRead = await evaluate(main.webContents, async id => {
    try { await window.wuu.readPageContext('capsule-plugin-fixture', id); return true; } catch { return false; }
  }, plugin.activeTabId);
  assert.equal(hiddenRead, false, 'Hidden pages cannot be included by the capsule.');
  await evaluate(main.webContents, () => window.wuu.webWorkspace.close('capsule-plugin-fixture'));
  checks.push('Plugin website snapshots expose a stable page ID and hidden page context is denied.');
  const state = await evaluate(main.webContents, url => window.wuu.webWorkspace.createTab('ribbon-browser', url), `http://127.0.0.1:${port}/page`);
  const tabId = state.activeTabId;
  await until(() => evaluate(main.webContents, id => Boolean(document.querySelector(`[data-ribbon-id="${id}"]`)), tabId), 'Website destination exists');
  await evaluate(main.webContents, id => document.querySelector(`[data-ribbon-id="${id}"]`).click(), tabId);
  const page = await until(() => webContents.getAllWebContents().find(contents => contents.getURL() === `http://127.0.0.1:${port}/page`), 'Native website exists');
  await until(() => evaluate(page, () => Boolean(document.querySelector('#page-input'))), 'Fixture page is ready');
  await until(() => main.contentView.children.some(view => view.webContents === page && view.getVisible()), 'Fixture page has visible native ownership');
  main.focus();
  await until(() => main.isFocused(), 'Test window is foreground');
  await evaluate(page, () => document.querySelector('#page-input').focus()); page.focus();
  await until(() => page.isFocused(), 'Fixture page owns keyboard focus');
  await page.insertText('before');
  phase = 'disabled capsule during website resize';
  for (const offset of [32, 0]) {
    main.setBounds({ ...bounds, width: width - offset, height: height - offset });
    await until(() => main.getBounds().width === width - offset, 'Window resize applied');
    // Observe a bounded quiet period, rather than passing on a transient absence.
    for (let sample = 0; sample < 10; sample++) {
      await delay(30); await assertDisabled(phase);
      assert.equal(page.isFocused(), true, 'Disabled capsule must not steal native page focus');
    }
  }
  await page.insertText('-after');
  assert.equal(await evaluate(page, () => document.querySelector('#page-input').value), 'before-after', 'Page typing remains continuous through resize');
  checks.push('Visible website retains native input focus and draft through repeated resize, without capsule surfaces or IPC.');
  phase = 'disabled capsule after navigation';
  await evaluate(main.webContents, url => window.wuu.webWorkspace.command('ribbon-browser', 'navigate', url), `http://127.0.0.1:${port}/page-next`);
  await until(() => page.getURL().endsWith('/page-next') && !page.isLoadingMainFrame(), 'Navigation completes');
  await until(() => evaluate(page, () => Boolean(document.querySelector('#page-input'))), 'Navigated fixture ready');
  await evaluate(page, () => document.querySelector('#page-input').focus()); page.focus();
  for (let sample = 0; sample < 10; sample++) {
    await delay(30); await assertDisabled(phase);
    assert.equal(page.isFocused(), true, 'Navigation must not expose or focus a capsule');
  }
  await page.insertText('after-navigation');
  assert.equal(await evaluate(page, () => document.querySelector('#page-input').value), 'after-navigation');
  assert.deepEqual(await evaluate(main.webContents, async () => (await window.wuu.listThreads()).threads), [], 'Disabled chat must not create conversations');
  await shot('02-disabled-website');
  checks.push('Navigation preserves disabled capsule contract, page input works, and no conversations or provider requests are created.');
  phase = 'complete'; report(true); clearTimeout(timeout);
  server.close(); app.quit();
}
async function fail(error) {
  errors.push(error?.stack || (error?.name || error?.message ? `${error.name || 'Error'}: ${error.message || ''}` : String(error))); console.error(error);
  if (main && !main.isDestroyed()) {
    diagnostics = { ownerVisible: main.isVisible(), ownerFocused: main.isFocused(), bounds: main.getBounds(), display: screen.getDisplayMatching(main.getBounds()), views: main.contentView.children.filter(view => view.webContents && !view.webContents.isDestroyed())
      .map(view => ({ url: view.webContents.getURL(), visible: view.getVisible(), focused: view.webContents.isFocused(), bounds: view.getBounds() })) };
    try { diagnostics.capsule = await evaluate(capsuleView().webContents, () => ({ html: document.querySelector('#root')?.innerHTML.slice(0, 4000), focused: document.hasFocus(), active: document.activeElement?.outerHTML.slice(0, 300) })); } catch {}
    try { await shot('failure'); } catch (captureError) { errors.push(`Failure screenshot: ${captureError}`); }
  }
  report(false); clearTimeout(timeout); server.close(); app.exit(1);
}
