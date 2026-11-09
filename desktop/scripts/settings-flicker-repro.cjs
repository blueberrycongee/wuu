// Repro harness: opens the floating Settings panel on the synthetic backend and
// samples per-frame DOM state around each navigation between rail pages, so a
// blank/loading/geometry-shift frame shows up in the timeline instead of relying
// on screenshots lucky enough to land inside the flash window.
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const esbuild = require("esbuild");

const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_SETTINGS_FLICKER_OUTPUT || path.join(desktop, "out/e2e/settings-flicker"));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-settings-flicker-"));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(fixture, "profile"));
process.env.WUU_RESIZE_E2E_CWD = path.dirname(desktop);
app.enableSandbox();

const preload = fs.readFileSync(path.join(__dirname, "resize-e2e-preload.cjs"), "utf8");
esbuild.buildSync({
  stdin: { contents: preload, resolveDir: __dirname },
  outfile: path.join(fixture, "preload.cjs"),
  bundle: true, platform: "node", external: ["electron"],
  // Inline the env var so esbuild can bundle the extension module itself;
  // a runtime require of an absolute path is outside the sandbox's reach.
  define: {
    "process.env.WUU_RENDERER_E2E_EXTENSION": JSON.stringify(
      process.env.WUU_RENDERER_E2E_EXTENSION || path.join(__dirname, "settings-flicker-extension.cjs")),
  },
});

const report = { transitions: [], anomalousFrames: [] };
const save = () => fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`, true);
async function until(check, label) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) { const r = await check(); if (r) return r; await new Promise(r => setTimeout(r, 20)); }
  throw new Error(`Timed out: ${label}`);
}
async function click(selector) {
  const point = await until(() => evaluate(async selector => {
    const element = document.querySelector(selector);
    if (!element || element.disabled || element.closest("[inert]")) return null;
    const rect = element.getBoundingClientRect();
    if (!(rect.width > 0 && rect.height > 0)) return null;
    const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2;
    return element.contains(document.elementFromPoint(x, y)) ? { x, y } : null;
  }, selector), `hit-testable ${selector}`);
  const zoom = win.webContents.getZoomFactor();
  const position = { x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) };
  win.webContents.sendInputEvent({ type: "mouseMove", ...position });
  win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...position });
  win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...position });
}

let win;
app.on("web-contents-created", (_event, contents) => {
  contents.on("console-message", details => console.log("[renderer]", details.message.slice(0, 400)));
  contents.on("preload-error", (_e, file, error) => console.log("[preload-error]", file, String(error)));
  contents.on("did-fail-load", (_e, code, description) => console.log("[load-fail]", code, description));
});
async function main() {
  await app.whenReady();
  win = new BrowserWindow({
    width: 1180, height: 820, show: true,
    webPreferences: { preload: path.join(fixture, "preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await until(() => evaluate(() => Boolean(document.querySelector('[data-settings-entry="settings"]:not(:disabled)'))), "shell ready");
  await evaluate(() => document.fonts.ready.then(() => undefined));

  // Install the per-frame sampler before Settings opens.
  await evaluate(() => {
    window.__frames = [];
    window.__sampling = false;
    const sample = () => {
      requestAnimationFrame(sample);
      if (!window.__sampling) return;
      try {
      const page = document.querySelector(".settings-page");
      const scroll = document.querySelector(".settings-scroll");
      const main = document.querySelector(".settings-main");
      const crumb = document.querySelector(".settings-crumb-page");
      const rect = page?.getBoundingClientRect();
      const mainRect = main?.getBoundingClientRect();
      const hit = mainRect
        ? document.elementFromPoint(mainRect.x + mainRect.width / 2, mainRect.y + mainRect.height / 2)
        : null;
      const loading = [...document.querySelectorAll(
        ".settings-main .settings-spin, .settings-main .animate-spin, .settings-main [aria-busy='true'], .settings-main .settings-usage-skeleton, .settings-overlay-layer .view-switch-loading-inline",
      )].map(el => String(el.textContent || el.getAttribute("aria-label") || el.getAttribute("class") || "").slice(0, 60));
      const loadingText = [...document.querySelectorAll(".settings-main [role='status'], .settings-main .settings-group-empty")]
        .map(el => el.textContent || "")
        .filter(text => /加载中|Loading/i.test(text));
      loading.push(...loadingText);
      window.__frames.push({
        t: performance.now(),
        page: page?.getAttribute("data-wuu-page") ?? "",
        crumb: crumb?.textContent ?? "",
        pageX: rect ? Math.round(rect.x * 10) / 10 : null,
        pageY: rect ? Math.round(rect.y * 10) / 10 : null,
        pageH: rect ? Math.round(rect.height) : null,
        rows: page ? page.querySelectorAll(".settings-row, .settings-section, .settings-group, .settings-empty-state, .system-permission-row, .connection-summary-list > *").length : null,
        scrollH: scroll ? scroll.scrollHeight : null,
        scrollTop: scroll ? Math.round(scroll.scrollTop) : null,
        shell: Boolean(document.querySelector(".settings-shell")),
        suspended: Boolean(document.querySelector(".settings-overlay-layer .view-switch-loading-inline")),
        hitSettings: Boolean(hit && hit.closest(".settings-main")),
        loading,
        navActive: document.querySelector(".settings-nav-item.active")?.getAttribute("data-wuu-page") ?? "",
      });
      } catch (error) {
        window.__frames.push({ t: performance.now(), samplerError: String(error && error.message || error) });
      }
    };
    requestAnimationFrame(sample);
  });

  await click('[data-settings-entry="settings"]');
  await until(() => evaluate(() => Boolean(document.querySelector(".settings-nav-item[data-wuu-page='general']"))), "settings rail visible");
  // Wait for the open animation + first data reads to settle.
  await new Promise(r => setTimeout(r, 800));

  const transitions = [
    ["general", "appearance"], ["appearance", "providers"], ["providers", "mcp"],
    ["mcp", "usage"], ["usage", "archive"], ["archive", "general"],
    // Revisits hit the retained-data paths.
    ["general", "mcp"], ["mcp", "appearance"], ["appearance", "general"],
    ["general", "providers"], ["providers", "usage"], ["usage", "general"],
  ];

  for (const [from, to] of transitions) {
    // Confirm current page; if it drifted (e.g. unavailable page) re-open nav state.
    const current = await evaluate(() => document.querySelector(".settings-page")?.getAttribute("data-wuu-page") ?? "");
    const entry = { from, to, currentBefore: current, frames: [] };
    await evaluate(() => { window.__frames = []; window.__sampling = true; });
    await click(`.settings-nav-item[data-wuu-page='${to}']`);
    // Capture a screenshot ASAP after the click (paint-level evidence).
    const shot = await win.webContents.capturePage();
    fs.writeFileSync(path.join(output, `${from}-to-${to}-early.png`), shot.toPNG());
    await new Promise(r => setTimeout(r, 700));
    await evaluate(() => { window.__sampling = false; });
    entry.frames = await evaluate(() => window.__frames.splice(0));
    const late = await win.webContents.capturePage();
    fs.writeFileSync(path.join(output, `${from}-to-${to}-late.png`), late.toPNG());
    report.transitions.push(entry);
    save();
  }

  // Analyze: flag frames that suggest a flash.
  for (const entry of report.transitions) {
    const { from, to, frames } = entry;
    const seen = { switchedAt: null, anomalies: [] };
    let baselineX = null, baselineRows = null, baselineH = null;
    for (const f of frames) {
      if (seen.switchedAt === null && f.page === to) seen.switchedAt = f.t;
      const isAfter = seen.switchedAt !== null && f.t >= seen.switchedAt;
      const problems = [];
      if (f.samplerError) problems.push(`sampler-error:${f.samplerError}`);
      if (f.suspended) problems.push("suspense-fallback-visible");
      if (!f.shell) problems.push("settings-shell-missing");
      if (!f.hitSettings) problems.push("settings-main-not-hit-testable");
      if (f.loading?.length) problems.push(`loading:${f.loading.join("|")}`);
      if (f.page && f.page !== from && f.page !== to) problems.push(`unexpected-page:${f.page}`);
      if (isAfter && f.page === to) {
        if (f.scrollTop && f.scrollTop > 1) problems.push(`scrollTop:${f.scrollTop}`);
        if (baselineX === null && f.pageX !== null) baselineX = f.pageX;
        else if (f.pageX !== null && baselineX !== null && Math.abs(f.pageX - baselineX) > 0.6) problems.push(`pageX-shift:${baselineX}->${f.pageX}`);
        // A settled page keeps a stable row count and height; growth after the
        // first painted frame of the destination is a visible pop-in jump.
        if (baselineRows === null && f.rows !== null) baselineRows = f.rows;
        else if (f.rows !== null && baselineRows !== null && f.rows !== baselineRows) problems.push(`rows-grow:${baselineRows}->${f.rows}`);
        if (baselineH === null && f.pageH !== null) baselineH = f.pageH;
        else if (f.pageH !== null && baselineH !== null && Math.abs(f.pageH - baselineH) > 2) problems.push(`height-delta:${baselineH}->${f.pageH}`);
      }
      if (problems.length) seen.anomalies.push({ t: Math.round(f.t), page: f.page, problems });
    }
    entry.anomalies = seen.anomalies;
    entry.switchFrames = frames.filter(f => f.page === to).length;
    entry.frameCount = frames.length;
    save();
  }
  report.anomalousFrames = report.transitions
    .filter(t => t.anomalies?.length)
    .map(t => ({ from: t.from, to: t.to, anomalies: t.anomalies.slice(0, 12) }));
  save();
  console.log(JSON.stringify(report.anomalousFrames, null, 2));
  console.log(`DONE transitions=${report.transitions.length} anomalous=${report.anomalousFrames.length}`);
  app.exit(0);
}

main().catch(error => { console.error(error); app.exit(1); });
