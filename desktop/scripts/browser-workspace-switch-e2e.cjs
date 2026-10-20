// Actual shipping shell/core and real Electron mouse events. Capture every
// guest visibility transition as well as continuous composed-window video.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { execFileSync, spawn } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, ipcMain, webContents } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_BROWSER_SWITCH_OUTPUT || path.join(desktop, 'out/e2e/browser-workspace-switch'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-workspace-switch-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project');
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '1', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic', model: 'fixture' } }, engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])) }));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'switch-browser', name: 'Switching acceptance', path: project, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }], active_context: { kind: 'project', project_id: 'switch-browser', cwd: project } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 15 }));
const report = { status: 'running', platform: process.platform, versions: process.versions, commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: desktop, encoding: 'utf8' }).trim(), transitions: [], events: [], navigations: [], screenshots: [], limitations: ['Synthetic local websites and isolated account profile', 'Real Electron input events, not physical input', 'Visibility events and sampled stable frames cannot prove every compositor frame; composed recording is supplied for inspection'] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
let win, recording;
app.on('browser-window-created', (_event, created) => { win ||= created; });
const handle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, listener) => handle(channel, (...args) => { if (['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(channel)) throw new Error('No inference in switching fixture'); return listener(...args); });
const server = http.createServer((request, response) => {
  const name = request.url === '/a' ? 'A' : 'B';
  response.setHeader('Content-Type', 'text/html; charset=utf-8');
  response.end(`<!doctype html><title>Reading ${name}</title><style>body{margin:0;font:20px/1.7 system-ui;color:#172d35;background:${name === 'A' ? '#f8e8df' : '#dcefe9'}}header{position:fixed;top:0;left:0;right:0;background:#172d35;color:white;padding:18px 38px}main{padding:100px 38px 38px}input{font:inherit;padding:12px;width:80%}p{margin:44px 0}strong{font-size:42px}</style><header>LIVE READING ${name} — <span id="clock">0</span></header><main><strong>Page ${name}</strong><p><input id="draft" placeholder="Retained draft"></p>${Array.from({ length: 35 }, (_, i) => `<p>Section ${i + 1}: this is live page ${name}, retained across tabs, Chat and Settings.</p>`).join('')}</main><script>window.fixtureIdentity=crypto.randomUUID();window.framesSeen=0;function tick(){document.querySelector('#clock').textContent=++window.framesSeen;requestAnimationFrame(tick)}tick();</script>`);
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, timeout = 12000) { const end = Date.now() + timeout; while (Date.now() < end) { if (await check()) return; await delay(20); } throw new Error('Timed out: ' + label); }
const evaluate = (fn, argument) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(argument)})`);
function visibleContents(parent = win.contentView) { return parent.children.flatMap(view => !view.getVisible() ? [] : [...(view.webContents ? [view.webContents] : []), ...visibleContents(view)]); }
async function click(selector) {
  for (let attempt = 0; attempt < 100; attempt++) {
    for (const contents of [win.webContents, ...visibleContents()].reverse()) {
      const point = await contents.executeJavaScript(`(selector=>{const e=[...document.querySelectorAll(selector)].find(e=>e.getBoundingClientRect().width&&!e.closest('[inert]'));if(!e)return null;e.scrollIntoView({block:'nearest'});const r=e.getBoundingClientRect(),x=r.x+r.width/2,y=r.y+r.height/2;return e.contains(document.elementFromPoint(x,y))?{x,y}:null})(${JSON.stringify(selector)})`);
      if (!point) continue;
      contents.focus(); contents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...point }); contents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...point }); return;
    }
    await delay(30);
  }
  throw new Error('No clickable target: ' + selector);
}
async function frame() { await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))); }
async function capture(name) {
  const file = path.join(output, `${name}.png`);
  if (process.platform === 'darwin') execFileSync('screencapture', ['-x', '-o', '-l', win.getMediaSourceId().split(':')[1], file]);
  else fs.writeFileSync(file, (await win.webContents.capturePage()).toPNG());
  report.screenshots.push({ file, composed: process.platform === 'darwin' });
}
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.webContents.isLoadingMainFrame(), 'shell');
  await until(() => evaluate(() => Boolean(window.wuu?.webWorkspace && document.querySelector('.sidebar-ribbon'))), 'chrome ready');
  win.setBounds({ x: 120, y: 80, width: 1280, height: 850 }); win.show(); win.focus();
  await evaluate(() => window.wuu.webWorkspace.initializeTabs([]));
  const pages = {};
  for (const name of ['a', 'b']) {
    const tab = await evaluate(async url => (await window.wuu.webWorkspace.createTab('ribbon-browser', url)).activeTabId, `${base}/${name}`);
    await until(() => evaluate(id => Boolean(document.querySelector(`[data-ribbon-id="${id}"]`)), tab), 'tab chrome');
    await click(`[data-ribbon-id="${tab}"]`);
    let guest;
    await until(() => { guest = webContents.getAllWebContents().find(contents => contents.getURL() === `${base}/${name}` && !contents.isLoadingMainFrame()); return Boolean(guest); }, 'guest');
    const view = win.contentView.children.find(view => view.webContents === guest);
    await until(() => view.getVisible(), 'guest visible');
    await guest.executeJavaScript("document.querySelector('#draft').focus()"); guest.focus(); await guest.insertText(`Unsent draft ${name}`);
    await guest.executeJavaScript(`document.querySelector('#draft').blur();scrollTo(0,${name === 'a' ? 560 : 790})`);
    const original = await guest.executeJavaScript("({identity:window.fixtureIdentity,scroll:scrollY,draft:document.querySelector('#draft').value})");
    pages[name] = { tab, guest, view, original };
  }
  let step = 'ready';
  for (const [name, page] of Object.entries(pages)) {
    const originalVisibility = page.view.setVisible.bind(page.view);
    page.view.setVisible = visible => { if (page.view.getVisible() !== visible) report.events.push({ step, page: name, visible, at: Date.now(), monotonicMs: performance.now() }); return originalVisibility(visible); };
    page.guest.on('did-start-navigation', details => { if (details.isMainFrame) report.navigations.push({ name, step, url: details.url }); });
    page.guest.on('destroyed', () => { report.destroyed ||= []; report.destroyed.push({ name, step }); });
  }
  await evaluate(() => {
    window.__switchSurfaceProbe = { fullWindowLoadingFrames: [], paneLoadingFrames: [], stopped: false };
    const sample = () => {
      const probe = window.__switchSurfaceProbe;
      if (probe.stopped) return;
      for (const element of document.querySelectorAll('[role="status"]')) {
        const rect = element.getBoundingClientRect();
        const pane = element.closest('.settings-workbench-layer,.settings-standalone-layer');
        if (pane && rect.width > 0 && rect.height > 0) {
          const bounds = pane.getBoundingClientRect();
          probe.paneLoadingFrames.push({ time: performance.now(), contained: rect.left >= bounds.left - 1 && rect.top >= bounds.top - 1 && rect.right <= bounds.right + 1 && rect.bottom <= bounds.bottom + 1, rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } });
        }
        if (rect.x <= 0 && rect.y <= 0 && rect.width >= innerWidth - 1 && rect.height >= innerHeight - 1 && getComputedStyle(element).visibility !== 'hidden') {
          probe.fullWindowLoadingFrames.push({ time: performance.now(), label: element.getAttribute('aria-label'), rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height } });
        }
      }
      requestAnimationFrame(sample);
    };
    requestAnimationFrame(sample);
  });
  await frame(); await capture('ready-page-b');
  if (process.platform === 'darwin') {
    const file = path.join(output, 'workspace-switching.mov');
    fs.rmSync(file, { force: true });
    report.recordingStart = { wallClock: Date.now(), monotonicMs: performance.now() };
    const child = spawn('screencapture', ['-x', '-v', '-V', '30', '-l', win.getMediaSourceId().split(':')[1], file], { stdio: ['ignore', 'ignore', 'pipe'] });
    let errors = ''; child.stderr.on('data', data => { errors += data; });
    recording = new Promise(resolve => child.once('close', code => { report.recording = { file, exitCode: code, errors }; resolve(); }));
    // Let the macOS recorder initialize before the first visible interaction.
    await delay(1200);
  }
  const route = ['a', 'b', 'a', 'chat', 'a', 'settings', 'b', 'chat', 'a'];
  for (let cycle = 0; cycle < 6; cycle++) for (const target of route) {
    step = `${cycle + 1}:${target}`;
    const startedAt = performance.now();
    const start = report.events.length;
    await click(target === 'chat' ? '[data-wuu-component="conversations-section"]' : target === 'settings' ? 'button[aria-label="Settings"]' : `[data-ribbon-id="${pages[target].tab}"]`);
    await until(() => Object.entries(pages).every(([name, page]) => page.view.getVisible() === (name === target)), `expected guest visibility at ${step}`);
    if (target === 'settings') await until(() => evaluate(() => Boolean(document.querySelector('.settings-embedded'))), 'settings displayed');
    if (target === 'chat') await until(() => evaluate(() => !document.querySelector('.settings-workbench-layer')), 'chat displayed');
    const samples = [];
    for (let sample = 0; sample < 4; sample++) {
      await frame();
      samples.push(Object.fromEntries(Object.entries(pages).map(([name, page]) => [name, page.view.getVisible()])));
      assert.ok(Object.entries(pages).every(([name, page]) => page.view.getVisible() === (name === target)), `Stable visibility at ${step}`);
    }
    for (const [name, page] of Object.entries(pages)) {
      assert.equal(page.guest.isDestroyed(), false);
      const state = await page.guest.executeJavaScript("({identity:window.fixtureIdentity,scroll:scrollY,draft:document.querySelector('#draft').value})");
      assert.deepEqual(state, page.original, `Retained page state for ${name} at ${step}`);
      assert.ok(report.events.slice(start).filter(event => event.page === name).length <= 1, `Guest ${name} must not flicker hide/show during ${step}`);
    }
    report.transitions.push({ step, target, startedAt, settledAt: performance.now(), events: report.events.slice(start), samples });
    if (cycle === 0) await capture(`cycle-1-${target}-${report.transitions.length}`);
    save();
    // Keep every destination visible long enough to inspect in the recording.
    await delay(180);
  }
  report.surfaceProbe = await evaluate(() => { window.__switchSurfaceProbe.stopped = true; return window.__switchSurfaceProbe; });
  assert.ok(report.surfaceProbe.paneLoadingFrames.every(frame => frame.contained), 'Settings loading must fit inside its content pane');
  assert.equal(report.surfaceProbe.fullWindowLoadingFrames.length, 0, 'Destination loading must preserve window chrome rather than cover the entire window');
  assert.equal(report.navigations.length, 0, 'Switching destinations must not navigate/reload retained pages');
  assert.equal(report.destroyed?.length || 0, 0);
  if (recording) {
    await recording;
    assert.equal(report.recording.exitCode, 0, 'Composed-window recording must complete successfully');
    const file = fs.statSync(report.recording.file);
    report.recording.size = file.size;
    report.recording.modifiedAt = file.mtimeMs;
    assert.ok(file.size > 0 && file.mtimeMs >= report.recordingStart.wallClock, 'Recording must contain fresh footage from this run');
  }
  report.status = 'passed'; report.checks = ['54 real clicks across two browser tabs, Chat and Settings', 'Every guest visibility transition recorded with no repeated hide/show per action', 'Four stable compositor-paced visibility samples after each action', 'Guest identity, unsent draft and exact scroll retained throughout', 'No unexpected navigation or WebContents recreation', 'Cold first Settings visit never displays a window-covering loading surface']; save();
  console.log(JSON.stringify({ status: report.status, output, transitions: report.transitions.length, visibilityEvents: report.events.length, recording: report.recording }, null, 2));
  app.quit();
}
const timeout = setTimeout(() => { report.status = 'timed-out'; save(); app.exit(1); }, 100000);
run().catch(async error => { report.status = 'failed'; report.error = String(error.stack); await capture('failure').catch(() => {}); save(); console.error(error); if (recording) await recording; app.exit(1); }).finally(() => { clearTimeout(timeout); server.close(); });
