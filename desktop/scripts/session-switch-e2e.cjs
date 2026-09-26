// Real Electron/main/preload/Go round trips against disposable synthetic data.
// Build Electron and the core first; WUU_DESKTOP_CORE selects the tested binary.
// No external inference is sent. Timings are diagnostic, not hardware-dependent gates.
// WUU_SWITCH_TURNS=3000 stresses history; WUU_SWITCH_VARIANT=narrow checks dark/20px.
// WUU_SWITCH_SAFE_MODE=0 includes plugin startup; default 1 isolates transport costs.
// WUU_SWITCH_MAIN may select a separately built baseline main-process bundle.
// WUU_SWITCH_STREAM=1 also streams 128 KiB from a local synthetic SSE provider
// across workspace switches, through the real core, IPC, and renderer.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const http = require('node:http');
const childProcess = require('node:child_process');
const { syncBuiltinESMExports } = require('node:module');
const originalSpawn = childProcess.spawn;
let wireBytes = 0;
childProcess.spawn = (...args) => {
  const child = originalSpawn(...args);
  if (args[0] === process.env.WUU_DESKTOP_CORE) child.stdout.on('data', chunk => { wireBytes += typeof chunk === 'string' ? Buffer.byteLength(chunk) : chunk.length; });
  return child;
};
syncBuiltinESMExports();
const { pathToFileURL } = require('node:url');
const { app, BrowserWindow, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-session-switch-'));
const home = path.join(fixture, 'home');
fs.mkdirSync(home);
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE ||= path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = process.env.WUU_SWITCH_SAFE_MODE || '1';
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
`, home, process.env.WUU_SWITCH_TURNS || '160'], { encoding: 'utf8' });
assert.equal(seed.status, 0, seed.stderr);
const timings = [];
const originalHandle = ipcMain.handle.bind(ipcMain);
let archiveGate;
ipcMain.handle = (channel, listener) => originalHandle(channel, async (...args) => {
  const start = performance.now();
  try {
    const result = await listener(...args);
    if (channel === 'wuu:thread-list-archived' && archiveGate) await archiveGate;
    return result;
  } finally {
    if (/thread-(resume|list)|initialize|project-select/.test(channel)) timings.push({ channel, ms: +(performance.now() - start).toFixed(1) });
  }
});
const delay = ms => new Promise(r => setTimeout(r, ms));
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn, arg, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await evaluate(win, fn, arg)) return;
    await delay(25);
  }
  throw new Error(`Timed out: ${fn}`);
}
const results = [];
let providerServer;
let providerResponse;
let receiveProviderRequest;
const providerRequest = new Promise(resolve => { receiveProviderRequest = resolve; });
async function startFixtureProvider() {
  if (process.env.WUU_SWITCH_STREAM !== '1') return;
  providerServer = http.createServer((req, res) => {
    if (req.method !== 'POST' || req.url !== '/v1/chat/completions') {
      res.writeHead(404).end();
      return;
    }
    req.resume();
    req.on('end', () => {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      providerResponse = res;
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
  await switchTo(win, 0);
  const text = 'ISSUE429-STREAM:' + '0123456789abcdef'.repeat(8191);
  const startedAt = performance.now();
  await evaluate(win, async () => {
    window.streamFixture = { deltas: 0, completed: false };
    window.stopStreamFixture = window.wuu.onServerEvent(event => {
      if (event.kind !== 'notification') return;
      const { method, params } = event.message;
      if (params?.thread_id !== 'switch-thread-0') return;
      if (method === 'item/agentMessage/delta') window.streamFixture.deltas++;
      if (method === 'turn/completed') window.streamFixture.completed = true;
    });
    await window.wuu.startTurn('switch-thread-0', 'Run the local streaming fixture.');
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
    const result = await window.wuu.resumeThread('switch-thread-0');
    const turn = result.thread.turns.at(-1);
    return { status: turn.status, texts: turn.items.filter(item => item.type === 'agent_message').map(item => item.text) };
  });
  await waitFor(win, async () => {
    const result = await window.wuu.resumeThread('switch-thread-0');
    return result.thread.turns.at(-1).items.some(item => item.type === 'agent_message' && item.text?.length === 4096);
  });
  assert.deepEqual((await readText()).texts, [text.slice(0, 4096)]);
  await switchTo(win, 5);
  await sendUntil(4096, 65536);
  await switchTo(win, 0);
  await waitFor(win, async () => {
    const result = await window.wuu.resumeThread('switch-thread-0');
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
  fs.writeFileSync(path.join(fixture, 'stream-results.json'), JSON.stringify(result, null, 2));
  console.log('STREAM', JSON.stringify(result));
  providerServer.close();
}
async function switchTo(win, index) {
  await waitFor(win, index => [...document.querySelectorAll('.thread-row')].some(n => n.textContent.includes(`Switch session ${index}`)), index);
  const start = performance.now();
  const bytesBefore = wireBytes;
  const offset = timings.length;
  await evaluate(win, index => {
    const row = [...document.querySelectorAll('.thread-row')].find(n => n.textContent.includes(`Switch session ${index}`));
    (row.matches('button') ? row : row.querySelector('button') || row).click();
  }, index);
  await waitFor(win, index => document.querySelector('.thread-row.active')?.textContent.includes(`Switch session ${index}`) && document.querySelector('.conversation-pane')?.textContent.includes(`Session ${index} question`), index);
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const paintMs = +(performance.now() - start).toFixed(1);
  // Configuration readiness is separate from first paint of cached history.
  await waitFor(win, index => window.wuu.initialize().then(r => r.workspace_root.endsWith(`project-${index}`)), index);
  const result = { index, via: 'sidebar', paintMs, readyMs: +(performance.now() - start).toFixed(1), wireBytes: wireBytes - bytesBefore, rpc: timings.slice(offset) };
  results.push(result);
  console.log(JSON.stringify(result));
}
let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, process.env.WUU_SWITCH_STREAM === '1' ? 180000 : 120000);
startFixtureProvider().then(() => import(pathToFileURL(process.env.WUU_SWITCH_MAIN || path.join(desktop, 'out/main/index.js')).href)).then(async () => {
  while (!main) await delay(25);
  main.webContents.on('console-message', (_e, level, message) => { if (level >= 3) console.error(message); });
  await waitFor(main, () => document.querySelector('.composer textarea'));
  main.setSize(process.env.WUU_SWITCH_VARIANT === 'narrow' ? 820 : 1380, 860);
  if (process.env.WUU_SWITCH_VARIANT === 'narrow') await evaluate(main, () => {
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
  });
  console.log('FIXTURE', fixture);
  await evaluate(main, () => { for (const group of document.querySelectorAll('.project-group')) { const button = group.querySelector('button[aria-expanded="false"]'); button?.click(); } });
  for (const index of [1, 5, 0, 1, 5, 0]) await switchTo(main, index);
  for (const index of [2, 3, 4, 5, 0, 1, 5, 0]) await switchTo(main, index);
  let releaseArchive;
  archiveGate = new Promise(resolve => { releaseArchive = resolve; });
  await switchTo(main, 5);
  releaseArchive();
  archiveGate = undefined;
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
  }, Number(process.env.WUU_SWITCH_TURNS || 160));
  await checkStreaming(main);
  fs.writeFileSync(path.join(fixture, 'results.json'), JSON.stringify({ results, timings }, null, 2));
  fs.writeFileSync(path.join(fixture, 'final.png'), (await main.webContents.capturePage()).toPNG());
  console.log('RESULTS', path.join(fixture, 'results.json'));
  clearTimeout(timeout);
  app.quit();
}).catch(error => { console.error(error, 'FIXTURE', fixture); app.exit(1); });
