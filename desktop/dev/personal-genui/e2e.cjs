const { app, BrowserWindow } = require("electron");
const { spawn } = require("node:child_process");
const { once } = require("node:events");
const { createInterface } = require("node:readline");
const fs = require("node:fs/promises");
const path = require("node:path");
const assert = require("node:assert/strict");
const root = path.resolve(__dirname, "../../..");
const output = path.resolve(process.env.GENUI_OUTPUT || path.join(root, ".tmp/personal-genui"));
app.setPath("userData", path.join(output, "electron-fixture"));
let child, win, base;
const report = { fixture: true, realModel: false, steps: [], screenshots: [] };
const evaluate = fn => win.webContents.executeJavaScript(`(${fn.toString()})()`);
async function waitFor(fn) {
  for (let i = 0; i < 200; i++) { if (await evaluate(fn)) return; await new Promise(r => setTimeout(r, 25)); }
  throw new Error(`timeout: ${fn}`);
}
async function start() {
  child = spawn(process.env.GENUI_NODE || "/opt/homebrew/bin/node", [path.join(__dirname, "server.mjs")], { env: { ...process.env, GENUI_OUTPUT: output }, stdio: ["ignore", "pipe", "inherit"] });
  const lines = createInterface({ input: child.stdout });
  const [line] = await once(lines, "line"); base = JSON.parse(line).url; lines.close();
}
async function screenshot(name) { await evaluate(async () => { await Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => {}))); await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); }); const file = `${name}.png`; await fs.writeFile(path.join(output, file), (await win.webContents.capturePage()).toPNG()); report.screenshots.push(file); }
async function load() { await win.loadURL(`${base}/?service=fixture`); await waitFor(() => document.querySelectorAll(".personal-genui").length === 3); }
async function status() { return (await fetch(`${base}/fixture/status`)).json(); }
app.whenReady().then(async () => {
  await fs.rm(path.join(output, "fixture-state.json"), { force: true });
  await start();
  win = new BrowserWindow({ width: 1100, height: 1000, show: false, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  const errors = []; win.webContents.on("console-message", e => { if (e.level === "error") errors.push(e.message); });
  await win.loadURL(base);
  for (const damaged of ["null", "[]", "{}", '{"checklist":{}}', '{"checklist":null}']) {
    await win.webContents.executeJavaScript(`localStorage.setItem("wuu-synthetic-genui-preview-v1", ${JSON.stringify(damaged)})`);
    const loaded = once(win.webContents, "did-finish-load"); win.reload(); await loaded;
    await waitFor(() => document.querySelectorAll(".personal-genui").length === 3);
    await evaluate(() => [...document.querySelectorAll("button")].find(button => button.textContent === "重置示例").click());
    await waitFor(() => document.querySelectorAll(".personal-genui").length === 3);
  }
  report.steps.push("malformed standalone localStorage is validated and reset remains available");
  await evaluate(() => {
    const slider = document.querySelector('[data-example="calculator"] input[type="range"]');
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(slider, "8"); slider.dispatchEvent(new Event("input", { bubbles: true }));
    document.querySelector('[data-example="checklist"] input[type="checkbox"]').click();
  });
  await waitFor(() => document.querySelector('[data-example="calculator"] [data-genui-node="sum"]').textContent === "640");
  await evaluate(() => document.querySelector('[data-example="checklist"] [data-genui-node="save"]').click());
  await waitFor(() => document.querySelector('[data-example="checklist"]').textContent.includes("选择已保存"));
  await evaluate(() => document.querySelector('[data-example="calculator"] [data-genui-node="submit"]').click());
  await waitFor(() => document.querySelector('[data-example="calculator"] input[type="range"]').disabled);
  const submittedReload = once(win.webContents, "did-finish-load"); win.reload(); await submittedReload;
  await waitFor(() => document.querySelector('[data-example="calculator"] input[type="range"]')?.disabled);
  assert.equal(await evaluate(() => document.querySelector('[data-example="calculator"] [data-genui-node="sum"]').textContent), "640");
  await evaluate(() => document.querySelector('[data-example="calculator"]').scrollIntoView());
  await screenshot("standalone-before-reset-submitted");
  await evaluate(() => [...document.querySelectorAll("button")].find(button => button.textContent === "重置示例").click());
  await waitFor(() => {
    const slider = document.querySelector('[data-example="calculator"] input[type="range"]');
    const checkbox = document.querySelector('[data-example="checklist"] input[type="checkbox"]');
    return slider.value === "4" && !slider.disabled && !checkbox.checked && !checkbox.disabled && !document.querySelector('[data-example="calculator"] [data-genui-node="submit"]').disabled;
  });
  assert.equal(await evaluate(() => document.querySelector('[data-example="calculator"] [data-genui-node="sum"]').textContent), "320");
  assert.equal(await evaluate(() => Object.values(JSON.parse(localStorage.getItem("wuu-synthetic-genui-preview-v1"))).every(result => result.surface.revision === 0 && !result.surface.submitted)), true);
  await evaluate(() => document.querySelector('[data-example="calculator"]').scrollIntoView());
  await screenshot("standalone-after-reset-editable");
  report.steps.push("standalone reset immediately restores all default values and editability after loading persisted save plus simulated submit, with no second navigation or reload");
  await evaluate(() => {
    const slider = document.querySelector('[data-example="calculator"] input[type="range"]');
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(slider, "9"); slider.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await waitFor(() => document.querySelector('[data-example="calculator"] [data-genui-node="sum"]').textContent === "720");
  await evaluate(() => document.querySelector('[data-example="calculator"] [data-genui-node="save"]').click());
  await waitFor(() => document.querySelector('[data-example="calculator"]').textContent.includes("选择已保存"));
  const savedReload = once(win.webContents, "did-finish-load"); win.reload(); await savedReload;
  await waitFor(() => document.querySelector('[data-example="calculator"] [data-genui-node="sum"]')?.textContent === "720");
  await evaluate(() => [...document.querySelectorAll("button")].find(button => button.textContent === "重置示例").click());
  await waitFor(() => document.querySelector('[data-example="calculator"] [data-genui-node="sum"]').textContent === "320");
  assert.equal(await evaluate(() => document.querySelector('[data-example="calculator"] input[type="range"]').disabled), false);
  report.steps.push("standalone reset also restores defaults after loading a persisted edit and save, with no second refresh");
  await load();
  await screenshot("desktop-light");
  await evaluate(() => document.querySelector('[data-example="calculator"]').scrollIntoView()); await screenshot("desktop-calculator-comparison");
  await evaluate(() => window.scrollTo(0, 0));
  await evaluate(() => document.querySelector('[data-example="checklist"] input[type="checkbox"]').click());
  assert.equal((await status()).writes, 3); assert.equal((await status()).modelCalls, 0);
  await evaluate(() => { const button = document.querySelector('[data-example="checklist"] [data-genui-node="save"]'); button.click(); button.click(); });
  await waitFor(() => document.querySelector('[data-example="checklist"]').textContent.includes("选择已保存"));
  assert.equal((await status()).writes, 4);
  await load();
  assert.equal(await evaluate(() => document.querySelector('[data-example="checklist"] input[type="checkbox"]').checked), true);
  report.steps.push("checkbox is local until explicit save; double-click save writes once; refresh restores state");
  await evaluate(() => {
    const slider = document.querySelector('[data-example="calculator"] input[type="range"]');
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(slider, "8"); slider.dispatchEvent(new Event("input", { bubbles: true })); slider.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await waitFor(() => document.querySelector('[data-example="calculator"] [data-genui-node="sum"]').textContent === "640");
  assert.equal((await status()).modelCalls, 0); report.steps.push("slider calculates 8 × 80 = 640 locally without model invocation");
  await evaluate(() => { const section = document.querySelector('[data-example="comparison"]'); section.querySelector('input[aria-label="需要考虑无障碍条件"]').click(); section.querySelector('input[aria-label="展开说明（仅本次查看）"]').click(); });
  await waitFor(() => document.querySelector('input[aria-label="需要留意的条件"]'));
  await evaluate(() => document.querySelector('[data-example="comparison"] [data-genui-node="save"]').click());
  await waitFor(() => document.querySelector('[data-example="comparison"]').textContent.includes("选择已保存"));
  await load();
  assert.equal(await evaluate(() => document.querySelector('input[aria-label="展开说明（仅本次查看）"]').checked), false);
  assert.equal(await evaluate(() => !!document.querySelector('input[aria-label="需要留意的条件"]')), true);
  report.steps.push("conditional form restores persistent fields and resets temporary expansion");
  await evaluate(() => { const button = document.querySelector('[data-example="calculator"] [data-genui-node="submit"]'); button.click(); button.click(); });
  await waitFor(() => document.querySelector('[data-example="calculator"]').textContent.includes("示例选择已记录"));
  assert.equal((await status()).recordedSubmissions, 1);
  report.steps.push("double-click submit produces one recorded session-send in the fixture; no real model call");
  const stopped = once(child, "exit"); child.kill("SIGTERM"); await stopped; await start(); await load();
  assert.equal(await evaluate(() => document.querySelector('[data-example="checklist"] input[type="checkbox"]').checked), true);
  assert.equal((await status()).recordedSubmissions, 1);
  report.steps.push("Node service restart preserves three surfaces and submit receipt");
  await evaluate(() => { document.documentElement.dataset.theme = "dark"; }); await screenshot("desktop-dark");
  win.setSize(390, 844); await evaluate(() => { document.documentElement.dataset.theme = "light"; window.scrollTo(0, 0); });
  await screenshot("narrow-light");
  await evaluate(() => document.querySelector('[data-example="checklist"]').scrollIntoView()); await screenshot("narrow-long-chinese");
  const noOverflow = await evaluate(() => document.documentElement.scrollWidth <= innerWidth); assert.equal(noOverflow, true);
  await evaluate(() => { document.documentElement.dataset.theme = "dark"; document.documentElement.dataset.large = "true"; document.querySelector('[data-example="comparison"]').scrollIntoView(); });
  await screenshot("narrow-dark-large-conditional");
  assert.equal(await evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  win.webContents.debugger.attach("1.3"); await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await evaluate(() => { document.documentElement.dataset.focusModality = "keyboard"; document.querySelector('[data-example="comparison"] select').focus(); });
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" }); win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
  await waitFor(() => document.activeElement?.getAttribute("aria-label") === "需要考虑无障碍条件");
  await screenshot("narrow-keyboard-focus");
  report.steps.push("390px light/dark, enlarged text and keyboard Tab focus without horizontal overflow");
  assert.deepEqual(errors, []);
  await win.loadURL(`${base}/?service=fixture&invalid_fixture=1`);
  await waitFor(() => document.querySelector('[data-example="checklist"]')?.textContent.includes("示例清单"));
  assert.equal(await evaluate(() => document.querySelectorAll('[data-example="checklist"] button').length), 0);
  await screenshot("unsupported-version-fallback");
  report.steps.push("unsupported schema version renders readable fallback with no interactive controls");
  report.pass = true;
  await fs.writeFile(path.join(output, "e2e-report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
}).catch(async error => { report.pass = false; report.error = error.stack; await fs.writeFile(path.join(output, "e2e-report.json"), JSON.stringify(report, null, 2)); console.error(error); process.exitCode = 1; }).finally(() => { child?.kill("SIGTERM"); win?.destroy(); app.exit(report.pass ? 0 : 1); });
