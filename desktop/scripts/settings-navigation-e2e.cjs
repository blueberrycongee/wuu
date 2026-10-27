// Run through wuu-e2e: real renderer, synthetic startup, no credentials or model calls.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/e2e/settings-navigation");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "settings-navigation-fixture.cjs");

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1180, height: 820, show: true, webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true,
    nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(value => JSON.stringify(value)).join(",")})`);
  const until = fn => win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
    const predicate = (${fn});
    const observer = new MutationObserver(check);
    const timeout = setTimeout(() => { observer.disconnect(); reject(new Error("Startup condition timed out")); }, 20000);
    function check() { if (predicate()) { observer.disconnect(); clearTimeout(timeout); resolve(); } }
    observer.observe(document, { childList: true, subtree: true, attributes: true });
    check();
  })`);
  const capture = async name => {
    await evaluate(async () => {
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      await Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
    });
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  };
  try {
    await win.loadFile(path.join(desktop, "out/renderer/index.html"));
    win.setContentSize(1180, 820);
    await until(() => document.querySelector(".sidebar-account-trigger"));
    await evaluate(() => document.querySelector(".sidebar-account-trigger").click());
    await until(() => document.querySelector('[data-settings-page="providers"]'));
    await evaluate(() => document.querySelector('[data-settings-page="providers"]').click());
    await until(() => document.querySelector('.app-shell[data-settings-open]'));
    const measurements = [];
    await until(() => document.querySelector('[data-testid="settings-connection-add"]'));
    for (const theme of ["light", "dark"]) {
      for (const size of [15, 20]) {
        for (const width of [980, 1280]) {
          win.setContentSize(width, 640);
          await evaluate(({ theme, size }) => {
            document.documentElement.dataset.theme = theme;
            document.documentElement.style.setProperty("--font-ui", `${size}px`);
          }, { theme, size });
          const label = `settings-actions-${theme}-${size}-${width}`;
          await capture(label);
          const geometry = await evaluate(() => {
            const button = document.querySelector('[data-testid="settings-connection-add"]');
            const title = document.querySelector('.settings-titlebar-slot > .settings-page-title');
            const rect = button.getBoundingClientRect();
            const range = document.createRange();
            range.selectNodeContents(button);
            const textRects = [...range.getClientRects()].filter(rect => rect.width > 0 && rect.height > 0);
            return { button: rect.toJSON(), title: title.getBoundingClientRect().toJSON(),
              textRects: textRects.map(rect => rect.toJSON()), viewportWidth: innerWidth };
          });
          assert.ok(geometry.button.width > 0 && geometry.button.right <= geometry.viewportWidth, `${label}: action stays visible`);
          assert.ok(geometry.title.right <= geometry.button.left, `${label}: title does not overlap action`);
          for (const rect of geometry.textRects) {
            assert.ok(rect.left >= geometry.button.left && rect.right <= geometry.button.right
              && rect.top >= geometry.button.top && rect.bottom <= geometry.button.bottom, `${label}: action contents stay inside button`);
          }
          measurements.push({ label, ...geometry });
        }
      }
    }
    await evaluate(() => {
      document.documentElement.dataset.theme = "light";
      document.documentElement.style.setProperty("--font-ui", "15px");
    });
    const checkScroll = async (selector, fixedSelectors, label) => {
      const result = await evaluate(async ({ selector, fixedSelectors }) => {
        const node = document.querySelector(selector);
        const fixed = fixedSelectors.map(selector => document.querySelector(selector));
        const before = fixed.map(node => node.getBoundingClientRect().toJSON());
        node.scrollTop = node.scrollHeight;
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        const pane = node.closest(".sidebar-content");
        return {
          before, after: fixed.map(node => node.getBoundingClientRect().toJSON()),
          scrollTop: node.scrollTop,
          edgeGap: pane.getBoundingClientRect().right - node.getBoundingClientRect().right,
          horizontalOverflow: node.scrollWidth - node.clientWidth,
        };
      }, { selector, fixedSelectors });
      assert.ok(result.scrollTop > 0, `${label}: content scrolls`);
      assert.deepEqual(result.after, result.before, `${label}: header stays fixed`);
      assert.ok(Math.abs(result.edgeGap) <= 1, `${label}: scrollbar reaches pane edge`);
      assert.ok(result.horizontalOverflow <= 1, `${label}: no horizontal overflow`);
      measurements.push({ label, ...result });
      await capture(label);
    };
    win.setContentSize(980, 540);
    await capture("settings-before-return");
    await checkScroll(".settings-nav", [".settings-navigation-title", ".settings-search"], "settings-scroll");
    await evaluate(() => document.querySelector('[data-wuu-component="conversations-section"]').click());
    await until(() => !document.querySelector('.app-shell[data-settings-open]') && document.querySelector(".conversation-pane") && document.querySelector(".conversation-pane textarea"));
    await capture("conversation-after-settings");
    await evaluate(() => document.querySelector('[data-ribbon-id="catalog"]').click());
    await until(() => document.querySelector(".sidebar-plugin-shortcut"));
    for (const theme of ["light", "dark"]) {
      for (const size of [15, 20]) {
        for (const width of [980, 1280]) {
          win.setContentSize(width, 640);
          await evaluate(({ theme, size }) => {
            document.documentElement.dataset.theme = theme;
            document.documentElement.style.setProperty("--font-ui", `${size}px`);
            document.querySelector(".sidebar-section-scroll").scrollTop = 0;
          }, { theme, size });
          await capture(`catalog-${theme}-${size}-${width}-top`);
          await checkScroll(".sidebar-section-scroll", [".sidebar-section-title", ".sidebar-section-navigation > button"], `catalog-${theme}-${size}-${width}-bottom`);
        }
      }
    }
    fs.writeFileSync(path.join(output, "measurements.json"), JSON.stringify(measurements, null, 2));
    fs.writeFileSync(path.join(output, "result.json"), JSON.stringify({ passed: true, scope: "Settings external connections to conversations via sidebar ribbon" }, null, 2));
    app.exit(0);
  } catch (error) {
    console.error(error);
    await capture("failure");
    app.exit(1);
  }
});
