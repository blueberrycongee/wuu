// Real Electron/main/preload/Go round trips against disposable synthetic data.
// Build Electron and the core first; WUU_DESKTOP_CORE selects the tested binary.
// No inference is sent. Timings are diagnostic, not hardware-dependent gates.
// WUU_SWITCH_TURNS=3000 stresses history; WUU_SWITCH_VARIANT=narrow checks dark/20px.
// WUU_SWITCH_SAFE_MODE=0 includes plugin startup; default 1 isolates transport costs.
// WUU_SWITCH_MAIN may select a separately built baseline main-process bundle.
// WUU_SWITCH_ROUNDS controls warm repeats. Defaults live in the budget fixture.
// WUU_SWITCH_INIT_DELAY_MS injects a readiness fault; never pool it with baseline.
// WUU_SWITCH_CHECK_BUDGET=1 checks settled work counters, never wall-clock time.
// WUU_SWITCH_TRACE=1 records a Chromium trace; exclude traced runs from baselines.
// WUU_SWITCH_OUTPUT selects an evidence directory separate from fixture data.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { spawnSync } = require('node:child_process');
const childProcess = require('node:child_process');
const { syncBuiltinESMExports } = require('node:module');
const originalSpawn = childProcess.spawn;
let wireBytes = 0;
let coreSpawns = 0;
childProcess.spawn = (...args) => {
  const child = originalSpawn(...args);
  if (args[0] === process.env.WUU_DESKTOP_CORE) {
    coreSpawns++;
    child.stdout.on('data', chunk => { wireBytes += typeof chunk === 'string' ? Buffer.byteLength(chunk) : chunk.length; });
  }
  return child;
};
syncBuiltinESMExports();
const { pathToFileURL, fileURLToPath } = require('node:url');
const { app, BrowserWindow, ipcMain, contentTracing } = require('electron');
const { budget, assertSample } = require('./session-switch-budget.cjs');
const desktop = path.resolve(__dirname, '..');
const mainBundle = path.resolve(process.env.WUU_SWITCH_MAIN || path.join(desktop, 'out/main/index.js'));
const turns = Number(process.env.WUU_SWITCH_TURNS || budget.fixture.turns);
const rounds = Number(process.env.WUU_SWITCH_ROUNDS || budget.fixture.rounds);
const variant = process.env.WUU_SWITCH_VARIANT || budget.fixture.variant;
const safeMode = process.env.WUU_SWITCH_SAFE_MODE || budget.fixture.safeMode;
const initDelayMs = Number(process.env.WUU_SWITCH_INIT_DELAY_MS || 0);
assert.ok(Number.isInteger(rounds) && rounds > 0, 'Rounds must be a positive integer');
assert.ok(Number.isInteger(turns) && turns > 0, 'Turns must be a positive integer');
assert.ok(Number.isFinite(initDelayMs) && initDelayMs >= 0, 'Invalid readiness delay');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-session-switch-'));
const output = process.env.WUU_SWITCH_OUTPUT || fixture;
fs.mkdirSync(output, { recursive: true });
const traceEnabled = process.env.WUU_SWITCH_TRACE === '1';
const checkBudget = process.env.WUU_SWITCH_CHECK_BUDGET === '1';
if (checkBudget) {
  assert.deepEqual({ turns, rounds, variant, safeMode }, budget.fixture);
  assert.equal(initDelayMs, 0, 'Fault injection is not a comparable budget workload');
  assert.equal(traceEnabled, false, 'Tracing is not a comparable budget workload');
}
const home = path.join(fixture, 'home');
fs.mkdirSync(home);
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE ||= path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = safeMode;
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
const projects = Array.from({ length: 6 }, (_, i) => {
  const cwd = path.join(fixture, `project-${i}`);
  fs.mkdirSync(cwd);
  return { id: `project-${i}`, name: `Switch project ${i}`, path: cwd, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
});
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'fixture-only', model: 'fixture' } } }));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects, active_context: { kind: 'project', project_id: projects[0].id, cwd: projects[0].path } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: process.env.WUU_SWITCH_VARIANT === 'narrow' ? 'dark' : 'light' }));
const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', projects[0].path], {
  env: process.env, input: '{"jsonrpc":"2.0","id":1,"method":"thread/start","params":{}}\n', encoding: 'utf8', timeout: 30000,
});
assert.equal(boot.status, 0, boot.stderr);
const seed = spawnSync('python3', ['-c', `
import json, sqlite3, sys
from pathlib import Path
home=Path(sys.argv[1]); projects=json.loads((home/'projects.json').read_text())['projects']
db=sqlite3.connect(home/'sessions/sessions.sqlite3')
db.execute('DELETE FROM sessions')
for i,p in enumerate(projects):
    turns=int(sys.argv[2]) if i in (1,5) else 3
    sid='switch-thread-'+str(i)
    db.execute('INSERT INTO sessions (id,created_at,updated_at,title,cwd,workspace_id,provider,model,entries) VALUES (?,?,?,?,?,?,?,?,?)', (sid,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','Switch session '+str(i),p['path'],p['id'],'fixture','fixture',turns*2))
    for t in range(turns):
        for r,role in enumerate(('user','assistant')):
            text=('Session '+str(i)+' question '+str(t)) if r==0 else ('## Result '+str(t)+'\\n\\nA realistic paragraph with **formatting** and code.\\n\\n'+('Example content for history measurement. '*30)+'\\n\\n')*3
            db.execute('INSERT INTO session_messages (session_id,seq,role,content,at) VALUES (?,?,?,?,?)',(sid,t*2+r+1,role,text,'2026-01-01T00:00:00Z'))
db.commit()
`, home, String(turns)], { encoding: 'utf8' });
assert.equal(seed.status, 0, seed.stderr);
const timings = [];
const originalHandle = ipcMain.handle.bind(ipcMain);
let archiveGate;
let archiveBlocked = false;
let measuring = false;
ipcMain.handle = (channel, listener) => originalHandle(channel, async (...args) => {
  const start = performance.now();
  // Insert at invocation, not completion, so overlapping RPCs keep attribution.
  const timing = { channel, startedAt: start, ms: null };
  timings.push(timing);
  const delayInitialization = measuring && channel === 'wuu:initialize' && initDelayMs > 0;
  try {
    const result = await listener(...args);
    if (delayInitialization) await delay(initDelayMs);
    if (channel === 'wuu:thread-list-archived' && archiveGate) {
      archiveBlocked = true;
      await archiveGate;
    }
    return result;
  } finally {
    timing.ms = +(performance.now() - start).toFixed(1);
  }
});
const delay = ms => new Promise(r => setTimeout(r, ms));
const evaluate = async (win, fn, arg) => {
  const result = await win.webContents.executeJavaScript(`(async () => {
    try { return { value: await (${fn})(${JSON.stringify(arg)}) }; }
    catch (error) { return { error: String(error.stack || error) }; }
  })()`);
  if (result.error) throw new Error(result.error);
  return result.value;
};
async function waitFor(win, fn, arg, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await evaluate(win, fn, arg)) return;
    await delay(25);
  }
  throw new Error(`Timed out: ${fn}`);
}
const results = [];
// Startup automatically restores project 0 before the first measured click.
const seen = new Set([0]);
async function switchTo(win, index, scenario) {
  await waitFor(win, index => [...document.querySelectorAll('.thread-row')].some(n => n.textContent.includes(`Switch session ${index}`)), index);
  const point = await evaluate(win, async index => {
    const row = [...document.querySelectorAll('.thread-row')].find(n => n.textContent.includes(`Switch session ${index}`));
    const button = row.querySelector('.thread-row-main') || (row.matches('button') ? row : row.querySelector('button') || row);
    button.scrollIntoView({ block: 'nearest', behavior: 'instant' });
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const rect = button.getBoundingClientRect();
    window.__switchProbe = { index, longTasks: [] };
    const probe = window.__switchProbe;
    probe.observer = new PerformanceObserver(list => probe.longTasks.push(...list.getEntries().map(e => ({ start: e.startTime, duration: e.duration }))));
    probe.observer.observe({ type: 'longtask' });
    button.addEventListener('mousedown', event => {
      if (!event.isTrusted) throw new Error('Expected native mouse input');
      probe.start = event.timeStamp;
      if (window.__switchTrace) performance.mark(`switch-${index}-start`);
    }, { capture: true, once: true });
    return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) };
  }, index);
  const debug = win.webContents.debugger;
  const before = await debug.sendCommand('Performance.getMetrics');
  const start = performance.now();
  const bytesBefore = wireBytes;
  const spawnsBefore = coreSpawns;
  const offset = timings.length;
  measuring = true;
  // CDP accepts CSS viewport coordinates, including the product's page zoom.
  await debug.sendCommand('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
  await debug.sendCommand('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
  // Observe in the renderer: host polling and IPC scheduling must not define
  // the content endpoint. Two frames are a paint opportunity, not GPU proof.
  const contentFrameMs = await evaluate(win, index => new Promise((resolve, reject) => {
    const deadline = performance.now() + 30000;
    let readyFrames = 0;
    const check = () => {
      if (performance.now() > deadline) return reject(new Error('Target conversation never became visible'));
      const pane = document.querySelector(`.cached-conversation-pane[data-active="true"][data-thread-id="switch-thread-${index}"]`);
      const viewport = document.querySelector('.conversation-pane > .scroll-region');
      const rect = viewport?.getBoundingClientRect();
      const turns = pane?.getElementsByClassName('turn');
      const last = turns?.[turns.length - 1];
      const lastRect = last?.getBoundingClientRect();
      const ready = window.__switchProbe.start !== undefined && pane && !pane.closest('[inert]') &&
        last?.textContent.includes(`Session ${index} question`) && lastRect && rect &&
        lastRect.bottom > rect.top && lastRect.top < rect.bottom && lastRect.height > 0;
      readyFrames = ready ? readyFrames + 1 : 0;
      if (readyFrames >= 2) {
        if (window.__switchTrace) performance.mark(`switch-${index}-content`);
        return resolve(performance.now() - window.__switchProbe.start);
      }
      requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  }), index);
  await evaluate(win, () => new Promise((resolve, reject) => {
    const deadline = performance.now() + 30000;
    const check = () => {
      if (performance.now() > deadline) return reject(new Error('Composer never became editable'));
      const input = document.querySelector('.composer textarea');
      if (input && !input.disabled && !input.readOnly) {
        input.focus();
        input.select();
        if (document.activeElement === input) return resolve();
      }
      requestAnimationFrame(check);
    };
    check();
  }));
  const draft = `Switch probe ${results.length}`;
  await win.webContents.insertText(draft);
  const interaction = await evaluate(win, draft => new Promise((resolve, reject) => {
    const deadline = performance.now() + 30000;
    let readyFrames = 0;
    const check = () => {
      if (performance.now() > deadline) return reject(new Error('Draft or send readiness never rendered'));
      const input = document.querySelector('.composer textarea');
      const button = document.querySelector('.composer-send-button');
      const ready = input?.value === draft && document.activeElement === input && !input.disabled && button && !button.disabled;
      readyFrames = ready ? readyFrames + 1 : 0;
      if (readyFrames >= 2) {
        const probe = window.__switchProbe;
        const end = performance.now();
        if (window.__switchTrace) performance.mark(`switch-${probe.index}-interactive`);
        const tasks = [...probe.longTasks, ...probe.observer.takeRecords().map(e => ({ start: e.startTime, duration: e.duration }))]
          .filter(e => e.start + e.duration > probe.start && e.start < end);
        probe.observer.disconnect();
        return resolve({ interactiveFrameMs: end - probe.start, longTasks: tasks.length, longTaskMs: tasks.reduce((sum, e) => sum + Math.min(end, e.start + e.duration) - Math.max(probe.start, e.start), 0) });
      }
      requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  }), draft);
  measuring = false;
  const after = await debug.sendCommand('Performance.getMetrics');
  const prior = Object.fromEntries(before.metrics.map(m => [m.name, m.value]));
  const renderer = Object.fromEntries(after.metrics
    .filter(m => ['LayoutCount', 'RecalcStyleCount', 'LayoutDuration', 'RecalcStyleDuration', 'ScriptDuration', 'TaskDuration'].includes(m.name))
    .map(m => [m.name, m.value - prior[m.name]]));
  const result = {
    index, scenario, history: index === 1 || index === 5 ? 'large' : 'small',
    firstOpen: !seen.has(index), via: 'native-sidebar-click', contentFrameMs, ...interaction,
    hostEnvelopeMs: performance.now() - start, wireBytes: wireBytes - bytesBefore,
    coreSpawns: coreSpawns - spawnsBefore, renderer,
  };
  if (initDelayMs && !seen.has(index)) assert.ok(result.interactiveFrameMs >= initDelayMs, 'Readiness endpoint missed injected initialize delay');
  seen.add(index);
  results.push(result);
  // Drain background catalog work outside the measurement window. The archive
  // fault deliberately stays unresolved and is reported as a separate scenario.
  if (!archiveGate) {
    const deadline = Date.now() + 30000;
    let settled;
    let rpcCount;
    do {
      assert.ok(Date.now() < deadline, 'Background IPC did not settle');
      while (timings.slice(offset).some(t => t.ms === null)) {
        assert.ok(Date.now() < deadline, 'Background IPC did not settle');
        await delay(25);
      }
      // Resume responses can start catalog requests in the renderer. Include
      // those requests and their renders before finalizing the work counters.
      rpcCount = timings.length;
      await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      settled = await debug.sendCommand('Performance.getMetrics');
    } while (timings.length !== rpcCount || timings.slice(offset).some(t => t.ms === null));
    const metrics = Object.fromEntries(settled.metrics.map(m => [m.name, m.value]));
    result.work = {
      resumeCalls: timings.slice(offset).filter(t => t.channel === 'wuu:thread-resume').length,
      layoutCount: metrics.LayoutCount - prior.LayoutCount,
      recalcStyleCount: metrics.RecalcStyleCount - prior.RecalcStyleCount,
    };
  }
  // Include RPCs started during background resume and copy their final durations.
  // The archive-blocked sample intentionally retains its unresolved gate.
  result.rpc = timings.slice(offset).map(t => ({ channel: t.channel, startMs: t.startedAt - start, ms: t.ms }));
  if (!archiveGate) {
    for (const timing of result.rpc) {
      assert.ok(Number.isFinite(timing.ms), `${scenario}/${index}: ${timing.channel} lost its completed RPC duration`);
    }
    if (checkBudget && scenario === 'repeat' && result.history === 'large') assertSample(result.work);
  }
  console.log(JSON.stringify(result));
  assert.equal(await evaluate(win, () => document.querySelector('.composer textarea')?.value), draft, 'Runtime refresh lost the typed draft');
  // Clear without submitting; the fixture never invokes inference.
  await evaluate(win, () => document.querySelector('.composer textarea').select());
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Backspace' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Backspace' });
  await waitFor(win, () => document.querySelector('.composer textarea')?.value === '');
}
let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 120000 + rounds * 15000);
import(pathToFileURL(mainBundle).href).then(async () => {
  while (!main) await delay(25);
  main.webContents.on('console-message', (_e, level, message) => { if (level >= 3) console.error(message); });
  await waitFor(main, () => document.querySelector('.composer textarea'));
  main.show();
  main.focus();
  await evaluate(main, enabled => { window.__switchTrace = enabled; }, traceEnabled);
  main.webContents.debugger.attach('1.3');
  await main.webContents.debugger.sendCommand('Performance.enable');
  main.setSize(process.env.WUU_SWITCH_VARIANT === 'narrow' ? 820 : 1380, 860);
  if (process.env.WUU_SWITCH_VARIANT === 'narrow') await evaluate(main, () => {
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
  });
  console.log('FIXTURE', fixture);
  await waitFor(main, () => document.querySelector('.cached-conversation-pane[data-active="true"][data-thread-id="switch-thread-0"]'));
  await evaluate(main, () => { for (const group of document.querySelectorAll('.project-group')) { const button = group.querySelector('button[aria-expanded="false"]'); button?.click(); } });
  const startupDeadline = Date.now() + 30000;
  while (timings.some(t => t.ms === null)) {
    assert.ok(Date.now() < startupDeadline, 'Startup IPC did not settle');
    await delay(25);
  }
  if (traceEnabled) await contentTracing.startRecording({
    included_categories: ['devtools.timeline', 'blink.user_timing', 'v8', 'disabled-by-default-devtools.timeline.stack'],
    recording_mode: 'record-until-full', trace_buffer_size_in_kb: 256 * 1024,
  });
  for (const index of [1, 5, 0]) await switchTo(main, index, 'initial-pass');
  for (let round = 0; round < rounds; round++) {
    for (const index of [1, 5, 0]) await switchTo(main, index, 'repeat');
  }
  for (const index of [2, 3, 4, 5, 0, 1, 5, 0]) await switchTo(main, index, 'pool-churn');
  let releaseArchive;
  archiveGate = new Promise(resolve => { releaseArchive = resolve; });
  await switchTo(main, 5, 'archive-blocked');
  const archiveDeadline = Date.now() + 30000;
  while (!archiveBlocked) {
    assert.ok(Date.now() < archiveDeadline, 'Archive blocking scenario never reached its gate');
    await delay(25);
  }
  releaseArchive();
  archiveGate = undefined;
  // Cached UI can be interactive before its background resume returns. Drain
  // that work before subscribing for the separate snapshot protocol check.
  await evaluate(main, () => window.wuu.initialize());
  // An IPC barrier ensures all core notifications preceding initialize have
  // arrived before checking that resume still publishes exactly one snapshot.
  await evaluate(main, async expectedTurns => {
    const snapshots = [];
    const unsubscribe = window.wuu.onServerEvent(event => {
      if (event.kind === 'notification' && event.message.method === 'thread/resumed') snapshots.push(event.message.params);
    });
    const result = await window.wuu.resumeThread('switch-thread-5');
    await window.wuu.initialize();
    if (result.thread.turns.length !== expectedTurns) throw new Error('Resume lost conversation history');
    if (snapshots.length !== 1 || JSON.stringify(snapshots[0]) !== JSON.stringify(result)) throw new Error('Resume snapshot missing, duplicated, or changed');
    snapshots.length = 0;
    let rejected = false;
    try { await window.wuu.resumeThread('missing-switch-fixture'); } catch { rejected = true; }
    await window.wuu.initialize();
    unsubscribe();
    if (!rejected || snapshots.length) throw new Error('Failed resume published a snapshot or did not reject');
  }, turns);
  if (traceEnabled) {
    const usage = await contentTracing.getTraceBufferUsage();
    await contentTracing.stopRecording(path.join(output, 'trace.json'));
    assert.ok(usage.percentage < 1, 'Trace buffer filled; recording is incomplete');
  }
  const groups = {};
  for (const result of results) {
    const key = `${result.scenario}/${result.history}/${result.firstOpen ? 'first' : 'revisit'}/spawn-${result.coreSpawns}`;
    (groups[key] ||= []).push(result);
  }
  // Nearest-rank quantiles. Small groups remain diagnostics, not stable tails.
  const summary = Object.fromEntries(Object.entries(groups).map(([key, samples]) => [key, {
    n: samples.length,
    ...Object.fromEntries(['contentFrameMs', 'interactiveFrameMs', 'wireBytes', 'longTasks'].map(metric => {
      const sorted = samples.map(s => s[metric]).sort((a, b) => a - b);
      return [metric, { p50: sorted[Math.ceil(sorted.length * .5) - 1], p75: sorted[Math.ceil(sorted.length * .75) - 1], min: sorted[0], max: sorted.at(-1) }];
    })),
  }]));
  const hash = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  const git = args => {
    const result = spawnSync('git', args, { cwd: desktop, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    return result.stdout.trim();
  };
  const rendererAssets = path.join(path.dirname(fileURLToPath(main.webContents.getURL())), 'assets');
  const metadata = {
    schemaVersion: 3, recordedAt: new Date().toISOString(), sourceCommit: git(['rev-parse', 'HEAD']),
    sourceChanges: git(['status', '--short']), platform: process.platform, arch: process.arch,
    osRelease: os.release(), cpu: os.cpus()[0].model, cpuCount: os.cpus().length,
    versions: process.versions, turns, rounds, safeMode, variant, initDelayMs,
    traceEnabled, checkBudget, budget: checkBudget ? budget : null,
    zoomFactor: main.webContents.getZoomFactor(), windowSize: main.getSize(),
    coreSha256: hash(process.env.WUU_DESKTOP_CORE), harnessSha256: hash(__filename),
    mainSha256: hash(mainBundle),
    preloadSha256: hash(path.resolve(path.dirname(mainBundle), '../preload/index.cjs')),
    rendererAssets: Object.fromEntries(fs.readdirSync(rendererAssets).sort().map(name => [name, hash(path.join(rendererAssets, name))])),
    endpoint: 'Native mousedown timestamp to target pane intersecting viewport for two animation frames; then native draft insertion, focus and enabled Send for two frames. Includes probe IPC overhead; frames do not prove physical display presentation.',
    attribution: 'RPC durations are main handler envelopes (overlapping, not additive). Core stdout bytes include all clients/background work. Disk and network are not separately measured. No inference; safe mode excludes normal plugin startup. Interaction-window CDP counters are diagnostic. Settled work counters additionally cover background resume and two frames; only repeated large-fixture samples use the opt-in budget gate. Both include observer and draft-probe work.',
  };
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ metadata, summary, results, timings }, null, 2));
  const report = [
    '# Session switch baseline', '',
    `Source: ${metadata.sourceCommit}. ${metadata.platform}/${metadata.arch}; Electron ${process.versions.electron}; ${metadata.turns} turns; safe mode ${metadata.safeMode}; delay ${initDelayMs}ms.`, '',
    metadata.endpoint, '', metadata.attribution, '',
    `Work budget: ${checkBudget ? 'enforced' : 'diagnostic only'}. Trace: ${traceEnabled ? 'enabled; exclude from timing baselines' : 'disabled'}.`, '',
    '| Repeated large-fixture work | Observed maximum | Configured ceiling |',
    '| --- | ---: | ---: |',
    ...Object.entries(budget.ceilings).map(([counter, ceiling]) => `| ${counter} | ${Math.max(...results.filter(r => r.scenario === 'repeat' && r.history === 'large').map(r => r.work[counter]))} | ${ceiling} |`), '',
    '| Scenario / history / first visit / observed spawns | n | Content P50 / P75 (ms) | Interactive P50 / P75 (ms) |',
    '| --- | ---: | ---: | ---: |',
    ...Object.entries(summary).map(([key, s]) => `| ${key} | ${s.n} | ${s.contentFrameMs.p50.toFixed(1)} / ${s.contentFrameMs.p75.toFixed(1)} | ${s.interactiveFrameMs.p50.toFixed(1)} / ${s.interactiveFrameMs.p75.toFixed(1)} |`),
    '', 'Wall-clock samples are diagnostic, not thresholds. Do not pool scenarios, runtimes, or traced runs, or compare against the old paintMs/readyMs definition. Full samples and build hashes are in results.json.', '',
  ].join('\n');
  fs.writeFileSync(path.join(output, 'report.md'), report);
  fs.writeFileSync(path.join(output, 'final.png'), (await main.webContents.capturePage()).toPNG());
  console.log('RESULTS', path.join(output, 'results.json'));
  clearTimeout(timeout);
  app.quit();
}).catch(async error => {
  if (main && !main.isDestroyed()) {
    const state = await evaluate(main, () => ({
      start: window.__switchProbe?.start,
      active: document.querySelector('.cached-conversation-pane[data-active="true"]')?.getAttribute('data-thread-id'),
      viewport: document.querySelector('.conversation-pane > .scroll-region')?.getBoundingClientRect().toJSON(),
      tail: [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .turn')].slice(-1).map(n => ({ rect: n.getBoundingClientRect().toJSON(), text: n.textContent.slice(0, 100) })),
    }));
    fs.writeFileSync(path.join(output, 'failure-state.json'), JSON.stringify(state, null, 2));
    fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
  }
  fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: String(error.stack || error), results, timings }, null, 2));
  console.error(error, 'FIXTURE', fixture); app.exit(1);
});
