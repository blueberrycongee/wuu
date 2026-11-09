// Build the renderer, then run with Electron. WUU_SIDEBAR_RENDERER_URL can
// target the current Dev server instead. The backend and profile are disposable.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_SIDEBAR_HOVER_OUTPUT || path.join(desktop, "out/e2e/sidebar-hover"));
fs.mkdirSync(output, { recursive: true });
const scratch = path.join(desktop, "out/e2e/sidebar-hover");
fs.mkdirSync(scratch, { recursive: true });
const profile = fs.mkdtempSync(path.join(scratch, "profile-"));
app.setPath("userData", profile);
// Exercise renderer-owned chrome even when the runner is not on macOS.
const extension = path.join(profile, "bridge.cjs");
fs.writeFileSync(extension, 'module.exports = api => ({ ...api, platform: "darwin", windowToggleFullScreen: async () => {} });');
process.env.WUU_RENDERER_E2E_EXTENSION = extension;
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn) {
  await win.webContents.executeJavaScript(`(async () => {
    const predicate = (${fn});
    const deadline = performance.now() + 10000;
    while (!predicate()) {
      if (performance.now() > deadline) throw new Error("Timed out waiting for sidebar state");
      await new Promise(requestAnimationFrame);
    }
  })()`);
}
async function geometry(win) {
  return evaluate(win, () => {
    const box = selector => document.querySelector(selector)?.getBoundingClientRect().toJSON();
    return {
      toggle: box(".section-sidebar-toggle"),
      icon: box(".section-sidebar-toggle svg"),
      title: box(".conversation-title-heading"),
      newConversation: box(".conversation-title-heading button"),
    };
  });
}
async function pointer(win, x, y) {
  await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
}
const results = [];
const timeout = setTimeout(() => { console.error("Sidebar hover test timed out"); app.exit(1); }, 90000);
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1180, height: 820, show: false,
    webPreferences: { preload: path.join(__dirname, "resize-e2e-preload.cjs"), sandbox: false, backgroundThrottling: false } });
  win.webContents.debugger.attach("1.3");
  if (process.env.WUU_SIDEBAR_RENDERER_URL) await win.loadURL(process.env.WUU_SIDEBAR_RENDERER_URL);
  else await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await waitFor(win, () => document.querySelector(".sidebar-ribbon") && document.querySelector(".mac-window-controls"));
  for (const scenario of [
    { theme: "light", font: 14, width: 1180, zoom: 1 },
    { theme: "dark", font: 20, width: 820, zoom: 1.25 },
  ]) {
    win.setContentSize(scenario.width, 820);
    win.webContents.setZoomFactor(scenario.zoom);
    await evaluate(win, ({ theme, font, zoom }) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--font-ui", `${font}px`);
      document.documentElement.style.setProperty("--desktop-page-zoom", String(zoom));
      if (!document.querySelector(".app-shell.sidebar-collapsed")) document.querySelector(".section-sidebar-toggle").click();
    }, scenario);
    await pointer(win, 500 / scenario.zoom, 500 / scenario.zoom);
    await waitFor(win, () => !document.querySelector(".sidebar-animating, .sidebar-drawer-open, .sidebar-drawer-closing"));
    const before = await geometry(win);
    const x = before.toggle.x + before.toggle.width / 2;
    const y = before.toggle.y + before.toggle.height / 2;
    await pointer(win, x, y);
    await waitFor(win, () => document.querySelector(".sidebar-drawer-open"));
    await evaluate(win, async () => {
      const animations = document.querySelector(".sidebar").getAnimations({ subtree: true });
      await Promise.all(animations.filter(animation =>
        Number.isFinite(animation.effect.getComputedTiming().endTime)
      ).map(animation => animation.finished.catch(() => {})));
    });
    const opened = await geometry(win);
    fs.writeFileSync(path.join(output, `${scenario.theme}-hover.png`), (await win.webContents.capturePage()).toPNG());
    await pointer(win, 500 / scenario.zoom, 500 / scenario.zoom);
    await waitFor(win, () => !document.querySelector(".sidebar-drawer-open, .sidebar-drawer-closing"));
    const closed = await geometry(win);
    results.push({ scenario, before, opened, closed });
    fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
    fs.writeFileSync(path.join(output, `${scenario.theme}-closed.png`), (await win.webContents.capturePage()).toPNG());
    for (const state of [opened, closed]) {
      for (const target of Object.keys(before)) {
        assert.ok(before[target] && state[target], `${target} must remain mounted`);
        for (const axis of ["x", "y", "width", "height"]) {
          assert.ok(Math.abs(state[target][axis] - before[target][axis]) < 0.1,
            `Hover moved ${target}.${axis}: ${before[target][axis]} -> ${state[target][axis]}`);
        }
      }
    }
  }
  console.log(`PASS sidebar hover layout: ${output}`);
  clearTimeout(timeout);
  app.quit();
}).catch(error => { console.error(error); clearTimeout(timeout); app.exit(1); });
