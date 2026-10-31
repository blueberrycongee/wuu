// Production Personal renderer with synthetic receipt data.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const output = path.resolve(process.env.WUU_RECEIPT_E2E_OUTPUT || path.join(__dirname, "../out/personal-receipts-e2e"));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const report = { scope: "Electron renderer with synthetic receipts; no live model", cases: [] };
let win;
const evaluate = fn => win.webContents.executeJavaScript(`(${fn})()`, true);
async function until(fn) {
  const end = Date.now() + 10000;
  while (Date.now() < end) {
    if (await evaluate(fn)) return;
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  throw new Error(`Timed out: ${fn}`);
}

app.whenReady().then(async () => {
  win = new BrowserWindow({ width: 1100, height: 900, show: true, webPreferences: { contextIsolation: true } });
  for (const [name, width, height, theme, font] of [["light-wide", 1100, 900, "light", "15"], ["dark-narrow-large", 620, 900, "dark", "20"]]) {
    win.setSize(width, height);
    await win.loadFile(path.resolve(__dirname, "../out/personal-fixture/index.html"), { query: { receipts: "1", theme, font } });
    await until(() => document.querySelector('[data-wuu-plugin="personal"]'));
    await evaluate(() => document.querySelector('[data-wuu-plugin="personal"]').click());
    await until(() => document.querySelector('[data-receipt-id="receipt-done"]') && document.querySelector('[data-receipt-id="receipt-waiting"]'));
    const state = await evaluate(() => {
      const read = document.querySelector('[data-receipt-id="receipt-done"]');
      const delivered = document.querySelector('[data-receipt-id="receipt-waiting"]');
      const scroll = document.querySelector('.mosaic-main .plugin-conversation-scroll');
      scroll.scrollTop = 0;
      const bounds = element => {
        const r = element.getBoundingClientRect();
        return { left: r.left, right: r.right, width: r.width, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth };
      };
      return { read: read.dataset.deliveryStatus, delivered: delivered.dataset.deliveryStatus, readBounds: bounds(read), deliveredBounds: bounds(delivered), viewport: innerWidth };
    });
    assert.equal(state.read, "read");
    assert.equal(state.delivered, "delivered");
    for (const bounds of [state.readBounds, state.deliveredBounds]) {
      assert(bounds.width > 0 && bounds.left >= 0 && bounds.right <= state.viewport && bounds.scrollWidth <= bounds.clientWidth + 1);
    }
    await evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
    if (name === "dark-narrow-large") {
      await evaluate(() => {
        document.querySelector('[data-receipt-id="receipt-waiting"]').scrollIntoView({ block: "center" });
        return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      });
      fs.writeFileSync(path.join(output, `${name}-delivered.png`), (await win.webContents.capturePage()).toPNG());
    }
    report.cases.push({ name, ...state });
  }
  report.status = "passed";
}).catch(error => { report.status = "failed"; report.error = error.stack; console.error(error); }).finally(() => {
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
  app.exit(report.status === "passed" ? 0 : 1);
});
