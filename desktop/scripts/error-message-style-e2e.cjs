// Product acceptance: native input -> production main/preload -> Go -> loopback
// provider. Run with WUU_ERROR_STYLE_FONT=15 and 20 after a desktop/core build.
// The disposable profile and synthetic diagnostics never use a real account.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');
const desktop = path.resolve(process.env.WUU_ERROR_STYLE_DESKTOP || path.join(__dirname, '..'));
const font = Number(process.env.WUU_ERROR_STYLE_FONT || 15);
const output = path.resolve(process.env.WUU_ERROR_STYLE_OUTPUT || path.join(desktop, `out/e2e/error-message-style-${font}`));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-error-message-style-'));
const home = path.join(fixture, 'home'), userHome = path.join(fixture, 'user-home'), project = path.join(fixture, 'project');
for (const dir of [output, home, userHome, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: home, HOME: userHome, WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
const report = { status: 'running', source: process.env.WUU_E2E_SOURCE_COMMIT, font, checks: [], requests: [], ipc: [], samples: [], screenshots: [], errors: [], limitations: ['Real Linux Electron product with a loopback synthetic provider; not physical macOS acceptance.', 'Appearance modes are applied through the production saved-preference observer; message submission and disclosure/recovery use native input.'] };
let win, providerMode = 'auth', blockedRetry;
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
async function until(check, label, timeout = 30000) { const end = Date.now() + timeout; while (Date.now() < end) { const result = await check(); if (result) return result; await delay(30); } throw new Error(`Timed out: ${label}`); }
const register = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => register(channel, async (event, ...args) => {
  if (/^wuu:(thread-start|turn-start|turn-continue)$/.test(channel)) report.ipc.push({ channel, thread: channel === 'wuu:thread-start' ? undefined : args[0] });
  return handler(event, ...args);
});
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created;
  win.webContents.on('console-message', event => { if (event.level === 'error') report.errors.push(event.message); });
});
const diagnostic = 'Synthetic authentication failure. ' + 'A long diagnostic must wrap without hiding the recovery actions or crossing the message frame. '.repeat(4) + 'reference=' + '0123456789abcdef'.repeat(18);
const server = http.createServer((req, res) => {
  let raw = ''; req.on('data', chunk => { raw += chunk; }); req.on('end', () => {
    if (req.method === 'GET') return res.end(JSON.stringify({ data: [{ id: 'fixture' }] }));
    const body = JSON.parse(raw); report.requests.push({ path: req.url, stream: body.stream, mode: providerMode });
    if (!body.stream) { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Recovery appearance fixture' }, finish_reason: 'stop' }] })); }
    if (providerMode === 'auth') { res.writeHead(401, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ error: { message: diagnostic, type: 'authentication_error' } })); }
    if (providerMode === 'retry-blocked') { blockedRetry = res; return; }
    if (providerMode === 'retry') { providerMode = 'retry-blocked'; res.writeHead(503, { 'Content-Type': 'application/json', 'Retry-After': '2' }); return res.end(JSON.stringify({ error: { message: 'Synthetic service interruption before retry.' } })); }
    reply(res);
  });
});
function reply(res) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  res.write(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: { role: 'assistant', content: 'Recovery completed through the local provider.' }, finish_reason: null }] })}\n\n`);
  res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
}
const composer = '[data-main-conversation-composer] textarea:not(.composer-response-selection-comment)';
function key(keyCode, modifiers = []) { win.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers }); win.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers }); }
async function click(selector) {
  const point = await until(() => evaluate(selector => { const element = [...document.querySelectorAll(selector)].find(el => el.getBoundingClientRect().height && !el.closest('[inert]')); if (!element) return; element.scrollIntoView({ block: 'nearest' }); const r = element.getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) }; }, selector), selector);
  win.focus(); win.webContents.focus();
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...point });
}
async function type(text) { await click(composer); await until(() => evaluate(selector => document.activeElement.matches(selector), composer), 'composer focus'); await win.webContents.insertText(text); await until(() => evaluate(({ selector, text }) => document.querySelector(selector)?.value === text, { selector: composer, text }), 'inserted prompt'); for (const type of ['keyDown', 'char', 'keyUp']) win.webContents.sendInputEvent({ type, keyCode: type === 'char' ? '\r' : 'Return' }); }
async function settle() { await evaluate(async () => { await document.fonts.ready; while (document.getAnimations().some(animation => animation.playState === 'running' && Number.isFinite(animation.effect?.getComputedTiming().endTime))) await new Promise(requestAnimationFrame); let stable = 0, previous; while (stable < 3) { await new Promise(requestAnimationFrame); const el = document.querySelector('.turn-failure'); const r = el?.getBoundingClientRect(); const current = r && [r.x, r.y, r.width, r.height].join(); stable = current === previous ? stable + 1 : 0; previous = current; } }); }
async function capture(name) { await settle(); fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG()); report.screenshots.push(`${name}.png`); }
async function measure() {
  await settle();
  return evaluate(() => {
    const rect = el => { const { x, y, width, height, right, bottom } = el.getBoundingClientRect(); return { x, y, width, height, right, bottom }; };
    const notice = document.querySelector('[data-latest-turn="true"] .turn-failure'), user = notice.closest('.turn').querySelector('.user-message');
    const css = getComputedStyle(notice), userCSS = getComputedStyle(user);
    const message = notice.querySelector('.turn-failure-message');
    return { viewport: { width: innerWidth, height: innerHeight }, pageWidth: document.documentElement.scrollWidth, notice: rect(notice), user: rect(user), message: rect(message), messageOverflow: message.scrollWidth - message.clientWidth, noticeOverflow: notice.scrollWidth - notice.clientWidth, buttons: [...notice.querySelectorAll('button')].map(el => ({ ...rect(el), disabled: el.disabled, text: el.textContent })), fontSize: css.fontSize, userFontSize: userCSS.fontSize, radius: css.borderRadius, userRadius: userCSS.borderRadius, surface: css.backgroundColor, userSurface: userCSS.backgroundColor, role: notice.getAttribute('role'), detailsOpen: notice.querySelector('[aria-expanded]')?.getAttribute('aria-expanded'), diagnostic: notice.querySelector('.turn-failure-diagnostic')?.textContent, focused: document.activeElement?.className, focusVisible: document.activeElement?.matches(':focus-visible') };
  });
}
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])), providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } } }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Error appearance fixture', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'zh-CN', theme: 'light', message_flow_font_size: font }));
  report.coreSHA256 = createHash('sha256').update(fs.readFileSync(process.env.WUU_DESKTOP_CORE)).digest('hex');
  report.mainSHA256 = createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/main/index.js'))).digest('hex');
  report.rendererSHA256 = Object.fromEntries(fs.readdirSync(path.join(desktop, 'out/renderer/assets')).filter(file => /\.(js|css)$/.test(file)).map(file => [file, createHash('sha256').update(fs.readFileSync(path.join(desktop, 'out/renderer/assets', file))).digest('hex')]));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && evaluate(selector => Boolean(document.querySelector(selector)), composer), 'production composer');
  win.setBounds({ x: 0, y: 0, width: 1280, height: 1000 }); win.show(); win.focus(); win.webContents.setBackgroundThrottling(false);
  await type('请检查这条错误消息的气泡外观。Synthetic product acceptance; no live model.');
  await until(() => evaluate(() => Boolean(document.querySelector('.turn-failure[role="alert"]'))), 'real provider failure');
  assert.equal(report.requests.filter(item => item.stream && item.mode === 'auth').length, 1, 'authentication failure is not retried automatically');
  const persisted = await evaluate(id => window.wuu.resumeThread(id), report.ipc.find(item => item.channel === 'wuu:turn-start').thread);
  const expectedDiagnostic = persisted.thread.turns.at(-1).error.message;
  report.expectedDiagnostic = expectedDiagnostic;
  report.limitations.push('The provider sends a long synthetic diagnostic, but the core intentionally exposes its normalized authentication diagnostic. This run verifies that complete product diagnostic, not raw HTTP error-body retention.');
  const sidebar = '[data-wuu-component="sidebar-toggle"][aria-expanded="true"]';
  if (await evaluate(selector => Boolean(document.querySelector(selector)), sidebar)) await click(sidebar);
  win.webContents.sendInputEvent({ type: 'mouseMove', x: 1000, y: 800 });
  for (const width of [1280, 640]) for (const theme of ['light', 'dark']) for (const messageStyle of ['im', 'bubble']) {
    win.setBounds({ x: 0, y: 0, width, height: 1000 });
    await evaluate(async ({ theme, messageStyle }) => { await window.wuu.setThemePreference(theme); const key = 'wuu.appearance.v1'; const preferences = { ...JSON.parse(localStorage.getItem(key) || '{}'), messageStyle }; localStorage.setItem(key, JSON.stringify(preferences)); window.dispatchEvent(new StorageEvent('storage', { key })); }, { theme, messageStyle });
    await until(() => evaluate(({ theme, messageStyle }) => document.documentElement.dataset.theme === theme && document.documentElement.dataset.messageStyle === messageStyle, { theme, messageStyle }), 'applied product preferences');
    const name = `${messageStyle}-${width}-${font}-${theme}`;
    const closed = await measure();
    assert.equal(closed.role, 'alert', 'failure keeps its accessible alert role');
    assert.equal(parseFloat(closed.fontSize), font, 'message respects the saved UI text size');
    assert.equal(closed.fontSize, closed.userFontSize, 'failure text is not shrunk relative to messages');
    assert.ok(closed.pageWidth <= closed.viewport.width && closed.noticeOverflow <= 1 && closed.messageOverflow <= 1, `${name}: no horizontal overflow`);
    assert.ok(closed.notice.x >= 0 && closed.notice.right <= closed.viewport.width, `${name}: frame stays inside viewport`);
    for (const button of closed.buttons) assert.ok(button.x >= closed.notice.x && button.right <= closed.notice.right + 1 && button.y >= closed.notice.y && button.bottom <= closed.notice.bottom + 1, `${name}: recovery targets stay inside frame`);
    await capture(`${name}-closed`);
    await click('.turn-failure-details-toggle');
    await until(() => evaluate(() => document.querySelector('.turn-failure-details-toggle').getAttribute('aria-expanded') === 'true'), 'native disclosure opens');
    key('Tab'); key('Tab', ['shift']);
    const expanded = await measure();
    assert.equal(expanded.diagnostic, expectedDiagnostic, 'details preserve the complete core diagnostic');
    assert.ok(expanded.noticeOverflow <= 1 && expanded.messageOverflow <= 1, `${name}: long diagnostic wraps`);
    assert.ok(expanded.focusVisible, 'keyboard focus remains discoverable');
    await capture(`${name}-details-focused`);
    key('Space');
    await until(() => evaluate(() => document.querySelector('.turn-failure-details-toggle').getAttribute('aria-expanded') === 'false'), 'keyboard disclosure closes');
    report.samples.push({ width, theme, messageStyle, closed, expanded }); save();
  }
  report.checks.push('IM and regular bubble, light/dark, wide/narrow: readable saved font, no clipping, native expandable details and visible keyboard focus');
  providerMode = 'success';
  const before = report.requests.filter(item => item.stream).length;
  await click('.turn-failure-action:not(.is-primary)');
  await until(() => report.requests.filter(item => item.stream).length > before, 'native recovery reached the real core/provider');
  await until(() => evaluate(() => document.body.innerText.includes('Recovery completed through the local provider.')), 'recovery answer');
  await capture('recovered');
  report.checks.push('Native recovery action submits through production preload/main/Go and displays the local provider answer');
  await until(() => evaluate(() => !document.querySelector('[data-latest-turn="true"][data-turn-status="in_progress"]')), 'recovery settled');
  providerMode = 'retry';
  await type('检查自动重试期间的消息外观。Synthetic retry state.');
  await until(() => blockedRetry && evaluate(() => Boolean(document.querySelector('[data-latest-turn="true"] .turn-failure.is-retrying'))), 'real provider retry remains pending');
  for (const [width, theme] of [[1280, 'light'], [640, 'dark']]) {
    win.setBounds({ x: 0, y: 0, width, height: 1000 });
    await evaluate(async theme => { await window.wuu.setThemePreference(theme); const key = 'wuu.appearance.v1'; localStorage.setItem(key, JSON.stringify({ ...JSON.parse(localStorage.getItem(key) || '{}'), messageStyle: 'im' })); window.dispatchEvent(new StorageEvent('storage', { key })); }, theme);
    const retrying = await measure();
    assert.equal(retrying.role, 'status', 'in-progress retry keeps its accessible status role');
    assert.ok(retrying.noticeOverflow <= 1 && retrying.pageWidth <= retrying.viewport.width, 'retry progress fits its message frame');
    report.samples.push({ width, theme, messageStyle: 'im', retrying });
    await capture(`im-${width}-${font}-${theme}-retrying`);
  }
  providerMode = 'success'; reply(blockedRetry);
  await until(() => evaluate(() => !document.querySelector('[data-latest-turn="true"] .turn-failure.is-retrying') && document.querySelector('[data-latest-turn="true"]')?.innerText.includes('Recovery completed through the local provider.')), 'automatic retry completed');
  report.checks.push('Real 503 retry preserves status/countdown and completes without a manual retry');
  report.status = 'passed'; save(); console.log(`PASS error message style ${font}: ${output}`);
}
function finish(code) { server.closeAllConnections(); server.close(); app.once('will-quit', () => app.exit(code)); app.quit(); }
setTimeout(() => { report.status = 'failed'; report.error = 'watchdog expired'; save(); finish(1); }, 180000).unref();
run().then(() => finish(0), async error => { report.status = 'failed'; report.error = String(error.stack); if (win && !win.isDestroyed()) await capture('failure').catch(() => {}); save(); console.error(error); finish(1); });
