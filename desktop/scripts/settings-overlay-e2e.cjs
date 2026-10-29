// Shipping shell + scoped desktop service + real native overlay transport.
// Backend data is synthetic; this does not require a core, account, or model.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow, WebContentsView, ipcMain, protocol } = require("electron");
const esbuild = require("esbuild");
const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_SETTINGS_OVERLAY_OUTPUT || path.join(desktop, "out/e2e/settings-overlay"));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-settings-overlay-"));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(fixture, "profile"));
process.env.WUU_RESIZE_E2E_CWD = path.dirname(desktop);
process.env.WUU_HEIGHT_RUNNING_E2E = "1";
app.enableSandbox();
esbuild.buildSync({ entryPoints: [path.join(desktop, "src/main/chromeOverlayHost.ts")],
  outfile: path.join(fixture, "host.cjs"), bundle: true, platform: "node", external: ["electron"] });
const { ChromeOverlayHost, chromeOverlayScheme } = require(path.join(fixture, "host.cjs"));
protocol.registerSchemesAsPrivileged([chromeOverlayScheme]);
const preload = fs.readFileSync(path.join(__dirname, "resize-e2e-preload.cjs"), "utf8").replace(
  'contextBridge.exposeInMainWorld("wuu", extend(withDefaultShell(desktopAPI)));',
  `desktopAPI.chromeOverlay = {
    show: request => ipcRenderer.invoke("wuu:chrome-overlay-show", request),
    hide: (token, generation) => ipcRenderer.invoke("wuu:chrome-overlay-hide", token, generation),
    onEvent: listener => { const receive = (_event, value) => listener(value);
      ipcRenderer.on("wuu:chrome-overlay-event", receive);
      return () => ipcRenderer.removeListener("wuu:chrome-overlay-event", receive); },
  };
  contextBridge.exposeInMainWorld("wuu", withDefaultShell(desktopAPI));`,
);
esbuild.buildSync({ stdin: { contents: preload, resolveDir: __dirname }, outfile: path.join(fixture, "preload.cjs"),
  bundle: true, platform: "node", external: ["electron"] });
const report = { status: "running", platform: process.platform, versions: process.versions, cases: [],
  limitations: ["Synthetic backend, no inference or account access.", "Captures record shell and native menu separately; not a composed OS screenshot."] };
const save = () => fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
const trace = value => fs.appendFileSync(path.join(output, "events.jsonl"), JSON.stringify({ at: Date.now(), ...value }) + "\n");
let win, host, page, dropPaint = false;
const failures = [];
app.on("web-contents-created", (_event, contents) => {
  contents.on("console-message", details => trace({ kind: "console", id: contents.id, message: details.message }));
  contents.on("did-fail-load", (_event, code, description, url) => trace({ kind: "load-failed", code, description, url }));
  contents.on("preload-error", (_event, file, error) => trace({ kind: "preload-error", file, error: String(error) }));
});
async function until(check, label) {
  const deadline = Date.now() + 12000;
  while (Date.now() < deadline) { const result = await check(); if (result) return result; await new Promise(resolve => setTimeout(resolve, 20)); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (contents, fn, value) => contents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`, true);
const menuView = () => win.contentView.children.find(view => view.webContents?.getURL().endsWith("/chrome-overlay.html?surface=account"));
function key(contents, keyCode) {
  contents.sendInputEvent({ type: "keyDown", keyCode });
  contents.sendInputEvent({ type: "keyUp", keyCode });
}
async function click(contents, selector) {
  const point = await until(() => evaluate(contents, async selector => {
    const measure = () => {
      const element = document.querySelector(selector);
      if (!element || element.disabled || element.closest("[inert]")) return null;
      const rect = element.getBoundingClientRect();
      const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2;
      return rect.width > 0 && rect.height > 0 && element.contains(document.elementFromPoint(x, y))
        ? { x, y, width: rect.width, height: rect.height } : null;
    };
    const before = measure();
    if (!before) return null;
    // Font/viewport changes schedule composer measurement on the next frame.
    // Sample after that layout, rather than clicking its previous coordinates.
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const after = measure();
    return after && Object.keys(before).every(key => before[key] === after[key]) ? after : null;
  }, selector), `stable hit-testable ${selector}`);
  const zoom = contents.getZoomFactor();
  const position = { x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) };
  trace({ kind: "click", selector, point, zoom });
  contents.sendInputEvent({ type: "mouseMove", ...position });
  contents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...position });
  contents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...position });
}
async function focusedComposer() {
  await until(() => evaluate(win.webContents, () => {
    const input = document.querySelector("textarea");
    return input && !input.disabled && !input.closest("[inert]") && document.hasFocus() && document.activeElement === input;
  }), "native click focuses the composer");
}
async function openMenu() {
  await click(win.webContents, ".sidebar-account-trigger");
  const view = await until(() => { const view = menuView(); return view?.getVisible() && view.webContents.isFocused() && view; }, "native menu painted and focused");
  const bounds = view.getBounds(), [width, height] = win.getContentSize();
  assert.ok(bounds.width > 0 && bounds.height > 0 && bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= width && bounds.y + bounds.height <= height,
    `Menu is within the window: ${JSON.stringify(bounds)}`);
  assert.equal(win.contentView.children.at(-1), view, "Presented native menu is above shell and browser views");
  const rows = await evaluate(view.webContents, () => [...document.querySelectorAll('[role="menuitem"]')].map(element => {
    const r = element.getBoundingClientRect();
    return { text: element.textContent, bounds: r.toJSON(), hit: element.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)) };
  }));
  assert.ok(rows.length >= 2 && rows.every(row => row.hit), `Every menu item is actually hit-testable: ${JSON.stringify(rows)}`);
  return { view, bounds, rows };
}
async function capture(name, view) {
  fs.writeFileSync(path.join(output, `${name}-shell.png`), (await win.webContents.capturePage()).toPNG());
  if (view) {
    const image = await view.webContents.capturePage();
    fs.writeFileSync(path.join(output, `${name}-menu.png`), image.toPNG());
    const pixels = image.toBitmap();
    let minimum = 255, maximum = 0, painted = 0;
    for (let index = 0; index < pixels.length; index += 4) {
      if (pixels[index + 3] < 128) continue;
      painted++;
      const brightness = (pixels[index] + pixels[index + 1] + pixels[index + 2]) / 3;
      minimum = Math.min(minimum, brightness); maximum = Math.max(maximum, brightness);
    }
    assert.ok(!image.isEmpty() && painted > 0 && maximum - minimum > 40,
      "The native menu must paint actual foreground and background pixels, not just acknowledge a DOM update");
  }
}
async function closed() {
  await until(async () => !menuView()?.getVisible() && await evaluate(win.webContents, () => document.querySelector(".sidebar-account-trigger")?.getAttribute("aria-expanded") === "false"), "menu dismissed in native and renderer state");
}
async function run() {
  await app.whenReady();
  host = new ChromeOverlayHost((owner, event) => {
    trace({ kind: "event", ...event });
    if (event.command.type === "dismiss" && event.command.reason === "failed") failures.push(event);
    owner.webContents.send("wuu:chrome-overlay-event", event);
  },
    path.join(desktop, "out/renderer"), path.join(desktop, "out/preload/chromeOverlay.cjs"));
  ipcMain.handle("wuu:chrome-overlay-show", (_event, request) => {
    trace({ kind: "show", token: request.token, generation: request.generation, bounds: request.bounds });
    return host.show(win, request);
  });
  ipcMain.handle("wuu:chrome-overlay-hide", (_event, token, generation) => host.hide(win, token, generation));
  ipcMain.handle("wuu:chrome-overlay-ready", event => { trace({ kind: "ready", id: event.sender.id }); return host.ready(event); });
  ipcMain.on("wuu:chrome-overlay-painted", (event, token, generation) => {
    trace({ kind: "painted", token, generation, dropped: dropPaint });
    if (!dropPaint) host.painted(event, token, generation);
  });
  ipcMain.on("wuu:chrome-overlay-command", (event, command) => host.command(event, command));
  win = new BrowserWindow({ width: 1180, height: 820, show: true,
    webPreferences: { preload: path.join(fixture, "preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: true } });
  for (const event of ["focus", "blur"]) win.on(event, () => trace({ kind: "window", event }));
  for (const { width, font, theme } of [{ width: 1180, font: 15, theme: "light" }, { width: 760, font: 20, theme: "dark" }]) {
    win.setContentSize(1180, 820);
    await win.loadFile(path.join(desktop, "out/renderer/index.html"));
    win.show(); win.focus();
    await until(() => evaluate(win.webContents, () => Boolean(document.querySelector("textarea") && document.querySelector(".sidebar-account-trigger:not(:disabled)"))), "shipping default shell initialized");
    await evaluate(win.webContents, () => document.fonts.ready.then(() => undefined));
    if (await evaluate(win.webContents, () => document.querySelector(".app-shell")?.classList.contains("sidebar-collapsed"))) {
      await click(win.webContents, ".section-sidebar-toggle");
    }
    await click(win.webContents, ".thread-row-main");
    await until(() => evaluate(win.webContents, () => Boolean(document.querySelector('.cached-conversation-pane[data-active="true"][data-thread-id="resize-thread"] [data-turn-status="in_progress"]'))), "synthetic running turn selected");
    win.setContentSize(width, 820);
    await evaluate(win.webContents, ({ font, theme }) => {
      const root = document.documentElement;
      root.style.setProperty("--conversation-message-font-size", `${font}px`);
      root.style.setProperty("--appearance-scale", String(font / 14));
      root.dataset.theme = theme;
    }, { font, theme });
    const name = `${width}-${font}-${theme}`;
    const scenario = { name, checks: [] }; report.cases.push(scenario); save();
    await click(win.webContents, "textarea");
    await focusedComposer();
    if (await evaluate(win.webContents, () => document.querySelector("textarea").value.length > 0)) {
      win.webContents.selectAll();
      await until(() => evaluate(win.webContents, () => {
        const input = document.querySelector("textarea");
        return document.activeElement === input && input.selectionStart === 0 && input.selectionEnd === input.value.length;
      }), "native Select All reaches the focused draft");
      key(win.webContents, "Backspace");
      // A real value change acknowledges deletion. An already-empty input
      // would let the wait pass before the native key event was delivered.
      await until(() => evaluate(win.webContents, () => {
        const input = document.querySelector("textarea");
        return document.activeElement === input && input.value === "";
      }), "empty focused draft before scenario");
    }
    const draft = `Retain this draft through Settings: ${name}`;
    await win.webContents.insertText(draft);
    await until(() => evaluate(win.webContents, draft => document.querySelector("textarea")?.value === draft, draft), "scenario draft entered");
    const opened = await openMenu(); scenario.menu = { bounds: opened.bounds, rows: opened.rows };
    await capture(name, opened.view);
    key(opened.view.webContents, "Escape"); await closed();
    assert.ok(await evaluate(win.webContents, () => document.activeElement?.matches(".sidebar-account-trigger")), "Escape restores trigger focus");
    await openMenu(); await click(win.webContents, "textarea"); await closed(); await focusedComposer();
    assert.ok(await evaluate(win.webContents, () => document.activeElement?.tagName === "TEXTAREA"), "Outside click retains composer focus");
    const settingsMenu = await openMenu();
    await click(settingsMenu.view.webContents, '[data-settings-page="providers"]'); await closed();
    await until(() => evaluate(win.webContents, () => {
      const e = document.querySelector(".settings-main"), r = e?.getBoundingClientRect();
      return r?.width > 0 && r?.height > 0 && e.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2));
    }), "Settings page is visible and above the conversation");
    await capture(`${name}-settings`);
    await click(win.webContents, '[data-ribbon-id="conversations"]');
    await until(() => evaluate(win.webContents, draft => !document.querySelector(".settings-shell") && document.querySelector("textarea")?.value === draft, draft), "Settings closes without replacing draft or active turn");
    assert.ok(await evaluate(win.webContents, () => Boolean(document.querySelector('.cached-conversation-pane[data-active="true"][data-thread-id="resize-thread"] [data-turn-status="in_progress"]'))), "Running conversation remains active after Settings");
    scenario.checks.push("Real native menu paint, bounds, stacking and item hit tests", "Escape focus restoration, outside dismissal and reopen", "Settings navigation and exact draft preservation");
    scenario.status = "passed"; save();
  }
  // Reproduce a loaded renderer that never acknowledges its first presentation.
  // Only transport delivery is interrupted; the shipping UI and host are intact.
  dropPaint = true;
  await click(win.webContents, ".sidebar-account-trigger");
  const stalled = await until(() => menuView(), "unpresented native opening");
  const stalledContents = stalled.webContents;
  await until(() => failures.length === 1, "missing first paint reports failure within the opening budget");
  await closed();
  assert.ok(stalledContents.isDestroyed(), "An unresponsive renderer is retired rather than reused forever");
  dropPaint = false;
  const recovered = await openMenu();
  assert.notEqual(recovered.view, stalled, "A new user click gets a fresh native renderer");
  await capture("recovered-opening", recovered.view);
  key(recovered.view.webContents, "Escape"); await closed();
  report.recovery = "Lost first-paint acknowledgement fails closed; the next user opening paints and accepts input";
  save();
  // Native browser views must not cover chrome or be hidden/resized to reveal it.
  page = new WebContentsView({ webPreferences: { sandbox: true } });
  const pageBounds = { x: 52, y: 80, width: 600, height: 650 };
  page.setBounds(pageBounds); win.contentView.addChildView(page);
  await page.webContents.loadURL("data:text/html,<input aria-label='Browser draft' value='Preserve browser state'>");
  host.prepare(win);
  const browserMenu = await openMenu(); await capture("browser-overlap", browserMenu.view);
  assert.ok(page.getVisible()); assert.deepEqual(page.getBounds(), pageBounds);
  key(browserMenu.view.webContents, "Escape"); await closed();
  assert.ok(page.getVisible()); assert.deepEqual(page.getBounds(), pageBounds);
  assert.equal(await evaluate(page.webContents, () => document.querySelector("input").value), "Preserve browser state");
  report.status = "passed"; save();
  console.log(`PASS: native Settings overlay visibility and interaction. Evidence: ${output}`);
}
run().then(() => finish(0), async error => {
  report.status = "failed"; report.error = String(error.stack || error); save();
  if (win && !win.isDestroyed()) {
    report.surfaces = win.contentView.children.map(view => ({ url: view.webContents?.getURL(), visible: view.getVisible(), bounds: view.getBounds() }));
    report.input = await evaluate(win.webContents, () => ({
      documentFocused: document.hasFocus(),
      activeElement: { tag: document.activeElement?.tagName, className: document.activeElement?.className,
        role: document.activeElement?.getAttribute("role") },
      composers: [...document.querySelectorAll("textarea")].map(input => ({
        value: input.value, disabled: input.disabled, inert: Boolean(input.closest("[inert]")),
        focused: document.activeElement === input, selection: [input.selectionStart, input.selectionEnd],
        bounds: input.getBoundingClientRect().toJSON(),
      })),
    }));
    save();
    await capture("failure", menuView()).catch(() => {});
  }
  console.error(error); finish(1);
});
function finish(code) {
  if (page && !page.webContents.isDestroyed()) page.webContents.close();
  if (win && !win.isDestroyed()) win.destroy();
  fs.rmSync(fixture, { recursive: true, force: true });
  app.exit(code);
}
