// Real Chromium requests exercise the production host and native picker policy.
// Sources are synthetic; the actual restricted picker is rendered and clicked.
// Streams are denied at the final native
// callback so this fixture never captures user screens or requires macOS TCC.
// Failure cases: cancellation, default-first-source leaks, iframe/Agent requests,
// navigation or ownership changes while choosing, and stale source enumeration.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
if (!process.versions.electron) {
  const { spawnSync } = require("node:child_process");
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-display-capture-"));
  try {
    const result = spawnSync(process.env.WUU_E2E_ELECTRON || require("electron"), [
      ...(process.platform === "linux" ? ["--no-sandbox"] : []), __filename,
    ], { stdio: "inherit", env: { ...process.env, WUU_DISPLAY_CAPTURE_PROFILE: profile } });
    process.exitCode = result.status ?? 1;
  } finally { fs.rmSync(profile, { recursive: true, force: true }); }
  return;
}
const { app, BrowserWindow, desktopCapturer, dialog, nativeImage, session } = require("electron");
const output = process.env.WUU_DISPLAY_CAPTURE_OUTPUT || path.resolve(__dirname, "../out/e2e/browser-display-capture");
app.setPath("userData", process.env.WUU_DISPLAY_CAPTURE_PROFILE);
process.env.WUU_HOME = path.join(process.env.WUU_DISPLAY_CAPTURE_PROFILE, "wuu");
app.on("window-all-closed", () => {});
fs.mkdirSync(output, { recursive: true });
const checks = [];
const originals = { getSources: desktopCapturer.getSources, dialog: dialog.showMessageBox };
const sources = [
  { id: "screen:synthetic-one:0", name: "Desktop 1", thumbnail: nativeImage.createEmpty() },
  { id: "window:synthetic-two:0", name: "Project planning — notes and release checklist", thumbnail: nativeImage.createEmpty() },
];
let enumerationCount = 0, latestDecision, host, win, server;
desktopCapturer.getSources = async () => { console.log("Enumerating synthetic sources"); enumerationCount++; return sources; };
dialog.showMessageBox = async () => { console.log("Approving synthetic permission"); return { response: 1, checkboxChecked: false }; };
const check = (name, value, expected) => { assert.deepEqual(value, expected, name); checks.push(name); console.log("PASS:", name); };
const watchdog = setTimeout(() => { console.error("Display capture fixture timed out"); app.exit(1); }, 45000);
async function until(fn) {
  const deadline = Date.now() + 8000;
  while (!await fn()) {
    if (Date.now() > deadline) throw new Error("Timed out waiting for display capture state");
    await new Promise(resolve => setTimeout(resolve, 20));
  }
}
app.whenReady().then(async () => {
  const root = path.resolve(__dirname, "..");
  const entry = path.join(root, "out/main/browser-display-capture-fixture.cjs");
  require("esbuild").buildSync({
    stdin: { contents: 'export { WebWorkspaceHost } from "./src/main/webWorkspaceHost"; export { installBrowserSessionHandlers, BROWSER_PARTITION } from "./src/main/browserHostWindows"; export { setThemePreference, setMessageFlowFontSize } from "./src/main/desktopSettings"; export { setMainLocale } from "./src/main/i18n";', resolveDir: root },
    outfile: entry, bundle: true, platform: "node", format: "cjs", external: ["electron"],
    define: { __dirname: JSON.stringify(path.join(root, "out/main")) },
  });
  const { WebWorkspaceHost, installBrowserSessionHandlers, BROWSER_PARTITION, setThemePreference, setMessageFlowFontSize, setMainLocale } = require(entry);
  setThemePreference("light"); setMessageFlowFontSize(15); setMainLocale("en-US");
  server = require("node:http").createServer((_req, res) => {
    res.setHeader("Content-Type", "text/html");
    res.end(`<!doctype html><title>Screen sharing fixture</title><h1>Screen sharing</h1><button>Share a screen</button>
      <script>window.share=async()=>{try{const s=await navigator.mediaDevices.getDisplayMedia({video:true});s.getTracks().forEach(t=>t.stop());return"stream"}catch(e){return e.name}}</script>`);
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  win = new BrowserWindow({ width: 960, height: 720 });
  await win.loadURL("data:text/html,<title>Wuu screen sharing fixture</title>");
  host = new WebWorkspaceHost(() => {}, () => {});
  const browserSession = session.fromPartition(BROWSER_PARTITION);
  const nativeSetHandler = browserSession.setDisplayMediaRequestHandler.bind(browserSession);
  browserSession.setDisplayMediaRequestHandler = (handler, options) => {
    check("The system picker cannot bypass the ownership gate", options.useSystemPicker, false);
    nativeSetHandler((request, callback) => { console.log("Display media requested"); handler(request, streams => {
      latestDecision = streams;
      callback(null);
    }); }, options);
  };
  installBrowserSessionHandlers(browserSession, { ownsWebContents: () => false }, host);
  host.initializeTabs(win, []);
  host.reportBounds(win, "ribbon-browser", { x: 0, y: 0, width: 960, height: 650 });
  const state = host.createTab(win, "ribbon-browser", origin);
  let wc;
  await until(() => {
    wc = win.contentView.children.find(view => view.webContents?.getURL() === origin + "/")?.webContents;
    return wc && !wc.isLoading();
  });
  const share = () => wc.executeJavaScript("share()", true);
  sources[0].thumbnail = await wc.capturePage();
  const preview = new BrowserWindow({ show: false, width: 640, height: 400 });
  await preview.loadURL("data:text/html," + encodeURIComponent("<style>body{font:16px system-ui;background:#f6f7fa;color:#253142;padding:28px}h1{font-size:24px}p{line-height:2}</style><h1>Project planning</h1><p>Review the release checklist<br>Test the current browser session<br>Share feedback with the team</p>"));
  sources[1].thumbnail = await preview.webContents.capturePage();
  preview.destroy();
  const nextPicker = async () => {
    let picker;
    await until(() => {
      picker = BrowserWindow.getAllWindows().find(window => window !== win && window.getParentWindow() === win);
      return picker && !picker.webContents.isLoading() && picker.isVisible();
    });
    return picker;
  };
  const select = async picker => picker.webContents.executeJavaScript("document.querySelector('[data-source=\"1\"]').click();document.querySelector('#share').click()");
  const capturePicker = async (picker, name) => {
    await picker.webContents.executeJavaScript(`new Promise(resolve=>{
      document.querySelector('[aria-checked="true"]')?.scrollIntoView({block:"nearest"});
      requestAnimationFrame(()=>requestAnimationFrame(resolve));
    })`);
    await picker.webContents.capturePage().then(image => fs.writeFileSync(path.join(output, name), image.toPNG()));
  };
  console.log("Requesting first screen share");
  let sharing = share();
  let picker = await nextPicker();
  check("The share button starts disabled until a source is chosen", await picker.webContents.executeJavaScript("document.querySelector('#share').disabled"), true);
  check("The picker has no Wuu or Node bridge", await picker.webContents.executeJavaScript("[typeof window.wuu,typeof require,typeof process]"), ["undefined", "undefined", "undefined"]);
  await picker.webContents.executeJavaScript("document.querySelector('[data-source=\"0\"]').focus()");
  picker.webContents.sendInputEvent({ type: "keyDown", keyCode: "Right" });
  picker.webContents.sendInputEvent({ type: "keyUp", keyCode: "Right" });
  await until(() => picker.webContents.executeJavaScript('document.querySelector("[data-source=\\"1\\"]").getAttribute("aria-checked")==="true"'));
  await capturePicker(picker, "picker-light.png");
  await picker.webContents.executeJavaScript("document.querySelector('#share').click()");
  await sharing;
  check("Only the explicitly selected second source is returned", latestDecision?.video?.id, sources[1].id);
  check("System audio is not granted with video sharing", latestDecision?.audio, undefined);
  setThemePreference("dark"); setMessageFlowFontSize(20); setMainLocale("zh-CN");
  sharing = share(); picker = await nextPicker();
  picker.setSize(500, 620);
  await picker.webContents.executeJavaScript("document.querySelector('[data-source=\"1\"]').click()");
  await capturePicker(picker, "picker-dark-large.png");
  await picker.webContents.executeJavaScript("document.querySelector('#cancel').click()");
  await sharing;
  check("Cancelling never selects the first source", latestDecision, null);
  setThemePreference("light"); setMessageFlowFontSize(15); setMainLocale("en-US");
  const iframeCount = enumerationCount;
  await wc.executeJavaScript(`new Promise(resolve=>{const f=document.createElement("iframe");f.allow="display-capture";f.src=${JSON.stringify(origin + "/frame")};f.onload=resolve;document.body.append(f)})`);
  await wc.mainFrame.frames[0].executeJavaScript("share()", true);
  check("Embedded frames cannot enumerate capture sources", enumerationCount, iframeCount);
  latestDecision = undefined;
  const pending = share();
  picker = await nextPicker();
  const lease = host.browserSource(() => win).attach("workspace:" + state.activeTabId, () => {}, () => {}, () => false);
  await select(picker);
  await pending;
  check("Agent takeover while choosing invalidates the selection", latestDecision, null);
  const before = enumerationCount;
  await share();
  check("Agent-controlled tabs cannot enumerate sources", enumerationCount, before);
  lease.release();
  latestDecision = undefined;
  void share().catch(() => {});
  picker = await nextPicker();
  await wc.loadURL(origin + "/next");
  await select(picker);
  await until(() => latestDecision !== undefined);
  check("Navigation invalidates an outstanding screen selection", latestDecision, null);
  await wc.capturePage().then(image => fs.writeFileSync(path.join(output, "fixture.png"), image.toPNG()));
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify({
    status: "passed", electron: process.versions.electron, platform: process.platform, checks,
    exclusions: ["Actual desktop capture, macOS TCC and Linux PipeWire portal were not exercised. Source previews are disposable local fixtures."],
  }, null, 2));
  console.log(`PASS ${checks.length} display capture policy checks: ${output}`);
}).catch(error => {
  console.error(error);
  fs.writeFileSync(path.join(output, "failure.json"), JSON.stringify({ checks, error: error.stack }, null, 2));
  process.exitCode = 1;
}).finally(() => {
  clearTimeout(watchdog);
  host?.destroyAll();
  if (win && !win.isDestroyed()) win.destroy();
  server?.close();
  desktopCapturer.getSources = originals.getSources;
  dialog.showMessageBox = originals.dialog;
  app.exit(process.exitCode ?? 0);
});
