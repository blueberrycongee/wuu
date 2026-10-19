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
    const group = button?.parentElement;
    const chrome = document.querySelector('[data-wuu-component="window-chrome"]');
    const lowerBoxes = [...document.querySelectorAll('.app-shell, .sidebar, .sidebar-hover-zone, .sidebar-resizer, .workspace-right-panel, .workspace-right-panel-resizer')]
      .map(element => ({ target: element.className, bounds: element.getBoundingClientRect().toJSON() }))
      .filter(element => element.bounds.height > 0);
    const historyControls = [...group.querySelectorAll('button')].map(control => {
      const bounds = control.getBoundingClientRect();
      const target = document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
      return { id: control.dataset.wuuComponent, bounds: bounds.toJSON(),
        hit: target === control || control.contains(target),
        noDrag: getComputedStyle(control).getPropertyValue('-webkit-app-region') === 'no-drag',
        visible: control.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }) };
    });
    const hit = rect && document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
    return { width: window.innerWidth, mode: shell.dataset.wuuSidebarMode,
      sidebar: sidebar.toJSON(), reserved: parseFloat(shell.style.getPropertyValue('--sidebar-width')),
      savedCollapsed: localStorage.getItem('wuu.desktop.sidebarCollapsed'),
      savedWidth: localStorage.getItem('wuu.desktop.sidebarWidth'),
      button: rect?.toJSON(), hitsButton: !!button && (hit === button || button.contains(hit)),
      focusVisible: button?.matches(':focus-visible'), focused: document.activeElement === button,
      sameButton: button === window.sidebarToggleUnderTest,
      buttonCount: document.querySelectorAll('${toggle}').length,
      chrome: chrome?.getBoundingClientRect().toJSON(), lowerBoxes,
      shellOwned: !!chrome && chrome.contains(group) && chrome.parentElement === shell.parentElement && !shell.contains(chrome),
      sameGroup: group === window.pageHistoryGroupUnderTest,
      historyControls,
      groupDraggable: getComputedStyle(group).getPropertyValue('-webkit-app-region') === 'drag',
      outsideHiddenContent: !button?.closest('aside, [inert], .app-shell, .conversation-pane'),
      sidebarVisible: document.querySelector('.sidebar').checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }),
      titlebar: !!document.querySelector('[data-wuu-component="conversation-titlebar"]') };
  })()`);
  function assertHistoryControls(state, original) {
    const details = JSON.stringify(state);
    assert.deepEqual(state.historyControls.map(control => control.id), ['page-history-back', 'page-history-forward', 'sidebar-toggle']);
    assert(state.shellOwned && state.outsideHiddenContent && state.sameGroup && state.groupDraggable, details);
    assert(state.chrome && Math.abs(state.chrome.left) < 0.1 && Math.abs(state.chrome.right - state.width) < 0.1
      && Math.abs(state.chrome.top) < 0.1 && state.chrome.height > 0, `Chrome must span the window above content: ${details}`);
    for (const layer of state.lowerBoxes) assert(layer.bounds.top >= state.chrome.bottom - 0.1,
      `Content, drawer, hover and resize boxes must begin below chrome: ${details}`);
    for (const [index, control] of state.historyControls.entries()) {
      const box = control.bounds;
      assert(control.hit && control.noDrag && control.visible && box.width > 0 && box.height > 0, details);
      assert(box.left >= 0 && box.right <= state.width && box.top >= state.chrome.top && box.bottom <= state.chrome.bottom + 0.1, details);
      if (index > 0) assert(state.historyControls[index - 1].bounds.right <= box.left, details);
      if (original) for (const axis of ['x', 'y']) {
        assert(Math.abs(box[axis] - original.historyControls[index].bounds[axis]) < 0.1, `History controls moved: ${details}`);
      }
    }
  }
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
    await js(`window.sidebarToggleUnderTest = document.querySelector('${toggle}'); window.pageHistoryGroupUnderTest = window.sidebarToggleUnderTest.parentElement`);
    const original = await measure();
    assertHistoryControls(original);
    assert.equal(original.buttonCount, 1);
    assert(original.shellOwned && original.outsideHiddenContent, JSON.stringify(original));
    for (const width of [820, 760, 759, 600, 440, 1200]) {
      await resize(width);
      const current = await measure();
      assertHistoryControls(current, original);
      assert.equal(current.mode, "docked", JSON.stringify(current));
      assert(current.sameButton && current.shellOwned && current.outsideHiddenContent, JSON.stringify(current));
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
      assertHistoryControls(current, original);
      assert.equal(current.reserved, 0);
      assert.equal(current.buttonCount, 1);
      assert(current.sameButton && current.hitsButton && current.outsideHiddenContent, JSON.stringify(current));
      assert(current.sidebar.right <= 0 || !current.sidebarVisible,
        `Collapsed sidebar must be fully hidden: ${JSON.stringify(current)}`);
      assert.equal(current.savedCollapsed, "true");
      if (width === 440 || width === 1200) fs.writeFileSync(path.join(output, `${theme}-${font}-${width}-collapsed.png`),
        (await win.webContents.capturePage()).toPNG());
      report.push({ theme, font, ...current });
    }
    const beforeHover = await measure();
    win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(beforeHover.button.x + beforeHover.button.width / 2), y: Math.round(beforeHover.button.y + beforeHover.button.height / 2) });
    // Observe longer than the hover-reveal delay: toolbar hover must never reveal content.
    for (let sample = 0; sample < 12; sample++) {
      await new Promise(resolve => setTimeout(resolve, 40));
      assert(!(await js(drawerOpen)), "Hovering the top toolbar must not reveal the drawer");
    }
    win.webContents.sendInputEvent({ type: "mouseMove", x: 1, y: Math.round(beforeHover.chrome.bottom / 2) });
    for (let sample = 0; sample < 12; sample++) {
      await new Promise(resolve => setTimeout(resolve, 40));
      assert(!(await js(drawerOpen)), "The window edge above the sidebar must not reveal the drawer");
    }
    const edge = await js(`(() => { const box = document.querySelector('.sidebar-hover-zone').getBoundingClientRect();
      return { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + Math.min(20, box.height / 2)) }; })()`);
    win.webContents.sendInputEvent({ type: "mouseMove", ...edge });
    await wait(drawerOpen); await settled();
    const hover = await measure();
    assertHistoryControls(hover, original);
    assert(hover.hitsButton, JSON.stringify(hover));
    fs.writeFileSync(path.join(output, `${theme}-${font}-below-chrome-drawer.png`), (await win.webContents.capturePage()).toPNG());
    await clickToggle(); await wait(`!(${collapsed})`); await settled();
    away(); await js(`document.querySelector('${toggle}').focus()`);
    key("Enter"); await wait(collapsed); await settled();
    const keyboardCollapsed = await measure();
    assertHistoryControls(keyboardCollapsed, original);
    assert(keyboardCollapsed.sameButton && keyboardCollapsed.focused, "Collapse must preserve the focused DOM button");
    assert(keyboardCollapsed.sidebar.right <= 0 || !keyboardCollapsed.sidebarVisible,
      `A focused collapse toggle must not keep the sidebar exposed: ${JSON.stringify(keyboardCollapsed)}`);
    assert(keyboardCollapsed.hitsButton && keyboardCollapsed.outsideHiddenContent, JSON.stringify(keyboardCollapsed));
    for (const axis of ["x", "y"]) assert(Math.abs(keyboardCollapsed.button[axis] - original.button[axis]) < 0.1,
      `Collapse must not move toggle ${axis}`);
    // A second keypress must work without focusing a replacement control.
    key("Enter"); await wait(`!(${collapsed})`); await settled();
    const keyboardDocked = await measure();
    assertHistoryControls(keyboardDocked, original);
    assert(keyboardDocked.sameButton && keyboardDocked.focused && keyboardDocked.hitsButton,
      "Docking must retain the same focused, hittable button");
    for (const axis of ["x", "y"]) assert(Math.abs(keyboardDocked.button[axis] - keyboardCollapsed.button[axis]) < 0.1,
      `Docking must not move toggle ${axis}`);
    report.push({ theme, font, keyboardCollapsed, keyboardDocked });
    report.push({ theme, font, hover, keyboard: await measure() });
  }
  // Globalized tools retain drawer-only navigation, underneath the independent chrome.
  await resize(1200);
  await js(`document.querySelector('[data-wuu-component="right-sidebar-toggle"]').click()`);
  await wait(`!!document.querySelector('.workspace-panel-globalize')`);
  await js(`document.querySelector('.workspace-panel-globalize').click()`);
  await wait(`document.querySelector('.app-shell').classList.contains('right-panel-globalized')`);
  await settled();
  if (!(await js(collapsed))) {
    await clickToggle(); await wait(collapsed); await settled();
  }
  const globalized = await measure();
  assertHistoryControls(globalized);
  await clickToggle(); await wait(drawerOpen); await settled();
  const globalizedDrawer = await measure();
  assertHistoryControls(globalizedDrawer, globalized);
  assert(await js(collapsed), "Globalized navigation must remain drawer-only");
  win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(globalizedDrawer.button.x + globalizedDrawer.button.width / 2),
    y: Math.round(globalizedDrawer.button.y + globalizedDrawer.button.height / 2) });
  for (let sample = 0; sample < 12; sample++) {
    await new Promise(resolve => setTimeout(resolve, 40));
    assert(await js(drawerOpen), "A click-opened globalized drawer must survive pointer movement over its toggle");
  }
  win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(globalizedDrawer.button.x + globalizedDrawer.button.width / 2),
    y: Math.round(globalizedDrawer.chrome.bottom - 1) });
  await js(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
  assert(await js(drawerOpen), "Crossing the vertical chrome gap above the rail must preserve the click-opened drawer");
  win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(globalizedDrawer.sidebar.x + 50),
    y: Math.round(globalizedDrawer.sidebar.top + 80) });
  await js(`new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))`);
  assert(await js(drawerOpen), "Moving from the clicked toggle into its drawer must preserve navigation");
  fs.writeFileSync(path.join(output, "globalized-below-chrome-drawer.png"), (await win.webContents.capturePage()).toPNG());
  win.webContents.sendInputEvent({ type: "mouseMove", x: 600, y: Math.round(globalizedDrawer.chrome.bottom / 2) });
  await wait(`!(${drawerOpen})`); await settled();
  report.push({ globalized, globalizedDrawer });
  assert.equal(errors.length, 0, errors.join("\n"));
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ report, errors }, null, 2));
  console.log(`PASS: sidebar preference, rendered width, pointer and keyboard controls across ${report.length} scenarios. Evidence: ${output}`);
  win.destroy(); app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
