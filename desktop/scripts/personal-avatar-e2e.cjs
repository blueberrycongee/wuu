// Production Electron renderer → preload → Go core → Personal runtime → disk.
// Run after build:core and build. Three independent app processes share only the
// disposable fixture profile, proving persistence across a complete restart.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_AVATAR_OUTPUT || path.join(desktop, '../artifacts/personal-avatar'));
fs.mkdirSync(output, { recursive: true });
if (!process.versions.electron) {
  for (const name of ['edit-error.txt', 'edit-dom.txt', 'edit-failure.png', 'restart-error.txt', 'restart-dom.txt', 'restart-failure.png', 'edit-report.json', 'restart-report.json', 'reset-restart-error.txt', 'reset-restart-dom.txt', 'reset-restart-failure.png', 'reset-restart-report.json']) fs.rmSync(path.join(output, name), { force: true });
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-avatar-'));
  const electron = require('electron');
  for (const phase of ['edit', 'restart', 'reset-restart']) {
    const result = spawnSync(electron, [__filename], { env: { ...process.env, WUU_AVATAR_FIXTURE: fixture, WUU_AVATAR_PHASE: phase }, encoding: 'utf8', timeout: 180000 });
    fs.writeFileSync(path.join(output, `${phase}.log`), `${result.stdout || ''}\n${result.stderr || ''}`);
    if (result.status !== 0) { console.error(result.error || result.stderr, `Evidence: ${output}`); process.exit(1); }
  }
  console.log(`Personal avatar E2E passed, including full app restart. Evidence: ${output}`);
  process.exit(0);
}
const { app, BrowserWindow, nativeTheme, nativeImage } = require('electron');
const fixture = process.env.WUU_AVATAR_FIXTURE, phase = process.env.WUU_AVATAR_PHASE;
assert.ok(fixture && phase, 'Use node to run the isolated three-process harness');
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
// No model operation is part of this test, and even an accidental request has
// no external provider endpoint or real credential to use.
if (phase === 'edit') {
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines, providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:9/v1', api_key: 'synthetic-not-a-credential', model: 'unused' } } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
}
const navigationSelector = 'button[data-wuu-plugin="plugin:bundled:personal"]';
let win;
const errors = [], checks = [];
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
async function run() {
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
  if (phase === 'reset-restart') {
    const before = JSON.parse(fs.readFileSync(path.join(output, 'identity.json'), 'utf8'));
    await assertAvatar('mascot-v1:round:headset:222');
    assert.deepEqual(await avatar(), before.defaultAvatar);
    checks.push('Saved default survives another full app restart; sidebar and header match the original shape/accessory/color');
    await capture('persisted');
  } else if (phase === 'restart') {
    const before = JSON.parse(fs.readFileSync(path.join(output, 'identity.json'), 'utf8'));
    await assertSynchronized();
    assert.deepEqual(await avatar(), before.avatar);
    assert.deepEqual(await avatar(navigationSelector), before.avatar);
    await until(() => document.querySelector('.mosaic-identity-label strong')?.textContent === 'Avatar E2E');
    checks.push('Appearance and renamed identity survived complete Electron/core/runtime restart');
    await capture('persisted');
    await openEditor();
    await click('[data-action="reset-avatar"]');
    await click('[data-action="save-avatar"]');
    await until(() => !document.querySelector('.mosaic-avatar-dialog'));
    await assertAvatar('mascot-v1:round:headset:222');
    assert.deepEqual(await avatar(), before.defaultAvatar);
    await click('[data-action="toggle-personal-panel"]');
    assert.deepEqual(await avatar('.mosaic-profile-head'), before.defaultAvatar);
    checks.push('Reset requires Save and restores original shape/accessory/color across sidebar, header and profile');
    await capture('reset');
  } else {
    await assertAvatar('mascot-v1:round:headset:222');
    const defaultAvatar = await avatar();
    const defaultBody = defaultAvatar.body;
    assert.ok(defaultBody);
    checks.push('Legacy identity displays original default');
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
    await click('[data-action="reset-avatar"]');
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
    for (const shape of ['round', 'rounded-square', 'capsule', 'triangle', 'diamond']) await click(`[data-avatar-shape="${shape}"]`);
    for (const accessory of ['none', 'beanie', 'hard-hat', 'headset', 'leaf']) await click(`[data-avatar-accessory="${accessory}"]`);
    await click('[data-avatar-hue="288"]');
    win.setContentSize(680, 900);
    nativeTheme.themeSource = 'dark';
    await evaluate(async () => { await window.wuu.setThemePreference('dark'); document.documentElement.style.setProperty('--conversation-message-font-size', '20px'); });
    await until(() => getComputedStyle(document.documentElement).getPropertyValue('color-scheme').includes('dark'));
    win.webContents.debugger.attach('1.3');
    await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    assert.equal(await evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches), true);
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
  fs.writeFileSync(path.join(output, `${phase}-report.json`), JSON.stringify({ passed: true, boundary: 'Production Electron/main/preload/Go/Personal runtime and disk; isolated profile; no inference or real accounts', checks, errors }, null, 2));
  app.quit();
}
const watchdog = setTimeout(() => { console.error('Avatar E2E timeout'); app.exit(2); }, 150000);
app.on('will-quit', () => clearTimeout(watchdog));
run().catch(async error => { console.error(error); fs.writeFileSync(path.join(output, `${phase}-error.txt`), String(error.stack || error)); if (win && !win.isDestroyed()) { fs.writeFileSync(path.join(output, `${phase}-dom.txt`), await evaluate(() => document.body.innerText + '\n' + [...document.querySelectorAll('button')].map(button => button.outerHTML).join('\n')).catch(String)); await capture('failure').catch(() => {}); } app.exit(1); });
