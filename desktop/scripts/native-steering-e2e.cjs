// Run after electron-vite build. Production Electron/renderer, synthetic backend,
// real clipboard. No model, credentials, or user workspace is used.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow, ipcMain, clipboard } = require("electron");
const root = path.resolve(__dirname, "..");
const output = process.env.WUU_STEER_UI_OUTPUT || path.join(root, "out/e2e/native-steering");
const renderer = process.env.WUU_E2E_RENDERER || path.join(root, "out/renderer/index.html");
const prompt = require("./native-steering-e2e-preload.cjs").prompt;
const state = { renderer, checks: [], screenshots: [], mutations: [], errors: [], runnerSha256: crypto.createHash("sha256").update(fs.readFileSync(__filename)).digest("hex") };
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(os.tmpdir(), "wuu-native-steer-ui-")));
process.env.WUU_RESIZE_E2E_CWD = path.dirname(root);
process.env.WUU_RESIZE_LIVE_E2E = "1";
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "native-steering-e2e-preload.cjs");
const save = () => fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(state, null, 2));
const delay = (ms) => new Promise(resolve => setTimeout(resolve, ms));
let win;
async function evaluate(fn, value) { return win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`, true); }
async function until(fn, value) {
  const end = Date.now() + 12000;
  while (Date.now() < end) { const result = await evaluate(fn, value); if (result) return result; await delay(30); }
  throw new Error(`Timed out during ${state.phase}`);
}
async function capture(name) {
  await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  // Wait for the native compositor to present after the drawer transition.
  await delay(180);
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  state.screenshots.push(`${name}.png`);
  save();
}
app.on("window-all-closed", () => {});
app.whenReady().then(async () => {
  const timer = setTimeout(() => { state.failure = "UI fixture timed out"; save(); app.exit(1); }, 90000);
  ipcMain.on("test:steer-mutation", (_event, mutation) => state.mutations.push(mutation));
  for (const config of [
    { name: "light-wide-default", theme: "light", width: 1200, size: 14, language: "en-US" },
    { name: "dark-narrow-large", theme: "dark", width: 760, size: 20, language: "zh-CN" },
  ]) {
    state.phase = config.name;
    process.env.WUU_STEER_UI_THEME = config.theme;
    process.env.WUU_STEER_UI_SIZE = String(config.size);
    process.env.WUU_STEER_UI_LANGUAGE = config.language;
    win = new BrowserWindow({ width: config.width, height: 900, show: true, frame: false, autoHideMenuBar: true, webPreferences: {
      preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
    } });
    win.webContents.on("console-message", ({ level, message }) => { if (level === "error") state.errors.push(message); });
    await win.loadFile(renderer);
    win.showInactive();
    win.webContents.debugger.attach("1.3");
    await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
    await until(() => Boolean(document.querySelector(".composer-pending-drawer")));
    assert.equal(await evaluate(() => document.documentElement.dataset.theme), config.theme);
    await evaluate(() => {
      const button = document.querySelector(".composer-pending-drawer .composer-drawer-summary-select");
      if (button.getAttribute("aria-expanded") !== "true") button.click();
    });
    await until(() => document.querySelectorAll(".composer-queue-row").length === 2);
    const rows = await evaluate(() => Array.from(document.querySelectorAll(".composer-queue-row"), row => ({
      reason: row.getAttribute("aria-description"), text: row.querySelector(".composer-queue-preview")?.textContent,
      previewTag: row.querySelector(".composer-queue-preview")?.tagName,
      actions: Array.from(row.querySelectorAll("button"), button => ({ label: button.getAttribute("aria-label"), disabled: button.disabled })),
    })));
    assert.ok(rows[0].reason);
    assert.equal(rows[0].previewTag, "SPAN");
    assert.deepEqual(rows[0].actions.map(action => action.disabled), [false, true, true]);
    assert.equal(rows[1].previewTag, "BUTTON");
    assert.ok(rows[1].actions.every(action => !action.disabled));
    await clipboard.writeText("");
    const point = await evaluate(() => {
      const button = document.querySelector(".composer-queue-row .composer-queue-actions button");
      const rect = button.getBoundingClientRect();
      const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2;
      if (Number(getComputedStyle(button).opacity) < 0.9 || !button.contains(document.elementFromPoint(x, y))) throw new Error("Copy action is not visibly clickable");
      return { x: Math.round(x), y: Math.round(y) };
    });
    win.webContents.sendInputEvent({ type: "mouseMove", ...point });
    win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
    win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
    const end = Date.now() + 3000;
    while (await clipboard.readText() !== prompt && Date.now() < end) await delay(20);
    assert.equal(await clipboard.readText(), prompt, "copy retains complete original content");
    await evaluate(() => document.querySelectorAll(".composer-queue-row:first-child button:disabled").forEach(button => button.click()));
    assert.equal(state.mutations.length, 0);
    await capture(config.name);
    await evaluate(() => document.querySelector(".composer-pending-drawer .composer-drawer-summary-select").click());
    await until(() => document.querySelector(".composer-pending-drawer .composer-drawer-summary-select")?.getAttribute("aria-expanded") === "false");
    assert.ok(await evaluate(() => document.querySelector(".composer-pending-preview")?.textContent));
    await capture(`${config.name}-collapsed`);
    state.checks.push({ name: config.name, passed: true, rows });
    win.destroy();
    save();
  }
  assert.deepEqual(state.errors, []);
  clearTimeout(timer);
  state.passed = true;
  save();
  app.exit(0);
}).catch(async error => {
  state.passed = false; state.failure = String(error.stack || error); save();
  if (win && !win.isDestroyed()) await capture("failure").catch(() => {});
  console.error(error); app.exit(1);
});
