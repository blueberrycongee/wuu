// Shipping main, preload, renderer and Go core with disposable state and a
// held loopback model response. No real user sessions or credentials.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_CLOSE_OUTPUT || path.join(desktop, 'out/e2e/workspace-conversation-close'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-conversation-close-'));
const wuuHome = path.join(fixture, 'wuu-home');
const userHome = path.join(fixture, 'user-home');
const workspace = path.join(fixture, 'workspace');
const otherWorkspace = path.join(fixture, 'other-workspace');
for (const dir of [output, wuuHome, userHome, workspace, otherWorkspace]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, {
  WUU_HOME: wuuHome, HOME: userHome, CODEX_HOME: path.join(userHome, '.codex'), GROK_HOME: path.join(userHome, '.grok'),
  WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'),
  WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0',
  WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1',
});
delete process.env.ELECTRON_RENDERER_URL;
for (const name of Object.keys(process.env)) if (/(?:API_KEY|ACCESS_TOKEN|AUTH_TOKEN|SECRET|PASSWORD)$/.test(name)) delete process.env[name];
fs.writeFileSync(path.join(workspace, 'README.md'), '# Synthetic file\n\n' + 'Keep the reading position.\n\n'.repeat(100));

const report = { status: 'running', checks: [], screenshots: [], forbiddenCalls: [], errors: [], navigation: [], modelRequests: 0, threadStarts: 0,
  fixture: 'production Electron/main/preload/Go, isolated profile and loopback model' };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
let win, response, targetThread;
const register = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => register(channel, async (event, ...args) => {
  if (channel === 'wuu:thread-start') report.threadStarts++;
  if (['wuu:thread-archive', 'wuu:thread-delete', 'wuu:turn-interrupt'].includes(channel)) {
    report.forbiddenCalls.push(channel); save(); throw new Error(`Unexpected lifecycle action: ${channel}`);
  }
  if (['wuu:project-select', 'wuu:project-select-none', 'wuu:thread-resume'].includes(channel)) {
    const entry = { channel, args }; report.navigation.push(entry);
    try { const result = await handler(event, ...args); entry.result = channel === 'wuu:thread-resume'
      ? { id: result?.thread?.id, cwd: result?.thread?.cwd, workspaceId: result?.thread?.workspace_id } : result?.active_context; return result; }
    catch (error) { entry.error = String(error); throw error; }
    finally { save(); }
  }
  return handler(event, ...args);
});
app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created;
  win.webContents.on('console-message', event => { if (event.level === 'error') report.errors.push(event.message); });
});
const server = http.createServer((req, res) => {
  let raw = '';
  req.on('data', chunk => { raw += chunk; });
  req.on('end', () => {
    if (req.method === 'GET') return res.end(JSON.stringify({ data: [{ id: 'fixture' }] }));
    const body = JSON.parse(raw);
    report.modelRequests++;
    const title = body.messages?.some(message => message.role === 'system' && typeof message.content === 'string' && message.content.startsWith('You are a title generator.'));
    if (!body.stream) { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Synthetic session' }, finish_reason: 'stop' }] })); }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const text = title ? 'Synthetic session' : 'Open [README.md](README.md) while this task is running.';
    res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: 'assistant', content: text }, finish_reason: null }] })}\n\n`);
    if (title) return res.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
    response = res;
  });
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { const value = await check(); if (value) return value; await delay(30); }
  throw new Error(`Timed out: ${label}`);
}
async function click(selector) {
  const before = await evaluate(() => window.closeFixtureTrustedClicks || 0);
  const hover = await until(() => evaluate(selector => {
    const el = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().height && !node.closest('[inert]'));
    if (!el) return;
    const r = el.getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
  }, selector), `visible target ${selector}`);
  win.webContents.sendInputEvent({ type: 'mouseMove', ...hover });
  const target = await until(() => evaluate(async selector => {
    const el = [...document.querySelectorAll(selector)].find(node => node.getBoundingClientRect().height && !node.closest('[inert]'));
    if (!el || el.disabled) return;
    const panel = el.closest('.workspace-right-panel');
    if (panel?.getAnimations().some(animation => animation.playState === 'running'
      && animation.effect?.getComputedTiming().iterations !== Infinity)) return;
    el.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const r = el.getBoundingClientRect(), x = Math.round(r.x + r.width / 2), y = Math.round(r.y + r.height / 2);
    if (!el.contains(document.elementFromPoint(x, y))) return;
    document.addEventListener('click', event => {
      if (event.isTrusted && event.target instanceof Element && event.target.closest(selector)) {
        window.closeFixtureTrustedClicks = (window.closeFixtureTrustedClicks || 0) + 1;
      }
    }, { capture: true, once: true });
    return { x, y };
  }, selector), `click target ${selector}`);
  win.focus(); win.webContents.focus();
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) win.webContents.sendInputEvent({ type, ...target, button: 'left', clickCount: 1 });
  await until(() => evaluate(previous => (window.closeFixtureTrustedClicks || 0) > previous, before), `trusted click ${selector}`);
}
async function capture(name) {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  await delay(300);
  const png = (await win.webContents.capturePage()).toPNG();
  fs.writeFileSync(path.join(output, `${name}.png`), png);
  report.screenshots.push({ file: `${name}.png`, sha256: createHash('sha256').update(png).digest('hex') }); save();
}
const composer = '[data-main-conversation-composer] textarea';
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(wuuHome, 'config.json'), JSON.stringify({ default_provider: 'fixture',
    engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } } }));
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(wuuHome, 'projects.json'), JSON.stringify({ projects: [
    { id: 'fixture', name: 'Synthetic workspace', path: workspace, created_at: stamp, updated_at: stamp },
    { id: 'other', name: 'Another workspace', path: otherWorkspace, created_at: stamp, updated_at: stamp }],
    active_context: { kind: 'project', project_id: 'fixture', cwd: workspace } }));
  fs.writeFileSync(path.join(wuuHome, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light', message_flow_font_size: 15 }));
  report.coreSha256 = createHash('sha256').update(fs.readFileSync(process.env.WUU_DESKTOP_CORE)).digest('hex');
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && evaluate(selector => {
    const input = document.querySelector(selector);
    return input && !input.disabled && !input.readOnly && !input.closest('[inert]');
  }, composer), 'production composer');
  win.setContentSize(1280, 900); win.show(); win.focus(); win.webContents.setBackgroundThrottling(false);
  await click(composer); await win.webContents.insertText('Start the synthetic running task');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === 'Start the synthetic running task', composer), 'prompt entered');
  await click('[data-main-conversation-composer] .composer-action-button');
  await until(() => response && evaluate(() => Boolean(document.querySelector('.rich-file-link'))), 'running response with file link');
  const listed = await evaluate(() => window.wuu.listAllThreads());
  targetThread = listed.threads.find(thread => thread.status === 'in_progress')?.id;
  report.targetThread = targetThread;
  assert.ok(targetThread, 'a real synthetic session is running');
  await click(composer); await win.webContents.insertText('Retain this unsent draft');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === 'Retain this unsent draft', composer), 'draft entered');
  await click('.rich-file-link');
  await until(() => evaluate(() => document.querySelector('.workspace-markdown-reading')?.textContent.includes('Synthetic file')), 'file preview');
  await click('.workspace-panel-globalize');
  await until(() => evaluate(() => Boolean(document.querySelector('.workspace-conversation-tab .workspace-tool-tab-close'))), 'expanded conversation close');
  await evaluate(() => {
    window.closeFixtureFile = document.querySelector('.workspace-file-resource');
    window.closeFixtureScroller = document.querySelector('.workspace-file-editor-scroll');
    window.closeFixtureScroller.scrollTop = 400;
    window.closeFixtureFrames = [];
    window.closeFixtureObserve = true;
    const sample = () => {
      if (!window.closeFixtureObserve) return;
      const file = document.querySelector('.workspace-file-resource.active');
      window.closeFixtureFrames.push(Boolean(file && file === window.closeFixtureFile && file.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true })));
      requestAnimationFrame(sample);
    };
    requestAnimationFrame(sample);
  });
  const hover = await evaluate(() => {
    const rect = document.querySelector('.workspace-conversation-tab').getBoundingClientRect();
    return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) };
  });
  win.webContents.sendInputEvent({ type: 'mouseMove', ...hover });
  await capture('01-before-close');
  await click('.workspace-conversation-tab .workspace-tool-tab-close');
  await until(() => evaluate(() => !document.querySelector('.workspace-conversation-tab') && document.querySelector('.workspace-file-resource.active')), 'file only');
  const frames = await evaluate(() => { window.closeFixtureObserve = false; return window.closeFixtureFrames; });
  assert.ok(frames.length > 0 && frames.every(Boolean), 'the file remains painted throughout closing');
  report.closeFrames = frames.length;
  assert.equal(await evaluate(() => document.querySelector('.workspace-file-resource') === window.closeFixtureFile), true);
  assert.equal(await evaluate(() => document.querySelector('.workspace-file-editor-scroll') === window.closeFixtureScroller && window.closeFixtureScroller.scrollTop === 400), true);
  assert.equal(await evaluate(() => Boolean(document.querySelector('[data-main-conversation-composer="document"]'))), false);
  assert.equal(response.destroyed, false, 'closing the view does not cancel the running model response');
  await capture('02-file-only');
  await click('.thread-row:not([data-draft-id]) .thread-row-main');
  await until(() => evaluate(selector => document.querySelector('.workspace-conversation-tab [role="tab"]')?.getAttribute('aria-selected') === 'true'
    && document.querySelector(selector)?.value === 'Retain this unsent draft', composer), 'reopen in expanded workspace');
  assert.equal(response.destroyed, false);
  await click('[data-wuu-tab-kind="file"] [role="tab"]');
  await until(() => evaluate(() => Boolean(document.querySelector('.workspace-file-resource.active'))), 'file selected again');
  await click('.workspace-conversation-tab .workspace-tool-tab-close');
  await until(() => evaluate(() => !document.querySelector('.workspace-conversation-tab') && document.querySelector('.workspace-file-resource.active')), 'close reopened conversation');
  await click('.workspace-panel-globalize');
  await until(() => evaluate(() => !document.querySelector('.app-shell').classList.contains('right-panel-globalized')), 'docked');
  assert.equal(await evaluate(selector => document.querySelector(selector)?.value, composer), '');
  await capture('03-new-session-docked');
  await until(() => evaluate(() => Boolean(document.querySelector('.thread-row-main[aria-label^="Start the synthetic"]'))), 'session sidebar');
  await click('.thread-row:not([data-draft-id]) .thread-row-main');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === 'Retain this unsent draft', composer), 'reopened draft');
  assert.equal(response.destroyed, false);
  await capture('04-session-reopened');
  await click('.workspace-panel-globalize');
  await click('.workspace-conversation-tab [role="tab"]');
  await click('.workspace-conversation-tab .workspace-tool-tab-close');
  await until(() => evaluate(() => !document.querySelector('.workspace-conversation-tab') && document.querySelector('.workspace-file-resource.active')), 'close selected conversation');
  await click('.workspace-panel-globalize');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === '', composer), 'new session after second close');
  assert.equal(response.destroyed, false);
  await click('.project-row-new-thread[aria-label*="Another workspace"]');
  await until(() => evaluate(() => document.querySelector('.conversation-title-heading h1')?.textContent.includes('Another workspace')), 'workspace switch');
  await click('.thread-row:not([data-draft-id]) .thread-row-main');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === 'Retain this unsent draft', composer), 'draft after workspace switch');
  assert.equal(await evaluate(() => document.querySelector('.workspace-file-resource') === window.closeFixtureFile), true);
  if (!await evaluate(() => document.querySelector('.app-shell').classList.contains('right-panel-open'))) {
    await click('[data-wuu-component="right-sidebar-toggle"]');
    await until(() => evaluate(() => document.querySelector('.workspace-file-resource.active')), 'reopened file after workspace switch');
  }
  assert.equal(await evaluate(() => window.closeFixtureScroller.scrollTop), 400, 'workspace switch retains the reading position');
  await click('.workspace-panel-globalize');
  await click('.workspace-conversation-tab .workspace-tool-tab-close');
  await until(() => evaluate(() => !document.querySelector('.workspace-conversation-tab')), 'close before reload');
  await click('.workspace-panel-globalize');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === '', composer), 'home before reload');
  report.trustedClicksBeforeReload = await evaluate(() => window.closeFixtureTrustedClicks || 0);
  assert.ok(report.trustedClicksBeforeReload >= 9, 'native input reached the original window controls');
  win.webContents.reload();
  await until(() => evaluate(selector => document.querySelector(selector) && document.querySelector('.workspace-file-resource.active'), composer), 'file and home after reload');
  assert.equal(await evaluate(selector => document.querySelector(selector).value, composer), '');
  await until(() => evaluate(() => document.querySelector('.workspace-file-editor-scroll')?.scrollTop === 400), 'reading position after reload');
  await capture('05-refresh-home-and-file');
  await click('.thread-row:not([data-draft-id]) .thread-row-main');
  await until(() => evaluate(selector => document.querySelector(selector)?.value === 'Retain this unsent draft', composer), 'draft after reload');
  assert.equal(response.destroyed, false);
  assert.equal(report.threadStarts, 1, 'closing and recovery do not create a backend session');
  assert.deepEqual(report.forbiddenCalls, []);
  assert.ok(await evaluate(() => window.closeFixtureTrustedClicks >= 1), 'native input reached the restored window');
  report.checks.push('Active and inactive conversation entries close; file and reading position survive; draft and live response survive workspace switching and reload; docking shows a new session');
  response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
  report.status = 'passed'; save();
}
run().then(() => { server.close(); app.once('will-quit', () => app.exit(0)); app.quit(); setTimeout(() => app.exit(0), 1000).unref(); })
  .catch(async error => { report.status = 'failed'; report.error = String(error.stack || error); if (win && !win.isDestroyed()) {
    try { report.visibleText = (await evaluate(() => document.body.innerText)).slice(0, 4000); await capture('failure'); } catch {}
  } save(); console.error(error); if (response) response.destroy(); server.close(); app.exit(1); });
