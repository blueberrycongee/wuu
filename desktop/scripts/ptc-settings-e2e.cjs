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
async function openGeneral(win) {
  await waitFor(win, () => Boolean(document.querySelector(".sidebar-account-trigger")));
  await evaluate(win, () => document.querySelector(".sidebar-account-trigger").click());
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => Boolean(document.querySelector('.settings-nav-item')));
  await evaluate(win, () => [...document.querySelectorAll('.settings-nav-item')].find(button => /^(General|常规)$/.test(button.textContent.trim())).click());
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="settings-ptc-enabled"]')));
}
async function choose(win, selector, value) {
  await win.webContents.executeJavaScript(`document.querySelector(${JSON.stringify(selector)}).click()`);
  await waitFor(win, () => Boolean(document.querySelector('[role="menu"]')));
  await win.webContents.executeJavaScript(`document.querySelector('[role="menuitemradio"][data-value="${value}"]').click()`);
  await waitFor(win, () => !document.querySelector('[data-testid="settings-ptc-family-mode"]').disabled);
}
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1180, height: 860, show: false, webPreferences: {
    contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
    preload: path.join(__dirname, "resize-e2e-preload.cjs"),
  }});
  await win.loadFile(path.join(desktopRoot, "out", "renderer", "index.html"));
  await openGeneral(win);
  assert.equal(await evaluate(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').getAttribute("aria-checked")), "false");
  await evaluate(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').click());
  await waitFor(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').getAttribute("aria-checked") === "true");
  await choose(win, '[data-testid="settings-ptc-family-mode"]', "off");
  let settings = await evaluate(win, async () => (await window.wuu.initialize()).general_settings.ptc);
  assert.deepEqual(settings, { enabled: true, families: { gpt: false } });
  await choose(win, '[data-testid="settings-ptc-family-mode"]', "inherit");
  settings = await evaluate(win, async () => (await window.wuu.initialize()).general_settings.ptc);
  assert.deepEqual(settings, { enabled: true, families: {} });
  await evaluate(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').click());
  await waitFor(win, () => document.querySelector('[data-testid="settings-ptc-enabled"]').getAttribute("aria-checked") === "false");
  await choose(win, '[data-testid="settings-ptc-family-mode"]', "on");
  settings = await evaluate(win, async () => (await window.wuu.initialize()).general_settings.ptc);
  assert.deepEqual(settings, { enabled: false, families: { gpt: true } });
  const samples = [];
  for (const theme of ["light", "dark"]) for (const font of [14, 20]) for (const width of [1180, 640]) {
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
  await evaluate(win, () => document.querySelector('[data-testid="settings-ptc-family-mode"]').focus());
  win.webContents.sendInputEvent({type:"keyDown",keyCode:"Space"});
  win.webContents.sendInputEvent({type:"keyUp",keyCode:"Space"});
  await waitFor(win, () => Boolean(document.querySelector('[role="menu"]')));
  await settle(win);
  fs.writeFileSync(path.join(evidence, "keyboard-menu.png"), (await win.webContents.capturePage()).toPNG());
  await evaluate(win, () => document.querySelector('[role="menuitemradio"][data-value="inherit"]').click());
  await waitFor(win, () => !document.querySelector('[data-testid="settings-ptc-family-mode"]').disabled);
  await evaluate(win, () => [...document.querySelectorAll('[data-testid="settings-general"] button')].find(button => button.textContent.trim() === "English").click());
  await waitFor(win, () => document.documentElement.lang === "en-US");
  for (const font of [14, 20]) {
    await win.webContents.executeJavaScript(`document.documentElement.style.setProperty('--font-ui', '${font}px')`);
    await evaluate(win, () => document.querySelector('[data-testid="settings-ptc"]').scrollIntoView({block:"center"}));
    await settle(win);
    fs.writeFileSync(path.join(evidence, `english-${font}-640.png`), (await win.webContents.capturePage()).toPNG());
  }
  settings = await evaluate(win, async () => (await window.wuu.initialize()).general_settings.ptc);
  assert.deepEqual(settings, { enabled: false, families: {} });
  fs.writeFileSync(path.join(evidence, "results.json"), JSON.stringify({settings, samples}, null, 2));
  console.log(JSON.stringify({ok:true, samples:samples.length, settings, evidence}));
  win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
