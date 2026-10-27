import assert from 'node:assert/strict';
import { app, BrowserWindow, ipcMain } from 'electron';
import { PresentationFrameCapture } from '../src/main/presentationFrame';
import { writeFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
const fixture = process.env.WUU_CONVERSATION_TRANSITION_FIXTURE!;
const output = process.env.WUU_CONVERSATION_TRANSITION_OUTPUT!;
assert(fixture && output, 'Run through conversation-presentation-transition-e2e.cjs');
app.setPath('userData', join(fixture, 'profile')); app.disableHardwareAcceleration();
void app.whenReady().then(async () => {
console.log("[presentation] app ready");
const window = new BrowserWindow({ width: 1000, height: 700, useContentSize: true, show: true, webPreferences: { preload: join(fixture, 'preload.cjs'), contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
window.webContents.on("console-message", (_event, level, message) => console.log(`[renderer:${level}] ${message}`));
window.webContents.on("render-process-gone", (_event, details) => console.error("[presentation] renderer gone", details));
const framesService = new PresentationFrameCapture(contents => contents === window.webContents);
ipcMain.handle('fixture:presentation-frame', event => framesService.capture(event));
const run = (code: string) => window.webContents.executeJavaScript(code);
const frames = () => run('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
let messageBounds = { x: 244, y: 95, width: 732, height: 480 };
const capture = async (name: string) => {
  const image = await window.webContents.capturePage(); writeFileSync(join(output, `${name}.png`), image.toPNG());
  return createHash('sha256').update(image.crop(messageBounds).toBitmap()).digest('hex');
};
const sidebar = async () => createHash('sha256').update((await window.webContents.capturePage()).crop({ x: 0, y: 0, width: 220, height: 650 }).toBitmap()).digest('hex');
const results: unknown[] = [];
try {
  await window.loadFile(join(fixture, 'index.html'));
  console.log('[presentation] initial document loaded');
  window.webContents.debugger.attach('1.3');
  for (const motion of ['no-preference', 'reduce']) for (const timing of ['delayed', 'same-task', 'microtask']) {
    const label = `${motion}-${timing}`;
    await window.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: motion }] });
    console.log(`[presentation] loading ${label}`);
    await window.loadFile(join(fixture, 'index.html')); console.log('[presentation] loaded'); await frames(); console.log('[presentation] rendered');
    const api = await run('({ ownWindowCapture: typeof window.wuu?.capturePresentationFrame })');
    writeFileSync(join(output, 'apis.json'), JSON.stringify({ versions: process.versions, api }, null, 2));
    assert.equal(api.ownWindowCapture, 'function');
    await run('document.querySelector(".history").scrollTop=2000'); await frames();
    messageBounds = await run('(() => { const r = document.querySelector(".history").getBoundingClientRect(); return { x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height) }; })()');
    const before = await capture(`${label}-source`);
    await run(`window.begin(${JSON.stringify(timing)})`);
    await run('window.transitionReady');
    if (timing === 'delayed') await run('window.commit()');
    await frames(); const sourceSidebar = await sidebar();
    const held = await capture(`${label}-held`);
    window.webContents.sendInputEvent({ type: 'mouseDown', x: 65, y: 30, button: 'left', clickCount: 1 });
    window.webContents.sendInputEvent({ type: 'mouseUp', x: 65, y: 30, button: 'left', clickCount: 1 }); await frames();
    const navigation = await run('document.querySelector("aside button").textContent');
    const sidebarLive = sourceSidebar !== await sidebar();
    await run('window.retire()'); await frames();
    const retired = await capture(`${label}-retired`);
    const detached = await run('document.querySelector("main")===null');
    await run('window.replace()'); await frames();
    const successorHeld = await capture(`${label}-successor-held`);
    assert.equal(await run('document.querySelector("[data-wuu-presentation-frame=full]")===null && !document.querySelector("main").closest("[inert]")'), true, 'Successor chrome stays live before message reveal');
    await run('window.reveal();window.transitionFinished'); await frames();
    const after = await capture(`${label}-target`);
    window.webContents.sendInputEvent({ type: 'mouseDown', x: 65, y: 30, button: 'left', clickCount: 1 });
    window.webContents.sendInputEvent({ type: 'mouseUp', x: 65, y: 30, button: 'left', clickCount: 1 }); await frames();
    const navigationAfterRelease = await run('document.querySelector("aside button").textContent');
    const result = { motion, timing, api, navigationAfterRelease, dispatchLatencyMs: await run('window.dispatchLatencyMs'), pixelsHeld: before === held, retiredPixelsHeld: before === retired, successorPixelsHeld: before === successorHeld, detached, targetChanged: after !== before, navigation, sidebarLive,
      draft: await run('document.querySelector("textarea").value'), before, held, after };
    results.push(result); writeFileSync(join(output, 'results.json'), JSON.stringify(results, null, 2));
    assert.equal(result.draft, 'Preserved unsent draft');
  }
  assert(results.every((row: any) => row.pixelsHeld && row.retiredPixelsHeld && row.successorPixelsHeld && row.detached && row.targetChanged && row.sidebarLive && row.navigation === 'Navigation 1'), 'Host raster continuity/hit-testing failed; inspect results.json and PNGs');
  for (const [label, sourceGeometry, targetGeometry] of [
    ["wider-sidebar-taller-footer", { sidebar: 220, footer: 70 }, { sidebar: 300, footer: 140 }],
    ["narrower-sidebar-shorter-footer", { sidebar: 300, footer: 140 }, { sidebar: 220, footer: 70 }],
  ] as const) {
    await window.loadFile(join(fixture, 'index.html')); await frames();
    await run(`window.geometry(${JSON.stringify(sourceGeometry)});document.querySelector(".history").scrollTop=2000`); await frames();
    const geometry = () => run(`(() => { const rect = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height) }; }; return { messages: rect('.history'), sidebar: rect('aside'), composer: rect('textarea'), titlebar: rect('h2') }; })()`);
    const sourceBounds = await geometry(), sourceImage = await window.webContents.capturePage();
    writeFileSync(join(output, `${label}-source.png`), sourceImage.toPNG());
    await run('window.begin()'); await run('window.transitionReady'); await run('window.commit();window.retire()'); await frames();
    await run(`window.replace(${JSON.stringify(targetGeometry)})`); await frames();
    const targetBounds = await geometry();
    const textarea = targetBounds.composer;
    window.webContents.sendInputEvent({ type: 'mouseDown', x: textarea.x + 30, y: textarea.y + 20, button: 'left', clickCount: 1 });
    window.webContents.sendInputEvent({ type: 'mouseUp', x: textarea.x + 30, y: textarea.y + 20, button: 'left', clickCount: 1 }); await frames();
    assert.equal(await run('document.activeElement===document.querySelector("textarea")'), true, 'Retained message pixels blocked native composer focus');
    await window.webContents.insertText('live successor ');
    assert((await run('document.querySelector("textarea").value')).includes('live successor '), 'Retained message pixels blocked the live composer');
    window.webContents.sendInputEvent({ type: 'mouseDown', x: 65, y: 30, button: 'left', clickCount: 1 });
    window.webContents.sendInputEvent({ type: 'mouseUp', x: 65, y: 30, button: 'left', clickCount: 1 }); await frames();
    const heldImage = await window.webContents.capturePage(); writeFileSync(join(output, `${label}-held.png`), heldImage.toPNG());
    await run('window.reveal();window.transitionFinished'); await frames();
    const targetImage = await window.webContents.capturePage(); writeFileSync(join(output, `${label}-target.png`), targetImage.toPNG());
    const digest = (image: Electron.NativeImage, rect: Electron.Rectangle) => createHash('sha256').update(image.crop(rect).toBitmap()).digest('hex');
    for (const key of ['sidebar', 'composer', 'titlebar']) assert.equal(digest(heldImage, targetBounds[key]), digest(targetImage, targetBounds[key]), `${label}: retained pixels covered live ${key}`);
    const before = sourceBounds.messages, after = targetBounds.messages;
    const intersection = { x: Math.max(before.x, after.x), y: Math.max(before.y, after.y),
      width: Math.min(before.x + before.width, after.x + after.width) - Math.max(before.x, after.x),
      height: Math.min(before.y + before.height, after.y + after.height) - Math.max(before.y, after.y) };
    assert.equal(digest(sourceImage, intersection), digest(heldImage, intersection), `${label}: source/target message intersection was not retained`);
    results.push({ label, sourceGeometry, targetGeometry, intersection, liveChromePixelsEqual: true, composerInteractive: true });
    writeFileSync(join(output, 'results.json'), JSON.stringify(results, null, 2));
  }
  rmSync(join(output, 'failure.json'), { force: true });
  for (const motion of ['no-preference', 'reduce']) for (const age of [3900, 5000]) {
    const label = `${motion}-retirement-after-${age}ms`;
    await window.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: motion }] });
    await window.loadFile(join(fixture, 'index.html')); await frames();
    await run('document.querySelector(".history").scrollTop=2000'); await frames();
    messageBounds = await run('(() => { const r=document.querySelector(".history").getBoundingClientRect();return {x:Math.round(r.x),y:Math.round(r.y),width:Math.round(r.width),height:Math.round(r.height)};})()');
    const source = await capture(`${label}-source`);
    await run('window.begin()'); await run('window.transitionReady');
    await run(`window.waitInitialAge(${age})`);
    const initialHeld = await run('document.documentElement.hasAttribute("data-wuu-compositor-frame")');
    assert.equal(initialHeld, age < 4000, `${label}: exercise the intended original lease boundary`);
    await run('window.prepareRetirement()'); await run('window.transitionReady');
    await run('window.retire()'); await frames();
    await new Promise(resolve => setTimeout(resolve, 200));
    assert.equal(await run('document.querySelector("main")===null'), true, `${label}: old provider DOM must be removed`);
    assert.equal(await run('document.documentElement.dataset.wuuCompositorFrame'), 'full', `${label}: fresh frame must outlive the original lease`);
    const retired = await capture(`${label}-retired`);
    assert.equal(retired, source, `${label}: the fresh retained message pixels must survive actual retirement`);
    await run('window.replace()'); await frames();
    await run('window.reveal();window.transitionFinished'); await frames();
    const target = await capture(`${label}-successor`);
    assert.notEqual(target, source, `${label}: successor reveals normally`);
    results.push({ label, initialHeld, source, retired, target, oldDOMRemoved: true, freshRetirementFrame: true });
    writeFileSync(join(output, 'results.json'), JSON.stringify(results, null, 2));
  }
  console.log(JSON.stringify({ status: 'passed', output, results }));
} catch (error) { writeFileSync(join(output, 'failure.json'), JSON.stringify({ error: String(error), results }, null, 2)); console.error(error); process.exitCode = 1; }
finally { window.destroy(); app.exit(process.exitCode === 1 ? 1 : 0); }

}).catch(error => { console.error(error); app.exit(1); });
