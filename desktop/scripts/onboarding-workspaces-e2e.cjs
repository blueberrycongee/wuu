// Runs the real desktop, preload and core against disposable local histories.
// Build the desktop/core first, then run: electron scripts/onboarding-workspaces-e2e.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { app, dialog } = require('electron');
const desktop = path.resolve(__dirname, '..');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-workspaces-e2e-'));
const home = path.join(fixture, 'wuu-home');
const output = path.resolve(process.env.WUU_ONBOARDING_OUTPUT || path.join(desktop, 'out/e2e/onboarding-workspaces'));
const empty = process.env.WUU_ONBOARDING_EMPTY === '1';
function write(file, data) { fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, data); }
function project(name, git = true) {
  const directory = path.join(fixture, 'projects', name);
  fs.mkdirSync(git ? path.join(directory, '.git') : directory, { recursive: true });
  return fs.realpathSync(directory);
}
const first = project('website');
const second = project('api-server');
const old = project('older-workspace');
const manual = project('manually-selected');
const linked = project('linked-worktree', false);
write(path.join(linked, '.git'), 'gitdir: /repo/.git/worktrees/linked');
const codex = path.join(fixture, 'codex');
const claude = path.join(fixture, 'claude');
function history(source, id, cwd, age = 0) {
  if (empty) return;
  const file = source === 'codex' ? path.join(codex, 'sessions', '2026', '10', '05', `${id}.jsonl`) : path.join(claude, 'projects', 'sample', `${id}.jsonl`);
  write(file, JSON.stringify(source === 'codex' ? { type: 'session_meta', payload: { cwd } } : { type: 'user', cwd }) + '\n');
  const date = new Date(Date.now() - age * 86400000);
  fs.utimesSync(file, date, date);
}
for (let i = 0; i < 3; i++) { history('codex', `first-${i}`, first); history('claude', `second-${i}`, second, 1); }
history('claude', 'first-also', first);
history('codex', 'old', old, 60);
history('codex', 'missing', path.join(fixture, 'missing'));
history('codex', 'linked', linked);
for (let i = 0; i < 18; i++) history('codex', `extra-${i}`, project(`project-${i}-with-a-long-but-readable-directory-name`), 60);
write(path.join(home, 'desktop-settings.json'), JSON.stringify({ language: empty ? 'zh-CN' : 'en-US', theme: 'dark' }));
write(path.join(home, 'config.json'), JSON.stringify({
  default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', model: 'fixture', base_url: 'http://127.0.0.1:1', api_key: 'synthetic-test-key' } },
  engines: Object.fromEntries(['codex','claude','cursor','devin','grok','hermes','pi','opencode','antigravity'].map(id => [id, { enabled: false }])),
}));
app.setPath('userData', path.join(fixture, 'electron'));
process.env.WUU_HOME = home;
process.env.CODEX_HOME = codex;
process.env.CLAUDE_CONFIG_DIR = claude;
process.env.WUU_DESKTOP_CORE = path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
fs.mkdirSync(output, { recursive: true });
let main;
const observed = [];
const deadline = setTimeout(() => {
  fs.writeFileSync(path.join(output, 'failure.txt'), 'Onboarding E2E exceeded 60 seconds');
  app.exit(1);
}, 60000);
app.on('browser-window-created', (_event, win) => { if (!main) main = win; });
async function evaluate(fn, arg) { return main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`); }
async function waitFor(fn, arg) {
  return main.webContents.executeJavaScript(`new Promise((resolve, reject) => {
    const check = (${fn.toString()});
    const deadline = performance.now() + 20000;
    function tick() {
      try { if (check(${JSON.stringify(arg)})) return resolve(true); } catch (error) { return reject(error); }
      if (performance.now() > deadline) return reject(new Error('Timed out waiting for onboarding state'));
      requestAnimationFrame(tick);
    }
    tick();
  })`);
}
async function click(label) {
  await waitFor((text) => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === text && !button.disabled), label);
  const position = await evaluate((text) => {
    const element = [...document.querySelectorAll('button')].find((button) => button.textContent.trim() === text && !button.disabled);
    if (!element) throw new Error(`Missing enabled button: ${text}`);
    const box = element.getBoundingClientRect();
    return { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) };
  }, label);
  main.webContents.sendInputEvent({ type: 'mouseDown', ...position, button: 'left', clickCount: 1 });
  main.webContents.sendInputEvent({ type: 'mouseUp', ...position, button: 'left', clickCount: 1 });
}
async function screenshot(name) {
  console.log("CAPTURE", name);
  await evaluate(async () => {
    await document.fonts.ready;
    await Promise.all((document.querySelector('.first-run-onboarding')?.getAnimations({ subtree: true }) ?? []).filter(animation => animation.playState === 'running' && animation.effect?.getComputedTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  fs.writeFileSync(path.join(output, `${name}.png`), (await main.webContents.capturePage()).toPNG());
}
async function run() {
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await app.whenReady();
  if (!main) await new Promise(resolve => app.once('browser-window-created', resolve));
  if (main.webContents.isLoading()) await new Promise(resolve => main.webContents.once('did-finish-load', resolve));
  await waitFor(() => document.querySelector('.onboarding-stage-welcome'));
  await click(empty ? '开始设置' : 'Set up Wuu');
  await waitFor(() => document.querySelector('.onboarding-stage-plugins button:not(:disabled)'));
  await click(empty ? '极简' : 'Minimal');
  await click(empty ? '继续' : 'Continue');
  await waitFor(() => document.querySelector('.onboarding-stage-runtime'));
  await click(empty ? '继续' : 'Continue');
  await waitFor(() => document.querySelector('.onboarding-stage-provider'));
  await click(empty ? '继续' : 'Continue');
  if (empty) {
    await waitFor(() => document.querySelector('.onboarding-stage-ready') && [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '重新查找' && !button.disabled));
    assert.equal(await evaluate(() => document.querySelectorAll('.onboarding-workspace-row').length), 0);
    await screenshot('empty-zh');
    await click('开始使用 Wuu');
    await waitFor(() => !document.querySelector('.first-run-onboarding') && document.querySelector('.app-shell'));
    assert.equal(JSON.parse(fs.readFileSync(path.join(home, 'projects.json'))).projects.length, 0);
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed: true, synthetic: true, scenario: 'empty history and skip', fixture }, null, 2));
    clearTimeout(deadline);
    app.quit();
    return;
  }
  await waitFor(() => document.querySelectorAll('.onboarding-workspace-row').length === 21);
  const scan = await evaluate(() => [...document.querySelectorAll('.onboarding-workspace-row')].map(row => ({ name: row.querySelector('.onboarding-workspace-name').textContent, selected: row.querySelector('input').checked })));
  assert.deepEqual(scan.filter(row => row.selected).map(row => row.name).sort(), ['api-server', 'website']);
  observed.push({ scenario: 'real history discovery', candidates: scan.length, selected: scan.filter(row => row.selected).length });
  await screenshot('wide');
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Tab' });
  main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Tab' });
  await screenshot('keyboard-focus');
  main.setResizable(true); main.setSize(620, 600);
  await main.webContents.executeJavaScript("document.documentElement.style.setProperty('--conversation-message-font-size', '20px')");
  await screenshot('narrow-large-font');
  const geometry = await evaluate(() => ({ fontSize: getComputedStyle(document.querySelector('.onboarding-workspace-name')).fontSize, width: innerWidth, page: document.documentElement.scrollWidth, actions: document.querySelector('.onboarding-actions').getBoundingClientRect().toJSON(), body: document.querySelector('.onboarding-workspaces').getBoundingClientRect().toJSON() }));
  assert.equal(geometry.fontSize, '20px');
  assert.ok(geometry.page <= geometry.width);
  assert.ok(geometry.body.bottom <= geometry.actions.top);
  observed.push({ scenario: 'narrow large font', geometry });
  main.setSize(920, 720);
  await main.webContents.executeJavaScript("document.documentElement.style.setProperty('--conversation-message-font-size', '14px')");
  const originalDialog = dialog.showOpenDialog;
  dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [manual] });
  await click('Choose a folder');
  await waitFor(() => document.querySelectorAll('.onboarding-workspace-row input:checked').length === 3);
  dialog.showOpenDialog = originalDialog;
  await screenshot('manual-folder');
  fs.rmSync(manual, { recursive: true });
  await click('Import and start (3)');
  await waitFor(() => document.querySelector('.onboarding-error'));
  assert.ok(!(await evaluate(() => document.querySelector('.onboarding-error').textContent)).includes('wuu:onboarding-import-workspaces'));
  assert.equal(JSON.parse(fs.readFileSync(path.join(home, 'projects.json'))).projects.length, 0);
  await screenshot('retry');
  fs.mkdirSync(manual);
  await click('Import and start (3)');
  await waitFor(() => !document.querySelector('.first-run-onboarding') && document.querySelector('.app-shell'));
  const persisted = JSON.parse(fs.readFileSync(path.join(home, 'projects.json')));
  assert.equal(persisted.projects.length, 3);
  assert.equal(persisted.active_context.cwd, first);
  const listed = await main.webContents.executeJavaScript('window.wuu.listProjects()');
  assert.equal(listed.active_context.cwd, first);
  observed.push({ scenario: 'atomic retry and landing', workspaceCount: persisted.projects.length, selectedWorkspace: 'website' });
  await screenshot('landed');
  main.webContents.reload();
  await new Promise(resolve => main.webContents.once('did-finish-load', resolve));
  await waitFor(() => document.querySelector('.app-shell') && !document.querySelector('.first-run-onboarding'));
  observed.push({ scenario: 'completion survives reload', passed: true });
  for (const file of ['failure.txt', 'failure.png']) fs.rmSync(path.join(output, file), { force: true });
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed: true, synthetic: true, fixture, observed }, null, 2));
  console.log(JSON.stringify({ passed: true, output, fixture }));
  clearTimeout(deadline);
  app.quit();
}
run().catch(async (error) => {
  console.error(error, 'fixture:', fixture);
  if (main && !main.isDestroyed()) {
    await screenshot('failure').catch(() => {});
    const state = await evaluate(() => document.body.innerText).catch(() => 'unavailable');
    fs.writeFileSync(path.join(output, 'failure.txt'), String(error.stack) + '\n' + state);
  }
  app.exit(1);
});
