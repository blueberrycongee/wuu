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
function visibleContents(parent = win.contentView) {
  return parent.children.flatMap(view => !view.getVisible() ? [] : [...(view.webContents ? [view.webContents] : []), ...visibleContents(view)]);
}
async function click(selector) {
  const deadline = Date.now() + 5000;
  do {
    const surfaces = [win.webContents, ...visibleContents()];
    for (const wc of surfaces.reverse()) {
      const point = await wc.executeJavaScript(`(selector => {
        const element = [...document.querySelectorAll(selector)].find(e => e.getBoundingClientRect().width && !e.closest("[inert]"));
        if (!element || element.disabled) return null;
        element.scrollIntoView({block:"nearest"});
        const r = element.getBoundingClientRect(), x=r.x+r.width/2, y=r.y+r.height/2;
        return element.contains(document.elementFromPoint(x,y)) ? {x,y} : null;
      })(${JSON.stringify(selector)})`);
      if (!point) continue;
      await wc.executeJavaScript("window.__wuuClickObserved=false;document.addEventListener('click',()=>{window.__wuuClickObserved=true},{once:true,capture:true})");
      wc.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
      wc.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
      await until(() => wc.executeJavaScript("window.__wuuClickObserved === true"), `click delivered: ${selector}`);
      await wc.executeJavaScript("new Promise(resolve => setTimeout(resolve, 0))");
      return wc;
    }
    await delay(40);
  } while (Date.now() < deadline);
  throw new Error(`No visible clickable target: ${selector}`);
}
async function capture(name) {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  for (const contents of visibleContents()) await contents.capturePage();
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
  if (process.env.WUU_BROWSER_STRESS === "1") {
    const transitions = [], unexpectedNavigations = [];
    const visibility = view.setVisible.bind(view);
    view.setVisible = value => { if (view.getVisible() !== value) transitions.push({ at: Date.now(), visible: value }); return visibility(value); };
    const navigation = details => { if (details.isMainFrame) unexpectedNavigations.push(details.url); };
    guest.on("did-start-navigation", navigation);
    report.repeatedControls = [];
    let recording;
    if (process.platform === "darwin") {
      const video = path.join(output, "repeated-controls.mov");
      fs.rmSync(video, { force: true });
      const recordingStarted = Date.now();
      const recorder = require("node:child_process").spawn("screencapture", ["-x", "-v", "-V", "35", "-l", win.getMediaSourceId().split(":")[1], video], { stdio: ["ignore", "ignore", "pipe"] });
      let recordingLog = ""; recorder.stderr.on("data", data => { recordingLog += data; });
      recording = new Promise(resolve => recorder.once("close", code => { report.recording = { path: video, exitCode: code, log: recordingLog, fresh: fs.existsSync(video) && fs.statSync(video).mtimeMs >= recordingStarted }; resolve(); }));
    }
    if (recording) await delay(1200); // Let the native recorder start before the interaction sequence.
    const expanded = selector => evaluate(selector => document.querySelector(selector)?.getAttribute("aria-expanded"), selector);
    for (let cycle = 0; cycle < 12; cycle++) {
      for (const selector of ["[data-browser-library-trigger]", "[data-web-workspace-downloads-trigger]"]) {
        const before = transitions.length;
        let error;
        try {
          await click(selector);
          await until(async () => await expanded(selector) === "true", "panel opens", 2000);
          await until(() => visibleContents().some(wc => wc.getURL() === "about:blank"), "native panel visible");
          await delay(140); // Deliberate repeat-click cadence for the recording.
          await click(selector);
          await until(async () => await expanded(selector) === "false", "same button closes panel", 2000);
          await delay(140);
        } catch (cause) { error = String(cause); }
        const popup = visibleContents().find(wc => wc.getURL() === "about:blank");
        if (popup) key(popup, "ESC");
        report.repeatedControls.push({ cycle, selector, pageVisibilityChanges: transitions.length - before, error });
      }
      const before = transitions.length;
      await click("[data-browser-bookmark-current]");
      await click("[data-browser-bookmark-current]");
      report.repeatedControls.push({ cycle, selector: "bookmark toggle twice", pageVisibilityChanges: transitions.length - before });
      guest.focus(); key(guest, "L", [process.platform === "darwin" ? "meta" : "control"]);
      await until(() => evaluate(() => document.activeElement?.matches(".workspace-browser-url-input")), "repeated address focus");
      await win.webContents.insertText("Browser");
      key(win.webContents, "ESC"); key(win.webContents, "ESC");
      assert.equal(await guest.executeJavaScript("document.querySelector('#draft').value"), "Keep this draft");
    }
    guest.removeListener("did-start-navigation", navigation);
    view.setVisible = visibility;
    report.repeatedSummary = { navigationCount: unexpectedNavigations.length, pageVisibilityChanges: transitions.length,
      errors: report.repeatedControls.filter(step => step.error), cycles: 12 };
    if (recording) {
      await recording;
      assert.equal(report.recording.exitCode, 0, "Native recorder must finish successfully");
      assert.equal(report.recording.fresh, true, "Native recording must be produced by this run");
      assert.ok(fs.statSync(report.recording.path).size > 0, "Native recording must not be empty");
    }
    save();
    assert.equal(unexpectedNavigations.length, 0, "Chrome buttons must not reload the webpage");
    assert.equal(transitions.length, 0, "Chrome buttons must not hide/show the webpage");
    assert.equal(report.repeatedSummary.errors.length, 0, "Repeated toolbar buttons must toggle predictably");
  }
  await click("[data-browser-bookmark-current]");
  await until(() => evaluate(() => document.querySelector("[data-browser-bookmark-current]")?.getAttribute("aria-pressed") === "true"), "bookmark saved");
  await click("[data-browser-library-trigger]");
  await until(async () => (await Promise.all(webContents.getAllWebContents().map(wc => wc.executeJavaScript("Boolean(document.querySelector('[data-library-section=\"bookmarks\"]'))").catch(() => false)))).some(Boolean), "library menu");
  await click('[data-library-section="bookmarks"]');
  const libraryContents = await click("[data-library-edit-bookmark]");
  await until(() => libraryContents.executeJavaScript("Boolean(document.querySelector('[data-library-bookmark-title]'))"), "bookmark editor");
  await capture("04-bookmark-editor-dark-large");
  await click("[data-library-bookmark-title]");
  libraryContents.selectAll(); // Native Edit > Select All; synthetic macOS keys do not invoke menu accelerators.
  await until(() => libraryContents.executeJavaScript("(() => { const e=document.querySelector('[data-library-bookmark-title]'); return e && e.selectionStart===0 && e.selectionEnd===e.value.length; })()"), "bookmark title select all");
  await libraryContents.insertText("My reading desk");
  await click("[data-library-save-bookmark]");
  await until(() => evaluate(async () => (await window.wuu.webWorkspace.library.getState()).bookmarks.some(bookmark => bookmark.title === "My reading desk")), "bookmark renamed through product UI");
  key(libraryContents, "ESC");
  report.checks.push("Bookmark editor opens in native chrome and saves through production IPC");
  if (process.env.WUU_BROWSER_PUBLIC === "1") {
    report.publicSites = [];
    for (const [name, address] of [["GitHub", "https://github.com"], ["Wikipedia", "https://www.wikipedia.org"], ["YouTube", "https://www.youtube.com"], ["Bilibili", "https://www.bilibili.com"]]) {
      await evaluate(url => window.wuu.webWorkspace.command("ribbon-browser", "navigate", url), address);
      let error;
      try { await until(() => !guest.isLoadingMainFrame() && guest.getURL().startsWith(address), `${name} public page`, 20000); }
      catch (cause) { error = String(cause); }
      const document = await guest.executeJavaScript("({title:document.title, text:document.body?.innerText.slice(0,400), links:document.links.length})").catch(() => null);
      report.publicSites.push({ name, address, finalURL: guest.getURL(), document, error,
        scope: "Public-page rendering only; login, DRM and protected playback unverified" });
      await capture(`public-${name}`); save();
    }
  }
  report.status = "passed"; save();
  if (process.env.WUU_BROWSER_DAILY_KEEP_OPEN === "1") { console.log(`Ready for native inspection: ${output}`); return; }
  app.quit();
}
const watchdog = setTimeout(() => { report.status = "timed-out"; save(); app.exit(1); }, 240000);
run().catch(async error => {
  if (win && !win.isDestroyed()) {
    await capture("failure").catch(() => {});
  }
  report.status = "failed"; report.errors.push(String(error.stack)); save(); console.error(error); app.exit(1); })
  .finally(() => { clearTimeout(watchdog); if (process.env.WUU_BROWSER_DAILY_KEEP_OPEN !== "1") server.close(); });
