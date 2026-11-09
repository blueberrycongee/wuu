// Manual rendered-geometry review, not a CSS-source or golden-image merge gate.
// From desktop/: npx vite --config dev/environment-panel/vite.config.ts
// then: ./node_modules/.bin/electron dev/environment-panel/capture.cjs
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");

const output = path.resolve(__dirname, "../../../artifacts/environment-panel-layout");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const origin = process.env.WUU_FIXTURE_ORIGIN || "http://127.0.0.1:5218";

const measure = () => {
  const cards = [...document.querySelectorAll(".environment-panel-preview-card")].map((card) => {
    const panel = card.querySelector(".environment-panel");
    const close = panel.querySelector(".environment-panel-close-row .icon-button");
    const title = panel.querySelector(".plugin-inspector-section > h2");
    const firstRow = panel.querySelector(".environment-row");
    const todoMarker = panel.querySelector(".plugin-todo-marker");
    const rowIcon = firstRow?.querySelector(":scope > svg");
    const closeRect = close.getBoundingClientRect();
    const panelRect = panel.getBoundingClientRect();
    const firstRect = firstRow.getBoundingClientRect();
    const overlaps = (a, b) => !(a.right <= b.left || a.left >= b.right || a.bottom <= b.top || a.top >= b.bottom);
    const items = [...panel.querySelectorAll(".environment-row, .plugin-todo-item, .plugin-inspector-section > h2")];
    const lastRow = [...panel.querySelectorAll(".environment-row")].at(-1);
    const lastRect = lastRow?.getBoundingClientRect();
    return {
      label: card.querySelector("figcaption")?.textContent,
      closeInPanel: closeRect.right <= panelRect.right + 0.5 && closeRect.top >= panelRect.top - 0.5,
      closeOverlapsRow: overlaps(closeRect, firstRect),
      closeOverlapsTitle: title ? (() => {
        const t = title.getBoundingClientRect();
        const textRight = t.right - parseFloat(getComputedStyle(title).paddingRight);
        return !(closeRect.left + 0.5 >= textRight || closeRect.right <= t.left || closeRect.bottom <= t.top || closeRect.top >= t.bottom);
      })() : false,
      closeAboveOrBesideFirst: closeRect.bottom <= firstRect.top + 1 || closeRect.left >= firstRect.right - 1,
      closeSharesLead: Math.abs(closeRect.top - (title || firstRow).getBoundingClientRect().top) < 6,
      titleBottom: title ? title.getBoundingClientRect().bottom : null,
      closeBottom: closeRect.bottom,
      markerX: todoMarker ? todoMarker.getBoundingClientRect().left : null,
      iconX: rowIcon
        ? firstRow.getBoundingClientRect().left + parseFloat(getComputedStyle(firstRow).paddingLeft)
        : null,
      itemCount: items.length,
      panelHeight: panelRect.height,
      lastRowVisible: lastRect ? lastRect.bottom <= panelRect.bottom + 1 : false,
    };
  });
  return { cards, theme: document.documentElement.dataset.theme };
};

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    show: false,
    width: 900,
    height: 1600,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false },
  });
  const errors = [];
  win.webContents.on("console-message", (event) => {
    if (event.level === 3) errors.push(event.message);
  });
  const report = [];
  for (const theme of ["light", "dark"]) {
    for (const size of [14, 20]) {
      const name = `${theme}-${size}`;
      await win.loadURL(`${origin}/dev/environment-panel/index.html?theme=${theme}&size=${size}`);
      await win.webContents.executeJavaScript("new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))");
      const result = await win.webContents.executeJavaScript(`(${measure})()`);
      for (const card of result.cards) {
        if (!card.closeInPanel) throw new Error(`${name} ${card.label}: close outside panel`);
        if (card.closeOverlapsRow) throw new Error(`${name} ${card.label}: close overlaps git row`);
        if (card.closeOverlapsTitle) throw new Error(`${name} ${card.label}: close overlaps TODO title`);
        if (card.markerX != null && card.iconX != null && Math.abs(card.markerX - card.iconX) > 2) {
          throw new Error(`${name} ${card.label}: todo marker ${card.markerX} vs git icon ${card.iconX}`);
        }
        if (!card.lastRowVisible) throw new Error(`${name} ${card.label}: last git row clipped (${JSON.stringify(card)})`);
        if (!card.closeSharesLead) throw new Error(`${name} ${card.label}: close sits on a blank row (${JSON.stringify(card)})`);
      }
      fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
      report.push({ name, ...result });
      console.log(`captured ${name}`);
    }
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2));
  if (errors.length) console.error(errors.join("\n"));
  app.exit(0);
}).catch((error) => {
  console.error(error);
  app.exit(1);
});
