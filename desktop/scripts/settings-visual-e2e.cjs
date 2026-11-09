// Screenshot pass over the settings pages for visual review: real renderer,
// synthetic startup via the navigation fixture, no credentials or model calls.
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/e2e/settings-visual");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "settings-navigation-fixture.cjs");

const pages = process.argv.includes("--pages")
  ? process.argv[process.argv.indexOf("--pages") + 1].split(",")
  : ["general", "appearance", "providers", "usage", "archive", "mcp"];
const themes = process.argv.includes("--themes")
  ? process.argv[process.argv.indexOf("--themes") + 1].split(",")
  : ["dark", "light"];

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1280, height: 820, show: true, webPreferences: {
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
    win.setContentSize(1280, 820);
    await until(() => document.querySelector('[data-settings-entry="settings"]'));
    await evaluate(() => document.querySelector('[data-settings-entry="settings"]').click());
    await until(() => document.querySelector('.app-shell[data-settings-open]'));
    for (const theme of themes) {
      await evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      for (const page of pages) {
        await evaluate(page => document.querySelector(`.settings-nav [data-wuu-page="${page}"]`)?.click(), page);
        await new Promise(resolve => setTimeout(resolve, 400));
        await capture(`${page}-${theme}`);
      }
    }
    app.exit(0);
  } catch (error) {
    console.error(error);
    await capture("failure");
    app.exit(1);
  }
});
