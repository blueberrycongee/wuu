// Shipping main/preload/renderer/Go. An authored loopback provider is the only
// configured model; this never calls a paid service or reads a real profile.
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
  return originalHandle.call(this, channel, (event, request) => {
    if (request.model?.kind === 'capsule') {
      capsuleModel = { token: request.token, generation: request.generation };
      capsuleProtocol.push({ type: 'model', ...capsuleModel, at: Date.now() });
    }
    return handler(event, request);
  });
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
  if (req.method === 'GET') { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] })); }
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
function movePointer(x, y) {
  const moved = spawnSync('xdotool', ['mousemove', '--sync', String(Math.round(x)), String(Math.round(y))], { encoding: 'utf8', timeout: 5000 });
  assert.equal(moved.status, 0, moved.stderr || String(moved.error));
}
async function hoverCapsule() {
  const view = await until(() => { const value = capsuleView(); return value?.getVisible() ? value : null; }, 'Native capsule completes its paint handshake');
  const target = view.webContents;
  await until(() => evaluate(target, () => {
    const ball = document.querySelector('.context-chat-capsule-ball'), bounds = ball?.getBoundingClientRect();
    return Boolean(ball && bounds.width > 0 && bounds.height > 0);
  }), 'Native capsule ball is rendered');
  const origin = main.getContentBounds(), bounds = view.getBounds();
  if (process.platform === 'darwin') target.sendInputEvent({ type: 'mouseLeave', x: -1, y: -1 });
  else movePointer(origin.x + bounds.x - 30, origin.y + bounds.y + 24);
  await until(() => evaluate(target, () => !document.querySelector('[data-context-capsule]')?.matches(':hover')), 'Pointer leaves capsule before hover');
  const point = await evaluate(target, () => { const rect = document.querySelector('.context-chat-capsule-ball').getBoundingClientRect(); return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 }; });
  const zoom = target.getZoomFactor(), current = view.getBounds();
  if (process.platform === 'darwin') target.sendInputEvent({ type: 'mouseMove', x: Math.round(point.x), y: Math.round(point.y) });
  else movePointer(origin.x + current.x + point.x * zoom, origin.y + current.y + point.y * zoom);
  await until(() => evaluate(target, () => document.querySelector('.context-chat-capsule-ball').matches(':hover')), 'Pointer reaches mascot');
  assert.equal(await evaluate(target, () => Boolean(document.querySelector('.context-chat-capsule input'))), false, 'Hover keeps the page unobstructed');
  return target;
}
async function clickCapsule(target, button = 'left') {
  const point = await evaluate(target, () => { const r = document.querySelector('.context-chat-capsule-ball').getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) }; });
  target.sendInputEvent({ type: 'mouseDown', ...point, button, clickCount: 1 });
  target.sendInputEvent({ type: 'mouseUp', ...point, button, clickCount: 1 });
}
function report(passed) { fs.writeFileSync(path.join(output, 'capsule-protocol.json'), JSON.stringify(capsuleProtocol, null, 2)); fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed, platform: process.platform, executable: process.execPath, desktop, phase, checks, errors, screenshots, diagnostics,
  boundary: 'Shipping Electron main/preload/renderer and Go core; disposable profile and local synthetic HTTP model provider. Chromium input on macOS and native pointer hover on X11 exercise the isolated capsule surface. Screenshots capture the composed native window on the recorded platform.', requests: requests.length }, null, 2));
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
  ipcMain.handle = originalHandle;
  void run(port).catch(fail);
})().catch(fail);
async function run(port) {
  await until(() => main);
  // Resizing preserves the startup window's position, which can put its lower
  // and right edges outside Xvfb. Place the entire window before native input.
  const workArea = screen.getPrimaryDisplay().workArea;
  const width = Math.min(1280, workArea.width), height = Math.min(900, workArea.height);
  const bounds = { x: workArea.x + Math.floor((workArea.width - width) / 2), y: workArea.y + Math.floor((workArea.height - height) / 2), width, height };
  main.setBounds(bounds); main.show(); main.focus(); if (process.platform === 'darwin') app.focus({ steal: true });
  await until(() => { const actual = main.getBounds(); return Object.keys(bounds).every(key => actual[key] === bounds[key]); }, 'Native window fits the display work area');
  await until(() => evaluate(main.webContents, () => Boolean(document.querySelector('[data-context-capsule]'))));
  await shot('01-internal-ball'); checks.push('Internal Wuu view has compact capsule without opening a new conversation.');
  phase = 'hidden-page context boundary';
  const plugin = await evaluate(main.webContents, url => window.wuu.webWorkspace.open({ id: 'capsule-plugin-fixture', url }), `http://127.0.0.1:${port}/page`);
  assert.equal(typeof plugin.activeTabId, 'string', 'Non-tabbed plugin pages expose their stable browser target.');
  const hiddenRead = await evaluate(main.webContents, async id => {
    try { await window.wuu.readPageContext('capsule-plugin-fixture', id); return true; } catch { return false; }
  }, plugin.activeTabId);
  assert.equal(hiddenRead, false, 'Hidden pages cannot be included by the capsule.');
  await evaluate(main.webContents, () => window.wuu.webWorkspace.close('capsule-plugin-fixture'));
  checks.push('Plugin website snapshots expose a stable page ID and hidden page context is denied.');
  phase = 'native website and capsule readiness';
  const state = await evaluate(main.webContents, url => window.wuu.webWorkspace.createTab('ribbon-browser', url), `http://127.0.0.1:${port}/page?token=secret-url#secret-fragment`);
  const tabId = state.activeTabId;
  await until(() => evaluate(main.webContents, id => Boolean(document.querySelector(`[data-ribbon-id="${id}"]`)), tabId));
  await evaluate(main.webContents, id => document.querySelector(`[data-ribbon-id="${id}"]`).click(), tabId);
  const page = await until(() => webContents.getAllWebContents().find(contents => contents.getURL().startsWith(`http://127.0.0.1:${port}/page`)));
  await until(() => evaluate(page, () => Boolean(document.querySelector('#page-input'))));
  await until(() => main.contentView.children.some(view => view.webContents === page && view.getVisible()), 'Fixture page has visible native ownership');
  await until(() => capsuleView()?.getVisible(), 'Capsule paints before the focus scenario');
  main.focus(); if (process.platform === 'darwin') app.focus({ steal: true });
  await until(() => main.isFocused(), 'Test window is foreground');
  await evaluate(page, () => document.querySelector('#page-input').focus()); page.focus();
  await until(() => page.isFocused(), 'Fixture page owns keyboard focus before hover');
  const focusedBefore = page.isFocused();
  const native = await hoverCapsule();
  assert.equal(page.isFocused(), focusedBefore, 'hover must not steal keyboard focus');
  await shot('02-native-hover');
  const anchorBefore = capsuleView().getBounds();
  await clickCapsule(native);
  await until(() => evaluate(native, () => document.activeElement === document.querySelector('input')), 'Click transfers focus to editor');
  assert.equal(native.isFocused(), true);
  await native.insertText('Explain this page');
  native.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' }); native.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
  await until(() => evaluate(native, () => !document.querySelector('input')));
  await until(() => page.isFocused(), 'Escape returns focus to the live page');
  await hoverCapsule();
  await clickCapsule(native, 'right');
  await until(() => evaluate(native, () => document.activeElement?.getAttribute('role') === 'menuitem'), 'Right click focuses a menu action');
  await shot('02-native-context-menu');
  native.sendInputEvent({ type: 'keyDown', keyCode: 'Return' }); native.sendInputEvent({ type: 'keyUp', keyCode: 'Return' });
  await until(() => evaluate(native, () => document.activeElement === document.querySelector('input')), 'Menu action opens editor');
  assert.equal(await evaluate(native, () => document.querySelector('input').value), 'Explain this page');
  assert.equal(requests.length, 0, 'Opening the editor from a menu never sends its draft');
  const anchorAfter = await evaluate(native, () => { const r = document.querySelector('.context-chat-capsule-ball').getBoundingClientRect(); return { right: r.right, bottom: r.bottom }; });
  const expandedBounds = capsuleView().getBounds();
  assert.equal(expandedBounds.x + anchorAfter.right, anchorBefore.x + anchorBefore.width - 8, 'Mascot stays anchored horizontally');
  assert.equal(expandedBounds.y + anchorAfter.bottom, anchorBefore.y + anchorBefore.height - 8, 'Mascot stays anchored vertically');
  // Native focus changes cross WebContents; Chromium input alone does not focus a macOS sibling view.
  const field = await evaluate(page, () => { const r = document.querySelector('#page-input').getBoundingClientRect(); return { x: r.x + 10, y: r.y + 10 }; });
  page.focus();
  page.sendInputEvent({ type: 'mouseDown', ...field, button: 'left', clickCount: 1 });
  page.sendInputEvent({ type: 'mouseUp', ...field, button: 'left', clickCount: 1 });
  await until(() => evaluate(native, () => !document.querySelector('input')), 'Leaving for page collapses editor');
  assert.equal(await evaluate(page, () => document.activeElement.id), 'page-input');
  // Accessibility activation does not necessarily send pointer-enter first.
  await evaluate(native, () => document.querySelector('.context-chat-capsule-ball').click());
  await until(() => evaluate(native, () => document.activeElement === document.querySelector('input')));
  native.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' }); native.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
  await until(() => page.isFocused(), 'Escape restores page focus without a preceding hover');
  await hoverCapsule(); await clickCapsule(native);
  await until(() => evaluate(native, () => document.querySelector('input')?.value === 'Explain this page'));
  checks.push('Hover preserves page focus; click focuses the editor; right click and keyboard menu activation preserve drafts; Escape restores page focus; moving focus back to the page collapses the editor.');
  const context = await evaluate(main.webContents, id => window.wuu.readPageContext('ribbon-browser', id), tabId);
  assert.match(context.text, /Visible page reference/); assert.ok(!JSON.stringify(context).includes('secret-'));
  assert.ok(context.pageId.endsWith(tabId)); assert.ok(!context.url.includes('?') && !context.url.includes('#'));
  await shot('03-native-draft');
  phase = 'appearance matrix';
  for (const [theme, fontSize, width, reduced] of [['light', 15, 1280, false], ['dark', 15, 1280, false], ['light', 20, 720, false], ['dark', 20, 720, true]]) {
    main.setSize(width, 900);
    await evaluate(main.webContents, ({ theme, fontSize, reduced }) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty('--font-ui', fontSize + 'px');
      document.documentElement.style.setProperty('--motion-reduced', reduced ? '1' : '0');
    }, { theme, fontSize, reduced });
    await until(() => evaluate(native, ({ theme, fontSize }) => document.documentElement.dataset.theme === theme
      && getComputedStyle(document.querySelector('input')).fontSize === fontSize + 'px', { theme, fontSize }), 'Native appearance follows owner');
    await until(() => capsuleView().getBounds().x + capsuleView().getBounds().width <= width, 'Native bounds follow resize');
    const layout = await evaluate(native, () => {
      const input = document.querySelector('input'), ball = document.querySelector('.context-chat-capsule-ball');
      const ir = input.getBoundingClientRect(), br = ball.getBoundingClientRect();
      return { inputWidth: ir.width, overlap: ir.right > br.left, clipped: br.right > innerWidth || br.bottom > innerHeight,
        reduced: getComputedStyle(document.querySelector('.context-chat-capsule-body')).animationName };
    });
    assert.ok(layout.inputWidth >= 150 && !layout.overlap && !layout.clipped, JSON.stringify(layout));
    if (reduced) assert.equal(layout.reduced, 'none');
    await shot(`matrix-${theme}-${fontSize}-${width}`);
  }
  checks.push('Light/dark, 15/20px UI, 1280/720px window and reduced motion render without clipped controls or overlapping editor.');
  await evaluate(main.webContents, () => { document.documentElement.dataset.theme = 'light'; document.documentElement.style.removeProperty('--font-ui'); document.documentElement.style.removeProperty('--motion-reduced'); });
  main.setSize(1280, 900);
  phase = 'send normal chat';
  main.focus(); if (process.platform === 'darwin') app.focus({ steal: true });
  await until(() => main.isFocused() && capsuleView()?.getVisible(), 'Foreground capsule is ready to send');
  await until(() => evaluate(native, () => document.documentElement.dataset.theme === 'light' && innerWidth === 416), 'Final appearance has painted');
  // Theme restoration and window resize can publish another generation after
  // the viewport already matches. Let the owner commit those updates, then
  // require the same generation's native paint before issuing one Send.
  await evaluate(main.webContents, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await until(() => capsuleModel && capsulePaint?.token === capsuleModel.token
    && capsulePaint.generation === capsuleModel.generation, 'Final capsule generation has painted');
  native.focus();
  await evaluate(native, () => document.querySelector('form').requestSubmit());
  const thread = await until(() => evaluate(main.webContents, async () => (await window.wuu.listThreads()).threads.find(thread => !thread.ephemeral)), 'Single capsule Send creates a normal thread');
  await until(() => requests.some(body => body.tools?.length && JSON.stringify(body.messages).includes('Explain this page')));
  await until(() => evaluate(main.webContents, async id => (await window.wuu.resumeThread(id)).thread.turns.some(turn => turn.status === 'completed'), thread.id));
  assert.equal(await evaluate(main.webContents, () => document.querySelector('.web-workspace-stage')?.hidden), false);
  const request = requests.find(body => body.tools?.length && JSON.stringify(body.messages).includes('Explain this page'));
  assert.ok(JSON.stringify(request.messages).includes(context.pageId));
  assert.ok(JSON.stringify(request.messages).includes('Visible page reference.'));
  assert.ok(JSON.stringify(request.messages).includes('untrusted page content'));
  assert.ok(!JSON.stringify(request.messages).includes('secret-'));
  checks.push('Send uses production startThread/startTurn and exact bounded page reference; the normal Chat session persists while the source website remains visible.');
  await until(() => evaluate(native, () => Boolean(document.querySelector('.context-chat-capsule-status button'))));
  await shot('04-chat-created');
  await evaluate(native, () => document.querySelector('input').focus()); await native.insertText('Continue this same page');
  await evaluate(native, () => document.querySelector('form').requestSubmit());
  await until(() => evaluate(main.webContents, async id => (await window.wuu.resumeThread(id)).thread.turns.filter(turn => turn.status === 'completed').length >= 2, thread.id));
  assert.equal((await evaluate(main.webContents, async () => (await window.wuu.listThreads()).threads)).length, 1);
  checks.push('Second send reuses the ordinary conversation, without creating a parallel chat backend.');
  await evaluate(native, () => document.querySelector('.context-chat-capsule-status button').click());
  await until(() => evaluate(main.webContents, () => document.querySelector('.web-workspace-stage')?.hidden));
  await shot('05-open-chat'); checks.push('Open chat navigates explicitly to the conversation.');
  phase = 'complete'; report(true); clearTimeout(timeout);
  if (process.env.WUU_CAPSULE_KEEP_OPEN === '1') {
    await evaluate(main.webContents, id => document.querySelector(`[data-ribbon-id="${id}"]`).click(), tabId);
    return;
  }
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
