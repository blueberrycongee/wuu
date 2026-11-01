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
const report = { status: 'running', order, source: process.env.WUU_E2E_SOURCE_COMMIT, checks: [], ipc: [], events: [], milestones: [], requests: [], screenshots: [], limitations: [`${process.platform} Electron rendering and native keyboard input; not physical-device acceptance.`, 'Chinese text uses native insertText; OS IME candidate selection is not simulated.', 'The real transport is gated deterministically; no paid model or user profile.'] };
let win, creating = false, admitting = false, admitted, heldEvents = [], holdEvents = false, sendEvent, providerResponse, acknowledgementReturned = false, sequence = 0;
const milestone = name => { const entry = { name, sequence: ++sequence }; report.milestones.push(entry); return entry.sequence; };
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
    report.ipc.push({ channel, thread: args[0], prompt: args[1], images: args[2], files: args[3], clientId: args[8] }); admitting = true;
    await admission.promise;
    if (order === 'rejected') throw new Error('First-send fixture: admission rejected');
    holdEvents = true;
    admitted = await handler(event, ...args);
    await acknowledgement.promise;
    acknowledgementReturned = true;
    milestone('acknowledgement-returned');
    return admitted;
  }
  return handler(event, ...args);
});
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created;
  win.webContents.on('console-message', event => { if (event.level === 'error') console.error(`[renderer] ${event.message}`); });
});
function gateMainFrameEvents() {
  // Production broadcasts through the current main frame, not webContents.send.
  // Install after navigation is ready so this is the frame receiving real events.
  const frame = win.webContents.mainFrame;
  sendEvent = frame.send.bind(frame);
  frame.send = (channel, ...args) => {
    assert.equal(win.webContents.mainFrame, frame, 'the gated production frame remains current');
    const method = channel === 'wuu:server-event' && args[0]?.kind === 'notification' ? args[0].message?.method : undefined;
    if (holdEvents && /^(turn\/|item\/|thread\/updated)/.test(method || '')) {
      heldEvents.push([channel, ...args]);
      report.events.push({ method, phase: 'held', sequence: ++sequence });
      return;
    }
    sendEvent(channel, ...args);
  };
  report.eventTransport = 'webContents.mainFrame.send';
}
function releaseEvents() {
  holdEvents = false;
  for (const [channel, ...args] of heldEvents) {
    report.events.push({ method: args[0].message.method, phase: 'released', sequence: ++sequence });
    sendEvent(channel, ...args);
  }
  heldEvents = [];
}
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
const draftRecoveryAction = '[data-main-conversation-composer] [data-wuu-component="draft-recovery"] button';
async function nativeTarget(selector) {
  // Focus and startup layout can move the composer after its first render.
  // Observe a stable, editable hit target before sending the single native click.
  win.show(); win.focus(); win.webContents.focus();
  await until(() => win.isFocused() && win.webContents.isFocused(), 'native window focus');
  await evaluate(() => document.fonts.ready.then(() => undefined));
  const point = await until(() => evaluate(async selector => {
    let previous, target;
    for (let frame = 0; frame < 4; frame++) {
      await new Promise(requestAnimationFrame);
      const element = [...document.querySelectorAll(selector)].find(el => {
        const r = el.getBoundingClientRect();
        return r.width > 0 && r.height > 0 && !el.disabled && !el.readOnly && !el.closest('[inert], [aria-hidden="true"]');
      });
      if (!element) return;
      if (frame === 0) element.scrollIntoView({ block: 'nearest' });
      const r = element.getBoundingClientRect();
      const current = { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
      const hit = document.elementFromPoint(current.x, current.y);
      const bounds = [r.x, r.y, r.width, r.height].join();
      if (!hit || !element.contains(hit) || (previous && (previous !== bounds || target !== element))) return;
      previous = bounds; target = element;
      if (frame === 3) return current;
    }
  }, selector), `stable native hit target: ${selector}`);
  (report.nativeTargets ||= []).push({ selector, ...point });
  const zoom = win.webContents.getZoomFactor();
  return { x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) };
}
async function type(text) {
  const point = await nativeTarget(composer);
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
  await until(() => evaluate(selector => [...document.querySelectorAll(selector)].some(el => document.activeElement === el), composer), 'native composer focus');
  win.webContents.selectAll();
  await until(() => evaluate(selector => [...document.querySelectorAll(selector)].some(el => document.activeElement === el && el.selectionStart === 0 && el.selectionEnd === el.value.length), composer), 'native select all');
  await win.webContents.insertText(text);
  await until(() => evaluate(({ selector, text }) => [...document.querySelectorAll(selector)].some(el => el.value === text), { selector: composer, text }), 'native text inserted');
  // Subsequent input goes directly to webContents; keep real user typing out
  // of the isolated fixture while admission and rendering are observed.
  win.hide();
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
  gateMainFrameEvents();
  win.setBounds({ x: 0, y: 0, width: Number(process.env.WUU_FIRST_SEND_WIDTH || 1280), height: 900 }); win.show(); win.focus(); win.webContents.setBackgroundThrottling(false);
  const prompt = '第一条消息 — retain this first message';
  await type(prompt);
  if (process.env.WUU_FIRST_SEND_ATTACHMENT === '1') {
    await evaluate(selector => {
      const input = document.querySelector(selector), data = new DataTransfer();
      data.items.add(new File(['FIRST_SEND_ATTACHMENT'], 'context.txt', { type: 'text/plain' }));
      const canvas = document.createElement('canvas'); canvas.width = 80; canvas.height = 60;
      const context = canvas.getContext('2d'); context.fillStyle = '#4682b4'; context.fillRect(0, 0, 80, 60);
      const bytes = Uint8Array.from(atob(canvas.toDataURL('image/png').split(',')[1]), char => char.charCodeAt(0));
      data.items.add(new File([bytes], 'image.png', { type: 'image/png' }));
      input.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true }));
    }, composer);
    await until(() => evaluate(() => document.querySelectorAll('[data-main-conversation-composer] .composer-attachment-tray-item').length === 2), 'image and file attachment previews');
  }
  await evaluate(({ prompt, selector }) => {
    const nodes = new WeakMap(), animations = new WeakMap(); let nextNode = 0, nextAnimation = 0;
    const identity = (map, value, next) => { if (!map.has(value)) map.set(value, next()); return map.get(value); };
    window.firstSend = { phase: 'before-enter', frames: [], receivedEvents: [], record: true };
    window.wuu.onServerEvent(event => {
      const method = event.kind === 'notification' ? event.message?.method : undefined;
      if (/^(turn\/|item\/|thread\/updated)/.test(method || '')) window.firstSend.receivedEvents.push({ method, phase: window.firstSend.phase, time: performance.now() });
    });
    const sample = () => {
      if (!window.firstSend.record) return;
      const bubbles = [...document.querySelectorAll('[data-user-message-id]')].filter(row => row.textContent.includes(prompt) && !row.closest('[aria-hidden="true"]')).map(row => {
        const bubble = row.querySelector('[data-message-arrival]') || row;
        const r = bubble.getBoundingClientRect(); let opacity = 1, visible = true;
        for (let parent = bubble; parent; parent = parent.parentElement) { const css = getComputedStyle(parent); opacity *= Number(css.opacity); if (css.display === 'none' || css.visibility === 'hidden') visible = false; }
        return { id: row.dataset.userMessageId, node: identity(nodes, row, () => ++nextNode), top: r.top, bottom: r.bottom, left: r.left, width: r.width, height: r.height, opacity, visible: visible && r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < innerHeight, animations: bubble.getAnimations().map(a => ({ id: identity(animations, a, () => ++nextAnimation), time: a.currentTime, start: a.startTime, duration: a.effect?.getComputedTiming().duration, state: a.playState })) };
      });
      const input = [...document.querySelectorAll(selector)].find(el => el.getBoundingClientRect().height);
      const attachments = input?.closest('.composer-wrap')?.querySelectorAll('.composer-attachment-tray-item:not([data-exiting])').length ?? 0;
      window.firstSend.frames.push({ time: performance.now(), phase: window.firstSend.phase, bubbles, draft: input?.value, attachments });
      requestAnimationFrame(sample);
    }; sample();
  }, { prompt, selector: composer });
  enter(); await until(() => creating, 'real thread creation requested'); await phase('creation-held');
  assert.equal(await evaluate(() => document.querySelectorAll('[data-user-message-id]').length), 0, 'thread creation retains the input without displaying a sent bubble');
  enter(); await frames(3);
  assert.equal(report.ipc.filter(row => row.channel === 'wuu:thread-start').length, 1, 'double Enter creates one thread');
  const newer = process.env.WUU_FIRST_SEND_NEWER !== '0';
  if (newer) await type('Newer draft 内容 remains mine');
  creation.resolve(); await until(() => admitting, 'real turn admission requested'); await phase('identity-bound-admission-held');
  assert.equal(await evaluate(() => document.querySelectorAll('[data-user-message-id]').length), 0, 'binding the thread does not display an unaccepted message');
  enter(); await frames(3);
  assert.equal(report.ipc.filter(row => row.channel === 'wuu:turn-start').length, 1, 'double Enter admits once');
  await evaluate(phase => { window.firstSend.phase = phase; }, order === 'rejected' ? 'rejected-recoverable' : 'admission-requested');
  admission.resolve();
  if (order !== 'rejected') {
    await until(() => admitted, 'real core admitted the turn');
    await until(() => heldEvents.some(([, event]) => /^(turn\/|item\/)/.test(event.message.method)), 'real turn events intercepted at the production main frame');
    const heldCount = heldEvents.length;
    assert.ok(heldCount > 0, 'race ordering must gate actual production events');
    const observedTurn = () => evaluate(id => [...document.querySelectorAll('[data-turn-id]')].some(el => el.dataset.turnId === id && !el.closest('[aria-hidden="true"]')), admitted.turn.id);
    const observedEvent = () => evaluate(() => window.firstSend.receivedEvents.some(event => /^(turn\/|item\/)/.test(event.method)));
    if (order === 'event-first') {
      releaseEvents();
      await until(observedEvent, 'renderer receives the gated event before acknowledgement');
      await until(observedTurn, 'renderer applies the event before acknowledgement');
      assert.equal(acknowledgementReturned, false, 'acknowledgement is still held after the event is rendered');
      milestone('event-rendered-before-acknowledgement');
      await phase('events-before-ack'); acknowledgement.resolve();
      await until(() => acknowledgementReturned, 'acknowledgement returned after rendered event');
    } else {
      acknowledgement.resolve();
      await until(() => acknowledgementReturned, 'real acknowledgement returned with events held');
      await until(observedTurn, 'renderer applies acknowledgement while events remain held');
      assert.equal(await observedEvent(), false, 'no turn event reaches the renderer before acknowledgement is applied');
      assert.equal(report.events.filter(event => event.phase === 'released').length, 0, 'real events remain held through acknowledgement rendering');
      milestone('acknowledgement-rendered-before-events');
      await phase('ack-before-events'); releaseEvents();
      await until(observedEvent, 'renderer receives events after rendered acknowledgement');
      milestone('event-received-after-acknowledgement');
    }
    const ackSequence = report.milestones.find(entry => entry.name === 'acknowledgement-returned').sequence;
    const firstReleaseSequence = report.events.find(event => event.phase === 'released').sequence;
    assert.ok(order === 'event-first' ? firstReleaseSequence < ackSequence : ackSequence < firstReleaseSequence, 'native IPC delivery order matches the requested race');
    report.orderProof = { heldCount, ackSequence, firstReleaseSequence, order };
    report.checks.push('Actual production main-frame events are gated and the requested event/ACK order is observed by the renderer');
    await until(() => providerResponse, 'loopback inference started');
    await phase('admitted'); finishProvider();
    await until(() => evaluate(() => document.body.innerText.includes('The first message was received once.')), 'loopback answer');
    await phase('completed');
    const snapshot = await evaluate(id => window.wuu.resumeThread(id), report.ipc.find(row => row.channel === 'wuu:turn-start').thread);
    assert.equal(snapshot.thread.turns.flatMap(turn => turn.items).filter(item => item.type === 'user_message' && item.text === prompt).length, 1, 'one durable backend user message');
    report.checks.push('Exactly one thread/start, one turn/start, and one durable user message');
  } else {
    if (newer) {
      await until(() => evaluate(() => [...document.querySelectorAll('.turn[data-turn-status="failed"]')].some(turn =>
        turn.getBoundingClientRect().height > 0 && !turn.closest('[inert], [aria-hidden="true"]'))), 'failed message remains visible');
      await nativeTarget(draftRecoveryAction);
    }
    else await until(() => evaluate(() => !document.querySelector('[data-main-conversation-composer] [data-wuu-state="submitting"]')), 'rejection releases the pending input');
    await phase('rejected-recoverable');
    assert.equal(report.requests.filter(row => row.stream).length, 0, 'rejected admission never reaches a model');
  }
  report.receivedEvents = await evaluate(() => window.firstSend.receivedEvents);
  const samples = await evaluate(() => { window.firstSend.record = false; return window.firstSend.frames; });
  fs.writeFileSync(path.join(output, 'frames.json'), JSON.stringify(samples, null, 2));
  assert.equal(report.ipc.filter(row => row.channel === 'wuu:turn-queue').length, 0, 'Enter was never converted into a duplicate queued send');
  if (process.env.WUU_FIRST_SEND_ATTACHMENT === '1') {
    const sent = report.ipc.find(row => row.channel === 'wuu:turn-start');
    assert.equal(sent.files.length, 1, 'file retained through thread binding');
    assert.equal(sent.images.length, 1, 'image retained through thread binding');
  }
  const finalDraft = await evaluate(selector => document.querySelector(selector)?.value, composer);
  assert.equal(finalDraft, newer ? 'Newer draft 内容 remains mine' : order === 'rejected' ? prompt : '', 'admission only releases the draft it owns');
  if (process.env.WUU_FIRST_SEND_ATTACHMENT === '1' && (newer || order === 'rejected')) {
    report.attachmentGeometry = await evaluate(async () => {
      const list = document.querySelector('.composer-attachment-tray-list');
      const card = list?.querySelector('.composer-document-card');
      const title = card?.querySelector('.composer-document-card-title');
      const icon = card?.querySelector('.composer-document-card-icon');
      const remove = card?.querySelector('.composer-attachment-card-remove');
      if (!list || !card || !title || !icon || !remove) throw new Error('Retained attachment controls are missing');
      // The fixture retains both an image and a file. Inspect the document
      // card after revealing it in the horizontal tray, not the last image.
      card.scrollIntoView({ block: 'nearest', inline: 'nearest' });
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const box = node => { const r = node.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width }; };
      const r = remove.getBoundingClientRect();
      return { list: box(list), card: box(card), title: box(title), icon: box(icon), remove: box(remove),
        titleWidth: title.clientWidth, titleContentWidth: title.scrollWidth,
        removeHit: remove.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)) };
    });
    const geometry = report.attachmentGeometry;
    for (const part of ['card', 'title', 'icon', 'remove']) {
      assert.ok(geometry[part].width > 0 && geometry[part].left >= geometry.list.left - 1
        && geometry[part].right <= geometry.list.right + 1, `Retained attachment ${part} fits the visible tray`);
    }
    assert.ok(geometry.titleContentWidth <= geometry.titleWidth + 1, 'Short retained attachment name stays readable');
    assert.ok(geometry.removeHit, 'Retained attachment remove control stays hit-testable');
    report.checks.push('Retained attachment name, icon and remove control fit the visible tray without horizontal clipping');
  }
  if (order === 'rejected' && newer) {
    // Recovery is durable composer state, not a transient root error banner.
    // Use the same stable, hit-testable native target contract as composer input.
    const point = await nativeTarget(draftRecoveryAction);
    for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
    await until(() => evaluate(({ selector, prompt }) => document.querySelector(selector)?.value === prompt, { selector: composer, prompt }), 'native recovery restores the original failed draft');
    assert.equal(report.ipc.filter(row => row.channel === 'wuu:turn-start').length, 1, 'recovering a rejected draft does not silently resubmit it');
    await capture('recovered-original');
    report.checks.push('Rejection preserves newer input and offers native recovery of the original without automatic resubmission');
  }
  const visibleFrom = samples.findIndex(frame => frame.bubbles.some(bubble => bubble.visible && bubble.opacity > 0.05));
  if (order !== 'rejected') assert.ok(visibleFrom >= 0, 'admission displays the message');
  const waiting = samples.filter(frame => ['creation-held', 'identity-bound-admission-held'].includes(frame.phase));
  assert.ok(waiting.length > 0, 'waiting states were sampled');
  assert.ok(waiting.every(frame => !frame.bubbles.some(bubble => bubble.visible)), 'waiting input is never also displayed as a sent message');
  const duplicates = samples.filter(frame => frame.draft === prompt && frame.bubbles.some(bubble => bubble.visible));
  assert.equal(duplicates.length, 0, 'no painted frame displays the submitted text in both input and conversation');
  const continuous = visibleFrom < 0 ? [] : samples.slice(visibleFrom).filter(frame => frame.phase !== 'rejected-recoverable');
  if (!newer && order !== 'rejected' && process.env.WUU_FIRST_SEND_ATTACHMENT === '1') {
    assert.ok(continuous.every(frame => frame.attachments === 0), 'admission releases attachments together with the submitted text');
  }
  const absent = continuous.filter(frame => frame.bubbles.filter(bubble => bubble.visible && bubble.opacity > 0.05).length !== 1);
  report.continuity = { totalFrames: samples.length, retainedFrames: continuous.length, absentFrames: absent.length, missingPhases: [...new Set(absent.map(frame => frame.phase))] };
  save();
  assert.equal(absent.length, 0, 'first bubble remains visible exactly once on every painted frame after admission');
  const opacityDrops = continuous.slice(1).filter((frame, i) => frame.bubbles[0]?.opacity + 0.08 < continuous[i].bubbles[0]?.opacity);
  assert.equal(opacityDrops.length, 0, 'thread binding and acknowledgement never restart the entrance fade');
  const downwardJumps = continuous.slice(1).filter((frame, i) => frame.bubbles[0]?.top > continuous[i].bubbles[0]?.top + 2);
  assert.equal(downwardJumps.length, 0, 'first-message identity handoff never moves its settled bubble back down for another entrance');
  report.checks.push('Waiting input transfers to one admitted bubble without duplicate text, retained attachments, a second entrance, or lost newer draft');
  report.status = 'passed'; save(); console.log(`PASS first-send continuity ${order}: ${output}`);
}
function finish(code) { server.closeAllConnections(); server.close(); app.once('will-quit', () => app.exit(code)); app.quit(); }
setTimeout(() => { report.status = 'failed'; report.error = 'watchdog expired'; save(); finish(1); }, 120000).unref();
run().then(() => finish(0), async error => { report.status = 'failed'; report.error = String(error.stack); if (win && !win.isDestroyed()) { await capture('failure').catch(() => {}); const frames = await evaluate(() => window.firstSend?.frames).catch(() => undefined); if (frames) fs.writeFileSync(path.join(output, 'frames.json'), JSON.stringify(frames, null, 2)); } save(); console.error(error); finish(1); });
