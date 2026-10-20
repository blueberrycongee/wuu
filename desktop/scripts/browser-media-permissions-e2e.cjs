// Real Chromium media requests against the shipping host. Native permission
// answers are scripted; synthetic Chromium devices never open user hardware.
// Failure cases: repeated microphone prompts, microphone approval granting a
// camera, iframe-origin borrowing, navigation races, and Agent grant borrowing.
// Run: node scripts/browser-media-permissions-e2e.cjs
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const http = require("node:http");
if (!process.versions.electron) {
  const { spawnSync } = require("node:child_process");
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-media-permissions-"));
  try {
    const result = spawnSync(process.env.WUU_E2E_ELECTRON || String(require("electron")), [
      ...(process.platform === "linux" ? ["--no-sandbox", ...(!process.env.DISPLAY ? ["--ozone-platform=headless"] : [])] : []),
      __filename,
    ], { stdio: "inherit", env: { ...process.env, WUU_MEDIA_PERMISSIONS_PROFILE: profile } });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } finally {
    fs.rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
  return;
}
const { app, BrowserWindow, dialog, session } = require("electron");
const { buildSync } = require("esbuild");

const root = path.resolve(__dirname, "..");
const output = process.env.WUU_MEDIA_PERMISSIONS_OUTPUT || path.join(root, "out/e2e/browser-media-permissions");
const profile = process.env.WUU_MEDIA_PERMISSIONS_PROFILE;
assert(profile, "Use the Node launcher so the temporary browser profile is cleaned after Electron exits.");
fs.mkdirSync(output, { recursive: true });
fs.rmSync(path.join(output, "failure.json"), { force: true });
app.setPath("userData", profile);
app.commandLine.appendSwitch("use-fake-device-for-media-stream");
const checks = [];
const prompts = [];
const check = (name, actual, expected) => {
  assert.deepEqual(actual, expected, name);
  checks.push(name);
  console.log("PASS:", name);
};
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(fn, label) {
  const end = Date.now() + 10000;
  while (Date.now() < end) { if (await fn()) return; await pause(20); }
  throw new Error("Timeout: " + label);
}
let host, win, server;
const originalPrompt = dialog.showMessageBox;
let reply = 1;
let heldPrompt;
dialog.showMessageBox = async (_parent, options) => {
  prompts.push({ message: options.message, detail: options.detail });
  if (reply === "hold") return new Promise(resolve => { heldPrompt = resolve; });
  return { response: reply, checkboxChecked: false };
};
const fixture = `<!doctype html><title>Browser media permission acceptance</title>
  <style>body{font:18px system-ui;background:#f5f6f9;color:#171b27;padding:40px}h1{font-size:28px}li{margin:12px}</style>
  <h1>Browser media permission acceptance</h1><p>Synthetic camera and microphone. No user hardware is accessed.</p><ol id="results"></ol>
  <script>window.acquire=async constraints=>{try{const stream=await navigator.mediaDevices.getUserMedia(constraints);
  const kinds=stream.getTracks().map(track=>track.kind);stream.getTracks().forEach(track=>track.stop());return kinds.join(",");}
  catch(error){return error.name;}}</script>`;

(async () => {
  await app.whenReady();
  const entry = path.join(output, "host.cjs");
  buildSync({
    stdin: { contents: `export { WebWorkspaceHost } from "./src/main/webWorkspaceHost"; export { installBrowserSessionHandlers, BROWSER_PARTITION } from "./src/main/browserHostWindows";`, resolveDir: root },
    outfile: entry, bundle: true, platform: "node", format: "cjs", external: ["electron"],
    define: { __dirname: JSON.stringify(path.join(root, "out/main")) },
  });
  const { WebWorkspaceHost, installBrowserSessionHandlers, BROWSER_PARTITION } = require(entry);
  server = http.createServer((_req, res) => { res.writeHead(200, { "Content-Type": "text/html" }); res.end(fixture); });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const otherOrigin = `http://localhost:${server.address().port}`;
  win = new BrowserWindow({ width: 960, height: 760, webPreferences: { sandbox: true } });
  await win.loadURL("data:text/html,<title>Wuu browser media acceptance</title>");
  const states = [];
  host = new WebWorkspaceHost((_owner, next) => states.push(next), () => {});
  installBrowserSessionHandlers(session.fromPartition(BROWSER_PARTITION), { ownsWebContents: () => false }, host);
  host.initializeTabs(win, []);
  const group = "ribbon-browser";
  host.reportBounds(win, group, { x: 0, y: 0, width: 960, height: 700 });
  const state = host.createTab(win, group, origin + "/media");
  let wc;
  await until(() => {
    wc = win.contentView.children.find(view => view.webContents?.getURL() === origin + "/media")?.webContents;
    return wc && !wc.isLoading();
  }, "fixture loaded");
  const acquire = constraints => wc.executeJavaScript(`acquire(${JSON.stringify(constraints)})`, true);
  check("Microphone approval produces an audio track", await acquire({ audio: true }), "audio");
  check("The first microphone request prompts once", prompts.length, 1);
  check("Repeated microphone use succeeds", await acquire({ audio: true }), "audio");
  check("Repeated microphone use reuses only its microphone grant", prompts.length, 1);
  reply = 0;
  check("Camera access remains a separate decision", await acquire({ video: true }), "NotAllowedError");
  check("Camera request showed a separate prompt", prompts.length, 2);
  check("A denied camera does not revoke the microphone", await acquire({ audio: true }), "audio");
  check("Camera denial did not force another microphone prompt", prompts.length, 2);
  reply = 1;
  check("The user can approve the camera later", await acquire({ video: true }), "video");
  check("Combined audio and video reuse both explicit grants", await acquire({ audio: true, video: true }), "audio,video");
  check("Combined reuse does not prompt", prompts.length, 3);
  check("Unknown media checks cannot borrow a specific device grant", host.checkPermission(wc, "media", origin, "unknown"), false);
  check("Cross-origin media checks cannot borrow the parent grant", host.checkPermission(wc, "media", otherOrigin, "audio"), false);

  // A real cross-origin iframe must pass both Chromium Permissions Policy and
  // the host's origin-scoped permission boundary.
  reply = 0;
  await wc.executeJavaScript(`new Promise(resolve=>{const frame=document.createElement("iframe");frame.allow="microphone; camera";frame.src=${JSON.stringify(otherOrigin + "/frame")};frame.onload=resolve;document.body.append(frame)})`);
  const frame = wc.mainFrame.frames[0];
  check("A cross-origin iframe is denied independently", await frame.executeJavaScript("acquire({audio:true})"), "NotAllowedError");
  check("The iframe received its own permission prompt", prompts.length, 4);

  const source = host.browserSource(() => win);
  const lease = source.attach("workspace:" + state.activeTabId, () => {}, () => {}, () => false);
  check("Agent attachment cannot reuse the user's microphone grant", await acquire({ audio: true }), "NotAllowedError");
  check("Agent requests do not show a user prompt", prompts.length, 4);
  lease.release();
  check("Releasing Agent control restores the user's microphone grant", await acquire({ audio: true }), "audio");

  await wc.loadURL(otherOrigin + "/next");
  reply = "hold";
  // Do not await a page promise that navigation will destroy.
  void acquire({ audio: true }).catch(() => {});
  await until(() => heldPrompt, "pending permission dialog");
  await wc.loadURL(origin + "/returned");
  heldPrompt({ response: 1, checkboxChecked: false });
  await wc.loadURL(otherOrigin + "/verify");
  reply = 0;
  check("A permission accepted after navigation is not retained", await acquire({ audio: true }), "NotAllowedError");
  check("Returning to the interrupted origin asks again", prompts.length, 6);
  await wc.loadURL(origin + "/returned");
  check("Returning to the original origin retains its own tab grant", await acquire({ audio: true }), "audio");
  // A tab mute must affect Chromium output, retain selection and permission
  // state, and survive navigation without muting same-origin sibling tabs.
  const tabID = state.activeTabId;
  let audioState = host.setTabMuted(win, group, tabID, true);
  check("Muting a tab reaches Chromium audio output", wc.isAudioMuted(), true);
  check("Muted status reaches browser tab state", audioState.tabs.find(tab => tab.id === tabID).muted, true);
  await wc.loadURL(origin + "/muted-navigation");
  check("Tab mute survives navigation", wc.isAudioMuted(), true);
  const sibling = host.createTab(win, group, origin + "/sibling");
  let siblingContents;
  await until(() => {
    siblingContents = win.contentView.children.find(view => view.webContents?.getURL() === origin + "/sibling")?.webContents;
    return siblingContents && !siblingContents.isLoading();
  }, "audio sibling");
  check("A same-origin sibling does not inherit another tab's mute", siblingContents.isAudioMuted(), false);
  audioState = host.setTabMuted(win, group, tabID, false);
  check("Unmuting a background tab does not change the selected tab", audioState.activeTabId, sibling.activeTabId);
  check("Unmute reaches Chromium", wc.isAudioMuted(), false);
  host.selectTab(win, group, tabID);
  // A brief, quiet tone exercises Chromium's real audibility signal. Chromium's
  // --mute-audio disables that signal, so it cannot validate the product state.
  await wc.executeJavaScript(`window.audioContext=new AudioContext();window.tone=audioContext.createOscillator();
    const gain=audioContext.createGain();gain.gain.value=0.01;tone.frequency.value=220;
    tone.connect(gain);gain.connect(audioContext.destination);tone.start();tone.stop(audioContext.currentTime+0.5);audioContext.resume()`, true);
  await until(() => states.at(-1)?.tabs.find(tab => tab.id === tabID)?.audible === true, "real Chromium audio status");
  check("Chromium audio playback is reported to the ribbon", wc.isCurrentlyAudible(), true);
  await wc.executeJavaScript("audioContext.close()", true);
  await until(() => !states.at(-1)?.tabs.find(tab => tab.id === tabID)?.audible, "audio status cleared");
  check("Stopping playback clears the audio indicator", wc.isCurrentlyAudible(), false);
  await wc.executeJavaScript(`document.querySelector("#results").innerHTML=${JSON.stringify(checks.map(name => `<li>PASS: ${name}</li>`).join(""))}`);
  fs.writeFileSync(path.join(output, "media-permissions.png"), (await wc.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify({
    status: "passed", platform: process.platform, versions: process.versions, checks, prompts,
    limitations: ["Native permission dialog answers are scripted.", "Chromium fake media devices are used; OS camera/microphone permission dialogs and actual devices are not exercised."],
  }, null, 2));
  console.log("Evidence:", output);
})().catch(error => {
  console.error(error);
  fs.writeFileSync(path.join(output, "failure.json"), JSON.stringify({ error: error.stack, checks, prompts }, null, 2));
  process.exitCode = 1;
}).finally(() => {
  dialog.showMessageBox = originalPrompt;
  host?.destroyAll(); win?.destroy(); server?.close();
  app.exit(process.exitCode || 0);
});
setTimeout(() => { console.error("Media permission E2E timed out"); app.exit(1); }, 60000).unref();
