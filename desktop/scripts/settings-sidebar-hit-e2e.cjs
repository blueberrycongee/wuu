const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { ownsHeaderSample } = require("./header-hit-ownership.cjs");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.resolve(process.env.WUU_TITLEBAR_RENDERER_HTML || path.join(desktopRoot, "out", "renderer", "index.html"));
let preload = path.join(__dirname, "resize-e2e-preload.cjs");
const evidenceDir = path.resolve(process.env.WUU_TITLEBAR_OUTPUT || path.join(desktopRoot, "out", "e2e", "settings-titlebar"));
const userData = path.join(evidenceDir, "settings-sidebar-hit-user-data");
const fixturePlatform = process.env.WUU_TITLEBAR_FIXTURE_PLATFORM || process.platform;
const focusedControls = process.env.WUU_TITLEBAR_FOCUSED_CONTROLS === "1";
if (focusedControls) app.enableSandbox();
assert.ok(["darwin", "linux", "win32"].includes(fixturePlatform), "Unsupported fixture platform");
// This flag enables self-contained caption capabilities in the existing preload,
// including when Electron sandboxing forbids dynamic local module requires.
process.env.WUU_TITLEBAR_FIXTURE_PLATFORM = fixturePlatform;

process.env.WUU_RESIZE_E2E_CWD = repoRoot;
fs.rmSync(userData, { recursive: true, force: true });
fs.mkdirSync(userData, { recursive: true });
app.setPath("userData", userData);
if (focusedControls) {
  const source = fs.readFileSync(preload, "utf8").replace('const withDefaultShell = require("./default-shell-e2e.cjs");',
    `const withDefaultShell = ${require("./default-shell-e2e.cjs")};`);
  preload = path.join(evidenceDir, "preload.cjs");
  fs.writeFileSync(preload, source);
}

let activeWindow;
const results = [];
app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "E2E preload is missing.");

  const win = new BrowserWindow({
    width: 1180,
    height: 820,
    show: false,
    ...(process.platform === "darwin" ? {
      titleBarStyle: "hiddenInset",
      trafficLightPosition: { x: 18, y: 21 },
    } : {}),
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload,
      sandbox: focusedControls,
      backgroundThrottling: false
    }
  });

  activeWindow = win;
  win.webContents.debugger.attach("1.3");
  win.webContents.on("render-process-gone", (_event, details) => {
    fail(new Error(`Renderer process exited: ${details.reason}`));
  });

  await loadFile(win, rendererHtml);
  win.showInactive();
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);
  const macControlsPresent = await evaluate(win, () => !!document.querySelector('.mac-window-controls'));
  assert.equal(macControlsPresent, fixturePlatform === "darwin",
    "Fixture caption capabilities must activate the requested platform branch; dataset.platform alone is insufficient");
  await verifyNarrowSidebarControls(win);
  if (focusedControls) {
    saveResults();
    console.log(`PASS: focused ${fixturePlatform} renderer controls; native OS behavior is not exercised. Evidence: ${evidenceDir}`);
    win.close();
    app.quit();
    return;
  }
  await verifyTitlebarContinuity(win);
  win.setContentSize(1180, 820);
  win.webContents.setZoomFactor(1);
  await waitFor(win, () => window.innerWidth === 1180, 3000);
  await evaluate(win, () => {
    document.documentElement.style.setProperty("--desktop-page-zoom", "1");
    document.documentElement.style.setProperty("--font-ui", "14px");
    document.querySelector('[data-wuu-component="sidebar-toggle"]').click();
  });
  await waitFor(win, () => !document.querySelector(".app-shell")?.classList.contains("sidebar-collapsed"), 3000);
  win.show();
  win.focus();
  await evaluate(win, () => {
    const button = document.querySelector(".sidebar-account-trigger");
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Settings button not found.");
    }
    button.click();
  });
  await waitFor(win, () => Boolean(document.querySelector(".sidebar-account-menu")), 3000);
  await evaluate(win, () => {
    const button = document.querySelector('[data-settings-page="providers"]');
    if (!(button instanceof HTMLButtonElement)) throw new Error("Settings menu item not found.");
    button.click();
  });
  await waitFor(win, () => Boolean(document.querySelector(".settings-shell")), 3000);

  await evaluate(win, () => {
    const button = document.querySelector(".section-sidebar-toggle");
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Settings sidebar toggle not found.");
    }
    button.click();
  });
  await waitFor(
    win,
    () => document.querySelector(".settings-shell")?.classList.contains("sidebar-collapsed") || null,
    3000
  );
  win.setContentSize(760, 820);
  await waitFor(win, () => innerWidth === 760, 3000);
  await settleChrome(win);

  const before = await toggleHitState(win);
  assert.equal(before.hitOwnedByToggle, true, `Toggle must own its center hit before drawer opens: ${JSON.stringify(before)}`);
  assert.equal(before.visible, true, `Toggle must be visible before drawer opens: ${JSON.stringify(before)}`);
  await capture(win, "settings-sidebar-hit-before.png");

  win.webContents.sendInputEvent({ type: "mouseMove", x: before.centerX, y: before.centerY });
  for (let sample = 0; sample < 12; sample++) {
    await delay(40);
    assert.equal(await evaluate(win, () => !!document.querySelector('.sidebar-drawer-open')), false,
      "Hovering the persistent toggle must not reveal the secondary pane");
  }
  await revealDrawerAtEdge(win);
  const hitSamples = [];
  for (let index = 0; index < 8; index++) {
    hitSamples.push(await toggleHitState(win));
    await delay(35);
  }
  assert.ok(hitSamples.every(sample => sample.hitOwnedByToggle && sample.visible),
    `The toggle must remain clickable throughout drawer reveal: ${JSON.stringify(hitSamples)}`);
  await settleChrome(win);

  win.webContents.sendInputEvent({ type: "mouseMove", x: before.centerX, y: before.centerY });
  await evaluate(win, () => new Promise(requestAnimationFrame));
  const after = await toggleHitState(win);
  assert.equal(after.centerX, before.centerX, "Hover must not move the sidebar click target horizontally");
  assert.equal(after.centerY, before.centerY, "Hover must not move the sidebar click target vertically");
  assert.equal(after.hitOwnedByToggle, true, `Toggle must own its center hit after drawer opens: ${JSON.stringify(after)}`);
  assert.equal(after.visible, true, `Toggle must remain visible after drawer opens: ${JSON.stringify(after)}`);
  assert.equal(after.hovered, true, `The real Electron pointer must still hover the toggle: ${JSON.stringify(after)}`);
  const readability = await sidebarReadabilityState(win);
  assert.ok(readability.width > 0 && readability.labels.length >= 2, `Narrow-window drawer must stay readable: ${JSON.stringify(readability)}`);
  for (const label of readability.labels) {
    assert.equal(label.whiteSpace, "nowrap", `Navigation labels remain on one line: ${JSON.stringify(readability)}`);
    assert.ok(label.fits, `Built-in navigation labels fit their secondary pane: ${JSON.stringify(readability)}`);
  }
  await capture(win, "settings-sidebar-hit-after.png");

  win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, x: after.centerX, y: after.centerY });
  win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, x: after.centerX, y: after.centerY });
  await waitFor(
    win,
    () => !document.querySelector(".settings-shell")?.classList.contains("sidebar-collapsed") || null,
    3000
  );

  results.push({ scenario: "real-pointer-hover-and-dock", before, after, readability, hitSamples });
  saveResults();
  await verifyCompactRibbonSettingsResize(win);
  console.log(`PASS: independent workbench rows, unclipped controls, and stable page/drawer click targets. Evidence: ${evidenceDir}`);
  win.close();
  app.quit();
}

// The saved sidebar width includes the fixed 52px icon rail, while a docked
// ribbon adds its extra width on top. The settings rail must start after the
// compact ribbon and keep the same right edge as the main sidebar so the
// shared separator still owns the visible boundary.
async function verifyCompactRibbonSettingsResize(win) {
  win.setContentSize(1180, 820);
  win.webContents.setZoomFactor(1);
  await evaluate(win, () => {
    localStorage.setItem("wuu.desktop.sidebarWidth", "296");
    localStorage.setItem("wuu.desktop.sidebarCollapsed", "false");
    localStorage.setItem("wuu.navigation-ribbon.v1", JSON.stringify({
      websites: [], order: [], width: 248,
    }));
  });
  await loadFile(win, rendererHtml);
  await waitFor(win, () => !!document.querySelector(".conversation-pane"), 5000);
  await evaluate(win, () => document.querySelector(".sidebar-account-trigger").click());
  await waitFor(win, () => !!document.querySelector('[data-settings-page="providers"]'), 3000);
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => !!document.querySelector(".settings-shell"), 3000);
  await settleChrome(win);

  const before = await settingsResizeGeometry(win);
  assert.ok(Math.abs(before.rail.width - (before.width - before.compactWidth)) < 1,
    `Compact ribbon leaves the settings rail at the saved content width: ${JSON.stringify(before)}`);
  assert.ok(Math.abs(before.rail.right - before.resizerCenter) < 1,
    `Settings rail ends on the shared separator: ${JSON.stringify(before)}`);
  assert.equal(before.hitResizer, true,
    `The visible settings boundary owns the separator hit target: ${JSON.stringify(before)}`);

  const zoom = win.webContents.getZoomFactor();
  const point = { x: Math.round(before.rail.right * zoom), y: Math.round((before.rail.top + before.rail.height / 2) * zoom) };
  win.webContents.sendInputEvent({ type: "mouseMove", ...point });
  win.webContents.sendInputEvent({ type: "mouseDown", ...point, button: "left", clickCount: 1 });
  win.webContents.sendInputEvent({ type: "mouseMove", x: point.x + 40, y: point.y, button: "left", modifiers: ["leftButtonDown"] });
  win.webContents.sendInputEvent({ type: "mouseUp", x: point.x + 40, y: point.y, button: "left", clickCount: 1 });
  await waitFor(win, width => Number(document.querySelector(".app-shell > .sidebar-resizer").getAttribute("aria-valuenow")) > width, 3000, before.width);
  await settleChrome(win);

  const after = await settingsResizeGeometry(win);
  assert.ok(after.width > before.width, `Dragging the settings boundary grows the sidebar: ${JSON.stringify({ before, after })}`);
  assert.ok(Math.abs(after.rail.width - (after.width - after.compactWidth)) < 1,
    `Settings rail keeps the saved content width after resizing: ${JSON.stringify(after)}`);
  assert.ok(Math.abs(after.rail.right - after.resizerCenter) < 1,
    `Settings rail stays on the separator after resizing: ${JSON.stringify(after)}`);
  results.push({ scenario: "compact-ribbon-settings-resize", before, after, point });
  saveResults();
  await capture(win, "settings-sidebar-compact-ribbon.png");
}

async function settingsResizeGeometry(win) {
  return evaluate(win, () => {
    const shell = document.querySelector(".app-shell");
    const rail = document.querySelector(".settings-sidebar").getBoundingClientRect();
    const resizer = document.querySelector(".app-shell > .sidebar-resizer").getBoundingClientRect();
    const hit = document.elementFromPoint(rail.right, rail.top + rail.height / 2);
    return {
      rail: rail.toJSON(),
      resizer: resizer.toJSON(),
      resizerCenter: resizer.left + resizer.width / 2,
      width: parseFloat(shell.style.getPropertyValue("--sidebar-width")),
      compactWidth: parseFloat(getComputedStyle(shell).getPropertyValue("--sidebar-ribbon-compact-width")),
      hitResizer: hit === document.querySelector(".app-shell > .sidebar-resizer"),
    };
  });
}

// Reproduce the native Mac collision: a 240px total sidebar is narrower than
// a 240px secondary pane (292px including the ribbon). Both must stay usable.
async function verifyNarrowSidebarControls(win) {
  const savedWidth = await evaluate(win, () => localStorage.getItem("wuu.desktop.sidebarWidth"));
  for (const totalWidth of focusedControls ? [240] : [240, 292]) {
    await evaluate(win, width => {
      localStorage.setItem("wuu.desktop.sidebarWidth", String(width));
      localStorage.setItem("wuu.desktop.sidebarCollapsed", "false");
    }, totalWidth);
    win.setContentSize(900, 675);
    win.webContents.setZoomFactor(0.75);
    await loadFile(win, rendererHtml);
    await waitFor(win, () => !!document.querySelector(".conversation-pane"), 5000);
    await evaluate(win, platform => {
      document.documentElement.dataset.platform = platform;
      document.documentElement.dataset.hostKind = "desktop";
      document.documentElement.style.setProperty("--font-ui", "20px");
      document.documentElement.style.setProperty("--desktop-page-zoom", "0.75");
    }, fixturePlatform);
    await settleChrome(win);
    assert.equal(await evaluate(win, () => parseFloat(getComputedStyle(document.querySelector(".app-shell")).getPropertyValue("--sidebar-open-width"))), totalWidth);
    if (totalWidth === 240) {
      for (const zoom of [0.75, 0.8]) {
        win.setContentSize(Math.round(1200 * zoom), Math.round(900 * zoom));
        win.webContents.setZoomFactor(zoom);
        await evaluate(win, value => document.documentElement.style.setProperty("--desktop-page-zoom", String(value)), zoom);
        await settleChrome(win);
        await preparePointerInput(win);
        await verifyNotificationClick(win, zoom);
      }
      win.setContentSize(900, 675);
      win.webContents.setZoomFactor(0.75);
      await evaluate(win, () => document.documentElement.style.setProperty("--desktop-page-zoom", "0.75"));
      await settleChrome(win);
    }
    if (fixturePlatform === "darwin" && totalWidth === 240) {
      await verifyRibbonControlFocus(win, "conversation");
    }
    let collapsedGeometry;
    for (const layout of ["docked", "collapsed", "drawer"]) {
      if (layout === "collapsed") {
        await evaluate(win, () => document.querySelector(".section-sidebar-toggle").click());
      } else if (layout === "drawer") {
        await revealDrawerAtEdge(win);
      }
      await settleChrome(win);
      const geometry = await chromeToggleGeometry(win);
      const scenario = `sidebar-${totalWidth}-20px-0.75-${layout}`;
      results.push({ scenario, page: "conversation", geometry });
      saveResults();
      await capture(win, `${scenario}.png`);
      if (layout === "collapsed") collapsedGeometry = geometry;
      if (layout === "drawer") {
        assert.equal(geometry.titleX, collapsedGeometry.titleX, "Preview does not reflow the content title");
        assert.deepEqual(geometry.header, collapsedGeometry.header, "Preview does not move the content header");
      }
      assertTopRow(geometry, { page: layout === "drawer" ? "conversation-drawer" : "conversation", layout, font: 20, zoom: 0.75 });
    }
    await evaluate(win, () => document.querySelector(".sidebar-account-trigger").click());
    await waitFor(win, () => !!document.querySelector('[data-settings-page="providers"]'), 3000);
    await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
    await waitFor(win, () => !!document.querySelector(".settings-shell"), 3000);
    await settleChrome(win);
    const geometry = await chromeToggleGeometry(win);
    results.push({ scenario: `sidebar-${totalWidth}-20px-0.75-settings`, geometry });
    saveResults();
    assertTopRow(geometry, { totalWidth, page: "settings", layout: "drawer", font: 20, zoom: 0.75 });
    await capture(win, `sidebar-${totalWidth}-20px-0.75-settings.png`);
    if (fixturePlatform === "darwin" && totalWidth === 240) await verifyRibbonControlFocus(win, "settings");
    await evaluate(win, () => document.querySelector('[data-ribbon-id="conversations"]').click());
    const away = await evaluate(win, () => ({ x: innerWidth - 40, y: innerHeight - 40 }));
    win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(away.x * 0.75), y: Math.round(away.y * 0.75) });
    await waitFor(win, () => !document.querySelector('.sidebar-drawer-open, .sidebar-drawer-closing, .sidebar-drawer-docking'), 3000);
    await settleChrome(win);
    const restored = await chromeToggleGeometry(win);
    assertControlSeparation(restored, { totalWidth, page: "conversation-restored" });
  }
  await evaluate(win, width => {
    if (width === null) localStorage.removeItem("wuu.desktop.sidebarWidth");
    else localStorage.setItem("wuu.desktop.sidebarWidth", width);
    localStorage.setItem("wuu.desktop.sidebarCollapsed", "false");
  }, savedWidth);
  win.setContentSize(1180, 820);
  win.webContents.setZoomFactor(1);
  await loadFile(win, rendererHtml);
  await waitFor(win, () => !!document.querySelector(".conversation-pane"), 5000);
}

async function verifyRibbonControlFocus(win, page) {
  const originalCollapsed = await evaluate(win, () => document.querySelector('.app-shell').classList.contains('sidebar-collapsed'));
  const snapshot = () => evaluate(win, () => {
    const toggle = document.querySelector('.section-sidebar-toggle');
    const shell = document.querySelector('.app-shell');
    const settings = shell.hasAttribute('data-settings-open');
    return { toggle: { hidden: toggle.hidden, inert: toggle.inert, rect: toggle.getBoundingClientRect().toJSON() },
      title: document.querySelector(settings ? '.settings-page-title' : '.conversation-title-heading').getBoundingClientRect().toJSON(),
      content: document.querySelector(settings ? '.settings-workbench-layer' : '.conversation-pane').getBoundingClientRect().toJSON(),
      ribbonExpanded: !!document.querySelector('.sidebar-ribbon[data-expanded="true"]'),
      drawerVisible: shell.classList.contains('sidebar-drawer-open'),
      focusedControl: document.activeElement.getAttribute('data-wuu-component'),
      focusInRibbon: !!document.activeElement.closest('.sidebar-ribbon'), focusOnShell: document.activeElement === document.querySelector('.app-shell') };
  });
  // The caller first records/asserts the unmodified settings transition. Move
  // focus normally to the visible shell before starting independent cases.
  await evaluate(win, () => document.querySelector('.app-shell').focus());
  await preparePointerInput(win);
  for (const layout of ['docked', 'collapsed', 'drawer']) {
    await evaluate(win, collapsed => {
      if (document.querySelector('.app-shell').classList.contains('sidebar-collapsed') !== collapsed)
        document.querySelector('.section-sidebar-toggle').click();
    }, layout !== 'docked');
    await settleChrome(win);
    if (layout === 'drawer') { await revealDrawerAtEdge(win); await settleChrome(win); }
    for (const owner of ['toggle']) {
      const focused = await evaluate(win, owner => {
        const target = document.querySelector('.section-sidebar-toggle');
        if (!target || target.closest('[hidden], [inert]')) return false;
        target.focus(); return document.activeElement === target;
      }, owner);
      assert.ok(focused, `${page}/${layout}/${owner}: the tested control must own focus`);
      const before = await snapshot();
      assert.equal(before.drawerVisible, layout === 'drawer', `${page}/${layout}/${owner}: the requested drawer state is active`);
      await hoverCompactRibbon(win);
      await settleChrome(win);
      const hovered = await snapshot();
      results.push({ scenario: `ribbon-hover-${page}-${layout}-${owner}`, before, hovered });
      saveResults();
      if (page === 'settings' && layout === 'drawer') {
        // The ribbon is outside .settings-sidebar, so leaving that hover
        // drawer closes it. Reopen it before testing keyboard reveal.
        assert.deepEqual(hovered, { ...before, drawerVisible: false },
          'Leaving the drawer preserves controls and their focus');
        await revealDrawerAtEdge(win);
        await settleChrome(win);
        assert.deepEqual(await snapshot(), before, 'The same drawer and focused control return before keyboard reveal');
      } else {
        assert.deepEqual(hovered, before, `${page}/${layout}/${owner}: hover preserves control focus, accessibility and geometry`);
      }
      await focusRibbonWithKeyboard(win);
      const expanded = await snapshot();
      assert.ok(!expanded.toggle.hidden && !expanded.toggle.inert, "The compact ribbon keeps its sidebar toggle reachable");
      assert.equal(expanded.focusInRibbon, true, 'Keyboard navigation retains focus in the ribbon');
      assert.deepEqual(expanded.title, before.title);
      assert.deepEqual(expanded.content, before.content);
      await evaluate(win, () => document.querySelector('.app-shell').focus());
      await preparePointerInput(win);
      await settleChrome(win);
      const restored = await snapshot();
      assert.ok(!restored.toggle.hidden && !restored.toggle.inert && !restored.ribbonExpanded);
      assert.ok(restored.focusOnShell, 'Closing returns focus outside the ribbon');
      assert.deepEqual(restored.toggle.rect, before.toggle.rect, 'The same toggle anchor returns');
      assert.deepEqual(restored.title, before.title);
      assert.deepEqual(restored.content, before.content);
      results.push({ scenario: `ribbon-focus-${page}-${layout}-${owner}`, before, expanded, restored });
      saveResults();
    }
  }
  // Keyboard navigation retains focus without expanding the ribbon.
  await focusRibbonWithKeyboard(win);
  const away = await evaluate(win, () => ({ x: innerWidth - 30, y: innerHeight - 30 }));
  const zoom = win.webContents.getZoomFactor();
  win.webContents.sendInputEvent({ type: 'mouseMove', x: Math.round(away.x * zoom), y: Math.round(away.y * zoom) });
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const keyboard = await snapshot();
  assert.ok(!keyboard.ribbonExpanded && keyboard.focusInRibbon, 'Keyboard ribbon focus survives pointer exit at compact width');
  await evaluate(win, () => document.querySelector('.app-shell').focus());
  await waitFor(win, () => !document.querySelector('.sidebar-ribbon[data-expanded="true"]'), 3000);
  await settleRibbonMotion(win);
  results.push({ scenario: `ribbon-keyboard-retention-${page}`, keyboard });
  saveResults();
  await evaluate(win, collapsed => {
    if (document.querySelector('.app-shell').classList.contains('sidebar-collapsed') !== collapsed)
      document.querySelector('.section-sidebar-toggle').click();
  }, originalCollapsed);
  await settleChrome(win);
}

async function notificationRouteState(win) {
  return evaluate(win, () => ({
    title: document.querySelector('.conversation-title-heading h1')?.textContent,
    draft: document.querySelector('.composer textarea')?.value,
    settingsOpen: document.querySelector('.app-shell').hasAttribute('data-settings-open'),
    activeRibbon: [...document.querySelectorAll('[data-ribbon-id][aria-current="page"]')].map(node => node.dataset.ribbonId),
  }));
}

async function settleRibbonMotion(win) {
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}

async function hoverCompactRibbon(win) {
  const point = await evaluate(win, () => {
    const ribbon = document.querySelector('.sidebar-ribbon').getBoundingClientRect();
    const item = document.querySelector('.sidebar-ribbon-main .sidebar-ribbon-button').getBoundingClientRect();
    return { x: ribbon.left + Math.min(12, ribbon.width / 2), y: item.top + item.height / 2 };
  });
  const zoom = win.webContents.getZoomFactor();
  const position = { x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) };
  win.webContents.sendInputEvent({ type: 'mouseMove', ...position });
  await waitFor(win, () => document.querySelector('.sidebar-ribbon').matches(':hover'), 3000);
  const hover = await evaluate(win, ({ position, zoom }) => {
    const ribbon = document.querySelector('.sidebar-ribbon');
    const hit = document.elementFromPoint(position.x / zoom, position.y / zoom);
    return { owned: hit === ribbon || ribbon.contains(hit), hit: hit?.getAttribute('class') };
  }, { position, zoom });
  assert.ok(hover.owned, `The ribbon owns the actual rounded hover point: ${JSON.stringify(hover)}`);
  for (let sample = 0; sample < 12; sample++) {
    await delay(40);
    assert.equal(await evaluate(win, () => !!document.querySelector('.sidebar-ribbon[data-expanded="true"]')), false,
      'Pointer hover keeps the ribbon compact');
  }
  results.push({ scenario: 'compact-ribbon-hover', zoom, position, hover });
  saveResults();
}

async function focusRibbonWithKeyboard(win) {
  await evaluate(win, () => document.querySelector('.sidebar-ribbon-main .sidebar-ribbon-button').focus());
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'ArrowDown' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'ArrowDown' });
  await waitFor(win, () => !!document.activeElement.closest('.sidebar-ribbon'), 3000);
  assert.equal(await evaluate(win, () => document.querySelector('.sidebar-ribbon').getBoundingClientRect().width), 52);
  await settleRibbonMotion(win);
}

async function preparePointerInput(win) {
  // These interactions run in the CI-owned Electron window. CDP focus
  // emulation alone does not satisfy sendInputEvent's native-window contract.
  win.show();
  win.focus();
  const started = Date.now();
  while (!win.isFocused() && Date.now() - started < 3000) await delay(40);
  assert.ok(win.isFocused(), "The fixture window must own focus before physical input");
  const zoom = win.webContents.getZoomFactor();
  const [width, height] = win.getContentSize();
  // Native resize/zoom setters can return before Chromium applies its viewport.
  await waitFor(win, ({ width, height, zoom }) => Math.abs(innerWidth - width / zoom) <= 1 &&
    Math.abs(innerHeight - height / zoom) <= 1, 3000, { width, height, zoom });
  const point = await evaluate(win, () => ({ x: innerWidth - 30, y: innerHeight - 30 }));
  win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) });
  await evaluate(win, () => new Promise(requestAnimationFrame));
  const state = await evaluate(win, () => ({
    ribbonExpanded: !!document.querySelector('.sidebar-ribbon[data-expanded="true"]'),
    activeElement: { tag: document.activeElement?.tagName, className: document.activeElement?.getAttribute("class"),
      component: document.activeElement?.getAttribute("data-wuu-component") },
  }));
  results.push({ scenario: "neutral-pointer-precondition", zoom, point, focused: win.isFocused(), state });
  saveResults();
  await waitFor(win, () => !document.querySelector('.sidebar-ribbon[data-expanded="true"]'), 3000);
  await settleRibbonMotion(win);
}

async function pointerClick(win, selector) {
  await preparePointerInput(win);
  const point = await evaluate(win, selector => {
    const target = document.querySelector(selector);
    const box = target.getBoundingClientRect();
    return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
  }, selector);
  const zoom = win.webContents.getZoomFactor();
  const position = { x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) };
  win.webContents.sendInputEvent({ type: "mouseMove", ...position });
  await evaluate(win, () => new Promise(requestAnimationFrame));
  const before = await evaluate(win, ({ selector, position, zoom }) => {
    const target = document.querySelector(selector);
    const x = position.x / zoom, y = position.y / zoom;
    const hit = document.elementFromPoint(x, y);
    return { x, y, owned: hit === target || target.contains(hit),
      targetBounds: target.getBoundingClientRect().toJSON(),
      hit: { tag: hit?.tagName, className: hit?.getAttribute('class'),
        component: hit?.closest('button')?.getAttribute('data-wuu-component'),
        bounds: hit?.getBoundingClientRect().toJSON() },
      ribbonExpanded: !!document.querySelector('.sidebar-ribbon[data-expanded="true"]') };
  }, { selector, position, zoom });
  results.push({ scenario: "physical-click-precondition", selector, zoom, focused: win.isFocused(), before });
  saveResults();
  assert.ok(before.owned, `The intended button must own the actual rounded input point before clicking: ${JSON.stringify(before)}`);
  await evaluate(win, selector => {
    const intended = document.querySelector(selector);
    window.__titlebarInputTrace = [];
    window.__titlebarInputObserver = event => {
      const target = event.target instanceof Element ? event.target.closest('button') || event.target : null;
      window.__titlebarInputTrace.push({ type: event.type, x: event.clientX, y: event.clientY,
        target: target?.getAttribute('data-wuu-component') || target?.getAttribute('class'),
        trusted: event.isTrusted, owned: event.target === intended || intended.contains(event.target) });
    };
    for (const type of ['pointerdown', 'pointerup', 'click']) document.addEventListener(type, window.__titlebarInputObserver, true);
  }, selector);
  win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...position });
  win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...position });
  const events = await evaluate(win, async () => {
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    for (const type of ['pointerdown', 'pointerup', 'click']) document.removeEventListener(type, window.__titlebarInputObserver, true);
    const events = window.__titlebarInputTrace;
    delete window.__titlebarInputObserver;
    delete window.__titlebarInputTrace;
    return events;
  });
  results.push({ scenario: "physical-click-delivery", selector, zoom, position, events });
  saveResults();
  for (const type of ['pointerdown', 'pointerup', 'click']) {
    assert.ok(events.some(event => event.type === type && event.trusted && event.owned),
      `Electron must deliver ${type} to the intended control: ${JSON.stringify(events)}`);
  }
}

async function verifyNotificationClick(win, zoom) {
  await evaluate(win, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Keep notification click draft');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('.sidebar-account-trigger').click();
  });
  await waitFor(win, () => !!document.querySelector('[data-settings-page="providers"]'), 3000);
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => !!document.querySelector('.settings-shell'), 3000);
  await evaluate(win, () => {
    const conversations = document.querySelector('[data-ribbon-id="conversations"]');
    conversations.focus();
    conversations.click();
  });
  await waitFor(win, () => !document.querySelector('.app-shell[data-settings-open]'), 3000);
  await settleChrome(win);
  const before = await notificationRouteState(win);
  assert.equal(before.draft, 'Keep notification click draft');
  await pointerClick(win, '.sidebar-notifications-button');
  await waitFor(win, () => document.querySelector('.sidebar-notifications-button')?.getAttribute('aria-pressed') === 'true' &&
    !!document.querySelector('.sidebar-unread-view'), 3000);
  const after = await notificationRouteState(win);
  assert.deepEqual(after, before, 'A physical notification click must not navigate or change the active conversation draft');
  results.push({ scenario: `notification-click-240-20px-${zoom}`, before, after, attentionOpen: true });
  saveResults();
  await capture(win, `notification-click-240-20px-${zoom}.png`);
  await pointerClick(win, '.sidebar-notifications-button');
  await waitFor(win, () => !document.querySelector('.sidebar-unread-view'), 3000);
  await evaluate(win, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, '');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

// Exercise the real app, not copies of header markup or stylesheet literals.
// Page switching must preserve the control's hit box and rendered glyph;
// Shell controls and content headings retain their respective rows without clipping.
async function verifyTitlebarContinuity(win) {
  for (const theme of ["light", "dark"]) {
    for (const font of [13, 15, 20]) {
      for (const zoom of [1, 0.75, 1.25, 2]) {
        win.webContents.setZoomFactor(zoom);
        await win.webContents.executeJavaScript(`
          document.documentElement.dataset.platform = ${JSON.stringify(fixturePlatform)};
          document.documentElement.dataset.hostKind = 'desktop';
          document.documentElement.dataset.theme = ${JSON.stringify(theme)};
          document.documentElement.style.setProperty('--font-ui', '${font}px');
          document.documentElement.style.setProperty('--desktop-page-zoom', '${zoom}');
        `);
        for (const layout of ["docked", "collapsed", "narrow"]) {
          // Keep the CSS viewport in the requested layout as page zoom changes.
          win.setContentSize(Math.round((layout === "narrow" ? 600 : 1180) * zoom), 820);
          await waitFor(win, layout === "narrow"
            ? () => Math.abs(window.innerWidth - 600) <= 1
            : () => Math.abs(window.innerWidth - 1180) <= 1, 3000);
          await waitFor(win, () => !document.querySelector(".app-shell")?.classList.contains("sidebar-animating"), 3000);
          const collapsed = await evaluate(win, () => document.querySelector(".app-shell").classList.contains("sidebar-collapsed"));
          if (collapsed !== (layout !== "docked")) {
            await evaluate(win, () => document.querySelector('[data-wuu-component="sidebar-toggle"]').click());
          }
          await settleChrome(win);
          const main = await chromeToggleGeometry(win);
          const scenario = `${theme}-${font}px-${zoom}-${layout}`;
          results.push({ scenario, page: "conversation", geometry: main });
          saveResults();
          assertTopRow(main, { theme, font, zoom, layout, page: "conversation" });
          if (zoom === 1 && font !== 13) await capture(win, `${scenario}-conversation.png`);
          assert.deepEqual(main.clippedChrome, [], `Main chrome must fit: ${JSON.stringify({ theme, font, zoom, layout, main })}`);
          if (layout !== "docked") {
            await revealDrawerAtEdge(win);
            await settleChrome(win);
            const drawer = await chromeToggleGeometry(win);
            results.push({ scenario, page: "conversation-drawer", geometry: drawer });
            assertTopRow(drawer, { theme, font, zoom, layout, page: "conversation-drawer" });
            assert.deepEqual(drawer.clippedChrome, [], `Drawer chrome must fit: ${JSON.stringify({ theme, font, zoom, layout, drawer })}`);
            for (const key of ["x", "y", "width", "height"]) {
              assert.ok(Math.abs(drawer[key] - main[key]) < 0.1, `Opening the drawer moved ${key}: ${JSON.stringify({layout,zoom,main,drawer})}`);
            }
            assert.ok(Math.abs(drawer.titleX - main.titleX) < 0.1, "Opening the drawer must not move the adjacent title");
          }
          await evaluate(win, () => document.querySelector(".sidebar-account-trigger").click());
          await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')), 3000);
          await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
          await waitFor(win, () => Boolean(document.querySelector(".settings-shell")), 3000);
          await settleChrome(win);
          const settings = await chromeToggleGeometry(win);
          results.push({ scenario, page: "settings", geometry: settings });
          saveResults();
          assertTopRow(settings, { theme, font, zoom, layout, page: "settings" });
          if (zoom === 1 && font !== 13) await capture(win, `${scenario}-settings.png`);
          const context = JSON.stringify({ theme, font, zoom, layout, main, settings });
          assert.deepEqual(settings.clippedChrome, [], `Settings chrome must fit: ${context}`);
          for (const key of ["x", "y", "width", "height", "iconWidth", "iconHeight"]) {
            assert.ok(Math.abs(main[key] - settings[key]) < 0.1, `Switching pages moved/resized ${key}: ${context}`);
          }
          assert.equal(settings.icon, main.icon, `Switching pages changed the sidebar glyph: ${context}`);
          assert.ok(main.hit && settings.hit, `The visible toggle must own its center hit: ${context}`);
          await evaluate(win, () => document.querySelector('[data-ribbon-id="conversations"]').click());
          await waitFor(win, () => !document.querySelector(".app-shell[data-settings-open]"), 3000);
          // Move the real pointer off the edge, including at zoom levels where
          // the CSS viewport is shorter than the native window.
          const outside = await evaluate(win, () => ({ x: innerWidth - 40, y: innerHeight - 40 }));
          win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(outside.x * zoom), y: Math.round(outside.y * zoom) });
          await waitFor(win, () => !document.querySelector(".sidebar-drawer-open, .sidebar-drawer-closing"), 3000);
        }
      }
    }
  }
  saveResults();
}

async function settleChrome(win) {
  await waitFor(win, () => !document.querySelector(".sidebar-animating, .sidebar-drawer-docking, .sidebar-drawer-closing, .window-resizing"), 3000);
  await evaluate(win, async () => {
    await document.fonts.ready;
    await new Promise(requestAnimationFrame);
    const chrome = document.querySelectorAll(".app-shell, .settings-shell");
    await Promise.all([...chrome].flatMap(element => element.getAnimations({ subtree: true })).filter(animation =>
      animation.timeline instanceof DocumentTimeline && animation.playState === "running" && Number.isFinite(animation.effect.getComputedTiming().endTime)
    ).map(animation => animation.finished.catch(() => {})));
    await new Promise(requestAnimationFrame);
  });
}

async function chromeToggleGeometry(win) {
  return evaluate(win, () => {
    const button = document.querySelector('[data-wuu-component="sidebar-toggle"]');
    const rect = button.getBoundingClientRect();
    const icon = button.querySelector("svg");
    const glyph = icon.getBoundingClientRect();
    const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
    const settingsOpen = document.querySelector('.app-shell').hasAttribute('data-settings-open');
    const activeHeader = document.querySelector(settingsOpen ? '.settings-titlebar' : '.conversation-pane .titlebar');
    const headers = [activeHeader];
    const clippedChrome = [];
    for (const header of headers) {
      const bounds = header.getBoundingClientRect();
      for (const element of header.querySelectorAll(".icon-button, .conversation-title-heading")) {
        const box = element.getBoundingClientRect();
        if (!box.width || !box.height || getComputedStyle(element).visibility === "hidden") continue;
        if (box.top < Math.max(0, bounds.top) - 0.1 || box.bottom > bounds.bottom + 0.1) {
          clippedChrome.push({ target: element.className, top: box.top, bottom: box.bottom, headerTop: bounds.top, headerBottom: bounds.bottom });
        }
      }
    }
    const header = activeHeader.getBoundingClientRect();
    const headerStyle = getComputedStyle(activeHeader);
    const peers = [...activeHeader.querySelectorAll('h1, .conversation-title-heading .icon-button, .title-actions .icon-button, .settings-page-actions button')]
      .filter(element => element.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }))
      .map(element => {
        const box = element.getBoundingClientRect();
        const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
        return { target: element.dataset.wuuComponent || element.className || element.tagName,
          bounds: box.toJSON(), centerY: box.y + box.height / 2,
          button: element instanceof HTMLButtonElement, hit: element === hit || element.contains(hit),
          coveredByDrawer: Boolean(document.querySelector(settingsOpen ? '.sidebar-drawer-open .settings-sidebar' : '.sidebar-drawer-open .sidebar')?.contains(hit)) };
      });
    const shellRow = document.querySelector('.mac-window-controls') ? activeHeader : document.querySelector('.workbench-window-chrome');
    const shellBounds = shellRow.getBoundingClientRect();
    const shellStyle = getComputedStyle(shellRow);
    const shellControls = [...document.querySelectorAll('.section-sidebar-toggle, .shell-right-sidebar-toggle')]
      .filter(element => element.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }))
      .map(element => {
        const box = element.getBoundingClientRect();
        const target = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
        return { target: element.dataset.wuuComponent, bounds: box.toJSON(),
          hit: target === element || element.contains(target),
          noDrag: getComputedStyle(element).getPropertyValue('-webkit-app-region') === 'no-drag' };
      });
    const shell = document.querySelector('.app-shell');
    const visible = element => element.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true });
    const guardElements = [...new Set([
      ...document.querySelectorAll('.section-sidebar-toggle, .sidebar-notifications-button, .sidebar-brand .conversation-search-trigger'),
      ...activeHeader.querySelectorAll('h1, button'),
    ])].filter(visible);
    const drawer = shell.classList.contains('sidebar-drawer-open')
      ? document.querySelector(settingsOpen ? '.settings-sidebar' : '.sidebar > .sidebar-content') : null;
    const drawerBounds = drawer && visible(drawer) ? drawer.getBoundingClientRect().toJSON() : null;
    const separationTargets = guardElements.map(element => {
      const box = element.getBoundingClientRect();
      const samples = [[0.5, 0.5], [0.2, 0.2], [0.8, 0.2], [0.2, 0.8], [0.8, 0.8]].map(([x, y]) => {
        const point = { x: box.left + box.width * x, y: box.top + box.height * y };
        const hit = document.elementFromPoint(point.x, point.y);
        return { ...point, owned: hit === element || element.contains(hit),
          drawer: !!drawer?.contains(hit),
          hit: hit?.closest('button')?.dataset.wuuComponent || hit?.className || null };
      });
      return { target: element.dataset.wuuComponent || element.className || element.tagName,
        bounds: box.toJSON(), button: element instanceof HTMLButtonElement,
        contentHeader: activeHeader.contains(element),
        drawerOwned: !!element.closest(settingsOpen ? '.settings-sidebar' : '.sidebar-content'), samples };
    });
    return {
      header: header.toJSON(), peers, separationTargets, drawerBounds,
      toggleState: { expectedSuppressed: !!document.querySelector('.mac-window-controls') && !!document.querySelector('.sidebar-ribbon[data-expanded="true"]'), hidden: button.hidden, inert: button.inert },
      contentAxis: (header.top + parseFloat(headerStyle.paddingTop) + header.bottom - parseFloat(headerStyle.paddingBottom)) / 2,
      shellRow: shellBounds.toJSON(), shellControls,
      shellAxis: (shellBounds.top + parseFloat(shellStyle.paddingTop) + shellBounds.bottom - parseFloat(shellStyle.paddingBottom)) / 2,
      viewport: { width: innerWidth, height: innerHeight },
      x: rect.x, y: rect.y, width: rect.width, height: rect.height,
      iconWidth: glyph.width, iconHeight: glyph.height, icon: icon.innerHTML,
      hit: button === hit || button.contains(hit),
      clippedChrome,
      titleX: document.querySelector(".conversation-title-heading")?.getBoundingClientRect().x,
    };
  });
}

async function toggleHitState(win) {
  return evaluate(win, () => {
    const toggle = document.querySelector(".section-sidebar-toggle");
    if (!(toggle instanceof HTMLButtonElement)) {
      throw new Error("Settings sidebar toggle not found.");
    }
    const rect = toggle.getBoundingClientRect();
    const centerX = Math.round(rect.left + rect.width / 2);
    const centerY = Math.round(rect.top + rect.height / 2);
    const hit = document.elementFromPoint(centerX, centerY);
    const style = getComputedStyle(toggle);
    return {
      centerX,
      centerY,
      hitOwnedByToggle: hit === toggle || toggle.contains(hit),
      hitTag: hit?.tagName ?? null,
      hovered: toggle.matches(":hover"),
      visible: rect.width > 0 && rect.height > 0 && style.visibility === "visible" && Number(style.opacity) > 0
    };
  });
}

async function revealDrawerAtEdge(win) {
  const point = await evaluate(win, () => {
    const edge = document.querySelector('.app-shell > .sidebar-hover-zone');
    const box = edge.getBoundingClientRect();
    return { x: box.left + box.width / 2, y: box.top + Math.min(80, box.height / 2) };
  });
  const zoom = win.webContents.getZoomFactor();
  win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) });
  await waitFor(win, () => !!document.querySelector('.app-shell.sidebar-drawer-open'), 3000);
}

async function sidebarReadabilityState(win) {
  return evaluate(win, () => {
    const drawer = document.querySelector('.settings-sidebar');
    if (!(drawer instanceof HTMLElement)) throw new Error('Settings secondary pane missing');
    return { width: drawer.getBoundingClientRect().width,
      labels: [...drawer.querySelectorAll('.settings-nav-item[data-wuu-page="general"] .settings-nav-label, .settings-nav-item[data-wuu-page="providers"] .settings-nav-label')]
        .map(label => ({ text: label.textContent, whiteSpace: getComputedStyle(label).whiteSpace,
          fits: label.clientWidth > 0 && label.scrollWidth <= label.clientWidth })) };
  });
}

async function capture(win, name) {
  const image = await win.webContents.capturePage();
  fs.mkdirSync(evidenceDir, { recursive: true });
  fs.writeFileSync(path.join(evidenceDir, name), image.toPNG());
}

function loadFile(win, file) {
  return new Promise((resolve, reject) => {
    win.webContents.once("did-fail-load", (_event, _code, description) => reject(new Error(description)));
    win.webContents.once("did-finish-load", resolve);
    win.loadFile(file);
  });
}

async function waitFor(win, predicate, timeoutMs, argument) {
  const started = Date.now();
  let lastValue;
  while (Date.now() - started < timeoutMs) {
    lastValue = await evaluate(win, predicate, argument);
    if (lastValue) {
      return lastValue;
    }
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

function evaluate(win, fn, argument) {
  return win.webContents.executeJavaScript(`(${fn.toString()})(${JSON.stringify(argument) ?? ""})`, true);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function assertTopRow(geometry, context) {
  const details = JSON.stringify({ ...context, geometry });
  const axis = geometry.contentAxis;
  assert.ok(geometry.peers.length >= 2, `Active page title and actions must remain present: ${details}`);
  const expectedControls = [...(geometry.toggleState.expectedSuppressed ? [] : ["sidebar-toggle"]),
    ...(context.page === "settings" ? [] : ["right-sidebar-toggle"])];
  assert.deepEqual(geometry.shellControls.map(control => control.target).sort(), expectedControls.sort(),
    `The active surface exposes its sidebar controls: ${details}`);
  for (const control of geometry.shellControls) {
    const box = control.bounds;
    assert.ok(control.hit && control.noDrag, `Shell controls retain no-drag click targets: ${details}`);
    assert.ok(box.width > 0 && box.height > 0 && box.left >= 0 && box.right <= geometry.viewport.width + 0.1,
      `Shell controls fit the window: ${details}`);
    assert.ok(box.top >= geometry.shellRow.top - 0.1 && box.bottom <= geometry.shellRow.bottom + 0.1,
      `Shell controls fit their actual workbench row: ${details}`);
    assert.ok(Math.abs(box.top + box.height / 2 - geometry.shellAxis) < 0.6,
      `Shell controls share their own row, separate from content headings: ${details}`);
  }
  assertControlSeparation(geometry, context);
  for (const peer of geometry.peers) {
    assert.ok(Math.abs(peer.centerY - axis) < 0.6, `Peer controls must share the titlebar axis: ${details}`);
    assert.ok(peer.bounds.top >= geometry.header.top - 0.1 && peer.bounds.bottom <= geometry.header.bottom + 0.1,
      `Chrome must not be vertically clipped: ${details}`);
    // A hover drawer overlays the canvas without shifting its header. Its
    // covered controls cannot receive clicks until the drawer closes.
    if (peer.button) assert.ok(peer.hit || (["conversation-drawer", "settings"].includes(context.page)
      && peer.target !== "sidebar-toggle" && peer.coveredByDrawer),
    `Visible controls must own their center hit: ${details}`);
  }
}

function assertControlSeparation(geometry, context) {
  const details = JSON.stringify({ ...context, geometry });
  assert.equal(geometry.toggleState.hidden, geometry.toggleState.expectedSuppressed, `Ribbon toggle visibility is explicit: ${details}`);
  assert.equal(geometry.toggleState.inert, geometry.toggleState.expectedSuppressed, `Covered toggle cannot receive keyboard input: ${details}`);
  const targets = geometry.separationTargets;
  for (const target of targets) {
    if (target.button) {
      assert.ok(target.samples.every(sample => ownsHeaderSample(target, sample, geometry.drawerBounds)),
        `Visible controls own their interior, not only their center: ${details}`);
    }
  }
}

function saveResults(failure) {
  fs.writeFileSync(path.join(evidenceDir, "results.json"), JSON.stringify({
    platform: process.platform, fixturePlatform, focusedControls, simulatedPlatform: fixturePlatform !== process.platform,
    captionCapabilities: "Renderer-only stubs; not evidence of native OS caption behavior.",
    electron: process.versions.electron, rendererHtml,
    nativeTrafficLights: {
      verification: "Renderer geometry only; native macOS traffic-light pixels require a macOS acceptance run." },
    account: "Not exercised: account UI is disabled in production bundles.",
    linuxCaption: "The titlebar fixture does not add Linux caption actions; onboarding-workspaces E2E covers the real main/preload caption.",
    results, ...(failure ? { failure } : {}),
  }, null, 2));
}

async function fail(error) {
  saveResults(error.stack || String(error));
  if (activeWindow && !activeWindow.isDestroyed()) await capture(activeWindow, "failure.png");
  console.error(error);
  app.exit(1);
}
