// Run through wuu-e2e: real renderer, synthetic startup, no credentials or model calls.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/e2e/runtime-loading");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "runtime-loading-fixture.cjs");

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
    await until(() => document.querySelector('[role="status"]'));
    assert.equal(await evaluate(() => Boolean(document.querySelector('[role="status"] svg'))), true, "Startup must render the shared loading identity");
    await capture("starting-light-wide");
    await until(() => document.querySelector('[role="status"]')?.getAttribute("aria-describedby"));
    assert.equal(await evaluate(() => Boolean(document.querySelector('[role="status"] button'))), false, "Waiting must not invite concurrent retries");
    win.setContentSize(420, 620);
    await evaluate(() => {
      document.documentElement.dataset.theme = "dark";
      document.documentElement.style.setProperty("--conversation-message-font-size", "20px");
      document.documentElement.dataset.appearanceMotion = "reduce";
    });
    await capture("waiting-dark-narrow-large-reduced");
    assert.equal(await evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, "Startup must fit a narrow window");
    await evaluate(() => window.wuu.testStartup("Synthetic startup failure: " + "long-diagnostic-".repeat(30)));
    await until(() => document.querySelector('[role="alert"] button'));
    assert.equal(await evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, "Long failures must wrap");
    await capture("failed-dark-narrow-large");
    const reloaded = new Promise(resolve => win.webContents.once("did-finish-load", resolve));
    await evaluate(() => document.querySelector('[role="alert"] button').click());
    await reloaded;
    await until(() => document.querySelector('[role="status"]'));
    await evaluate(() => window.wuu.testStartup("ready"));
    await until(() => document.querySelector(".conversation-pane"));
    assert.equal(await evaluate(() => Boolean(document.querySelector(".application-recovery"))), false);
    fs.writeFileSync(path.join(output, "result.json"), JSON.stringify({ passed: true, scope: "Startup wait, long wait, failure and user retry to ready; light/dark, wide/narrow, large UI font and reduced motion" }, null, 2));
    app.exit(0);
  } catch (error) {
    console.error(error);
    await capture("failure");
    app.exit(1);
  }
});
