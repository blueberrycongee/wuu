// Real Electron native Enter -> production preload/main -> Go -> local HTTP.
// Run after building desktop/out, with WUU_DESKTOP_CORE identifying the core.
// WUU_FIRST_SEND_ORDER=event-first|ack-first|rejected; one fresh home per run.
// WUU_FIRST_SEND_DESKTOP may identify a frozen pre-fix desktop for RED evidence.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');
const desktop = path.resolve(process.env.WUU_FIRST_SEND_DESKTOP || path.join(__dirname, '..'));
const output = path.resolve(process.env.WUU_FIRST_SEND_OUTPUT || path.join(desktop, 'out/e2e/first-send'));
const order = process.env.WUU_FIRST_SEND_ORDER || 'event-first';
const fast = process.env.WUU_FIRST_SEND_FAST === '1';
assert.ok(['event-first', 'ack-first', 'rejected'].includes(order));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-first-send-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project');
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };
const creation = deferred(), admission = deferred(), acknowledgement = deferred();
const report = { status: 'running', order, source: process.env.WUU_E2E_SOURCE_COMMIT, checks: [], ipc: [], events: [], requests: [], screenshots: [], limitations: ['Linux Electron rendering and native keyboard input; not physical macOS acceptance.', 'Chinese text uses native insertText; OS IME candidate selection is not simulated.', 'The real transport is gated deterministically; no paid model or user profile.'] };
let win, creating = false, admitting = false, admitted, heldEvents = [], holdEvents = false, sendEvent, providerResponse;
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, timeout = 30000) { const end = Date.now() + timeout; while (Date.now() < end) { const result = await check(); if (result) return result; await delay(20); } throw new Error(`Timed out: ${label}`); }
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
async function frames(count = fast ? 1 : 12) { await evaluate(count => new Promise(resolve => { const frame = () => --count > 0 ? requestAnimationFrame(frame) : resolve(); requestAnimationFrame(frame); }), count); }
const register = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => register(channel, async (event, ...args) => {
  if (channel === 'wuu:thread-start') { report.ipc.push({ channel, args }); creating = true; await creation.promise; }
  if (channel === 'wuu:turn-queue') report.ipc.push({ channel, args });
  if (channel === 'wuu:turn-start') {
    report.ipc.push({ channel, thread: args[0], prompt: args[1], files: args[3], clientId: args[8] }); admitting = true;
    await admission.promise;
    if (order === 'rejected') throw new Error('First-send fixture: admission rejected');
    holdEvents = true;
    admitted = await handler(event, ...args);
    await acknowledgement.promise;
    return admitted;
  }
  return handler(event, ...args);
});
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created;
  sendEvent = win.webContents.send.bind(win.webContents);
  win.webContents.send = (channel, ...args) => {
    const notification = channel === 'wuu:server-event' && args[0]?.kind === 'notification';
    if (notification) {
      const method = args[0].message?.method;
      if (holdEvents && /^(turn\/|item\/|thread\/updated)/.test(method || '')) { heldEvents.push([channel, ...args]); report.events.push({ method, phase: 'held' }); return; }
    }
    sendEvent(channel, ...args);
  };
  win.webContents.on('console-message', event => { if (event.level === 'error') console.error(`[renderer] ${event.message}`); });
});
function releaseEvents() { holdEvents = false; for (const [channel, ...args] of heldEvents) { report.events.push({ method: args[0].message.method, phase: 'released' }); sendEvent(channel, ...args); } heldEvents = []; }
const server = http.createServer((req, res) => {
  let raw = ''; req.on('data', chunk => { raw += chunk; }); req.on('end', () => {
    if (req.method === 'GET') return res.end(JSON.stringify({ data: [{ id: 'fixture' }] }));
    const body = JSON.parse(raw); report.requests.push({ path: req.url, stream: body.stream, messages: body.messages });
    if (!body.stream) { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'First send' }, finish_reason: 'stop' }] })); return; }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: { role: 'assistant' }, finish_reason: null }] })}\n\n`);
    providerResponse = res;
  });
});
function finishProvider() { for (const [delta, finish_reason] of [[{ content: 'The first message was received once.' }, null], [{}, 'stop']]) providerResponse.write(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta, finish_reason }] })}\n\n`); providerResponse.end('data: [DONE]\n\n'); }
function enter() { for (const type of ['keyDown', 'char', 'keyUp']) win.webContents.sendInputEvent({ type, keyCode: type === 'char' ? '\r' : 'Return' }); }
const composer = '[data-main-conversation-composer] textarea:not(.composer-response-selection-comment)';
async function type(text) {
  const point = await until(() => evaluate(selector => { const input = [...document.querySelectorAll(selector)].find(el => el.getBoundingClientRect().height && !el.closest('[inert]')); if (!input) return; const r = input.getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) }; }, composer), 'composer hit target');
  win.focus(); win.webContents.focus();
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
  await until(() => evaluate(selector => [...document.querySelectorAll(selector)].some(el => document.activeElement === el), composer), 'native composer focus');
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'a', modifiers: ['control'] });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'a', modifiers: ['control'] });
  await until(() => evaluate(selector => [...document.querySelectorAll(selector)].some(el => document.activeElement === el && el.selectionStart === 0 && el.selectionEnd === el.value.length), composer), 'native select all');
  await win.webContents.insertText(text);
  await until(() => evaluate(({ selector, text }) => [...document.querySelectorAll(selector)].some(el => el.value === text), { selector: composer, text }), 'native text inserted');
}
async function capture(name) { await frames(2); fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG()); report.screenshots.push(`${name}.png`); }
async function phase(name) { await evaluate(name => { window.firstSend.phase = name; }, name); await frames(); if (!fast || name === 'completed') await capture(name); }
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])), providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } } }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'First send fixture', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: process.env.WUU_FIRST_SEND_THEME || 'light', message_flow_font_size: Number(process.env.WUU_FIRST_SEND_FONT || 15) }));
  report.coreSHA256 = createHash('sha256').update(fs.readFileSync(process.env.WUU_DESKTOP_CORE)).digest('hex');
  report.mainSHA256 = createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/main/index.js'))).digest('hex');
  report.rendererSHA256 = Object.fromEntries(fs.readdirSync(path.join(desktop, 'out/renderer/assets')).filter(file => /\.(js|css)$/.test(file)).map(file => [file, createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/renderer/assets', file))).digest('hex')]));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && evaluate(selector => Boolean(document.querySelector(selector)), composer), 'production composer');
  win.setBounds({ x: 0, y: 0, width: Number(process.env.WUU_FIRST_SEND_WIDTH || 1280), height: 900 }); win.show(); win.focus(); win.webContents.setBackgroundThrottling(false);
  const prompt = '第一条消息 — retain this first message';
  await type(prompt);
  if (process.env.WUU_FIRST_SEND_ATTACHMENT === '1') {
    await evaluate(selector => { const input = document.querySelector(selector); const data = new DataTransfer(); data.items.add(new File(['FIRST_SEND_ATTACHMENT'], 'context.txt', { type: 'text/plain' })); input.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true })); }, composer);
    await until(() => evaluate(() => document.body.innerText.includes('context.txt')), 'attachment preview');
  }
  await evaluate(({ prompt, selector }) => {
    const nodes = new WeakMap(), animations = new WeakMap(); let nextNode = 0, nextAnimation = 0;
    const identity = (map, value, next) => { if (!map.has(value)) map.set(value, next()); return map.get(value); };
    window.firstSend = { phase: 'before-enter', frames: [], record: true };
    const sample = () => {
      if (!window.firstSend.record) return;
      const bubbles = [...document.querySelectorAll('[data-user-message-id]')].filter(row => row.textContent.includes(prompt) && !row.closest('[aria-hidden="true"]')).map(row => {
        const bubble = row.querySelector('[data-message-arrival]') || row;
        const r = bubble.getBoundingClientRect(); let opacity = 1, visible = true;
        for (let parent = bubble; parent; parent = parent.parentElement) { const css = getComputedStyle(parent); opacity *= Number(css.opacity); if (css.display === 'none' || css.visibility === 'hidden') visible = false; }
        return { id: row.dataset.userMessageId, node: identity(nodes, row, () => ++nextNode), top: r.top, bottom: r.bottom, left: r.left, width: r.width, height: r.height, opacity, visible: visible && r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < innerHeight, animations: bubble.getAnimations().map(a => ({ id: identity(animations, a, () => ++nextAnimation), time: a.currentTime, start: a.startTime, duration: a.effect?.getComputedTiming().duration, state: a.playState })) };
      });
      window.firstSend.frames.push({ time: performance.now(), phase: window.firstSend.phase, bubbles, draft: [...document.querySelectorAll(selector)].find(el => el.getBoundingClientRect().height)?.value });
      requestAnimationFrame(sample);
    }; sample();
  }, { prompt, selector: composer });
  enter(); await until(() => creating, 'real thread creation requested'); await phase('creation-held');
  enter(); await frames(3);
  assert.equal(report.ipc.filter(row => row.channel === 'wuu:thread-start').length, 1, 'double Enter creates one thread');
  const newer = process.env.WUU_FIRST_SEND_NEWER !== '0';
  if (newer) await type('Newer draft 内容 remains mine');
  creation.resolve(); await until(() => admitting, 'real turn admission requested'); await phase('identity-bound-admission-held');
  enter(); await frames(3);
  assert.equal(report.ipc.filter(row => row.channel === 'wuu:turn-start').length, 1, 'double Enter admits once');
  if (order === 'rejected') await evaluate(() => { window.firstSend.phase = 'rejected-recoverable'; });
  admission.resolve();
  if (order !== 'rejected') {
    await until(() => admitted, 'real core admitted the turn');
    if (order === 'event-first') { releaseEvents(); await phase('events-before-ack'); acknowledgement.resolve(); }
    else { acknowledgement.resolve(); await phase('ack-before-events'); releaseEvents(); }
    await until(() => providerResponse, 'loopback inference started');
    await phase('admitted'); finishProvider();
    await until(() => evaluate(() => document.body.innerText.includes('The first message was received once.')), 'loopback answer');
    await phase('completed');
    const snapshot = await evaluate(id => window.wuu.resumeThread(id), report.ipc.find(row => row.channel === 'wuu:turn-start').thread);
    assert.equal(snapshot.thread.turns.flatMap(turn => turn.items).filter(item => item.type === 'user_message' && item.text === prompt).length, 1, 'one durable backend user message');
    report.checks.push('Exactly one thread/start, one turn/start, and one durable user message');
  } else {
    await until(() => evaluate(() => Boolean(document.querySelector('.turn[data-turn-status="failed"]')) && Boolean(document.querySelector('[role="alert"] .archive-tip-action'))), 'failed message and recovery action are visible');
    await phase('rejected-recoverable');
    assert.equal(report.requests.filter(row => row.stream).length, 0, 'rejected admission never reaches a model');
  }
  const samples = await evaluate(() => { window.firstSend.record = false; return window.firstSend.frames; });
  fs.writeFileSync(path.join(output, 'frames.json'), JSON.stringify(samples, null, 2));
  assert.equal(report.ipc.filter(row => row.channel === 'wuu:turn-queue').length, 0, 'Enter was never converted into a duplicate queued send');
  if (process.env.WUU_FIRST_SEND_ATTACHMENT === '1') assert.equal(report.ipc.find(row => row.channel === 'wuu:turn-start').files.length, 1, 'attachment retained through thread binding');
  const finalDraft = await evaluate(selector => document.querySelector(selector)?.value, composer);
  assert.equal(finalDraft, newer ? 'Newer draft 内容 remains mine' : order === 'rejected' ? prompt : '', 'admission only releases the draft it owns');
  if (order === 'rejected' && newer) {
    const point = await until(() => evaluate(() => {
      const button = document.querySelector('[role="alert"] .archive-tip-action');
      if (!button) return;
      const r = button.getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
    }), 'failed original has a visible recovery action');
    for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
    await until(() => evaluate(({ selector, prompt }) => document.querySelector(selector)?.value === prompt, { selector: composer, prompt }), 'native recovery restores the original failed draft');
    assert.equal(report.ipc.filter(row => row.channel === 'wuu:turn-start').length, 1, 'recovering a rejected draft does not silently resubmit it');
    await capture('recovered-original');
    report.checks.push('Rejection preserves newer input and offers native recovery of the original without automatic resubmission');
  }
  const visibleFrom = samples.findIndex(frame => frame.bubbles.some(bubble => bubble.visible && bubble.opacity > 0.05));
  assert.ok(visibleFrom >= 0, 'Enter displays its message immediately');
  const continuous = samples.slice(visibleFrom).filter(frame => frame.phase !== 'rejected-recoverable');
  const absent = continuous.filter(frame => frame.bubbles.filter(bubble => bubble.visible && bubble.opacity > 0.05).length !== 1);
  report.continuity = { totalFrames: samples.length, retainedFrames: continuous.length, absentFrames: absent.length, missingPhases: [...new Set(absent.map(frame => frame.phase))] };
  save();
  assert.equal(absent.length, 0, 'first bubble remains visible exactly once on every painted frame through creation and admission');
  const opacityDrops = continuous.slice(1).filter((frame, i) => frame.bubbles[0]?.opacity + 0.08 < continuous[i].bubbles[0]?.opacity);
  assert.equal(opacityDrops.length, 0, 'thread binding and acknowledgement never restart the entrance fade');
  const downwardJumps = continuous.slice(1).filter((frame, i) => frame.bubbles[0]?.top > continuous[i].bubbles[0]?.top + 2);
  assert.equal(downwardJumps.length, 0, 'first-message identity handoff never moves its settled bubble back down for another entrance');
  report.checks.push('Every sampled frame retains exactly one bubble, without a second entrance or lost newer draft');
  report.status = 'passed'; save(); console.log(`PASS first-send continuity ${order}: ${output}`);
}
function finish(code) { server.closeAllConnections(); server.close(); app.once('will-quit', () => app.exit(code)); app.quit(); }
setTimeout(() => { report.status = 'failed'; report.error = 'watchdog expired'; save(); finish(1); }, 120000).unref();
run().then(() => finish(0), async error => { report.status = 'failed'; report.error = String(error.stack); if (win && !win.isDestroyed()) { await capture('failure').catch(() => {}); const frames = await evaluate(() => window.firstSend?.frames).catch(() => undefined); if (frames) fs.writeFileSync(path.join(output, 'frames.json'), JSON.stringify(frames, null, 2)); } save(); console.error(error); finish(1); });
