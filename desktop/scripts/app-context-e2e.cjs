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
let state = empty(), sends = 0, recording = false;
let permissions = { available: true, screen: false, accessibility: false }, permissionFailure = false;
let permissionRequested, guideClosed = 0;
ipcMain.handle('permissions:state', () => {
  if (permissionFailure) throw new Error('Synthetic permission status failure');
  return permissions;
});
ipcMain.handle('permissions:request', (_event, permission) => { permissionRequested = permission; });
ipcMain.handle('permissions:close', () => { guideClosed++; });
const screenshots = process.env.WUU_APP_CONTEXT_E2E_SCREENSHOTS !== '0';
let holdNextStateRead = false, releaseStateRead, stateReadStarted;
ipcMain.handle('snapshot:state', () => {
  const result = state;
  if (!holdNextStateRead) return result;
  holdNextStateRead = false;
  stateReadStarted?.();
  return new Promise(resolve => { releaseStateRead = () => resolve(result); });
});
ipcMain.handle('snapshot:recording', (_event, value) => { recording = value; return { ...state, shortcut_registered: !recording && state.settings.enabled }; });
ipcMain.handle('snapshot:save', (_event, settings) => (state = { ...empty(), settings, shortcut_registered: settings.enabled }));
ipcMain.handle('snapshot:discard', (_event, id) => {
  if (id && state.snapshot?.id !== id) throw new Error('This snapshot was replaced or discarded.');
  return state = { ...state, snapshot: undefined, error: undefined, phase: 'idle' };
});
ipcMain.on('snapshot:send', () => sends++);
let holdNextResume = false, resumeStarted, releaseResume;
ipcMain.handle('snapshot:resume', () => {
  if (!holdNextResume) return;
  holdNextResume = false;
  resumeStarted?.();
  return new Promise(resolve => { releaseResume = resolve; });
});
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
  setAppContextShortcutRecording: value => ipcRenderer.invoke('snapshot:recording', value),
  updateAppContextSettings: value => ipcRenderer.invoke('snapshot:save', value),
  requestAppContextPermission: () => ipcRenderer.invoke('snapshot:state'),
  getSystemPermissions: () => ipcRenderer.invoke('permissions:state'),
  requestSystemPermission: permission => ipcRenderer.invoke('permissions:request', permission),
  closeSystemPermissionGuide: () => ipcRenderer.invoke('permissions:close'),
  discardAppContextSnapshot: id => ipcRenderer.invoke('snapshot:discard', id),
  onAppContextChanged: handler => { const listener = () => handler(); ipcRenderer.on('snapshot:changed', listener); return () => ipcRenderer.removeListener('snapshot:changed', listener); },`);
preload = preload.replace('startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => {', 'startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => { ipcRenderer.send("snapshot:send");');
preload = preload.replace('resumeThread: async (id) => ({ thread: threads.get(id) ?? null }),', 'resumeThread: async (id) => { await ipcRenderer.invoke("snapshot:resume"); return { thread: threads.get(id) ?? null }; },');
const preloadPath = path.join(output, 'preload.cjs'); fs.writeFileSync(preloadPath, preload);
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn.toString()})()`, true).catch(error => { console.error('Failed expression:', fn.toString()); throw error; });
async function waitForSignal(signal, description) {
  let timer;
  try {
    await Promise.race([signal, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`Timed out waiting for ${description}`)), 12_000);
    })]);
  } finally { clearTimeout(timer); }
}
async function waitFor(win, fn) {
  const until = Date.now() + 12_000;
  while (Date.now() < until) { const value = await evaluate(win, fn); if (value) return value; await new Promise(resolve => setTimeout(resolve, 40)); }
  throw new Error(`Timed out: ${fn.toString()}`);
}
async function capture(win, name) {
  if (!screenshots) return;
  await evaluate(win, async () => {
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(animation =>
      animation.playState === 'running' && Number.isFinite(animation.effect.getComputedTiming().endTime)
    ).map(animation => animation.finished.catch(() => {})));
  });
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
async function holdEncoding(win) {
  await evaluate(win, () => {
    window.snapshotEncodingHeld = false;
    const original = File.prototype.arrayBuffer;
    File.prototype.arrayBuffer = function () {
      File.prototype.arrayBuffer = original;
      window.snapshotEncodingHeld = true;
      return new Promise(resolve => { window.releaseSnapshotEncoding = () => resolve(original.call(this)); });
    };
  });
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
  });
  await evaluate(win, () => document.querySelector('.sidebar-account-trigger').click());
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => Boolean(document.querySelector('.settings-nav-item')));
  await evaluate(win, () => [...document.querySelectorAll('.settings-nav-item')].find(button => /^(General|常规)$/.test(button.textContent.trim())).click());
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="settings-app-context"] [role="switch"]:not(:disabled)')));
  assert.equal(await evaluate(win, () => document.querySelector('[data-testid="settings-app-context"] [role="switch"]').getAttribute('aria-checked')), 'false');
  await evaluate(win, () => document.querySelectorAll('[data-testid="settings-app-context"] [role="switch"]')[1].click());
  await waitFor(win, async () => (await window.wuu.getAppContextState()).settings.include_text);
  for (const [theme, width, size] of [['light', 1150, 15], ['dark', 1150, 15], ['light', 620, 20], ['dark', 620, 20]]) {
    win.setContentSize(width, 900);
    await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--font-ui', '${size}px');document.querySelector('[data-testid="settings-app-context"]').scrollIntoView({block:'center'});`);
    await capture(win, `setup-${theme}-${width}-${size}`);
  }
  await waitFor(win, () => document.querySelectorAll('.system-permission-row button:not(:disabled)').length === 2);
  await evaluate(win, () => document.querySelector('.system-permission-row button').click());
  await waitFor(win, () => !document.querySelector('.system-permission-row button').disabled);
  assert.equal(permissionRequested, 'screen');
  permissions = { ...permissions, screen: true };
  await evaluate(win, () => window.dispatchEvent(new Event('focus')));
  await waitFor(win, () => document.querySelectorAll('.system-permission-granted').length === 1);
  for (const [theme, width, size] of [['light', 1150, 15], ['dark', 1150, 15], ['light', 620, 20], ['dark', 620, 20]]) {
    win.setContentSize(width, 900);
    await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--font-ui', '${size}px');document.querySelector('[data-testid="settings-system-permissions"]').scrollIntoView({block:'center'});`);
    await capture(win, `permissions-${theme}-${width}-${size}`);
  }
  permissionFailure = true;
  await evaluate(win, () => window.dispatchEvent(new Event('focus')));
  await waitFor(win, () => document.querySelector('.system-permissions [role="alert"]'));
  assert.equal(await evaluate(win, () => document.querySelectorAll('.system-permission-granted').length), 0);
  await capture(win, 'permissions-error');
  permissionFailure = false;
  permissions = { ...permissions, accessibility: true };
  await evaluate(win, () => document.querySelector('.system-permissions-error button').click());
  await waitFor(win, () => document.querySelectorAll('.system-permission-granted').length === 2);
  await capture(win, 'permissions-granted');
  await evaluate(win, () => { const button = document.querySelector('[data-app-context-shortcut]'); button.scrollIntoView({block:'center'}); button.focus(); button.click(); });
  await waitFor(win, () => document.querySelector('[data-app-context-shortcut]').getAttribute('aria-pressed') === 'true');
  assert.equal(recording, true);
  await capture(win, 'shortcut-recording');
  await evaluate(win, () => document.querySelector('[data-app-context-shortcut]').dispatchEvent(new KeyboardEvent('keydown', { key: '˚', code: 'KeyK', ctrlKey: true, altKey: true, bubbles: true })));
  await waitFor(win, async () => (await window.wuu.getAppContextState()).settings.shortcut === 'Control+Alt+K');
  await evaluate(win, () => document.querySelector('[data-testid="settings-app-context"] [role="switch"]').click());
  await waitFor(win, async () => (await window.wuu.getAppContextState()).shortcut_registered);
  // Modifier-only presses keep recording open; Escape and focus loss preserve the saved shortcut.
  await evaluate(win, () => { const button = document.querySelector('[data-app-context-shortcut]'); button.focus(); button.click(); });
  await waitFor(win, () => document.querySelector('[data-app-context-shortcut]').getAttribute('aria-pressed') === 'true');
  await evaluate(win, () => document.querySelector('[data-app-context-shortcut]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Control', ctrlKey: true, bubbles: true })));
  assert.equal(recording, true);
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
  await waitFor(win, () => document.querySelector('[data-app-context-shortcut]').getAttribute('aria-pressed') === 'false');
  assert.equal(recording, false);
  assert.equal(state.settings.shortcut, 'Control+Alt+K');
  await evaluate(win, () => { const button = document.querySelector('[data-app-context-shortcut]'); button.focus(); button.click(); });
  await waitFor(win, () => document.querySelector('[data-app-context-shortcut]').getAttribute('aria-pressed') === 'true');
  await waitFor(win, () => document.activeElement === document.querySelector('[data-app-context-shortcut]'));
  await evaluate(win, () => document.querySelector('.settings-back-button').focus());
  await waitFor(win, () => document.querySelector('[data-app-context-shortcut]').getAttribute('aria-pressed') === 'false');
  assert.equal(recording, false);
  assert.equal(state.settings.shortcut, 'Control+Alt+K');
  await evaluate(win, () => document.querySelector('.settings-back-button').click());
  win.setContentSize(1150, 900);
  await evaluate(win, () => { document.documentElement.dataset.theme = 'light'; document.documentElement.style.setProperty('--font-ui', '15px'); });
  await waitFor(win, async () => (await window.wuu.getAppContextState()).phase === 'idle');
  assert.equal(recording, false);
  assert.ok(guideClosed > 0, 'Leaving permission settings closes the guide');
  publish(win, { ...state, phase: 'ready', snapshot });
  await waitFor(win, () => document.querySelectorAll('.composer-attachment-tray-item').length === 2);
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('.app-context-dialog'))), false);
  assert.equal(await evaluate(win, () => document.querySelector('.composer textarea').value), 'Keep my existing draft');
  await waitFor(win, () => document.activeElement === document.querySelector('.composer textarea'));
  assert.equal(sends, 0, 'Capture must never submit the draft');
  // A delayed duplicate notification cannot attach the consumed snapshot again.
  state = { ...state, phase: 'ready', snapshot };
  holdNextStateRead = true;
  const readStarted = new Promise(resolve => { stateReadStarted = resolve; });
  win.webContents.send('snapshot:changed');
  await waitForSignal(readStarted, "the delayed snapshot state read");
  publish(win, { ...state, phase: 'idle', snapshot: undefined });
  await evaluate(win, () => window.wuu.getAppContextState());
  releaseStateRead();
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 2);
  for (const [theme, width, size] of [['light', 1150, 15], ['dark', 1150, 15], ['light', 620, 20], ['dark', 620, 20]]) {
    win.setContentSize(width, 900);
    await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--font-ui', '${size}px');`);
    await capture(win, `attached-${theme}-${width}-${size}`);
  }
  await capture(win, 'attached-draft');
  publish(win, { ...state, phase: 'error', error: 'The target window changed during capture.', snapshot: undefined });
  await waitFor(win, () => Boolean(document.querySelector('.app-context-dialog [role="alert"]')));
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('[data-app-context-action="add"]'))), false);
  await capture(win, 'target-changed'); await click(win, 'cancel');
  await waitFor(win, async () => (await window.wuu.getAppContextState()).phase === 'idle');
  await holdEncoding(win);
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'stale' } });
  await waitFor(win, () => window.snapshotEncodingHeld);
  state = { ...state, phase: 'idle', snapshot: undefined };
  await evaluate(win, () => window.releaseSnapshotEncoding());
  await waitFor(win, () => document.querySelector('.app-context-dialog [role="alert"]')?.textContent.includes('replaced'));
  assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 2);
  assert.equal(sends, 0); await capture(win, 'revoked-capture');
  await click(win, 'cancel');
  await waitFor(win, async () => (await window.wuu.getAppContextState()).phase === 'idle');
  win.setContentSize(1450, 950);
  await evaluate(win, () => document.querySelector('.sidebar-account-trigger').click());
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => Boolean(document.querySelector('.settings-back-button')));
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'settings-visible' } });
  await waitFor(win, () => !document.querySelector('.settings-back-button'));
  await waitFor(win, () => document.querySelectorAll('.composer-attachment-tray-item').length === 4);
  assert.equal(await evaluate(win, () => document.querySelector('.composer textarea').value), 'Keep my existing draft');

  // Navigation owns the draft as soon as it starts, including before the
  // delayed thread-load indicator appears. Startup restores Conversation 01,
  // so select a different thread to enter the real pending navigation path.
  holdNextResume = true;
  const navigationStarted = new Promise(resolve => { resumeStarted = resolve; });
  await evaluate(win, () => {
    const row = [...document.querySelectorAll('.thread-row')].find(element => element.textContent.includes('Conversation 02'));
    (row.querySelector('.thread-row-main') || row).click();
  });
  await waitForSignal(navigationStarted, "the held Conversation 02 resume");
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'navigation-pending' } });
  await waitFor(win, () => document.querySelector('.app-context-dialog [role="alert"]')?.textContent.includes('unavailable'));
  assert.equal(state.snapshot?.id, 'navigation-pending', 'A pending navigation must not attach to the outgoing draft');
  assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 4);
  await click(win, 'cancel');
  releaseResume();
  await waitFor(win, () => document.querySelector('.thread-row.active')?.textContent.includes('Conversation 02')
    && document.querySelector('[data-pip-anchor-host="conversation"]')?.inert === false
    && document.querySelector('[data-main-conversation-composer] textarea')?.readOnly === false);
  await evaluate(win, () => {
    const row = [...document.querySelectorAll('.thread-row')].find(element => element.textContent.includes('Conversation 01'));
    (row.querySelector('.thread-row-main') || row).click();
  });
  await waitFor(win, () => document.querySelector('.thread-row.active')?.textContent.includes('Conversation 01')
    && document.querySelector('[data-pip-anchor-host="conversation"]')?.inert === false
    && document.querySelector('[data-main-conversation-composer] textarea')?.readOnly === false);
  assert.equal(await evaluate(win, () => document.querySelector('.composer textarea').value), 'Keep my existing draft');
  assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 4);
  // Open a real related-session split through its production message action.
  await waitFor(win, () => Boolean(document.querySelector('.user-message-actions button:has(svg[data-icon="info"])')));
  await evaluate(win, () => document.querySelector('.user-message-actions button:has(svg[data-icon="info"])').click());
  await waitFor(win, () => document.querySelectorAll('.conversation-split-pane').length === 2);
  const primaryAttachmentsBefore = await evaluate(win, () => [...document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-0"] .composer-attachment-tray-item')]
    .map(item => ({ text: item.textContent, image: item.querySelector('img')?.getAttribute('src') })));
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
  await waitFor(win, () => document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-1"] .composer-attachment-tray-item').length === 2);
  await waitFor(win, () => !document.querySelector('.conversation-pane')?.hasAttribute('inert'));
  await waitFor(win, () => {
    const shell = document.querySelector('.app-shell');
    if (!shell || shell.classList.contains('right-panel-open') || shell.classList.contains('right-panel-animating')) return false;
    if (document.querySelector('.workspace-right-panel')?.hasAttribute('data-sheet')) return false;
    const input = document.querySelector('.conversation-split-pane[data-thread-id="sidebar-fade-1"] textarea');
    if (!input) return false;
    const rect = input.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0 && document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2) === input;
  });
  assert.equal(await evaluate(win, () => document.querySelector('.conversation-split-pane[data-thread-id="sidebar-fade-1"] textarea').value), 'Preserve the secondary draft');
  assert.equal(await evaluate(win, () => document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-1"] .composer-attachment-tray-item').length), 2);
  assert.deepEqual(await evaluate(win, () => [...document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-0"] .composer-attachment-tray-item')]
    .map(item => ({ text: item.textContent, image: item.querySelector('img')?.getAttribute('src') }))), primaryAttachmentsBefore);
  await waitFor(win, () => document.activeElement === document.querySelector('.conversation-split-pane[data-thread-id="sidebar-fade-1"] textarea'));
  assert.equal(sends, 0); await capture(win, 'active-split-draft');
  // A draft change during encoding must not redirect the capture.
  await holdEncoding(win);
  publish(win, { ...state, phase: 'ready', snapshot: { ...snapshot, id: 'draft-changed' } });
  await waitFor(win, () => window.snapshotEncodingHeld);
  await evaluate(win, () => document.querySelector('.conversation-split-pane[data-thread-id="sidebar-fade-0"]').dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })));
  await waitFor(win, () => document.querySelector('.conversation-split-pane[data-thread-id="sidebar-fade-0"]').classList.contains('active'));
  await evaluate(win, () => window.releaseSnapshotEncoding());
  await waitFor(win, () => document.querySelector('.app-context-dialog [role="alert"]')?.textContent.includes('changed'));
  assert.equal(await evaluate(win, () => document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-1"] .composer-attachment-tray-item').length), 2);
  assert.deepEqual(await evaluate(win, () => [...document.querySelectorAll('.conversation-split-pane[data-thread-id="sidebar-fade-0"] .composer-attachment-tray-item')]
    .map(item => ({ text: item.textContent, image: item.querySelector('img')?.getAttribute('src') }))), primaryAttachmentsBefore);
  assert.equal(state.snapshot?.id, 'draft-changed', 'Failed preparation must leave the snapshot unconsumed');
  assert.equal(sends, 0);
  await click(win, 'cancel');
  const report = { result: 'passed', coverage: ['permission request, grant refresh and retry', 'opt-in setup', 'shortcut recording with Option symbols, modifier-only keys, Escape and blur', 'direct attachment and focus', 'late state reads cannot duplicate attachments', 'draft changes during preparation are rejected', 'pending navigation cannot consume captures', 'preserves draft text', 'no implicit send', 'target failure', 'revoked capture cannot attach', 'light/dark narrow/large renderer layouts', 'returns from Settings to draft', 'covered split routes to actual active draft'], native_macos_capture: 'not exercised', screenshots: screenshots ? 'captured' : 'not exercised (functional-only mode)', console_errors: errors };
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report)); console.log(`Evidence: ${output}`); app.exit(0);
}).catch(async error => {
  console.error(error);
  for (const win of BrowserWindow.getAllWindows()) {
    if (win.isDestroyed()) continue;
    console.error(await evaluate(win, () => [...document.querySelectorAll('[role="alert"]')].map(element => element.textContent)));
    await capture(win, 'failure');
  }
  app.exit(1);
});
