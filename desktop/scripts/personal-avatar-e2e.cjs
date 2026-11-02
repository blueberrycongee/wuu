// Production Electron renderer → preload → Go core → Personal runtime → disk.
// Run after build:core and build. Two independent app processes share only the
// disposable fixture profile, proving persistence across a complete restart.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const http = require('node:http');
const { pathToFileURL } = require('node:url');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_AVATAR_OUTPUT || path.join(desktop, '../artifacts/personal-avatar'));
fs.mkdirSync(output, { recursive: true });
if (!process.versions.electron) {
  for (const name of ['edit-error.txt', 'edit-dom.txt', 'edit-failure.png', 'restart-error.txt', 'restart-dom.txt', 'restart-failure.png', 'edit-report.json', 'restart-report.json']) fs.rmSync(path.join(output, name), { force: true });
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-avatar-'));
  const electron = require('electron');
  for (const phase of ['edit', 'restart']) {
    const result = spawnSync(electron, [__filename], { env: { ...process.env, WUU_AVATAR_FIXTURE: fixture, WUU_AVATAR_PHASE: phase }, encoding: 'utf8', timeout: 180000 });
    fs.writeFileSync(path.join(output, `${phase}.log`), `${result.stdout || ''}\n${result.stderr || ''}`);
    if (result.status !== 0) { console.error(result.error || result.stderr || `Electron exited: status=${result.status}, signal=${result.signal}`, `Evidence: ${output}`); process.exit(1); }
  }
  console.log(`Personal avatar E2E passed, including full app restart. Evidence: ${output}`);
  process.exit(0);
}
const { app, BrowserWindow, nativeTheme, nativeImage } = require('electron');
const fixture = process.env.WUU_AVATAR_FIXTURE, phase = process.env.WUU_AVATAR_PHASE;
assert.ok(fixture && phase, 'Use node to run the isolated two-process harness');
const home = path.join(fixture, 'wuu-home');
const userHome = path.join(fixture, 'user-home');
for (const dir of [home, userHome]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.HOME = userHome;
process.env.WUU_DESKTOP_CORE = path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_NODE_EXECUTABLE = process.execPath;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
// Every model request uses this process's loopback fixture; no external
// provider, paid inference or real credential is available.
if (phase === 'edit') {
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines, providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:9/v1', api_key: 'synthetic-not-a-credential', model: 'unused' } } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
}
let fixtureReply;
const fixtureRequests = [];
const server = http.createServer((request, response) => {
  let raw = '';
  request.on('data', chunk => { raw += chunk; });
  request.on('end', () => {
    if (request.method === 'GET') return response.end(JSON.stringify({ data: [{ id: 'unused' }] }));
    const body = JSON.parse(raw);
    fixtureRequests.push({ url: request.url, stream: Boolean(body.stream) });
    if (!body.stream) return response.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Avatar fixture' } }] }));
    response.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = (delta, finish_reason = null) => response.write(`data: ${JSON.stringify({ id: 'avatar-fixture', model: 'unused', choices: [{ index: 0, delta, finish_reason }] })}\n\n`);
    const finish = () => { chunk({}, 'stop'); response.end('data: [DONE]\n\n'); };
    chunk({ role: 'assistant' });
    if (JSON.stringify(body.messages).includes('avatar-motion-fixture')) fixtureReply = { chunk, finish };
    else { chunk({ content: 'Avatar fixture' }); finish(); }
  });
});
const navigationSelector = 'button[data-wuu-plugin="plugin:bundled:personal"]';
let win;
const errors = [], checks = [], limitations = [];
app.on('browser-window-created', (_, window) => { win ||= window; window.webContents.on('render-process-gone', (_, details) => errors.push(details)); });
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
async function until(fn, arg) {
  const end = Date.now() + 30000;
  while (Date.now() < end) { if (await win.webContents.executeJavaScript(`(async () => Boolean(await (${fn})(${JSON.stringify(arg)})))()`, true)) return; await delay(40); }
  throw new Error(`Timed out: ${fn}; argument=${JSON.stringify(arg)}`);
}
async function click(selector) {
  await until(selector => { const element = document.querySelector(selector); return element && element.getBoundingClientRect().width > 0 && !element.disabled && !element.closest('[inert]'); }, selector);
  await evaluate(selector => document.querySelector(selector).click(), selector);
}
const key = async (keyCode, modifiers = []) => {
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
  await delay(60);
};
async function capture(name) {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  await delay(350);
  const shot = await win.webContents.capturePage();
  fs.writeFileSync(path.join(output, `${phase}-${name}.png`), shot.toPNG());
  return shot;
}
async function setName(value) {
  await evaluate(value => { const input = document.querySelector('.mosaic-name-input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); }, value);
}
async function openEditor() {
  await click('[data-action="toggle-personal-panel"]');
  await until(() => document.querySelector('.mosaic-sidebar')?.dataset.panelPhase === 'open');
  if (!checks.includes('Avatar edit control appears on hover and keyboard focus')) {
    win.webContents.sendInputEvent({ type: 'mouseMove', x: 10, y: 10 });
    await until(() => getComputedStyle(document.querySelector('.mosaic-profile-head .mosaic-avatar-edit')).opacity === '0');
    const rect = await evaluate(() => document.querySelector('.mosaic-profile-head .mosaic-avatar-editable').getBoundingClientRect().toJSON());
    win.webContents.sendInputEvent({ type: 'mouseMove', x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) });
    await until(() => getComputedStyle(document.querySelector('.mosaic-profile-head .mosaic-avatar-edit')).opacity === '1');
    await capture('avatar-hover');
    win.webContents.sendInputEvent({ type: 'mouseMove', x: 10, y: 10 });
    await evaluate(() => document.querySelector('.mosaic-profile-head .mosaic-avatar-edit').focus());
    await until(() => getComputedStyle(document.querySelector('.mosaic-profile-head .mosaic-avatar-edit')).opacity === '1');
    checks.push('Avatar edit control appears on hover and keyboard focus');
  }
  await click('.mosaic-profile-head [data-action="edit-personal"]');
  await until(() => document.querySelector('.mosaic-avatar-dialog'));
}
async function avatar(selector = '.mosaic-profile-trigger') {
  return evaluate(selector => { const element = document.querySelector(`${selector} [data-wuu-mascot-provider-hue]`); return element && { hue: element.getAttribute('data-wuu-mascot-provider-hue'), accessory: element.getAttribute('data-wuu-mascot-accessory'), body: element.querySelector('.mo-bob > g:not(.mo-eyes) > path')?.getAttribute('d') }; }, selector);
}
async function assertSynchronized() {
  await until(selector => {
    const read = root => { const svg = document.querySelector(`${root} [data-wuu-mascot-provider-hue]`); return svg && [svg.getAttribute('data-wuu-mascot-provider-hue'), svg.getAttribute('data-wuu-mascot-accessory'), svg.querySelector('.mo-bob > g:not(.mo-eyes) > path')?.getAttribute('d')]; };
    const header = read('.mosaic-profile-trigger');
    return header && JSON.stringify(header) === JSON.stringify(read(selector));
  }, navigationSelector);
  assert.deepEqual(await avatar(navigationSelector), await avatar());
}
async function assertAvatar(key) {
  await until(key => (() => { const [, , accessory, hue] = key.split(':'); const svg = document.querySelector('.mosaic-profile-trigger [data-wuu-mascot-provider-hue]'); return svg?.getAttribute('data-wuu-mascot-provider-hue') === hue && svg?.getAttribute('data-wuu-mascot-accessory') === accessory; })(), key);
  await assertSynchronized();
}
// Observable motion contracts: frames must change only while paintable, preserve
// the body/accessory relationship, and settle with both reduced-motion sources.
async function sampleMotion(selector, count = 5) {
  return evaluate(async ({ selector, count }) => {
    const svg = document.querySelector(`${selector} [data-wuu-mascot-activity]`);
    if (!svg) throw new Error(`Missing avatar: ${selector}`);
    const frames = [];
    for (let i = 0; i < count; i++) {
      await new Promise(resolve => setTimeout(resolve, 120));
      frames.push({
        body: svg.querySelector('.mo-bob > g > path').getAttribute('d'),
        breathe: getComputedStyle(svg.querySelector('.mo-breathe')).transform,
        bob: getComputedStyle(svg.querySelector('.mo-bob')).transform,
        eyes: [...svg.querySelectorAll('.mo-eye > path')].map(path => path.getAttribute('d')),
        accessories: [...svg.querySelectorAll('.wuu-mascot-layer')].map(layer => {
          const matrix = layer.getCTM(), bodyMatrix = svg.querySelector('.mo-bob').getCTM();
          const relative = bodyMatrix.inverse().multiply(matrix);
          return [relative.a, relative.b, relative.c, relative.d, relative.e, relative.f].map(n => Math.round(n * 10000) / 10000 || 0);
        }),
      });
    }
    return frames;
  }, { selector, count });
}
async function assertMotion(selector, label) {
  const frames = await sampleMotion(selector);
  fs.writeFileSync(path.join(output, `${phase}-${label}-frames.json`), JSON.stringify(frames, null, 2));
  assert.ok(new Set(frames.map(frame => JSON.stringify([frame.breathe, frame.bob]))).size > 1, `${label} did not move`);
  for (const frame of frames) {
    assert.equal(frame.body, frames[0].body, `${label} lost its chosen silhouette`);
    assert.deepEqual(frame.accessories, frames[0].accessories, `${label} accessories drifted away from the body`);
  }
}
async function assertStill(selector) {
  const frames = await sampleMotion(selector);
  assert.equal(new Set(frames.map(frame => JSON.stringify(frame))).size, 1, `${selector} kept moving while paused`);
}
async function assertBlink() {
  const samples = await evaluate(async selectors => {
    const samples = [];
    const start = performance.now();
    while (performance.now() - start < 7000) {
      await new Promise(resolve => requestAnimationFrame(resolve));
      samples.push({ time: performance.now() - start, heights: selectors.map(selector =>
        [...document.querySelectorAll(`${selector} .mo-eye > path`)].map(path => path.getBBox().height)) });
    }
    return samples;
  }, ['.mosaic-profile-trigger', navigationSelector]);
  fs.writeFileSync(path.join(output, 'blink-samples.json'), JSON.stringify(samples));
  for (const index of [0, 1]) {
    const heights = samples.map(sample => sample.heights[index][0]);
    assert.ok(Math.min(...heights) < Math.max(...heights) * .5, `Avatar ${index} did not blink`);
    const closed = heights.findIndex(height => height < Math.max(...heights) * .5);
    assert.ok(heights.slice(closed + 1).some(height => height > Math.max(...heights) * .9), `Avatar ${index} stayed closed`);
  }
}
async function assertHover() {
  win.webContents.sendInputEvent({ type: 'mouseMove', x: 600, y: 500 });
  await delay(500);
  const read = () => evaluate(() => {
    const button = document.querySelector('.mosaic-profile-trigger');
    return { button: button.getBoundingClientRect().toJSON(), transform: getComputedStyle(button.querySelector('.mo-root')).transform };
  });
  const before = await read();
  win.webContents.sendInputEvent({ type: 'mouseMove', x: Math.round(before.button.x + before.button.width / 2), y: Math.round(before.button.y + before.button.height / 2) });
  await until(previous => getComputedStyle(document.querySelector('.mosaic-profile-trigger .mo-root')).transform !== previous, before.transform);
  await capture('header-hover');
  const after = await read();
  assert.deepEqual(after.button, before.button, 'Hover moved the click target');
  win.webContents.sendInputEvent({ type: 'mouseMove', x: 600, y: 500 });
  await until(previous => getComputedStyle(document.querySelector('.mosaic-profile-trigger .mo-root')).transform === previous, before.transform);
  checks.push('Hover responds and settles without moving the click target; header and sidebar blink');
}
async function startRecording(label) {
  const directory = path.join(output, `${label}-motion-recording`);
  fs.mkdirSync(directory, { recursive: true });
  const frames = [];
  const receive = (_event, method, params) => {
    if (method !== 'Page.screencastFrame') return;
    const file = `frame-${String(frames.length).padStart(5, '0')}.jpg`;
    fs.writeFileSync(path.join(directory, file), Buffer.from(params.data, 'base64'));
    frames.push({ file, time: params.metadata.timestamp });
    void win.webContents.debugger.sendCommand('Page.screencastFrameAck', { sessionId: params.sessionId });
  };
  win.webContents.debugger.on('message', receive);
  await win.webContents.debugger.sendCommand('Page.startScreencast', { format: 'jpeg', quality: 80, maxWidth: 1280, maxHeight: 920, everyNthFrame: 2 });
  return async () => {
    await win.webContents.debugger.sendCommand('Page.stopScreencast');
    win.webContents.debugger.removeListener('message', receive);
    assert.ok(frames.length > 10, 'Motion recording did not capture frames');
    fs.writeFileSync(path.join(directory, 'frames.json'), JSON.stringify(frames));
    fs.writeFileSync(path.join(directory, 'index.html'), `<!doctype html><meta charset="utf-8"><title>Twin avatar motion — isolated Electron fixture</title><style>body{margin:0;background:#222}img{max-width:100vw;max-height:95vh;display:block;margin:auto}button{position:fixed;bottom:0;left:0}</style><img id="frame"><button onclick="start=performance.now()">Replay</button><script>const frames=${JSON.stringify(frames)};let start=performance.now();function show(now){const elapsed=(now-start)/1000;const target=frames.findLast(f=>f.time-frames[0].time<=elapsed)||frames[0];document.querySelector('img').src=target.file;requestAnimationFrame(show)}requestAnimationFrame(show)</script>`);
  };
}
async function exerciseLiveActivity(label) {
  const finishRecording = await startRecording(label);
  fixtureReply = undefined;
  await assertBlink();
  await assertHover();
  await evaluate(() => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'avatar-motion-fixture');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.focus();
  });
  await key('Return');
  await until(() => document.querySelector('.mosaic-profile-trigger [data-wuu-mascot-activity]')?.dataset.wuuMascotActivity === 'thinking');
  await assertMotion('.mosaic-profile-trigger', `${label}-thinking`);
  await capture(`${label}-thinking`);
  const thinking = (await sampleMotion('.mosaic-profile-trigger', 1))[0];
  const deadline = Date.now() + 30000;
  while (!fixtureReply && Date.now() < deadline) await delay(40);
  assert.ok(fixtureReply, 'Local provider never received the real turn');
  // Change activity while paused. The face must adopt the new expression even
  // though contour frame reads and CSS loops have stopped.
  await evaluate(() => { document.documentElement.dataset.appearanceMotion = 'reduce'; });
  await delay(100);
  const pausedThinking = (await sampleMotion('.mosaic-profile-trigger', 1))[0];
  fixtureReply.chunk({ content: 'Your saved twin is responding.' });
  await until(() => document.querySelector('.mosaic-profile-trigger [data-wuu-mascot-activity]')?.dataset.wuuMascotActivity === 'responding');
  await assertStill('.mosaic-profile-trigger');
  const pausedResponding = (await sampleMotion('.mosaic-profile-trigger', 1))[0];
  assert.notDeepEqual(pausedResponding.eyes, pausedThinking.eyes, 'Paused expression ignored real activity');
  await evaluate(() => { delete document.documentElement.dataset.appearanceMotion; });
  await assertMotion('.mosaic-profile-trigger', `${label}-responding`);
  await capture(`${label}-responding`);
  const responding = (await sampleMotion('.mosaic-profile-trigger', 1))[0];
  assert.notDeepEqual(thinking.eyes, responding.eyes, 'Thinking and responding have the same expression');
  fixtureReply.finish();
  await until(() => document.querySelector('.mosaic-profile-trigger [data-wuu-mascot-activity]')?.dataset.wuuMascotActivity === 'idle');
  await assertMotion('.mosaic-profile-trigger', `${label}-settled-idle`);
  await finishRecording();
  fs.writeFileSync(path.join(output, 'fixture-requests.json'), JSON.stringify(fixtureRequests, null, 2));
  checks.push('Real loopback SSE turn drives idle → thinking → responding → idle, including expression change while reduced; recording retained');
}
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const config = JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8'));
  config.providers.fixture.base_url = `http://127.0.0.1:${server.address().port}/v1`;
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify(config));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!win) await delay(30);
  win.setContentSize(1280, 920);
  await until(() => Boolean(window.wuu));
  const runtimeIdentity = await evaluate(async () => {
    const catalog = await window.wuu.refreshExtensionCatalog();
    return catalog.extension_inventory.find(row => row.id === 'plugin:bundled:personal');
  });
  await click('button[data-wuu-plugin="plugin:bundled:personal"]');
  await until(() => document.querySelector('.mosaic-profile-trigger'));
  if (phase === 'restart') {
    const before = JSON.parse(fs.readFileSync(path.join(output, 'identity.json'), 'utf8'));
    await assertSynchronized();
    assert.deepEqual(await avatar(), before.avatar);
    assert.deepEqual(await avatar(navigationSelector), before.avatar);
    await until(() => document.querySelector('.mosaic-identity-label strong')?.textContent === 'Avatar E2E');
    checks.push('Appearance and renamed identity survived complete Electron/core/runtime restart');
    await capture('persisted');
  } else {
    await assertAvatar('mascot-v1:round:headset:222');
    const defaultAvatar = await avatar();
    const defaultBody = defaultAvatar.body;
    assert.ok(defaultBody);
    checks.push('Legacy identity displays original default');
    await assertMotion('.mosaic-profile-trigger', 'idle-header');
    await evaluate(() => { document.documentElement.dataset.appearanceMotion = 'reduce'; });
    await delay(100);
    await assertStill('.mosaic-profile-trigger');
    await evaluate(() => { delete document.documentElement.dataset.appearanceMotion; });
    await assertMotion('.mosaic-profile-trigger', 'motion-restored');
    // Some desktop capture environments keep Chromium visible even while the
    // native window is hidden/minimized. Record that boundary rather than
    // fabricating document.hidden or letting it block the independent checks.
    const visibility = [];
    const recordVisibility = async action => {
      const state = { action, windowVisible: win.isVisible(), minimized: win.isMinimized(), backgroundThrottling: win.webContents.getBackgroundThrottling(), renderer: await evaluate(() => ({ hidden: document.hidden, visibility: document.visibilityState })) };
      visibility.push(state);
      return state.renderer.hidden;
    };
    win.hide();
    assert.equal(win.isVisible(), false);
    await delay(500);
    let hidden = await recordVisibility('hide');
    if (!hidden) {
      win.show();
      win.minimize();
      await delay(1500);
      hidden = await recordVisibility('minimize');
    }
    if (hidden) {
      await assertStill('.mosaic-profile-trigger');
      checks.push('Real hidden document pauses avatar frames');
    } else limitations.push('This Electron/macOS session reports document.visibilityState=visible after native hide and minimize; hidden-document pause could not be exercised. Offscreen and reduced-motion pause are checked separately.');
    fs.writeFileSync(path.join(output, 'visibility-audit.json'), JSON.stringify(visibility, null, 2));
    win.restore();
    win.show();
    await until(() => !document.hidden);
    win.webContents.debugger.attach('1.3');
    await assertMotion('.mosaic-profile-trigger', 'visible-again');
    await evaluate(() => { document.querySelector('.mosaic-profile-trigger').style.transform = 'translateY(-2000px)'; });
    await delay(100);
    await assertStill('.mosaic-profile-trigger');
    await evaluate(() => { document.querySelector('.mosaic-profile-trigger').style.removeProperty('transform'); });
    await assertMotion('.mosaic-profile-trigger', 'onscreen-again');
    checks.push('Idle breathing resumes after in-app reduction and offscreen pause; accessory planes stay attached');
    await exerciseLiveActivity('default');
    await openEditor();
    await click('[data-avatar-shape="diamond"]');
    await click('[data-avatar-accessory="leaf"]');
    await click('[data-avatar-hue="150"]');
    assert.equal((await avatar('.mosaic-avatar-preview')).hue, '150');
    assert.equal((await avatar('.mosaic-avatar-preview')).accessory, 'leaf');
    assert.notEqual((await avatar('.mosaic-avatar-preview')).body, defaultBody);
    await setName('Combined identity');
    await capture('preview-light');
    // Repeated submits must not create conflicting writes or close a new dialog.
    await evaluate(() => { const form = document.querySelector('.mosaic-avatar-dialog'); form.requestSubmit(); form.requestSubmit(); });
    await until(() => !document.querySelector('.mosaic-avatar-dialog'));
    await assertAvatar('mascot-v1:diamond:leaf:150');
    await until(() => document.querySelector('.mosaic-identity-label strong')?.textContent === 'Combined identity');
    checks.push('Name and appearance save together through real backend; duplicate submit is ignored');
    await click('[data-action="toggle-personal-panel"]');
    assert.deepEqual(await avatar('.mosaic-profile-head'), await avatar());
    await capture('profile-synchronized');
    await click('.mosaic-profile-head [data-action="edit-personal"]');
    await click('[data-avatar-shape="triangle"]');
    await setName('Discard this name');
    await click('.mosaic-avatar-dialog .mosaic-name-close');
    await assertAvatar('mascot-v1:diamond:leaf:150');
    await until(() => document.querySelector('.mosaic-identity-label strong')?.textContent === 'Combined identity');
    await openEditor();
    await setName('Discard on Escape');
    await click('[data-avatar-shape="round"]');
    await key('Escape');
    await until(() => !document.querySelector('.mosaic-avatar-dialog'));
    await assertAvatar('mascot-v1:diamond:leaf:150');
    await until(() => document.querySelector('.mosaic-identity-label strong')?.textContent === 'Combined identity');
    checks.push('Close and Escape discard both name and appearance drafts');
    // A second writer changes only the name via the real public IPC service.
    // Submit in the same continuation before any periodic view refresh can hide
    // the stale-revision boundary; the UI must keep the draft and allow retry.
    await openEditor();
    await click('[data-avatar-hue="202"]');
    await evaluate(async record => {
      if (!record?.fingerprint || !record.graph_generation) throw new Error('Personal runtime identity missing');
      const invoke = async (method, input) => {
        const result = await window.wuu.requestPluginRuntime({ id: record.id, fingerprint: record.fingerprint, graph_generation: record.graph_generation, request_id: crypto.randomUUID(), user_space_id: 'local', product_domain: 'personal', composition_profile: 'personal', method, input });
        if (!result.ok) throw new Error(JSON.stringify(result));
        return result.value.result;
      };
      const current = (await invoke('agents.ensure_default', {})).agent;
      await invoke('agents.update', { agent_id: current.agent_id, expected_revision: current.revision, name: 'Concurrent rename' });
      document.querySelector('.mosaic-avatar-dialog').requestSubmit();
    }, runtimeIdentity);
    await until(() => document.querySelector('.mosaic-avatar-dialog [role="alert"]'));
    assert.equal((await avatar('.mosaic-avatar-preview')).hue, '202');
    await capture('revision-conflict');
    await click('[data-action="save-avatar"]');
    await until(() => !document.querySelector('.mosaic-avatar-dialog'));
    await assertAvatar('mascot-v1:diamond:leaf:202');
    await until(() => document.querySelector('.mosaic-identity-label strong')?.textContent === 'Concurrent rename');
    checks.push('Real concurrent rename causes CAS conflict; draft survives failure, retry preserves rename');
    // Switching away discards an unsubmitted editor; returning reads durable style.
    await openEditor();
    await click('[data-avatar-hue="350"]');
    await click('[data-ribbon-id="conversations"]');
    assert.equal((await avatar(navigationSelector)).hue, '202');
    await capture('sidebar-on-coding');
    await click('button[data-wuu-plugin="plugin:bundled:personal"]');
    await until(() => document.querySelector('.mosaic-profile-trigger'));
    assert.equal(await evaluate(() => !!document.querySelector('.mosaic-avatar-dialog')), false);
    await assertAvatar('mascot-v1:diamond:leaf:202');
    checks.push('Navigation away/back discards unsaved editor; sidebar retains saved shape/accessory/color on Coding and Personal');
    await openEditor();
    await key('Tab', ['shift']);
    assert.equal(await evaluate(() => document.querySelector('.mosaic-avatar-dialog').contains(document.activeElement)), true);
    await key('Tab');
    assert.equal(await evaluate(() => document.querySelector('.mosaic-avatar-dialog').contains(document.activeElement)), true);
    // All available shapes/accessories are real rendered options, including none.
    for (const shape of ['round', 'rounded-square', 'capsule', 'triangle', 'diamond']) {
      await click(`[data-avatar-shape="${shape}"]`);
      for (const accessory of ['none', 'beanie', 'hard-hat', 'headset', 'leaf']) {
        await click(`[data-avatar-accessory="${accessory}"]`);
        await assertMotion('.mosaic-avatar-preview', `${shape}-${accessory}`);
        await capture(`${shape}-${accessory}`);
      }
    }
    for (const button of await evaluate(() => [...document.querySelectorAll('[data-avatar-hue]')].map(button => button.dataset.avatarHue))) {
      await click(`[data-avatar-hue="${button}"]`);
      assert.equal((await avatar('.mosaic-avatar-preview')).hue, button);
    }
    await click('[data-avatar-hue="288"]');
    win.setContentSize(680, 900);
    nativeTheme.themeSource = 'dark';
    await evaluate(async () => { await window.wuu.setThemePreference('dark'); document.documentElement.style.setProperty('--conversation-message-font-size', '20px'); });
    await until(() => getComputedStyle(document.documentElement).getPropertyValue('color-scheme').includes('dark'));
    await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    assert.equal(await evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches), true);
    await assertStill('.mosaic-avatar-preview');
    await capture('dark-narrow-layout');
    const eyes = await evaluate(() => [...document.querySelectorAll('.mosaic-avatar-dialog [data-wuu-mascot-provider-hue], button[data-wuu-plugin="plugin:bundled:personal"] [data-wuu-mascot-provider-hue]')].map(svg => ({
      box: svg.getBoundingClientRect().toJSON(),
      eyes: [...svg.querySelectorAll('.mo-eye > path')].map(eye => ({ rect: eye.getBoundingClientRect().toJSON(), fill: getComputedStyle(eye).fill, visibility: getComputedStyle(eye).visibility }))
    })));
    fs.writeFileSync(path.join(output, 'eye-audit.json'), JSON.stringify(eyes, null, 2));
    const shot = await capture('dark-narrow-large-reduced-motion');
    const nativeShot = nativeImage.createFromBuffer(shot.toPNG());
    const pixels = nativeShot.toBitmap(), size = nativeShot.getSize();
    const scale = size.width / await evaluate(() => innerWidth);
    assert.equal(pixels.length, size.width * size.height * 4);
    // SVG geometry alone passed while Chromium failed to paint the updated
    // eyes. Sample the native-resolution bitmap so downsampling does not wash
    // out the narrow eyes before comparing them with their CSS-pixel bounds.
    for (const [index, svg] of eyes.entries()) {
      assert.equal(svg.eyes.length, 2);
      for (const eye of svg.eyes) {
        assert.ok(eye.rect.width > 1 && eye.rect.height > 1);
        let dark = 0, total = 0;
        for (let y = Math.ceil(scale * (eye.rect.y + eye.rect.height * .25)); y < scale * (eye.rect.y + eye.rect.height * .75); y++) {
          for (let x = Math.ceil(scale * (eye.rect.x + eye.rect.width * .25)); x < scale * (eye.rect.x + eye.rect.width * .75); x++) {
            const offset = (y * size.width + x) * 4;
            if (pixels[offset] < 80 && pixels[offset + 1] < 80 && pixels[offset + 2] < 80) dark++;
            total++;
          }
        }
        assert.ok(total > 0 && dark / total > .5, `Avatar ${index} has an unpainted eye (${dark}/${total})`);
      }
    }
    checks.push('Actual screenshot pixels retain both eyes after repeated style changes and reduced motion');
    const geometry = await evaluate(() => { const dialog = document.querySelector('.mosaic-avatar-dialog'); const rect = dialog.getBoundingClientRect(); return { width: dialog.clientWidth, scrollWidth: dialog.scrollWidth, left: rect.left, right: rect.right, viewport: innerWidth }; });
    assert.ok(geometry.scrollWidth <= geometry.width + 1 && geometry.left >= 0 && geometry.right <= geometry.viewport);
    checks.push('Five shapes/five accessory choices, color presets, keyboard focus containment, dark/narrow/large/reduced motion');
    win.setMinimumSize(360, 600);
    win.setContentSize(420, 760);
    await setName('Long twin name '.repeat(4).trim());
    await capture('compact-long-name');
    await evaluate(() => { const dialog = document.querySelector('.mosaic-avatar-dialog'); dialog.scrollTop = dialog.scrollHeight; });
    await capture('compact-scrolled');
    await setName('Concurrent rename');
    win.setContentSize(680, 900);
    await click('[data-action="save-avatar"]');
    await until(() => !document.querySelector('.mosaic-avatar-dialog'));
    await assertAvatar('mascot-v1:diamond:leaf:288');
    await click('.mosaic-identity-label');
    await until(() => document.querySelector('.mosaic-name-input'));
    assert.deepEqual(await avatar('.mosaic-avatar-preview'), await avatar());
    await setName('   ');
    assert.equal(await evaluate(() => document.querySelector('[data-action="save-avatar"]').disabled), true);
    await setName('a'.repeat(65));
    assert.equal(await evaluate(() => document.querySelector('[data-action="save-avatar"]').disabled), true);
    await evaluate(() => { const input = document.querySelector('.mosaic-name-input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, 'Avatar E2E'); input.dispatchEvent(new Event('input', { bubbles: true })); });
    await click('[data-action="save-avatar"]');
    await until(() => !document.querySelector('.mosaic-avatar-dialog'));
    await assertAvatar('mascot-v1:diamond:leaf:288');
    await until(() => document.querySelector('.mosaic-identity-label strong')?.textContent === 'Avatar E2E');
    checks.push('Unified editor rejects invalid names; name-only save preserves appearance');
    fs.writeFileSync(path.join(output, 'identity.json'), JSON.stringify({ avatar: await avatar(), defaultAvatar }));
    await capture('saved-renamed');
    await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'no-preference' }] });
    win.setContentSize(1280, 920);
    await exerciseLiveActivity('customized');
    const saved = await avatar(navigationSelector);
    win.webContents.once('dom-ready', () => {
      void evaluate(selector => {
        window.avatarObservations = [];
        const observe = () => {
          const svg = document.querySelector(`${selector} [data-wuu-mascot-provider-hue]`);
          if (svg) window.avatarObservations.push({ hue: svg.getAttribute('data-wuu-mascot-provider-hue'), accessory: svg.getAttribute('data-wuu-mascot-accessory'), body: svg.querySelector('.mo-bob > g:not(.mo-eyes) > path')?.getAttribute('d') });
        };
        new MutationObserver(observe).observe(document.documentElement, { childList: true, subtree: true, attributes: true });
        observe();
      }, navigationSelector);
    });
    win.webContents.reload();
    await until(() => document.readyState === 'complete' && Array.isArray(window.avatarObservations));
    await click(navigationSelector);
    await assertAvatar('mascot-v1:diamond:leaf:288');
    const observations = await evaluate(() => window.avatarObservations);
    assert.ok(observations.length > 0);
    for (const observation of observations) assert.deepEqual(observation, saved, 'Reload briefly exposed a different navigation avatar');
    checks.push('Renderer reload hydrates saved sidebar identity before publishing; no default-avatar flash observed');
    await capture('reloaded');
  }
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, `${phase}-report.json`), JSON.stringify({ passed: true, boundary: 'Production Electron/main/preload/Go/Personal runtime and disk; isolated profile and loopback SSE provider; no paid inference or real accounts', checks, errors, limitations }, null, 2));
  server.closeAllConnections();
  server.close();
  app.quit();
}
const watchdog = setTimeout(() => { console.error('Avatar E2E timeout'); app.exit(2); }, 150000);
app.on('will-quit', () => clearTimeout(watchdog));
run().catch(async error => { console.error(error); fs.writeFileSync(path.join(output, `${phase}-error.txt`), String(error.stack || error)); if (win && !win.isDestroyed()) { fs.writeFileSync(path.join(output, `${phase}-dom.txt`), await evaluate(() => document.body.innerText + '\n' + [...document.querySelectorAll('button')].map(button => button.outerHTML).join('\n')).catch(String)); await capture('failure').catch(() => {}); } app.exit(1); });
