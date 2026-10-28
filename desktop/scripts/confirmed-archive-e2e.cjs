// Native sidebar/notice clicks -> selected application provider -> preload/main
// -> real Go state/execution providers. Disposable data and loopback inference.
// Build first; WUU_ARCHIVE_MODE=running|stale-completed|workspace-switched|provider-replaced.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');
const fixtureBuildEnv = { ...process.env };
const repository = path.resolve(__dirname, '../..');
const desktop = path.resolve(process.env.WUU_ARCHIVE_DESKTOP || path.join(__dirname, '..'));
const mode = process.env.WUU_ARCHIVE_MODE || 'running';
assert.ok(['running', 'stale-completed', 'workspace-switched', 'provider-replaced'].includes(mode));
const output = path.resolve(process.env.WUU_ARCHIVE_OUTPUT || path.join(desktop, 'out/e2e/confirmed-archive', mode));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-confirmed-archive-'));
const home = path.join(fixture, 'home'), userHome = path.join(fixture, 'user-home');
const projects = ['source', 'other'].map(id => ({ id, name: `Archive ${id}`, path: path.join(fixture, id), created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }));
for (const dir of [output, home, userHome, ...projects.map(row => row.path)]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: home, HOME: userHome, CODEX_HOME: path.join(userHome, '.codex'), GROK_HOME: path.join(userHome, '.grok'), WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
const report = { status: 'running', mode, source: process.env.WUU_E2E_SOURCE_COMMIT, checks: [], ipc: [], queueIPC: [], modelRequests: [], screenshots: [], errors: [], limitations: ['Linux Electron native input, not physical macOS acceptance.', 'Synthetic local provider; terminal notifications may be delayed to reproduce stale UI, but archive requests and results are real.'] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
let win, response, threadId, holdTerminal = false, held = [], releaseArchive;
const archiveGate = new Promise(resolve => { releaseArchive = resolve; });
const register = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => register(channel, async (event, ...args) => {
  if (channel === 'wuu:turn-steer') { (report.steerIPC ||= []).push({ args }); save(); return handler(event, ...args); }
  if (channel === 'wuu:turn-start') {
    const entry = { args }; (report.startIPC ||= []).push(entry); save();
    try { const result = await handler(event, ...args); entry.result = result; save(); return result; }
    catch (error) { entry.error = String(error); save(); throw error; }
  }
  if (channel === 'wuu:turn-queue') {
    const entry = { args }; report.queueIPC.push(entry); save();
    try { const result = await handler(event, ...args); entry.result = result; save(); return result; }
    catch (error) { entry.error = String(error); save(); throw error; }
  }
  if (channel !== 'wuu:thread-archive') return handler(event, ...args);
  const entry = { channel, args }; report.ipc.push(entry); save();
  // Hold admission to make repeated native clicks deterministic.
  if (args[2] === true) await archiveGate;
  try { const result = await handler(event, ...args); entry.result = result; save(); return result; }
  catch (error) { entry.error = String(error); save(); throw error; }
});
app.on('browser-window-created', (_event, created) => {
  if (win) return; win = created;
  const observedFrames = new WeakSet();
  const observeFrame = () => {
    const frame = win.webContents.mainFrame;
    if (observedFrames.has(frame)) return; observedFrames.add(frame);
    const send = frame.send.bind(frame);
    frame.send = (channel, ...args) => {
      const notification = channel === 'wuu:server-event' && args[0]?.kind === 'notification';
      const method = notification ? args[0].message?.method : undefined;
      if (method && (report.events ||= []).length < 150) report.events.push(method);
      if (method === 'turn/held') (report.heldNotifications ||= []).push(args[0].message.params);
      if (holdTerminal && (method === 'turn/completed' || method === 'thread/updated' || channel === 'wuu:running-threads-changed')) { (report.delayedTerminalMethods ||= []).push(method || channel); held.push(() => send(channel, ...args)); return; }
      send(channel, ...args);
    };
  };
  observeFrame(); win.webContents.on('did-frame-navigate', observeFrame); win.webContents.on('dom-ready', observeFrame);
  win.webContents.on('console-message', event => { if (event.level === 'error') report.errors.push(event.message); });
});
const server = http.createServer((req, res) => {
  let raw = ''; req.on('data', chunk => { raw += chunk; }); req.on('end', () => {
    if (req.method === 'GET') return res.end(JSON.stringify({ data: [{ id: 'fixture' }] }));
    const body = JSON.parse(raw);
    const title = body.messages?.some(message => message.role === 'system' && typeof message.content === 'string' && message.content.startsWith('You are a title generator.'));
    report.modelRequests.push({ stream: body.stream, title, messages: body.messages });
    if (!body.stream) { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Archive fixture' }, finish_reason: 'stop' }] })); }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    if (title) return res.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: 'assistant', content: 'Archive fixture' }, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
    res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: 'assistant', content: 'Historical answer must survive confirmed archival.' }, finish_reason: null }] })}\n\n`);
    response = res;
  });
});
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
async function until(check, label, timeout = 30000) { const end = Date.now() + timeout; while (Date.now() < end) { const value = await check(); if (value) return value; await delay(30); } throw new Error(`Timed out: ${label}`); }
async function nativeClick(selector, allowDisabled = false) {
  // Hover-only controls must receive native pointer movement before hit testing.
  const hover = await until(() => evaluate(selector => {
    const el = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().height && !node.closest('[inert]'));
    if (!el) return; el.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    const r = el.getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
  }, selector), `visible target ${selector}`);
  win.focus(); win.webContents.focus(); win.webContents.sendInputEvent({ type: 'mouseMove', ...hover });
  const target = await until(() => evaluate(async ({ selector, allowDisabled }) => {
    const el = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().height && !node.closest('[inert]'));
    if (!el || el.disabled && !allowDisabled) return; const before = el.getBoundingClientRect();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const r = el.getBoundingClientRect(); if (Math.abs(r.x - before.x) + Math.abs(r.y - before.y) > 0.5) return;
    const x = Math.round(r.x + r.width / 2), y = Math.round(r.y + r.height / 2);
    if (!el.contains(document.elementFromPoint(x, y))) {
      const failures = (window.archiveHitFailures ||= []);
      if (failures.length < 12) failures.push({ selector, x, y, target: el.outerHTML, hit: document.elementFromPoint(x, y)?.outerHTML.slice(0, 800) });
      return;
    }
    const id = window.archiveClickSequence = (window.archiveClickSequence || 0) + 1;
    el.addEventListener('click', event => { (window.archiveNativeClicks ||= []).push({ id, selector, trusted: event.isTrusted }); }, { once: true });
    return { x, y, id, disabled: Boolean(el.disabled) };
  }, { selector, allowDisabled }), `hit target ${selector}`);
  const point = { x: target.x, y: target.y };
  win.focus(); win.webContents.focus();
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
  if (!target.disabled) await until(() => evaluate(id => window.archiveNativeClicks?.some(click => click.id === id), target.id), `native click dispatched ${selector}`);
}
const queuedText = 'Queued input must remain held after confirmed archive.';
const fixtureHelper = process.env.WUU_SESSION_FIXTURE || path.join(fixture, 'session-fixture');
const composer = '[data-main-conversation-composer] textarea:not(.composer-response-selection-comment)';
async function showSessionSidebar() {
  const toggle = '.conversation-pane [data-wuu-component="sidebar-toggle"][aria-pressed="false"]';
  if (await evaluate(selector => Boolean(document.querySelector(selector)), toggle)) await nativeClick(toggle);
}
async function capture(name) {
  // Observe settled paint rather than capturing the previous compositor frame.
  await evaluate(async () => {
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  await delay(220);
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG()); report.screenshots.push(`${name}.png`); save();
}
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  if (!process.env.WUU_SESSION_FIXTURE) {
    const build = spawnSync('go', ['build', '-o', fixtureHelper, './scripts/session-fixture'], { cwd: repository, env: fixtureBuildEnv, encoding: 'utf8', timeout: 120000 });
    assert.equal(build.status, 0, build.stderr || String(build.error));
  }
  fs.writeFileSync(path.join(home, '.wuu-session-fixture'), 'wuu-session-fixture-v1\n', { mode: 0o600 });
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])), providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } } }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects, active_context: { kind: 'project', project_id: 'source', cwd: projects[0].path } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: process.env.WUU_ARCHIVE_THEME || 'light', message_flow_font_size: Number(process.env.WUU_ARCHIVE_FONT || 15) }));
  const plugin = path.join(fixture, 'archive-fixture'); fs.mkdirSync(plugin);
  fs.writeFileSync(path.join(plugin, 'package.json'), JSON.stringify({ name: 'archive-fixture', private: true, type: 'module', scripts: { build: 'node --check desktop.js' } }));
  fs.writeFileSync(path.join(plugin, 'plugin.json'), JSON.stringify({ schemaVersion: 1, id: 'archive-fixture', name: 'Archive fixture', version: '1.0.0', desktop: { entry: 'desktop.js' } }));
  fs.writeFileSync(path.join(plugin, 'desktop.js'), `export function activate(api) { api.registerUIContribution({ id: 'marker', target: 'wuu.default-navigation/sidebar.footer', render() { return api.react.createElement('span', { hidden: true, 'data-archive-fixture': true }); } }); }`);
  const published = spawnSync(process.env.WUU_DESKTOP_CORE, ['plugin', 'dev', '--watch=false', plugin], { cwd: projects[0].path, env: process.env, encoding: 'utf8', timeout: 30000 });
  fs.writeFileSync(path.join(output, 'plugin-dev.log'), `${published.stdout}\n${published.stderr}`); assert.equal(published.status, 0, 'public fixture plugin publication');
  report.coreSHA256 = createHash('sha256').update(fs.readFileSync(process.env.WUU_DESKTOP_CORE)).digest('hex');
  report.rendererSHA256 = Object.fromEntries(fs.readdirSync(path.join(desktop, 'out/renderer/assets')).filter(file => /\.(js|css)$/.test(file)).map(file => [file, createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/renderer/assets', file))).digest('hex')]));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && evaluate(selector => Boolean(document.querySelector(selector)), composer), 'production composer');
  win.setBounds({ x: 0, y: 0, width: Number(process.env.WUU_ARCHIVE_WIDTH || 1280), height: 900 }); win.show(); win.focus(); win.webContents.setBackgroundThrottling(false);
  await nativeClick(composer); await win.webContents.insertText('Preserve this synthetic archive history.');
  for (const type of ['keyDown', 'char', 'keyUp']) win.webContents.sendInputEvent({ type, keyCode: type === 'char' ? '\r' : 'Return' });
  await until(() => response && evaluate(() => document.body.textContent.includes('Historical answer must survive confirmed archival.')), 'live streamed answer');
  report.targetTitle = await evaluate(() => document.querySelector('.thread-row.active .thread-row-title')?.textContent);
  const listed = await evaluate(() => window.wuu.listAllThreads());
  threadId = listed.threads.find(row => !row.ephemeral && row.status === 'in_progress')?.id; assert.ok(threadId, 'real live synthetic thread');
  if (mode === 'running') {
    await until(() => report.startIPC?.[0]?.result || report.startIPC?.[0]?.error, 'first turn admission acknowledged before queued input');
    assert.equal(report.startIPC[0].error, undefined);
    await nativeClick(composer); await win.webContents.insertText(queuedText);
    for (const type of ['keyDown', 'char', 'keyUp']) win.webContents.sendInputEvent({ type, keyCode: type === 'char' ? '\r' : 'Return', modifiers: [process.platform === 'darwin' ? 'meta' : 'control'] });
    await until(() => report.queueIPC[0]?.result || report.queueIPC[0]?.error, 'native queued input acknowledged');
    assert.equal(report.queueIPC[0].error, undefined, 'native queued input must be durably accepted');
    assert.equal(report.queueIPC[0].args[0], threadId, 'queued input belongs to source');
    assert.equal(report.queueIPC[0].args[1], queuedText);
    assert.equal(report.steerIPC?.length || 0, 0, 'explicit queue shortcut never steers live inference');
  }
  // Hover exposes the real shared sidebar action; no synthetic handler calls.
  await showSessionSidebar();
  const row = await evaluate(() => { const r = document.querySelector('.thread-row.active').getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) }; });
  win.webContents.sendInputEvent({ type: 'mouseMove', ...row });
  await nativeClick('.thread-row.active .thread-row-action.archive');
  await until(() => evaluate(() => Boolean(document.querySelector('[role=alert] .archive-tip-action'))), 'running confirmation notice');
  assert.equal(report.ipc.length, 0, 'unconfirmed running archive never reaches RPC'); await capture('confirmation');
  if (mode === 'provider-replaced') {
    await until(() => evaluate(() => Boolean(document.querySelector('[data-archive-fixture]'))), 'fixture contribution active before replacement');
    const inventory = (await evaluate(() => window.wuu.initialize())).extension_inventory;
    report.graphBefore = [...new Set(inventory.filter(row => row.state === 'active').map(row => row.graph_generation).filter(Boolean))];
    const installed = inventory.find(row => row.provenance.plugin_id === 'archive-fixture'); assert.ok(installed);
    report.replacement = await evaluate(row => window.wuu.updateExtensionPackage({ id: row.id, fingerprint: row.fingerprint, action: 'disable' }), installed);
    await until(() => evaluate(() => !document.querySelector('[data-archive-fixture]')), 'provider graph replacement settled');
    const current = (await evaluate(() => window.wuu.initialize())).extension_inventory;
    report.graphAfter = [...new Set(current.filter(row => row.state === 'active').map(row => row.graph_generation).filter(Boolean))];
    assert.ok(report.graphBefore.length > 0 && report.graphAfter.some(generation => !report.graphBefore.includes(generation)), 'public plugin mutation committed a replacement provider graph');
    assert.equal(report.ipc.length, 0, 'provider replacement never replays retired confirmation');
    await until(() => evaluate(() => !document.querySelector('[role=alert] .archive-tip-action')), 'retired provider confirmation disappears');
    await capture('retired-confirmation');
    await showSessionSidebar();
    await nativeClick('.thread-row .thread-row-action.archive');
    await until(() => evaluate(() => Boolean(document.querySelector('[role=alert] .archive-tip-action'))), 'replacement provider explicit confirmation');
  }
  if (mode === 'stale-completed') {
    holdTerminal = true;
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
    await until(() => report.delayedTerminalMethods?.includes('turn/completed'), 'backend terminal notification held while UI notice remained');
    const completed = await evaluate(id => window.wuu.getThreadPreview(id), threadId);
    report.completedPreview = completed;
    assert.ok(completed.turns.some(turn => turn.items.some(item => item.text?.includes('Historical answer must survive'))), 'stale running UI follows a durable completed reply');
  }
  if (mode === 'workspace-switched') {
    await nativeClick('.project-row-new-thread[aria-label*="Archive other"]');
    // Workspace identity belongs to the titlebar; the responsive tool rail may
    // intentionally omit the old footer workspace label.
    report.destinationDraft = await until(() => evaluate(selector => {
      const title = [...document.querySelectorAll('[data-wuu-component="conversation-titlebar"]')].find(node => node.getBoundingClientRect().height)?.textContent;
      const input = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().height && !node.closest('[inert]'));
      if (document.querySelector('.cached-conversation-pane[data-active="true"], .view-switch-loading-conversation, .view-switch-loading') || !title?.includes('Archive other') || !input || input.disabled || input.readOnly) return;
      return { title, editable: true, activeConversation: false, loading: false };
    }, composer), 'destination draft ready'); save();
    await nativeClick(composer); await win.webContents.insertText('Unrelated destination draft must survive.');
    await until(() => evaluate(selector => [...document.querySelectorAll(selector)].some(node => node.getBoundingClientRect().height && node.value === 'Unrelated destination draft must survive.'), composer), 'new workspace draft');
    // A replaced navigation generation retires its consent instead of replaying
    // it. Confirm the same source again through the replacement's visible row.
    if (!await evaluate(() => Boolean(document.querySelector('[role=alert] .archive-tip-action')))) {
      assert.equal(report.ipc.length, 0, 'workspace replacement does not replay retired consent');
      await showSessionSidebar();
      const source = await evaluate(() => { const row = document.querySelector('.thread-row .thread-row-action.archive')?.closest('.thread-row'); if (!row) return; const r = row.getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) }; });
      assert.ok(source, 'original source remains available to explicitly confirm');
      win.webContents.sendInputEvent({ type: 'mouseMove', ...source });
      await nativeClick('.thread-row .thread-row-action.archive');
      await until(() => evaluate(() => Boolean(document.querySelector('[role=alert] .archive-tip-action'))), 'new scope confirmation');
    }
  }
  report.beforeConfirmation = await evaluate(() => ({ notices: [...document.querySelectorAll('.archive-tip')].map(node => node.outerHTML), nativeClicks: window.archiveNativeClicks })); save();
  await capture('ready-to-confirm');
  await nativeClick('[role=alert] .archive-tip-action');
  await until(() => report.ipc.some(row => row.args[2] === true), 'confirmed force reaches real IPC');
  await nativeClick('[role=alert] .archive-tip-action', true);
  const confirmed = report.ipc.filter(row => row.args[2] === true);
  assert.deepEqual(confirmed.map(row => row.args), [[threadId, true, true]], 'repeated confirmation admits exactly one target and force');
  assert.ok(report.ipc.filter(row => row.args[2] !== true).every(row => row.error && !row.result), 'unconfirmed probes never archive live execution');
  assert.equal(await evaluate(() => document.querySelector('[role=alert] .archive-tip-action')?.disabled), true, 'pending confirmation is disabled');
  await capture('pending'); releaseArchive();
  await until(() => confirmed[0].result || confirmed[0].error, 'durable archive disposition');
  assert.equal(confirmed[0].error, undefined, `confirmed archive must succeed: ${confirmed[0].error}`);
  await until(() => evaluate(() => Boolean(document.querySelector('[role=status] .archive-tip-action'))), 'archive success notice');
  await capture('success-notice');
  holdTerminal = false; for (const send of held.splice(0)) send();
  await until(() => evaluate(() => !document.querySelector('.thread-row-action.archive')), 'the only submitted source stays out of active sidebar');
  if (mode === 'workspace-switched') assert.equal(await evaluate(selector => [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().height)?.value, composer), 'Unrelated destination draft must survive.', 'unrelated destination draft preserved');
  // The success action expires; inspect it before slower durable-storage reads.
  await nativeClick('[role=status] .archive-tip-action');
  await until(() => evaluate(() => Boolean(document.querySelector('.settings-archive-row'))), 'native view-archive action');
  await capture('archive-history');
  const durable = await evaluate(id => window.wuu.getThreadPreview(id), threadId);
  report.durableHistory = durable;
  assert.ok(durable.turns.flatMap(turn => turn.items).some(item => item.text?.includes('Preserve this synthetic archive history.')), 'durable user history retained');
  assert.ok(durable.turns.flatMap(turn => turn.items).some(item => item.text?.includes('Historical answer must survive')), 'durable assistant history retained, not only a cached projection');
  // Preview intentionally omits meta records; use the selected storage contract
  // independently for terminal timing/status rather than a cached resume view.
  const stored = spawnSync(fixtureHelper, [], { input: JSON.stringify({ home, workdir: projects[0].path, op: 'read', ids: [threadId] }), env: process.env, encoding: 'utf8', timeout: 30000 });
  assert.equal(stored.status, 0, stored.stderr || String(stored.error));
  report.selectedStorage = JSON.parse(stored.stdout);
  report.fixtureSHA256 = createHash('sha256').update(fs.readFileSync(fixtureHelper)).digest('hex');
  const selected = report.selectedStorage.sessions[0];
  assert.ok(selected.session.archived_at, 'selected metadata is durably archived');
  const terminal = selected.history.filter(record => record.role === 'meta' && record.content === 'turn_terminal');
  assert.equal(terminal.length, 1, 'one durable terminal record');
  assert.equal(terminal[0].stop_reason, mode === 'stale-completed' ? 'completed' : 'interrupted');
  assert.ok(terminal[0].at, 'durable terminal timestamp');
  await until(() => response.destroyed || response.writableEnded, 'live provider stream is cancelled or already complete');
  assert.equal(report.modelRequests.filter(request => request.stream && !request.title).length, 1, 'confirmation never restarts inference');
  if (mode === 'running') {
    // This separate process opens the selected production state contract; it
    // cannot observe the app-server's in-memory pending queue or thread cache.
    const inspected = spawnSync(fixtureHelper, [], { input: JSON.stringify({ home, workdir: projects[0].path, op: 'inspect', domains: ['held_user_work'] }), env: process.env, encoding: 'utf8', timeout: 30000 });
    assert.equal(inspected.status, 0, inspected.stderr || String(inspected.error));
    report.heldStorage = JSON.parse(inspected.stdout);
    report.fixtureSHA256 = createHash('sha256').update(fs.readFileSync(fixtureHelper)).digest('hex');
    const queuedID = report.queueIPC[0].result.queued.id;
    const row = report.heldStorage.documents.held_user_work.map(document => document.value).find(row => row.SessionID === threadId && row.ID === queuedID);
    assert.ok(row, 'selected durable storage retains queued input');
    assert.equal(row.State, 'pending', 'archive leaves input held, never consumed or automatically dispatched');
    const message = JSON.parse(Buffer.from(row.MessageJSON, 'base64').toString('utf8'));
    assert.equal(message.Content, queuedText, 'exact queued text retained durably');
    assert.equal(row.DeliveryUnconfirmed, false, 'acknowledged queued input remains confirmed');
    assert.ok(report.heldNotifications?.some(notification => notification.thread_id === threadId), 'archive reports held input');
    assert.ok(report.heldNotifications.every(notification => !notification.error), 'successful user cancellation never reports a false held-input error');
    assert.ok(!durable.turns.flatMap(turn => turn.items).some(item => item.text === queuedText), 'held input was not silently appended as an executed turn');
  }
  assert.ok((await evaluate(() => window.wuu.listArchivedThreads())).threads.some(row => row.id === threadId), 'archive history remains discoverable');
  report.checks.push('Native sidebar guard and notice confirmation', 'Exactly one force RPC under repeated native clicks', 'Confirmed selected-provider durable archive with retained user/assistant history', 'Terminal execution state and archive catalog', ...(mode === 'workspace-switched' ? ['Destination draft preserved'] : []));
  report.nativeClicks = await evaluate(() => window.archiveNativeClicks);
  assert.ok(report.nativeClicks.length >= 4 && report.nativeClicks.every(click => click.trusted), 'archive journey uses native trusted input');
  report.status = 'passed'; save(); console.log(`PASS confirmed archive ${mode}: ${output}`);
}
function finish(code) { releaseArchive(); server.closeAllConnections(); server.close(); app.once('will-quit', () => app.exit(code)); app.quit(); }
setTimeout(() => { report.status = 'failed'; report.error = 'watchdog expired'; save(); finish(1); }, 150000).unref();
run().then(() => finish(0), async error => { report.status = 'failed'; report.error = String(error.stack); if (win && !win.isDestroyed()) { report.hitFailures = await evaluate(() => window.archiveHitFailures).catch(() => []); report.nativeClicks = await evaluate(() => window.archiveNativeClicks).catch(() => []); report.finalNotices = await evaluate(() => [...document.querySelectorAll('.archive-tip')].map(node => node.outerHTML)).catch(() => []); await capture('failure').catch(() => {}); } save(); console.error(error); finish(1); });
