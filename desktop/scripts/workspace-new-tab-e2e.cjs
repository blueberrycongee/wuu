// Production renderer with the shared synthetic bridge. Verifies new-page
// selection/recovery and an installed extension through the public view API.
// Run: npm run test:e2e:workspace-new-tab; evidence: out/workspace-new-tab-e2e.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow, protocol } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/workspace-new-tab-e2e");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_STREAM_E2E_CWD = output;
process.env.WUU_WORKSPACE_NEW_TAB_E2E = "1";
protocol.registerSchemesAsPrivileged([{ scheme: "wuu-plugin", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);

const results = [];
app.whenReady().then(async () => {
  const timeout = setTimeout(() => { console.error("Workspace new page E2E timed out"); app.exit(1); }, 120_000);
  protocol.handle("wuu-plugin", () => new Response("export function activate(api) {"
    + "api.registerViewType({ id: 'notes', title: 'Workspace notes',"
    + "render: () => api.react.createElement('div', { 'data-workspace-e2e-notes': true }, 'Extension notes') });"
    + "}", { headers: { "Content-Type": "text/javascript", "Access-Control-Allow-Origin": "*" } }));
  const win = new BrowserWindow({ width: 1440, height: 900, show: false, titleBarStyle: "hiddenInset",
    webPreferences: { preload: path.join(__dirname, "streaming-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false } });
  win.webContents.on("console-message", ({ level, message }) => {
    if (level === "error") console.error("Renderer: " + message);
  });
  const evaluate = (fn, value) => win.webContents.executeJavaScript("(" + fn + ")(" + JSON.stringify(value ?? null) + ")", true);
  const waitFor = async (fn, value) => {
    const deadline = Date.now() + 8000;
    while (Date.now() < deadline) {
      const result = await evaluate(fn, value);
      if (result) return result;
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    throw new Error("Timed out: " + fn);
  };
  const click = async (selector) => {
    await waitFor((selector) => Boolean(document.querySelector(selector)), selector);
    await evaluate((selector) => document.querySelector(selector).click(), selector);
  };
  const tabs = () => evaluate(() => [...document.querySelectorAll('.workspace-panel-tabs [role="tab"]')].map((tab) => ({
    selected: tab.getAttribute("aria-selected") === "true", label: tab.getAttribute("aria-label"),
  })));
  const capture = async (name) => {
    await evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    // Scroll-driven edge fades have no elapsed-time finish.
    await waitFor(() => (document.querySelector(".workspace-right-panel")?.getAnimations({ subtree: true }) ?? [])
      .filter((animation) => animation.timeline instanceof DocumentTimeline && animation.effect?.getComputedTiming().iterations !== Infinity)
      .every((animation) => animation.playState !== "running"));
    fs.writeFileSync(path.join(output, name + ".png"), (await win.webContents.capturePage()).toPNG());
  };
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  // Visible compositor frames keep resize and focus acceptance deterministic.
  win.showInactive();
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await waitFor(() => Boolean(document.querySelector(".composer textarea")));
  await click("[data-wuu-component=right-sidebar-toggle]");
  await click(".workspace-panel-add");
  await waitFor(() => Boolean(document.querySelector('[data-wuu-view="new"] .workspace-tool-menu')));
  assert.equal((await tabs()).filter((tab) => tab.selected).length, 1);
  assert.equal(await evaluate(() => document.querySelectorAll('[data-wuu-component="workspace-resume-tab"]').length), 0);
  await capture("empty-light");

  // Both the dock and the full-width canvas use the same tab strip. Check
  // live hit targets and overflow, including selection after a resize.
  const checkTabGeometry = async (scenario) => {
    await capture(scenario);
    const geometry = await evaluate(() => {
      const strip = document.querySelector(".workspace-panel-tabs");
      const header = document.querySelector(".workspace-panel-tabbar");
      const active = strip.querySelector(".workspace-tool-tab.active");
      const box = (node) => node.getBoundingClientRect().toJSON();
      return { strip: box(strip), header: box(header), active: box(active),
        tabs: [...strip.querySelectorAll(".workspace-tool-tab:not(.closing)")].map((tab) => ({
          tab: box(tab), label: box(tab.querySelector(".workspace-tool-tab-main > span")),
          close: box(tab.querySelector(".workspace-tool-tab-close")),
        })) };
    });
    fs.writeFileSync(path.join(output, scenario + ".json"), JSON.stringify(geometry, null, 2));
    assert.ok(geometry.active.width <= geometry.strip.width + 1,
      "One tab fits the available strip even with large text and docked toolbar controls");
    assert.ok(Math.abs(geometry.active.bottom - geometry.header.bottom) <= 1,
      "The active tab meets its content edge");
    assert.ok(geometry.active.left >= geometry.strip.left - 1 && geometry.active.right <= geometry.strip.right + 1,
      "The selected tab remains in view");
    for (const { tab, label, close } of geometry.tabs) {
      assert.ok(label.width >= 16, "Overflow keeps tab labels readable rather than hiding them");
      assert.ok(close.width >= 24 && close.height >= 24, "Close retains its independent hit target");
      assert.ok(close.left >= label.right - 1 && close.right <= tab.right + 1, "Label and close lanes do not overlap");
    }
    results.push({ scenario, geometry });
  };
  await checkTabGeometry("tabs-docked-light");

  await click('[data-wuu-tool="review"]');
  await waitFor(() => Boolean(document.querySelector('[data-wuu-view="review"]')));
  const reviewTabs = await tabs();
  await click(".workspace-panel-add");
  await click('[data-wuu-tool="review"]');
  await waitFor((count) => document.querySelectorAll('.workspace-panel-tabs [role="tab"]').length === count, reviewTabs.length);
  assert.deepEqual(await tabs(), reviewTabs, "An existing singleton consumes the new page without duplication");
  results.push({ scenario: "new-page replacement and singleton reuse", passed: true });

  await click(".workspace-panel-add");
  await click(".workspace-panel-add");
  await waitFor(() => document.querySelectorAll('.workspace-panel-tabs [role="tab"]').length === 3);
  await click(".workspace-tool-tab.active .workspace-tool-tab-close");
  await waitFor(() => document.querySelectorAll('.workspace-panel-tabs [role="tab"]').length === 2);
  assert.equal(await evaluate(() => document.querySelector('[data-wuu-component="workspace-panel"]').dataset.wuuView), "new");
  results.push({ scenario: "multiple new pages and close recovery", passed: true });

  await waitFor(() => Boolean(document.querySelector(".workspace-tool-menu-more")));
  await evaluate(() => document.querySelector('[data-wuu-tool="browser"]').focus());
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Down" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Down" });
  await waitFor(() => document.activeElement?.tagName === "SUMMARY");
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Return" });
  win.webContents.sendInputEvent({ type: "char", keyCode: "\r" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Return" });
  await waitFor(() => document.querySelector(".workspace-tool-menu-more").open);
  await click('[data-wuu-plugin="user:workspace-e2e"]');
  await waitFor(() => Boolean(document.querySelector("[data-workspace-e2e-notes]")));
  assert.equal((await tabs()).length, 2);
  for (const theme of ["light", "dark"]) {
    for (const size of [14, 20]) {
      await evaluate(({ theme, size }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--font-ui", size + "px");
      }, { theme, size });
      await checkTabGeometry(`tabs-docked-${theme}-${size}`);
      await click(".workspace-panel-globalize");
      await checkTabGeometry(`tabs-canvas-${theme}-${size}`);
      await click(".workspace-panel-globalize");
    }
  }
  await evaluate(() => {
    document.documentElement.dataset.theme = "light";
    document.documentElement.style.setProperty("--font-ui", "14px");
  });

  results.push({ scenario: "keyboard discovery and installed extension selection", passed: true });

  for (let index = 0; index < 5; index++) await click(".workspace-panel-add");
  await waitFor(() => document.querySelectorAll('.workspace-panel-tabs [role="tab"]').length === 7);
  await checkTabGeometry("tabs-overflow-last");
  await evaluate(() => document.querySelector('.workspace-tool-tab.active [role="tab"]').focus());
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Home" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Home" });
  await waitFor(() => document.querySelector('.workspace-panel-tabs [role="tab"]').getAttribute("aria-selected") === "true");
  await checkTabGeometry("tabs-overflow-first");
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "End" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "End" });
  await waitFor(() => [...document.querySelectorAll('.workspace-panel-tabs [role="tab"]')].at(-1).getAttribute("aria-selected") === "true");
  await checkTabGeometry("tabs-overflow-keyboard-last");
  for (let count = 7; count > 2; count--) {
    await click(".workspace-tool-tab.active .workspace-tool-tab-close");
    await waitFor((count) => document.querySelectorAll('.workspace-panel-tabs [role="tab"]').length === count, count - 1);
  }


  // Open a real conversation file link; resume must retain the editor resource.
  await evaluate(() => {
    const input = document.querySelector(".composer textarea");
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set.call(input, "Open workspace notes");
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  });
  await waitFor(() => document.querySelector(".conversation-width")?.textContent.includes("Open workspace notes"));
  const threadID = "thread-immediate-title-e2e";
  win.webContents.send("test:server-event", { workdir: output, kind: "notification", message: {
    method: "turn/completed", params: { thread_id: threadID, turn: {
      id: "turn-" + threadID, status: "completed", items_view: "full", items: [
        { id: "file-link", type: "agent_message", status: "completed",
          text: "[Workspace notes](src/a-long-workspace-file-name-for-checking-new-page-truncation.md)" },
      ],
    } },
  } });
  await click(".conversation-width .rich-file-link");
  await waitFor(() => Boolean(document.querySelector('[data-wuu-view="file"]')));
  await click('[data-wuu-destination="files"]');
  await waitFor(() => document.querySelector('.workspace-right-panel').dataset.navigationMode === "files");
  for (const theme of ["light", "dark"]) {
    for (const size of [14, 20]) {
      await evaluate(({ theme, size }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--font-ui", size + "px");
      }, { theme, size });
      await checkTabGeometry(`tabs-files-${theme}-${size}`);
    }
  }
  await evaluate(() => {
    document.documentElement.dataset.theme = "light";
    document.documentElement.style.setProperty("--font-ui", "14px");
  });
  await click('[data-wuu-destination="conversations"]');
  await waitFor(() => !document.querySelector('.workspace-right-panel').dataset.navigationMode);
  const fileTabs = await tabs();
  const editorID = await evaluate(() => document.querySelector(".workspace-file-resource.active").dataset.workspaceTabId);
  await click(".workspace-panel-add");
  await click('[data-wuu-component="workspace-resume-tab"]');
  await waitFor((count) => document.querySelectorAll('.workspace-panel-tabs [role="tab"]').length === count, fileTabs.length);
  assert.deepEqual(await tabs(), fileTabs, "Resuming a file consumes the page without duplicate editor tabs");
  assert.equal(await evaluate(() => document.querySelector(".workspace-file-resource.active").dataset.workspaceTabId), editorID);
  results.push({ scenario: "resume actual open file", passed: true });
  await click(".workspace-panel-add");
  await click(".workspace-tool-menu-more > summary");

  for (const theme of ["light", "dark"]) {
    for (const size of [14, 20]) {
      await evaluate(({ theme, size }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--font-ui", size + "px");
      }, { theme, size });
      for (const width of [1440, 1000, 560]) {
        win.setSize(width, 900);
        await waitFor((width) => window.innerWidth === width, width);
        const geometry = await evaluate(() => {
          const menu = document.querySelector(".workspace-tool-menu");
          return { width: menu.getBoundingClientRect().width, clientWidth: menu.clientWidth, scrollWidth: menu.scrollWidth,
            scrollHeight: menu.scrollHeight, clientHeight: menu.clientHeight,
            rows: [...menu.querySelectorAll("button, summary")].filter((node) => node.getClientRects().length).map((node) => {
              const box = node.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height };
            }) };
        });
        assert.ok(geometry.width > 0, "The page remains visible");
        assert.ok(geometry.scrollWidth <= geometry.clientWidth + 1, "The page has no horizontal overflow");
        const name = [theme, size, width].join("-");
        results.push({ scenario: name, geometry });
        await capture(name);
      }
    }
  }
  win.setSize(560, 420);
  await waitFor(() => window.innerWidth === 560 && window.innerHeight === 420);
  const scroll = await evaluate(() => {
    const menu = document.querySelector(".workspace-tool-menu");
    const headerY = document.querySelector(".workspace-panel-tabbar").getBoundingClientRect().y;
    menu.scrollTop = menu.scrollHeight;
    return { scrollHeight: menu.scrollHeight, clientHeight: menu.clientHeight, headerY };
  });
  assert.ok(scroll.scrollHeight > scroll.clientHeight, "A short window scrolls the page rather than clipping its actions");
  await capture("dark-20-short-scrolled");
  assert.equal(await evaluate(() => document.querySelector(".workspace-panel-tabbar").getBoundingClientRect().y), scroll.headerY);
  results.push({ scenario: "short window scrolling with fixed header", geometry: scroll });

  win.setSize(1440, 900);
  await waitFor(() => window.innerWidth === 1440 && window.innerHeight === 900);
  await evaluate(() => {
    document.documentElement.dataset.theme = "light";
    document.documentElement.style.setProperty("--font-ui", "14px");
    document.querySelector(".workspace-tool-menu").scrollTop = 0;
    document.querySelector(".workspace-tool-menu-more").open = false;
  });
  await waitFor(() => !document.querySelector(".workspace-right-panel.compact-navigation"));
  await capture("light-14-default");
  await evaluate(() => document.querySelector(".workspace-tool-menu-more > summary").focus());
  await waitFor(() => document.activeElement === document.querySelector(".workspace-tool-menu-more > summary"));
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Down" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Down" });
  await waitFor(() => document.activeElement?.dataset.wuuComponent === "workspace-resume-tab");
  await capture("light-14-keyboard-focus");
  results.push({ scenario: "keyboard skips collapsed extensions", passed: true });
  const hover = await evaluate(() => {
    const box = document.querySelector('[data-wuu-tool="files"]').getBoundingClientRect();
    return { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) };
  });
  win.webContents.sendInputEvent({ type: "mouseMove", ...hover });
  await capture("light-14-hover");
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  win.webContents.sendInputEvent({ type: "mouseMove", x: 10, y: 10 });
  await click(".workspace-panel-add");
  await waitFor(() => Boolean(document.querySelector('[data-wuu-view="new"]')));
  await capture("light-14-reduced-motion");
  const panelBounds = await evaluate(() => {
    const box = document.querySelector(".workspace-right-panel").getBoundingClientRect();
    return { x: Math.round(box.x), y: Math.round(box.y), width: Math.round(box.width), height: Math.round(box.height) };
  });
  fs.writeFileSync(path.join(output, "preview-light.png"), (await win.webContents.capturePage(panelBounds)).toPNG());
  results.push({ scenario: "new page with reduced motion", passed: true });
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
  console.log("Workspace new page E2E passed. Evidence: " + output);
  clearTimeout(timeout);
  win.destroy(); app.quit();
}).catch((error) => {
  fs.writeFileSync(path.join(output, "failure.txt"), error.stack || String(error));
  console.error(error); app.exit(1);
});
