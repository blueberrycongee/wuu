// Synthetic, inference-free recovery acceptance. Uses the real bundled entry,
// preload and native menus; never reads an installed-app profile.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const root = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_RECOVERY_OUTPUT || fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-recovery-evidence-')));
fs.mkdirSync(output, { recursive: true });
const entry = path.join(root, 'out/main/index.js');
assert.ok(fs.existsSync(entry), 'Build desktop first');
for (const mode of ['diagnostic', 'normal']) {
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), `wuu-recovery-${mode}-`));
  const home = path.join(fixture, 'home'); fs.mkdirSync(home);
  const broken = '{"api_key":"synthetic-secret", broken-json';
  fs.writeFileSync(path.join(home, 'config.json'), broken);
  fs.writeFileSync(path.join(home, 'preserve-sentinel'), 'saved-state');
  const harness = path.join(fixture, 'harness.mjs');
  fs.writeFileSync(harness, `
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { app, BrowserWindow, Menu, dialog, nativeTheme } from 'electron';
const mode = ${JSON.stringify(mode)};
const output = ${JSON.stringify(output)};
const spawns = [];
const originalSpawn = childProcess.spawn;
childProcess.spawn = function(command, ...args) { spawns.push(path.basename(String(command))); return originalSpawn.call(this, command, ...args); };
syncBuiltinESMExports();
app.setPath('userData', ${JSON.stringify(path.join(fixture, 'profile'))});
app.setPath('crashDumps', ${JSON.stringify(path.join(fixture, 'crashes'))});
// Do not block a failing headless test on a native fallback dialog.
dialog.showMessageBox = async () => ({ response: 0, checkboxChecked: false });
dialog.showSaveDialog = async () => ({ canceled: true });
const errors = [];
app.on('web-contents-created', (_event, contents) => {
  contents.on('preload-error', (_e, _p, error) => errors.push(String(error)));
});
const timer = setTimeout(() => { fs.writeFileSync(path.join(output, mode + '-timeout.json'), JSON.stringify({ spawns, errors })); app.exit(1); }, 25000);
assert.equal(app.isReady(), false, 'Entry must load before Electron ready');
await import(${JSON.stringify(pathToFileURL(entry).href)});
void app.whenReady().then(async () => {
const until = async (read) => { const end = Date.now() + 15000; while (Date.now() < end) { const value = await read(); if (value) return value; await new Promise(resolve => setTimeout(resolve, 50)); } throw new Error('Recovery condition timed out'); };
try {
  const menuItem = await until(() => Menu.getApplicationMenu()?.getMenuItemById('wuu-recovery'));
  assert.ok(menuItem.enabled);
  if (mode === 'normal') {
    await until(() => spawns.includes('wuu-core') || spawns.includes('wuu-core.exe'));
    const application = BrowserWindow.getAllWindows().find(win => win.webContents.getURL().startsWith('file:'));
    assert.ok(application, 'Normal application renderer must load');
    // Fault injection: a blocked product renderer must not block the native entry
    // or the recovery renderer's separate, non-persistent session partition.
    void application.webContents.executeJavaScript('while (true) {}').catch(() => {});
    menuItem.click(menuItem, undefined, {});
  }
  const window = await until(() => BrowserWindow.getAllWindows().find(win => win.webContents.getURL().startsWith('data:text/html')));
  await until(async () => window.webContents.executeJavaScript('document.getElementById("summary")?.textContent'));
  const page = await window.webContents.executeJavaScript('({ text: document.body.innerText, preview: document.getElementById("preview").textContent, retryDisabled: document.getElementById("retry").disabled, bridge: typeof window.wuu })');
  assert.equal(page.bridge, 'undefined', 'Recovery must not expose product IPC');
  assert.equal(JSON.parse(page.preview).mode, mode);
  assert.ok(!page.preview.includes('synthetic-secret'));
  if (mode === 'diagnostic') { assert.deepEqual(spawns, [], 'Diagnostic launch must not spawn the core or plugin helpers'); assert.equal(page.retryDisabled, true); }
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, mode + '.png'), (await window.webContents.capturePage()).toPNG());
  nativeTheme.themeSource = 'dark'; await new Promise(resolve => setTimeout(resolve, 100));
  fs.writeFileSync(path.join(output, mode + '-dark.png'), (await window.webContents.capturePage()).toPNG());
  nativeTheme.themeSource = 'light';
  window.setSize(390, 720); window.webContents.setZoomFactor(1.35);
  await new Promise(resolve => setTimeout(resolve, 150));
  const layout = await window.webContents.executeJavaScript('({width: innerWidth, scroll: document.documentElement.scrollWidth})');
  assert.ok(layout.scroll <= layout.width + 1, 'Recovery must not overflow horizontally');
  fs.writeFileSync(path.join(output, mode + '-narrow.png'), (await window.webContents.capturePage()).toPNG());
  await window.webContents.executeJavaScript('document.querySelector("details").open = true; document.getElementById("export").focus(); document.getElementById("export").scrollIntoView()');
  await new Promise(resolve => setTimeout(resolve, 100));
  fs.writeFileSync(path.join(output, mode + '-narrow-bottom.png'), (await window.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, mode + '.json'), JSON.stringify({ mode, spawns, errors, page, layout, limitation: 'Synthetic Linux headless Electron; not physical macOS acceptance.' }, null, 2));
  clearTimeout(timer); app.exit(0);
} catch (error) { fs.writeFileSync(path.join(output, mode + '-failure.json'), JSON.stringify({ error: String(error.stack), spawns, errors }, null, 2)); clearTimeout(timer); app.exit(1); }
});
`);
  const env = { ...process.env, HOME: fixture, XDG_CONFIG_HOME: path.join(fixture, 'config'), WUU_HOME: home, WUU_DESKTOP_CORE: process.env.WUU_DESKTOP_CORE || path.join(root, 'build/bin/wuu-core'), WUU_SAFE_MODE: mode === 'diagnostic' ? '1' : '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_ENABLE_BROWSER: '0' };
  delete env.ELECTRON_RUN_AS_NODE; delete env.ELECTRON_RENDERER_URL;
  const args = [...(process.platform === 'linux' ? ['--no-sandbox', '--ozone-platform=headless', '--ozone-override-screen-size=1440,1000'] : []), harness];
  const result = spawnSync(require('electron'), args, { env, encoding: 'utf8', timeout: 35000 });
  fs.writeFileSync(path.join(output, mode + '.log'), String(result.stdout || '') + String(result.stderr || ''));
  assert.equal(fs.readFileSync(path.join(home, 'config.json'), 'utf8'), broken);
  assert.equal(fs.readFileSync(path.join(home, 'preserve-sentinel'), 'utf8'), 'saved-state');
  if (result.error) throw result.error;
  assert.equal(result.status, 0, `${mode} recovery acceptance failed; see ${output}`);
}
console.log(JSON.stringify({ status: 'passed', output, modes: ['diagnostic', 'normal'], fixture: 'bad JSON; saved-state sentinel preserved' }));
