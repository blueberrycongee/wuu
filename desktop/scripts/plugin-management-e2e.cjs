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
process.env.WUU_ENABLE_BROWSER = "1";
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
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
  });
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
async function openAutomations(origin) {
  console.log(`Opening Automations from ${origin}`);
  await evaluate(() => document.querySelector('.plugin-navigation-item[aria-label="Automations"]').click());
  await until(() => document.querySelector('.conversation-pane[data-primary-plugin-view] .plugin-automation'));
  await capture(`automations-from-${origin}`);
  assert.equal(await evaluate(() => Boolean(document.querySelector('.conversation-pane[data-primary-plugin-view] .plugin-workbench-view-primary'))), true, "An empty browser must not deactivate the selected plugin page");
  assert.equal(await evaluate(() => document.querySelector('[data-main-conversation-composer]') === null), true);
}
async function checkCompactRibbon(name) {
  await evaluate(() => {
    const saved = JSON.parse(localStorage.getItem("wuu.navigation-ribbon.v1"));
    localStorage.setItem("wuu.navigation-ribbon.v1", JSON.stringify({ ...saved, width: 360 }));
    window.dispatchEvent(new Event("wuu-navigation-ribbon-change"));
    document.querySelector('[data-ribbon-id="conversations"]').focus();
  });
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Down" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Down" });
  await until(() => document.activeElement?.getAttribute('data-ribbon-id') === 'catalog');
  await capture(`compact-ribbon-${name}`);
  const geometry = await evaluate(() => ({
    ribbon: document.querySelector('.sidebar-ribbon').getBoundingClientRect().width,
    focused: document.activeElement?.getAttribute('data-ribbon-id'),
    toggleReachable: !document.querySelector('.section-sidebar-toggle').hidden,
  }));
  assert.equal(geometry.ribbon, 52, "Saved width and keyboard traversal must preserve the compact icon rail");
  assert.equal(geometry.focused, "catalog");
  assert.equal(geometry.toggleReachable, true);
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
  const draft = "Keep this unsent draft\nwhile browsing plugins and skills.";
  await until(() => document.querySelector('[data-main-conversation-composer] textarea:not(:disabled)'));
  win.focus();
  await evaluate(() => document.querySelector('[data-main-conversation-composer] textarea').focus());
  await win.webContents.insertText(draft);
  assert.equal(await evaluate(() => document.querySelector('[data-main-conversation-composer] textarea').value), draft);
  await evaluate(() => document.querySelector('[data-ribbon-id="catalog"]').click());
  await until(() => document.querySelector(".sidebar-plugin-shortcut"));
  await until(() => document.querySelector('.catalog-refresh:not(:disabled)'));
  assert.equal(await evaluate(() => document.querySelector('[data-main-conversation-composer]') === null), true, "Plugin management must not mount the conversation composer");
  await capture("plugin-list-without-composer");
  await checkCompactRibbon("light-wide");
  await openAutomations("plugins");
  await evaluate(() => document.querySelector('[data-ribbon-id="catalog"]').click());
  await until(() => document.querySelector('.catalog-refresh:not(:disabled)'));
  for (const record of [...modules, ...automation]) {
    await selectPlugin(record);
    assert.equal(await evaluate(() => document.querySelector('[data-main-conversation-composer]') === null), true, "Plugin details must not mount the conversation composer");
    const result = await evaluate(record => window.wuu.getPluginDiagnostics({ id: record.id, fingerprint: record.fingerprint }), record);
    assert.equal(result.id, record.id);
    assert.deepEqual(result.diagnostics, []);
    await capture(record.provenance.plugin_id);
    assert.equal(await evaluate(() => Boolean(document.querySelector('.plugin-page [role="alert"]'))), false, "Healthy plugin details must not show a diagnostic error");
  }
  await evaluate(async () => {
    await window.wuu.setThemePreference("dark");
    document.documentElement.style.setProperty("--conversation-message-font-size", "20px");
  });
  win.setSize(800, 900);
  await evaluate(() => document.querySelector('.sidebar-section-navigation > button:nth-of-type(2)').click());
  await until(() => document.querySelector('[data-tab="skills"][aria-selected="true"]'));
  assert.equal(await evaluate(() => document.querySelector('[data-main-conversation-composer]') === null), true, "Skill management must not mount the conversation composer");
  await capture("skills-dark-narrow-large-without-composer");
  await checkCompactRibbon("dark-narrow-large");
  await openAutomations("skills");
  await evaluate(() => document.querySelector('[data-ribbon-id="conversations"]').click());
  await until(() => document.querySelector('[data-main-conversation-composer] textarea:not(:disabled)'));
  assert.equal(await evaluate(() => document.querySelector('[data-main-conversation-composer] textarea').value), draft, "Returning to chat must restore the entire unsent draft");
  await capture("chat-draft-restored");
  await openAutomations("chat");
  await evaluate(() => document.querySelector('[data-ribbon-id="conversations"]').click());
  await until(() => document.querySelector('[data-main-conversation-composer] textarea:not(:disabled)'));
  assert.equal(await evaluate(() => document.querySelector('[data-main-conversation-composer] textarea').value), draft);
  await evaluate(async () => {
    await window.wuu.setThemePreference("light");
    document.documentElement.style.setProperty("--conversation-message-font-size", "15px");
  });
  win.setSize(1280, 900);
  await evaluate(() => document.querySelector('[data-ribbon-id="catalog"]').click());
  await until(() => document.querySelector(".sidebar-plugin-shortcut"));
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
  fs.writeFileSync(path.join(output, "result.json"), JSON.stringify({ passed: true, modules: modules.map(({ id, name }) => ({ id, name })), automation: automation.map(({ id, name }) => ({ id, name })), scope: "Plugin list, plugin details and skill management omit the composer; multiline draft survives navigation back to chat; light/wide/default and dark/narrow/large font; real IPC diagnostics, stale identity rejected and renderer failure visible" }, null, 2));
  clearTimeout(timeout);
  app.quit();
}
run().catch(async error => { console.error(error); if (win) await capture("failure").catch(() => {}); app.exit(1); });
