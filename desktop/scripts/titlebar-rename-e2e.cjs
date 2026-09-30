const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_TITLEBAR_OUTPUT || path.join(desktop, "out/titlebar-rename-e2e");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
app.on("window-all-closed", () => {});
let win;
const results = [];
const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(arg => JSON.stringify(arg)).join(",")})`, true);
const frames = () => evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function until(fn) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(fn)) return;
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function key(keyCode) {
  win.webContents.sendInputEvent({ type: "keyDown", keyCode });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode });
  await frames();
}
async function capture(name) {
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
}
async function measure(state) {
  const geometry = await evaluate(() => {
    const heading = document.querySelector(".conversation-title-heading").getBoundingClientRect();
    const title = document.querySelector(".conversation-title-heading h1").getBoundingClientRect();
    const field = document.querySelector(".conversation-title-edit, .conversation-title-rename").getBoundingClientRect();
    const actions = document.querySelector(".title-actions").getBoundingClientRect();
    return { headingWidth: heading.width, titleWidth: title.width, fieldWidth: field.width,
      titleRight: title.right, actionsLeft: actions.left, fieldRight: field.right };
  });
  assert.ok(geometry.titleWidth > 0 && geometry.titleWidth <= geometry.headingWidth * 0.6 + 1,
    `${state}: title must leave at least 40% of the heading available: ${JSON.stringify(geometry)}`);
  assert.ok(geometry.fieldWidth <= geometry.titleWidth + 1 && geometry.fieldRight <= geometry.titleRight + 1,
    `${state}: title field must stay within its cap: ${JSON.stringify(geometry)}`);
  assert.ok(geometry.titleRight <= geometry.actionsLeft, `${state}: title overlaps actions`);
  results.push({ state, ...geometry });
  await capture(state);
}
app.whenReady().then(async () => {
  win = new BrowserWindow({ width: 1180, height: 820, show: true, webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true,
    nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await until(() => Boolean(document.querySelector(".turn"))
    && document.querySelector(".conversation-title-rename")?.textContent.includes("Resize fixture"));
  win.focus(); win.webContents.focus();
  const point = await evaluate(() => {
    const rect = document.querySelector(".conversation-title-rename").getBoundingClientRect();
    return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
  });
  for (const clickCount of [1, 2]) {
    win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount, ...point });
    win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount, ...point });
    await frames();
    assert.equal(await evaluate(() => Boolean(document.querySelector(".conversation-title-edit"))), clickCount === 2,
      "Only the second pointer click should open the title editor");
  }
  await key("Escape");
  for (const activation of ["Enter", "Space"]) {
    await evaluate(() => document.querySelector(".conversation-title-rename").focus());
    await key(activation);
    await until(() => document.activeElement?.classList.contains("conversation-title-edit"));
    await key("Escape");
    assert.ok(await evaluate(() => !document.querySelector(".conversation-title-edit")), "Escape must cancel the edit");
  }
  for (const theme of ["light", "dark"]) for (const font of [14, 20]) for (const width of [1180, 420]) {
    win.setContentSize(width, 820);
    await evaluate((theme, font) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--font-ui", `${font}px`);
    }, theme, font);
    await frames();
    const name = `${theme}-${font}-${width}`;
    await measure(`${name}-idle`);
    await evaluate(() => document.querySelector(".conversation-title-rename").focus());
    await key("Enter");
    await until(() => Boolean(document.querySelector(".conversation-title-edit")));
    await measure(`${name}-editing`);
    await key("Escape");
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
  console.log("Titlebar pointer, keyboard, cancellation, and rendered width checks passed");
  app.exit(0);
}).catch(async error => {
  console.error(error);
  if (win && !win.isDestroyed()) await capture("failure");
  fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack));
  app.exit(1);
});
