// Build the desktop/core and fakecodex first (see the development guide).
// Run with Electron, or through macOS LaunchServices, using a system-only PATH.
// Uses the real renderer, preload, main process, core and native Codex protocol.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { createHash } = require('node:crypto');
const os = require('node:os');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');

const desktop = path.resolve(__dirname, '..');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-engine-discovery-'));
const userHome = path.join(fixture, 'user');
const wuuData = path.join(fixture, 'wuu');
const project = path.join(fixture, 'project');
const output = path.resolve(process.env.WUU_ENGINE_DISCOVERY_OUTPUT || path.join(desktop, 'out/e2e/engine-discovery'));
const core = process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core');
const fakeCodex = path.join(desktop, 'build/bin/fakecodex');
const launchPath = process.env.PATH;
const checks = [];
let main;
function write(file, data, mode) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, data, { mode });
}
for (const directory of [userHome, project, output]) fs.mkdirSync(directory, { recursive: true });
write(path.join(wuuData, 'desktop-settings.json'), JSON.stringify({ language: 'en-US', theme: 'dark', onboarding_version: 1 }));
write(path.join(wuuData, 'projects.json'), JSON.stringify({
  projects: [{ id: 'fixture', name: 'Engine discovery', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }],
  active_context: { kind: 'project', project_id: 'fixture', cwd: project },
}));
write(path.join(wuuData, 'config.json'), JSON.stringify({
  default_provider: 'fixture',
  providers: { fixture: { type: 'openai-compatible', model: 'fixture', base_url: 'http://127.0.0.1:1', api_key: 'synthetic-test-key' } },
  engines: Object.fromEntries(['claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
}));
// The shell probe must only read the disposable user's configuration.
write(path.join(userHome, '.zshrc'), 'export PATH="$HOME/custom-bin:$PATH"\n');
write(path.join(userHome, '.bash_profile'), 'export PATH="$HOME/custom-bin:$PATH"\n');
for (const name of ['claude', 'cursor-agent', 'opencode']) {
  write(path.join(userHome, '.local/bin', name), '#!/bin/sh\nexit 0\n', 0o755);
}
app.setPath('userData', path.join(fixture, 'electron'));
Object.assign(process.env, {
  HOME: userHome, WUU_HOME: wuuData, CODEX_HOME: path.join(userHome, '.codex'),
  CLAUDE_CONFIG_DIR: path.join(userHome, '.claude'),
  SHELL: process.platform === 'darwin' ? '/bin/zsh' : '/bin/bash',
  WUU_DESKTOP_CORE: core, WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0',
  WUU_SAFE_MODE: '1', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1',
});
for (const name of Object.keys(process.env)) {
  if (/^WUU_[A-Z0-9_]+_BINARY$/.test(name) || ['ELECTRON_RENDERER_URL', 'NVM_DIR', 'MISE_DATA_DIR', 'XDG_DATA_HOME', 'ZDOTDIR'].includes(name)) delete process.env[name];
}
app.on('browser-window-created', (_event, win) => { if (!main) main = win; });
const deadline = setTimeout(() => finish(new Error('Engine discovery exceeded 90 seconds')), 90000);
async function evaluate(fn, arg) { return main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`); }
async function waitFor(fn, arg) {
  return main.webContents.executeJavaScript(`new Promise((resolve, reject) => {
    const check = (${fn.toString()});
    const deadline = performance.now() + 30000;
    function tick() {
      try { if (check(${JSON.stringify(arg)})) return resolve(true); } catch (error) { return reject(error); }
      if (performance.now() > deadline) return reject(new Error('Timed out: ' + check.toString()));
      requestAnimationFrame(tick);
    }
    tick();
  })`);
}
async function click(selector) {
  await waitFor(selector => document.querySelector(selector) && !document.querySelector(selector).disabled, selector);
  await evaluate(selector => document.querySelector(selector).click(), selector);
}
function record(name, data) { checks.push({ name, ...data }); }
async function refresh() {
  await click('[data-testid="settings-engine-refresh"]');
  await waitFor(() => document.querySelector('[data-testid="settings-engine-refresh"]')?.getAttribute('aria-busy') === 'false');
  return evaluate(() => window.wuu.listEngines());
}
function codex(inventory) { return inventory.engines.find(engine => engine.id === 'codex'); }
async function run() {
  assert.equal(launchPath, '/usr/local/bin:/usr/bin:/bin', 'Launch with the Finder/Dock-style PATH');
  assert(fs.existsSync(core) && fs.existsSync(fakeCodex), 'Build core and fakecodex first');
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await app.whenReady();
  if (!main) await new Promise(resolve => app.once('browser-window-created', resolve));
  if (main.webContents.isLoading()) await new Promise(resolve => main.webContents.once('did-finish-load', resolve));
  await waitFor(() => document.querySelector('.sidebar-account-trigger'));
  await click('.sidebar-account-trigger');
  await click('[data-settings-page="providers"]');
  await waitFor(() => [...document.querySelectorAll('.settings-nav button')].some(button => button.textContent.trim() === 'Agents'));
  await evaluate(() => [...document.querySelectorAll('.settings-nav button')].find(button => button.textContent.trim() === 'Agents').click());
  await waitFor(() => document.querySelector('[data-testid="settings-engine-codex-status"]'));
  const initial = await evaluate(() => window.wuu.listEngines());
  assert.equal(codex(initial).binary_ok, false, 'Host must not have a system-wide Codex installation');
  assert.equal(codex(initial).enabled, false);
  for (const id of ['claude', 'cursor', 'opencode']) {
    const engine = initial.engines.find(engine => engine.id === id);
    assert.equal(engine.binary_ok, true, id + ' detected outside PATH');
    assert.equal(engine.enabled, false, id + ' preserves explicit disable');
  }
  record('system PATH startup detects standard user installations', { engines: initial.engines });

  const installed = path.join(userHome, '.local/bin/codex');
  fs.copyFileSync(fakeCodex, installed);
  fs.chmodSync(installed, 0o755);
  const refreshed = await refresh();
  assert.equal(codex(refreshed).binary_path, installed);
  assert.equal(codex(refreshed).enabled, true, 'Refresh must register the newly installed engine');
  assert(codex(refreshed).models.some(model => model.id === 'gpt-6-astra'));
  assert.equal(refreshed.engines.find(engine => engine.id === 'claude').enabled, false);
  await waitFor(() => document.querySelector('[data-testid="settings-engine-codex-enabled"]')?.getAttribute('aria-checked') === 'true');
  record('settings refresh bypasses cached absence and registers Codex', { engine: codex(refreshed) });

  const started = await evaluate(() => window.wuu.startThread({ engine: 'codex', model: 'gpt-6-astra', permission_mode: 'standard' }));
  assert(started.thread.id);
  await evaluate(() => { window.__discoveryEvents = []; window.wuu.onServerEvent(event => window.__discoveryEvents.push(event)); });
  const turn = await evaluate(id => window.wuu.startTurn(id, 'engine discovery smoke'), started.thread.id);
  await waitFor(id => window.__discoveryEvents.some(event => event.message?.method === 'turn/completed' && event.message.params?.turn?.id === id), turn.turn.id);
  record('newly discovered engine completes a native protocol turn', { thread: started.thread.id, turn: turn.turn.id });

  // This directory did not exist during the initial, cached shell probe.
  const moved = path.join(userHome, 'custom-bin/codex');
  fs.mkdirSync(path.dirname(moved), { recursive: true });
  fs.renameSync(installed, moved);
  const relocated = await refresh();
  assert.equal(codex(relocated).binary_path, moved);
  assert.equal(codex(relocated).enabled, true);
  record('refresh rereads login-shell locations added after startup', { engine: codex(relocated) });

  await evaluate(() => window.wuu.updateEngines({ codex: { enabled: false } }));
  const disabled = await refresh();
  assert.equal(codex(disabled).binary_ok, true);
  assert.equal(codex(disabled).enabled, false);
  record('refresh preserves explicit disable', { engine: codex(disabled) });
  finish();
}
function finish(error) {
  clearTimeout(deadline);
  const coreSHA256 = fs.existsSync(core) ? createHash('sha256').update(fs.readFileSync(core)).digest('hex') : undefined;
  fs.writeFileSync(path.join(output, 'result.json'), JSON.stringify({ status: error ? 'failed' : 'passed', launchPath, core, coreSHA256, checks, error: error?.stack }, null, 2));
  if (error) console.error(error); else console.log(`PASS: engine discovery. Evidence: ${output}`);
  app.once('will-quit', () => app.exit(error ? 1 : 0));
  app.quit();
  setTimeout(() => app.exit(error ? 1 : 0), 5000).unref();
}
app.on('quit', () => fs.rmSync(fixture, { recursive: true, force: true }));
run().catch(finish);
