// Diagnostic shipping-main/preload/renderer benchmark. Run the same harness
// against both builds; never record video or enable tracing during timed phases.
// WUU_BROWSER_PERF_ROOT selects a baseline checkout without modifying its source.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawn, spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app, ipcMain, webContents } = require('electron');

const root = path.resolve(process.env.WUU_BROWSER_PERF_ROOT || path.join(__dirname, '../..'));
const desktop = path.join(root, 'desktop');
const output = path.resolve(process.env.WUU_BROWSER_PERF_OUTPUT || path.join(desktop, 'out/e2e/browser-window-perf'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-browser-perf-'));
const home = path.join(fixture, 'wuu-home');
const project = path.join(fixture, 'project');
for (const dir of [output, home, project, path.join(fixture, 'user-home')]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.HOME = process.env.USERPROFILE = path.join(fixture, 'user-home');
process.env.CODEX_HOME = path.join(process.env.HOME, '.codex');
process.env.GROK_HOME = path.join(process.env.HOME, '.grok');
process.env.XDG_CONFIG_HOME = path.join(fixture, 'xdg-config');
process.env.XDG_DATA_HOME = path.join(fixture, 'xdg-data');
for (const name of Object.keys(process.env)) if (/(?:API_KEY|ACCESS_TOKEN|AUTH_TOKEN|SECRET|PASSWORD)$/.test(name)) delete process.env[name];
Object.assign(process.env, {
  WUU_HOME: home, WUU_DESKTOP_CORE: path.join(desktop, 'build/bin/wuu-core'),
  WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '1', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0',
  WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1',
});
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
  default_provider: 'fixture',
  providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic-not-a-credential', model: 'fixture' } },
  engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
}));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({
  projects: [{ id: 'perf-project', name: 'Performance fixture', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }],
  active_context: { kind: 'project', project_id: 'perf-project', cwd: project },
}));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 15 }));
const duration = Number(process.env.WUU_BROWSER_PERF_PHASE_MS || 6000);
assert(duration >= 6000 && duration <= 10000, 'Timed phases must be bounded to 6–10 seconds');
const pointer = process.platform === 'linux' && process.env.WUU_BROWSER_PERF_POINTER === '1';
const report = {
  status: 'running', startedAt: new Date().toISOString(), fixtureDirectory: fixture,
  commit: spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).stdout.trim(),
  harnessSha256: createHash('sha256').update(fs.readFileSync(__filename)).digest('hex'),
  environment: { platform: process.platform, arch: process.arch, electron: process.versions.electron, chromium: process.versions.chrome,
    cpus: os.cpus().length, cpuModel: os.cpus()[0]?.model, phaseMs: duration, display: process.env.DISPLAY || null,
    motion: pointer ? 'X11/Openbox native pointer Alt+button1 move, Alt+button3 resize' : 'programmatic BrowserWindow.setBounds; not a native pointer/live-resize-loop test' },
  limitations: ['Synthetic local animated/scrolling content; no third-party accounts or network pages.',
    'rAF intervals measure renderer scheduling, not compositor/presentation FPS or tearing.',
    'Input probes use Electron sendInputEvent, not physical keyboard input; dispatch-to-HTTP-ack includes loopback/main-loop scheduling.',
    'Virtual/cloud display and GPU differ from a user desktop. macOS pointer/live-resize acceptance remains separate.',
    'Minimal counters add the same overhead to both builds. No video, tracing, capturePage or CDP during timed phases.'],
  phases: [], errors: [],
};
const save = () => fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify(report, null, 2) + '\n');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
let win, guest, active, inputSerial = 0, pendingInputs = new Map();
const originalHandle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => originalHandle(channel, (...args) => {
  if (['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(channel)) throw new Error('Inference is forbidden in the performance fixture');
  if (active) active.ipc[channel] = (active.ipc[channel] || 0) + 1;
  return handler(...args);
});
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created;
  for (const name of ['move', 'moved', 'resize', 'resized', 'will-move', 'will-resize']) win.on(name, () => {
    if (active) { active.windowEvents[name] = (active.windowEvents[name] || 0) + 1; active.windowBounds.push(win.getBounds()); }
  });
  const send = win.webContents.send.bind(win.webContents);
  win.webContents.send = (channel, ...args) => {
    if (active && /resize|workspace/.test(channel)) active.sent[channel] = (active.sent[channel] || 0) + 1;
    return send(channel, ...args);
  };
  win.webContents.on('render-process-gone', (_event, detail) => { report.errors.push(detail); });
});

const website = `<!doctype html><meta charset="utf-8"><title>Browser motion performance fixture</title>
<style>body{margin:0;font:16px system-ui;background:#edf4fa;color:#17354a}header{height:80px;padding:20px;background:#fff}#viewport{height:calc(100vh - 120px);overflow:auto}main{display:grid;grid-template-columns:repeat(auto-fit,minmax(210px,1fr));gap:16px;padding:22px}.card{height:120px;padding:14px;background:white;border-radius:12px;box-shadow:0 1px 8px #1232}.bar{width:30%;height:8px;background:#62a6d8;animation:slide 1.2s linear infinite alternate}@keyframes slide{to{transform:translateX(160%)}}#response{display:inline-block;margin-left:20px}</style>
<header>Animated and scrolling local website <output id="response">0</output><p>96 cards, CSS transforms, continuous viewport scroll. No external requests.</p></header>
<div id="viewport"><main>${Array.from({ length: 96 }, (_, i) => `<section class="card"><h3>Local card ${i + 1}</h3><div class="bar"></div><p>Responsive website content and input feedback.</p></section>`).join('')}</main></div>
<script>const viewport=document.querySelector('#viewport');let direction=1,last=0;function animate(now){if(last){viewport.scrollTop+=direction*(now-last)*0.14;if(viewport.scrollTop+viewport.clientHeight>=viewport.scrollHeight-2)direction=-1;else if(viewport.scrollTop<=0)direction=1;}last=now;requestAnimationFrame(animate);}requestAnimationFrame(animate);addEventListener('keydown',event=>{if(/^Key[A-Z]$/.test(event.code)){const serial=event.code.charCodeAt(3)-64+(event.shiftKey?26:0);document.querySelector('#response').textContent=serial;fetch('/input?serial='+serial);}});</script>`;
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  if (url.pathname === '/input') {
    const sent = pendingInputs.get(Number(url.searchParams.get('serial')));
    if (sent && active && sent.phase === active.name) active.inputAckMs.push(performance.now() - sent.at);
    pendingInputs.delete(Number(url.searchParams.get('serial')));
    res.writeHead(204).end(); return;
  }
  res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' }); res.end(website);
});
async function evaluate(wc, fn, arg) {
  return wc.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
}
async function until(fn, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await fn()) return; await delay(50); }
  throw new Error(`Timed out: ${label}`);
}
function stats(samples) {
  const values = [...samples].sort((a, b) => a - b);
  const percentile = p => values.length ? values[Math.min(values.length - 1, Math.ceil(values.length * p) - 1)] : null;
  return { count: values.length, p50Ms: percentile(0.5), p95Ms: percentile(0.95), maxMs: values.at(-1) ?? null,
    stallsOver50Ms: values.filter(value => value > 50).length, totalOver50Ms: values.filter(value => value > 50).reduce((sum, value) => sum + value, 0) };
}
// Installed once outside measurement. Counters do no extra geometry reads.
function installProbe() {
  let current = null, previous = 0;
  const state = { start() { previous = 0; current = { startedAt: performance.now(), initialFrameGapMs: null, frames: [], longTasks: [], rectReads: 0, queryScans: 0, inputToFrame: [], visibility: document.visibilityState }; },
    stop() { const result = current; result.elapsedMs = performance.now() - result.startedAt; result.trailingFrameGapMs = previous ? performance.now() - previous : result.elapsedMs; current = null; return result; } };
  window.__browserPerf = state;
  const rect = Element.prototype.getBoundingClientRect;
  Element.prototype.getBoundingClientRect = function (...args) { if (current) current.rectReads++; return rect.apply(this, args); };
  for (const proto of [Element.prototype, Document.prototype]) {
    const query = proto.querySelectorAll;
    proto.querySelectorAll = function (...args) { if (current) current.queryScans++; return query.apply(this, args); };
  }
  state.longTasksSupported = PerformanceObserver.supportedEntryTypes.includes('longtask');
  if (state.longTasksSupported) new PerformanceObserver(list => { if (current) current.longTasks.push(...list.getEntries().map(entry => entry.duration)); }).observe({ type: 'longtask' });
  addEventListener('keydown', event => { if (current && /^Key[A-Z]$/.test(event.code)) { const at = performance.now(), phase = current; requestAnimationFrame(() => { if (current === phase) current.inputToFrame.push(performance.now() - at); }); } });
  function frame(now) { if (current) { if (previous) current.frames.push(now - previous); else current.initialFrameGapMs = performance.now() - current.startedAt; previous = now; } requestAnimationFrame(frame); }
  requestAnimationFrame(frame);
  return { longTasksSupported: state.longTasksSupported };
}
function nativeDrag(kind, bounds) {
  const start = kind === 'move' ? { x: bounds.x + 700, y: bounds.y + 100 } : { x: bounds.x + bounds.width - 25, y: bounds.y + bounds.height - 25 };
  const button = kind === 'move' ? '1' : '3';
  const args = ['mousemove', '--sync', String(start.x), String(start.y), 'keydown', 'Alt_L', 'mousedown', button];
  const frames = Math.ceil(duration / (1000 / 60));
  for (let i = 1; i <= frames; i++) {
    const wave = Math.sin(i / frames * Math.PI * 4);
    args.push('mousemove', String(Math.round(start.x + wave * (kind === 'move' ? 95 : 135))), String(Math.round(start.y + wave * 45)), 'sleep', '0.016667');
  }
  args.push('mouseup', button, 'keyup', 'Alt_L');
  return new Promise((resolve, reject) => {
    const child = spawn('xdotool', args, { stdio: ['ignore', 'ignore', 'pipe'] }); let stderr = '';
    child.stderr.on('data', bytes => { stderr += bytes; });
    const timer = setTimeout(() => { child.kill('SIGTERM'); reject(new Error('Native drag exceeded its bound')); }, duration + 5000);
    child.on('error', error => { clearTimeout(timer); reject(error); });
    child.on('close', code => { clearTimeout(timer); code === 0 ? resolve() : reject(new Error(`xdotool exited ${code}: ${stderr}`)); });
  });
}
async function phase(name) {
  win.setBounds({ x: 150, y: 120, width: 1240, height: 800 }); win.show(); win.focus(); guest.focus();
  await delay(1000);
  await Promise.all([evaluate(win.webContents, () => { window.__browserPerf.start(); }), evaluate(guest, () => { window.__browserPerf.start(); })]);
  const result = { name, ipc: {}, sent: {}, windowEvents: {}, windowBounds: [win.getBounds()], nativeSetBounds: 0, nativeSetVisible: 0, nativeSetZoomFactor: 0, nativeReparent: 0, nativeChrome: {}, inputSent: 0, inputAckMs: [], mainIntervalsMs: [] };
  active = result;
  const started = performance.now(); let previous = started;
  const clock = setInterval(() => { const now = performance.now(); result.mainIntervalsMs.push(now - previous); previous = now; }, 16);
  const input = setInterval(() => {
    pendingInputs.set(++inputSerial, { at: performance.now(), phase: name }); result.inputSent++;
    // Encode probe identity in the native key, rather than counting received
    // events: one dropped key must not shift every later latency measurement.
    // Three phases of at most ten seconds need fewer than 52 distinct keys.
    const keyCode = String.fromCharCode(65 + (inputSerial - 1) % 26), modifiers = inputSerial > 26 ? ['shift'] : [];
    guest.sendInputEvent({ type: 'keyDown', keyCode, modifiers }); guest.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
  }, 750);
  let motion, motionError;
  try {
    if (name === 'idle') await delay(duration);
    else if (pointer) await nativeDrag(name, win.getBounds());
    else {
      motion = setInterval(() => {
        const wave = Math.sin((performance.now() - started) / duration * Math.PI * 4);
        win.setBounds(name === 'move' ? { x: Math.round(150 + wave * 95), y: Math.round(120 + wave * 45) }
          : { width: Math.round(1240 + wave * 135), height: Math.round(800 + wave * 45) });
      }, 1000 / 60);
      await delay(duration);
    }
  } catch (error) { motionError = error; }
  finally { clearInterval(motion); clearInterval(input); clearInterval(clock); }
  result.elapsedMs = performance.now() - started;
  active = null;
  const [host, page] = await Promise.all([evaluate(win.webContents, () => window.__browserPerf.stop()), evaluate(guest, () => window.__browserPerf.stop())]);
  const summarize = sample => ({ frameIntervals: stats(sample.frames), longTasks: stats(sample.longTasks), rectReads: sample.rectReads,
    queryScans: sample.queryScans, elapsedMs: sample.elapsedMs, initialFrameGapMs: sample.initialFrameGapMs, trailingFrameGapMs: sample.trailingFrameGapMs, inputEventToNextFrame: stats(sample.inputToFrame), visibility: sample.visibility });
  result.host = summarize(host); result.guest = summarize(page);
  result.raw = { host, guest: page, mainIntervalsMs: result.mainIntervalsMs, inputAckMs: result.inputAckMs };
  result.mainIntervals = stats(result.mainIntervalsMs); result.inputDispatchToAck = stats(result.inputAckMs);
  delete result.mainIntervalsMs; delete result.inputAckMs;
  result.boundsRange = Object.fromEntries(['x', 'y', 'width', 'height'].map(key => [key, Math.max(...result.windowBounds.map(b => b[key])) - Math.min(...result.windowBounds.map(b => b[key]))]));
  report.phases.push(result); save();
  if (motionError) throw motionError;
  // Verify settled alignment outside the timed interval, without screenshots.
  const view = win.contentView.children.find(child => child.webContents === guest);
  await until(async () => {
    const host = await evaluate(win.webContents, () => { const r = document.querySelector('.web-workspace-host').getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; });
    const bounds = view.getBounds(), zoom = win.webContents.getZoomFactor();
    result.settledGeometry = { host, bounds, zoom, visible: view.getVisible() };
    return view.getVisible() && ['x', 'y', 'width', 'height'].every(key => Math.abs(bounds[key] - Math.round(host[key] * zoom)) <= 2);
  }, `${name}: native view aligns after motion`, 3000);
  save();
  console.log(JSON.stringify({ phase: name, elapsedMs: result.elapsedMs, host: result.host, guest: result.guest, boundsRange: result.boundsRange, nativeSetBounds: result.nativeSetBounds, ipc: result.ipc, input: result.inputDispatchToAck }));
  assert(host.frames.length > 30 && page.frames.length > 30, `${name}: both visible renderers must collect frames`);
  assert(result.inputDispatchToAck.count >= 3, `${name}: native guest must acknowledge input during motion`);
  if (name === 'move') assert(result.boundsRange.x > 80 && result.boundsRange.width < 4, 'Move must change actual window position without changing its size');
  if (name === 'resize') assert(result.boundsRange.width > 120, 'Resize must change actual native window width');
}
const watchdog = setTimeout(() => { report.status = 'timed-out'; save(); app.exit(1); }, 150000);
async function run() {
  for (const file of ['out/main/index.js', 'out/preload/index.cjs', 'out/renderer/index.html', 'build/bin/wuu-core']) assert(fs.existsSync(path.join(desktop, file)), `Build required: ${file}`);
  report.buildSha256 = Object.fromEntries(['out/main/index.js', 'out/preload/index.cjs', 'out/renderer/index.html'].map(file => [file, createHash('sha256').update(fs.readFileSync(path.join(desktop, file))).digest('hex')]));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}/animated`;
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.webContents.isLoadingMainFrame() && win.webContents.getURL() === pathToFileURL(path.join(desktop, 'out/renderer/index.html')).href, 'shipping app loaded');
  win.setBounds({ x: 150, y: 120, width: 1240, height: 800 }); win.show(); win.focus();
  await until(() => evaluate(win.webContents, () => !!window.wuu && !!document.querySelector('.sidebar-ribbon')), 'shipping ribbon ready');
  const tabID = await evaluate(win.webContents, async url => {
    // Fixture creation precedes every timed phase. The same harness runs on
    // the old A/B baseline and the current flat-ribbon browser model.
    if (window.wuu.webWorkspace.initializeTabs) {
      await window.wuu.webWorkspace.initializeTabs([]);
      const state = await window.wuu.webWorkspace.createTab('ribbon-browser', url);
      return state.activeTabId;
    }
    localStorage.setItem('wuu.navigation-ribbon.v1', JSON.stringify({ websites: [{ id: 'website:performance', title: 'Performance fixture', url }], order: [] }));
    dispatchEvent(new Event('wuu-navigation-ribbon-change'));
    return 'website:performance';
  }, url);
  await until(() => evaluate(win.webContents, id => !!document.querySelector(`.sidebar-ribbon button[data-ribbon-id="${id}"]`), tabID), 'local website ribbon');
  // Fixture selection is setup only, not a clickthrough assertion. A synthetic
  // shell mouseDown can expand/focus a different native chrome WebContents,
  // which a subsequent shell-only mouseMove cannot leave. Invoke the existing
  // selection control without generating a fictitious hover on either build.
  if (process.platform === 'linux') {
    const bounds = win.getContentBounds();
    const moved = spawnSync('xdotool', ['mousemove', String(bounds.x + 400), String(bounds.y + 100)], { encoding: 'utf8', timeout: 5000 });
    assert.equal(moved.status, 0, moved.stderr || String(moved.error));
  }
  await evaluate(win.webContents, id => document.querySelector(`.sidebar-ribbon button[data-ribbon-id="${id}"]`).click(), tabID);
  report.fixtureSelection = 'DOM selection control for setup only; no UI interaction claim';
  await until(() => evaluate(win.webContents, () => document.querySelector('.sidebar-ribbon')?.dataset.expanded !== 'true'), 'fixture pointer leaves the hover rail');
  await until(() => { guest = webContents.getAllWebContents().find(wc => !wc.isDestroyed() && wc.getURL() === url && !wc.isLoadingMainFrame()); return guest; }, 'production native website');
  await until(() => win.contentView.children.some(view => view.webContents === guest && view.getVisible()), 'native guest visible');
  const view = win.contentView.children.find(view => view.webContents === guest);
  for (const [method, key] of [['setBounds', 'nativeSetBounds'], ['setVisible', 'nativeSetVisible']]) {
    const original = view[method].bind(view); view[method] = (...args) => { if (active) active[key]++; return original(...args); };
  }
  const zoom = guest.setZoomFactor.bind(guest);
  guest.setZoomFactor = (...args) => { if (active) active.nativeSetZoomFactor++; return zoom(...args); };
  const watchedChrome = new Set();
  const chromeCounter = child => {
    const url = child.webContents?.getURL();
    if (!active || !url?.startsWith('wuu-chrome://ui/')) return null;
    const kind = new URL(url).searchParams.get('surface');
    return active.nativeChrome[kind] ||= { boundsCalls: 0, visibilityChanges: 0, reparentCalls: 0 };
  };
  const watchChrome = child => {
    if (child === view || !child.webContents || watchedChrome.has(child)) return;
    watchedChrome.add(child);
    for (const method of ['setBounds', 'setVisible']) {
      const original = child[method].bind(child);
      child[method] = (...args) => {
        const before = child.getVisible(), result = original(...args), counter = chromeCounter(child);
        if (counter && method === 'setBounds') counter.boundsCalls++;
        if (counter && method === 'setVisible' && before !== child.getVisible()) counter.visibilityChanges++;
        return result;
      };
    }
  };
  for (const child of win.contentView.children) watchChrome(child);
  const addChild = win.contentView.addChildView.bind(win.contentView);
  win.contentView.addChildView = (...args) => {
    if (active && args[0] === view) active.nativeReparent++;
    watchChrome(args[0]);
    const counter = chromeCounter(args[0]);
    if (counter) counter.reparentCalls++;
    return addChild(...args);
  };
  report.probes = { host: await evaluate(win.webContents, installProbe), guest: await evaluate(guest, installProbe) };
  report.environment.gpuFeatureStatus = app.getGPUFeatureStatus();
  report.environment.window = win.getBounds();
  await delay(1500);
  for (const name of ['idle', 'move', 'resize']) await phase(name);
  report.status = 'passed'; save(); clearTimeout(watchdog);
  server.close(); app.quit();
  setTimeout(() => app.exit(0), 1000).unref();
}
run().catch(error => { report.status = 'failed'; report.errors.push(String(error.stack || error)); save(); console.error(error); clearTimeout(watchdog); server.close(); app.exit(1); });
