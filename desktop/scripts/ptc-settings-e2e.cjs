// Renderer/bridge acceptance with a synthetic backend; Go integration tests own
// real configuration persistence and program execution.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktopRoot = path.resolve(__dirname, "..");
const evidence = path.join(desktopRoot, "out", "e2e", "ptc");
fs.mkdirSync(evidence, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(evidence, "profile-")));
process.env.WUU_RESIZE_E2E_CWD = path.resolve(desktopRoot, "..");
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn.toString()})()`, true);
async function waitFor(win, fn) {
  const end = Date.now() + 15000;
  while (Date.now() < end) {
    if (await evaluate(win, fn)) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function settle(win) {
  await evaluate(win, async () => {
    await document.fonts.ready;
    await new Promise(requestAnimationFrame);
    await Promise.all(document.getAnimations().filter(a => a.playState === "running" && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
  });
}
// PTC lives on the built-in agent page; the language switch stays on General.
async function openSettingsPage(win, label, ready) {
  await win.webContents.executeJavaScript(`[...document.querySelectorAll('.settings-nav-item')].find(button => ${label}.test(button.textContent.trim())).click()`);
  const end = Date.now() + 15000;
  while (!(await win.webContents.executeJavaScript(`Boolean(document.querySelector(${JSON.stringify(ready)}))`))) {
    if (Date.now() > end) throw new Error(`Timed out opening ${label}`);
    await new Promise(resolve => setTimeout(resolve, 40));
  }
}
async function openRuntime(win) {
  await waitFor(win, () => Boolean(document.querySelector(".sidebar-account-trigger")));
  await evaluate(win, () => document.querySelector(".sidebar-account-trigger").click());
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-entry="settings"]')));
  await evaluate(win, () => document.querySelector('[data-settings-entry="settings"]').click());
  await waitFor(win, () => Boolean(document.querySelector('.settings-nav-item')));
  await openSettingsPage(win, "/^(Built-in agent|内置 Agent)$/", '[data-testid="settings-ptc-enabled"]');
  await revealRuntimeControls(win);
}
async function revealRuntimeControls(win) {
  await evaluate(win, () => {
    const disclosure = document.querySelector('[data-testid="settings-advanced-disclosure"]');
    if (!disclosure.open) disclosure.querySelector('summary').click();
    document.querySelector('[data-testid="settings-ptc"]').scrollIntoView({ block: "center" });
  });
  await settle(win);
  await waitFor(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').checkVisibility());
}
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1180, height: 860, show: false, webPreferences: {
    contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
    preload: path.join(__dirname, "resize-e2e-preload.cjs"),
  }});
  await win.loadFile(path.join(desktopRoot, "out", "renderer", "index.html"));
  win.setContentSize(1180, 860);
  await waitFor(win, () => innerWidth === 1180 && innerHeight === 860);
  await openRuntime(win);
  assert.equal(await evaluate(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').getAttribute("aria-checked")), "true");
  let settings;
  const toggle = await evaluate(win, () => {
    const button = document.querySelector('[data-testid="settings-ptc-enabled"]');
    const box = button.getBoundingClientRect();
    const x = box.x + box.width / 2, y = box.y + box.height / 2;
    const hit = document.elementFromPoint(x, y);
    return { x, y, owned: hit === button || button.contains(hit) };
  });
  assert.ok(toggle.owned, `The expanded PTC switch must own its pointer target: ${JSON.stringify(toggle)}`);
  win.webContents.sendInputEvent({ type: "mouseDown", x: Math.round(toggle.x), y: Math.round(toggle.y), button: "left", clickCount: 1 });
  win.webContents.sendInputEvent({ type: "mouseUp", x: Math.round(toggle.x), y: Math.round(toggle.y), button: "left", clickCount: 1 });
  await waitFor(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').getAttribute("aria-checked") === "false");
  settings = await evaluate(win, async () => (await window.wuu.initialize()).general_settings.ptc);
  assert.deepEqual(settings, { enabled: false });
  const samples = [];
  for (const theme of ["light", "dark"]) for (const font of [15, 20]) for (const width of [1180, 640]) {
    win.setContentSize(width, 860);
    await win.webContents.executeJavaScript(`document.documentElement.dataset.theme = '${theme}'; document.documentElement.style.setProperty('--font-ui', '${font}px');`);
    await evaluate(win, () => document.querySelector('[data-testid="settings-ptc"]').scrollIntoView({block:"center"}));
    await settle(win);
    const geometry = await evaluate(win, () => {
      const section = document.querySelector('[data-testid="settings-ptc"]');
      return [...section.querySelectorAll('button')].map(button => {
        const r = button.getBoundingClientRect();
        const target = document.elementFromPoint(r.x+r.width/2, r.y+r.height/2);
        return { label: button.getAttribute('aria-label') || button.textContent, x:r.x, y:r.y, right:r.right, bottom:r.bottom,
          visible:r.width > 0 && r.height > 0 && r.x >= 0 && r.right <= innerWidth && r.y >= 0 && r.bottom <= innerHeight,
          hit:target === button || button.contains(target) };
      });
    });
    fs.writeFileSync(path.join(evidence, `${theme}-${font}-${width}.png`), (await win.webContents.capturePage()).toPNG());
    assert.ok(geometry.every(g => g.visible && g.hit), JSON.stringify({theme,font,width,geometry}));
    samples.push({ theme, font, width, geometry });
  }
  await openSettingsPage(win, "/^(General|常规)$/", '[data-testid="settings-general"]');
  await evaluate(win, () => [...document.querySelectorAll('[data-testid="settings-general"] button')].find(button => button.textContent.trim() === "English").click());
  await waitFor(win, () => document.documentElement.lang === "en-US");
  await openSettingsPage(win, "/^(Built-in agent|内置 Agent)$/", '[data-testid="settings-ptc"]');
  await revealRuntimeControls(win);
  for (const font of [15, 20]) {
    await win.webContents.executeJavaScript(`document.documentElement.style.setProperty('--font-ui', '${font}px')`);
    await evaluate(win, () => document.querySelector('[data-testid="settings-ptc"]').scrollIntoView({block:"center"}));
    await settle(win);
    fs.writeFileSync(path.join(evidence, `english-${font}-640.png`), (await win.webContents.capturePage()).toPNG());
  }
  settings = await evaluate(win, async () => (await window.wuu.initialize()).general_settings.ptc);
  assert.deepEqual(settings, { enabled: false });
  fs.writeFileSync(path.join(evidence, "results.json"), JSON.stringify({settings, samples}, null, 2));
  console.log(JSON.stringify({ok:true, samples:samples.length, settings, evidence}));
  win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
