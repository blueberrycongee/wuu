const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const root = path.resolve(__dirname, "..");
const output = path.resolve(root, "../.tmp/sidebar-main-toggle/verified");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1200, height: 820, show: false, titleBarStyle: "hiddenInset", webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, sandbox: false, backgroundThrottling: false,
  } });
  win.webContents.debugger.attach("1.3");
  win.webContents.on("did-finish-load", () => void win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true }));
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  const js = expression => win.webContents.executeJavaScript(expression);
  const toggle = '[data-wuu-component="sidebar-toggle"]';
  const collapsed = `document.querySelector('.app-shell').classList.contains('sidebar-collapsed')`;
  const drawerOpen = `document.querySelector('.app-shell').classList.contains('sidebar-drawer-open')`;
  async function wait(expression) {
    for (let i = 0; i < 180; i++) {
      if (await js(expression)) return;
      await new Promise(resolve => setTimeout(resolve, 30));
    }
    throw new Error(`Timed out: ${expression}\n${JSON.stringify(await measure())}\n${errors.join("\n")}`);
  }
  const measure = () => js(`(() => {
    const shell = document.querySelector('.app-shell');
    const sidebar = document.querySelector('.sidebar').getBoundingClientRect();
    const button = document.querySelector('${toggle}');
    const rect = button?.getBoundingClientRect();
    const hit = rect && document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
    return { width: window.innerWidth, mode: shell.dataset.wuuSidebarMode,
      sidebar: sidebar.toJSON(), reserved: parseFloat(shell.style.getPropertyValue('--sidebar-width')),
      savedCollapsed: localStorage.getItem('wuu.desktop.sidebarCollapsed'),
      savedWidth: localStorage.getItem('wuu.desktop.sidebarWidth'),
      button: rect?.toJSON(), hitsButton: !!button && (hit === button || button.contains(hit)),
      focusVisible: button?.matches(':focus-visible'),
      titlebar: !!document.querySelector('[data-wuu-component="conversation-titlebar"]') };
  })()`);
  async function settled() {
    await wait(`!document.querySelector('.sidebar-animating, .sidebar-drawer-closing, .sidebar-drawer-docking, .window-resizing')`);
    await js(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
  }
  async function resize(width) {
    win.setContentSize(width, 820);
    await wait(`window.innerWidth === ${width}`);
    await settled();
  }
  async function clickToggle() {
    const { button, hitsButton } = await measure();
    assert(hitsButton, "Sidebar toggle must own its hit target");
    const point = { x: Math.round(button.x + button.width / 2), y: Math.round(button.y + button.height / 2) };
    win.webContents.sendInputEvent({ type: "mouseDown", ...point, button: "left", clickCount: 1 });
    win.webContents.sendInputEvent({ type: "mouseUp", ...point, button: "left", clickCount: 1 });
  }
  function key(keyCode, modifiers = []) {
    win.webContents.sendInputEvent({ type: "keyDown", keyCode, modifiers });
    if (keyCode === "Enter") win.webContents.sendInputEvent({ type: "char", keyCode: "\r" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
  }
  const away = () => win.webContents.sendInputEvent({ type: "mouseMove", x: 580, y: 700 });
  const report = [];
  await win.loadFile(path.join(root, "out/renderer/index.html"));
  await wait(`!!document.querySelector('${toggle}')`);
  for (const theme of ["light", "dark"]) for (const font of [14, 20]) {
    await js(`document.documentElement.dataset.theme='${theme}';document.documentElement.style.setProperty('--font-ui','${font}px')`);
    await resize(1200);
    if (await js(collapsed)) { await clickToggle(); await wait(`!(${collapsed})`); await settled(); }
    const original = await measure();
    for (const width of [820, 760, 759, 600, 440, 1200]) {
      await resize(width);
      const current = await measure();
      assert.equal(current.mode, "docked", JSON.stringify(current));
      assert.equal(current.reserved, original.reserved);
      assert.equal(current.savedWidth, original.savedWidth);
      assert.equal(current.savedCollapsed, "false");
      assert(Math.abs(current.sidebar.width - original.sidebar.width) < 1, JSON.stringify(current));
      assert(current.sidebar.x >= 0 && current.sidebar.right <= width, JSON.stringify(current));
      assert(current.titlebar && current.hitsButton, JSON.stringify(current));
      if (width === 600 || width === 440 || width === 1200) {
        fs.writeFileSync(path.join(output, `${theme}-${font}-${width}-docked.png`), (await win.webContents.capturePage()).toPNG());
      }
      report.push({ theme, font, ...current });
    }
    // Explicitly collapsed navigation stays collapsed across the same widths.
    await clickToggle(); await wait(collapsed); await settled(); away();
    for (const width of [600, 440, 1200, 600]) {
      await resize(width);
      assert(await js(collapsed));
      const current = await measure();
      assert.equal(current.reserved, 0);
      assert.equal(current.savedCollapsed, "true");
      report.push({ theme, font, ...current });
    }
    const beforeHover = await measure();
    win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(beforeHover.button.x + beforeHover.button.width / 2), y: Math.round(beforeHover.button.y + beforeHover.button.height / 2) });
    await wait(drawerOpen); await settled();
    const hover = await measure();
    assert(hover.hitsButton, JSON.stringify(hover));
    await clickToggle(); await wait(`!(${collapsed})`); await settled();
    away(); await js(`document.querySelector('${toggle}').focus()`);
    key("Enter"); await wait(collapsed); await settled();
    await js(`document.querySelector('${toggle}').focus()`);
    key("Enter"); await wait(`!(${collapsed})`); await settled();
    report.push({ theme, font, hover, keyboard: await measure() });
  }
  assert.equal(errors.length, 0, errors.join("\n"));
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ report, errors }, null, 2));
  console.log(`PASS: sidebar preference, rendered width, pointer and keyboard controls across ${report.length} scenarios. Evidence: ${output}`);
  win.destroy(); app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
