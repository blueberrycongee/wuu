// Real Electron/main/preload/Go coverage with a disposable profile and HTTP
// provider. Build core and desktop first; WUU_FAST_CORE can override the binary.
// No account or paid inference is used.
// Artifacts (wire requests, persisted state, screenshots) are kept in FIXTURE.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, BrowserWindow } = require('electron');
const desktop = path.resolve(__dirname, '..');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-fast-mode-'));
const home = path.join(fixture, 'home');
const project = path.join(fixture, 'project');
fs.mkdirSync(home); fs.mkdirSync(project);
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = process.env.WUU_FAST_CORE || path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
const requests = [];
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    const input = JSON.parse(body);
    requests.push({ path: req.url, body: input });
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = delta => `data: ${JSON.stringify({ id: 'fixture-response', model: 'fixture', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`;
    res.write(chunk({ role: 'assistant', content: 'Fast mode fixture completed.' }));
    res.end(`data: ${JSON.stringify({ id: 'fixture-response', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 6, total_tokens: 16 } })}\n\ndata: [DONE]\n\n`);
  });
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn, arg, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await evaluate(win, fn, arg)) return;
    await delay(50);
  }
  throw new Error(`Timed out: ${fn}`);
}
let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 120000);
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  const codexBinary = path.join(fixture, 'fake-codex');
  const compile = spawnSync('go', ['build', '-o', codexBinary, './internal/codexengine/testdata/fakecodex'], { cwd: path.dirname(desktop), encoding: 'utf8' });
  assert.equal(compile.status, 0, compile.stderr);
  engines.codex = { enabled: true, binary_path: codexBinary };
  process.env.WUU_TEST_CODEX_REQUESTS = path.join(fixture, 'codex-requests.jsonl');
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', engines,
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture', models: { fixture: { fast_mode: true, variants: { low: { reasoningEffort: 'low' }, high: { reasoningEffort: 'high' } } } } } },
  }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Fast mode fixture', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', project], { env: process.env, input: '{"id":1,"method":"thread/start","params":{"engine":"wuu","provider":"fixture","model":"fixture","effort":"high","speed":"standard"}}\n', encoding: 'utf8', timeout: 30000 });
  assert.equal(boot.status, 0, boot.stderr);
  const response = boot.stdout.split('\n').filter(Boolean).map(line => JSON.parse(line)).find(item => item.id === 1);
  assert.ok(response?.result?.thread, boot.stdout);
  const threadID = response.result.thread.id;
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!main) await delay(25);
  console.log('FIXTURE', fixture);
  main.setSize(1380, 860);
  await waitFor(main, () => document.querySelector('.composer textarea'));
  await evaluate(main, async id => { await window.wuu.resumeThread(id); }, threadID);
  await waitFor(main, () => document.querySelector('.codex-runtime-trigger'));
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  await waitFor(main, () => document.querySelector('button[aria-label="Fast mode"]'));
  assert.equal(await evaluate(main, () => document.querySelector('button[aria-label="Fast mode"]').getAttribute('aria-pressed')), 'false');
  await evaluate(main, () => document.querySelector('button[aria-label="Fast mode"]').click());
  await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.speed === 'fast', threadID);
  async function turn(marker, expectedTier) {
    await evaluate(main, async ({ id, marker }) => {
      window.__fastModeCompleted = false;
      const unsubscribe = window.wuu.onServerEvent(event => {
        if (event.kind === 'notification' && event.message.method === 'turn/completed') { window.__fastModeCompleted = true; unsubscribe(); }
      });
      await window.wuu.startTurn(id, marker);
    }, { id: threadID, marker });
    await waitFor(main, () => window.__fastModeCompleted);
    const request = requests.find(item => JSON.stringify(item.body.messages).includes(marker));
    assert.ok(request, `missing provider request ${marker}`);
    assert.equal(request.body.service_tier, expectedTier);
    assert.equal(request.body.model, 'fixture');
    assert.equal(request.body.reasoning_effort, 'high');
  }
  await turn('fast-mode-fast', 'priority');
  async function openPanel() {
    if (!await evaluate(main, () => Boolean(document.querySelector('button[aria-label="Fast mode"]')))) await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
    await waitFor(main, () => document.querySelector('button[aria-label="Fast mode"]:not(:disabled)'));
  }
  await openPanel();
  await evaluate(main, () => document.querySelector('button[aria-label="Fast mode"]').focus());
  await evaluate(main, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
  fs.writeFileSync(path.join(fixture, 'light-wide-fast.png'), (await main.webContents.capturePage()).toPNG());
  await evaluate(main, () => document.querySelector('button[aria-label="Fast mode"]').click());
  await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.speed === 'standard', threadID);
  await turn('fast-mode-standard', 'default');
  await openPanel();
  await evaluate(main, () => document.querySelector('.runtime-panel-speed-reset').click());
  await waitFor(main, async id => !(await window.wuu.resumeThread(id)).thread.speed, threadID);
  await turn('fast-mode-inherit', undefined);
  await openPanel();
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  main.setSize(820, 860);
  await evaluate(main, () => {
    document.documentElement.setAttribute('data-theme', 'dark');
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
  });
  await openPanel();
  await evaluate(main, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
  await evaluate(main, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  fs.writeFileSync(path.join(fixture, 'dark-narrow-default.png'), (await main.webContents.capturePage()).toPNG());
  const persisted = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, threadID);
  assert.equal(persisted.model_variant, 'high');
  assert.ok(!persisted.speed);
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  main.setSize(1380, 860);
  await evaluate(main, () => {
    document.documentElement.setAttribute('data-theme', 'light');
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });
  const native = await evaluate(main, async () => {
    const result = await window.wuu.startThread({ engine: 'codex', model: 'gpt-6-astra', effort: 'high', speed: 'standard' });
    await window.wuu.renameThread(result.thread.id, 'Codex speed fixture');
    return result.thread.id;
  });
  await evaluate(main, () => { for (const group of document.querySelectorAll('.project-group')) group.querySelector('button[aria-expanded="false"]')?.click(); });
  await waitFor(main, () => [...document.querySelectorAll('.thread-row')].some(row => row.textContent.includes('Codex speed fixture')));
  await evaluate(main, () => [...document.querySelectorAll('.thread-row')].find(row => row.textContent.includes('Codex speed fixture')).querySelector('.thread-row-main').click());
  await waitFor(main, () => document.querySelector('.codex-runtime-trigger')?.textContent.includes('GPT-6 Astra'));
  for (const [speed, tier] of [['fast', 'fast'], ['standard', 'default'], ['', 'default']]) {
    await openPanel();
    await evaluate(main, speed => document.querySelector(speed ? 'button[aria-label="Fast mode"]' : '.runtime-panel-speed-reset').click(), speed);
    await waitFor(main, async ({ id, speed }) => ((await window.wuu.resumeThread(id)).thread.speed || '') === speed, { id: native, speed });
    await evaluate(main, async id => {
      window.__fastModeCompleted = false;
      const unsubscribe = window.wuu.onServerEvent(event => {
        if (event.kind === 'notification' && event.message.method === 'turn/completed') { window.__fastModeCompleted = true; unsubscribe(); }
      });
      await window.wuu.startTurn(id, 'hello');
    }, native);
    await waitFor(main, () => window.__fastModeCompleted);
    const log = fs.readFileSync(process.env.WUU_TEST_CODEX_REQUESTS, 'utf8').trim().split('\n').map(line => JSON.parse(line));
    const last = log.filter(request => request.method === 'turn/start').at(-1);
    assert.equal(last.params.serviceTier, tier);
    assert.equal(last.params.reasoningEffort, 'high');
    if (speed === 'fast') {
      await openPanel();
      await evaluate(main, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
      const visual = await evaluate(main, () => { const button = document.querySelector('button[aria-label="Fast mode"]'); return { pressed: button.getAttribute('aria-pressed'), disabled: button.disabled, color: getComputedStyle(button.querySelector('svg')).color, accent: getComputedStyle(button).getPropertyValue('--interaction-accent') }; });
      console.log('CODEX_VISUAL', JSON.stringify(visual));
      assert.equal(visual.pressed, 'true');
      fs.writeFileSync(path.join(fixture, 'codex-fast.png'), (await main.webContents.capturePage()).toPNG());
    }
  }
  fs.writeFileSync(path.join(fixture, 'results.json'), JSON.stringify({ threadID, nativeThreadID: native, requests, persisted }, null, 2));
  console.log('PASS: fast / standard / inherited speed reached the provider and native Codex; model and effort preserved. Artifacts:', fixture);
  clearTimeout(timeout); server.close(); app.quit();
}
run().catch(error => { console.error(error, 'FIXTURE', fixture); server.close(); app.exit(1); });
