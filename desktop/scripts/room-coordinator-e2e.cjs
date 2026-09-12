const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/room-activity-motion");
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
    await waitFor(win, `!!document.querySelector('.channel-activity-motion [role=status]')`);
    const activity = await win.webContents.executeJavaScript(`(() => {
      const node = document.querySelector('.channel-activity-motion [role=status]');
      const r = node.getBoundingClientRect();
      return {text: node.textContent, x:r.x, right:r.right, bottom:r.bottom, width:innerWidth, height:innerHeight, overflow:node.scrollWidth > node.clientWidth + 1};
    })()`);
    assert(activity.x >= 0 && activity.right <= activity.width && activity.bottom <= activity.height && !activity.overflow, "Coordination status must remain visible without overflow");
    assert(!activity.text.includes("private-room-session"));
    if (["working", "waiting", "queued"].includes(state)) assert.equal(activity.text, "", "Routine coordination must not display status text");
    if (state === "waiting") assert(await win.webContents.executeJavaScript(`!!document.querySelector('[data-agent-avatar-id="a0"]')`));
    if (state === "failed" || state === "waiting" || state === "working") {
      await waitFor(win, `Array.from(document.querySelectorAll('.channel-activity-slot')).every(node => node.getAnimations().every(animation => animation.playState !== 'running'))`);
      await win.webContents.executeJavaScript(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
      fs.writeFileSync(path.join(output, `${theme}-${width}-${state}.png`), (await win.webContents.capturePage()).toPNG());
    }
    if (state === "failed") {
      await win.webContents.executeJavaScript(`document.querySelector('.channel-coordinator-activity button').click()`);
      await waitFor(win, `window.retryCount === 1 && !!document.querySelector('.channel-activity-motion [role=status]') && !document.querySelector('.channel-coordinator-activity button')`);
      assert.equal(await win.webContents.executeJavaScript(`window.retryCount`), 1);
    }
  }
  for (const reduced of [false, true]) {
    await win.webContents.debugger.attach("1.3");
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: reduced ? "reduce" : "no-preference" }] });
    await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?state=working&theme=dark`);
    await waitFor(win, `!!document.querySelector('.channel-coordinator-mascot')`);
    await win.webContents.executeJavaScript(`window.setScene('waiting', ['thinking', 'queued'])`);
    await waitFor(win, `document.querySelectorAll('.channel-activity-slot:not([inert]) .channel-activity-inspect').length === 2`);
    await waitFor(win, `!document.querySelector('.channel-coordinator-mascot')`);
    const members = await win.webContents.executeJavaScript(`Array.from(document.querySelectorAll('.channel-activity-inspect'), node => {
      const copy = node.querySelector('.channel-activity-accessible');
      return { label: node.getAttribute('aria-label'), height: copy.getBoundingClientRect().height, title: node.title };
    })`);
    assert(members[0].label.includes("Alice") && members[1].label.includes("Bob"));
    assert(members.every(member => member.height <= 1 && member.title), "Member names belong in accessible labels and hover hints");
    assert.equal(await win.webContents.executeJavaScript(`document.querySelector('.channel-response-status-avatar').getAnimations().length > 0`), !reduced);
    fs.writeFileSync(path.join(output, `handoff-${reduced ? 'reduced' : 'animated'}.png`), (await win.webContents.capturePage()).toPNG());
    await win.webContents.executeJavaScript(`document.querySelector('.channel-activity-inspect').click()`);
    await waitFor(win, `window.lastReadSession === 'member-session-0'`);
    await win.webContents.executeJavaScript(`window.setScene('idle', [])`);
    await waitFor(win, `!document.querySelector('.channel-activity-inspect')`);
    await win.webContents.debugger.detach();
  }
  await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?state=idle&locale=en-US`);
  await waitFor(win, `!!document.querySelector('.channel-message-stream')`);
  assert.equal(await win.webContents.executeJavaScript(`!!document.querySelector('.channel-activity-motion [role=status]')`), false);
  console.log("PASS: text-free room activity, member handoff/inspection, exit, retry, reduced motion, both themes and 390/1100px layouts");
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
