// Production renderer E2E with a synthetic native boundary, not macOS TCC acceptance.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { app, BrowserWindow, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_APP_CONTEXT_E2E_OUTPUT || path.join(desktop, 'out/app-context-e2e'));
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
process.env.WUU_STREAM_E2E_CWD = output;
process.env.WUU_STREAM_E2E_SIDEBAR_THREADS = "2";
app.commandLine.appendSwitch('disable-gpu');
app.on('window-all-closed', () => {});
const empty = () => ({ available: true, settings: { enabled: false, shortcut: 'Control+Alt+Space', include_text: false }, shortcut_registered: false, phase: 'idle' });
let state = empty(), sends = 0;
const screenshots = process.env.WUU_APP_CONTEXT_E2E_SCREENSHOTS !== '0';
ipcMain.handle('snapshot:state', () => state);
ipcMain.handle('snapshot:save', (_event, settings) => (state = { ...empty(), settings, shortcut_registered: settings.enabled }));
ipcMain.handle('snapshot:discard', (_event, id) => {
  if (id && state.snapshot?.id !== id) throw new Error('This snapshot was replaced or discarded.');
  return state = { ...state, snapshot: undefined, error: undefined, phase: 'idle' };
});
ipcMain.on('snapshot:send', () => sends++);
let preload = fs.readFileSync(path.join(__dirname, 'streaming-e2e-preload.cjs'), 'utf8');
preload = preload.replace('contextBridge.exposeInMainWorld("wuu", {', `
threads.get('sidebar-fade-0').turns = [{ id: 'snapshot-related-turn', status: 'completed', items: [{
  id: 'snapshot-related-message', type: 'user_message', text: 'A related conversation is ready',
  input_text: 'Fixture handoff for the related conversation.', related_session_id: 'sidebar-fade-1',
  read_only: true, origin: 'plugin', origin_id: 'subagent', cause: 'subagent.completion', presentation_kind: 'query_bubble',
}] }];
contextBridge.exposeInMainWorld("wuu", {`);

preload = preload.replace('contextBridge.exposeInMainWorld("wuu", {', `contextBridge.exposeInMainWorld("wuu", {
  platform: 'darwin',
  getBuildInfo: async () => ({ core: undefined, desktop: { version: 'app-snapshot-e2e', date: '2026-10-09' } }),
  listMCPServers: async () => ({ servers: [] }),
  getAppContextState: () => ipcRenderer.invoke('snapshot:state'),
  updateAppContextSettings: value => ipcRenderer.invoke('snapshot:save', value),
  requestAppContextPermission: () => ipcRenderer.invoke('snapshot:state'),
  discardAppContextSnapshot: id => ipcRenderer.invoke('snapshot:discard', id),
  onAppContextChanged: handler => { const listener = () => handler(); ipcRenderer.on('snapshot:changed', listener); return () => ipcRenderer.removeListener('snapshot:changed', listener); },`);
preload = preload.replace('startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => {', 'startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => { ipcRenderer.send("snapshot:send");');
const preloadPath = path.join(output, 'preload.cjs'); fs.writeFileSync(preloadPath, preload);
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn.toString()})()`, true).catch(error => { console.error('Failed expression:', fn.toString()); throw error; });
async function waitFor(win, fn) {
  const until = Date.now() + 12_000;
  while (Date.now() < until) { const value = await evaluate(win, fn); if (value) return value; await new Promise(resolve => setTimeout(resolve, 40)); }
  throw new Error(`Timed out: ${fn.toString()}`);
}
async function capture(win, name) {
  if (!screenshots) return;
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  // Background/cached panes intentionally pause animations. Only the visible
  // snapshot dialog and its scrim must finish entering; keep the wait bounded.
  await waitFor(win, () => {
    const panel = document.querySelector('.app-context-dialog');
    if (!panel) return true;
    return [panel, panel.closest('.app-modal-backdrop')].filter(Boolean).every(element =>
      element.getAnimations().every(animation => ['finished', 'idle'].includes(animation.playState)));
  });
  const screenshot = await win.webContents.capturePage();
  assert.ok(screenshot.getSize().width > 1, 'A real display is required for visual evidence. Set WUU_APP_CONTEXT_E2E_SCREENSHOTS=0 for functional-only E2E.');
  fs.writeFileSync(path.join(output, name + '.png'), screenshot.toPNG());
}
function publish(win, next) { state = next; win.webContents.send('snapshot:changed'); }
function click(win, action) { return win.webContents.executeJavaScript(`document.querySelector('[data-app-context-action="${action}"]').click()`); }
app.whenReady().then(async () => {
  const reference = new BrowserWindow({ width: 720, height: 440, show: false, webPreferences: { backgroundThrottling: false } });
  await reference.loadURL('data:text/html,' + encodeURIComponent('<body style="font:18px system-ui;background:#f7f8fa;padding:32px"><h1>Example project notes</h1><p>Design review · Friday</p><p>Keep the draft local until you choose Send.</p><hr><p>Research complete. Native Mac acceptance is pending.</p></body>'));
  await evaluate(reference, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  // Render only synthetic fixture content; this is not native capture evidence.
  const sourceImage = await evaluate(reference, () => {
    const canvas = document.createElement('canvas'); canvas.width = 720; canvas.height = 440;
    const context = canvas.getContext('2d'); context.fillStyle = '#f7f8fa'; context.fillRect(0, 0, 720, 440);
    context.fillStyle = '#243245'; context.font = 'bold 28px sans-serif'; context.fillText('Example project notes', 32, 64);
    context.font = '18px sans-serif'; context.fillText('Design review · Friday', 32, 120);
    context.fillText('Keep the draft local until you choose Send.', 32, 170);
    return canvas.toDataURL('image/png').split(',')[1];
  });
  const image = Buffer.from(sourceImage, 'base64');
  fs.writeFileSync(path.join(output, 'source-window-fixture.png'), image); reference.destroy();
  const snapshot = { id: 'fixture-1', captured_at: '2026-10-09T19:00:00Z', app_name: 'Example Notes', bundle_id: 'example.notes', window_title: 'Project notes', window_id: 7, width: 720, height: 440, image_base64: image.toString('base64'), available_text: 'Design review\nResearch complete.\nNative Mac acceptance is pending.', text_status: 'included' };
  const win = new BrowserWindow({ width: 1150, height: 900, show: true, webPreferences: { preload: preloadPath, contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false } });
  const errors = [];
  win.webContents.on('console-message', event => { if (event.level === 'error') { errors.push(event.message); console.error('Renderer:', event.message); } });
  await win.loadFile(path.join(desktop, 'out/renderer/index.html'));
  await waitFor(win, () => Boolean(document.querySelector('.composer textarea')));
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('.app-context-dialog'))), false);
  await evaluate(win, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Keep my existing draft');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    window.dispatchEvent(new Event('wuu:open-app-snapshot'));
  });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-settings')));
  assert.equal(await evaluate(win, () => document.querySelector('.app-context-settings input[type="checkbox"]').checked), false);
  await capture(win, 'setup-light');
  await evaluate(win, () => document.querySelector('.app-context-settings input[type="checkbox"]').click());
  await click(win, 'save');
  await waitFor(win, () => document.querySelector('.app-context-settings [role="status"]')?.textContent.includes('Control+Alt+Space'));
  await click(win, 'cancel');
  await waitFor(win, async () => (await window.wuu.getAppContextState()).phase === 'idle');
  publish(win, { ...state, phase: 'ready', snapshot });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-preview img')));
  assert.equal(sends, 0);
  for (const [theme, width, size] of [['light', 1150, 14], ['dark', 780, 14], ['light', 620, 20], ['dark', 620, 20]]) {
    win.setContentSize(width, 900);
    await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--conversation-message-font-size', '${size}px')`);
    await capture(win, `preview-${theme}-${width}-${size}`);
    const layout = await evaluate(win, () => { const panel = document.querySelector('.app-context-dialog').getBoundingClientRect(); const image = document.querySelector('.app-context-preview img').getBoundingClientRect(); return { panelVisible: panel.left >= 0 && panel.right <= innerWidth, imageContained: image.left >= panel.left && image.right <= panel.right }; });
    assert.ok(layout.panelVisible && layout.imageContained, JSON.stringify(layout));
  }
  await click(win, 'cancel');
  await waitFor(win, async () => (await window.wuu.getAppContextState()).phase === 'idle');
  assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 0);
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'fixture-add' } });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-preview img')));
  await click(win, 'add');
  await waitFor(win, () => !document.querySelector('.app-context-dialog'));
  assert.equal(await evaluate(win, () => document.querySelector('.composer textarea').value), 'Keep my existing draft');
  assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 2);
  assert.equal(sends, 0, 'Adding must never submit the draft');
  await capture(win, 'attached-draft');
  publish(win, { ...state, phase: 'error', error: 'The target window changed during capture.', snapshot: undefined });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-dialog [role="alert"]')));
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('[data-app-context-action="add"]'))), false);
  await capture(win, 'target-changed'); await click(win, 'cancel');
  await waitFor(win, async () => (await window.wuu.getAppContextState()).phase === 'idle');
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'stale' } });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-preview img')));
  state = { ...state, phase: 'idle', snapshot: undefined };
  await click(win, 'add');
  await waitFor(win, () => document.querySelector('.app-context-dialog [role="alert"]')?.textContent.includes('replaced'));
  assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 2);
  assert.equal(sends, 0); await capture(win, 'revoked-preview');
  await click(win, 'cancel');
  await waitFor(win, async () => (await window.wuu.getAppContextState()).phase === 'idle');
  win.setContentSize(1450, 950);
  await evaluate(win, () => document.querySelector('.sidebar-account-trigger').click());
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => Boolean(document.querySelector('.settings-back-button')));
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'settings-visible' } });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-preview img')));
  assert.ok(await evaluate(win, () => Boolean(document.querySelector('.settings-back-button'))), 'Preview must not destroy Settings navigation');
  await capture(win, 'preview-over-settings'); await click(win, 'add');
  await waitFor(win, () => !document.querySelector('.app-context-dialog'));
  await evaluate(win, () => document.querySelector('.settings-back-button').click());
  await waitFor(win, () => document.querySelectorAll('.composer-attachment-tray-item').length === 4);
  assert.equal(await evaluate(win, () => document.querySelector('.composer textarea').value), 'Keep my existing draft');

  // Open a real related-session split through its production message action.
  await evaluate(win, () => {
    const row = [...document.querySelectorAll('.thread-row')].find(element => element.textContent.includes('Conversation 01'));
    (row.querySelector('.thread-row-main') || row).click();
  });
  await waitFor(win, () => Boolean(document.querySelector('.user-message-actions button:has(svg[data-icon="info"])')));
  await evaluate(win, () => document.querySelector('.user-message-actions button:has(svg[data-icon="info"])').click());
  await waitFor(win, () => document.querySelectorAll('.conversation-split-pane').length === 2);
  await evaluate(win, () => {
    const pane = document.querySelector('.conversation-split-pane[data-thread-id="sidebar-fade-1"]');
    pane.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    const input = pane.querySelector('textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Preserve the secondary draft');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('[data-wuu-component="right-sidebar-toggle"]').click();
  });
  await waitFor(win, () => Boolean(document.querySelector('.workspace-panel-globalize')));
  await evaluate(win, () => {
    const toggle = document.querySelector('.workspace-panel-globalize');
    if (toggle.getAttribute('aria-pressed') !== 'true') toggle.click();
  });
  await waitFor(win, () => document.querySelector('.conversation-pane')?.hasAttribute('inert'));
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'covered-split' } });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-preview img')));
  await capture(win, 'preview-over-covered-split'); await click(win, 'add');
  await waitFor(win, () => !document.querySelector('.app-context-dialog'));
  await evaluate(win, () => document.querySelector('[data-wuu-component="right-sidebar-toggle"]').click());
  await waitFor(win, () => !document.querySelector('.conversation-pane')?.hasAttribute('inert'));
  assert.equal(await evaluate(win, () => document.querySelector('.conversation-split-pane[data-thread-id="sidebar-fade-1"] textarea').value), 'Preserve the secondary draft');
  assert.equal(await evaluate(win, () => document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-1"] .composer-attachment-tray-item').length), 2);
  assert.equal(await evaluate(win, () => document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-0"] .composer-attachment-tray-item').length), 0);
  assert.equal(sends, 0); await capture(win, 'active-split-draft');
  const report = { result: 'passed', coverage: ['opt-in setup', 'local preview', 'cancel discards', 'add preserves draft', 'no implicit send', 'target failure has no Add', 'revoked preview cannot add', 'light/dark narrow/large renderer layouts', 'preview survives Settings route', 'covered split routes to actual active draft'], native_macos_capture: 'not exercised', screenshots: screenshots ? 'captured' : 'not exercised (functional-only mode)', console_errors: errors };
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report)); console.log(`Evidence: ${output}`); app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
