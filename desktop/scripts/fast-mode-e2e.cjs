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
let endpointCase;
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-fast-mode-'));
const home = path.join(fixture, 'home');
const project = path.join(fixture, 'project');
fs.mkdirSync(home); fs.mkdirSync(project);
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = process.env.WUU_FAST_CORE || path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '0';
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
// An asynchronous runtime reply can cancel and replace a transition after
// getAnimations() was read. Recheck each frame instead of awaiting one snapshot.
async function settlePanel(win) {
  await evaluate(win, () => new Promise((resolve, reject) => {
    let frame;
    let settledFrames = 0;
    const timeout = setTimeout(() => {
      cancelAnimationFrame(frame);
      reject(new Error('Runtime panel animations did not settle'));
    }, 30000);
    const check = () => {
      const panel = document.querySelector('.runtime-panel');
      const active = panel?.getAnimations({ subtree: true }).some(animation => animation.pending || animation.playState === 'running');
      settledFrames = panel && !active ? settledFrames + 1 : 0;
      if (settledFrames >= 2) {
        clearTimeout(timeout);
        return resolve();
      }
      frame = requestAnimationFrame(check);
    };
    frame = requestAnimationFrame(check);
  }));
}
// Selector helpers for the flat picker: engine rail icons, flat model rows,
// effort radio rows, and the segmented speed footer.
async function capturePanel(win, name, fastSupported = true) {
  await settlePanel(win);
  const state = await evaluate(win, () => {
    const panel = document.querySelector('.runtime-panel');
    const rail = panel.querySelector('.runtime-engine-rail');
    const fast = panel.querySelector('.runtime-speed-icon')?.closest('button');
    const footer = panel.querySelector('.runtime-flat-footer');
    const content = footer ?? panel.querySelector('.runtime-flat');
    const model = panel.querySelector('.codex-model-item[aria-checked="true"]');
    const column = panel.querySelector('.runtime-effort-column');
    const efforts = [...panel.querySelectorAll('.runtime-effort-option')];
    const effort = panel.querySelector('.runtime-effort-option[aria-checked="true"]');
    const rect = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height, left: r.left, right: r.right, top: r.top, bottom: r.bottom }; };
    const p = rect(panel);
    return {
      panel: p, rail: rail ? rect(rail) : null, fast: fast ? rect(fast) : null, footer: footer ? rect(footer) : null,
      content: rect(content), model: model ? rect(model) : null, column: column ? rect(column) : null,
      endpoint: effort ? (efforts.indexOf(effort) === 0 ? 'min' : efforts.indexOf(effort) === efforts.length - 1 ? 'max' : 'middle') : null,
      effort: effort ? rect(effort) : null,
      effortText: effort?.querySelector('.runtime-effort-option-name')?.textContent ?? null,
      triggerLabel: document.querySelector('.codex-runtime-trigger')?.getAttribute('aria-label') ?? '',
      modelOutlineOffset: model ? getComputedStyle(model).outlineOffset : null,
      effortOutlineOffset: effort ? getComputedStyle(effort).outlineOffset : null,
      pressed: fast?.getAttribute('aria-pressed') ?? null,
      bottomInset: parseFloat(getComputedStyle(panel).paddingBottom) + parseFloat(getComputedStyle(panel).borderBottomWidth),
      fastColor: fast ? getComputedStyle(fast).color : null, fastBackground: fast ? getComputedStyle(fast).backgroundColor : null,
      font: getComputedStyle(panel).getPropertyValue('--font-ui').trim(), theme: document.documentElement.dataset.theme,
      viewport: { width: innerWidth, height: innerHeight },
      nestedButton: Boolean(fast?.parentElement.closest('button')),
      focusedFast: document.activeElement === fast, focusModality: document.documentElement.dataset.focusModality,
      focusOutline: fast ? getComputedStyle(fast).outlineStyle : null,
      crop: { x: Math.max(0, Math.floor(p.x - 8)), y: Math.max(0, Math.floor(p.y - 8)), width: Math.min(innerWidth - Math.max(0, Math.floor(p.x - 8)), Math.ceil(p.width + 16)), height: Math.min(innerHeight - Math.max(0, Math.floor(p.y - 8)), Math.ceil(p.height + 16)) },
    };
  });
  state.zoomFactor = win.webContents.getZoomFactor();
  state.focusEmulated = true;
  layoutEvidence.push({ name, ...state });
  fs.writeFileSync(path.join(output, 'layout-evidence.json'), JSON.stringify(layoutEvidence, null, 2));
  assert.equal(Boolean(state.fast), fastSupported, 'Only supported models show speed controls.');
  assert(!state.nestedButton, 'Speed must be independent of the model rows.');
  if (state.fast) {
    // Browser geometry can differ by a fractional CSS pixel at non-integer zoom.
    assert(state.fast.width >= 27.5 && state.fast.height >= 27.5, `The segmented speed option retains a usable hit target: ${JSON.stringify(state.fast)}`);
    assert(state.footer && state.fast.top >= state.footer.top - 1 && state.fast.bottom <= state.footer.bottom + 1, 'Speed lives in the panel footer.');
    assert(state.model && state.fast.top >= state.model.bottom - 1, 'Speed controls must not cover the model rows.');
  }
  if (state.effortText) assert(state.triggerLabel.includes(state.effortText), 'The checked effort row matches the trigger label.');
  for (const control of [state.rail, state.effort, state.model, state.column].filter(Boolean)) {
    assert(control.left >= state.panel.left && control.right <= state.panel.right
      && control.top >= state.panel.top && control.bottom <= state.panel.bottom, 'Panel content stays inside the popover.');
  }
  assert(Number.parseFloat(state.modelOutlineOffset ?? '0') <= 0 && Number.parseFloat(state.effortOutlineOffset ?? '0') <= 0,
    'Focus rings must not move the visible edges outside the alignment guides.');
  assert(state.panel.left >= 0 && state.panel.right <= state.viewport.width && state.panel.top >= 0 && state.panel.bottom <= state.viewport.height, 'The popover stays inside the window.');
  assert(state.content.bottom <= state.panel.bottom + 0.5 && Math.abs(state.panel.bottom - state.content.bottom - state.bottomInset) <= 1,
    'The panel ends at its normal inset.');
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
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  const codexBinary = path.join(fixture, 'fake-codex');
  const compile = spawnSync('go', ['build', '-o', codexBinary, './internal/codexengine/testdata/fakecodex'], { cwd: path.dirname(desktop), encoding: 'utf8' });
  assert.equal(compile.status, 0, compile.stderr);
  engines.codex = { enabled: true, binary_path: codexBinary };
  process.env.WUU_TEST_CODEX_REQUESTS = path.join(fixture, 'codex-requests.jsonl');
  process.env.WUU_TEST_CODEX_DEFAULT_TIER = 'fast';
  // Stall the first Codex model catalog until the test opens the gate.
  process.env.WUU_TEST_CODEX_MODEL_LIST_GATE = path.join(fixture, 'codex-model-list.gate');
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', engines,
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture', models: { fixture: { name: 'Fixture model with a deliberately long display name', fast_mode: true, variants: { low: { reasoningEffort: 'low' }, high: { reasoningEffort: 'high' } } }, 'fixture-basic': { name: 'Basic fixture model', fast_mode: false, variants: { low: { reasoningEffort: 'low' }, high: { reasoningEffort: 'high' } } } } } },
  }));
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Fast mode fixture', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fixture', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--workdir', project], { env: process.env, input: '{"id":1,"method":"thread/start","params":{"engine":"wuu","provider":"fixture","model":"fixture","effort":"high","speed":"standard"}}\n', encoding: 'utf8', timeout: 30000 });
  assert.equal(boot.status, 0, boot.stderr);
  const response = boot.stdout.split('\n').filter(Boolean).map(line => JSON.parse(line)).find(item => item.id === 1);
  assert.ok(response?.result?.thread, boot.stdout);
  const threadID = response.result.thread.id;
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!main) await delay(25);
  console.log('FIXTURE', fixture);
  main.setSize(1380, 860);
  await waitFor(main, () => document.querySelector('.composer textarea'));
  // A stalled native catalog must still complete inside the already-open panel:
  // the deferred availability publish is not fresh, so the surface re-invokes
  // the full refresh after its aborted first attempt.
  await waitFor(main, () => document.querySelector('.codex-runtime-trigger:not(:disabled)'));
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  await waitFor(main, () => document.querySelector('.runtime-engine-icon[aria-label="Codex"]'));
  await evaluate(main, () => document.querySelector('.runtime-engine-icon[aria-label="Codex"]').click());
  await waitFor(main, () => document.querySelector('.runtime-engine-icon[aria-label="Codex"]')?.getAttribute('aria-checked') === 'true');
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('.runtime-effort-option'))), false,
    'The gated Codex catalog must not have loaded before the gate opens.');
  fs.writeFileSync(process.env.WUU_TEST_CODEX_MODEL_LIST_GATE, '');
  await waitFor(main, () => document.querySelector('.runtime-effort-option'), null, 15000);
  await settlePanel(main);
  const panelCrop = () => evaluate(main, () => {
    const p = document.querySelector('.runtime-panel').getBoundingClientRect();
    return { x: Math.max(0, Math.floor(p.x - 8)), y: Math.max(0, Math.floor(p.y - 8)),
      width: Math.min(innerWidth - Math.max(0, Math.floor(p.x - 8)), Math.ceil(p.width + 16)),
      height: Math.min(innerHeight - Math.max(0, Math.floor(p.y - 8)), Math.ceil(p.height + 16)) };
  }).then(crop => Object.fromEntries(Object.entries(crop).map(([key, value]) => [key, Math.round(value * main.webContents.getZoomFactor())])));
  const deferredCatalog = await evaluate(main, () => ({ effort: document.querySelector('.runtime-effort-option[aria-checked="true"] .runtime-effort-option-name')?.textContent }));
  fs.writeFileSync(path.join(output, 'deferred-catalog-summary.png'), (await main.webContents.capturePage(await panelCrop())).toPNG());
  await waitFor(main, () => [...document.querySelectorAll('.codex-model-item-name')].some(item => item.textContent === 'GPT-6 Astra'), null, 15000);
  await settlePanel(main);
  deferredCatalog.models = await evaluate(main, () => [...document.querySelectorAll('.codex-model-item-name')].map(item => item.textContent));
  fs.writeFileSync(path.join(output, 'deferred-catalog-models.png'), (await main.webContents.capturePage(await panelCrop())).toPNG());
  // Escape closes the flat panel — there is no page stack to step back through.
  await key(main, 'Escape');
  await waitFor(main, () => !document.querySelector('.runtime-panel'));
  // Leave the draft on the default engine and close the panel for the rest.
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  await waitFor(main, () => document.querySelector('.runtime-engine-icon[aria-label="Wuu"]'));
  await evaluate(main, () => document.querySelector('.runtime-engine-icon[aria-label="Wuu"]').click());
  await waitFor(main, () => document.querySelector('.runtime-engine-icon[aria-label="Wuu"]')?.getAttribute('aria-checked') === 'true'
    && document.querySelector('.runtime-effort-option'));
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  await waitFor(main, () => !document.querySelector('.runtime-panel'));
  // Keep native keyboard checks independent of other desktop windows.
  main.webContents.debugger.attach('1.3');
  await main.webContents.debugger.sendCommand('Emulation.setFocusEmulationEnabled', { enabled: true });
  await evaluate(main, async id => {
    await window.wuu.resumeThread(id);
    await window.wuu.renameThread(id, 'Provider effort fixture');
  }, threadID);
  await evaluate(main, () => { for (const group of document.querySelectorAll('.project-group')) group.querySelector('button[aria-expanded="false"]')?.click(); });
  await waitFor(main, () => [...document.querySelectorAll('.thread-row')].some(row => row.textContent.includes('Provider effort fixture')));
  await evaluate(main, () => [...document.querySelectorAll('.thread-row')].find(row => row.textContent.includes('Provider effort fixture')).querySelector('.thread-row-main').click());
  await waitFor(main, () => document.querySelector('.thread-row[aria-current="page"]')?.textContent.includes('Provider effort fixture'));
  await waitFor(main, () => {
    const input = document.querySelector('.composer textarea');
    return input && !input.disabled && !input.readOnly && document.querySelector('.codex-runtime-trigger:not(:disabled)');
  });
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  await waitFor(main, () => document.querySelector('.runtime-speed-icon'));
  assert.equal(await evaluate(main, () => document.querySelector('.runtime-speed-icon').closest('button').getAttribute('aria-pressed')), 'false');
  main.focus(); main.webContents.focus();
  const beforeToggle = await capturePanel(main, 'provider-standard');
  // The speed options are the last focusable rows in the flat panel; End lands
  // on the fast button from any model or effort row.
  await evaluate(main, () => document.querySelector('.codex-model-item[aria-checked="true"]').focus());
  await key(main, 'End');
  assert(await evaluate(main, () => document.activeElement === document.querySelector('.runtime-speed-icon').closest('button')), 'Keyboard navigation reaches the speed control.');
  await key(main, 'Space');
  // Freeze the live browser animations to keep fill and shake evidence repeatable.
  const chargeDuration = await evaluate(main, () => {
    window.__speedAnimations = document.querySelector('.runtime-speed-icon').getAnimations({ subtree: true });
    for (const animation of window.__speedAnimations) animation.pause();
    return Math.max(0, ...window.__speedAnimations.map(animation => animation.effect.getComputedTiming().endTime));
  });
  for (const progress of [0, 0.35, 0.65, 1]) {
    await evaluate(main, time => {
      for (const animation of window.__speedAnimations) animation.currentTime = time;
      return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    }, chargeDuration * progress);
    const crop = { ...beforeToggle.crop };
    for (const field of ['x', 'y', 'width', 'height']) crop[field] = Math.round(crop[field] * main.webContents.getZoomFactor());
    fs.writeFileSync(path.join(output, `speed-charge-${progress}.png`), (await main.webContents.capturePage(crop)).toPNG());
  }
  await evaluate(main, () => {
    for (const animation of window.__speedAnimations) animation.play();
    delete window.__speedAnimations;
  });
  await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.speed === 'fast', threadID);
  const afterToggle = await capturePanel(main, 'provider-fast-keyboard');
  assert.equal(afterToggle.pressed, 'true');
  assert.deepEqual(afterToggle.fast, beforeToggle.fast, 'Toggling speed must not move its target.');
  assert.notEqual(afterToggle.fastColor, beforeToggle.fastColor, 'Fast mode has a distinct enabled icon.');
  assert(await evaluate(main, () => document.activeElement === document.querySelector('.runtime-speed-icon').closest('button') && getComputedStyle(document.activeElement).outlineStyle !== 'none'), 'Keyboard activation retains a visible focus ring.');
  // The effort column is a readout plus radio rows, not a navigation step.
  assert(await evaluate(main, () => Boolean(document.querySelector('.runtime-effort-column')) && !document.querySelector('.runtime-flat-side-title button')), 'The effort readout does not navigate to model selection.');
  // A click on an effort row commits immediately; there is no drag preview.
  await evaluate(main, () => [...document.querySelectorAll('.runtime-effort-option')].find(option => option.querySelector('.runtime-effort-option-name')?.textContent === 'Low').click());
  await waitFor(main, () => document.querySelector('.runtime-effort-option[aria-checked="true"] .runtime-effort-option-name')?.textContent === 'Low');
  await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.model_variant === 'low', threadID);
  await capturePanel(main, 'effort-click-committed');
  await evaluate(main, () => [...document.querySelectorAll('.runtime-effort-option')].find(option => option.querySelector('.runtime-effort-option-name')?.textContent === 'High').click());
  await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.model_variant === 'high', threadID);
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
  async function openPanel(fastSupported = true) {
    if (!await evaluate(main, () => Boolean(document.querySelector('.runtime-panel')))) {
      // Retiring provider chrome can remain visible with an enabled button
      // inside an inert owner. Only the live destination can receive a click.
      await waitFor(main, () => {
        const trigger = document.querySelector('.codex-runtime-trigger:not(:disabled)');
        return trigger && !trigger.closest('[inert]');
      });
      const target = await evaluate(main, () => {
        const trigger = document.querySelector('.codex-runtime-trigger:not(:disabled)');
        const rect = trigger.getBoundingClientRect();
        const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2;
        return { x, y, reachable: !trigger.closest('[inert]') && trigger.contains(document.elementFromPoint(x, y)) };
      });
      assert(target.reachable, 'The live runtime trigger must receive the pointer, not retiring chrome.');
      const position = { x: Math.round(target.x * main.webContents.getZoomFactor()), y: Math.round(target.y * main.webContents.getZoomFactor()) };
      main.webContents.sendInputEvent({ type: 'mouseMove', ...position });
      main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...position });
      main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...position });
    }
    await waitFor(main, () => document.querySelector('.runtime-flat-panel .codex-model-item'));
    if (fastSupported) await waitFor(main, () => document.querySelector('.runtime-speed-icon')?.closest('button:not(:disabled)'));
    // Opening schedules model autofocus; finish that frame before choosing an
    // effort row or the initial focus can overwrite the test's input.
    await evaluate(main, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  }
  await openPanel();
  await evaluate(main, () => document.querySelector('.runtime-speed-icon').closest('button').focus());
  await evaluate(main, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
  const lightFast = await capturePanel(main, 'light-wide-fast');
  await evaluate(main, () => document.activeElement.blur());
  main.webContents.sendInputEvent({ type: 'mouseMove', x: Math.round((lightFast.fast.x + lightFast.fast.width / 2) * lightFast.zoomFactor), y: Math.round((lightFast.fast.y + lightFast.fast.height / 2) * lightFast.zoomFactor) });
  await waitFor(main, () => document.querySelector('.runtime-speed-icon').closest('button').matches(':hover'));
  await capturePanel(main, 'light-wide-fast-hover');
  await evaluate(main, () => [...document.querySelectorAll('.runtime-speed-option')].find(option => !option.querySelector('.runtime-speed-icon')).click());
  await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.speed === 'standard', threadID);
  await capturePanel(main, 'light-wide-standard-hover');
  await turn('fast-mode-standard', 'default');
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
  await capturePanel(main, 'dark-narrow-standard');
  const persisted = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, threadID);
  assert.equal(persisted.model_variant, 'high');
  assert.equal(persisted.speed, 'standard');
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  main.setSize(1380, 860);
  await evaluate(main, () => {
    document.documentElement.setAttribute('data-theme', 'light');
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });
  // Exercise the effort column's first and last rows, with and without speed support.
  for (const fastSupported of [true, false]) {
    if (!fastSupported) {
      await openPanel();
      await evaluate(main, () => [...document.querySelectorAll('.codex-model-item')].find(item => item.querySelector('.codex-model-item-name')?.textContent === 'Basic fixture model').click());
      // The label updates optimistically; reopening the panel needs the committed model.
      await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.model === 'fixture-basic', threadID);
      await waitFor(main, () => document.querySelector('.codex-model-item[aria-checked="true"] .codex-model-item-name')?.textContent === 'Basic fixture model'
        && !document.querySelector('.runtime-speed-icon'));
    }
    for (const theme of ['light', 'dark']) for (const font of [14, 20]) for (const width of [1280, 760]) {
      await evaluate(main, () => { if (document.querySelector('.runtime-panel')) document.querySelector('.codex-runtime-trigger').click(); });
      main.setContentSize(width, 820);
      await evaluate(main, ({ theme, font }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty('--conversation-message-font-size', `${font}px`);
      }, { theme, font });
      await openPanel(fastSupported);
      for (const endpoint of ['min', 'max']) {
        endpointCase = { fastSupported, theme, font, width, endpoint };
        await evaluate(main, endpoint => {
          const options = document.querySelectorAll('.runtime-effort-option');
          options[endpoint === 'min' ? 0 : options.length - 1].click();
        }, endpoint);
        await waitFor(main, endpoint => {
          const options = [...document.querySelectorAll('.runtime-effort-option')];
          return options[endpoint === 'min' ? 0 : options.length - 1]?.getAttribute('aria-checked') === 'true';
        }, endpoint);
        await capturePanel(main, `aligned-${fastSupported ? 'fast' : 'basic'}-${theme}-${font}-${width}-${endpoint}`, fastSupported);
      }
    }
  }
  await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
  main.setContentSize(1380, 860);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'light';
    document.documentElement.style.removeProperty('--conversation-message-font-size');
  });
  const native = await evaluate(main, async () => {
    const result = await window.wuu.startThread({ engine: 'codex', model: 'gpt-6-astra', effort: 'high' });
    await window.wuu.renameThread(result.thread.id, 'Codex speed fixture');
    return result.thread.id;
  });
  await evaluate(main, () => { for (const group of document.querySelectorAll('.project-group')) group.querySelector('button[aria-expanded="false"]')?.click(); });
  await waitFor(main, () => [...document.querySelectorAll('.thread-row')].some(row => row.textContent.includes('Codex speed fixture')));
  await evaluate(main, () => [...document.querySelectorAll('.thread-row')].find(row => row.textContent.includes('Codex speed fixture')).querySelector('.thread-row-main').click());
  await waitFor(main, () => {
    const input = document.querySelector('.composer textarea');
    // The tool rail uses an icon-only trigger; the full selection remains in
    // its accessible name even when there is no visible model text.
    return input && !input.disabled && !input.readOnly
      && document.querySelector('.codex-runtime-trigger:not(:disabled)')?.getAttribute('aria-label')?.includes('GPT-6 Astra');
  });
  const nativeRuntime = await evaluate(main, async id => {
    const { thread } = await window.wuu.resumeThread(id);
    const trigger = document.querySelector('.codex-runtime-trigger');
    const rect = trigger.getBoundingClientRect();
    const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2;
    return { engine: thread.engine_id, model: thread.model, accessibleName: trigger.getAttribute('aria-label'),
      disabled: trigger.disabled, reachable: rect.width > 0 && rect.height > 0 && trigger.contains(document.elementFromPoint(x, y)), x, y };
  }, native);
  assert.equal(nativeRuntime.engine, 'codex');
  assert.equal(nativeRuntime.model, 'gpt-6-astra');
  assert.equal(nativeRuntime.disabled, false);
  assert.equal(nativeRuntime.reachable, true, 'The native Codex runtime trigger remains a visible pointer target.');
  const nativePoint = { x: Math.round(nativeRuntime.x * main.webContents.getZoomFactor()), y: Math.round(nativeRuntime.y * main.webContents.getZoomFactor()) };
  main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...nativePoint });
  main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...nativePoint });
  await waitFor(main, () => document.querySelector('.codex-runtime-trigger')?.getAttribute('aria-expanded') === 'true');
  await openPanel();
  assert.equal(await evaluate(main, () => document.querySelector('.runtime-speed-icon').closest('button').getAttribute('aria-pressed')), 'true');
  await evaluate(main, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
  await capturePanel(main, 'codex-inherited-fast');
  const clickEffort = (label) => evaluate(main, name => [...document.querySelectorAll('.runtime-effort-option')].find(option => option.querySelector('.runtime-effort-option-name')?.textContent === name).click(), label);
  for (const [speed, tier, slash, effort] of [['standard', 'default', false, 'high'], ['fast', 'fast', false, 'low'], ['standard', 'default', true, 'low'], ['fast', 'fast', false, 'high']]) {
    await openPanel();
    await clickEffort('High');
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.model_effort === 'high'
      && document.querySelector('.runtime-effort-option[aria-busy="true"]') === null, native);
    if (effort === 'low') await clickEffort('Low');
    await waitFor(main, async ({ id, effort }) => (await window.wuu.resumeThread(id)).thread.model_effort === effort, { id: native, effort });
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
      await evaluate(main, () => document.querySelector('.runtime-speed-icon').closest('button').click());
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
    assert.equal(last.params.effort, effort);
    assert.equal(Object.hasOwn(last.params, 'reasoningEffort'), false);
    if (speed === 'fast') {
      await openPanel();
      await evaluate(main, () => Promise.all(document.querySelector('.runtime-panel').getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))));
      const visual = await evaluate(main, () => { const button = document.querySelector('.runtime-speed-icon').closest('button'); return { pressed: button.getAttribute('aria-pressed'), disabled: button.disabled, color: getComputedStyle(button.querySelector('svg')).color, accent: getComputedStyle(button).getPropertyValue('--interaction-accent') }; });
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
  await evaluate(main, () => {
    document.querySelector('.codex-runtime-trigger').click();
    document.documentElement.dataset.theme = 'light';
    document.documentElement.style.removeProperty('--conversation-message-font-size');
  });
  main.setContentSize(1380, 860);
  await waitFor(main, () => document.querySelector('.project-row-new-thread'));
  await evaluate(main, () => document.querySelector('.project-row-new-thread').click());
  // The conversation and draft share the runtime trigger. Wait for the draft
  // commit that closes the old menu before opening the new one.
  await waitFor(main, () => document.querySelector('.empty-scroll-region')
    && document.querySelector('.codex-runtime-trigger[aria-expanded="false"]:not(:disabled)')
    && !document.querySelector('.runtime-panel'));
  await openPanel(false);
  await waitFor(main, () => document.querySelector('.runtime-engine-icon[aria-label]'));
  const panelState = () => evaluate(main, () => ({
    model: document.querySelector('.codex-model-item[aria-checked="true"] .codex-model-item-name')?.textContent,
    effort: [...document.querySelectorAll('.runtime-effort-option')].findIndex(option => option.getAttribute('aria-checked') === 'true'),
  }));
  for (const engine of ['Wuu', 'Codex']) {
    await evaluate(main, engine => document.querySelector(`.runtime-engine-icon[aria-label="${engine}"]`).click(), engine);
    await waitFor(main, engine => document.querySelector(`.runtime-engine-icon[aria-label="${engine}"]`)?.getAttribute('aria-checked') === 'true'
      && document.querySelector('.runtime-effort-option'), engine);
    // A project draft restores its own remembered model, not the last thread's.
    // Choose the unsupported provider model explicitly for this reopen check.
    if (engine === 'Wuu') {
      await evaluate(main, () => [...document.querySelectorAll('.codex-model-item')].find(item => item.querySelector('.codex-model-item-name')?.textContent === 'Basic fixture model').click());
      await waitFor(main, () => document.querySelector('.codex-model-item[aria-checked="true"] .codex-model-item-name')?.textContent === 'Basic fixture model'
        && !document.querySelector('.runtime-speed-icon'));
    }
    const selection = await panelState();
    // Closing and reopening the panel restores the committed selection.
    await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
    await waitFor(main, () => !document.querySelector('.runtime-panel'));
    await openPanel(engine === 'Codex');
    assert.deepEqual(await panelState(), selection, 'Reopening the current engine preserves its model and effort.');
    await capturePanel(main, `current-engine-${engine.toLowerCase()}`, engine === 'Codex');
  }
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ threadID, nativeThreadID: native, nativeRuntime, deferredCatalog, requests, persisted, layoutEvidence }, null, 2));
  console.log('PASS: fast / standard speed reached the provider and native Codex; inherited native speed displayed; model and effort preserved. Artifacts:', output);
  clearTimeout(timeout); server.close(); app.quit();
}
run().catch(async error => {
  console.error(error, 'FIXTURE', fixture);
  fs.writeFileSync(path.join(output, 'failure.txt'), String(error.stack));
  if (main && !main.isDestroyed()) {
    const effort = await evaluate(main, () => {
      const checked = document.querySelector('.runtime-effort-option[aria-checked="true"]');
      return { checked: checked?.querySelector('.runtime-effort-option-name')?.textContent,
        options: [...document.querySelectorAll('.runtime-effort-option .runtime-effort-option-name')].map(node => node.textContent),
        activeTag: document.activeElement?.tagName, activeClass: document.activeElement?.className };
    });
    fs.writeFileSync(path.join(output, 'failure-state.json'), JSON.stringify({ endpointCase, effort }, null, 2));
    fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
  }
  server.close(); app.exit(1);
});
