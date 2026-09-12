// Server: npx vite --config dev/channel-replies/vite.config.ts
// Check: npx electron scripts/collaboration-polish-e2e.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/single-conversation");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
async function waitFor(win, expression) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await win.webContents.executeJavaScript(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out: ${expression}`);
}
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1400, height: 800, webPreferences: { backgroundThrottling: false } });
  const js = expression => win.webContents.executeJavaScript(expression);
  const report = [];
  for (const theme of ["light", "dark"]) for (const width of [1400, 900, 600, 360]) {
    win.setContentSize(width, 800);
    await win.loadURL(`http://127.0.0.1:5199/dev/channel-replies/index.html?theme=${theme}`);
    await waitFor(win, `document.querySelectorAll('.channel-activity-inspect').length === 2`);
    await js(`(() => {
      window.wuu.listChannelSessions = async () => ({sessions: [{primary:true, session_ref:'a0', named_agent_id:'a0', state:'running', purpose:'conversation'}]});
      document.querySelector('.channel-activity-inspect').click();
    })()`);
    await waitFor(win, `!!document.querySelector('.session-inspector-history .turn')`);
    await waitFor(win, `!document.querySelector('.session-inspector-extension').getAnimations().some(a => a.playState === 'running')`);
    assert(await js(`window.lastReadSession === 'a0' && !document.querySelector('.channel-activity-session-entry') && !document.querySelector('button[aria-label="全部会话"]')`), "Avatar must open the single conversation directly");
    const geometry = await js(`(() => {
      const panel=document.querySelector('.session-inspector-extension');
      const history=document.querySelector('.session-inspector-history');
      return {overflow:panel.scrollWidth-panel.clientWidth,scrollable:history.scrollHeight>history.clientHeight,headerOverflow:panel.querySelector('header').scrollWidth-panel.querySelector('header').clientWidth};
    })()`);
    assert(geometry.overflow <= 1 && geometry.headerOverflow <= 1, `No horizontal overflow at ${theme}/${width}`);
    assert(geometry.scrollable, "Long history must scroll inside the panel");
    await js(`window.stage=7; window.emitSession('a0')`);
    await waitFor(win, `document.querySelector('.session-inspector-history').textContent.includes('实时输出版本 7')`);
    fs.writeFileSync(path.join(output, `${theme}-${width}-latest.png`), (await win.webContents.capturePage()).toPNG());
    await js(`document.querySelector('.session-inspector-history').scrollTop=0`);
    await waitFor(win, `document.querySelector('.session-inspector-history').textContent.includes('历史问题 0')`);
    await js(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
    await new Promise(resolve => setTimeout(resolve, 200));
    fs.writeFileSync(path.join(output, `${theme}-${width}-history.png`), (await win.webContents.capturePage()).toPNG());
    await js(`document.querySelector('.channel-sessions-close').click()`);
    await waitFor(win, `!document.querySelector('.session-inspector-extension')`);
    assert(await js(`!!document.querySelector('.channel-message-stream')`), "Closing the trace preserves the room");
    report.push({theme,width,...geometry});
  }
  fs.writeFileSync(path.join(output,"geometry.json"),JSON.stringify(report,null,2));
  console.log("PASS: direct avatar entry, complete history, live output and close navigation across 8 theme/width layouts.");
  win.destroy(); app.quit();
}).catch(error=>{console.error(error);app.exit(1)});
