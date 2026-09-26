const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const root = path.resolve(__dirname, "..");
const output = path.resolve(root, "../.tmp/sidebar-main-toggle/verified");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1200, height: 820, show: false, titleBarStyle: "hiddenInset", webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, sandbox: false, backgroundThrottling: false,
  } });
  win.webContents.debugger.attach("1.3");
  win.webContents.on("did-finish-load", () => void win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true }));
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  const js = expression => win.webContents.executeJavaScript(expression);
  const selector = '.conversation-pane [data-wuu-component="sidebar-toggle"]';
  const drawerOpen = `document.querySelector('.app-shell').classList.contains('sidebar-drawer-open')`;
  const collapsed = `document.querySelector('.app-shell').classList.contains('sidebar-collapsed')`;
  async function wait(expression) {
    for (let i = 0; i < 180; i++) {
      if (await js(expression)) return;
      await new Promise(resolve => setTimeout(resolve, 30));
    }
    throw new Error(`Timed out: ${expression}\n${JSON.stringify(await measure())}\n${errors.join("\n")}`);
  }
  const measure = () => js(`(()=>{const b=document.querySelector('${selector}'),r=b.getBoundingClientRect(),hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2),s=getComputedStyle(b);return {pressed:b.getAttribute('aria-pressed'),label:b.getAttribute('aria-label'),rect:r.toJSON(),hitsButton:b===hit||b.contains(hit),hit:hit?.outerHTML.slice(0,250),hover:b.matches(':hover'),background:s.backgroundColor,focusVisible:b.matches(':focus-visible'),outline:s.outlineWidth,region:s.webkitAppRegion,title:b.getAttribute('title'),shell:document.querySelector('.app-shell').className,sidebar:document.querySelector('.sidebar').getBoundingClientRect().toJSON()}})()`);
  async function settled() {
    await wait(`!document.querySelector('.app-shell').classList.contains('sidebar-animating') && !document.querySelector('.app-shell').classList.contains('sidebar-drawer-closing') && !document.querySelector('.app-shell').classList.contains('sidebar-drawer-docking') && [document.querySelector('.app-shell'),document.querySelector('.sidebar'),document.querySelector('${selector}').closest('.titlebar')].every(n=>n.getAnimations().every(a=>a.playState!=='running'))`);
  }
  function key(keyCode, modifiers = []) {
    win.webContents.sendInputEvent({ type: "keyDown", keyCode, modifiers });
    if (keyCode === "Enter") win.webContents.sendInputEvent({ type: "char", keyCode: "\r" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
  }
  const away = () => win.webContents.sendInputEvent({ type: "mouseMove", x: 550, y: 500 });
  async function point() {
    const { rect } = await measure();
    return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) };
  }
  function click(p) {
    win.webContents.sendInputEvent({ type: "mouseDown", ...p, button: "left", clickCount: 1 });
    win.webContents.sendInputEvent({ type: "mouseUp", ...p, button: "left", clickCount: 1 });
  }
  const report = [];
  await win.loadFile(path.join(root, "out/renderer/index.html"));
  await wait(`!!document.querySelector('${selector}')`);
  win.setContentSize(1200, 820); await settled();
  for (const theme of ["light", "dark"]) for (const font of [14, 18]) {
    await js(`document.documentElement.dataset.theme='${theme}';document.documentElement.style.setProperty('--appearance-scale','${font / 14}')`);
    win.setContentSize(600, 820);
    await wait(`document.querySelector('.app-shell').classList.contains('compact-navigation')`);
    await settled(); away();
    const before = await measure();
    const p = await point();
    win.webContents.sendInputEvent({ type: "mouseMove", ...p });
    await wait(drawerOpen); await settled();
    const hover = await measure();
    assert.equal(hover.pressed, "true");
    assert(hover.hitsButton && hover.hover && hover.sidebar.right > p.x, JSON.stringify(hover));
    assert.equal(hover.rect.x, before.rect.x, "Drawer must not displace its trigger");
    assert.notEqual(hover.background, before.background, "Hover must remain visibly active above the drawer");
    fs.writeFileSync(path.join(output, `${theme}-${font}-hover.png`), (await win.webContents.capturePage()).toPNG());
    click(p); await wait(`!(${drawerOpen})`); await settled();
    assert.equal((await measure()).pressed, "false");

    // Keyboard follows the same toggle contract without requiring hover.
    away(); await js(`document.querySelector('${selector}').focus()`);
    key("Tab"); key("Tab", ["shift"]);
    await wait(`document.activeElement===document.querySelector('${selector}') && document.activeElement.matches(':focus-visible')`);
    const focused = await measure();
    key("Enter"); await wait(drawerOpen); await settled();
    key("Enter"); await wait(`!(${drawerOpen})`); await settled();

    // The wide layout restores its docked preference. A hover preview there
    // pins on click, rather than using the compact drawer's close action.
    win.setContentSize(1100, 820);
    await wait(`!document.querySelector('.app-shell').classList.contains('compact-navigation') && !(${collapsed})`);
    await settled();
    click(await point()); await wait(collapsed); await settled();
    away(); win.webContents.sendInputEvent({ type: "mouseMove", ...await point() });
    await wait(drawerOpen); await settled();
    const wide = await measure();
    assert(wide.hitsButton && wide.pressed === "true", JSON.stringify(wide));
    click(await point()); await wait(`!(${collapsed})`); await settled();
    report.push({ theme, font, before, hover, focused, wide });
    away();
  }
  assert.equal(errors.length, 0, errors.join("\n"));
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ report, errors }, null, 2));
  console.log("PASS: main sidebar hover/click/focus, compact drawer, wide pinning and breakpoint restoration");
  win.destroy(); app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
