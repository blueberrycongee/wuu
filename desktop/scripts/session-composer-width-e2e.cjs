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
  await win.loadFile(process.env.WUU_E2E_RENDERER || path.join(desktopRoot, "out", "renderer", "index.html"));
  await waitFor(win, () => !!document.querySelector(".thread-row-main"));
  await evaluate(win, () => [...document.querySelectorAll(".thread-row-main")].find(row => row.textContent.includes("Resize fixture")).click());
  await waitFor(win, () => !!document.querySelector(".turn"));

  // Hold navigation closed while crossing its breakpoint to distinguish a
  // gutter discontinuity from an intentional sidebar layout change.
  await evaluate(win, () => {
    for (const toggle of document.querySelectorAll(
      '.environment-toggle-button[aria-pressed="true"], [data-wuu-component=right-sidebar-toggle][aria-pressed="true"]'
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
    checkReadingWidth(geometry);
    populated.set(width, geometry);
    if (width === 1380 || width === 390) {
      fs.writeFileSync(path.join(userData, `conversation-${width}.png`), (await win.webContents.capturePage()).toPNG());
    }
  }
  fs.writeFileSync(path.join(userData, "geometry.json"), JSON.stringify([...populated.entries()], null, 2));
  console.log(`Geometry and screenshots: ${userData}`);
  for (const theme of ["light", "dark"]) {
    for (const size of [15, 20]) {
      await evaluate(win, ({ theme, size }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
        window.dispatchEvent(new Event("wuu-content-size-change"));
      }, { theme, size });
      for (const width of [1380, 600]) {
        win.setContentSize(width, 820);
        const geometry = await settledGeometry(win, width);
        checkGeometry(geometry);
        checkReadingWidth(geometry);
        fs.writeFileSync(path.join(userData, `${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
      }
    }
  }
  await evaluate(win, () => {
    document.documentElement.style.removeProperty("--conversation-message-font-size");
    document.documentElement.dataset.theme = "light";
    window.dispatchEvent(new Event("wuu-content-size-change"));
  });
  await evaluate(win, () => {
    const key = 'wuu.appearance.v1';
    const appearance = JSON.parse(localStorage.getItem(key) || '{}');
    localStorage.setItem(key, JSON.stringify({ ...appearance, messageStyle: 'im' }));
    document.documentElement.style.setProperty("--conversation-message-font-size", "20px");
    window.dispatchEvent(new Event("wuu-content-size-change"));
  });
  for (const width of [1380, 600, 390]) {
    win.setContentSize(width, 820);
    const geometry = await settledGeometry(win, width);
    checkGeometry(geometry);
    checkReadingWidth(geometry);
    const frame = await evaluate(win, () => {
      const message = document.querySelector('.cached-conversation-pane[data-active="true"] .user-message');
      const bounds = message.getBoundingClientRect();
      return { left: bounds.left, right: bounds.right, width: bounds.width, style: document.documentElement.dataset.messageStyle,
        textPadding: parseFloat(getComputedStyle(message).paddingLeft) };
    });
    assert.equal(frame.style, "im");
    assert.ok(frame.textPadding >= 6, `IM text needs an inset inside its frame: ${JSON.stringify(frame)}`);
    assert.ok(frame.width >= geometry.flowRight - geometry.flowLeft - 1 && frame.width <= geometry.composerWidth + 1
      && frame.left >= geometry.composerLeft - 1 && frame.right <= geometry.composerRight + 1,
      `IM frame should fit the composer width: ${JSON.stringify({ geometry, frame })}`);
    await evaluate(win, () => document.querySelector('.cached-conversation-pane[data-active="true"] .user-message').scrollIntoView({ block: "center" }));
    await waitFor(win, () => !!document.querySelector('.jump-to-latest-cluster'));
    const statusCenter = await evaluate(win, () => {
      const bounds = document.querySelector('.jump-to-latest-cluster').getBoundingClientRect();
      return bounds.left + bounds.right;
    });
    assert.ok(Math.abs(statusCenter - geometry.composerLeft - geometry.composerRight) <= 2,
      `Floating status and composer must share a centerline: ${JSON.stringify({ geometry, statusCenter })}`);
    fs.writeFileSync(path.join(userData, `im-20-${width}.png`), (await win.webContents.capturePage()).toPNG());
  }
  await evaluate(win, () => {
    const key = 'wuu.appearance.v1';
    const appearance = JSON.parse(localStorage.getItem(key) || '{}');
    localStorage.setItem(key, JSON.stringify({ ...appearance, messageStyle: 'bubble' }));
    document.documentElement.style.removeProperty("--conversation-message-font-size");
    window.dispatchEvent(new Event("wuu-content-size-change"));
  });
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

  // The environment panel is currently feature-disabled. Exercise its
  // reserved-layout CSS directly so it cannot silently misalign the dock.
  await evaluate(win, () => document.querySelector('.conversation-pane').classList.add('environment-panel-reserved'));
  for (const width of [1380, 1024]) {
    win.setContentSize(width, 820);
    const geometry = await settledGeometry(win, width);
    checkGeometry(geometry);
    checkReadingWidth(geometry);
  }
  await evaluate(win, () => document.querySelector('.conversation-pane').classList.remove('environment-panel-reserved'));

  win.setContentSize(1024, 820);
  await settledGeometry(win, 1024);
  await evaluate(win, () => document.querySelector('button[aria-label="Open related session"], button[aria-label="打开关联会话"]').click());
  await waitFor(win, () => document.querySelectorAll(".conversation-split-pane").length === 2);
  for (const width of [1024, 800, 600]) {
    win.setContentSize(width, 820);
    for (const side of ["first-child", "last-child"]) {
      checkGeometry(await settledGeometry(win, width, `.conversation-split-pane:${side}`));
    }
    const split = await evaluate(win, () => {
      const scroll = document.querySelector('.split-scroll-region');
      const content = scroll.querySelector(':scope > .scroll-region-content');
      const panes = [...content.querySelectorAll('.conversation-split-pane')].map(pane => pane.getBoundingClientRect().width);
      return { available: scroll.clientWidth, rail: parseFloat(getComputedStyle(content).paddingRight), panes };
    });
    assert.ok(split.panes.every(pane => pane >= (split.available - split.rail) / 2 - 1),
      `Split panes should use the available width beside the rail: ${JSON.stringify(split)}`);
    if (width === 600) fs.writeFileSync(path.join(userData, 'split-600.png'), (await win.webContents.capturePage()).toPNG());
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

  for (const width of [800, 390]) {
    win.setContentSize(width, 820);
    const geometry = await settledGeometry(win, width);
    assert.ok(geometry.scrollbarWidth > 0, `Main history must reserve a classic scrollbar: ${JSON.stringify(geometry)}`);
    checkGeometry(geometry);
    checkReadingWidth(geometry);
  }

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
  await evaluate(win, () => document.querySelector('[data-wuu-tab-kind="side-thread"] .workspace-tool-tab-close').click());
  await waitFor(win, () => !document.querySelector(".side-thread-panel"));

  win.setContentSize(720, 820);
  await evaluate(win, () => {
    document.querySelector('.sidebar-toggle-button').click();
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    window.dispatchEvent(new Event('wuu-content-size-change'));
  });
  await waitFor(win, () => !document.querySelector('.app-shell').classList.contains('sidebar-collapsed'));
  const populatedBesideSidebar = await settledGeometry(win, 720);
  checkGeometry(populatedBesideSidebar);
  checkReadingWidth(populatedBesideSidebar);
  fs.writeFileSync(path.join(userData, 'populated-sidebar-20-720.png'), (await win.webContents.capturePage()).toPNG());
  await evaluate(win, () => {
    document.querySelector('.sidebar-toggle-button').click();
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    window.dispatchEvent(new Event('wuu-content-size-change'));
  });
  await waitFor(win, () => document.querySelector('.app-shell').classList.contains('sidebar-collapsed'));

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

  win.setContentSize(720, 820);
  await evaluate(win, () => {
    document.documentElement.style.setProperty("--conversation-message-font-size", "20px");
    window.dispatchEvent(new Event("wuu-content-size-change"));
  });
  const narrowSidebar = await settledGeometry(win, 720);
  checkGeometry(narrowSidebar);
  checkReadingWidth(narrowSidebar);
  fs.writeFileSync(path.join(userData, "sidebar-20-720.png"), (await win.webContents.capturePage()).toPNG());

  console.log(`Session composer geometry verified at ${populated.size} widths, including empty/populated sessions, split/side threads, classic scrollbars and one-sided safe areas.`);
  win.close();
  app.quit();
}

function checkGeometry(value) {
  assert.ok(value.composerWidth > 0, JSON.stringify(value));
  assert.ok(value.leftGap >= 11 && value.rightGap >= 11,
    `Composer must stay inside its available pane: ${JSON.stringify(value)}`);
  assert.ok(Math.abs(value.leftGap - value.rightGap) <= 1,
    `Composer must stay centered in its pane: ${JSON.stringify(value)}`);
  assert.ok(value.composerWidth >= value.flowRight - value.flowLeft - 1,
    `Composer must be at least as wide as the content column: ${JSON.stringify(value)}`);
  assert.ok(value.flowLeft >= value.composerLeft - 1 && value.flowRight <= value.composerRight + 1,
    `The content column must fit within the composer: ${JSON.stringify(value)}`);
}

function checkReadingWidth(value) {
  assert.ok(value.flowRight - value.flowLeft >= Math.min(value.readingTarget, value.rightOnlyReadingWidth) - 1,
    `Avoid sacrificing a readable measure to a matching empty rail gutter: ${JSON.stringify(value)}`);
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
      const content = scroll.querySelector(":scope > .scroll-region-content");
      const contentStyle = content ? getComputedStyle(content) : null;
      const scrollbarWidth = scroll.offsetWidth - scroll.clientWidth;
      const railInset = pane.querySelector('[data-main-conversation-composer="dock"]')
        ? parseFloat(contentStyle?.paddingRight || "0") : 0;
      const contentLeft = scrollRect.left + scrollbarWidth / 2;
      const contentRight = scrollRect.right - scrollbarWidth / 2;
      return {
        windowWidth: innerWidth,
        composerWidth: rect.width,
        composerLeft: rect.left,
        composerRight: rect.right,
        leftGap: rect.left - contentLeft,
        rightGap: contentRight - rect.right,
        scrollbarWidth,
        railInset,
        readingTarget: 20 * parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--conversation-message-font-size")),
        rightOnlyReadingWidth: scroll.clientWidth - parseFloat(scrollStyle.paddingLeft) - parseFloat(scrollStyle.paddingRight)
          - railInset - parseFloat(flowStyle.paddingLeft) - parseFloat(flowStyle.paddingRight),
        scrollbarMode: scrollStyle.scrollbarWidth,
        scrollbarGutter: scrollStyle.scrollbarGutter,
        flowLeft: flowRect.left + parseFloat(flowStyle.paddingLeft),
        flowRight: flowRect.right - parseFloat(flowStyle.paddingRight)
      };
    }, paneSelector);
    if (value?.windowWidth === width && JSON.stringify(value) === previous) {
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
