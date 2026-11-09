// Run after electron-vite build: electron scripts/compact-composer-e2e.cjs
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const root = path.resolve(__dirname, "..");
const userData = fs.mkdtempSync(path.join(root, "out", "compact-composer-e2e-"));
app.setPath("userData", userData);
process.env.WUU_RESIZE_E2E_CWD = path.dirname(root);
const evaluate = (win, fn) => win.webContents.executeJavaScript("(" + fn + ")()");

async function waitFor(win, predicate) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(win, predicate)) return;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error("Timed out: " + predicate);
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    width: 1200, height: 844, show: false,
    webPreferences: {
      preload: path.join(__dirname, "resize-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false,
    },
  });
  await win.loadFile(path.join(root, "out", "renderer", "index.html"));
  await waitFor(win, () => document.querySelector(".session-tab-new"));
  await evaluate(win, () => document.querySelector(".session-tab-new").click());
  await waitFor(win, () => document.querySelector(".empty-home textarea"));

  // Shrinking height exercises available-space layout, not a real soft keyboard.
  for (const [width, height] of [[390, 844], [390, 460], [700, 390], [390, 844]]) {
    win.setContentSize(width, height);
    await waitFor(win, new Function("return innerWidth === " + width + " && innerHeight === " + height));
    await waitFor(win, () => {
      const input = document.querySelector('[data-main-conversation-composer="dock"] textarea');
      const frame = input?.closest(".composer-frame")?.getBoundingClientRect();
      const content = document.querySelector(".empty-scroll-region")?.getBoundingClientRect();
      return frame && content && content.bottom <= frame.top &&
        frame.width > innerWidth * 0.9 && innerHeight - frame.bottom < 40;
    });
    const geometry = await evaluate(win, () => {
      const composer = document.querySelector('[data-main-conversation-composer="dock"] .composer-frame').getBoundingClientRect();
      const greeting = document.querySelector(".empty-home-header").getBoundingClientRect();
      return { composer: composer.toJSON(), greeting: greeting.toJSON(),
        width: innerWidth, height: innerHeight,
        count: document.querySelectorAll("[data-main-conversation-composer]").length };
    });
    assert.equal(geometry.count, 1, "only one input surface should be mounted");
    assert.ok(geometry.composer.top > geometry.height / 2, JSON.stringify(geometry));
    assert.ok(geometry.composer.bottom <= geometry.height, JSON.stringify(geometry));
    assert.ok(geometry.greeting.bottom <= geometry.composer.top, JSON.stringify(geometry));
    assert.ok(geometry.composer.left >= 0 && geometry.composer.right <= geometry.width);
    console.log(JSON.stringify({ viewport: [width, height], ...geometry }));
  }
  win.destroy();
  app.quit();
}).catch((error) => {
  console.error(error);
  app.exit(1);
});
