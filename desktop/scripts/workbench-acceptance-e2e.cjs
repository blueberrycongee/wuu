// Shipping Electron/main/preload/Go acceptance with a disposable profile.
// Build with npm run build:core and electron-vite build, then run under Xvfb:
//   electron --no-sandbox scripts/workbench-acceptance-e2e.cjs
// The companion workflow records the entire X11 display, including the real
// Electron window and native pointer. PNGs and layout data accompany every
// meaningful click. No CSS substitutes, paid inference or live credentials.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const createSessionFixture = require('./session-fixture.cjs');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app, ipcMain, nativeImage } = require('electron');
const { validateCorpus } = require('./conversation-corpus.cjs');
const { createWebsiteFixture, runWebsiteAcceptance, readRibbonInteraction } = require('./web-workspace-acceptance.cjs');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_WORKBENCH_OUTPUT || path.join(desktop, 'out/e2e/workbench-acceptance'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-workbench-acceptance-'));
const home = path.join(fixture, 'wuu-home');
const project = path.join(fixture, 'acceptance-project');
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.HOME = path.join(fixture, 'user-home');
process.env.USERPROFILE = process.env.HOME;
process.env.CODEX_HOME = path.join(process.env.HOME, '.codex');
process.env.GROK_HOME = path.join(process.env.HOME, '.grok');
process.env.XDG_CONFIG_HOME = path.join(fixture, 'xdg-config');
process.env.XDG_DATA_HOME = path.join(fixture, 'xdg-data');
fs.mkdirSync(process.env.HOME);
for (const name of Object.keys(process.env)) {
  if (/(?:API_KEY|ACCESS_TOKEN|AUTH_TOKEN|SECRET|PASSWORD)$/.test(name)) delete process.env[name];
}
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = path.resolve(process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'));
// Only bundled extensions and the authored local fixture can execute in this isolated home.
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_ENABLE_BROWSER = '1';
app.commandLine.appendSwitch('use-fake-device-for-media-stream');
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;

const source = path.join(__dirname, 'fixtures/conversation-corpus/corpus.json');
const corpusBytes = fs.readFileSync(source);
const corpus = validateCorpus(JSON.parse(corpusBytes));
const sample = corpus.samples[0];
const longSample = [...corpus.samples].sort((a, b) => b.steps.at(-1).text.length - a.steps.at(-1).text.length)[0];
const sessions = [
  { id: 'acceptance-reading', title: 'Layout acceptance: long reading', turns: 16, text: longSample.steps.at(-1).text },
  { id: 'acceptance-second', title: 'Layout acceptance: second conversation', turns: 2, text: sample.steps.at(-1).text },
  { id: 'acceptance-empty', title: 'Layout acceptance: empty conversation', turns: 0, text: '' },
];
const stamp = '2026-01-01T00:00:00Z';
fs.writeFileSync(path.join(project, 'README.md'), '# Acceptance project\n\nSynthetic file preview.\n');
fs.writeFileSync(path.join(project, 'example.ts'), 'export const greeting = "Hello, Wuu";\n');
const skillDir = path.join(project, '.agents/skills/acceptance-skill');
const pluginDir = path.join(home, 'plugins/acceptance-plugin');
fs.mkdirSync(skillDir, { recursive: true }); fs.mkdirSync(pluginDir, { recursive: true });
fs.writeFileSync(path.join(skillDir, 'SKILL.md'), '---\nname: acceptance-skill\ndescription: Synthetic acceptance preview\n---\n# Acceptance preview\n\nThis is a read-only synthetic skill preview.\n');
// A manifest-only package exercises inventory and details without loading code.
fs.writeFileSync(path.join(pluginDir, 'plugin.json'), JSON.stringify({ schema_version: 1, id: 'acceptance-plugin', name: 'Acceptance Plugin', description: 'Synthetic manifest-only acceptance package' }));
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
  default_provider: 'fixture',
  providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic-not-a-credential', model: 'fixture' } },
  engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
}));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({
  projects: [{ id: 'acceptance-project', name: 'Acceptance project', path: project, created_at: stamp, updated_at: stamp }],
  active_context: { kind: 'project', project_id: 'acceptance-project', cwd: project },
}));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({
  onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 15,
}));

const report = {
  status: 'running', startedAt: new Date().toISOString(), timeoutMs: 12 * 60 * 1000,
  commit: spawnSync('git', ['rev-parse', 'HEAD'], { cwd: desktop, encoding: 'utf8' }).stdout.trim(),
  versions: { electron: process.versions.electron, chromium: process.versions.chrome, platform: process.platform },
  fixture: { corpusSha256: createHash('sha256').update(corpusBytes).digest('hex'), noInference: true, isolatedHome: true, safeMode: false, extensionSources: ['bundled', 'authored-local-fixture'] },
  layoutMatrixScope: process.env.WUU_WORKBENCH_MATRIX || 'full',
  checks: [], screenshots: [], actions: [], errors: [],
  exclusions: ['Native macOS traffic lights (Linux capture)', 'Live provider/account authentication',
    'Third-party plugin compatibility and dynamic plugin settings (authored fixture only)', 'Agent browser PiP pixels',
    'Live camera/microphone hardware and allow-grant escalation (permission denial uses fake devices)' ],
};
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2) + '\n');
save();
const rpc = [];
const originalHandle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => originalHandle(channel, async (...args) => {
  // Guard the real bridge rather than replacing its result. An accidental Enter
  // in the composer must fail acceptance before an inference request can start.
  if (['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(channel)) {
    report.errors.push({ kind: 'inference-guard', channel });
    throw new Error(`Acceptance must never invoke inference: ${channel}`);
  }
  const entry = { channel, completed: false, ...(channel === 'wuu:browser-pip-host-layout' ? { payload: args[1] } : {}) };
  const catalogLifecycle = ['wuu:initialize', 'wuu:extension-catalog-refresh', 'wuu:plugin-desktop-activation'].includes(channel);
  if (catalogLifecycle) Object.assign(entry, { sender: args[0].sender.id, startedAtMs: Date.now() - started });
  if (channel === 'wuu:plugin-desktop-activation') {
    const { id, fingerprint, graph_generation, activation, phase, status } = args[1];
    entry.activation = { id, fingerprint, graph_generation, activation, phase, status };
  }
  rpc.push(entry);
  if (channel === 'wuu:web-workspace-bounds' && report.activeTabSwitchTrace) {
    report.activeTabSwitchTrace.boundsIPC.push({ id: args[1], bounds: args[2], atMs: Date.now() - started });
  }
  // Diagnose the public website setting only, never credential settings.
  const websiteSetting = channel === 'wuu:plugin-setting-get' && args[1]?.key === 'website-url'
    ? { id: args[1].id, key: args[1].key, startedAtMs: Date.now() - started } : undefined;
  if (websiteSetting) (report.websiteSettingReads ||= []).push(websiteSetting);
  try {
    const value = await handler(...args); entry.completed = true;
    // Retain only public lifecycle identities, never initialize configuration.
    if (channel === 'wuu:initialize' && value.application_shell) {
      const { plugin_id, generation, provider_id } = value.application_shell;
      entry.applicationShell = { plugin_id, generation, provider_id };
    }
    if (channel === 'wuu:extension-catalog-refresh') {
      entry.inventory = value.extension_inventory.map(({ id, fingerprint, graph_generation }) => ({ id, fingerprint, graph_generation }));
    }
    if (websiteSetting) Object.assign(websiteSetting, { completedAtMs: Date.now() - started, value: value.value });
    return value;
  } catch (error) {
    entry.error = String(error);
    if (websiteSetting) websiteSetting.error = String(error);
    throw error;
  } finally {
    if (catalogLifecycle) entry.completedAtMs = Date.now() - started;
  }
});
let win;
let websiteFixture;
app.on('browser-window-created', (_event, window) => {
  win ||= window;
  window.webContents.on('console-message', event => {
    if (event.level === 'error' && !/Autofill/.test(event.message)) report.errors.push({ kind: 'console', message: event.message, sourceId: event.sourceId, lineNumber: event.lineNumber, url: window.webContents.getURL(), atMs: Date.now() - started });
  });
  window.webContents.on('render-process-gone', (_event, details) => report.errors.push({ kind: 'renderer', ...details }));
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function evaluate(fn, arg) {
  const result = await win.webContents.executeJavaScript(`(async () => {
    try { return { value: await (${fn})(${JSON.stringify(arg)}) }; }
    catch (error) { return { error: String(error.stack || error) }; }
  })()`);
  if (result.error) throw new Error(result.error);
  return result.value;
}
async function waitFor(fn, arg, label = String(fn), timeout = 12000) {
  // DOM elements and React-attached nodes are not structured-cloneable.
  // Predicates cross the Electron bridge only as booleans.
  return waitForValue(() => evaluate(`async argument => Boolean(await (${fn})(argument))`, arg), label, timeout);
}
async function waitForValue(read, label, timeout = 12000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = await read();
    if (value) return value;
    await delay(40);
  }
  throw new Error(`Timed out: ${label}`);
}
async function waitForCatalogRefreshRender(sender, binding, afterIPC) {
  return waitForValue(() => {
    const entries = rpc.slice(afterIPC).filter(entry => entry.sender === sender);
    const refresh = entries.find(entry => entry.channel === 'wuu:extension-catalog-refresh');
    if (refresh?.error) throw new Error(refresh.error);
    if (!refresh?.completed) return;
    const target = refresh.inventory.find(item => item.id === binding.plugin_id);
    assert.ok(target?.fingerprint && target.graph_generation, 'Refresh publishes the selected shell identity');
    const reports = entries.filter(entry => entry.channel === 'wuu:plugin-desktop-activation'
      && entry.activation.id === target.id && entry.activation.fingerprint === target.fingerprint
      && entry.activation.graph_generation === target.graph_generation);
    const failure = reports.find(entry => entry.error || entry.activation.status === 'failed');
    assert.ok(!failure, `Selected shell activation failed: ${JSON.stringify(failure)}`);
    const render = reports.find(entry => entry.completed && entry.activation.phase === 'render' && entry.activation.status === 'active');
    if (!render) return;
    return { target, refreshStartedAtMs: refresh.startedAtMs, refreshCompletedAtMs: refresh.completedAtMs,
      render: { ...render.activation, sender: render.sender, acknowledgedAtMs: render.completedAtMs } };
  }, 'This window acknowledges rendering the refreshed shell graph');
}
async function settle() {
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const animations = document.getAnimations().filter(a => a.playState === 'running'
      && Number.isFinite(a.effect.getComputedTiming().endTime)
      && !(a.effect.target instanceof Element && a.effect.target.closest('[inert]')));
    await Promise.race([Promise.all(animations.map(a => a.finished.catch(() => {}))), new Promise(resolve => setTimeout(resolve, 1000))]);
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
}
function key(keyCode, modifiers = []) {
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
}
function moveNativePointer(x, y) {
  const bounds = win.getContentBounds();
  const zoom = win.webContents.getZoomFactor();
  const moved = spawnSync('xdotool', ['mousemove', '--sync',
    String(Math.round(bounds.x + x * zoom)), String(Math.round(bounds.y + y * zoom))], { encoding: 'utf8' });
  assert.equal(moved.status, 0, moved.stderr || String(moved.error));
}
const activePane = '.cached-conversation-pane[data-active="true"]';
const composer = '.conversation-pane .composer textarea';
const scroller = '.conversation-pane > .scroll-region';
const conversations = '[data-wuu-component="conversations-section"]';
const settings = 'button[role="menuitem"][data-settings-page="providers"]';
const plugins = '.sidebar-ribbon button[aria-label="Extensions"]';
const leftToggle = '.section-sidebar-toggle';
const rightToggle = '.shell-right-sidebar-toggle';
function pick(selector, text) { return { selector, text }; }
async function point(target, scroll = true) {
  // Accessible names can include counts while the visible control label stays stable.
  const query = typeof target === 'string' ? { selector: target } : target;
  for (const view of [...win.contentView.children].reverse()) {
    if (!view.webContents || !view.getVisible() || !view.webContents.getURL().startsWith('wuu-chrome://ui/chrome-overlay.html')) continue;
    const p = await view.webContents.executeJavaScript(`(({selector,text}) => {
      const node = [...document.querySelectorAll(selector)].find(n => (text === undefined || (n.getAttribute('aria-label') || '').trim() === text || (n.textContent || '').trim() === text) && n.getBoundingClientRect().width > 0);
      if (!node) return null;
      const r = node.getBoundingClientRect(), x = r.x + r.width / 2, y = r.y + r.height / 2;
      if (!node.contains(document.elementFromPoint(x,y)) || node.disabled) throw new Error('Native chrome target is occluded or disabled');
      return {x,y};
    })(${JSON.stringify(query)})`);
    if (p) { const b = view.getBounds(), z = view.webContents.getZoomFactor(), shellZoom = win.webContents.getZoomFactor(); return { x: (b.x + p.x*z)/shellZoom, y: (b.y+p.y*z)/shellZoom, nativeChrome: true }; }
  }
  return evaluate(async ({ selector, text, scroll }) => {
    const nodes = [...document.querySelectorAll(selector)];
    const node = nodes.find(n => (text === undefined || (n.getAttribute('aria-label') || '').trim() === text || (n.textContent || '').trim() === text)
      && n.getBoundingClientRect().width > 0 && !n.closest('[inert]'));
    if (!node) throw new Error(`Missing interactive target: ${selector} / ${text}`);
    if (scroll) node.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' });
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const r = node.getBoundingClientRect();
    let x = Math.round(r.x + r.width / 2), y = Math.round(r.y + r.height / 2);
    // Native-backed rails retain expanded measurement bounds while only their
    // collapsed icon lane is painted. Click a visible child, never the clipped
    // label midpoint or a renderer-only event underneath native chrome.
    if (!node.contains(document.elementFromPoint(x, y))
      && node.closest('.sidebar-ribbon[data-native-supported="true"]')) {
      for (const child of node.children) {
        const b = child.getBoundingClientRect();
        const cx = Math.round(b.x + b.width / 2), cy = Math.round(b.y + b.height / 2);
        if (b.width > 0 && b.height > 0 && child.contains(document.elementFromPoint(cx, cy))) {
          x = cx; y = cy; break;
        }
      }
    }
    const hit = document.elementFromPoint(x, y);
    if (r.width < 1 || r.height < 1 || x < 0 || x >= innerWidth || y < 0 || y >= innerHeight || !node.contains(hit)) {
      throw new Error(`Occluded target ${selector}: ${JSON.stringify({ rect: r.toJSON(), hit: hit?.outerHTML.slice(0, 260) })}`);
    }
    if (node.disabled) throw new Error(`Disabled target: ${selector}`);
    return { x, y, rect: r.toJSON(), name: node.getAttribute('aria-label') || node.textContent?.trim() };
  }, { ...(typeof target === 'string' ? { selector: target } : target), scroll });
}
async function capture(label, afterPixels) {
  await settle();
  const name = `${String(report.screenshots.length + 1).padStart(3, '0')}-${label.replace(/[^a-zA-Z0-9_-]+/g, '-').slice(0, 90)}.png`;
  const captureStartedAtMs = Date.now() - started;
  const screenshot = await win.webContents.capturePage();
  const png = screenshot.toPNG();
  fs.writeFileSync(path.join(output, name), png);
  report.screenshots.push({ file: name, sha256: createHash('sha256').update(png).digest('hex'), captureStartedAtMs, atMs: Date.now() - started, image: screenshot.getSize(), window: win.getBounds(),
    viewport: await evaluate(() => ({ width: innerWidth, height: innerHeight, dpr: devicePixelRatio, theme: document.documentElement.dataset.theme,
      uiSize: getComputedStyle(document.documentElement).getPropertyValue('--conversation-message-font-size').trim(),
      activeElement: document.activeElement?.outerHTML.slice(0, 180) })),
    ribbonInteraction: await readRibbonInteraction(win) });
  save();
  if (afterPixels) await afterPixels(report.screenshots.at(-1));
  // A short viewing hold makes the real-time recording readable; success is
  // always decided by observable state, never by this pacing delay.
  await delay(160);
}
async function click(target, label, evidence = true) {
  if (evidence) { await point(target); await capture(`before-${label}`); }
  // Capturing can finish a transition; never click its earlier midpoint.
  const p = await point(target);
  const inRail = p.nativeChrome || await evaluate(p => Boolean(document.elementFromPoint(p.x, p.y)?.closest('.sidebar-ribbon')), p);
  // Route through the OS so a native chrome view cannot be bypassed by a
  // renderer-only event sent to the placeholder underneath it.
  const zoom = win.webContents.getZoomFactor();
  const bounds = win.getContentBounds();
  const input = spawnSync('xdotool', ['mousemove', String(Math.round(bounds.x + p.x * zoom)), String(Math.round(bounds.y + p.y * zoom)), 'click', '1'], { encoding: 'utf8', timeout: 5000 });
  assert.equal(input.status, 0, input.stderr || String(input.error));
  report.actions.push({ label, input: 'native-x11', atMs: Date.now() - started, point: p });
  await settle();
  if (evidence) await capture(`after-${label}`);
  if (inRail) {
    // End ordinary pointer navigation before measuring contextual controls
    // beneath the intentional hover overlay. Explicit hover/focus journeys
    // still drive and assert expansion themselves; no Escape or forced blur.
    const moved = spawnSync('xdotool', ['mousemove', String(Math.round(bounds.x + Math.min(300 * zoom, bounds.width - 20))), String(Math.round(bounds.y + 90 * zoom))], { encoding: 'utf8', timeout: 5000 });
    assert.equal(moved.status, 0, moved.stderr || String(moved.error));
    report.actions.push({ label: `${label}-leave-rail`, atMs: Date.now() - started });
    await settle();
    const account = win.contentView.children.find(view => view.webContents?.getURL() === 'wuu-chrome://ui/chrome-overlay.html?surface=account');
    const menuRetains = account?.getVisible() || await evaluate(() => [...document.querySelectorAll('.sidebar-account-menu[role=menu]')].some(node => node.getBoundingClientRect().width > 0 && getComputedStyle(node).visibility !== 'hidden'));
    if (!menuRetains) {
      await waitFor(() => document.querySelector('.sidebar-ribbon')?.dataset.expanded !== 'true', undefined, 'Ordinary pointer navigation leaves the hover rail', 4000);
      const deadline = Date.now() + 4000;
      while (win.contentView.children.some(view => view.webContents?.getURL() === 'wuu-chrome://ui/chrome-overlay.html?surface=ribbon' && view.getVisible())) {
        if (Date.now() >= deadline) throw new Error('Native hover rail remains visible after pointer leave');
        await delay(40);
      }
      await settle();
    }
  }
}
async function fill(target, text) {
  await click(target, 'focus-input', false);
  const selector = typeof target === 'string' ? target : target.selector;
  await waitFor(selector => document.activeElement?.matches(selector), selector);
  const current = await evaluate(selector => document.querySelector(selector).value, selector);
  if (current === text) return;
  if (current) {
    key('A', [process.platform === 'darwin' ? 'meta' : 'control']);
    key('Backspace');
    // Native key delivery and insertText are separate IPC paths. Acknowledge
    // deletion before inserting, otherwise "15" can become "1515" under load.
    await waitFor(selector => document.querySelector(selector).value === '', selector);
  }
  if (text) await win.webContents.insertText(text);
  await waitFor(({ selector, text }) => [...document.querySelectorAll(selector)].some(n => n === document.activeElement && n.value === text), { selector, text });
}
async function test(name, fn) {
  const start = Date.now();
  report.activeCheck = name;
  try {
    const detail = await fn();
    report.checks.push({ name, status: 'passed', durationMs: Date.now() - start, detail });
    console.log(`PASS ${name}`);
  } catch (error) {
    const failure = { name, status: 'failed', durationMs: Date.now() - start, error: String(error.stack || error) };
    report.checks.push(failure);
    console.error(`FAIL ${name}: ${error.stack || error}`);
    try { await capture(`failure-${name}`); } catch (captureError) { report.errors.push({ kind: 'capture', message: String(captureError) }); }
    // Preserve the original failure and its pixels before using real dismissal
    // controls, so one failed modal does not contaminate independent journeys.
    try {
      // A failed native drag can leave the window manager's move grab active.
      // Renderer key events cannot cancel that grab. Release the real pointer
      // and send native Escape with event-loop gaps before resetting geometry.
      const before = win.getBounds();
      for (const args of [['mouseup', '1'], ['key', '--clearmodifiers', 'Escape'], ['mouseup', '1']]) {
        const result = spawnSync('xdotool', args, { encoding: 'utf8', timeout: 3000 });
        assert.equal(result.status, 0, result.stderr || String(result.error));
        await delay(120);
      }
      if (win.isFullScreen()) win.setFullScreen(false);
      if (win.isMaximized()) win.unmaximize();
      win.show(); win.focus();
      await delay(120);
      win.setBounds({ x: 30, y: 30, width: 1280, height: 900 });
      await delay(200);
      const after = win.getBounds();
      assert.ok(after.x >= 0 && after.y >= 0 && after.x + after.width <= 1600
        && after.y + after.height <= 1000, 'Failed-journey window returns inside the recorded display');
      failure.recoveryGeometry = { before, after };
      for (let attempt = 0; attempt < 3; attempt++) {
        key('Escape');
        await settle();
        if (await evaluate(() => ![...document.querySelectorAll('[role="dialog"][aria-modal="true"]')]
          .some(node => node.getBoundingClientRect().width > 0))) break;
        await delay(250);
      }
      await waitFor(() => ![...document.querySelectorAll('[role="dialog"][aria-modal="true"]')]
        .some(node => node.getBoundingClientRect().width > 0), undefined, 'failed-journey modal dismissed', 3000);
      await goConversations();
      await dockSidebar();
      failure.recovery = 'Released native input, restored visible bounds and returned to Conversations';
    } catch (recoveryError) {
      failure.recovery = String(recoveryError.stack || recoveryError);
    }
  }
  delete report.activeCheck;
  save();
}
async function goConversations() {
  key('Escape');
  await click(conversations, 'conversations');
  await waitFor(() => !document.querySelector('.settings-workbench-layer') && !document.querySelector('.catalog-section-pane:not([hidden])'));
}
async function goSettings(pageLabel) {
  await click('.sidebar-account-trigger', 'account-menu');
  await click(settings, 'settings');
  await waitFor(() => document.querySelector('.settings-embedded'));
  if (pageLabel) await click(pick('.settings-nav-item', pageLabel), `settings-${pageLabel}`);
}
async function dockSidebar() {
  if (await evaluate(() => document.querySelector('.app-shell').classList.contains('sidebar-collapsed'))) {
    await click(leftToggle, 'dock-left-sidebar');
  }
  await waitFor(() => !document.querySelector('.app-shell').classList.contains('sidebar-collapsed'));
}
async function selectThread(id) {
  await goConversations();
  await dockSidebar();
  const title = sessions.find(s => s.id === id).title;
  const exists = await evaluate(title => [...document.querySelectorAll('.thread-row-main')].some(n => (n.getAttribute('aria-label') || n.textContent).includes(title)), title);
  if (!exists) {
    const collapsed = await evaluate(() => document.querySelector('.project-row[aria-expanded="false"]') !== null);
    if (collapsed) await click('.project-row[aria-expanded="false"]', 'expand-project');
  }
  const target = await evaluate(title => [...document.querySelectorAll('.thread-row-main')].find(n => (n.getAttribute('aria-label') || n.textContent).includes(title))?.getAttribute('aria-label'), title);
  assert.ok(target, `Thread ${id} appears in the real sidebar`);
  await click(pick('.thread-row-main', target), `open-${id}`);
  await waitFor(({ id, title }) => id === 'acceptance-empty'
    ? !!document.querySelector('.empty-scroll-region') && [...document.querySelectorAll('.thread-row.active')].some(n => n.textContent.includes(title))
    : document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId === id, { id, title });
}
async function geometry(label) {
  const g = await evaluate(() => {
    const rect = selector => document.querySelector(selector)?.getBoundingClientRect().toJSON() ?? null;
    const shell = document.querySelector('.app-shell');
    const surface = getComputedStyle(shell, '::before');
    const ribbon = rect('.sidebar-ribbon');
    const settings = !!document.querySelector('.settings-embedded');
    const context = rect(settings ? '.settings-sidebar' : '.sidebar > .sidebar-content');
    const main = rect(settings ? '.settings-main' : '.conversation-pane');
    const chrome = rect('.workbench-window-chrome');
    const conversation = !settings && document.querySelector('.conversation-pane')?.dataset.nativeSection === 'conversations';
    const rightControlPresent = Boolean(document.querySelector('.shell-right-sidebar-toggle'));
    const controls = ['.section-sidebar-toggle', ...(conversation ? ['.shell-right-sidebar-toggle'] : [])].map(selector => {
      const node = document.querySelector(selector), r = node.getBoundingClientRect();
      const x = r.x + r.width / 2, y = r.y + r.height / 2;
      return { selector, rect: r.toJSON(), visible: getComputedStyle(node).visibility === 'visible', hit: node.contains(document.elementFromPoint(x, y)) };
    });
    return { viewport: { width: innerWidth, height: innerHeight }, overflow: document.documentElement.scrollWidth - innerWidth,
      settings, conversation, rightControlPresent, collapsed: shell.classList.contains('sidebar-collapsed'), ribbon, context, main, chrome, controls,
      surface: { content: surface.content, radius: surface.borderTopLeftRadius, top: parseFloat(surface.top), left: parseFloat(surface.left),
        right: parseFloat(surface.right), bottom: parseFloat(surface.bottom) } };
  });
  assert.ok(g.ribbon && g.context && g.main && g.chrome, `${label}: all workbench regions render`);
  assert.ok(g.overflow <= 1, `${label}: page overflows horizontally by ${g.overflow}px`);
  assert.ok(g.surface.content !== 'none' && parseFloat(g.surface.radius) > 0, `${label}: shared rounded workbench surface exists`);
  assert.ok(Math.abs(g.surface.left - g.ribbon.right) <= 2, `${label}: shared surface begins after the ribbon`);
  assert.ok(Math.abs(g.surface.top - g.chrome.bottom) <= 2, `${label}: shared surface begins below native window chrome`);
  assert.ok(Math.abs(g.context.y - g.main.y) <= 2, `${label}: contextual sidebar and main content share their top edge`);
  assert.ok(g.main.right <= g.viewport.width + 1 && g.main.bottom <= g.viewport.height + 1, `${label}: main panel fits the window`);
  assert.equal(g.rightControlPresent, g.conversation, `${label}: workspace controls belong to conversations`);
  assert.ok(g.controls.every(c => c.visible && c.hit), `${label}: native caption/chrome or an overlay blocks a sidebar toggle`);
  return g;
}
async function appearance(theme, size, width) {
  win.setContentSize(width, 900);
  await goSettings('Appearance');
  await click(`[data-testid="settings-theme-${theme}"]`, `theme-${theme}`, false);
  await fill('[data-testid="settings-message-flow-font-size-input"]', String(size));
  key('Enter');
  await waitFor(({ theme, size }) => document.documentElement.dataset.theme === theme
    && parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--conversation-message-font-size')) === size, { theme, size });
  await settle();
}
const started = Date.now();
const watchdog = setTimeout(() => {
  report.status = 'timed-out';
  report.finishedAt = new Date().toISOString(); report.durationMs = Date.now() - started;
  report.timeout = { budgetMs: report.timeoutMs, check: report.activeCheck || null };
  console.error(`WORKBENCH timed out after ${report.durationMs}ms during ${report.timeout.check || 'setup or teardown'}`);
  save(); app.exit(1);
}, report.timeoutMs);
async function run() {
  for (const file of [process.env.WUU_DESKTOP_CORE, path.join(desktop, 'out/main/index.js'), path.join(desktop, 'out/preload/index.cjs'), path.join(desktop, 'out/renderer/index.html')]) {
    assert.ok(fs.existsSync(file), `Build required: ${file}`);
  }
  report.buildSha256 = Object.fromEntries(['out/main/index.js', 'out/preload/index.cjs', 'out/renderer/index.html'].map(file =>
    [file, createHash('sha256').update(fs.readFileSync(path.join(desktop, file))).digest('hex')]));
  const sessionFixture = createSessionFixture(home, project);
  const seededSessions = sessions.map(s => ({
    session: { id: s.id, created_at: stamp, updated_at: '2026-01-02T00:00:00Z', title: s.title,
      cwd: project, workspace_id: 'acceptance-project', provider: 'fixture', model: 'fixture' },
    history: Array.from({ length: s.turns }, (_, turn) => [
      { role: 'user', content: `Synthetic acceptance question ${turn + 1}`, at: stamp },
      { role: 'assistant', content: s.text, at: stamp, phase: '' },
    ]).flat(),
  }));
  const seed = sessionFixture({ op: 'seed', sessions: seededSessions });
  assert.equal(seed.status, 0, seed.stderr || String(seed.error));
  websiteFixture = await createWebsiteFixture(home);
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  const until = Date.now() + 30000;
  while (!win && Date.now() < until) await delay(40);
  assert.ok(win, 'Production main created its window');
  win.setPosition(30, 30); win.setContentSize(1280, 900); win.show(); win.focus();
  win.webContents.setBackgroundThrottling(false);
  // Do not attach CDP or execute JavaScript in Electron's provisional blank
  // document: its sandbox startup data is not initialized during navigation.
  const rendererURL = pathToFileURL(path.join(desktop, 'out/renderer/index.html')).href;
  const loadDeadline = Date.now() + 30000;
  while ((win.webContents.getURL() !== rendererURL || win.webContents.isLoadingMainFrame()) && Date.now() < loadDeadline) await delay(40);
  assert.equal(win.webContents.getURL(), rendererURL, 'Shipping renderer loaded');
  assert.ok(!win.webContents.isLoadingMainFrame(), 'Initial renderer navigation finished');
  win.webContents.debugger.attach('1.3');
  await win.webContents.debugger.sendCommand('Runtime.enable');
  win.webContents.debugger.on('message', (_event, method, params) => {
    if (method === 'Runtime.exceptionThrown') report.errors.push({ kind: 'exception', detail: params.exceptionDetails });
  });
  await waitFor(() => window.wuu && document.querySelector('.composer textarea') && document.querySelector('.sidebar-ribbon'), undefined, 'shipping app ready', 30000);
  await capture('initial-shipping-window');

  await test('shared workbench and contextual navigation', async () => {
    await selectThread('acceptance-reading');
    win.webContents.sendInputEvent({ type: 'mouseMove', x: 800, y: 500 });
    await settle();
    const conversationsLabel = await evaluate(() => {
      const label = [...document.querySelectorAll('.project-row-name')].find(node => node.textContent.trim() === 'Conversations');
      return label && { text: label.textContent, width: label.clientWidth, contentWidth: label.scrollWidth };
    });
    assert.ok(conversationsLabel && conversationsLabel.width > 0 && conversationsLabel.contentWidth <= conversationsLabel.width + 1,
      `Built-in Conversations label fits at default size: ${JSON.stringify(conversationsLabel)}`);
    const reading = await geometry('conversations');
    await click(plugins, 'plugins');
    await waitFor(() => document.querySelector('.skills-catalog'));
    // Enabled extension shortcuts are a separate nested group, not primary catalog choices.
    assert.equal(await evaluate(() => document.querySelectorAll('.sidebar-section-navigation > .sidebar-section-item').length), 2);
    await click(pick('.sidebar-section-item', 'Skills'), 'skills-context');
    await waitFor(() => document.querySelector('.catalog-tab[data-tab="skills"]')?.getAttribute('aria-selected') === 'true');
    await click(pick('.sidebar-section-item', 'Plugins'), 'plugins-context');
    await waitFor(() => document.querySelector('.catalog-tab[data-tab="plugins"]')?.getAttribute('aria-selected') === 'true');
    const catalog = await geometry('catalog');
    await goSettings('Appearance');
    const settings = await geometry('settings');
    await goConversations();
    assert.equal(await evaluate(() => document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId), 'acceptance-reading');
    return { reading, catalog, settings, conversationsLabel };
  });

  await test('production PiP host IPC suspends beneath Settings and catalog', async () => {
    await selectThread('acceptance-reading');
    const expectHost = async (available) => {
      const deadline = Date.now() + 12000;
      while (Date.now() < deadline) {
        const entry = rpc.filter(r => r.channel === 'wuu:browser-pip-host-layout' && r.completed).at(-1);
        if (entry && Boolean(entry.payload?.host) === available) return entry.payload;
        await delay(40);
      }
      throw new Error(`Production PiP host IPC did not become ${available ? 'available' : 'hidden'}`);
    };
    const before = await expectHost(true);
    await goSettings('Appearance');
    const inSettings = await expectHost(false);
    await goConversations();
    const restored = await expectHost(true);
    await click(plugins, 'pip-host-catalog');
    const inCatalog = await expectHost(false);
    await goConversations();
    const after = await expectHost(true);
    return { before, inSettings, restored, inCatalog, after, nativePiPWindow: 'not exercised' };
  });

  await test('plugin details, skill preview and catalog empty search', async () => {
    await click(plugins, 'catalog-detail-entry'); await dockSidebar();
    await click(pick('.sidebar-section-item', 'Plugins'), 'plugins-list');
    await waitFor(() => document.querySelector('.catalog-row-open[aria-label*="Acceptance Plugin"]'));
    await click('.catalog-row-open[aria-label*="Acceptance Plugin"]', 'open-synthetic-plugin-details');
    await waitFor(() => document.querySelector('[data-wuu-component="plugin-detail"]'));
    await capture('manifest-only-plugin-details');
    await click('[data-testid="plugin-page-back"]', 'plugin-details-back');
    await click(pick('.sidebar-section-item', 'Skills'), 'skills-preview-list');
    await fill('.catalog-toolbar input', 'acceptance-skill');
    await waitFor(() => document.querySelector('.catalog-row[aria-label="Preview acceptance-skill"]'));
    await click('.catalog-row[aria-label="Preview acceptance-skill"]', 'skill-preview-open');
    await waitFor(() => document.querySelector('.skill-preview-body')?.textContent.includes('read-only synthetic skill preview'));
    await capture('skill-preview-loaded-from-core'); key('Escape');
    await waitFor(() => !document.querySelector('.skill-preview-dialog'));
    await fill('.catalog-toolbar input', 'no-such-skill-acceptance');
    await waitFor(() => document.querySelector('.skills-catalog .settings-group-empty'));
    await capture('catalog-empty-search');
    await fill('.catalog-toolbar input', '');
    await evaluate(() => {
      const evidence = { timeOrigin: performance.timeOrigin, startedAt: performance.now(), frames: 0, mutations: 0, dropped: 0, samples: [] };
      const identities = new WeakMap();
      let sequence = 0, frame, previous;
      const inspect = selector => {
        const node = document.querySelector(selector);
        if (!node) return null;
        if (!identities.has(node)) identities.set(node, ++sequence);
        const style = getComputedStyle(node), rect = node.getBoundingClientRect();
        const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
        return { node: identities.get(node), rect: rect.toJSON(), display: style.display, visibility: style.visibility,
          opacity: style.opacity, transform: style.transform, hidden: node.hidden, inert: node.inert,
          hitWithin: Boolean(hit && node.contains(hit)), text: node.textContent.trim().slice(0, 100) };
      };
      const sample = (source, force = false) => {
        const state = { shell: inspect('.app-shell'), main: inspect('.conversation-pane'),
          title: inspect('.conversation-pane > .titlebar'), catalog: inspect('.catalog-section-pane:not([hidden])'),
          page: inspect('.catalog-section-pane:not([hidden]) .settings-page'),
          heading: inspect('.catalog-section-pane:not([hidden]) .settings-page-title'), toolbar: inspect('.catalog-toolbar'),
          shellClass: document.querySelector('.app-shell')?.className ?? null,
          busy: document.querySelector('.catalog-refresh')?.getAttribute('aria-busy') ?? null,
          recovery: document.querySelector('.application-recovery')?.textContent ?? null,
          animations: document.getAnimations().flatMap(animation => {
            const target = animation.effect?.target, timing = animation.effect?.getComputedTiming();
            if (!(target instanceof Element) || !target.closest('.conversation-pane') || !Number.isFinite(timing?.endTime)) return [];
            return [{ target: target.className, name: animation.animationName ?? animation.transitionProperty ?? animation.id,
              playState: animation.playState, currentTime: animation.currentTime, endTime: timing.endTime, progress: timing.progress }];
          }).slice(0, 12) };
        const key = JSON.stringify(state);
        if (force || key !== previous) {
          if (evidence.samples.length < 600) evidence.samples.push({ at: performance.now(), source, ...state });
          else evidence.dropped++;
          previous = key;
        }
      };
      const observer = new MutationObserver(records => { evidence.mutations += records.length; sample('mutation'); });
      observer.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
      const tick = () => { evidence.frames++; sample('frame'); frame = requestAnimationFrame(tick); };
      sample('before-refresh', true); frame = requestAnimationFrame(tick);
      const originalShell = inspect('.app-shell').node;
      const catalogSnapshot = () => {
        const catalog = document.querySelector('.catalog-section-pane:not([hidden])');
        return { at: performance.now(), shell: inspect('.app-shell')?.node ?? null, originalShell,
          catalogVisible: Boolean(catalog && !catalog.inert),
          skillsSelected: catalog?.querySelector('.catalog-tab[data-tab="skills"]')?.getAttribute('aria-selected') === 'true',
          busy: catalog?.querySelector('.catalog-refresh')?.getAttribute('aria-busy') ?? null,
          row: Boolean(catalog?.querySelector('.catalog-row[aria-label="Preview acceptance-skill"]')),
          error: catalog?.querySelector('.skills-catalog-error')?.textContent ?? null };
      };
      window.__wuuSkillsRefreshPresentation = { sample, catalogSnapshot, stop: () => {
        sample('before-Escape', true); observer.disconnect(); cancelAnimationFrame(frame);
        return { ...evidence, endedAt: performance.now() };
      } };
    });
    const presentation = { pairs: [], reportEpochMs: started, clock: 'Capture intervals use report atMs; DOM samples use timeOrigin + performance.now().',
      boundary: 'Sequential renderer and X11 captures are not atomic. A changed DOM/animation state or a gap between captures prevents a capture-only verdict. Layout sampling can affect rendering timing.' };
    try {
      const sender = win.webContents.id;
      const binding = rpc.findLast(entry => entry.channel === 'wuu:initialize' && entry.sender === sender && entry.completed)?.applicationShell;
      assert.ok(binding, 'This window initialized a selected application shell');
      const afterIPC = rpc.length;
      presentation.refresh = { sender, binding, afterIPC };
      await click('.catalog-refresh', 'catalog-refresh');
      Object.assign(presentation.refresh, await waitForCatalogRefreshRender(sender, binding, afterIPC));
      const loadedCatalog = await waitForValue(async () => {
        const snapshot = await evaluate(() => window.__wuuSkillsRefreshPresentation.catalogSnapshot());
        presentation.refresh.latestCatalog = snapshot;
        assert.ok(snapshot.shell && snapshot.shell !== snapshot.originalShell, 'Refresh renders a new shell activation');
        assert.ok(snapshot.catalogVisible && snapshot.skillsSelected, 'Refreshing the catalog preserves the selected Skills page');
        assert.ok(!snapshot.error, snapshot.error);
        return snapshot.busy === 'false' ? snapshot : undefined;
      }, 'The acknowledged shell finishes loading its Skills catalog');
      presentation.refresh.loadedCatalog = loadedCatalog;
      assert.ok(loadedCatalog.row, 'The refreshed Skills page retains acceptance-skill');
      await capture('skills-after-catalog-refresh', async renderer => {
        const pair = { renderer: { file: renderer.file, sha256: renderer.sha256, startedAtMs: renderer.captureStartedAtMs, endedAtMs: renderer.atMs } };
        presentation.pairs.push(pair);
        await evaluate(() => window.__wuuSkillsRefreshPresentation.sample('before-native-capture', true));
        const nativeLayers = (parent = win.contentView, visible = true, prefix = 'root') => parent.children.flatMap((view, index) => [{
          path: `${prefix}/${index}`, id: view.webContents?.id ?? null, visible: visible && view.getVisible(),
          ownVisible: view.getVisible(), bounds: view.getBounds(),
        }, ...nativeLayers(view, visible && view.getVisible(), `${prefix}/${index}`)]);
        const bounds = win.getBounds();
        pair.native = { file: 'skills-refresh-native-after-renderer.png', source: 'x11-whole-native-window', window: bounds,
          layersBefore: nativeLayers(), startedAtMs: Date.now() - started };
        // Capture before the existing viewing hold and before goConversations sends Escape.
        const taken = spawnSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'x11grab', '-video_size', `${bounds.width}x${bounds.height}`, '-i', `${process.env.DISPLAY}+${bounds.x},${bounds.y}`, '-frames:v', '1', '-f', 'image2pipe', '-vcodec', 'png', 'pipe:1'], { timeout: 12000, maxBuffer: 24 * 1024 * 1024 });
        Object.assign(pair.native, { endedAtMs: Date.now() - started, status: taken.status,
          error: taken.status === 0 ? undefined : String(taken.stderr || taken.error), layersAfter: nativeLayers() });
        await evaluate(() => window.__wuuSkillsRefreshPresentation.sample('after-native-capture', true));
        assert.equal(taken.status, 0, pair.native.error);
        const image = nativeImage.createFromBuffer(taken.stdout);
        assert.ok(!image.isEmpty(), 'Skills refresh native-window PNG is readable');
        fs.writeFileSync(path.join(output, pair.native.file), taken.stdout);
        Object.assign(pair.native, { sha256: createHash('sha256').update(taken.stdout).digest('hex'), image: image.getSize() });
      });
    } finally {
      presentation.dom = await evaluate(() => {
        const result = window.__wuuSkillsRefreshPresentation.stop();
        delete window.__wuuSkillsRefreshPresentation; return result;
      }).catch(error => ({ unavailable: String(error) }));
      report.skillsRefreshPresentation = presentation;
      fs.writeFileSync(path.join(output, 'skills-refresh-presentation.json'), JSON.stringify(presentation, null, 2));
      save();
    }
    assert.equal(presentation.dom.unavailable, undefined, 'Skills refresh presentation evidence is available');
    assert.ok(presentation.dom.frames > 0, 'Skills refresh presentation spans renderer frames');
    await goConversations();
  });

  await test('draft and reading position survive all global sections', async () => {
    await selectThread('acceptance-reading');
    await fill(composer, 'Unsent acceptance draft: keep this exactly.');
    const scrollPoint = await point(scroller, false);
    for (let i = 0; i < 6; i++) {
      win.webContents.sendInputEvent({ type: 'mouseWheel', x: scrollPoint.x, y: scrollPoint.y, deltaX: 0, deltaY: 300, canScroll: true });
      await delay(60);
    }
    await settle();
    const before = await evaluate(selector => { const s = document.querySelector(selector); return { top: s.scrollTop, max: s.scrollHeight - s.clientHeight }; }, scroller);
    assert.ok(before.max > 500 && before.top < before.max - 200, 'Native wheel input leaves the reading tail');
    await capture('reading-anchor-before-navigation');
    await goSettings('General');
    await click(plugins, 'draft-to-plugins');
    await goConversations();
    const back = '[data-wuu-component="page-history-back"]';
    const forward = '[data-wuu-component="page-history-forward"]';
    await click(back, 'history-back-to-extensions');
    await waitFor(() => !!document.querySelector('.catalog-section-pane:not([hidden]) .skills-catalog')
      && !document.querySelector('.app-shell').hasAttribute('data-settings-open'));
    await click(back, 'history-back-to-settings');
    await waitFor(() => document.querySelector('.app-shell').hasAttribute('data-settings-open')
      && document.querySelector('[data-wuu-component="settings-page"]')?.dataset.wuuPage === 'general');
    await click(forward, 'history-forward-to-extensions');
    await waitFor(() => !!document.querySelector('.catalog-section-pane:not([hidden]) .skills-catalog')
      && !document.querySelector('.app-shell').hasAttribute('data-settings-open'));
    await click(forward, 'history-forward-to-conversation');
    await waitFor(() => !document.querySelector('.app-shell').hasAttribute('data-settings-open')
      && !document.querySelector('.catalog-section-pane:not([hidden])')
      && document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId === 'acceptance-reading');
    const after = await evaluate(({ selector, input }) => ({ top: document.querySelector(selector).scrollTop, draft: document.querySelector(input).value }), { selector: scroller, input: composer });
    assert.equal(after.draft, 'Unsent acceptance draft: keep this exactly.');
    assert.ok(Math.abs(after.top - before.top) <= 8, `Reading position moved ${before.top} -> ${after.top}`);
    await selectThread('acceptance-second');
    assert.equal(await evaluate(selector => document.querySelector(selector).value, composer), '');
    await selectThread('acceptance-reading');
    assert.equal(await evaluate(selector => document.querySelector(selector).value, composer), after.draft);
    return { before, after, history: ['conversation', 'extensions', 'settings:general', 'extensions', 'conversation'] };
  });

  await test('search opens by mouse, selects by Enter, and restores focus', async () => {
    await goConversations(); await dockSidebar();
    await click('.conversation-search-trigger', 'open-search-mouse');
    await waitFor(() => document.querySelector('.conversation-search-dialog input') === document.activeElement);
    await fill('.conversation-search-dialog input', 'second conversation');
    await waitFor(() => document.querySelector('.conversation-search-results')?.getAttribute('aria-busy') === 'false'
      && document.querySelector('.conversation-search-result[aria-selected="true"] .conversation-search-result-title')?.textContent.includes('second conversation'));
    await capture('search-before-enter'); key('Enter');
    await waitFor(() => !document.querySelector('.conversation-search-dialog')
      && document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId === 'acceptance-second');
    await capture('search-after-enter');
    await click(composer, 'focus-composer', false); key('P', ['control']);
    await waitFor(() => document.querySelector('.conversation-search-dialog input') === document.activeElement);
    key('Escape');
    await waitFor(selector => !document.querySelector('.conversation-search-dialog') && document.activeElement === document.querySelector(selector), composer);
    key('P', ['control']); await waitFor(() => document.querySelector('.conversation-search-dialog input') === document.activeElement);
    await fill('.conversation-search-dialog input', 'long reading');
    await waitFor(() => document.querySelector('.conversation-search-results')?.getAttribute('aria-busy') === 'false'
      && document.querySelector('.conversation-search-result[aria-selected="true"] .conversation-search-result-title')?.textContent.includes('long reading'));
    await click('.conversation-search-result', 'search-mouse-result');
    await waitFor(() => document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId === 'acceptance-reading'
      && !document.querySelector('.conversation-search-dialog'));
  });

  await test('settings search, no-results, Escape, and all native pages', async () => {
    await goSettings(); await dockSidebar();
    const labels = await evaluate(() => [...document.querySelectorAll('.settings-nav-item')].map(n => n.textContent.trim()));
    assert.ok(labels.length >= 6, 'Capability-aware settings navigation is populated');
    const pages = [];
    for (const label of labels) {
      await click(pick('.settings-nav-item', label), `settings-page-${label}`);
      pages.push(await evaluate(() => document.querySelector('[data-wuu-component="settings-page"]').dataset.wuuPage));
      assert.ok(await evaluate(() => !!document.querySelector('.settings-titlebar-slot')?.textContent.trim()), 'Settings titlebar has the active page title');
    }
    await fill('.settings-search input', 'Appearance');
    await waitFor(() => document.querySelectorAll('.settings-nav-item').length === 1);
    await click(pick('.settings-nav-item', 'Appearance'), 'filtered-appearance');
    await fill('.settings-search input', 'no-such-setting-acceptance');
    await waitFor(() => document.querySelector('.settings-nav-empty'));
    await capture('settings-search-empty'); key('Escape');
    await waitFor(() => document.querySelector('.settings-search input').value === '' && !document.querySelector('.settings-nav-empty'));
    return { labels, pages };
  });

  await test('runtime edit commits when leaving through ribbon', async () => {
    await goSettings('Built-in agent'); await dockSidebar();
    const field = '[data-testid="settings-advanced"] input';
    await fill(field, '73');
    await click(conversations, 'runtime-blur-save-via-ribbon');
    await waitFor(() => !document.querySelector('.settings-workbench-layer'));
    await goSettings('Built-in agent');
    await waitFor(selector => document.querySelector(selector)?.value === '73', field, 'runtime setting persisted after ribbon navigation');
    assert.ok(rpc.some(r => r.channel === 'wuu:config-advanced-update' && r.completed), 'Real core settings update completed');
    await capture('runtime-persisted');
  });

  await test('provider connect validation and inline cancellation', async () => {
    const rpcStart = rpc.length;
    await goSettings(); await dockSidebar();
    await click('[data-wuu-component="settings-navigation-item"][data-wuu-page="providers"]', 'settings-connections');
    await click('[data-testid="settings-connection-add"]', 'provider-add-connection');
    await click('[data-testid="settings-provider-custom"]', 'provider-open-custom');
    await waitFor(() => document.querySelector('.model-connect-dialog'));
    assert.ok(await evaluate(() => document.querySelector('[data-testid="settings-provider-connect"]').disabled), 'Empty provider cannot be saved');
    await fill('[data-testid="settings-provider-connect-base-url"]', 'http://127.0.0.1:1/v1');
    assert.ok(await evaluate(() => document.querySelector('[data-testid="settings-provider-connect"]').disabled), 'Missing key/model cannot be saved');
    await capture('provider-incomplete-form');
    await click('.model-connect-dialog [data-testid="settings-connection-back"]', 'provider-cancel');
    await waitFor(() => !document.querySelector('.model-connect-dialog'));
    await click('[data-testid="settings-provider-custom"]', 'provider-reopen');
    await waitFor(() => document.querySelector('.model-connect-dialog'));
    await click('.model-connect-dialog [data-testid="settings-connection-back"]', 'provider-cancel-reopened');
    await waitFor(() => !document.querySelector('.model-connect-dialog'));
    assert.ok(!rpc.slice(rpcStart).some(r => r.channel === 'wuu:config-model-update' || /connection.*update|provider.*connect/.test(r.channel)), 'Cancelled provider never saves configuration or contacts a provider');
  });

  await test('collapsed contextual sidebars hover, dock, focus and keyboard resize', async () => {
    await goConversations(); await dockSidebar();
    const runs = [];
    for (const section of ['conversations', 'settings']) {
      if (section === 'settings') await goSettings('Appearance');
      const before = await point(leftToggle, false);
      await click(leftToggle, `collapse-${section}`);
      moveNativePointer(800, 500);
      await waitFor(() => document.querySelector('.app-shell').classList.contains('sidebar-collapsed')
        && !document.querySelector('.app-shell').classList.contains('sidebar-drawer-open'));
      const collapsed = await point(leftToggle, false);
      assert.equal(collapsed.x, before.x); assert.equal(collapsed.y, before.y);
      assert.ok(await point(conversations, false), 'Ribbon remains interactive when context is collapsed');
      moveNativePointer(collapsed.x, collapsed.y);
      await waitFor(selector => document.querySelector(selector).matches(':hover'), leftToggle);
      // Observe beyond the drawer's 240ms reveal delay: the persistent toggle
      // may retain an existing preview, but must never activate one itself.
      const toggleStayedClosed = await evaluate(async () => {
        const shell = document.querySelector('.app-shell');
        const deadline = performance.now() + 400;
        do {
          if (!shell.classList.contains('sidebar-collapsed') || shell.classList.contains('sidebar-drawer-open')) return false;
          await new Promise(requestAnimationFrame);
        } while (performance.now() < deadline);
        return true;
      });
      assert.ok(toggleStayedClosed, 'Hovering the persistent toggle keeps the secondary pane collapsed');
      await capture(`${section}-collapsed-toggle-hover`);
      const edge = await point('.app-shell > .sidebar-hover-zone', false);
      moveNativePointer(edge.x, edge.y);
      await waitFor(() => document.querySelector('.app-shell').classList.contains('sidebar-drawer-open'));
      await capture(`${section}-hover-drawer`);
      const hovered = await point(leftToggle, false);
      assert.equal(hovered.x, before.x); assert.equal(hovered.y, before.y);
      const focusTarget = section === 'settings' ? '.settings-search input' : '.conversation-search-trigger';
      // Start at an adjacent drawer control, then require native keyboard
      // traversal into Search. The outer chrome toggle is last in DOM order
      // for native drag-region safety, not the start of the sidebar tab order.
      const focusAnchor = section === 'settings' ? '.settings-nav-item' : '.sidebar-notifications-button';
      const anchorPoint = await point(focusAnchor, false);
      moveNativePointer(anchorPoint.x, anchorPoint.y);
      await evaluate(selector => document.querySelector(selector).focus(), focusAnchor);
      await waitFor(selector => document.activeElement === document.querySelector(selector), focusAnchor);
      key('Tab', section === 'settings' ? ['shift'] : []);
      await waitFor(selector => document.activeElement === document.querySelector(selector), focusTarget);
      assert.equal(await evaluate(() => document.documentElement.dataset.focusModality), 'keyboard');
      await capture(`${section}-keyboard-focused-drawer`);
      moveNativePointer(800, 500);
      await settle();
      await delay(400);
      assert.ok(await evaluate(selector => document.activeElement === document.querySelector(selector) && !document.activeElement.closest('[inert]'), focusTarget), 'Focused drawer contents remain reachable after pointer leaves');
      await click(leftToggle, `dock-${section}`);
      await waitFor(() => !document.querySelector('.app-shell').classList.contains('sidebar-collapsed'));
      const resize = '.app-shell > .sidebar-resizer';
      const width = await evaluate(selector => Number(document.querySelector(selector).getAttribute('aria-valuenow')), resize);
      await evaluate(selector => document.querySelector(selector).focus(), resize);
      key('Right');
      await waitFor(({ selector, width }) => Number(document.querySelector(selector).getAttribute('aria-valuenow')) > width, { selector: resize, width });
      const grown = await evaluate(selector => Number(document.querySelector(selector).getAttribute('aria-valuenow')), resize);
      key('Left');
      await waitFor(({ selector, width }) => Number(document.querySelector(selector).getAttribute('aria-valuenow')) === width, { selector: resize, width });
      await capture(`${section}-keyboard-resized`);
      runs.push({ section, before, collapsed, toggleStayedClosed, edge, hovered, focusAnchor, focusTarget, width, grown });
    }
    await goConversations(); return runs;
  });

  await test('right preview toggle retains state across settings and restores focus', async () => {
    await selectThread('acceptance-reading'); await dockSidebar();
    if (await evaluate(() => document.querySelector('.app-shell').classList.contains('right-panel-open'))) await click(rightToggle, 'reset-right-panel');
    await click(composer, 'focus-before-preview', false);
    await click(rightToggle, 'open-right-preview');
    await waitFor(() => document.querySelector('.app-shell').classList.contains('right-panel-open'));
    if (await evaluate(() => !!document.querySelector('[data-wuu-tool="files"]'))) {
      await click('[data-wuu-tool="files"]', 'preview-files');
      await waitFor(() => !!document.querySelector('file-tree-container'));
    }
    await capture('right-preview-open');
    const before = await evaluate(() => ({ tabs: [...document.querySelectorAll('.workspace-panel-tabs [role="tab"]')].map(n => n.textContent), text: document.querySelector('.workspace-right-panel')?.innerText.slice(0, 800) }));
    await goSettings('General');
    await goConversations();
    assert.ok(await evaluate(() => document.querySelector('.app-shell').classList.contains('right-panel-open')), 'Settings preserves the open preview');
    const after = await evaluate(() => ({ tabs: [...document.querySelectorAll('.workspace-panel-tabs [role="tab"]')].map(n => n.textContent), text: document.querySelector('.workspace-right-panel')?.innerText.slice(0, 800) }));
    assert.deepEqual(after, before, 'Settings preserves preview content');
    await click(rightToggle, 'close-right-preview');
    await waitFor(() => !document.querySelector('.app-shell').classList.contains('right-panel-open'));
    await click(composer, 'composer-reachable-after-preview', false);
    assert.ok(await evaluate(selector => document.activeElement === document.querySelector(selector), composer));
    await click(rightToggle, 'reopen-right-preview'); await click(rightToggle, 'close-right-preview-again');
    return { before, after };
  });

  for (const theme of ['light', 'dark']) {
    for (const size of [15, 20]) {
      for (const width of [1280, 960, 1440]) {
        if (process.env.WUU_WORKBENCH_MATRIX === 'smoke' && !((theme === 'light' && size === 15 && width === 1280) || (theme === 'dark' && size === 20 && width === 960))) continue;
        await test(`rendered matrix ${theme} ${size}px ${width}px`, async () => {
          await goConversations(); await dockSidebar();
          await appearance(theme, size, width);
          const settings = await geometry(`settings-${theme}-${size}-${width}`);
          await capture(`matrix-settings-${theme}-${size}-${width}`);
          await goConversations();
          const reading = await geometry(`conversation-${theme}-${size}-${width}`);
          await capture(`matrix-conversation-${theme}-${size}-${width}`);
          await click(plugins, 'matrix-plugins', false);
          await click(pick('.sidebar-section-item', 'Skills'), 'matrix-skills', false);
          const skills = await geometry(`skills-${theme}-${size}-${width}`);
          await capture(`matrix-skills-${theme}-${size}-${width}`);
          return { settings, reading, skills };
        });
      }
    }
  }

  await test('125 percent desktop zoom keeps controls reachable', async () => {
    await appearance('light', 20, 1440); await goConversations();
    try {
      win.webContents.setZoomFactor(1.25);
      await settle();
      await capture('desktop-zoom-125-percent');
      const reading = await geometry('desktop-zoom-reading');
      await goSettings('Appearance');
      const settings = await geometry('desktop-zoom-settings');
      return { reading, settings };
    } finally { win.webContents.setZoomFactor(1); await settle(); }
  });

  await test('Linux outer chrome double-click and caption restore', async () => {
    await appearance('light', 15, 1280); await goConversations();
    const chrome = await point('.workbench-window-chrome', false);
    const bounds = win.getContentBounds();
    await capture('before-titlebar-double-click');
    // X11 input reaches the actual OS drag strip, unlike synthetic DOM events.
    const input = spawnSync('xdotool', ['mousemove', String(bounds.x + chrome.x), String(bounds.y + chrome.y), 'click', '--repeat', '2', '--delay', '100', '1'], { encoding: 'utf8' });
    assert.equal(input.status, 0, input.stderr || String(input.error));
    const deadline = Date.now() + 6000;
    while (!win.isMaximized() && Date.now() < deadline) await delay(40);
    assert.ok(win.isMaximized(), 'Double-clicking the outer window chrome maximizes the real OS window');
    await capture('after-titlebar-maximize');
    await click('.linux-window-control[aria-label="Restore"]', 'caption-restore');
    const restoreDeadline = Date.now() + 6000;
    while (win.isMaximized() && Date.now() < restoreDeadline) await delay(40);
    assert.ok(!win.isMaximized(), 'Caption restore returns to normal window geometry');
    win.setPosition(30, 30); win.setContentSize(1280, 900);
  });

  await test('empty conversation and repeated section switching recover', async () => {
    await appearance('light', 15, 1280);
    await selectThread('acceptance-empty');
    await capture('empty-conversation');
    for (let i = 0; i < 3; i++) {
      await click(plugins, 'repeat-plugins', false);
      await goSettings('Appearance');
      await goConversations();
    }
    assert.ok(await evaluate(() => !!document.querySelector('.empty-scroll-region') && [...document.querySelectorAll('.thread-row.active')].some(n => n.textContent.includes('empty conversation'))));
    await click(composer, 'empty-composer-reachable', false);
    assert.ok(await evaluate(selector => document.activeElement === document.querySelector(selector), composer));
    await capture('final-recovered-window');
  });
  await selectThread('acceptance-reading');
  await runWebsiteAcceptance({ win, report, output, started, save, evaluate, waitFor, settle, delay, point, fill, key, pick, test,
    goConversations, goSettings, appearance, plugins, conversations }, websiteFixture);
  await test('no renderer errors or accidental inference', async () => {
    assert.deepEqual(report.errors, []);
    assert.ok(!rpc.some(r => ['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(r.channel)));
  });
  if (report.status !== 'timed-out') {
    report.status = report.checks.some(c => c.status === 'failed') ? 'failed' : 'passed';
    report.finishedAt = new Date().toISOString(); report.durationMs = Date.now() - started;
  }
  report.ipc = rpc.map(({ channel, completed, error }) => ({ channel, completed, error }));
  save();
  console.log(`WORKBENCH ${report.status}: ${report.checks.filter(c => c.status === 'passed').length}/${report.checks.length} checks, ${report.screenshots.length} real-window captures`);
}
run().then(async () => {
  clearTimeout(watchdog); websiteFixture?.close();
  const code = report.status === 'passed' ? 0 : 1;
  // The real app's quit hook shuts down its Go child; preserve failing status.
  app.once('will-quit', () => app.exit(code));
  app.quit();
  setTimeout(() => app.exit(code), 1000).unref();
}).catch(async error => {
  clearTimeout(watchdog); websiteFixture?.close();
  if (report.status !== 'timed-out') report.status = 'failed';
  report.fatal = String(error.stack || error);
  console.error(error);
  if (win && !win.isDestroyed()) { try { await capture('fatal'); } catch {} }
  save(); app.exit(1);
});
