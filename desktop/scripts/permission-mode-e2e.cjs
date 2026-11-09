const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "permission-mode-e2e-preload.cjs");
const evidenceDir = path.join(desktopRoot, "out", "e2e");
const consoleErrors = [];

process.env.WUU_PERMISSION_E2E_CWD = repoRoot;
app.commandLine.appendSwitch("disable-gpu");
app.commandLine.appendSwitch("disable-software-rasterizer");

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "Permission mode E2E preload is missing.");
  fs.mkdirSync(evidenceDir, { recursive: true });

  const win = new BrowserWindow({
    width: 1100,
    height: 820,
    show: false,
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
  win.webContents.on("console-message", (_event, level, message, line, sourceId) => {
    if (level >= 2) {
      consoleErrors.push(`${message} (${sourceId}:${line})`);
    }
  });

  await loadFile(win, rendererHtml);
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);
  await waitFor(win, () => Boolean(document.querySelector(".permission-chip")), 5000);

  const initial = await waitFor(win, permissionState, 3000);
  assert.equal(initial.chipText, "标准", "Initial agent permission mode should render as the standard permission mode.");
  assert.equal(initial.chipDisabled, false, "Permission mode chip should be clickable when no turn is running.");
  await capture(win, "permission-mode-initial.png");

  await openPermissionMenu(win);
  const openState = await waitFor(win, permissionStateWithOpenMenu, 3000);
  assert.equal(openState.optionLabels.length, 3, "Permission menu should expose the three current permission modes.");
  assert.deepEqual(openState.optionLabels, ["工作区内完全信任", "只读", "无边界"]);
  assert.equal(openState.checkedLabels.join(","), "工作区内完全信任", "Standard mode should be marked as the current mode.");
  assert.ok(
    openState.menuRect.width >= 170 && openState.menuRect.width <= 180,
    `Permission menu should keep the compact preset width. Width=${openState.menuRect.width}`
  );
  assert.ok(
    openState.optionRects.every((rect) => rect.height <= 34),
    `Permission mode options should stay single-line and compact. Heights=${openState.optionRects.map((rect) => rect.height).join(",")}`
  );
  assert.ok(
    openState.optionRects.every((rect, index, rects) => index === 0 || rect.top >= rects[index - 1].top + rects[index - 1].height),
    "Permission mode options should be vertically ordered without overlap."
  );
  assert.ok(
    openState.optionRects.every((rect, index, rects) => index === 0 || rect.top - (rects[index - 1].top + rects[index - 1].height) <= 4),
    "Permission mode rows should keep compact vertical spacing."
  );
  await capture(win, "permission-mode-menu-open.png");

  await chooseMode(win, "只读", "只读", 1);
  await chooseMode(win, "无边界", "无边界", 2);
  await chooseMode(win, "工作区内完全信任", "标准", 3);

  const finalState = await waitFor(win, permissionState, 3000);
  assert.equal(finalState.chipText, "标准", "Returning to standard mode should update the chip label.");
  assert.deepEqual(finalState.updatePermissionModes, ["read_only", "unconfined", "standard"]);
  assert.equal(finalState.menuOpen, false, "Permission menu should close after selection.");
  await capture(win, "permission-mode-final.png");

  assert.deepEqual(consoleErrors, [], "Renderer should not log console errors during permission mode e2e.");
  console.log("permission mode e2e passed");
  console.log(`screenshots: ${path.join(evidenceDir, "permission-mode-menu-open.png")}`);
  app.exit(0);
}

async function openPermissionMenu(win) {
  const alreadyOpen = await evaluate(win, () => Boolean(document.querySelector('[data-floating-menu-owner="composer-access"] .access-menu')));
  if (alreadyOpen) {
    return;
  }
  await evaluate(win, () => {
    const chip = document.querySelector(".permission-chip");
    if (!(chip instanceof HTMLButtonElement)) {
      throw new Error("Permission mode chip not found.");
    }
    chip.click();
  });
  await waitFor(win, permissionStateWithOpenMenu, 3000);
}

async function chooseMode(win, label, expectedChipText, expectedUpdateCount) {
  await openPermissionMenu(win);
  await evaluate(
    win,
    (targetLabel) => {
      const options = Array.from(document.querySelectorAll('button[role="menuitemradio"]'));
      const option = options.find((button) => button.querySelector("strong")?.textContent?.trim() === targetLabel);
      if (!(option instanceof HTMLButtonElement)) {
        throw new Error(`Permission mode option ${targetLabel} not found.`);
      }
      option.click();
    },
    label
  );
  const updated = await waitFor(
    win,
    () => {
      const state = window.permissionE2E.state();
      const chipText = document.querySelector(".permission-chip span")?.textContent?.trim() ?? "";
      return chipText === window.__EXPECTED_CHIP__ && state.updateCalls.length === __ARG__.expectedUpdateCount
        ? permissionState()
        : null;
    },
    3000,
    { expectedChipText, expectedUpdateCount }
  );
  assert.equal(updated.chipText, expectedChipText, `${label} mode should update the visible chip.`);
}

function permissionStateWithOpenMenu() {
  const state = permissionState();
  return state.menuOpen && state.menuVisibility === "visible" ? state : null;
}

function permissionState() {
  const chip = document.querySelector(".permission-chip");
  const menuLayer = document.querySelector('[data-floating-menu-owner="composer-access"]');
  const menu = menuLayer?.querySelector(".access-menu") ?? null;
  const options = Array.from(document.querySelectorAll('button[role="menuitemradio"]'));
  const labels = options.map((button) => button.querySelector("strong")?.textContent?.trim() ?? "");
  const checkedLabels = options
    .filter((button) => button.getAttribute("aria-checked") === "true")
    .map((button) => button.querySelector("strong")?.textContent?.trim() ?? "");
  const updateCalls = window.permissionE2E.state().updateCalls;
  return {
    chipText: chip?.querySelector("span")?.textContent?.trim() ?? "",
    chipDisabled: chip instanceof HTMLButtonElement ? chip.disabled : true,
    menuOpen: Boolean(menu),
    menuVisibility: menuLayer ? getComputedStyle(menuLayer).visibility : "",
    menuRect: rectFor(menu),
    optionLabels: labels,
    checkedLabels,
    optionRects: options.map(rectFor),
    updatePermissionModes: updateCalls.map((call) => call.permissionMode)
  };
}

function rectFor(element) {
  if (!element) {
    return { left: 0, top: 0, width: 0, height: 0 };
  }
  const rect = element.getBoundingClientRect();
  return {
    left: Math.round(rect.left),
    top: Math.round(rect.top),
    width: Math.round(rect.width),
    height: Math.round(rect.height)
  };
}

async function capture(win, name) {
  await waitForPaint(win);
  await delay(180);
  const image = await win.webContents.capturePage();
  fs.writeFileSync(path.join(evidenceDir, name), image.toPNG());
}

async function waitForPaint(win) {
  await win.webContents.executeJavaScript(
    "new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))",
    true
  );
}

function loadFile(win, file) {
  return new Promise((resolve, reject) => {
    win.webContents.once("did-fail-load", (_event, _code, description) => reject(new Error(description)));
    win.webContents.once("did-finish-load", () => resolve());
    win.loadFile(file);
  });
}

async function waitFor(win, predicate, timeoutMs, options = {}) {
  const started = Date.now();
  let lastValue;
  while (Date.now() - started < timeoutMs) {
    lastValue = await evaluate(win, predicate, options);
    if (lastValue) {
      return lastValue;
    }
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

async function evaluate(win, fn, options = {}) {
  const source = `(() => {
    window.__EXPECTED_CHIP__ = ${JSON.stringify(options.expectedChipText ?? "")};
    window.__EXPECTED_PERMISSION_MODE__ = ${JSON.stringify(options.expectedPermissionMode ?? "")};
    const __ARG__ = ${JSON.stringify(options)};
    const rectFor = ${rectFor.toString()};
    const permissionState = ${permissionState.toString()};
    const permissionStateWithOpenMenu = ${permissionStateWithOpenMenu.toString()};
    return (${fn.toString()})(__ARG__);
  })()`;
  return win.webContents.executeJavaScript(source, true);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fail(error) {
  console.error(error);
  app.exit(1);
}
