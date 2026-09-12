const { app, BrowserWindow, screen } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");

const output = path.resolve(__dirname, "../../artifacts/channel-inspector");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));

async function waitFor(win, expression, message) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (await win.webContents.executeJavaScript(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  console.error(await win.webContents.executeJavaScript(`(() => {
    const h = document.querySelector('.session-inspector-history');
    const turn = h?.querySelector('[data-turn-id="turn-2"]');
    return {scroll: h?.scrollTop, height:h?.clientHeight, total:h?.scrollHeight, turn:turn?.getBoundingClientRect().top, history:h?.getBoundingClientRect().top};
  })()`));
  throw new Error(message);
}

app.whenReady().then(async () => {
  const area = screen.getPrimaryDisplay().workArea;
  const width = Math.min(1100, area.width - 20);
  assert(width >= 760, "This inspector check needs at least 760px of display work area");
  const win = new BrowserWindow({
    show: false,
    x: area.x + area.width - width - 10,
    y: area.y + 10,
    width,
    height: Math.min(700, area.height - 20),
    webPreferences: { backgroundThrottling: false },
  });
  win.webContents.on("console-message", event => {
    if (event.level === "error") console.error(event.message);
  });

  const settleMotion = () => waitFor(win, `['.channel-conversation', '.session-inspector-extension'].every(selector => !document.querySelector(selector)?.getAnimations().some(animation => animation.playState === 'running'))`, "Panel motion must settle");
  const geometry = () => win.webContents.executeJavaScript(`(() => {
    const rect = selector => {
      const node = document.querySelector(selector);
      if (!node) return null;
      const r = node.getBoundingClientRect();
      return {x:r.x,y:r.y,width:r.width,height:r.height,right:r.right};
    };
    return {stream:rect('.channel-message-stream'),composer:rect('.channel-composer'),header:rect('.channel-room-header'),panelHeader:rect('.session-inspector-header'),flow:rect('.session-inspector-history > .conversation-width'),panel:rect('.session-inspector-extension'),width:innerWidth};
  })()`);

  for (const theme of ["light", "dark"]) {
    await win.loadURL(`http://127.0.0.1:5199/dev/channel-replies/index.html?theme=${theme}`);
    await waitFor(win, `document.querySelectorAll('.channel-activity-inspect').length === 2`, "Room fixture failed to mount");
    const bounds = win.getBounds();
    const before = await geometry();
    await win.webContents.executeJavaScript(`document.querySelectorAll('.channel-activity-inspect')[1].click()`);
    await waitFor(win, `!!document.querySelector('.session-inspector-extension')`, "Inspector failed to open");
    const enterOpacity = await win.webContents.executeJavaScript(`(() => {
      for (const motion of document.querySelector('.channel-conversation').getAnimations()) {
        motion.pause();
        motion.currentTime = Number(motion.effect.getTiming().duration) / 2;
      }
      const panel = document.querySelector('.session-inspector-extension');
      const animation = panel.getAnimations()[0];
      if (!animation) return -1;
      animation.pause();
      animation.currentTime = Number(animation.effect.getTiming().duration) / 2;
      return Number(getComputedStyle(panel).opacity);
    })()`);
    assert(enterOpacity > 0 && enterOpacity < 1, "The panel must fade in through an intermediate frame");
    await win.webContents.executeJavaScript(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
    fs.writeFileSync(path.join(output, `${theme}-trace-enter.png`), (await win.webContents.capturePage()).toPNG());
    const during = await geometry();
    assert(during.header.width < before.header.width && during.header.width > before.header.width - during.panel.width, "The chat width must interpolate while the panel enters");
    await win.webContents.executeJavaScript(`['.channel-conversation', '.session-inspector-extension'].forEach(selector => document.querySelector(selector).getAnimations().forEach(animation => animation.play()))`);
    await settleMotion();
    const opened = await geometry();
    assert.deepEqual(win.getBounds(), bounds, "Opening the inspector must not resize or move the native window");
    assert.equal(opened.panel.right, opened.width);
    assert(Math.abs(opened.header.right - opened.panel.x) < 1, "The reserved width must meet the panel without a blank gutter");
    assert.equal(opened.panelHeader.height, opened.header.height, "Both headers must have the same height");
    assert.equal(opened.panelHeader.y, opened.header.y);
    const spacing = await win.webContents.executeJavaScript(`(() => {
      const history = document.querySelector('.session-inspector-history');
      const flow = history.firstElementChild;
      return {historyRight: getComputedStyle(history).paddingRight, left: getComputedStyle(flow).paddingLeft, right: getComputedStyle(flow).paddingRight};
    })()`);
    assert.equal(spacing.historyRight, "0px", "The trace must not reserve space for another panel");
    assert.equal(spacing.left, spacing.right, "Trace content needs symmetric gutters");
    assert(opened.panel.width <= 600 && opened.panel.width >= 420);
    assert(opened.stream.width < before.stream.width, "Wide layouts should dock the inspector beside the room");
    assert.equal(await win.webContents.executeJavaScript(`window.lastReadSession`), "a1");
    assert.equal(await win.webContents.executeJavaScript(`document.querySelectorAll('.session-inspector-extension .turn').length`), 24);

    await win.webContents.executeJavaScript(`window.stage=1; window.emitSession('a1')`);
    await waitFor(win, `document.querySelector('.session-inspector-extension').textContent.includes('实时输出版本 1')`, "Live session update did not render");
    await waitFor(win, `(() => { const node = document.querySelector('.session-inspector-history'); return node.scrollHeight-node.scrollTop-node.clientHeight < 2; })()`, "Live output and image layout must keep following the latest content");
    fs.writeFileSync(path.join(output, `${theme}-adaptive-panel.png`), (await win.webContents.capturePage()).toPNG());

    await win.webContents.executeJavaScript(`document.querySelector('.channel-sessions-close').click()`);
    await waitFor(win, `!document.querySelector('.session-inspector-extension')`, "Inspector failed to close");
    assert.deepEqual(win.getBounds(), bounds);
    const closed = await geometry();
    assert.equal(closed.stream.width, before.stream.width);
    await win.webContents.executeJavaScript(`window.stage=4`);
    await waitFor(win, `document.querySelectorAll('.channel-agent-hover-trigger').length === 2`, "Published replies need bot cards");
    assert.equal(await win.webContents.executeJavaScript(`document.querySelector('.channel-message-stream').textContent.includes('查看轨迹')`), false);
    const avatarPoint = await win.webContents.executeJavaScript(`(() => { const a = document.querySelector('.channel-agent-hover-trigger'); a.scrollIntoView({block:'center'}); const r = a.getBoundingClientRect(); return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}; })()`);
    win.webContents.sendInputEvent({ type: "mouseMove", ...avatarPoint });
    await waitFor(win, `!!document.querySelector('.channel-agent-hover-card')`, "Hovering the avatar opens its card");
    const actionPoint = await win.webContents.executeJavaScript(`(() => { const r = document.querySelector('.channel-agent-view-trace').getBoundingClientRect(); return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}; })()`);
    win.webContents.sendInputEvent({ type: "mouseMove", ...actionPoint });
    await new Promise(resolve => setTimeout(resolve, 250));
    assert(await win.webContents.executeJavaScript(`!!document.querySelector('.channel-agent-hover-card')`), "The card stays open while moving into its action");
    fs.writeFileSync(path.join(output, `${theme}-bot-hover.png`), (await win.webContents.capturePage()).toPNG());
    await win.webContents.executeJavaScript(`document.querySelector('.channel-agent-view-trace').click()`);
    await waitFor(win, `!document.querySelector('.channel-agent-hover-card')`, "Inspecting closes the hover card");
    await waitFor(win, `!!document.querySelector('.session-inspector-extension [data-turn-id="turn-2"]')`, "Original turn must render");
    await waitFor(win, `(() => {
      const history = document.querySelector('.session-inspector-history');
      const turn = history.querySelector('[data-turn-id="turn-2"]');
      return Math.abs(turn.getBoundingClientRect().top - history.getBoundingClientRect().top - 16) < 2;
    })()`, "Published reply must position its original turn, not the latest turn");
    const oldPosition = await win.webContents.executeJavaScript(`document.querySelector('.session-inspector-history').scrollTop`);
    await win.webContents.executeJavaScript(`window.stage=5; window.emitSession('a0')`);
    await waitFor(win, `document.querySelector('.session-inspector-extension').textContent.includes('实时输出版本 5')`, "History must stay live");
    assert.equal(await win.webContents.executeJavaScript(`document.querySelector('.session-inspector-history').scrollTop`), oldPosition, "Incoming output must not move historical reading position");
    await win.webContents.executeJavaScript(`document.querySelector('.session-inspector-latest').click()`);
    await waitFor(win, `(() => { const node = document.querySelector('.session-inspector-history'); return node.scrollHeight-node.scrollTop-node.clientHeight < 2; })()`, "Jump to latest must reach bottom");
    await win.webContents.executeJavaScript(`window.stage=0`);

  }

  for (const theme of ["light", "dark"]) for (const size of [900, 640, 390]) {
    win.setBounds({ x: area.x + 10, y: area.y + 10, width: size, height: Math.min(700, area.height - 20) });
    await win.loadURL(`http://127.0.0.1:5199/dev/channel-replies/index.html?theme=${theme}`);
    await waitFor(win, `document.querySelectorAll('.channel-activity-inspect').length === 2`, "Narrow room fixture failed to mount");
    await win.webContents.executeJavaScript(`document.querySelectorAll('.channel-activity-inspect')[1].click()`);
    await waitFor(win, `!!document.querySelector('.session-inspector-extension')`, "Narrow inspector failed to open");
    await settleMotion();
    const narrow = await geometry();
    assert.equal(narrow.panel.x, 0);
    assert.equal(narrow.panel.width, narrow.width);
    assert.equal(narrow.panelHeader.height, narrow.header.height);
    assert.equal(await win.webContents.executeJavaScript(`document.querySelector('.channel-room-main').inert`), true);
    fs.writeFileSync(path.join(output, `${theme}-trace-${size}.png`), (await win.webContents.capturePage()).toPNG());
    await win.webContents.executeJavaScript(`document.querySelector('.channel-sessions-close').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))`);
    await waitFor(win, `!document.querySelector('.session-inspector-extension')`, "Escape must return to chat");
    assert.equal(await win.webContents.executeJavaScript(`document.querySelector('.channel-room-main').inert`), false);
  }

  console.log("PASS: inspector opens inside wide, edge-aligned, and narrow windows without native resizing; live output and close are reversible.");
  win.destroy();
  app.quit();
}).catch(error => {
  console.error(error);
  app.exit(1);
});
