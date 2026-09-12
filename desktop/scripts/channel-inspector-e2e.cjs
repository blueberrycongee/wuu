const { app, BrowserWindow, ipcMain, screen } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const { transformSync } = require("esbuild");
const output = path.resolve(__dirname, "../../artifacts/channel-inspector");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
const nativeModule = { exports: {} };
new Function("exports", "require", "module", transformSync(fs.readFileSync(path.resolve(__dirname, "../src/main/sessionInspectorExpansion.ts"), "utf8"), { loader: "ts", format: "cjs" }).code)(nativeModule.exports, require, nativeModule);
app.whenReady().then(async () => {
  const controller = nativeModule.exports.createSessionInspectorExpansion(bounds => screen.getDisplayMatching(bounds).workArea);
  const area = screen.getPrimaryDisplay().workArea;
  const width = Math.min(900, area.width - 500);
  assert(width >= 760, "This native expansion check needs at least 1260px of display work area");
  const win = new BrowserWindow({ show: false, x: area.x + 10, y: area.y + 10, width, height: 700,
    webPreferences: { preload: path.join(__dirname, "session-expansion-preload.cjs"), backgroundThrottling: false } });
  const other = new BrowserWindow({show: false, x: area.x + 20, y: area.y + 20, width: 400, height: 300});
  win.webContents.on("console-message", event => { if (event.level === "error") console.error(event.message); });
  const otherBounds = other.getBounds();
  ipcMain.handle("test:session-expansion", (event, params) => { assert.equal(event.sender.id, win.webContents.id); return controller.set(win, params); });
  const geometry = () => win.webContents.executeJavaScript(`(() => {
    const rect = selector => { const r=document.querySelector(selector).getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height}; };
    return {stream:rect('.channel-message-stream'),composer:rect('.channel-composer'),header:rect('.channel-room-header')};
  })()`);
  for (const theme of ["light", "dark"]) {
    await win.loadURL(`http://127.0.0.1:5199/dev/channel-replies/index.html?theme=${theme}`);
    for (let attempt = 0; attempt < 100; attempt++) {
      if (await win.webContents.executeJavaScript(`document.querySelectorAll('.channel-activity-inspect').length === 2`)) break;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    assert(await win.webContents.executeJavaScript(`document.querySelectorAll('.channel-activity-inspect').length === 2`), "Room fixture failed to mount");
    const before = await geometry(); const bounds = win.getBounds();
    await win.webContents.executeJavaScript(`document.querySelectorAll('.channel-activity-inspect')[1].click()`);
    await new Promise(resolve => setTimeout(resolve, 600));
    assert.deepEqual(win.getBounds(), {...bounds, width: bounds.width + 480});
    assert.deepEqual(other.getBounds(), otherBounds);
    assert.deepEqual(await geometry(), before, "The original workspace must not move or resize");
    const state = await win.webContents.executeJavaScript(`(() => {
      const panel=document.querySelector('.session-inspector-extension'); const r=panel.getBoundingClientRect();
      return {session:window.lastReadSession,turns:panel.querySelectorAll('.turn').length,left:r.left,right:r.right,width:innerWidth,modal:!!document.querySelector('[role=dialog]')};
    })()`);
    assert.equal(state.session, "a1"); assert.equal(state.turns, 24); assert(!state.modal);
    assert.equal(state.left, before.stream.width); assert.equal(state.right, state.width);
    const folded = await win.webContents.executeJavaScript(`(() => {
      const panel = document.querySelector('.session-inspector-extension');
      const fold = panel.querySelector('.tool-result-data details');
      const notice = panel.querySelector('.context-compaction-notice');
      return {hasFold:!!fold, open:fold?.open, raw:panel.textContent.includes('Raw message 99'),
        failureRole:notice?.getAttribute('role'), failureOpen:notice?.querySelector('details')?.open};
    })()`);
    assert(folded.hasFold); assert(!folded.open); assert(!folded.raw);
    assert.equal(folded.failureRole, "alert"); assert(!folded.failureOpen);
    await win.webContents.executeJavaScript(`document.querySelector('.tool-result-data summary').click()`);
    await new Promise(resolve => setTimeout(resolve, 250));
    const expanded = await win.webContents.executeJavaScript(`(() => {
      const body=document.querySelector('.tool-result-data .process-surface-body');
      return {messages:JSON.parse(body.querySelector('pre').textContent).messages.length,height:body.getBoundingClientRect().height,limit:innerHeight/2};
    })()`);
    assert.equal(expanded.messages, 100); assert(expanded.height <= expanded.limit + 1);
    await win.webContents.executeJavaScript(`document.querySelector('.tool-result-data summary').click()`);
    const appearance = await win.webContents.executeJavaScript(`(() => {
      const panel = document.querySelector('.session-inspector-extension');
      const reference = panel.cloneNode(true);
      reference.className = 'conversation-pane';
      reference.style.cssText = 'position:fixed;left:-10000px;top:0;width:' + panel.getBoundingClientRect().width + 'px;height:600px';
      reference.querySelector('.session-inspector-history').className = 'scroll-region';
      document.body.append(reference);
      const sample = root => {
        const surface = getComputedStyle(root);
        const flow = getComputedStyle(root.querySelector('.conversation-width'));
        const turn = getComputedStyle(root.querySelector('.turn'));
        return {background:surface.backgroundColor, queryReplyGap:surface.getPropertyValue('--conversation-user-rule-gap'),
          top:flow.paddingTop, left:flow.paddingLeft, right:flow.paddingRight, gap:turn.gap, boundary:turn.marginBottom};
      };
      const result = {inspector:sample(panel), harness:sample(reference)};
      reference.remove(); return result;
    })()`);
    assert.deepEqual(appearance.inspector, appearance.harness, "Inspector must inherit Harness surface and message spacing");
    await win.webContents.executeJavaScript(`window.stage=1; window.emitSession('a1')`);
    await new Promise(resolve => setTimeout(resolve, 500));
    assert(await win.webContents.executeJavaScript(`document.querySelector('.session-inspector-extension').textContent.includes('实时输出版本 1')`));
    fs.writeFileSync(path.join(output, `${theme}-native-extension.png`), (await win.webContents.capturePage()).toPNG());
    await win.webContents.executeJavaScript(`document.querySelector('.channel-sessions-close').click()`);
    await new Promise(resolve => setTimeout(resolve, 300));
    assert.deepEqual(win.getBounds(), bounds);
    assert.deepEqual(await geometry(), before);
  }
  const edgeBounds = { ...win.getBounds(), x: area.x + area.width - win.getBounds().width - 50 };
  win.setBounds(edgeBounds);
  const edgeGeometry = await geometry();
  await win.webContents.executeJavaScript(`document.querySelectorAll('.channel-activity-inspect')[1].click()`);
  await new Promise(resolve => setTimeout(resolve, 300));
  assert.deepEqual(win.getBounds(), edgeBounds);
  assert.deepEqual(await geometry(), edgeGeometry);
  assert.deepEqual(other.getBounds(), otherBounds);
  assert(await win.webContents.executeJavaScript(`!document.querySelector('.session-inspector-extension')`));
  console.log("PASS: native right-edge expansion; original workspace and other window unchanged; Harness history, live output and reversible close.");
  other.destroy(); win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
