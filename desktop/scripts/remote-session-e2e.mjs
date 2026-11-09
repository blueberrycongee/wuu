// Run after electron-vite build and building desktop/build/bin/wuu-core.
// Uses real renderer/preload/main/pool, a sealed synthetic peer and real Go core.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import cp from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';
import electron from 'electron';
const { app } = electron;
const desktop = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const output = path.resolve(process.env.WUU_REMOTE_SESSION_OUTPUT || path.join(desktop, 'out/e2e/remote-session'));
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-remote-session-'));
const displayHome = path.join(scratch, 'display-home'), remoteHome = path.join(scratch, 'remote-home');
const displayProject = path.join(scratch, 'display-project'), remoteProject = path.join(scratch, 'execution-project');
for (const dir of [output, displayHome, remoteHome, displayProject, remoteProject]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(scratch, 'profile'));
Object.assign(process.env, { WUU_HOME: displayHome, WUU_DESKTOP_CORE: path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
const report = { status: 'running', platform: process.platform, hostname: os.hostname(), checks: [], localAppServerSpawns: 0,
  limitations: ['Synthetic full-control identities and in-memory relay with real sealed-channel crypto; not an Internet deployment or restricted agent delegation.', 'No real credentials or paid inference.', 'Local Git and workspace file tools are outside session-connection scope; Git polling rejected the remote execution path.', 'Keyboard submission is native Electron input; final desktop approval remains with the user.'] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const connectorPath = path.join(output, 'connector.mjs');
save();
const peer = await import(pathToFileURL(connectorPath).href);
let stream;
const provider = http.createServer((req, res) => {
  let raw = ''; req.on('data', chunk => { raw += chunk; }); req.on('end', () => {
    if (req.method === 'GET') return res.end(JSON.stringify({ data: [{ id: 'fixture' }] }));
    const body = JSON.parse(raw);
    if (!body.stream) return res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Remote session' }, finish_reason: 'stop' }] }));
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const line = text => `data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: { role: 'assistant', content: text }, finish_reason: null }] })}\n\n`;
    if (body.messages?.some(message => typeof message.content === 'string' && message.content.startsWith('You are a title generator.'))) {
      return res.end(line('Remote session') + 'data: [DONE]\n\n');
    }
    stream = res; res.write(line('Synthetic remote host is streaming.'));
  });
});
await new Promise(resolve => provider.listen(0, '127.0.0.1', resolve));
const config = { default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${provider.address().port}/v1`, api_key: 'synthetic', model: 'fixture' } },
  engines: Object.fromEntries(['codex','claude','cursor','devin','dsh','grok','hermes','pi','opencode','antigravity'].map(id => [id, { enabled: false }])) };
for (const home of [displayHome, remoteHome]) fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify(config));
fs.writeFileSync(path.join(displayHome, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light' }));
fs.writeFileSync(path.join(displayHome, 'projects.json'), JSON.stringify({ projects: [{ id: 'remote-display', name: 'Remote session acceptance', path: displayProject, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'remote-display', cwd: displayProject } }));
const routes = path.join(scratch, 'routes.json');
fs.writeFileSync(routes, JSON.stringify({ version: 1, default: { kind: 'remote', execution_host: peer.hostID, storage_host: peer.hostID, display_host: 'synthetic-display', workdir: remoteProject, connector: { kind: 'module', path: connectorPath } } }));
process.env.WUU_DESKTOP_SESSION_ROUTES = routes;
peer.configure({ core: process.env.WUU_DESKTOP_CORE, home: remoteHome, workdir: remoteProject, log: path.join(output, 'remote-core.log') });
const originalSpawn = cp.spawn;
cp.spawn = (...args) => {
  if (args[1]?.includes('app-server')) { report.localAppServerSpawns++; throw new Error('GUI attempted local execution for a selected remote route'); }
  return originalSpawn(...args);
};
syncBuiltinESMExports();
let main;
app.on('browser-window-created', (_event, window) => { main ||= window; window.webContents.on('console-message', event => { if (event.level === 'error') (report.rendererErrors ||= []).push(event.message); }); });
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, arg) => main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function until(check, label, timeout = 30000) { const deadline = Date.now() + timeout; while (Date.now() < deadline) { if (await check()) return; await delay(30); } throw new Error(`Timed out: ${label}`); }
const watchdog = setTimeout(() => { report.status = 'failed'; report.error = 'watchdog'; save(); void peer.stopFixture(); app.exit(1); }, 100000);
await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
async function run() {
  try {
    await until(() => main && evaluate(() => !!document.querySelector('.composer textarea')), 'default GUI composer');
    main.webContents.setBackgroundThrottling(false);
    await evaluate(() => {
      const input = document.querySelector('.composer textarea'); input.focus();
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Validate the configured remote session');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await until(() => evaluate(() => !!document.querySelector('.composer-send-button:not(:disabled)')), 'send enabled');
    main.focus(); main.webContents.focus();
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Return' });
    main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Return' });
    await until(() => stream && evaluate(() => document.body.innerText.includes('Synthetic remote host is streaming.')), 'remote message stream rendered');
    const { threads } = await evaluate(() => window.wuu.listThreads());
    assert.equal(threads.length, 1); report.thread = threads[0].id;
    assert.equal(threads[0].cwd, remoteProject);
    assert.deepEqual(threads[0].session_locator, { version: 1, execution_host: peer.hostID, storage_host: peer.hostID, display_host: 'synthetic-display', session_id: threads[0].id });
    const running = await evaluate(() => window.wuu.getRunningThreadsSnapshot());
    assert.ok(running.some(row => row.thread_id === threads[0].id && row.workdir === displayProject));
    report.checks.push('default GUI sent through configured remote pool', 'message stream rendered', 'remote execution cwd differs from display cwd', 'running status mapped to display context');
    fs.writeFileSync(path.join(output, 'streaming.png'), (await main.webContents.capturePage()).toPNG());
    stream.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { content: ' Remote turn completed.' }, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
    await until(() => evaluate(() => document.body.innerText.includes('Remote turn completed.')), 'completed stream rendered');
    await until(async () => !(await evaluate(() => window.wuu.getRunningThreadsSnapshot())).some(row => row.thread_id === threads[0].id), 'idle status');
    fs.writeFileSync(path.join(output, 'completed.png'), (await main.webContents.capturePage()).toPNG());
    report.checks.push('completion and idle status observed');
    assert.equal(report.localAppServerSpawns, 0);
    const previousStream = stream;
    await evaluate(id => window.wuu.startTurn(id, 'Continue running after the GUI closes'), threads[0].id);
    await until(() => stream !== previousStream && !stream.writableEnded, 'remote turn running before GUI close');
    report.beforeClose = peer.status();
    assert.ok(report.beforeClose.methods.includes('turn/start'));
    assert.equal(report.beforeClose.shutdowns, 0);
    report.coreSHA256 = createHash('sha256').update(fs.readFileSync(process.env.WUU_DESKTOP_CORE)).digest('hex');
    // Exercise the product's before-quit cleanup. The isolated peer stays alive
    // until this acceptance process explicitly disposes its fixture below.
    app.once('will-quit', event => {
      event.preventDefault();
      void (async () => {
        try {
          const detached = peer.status();
          assert.equal(detached.shutdowns, 0); assert.equal(detached.running, true);
          stream.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { content: ' Completed after GUI detach.' }, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
          await until(() => peer.status().turnCompletions > report.beforeClose.turnCompletions, 'remote turn completed after GUI detach');
          report.afterClose = peer.status();
          assert.equal(report.afterClose.shutdowns, 0);
          report.checks.push('GUI cleanup detached without shutdown; active remote turn completed afterward');
          report.status = 'passed';
        } catch (error) { report.status = 'failed'; report.error = String(error.stack || error); }
        save(); clearTimeout(watchdog); provider.close(); await peer.stopFixture(); app.exit(report.status === 'passed' ? 0 : 1);
      })();
    });
    app.quit();
  } catch (error) {
    report.status = 'failed'; report.error = String(error.stack || error); report.peer = peer.status();
    if (main && !main.isDestroyed()) { report.body = await evaluate(() => document.body.innerText).catch(() => 'unavailable'); fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG()); }
    save(); clearTimeout(watchdog); provider.close(); await peer.stopFixture(); app.exit(1);
  }
}
void app.whenReady().then(run);
