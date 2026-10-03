// Manual rendered review of the Review panel, not a golden-image merge gate.
// From desktop/: npx vite --config dev/review/vite.config.ts
// then: ./node_modules/.bin/electron dev/review/capture.cjs
// Output: artifacts/review-panel/ (screenshots and results.json).
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");

const output = path.resolve(__dirname, "../../../artifacts/review-panel");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const origin = process.env.WUU_FIXTURE_ORIGIN || "http://127.0.0.1:5219";
const only = process.env.WUU_SHOT_ONLY;

const shots = [
  { name: "wide-light-14", width: 1100, theme: "light", size: 14 },
  { name: "wide-dark-14", width: 1100, theme: "dark", size: 14 },
  { name: "wide-light-20", width: 1100, theme: "light", size: 20 },
  { name: "docked-light-14", width: 560, theme: "light", size: 14 },
  { name: "docked-dark-20", width: 560, theme: "dark", size: 20 },
  { name: "narrow-light-14", width: 380, theme: "light", size: 14 },
  { name: "one-file-light-14", width: 760, theme: "light", size: 14, set: "one" },
  { name: "clean-light-14", width: 560, theme: "light", size: 14, set: "clean" },
  { name: "long-branch-en-14", width: 760, theme: "light", size: 14, set: "long", lang: "en" },
  // A split panel whose file header wraps the folder under the name.
  { name: "split-nested-en-20", width: 760, theme: "light", size: 20, lang: "en", open: "desktop/src/renderer/styles/sidebar.css" },
  // A narrow panel after opening a file from the list.
  { name: "docked-detail-light-14", width: 560, theme: "light", size: 14, open: "desktop/src/renderer/WorkspaceReviewPanels.tsx" },
  { name: "narrow-detail-dark-20", width: 380, theme: "dark", size: 20, open: "docs/en/project/review.md" },
];

const settle = "new Promise((r) => setTimeout(() => requestAnimationFrame(() => requestAnimationFrame(r)), 700))";

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    show: false,
    width: 1200,
    height: 760,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false },
  });
  const errors = [];
  win.webContents.on("console-message", (event) => {
    if (event.level === "error" || event.level === 3) errors.push(event.message);
  });
  const results = [];
  for (const shot of shots) {
    if (only && !shot.name.includes(only)) continue;
    win.setContentSize(shot.width + 40, 760);
    const params = new URLSearchParams({ theme: shot.theme, size: String(shot.size), width: String(shot.width) });
    if (shot.set) params.set("set", shot.set);
    if (shot.lang) params.set("lang", shot.lang);
    await win.loadURL(`${origin}/dev/review/index.html?${params}`);
    await win.webContents.executeJavaScript(settle);
    if (shot.open) {
      await win.webContents.executeJavaScript(
        `document.querySelector('.workspace-review-row[data-wuu-path="${shot.open}"]').click()`,
      );
      await win.webContents.executeJavaScript(settle);
    }
    const state = await win.webContents.executeJavaScript(`(() => {
      const panel = document.querySelector('[data-wuu-component="workspace-review"]');
      const overflow = [...document.querySelectorAll('[data-wuu-component^="workspace-review"] *')]
        .filter((el) => el.scrollWidth > el.clientWidth + 1 && getComputedStyle(el).overflowX === "visible")
        .map((el) => el.className);
      return {
        layout: panel?.dataset.wuuLayout ?? null,
        state: panel?.dataset.wuuState ?? null,
        overflow,
      };
    })()`);
    fs.writeFileSync(path.join(output, `${shot.name}.png`), (await win.webContents.capturePage()).toPNG());
    results.push({ ...shot, ...state });
    console.log(`captured ${shot.name}`);
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ results, errors }, null, 2));
  if (errors.length) console.error(errors.join("\n"));
  app.exit(0);
}).catch((error) => {
  console.error(error);
  app.exit(1);
});
