// Production renderer E2E for the disabled snapshot boundary and shared permissions.
// The native boundary is synthetic; this does not exercise macOS TCC or capture.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { app, BrowserWindow, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_APP_CONTEXT_E2E_OUTPUT || path.join(desktop, 'out/app-context-e2e'));
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
process.env.WUU_STREAM_E2E_CWD = output;
process.env.WUU_STREAM_E2E_SIDEBAR_THREADS = '2';
app.commandLine.appendSwitch('disable-gpu');
app.on('window-all-closed', () => {});
const screenshots = process.env.WUU_APP_CONTEXT_E2E_SCREENSHOTS !== '0';
const snapshotCalls = [];
let sends = 0, guideClosed = 0, permissionRequested, permissionFailure = false;
let permissions = { available: true, screen: false, accessibility: false };
// Model an older host with saved opt-in settings: the release renderer must
// still keep the disabled feature from reading captures or changing drafts.
let state = { available: true, settings: { enabled: true, shortcut: 'Control+Alt+Space', include_text: true }, shortcut_registered: true, phase: 'idle' };
ipcMain.handle('permissions:state', () => {
  if (permissionFailure) throw new Error('Synthetic permission status failure');
  return permissions;
});
ipcMain.handle('permissions:request', (_event, permission) => { permissionRequested = permission; });
ipcMain.handle('permissions:close', () => { guideClosed++; });
for (const operation of ['state', 'recording', 'save', 'permission', 'discard']) {
  ipcMain.handle(`snapshot:${operation}`, () => { snapshotCalls.push(operation); return state; });
}
ipcMain.on('snapshot:subscribe', () => snapshotCalls.push('subscribe'));
ipcMain.on('snapshot:send', () => sends++);
let preload = fs.readFileSync(path.join(__dirname, 'streaming-e2e-preload.cjs'), 'utf8');
preload = preload.replace('contextBridge.exposeInMainWorld("wuu", {', `
ipcRenderer.on('snapshot:changed', () => ipcRenderer.send('snapshot:delivered'));
contextBridge.exposeInMainWorld("wuu", {
  platform: 'darwin',
  getBuildInfo: async () => ({ core: undefined, desktop: { version: 'app-snapshot-e2e', date: '2026-10-09' } }),
  listMCPServers: async () => ({ servers: [] }),
  listEngines: async () => ({ engines: [] }),
  getAppContextState: () => ipcRenderer.invoke('snapshot:state'),
  setAppContextShortcutRecording: value => ipcRenderer.invoke('snapshot:recording', value),
  updateAppContextSettings: value => ipcRenderer.invoke('snapshot:save', value),
  requestAppContextPermission: () => ipcRenderer.invoke('snapshot:permission'),
  discardAppContextSnapshot: id => ipcRenderer.invoke('snapshot:discard', id),
  onAppContextChanged: handler => { ipcRenderer.send('snapshot:subscribe'); const listener = () => handler(); ipcRenderer.on('snapshot:changed', listener); return () => ipcRenderer.removeListener('snapshot:changed', listener); },
  getSystemPermissions: () => ipcRenderer.invoke('permissions:state'),
  requestSystemPermission: permission => ipcRenderer.invoke('permissions:request', permission),
  closeSystemPermissionGuide: () => ipcRenderer.invoke('permissions:close'),`);
preload = preload.replace('startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => {', 'startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => { ipcRenderer.send("snapshot:send");');
const preloadPath = path.join(output, 'preload.cjs');
fs.writeFileSync(preloadPath, preload);
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn.toString()})()`, true);
async function waitFor(win, fn) {
  const until = Date.now() + 12_000;
  while (Date.now() < until) {
    const value = await evaluate(win, fn);
    if (value) return value;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
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
  const screenshot = await win.webContents.capturePage();
  assert.ok(screenshot.getSize().width > 1, 'A real display is required for visual evidence. Set WUU_APP_CONTEXT_E2E_SCREENSHOTS=0 for functional-only E2E.');
  fs.writeFileSync(path.join(output, name + '.png'), screenshot.toPNG());
}
async function publish(win, next) {
  state = next;
  // Acknowledge receipt in the preload before checking that no consumer ran.
  await new Promise((resolve, reject) => {
    const delivered = () => { clearTimeout(timer); resolve(); };
    const timer = setTimeout(() => { ipcMain.removeListener('snapshot:delivered', delivered); reject(new Error('Snapshot event was not delivered')); }, 12_000);
    ipcMain.once('snapshot:delivered', delivered);
    win.webContents.send('snapshot:changed');
  });
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1150, height: 900, show: true, webPreferences: { preload: preloadPath, contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false } });
  const errors = [];
  win.webContents.on('console-message', event => { if (event.level === 'error') { errors.push(event.message); console.error('Renderer:', event.message); } });
  await win.loadFile(path.join(desktop, 'out/renderer/index.html'));
  await waitFor(win, () => Boolean(document.querySelector('.composer textarea')));
  await evaluate(win, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Keep my existing draft');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await waitFor(win, () => document.querySelector('.composer textarea').value === 'Keep my existing draft');
  assert.deepEqual(snapshotCalls, [], 'The disabled capture host must not read state or subscribe');
  await evaluate(win, () => document.querySelector('.sidebar-account-trigger').click());
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => Boolean(document.querySelector('.settings-nav-item')));
  await evaluate(win, () => [...document.querySelectorAll('.settings-nav-item')].find(button => /^(General|常规)$/.test(button.textContent.trim())).click());
  await waitFor(win, () => document.querySelectorAll('.system-permission-row button:not(:disabled)').length === 2);
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('[data-testid="settings-app-context"]'))), false, 'Release settings must not offer disabled snapshots');
  assert.deepEqual(snapshotCalls, [], 'Opening settings must not activate snapshot APIs');
  await evaluate(win, () => document.querySelector('.system-permission-row button').click());
  await waitFor(win, () => !document.querySelector('.system-permission-row button').disabled);
  assert.equal(permissionRequested, 'screen');
  permissions = { ...permissions, screen: true };
  await evaluate(win, () => window.dispatchEvent(new Event('focus')));
  await waitFor(win, () => document.querySelectorAll('.system-permission-granted').length === 1);
  for (const [theme, width, size] of [['light', 1150, 15], ['dark', 1150, 15], ['light', 620, 20], ['dark', 620, 20]]) {
    win.setContentSize(width, 900);
    await win.webContents.executeJavaScript(`document.documentElement.dataset.theme=${JSON.stringify(theme)};document.documentElement.style.setProperty('--conversation-message-font-size', '${size}px');document.querySelector('[data-testid="settings-system-permissions"]').scrollIntoView({block:'center'});`);
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
  await evaluate(win, () => document.querySelector('.settings-back-button').click());
  await waitFor(win, () => Boolean(document.querySelector('.composer textarea')) && !document.querySelector('.settings-back-button'));
  assert.ok(guideClosed > 0, 'Leaving permission settings closes the guide');
  win.setContentSize(1150, 900);
  await evaluate(win, () => { document.documentElement.dataset.theme = 'light'; document.documentElement.style.setProperty('--conversation-message-font-size', '15px'); });
  const snapshot = { id: 'disabled-fixture', captured_at: '2026-10-09T19:00:00Z', app_name: 'Example Notes', bundle_id: 'example.notes', window_title: 'Project notes', window_id: 7, width: 1, height: 1,
    image_base64: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', available_text: 'Synthetic snapshot', text_status: 'included' };
  for (const next of [{ ...state, phase: 'ready', snapshot }, { ...state, phase: 'error', error: 'Synthetic capture failure' }]) {
    await publish(win, next);
    assert.deepEqual(snapshotCalls, [], 'Disabled snapshots must ignore ready and error notifications');
    assert.equal(await evaluate(win, () => Boolean(document.querySelector('.app-context-dialog'))), false);
    assert.equal(await evaluate(win, () => document.querySelectorAll('.composer-attachment-tray-item').length), 0);
    assert.equal(await evaluate(win, () => document.querySelector('.composer textarea').value), 'Keep my existing draft');
    assert.equal(sends, 0, 'A disabled snapshot must never submit the draft');
  }
  await capture(win, 'disabled-snapshot-preserves-draft');
  assert.deepEqual(errors, []);
  const report = { result: 'passed', coverage: ['release settings hide app snapshots despite saved opt-in', 'disabled capture host never reads state or subscribes', 'ready and error events preserve drafts without attachments, dialogs or sends', 'permission request, grant refresh and retry remain available', 'leaving settings closes the permission guide', 'light/dark narrow/large permission layouts'], snapshot_calls: snapshotCalls, sends, native_macos_capture: 'not exercised', screenshots: screenshots ? 'captured' : 'not exercised (functional-only mode)', console_errors: errors };
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
