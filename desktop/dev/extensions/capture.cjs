// Manual visual evidence, not a CSS-source or screenshot merge gate.
// From desktop/: vite --config dev/extensions/vite.config.ts
// then electron dev/extensions/capture.cjs.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");

const origin = process.env.WUU_FIXTURE_ORIGIN || "http://127.0.0.1:5173";
const output = path.resolve(process.env.WUU_CAPTURE_OUTPUT || "../artifacts/extensions");
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-extensions-preview-"));
app.setPath("userData", profile);
fs.mkdirSync(output, { recursive: true });

function measure() {
  const rect = (el) => {
    const { x, y, width, height } = el.getBoundingClientRect();
    return { x, y, width, height };
  };
  const selectors = [".settings-page-header", ".catalog-search", ".settings-section", ".settings-group", ".catalog-row", ".catalog-row-title", ".catalog-row-description", ".catalog-row > .settings-status", ".catalog-row > .catalog-row-meta", ".catalog-row > .settings-disclosure-chevron", ".plugin-detail-dialog", ".plugin-permission-chip", ".environment-dialog-footer"];
  return {
    viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio },
    font: getComputedStyle(document.body).fontFamily,
    theme: document.documentElement.dataset.theme,
    modality: document.documentElement.dataset.focusModality,
    permissionOverflow: [...document.querySelectorAll('.plugin-permission-chip')].filter(el => {
      const panel = el.closest('.plugin-detail-dialog').getBoundingClientRect();
      const bounds = el.getBoundingClientRect();
      return bounds.right > panel.right + 1 || el.scrollWidth > el.clientWidth + 1;
    }).map(el => el.textContent),
    regions: selectors.flatMap((selector) => [...document.querySelectorAll(selector)].map((el, index) => ({
      selector, index, text: el.textContent?.trim(), rect: rect(el),
      clientWidth: el.clientWidth, scrollWidth: el.scrollWidth,
    }))),
  };
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: true, width: 1180, height: 900,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  const wc = win.webContents;
  const errors = [];
  wc.on("console-message", (event) => { if (event.level === 3) errors.push(event.message); });
  const run = (code) => wc.executeJavaScript(code);
  const settle = () => run(`(async () => {
    await document.fonts.ready;
    for (let i = 0; i < 120; i++) {
      await new Promise(requestAnimationFrame);
      if (document.querySelector('.catalog-search') && !document.querySelector('.catalog-refresh[aria-busy="true"]') && !document.getAnimations().some(a => a.playState === 'running' && a.effect.getTiming().iterations !== Infinity)) {
        await new Promise(requestAnimationFrame); return;
      }
    }
    throw new Error('Preview did not settle');
  })()`);
  const results = [];
  async function capture(name) {
    await settle();
    const before = await run(`(${measure})()`);
    const screenshot = await wc.capturePage();
    const after = await run(`(${measure})()`);
    if (JSON.stringify(before) !== JSON.stringify(after)) throw new Error(`${name}: layout changed during capture`);
    const png = screenshot.toPNG();
    fs.writeFileSync(path.join(output, `${name}.png`), png);
    results.push({ name, ...before, image: screenshot.getSize(), sha256: crypto.createHash("sha256").update(png).digest("hex") });
    console.log(`captured ${name}`);
  }
  async function open(width, query) {
    console.log(`opening ${width} ${query}`);
    win.setContentSize(width, 900);
    await win.loadURL(`${origin}/dev/extensions/?${query}`);
    await settle();
    wc.sendInputEvent({ type: "mouseMove", x: 0, y: 0 });
  }
  for (const theme of ["light", "dark"]) {
    for (const size of [14.5, 20]) {
      for (const width of [1180, 640, 420]) {
        const name = `${theme}-${size}-${width}`;
        await open(width, `theme=${theme}&size=${size}${width === 1180 ? "" : "&long&lang=en"}`);
        await capture(name);
        await run("document.querySelector('.skills-scroll-region').scrollTop = 100000");
        await capture(`${name}-scrolled`);
      }
    }
    await open(640, `theme=${theme}&size=20&empty`);
    await capture(`${theme}-empty`);
    await open(1180, `theme=${theme}`);
    await run("document.querySelector('.catalog-search input').focus()");
    await wc.insertText("nothing-matches-this");
    await capture(`${theme}-no-matches`);
    await open(1180, `theme=${theme}`);
    await run("document.querySelector('.catalog-search input').focus()");
    await wc.insertText("browser");
    await capture(`${theme}-filtered`);
    await run("document.querySelector('.catalog-row').click()");
    await capture(`${theme}-skill-preview`);
    await open(1180, `theme=${theme}`);
    const point = await run("(() => { const r = document.querySelector('.catalog-row').getBoundingClientRect(); return {x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2)}; })()");
    wc.sendInputEvent({ type: "mouseMove", ...point });
    await capture(`${theme}-hover`);
    wc.sendInputEvent({ type: "mouseMove", x: 0, y: 0 });
    await run("document.querySelector('.catalog-search input').focus()");
    wc.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
    wc.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
    await capture(`${theme}-focus`);
    for (const width of [1180, 420]) {
      for (const title of ["Computer Use for Mac", "Developer Loop", "Git Delivery", "Memory"]) {
        await open(width, `theme=${theme}&size=${width === 420 ? 20 : 14.5}&long&lang=en`);
        await run(`Array.from(document.querySelectorAll('.catalog-row')).find(el => el.querySelector('.catalog-row-title').textContent.startsWith(${JSON.stringify(title)})).click()`);
        await capture(`${theme}-${width}-detail-${title.replaceAll(" ", "-")}`);
        if (title === "Git Delivery") {
          await run("document.querySelector('.extension-package-more').click()");
          await capture(`${theme}-${width}-menu`);
        }
      }
    }
    for (let repeat = 2; repeat <= 3; repeat++) {
      await open(1180, `theme=${theme}&size=14.5`);
      await capture(`${theme}-14.5-1180-repeat-${repeat}`);
    }
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ platform: process.platform, versions: process.versions, errors, captures: results }, null, 2));
  if (errors.length) throw new Error(errors.join("\n"));
  const clipped = results.filter(result => result.permissionOverflow.length);
  if (clipped.length) throw new Error(`Permissions clipped: ${clipped.map(result => result.name).join(', ')}`);
  win.destroy();
  app.quit();
}).catch((error) => {
  console.error(error);
  for (const win of BrowserWindow.getAllWindows()) win.destroy();
  fs.rmSync(profile, { recursive: true, force: true });
  app.exit(1);
});
app.on("will-quit", () => fs.rmSync(profile, { recursive: true, force: true }));
