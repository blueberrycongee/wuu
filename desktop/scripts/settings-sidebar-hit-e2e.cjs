const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "resize-e2e-preload.cjs");
const evidenceDir = path.join(desktopRoot, "out", "e2e");
const userData = path.join(evidenceDir, "settings-sidebar-hit-user-data");

process.env.WUU_RESIZE_E2E_CWD = repoRoot;
fs.rmSync(userData, { recursive: true, force: true });
fs.mkdirSync(userData, { recursive: true });
app.setPath("userData", userData);

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "E2E preload is missing.");

  const win = new BrowserWindow({
    width: 1180,
    height: 820,
    show: true,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload,
      sandbox: false
    }
  });

  win.webContents.on("render-process-gone", (_event, details) => {
    fail(new Error(`Renderer process exited: ${details.reason}`));
  });

  await loadFile(win, rendererHtml);
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);
  await evaluate(win, () => {
    const button = document.querySelector(".sidebar-account-trigger");
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Settings button not found.");
    }
    button.click();
  });
  await waitFor(win, () => Boolean(document.querySelector(".sidebar-account-menu")), 3000);
  await evaluate(win, () => {
    const button = document.querySelector('[data-settings-page="providers"]');
    if (!(button instanceof HTMLButtonElement)) throw new Error("Settings menu item not found.");
    button.click();
  });
  await waitFor(win, () => Boolean(document.querySelector(".settings-shell")), 3000);

  await evaluate(win, () => {
    const button = document.querySelector(".settings-titlebar .settings-sidebar-toggle");
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Settings sidebar toggle not found.");
    }
    button.click();
  });
  await waitFor(
    win,
    () => document.querySelector(".settings-shell")?.classList.contains("sidebar-collapsed") || null,
    3000
  );
  win.setSize(760, 820);
  await delay(300);

  const before = await toggleHitState(win);
  assert.equal(before.hitOwnedByToggle, true, `Toggle must own its center hit before drawer opens: ${JSON.stringify(before)}`);
  assert.equal(before.visible, true, `Toggle must be visible before drawer opens: ${JSON.stringify(before)}`);
  await capture(win, "settings-sidebar-hit-before.png");

  win.webContents.sendInputEvent({ type: "mouseMove", x: before.centerX, y: before.centerY });
  await waitFor(
    win,
    () => document.querySelector(".settings-shell")?.classList.contains("sidebar-drawer-open") || null,
    3000
  );

  const transformSamples = [];
  for (let index = 0; index < 8; index += 1) {
    transformSamples.push(await drawerTranslateX(win));
    await delay(35);
  }
  assert.ok(
    new Set(transformSamples.map((value) => Math.round(value))).size >= 3,
    `Drawer should animate through multiple transform positions: ${transformSamples.join(", ")}`
  );
  await delay(180);

  const after = await toggleHitState(win);
  assert.equal(after.hitOwnedByToggle, true, `Toggle must own its center hit after drawer opens: ${JSON.stringify(after)}`);
  assert.equal(after.visible, true, `Toggle must remain visible after drawer opens: ${JSON.stringify(after)}`);
  assert.equal(after.hovered, true, `The real Electron pointer must still hover the toggle: ${JSON.stringify(after)}`);
  const readability = await sidebarReadabilityState(win);
  assert.ok(readability.width >= 240, `Narrow-window drawer must stay readable: ${JSON.stringify(readability)}`);
  assert.equal(readability.backWhiteSpace, "nowrap", `Back label must stay on one line: ${JSON.stringify(readability)}`);
  assert.equal(readability.providerWhiteSpace, "nowrap", `Provider label must stay on one line: ${JSON.stringify(readability)}`);
  assert.equal(readability.backFullyVisible, true, `Back label should fit at the readable floor: ${JSON.stringify(readability)}`);
  assert.equal(readability.providerFullyVisible, true, `Provider label should fit at the readable floor: ${JSON.stringify(readability)}`);
  await capture(win, "settings-sidebar-hit-after.png");

  win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, x: after.centerX, y: after.centerY });
  win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, x: after.centerX, y: after.centerY });
  await waitFor(
    win,
    () => !document.querySelector(".settings-shell")?.classList.contains("sidebar-collapsed") || null,
    3000
  );

  console.log(JSON.stringify({ before, after, readability, transformSamples }));
  win.close();
  app.quit();
}

async function toggleHitState(win) {
  return evaluate(win, () => {
    const toggle = document.querySelector(".settings-titlebar .settings-sidebar-toggle");
    if (!(toggle instanceof HTMLButtonElement)) {
      throw new Error("Settings sidebar toggle not found.");
    }
    const rect = toggle.getBoundingClientRect();
    const centerX = Math.round(rect.left + rect.width / 2);
    const centerY = Math.round(rect.top + rect.height / 2);
    const hit = document.elementFromPoint(centerX, centerY);
    const style = getComputedStyle(toggle);
    return {
      centerX,
      centerY,
      hitOwnedByToggle: hit === toggle || toggle.contains(hit),
      hitTag: hit?.tagName ?? null,
      hovered: toggle.matches(":hover"),
      visible: rect.width > 0 && rect.height > 0 && style.visibility === "visible" && Number(style.opacity) > 0
    };
  });
}

async function drawerTranslateX(win) {
  return evaluate(win, () => {
    const drawer = document.querySelector(".settings-sidebar");
    if (!(drawer instanceof HTMLElement)) {
      throw new Error("Settings sidebar drawer not found.");
    }
    const transform = getComputedStyle(drawer).transform;
    return transform === "none" ? 0 : new DOMMatrixReadOnly(transform).m41;
  });
}

async function sidebarReadabilityState(win) {
  return evaluate(win, () => {
    const drawer = document.querySelector(".settings-sidebar");
    const backLabel = document.querySelector(".settings-back-button > span");
    const providerLabel = document.querySelector(".settings-nav-item > span");
    if (
      !(drawer instanceof HTMLElement) ||
      !(backLabel instanceof HTMLElement) ||
      !(providerLabel instanceof HTMLElement)
    ) {
      throw new Error("Settings sidebar readability targets not found.");
    }
    return {
      width: drawer.getBoundingClientRect().width,
      backWhiteSpace: getComputedStyle(backLabel).whiteSpace,
      providerWhiteSpace: getComputedStyle(providerLabel).whiteSpace,
      backFullyVisible: backLabel.scrollWidth <= backLabel.clientWidth,
      providerFullyVisible: providerLabel.scrollWidth <= providerLabel.clientWidth
    };
  });
}

async function capture(win, name) {
  const image = await win.webContents.capturePage();
  fs.mkdirSync(evidenceDir, { recursive: true });
  fs.writeFileSync(path.join(evidenceDir, name), image.toPNG());
}

function loadFile(win, file) {
  return new Promise((resolve, reject) => {
    win.webContents.once("did-fail-load", (_event, _code, description) => reject(new Error(description)));
    win.webContents.once("did-finish-load", resolve);
    win.loadFile(file);
  });
}

async function waitFor(win, predicate, timeoutMs) {
  const started = Date.now();
  let lastValue;
  while (Date.now() - started < timeoutMs) {
    lastValue = await evaluate(win, predicate);
    if (lastValue) {
      return lastValue;
    }
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

function evaluate(win, fn) {
  return win.webContents.executeJavaScript(`(${fn.toString()})()`, true);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fail(error) {
  console.error(error);
  app.exit(1);
}
