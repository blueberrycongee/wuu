// Server: npx vite --config dev/channel-agent-settings/vite.config.ts
// Check: npx electron scripts/channel-agent-settings-e2e.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/header-agent-settings");
const baseURL = process.env.WUU_PREVIEW_URL || "http://127.0.0.1:5201";
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function waitFor(win, expression) {
  for (let i = 0; i < 80; i++) {
    if (await win.webContents.executeJavaScript(expression)) return;
    await pause(100);
  }
  throw new Error(`Timed out: ${expression}`);
}
app.whenReady().then(async () => {
  const deadline = setTimeout(() => { console.error('Render checks exceeded three minutes'); app.exit(1); }, 180000);
  const win = new BrowserWindow({ show: false, width: 1400, height: 900, webPreferences: { backgroundThrottling: false } });
  win.webContents.on("console-message", event => { if (event.level === "error") console.error(event.message); });
  const js = code => win.webContents.executeJavaScript(code);
  const click = selector => js(`document.querySelector(${JSON.stringify(selector)}).click()`);
  const settled = () => js(`Promise.all((document.querySelector('.channel-settings-panel')?.getAnimations() || []).filter(a => a.playState === 'running' && a.effect?.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {})))`);
  const capture = async name => {
    await settled();
    fs.writeFileSync(path.join(output, name + '.png'), (await win.webContents.capturePage()).toPNG());
    if (name === 'light-1400-14' || name === 'light-1400-14-appearance') {
      const crop = await js(`(() => { const r = document.querySelector('.channel-settings-panel').getBoundingClientRect(); return {x:Math.floor(r.x-8),y:Math.floor(r.y-8),width:Math.ceil(r.width+16),height:Math.ceil(r.height+16)}; })()`);
      fs.writeFileSync(path.join(output, name + '-panel.png'), (await win.webContents.capturePage(crop)).toPNG());
    }
  };
  const geometry = () => js(`(() => {
    const rect = s => { const r = document.querySelector(s).getBoundingClientRect(); return {x:r.x,y:r.y,w:r.width,h:r.height,right:r.right,bottom:r.bottom}; };
    return {chat:rect('.channel-conversation'),panel:rect('.channel-settings-panel'),composer:rect('.channel-composer'), viewport:innerWidth, height:innerHeight,
      header:rect('.channel-settings-header'),fields:rect('.channel-settings-scroll'),actions:rect('.channel-settings-actions'),save:rect('.channel-settings-actions button[type=submit]'),
      avatar:rect('.channel-identity-avatar-button'),mark:rect('.channel-identity-avatar-button .agent-avatar-mark'),role:rect('.channel-agent-editor-field textarea'),
      hidden:!!document.querySelector('.channel-room-main[inert]'), modal:!!document.querySelector('[aria-modal="true"]'),
      overflow:document.documentElement.scrollWidth > innerWidth};
  })()`);
  const report = [];
  const layouts = [1400, 720, 390].flatMap(width => [14, 20].map(font => ({width, font})));
  for (const theme of ["light", "dark"]) {
    for (const {width, font} of layouts) {
      const locale = font === 14 ? 'zh-CN' : 'en-US';
      console.log(`Checking ${theme} ${width}px ${font}px ${locale}`);
      win.setSize(width, width < 800 ? 640 : 900);
      await win.loadURL(`${baseURL}/dev/channel-agent-settings/index.html?theme=${theme}&font=${font}&locale=${locale}&long&${font === 20 ? 'stress' : 'empty'}`);
      await waitFor(win, `document.querySelector('.channel-room-settings-name')?.textContent === '日程助手'`);
      // Mobile navigation is normally controlled by App. This fixture keeps it
      // hidden at handset widths so it exercises the actual conversation width.
      if (width < 800) await js(`document.querySelector('.app-shell').style.gridTemplateColumns='minmax(0,1fr)'; document.querySelector('.app-shell').firstElementChild.style.display='none'`);
      await js(`window.previewStream = document.querySelector('[role=log]'); window.previewComposer = document.querySelector('.channel-composer')`);
      const entryPoint = await js(`(() => { const r = document.querySelector('.channel-room-settings-trigger').getBoundingClientRect(); return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}; })()`);
      win.webContents.sendInputEvent({type: 'mouseDown', ...entryPoint, button: 'left', clickCount: 1});
      win.webContents.sendInputEvent({type: 'mouseUp', ...entryPoint, button: 'left', clickCount: 1});
      await waitFor(win, `!!document.querySelector('.channel-settings-panel input')`);
      await settled();
      const g = await geometry();
      assert(!g.hidden && !g.modal && !g.overflow, "Settings must retain the chat without overlays or horizontal overflow");
      assert(g.panel.x > g.chat.x && g.panel.right < g.chat.right, 'Floating settings must fit within the conversation');
      assert(g.panel.y >= 0 && g.panel.bottom < g.height, 'Settings must fit vertically');
      assert(g.panel.right - g.save.right >= 8 && g.panel.bottom - g.save.bottom >= 8, 'Save needs a usable inset from the rounded shell');
      assert(Math.abs(g.save.right - g.role.right) < 1, 'The footer and form must share their trailing alignment');
      assert(g.mark.w <= g.avatar.w && g.mark.h <= g.avatar.h, 'Avatar artwork must fit its actual control');
      assert(await js(`window.previewStream === document.querySelector('[role=log]') && window.previewComposer === document.querySelector('.channel-composer')`), 'Opening must preserve mounted chat and composer');
      await capture(`${theme}-${width}-${font}`);

      // Compare the rendered interior over two backgrounds. This catches text
      // bleed-through without pinning a theme color or a CSS implementation.
      await js(`window.previewMain = document.querySelector('.channel-room-main'); window.previewMain.style.visibility = 'hidden'; window.previewParent = document.querySelector('.channel-settings-panel').parentElement; window.previewBackground = window.previewParent.style.background; window.previewParent.style.background = 'black'`);
      const crop = { x: Math.round(g.panel.x + 32), y: Math.round(g.panel.y + 8), width: Math.floor(g.panel.w - 64), height: 4 };
      const black = (await win.webContents.capturePage(crop)).toBitmap();
      await js(`window.previewParent.style.background = 'white'`);
      const white = (await win.webContents.capturePage(crop)).toBitmap();
      assert(black.equals(white), 'The card interior must not reveal the conversation underneath');
      await js(`window.previewMain.style.visibility = ''; window.previewParent.style.background = window.previewBackground`);
      await click('.channel-identity-avatar-button');
      await waitFor(win, `!!document.querySelector('.channel-agent-editor-appearance')`);
      await capture(`${theme}-${width}-${font}-appearance`);
      await click('.agent-avatar-custom-color summary');
      await click('.channel-agent-editor-more summary');
      const overflow = await js(`Array.from(document.querySelectorAll('.channel-settings-fields *')).filter(el => el.getBoundingClientRect().width && el.getBoundingClientRect().right > innerWidth + 1 && getComputedStyle(el).position !== 'absolute').map(el => el.className)`);
      assert.equal(overflow.length, 0, `Settings content must fit: ${overflow}`);
      const expanded = await geometry();
      await js(`document.querySelector('.channel-settings-scroll').scrollTop = 10000`);
      const scrolled = await geometry();
      assert(await js(`(() => { const f = document.querySelector('.channel-settings-scroll'); return f.scrollHeight <= f.clientHeight || f.scrollTop > 0; })()`), 'Overflowing fields must actually scroll');
      assert.equal(scrolled.header.y, expanded.header.y, 'Scrolling must keep the close control in place');
      assert.equal(scrolled.save.y, expanded.save.y, 'Scrolling must keep Save in place');
      assert(scrolled.fields.bottom <= scrolled.actions.y + 1, 'Fields must not cover the fixed actions');
      const saveHit = await js(`(() => { const button = document.querySelector('.channel-settings-actions button[type=submit]'); const r = button.getBoundingClientRect(); const hit = document.elementFromPoint(r.x+r.width/2,r.y+r.height/2); return { reachable: button.contains(hit), hit: hit?.className, x:r.x, y:r.y, height:innerHeight }; })()`);
      assert(saveHit.reachable, `Save must remain reachable after expansion: ${JSON.stringify({saveHit, scrolled})}`);
      await capture(`${theme}-${width}-${font}-scrolled`);
      win.webContents.focus();
      await js(`document.querySelector('.channel-agent-editor-setting .select-menu-trigger').focus()`);
      win.webContents.sendInputEvent({type:'keyDown',keyCode:'Tab'});
      win.webContents.sendInputEvent({type:'keyUp',keyCode:'Tab'});
      await js(`document.querySelector('.channel-agent-editor-setting .select-menu-trigger').focus()`);
      await waitFor(win, `document.activeElement?.matches('.channel-agent-editor-setting .select-menu-trigger:focus-visible')`);
      await capture(`${theme}-${width}-${font}-keyboard`);
      await click('.channel-agent-editor-setting .select-menu-trigger');
      await waitFor(win, `!!document.querySelector('[role=menuitemradio]')`);
      await capture(`${theme}-${width}-${font}-menu`);
      await js(`document.querySelector('[role=menuitemradio]').dispatchEvent(new KeyboardEvent('keydown', {key:'Escape',bubbles:true,cancelable:true}))`);
      await waitFor(win, `!document.querySelector('[role=menuitemradio]')`);
      assert(await js(`!!document.querySelector('.channel-settings-panel')`), 'Escape in a picker must not close the editor');
      await js(`window.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape',bubbles:true}))`);
      await waitFor(win, `!document.querySelector('.channel-settings-panel')`);
      await waitFor(win, `document.activeElement === document.querySelector('.channel-room-settings-trigger')`);
      report.push({theme,width,font,locale,...g});
    }
  }
  for (const theme of ['light', 'dark']) {
    win.setSize(600, 640);
    await win.loadURL(`${baseURL}/dev/channel-agent-settings/index.html?modal&theme=${theme}&font=20&locale=en-US&stress`);
    await waitFor(win, `!!document.querySelector('.sidebar-name-dialog.channel-agent-editor-dialog')`);
    await js(`(() => { const input = document.querySelector('.channel-agent-editor-name input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'An agent name that is longer than the available input width'); input.dispatchEvent(new Event('input',{bubbles:true})); })()`);
    await click('.channel-identity-avatar-button');
    await click('.agent-avatar-custom-color summary');
    await click('.channel-agent-editor-more summary');
    await js(`document.querySelector('.channel-agent-editor-body').scrollTop = 10000`);
    assert(await js(`(() => { const dialog = document.querySelector('.sidebar-name-dialog'); const body = dialog.querySelector('.channel-agent-editor-body'); const save = dialog.querySelector('button[type=submit]'); const r = save.getBoundingClientRect(); return body.scrollTop > 0 && dialog.scrollWidth <= dialog.clientWidth && r.bottom < innerHeight && save.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)); })()`), 'The directory modal must also contain scrolling and keep Save reachable');
    await capture(`${theme}-modal-expanded`);
  }
  win.setSize(1400, 900);
  await win.loadURL(`${baseURL}/dev/channel-agent-settings/index.html?theme=light`);
  await waitFor(win, `!!document.querySelector('.channel-room-settings-trigger')`);
  await js(`window.selectRoom('group')`);
  await waitFor(win, `document.querySelector('.channel-room-settings-name')?.textContent === '项目讨论'`);
  await click(".channel-room-settings-trigger");
  await waitFor(win, `document.querySelectorAll('.channel-settings-member').length === 2`);
  fs.writeFileSync(path.join(output, "group-members.png"), (await win.webContents.capturePage()).toPNG());
  await js(`document.querySelectorAll('.channel-settings-member')[1].click()`);
  await waitFor(win, `document.querySelector('.channel-agent-editor-name input')?.value === '研究助手'`);
  await js(`(() => { const input = document.querySelector('.channel-agent-editor-name input'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'资料核查助手'); input.dispatchEvent(new Event('input',{bubbles:true})); window.failSave=true; })()`);
  await js(`document.querySelector('.channel-settings-form').requestSubmit()`);
  await waitFor(win, `!!document.querySelector('.channel-settings-form [role=alert]')`);
  assert.equal(await js(`window.updates.at(-1).agent_id`), "agent-1");
  fs.writeFileSync(path.join(output, "save-error.png"), (await win.webContents.capturePage()).toPNG());
  await js(`window.failSave=false; document.querySelector('.channel-settings-form').requestSubmit()`);
  await waitFor(win, `!document.querySelector('.channel-settings-panel')`);
  await click(".channel-room-settings-trigger");
  await waitFor(win, `document.querySelector('.channel-settings-members')?.textContent.includes('资料核查助手')`);
  fs.writeFileSync(path.join(output, "geometry.json"), JSON.stringify(report, null, 2));
  console.log("PASS: 12 floating layouts and 2 expanded modals, opacity, avatar expansion, scroll containment, keyboard focus, menus, member routing and save failure/retry");
  clearTimeout(deadline);
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
