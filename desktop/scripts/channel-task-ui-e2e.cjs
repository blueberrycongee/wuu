const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/channel-task-ui");
const baseURL = process.env.WUU_COORDINATOR_PREVIEW_URL || "http://127.0.0.1:5250";
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
async function waitFor(win, expression) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await win.webContents.executeJavaScript(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out: ${expression}`);
}
app.whenReady().then(async () => {
  console.log("Starting task UI checks");
  const win = new BrowserWindow({ show: false, width: 1100, height: 760, webPreferences: { backgroundThrottling: false } });
  win.webContents.debugger.attach("1.3");
  for (const theme of ["light", "dark"]) for (const width of [1600, 1100, 390]) {
    console.log(theme, width);
    win.setSize(width, 760);
    await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?tasks=1&theme=${theme}`);
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
    await waitFor(win, `!!document.querySelector('.channel-assignment-heading')`);
    const collapsed = await win.webContents.executeJavaScript(`(() => {
      const task = document.querySelector('.channel-assignment-item');
      const summary = task.querySelector('summary');
      return { open: task.open, height: task.getBoundingClientRect().height, summaryHeight: summary.getBoundingClientRect().height,
        text: summary.textContent, avatar: !!summary.querySelector('.agent-avatar-mark'),
        overflow: document.documentElement.scrollWidth > innerWidth };
    })()`);
    assert.equal(collapsed.open, false);
    assert.equal(collapsed.avatar, true);
    assert.equal(collapsed.text, "把群成员头像/名称做成资料与配置入口");
    assert(Math.abs(collapsed.height - collapsed.summaryHeight) < 1 && collapsed.height <= 36);
    assert.equal(collapsed.overflow, false);
    for (const member of [false, true]) {
      if (member) {
        await win.webContents.executeJavaScript(`window.setScene('waiting', ['thinking'])`);
        await waitFor(win, `!!document.querySelector('.channel-response-status-avatar') && !document.querySelector('.channel-coordinator-mascot')`);
      }
      const edges = await win.webContents.executeJavaScript(`(() => {
        const frame = document.querySelector('.composer-frame').getBoundingClientRect();
        const avatar = document.querySelector('${member ? '.channel-response-status-avatar' : '.channel-coordinator-mascot'}').getBoundingClientRect();
        return { input: frame.left, avatar: avatar.left };
      })()`);
      assert(Math.abs(edges.input - edges.avatar) < 1, JSON.stringify({ theme, width, member, ...edges }));
    }
    fs.writeFileSync(path.join(output, `${theme}-${width}-collapsed.png`), (await win.webContents.capturePage()).toPNG());
    await win.webContents.executeJavaScript(`document.querySelector('.channel-assignment-heading').click()`);
    const expanded = await win.webContents.executeJavaScript(`(() => {
      const task = document.querySelector('.channel-assignment-item');
      return { open: task.open, height: task.getBoundingClientRect().height,
        body: task.querySelector('.channel-assignment-body').innerText,
        overflow: document.documentElement.scrollWidth > innerWidth };
    })()`);
    assert(expanded.open && expanded.height > collapsed.height);
    assert(expanded.body.includes("完成后汇报结果并提交改动"));
    assert.equal(expanded.overflow, false);
    fs.writeFileSync(path.join(output, `${theme}-${width}-expanded.png`), (await win.webContents.capturePage()).toPNG());
  }
  console.log("PASS: avatar/title summaries, expandable details, composer alignment for coordinator and member, light/dark at 390/1100/1600px");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
