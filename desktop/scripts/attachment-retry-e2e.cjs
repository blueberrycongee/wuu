// After electron-vite build: electron scripts/attachment-retry-e2e.cjs
// Production renderer, native Chromium input/FileReader, synthetic backend.
// No model, credentials, real workspace files, or third-party network requests.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const { app, BrowserWindow, ipcMain } = require("electron");
const { attachComposerFiles } = require("./composer-attachment-e2e.cjs");
const root = path.resolve(__dirname, "..");
const output = process.env.WUU_ATTACHMENT_E2E_OUTPUT || path.join(root, "out/e2e/attachment-retry");
const renderer = process.env.WUU_E2E_RENDERER || path.join(root, "out/renderer/index.html");
const owner = "[data-main-conversation-composer]";
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-attachment-retry-"));
app.setPath("userData", profile);
process.env.HOME = profile;
app.on("window-all-closed", () => {});
process.env.WUU_RESIZE_E2E_CWD = profile;
process.env.WUU_RESIZE_LIVE_E2E = "1";
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "attachment-retry-e2e-preload.cjs");
const report = { renderer, runnerSha256: crypto.createHash("sha256").update(fs.readFileSync(__filename)).digest("hex"),
  fixture: "production renderer with synthetic IPC backend", checks: [], screenshots: [], errors: [], trustedInteractions: [], console: [] };
let win, resume, rejectNextSubmission = false, submissions = [];
const save = () => { fs.mkdirSync(output, { recursive: true }); fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2)); };
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
async function until(fn, arg, label = "UI state") {
  const deadline = Date.now() + 12000;
  while (Date.now() < deadline) {
    if (await evaluate(fn, arg)) return;
    await delay(20);
  }
  throw new Error(`Timed out waiting for ${label}`);
}
async function screenshot(name) {
  await evaluate(async () => {
    await document.fonts.ready;
    await Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  const file = `${name}.png`;
  fs.writeFileSync(path.join(output, file), (await win.webContents.capturePage()).toPNG());
  report.screenshots.push(file); save();
}
async function start() {
  if (win) win.destroy();
  submissions = [];
  rejectNextSubmission = false;
  win = new BrowserWindow({ width: 1200, height: 820, show: true,
    ...(process.platform === "linux" ? { frame: false, autoHideMenuBar: true } : {}),
    webPreferences: { preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true,
      nodeIntegration: false, sandbox: false, backgroundThrottling: false } });
  win.webContents.session.webRequest.onBeforeRequest(({ url }, callback) => callback({ cancel: /^https?:/.test(url) }));
  win.webContents.on("console-message", event => report.console.push(event.message));
  await win.loadFile(renderer);
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await until(() => !!document.querySelector('.turn') && !!document.querySelector('[data-main-conversation-composer] textarea'));
  await evaluate(() => {
    const original = File.prototype.arrayBuffer;
    const state = window.__attachmentReadFixture = { count: 0, pending: null };
    File.prototype.arrayBuffer = function () {
      if (!this.name.startsWith("retry-")) return original.call(this);
      state.count += 1;
      const file = this;
      return new Promise((resolve, reject) => {
        state.pending = { succeed: () => original.call(file).then(resolve, reject), fail: () => reject(new Error("Synthetic file temporarily unavailable")) };
      });
    };
  });
}
async function click(selector, count = 1) {
  const point = await evaluate(async selector => {
    const element = document.querySelector(selector);
    if (!element) throw new Error("Missing click target: " + selector);
    element.scrollIntoView({ block: "center", inline: "center" });
    let previous = "", stable = 0;
    for (let frame = 0; frame < 120; frame += 1) {
      await new Promise(resolve => requestAnimationFrame(resolve));
      const bounds = element.getBoundingClientRect();
      const current = [bounds.x, bounds.y, bounds.width, bounds.height].join(",");
      stable = current === previous ? stable + 1 : 0; previous = current;
      if (stable >= 6) return { x: Math.round(bounds.x + bounds.width / 2), y: Math.round(bounds.y + bounds.height / 2) };
    }
    throw new Error("Click target did not settle: " + selector);
  }, selector);
  await evaluate(selector => {
    window.__lastTrustedClick = false;
    document.querySelector(selector).addEventListener("click", event => {
      window.__lastTrustedClick = event.isTrusted;
    }, { once: true });
  }, selector);
  for (let index = 0; index < count; index += 1) {
    win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
    win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
  }
  await until(() => window.__lastTrustedClick === true, undefined, "trusted native click");
  report.trustedInteractions.push({ selector, count });
}
async function attach(kind = "file") {
  if (kind === "image") {
    await evaluate((owner) => {
      const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGMQaPj/HwAEwgKP3LagKAAAAABJRU5ErkJggg==";
      const transfer = new DataTransfer();
      transfer.items.add(new File([Uint8Array.from(atob(png), value => value.charCodeAt(0))], "retry-image.png", { type: "image/png" }));
      document.querySelector(`${owner} textarea`).dispatchEvent(new ClipboardEvent("paste", {
        bubbles: true, cancelable: true, clipboardData: transfer,
      }));
    }, owner);
  } else {
    await attachComposerFiles(win, owner, [{ name: "retry-context.txt", type: "text/plain", contents: "Exact local context 🌊" }]);
  }
  await until(() => window.__attachmentReadFixture.count === 1, undefined, "first file read");
}
async function settle(fail) {
  await evaluate((fail) => window.__attachmentReadFixture.pending[fail ? "fail" : "succeed"](), fail);
}
async function failed() { await until(() => !!document.querySelector(".composer-attachment-failed")); }
async function retry(count = 2) {
  await click(".composer-attachment-failed .composer-document-card-main", 2);
  await until((count) => window.__attachmentReadFixture.count === count, count);
}
async function ready() {
  await until(() => !!document.querySelector(".composer-attachment-card") &&
    !document.querySelector(".composer-attachment-failed, .composer-attachment-preparing, .composer-media-card[data-pending]"));
}
async function draft(text) {
  await evaluate((owner) => document.querySelector(`${owner} textarea`).focus(), owner);
  await win.webContents.insertText(text);
  await until(({ owner, text }) => document.querySelector(`${owner} textarea`)?.value === text, { owner, text });
}
async function send() { await click(`${owner} [data-wuu-component="composer-send"]`); }
async function check(name, run) {
  await start(); await run(); report.checks.push({ name, passed: true }); save(); console.log(`PASS ${name}`);
}
app.whenReady().then(async () => {
  save();
  ipcMain.on("test:attachment-submitted", (event, payload) => { if (event.sender === win.webContents) submissions.push(payload); });
  ipcMain.handle("test:attachment-submit-error", () => {
    const error = rejectNextSubmission ? "Synthetic send unavailable" : null;
    rejectNextSubmission = false;
    return error;
  });
  ipcMain.handle("test:attachment-resume", () => new Promise((resolve) => { resume = resolve; }));
  await check("failed attachment keeps identity and retries only when requested", async () => {
    await attach();
    const key = await evaluate(() => document.querySelector(".composer-attachment-tray-item").dataset.key);
    await settle(true); await failed();
    assert.equal(submissions.length, 0);
    await screenshot("failed-wide");
    await click('[data-wuu-component="sidebar-toggle"]');
    win.setContentSize(560, 820);
    await evaluate(() => { document.documentElement.style.setProperty("--conversation-message-font-size", "20px"); document.documentElement.style.setProperty("--appearance-scale", String(20 / 14)); document.documentElement.dataset.theme = "dark"; });
    await screenshot("failed-narrow-large-dark");
    await retry(); await settle(false); await ready();
    await screenshot("recovered-narrow-large-dark");
    win.setContentSize(1200, 820);
    await click('[data-wuu-component="sidebar-toggle"]');
    assert.equal(await evaluate(() => window.__attachmentReadFixture.count), 2);
    assert.equal(await evaluate(() => document.querySelector(".composer-attachment-tray-item").dataset.key), key);
    assert.equal(submissions.length, 0, "successful retry must not send");
    await draft("Use this attachment"); await send();
    const deadline = Date.now() + 12000;
    while (!submissions.length && Date.now() < deadline) await delay(20);
    assert.equal(submissions.length, 1);
    assert.equal(submissions[0].threadId, "resize-thread");
    assert.deepEqual(submissions[0].files, [{ filename: "retry-context.txt", media_type: "text/plain", data: Buffer.from("Exact local context 🌊").toString("base64") }]);
  });
  await check("send-before-failure restores a draft whose rejected preparation can be retried", async () => {
    await attach(); await draft("Keep my failed send"); await send();
    await settle(true); await failed();
    await until(() => document.querySelector('[data-main-conversation-composer] textarea')?.value === "Keep my failed send");
    assert.equal(submissions.length, 0);
    await retry(); await settle(false); await ready();
    assert.equal(submissions.length, 0);
    await send();
    const deadline = Date.now() + 12000;
    while (!submissions.length && Date.now() < deadline) await delay(20);
    assert.equal(submissions.length, 1);
  });
  await check("removing an in-flight retry does not resurrect its attachment", async () => {
    await attach(); await settle(true); await failed(); await retry();
    await click(".composer-attachment-card-remove");
    await settle(false);
    await until(() => !document.querySelector(".composer-attachment-tray-item:not([data-exiting])"));
    assert.equal(submissions.length, 0);
  });
  await check("a repeatedly unreadable attachment waits for each explicit retry", async () => {
    await attach(); await settle(true); await failed();
    await retry(); await settle(true); await failed();
    await evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal(await evaluate(() => window.__attachmentReadFixture.count), 2);
    assert.equal(submissions.length, 0);
    await retry(3); await settle(false); await ready();
    assert.equal(submissions.length, 0);
  });
  await check("pasted image read failure retries without reusing a revoked preview", async () => {
    await attach("image"); await settle(true); await failed();
    await retry(); await settle(false); await ready();
    await until(() => {
      const image = document.querySelector(".composer-media-card-image");
      return image?.src.startsWith("data:image/png;base64,") && image.complete && image.naturalWidth > 0;
    }, undefined, "retry image preview");
    assert.equal(submissions.length, 0);
  });
  for (const kind of ["file", "image"]) {
    await check(`send failure after ${kind} encoding restores a ready attachment`, async () => {
      await attach(kind); await draft("Keep the prepared attachment");
      rejectNextSubmission = true;
      await send();
      assert.equal(submissions.length, 0, "send must await preparation");
      await settle(false);
      await until(() => document.querySelector('[data-main-conversation-composer] textarea')?.value === "Keep the prepared attachment");
      await ready();
      assert.equal(submissions.length, 1);
      if (kind === "image") {
        await until(() => {
          const image = document.querySelector(".composer-media-card-image");
          return image?.src.startsWith("data:image/png;base64,") && image.complete && image.naturalWidth > 0;
        }, undefined, "restored image preview");
      }
      await send();
      const deadline = Date.now() + 12000;
      while (submissions.length < 2 && Date.now() < deadline) await delay(20);
      assert.equal(submissions.length, 2);
      assert.deepEqual(submissions[1], submissions[0]);
      assert.equal(await evaluate(() => window.__attachmentReadFixture.count), 1, "ready files are not read again");
      assert.equal(await evaluate(() => document.querySelectorAll(".composer-attachment-tray-item:not([data-exiting])").length), 0);
    });
  }
  await check("failure during cold navigation remains with its original conversation", async () => {
    await attach();
    await evaluate(() => [...document.querySelectorAll(".thread-row-main")].find(row => row.textContent.includes("Attachment other conversation")).dataset.e2eSelect = "other");
    await click('[data-e2e-select="other"]');
    const deadline = Date.now() + 12000;
    while (!resume && Date.now() < deadline) await delay(20);
    assert.ok(resume, "cold navigation reached the synthetic backend");
    await settle(true); resume(); resume = null;
    await until(() => document.querySelector(".conversation-title-heading h1")?.textContent.includes("Attachment other conversation"));
    assert.equal(await evaluate(() => document.querySelectorAll(".composer-attachment-failed").length), 0);
    await evaluate(() => [...document.querySelectorAll(".thread-row-main")].find(row => row.textContent.includes("Resize fixture")).dataset.e2eSelect = "original");
    await click('[data-e2e-select="original"]');
    await failed(); await retry(); await settle(false); await ready();
    assert.equal(submissions.length, 0);
  });
  win.destroy(); report.passed = true; save(); app.quit();
}).catch(async (error) => {
  report.errors.push(String(error.stack || error)); report.passed = false; save();
  if (win && !win.isDestroyed()) { try { await screenshot("failure"); } catch {} win.destroy(); }
  console.error(error); app.exit(1);
});
