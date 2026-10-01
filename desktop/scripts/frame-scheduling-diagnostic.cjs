// Isolate native-window scheduling from application rendering. This records
// evidence; it deliberately has no latency threshold or product assertions.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { performance } = require("node:perf_hooks");
const { createHash } = require("node:crypto");
const { app, BrowserWindow, screen } = require("electron");

const output = process.env.WUU_FRAME_DIAGNOSTIC_OUTPUT || path.resolve("out/frame-scheduling");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
app.on("window-all-closed", () => {});
const rows = [];
let win;
let environment;
const evaluate = (fn) => win.webContents.executeJavaScript(`(${fn})()`, true);
const save = () => fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ environment, rows }, null, 2));

async function frames(label) {
  const started = performance.now();
  const result = await evaluate(() => new Promise(resolve => {
    const times = [];
    const requestedAt = performance.now();
    function tick(timestamp) {
      times.push({ timestamp, now: performance.now(), visibility: document.visibilityState, focus: document.hasFocus() });
      if (times.length < 8) requestAnimationFrame(tick);
      else resolve({ requestedAt, times, width: innerWidth, height: innerHeight });
    }
    requestAnimationFrame(tick);
  }));
  assert.equal(result.times.length, 8);
  assert.ok(result.width > 0 && result.height > 0, "A nonzero viewport is required");
  rows.push({ label, hostMs: performance.now() - started, visible: win.isVisible(), focused: win.isFocused(), ...result });
  save();
  console.log(`${label}: ${rows.at(-1).hostMs.toFixed(1)} ms`);
}

app.whenReady().then(async () => {
  environment = {
    versions: process.versions, platform: process.platform, release: os.release(),
    sourceCommit: process.env.WUU_FRAME_DIAGNOSTIC_COMMIT || null,
    scriptSha256: createHash("sha256").update(fs.readFileSync(__filename)).digest("hex"),
    display: screen.getPrimaryDisplay(), arguments: process.argv.slice(1),
    backgroundThrottling: false, framelessControl: process.env.WUU_FRAME_DIAGNOSTIC_FRAMELESS === "1",
    protocol: "Fresh windows in ABBA order; 8 rAF before/after capture and next batch, and after 1500 ms idle; same hidden window then shown",
  };
  for (const [round, shown] of [["A-hidden", false], ["B-shown", true], ["B-shown-repeat", true], ["A-hidden-repeat", false]]) {
    win = new BrowserWindow({
      width: 1180, height: 820, show: shown,
      frame: !environment.framelessControl,
      webPreferences: { contextIsolation: true, sandbox: false, backgroundThrottling: false },
    });
    await win.loadURL("data:text/html,<html><body>Frame scheduling diagnostic</body></html>");
    await frames(`${round}:before-capture`);
    const captureStarted = performance.now();
    const png = (await win.webContents.capturePage()).toPNG();
    rows.push({ label: `${round}:capture`, hostMs: performance.now() - captureStarted, bytes: png.length });
    await frames(`${round}:after-capture`);
    await frames(`${round}:next-batch`);
    // Capture may temporarily wake the compositor; sample after quiescence too.
    const idleStarted = performance.now();
    await new Promise(resolve => setTimeout(resolve, 1500));
    rows.push({ label: `${round}:host-idle`, hostMs: performance.now() - idleStarted });
    await frames(`${round}:after-idle`);
    if (!shown) {
      win.show();
      await frames(`${round}:shown-same-window`);
    }
    win.destroy();
  }
  save();
  app.exit(0);
}).catch(error => {
  console.error(error);
  save();
  fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack));
  app.exit(1);
});
setTimeout(() => {
  fs.writeFileSync(path.join(output, "failure.txt"), "Frame scheduling diagnostic exceeded 90 seconds");
  save();
  app.exit(2);
}, 90000).unref();
