const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/room-coordinator");
const baseURL = process.env.WUU_COORDINATOR_PREVIEW_URL || "http://127.0.0.1:5207";
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
async function waitFor(win, expression) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await win.webContents.executeJavaScript(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  console.error(await win.webContents.executeJavaScript(`document.body.innerText`));
  throw new Error(`Timed out: ${expression}`);
}
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1100, height: 700, webPreferences: { backgroundThrottling: false } });
  win.webContents.on("console-message", event => { if (event.level === "error") console.error(event.message); });
  for (const theme of ["light", "dark"]) for (const width of [1100, 390]) for (const state of ["working", "waiting", "failed", "needs_members", "queued"]) {
    win.setSize(width, 700);
    await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?theme=${theme}&state=${state}`);
    await waitFor(win, `!!document.querySelector('.channel-coordinator-activity')`);
    const activity = await win.webContents.executeJavaScript(`(() => {
      const node = document.querySelector('.channel-coordinator-activity');
      const r = node.getBoundingClientRect();
      return {text: node.textContent, x:r.x, right:r.right, bottom:r.bottom, width:innerWidth, height:innerHeight, overflow:node.scrollWidth > node.clientWidth + 1};
    })()`);
    assert(activity.x >= 0 && activity.right <= activity.width && activity.bottom <= activity.height && !activity.overflow, "Coordination status must remain visible without overflow");
    assert(!activity.text.includes("private-room-session"));
    if (state === "waiting") { assert(activity.text.includes("Alice")); assert(!activity.text.includes("{")); }
    if (state === "failed" || state === "waiting" || state === "working") {
      await win.webContents.executeJavaScript(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
      fs.writeFileSync(path.join(output, `${theme}-${width}-${state}.png`), (await win.webContents.capturePage()).toPNG());
    }
    if (state === "failed") {
      await win.webContents.executeJavaScript(`document.querySelector('.channel-coordinator-activity button').click()`);
      await waitFor(win, `window.retryCount === 1 && !!document.querySelector('.channel-coordinator-activity') && !document.querySelector('.channel-coordinator-activity button')`);
      assert.equal(await win.webContents.executeJavaScript(`window.retryCount`), 1);
    }
  }
  await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?state=idle&locale=en-US`);
  await waitFor(win, `!!document.querySelector('.channel-message-stream')`);
  assert.equal(await win.webContents.executeJavaScript(`!!document.querySelector('.channel-coordinator-activity')`), false);
  console.log("PASS: room coordination status, retry, idle, both themes, and 390/1100px layouts");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
