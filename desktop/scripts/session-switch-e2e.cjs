// Real Electron/main/preload/Go round trips against disposable synthetic data.
// Build Electron and the core first; WUU_DESKTOP_CORE selects the tested binary.
// No external inference is sent. Timings are diagnostic, not hardware-dependent gates.
// WUU_SWITCH_TURNS=3000 stresses history; WUU_SWITCH_VARIANT=narrow checks dark/20px.
// WUU_SWITCH_SAFE_MODE=0 includes plugin startup; default 1 isolates transport costs.
// WUU_SWITCH_MAIN may select a separately built baseline main-process bundle.
// WUU_SWITCH_STREAM=1 also streams 128 KiB from a local synthetic SSE provider
// across workspace switches, through the real core, IPC, and renderer.
// WUU_SWITCH_PACED_STREAM=1 measures native send, paced Markdown and typing.
// WUU_SWITCH_SIDEBAR_THREADS=1500 adds metadata-only sidebar history.
// WUU_SWITCH_ROUNDS controls warm repeats. Defaults live in the budget fixture.
// WUU_SWITCH_INIT_DELAY_MS injects a readiness fault; never pool it with baseline.
// WUU_SWITCH_CHECK_BUDGET=1 checks settled work counters, never wall-clock time.
// WUU_SWITCH_TRACE=1 records a Chromium trace; exclude traced runs from baselines.
// WUU_SWITCH_CPU_PROFILE=1 records renderer CPU samples; exclude from baselines.
// WUU_SWITCH_OUTPUT selects an evidence directory separate from fixture data.
// WUU_SWITCH_SUBSCRIPTION_TURNS=30000 adds ~2 GiB of unrelated subscription
// history, two built-in services, and cross-project new-draft regression checks.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { spawnSync } = require('node:child_process');
const http = require('node:http');
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
const subscriptionTurns = Number(process.env.WUU_SWITCH_SUBSCRIPTION_TURNS || 0);
const sidebarThreads = Number(process.env.WUU_SWITCH_SIDEBAR_THREADS || 0);
const pacedStream = process.env.WUU_SWITCH_PACED_STREAM === '1';
assert.ok(Number.isInteger(sidebarThreads) && sidebarThreads >= 0);
assert.ok(Number.isInteger(subscriptionTurns) && subscriptionTurns >= 0 && subscriptionTurns % 2 === 0);
assert.ok(Number.isInteger(rounds) && rounds > 0, 'Rounds must be a positive integer');
assert.ok(Number.isInteger(turns) && turns > 0, 'Turns must be a positive integer');
assert.ok(Number.isFinite(initDelayMs) && initDelayMs >= 0, 'Invalid readiness delay');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-session-switch-'));
const output = process.env.WUU_SWITCH_OUTPUT || fixture;
fs.mkdirSync(output, { recursive: true });
const traceEnabled = process.env.WUU_SWITCH_TRACE === '1';
const cpuProfileEnabled = process.env.WUU_SWITCH_CPU_PROFILE === '1';
const checkBudget = process.env.WUU_SWITCH_CHECK_BUDGET === '1';
if (checkBudget) {
  assert.equal(subscriptionTurns, 0, 'Subscription history is a separate diagnostic workload');
  assert.equal(sidebarThreads, 0, 'Large sidebar is a separate diagnostic workload');
  assert.equal(pacedStream, false, 'Paced streaming is a separate diagnostic workload');
  assert.deepEqual({ turns, rounds, variant, safeMode }, budget.fixture);
  assert.equal(initDelayMs, 0, 'Fault injection is not a comparable budget workload');
  assert.equal(traceEnabled, false, 'Tracing is not a comparable budget workload');
  assert.equal(cpuProfileEnabled, false, 'CPU profiling is not a comparable budget workload');
}
const home = path.join(fixture, 'home');
fs.mkdirSync(home);
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
{
  // Never discover real subscription credentials or start installed engines.
  process.env.HOME = path.join(fixture, 'user-home');
  fs.mkdirSync(process.env.HOME);
  process.env.CODEX_HOME = path.join(process.env.HOME, '.codex');
  process.env.GROK_HOME = path.join(process.env.HOME, '.grok');
}
process.env.WUU_DESKTOP_CORE ||= path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = safeMode;
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
// A shell inherited from make dev must still exercise this checkout's build.
delete process.env.ELECTRON_RENDERER_URL;
const projects = Array.from({ length: 6 }, (_, i) => {
  const cwd = path.join(fixture, `project-${i}`);
  fs.mkdirSync(cwd);
  return { id: `project-${i}`, name: `Switch project ${i}`, path: cwd, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
});
const sidebarProjects = Array.from({ length: sidebarThreads ? (sidebarThreads >= 5000 ? 50 : 30) : 0 }, (_, i) => {
  const cwd = path.join(fixture, `sidebar-project-${i}`);
  fs.mkdirSync(cwd);
  return { id: `sidebar-project-${i}`, name: `Sidebar project ${i}`, path: cwd, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
});
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'fixture-only', model: 'fixture' } } }));
if (subscriptionTurns) {
  const configPath = path.join(home, 'config.json');
  const config = JSON.parse(fs.readFileSync(configPath));
  config.providers['subscription-grok'] = { type: 'grok-build', model: 'grok-4.5' };
  config.providers['subscription-codex'] = { type: 'openai-codex', model: 'gpt-5', reuse_codex_credentials: true };
  config.engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(configPath, JSON.stringify(config));
}
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [...projects, ...sidebarProjects], active_context: { kind: 'project', project_id: projects[0].id, cwd: projects[0].path } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: process.env.WUU_SWITCH_VARIANT === 'narrow' ? 'dark' : 'light' }));
const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', projects[0].path], {
  env: process.env, input: '{"jsonrpc":"2.0","id":1,"method":"thread/start","params":{}}\n', encoding: 'utf8', timeout: 30000,
});
assert.equal(boot.status, 0, boot.stderr);
const seed = spawnSync('python3', ['-c', `
import json, sqlite3, sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
home=Path(sys.argv[1]); projects=json.loads((home/'projects.json').read_text())['projects']
db=sqlite3.connect(home/'sessions/sessions.sqlite3')
db.execute('DELETE FROM sessions')
for i,p in enumerate(projects[:6]):
    turns=int(sys.argv[2]) if i in (1,5) else 3
    sid='switch-thread-'+str(i)
    db.execute('INSERT INTO sessions (id,created_at,updated_at,title,cwd,workspace_id,provider,model,entries) VALUES (?,?,?,?,?,?,?,?,?)', (sid,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','Switch session '+str(i),p['path'],p['id'],'fixture','fixture',turns*2))
    for t in range(turns):
        for r,role in enumerate(('user','assistant')):
            text=('Session '+str(i)+' question '+str(t)) if r==0 else ('## Result '+str(t)+'\\n\\nA realistic paragraph with **formatting** and code.\\n\\n'+('Example content for history measurement. '*30)+'\\n\\n')*3
            db.execute('INSERT INTO session_messages (session_id,seq,role,content,at) VALUES (?,?,?,?,?)',(sid,t*2+r+1,role,text,'2026-01-01T00:00:00Z'))
for i in range(int(sys.argv[4])):
    p=None if i%5==0 else projects[6+i%(len(projects)-6)]
    created=(datetime(2026,1,1,tzinfo=timezone.utc)+timedelta(minutes=i*997%int(sys.argv[4]))).isoformat()
    updated=(datetime(2026,1,1,tzinfo=timezone.utc)+timedelta(minutes=i*991%int(sys.argv[4]))).isoformat()
    db.execute('INSERT INTO sessions (id,created_at,updated_at,title,cwd,workspace_id,provider,model,entries,pinned_at,archived_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)', ('sidebar-thread-'+str(i),created,updated,'Sidebar history '+str(i),p['path'] if p else str(home/'scratch'/str(i)),p['id'] if p and i%3!=0 else '', 'fixture','fixture',0,created if i%40==0 else None,created if i%31==0 else None))
if int(sys.argv[3]):
    sid='unrelated-subscription-history'
    db.execute('INSERT INTO sessions (id,created_at,updated_at,title,cwd,provider,model) VALUES (?,?,?,?,?,?,?)', (sid,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','Unrelated history','/synthetic-unrelated','subscription-grok','fixture'))
    for t in range(int(sys.argv[3])):
        provider='subscription-grok' if t%2==0 else 'subscription-codex'
        for r,(role,content,tokens) in enumerate([('user','Synthetic request',0),('assistant','Synthetic historical content. '*2300,0),('meta','token_usage',7),('meta','turn_terminal',7)]):
            db.execute('INSERT INTO session_messages (session_id,seq,role,content,at,provider,model,input_tokens,stop_reason) VALUES (?,?,?,?,?,?,?,?,?)', (sid,t*4+r+1,role,content,'2026-01-01T00:00:00Z',provider,'fixture',tokens,'completed' if r==3 else ''))
db.commit()
`, home, String(turns), String(subscriptionTurns), String(sidebarThreads)], { encoding: 'utf8' });
assert.equal(seed.status, 0, seed.stderr);
const timings = [];
const originalHandle = ipcMain.handle.bind(ipcMain);
let archiveGate;
let archiveBlocked = false;
let measuring = false;
let subscriptionGate;
let subscriptionEntered = false;
let failSubscription = false;
ipcMain.handle = (channel, listener) => originalHandle(channel, async (...args) => {
  const start = performance.now();
  // Insert at invocation, not completion, so overlapping RPCs keep attribution.
  const timing = { channel, startedAt: start, ms: null };
  timings.push(timing);
  const subscriptionRead = channel === 'wuu:engines-list' && args[1]?.include_quota === true;
  if (subscriptionRead) subscriptionEntered = true;
  const delayInitialization = measuring && channel === 'wuu:initialize' && initDelayMs > 0;
  try {
    const result = await listener(...args);
    if (subscriptionRead) {
      timing.historyResponseMs = +(performance.now() - start).toFixed(1);
      if (subscriptionGate) await subscriptionGate;
      if (failSubscription) throw new Error('Synthetic subscription refresh failure');
    }
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
async function clearDraft(win, expected) {
  // History restoration commits its value before the deferred caret placement.
  // Let that native history action settle before beginning a new selection.
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await evaluate(win, expected => {
    const input = document.querySelector('.composer textarea');
    if (input?.value !== expected) throw new Error('Draft changed before cleanup');
    input.focus();
  }, expected);
  const modifiers = [process.platform === 'darwin' ? 'meta' : 'control'];
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'A', modifiers });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'A', modifiers });
  await evaluate(win, expected => new Promise((resolve, reject) => {
    const deadline = performance.now() + 30000;
    let frames = 0;
    const check = () => {
      if (performance.now() > deadline) return reject(new Error('Native Select All did not preserve the full draft selection'));
      const input = document.querySelector('.composer textarea');
      const ready = input?.value === expected && document.activeElement === input && input.selectionStart === 0 && input.selectionEnd === expected.length;
      frames = ready ? frames + 1 : 0;
      if (frames >= 2) {
        window.__cleanupInput = undefined;
        input.addEventListener('beforeinput', event => {
          window.__cleanupInput = { trusted: event.isTrusted, inputType: event.inputType, value: input.value, start: input.selectionStart, end: input.selectionEnd };
        }, { once: true });
        return resolve();
      }
      requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  }), expected);
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Backspace' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Backspace' });
  await waitFor(win, () => document.querySelector('.composer textarea')?.value === '');
  assert.deepEqual(await evaluate(win, () => window.__cleanupInput), {
    trusted: true, inputType: 'deleteContentBackward', value: expected, start: 0, end: expected.length,
  }, 'Native deletion did not operate on the complete selected draft');
}

const results = [];
const subscriptionResults = [];
let startup;
async function checkSubscriptionNavigation(win) {
  if (!subscriptionTurns) return;
  async function newDraft(index, scenario) {
    await showSessionSidebar(win);
    const point = await evaluate(win, async index => {
      await document.fonts.ready;
      await Promise.all(document.getAnimations().filter(a => Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
      const button = [...document.querySelectorAll('.project-row-new-thread')].find(node => node.getAttribute('aria-label')?.includes(`Switch project ${index}`));
      if (!button) throw new Error('Missing project new conversation button');
      button.scrollIntoView({ block: 'nearest', behavior: 'instant' });
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const rect = button.getBoundingClientRect();
      return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) };
    }, index);
    const rpcStart = timings.length;
    const spawnStart = coreSpawns;
    const start = performance.now();
    await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
    await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
    await waitFor(win, index => {
      const input = document.querySelector('.composer textarea');
      const workspace = document.querySelector('.composer-workspace-bar');
      return workspace?.textContent.includes(`Switch project ${index}`) && input && !input.disabled && !input.readOnly && !input.closest('[inert]') && !document.querySelector('.cached-conversation-pane[data-active="true"]') && document.activeElement === input;
    }, index);
    win.webContents.insertText('subscription navigation probe');
    await waitFor(win, () => {
      const input = document.querySelector('.composer textarea');
      const send = document.querySelector('.composer-send-button');
      return input?.value === 'subscription navigation probe' && send && !send.disabled;
    });
    await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const readyMs = +(performance.now() - start).toFixed(1);
    const initialized = await evaluate(win, () => window.wuu.initialize());
    assert.ok(initialized.workspace_root.endsWith(`project-${index}`), 'Draft opened in the wrong project');
    for (const provider of initialized.providers) {
      assert.equal(provider.latest_request, undefined, 'Initialize loaded request history');
      assert.equal(provider.local_usage, undefined, 'Initialize loaded usage history');
    }
    const result = { scenario, index, readyMs, coreSpawns: coreSpawns - spawnStart, rpc: timings.slice(rpcStart).map(t => ({ ...t })) };
    subscriptionResults.push(result);
    console.log('SUBSCRIPTION', JSON.stringify(result));
    await clearDraft(win, 'subscription navigation probe');
  }
  await newDraft(2, 'first-project-visit-new-draft');
  let release;
  subscriptionGate = new Promise(resolve => { release = resolve; });
  await evaluate(win, () => {
    window.__subscriptionResult = undefined;
    void window.wuu.listEngines({ include_quota: true }).then(result => { window.__subscriptionResult = { result }; }, error => { window.__subscriptionResult = { error: String(error) }; });
  });
  while (!subscriptionEntered) await delay(10);
  await newDraft(0, 'history-pending-running-project');
  await newDraft(2, 'history-pending-return-to-requesting-project');
  assert.equal(await evaluate(win, () => window.__subscriptionResult), undefined, 'Statistics gate was not held through navigation');
  release();
  subscriptionGate = undefined;
  await waitFor(win, () => window.__subscriptionResult);
  const snapshot = await evaluate(win, () => window.__subscriptionResult);
  assert.equal(snapshot.error, undefined);
  assert.equal(snapshot.result.subscription_providers.length, 2);
  for (const provider of snapshot.result.subscription_providers) {
    assert.equal(provider.local_usage.input_tokens, subscriptionTurns / 2 * 7);
    assert.equal(provider.local_usage.reported_turns, subscriptionTurns / 2);
    assert.equal(provider.latest_request.status, 'completed');
    assert.equal(provider.latest_request.input_tokens, 7);
  }
  failSubscription = true;
  assert.match(await evaluate(win, async () => {
    try { await window.wuu.listEngines({ include_quota: true }); return 'unexpected success'; }
    catch (error) { return String(error); }
  }), /Synthetic subscription refresh failure/);
  await newDraft(0, 'history-failed-running-project');
  failSubscription = false;
  fs.writeFileSync(path.join(output, 'subscription-navigation.png'), (await win.webContents.capturePage()).toPNG());
  // Return to a persisted conversation for the ordinary switching scenarios.
  await showSessionSidebar(win);
  await evaluate(win, () => {
    const row = [...document.querySelectorAll('.thread-row')].find(n => n.textContent.includes('Switch session 0'));
    (row.querySelector('.thread-row-main') || row).click();
  });
  await waitFor(win, () => document.querySelector('.cached-conversation-pane[data-active="true"][data-thread-id="switch-thread-0"]'));
}
async function showSessionSidebar(win) {
  await evaluate(win, () => {
    document.querySelector('.conversation-pane [data-wuu-component="sidebar-toggle"][aria-pressed="false"]')?.click();
  });
}
let providerServer;
let providerResponse;
let receiveProviderRequest;
let providerReceivedAt;
const providerRequest = new Promise(resolve => { receiveProviderRequest = resolve; });
async function startFixtureProvider() {
  if (process.env.WUU_SWITCH_STREAM !== '1' && !pacedStream) return;
  providerServer = http.createServer((req, res) => {
    console.log('FIXTURE_PROVIDER_REQUEST', req.method, req.url);
    if (req.method !== 'POST' || req.url !== '/v1/chat/completions') {
      res.writeHead(404).end();
      return;
    }
    req.resume();
    req.on('end', () => {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      providerResponse = res;
      providerReceivedAt = performance.now();
      receiveProviderRequest();
    });
  });
  await new Promise(resolve => providerServer.listen(0, '127.0.0.1', resolve));
  const configPath = path.join(home, 'config.json');
  const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
  config.providers.fixture.base_url = `http://127.0.0.1:${providerServer.address().port}/v1`;
  fs.writeFileSync(configPath, JSON.stringify(config));
}
async function checkStreaming(win) {
  if (!providerServer) return;
  if (pacedStream) return checkPacedStreaming(win);
  // Streaming switches check recovery separately from the idle draft benchmark.
  async function switchStreamingTo(index) {
    await showSessionSidebar(win);
    await evaluate(win, index => {
      const row = [...document.querySelectorAll('.thread-row')].find(n => n.textContent.includes(`Switch session ${index}`));
      (row.querySelector('.thread-row-main') || row).click();
    }, index);
    await waitFor(win, index => !!document.querySelector(`.cached-conversation-pane[data-active="true"][data-thread-id="switch-thread-${index}"]`), index);
    await waitFor(win, index => window.wuu.initialize().then(r => r.workspace_root.endsWith(`project-${index}`)), index);
  }
  await switchStreamingTo(5);
  const text = 'ISSUE429-STREAM:' + '0123456789abcdef'.repeat(8191);
  const startedAt = performance.now();
  await evaluate(win, async () => {
    window.streamFixture = { deltas: 0, completed: false };
    window.stopStreamFixture = window.wuu.onServerEvent(event => {
      if (event.kind !== 'notification') return;
      const { method, params } = event.message;
      if (params?.thread_id !== 'switch-thread-5') return;
      if (method === 'item/agentMessage/delta') window.streamFixture.deltas++;
      if (method === 'turn/completed') window.streamFixture.completed = true;
    });
    await window.wuu.startTurn('switch-thread-5', 'Run the local streaming fixture.');
  });
  await providerRequest;
  async function sendUntil(start, end) {
    for (let offset = start; offset < end; offset += 16) {
      providerResponse.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', choices: [{ index: 0, delta: { content: text.slice(offset, offset + 16) }, finish_reason: null }] })}\n\n`);
      if (offset % 1024 === 0) await new Promise(setImmediate);
    }
  }
  await sendUntil(0, 4096);
  await waitFor(win, () => document.querySelector('.conversation-pane')?.textContent.includes('ISSUE429-STREAM:'));
  const readText = () => evaluate(win, async () => {
    const result = await window.wuu.resumeThread('switch-thread-5');
    const turn = result.thread.turns.at(-1);
    return { status: turn.status, texts: turn.items.filter(item => item.type === 'agent_message').map(item => item.text) };
  });
  await waitFor(win, async () => {
    const result = await window.wuu.resumeThread('switch-thread-5');
    return result.thread.turns.at(-1).items.some(item => item.type === 'agent_message' && item.text?.length === 4096);
  });
  assert.deepEqual((await readText()).texts, [text.slice(0, 4096)]);
  await switchStreamingTo(0);
  await sendUntil(4096, 65536);
  await switchStreamingTo(5);
  await waitFor(win, async () => {
    const result = await window.wuu.resumeThread('switch-thread-5');
    return result.thread.turns.at(-1).items.some(item => item.type === 'agent_message' && item.text?.length === 65536);
  });
  assert.deepEqual((await readText()).texts, [text.slice(0, 65536)]);
  await sendUntil(65536, text.length);
  providerResponse.end(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
  await waitFor(win, () => window.streamFixture.completed);
  const final = await readText();
  assert.equal(final.status, 'completed');
  assert.deepEqual(final.texts, [text]);
  await waitFor(win, () => document.querySelector('.conversation-pane')?.textContent.includes('ISSUE429-STREAM:'));
  const deltas = await evaluate(win, () => { window.stopStreamFixture(); return window.streamFixture.deltas; });
  const result = { bytes: text.length, providerChunkBytes: 16, rendererDeltaEvents: deltas, elapsedMs: +(performance.now() - startedAt).toFixed(1), snapshots: [4096, 65536, text.length] };
  fs.writeFileSync(path.join(output, 'stream-results.json'), JSON.stringify(result, null, 2));
  console.log('STREAM', JSON.stringify(result));
  providerServer.close();
}
async function checkStartup(win, launchStart) {
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const conversationFrameMs = performance.now() - launchStart;
  await waitFor(win, () => {
    const input = document.querySelector('.composer textarea');
    if (!input || input.disabled || input.readOnly || input.closest('[inert]')) return false;
    input.focus();
    return document.activeElement === input;
  });
  await win.webContents.insertText('Startup typeability probe');
  await evaluate(win, () => new Promise((resolve, reject) => {
    const deadline = performance.now() + 30000;
    let frames = 0;
    const check = () => {
      if (performance.now() > deadline) return reject(new Error('Startup draft never became send-ready'));
      const input = document.querySelector('.composer textarea');
      const send = document.querySelector('.composer-send-button');
      frames = input?.value === 'Startup typeability probe' && document.activeElement === input && !input.disabled && send && !send.disabled ? frames + 1 : 0;
      if (frames >= 2) return resolve();
      requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  }));
  startup = { conversationFrameMs, interactiveFrameMs: performance.now() - launchStart, coreSpawns, rpc: timings.map(t => ({ ...t })) };
  console.log('STARTUP', JSON.stringify(startup));
  await clearDraft(win, 'Startup typeability probe');
}
async function checkPacedStreaming(win) {
  await evaluate(win, () => document.querySelector('.composer textarea').focus());
  await win.webContents.insertText('Run the local paced Markdown fixture.');
  await waitFor(win, () => { const send = document.querySelector('.composer-send-button'); return send && !send.disabled; });
  await evaluate(win, () => {
    const probe = window.__pacedProbe = { frames: [], longTasks: [], mutations: 0, deltas: 0 };
    probe.longObserver = new PerformanceObserver(list => probe.longTasks.push(...list.getEntries().map(e => ({ start: e.startTime, duration: e.duration }))));
    probe.longObserver.observe({ type: 'longtask' });
    probe.mutationObserver = new MutationObserver(list => { probe.mutations += list.length; });
    probe.mutationObserver.observe(document.querySelector('.conversation-pane'), { childList: true, characterData: true, subtree: true });
    probe.unsubscribe = window.wuu.onServerEvent(event => {
      if (event.kind !== 'notification') return;
      if (event.message.params?.thread_id !== 'switch-thread-5') return;
      if (event.message.method === 'item/agentMessage/delta') {
        probe.firstDeltaAt ??= performance.now();
        probe.deltas++;
      }
      if (event.message.method === 'turn/completed') probe.completedAt = performance.now();
    });
    // Capture native keyboard submission before React handles the event.
    document.addEventListener('keydown', event => {
      if (event.key !== 'Enter' || !event.target.matches('.composer textarea')) return;
      probe.start = event.timeStamp;
      probe.trusted = event.isTrusted;
    }, { capture: true, once: true });
    let firstFrames = 0;
    let finalFrames = 0;
    const tick = timestamp => {
      if (probe.stop) return;
      if (probe.start !== undefined) {
        if (probe.lastFrame !== undefined) probe.frames.push(timestamp - probe.lastFrame);
        probe.lastFrame = timestamp;
        const pane = document.querySelector('.cached-conversation-pane[data-active="true"][data-thread-id="switch-thread-5"]');
        const turn = pane?.querySelector('.turn[data-latest-turn="true"]');
        const visibleMarker = marker => {
          const node = [...(turn?.querySelectorAll('p') || [])].find(p => p.textContent.includes(marker));
          const rect = node?.getBoundingClientRect();
          const viewport = document.querySelector('.conversation-pane > .scroll-region')?.getBoundingClientRect();
          return rect && viewport && rect.height > 0 && rect.bottom > viewport.top && rect.top < viewport.bottom;
        };
        if (probe.firstContentAt === undefined) {
          firstFrames = visibleMarker('WUU-PACED-START') ? firstFrames + 1 : 0;
          if (firstFrames >= 2) probe.firstContentAt = performance.now();
        }
        if (probe.completedAt !== undefined) {
          finalFrames = visibleMarker('WUU-PACED-END') ? finalFrames + 1 : 0;
          if (finalFrames >= 2) { probe.finalContentAt = performance.now(); return; }
        }
      }
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
  const debug = win.webContents.debugger;
  const before = await debug.sendCommand('Performance.getMetrics');
  const processBefore = app.getAppMetrics();
  const hostStart = performance.now();
  const bytesBefore = wireBytes;
  const rpcStart = timings.length;
  await evaluate(win, () => document.querySelector('.composer textarea').focus());
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Enter' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Enter' });
  await Promise.race([providerRequest, delay(30000).then(() => { throw new Error('Native send did not reach the synthetic provider'); })]);
  const text = 'WUU-PACED-START\n\n' + Array.from({ length: 36 }, (_, i) =>
    `## Section ${i}\n\n` + 'A **formatted** answer with [local context](#context), a stable paragraph, and useful details. '.repeat(8) +
    '\n\n```typescript\nconst values = [1, 2, 3];\nconst result = values.map(value => value * 2);\n```\n\n' +
    '| Step | Result |\n| --- | --- |\n| Read | Complete |\n| Verify | Preserved |\n\n').join('') + 'WUU-PACED-END';
  const chunkBytes = 256;
  const cadenceMs = 16;
  const chunks = [];
  let typing;
  const streamStart = performance.now();
  for (let offset = 0; offset < text.length; offset += chunkBytes) {
    // Pace from absolute deadlines so provider cadence does not accumulate drift.
    const due = streamStart + chunks.length * cadenceMs;
    if (performance.now() < due) await delay(due - performance.now());
    chunks.push(performance.now() - hostStart);
    providerResponse.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', choices: [{ index: 0, delta: { content: text.slice(offset, offset + chunkBytes) }, finish_reason: null }] })}\n\n`);
    if (!typing && offset >= text.length / 3) {
      // The text is deliberately left unsent, and must survive stream settlement.
      typing = (async () => {
        await evaluate(win, () => {
          const input = document.querySelector('.composer textarea');
          input.focus();
          input.addEventListener('beforeinput', event => {
            window.__pacedProbe.typingStart = event.timeStamp;
            window.__pacedProbe.typingTrusted = event.isTrusted;
          }, { once: true });
        });
        await win.webContents.insertText('Draft preserved during streaming');
        return evaluate(win, () => new Promise((resolve, reject) => {
          const deadline = performance.now() + 30000;
          let frames = 0;
          const tick = () => {
            if (performance.now() > deadline) return reject(new Error('Typing during streaming never rendered'));
            const input = document.querySelector('.composer textarea');
            frames = input?.value === 'Draft preserved during streaming' && document.activeElement === input && !input.disabled ? frames + 1 : 0;
            if (frames >= 2) return resolve(performance.now() - window.__pacedProbe.typingStart);
            requestAnimationFrame(tick);
          };
          requestAnimationFrame(tick);
        }));
      })();
    }
  }
  const providerEndMs = performance.now() - hostStart;
  providerResponse.end(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
  const typingFrameMs = await typing;
  await waitFor(win, () => window.__pacedProbe.finalContentAt !== undefined);
  const probe = await evaluate(win, () => {
    const p = window.__pacedProbe;
    p.stop = true;
    p.unsubscribe();
    p.longTasks.push(...p.longObserver.takeRecords().map(e => ({ start: e.startTime, duration: e.duration })));
    p.longObserver.disconnect();
    p.mutations += p.mutationObserver.takeRecords().length;
    p.mutationObserver.disconnect();
    return { start: p.start, trusted: p.trusted, typingTrusted: p.typingTrusted, firstDeltaMs: p.firstDeltaAt - p.start, firstContentFrameMs: p.firstContentAt - p.start, completedMs: p.completedAt - p.start, finalContentFrameMs: p.finalContentAt - p.start, frames: p.frames, longTasks: p.longTasks.filter(t => t.start + t.duration > p.start && t.start < p.finalContentAt), mutations: p.mutations, rendererDeltaEvents: p.deltas };
  });
  const after = await debug.sendCommand('Performance.getMetrics');
  const processAfter = app.getAppMetrics();
  assert.ok(probe.trusted && probe.typingTrusted, 'Expected native send and text input');
  for (const timing of [probe.firstDeltaMs, probe.firstContentFrameMs, probe.completedMs, probe.finalContentFrameMs, typingFrameMs]) {
    assert.ok(Number.isFinite(timing) && timing >= 0, 'Missing renderer journey endpoint');
  }
  assert.equal(await evaluate(win, () => document.querySelector('.composer textarea')?.value), 'Draft preserved during streaming');
  const persisted = await evaluate(win, async () => (await window.wuu.resumeThread('switch-thread-5')).thread.turns.at(-1));
  assert.equal(persisted.status, 'completed');
  assert.deepEqual(persisted.items.filter(item => item.type === 'agent_message').map(item => item.text), [text]);
  const prior = Object.fromEntries(before.metrics.map(m => [m.name, m.value]));
  const renderer = Object.fromEntries(after.metrics.filter(m => ['LayoutCount', 'RecalcStyleCount', 'LayoutDuration', 'RecalcStyleDuration', 'ScriptDuration', 'TaskDuration', 'JSHeapUsedSize'].includes(m.name)).map(m => [m.name, { before: prior[m.name], after: m.value, delta: m.value - prior[m.name] }]));
  const frameGaps = [...probe.frames].sort((a, b) => a - b);
  const result = { schemaVersion: 1, submission: 'native-enter', scope: 'Native Enter submission through the selected Electron/main/preload/Go application build and paced synthetic local SSE to two renderer frame opportunities. No real model, external account, production telemetry, or physical-display proof. Frame gaps reflect this rig cadence, not a 120 Hz claim.', bytes: Buffer.byteLength(text), chunkBytes, cadenceMs, providerReceivedMs: providerReceivedAt - hostStart, providerEndMs, providerChunksAtMs: chunks, ...probe, typingFrameMs, frameSummary: { count: frameGaps.length, p50: frameGaps[Math.ceil(frameGaps.length * .5) - 1], p95: frameGaps[Math.ceil(frameGaps.length * .95) - 1], max: frameGaps.at(-1), over20Ms: frameGaps.filter(x => x > 20).length, over33_34Ms: frameGaps.filter(x => x > 33.34).length }, renderer, processBefore, processAfter, wireBytes: wireBytes - bytesBefore, rpc: timings.slice(rpcStart).map(t => ({ channel: t.channel, startMs: t.startedAt - hostStart, ms: t.ms })) };
  fs.writeFileSync(path.join(output, 'paced-stream-results.json'), JSON.stringify(result, null, 2));
  console.log('PACED_STREAM', JSON.stringify({ firstContentFrameMs: probe.firstContentFrameMs, finalContentFrameMs: probe.finalContentFrameMs, typingFrameMs, frameSummary: result.frameSummary, longTasks: probe.longTasks.length, mutations: probe.mutations, rendererDeltaEvents: probe.rendererDeltaEvents }));
  providerServer.close();
}

// Startup automatically restores project 0 before the first measured click.
const seen = new Set([0]);
async function switchTo(win, index, scenario) {
  await showSessionSidebar(win);
  await waitFor(win, index => {
    const row = [...document.querySelectorAll('.thread-row')].find(n => n.textContent.includes(`Switch session ${index}`));
    if (!row) return false;
    const button = row.querySelector('.thread-row-main') || (row.matches('button') ? row : row.querySelector('button') || row);
    button.scrollIntoView({ block: 'nearest', behavior: 'instant' });
    // A row can have a rectangle while an expanding fold still clips it or
    // moves its click target. Settle input preparation outside the measurement.
    const folding = [...document.querySelectorAll('.thread-list-collapse')]
      .some(fold => fold.getAnimations().some(animation => animation.pending || animation.playState === 'running'));
    if (folding) return false;
    const rect = button.getBoundingClientRect();
    const hit = document.elementFromPoint(Math.round(rect.x + rect.width / 2), Math.round(rect.y + rect.height / 2));
    return rect.width > 0 && rect.height > 0 && button.contains(hit);
  }, index);
  const point = await evaluate(win, async index => {
    const row = [...document.querySelectorAll('.thread-row')].find(n => n.textContent.includes(`Switch session ${index}`));
    const button = row.querySelector('.thread-row-main') || (row.matches('button') ? row : row.querySelector('button') || row);
    button.scrollIntoView({ block: 'nearest', behavior: 'instant' });
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const rect = button.getBoundingClientRect();
    window.__switchProbe = { index, longTasks: [], inputEvents: [] };
    const probe = window.__switchProbe;
    for (const type of ['mousedown', 'mouseup', 'click']) {
      button.addEventListener(type, event => {
        probe.inputEvents.push({ type, trusted: event.isTrusted, x: event.clientX, y: event.clientY });
      }, { capture: true, once: true });
    }
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
  // Check history recall and draft restoration outside the timing window.
  await evaluate(win, () => document.querySelector('.composer textarea').setSelectionRange(0, 0));
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Up' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Up' });
  const lastQuestion = `Session ${index} question ${(index === 1 || index === 5 ? turns : 3) - 1}`;
  await waitFor(win, text => document.querySelector('.composer textarea')?.value === text, lastQuestion);
  await evaluate(win, () => {
    const input = document.querySelector('.composer textarea');
    input.setSelectionRange(input.value.length, input.value.length);
  });
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Down' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Down' });
  await waitFor(win, text => document.querySelector('.composer textarea')?.value === text, draft);
  // Clear without submitting; the fixture never invokes inference.
  await clearDraft(win, draft);
}
let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 120000 + rounds * 15000 + (process.env.WUU_SWITCH_STREAM === '1' || pacedStream ? 60000 : 0));
const launchStart = performance.now();
startFixtureProvider().then(() => import(pathToFileURL(mainBundle).href)).then(async () => {
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
  await checkStartup(main, launchStart);
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
  if (cpuProfileEnabled) {
    await main.webContents.debugger.sendCommand('Profiler.enable');
    await main.webContents.debugger.sendCommand('Profiler.start');
  }
  await checkSubscriptionNavigation(main);
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
  if (cpuProfileEnabled) {
    const { profile } = await main.webContents.debugger.sendCommand('Profiler.stop');
    fs.writeFileSync(path.join(output, 'renderer.cpuprofile'), JSON.stringify(profile));
    await main.webContents.debugger.sendCommand('Profiler.disable');
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
    buildKind: process.env.WUU_SWITCH_BUILD_KIND || 'production-vite',
    schemaVersion: 4, recordedAt: new Date().toISOString(), sourceCommit: git(['rev-parse', 'HEAD']),
    sourceChanges: git(['status', '--short']),
    productSourceCommit: process.env.WUU_SWITCH_BUILD_COMMIT || git(['-C', path.dirname(mainBundle), 'rev-parse', 'HEAD']),
    productSourceChanges: git(['-C', path.dirname(mainBundle), 'status', '--short']), platform: process.platform, arch: process.arch,
    osRelease: os.release(), cpu: os.cpus()[0].model, cpuCount: os.cpus().length,
    versions: process.versions, turns, rounds, safeMode, variant, initDelayMs, subscriptionTurns, sidebarThreads, pacedStream,
    hostLoadAverage: os.loadavg(), totalMemoryBytes: os.totalmem(), freeMemoryBytes: os.freemem(),
    startupEndpoint: 'Main bundle import to restored conversation, then focused editable input, native inserted draft and enabled Send for two frames. Excludes synthetic data seeding and Electron executable startup; includes host polling and probe IPC. Cold profile with warm filesystem cache, not a physical-paint measurement.',
    databaseBytes: fs.statSync(path.join(home, 'sessions/sessions.sqlite3')).size,
    subscriptionEndpoint: 'Host CDP mouse dispatch to target workspace draft, focused editable input, inserted text and enabled Send plus two frames. Includes dispatch, polling and probe IPC overhead. Startup is measured separately from main import to the initial conversation frame, excluding fixture creation.',
    traceEnabled, cpuProfileEnabled, checkBudget, budget: checkBudget ? budget : null,
    zoomFactor: main.webContents.getZoomFactor(), windowSize: main.getSize(),
    coreSha256: hash(process.env.WUU_DESKTOP_CORE), harnessSha256: hash(__filename),
    mainSha256: hash(mainBundle),
    preloadSha256: hash(path.resolve(path.dirname(mainBundle), '../preload/index.cjs')),
    rendererAssets: Object.fromEntries(fs.readdirSync(rendererAssets).sort().map(name => [name, hash(path.join(rendererAssets, name))])),
    endpoint: 'Native mousedown timestamp to target pane intersecting viewport for two animation frames; then native draft insertion, focus and enabled Send for two frames. Includes probe IPC overhead; frames do not prove physical display presentation.',
    attribution: 'RPC durations are main handler envelopes (overlapping, not additive). Core stdout bytes include all clients/background work. Disk and network are not separately measured. No inference; safe mode excludes normal plugin startup. Interaction-window CDP counters are diagnostic. Settled work counters additionally cover background resume and two frames; only repeated large-fixture samples use the opt-in budget gate. Both include observer and draft-probe work.',
  };
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ metadata, summary, results, startup, subscriptionResults, timings }, null, 2));
  const report = [
    '# Session switch baseline', '',
    `Source: ${metadata.sourceCommit}. ${metadata.platform}/${metadata.arch}; Electron ${process.versions.electron}; ${metadata.turns} turns; safe mode ${metadata.safeMode}; delay ${initDelayMs}ms.`, '',
    metadata.endpoint, '', metadata.attribution, '',
    `Work budget: ${checkBudget ? 'enforced' : 'diagnostic only'}. Trace: ${traceEnabled ? 'enabled; exclude from timing baselines' : 'disabled'}. CPU profile: ${cpuProfileEnabled ? 'enabled; exclude from timing baselines' : 'disabled'}.`, '',
    '| Repeated large-fixture work | Observed maximum | Configured ceiling |',
    '| --- | ---: | ---: |',
    ...Object.entries(budget.ceilings).map(([counter, ceiling]) => `| ${counter} | ${Math.max(...results.filter(r => r.scenario === 'repeat' && r.history === 'large').map(r => r.work[counter]))} | ${ceiling} |`), '',
    '| Scenario / history / first visit / observed spawns | n | Content P50 / P75 (ms) | Interactive P50 / P75 (ms) |',
    '| --- | ---: | ---: | ---: |',
    ...Object.entries(summary).map(([key, s]) => `| ${key} | ${s.n} | ${s.contentFrameMs.p50.toFixed(1)} / ${s.contentFrameMs.p75.toFixed(1)} | ${s.interactiveFrameMs.p50.toFixed(1)} / ${s.interactiveFrameMs.p75.toFixed(1)} |`),
    '', 'Wall-clock samples are diagnostic, not thresholds. Do not pool scenarios, runtimes, or profiled/traced runs, or compare against the old paintMs/readyMs definition. Full samples and build hashes are in results.json.', '',
  ].join('\n');
  fs.writeFileSync(path.join(output, 'report.md'), report);
  await checkStreaming(main);
  fs.writeFileSync(path.join(output, 'final.png'), (await main.webContents.capturePage()).toPNG());
  console.log('RESULTS', path.join(output, 'results.json'));
  clearTimeout(timeout);
  app.quit();
}).catch(async error => {
  if (main && !main.isDestroyed()) {
    const state = await evaluate(main, () => ({
      paced: window.__pacedProbe && { latestTurns: [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .turn')].slice(-1).map(t => ({ latest: t.getAttribute('data-latest-turn'), lastParagraph: [...t.querySelectorAll('p')].at(-1)?.textContent })), start: window.__pacedProbe.start, trusted: window.__pacedProbe.trusted, firstDeltaAt: window.__pacedProbe.firstDeltaAt, completedAt: window.__pacedProbe.completedAt, firstContentAt: window.__pacedProbe.firstContentAt, finalContentAt: window.__pacedProbe.finalContentAt },
      cleanupInput: window.__cleanupInput,
      selection: (() => { const input = document.querySelector('.composer textarea'); return input && { start: input.selectionStart, end: input.selectionEnd, focused: document.activeElement === input }; })(),
      draft: document.querySelector('.composer textarea')?.value,
      send: document.querySelector('.composer-send-button')?.outerHTML,
      notifications: document.querySelector('[data-toast-viewport]')?.textContent,
      start: window.__switchProbe?.start,
      inputEvents: window.__switchProbe?.inputEvents,
      active: document.querySelector('.cached-conversation-pane[data-active="true"]')?.getAttribute('data-thread-id'),
      viewport: document.querySelector('.conversation-pane > .scroll-region')?.getBoundingClientRect().toJSON(),
      tail: [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .turn')].slice(-1).map(n => ({ rect: n.getBoundingClientRect().toJSON(), text: n.textContent.slice(0, 100) })),
    }));
    fs.writeFileSync(path.join(output, 'failure-state.json'), JSON.stringify(state, null, 2));
    fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
  }
  fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: String(error.stack || error), results, subscriptionResults, timings }, null, 2));
  console.error(error, 'FIXTURE', fixture); app.exit(1);
});
