// Production renderer with the shared synthetic bridge; no real user data or inference.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/project-panel-e2e");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
process.env.WUU_PROJECT_PANEL_E2E = "1";

app.whenReady().then(async () => {
  const timeout = setTimeout(() => app.exit(1), 60000);
  const results = [];
  try {
    const win = new BrowserWindow({ width: 1440, height: 900, show: false, webPreferences: {
      preload: path.join(__dirname, "streaming-e2e-preload.cjs"), sandbox: false,
      backgroundThrottling: false,
    } });
    const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(value => JSON.stringify(value)).join(",")})`, true);
    const wait = (selector, text) => evaluate(async (selector, text) => {
      const deadline = performance.now() + 5000;
      while (performance.now() < deadline) {
        if (document.querySelector(selector)?.textContent.includes(text)) return;
        await new Promise(requestAnimationFrame);
      }
      throw new Error(`Missing ${selector}: ${text}`);
    }, selector, text);
    const select = (title) => evaluate((title) => {
      const button = [...document.querySelectorAll(".sidebar button")].find(button => button.textContent === title);
      if (!button) throw new Error(`Missing conversation ${title}`);
      button.click();
    }, title);
    for (const [theme, size, width] of [["light", 14, 1440], ["dark", 20, 1440], ["light", 20, 1000]]) {
      win.setSize(width, 900);
      await win.loadFile(path.join(desktop, "out/renderer/index.html"));
      await wait(".sidebar", "Project Alpha");
      await evaluate((theme, size) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
        document.documentElement.style.setProperty("--appearance-scale", String(size / 14));
      }, theme, size);
      await select("Project Alpha");
      await wait(".conversation-title-heading", "Project Alpha");
      await evaluate(async () => {
        while (!document.querySelector(".project-status-capsule")) await new Promise(requestAnimationFrame);
        document.querySelector(".project-status-capsule").click();
      });
      await wait(".project-panel", "Alpha worker");
      await select("Project Beta");
      await wait(".project-panel", "Beta worker");
      assert.equal(await evaluate(() => document.querySelector(".project-panel").textContent.includes("Alpha worker")), false);
      await evaluate(() => document.querySelector(".project-panel-row-main").click());
      await wait(".conversation-title-heading", "Beta worker");
      await wait(".project-panel h2", "Project Beta");
      await select("Project Alpha");
      await wait(".project-panel h2", "Project Alpha");
      await evaluate(async () => {
        await document.fonts.ready;
        while (document.querySelector(".right-panel-animating")) await new Promise(requestAnimationFrame);
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        document.getAnimations().filter(a => a.effect.getComputedTiming().iterations !== Infinity).forEach(a => a.finish());
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      });
      const screenshot = `${theme}-${size}-${width}.png`;
      fs.writeFileSync(path.join(output, screenshot), (await win.webContents.capturePage()).toPNG());
      await select("Ordinary conversation");
      await wait(".conversation-title-heading", "Ordinary conversation");
      assert.equal(await evaluate(() => Boolean(document.querySelector(".project-panel"))), false);
      results.push({ theme, size, width, screenshot, passed: true });
    }
    fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
    console.log(JSON.stringify({ output, results }, null, 2));
    clearTimeout(timeout);
    app.exit(0);
  } catch (error) {
    console.error(error);
    app.exit(1);
  }
});
