// Renders the manual to PDF and page images, the documentation previews, and the
// communication templates. Uses the desktop package's Electron:
//   npm ci --prefix desktop && npm --prefix brand run build && npm --prefix brand run render
// Outputs: artifacts/brand/ (PDF, all pages; ignored), docs/en/assets/brand/ (previews),
// brand/assets/social/ and brand/assets/installer/ (template PNGs).
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const brand = path.resolve(__dirname, "..");
const repo = path.resolve(brand, "..");
const manual = path.join(brand, "manual", "index.html");
const artifacts = path.join(repo, "artifacts", "brand");
const previews = path.join(repo, "docs", "en", "assets", "brand");
// Pages shown on the documentation site; ids from brand/src/manual/pages.
const PREVIEW_PAGES = ["cover", "decision", "ball-construction", "lockups", "colour-roles", "neutrals-light", "agent-colours", "type-families", "motion-states", "app-icon", "product-light", "website"];

app.commandLine.appendSwitch("force-device-scale-factor", "1");
app.disableHardwareAcceleration();
// Windows open and close one at a time; keep the app alive between them.
app.on("window-all-closed", () => {});

function makeWindow(width, height) {
  const win = new BrowserWindow({ width, height, show: false, useContentSize: true, enableLargerThanScreen: true, webPreferences: { offscreen: true, backgroundThrottling: false } });
  win.setContentSize(width, height);
  return win;
}
const js = (win, fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map((a) => JSON.stringify(a)).join(",")})`);

// Fail loudly when a required face is missing instead of rendering with a fallback.
// Chinese text uses an installed face, listed through the Local Font Access API.
const CJK_FAMILIES = ["Source Han Sans SC", "Noto Sans SC", "Noto Sans CJK SC"];
async function checkFonts(win) {
  const result = await win.webContents.executeJavaScript(`(async () => {
    await Promise.all(["Hanken Grotesk", "Fragment Mono"].map((f) => document.fonts.load('16px "' + f + '"')));
    const loaded = (name) => [...document.fonts].some((f) => f.family.replace(/"/g, "") === name && f.status === "loaded");
    const local = new Set((await queryLocalFonts()).map((f) => f.family));
    return { "Hanken Grotesk": loaded("Hanken Grotesk"), "Fragment Mono": loaded("Fragment Mono"), "Source Han Sans SC / Noto Sans CJK SC": ${JSON.stringify(CJK_FAMILIES)}.some((f) => local.has(f)) };
  })()`, true);
  const missing = Object.entries(result).filter(([, ok]) => !ok).map(([k]) => k);
  if (missing.length) throw new Error(`Missing fonts: ${missing.join(", ")}`);
}

async function capture(win, rect) {
  const image = await win.webContents.capturePage(rect);
  const size = image.getSize();
  // A window clamped to a small display would silently crop the page.
  if (size.width !== rect.width || size.height !== rect.height) {
    throw new Error(`Captured ${size.width}×${size.height}, expected ${rect.width}×${rect.height}; use a display at least this large`);
  }
  return image.toPNG();
}

async function renderManual() {
  const win = makeWindow(1600, 1000);
  await win.loadURL(pathToFileURL(manual).href);
  await checkFonts(win);
  // Print layout: pages stacked without gaps, live motion at its first frame.
  await js(win, () => { const st = document.createElement("style"); st.textContent = "html,body{overflow:hidden!important}::-webkit-scrollbar{display:none}"; document.head.appendChild(st); document.documentElement.style.background = "none"; const b = document.querySelector(".book"); b.style.padding = "0"; b.style.gap = "0"; document.querySelectorAll(".page").forEach((p) => { p.style.boxShadow = "none"; p.style.borderRadius = "0"; }); });
  await new Promise((r) => setTimeout(r, 400));
  const ids = await js(win, () => [...document.querySelectorAll(".page")].map((p) => p.id));
  fs.mkdirSync(path.join(artifacts, "pages"), { recursive: true });
  fs.mkdirSync(previews, { recursive: true });
  for (const [i, id] of ids.entries()) {
    await js(win, (y) => { document.documentElement.scrollTop = y; }, i * 1000);
    await new Promise((r) => setTimeout(r, 120));
    const png = await capture(win, { x: 0, y: 0, width: 1600, height: 1000 });
    fs.writeFileSync(path.join(artifacts, "pages", `${String(i + 1).padStart(2, "0")}-${id}.png`), png);
    if (PREVIEW_PAGES.includes(id)) fs.writeFileSync(path.join(previews, `manual-${id}.png`), png);
  }
  await js(win, () => { document.documentElement.scrollTop = 0; });
  const pdf = await win.webContents.printToPDF({ preferCSSPageSize: true, printBackground: true, margins: { marginType: "none" } });
  fs.writeFileSync(path.join(artifacts, "wuu-brand-manual.pdf"), pdf);
  win.destroy();
  return ids.length;
}

async function renderTemplates() {
  const { TEMPLATES } = await import(pathToFileURL(path.join(brand, "src", "templates.mjs")).href);
  // A temporary page next to manual.css so relative font URLs resolve; removed afterwards.
  const scratch = path.join(brand, "manual", `.render-${process.pid}.html`);
  const written = [];
  try {
  for (const [name, make] of Object.entries(TEMPLATES)) {
    const t = make();
    const scales = name.startsWith("installer/") ? [1, 2] : [1];
    for (const scale of scales) {
      const win = makeWindow(t.width * scale, t.height * scale);
      const html = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="manual.css"><style>html,body{margin:0;background:none}body{zoom:${scale}}</style></head><body>${t.html}</body></html>`;
      fs.writeFileSync(scratch, html);
      await win.loadFile(scratch);
      await checkFonts(win);
      await new Promise((r) => setTimeout(r, 200));
      const png = await capture(win, { x: 0, y: 0, width: t.width * scale, height: t.height * scale });
      const file = path.join(brand, "assets", `${name}${scale === 2 ? "@2x" : ""}.png`);
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fs.writeFileSync(file, png);
      written.push(path.relative(repo, file));
      win.destroy();
    }
  }
  } finally {
    fs.rmSync(scratch, { force: true });
  }
  return written;
}

app.whenReady().then(async () => {
  const { session } = require("electron");
  session.defaultSession.setPermissionCheckHandler((_, permission) => permission === "local-fonts");
  session.defaultSession.setPermissionRequestHandler((_, permission, done) => done(permission === "local-fonts"));
  try {
    const pages = await renderManual();
    const templates = await renderTemplates();
    console.log(`Rendered ${pages} manual pages, ${PREVIEW_PAGES.length} previews and ${templates.length} template images.`);
  } catch (error) {
    console.error(error);
    process.exitCode = 1;
  }
  app.exit(process.exitCode ?? 0);
});
