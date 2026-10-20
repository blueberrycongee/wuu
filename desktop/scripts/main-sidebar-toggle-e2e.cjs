const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const root = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_SIDEBAR_TOGGLE_OUTPUT || path.join(root, "../.tmp/sidebar-main-toggle/verified"));
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
  // Opt-in diagnostic until this interruption case is verified on a native
  // desktop. Use real Chromium layout and trusted mouse input, never mocked
  // rectangles or pointerdown dispatched past CSS hit-testing restrictions.
  if (process.env.WUU_E2E_INTERRUPT_RESIZE === "true") {
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-reduced-motion", value: "no-preference" }],
    });
    const interruptions = [];
    for (const gesture of ["release", "outward", "collapse"]) {
    if (!(await js(collapsed))) { await clickToggle(); await wait(collapsed); await settled(); }
    await js(`document.documentElement.dataset.appearanceMotion='full';document.documentElement.style.setProperty('--sidebar-motion-duration','10000ms')`);
    await clickToggle();
    const before = await js(`(async () => {
      const shell = document.querySelector('.app-shell');
      for (let i = 0; i < 20; i++) {
        const animation = shell.getAnimations().find(item => item.transitionProperty === 'grid-template-columns');
        if (animation) {
          animation.pause();
          animation.currentTime = animation.effect.getComputedTiming().endTime * 0.25;
          const divider = document.querySelector('.sidebar-resizer');
          const rect = divider.getBoundingClientRect();
          const x = rect.left + rect.width / 2;
          const y = Math.min(rect.bottom - 20, Math.max(rect.top + 80, 180));
          const hit = document.elementFromPoint(x, y);
          return { x, y, targetWidth: parseFloat(getComputedStyle(shell).getPropertyValue('--sidebar-open-width')), sidebar: document.querySelector('.sidebar').getBoundingClientRect().toJSON(),
            divider: rect.toJSON(), hitsDivider: hit === divider || divider.contains(hit),
            animating: shell.classList.contains('sidebar-animating') };
        }
        await new Promise(requestAnimationFrame);
      }
      throw new Error('No actual opening grid transition found');
    })()`);
    assert(before.animating && before.hitsDivider, `Mid-flight divider must be reachable: ${JSON.stringify(before)}`);
    assert(Math.abs(before.x - before.sidebar.right) <= 2,
      `Divider must follow the painted sidebar boundary during opening: ${JSON.stringify(before)}`);
    const point = { x: Math.round(before.x), y: Math.round(before.y) };
    win.webContents.sendInputEvent({ type: "mouseMove", ...point });
    assert(await js(`document.querySelector('.app-shell').classList.contains('sidebar-animating')`), "Diagnostic timing invalid: opening transition settled before pointer input");
    win.webContents.sendInputEvent({ type: "mouseDown", ...point, button: "left", clickCount: 1 });
    await wait(`document.querySelector('.app-shell').classList.contains('resizing-sidebar')`);
    const after = await measure();
    const evidence = { gesture, before, after, jump: after.sidebar.width - before.sidebar.width };
    interruptions.push(evidence);
    const save = () => fs.writeFileSync(path.join(output, "interrupt-resize.json"), JSON.stringify(interruptions, null, 2));
    save();
    assert(Math.abs(evidence.jump) <= 2, `Pointer-down without movement must preserve rendered width: ${JSON.stringify(evidence)}`);
    if (gesture !== "release") {
      const delta = gesture === "outward" ? 12 : -12;
      win.webContents.sendInputEvent({ type: "mouseMove", x: point.x + delta, y: point.y, button: "left" });
      await js(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
      evidence.moved = await measure();
      save();
      if (gesture === "outward") {
        assert(Math.abs(evidence.moved.sidebar.width - before.sidebar.width - delta) <= 2,
          `Outward drag must track the pointer from its displayed width: ${JSON.stringify(evidence)}`);
      } else {
        assert(await js(collapsed), `An intentional inward drag below the collapse threshold still collapses: ${JSON.stringify(evidence)}`);
      }
    }
    // Restore the normal rhythm before release; the long duration above only
    // makes the captured interruption independent of slow CI scheduling.
    await js(`document.documentElement.style.setProperty('--sidebar-motion-duration','180ms')`);
    win.webContents.sendInputEvent({ type: "mouseUp", ...point, button: "left", clickCount: 1 });
    await settled();
    evidence.released = await measure();
    save();
    if (gesture === "release") {
      assert(Math.abs(evidence.released.sidebar.width - before.targetWidth) <= 2,
        `A press without dragging resumes the original width: ${JSON.stringify(evidence)}`);
    } else if (gesture === "outward") {
      assert(evidence.released.sidebar.width >= 240 && evidence.released.sidebar.width <= 520,
        `Released sidebar obeys its normal width constraints: ${JSON.stringify(evidence)}`);
    } else assert(await js(collapsed));
    }
    console.log("PASS: midpoint sidebar divider takeover, drag and release preserve continuity");
    win.destroy(); app.exit(0); return;
  }
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
