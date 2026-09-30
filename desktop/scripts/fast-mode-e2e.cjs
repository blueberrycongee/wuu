// Real Electron/main/preload/Go coverage with a disposable profile and HTTP
// provider. Build core and desktop first; WUU_FAST_CORE can override the binary.
// No account or paid inference is used.
// Evidence (wire requests, persisted state, cropped screenshots) is kept outside the temporary profile.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, BrowserWindow } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = process.env.WUU_FAST_OUTPUT || path.join(desktop, 'out/fast-mode-e2e');
fs.mkdirSync(output, { recursive: true });
const layoutEvidence = [];
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
async function key(win, keyCode) {
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode });
  if (keyCode === 'Space' || keyCode === 'Enter') win.webContents.sendInputEvent({ type: 'char', keyCode: keyCode === 'Space' ? ' ' : '\r' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode });
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
async function capturePanel(win, name) {
  await evaluate(win, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const state = await evaluate(win, () => {
    const panel = document.querySelector('.runtime-panel');
    const context = panel.querySelector('.runtime-panel-context');
    const source = panel.querySelector('.runtime-panel-context-source');
    const fast = panel.querySelector('.runtime-panel-fast');
    const reset = panel.querySelector('.runtime-panel-speed-reset');
    const model = panel.querySelector('.runtime-panel-model');
    const slider = panel.querySelector('.runtime-panel-effort');
    const rect = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height, left: r.left, right: r.right, top: r.top, bottom: r.bottom }; };
    const p = rect(panel);
    return {
      panel: p, context: rect(context), source: rect(source), fast: rect(fast), reset: rect(reset), model: rect(model), slider: slider ? rect(slider) : null,
      pressed: fast.getAttribute('aria-pressed'), resetDisabled: reset.disabled,
      bottomInset: parseFloat(getComputedStyle(panel.querySelector('.runtime-panel-summary')).paddingBottom) + parseFloat(getComputedStyle(panel).borderBottomWidth),
      fastColor: getComputedStyle(fast).color, fastBackground: getComputedStyle(fast).backgroundColor,
      font: getComputedStyle(panel).getPropertyValue('--font-ui').trim(), theme: document.documentElement.dataset.theme,
      viewport: { width: innerWidth, height: innerHeight },
      nestedButton: Boolean(fast.parentElement.closest('button')),
      focusedFast: document.activeElement === fast, focusModality: document.documentElement.dataset.focusModality,
      focusOutline: getComputedStyle(fast).outlineStyle,
      crop: { x: Math.max(0, Math.floor(p.x - 8)), y: Math.max(0, Math.floor(p.y - 8)), width: Math.min(innerWidth - Math.max(0, Math.floor(p.x - 8)), Math.ceil(p.width + 16)), height: Math.min(innerHeight - Math.max(0, Math.floor(p.y - 8)), Math.ceil(p.height + 16)) },
    };
  });
  state.zoomFactor = win.webContents.getZoomFactor();
  layoutEvidence.push({ name, ...state });
  fs.writeFileSync(path.join(output, 'layout-evidence.json'), JSON.stringify(layoutEvidence, null, 2));
  assert(!state.nestedButton, 'Speed must be independent of the model navigation button.');
  // Browser geometry can differ by a fractional CSS pixel at non-integer zoom.
  assert(state.fast.width >= 27.5 && state.fast.height >= 27.5, `The compact speed toggle retains a usable hit target: ${JSON.stringify(state.fast)}`);
  assert(state.fast.top >= state.context.top - 1 && state.fast.bottom <= state.context.bottom + 1, 'Fast mode shares the existing context header.');
  assert(state.source.right <= state.fast.left + 0.5 && state.fast.right <= state.reset.left + 0.5, 'Header labels, speed and reset must not overlap.');
  assert(state.fast.bottom <= state.model.top + 1, 'Speed controls must not cover the model row.');
  assert(state.panel.left >= 0 && state.panel.right <= state.viewport.width && state.panel.top >= 0 && state.panel.bottom <= state.viewport.height, 'The popover stays inside the window.');
  const last = state.slider || state.model;
  assert(last.bottom <= state.panel.bottom + 0.5 && Math.abs(state.panel.bottom - last.bottom - state.bottomInset) <= 1,
    'The summary ends at its normal inset without a leftover speed row.');
  // DOM rectangles use CSS pixels; Electron capture rectangles use DIP.
  const crop = Object.fromEntries(Object.entries(state.crop).map(([key, value]) => [key, Math.round(value * state.zoomFactor)]));
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage(crop)).toPNG());
  return state;
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
  process.env.WUU_TEST_CODEX_DEFAULT_TIER = 'fast';
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', engines,
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture', models: { fixture: { name: 'Fixture model with a deliberately long display name', fast_mode: true, variants: { low: { reasoningEffort: 'low' }, high: { reasoningEffort: 'high' } } } } } },
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
  main.focus(); main.webContents.focus();
  await evaluate(main, () => document.querySelector('.runtime-panel-model').focus());
  await key(main, 'Home');
  assert(await evaluate(main, () => document.activeElement === document.querySelector('.runtime-panel-fast')), 'Keyboard navigation reaches the speed control.');
  const beforeToggle = await capturePanel(main, 'provider-standard');
  await key(main, 'Space');
  await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.speed === 'fast', threadID);
  const afterToggle = await capturePanel(main, 'provider-fast-keyboard');
  assert.equal(afterToggle.pressed, 'true');
  assert.deepEqual(afterToggle.fast, beforeToggle.fast, 'Toggling speed must not move its target.');
  assert.notEqual(afterToggle.fastBackground, beforeToggle.fastBackground, 'Fast mode has a distinct enabled surface.');
  assert(await evaluate(main, () => document.activeElement === document.querySelector('.runtime-panel-fast') && getComputedStyle(document.activeElement).outlineStyle !== 'none'), 'Keyboard activation retains a visible focus ring.');
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
  await capturePanel(main, 'light-wide-fast');
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
  await capturePanel(main, 'dark-narrow-default');
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
    const result = await window.wuu.startThread({ engine: 'codex', model: 'gpt-6-astra', effort: 'high' });
    await window.wuu.renameThread(result.thread.id, 'Codex speed fixture');
    return result.thread.id;
  });
  await evaluate(main, () => { for (const group of document.querySelectorAll('.project-group')) group.querySelector('button[aria-expanded="false"]')?.click(); });
  await waitFor(main, () => [...document.querySelectorAll('.thread-row')].some(row => row.textContent.includes('Codex speed fixture')));
  await evaluate(main, () => [...document.querySelectorAll('.thread-row')].find(row => row.textContent.includes('Codex speed fixture')).querySelector('.thread-row-main').click());
  await waitFor(main, () => document.querySelector('.codex-runtime-trigger')?.textContent.includes('GPT-6 Astra'));
  await openPanel();
  assert.equal(await evaluate(main, () => document.querySelector('button[aria-label="Fast mode"]').getAttribute('aria-pressed')), 'true');
  await evaluate(main, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
  await capturePanel(main, 'codex-inherited-fast');
  for (const [speed, tier, slash] of [['standard', 'default'], ['', 'fast'], ['standard', 'default', true], ['fast', 'fast']]) {
    await openPanel();
    if (slash) {
      await evaluate(main, () => {
        document.querySelector('.codex-runtime-trigger').click();
        const textarea = document.querySelector('.composer textarea');
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(textarea, '/fast');
        textarea.dispatchEvent(new Event('input', { bubbles: true }));
      });
      await waitFor(main, () => Boolean(document.querySelector('.slash-command-item[data-command-name="fast"]')));
      await evaluate(main, () => document.querySelector('.composer textarea').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })));
    } else {
      await evaluate(main, speed => document.querySelector(speed ? 'button[aria-label="Fast mode"]' : '.runtime-panel-speed-reset').click(), speed);
    }
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
      await capturePanel(main, 'codex-fast');
    }
  }
  for (const theme of ['light', 'dark']) for (const font of [14, 20]) for (const width of [1280, 760]) {
    await evaluate(main, () => { if (document.querySelector('.runtime-panel')) document.querySelector('.codex-runtime-trigger').click(); });
    main.setContentSize(width, 820);
    await evaluate(main, ({ theme, font }) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty('--conversation-message-font-size', `${font}px`);
    }, { theme, font });
    await openPanel();
    await capturePanel(main, `codex-${theme}-${font}-${width}`);
  }
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ threadID, nativeThreadID: native, requests, persisted, layoutEvidence }, null, 2));
  console.log('PASS: fast / standard / inherited speed reached the provider and native Codex; model and effort preserved. Artifacts:', output);
  clearTimeout(timeout); server.close(); app.quit();
}
run().catch(async error => {
  console.error(error, 'FIXTURE', fixture);
  fs.writeFileSync(path.join(output, 'failure.txt'), String(error.stack));
  if (main && !main.isDestroyed()) fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
  server.close(); app.exit(1);
});
