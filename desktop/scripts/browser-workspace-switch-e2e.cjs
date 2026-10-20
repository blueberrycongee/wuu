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
const bundle = path.resolve(process.env.WUU_E2E_BUNDLE || desktop);
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
const report = { status: 'running', platform: process.platform, versions: process.versions, bundle, commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: desktop, encoding: 'utf8' }).trim(), transitions: [], events: [], navigations: [], screenshots: [], limitations: ['Synthetic local websites and isolated account profile', 'Real Electron input events, not physical input', 'Visibility events and sampled stable frames cannot prove every compositor frame; composed recording is supplied for inspection'] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
// A late native blur callback can fail after the last assertion, even after
// Electron emits quit. Fail the process instead of leaving a modal error open.
process.on('uncaughtException', error => {
  report.status = 'failed'; report.error = String(error.stack || error); save();
  console.error(error); app.exit(1);
});
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

// Diagnostic-only event capture: does not synthesize events or alter readiness.
const observedInputContents = new Set();
async function observeInput(contents) {
  if (observedInputContents.has(contents.id)) return;
  await contents.executeJavaScript(`(() => {
    window.__fixtureInputTrace = [];
    for (const type of ['pointerdown', 'pointerup', 'click', 'keydown', 'focusin', 'focusout']) {
      document.addEventListener(type, event => {
        const target = event.target instanceof Element ? event.target : null;
        window.__fixtureInputTrace.push({ type, at: performance.now(), trusted: event.isTrusted,
          key: event.key, x: event.clientX, y: event.clientY,
          target: target ? { tag: target.tagName, className: target.className,
            ribbonId: target.closest('[data-ribbon-id]')?.getAttribute('data-ribbon-id') } : null });
        if (window.__fixtureInputTrace.length > 32) window.__fixtureInputTrace.shift();
      }, { capture: true, passive: true });
    }
  })()`);
  observedInputContents.add(contents.id);
}
async function failureInputState() {
  if (!win || win.isDestroyed()) return null;
  const surfaces = win.contentView.children.filter(view => view.webContents).map(view => ({
    id: view.webContents.id, url: view.webContents.getURL(), visible: view.getVisible(), bounds: view.getBounds(),
    focused: view.webContents.isFocused(), loading: view.webContents.isLoadingMainFrame(),
  }));
  const delivered = [];
  for (const contents of webContents.getAllWebContents()) {
    if (!observedInputContents.has(contents.id) || contents.isDestroyed()) continue;
    delivered.push({ id: contents.id, events: await contents.executeJavaScript('window.__fixtureInputTrace').catch(() => null) });
  }
  return { ownerFocused: win.isFocused(), shellFocused: win.webContents.isFocused(), surfaces, delivered,
    focusedContents: webContents.getAllWebContents().filter(contents => contents.isFocused()).map(contents => ({ id: contents.id, url: contents.getURL() })),
    shell: await evaluate(() => ({
      selected: [...document.querySelectorAll('[data-ribbon-id][aria-current="page"]')].map(node => node.getAttribute('data-ribbon-id')),
      ribbon: document.querySelector('.sidebar-ribbon') ? { ...document.querySelector('.sidebar-ribbon').dataset } : null,
      activeElement: document.activeElement ? { tag: document.activeElement.tagName, className: document.activeElement.className } : null,
      focusModality: document.documentElement.dataset.focusModality,
    })).catch(() => null) };
}
async function click(selector) {
  for (let attempt = 0; attempt < 100; attempt++) {
    for (const contents of [win.webContents, ...visibleContents()].reverse()) {
      const point = await contents.executeJavaScript(`(selector => {
        const element = [...document.querySelectorAll(selector)].find(e => e.getBoundingClientRect().width && !e.closest('[inert]'));
        if (!element) return null;
        element.scrollIntoView({ block: 'nearest' });
        // A collapsed rail keeps a wider layout behind its visible icon.
        // Use a hittable child when the button center lies under the page.
        for (const target of [element, ...element.querySelectorAll('svg,span')]) {
          const r = target.getBoundingClientRect(), x = r.x + r.width / 2, y = r.y + r.height / 2;
          if (r.width && r.height && element.contains(document.elementFromPoint(x, y))) return { x, y };
        }
        return null;
      })(${JSON.stringify(selector)})`);
      if (!point) continue;
      await observeInput(contents);
      report.lastInput = { type: 'click', selector, contents: contents.id, point, at: performance.now() };
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
// A native page does not inherit CSS transforms from the shell. Check position
// tracking, retargeting, concurrent effects, and occlusion through actual views.
async function checkGeometryMotion(page) {
  report.geometryMotion = [];
  await evaluate(() => {
    const host = document.querySelector('.web-workspace-stage:not([hidden]) .web-workspace-host');
    const ancestor = host.parentElement;
    const style = document.createElement('style');
    style.textContent = `
      .geometry-motion-host { transition: transform 900ms linear !important; }
      @keyframes geometry-travel { from { transform:translateX(-240px) } to { transform:translateX(240px) } }
      @keyframes geometry-fade { from { opacity:.99 } to { opacity:1 } }
      .geometry-motion-effects { animation:geometry-travel 900ms linear both,geometry-fade 150ms linear both !important; }
      .geometry-scroll-effect { animation:geometry-fade linear both; animation-timeline:scroll(self); }
    `;
    document.head.append(style);
    window.__geometryMotion = { host, ancestor, style };
  });
  const sample = async label => {
    const dom = await evaluate(() => {
      const r = window.__geometryMotion.host.getBoundingClientRect();
      const x = Math.max(0, Math.round(r.x)), y = Math.max(0, Math.round(r.y));
      return { x, y, width: Math.min(innerWidth, Math.round(r.right)) - x, height: Math.min(innerHeight, Math.round(r.bottom)) - y };
    });
    const native = page.view.getBounds();
    const delta = Math.max(...Object.keys(dom).map(key => Math.abs(dom[key] - native[key])));
    report.geometryMotion.push({ label, dom, native, delta });
    save();
    // A renderer sample and native read are not an atomic compositor snapshot.
    // The generous moving tolerance still rejects an unchanged start rectangle.
    assert.ok(delta <= 10, `${label}: native bounds follow shell geometry (${delta}px)`);
  };
  const progress = (element, target, name) => evaluate(({ element, target, name }) => new Promise((resolve, reject) => {
    const started = performance.now();
    const tick = () => {
      const animation = window.__geometryMotion[element].getAnimations().find(animation => name ? animation.animationName === name : animation.playState === 'running');
      const current = animation?.effect.getComputedTiming().progress;
      if (typeof current === 'number' && current >= target) resolve();
      else if (performance.now() - started > 5000) reject(new Error(`No animation progress on ${element}`));
      else requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  }), { element, target, name });
  for (const element of ['host', 'ancestor']) {
    await evaluate(element => {
      const target = window.__geometryMotion[element];
      target.classList.add('geometry-motion-host');
      getComputedStyle(target).transform;
      target.style.transform = 'translateX(72px)';
    }, element);
    await progress(element, 0.5);
    await sample(`${element}-position-transition`);
    await evaluate(element => { window.__geometryMotion[element].style.transform = 'translateX(0px)'; }, element);
    await progress(element, 0.5);
    await sample(`${element}-reversed-position-transition`);
    await evaluate(element => Promise.all(window.__geometryMotion[element].getAnimations().map(animation => animation.finished)), element);
    await frame();
    await sample(`${element}-reversed-position-settled`);
    await evaluate(element => {
      const target = window.__geometryMotion[element];
      target.classList.remove('geometry-motion-host');
      target.style.transform = '';
    }, element);
  }
  for (const indirect of [false, true]) {
    await evaluate(indirect => {
      const moving = document.createElement('div');
      moving.style.cssText = 'position:fixed;left:0;top:180px;width:120px;height:100px;z-index:99999;transform:translateX(-240px)';
      const menu = indirect ? moving.appendChild(document.createElement('div')) : moving;
      menu.setAttribute('role', 'menu');
      menu.style.width = '120px'; menu.style.height = '100px'; menu.style.background = '#537eab';
      document.body.append(moving);
      window.__geometryMotion.moving = moving;
    }, indirect);
    await frame();
    assert.equal(page.view.getVisible(), true, 'Offscreen menu leaves the page visible');
    await evaluate(() => { window.__geometryMotion.moving.classList.add('geometry-motion-effects'); });
    await progress('moving', 0.8, 'geometry-travel');
    await frame();
    report.geometryMotion.push({ label: indirect ? 'moving-menu-ancestor' : 'concurrent-menu-effects', visible: page.view.getVisible() });
    save();
    assert.equal(page.view.getVisible(), false, 'Moving menu occludes the native page after its short sibling effect ends');
    await evaluate(() => { window.__geometryMotion.moving.remove(); });
    await until(() => page.view.getVisible(), 'Removing a moving menu restores the same page');
  }
  await evaluate(() => {
    const scrolling = document.createElement('div');
    scrolling.setAttribute('role', 'menu');
    scrolling.className = 'geometry-scroll-effect';
    scrolling.style.cssText = 'position:fixed;left:-240px;top:180px;width:120px;height:100px;overflow:auto';
    scrolling.appendChild(document.createElement('div')).style.height = '1000px';
    document.body.append(scrolling);
    scrolling.scrollTop = 100;
    window.__geometryMotion.scrolling = scrolling;
  });
  await until(() => evaluate(() => window.__geometryMotion.scrolling.getAnimations().some(animation =>
    animation.playState === 'running' && animation.timeline !== document.timeline)), 'scroll-linked effect is active');
  await frame();
  const idle = await evaluate(() => new Promise(resolve => {
    const { host } = window.__geometryMotion, read = host.getBoundingClientRect;
    let reads = 0, frames = 0, previousReads = 0, quietFrames = 0;
    host.getBoundingClientRect = function () { reads++; return read.call(this); };
    const tick = () => {
      quietFrames = reads === previousReads ? quietFrames + 1 : 0;
      previousReads = reads;
      // Let ordinary scrollbar/reveal transitions settle, but reject a loop
      // that keeps measuring a stationary scroll timeline indefinitely.
      if (++frames < 120 && quietFrames < 12) { requestAnimationFrame(tick); return; }
      host.getBoundingClientRect = read;
      resolve({ reads, frames, quietFrames });
    };
    requestAnimationFrame(tick);
  }));
  report.geometryMotion.push({ label: 'settled-scroll-timeline-idle-measurements', ...idle });
  save();
  assert.equal(idle.quietFrames, 12, 'Completed effects and stationary scroll-linked effects do not leave a permanent bounds loop');
  await evaluate(() => { window.__geometryMotion.scrolling.remove(); window.__geometryMotion.style.remove(); delete window.__geometryMotion; });
  assert.equal(await page.guest.executeJavaScript('window.fixtureIdentity'), page.original.identity, 'Geometry motion retains the page document');
  await capture('native-page-geometry-settled');
}
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  await import(pathToFileURL(path.join(bundle, 'out/main/index.js')).href);
  const rendererURL = pathToFileURL(path.join(bundle, 'out/renderer/index.html')).href;
  await until(() => win && !win.webContents.isLoadingMainFrame() && win.webContents.getURL() === rendererURL, 'matching shell build');
  report.rendererURL = win.webContents.getURL();
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
  await checkGeometryMotion(pages.b);
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
  // Keep the native ribbon focused when its owner closes, so its queued blur
  // runs during native teardown rather than testing only an unfocused surface.
  win.webContents.focus();
  await evaluate(() => document.querySelector('.sidebar-ribbon [data-ribbon-id]').focus());
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'ArrowDown' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'ArrowDown' });
  const ribbon = win.contentView.children.find(view => view.webContents && !view.webContents.isDestroyed()
    && view.webContents.getURL() === 'wuu-chrome://ui/chrome-overlay.html?surface=ribbon');
  await until(() => ribbon?.getVisible() && ribbon.webContents.isFocused(), 'keyboard opens and focuses native ribbon before quit');
  step = 'quit';
  report.status = 'quitting'; report.checks = ['54 real clicks across two browser tabs, Chat and Settings', 'Every guest visibility transition recorded with no repeated hide/show per action', 'Four stable compositor-paced visibility samples after each action', 'Guest identity, unsent draft and exact scroll retained throughout', 'No unexpected navigation or WebContents recreation', 'Cold first Settings visit never displays a window-covering loading surface', 'Native page follows ancestor transforms and reversals, moving menu ancestors, and concurrent effects without idle polling']; save();
  app.once('quit', (_event, exitCode) => {
    if (exitCode !== 0 || report.status !== 'quitting') return;
    report.status = 'passed'; report.normalQuit = true;
    report.checks.push('Closing the focused native ribbon owner completes without a late blur exception');
    save();
    console.log(JSON.stringify({ status: report.status, output, transitions: report.transitions.length, visibilityEvents: report.events.length, recording: report.recording, normalQuit: report.normalQuit }, null, 2));
  });
  app.quit();
}
setTimeout(() => { report.status = 'timed-out'; save(); app.exit(1); }, 100000).unref();
run().catch(async error => { report.failureInput = await failureInputState().catch(cause => ({ error: String(cause) })); report.status = 'failed'; report.error = String(error.stack); await capture('failure').catch(() => {}); save(); console.error(error); if (recording) await recording; app.exit(1); }).finally(() => { server.close(); });
