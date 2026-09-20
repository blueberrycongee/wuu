// Real Chromium process lifecycle probe. No product profile, UI automation, or
// model service is used; native dialog choices are supplied by the test.
const assert = require("node:assert/strict");
const { once } = require("node:events");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

if (!process.versions.electron) {
  const { spawnSync } = require("node:child_process");
  const { transformSync } = require("esbuild");
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-renderer-recovery-"));
  try {
    const source = fs.readFileSync(path.join(__dirname, "../src/main/rendererProcessGone.ts"), "utf8");
    fs.writeFileSync(path.join(directory, "recovery.cjs"), transformSync(source, {
      loader: "ts", format: "cjs", target: "es2022",
    }).code);
    fs.writeFileSync(path.join(directory, "index.html"), "<!doctype html><title>Recovery probe</title>");
    fs.writeFileSync(path.join(directory, "preload.cjs"), `
      const { ipcRenderer } = require("electron");
      ipcRenderer.on("probe", (_event, payload) => ipcRenderer.send("probe-ack", payload));
    `);
    const env = { ...process.env };
    delete env.ELECTRON_RUN_AS_NODE;
    const result = spawnSync(require("electron"), [__filename, directory], {
      env, encoding: "utf8", timeout: 90_000,
    });
    process.stdout.write(result.stdout || "");
    process.stderr.write(result.stderr || "");
    if (result.error) throw result.error;
    assert.equal(result.status, 0, "Electron recovery probe failed");
    assert.doesNotMatch(`${result.stdout}${result.stderr}`, /Render frame was disposed/);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
} else {
  const { app, BrowserWindow, ipcMain } = require("electron");
  const directory = process.argv[2];
  const { installRendererRecovery, sendToWindow } = require(path.join(directory, "recovery.cjs"));
  app.setPath("userData", path.join(directory, "profile"));
  app.setPath("crashDumps", directory);
  app.on("window-all-closed", () => {});
  const windows = [];
  const cleanupOwners = [];
  let broadcasts;

  function event(emitter, name) {
    return once(emitter, name, { signal: AbortSignal.timeout(15_000) });
  }

  function crash(window) {
    // forcefullyCrashRenderer reports "killed" on macOS. Chromium's diagnostic
    // URL causes an actual crash and also exercises restoring the known entry.
    void window.loadURL("chrome://crash").catch(() => {});
  }

  async function createWindow(partition, recoveryEntry = "index.html") {
    const window = new BrowserWindow({
      show: false,
      webPreferences: {
        partition, preload: path.join(directory, "preload.cjs"),
        contextIsolation: true, nodeIntegration: false, sandbox: true,
      },
    });
    windows.push(window);
    const state = { window, attempts: 0, choose: undefined };
    installRendererRecovery(window, {
      app,
      load: () => { state.attempts++; return window.loadFile(path.join(directory, recoveryEntry)); },
      stopTerminals: (id) => cleanupOwners.push(id),
      prompt: () => new Promise((resolve) => {
        state.choose = resolve;
        window.emit("recovery-prompt");
      }),
    });
    await window.loadFile(path.join(directory, "index.html"));
    return state;
  }

  async function acknowledge(window) {
    const received = event(ipcMain, "probe-ack");
    sendToWindow(window, "probe", "alive");
    const [message, payload] = await received;
    assert.equal(message.sender.id, window.webContents.id);
    assert.equal(payload, "alive");
  }

  app.whenReady().then(async () => {
    const first = await createWindow("recovery-first");
    const second = await createWindow("recovery-second");
    const ownerID = first.window.webContents.id;
    await first.window.webContents.executeJavaScript("localStorage.setItem('saved', 'conversation'); window.unsentDraft = 'draft'");
    // Exercise sends while frames disappear and replacements launch. Use a
    // separate channel so acknowledgements remain event-coordinated.
    broadcasts = setInterval(() => {
      for (const window of windows) sendToWindow(window, "background-event", {});
    }, 1);
    for (let attempt = 1; attempt <= 3; attempt++) {
      const gone = event(first.window.webContents, "render-process-gone");
      const loaded = event(first.window.webContents, "did-finish-load");
      crash(first.window);
      const [, details] = await gone;
      assert.equal(details.reason, "crashed");
      await loaded;
      assert.equal(first.window.webContents.id, ownerID);
      assert.equal(first.attempts, attempt);
      await acknowledge(first.window);
      await acknowledge(second.window);
    }
    assert.equal(await first.window.webContents.executeJavaScript("localStorage.getItem('saved')"), "conversation");
    assert.equal(await first.window.webContents.executeJavaScript("typeof window.unsentDraft"), "undefined");
    assert.equal(second.attempts, 0);
    assert.deepEqual(cleanupOwners, [ownerID, ownerID, ownerID]);

    const prompted = event(first.window, "recovery-prompt");
    crash(first.window);
    await prompted;
    assert.equal(first.attempts, 3);
    const reloaded = event(first.window.webContents, "did-finish-load");
    first.choose("reload");
    await reloaded;
    await acknowledge(first.window);

    // Closing in the cooldown must cancel the queued load.
    const gone = event(first.window.webContents, "render-process-gone");
    crash(first.window);
    await gone;
    first.window.close();
    await acknowledge(second.window);
    assert.equal(first.attempts, 4);

    // Chromium can finish loading its error page after the app entry fails.
    // That must not cancel recovery or count as a stable app renderer.
    const failed = await createWindow("recovery-failed-load", "missing.html");
    const failedPrompt = event(failed.window, "recovery-prompt");
    crash(failed.window);
    await failedPrompt;
    assert.equal(failed.attempts, 3);
    const closed = event(failed.window, "closed");
    failed.choose("close");
    await closed;
    await acknowledge(second.window);
    console.log("PASS: real renderer crashes, cooldown recovery, bounded retries, manual retry, failed loads, window isolation, persisted state, and IPC during frame disposal");
  }).then(() => finish(0), (error) => { console.error(error); finish(1); });

  function finish(code) {
    clearInterval(broadcasts);
    for (const window of windows) if (!window.isDestroyed()) window.destroy();
    app.exit(code);
  }
}
