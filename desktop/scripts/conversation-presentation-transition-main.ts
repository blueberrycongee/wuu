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
const capture = async (name: string) => {
  const image = await window.webContents.capturePage(); writeFileSync(join(output, `${name}.png`), image.toPNG());
  return createHash('sha256').update(image.crop({ x: 220, y: 0, width: 780, height: 650 }).toBitmap()).digest('hex');
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
    const before = await capture(`${label}-source`);
    await run(`window.begin(${JSON.stringify(timing)})`);
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
    await run('window.reveal()'); await frames();
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
  rmSync(join(output, 'failure.json'), { force: true });
  console.log(JSON.stringify({ status: 'passed', output, results }));
} catch (error) { writeFileSync(join(output, 'failure.json'), JSON.stringify({ error: String(error), results }, null, 2)); console.error(error); process.exitCode = 1; }
finally { window.destroy(); app.exit(process.exitCode === 1 ? 1 : 0); }

}).catch(error => { console.error(error); app.exit(1); });
