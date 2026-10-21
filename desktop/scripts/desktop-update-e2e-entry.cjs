const assert = require("node:assert/strict");
const { createHash, randomBytes } = require("node:crypto");
const { createServer } = require("node:http");
const { EventEmitter, once } = require("node:events");
const { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } = require("node:fs");
const { tmpdir, homedir } = require("node:os");
const { join, basename } = require("node:path");
const { execFileSync } = require("node:child_process");
const { app, BrowserWindow, dialog, ipcMain, Menu } = require("electron");
const { autoUpdater } = require("electron-updater");
const { installDesktopUpdates } = require("./host.cjs");
const output = process.env.WUU_UPDATE_E2E_OUTPUT;
const profile = mkdtempSync(join(tmpdir(), "wuu-update-e2e-"));
const cache = mkdtempSync(join(homedir(), "Library/Caches/wuu-update-e2e-"));
app.setPath("userData", profile);
app.on("window-all-closed", () => {});
app.commandLine.appendSwitch("disable-gpu");
// Exercise the release-only entry without modifying the installed application.
Object.defineProperty(app, "isPackaged", { value: true });
Object.defineProperty(process, "resourcesPath", { value: join(profile, "resources") });
mkdirSync(process.resourcesPath);

const requests = [];
const states = [];
const prompts = [];
const downloads = [];
const checkpoints = [];
const events = new EventEmitter();
let current;
let win;
let initial;
let periodic;
let offeredVersion = "2026.10.16";
let feedFailure = false;
let releaseDownload;
let promptAction = async () => 1;
let shutdowns = 0;
const sha512 = bytes => createHash("sha512").update(bytes).digest("base64");
writeFileSync(join(profile, "payload"), randomBytes(1024 * 1024));
const archives = new Map();
function archiveFor(version) {
  if (!archives.has(version)) {
    writeFileSync(join(profile, "version.txt"), version);
    execFileSync("/usr/bin/zip", ["-q", `${version}.zip`, "payload", "version.txt"], { cwd: profile });
    archives.set(version, readFileSync(join(profile, `${version}.zip`)));
  }
  return archives.get(version);
}
const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  requests.push({ path: url.pathname, version: offeredVersion, failed: feedFailure });
  if (url.pathname.endsWith(".yml")) {
    if (feedFailure) { res.writeHead(503); res.end("Synthetic feed failure"); return; }
    const archive = archiveFor(offeredVersion);
    res.end(JSON.stringify({ version: offeredVersion, files: [{ url: `wuu-${offeredVersion}-arm64.zip`, sha512: sha512(archive), size: archive.length }] }));
  } else if (url.pathname.endsWith(".zip")) {
    const archive = archiveFor(url.pathname.match(/^\/wuu-(.+)-arm64\.zip$/)[1]);
    const gate = new Promise(resolve => { releaseDownload = resolve; });
    events.emit("zip-request");
    await gate;
    res.writeHead(200, { "Content-Length": archive.length, "Content-Type": "application/zip" });
    res.end(archive);
  } else { res.writeHead(404); res.end(); }
});

autoUpdater.disableDifferentialDownload = true;
autoUpdater.on("update-downloaded", event => { downloads.push(event); });
autoUpdater.on("error", error => events.emit("updater-error", error.message));
dialog.showMessageBox = async options => {
  prompts.push({ message: options.message, buttons: options.buttons });
  events.emit("prompt");
  return { response: await promptAction(options), checkboxChecked: false };
};
// Keep the production timers' callbacks and delays, advancing them deterministically.
function start() {
  const timeout = global.setTimeout;
  const interval = global.setInterval;
  global.setTimeout = (callback, delay, ...args) => {
    if (delay === 30_000) initial = callback;
    return timeout(() => {}, delay, ...args);
  };
  global.setInterval = (callback, delay, ...args) => {
    assert.equal(delay, 6 * 60 * 60_000);
    periodic = callback;
    return interval(() => {}, delay, ...args);
  };
  try {
    current = installDesktopUpdates(async () => { shutdowns++; }, state => {
      states.push(state);
      win?.webContents.send("wuu:desktop-update-changed", state);
      events.emit("state", state);
    });
    assert.ok(current, "Release updater must be active");
    assert.ok(initial && periodic, "Both production polling timers must be installed");
  } finally {
    global.setTimeout = timeout;
    global.setInterval = interval;
  }
}

function stateIs(kind, version) {
  return current.state.kind === kind && (version === undefined || current.state.version === version);
}
async function waitState(kind, version) {
  const signal = AbortSignal.timeout(10_000);
  while (!stateIs(kind, version)) await once(events, "state", { signal });
}
async function poll(callback, kind, version) {
  const done = once(events, "state", { signal: AbortSignal.timeout(10_000) });
  callback();
  await done;
  await waitState(kind, version);
  await rendererState(kind, version);
}
async function rendererState(kind, version) {
  await win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
    const check = () => {
      const update = document.querySelector('.desktop-update');
      const button = update?.querySelector('button');
      const valid = ${JSON.stringify(kind)} === 'idle' ? !update : update?.dataset.state === ${JSON.stringify(kind)}
        && (!${JSON.stringify(version)} || button?.getAttribute('aria-label').includes(${JSON.stringify(version)}));
      if (valid) { observer.disconnect(); clearTimeout(timer); resolve(true); }
    };
    const observer = new MutationObserver(check);
    const timer = setTimeout(() => { observer.disconnect(); reject(new Error('Renderer state timeout')); }, 5000);
    observer.observe(document.body, {subtree:true,childList:true,attributes:true});
    check();
  })`);
}
async function click() {
  const rect = await win.webContents.executeJavaScript(`(() => {
    const r = document.querySelector('.desktop-update-button').getBoundingClientRect();
    return {x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2)};
  })()`);
  win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...rect });
  win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...rect });
}
async function capture(name) {
  await win.webContents.executeJavaScript(`Promise.all(document.querySelector('.desktop-update').getAnimations({subtree:true})
    .filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime)).map(animation => animation.finished))`);
  await win.webContents.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  writeFileSync(join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  checkpoints.push({ name, state: current.state, rendered: await win.webContents.executeJavaScript(`(() => {
    const button = document.querySelector('.desktop-update-button');
    const rect = button.getBoundingClientRect();
    return {state: button.closest('.desktop-update').dataset.state, label: button.getAttribute('aria-label'),
      color: getComputedStyle(button).color, bounds: {x:rect.x,y:rect.y,width:rect.width,height:rect.height}};
  })()`) });
}
function stop() {
  current?.stop();
  autoUpdater.closeServerIfExists();
}

app.whenReady().then(async () => {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  writeFileSync(join(process.resourcesPath, "app-update.yml"), JSON.stringify({
    provider: "generic", url: `http://127.0.0.1:${server.address().port}/`, updaterCacheDirName: basename(cache),
  }));
  for (const [channel, value] of Object.entries({
    "wuu:theme-preference-get-sync": "light", "wuu:language-preference-get-sync": "en-US",
    "wuu:onboarding-complete-get-sync": true, "wuu:message-flow-font-size-get-sync": 15,
  })) ipcMain.on(channel, event => { event.returnValue = value; });
  ipcMain.handle("wuu:desktop-update-state", () => current.state);
  ipcMain.handle("wuu:desktop-update-download", () => current.download());
  ipcMain.handle("wuu:desktop-update-restart", () => current.restart());
  start();
  win = new BrowserWindow({ width: 520, height: 260, show: false, webPreferences: {
    preload: join(output, "preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  await win.loadFile(join(output, "index.html"));
  win.show();
  await rendererState("idle");
  await poll(initial, "available", offeredVersion);
  offeredVersion = "2026.10.17";
  await poll(periodic, "available", offeredVersion);
  await capture("poll-refreshed");
  feedFailure = true;
  await poll(periodic, "available", offeredVersion);
  await click();
  await waitState("error", offeredVersion);
  await rendererState("error");
  assert.equal(downloads.length, 0);
  assert.equal(requests.filter(req => req.path.endsWith(".zip")).length, 0);
  assert.equal(prompts.length, 0, "Background checks and sidebar actions must remain quiet");
  await capture("refresh-failed");
  feedFailure = false;
  offeredVersion = app.getVersion();
  await poll(periodic, "idle");
  offeredVersion = "2026.10.18";
  await poll(periodic, "available", offeredVersion);
  offeredVersion = "2026.10.19";
  const sidebarZip = once(events, "zip-request", { signal: AbortSignal.timeout(10_000) });
  await click();
  await sidebarZip;
  await rendererState("downloading");
  const countBeforeBusy = requests.length;
  periodic();
  await current.download();
  assert.equal(requests.length, countBeforeBusy, "Polling/download must serialize with the active download");
  assert.equal(await win.webContents.executeJavaScript("document.querySelector('.desktop-update-button').getAttribute('aria-disabled')"), "true");
  await capture("downloading-latest");
  releaseDownload();
  await waitState("ready", offeredVersion);
  await rendererState("ready", offeredVersion);
  assert.equal(downloads.at(-1).version, offeredVersion);
  assert.equal(sha512(readFileSync(downloads.at(-1).downloadedFile)), sha512(archiveFor(offeredVersion)));
  await capture("sidebar-latest-ready");
  const restartPrompt = once(events, "prompt", { signal: AbortSignal.timeout(10_000) });
  await click();
  await restartPrompt;
  await waitState("ready", offeredVersion);
  await rendererState("ready", offeredVersion);
  assert.equal(shutdowns, 0, "Later must not request installation");
  stop();

  // A release can change while the native menu consent dialog is open.
  start();
  offeredVersion = "2026.10.20";
  promptAction = async options => {
    if (options.buttons?.length === 2 && current.state.kind === "prompting") {
      offeredVersion = "2026.10.21";
      return 0;
    }
    return 1;
  };
  const menuZip = once(events, "zip-request", { signal: AbortSignal.timeout(10_000) });
  Menu.getApplicationMenu().getMenuItemById("check-for-updates").click();
  await menuZip;
  assert.ok(stateIs("downloading", "2026.10.21"));
  releaseDownload();
  await waitState("ready", "2026.10.21");
  await rendererState("ready", "2026.10.21");
  await capture("menu-latest-ready");
  assert.equal(downloads.at(-1).version, "2026.10.21");
  assert.equal(sha512(readFileSync(downloads.at(-1).downloadedFile)), sha512(archiveFor(offeredVersion)));
  assert.deepEqual(requests.filter(req => req.path.endsWith(".zip")).map(req => req.path), [
    "/wuu-2026.10.19-arm64.zip", "/wuu-2026.10.21-arm64.zip",
  ]);
  assert.equal(shutdowns, 0);
  assert.ok(states.some(state => state.kind === "downloading" && state.percent > 0), "Real download progress must reach the controller");
  writeFileSync(join(output, "result.json"), JSON.stringify({
    passed: true, electron: process.versions.electron, platform: process.platform,
    scope: "Production updater wiring, polling callbacks, native menu, preload, update button, MacUpdater HTTP download and SHA-512; scripted consent; no installation",
    requests, states, prompts, checkpoints, downloads: downloads.map(({ version, downloadedFile }) => ({ version, file: basename(downloadedFile) })),
  }, null, 2));
  console.log(`Desktop update E2E passed: ${join(output, "result.json")}`);
}).then(() => finish(0), error => {
  console.error(error);
  writeFileSync(join(output, "failure.json"), JSON.stringify({ error: error.stack, requests, states, prompts, checkpoints }, null, 2));
  finish(1);
});

async function finish(code) {
  stop();
  releaseDownload?.();
  win?.destroy();
  await new Promise(resolve => server.close(resolve));
  rmSync(profile, { recursive: true, force: true });
  rmSync(cache, { recursive: true, force: true });
  app.exit(code);
}
