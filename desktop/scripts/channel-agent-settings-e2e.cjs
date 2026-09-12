// Server: npx vite --config dev/channel-agent-settings/vite.config.ts
// Check: npx electron scripts/channel-agent-settings-e2e.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/header-agent-settings");
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
  const win = new BrowserWindow({ show: false, width: 1400, height: 900, webPreferences: { backgroundThrottling: false } });
  win.webContents.on("console-message", event => { if (event.level === "error") console.error(event.message); });
  const js = code => win.webContents.executeJavaScript(code);
  const click = selector => js(`document.querySelector(${JSON.stringify(selector)}).click()`);
  const geometry = () => js(`(() => {
    const rect = s => { const r = document.querySelector(s).getBoundingClientRect(); return {x:r.x,y:r.y,w:r.width,h:r.height,right:r.right,bottom:r.bottom}; };
    return {chat:rect('.channel-conversation'),panel:rect('.channel-settings-panel'),composer:rect('.channel-composer'), viewport:innerWidth,
      hidden:!!document.querySelector('.channel-room-main[inert]'), modal:!!document.querySelector('[aria-modal="true"]'),
      overflow:document.documentElement.scrollWidth > innerWidth};
  })()`);
  const report = [];
  for (const theme of ["light", "dark"]) {
    for (const width of [1400, 1100, 720, 390]) {
      win.setSize(width, 900);
      await win.loadURL(`http://127.0.0.1:5201/dev/channel-agent-settings/index.html?theme=${theme}`);
      await waitFor(win, `document.querySelector('.channel-room-settings-name')?.textContent === '日程助手'`);
      // Mobile navigation is normally controlled by App. This fixture keeps it
      // hidden at handset widths so it exercises the actual conversation width.
      if (width < 800) await js(`document.querySelector('.app-shell').style.gridTemplateColumns='minmax(0,1fr)'; document.querySelector('.app-shell').firstElementChild.style.display='none'`);
      await click(".channel-room-settings-trigger");
      await waitFor(win, `!!document.querySelector('.channel-settings-panel input')`);
      const g = await geometry();
      assert(!g.hidden && !g.modal && !g.overflow, "Settings must retain the chat without overlays or horizontal overflow");
      if (width > 1000) {
        assert(g.chat.w >= 480, "Wide chat must remain readable");
        assert(Math.abs(g.chat.right - g.panel.x) < 1, "Right panel must dock beside the chat");
      } else {
        assert(g.chat.bottom <= g.panel.y + 1, "Narrow panel must stack below, not cover the chat");
        assert(g.composer.bottom <= g.panel.y + 1, "Composer must stay above the settings");
      }
      await pause(80);
      fs.writeFileSync(path.join(output, `${theme}-${width}.png`), (await win.webContents.capturePage()).toPNG());
      await click('.channel-identity-avatar-button');
      await waitFor(win, `!!document.querySelector('.channel-agent-editor-appearance')`);
      fs.writeFileSync(path.join(output, `${theme}-${width}-appearance.png`), (await win.webContents.capturePage()).toPNG());
      const overflow = await js(`Array.from(document.querySelectorAll('.channel-settings-fields *')).filter(el => el.getBoundingClientRect().right > innerWidth + 1 && getComputedStyle(el).position !== 'absolute').map(el => el.className)`);
      assert.equal(overflow.length, 0, `Settings content must fit: ${overflow}`);
      await js(`window.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape',bubbles:true}))`);
      await waitFor(win, `!document.querySelector('.channel-settings-panel')`);
      await pause(50);
      assert(await js(`document.activeElement === document.querySelector('.channel-room-settings-trigger')`), "Closing must return keyboard focus to the entry point");
      report.push({theme,width,...g});
    }
  }
  win.setSize(1400, 900);
  await win.loadURL("http://127.0.0.1:5201/dev/channel-agent-settings/index.html?theme=light");
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
  console.log("PASS: 8 theme/size layouts, avatar expansion, focus return, member routing, save failure/retry and refresh");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
