// Full source-built application portion of linux-cua-e2e.cjs. The loopback
// provider scripts tool selection only: real Go/plugin/native input, activity
// notifications, production main/preload/renderer, and PiP remain unchanged.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { spawn, spawnSync, execFileSync } from 'node:child_process';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { monitorEventLoopDelay, performance } from 'node:perf_hooks';
import { app, BrowserWindow, WebContentsView, nativeImage } from 'electron';
const desktop = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const profile = process.env.WUU_LINUX_CUA_PROFILE;
assert(profile, 'Run through linux-cua-e2e.cjs with a disposable profile');
const output = path.resolve(process.env.WUU_LINUX_CUA_OUTPUT || 'out/e2e/linux-cua');
const home = path.join(profile, 'full-app-home');
const project = path.join(profile, 'full-app-project');
for (const directory of [home, project, output]) fs.mkdirSync(directory, { recursive: true });
app.setPath('userData', path.join(profile, 'full-app-profile'));
app.disableHardwareAcceleration();
app.commandLine.appendSwitch('ozone-platform', 'x11');
process.env.WUU_HOME = home;
process.env.HOME = home;
process.env.WUU_DESKTOP_CORE = path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_CUA_LINUX = '1';
process.env.WUU_ENABLE_BROWSER = '0';
// Safe mode disables all plugins, including the production Linux CUA plugin.
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
const checks = [];
const report = { status: 'running', environment: { platform: process.platform, arch: process.arch, versions: process.versions, display: process.env.DISPLAY, softwareRendering: true, virtualDisplay: process.env.WUU_LINUX_CUA_VIRTUAL_DISPLAY === '1' }, scope: 'Source-built production Electron main/preload/renderer and Go app-server turn with the bundled Linux CUA MCP plugin. Scripted loopback provider, isolated profile, no credentials or live inference; not an installed package or hardware benchmark.', checks, providerCalls: [] };
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (contents, fn, arg) => contents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
let failure, main, target, fixture, threadID, canonical, snapshot, toolName, serial = 0, stage = 0;
const replies = new Map();
const held = new Map();
const events = [];
async function waitFor(label, predicate, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (failure) throw failure;
    const value = await predicate();
    if (value) return value;
    await delay(50);
  }
  throw new Error(`Timed out: ${label}`);
}
function fixtureRequest(command, value) {
  return new Promise((resolve, reject) => {
    const id = ++serial;
    const timer = setTimeout(() => { replies.delete(id); reject(new Error(`Target timed out: ${command}`)); }, 5000);
    replies.set(id, { resolve: result => { clearTimeout(timer); resolve(result); }, reject });
    fixture.send({ id, command, value });
  });
}
function respond(res, args) {
  assert(!res.destroyed, 'Scripted provider response is still connected');
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const delta = args ? { role: 'assistant', tool_calls: [{ index: 0, id: `linux-full-app-${++serial}`, type: 'function', function: { name: toolName, arguments: JSON.stringify(args) } }] }
    : { role: 'assistant', content: 'Linux full-app acceptance completed.' };
  res.write(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`);
  res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: args ? 'tool_calls' : 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } })}\n\ndata: [DONE]\n\n`);
}
function toolText(input) {
  const message = [...(input.messages || [])].reverse().find(item => item.role === 'tool');
  return typeof message?.content === 'string' ? message.content : (message?.content || []).filter(part => part.type === 'text').map(part => part.text).join('\n');
}
function currentObservationImage(input) {
  const current = input.messages.slice(input.messages.findLastIndex(message => message.role === 'tool'));
  return current.flatMap(message => Array.isArray(message.content) ? message.content : [])
    .find(part => part.type === 'image_url' && part.image_url?.url.startsWith('data:image/png;base64,'))?.image_url.url;
}
function readSnapshot(input, text) {
  const match = /"snapshot_id"\s*:\s*"([^"]+)"/.exec(text);
  assert(match, `Actual model-facing observation must expose snapshot_id: ${text}`);
  assert(text.includes(canonical), 'Actual model-facing observation identifies the canonical target');
  assert(/"width"\s*:\s*\d+/.test(text) && /"height"\s*:\s*\d+/.test(text), 'Model receives screenshot dimensions');
  assert(currentObservationImage(input), 'Current tool result supplies its own screenshot image to the actual provider');
  return match[1];
}
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    try {
      const input = JSON.parse(body);
      const tool = input.tools?.find(item => /cua_linux.*computer/.test(item.function?.name || ''));
      if (!input.tools?.length) { respond(res); return; } // Optional title generation.
      assert(tool, `Production Linux computer tool is exposed: ${input.tools.map(item => item.function?.name)}`);
      toolName = tool.function.name;
      const text = toolText(input);
      report.providerCalls.push({ stage, toolName, toolText: text, imageParts: input.messages.flatMap(message => Array.isArray(message.content) ? message.content : []).filter(part => part.type === 'image_url').length });
      switch (stage++) {
        case 0:
          respond(res, { action: 'list_apps', app: `linux:${target.processID}:${target.windowID}` });
          break;
        case 1:
          canonical = new RegExp(`linux:${target.processID}:${target.windowID}:\\d+`).exec(text)?.[0];
          assert(canonical, `Actual model-facing list_apps must expose canonical app IDs: ${text}`);
          respond(res, { action: 'observe', app: canonical, mode: 'vision' });
          break;
        case 2:
          snapshot = readSnapshot(input, text);
          held.set('observed', res);
          break;
        case 3:
          assert(/"delivery"\s*:\s*"delivered"/.test(text), `Native click was delivered: ${text}`);
          held.set('clicked-hidden', res);
          break;
        case 4: {
          const next = readSnapshot(input, text);
          assert.notEqual(next, snapshot, 'Next action uses a fresh model-visible snapshot');
          snapshot = next;
          respond(res, { action: 'click', app: canonical, snapshot_id: snapshot, coordinate_space: 'screenshot', x: 130, y: 180, foreground_policy: 'allow', after: 'vision' });
          break;
        }
        case 5: {
          assert.notEqual(readSnapshot(input, text), snapshot, 'after=vision exposes its fresh snapshot to the actual provider');
          assert(/"settling"\s*:\s*"quiet"/.test(text), `Stopped fixture reaches a quiet post-input observation: ${text}`);
          assert(/"delivery"\s*:\s*"delivered"/.test(text), `Second native click was delivered: ${text}`);
          // Inspect the image delivered to the provider, without asking the
          // target to redraw or polling it before this capture. The second
          // click paints green synchronously; the first click paints purple.
          const observed = nativeImage.createFromDataURL(currentObservationImage(input));
          report.afterVisionPixelBGRA = Array.from(observed.crop({ x: 60, y: 165, width: 1, height: 1 }).toBitmap());
          fs.writeFileSync(path.join(output, 'full-app-after-vision.png'), observed.toPNG());
          assert([102, 204, 34, 255].every((value, index) => Math.abs(report.afterVisionPixelBGRA[index] - value) < 16), `after=vision must contain the just-painted second-click state, not old pixels: ${report.afterVisionPixelBGRA}`);
          held.set('completed-input', res);
          break;
        }
        default: throw new Error(`Unexpected scripted provider stage ${stage}`);
      }
    } catch (error) { failure = error; if (!res.headersSent) res.writeHead(500); res.end(String(error)); }
  });
});
async function viewWith(pip, selector) {
  for (const child of pip.contentView.children) {
    if (child instanceof WebContentsView && !child.webContents.isDestroyed()
      && await evaluate(child.webContents, selector => Boolean(document.querySelector(selector)), selector)) return child.webContents;
  }
}
async function draft(text) {
  await evaluate(main.webContents, value => {
    const textarea = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(textarea, value);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  }, text);
}
async function setup() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const plugins = fs.readdirSync(path.join(desktop, '../internal/plugin/bundled'), { withFileTypes: true }).filter(entry => entry.isDirectory() && entry.name !== 'cua-linux').map(entry => [`plugin:bundled:${entry.name}`, true]);
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', ptc: { enabled: false }, agent: { tool_loading: 'flat', max_steps: 12 },
    extensions: { disabled: Object.fromEntries(plugins) },
    engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture', models: { fixture: { tool_call: true, modalities: { input: ['text', 'image'], output: ['text'] }, limit: { context: 200000, output: 8000 } } } } },
  }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Linux acceptance', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', project], {
    env: process.env, input: `${JSON.stringify({ id: 1, method: 'thread/start', params: { engine: 'wuu', provider: 'fixture', model: 'fixture', permission_mode: 'unconfined' } })}\n`, encoding: 'utf8', timeout: 30000,
  });
  assert.equal(boot.status, 0, boot.stderr);
  const response = boot.stdout.split('\n').filter(Boolean).map(line => JSON.parse(line)).find(item => item.id === 1);
  assert(response?.result?.thread, boot.stdout);
  threadID = response.result.thread.id;
  app.on('browser-window-created', (_event, win) => { main ||= win; });
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
}
async function run() {
  fixture = spawn(process.execPath, [...(app.commandLine.hasSwitch('no-sandbox') ? ['--no-sandbox'] : []), path.join(desktop, 'out/main/linux-cua-e2e.mjs'), '--linux-cua-fixture'], { env: process.env, stdio: ['ignore', 'inherit', 'inherit', 'ipc'] });
  fixture.on('message', message => {
    if (message.ready) target = message;
    else if (message.id) {
      const reply = replies.get(message.id); replies.delete(message.id);
      if (message.error) reply?.reject(new Error(message.error)); else reply?.resolve(message.result);
    }
  });
  await waitFor('disposable native target', () => target);
  await fixtureRequest('animate', true);
  await waitFor('production main window', () => main);
  main.setBounds({ x: 720, y: 40, width: 1380, height: 1000 });
  await waitFor('real application composer', () => evaluate(main.webContents, () => Boolean(document.querySelector('.composer textarea'))));
  await evaluate(main.webContents, async id => { await window.wuu.resumeThread(id); await window.wuu.renameThread(id, 'Linux full-app acceptance'); }, threadID);
  await evaluate(main.webContents, () => { for (const group of document.querySelectorAll('.project-group')) group.querySelector('button[aria-expanded="false"]')?.click(); });
  await waitFor('thread sidebar row', () => evaluate(main.webContents, () => [...document.querySelectorAll('.thread-row')].some(row => row.textContent.includes('Linux full-app acceptance'))));
  await evaluate(main.webContents, () => [...document.querySelectorAll('.thread-row')].find(row => row.textContent.includes('Linux full-app acceptance')).querySelector('.thread-row-main').click());
  await waitFor('selected real thread', () => evaluate(main.webContents, () => document.querySelector('.thread-row[aria-current="page"]')?.textContent.includes('Linux full-app acceptance')));
  await evaluate(main.webContents, () => {
    window.__linuxAcceptanceEvents = [];
    window.wuu.onServerEvent(event => { if (event.kind === 'notification') window.__linuxAcceptanceEvents.push(event.message); });
  });
  await draft(`Use Computer Use to observe and click the disposable Linux target linux:${target.processID}:${target.windowID} twice. This is an isolated acceptance fixture.`);
  await delay(150);
  await evaluate(main.webContents, () => document.querySelector('.composer textarea').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })));
  await waitFor('real Go observe reaches the scripted provider', () => held.has('observed'), 60000);
  checks.push('Actual composer starts a production Go turn; model sees canonical target, snapshot ID, screenshot dimensions and image');
  const activities = await evaluate(main.webContents, id => window.wuu.listActivities(id), threadID);
  const activity = activities.activities.find(item => item.kind === 'cua' && item.target === canonical);
  assert(activity && activity.thread_id === threadID && activity.process_id === target.processID && activity.window_id === target.windowID && activity.controller === 'agent', JSON.stringify(activities));
  report.activity = activity;
  let pip, video, overlay;
  await waitFor('automatic PiP from actual Go activity', async () => {
    for (const win of BrowserWindow.getAllWindows()) {
      if (win === main || !win.isVisible()) continue;
      const candidate = await viewWith(win, 'video');
      if (candidate && await evaluate(candidate, () => { const v = document.querySelector('video'); return v.readyState >= 2 && v.videoWidth > 0 && v.currentTime > 0; })) { pip = win; video = candidate; overlay = await viewWith(win, '#close'); return Boolean(overlay); }
    }
  });
  // The fixture's heading starts at (24,24); sample the same text-free
  // header corner as the isolated capture check, after a real frame arrives.
  await waitFor('automatic PiP contains real target pixels', async () => {
    report.previewPixel = await evaluate(video, () => {
      const v = document.querySelector('video'), c = document.createElement('canvas'); c.width = v.videoWidth; c.height = v.videoHeight;
      const ctx = c.getContext('2d'); ctx.drawImage(v, 0, 0);
      return Array.from(ctx.getImageData(Math.round(c.width * .02), Math.round(c.height * .02), 1, 1).data);
    });
    return [18, 52, 86].every((value, index) => Math.abs(report.previewPixel[index] - value) < 16);
  });
  checks.push('Real Go activity notifications automatically create production PiP with target pixels');
  main.focus();
  await draft('Draft remains editable while Linux preview is live.');
  const histogram = monitorEventLoopDelay({ resolution: 10 }); histogram.enable();
  const samples = [], start = performance.now();
  const firstVideoTime = await evaluate(video, () => document.querySelector('video').currentTime);
  while (performance.now() - start < 10000) {
    const before = performance.now();
    const draftValue = await evaluate(main.webContents, () => document.querySelector('.composer textarea').value);
    const rendererReplyMs = performance.now() - before;
    assert.equal(draftValue, 'Draft remains editable while Linux preview is live.');
    samples.push({ elapsedMs: performance.now() - start, rendererReplyMs, electron: app.getAppMetrics().map(metric => ({ pid: metric.pid, type: metric.type, cpu: metric.cpu, memory: metric.memory })) });
    await delay(250);
  }
  histogram.disable();
  report.responsiveness = { durationMs: performance.now() - start, mainEventLoopP95Ms: histogram.percentile(95) / 1e6, mainEventLoopMaxMs: histogram.max / 1e6, rendererReplyMaxMs: Math.max(...samples.map(sample => sample.rendererReplyMs)), videoElapsedSeconds: await evaluate(video, () => document.querySelector('video').currentTime) - firstVideoTime, samples };
  assert(report.responsiveness.mainEventLoopP95Ms < 100 && report.responsiveness.mainEventLoopMaxMs < 1000 && report.responsiveness.rendererReplyMaxMs < 1000, JSON.stringify(report.responsiveness));
  assert(report.responsiveness.videoElapsedSeconds > 5, 'Live preview advances while the actual app handles draft input');
  fs.writeFileSync(path.join(output, 'full-app-live.png'), (await main.webContents.capturePage()).toPNG());
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'x11grab', '-video_size', '2200x1100', '-i', process.env.DISPLAY, '-frames:v', '1', path.join(output, 'full-app-desktop.png')], { timeout: 15000 });
  checks.push('Actual application remains responsive with live preview and editable composer during a 10-second sampling window');
  // The live sample above is animated; make post-input settling observable
  // without unrelated animation keeping the target intentionally non-quiet.
  await fixtureRequest('animate', false);
  await evaluate(video, () => { window.__linuxTracks = [...document.querySelector('video').srcObject.getTracks()]; });
  await evaluate(overlay, () => document.getElementById('close').click());
  await waitFor('actual PiP closes', () => pip.isDestroyed() || !pip.isVisible());
  if (!video.isDestroyed()) await waitFor('hidden capture tracks end', () => evaluate(video, () => window.__linuxTracks.every(track => track.readyState === 'ended')));
  const release = name => { const res = held.get(name); assert(res, name); held.delete(name); return res; };
  respond(release('observed'), { action: 'click', app: canonical, snapshot_id: snapshot, coordinate_space: 'screenshot', x: 130, y: 180, foreground_policy: 'allow' });
  await waitFor('first actual input while preview hidden', async () => held.has('clicked-hidden') && (await fixtureRequest('state')).clicks === 1);
  assert(pip.isDestroyed() || !pip.isVisible(), 'User-dismissed preview stays hidden after real native input');
  const hiddenActivities = await evaluate(main.webContents, id => window.wuu.listActivities(id), threadID);
  assert(hiddenActivities.activities.some(item => item.id === activity.id && item.controller === 'agent' && item.state !== 'stopped'), JSON.stringify(hiddenActivities));
  respond(release('clicked-hidden'), { action: 'observe', app: canonical, mode: 'vision' });
  await waitFor('second actual native input', async () => held.has('completed-input') && (await fixtureRequest('state')).clicks === 2);
  assert(pip.isDestroyed() || !pip.isVisible(), 'Re-observation does not override user dismissal');
  report.targetState = await fixtureRequest('state');
  checks.push('Closing production PiP releases capture while the same real Go turn continues with two verified native clicks');
  respond(release('completed-input'));
  await waitFor('actual turn completion notification', () => evaluate(main.webContents, id => window.__linuxAcceptanceEvents.some(event => event.method === 'turn/completed' && event.params.thread_id === id), threadID));
  const persisted = await evaluate(main.webContents, id => window.wuu.resumeThread(id), threadID);
  assert(JSON.stringify(persisted).includes('Linux full-app acceptance completed.'), 'Real completed turn is persisted');
  events.push(...await evaluate(main.webContents, () => window.__linuxAcceptanceEvents));
  assert(events.some(event => event.method === 'activity/started' && event.params.id === activity.id), 'PiP was driven by an actual activity notification');
  report.activityEvents = events.filter(event => event.method.startsWith('activity/'));
  report.turnCompletion = events.find(event => event.method === 'turn/completed' && event.params.thread_id === threadID);
  checks.push('after=vision returns fresh snapshot metadata and the painted post-click pixels; actual activity events and turn completion reach the renderer; final answer persists');
  report.status = 'passed';
}
let ending = false;
async function finish(error) {
  if (ending) return; ending = true;
  clearTimeout(timeout);
  if (error) {
    report.status = 'failed'; report.error = error.stack || String(error); console.error(error);
    if (main && !main.isDestroyed()) {
      report.failureContext = await Promise.race([evaluate(main.webContents, () => ({ text: document.body.innerText.slice(0, 12000), events: window.__linuxAcceptanceEvents || [] })).catch(() => null), delay(1000).then(() => null)]);
    }
  }
  fs.writeFileSync(path.join(output, 'full-app.json'), JSON.stringify(report, null, 2));
  for (const res of held.values()) res.destroy();
  server.closeAllConnections(); server.close();
  if (fixture && fixture.exitCode === null) fixture.kill('SIGTERM');
  console.log('FULL_APP_ACCEPTANCE', JSON.stringify({ status: report.status, checks, error: report.error }));
  process.exitCode = error ? 1 : 0;
  // Production quit handlers tear down app-server children; fallback only after
  // evidence is durable and graceful shutdown has had time to complete.
  setTimeout(() => app.exit(error ? 1 : 0), 3000);
  app.quit();
}
const timeout = setTimeout(() => { void finish(new Error('Full application acceptance timed out')); }, 150000);
// Electron awaits this ESM entry's initial evaluation before app.ready. Only
// setup/import is awaited here, so production scheme registration happens before
// readiness; UI/fixture work must run afterward without blocking ready.
try {
  await setup();
  void run().then(() => finish(), error => finish(error));
} catch (error) { void finish(error); }
