// Server: npx vite --config dev/channel-agent-settings/vite.config.ts
// Check: npx electron scripts/channel-settings-resize-e2e.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/channel-settings-resize");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1400, height: 900, webPreferences: { backgroundThrottling: false } });
  const js = code => win.webContents.executeJavaScript(code);
  async function waitFor(expression) {
    for (let i = 0; i < 100; i++) {
      if (await js(expression)) return;
      await pause(50);
    }
    throw new Error(`Timed out: ${expression}`);
  }
  const geometry = () => js(`(() => {
    const rect = s => {const r=document.querySelector(s).getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,right:r.right,bottom:r.bottom}};
    const handle=document.querySelector('.channel-settings-resizer');
    return {chat:rect('.channel-conversation'),panel:rect('.channel-settings-panel'),handle:rect('.channel-settings-resizer'),
      min:+handle.getAttribute('aria-valuemin'),max:+handle.getAttribute('aria-valuemax'),hidden:handle.hidden,
      overflow:document.documentElement.scrollWidth>innerWidth};
  })()`);
  async function drag(delta) {
    const g = await geometry();
    const x = Math.round(g.handle.x + 2), y = Math.round(g.handle.y + 150);
    assert.equal(await js(`document.elementFromPoint(${x},${y})?.className`), "channel-settings-resizer", "Handle must win hit testing");
    win.webContents.sendInputEvent({ type: "mouseMove", x, y });
    win.webContents.sendInputEvent({ type: "mouseDown", x, y, button: "left", clickCount: 1 });
    await waitFor(`document.querySelector('.channel-settings-resizer').dataset.resizing === 'true'`);
    win.webContents.sendInputEvent({ type: "mouseMove", x: x + delta, y, button: "left" });
    await waitFor(`Math.abs(document.querySelector('.channel-settings-panel').getBoundingClientRect().width - ${Math.max(g.min, Math.min(g.max, g.panel.w - delta))}) < 1`);
    win.webContents.sendInputEvent({ type: "mouseUp", x: x + delta, y, button: "left", clickCount: 1 });
    await waitFor(`!document.querySelector('.channel-settings-resizer').dataset.resizing`);
    return geometry();
  }
  const report = [];
  for (const theme of ["light", "dark"]) for (const font of [14, 18]) {
    win.setSize(1400, 900);
    await win.loadURL(`http://127.0.0.1:5201/dev/channel-agent-settings/index.html?theme=${theme}&long=1`);
    await waitFor(`!!document.querySelector('.channel-room-settings-trigger')`);
    await js(`localStorage.removeItem('wuu.channels.settingsWidth'); document.documentElement.style.setProperty('--conversation-message-font-size','${font}px'); document.documentElement.style.setProperty('--appearance-scale','${font / 14}'); document.querySelector('.channel-room-settings-trigger').click()`);
    await waitFor(`document.querySelector('.channel-settings-resizer')?.hidden === false`);
    await js(`document.querySelector('.channel-settings-resizer').dispatchEvent(new MouseEvent('dblclick',{bubbles:true}))`);
    await waitFor(`document.querySelector('.channel-settings-panel').getBoundingClientRect().width === 340`);
    const before = await geometry();
    const larger = await drag(-80);
    assert.equal(larger.panel.w, before.panel.w + 80);
    assert.equal(larger.chat.w, before.chat.w - 80);
    const maximum = await drag(-600);
    assert.equal(maximum.panel.w, maximum.max);
    assert(maximum.chat.w >= 400);
    const minimum = await drag(700);
    assert.equal(minimum.panel.w, minimum.min);
    // The scrollbar's rightmost pixel belongs to the chat, not the resize target.
    assert(await js(`(() => {const s=document.querySelector('[role=log]'),r=s.getBoundingClientRect();return s.contains(document.elementFromPoint(r.right-1,r.y+100))})()`));
    await js(`document.querySelector('[role=log]').scrollTop=0`);
    const scroll = await js(`(() => {const s=document.querySelector('[role=log]'),r=s.getBoundingClientRect();return {x:Math.round(r.right-20),y:Math.round(r.y+100),max:s.scrollHeight-s.clientHeight}})()`);
    assert(scroll.max > 0, "Fixture must have real scrollable chat history");
    win.webContents.sendInputEvent({ type: "mouseWheel", x: scroll.x, y: scroll.y, deltaY: -250, deltaX: 0 });
    await waitFor(`document.querySelector('[role=log]').scrollTop > 0`);
    assert.equal((await geometry()).panel.w, minimum.panel.w, "Scrolling must not resize settings");
    // Keyboard and double-click use the same bounds as pointer dragging.
    await js(`document.querySelector('.channel-settings-resizer').focus()`);
    win.webContents.sendInputEvent({ type: "keyDown", keyCode: "LEFT" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode: "LEFT" });
    await waitFor(`document.querySelector('.channel-settings-panel').getBoundingClientRect().width === ${minimum.panel.w + 16}`);
    const k = await geometry();
    for (const type of ["mouseDown", "mouseUp"]) win.webContents.sendInputEvent({ type, x: Math.round(k.handle.x + 2), y: 150, button: "left", clickCount: 2 });
    await waitFor(`document.querySelector('.channel-settings-panel').getBoundingClientRect().width === 340`);
    await drag(-80);
    await js(`document.querySelector('.channel-room-settings-trigger').click()`);
    await waitFor(`!document.querySelector('.channel-settings-panel')`);
    await js(`document.querySelector('.channel-room-settings-trigger').click()`);
    await waitFor(`document.querySelector('.channel-settings-panel')?.getBoundingClientRect().width === 420`);
    fs.writeFileSync(path.join(output, `${theme}-${font}.png`), (await win.webContents.capturePage()).toPNG());
    win.setSize(1100, 900);
    await waitFor(`document.querySelector('.channel-view').clientWidth === 860`);
    const constrained = await drag(-200);
    assert.equal(constrained.panel.w, 460);
    assert.equal(constrained.chat.w, 400);
    win.setSize(1000, 900);
    await waitFor(`document.querySelector('.channel-settings-resizer').hidden`);
    const narrow = await geometry();
    assert(narrow.chat.bottom <= narrow.panel.y + 1 && !narrow.overflow);
    assert.equal(narrow.handle.w, 0);
    fs.writeFileSync(path.join(output, `${theme}-${font}-narrow.png`), (await win.webContents.capturePage()).toPNG());
    report.push({ theme, font, before, larger, maximum, minimum, constrained, narrow, scroll });
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2));
  console.log("PASS: light/dark × 14/18px; real mouse drag, min/max, chat clearance, hit testing, wheel scrolling, keyboard, reset, reopen, narrow stacking");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
