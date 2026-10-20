// Shipping shell, preload and Go core; disposable browser profile and local pages.
// Run with the current Wuu Dev host on macOS. No provider inference is used.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const http = require("node:http");
const { execFileSync } = require("node:child_process");
const { pathToFileURL } = require("node:url");
const { app, ipcMain, webContents } = require("electron");
const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_BROWSER_DAILY_OUTPUT || path.join(desktop, "out/e2e/browser-daily-use"));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-browser-daily-"));
const home = path.join(fixture, "home"), project = path.join(fixture, "project");
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath("userData", path.join(fixture, "profile"));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: path.join(desktop, "build/bin/wuu-core"),
  WUU_SAFE_MODE: "0", WUU_ENABLE_BROWSER: "1", WUU_ENABLE_CUA_MAC: "0", WUU_ENABLE_CUA_LINUX: "0",
  WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: "1" });
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(home, "config.json"), JSON.stringify({ default_provider: "fixture",
  providers: { fixture: { type: "openai-compatible", base_url: "http://127.0.0.1:1/v1", api_key: "synthetic", model: "fixture" } },
  engines: Object.fromEntries(["codex", "claude", "cursor", "devin", "dsh", "grok", "hermes", "pi", "opencode", "antigravity"].map(id => [id, { enabled: false }])) }));
fs.writeFileSync(path.join(home, "projects.json"), JSON.stringify({ projects: [{ id: "daily-browser", name: "Browser acceptance", path: project,
  created_at: new Date().toISOString(), updated_at: new Date().toISOString() }], active_context: { kind: "project", project_id: "daily-browser", cwd: project } }));
fs.writeFileSync(path.join(home, "desktop-settings.json"), JSON.stringify({ onboarding_version: 100, language: "en-US", theme: "light", message_flow_font_size: 15 }));
const report = { status: "running", platform: process.platform, versions: process.versions,
  commit: execFileSync("git", ["rev-parse", "HEAD"], { cwd: desktop, encoding: "utf8" }).trim(),
  checks: [], screenshots: [], limitations: ["Isolated synthetic profile; no account login", "Input uses real Electron events, not physical keyboard",
    "macOS native composed screenshots; other platforms capture shell only"], errors: [] };
const save = () => fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
let win;
app.on("browser-window-created", (_event, created) => { win ||= created; });
const handle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, listener) => handle(channel, (...args) => {
  if (["wuu:turn-start", "wuu:turn-queue", "wuu:turn-steer"].includes(channel)) throw new Error("Inference is disabled in this fixture");
  return listener(...args);
});
const server = http.createServer((request, response) => {
  if (request.url === "/download") {
    response.writeHead(200, { "Content-Type": "text/plain", "Content-Disposition": 'attachment; filename="browser-check.txt"' });
    response.end("Wuu daily browser download"); return;
  }
  response.setHeader("Content-Type", "text/html; charset=utf-8");
  response.end(`<!doctype html><title>${request.url.includes("second") ? "Research notes — a long title kept readable while switching between work and the web" : "Browser reading desk"}</title>
    <style>body{margin:0;font:18px/1.7 system-ui;color:#26323d;background:#f7f5ef}main{max-width:700px;margin:60px auto;padding:0 32px}h1{font-size:36px;line-height:1.2}input{font:inherit;padding:10px;width:80%}a{color:#285fb8}p{margin:28px 0}</style>
    <main><h1>A browser for everyday work</h1><p>This is a local reading and interaction fixture running in the actual native browser view.</p>
    <input id="draft" placeholder="Keep a draft while switching tabs"><p><a id="next" href="/second">Open research notes</a> · <a id="new" href="/second" target="_blank">Open a new tab</a></p>
    ${Array.from({ length: 14 }, (_, i) => `<p>Reading section ${i + 1}. Scroll, switch tabs, resize the window and return to your place. The agent and the user share this same webpage.</p>`).join("")}</main>`);
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { if (await check()) return; await delay(40); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
function key(contents, keyCode, modifiers = []) {
  contents.sendInputEvent({ type: "keyDown", keyCode, modifiers });
  contents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
}
async function click(selector) {
  const surfaces = [win.webContents, ...win.contentView.children.filter(v => v.webContents && v.getVisible()).map(v => v.webContents)];
  for (const wc of surfaces.reverse()) {
    const point = await wc.executeJavaScript(`(selector => {
      const element = [...document.querySelectorAll(selector)].find(e => e.getBoundingClientRect().width && !e.closest("[inert]"));
      if (!element) return null;
      element.scrollIntoView({block:"nearest"});
      const r = element.getBoundingClientRect(), x=r.x+r.width/2, y=r.y+r.height/2;
      return element.contains(document.elementFromPoint(x,y)) ? {x,y} : null;
    })(${JSON.stringify(selector)})`);
    if (!point) continue;
    wc.focus();
    wc.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
    wc.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
    return;
  }
  throw new Error(`No visible clickable target: ${selector}`);
}
async function capture(name) {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  for (const child of win.contentView.children) if (child.webContents && child.getVisible()) await child.webContents.capturePage();
  const file = path.join(output, `${name}.png`);
  if (process.platform === "darwin") execFileSync("screencapture", ["-x", "-o", "-l", win.getMediaSourceId().split(":")[1], file]);
  else fs.writeFileSync(file, (await win.webContents.capturePage()).toPNG());
  report.screenshots.push(file); save();
}
async function run() {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  await import(pathToFileURL(path.join(desktop, "out/main/index.js")).href);
  await until(() => win && !win.webContents.isLoadingMainFrame(), "shell load");
  await until(() => evaluate(() => Boolean(window.wuu?.webWorkspace && document.querySelector(".sidebar-ribbon"))), "browser chrome");
  win.setBounds({ x: 120, y: 90, width: 1280, height: 860 }); win.show(); win.focus();
  const first = await evaluate(async url => {
    await window.wuu.webWorkspace.initializeTabs([]);
    return (await window.wuu.webWorkspace.createTab("ribbon-browser", url)).activeTabId;
  }, `${base}/first`);
  await until(() => evaluate(id => Boolean(document.querySelector(`[data-ribbon-id="${id}"]`)), first), "tab entry");
  await click(`[data-ribbon-id="${first}"]`);
  let guest;
  await until(() => { guest = webContents.getAllWebContents().find(w => w.getURL() === `${base}/first` && !w.isLoadingMainFrame()); return guest; }, "native webpage");
  await until(() => win.contentView.children.some(v => v.webContents === guest && v.getVisible()), "webpage visible");
  await guest.executeJavaScript("document.querySelector('#draft').focus()");
  guest.focus(); await guest.insertText("Keep this draft");
  await guest.executeJavaScript("scrollTo(0,600)");
  const originalID = guest.id;
  const second = await evaluate(async url => (await window.wuu.webWorkspace.createTab("ribbon-browser", url)).activeTabId, `${base}/second`);
  await until(() => evaluate(id => Boolean(document.querySelector(`[data-ribbon-id="${id}"]`)), second), "second entry");
  await click(`[data-ribbon-id="${first}"]`);
  await until(() => win.contentView.children.some(v => v.webContents === guest && v.getVisible()), "original page restored");
  assert.equal(guest.id, originalID);
  assert.equal(await guest.executeJavaScript("document.querySelector('#draft').value"), "Keep this draft");
  assert.ok(await guest.executeJavaScript("scrollY") > 500);
  report.checks.push("Native tab switching retains WebContents identity, draft and scroll");
  assert.equal(await evaluate(() => {
    const controls = document.querySelector('[data-wuu-component="page-history-controls"]');
    return Boolean(controls && !controls.hidden && controls.getBoundingClientRect().width);
  }), false, "A website must show one navigation pair, without competing app-history arrows");
  await guest.executeJavaScript("scrollTo(0,0)");
  await capture("01-light-wide-native");
  guest.focus(); key(guest, "L", [process.platform === "darwin" ? "meta" : "control"]);
  await until(() => evaluate(() => document.activeElement?.matches(".workspace-browser-url-input")), "address shortcut focus");
  await win.webContents.insertText("Browser");
  await until(() => evaluate(() => Boolean(document.querySelector('[aria-expanded="true"][role="combobox"]'))), "history completion");
  await capture("02-address-completion");
  key(win.webContents, "ESC"); key(win.webContents, "ESC");
  await evaluate(async () => { await window.wuu.setThemePreference("dark"); await window.wuu.setMessageFlowFontSize(20); });
  win.setBounds({ width: 960, height: 740 });
  await until(() => evaluate(() => document.documentElement.dataset.theme === "dark"), "dark theme");
  await capture("03-dark-large-narrow-native");
  const geometry = await evaluate(() => {
    const r = document.querySelector(".web-workspace-stage:not([hidden]) .web-workspace-host").getBoundingClientRect();
    return { x: r.x, y: r.y, width: r.width, height: r.height };
  });
  const view = win.contentView.children.find(v => v.webContents === guest);
  await until(() => ["x", "y", "width", "height"].every(k => Math.abs(view.getBounds()[k] - Math.round(geometry[k] * win.webContents.getZoomFactor())) <= 2), "native view aligns");
  report.checks.push("Cmd/Ctrl+L focuses the address and local history suggestions open", "Dark theme at 20px and 960px keeps native page aligned");
  report.status = "passed"; save();
  if (process.env.WUU_BROWSER_DAILY_KEEP_OPEN === "1") { console.log(`Ready for native inspection: ${output}`); return; }
  app.quit();
}
const watchdog = setTimeout(() => { report.status = "timed-out"; save(); app.exit(1); }, 120000);
run().catch(error => { report.status = "failed"; report.errors.push(String(error.stack)); save(); console.error(error); app.exit(1); })
  .finally(() => { clearTimeout(watchdog); if (process.env.WUU_BROWSER_DAILY_KEEP_OPEN !== "1") server.close(); });
