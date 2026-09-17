const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const userData = fs.mkdtempSync(path.join(desktopRoot, "out", "session-width-e2e-"));
app.setPath("userData", userData);
process.env.WUU_RESIZE_E2E_CWD = path.resolve(desktopRoot, "..");

app.whenReady().then(run).catch((error) => {
  console.error(error);
  app.exit(1);
});

async function run() {
  const win = new BrowserWindow({
    width: 1380,
    height: 860,
    show: process.env.WUU_E2E_VISIBLE === "true",
    webPreferences: {
      preload: path.join(__dirname, "resize-e2e-preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
      backgroundThrottling: false
    }
  });
  await win.loadFile(path.join(desktopRoot, "out", "renderer", "index.html"));
  await waitFor(win, () => !!document.querySelector(".turn"));

  // Hold navigation closed while crossing its breakpoint to distinguish a
  // gutter discontinuity from an intentional sidebar layout change.
  await evaluate(win, () => {
    for (const toggle of document.querySelectorAll(
      '.environment-toggle-button[aria-pressed="true"], .title-actions .side-panel-toggle-button[aria-pressed="true"]'
    )) toggle.click();
    if (!document.querySelector(".app-shell").classList.contains("sidebar-collapsed")) {
      document.querySelector(".sidebar-toggle-button").click();
    }
  });
  await waitFor(win, () => document.querySelector(".app-shell").classList.contains("sidebar-collapsed"));

  const populated = new Map();
  for (const width of [1380, 1024, 896, 881, 880, 879, 800, 762, 761, 760, 759, 758, 600, 401, 400, 399, 390, 320]) {
    win.setContentSize(width, 820);
    const geometry = await settledGeometry(win, width);
    checkGeometry(geometry);
    populated.set(width, geometry);
  }
  const samples = [...populated.entries()];
  for (let index = 1; index < samples.length; index += 1) {
    const [width, current] = samples[index];
    const [previousWidth, previous] = samples[index - 1];
    const delta = previous.composerWidth - current.composerWidth;
    assert.ok(delta >= -1 && delta <= previousWidth - width + 1,
      `Shrinking the window must shrink the composer continuously: ${JSON.stringify({ previous, current })}`);
  }
  const phone = populated.get(390);
  assert.ok(phone.leftGap <= 16 && phone.rightGap <= 16,
    `Phone composer should stay close to both edges: ${JSON.stringify(phone)}`);

  for (const width of [390, 600, 759, 760, 800, 1024, 1380]) {
    win.setContentSize(width, 820);
    const geometry = await settledGeometry(win, width);
    checkGeometry(geometry);
    assert.ok(Math.abs(geometry.composerWidth - populated.get(width).composerWidth) <= 1,
      `Growing and shrinking should produce the same width: ${JSON.stringify(geometry)}`);
  }
  win.setContentSize(800, 390);
  const landscape = await settledGeometry(win, 800);
  checkGeometry(landscape);
  assert.ok(Math.abs(landscape.composerWidth - populated.get(800).composerWidth) <= 1,
    "Composer width should not depend on viewport height");

  // Exercise env() in the browser, including the deliberate symmetric clearance
  // for a one-sided notch. A desktop viewport alone always reports zero insets.
  win.webContents.debugger.attach("1.3");
  for (const insets of [{ left: 59, right: 0 }, { left: 0, right: 59 }]) {
    await win.webContents.debugger.sendCommand("Emulation.setSafeAreaInsetsOverride", { insets });
    const safe = await settledGeometry(win, 800);
    checkGeometry(safe);
    assert.ok(safe.leftGap >= 59 && safe.rightGap >= 59,
      `Both edges must clear the notch and retain a shared centerline: ${JSON.stringify(safe)}`);
    assert.ok(safe.composerWidth < landscape.composerWidth,
      "Safe-area emulation must actually constrain the composer");
  }
  await win.webContents.debugger.sendCommand("Emulation.setSafeAreaInsetsOverride", { insets: {} });
  win.webContents.debugger.detach();

  win.setContentSize(1024, 820);
  await settledGeometry(win, 1024);
  await evaluate(win, () => document.querySelector('[aria-label="打开关联会话"]').click());
  await waitFor(win, () => document.querySelectorAll(".conversation-split-pane").length === 2);
  for (const width of [1024, 800]) {
    win.setContentSize(width, 820);
    for (const side of ["first-child", "last-child"]) {
      checkGeometry(await settledGeometry(win, width, `.conversation-split-pane:${side}`));
    }
  }

  // Custom Chromium scrollbars occupy real layout space even on macOS. Measure
  // them through the app's focus sync instead of faking the compensation token.
  const classicScrollbars = await win.webContents.insertCSS(`
    * { scrollbar-width: auto !important; scrollbar-color: auto !important; }
    *::-webkit-scrollbar { width: 16px !important; height: 16px !important; }
  `);
  await evaluate(win, () => window.dispatchEvent(new Event("focus")));
  for (const width of [1024, 800]) {
    win.setContentSize(width, 820);
    for (const side of ["first-child", "last-child"]) {
      const geometry = await settledGeometry(win, width, `.conversation-split-pane:${side}`);
      assert.ok(geometry.scrollbarWidth > 0, `Classic-scrollbar coverage must reserve actual space: ${JSON.stringify(geometry)}`);
      checkGeometry(geometry);
    }
  }
  await evaluate(win, () => {
    document.querySelector(".conversation-split-close").click();
  });
  await waitFor(win, () => !document.querySelector(".conversation-split-pane"));

  win.setContentSize(1380, 820);
  await settledGeometry(win, 1380);
  await evaluate(win, () => {
    const textarea = document.querySelector(".dock-composer-wrap textarea");
    textarea.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(textarea, "/side");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await waitFor(win, () => !!document.querySelector('.slash-command-item[data-command-name="side"]'));
  await evaluate(win, () => document.querySelector('.slash-command-item[data-command-name="side"]').click());
  await waitFor(win, () => !!document.querySelector(".side-thread-panel__conversation .turn"));
  const sideThread = await settledGeometry(win, 1380, ".side-thread-panel");
  assert.ok(sideThread.scrollbarWidth > 0, "Side-thread history must reserve a classic scrollbar");
  checkGeometry(sideThread);
  await win.webContents.removeInsertedCSS(classicScrollbars);
  await evaluate(win, () => window.dispatchEvent(new Event("focus")));
  checkGeometry(await settledGeometry(win, 1380, ".side-thread-panel"));
  await evaluate(win, () => document.querySelector(".side-thread-panel__close").click());
  await waitFor(win, () => !document.querySelector(".side-thread-panel"));

  win.setContentSize(1024, 820);
  await settledGeometry(win, 1024);
  await evaluate(win, () => document.querySelector(".session-tab-new").click());
  await waitFor(win, () => !!document.querySelector(".empty-home-inner"));
  for (const width of [1024, 800, 760, 759, 600, 390, 320]) {
    win.setContentSize(width, 820);
    const geometry = await settledGeometry(win, width);
    checkGeometry(geometry);
    assert.ok(Math.abs(geometry.composerWidth - populated.get(width).composerWidth) <= 1,
      `Empty and populated sessions should have the same composer width: ${JSON.stringify(geometry)}`);
  }

  // A wide desktop window can still have a narrow conversation beside a sidebar.
  win.setContentSize(1024, 820);
  await settledGeometry(win, 1024);
  await evaluate(win, () => document.querySelector(".sidebar-toggle-button").click());
  await waitFor(win, () => !document.querySelector(".app-shell").classList.contains("sidebar-collapsed"));
  const besideSidebar = await settledGeometry(win, 1024);
  checkGeometry(besideSidebar);
  assert.ok(besideSidebar.composerWidth < populated.get(1024).composerWidth,
    "Composer should follow the available pane, not the whole window");

  console.log(`Session composer geometry verified at ${populated.size} widths, including empty/populated sessions, split/side threads, classic scrollbars and one-sided safe areas.`);
  win.close();
  app.quit();
}

function checkGeometry(value) {
  assert.ok(value.composerWidth > 0 && value.composerWidth <= 801, JSON.stringify(value));
  assert.ok(value.leftGap >= 11 && value.rightGap >= 11,
    `Composer must stay inside its available pane: ${JSON.stringify(value)}`);
  assert.ok(Math.abs(value.leftGap - value.rightGap) <= 1,
    `Composer must stay centered: ${JSON.stringify(value)}`);
  assert.ok(Math.abs(value.flowLeft - value.composerLeft) <= 1 &&
    Math.abs(value.flowRight - value.composerRight) <= 1,
    `Composer must align with the content column: ${JSON.stringify(value)}`);
}

async function settledGeometry(win, width, paneSelector = ".conversation-pane") {
  let previous;
  let stable = 0;
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    const value = await evaluate(win, (selector) => {
      const pane = document.querySelector(selector);
      const frame = pane?.querySelector(".hero-composer-wrap .composer-frame, .dock-composer-wrap .composer-frame, .split-composer .composer-frame");
      const scroll = pane?.querySelector(".scroll-region, .conversation-split-body, .side-thread-panel__body");
      const flow = scroll?.querySelector('.cached-conversation-pane[data-active="true"] .session-flow') ??
        scroll?.querySelector(".empty-home-inner, .conversation-split-width, .side-thread-panel__conversation");
      if (!frame || !flow || !scroll) return null;
      const rect = frame.getBoundingClientRect();
      const flowRect = flow.getBoundingClientRect();
      const flowStyle = getComputedStyle(flow);
      const scrollRect = scroll.getBoundingClientRect();
      const scrollStyle = getComputedStyle(scroll);
      const availableRight = scrollRect.left + scroll.clientWidth - parseFloat(scrollStyle.paddingRight);
      return {
        windowWidth: innerWidth,
        compact: document.querySelector(".app-shell").classList.contains("compact-navigation"),
        composerWidth: rect.width,
        composerLeft: rect.left,
        composerRight: rect.right,
        leftGap: rect.left - scrollRect.left,
        rightGap: availableRight - rect.right,
        scrollbarWidth: scroll.offsetWidth - scroll.clientWidth,
        scrollbarMode: scrollStyle.scrollbarWidth,
        scrollbarGutter: scrollStyle.scrollbarGutter,
        flowLeft: flowRect.left + parseFloat(flowStyle.paddingLeft),
        flowRight: flowRect.right - parseFloat(flowStyle.paddingRight)
      };
    }, paneSelector);
    if (value?.windowWidth === width && value.compact === (width < 760) && JSON.stringify(value) === previous) {
      stable += 1;
      if (stable >= 4) return value;
    } else {
      stable = 0;
    }
    previous = JSON.stringify(value);
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error(`Geometry did not settle at ${width}: ${previous}`);
}

async function waitFor(win, predicate) {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    if (await evaluate(win, predicate)) return;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error(`Timed out: ${predicate}`);
}

function evaluate(win, fn, ...args) {
  return win.webContents.executeJavaScript(`(${fn.toString()})(...${JSON.stringify(args)})`, true);
}
