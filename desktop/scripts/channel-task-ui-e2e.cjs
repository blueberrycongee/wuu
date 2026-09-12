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
    await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?tasks=1&messages=1&theme=${theme}`);
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
    const messageAlignment = await win.webContents.executeJavaScript(`(() => {
      const icon = document.querySelector('.channel-attachment-button svg').getBoundingClientRect();
      const centers = Array.from(document.querySelectorAll('.chat-avatar-slot .mo-bob > g:not(.mo-eyes) > path'), node => {
        const r = node.getBoundingClientRect(); return r.left + r.width / 2;
      });
      const contentLeft = id => document.querySelector('[data-message-id="' + id + '"] .channel-message-content').getBoundingClientRect().left;
      return { target: icon.left + icon.width / 2, centers, reply: contentLeft('reply'), continued: contentLeft('continued') };
    })()`);
    assert.equal(messageAlignment.centers.length, 2);
    assert(messageAlignment.centers.every(center => Math.abs(center - messageAlignment.target) < 1), JSON.stringify({ theme, width, ...messageAlignment }));
    assert.equal(messageAlignment.reply, messageAlignment.continued);

    for (const member of [false, true]) {
      if (member) {
        await win.webContents.executeJavaScript(`window.setScene('waiting', ['thinking'])`);
        await waitFor(win, `!!document.querySelector('.channel-response-status-avatar') && !document.querySelector('.channel-coordinator-mascot')`);
      }
      const centers = await win.webContents.executeJavaScript(`(() => {
        const icon = document.querySelector('.channel-attachment-button svg').getBoundingClientRect();
        const avatar = document.querySelector('${member ? '.channel-response-status-avatar' : '.channel-coordinator-mascot'} .mo-bob > g:not(.mo-eyes) > path').getBoundingClientRect();
        return { input: icon.left + icon.width / 2, avatar: avatar.left + avatar.width / 2 };
      })()`);
      assert(Math.abs(centers.input - centers.avatar) < 1, JSON.stringify({ theme, width, member, ...centers }));
      if (theme === "light" && width === 1100 && !member) {
        const footer = await win.webContents.executeJavaScript(`(() => {
          const r = document.querySelector('.channel-conversation-footer').getBoundingClientRect();
          return { x: 0, y: Math.floor(r.top), width: 240, height: Math.floor(innerHeight - r.top) };
        })()`);
        fs.writeFileSync(path.join(output, 'coordinator-alignment.png'), (await win.webContents.capturePage(footer)).toPNG());
      }
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
  for (const shape of ["round", "rounded-square", "capsule", "triangle", "diamond"]) {
    await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?state=waiting&avatar=mascot-v1:${shape}:none:140`);
    await waitFor(win, `!!document.querySelector('.channel-coordinator-member')`);
    const centers = await win.webContents.executeJavaScript(`(() => {
      const icon = document.querySelector('.channel-attachment-button svg').getBoundingClientRect();
      const body = document.querySelector('.channel-coordinator-member .mo-bob > g:not(.mo-eyes) > path').getBoundingClientRect();
      return { input: icon.left + icon.width / 2, body: body.left + body.width / 2 };
    })()`);
    assert(Math.abs(centers.input - centers.body) < 1, JSON.stringify({ shape, ...centers }));
    if (shape === "capsule") {
      const footer = await win.webContents.executeJavaScript(`(() => {
        const r = document.querySelector('.channel-conversation-footer').getBoundingClientRect();
        return { x: 0, y: Math.floor(r.top), width: innerWidth, height: Math.floor(innerHeight - r.top) };
      })()`);
      fs.writeFileSync(path.join(output, 'capsule-alignment.png'), (await win.webContents.capturePage(footer)).toPNG());
    }
  }
  console.log("PASS: avatar/title summaries, expandable details, shared centerline with the attachment button for coordinator and all five avatar shapes, light/dark at 390/1100/1600px");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
