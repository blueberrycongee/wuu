// Run through wuu-e2e. macOS chrome here is the real renderer component with a
// synthetic platform bridge on Linux, not native AppKit acceptance.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { app, BrowserWindow, ipcMain } = require('electron');

if (process.platform === 'linux') assert.ok(process.env.DISPLAY, 'Xvfb display required');
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_RIBBON_CHROME_OUTPUT || path.join(desktop, 'out/e2e/ribbon-chrome'));
const platform = process.env.WUU_RIBBON_CHROME_PLATFORM || 'darwin';
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-ribbon-chrome-'));
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(temporary, 'profile'));
const extension = path.join(temporary, 'extension.cjs');
fs.writeFileSync(extension, `module.exports = api => ({ ...api, platform: ${JSON.stringify(platform)}, windowToggleFullScreen: async () => {}, windowMinimize: async () => {}, windowClose: async () => {}, windowToggleMaximize: async () => {}, windowIsMaximized: async () => false, windowIsFullScreen: () => require("electron").ipcRenderer.invoke("test:is-fullscreen"), onWindowFullScreenChange: handler => { const listener = (_event, value) => handler(value); require("electron").ipcRenderer.on("test:fullscreen", listener); return () => require("electron").ipcRenderer.removeListener("test:fullscreen", listener); }, windowPointerPosition: () => require("electron").ipcRenderer.invoke("test:pointer"), windowSetControls: state => require("electron").ipcRenderer.invoke("test:controls", state) });`);
process.env.WUU_RENDERER_E2E_EXTENSION = extension;
const report = { status: 'running', platform, scope: `Shipping renderer with synthetic backend on ${process.platform}`, cases: [] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
let win;
let pointer = { x: 500, y: 300 };
let nativeControls;
ipcMain.handle('test:pointer', () => pointer);
ipcMain.handle('test:controls', (_event, state) => { nativeControls = state; });
ipcMain.handle('test:is-fullscreen', () => Boolean(win && !win.isDestroyed() && win.isFullScreen()));
const evaluate = (fn, argument) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(argument)})`);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(fn, label) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) { if (await evaluate(fn)) return; await delay(30); }
  throw new Error('Timed out: ' + label);
}
async function settle() {
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
  });
}
async function point(selector) {
  return evaluate(selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) }; }, selector);
}
function move(p) { pointer = p; win.webContents.sendInputEvent({ type: 'mouseMove', ...p }); }
function clickAt(p) {
  move(p);
  win.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...p });
  win.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...p });
}
async function closeRail() {
  move({ x: win.getContentSize()[0] - 80, y: 120 });
  await evaluate(() => document.activeElement?.blur());
  await until(() => document.querySelector('.sidebar-ribbon')?.dataset.expanded !== 'true', 'rail closes');
  await settle();
}
const read = (probe) => {
  const rail = document.querySelector('.sidebar-ribbon');
  const toggle = document.querySelector('.section-sidebar-toggle');
  const rect = toggle.getBoundingClientRect();
  const position = probe || { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
  const hit = document.elementFromPoint(position.x, position.y);
  const caption = [...document.querySelectorAll('.mac-window-control, .linux-window-controls button')].map(button => {
    const r = button.getBoundingClientRect();
    return { label: button.getAttribute('aria-label'), hit: button.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)) };
  });
  return { expanded: rail.dataset.expanded === 'true', toggleHit: toggle.contains(hit), railHit: rail.contains(hit), rect: rect.toJSON(), railRect: rail.getBoundingClientRect().toJSON(), caption, collapsed: document.querySelector('.app-shell').classList.contains('sidebar-collapsed') };
};
async function run() {
  // macOS fullscreen requires a real titlebar host; the layout cases keep the
  // frameless fixture they were written against.
  const windowChrome = process.env.WUU_TRAFFIC_LIGHTS_E2E === 'true' && process.platform === 'darwin'
    ? { titleBarStyle: 'hidden' }
    : { frame: false };
  win = new BrowserWindow({ width: 1280, height: 820, ...windowChrome, show: process.env.WUU_CHROME_HEADLESS !== 'true', webPreferences: {
    preload: path.join(__dirname, 'resize-e2e-preload.cjs'), sandbox: false, contextIsolation: true, nodeIntegration: false,
  } });
  win.on('enter-full-screen', () => win.webContents.send('test:fullscreen', true));
  win.on('leave-full-screen', () => win.webContents.send('test:fullscreen', false));
  if (process.env.WUU_E2E_URL) await win.loadURL(process.env.WUU_E2E_URL);
  else await win.loadFile(path.join(desktop, 'out/renderer/index.html'));
  await until(() => !!document.querySelector('.turn') && !!document.querySelector('.section-sidebar-toggle'), 'conversation ready');
  if (process.env.WUU_TRAFFIC_LIGHTS_E2E === 'true') {
    const controlsVisible = () => getComputedStyle(document.querySelector('.mac-window-controls')).visibility === 'visible';
    const controlsHidden = () => getComputedStyle(document.querySelector('.mac-window-controls')).visibility === 'hidden';
    for (const fullScreen of (process.platform === 'darwin' && process.env.WUU_CHROME_HEADLESS !== 'true' ? [false, true] : [false])) for (const theme of ['light','dark']) {
      if (win.isFullScreen() !== fullScreen) {
        await new Promise(resolve => { win.once(fullScreen ? 'enter-full-screen' : 'leave-full-screen', resolve); win.setFullScreen(fullScreen); });
      }
      await evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
      move({x:500,y:300});
      await until(controlsHidden, 'traffic lights initially hidden');
      const initial=await evaluate(()=>document.querySelector('.conversation-pane').getBoundingClientRect().toJSON());
      for (let entry = 0; entry < 3; entry++) {
        // Native coordinates alone must reopen the capsule after DOM hover goes stale.
        pointer = {x:400,y:9};
        await until(controlsVisible,'top edge reveals traffic lights');
        await settle();
        assert.equal(nativeControls.visible, true);
        const pill = await evaluate(()=>document.querySelector('.mac-window-controls').getBoundingClientRect().toJSON());
        assert.ok(nativeControls.x > pill.left && nativeControls.x < pill.right);
        assert.ok(nativeControls.y > pill.top && nativeControls.y < pill.bottom);
        const virtualControls = await evaluate(()=>document.querySelectorAll('.mac-window-control').length);
        if (fullScreen) {
          await until(()=>document.querySelectorAll('.mac-window-control').length === 3, 'fullscreen traffic lights render');
          const hit = await evaluate(()=>[...document.querySelectorAll('.mac-window-control')].every(button => {
            const rect = button.getBoundingClientRect();
            return button.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2));
          }));
          assert.equal(hit, true, 'Fullscreen traffic lights are hit-testable');
          if (entry === 0) {
            fs.writeFileSync(path.join(output, `traffic-lights-${theme}-fullscreen.png`), (await win.webContents.capturePage()).toPNG());
          }
        } else {
          assert.equal(virtualControls, 0, 'Windowed mode leaves the traffic lights to AppKit');
        }
        pointer = {x:pill.right-5,y:pill.bottom-5};
        await delay(300);
        assert.equal(await evaluate(controlsVisible), true, 'Capsule stays open over its drag area');
        assert.deepEqual(await evaluate(()=>document.querySelector('.conversation-pane').getBoundingClientRect().toJSON()),initial,'Revealing chrome must not move content');
        pointer = {x:500,y:300};
        await until(controlsHidden,'leaving hides traffic lights');
        await settle();
        assert.equal(nativeControls.visible, false);
      }
      report.cases.push({theme,fullScreen:win.isFullScreen(),repeatHover:3,dragAreaRemainsOpen:true,layoutStable:true,virtualControls:win.isFullScreen()?3:0}); save();
    }
    if (win.isFullScreen()) await new Promise(resolve=>{win.once('leave-full-screen',resolve);win.setFullScreen(false)});
    await until(()=>document.querySelectorAll('.mac-window-control').length === 0, 'fullscreen controls leave with native chrome');
    report.status='passed'; save(); win.destroy(); app.quit(); return;
  }
  for (const [theme, size, width] of [['light', 15, 1280], ['dark', 20, 1280], ['light', 20, 600], ['dark', 15, 600]]) {
    win.setContentSize(width, 820);
    await evaluate(({ theme, size }) => { document.documentElement.dataset.theme = theme; document.documentElement.style.setProperty('--conversation-message-font-size', size + 'px'); }, { theme, size });
    for (const section of ['conversations', 'settings']) {
      if (section === 'settings') {
        clickAt(await point('.sidebar-account-trigger'));
        await until(() => !!document.querySelector('.sidebar-account-menu'), 'account menu opens');
      }
      const target = section === 'conversations' ? '[data-wuu-component="conversations-section"]' : '.sidebar-account-menu [data-settings-page="providers"]';
      clickAt(await point(target));
      await until(section === 'settings' ? () => !!document.querySelector('.settings-embedded') : () => !document.querySelector('.settings-embedded'), 'section navigation');
      await settle();
      await closeRail();
      for (const collapsed of [false, true]) {
        if ((await evaluate(read)).collapsed !== collapsed) clickAt(await point('.section-sidebar-toggle'));
        await closeRail();
        const before = await evaluate(read);
        assert.equal(before.collapsed, collapsed);
        assert.equal(before.toggleHit, true, 'Context toggle is reachable with the rail closed');
        move(await point('[data-wuu-component="conversations-section"]'));
        await settle();
        // Hover must leave the contextual toggle in its original hit area.
        const togglePoint = { x: Math.round(before.rect.x + before.rect.width / 2), y: Math.round(before.rect.y + before.rect.height / 2) };
        const during = await evaluate(read, togglePoint);
        const label = `${platform}-${theme}-${size}-${width}-${section}-${collapsed ? 'collapsed' : 'docked'}`;
        const result = { label, before, during };
        report.cases.push(result); save();
        fs.writeFileSync(path.join(output, label + '-open.png'), (await win.webContents.capturePage()).toPNG());
        assert.equal(during.caption.length, platform === 'darwin' ? 0 : 3, 'macOS uses native buttons; Linux uses renderer controls');
        if (platform === 'darwin') assert.ok(during.caption.every(control => !control.hit), 'Traffic lights stay hidden while using the ribbon');
        else assert.ok(during.caption.every(control => control.hit), 'Caption controls remain reachable');
        assert.equal(during.toggleHit, true, 'Hover keeps the contextual toggle reachable');
        assert.deepEqual(during.railRect, before.railRect, 'Hover does not widen the ribbon');
        await closeRail();
        const restored = await evaluate(read);
        assert.equal(restored.toggleHit, true, 'Leaving hover restores the contextual toggle');
        clickAt(await point('.section-sidebar-toggle'));
        await closeRail();
        result.afterClick = await evaluate(read);
        assert.equal(result.afterClick.collapsed, !collapsed, 'Restored toggle still changes the sidebar');
        // Keyboard navigation keeps both the rail and toggle usable.
        await evaluate(() => document.querySelector('[data-wuu-component="conversations-section"]').focus());
        win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'ArrowDown' });
        win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'ArrowDown' });
        await until(() => document.querySelector('.sidebar-ribbon')?.contains(document.activeElement), 'keyboard focus stays in the rail');
        await settle();
        assert.equal((await evaluate(read, togglePoint)).toggleHit, true, 'Keyboard navigation keeps the toggle reachable');
        await closeRail();
        fs.writeFileSync(path.join(output, label + '-closed.png'), (await win.webContents.capturePage()).toPNG());
        save();
      }
    }
  }
  report.status = 'passed'; save();
  console.log(`Ribbon chrome passed: ${report.cases.length} cases (${platform} renderer on ${process.platform}).`);
  win.destroy(); app.quit();
}
const watchdog = setTimeout(() => { report.status = 'timed-out'; save(); app.exit(1); }, 180000);
watchdog.unref();
app.whenReady().then(run).catch(async error => {
  report.status = 'failed'; report.error = String(error.stack || error); save(); console.error(error);
  if (win && !win.isDestroyed()) fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG());
  app.exit(1);
});
