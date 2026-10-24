// Run with wuu-e2e --core: real Electron, preload and Go; disposable data and no model calls.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { app } = require("electron");
const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/e2e/plugin-management");
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-plugin-management-"));
const home = path.join(fixture, "wuu-home");
const project = path.join(fixture, "project");
const userHome = path.join(fixture, "user-home");
for (const dir of [output, home, project, userHome]) fs.mkdirSync(dir, { recursive: true });
app.setPath("userData", path.join(fixture, "profile"));
process.env.WUU_HOME = home;
process.env.HOME = userHome;
process.env.WUU_DESKTOP_CORE = path.join(desktop, "build/bin/wuu-core");
process.env.WUU_ENABLE_BROWSER = "0";
process.env.WUU_SAFE_MODE = "0";
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = "1";
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(home, "config.json"), JSON.stringify({
  default_provider: "fixture",
  providers: { fixture: { type: "openai-compatible", base_url: "http://127.0.0.1:9/v1", api_key: "fixture-only", model: "fixture" } },
  engines: Object.fromEntries(["codex", "claude", "cursor", "devin", "dsh", "grok", "hermes", "pi", "opencode", "antigravity"].map(id => [id, { enabled: false }])),
}));
const stamp = "2026-01-01T00:00:00Z";
fs.writeFileSync(path.join(home, "projects.json"), JSON.stringify({
  projects: [{ id: "repo", name: "plugin-management-fixture", path: project, created_at: stamp, updated_at: stamp }],
  active_context: { kind: "project", project_id: "repo", cwd: project },
}));
fs.writeFileSync(path.join(home, "desktop-settings.json"), JSON.stringify({ onboarding_version: 100, language: "zh-CN", theme: "light" }));
let win;
const windowCreated = new Promise(resolve => app.once("browser-window-created", (_event, window) => { win = window; resolve(window); }));
const timeout = setTimeout(() => { console.error("Plugin management journey timed out"); app.exit(1); }, 180000);
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
const until = (fn, arg) => win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
  const test = (${fn});
  const argument = ${JSON.stringify(arg)};
  const observer = new MutationObserver(check);
  const timer = setTimeout(() => { observer.disconnect(); reject(new Error("UI condition timed out")); }, 30000);
  function check() { if (test(argument)) { observer.disconnect(); clearTimeout(timer); resolve(); } }
  observer.observe(document, { childList: true, subtree: true, attributes: true });
  check();
})`);
const capture = async name => {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
};
async function selectPlugin(record) {
  await evaluate(name => {
    const button = [...document.querySelectorAll(".sidebar-plugin-shortcut")].find(button => button.textContent.trim() === name);
    if (!button) throw new Error(`Missing plugin shortcut: ${name}`);
    button.click();
  }, record.name);
  await until(name => document.querySelector(".plugin-page-title")?.textContent === name, record.name);
}
async function run() {
  await import(pathToFileURL(path.join(desktop, "out/main/index.js")).href);
  await windowCreated;
  if (win.webContents.isLoadingMainFrame()) await new Promise(resolve => win.webContents.once("did-finish-load", resolve));
  win.setSize(1280, 900);
  await until(() => document.querySelector('[data-ribbon-id="catalog"]:not(:disabled)'));
  const initialized = await evaluate(() => window.wuu.initialize());
  const modules = initialized.extension_inventory.filter(record => record.desktop?.entry.startsWith("wuu-builtin:"));
  assert.equal(modules.length, 6);
  assert.equal(new Set(modules.map(module => module.name)).size, modules.length, "Built-in interface modules must be distinguishable");
  const automation = initialized.extension_inventory.filter(record => ["automation", "automation-client"].includes(record.provenance.plugin_id));
  assert.equal(automation.length, 2);
  assert.equal(new Set(automation.map(record => record.name)).size, 2, "Automation client and scheduler must be distinguishable");
  await evaluate(() => document.querySelector('[data-ribbon-id="catalog"]').click());
  await until(() => document.querySelector(".sidebar-plugin-shortcut"));
  for (const record of [...modules, ...automation]) {
    await selectPlugin(record);
    const result = await evaluate(record => window.wuu.getPluginDiagnostics({ id: record.id, fingerprint: record.fingerprint }), record);
    assert.equal(result.id, record.id);
    assert.deepEqual(result.diagnostics, []);
    await capture(record.provenance.plugin_id);
    assert.equal(await evaluate(() => Boolean(document.querySelector('.plugin-page [role="alert"]'))), false, "Healthy plugin details must not show a diagnostic error");
  }
  const composer = modules.find(record => record.provenance.plugin_id === "wuu.default-composer");
  const stale = await evaluate(async record => {
    try { await window.wuu.getPluginDiagnostics({ id: record.id, fingerprint: "stale" }); return false; } catch { return true; }
  }, composer);
  assert.equal(stale, true, "Stale identity must still fail");
  await evaluate(async record => {
    const identity = { id: record.id, fingerprint: record.fingerprint, graph_generation: record.graph_generation, activation: 99 };
    await window.wuu.reportPluginDesktopActivation({ ...identity, phase: "registration", status: "active" });
    await window.wuu.reportPluginDesktopActivation({ ...identity, phase: "render", status: "failed", message: "Visible renderer diagnostic fixture" });
  }, composer);
  await selectPlugin(composer);
  await until(() => document.querySelector(".plugin-page")?.textContent.includes("Visible renderer diagnostic fixture"));
  await capture("renderer-failure-visible");
  fs.writeFileSync(path.join(output, "result.json"), JSON.stringify({ passed: true, modules: modules.map(({ id, name }) => ({ id, name })), automation: automation.map(({ id, name }) => ({ id, name })), scope: "Real IPC diagnostics for all built-in UI modules and automation plugins; stale identity rejected; renderer failure visible in plugin details" }, null, 2));
  clearTimeout(timeout);
  app.quit();
}
run().catch(async error => { console.error(error); if (win) await capture("failure").catch(() => {}); app.exit(1); });
