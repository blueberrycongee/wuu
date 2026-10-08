const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "resize-e2e-preload.cjs");
const userData = path.join(desktopRoot, "out", "e2e", "document-composer-width-user-data");

process.env.WUU_RESIZE_E2E_CWD = repoRoot;
fs.rmSync(userData, { recursive: true, force: true });
fs.mkdirSync(userData, { recursive: true });
app.setPath("userData", userData);

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "Resize E2E preload is missing.");

  const win = new BrowserWindow({
    width: 1380,
    height: 860,
    show: process.env.WUU_E2E_VISIBLE === "true",
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
  await waitFor(win, () => Boolean(document.querySelector(".turn")), 5000);
  await openFilesTool(win);

  await waitFor(
    win,
    () =>
      Boolean(
        document
          .querySelector(".workspace-file-tree-frame file-tree-container")
          ?.shadowRoot?.querySelector("[data-item-path]")
      ),
    3000
  );
  await evaluate(win, () => {
    const row = document
      .querySelector(".workspace-file-tree-frame file-tree-container")
      ?.shadowRoot?.querySelector("[data-item-path]");
    if (!(row instanceof HTMLElement)) {
      throw new Error("Workspace file row not found.");
    }
    row.click();
  });
  await waitFor(
    win,
    () => Boolean(document.querySelector(".workspace-file-resource.active")),
    3000
  );

  await evaluate(win, () => {
    const expand = document.querySelector('[aria-label="展开为全面板"]');
    if (!(expand instanceof HTMLButtonElement)) {
      throw new Error("Full-panel expand button not found.");
    }
    expand.click();
  });
  await waitFor(
    win,
    () => document.querySelector(".app-shell")?.classList.contains("right-panel-globalized") || null,
    3000
  );
  await waitFor(
    win,
    () =>
      Boolean(
        document.querySelector(".workspace-document-composer .composer-frame") &&
          document.querySelector(".workspace-document-turn-dock")
      ) || null,
    3000
  );

  const geometry = await evaluate(win, () => {
    const composerFrame = document.querySelector(
      ".workspace-document-composer .composer-frame"
    );
    const turnDock = document.querySelector(".workspace-document-turn-dock");
    if (!(composerFrame instanceof HTMLElement) || !(turnDock instanceof HTMLElement)) {
      throw new Error("Document composer geometry targets not found.");
    }
    const composerRect = composerFrame.getBoundingClientRect();
    const turnDockRect = turnDock.getBoundingClientRect();
    return {
      composerWidth: composerRect.width,
      composerCenter: composerRect.left + composerRect.width / 2,
      turnDockWidth: turnDockRect.width,
      turnDockCenter: turnDockRect.left + turnDockRect.width / 2
    };
  });

  assert.ok(
    Math.abs(geometry.composerWidth - geometry.turnDockWidth) <= 1,
    `Document composer should fill its width-capped dock: ${JSON.stringify(geometry)}`
  );
  assert.ok(
    Math.abs(geometry.composerCenter - geometry.turnDockCenter) <= 1,
    `Document composer and its dock should stay centered together: ${JSON.stringify(geometry)}`
  );

  console.log(`document composer geometry verified: ${JSON.stringify(geometry)}`);
  win.close();
  app.quit();
}

async function openFilesTool(win) {
  await evaluate(win, () => {
    const toggle = Array.from(document.querySelectorAll(".side-panel-toggle-button")).find(
      (candidate) => candidate.getAttribute("aria-label")?.includes("右侧栏")
    );
    const panel = document.querySelector(".workspace-right-panel");
    if (!(toggle instanceof HTMLButtonElement)) {
      throw new Error("Right panel toggle button not found.");
    }
    if (panel?.getAttribute("aria-hidden") !== "false") {
      toggle.click();
    }
  });
  await waitFor(
    win,
    () => document.querySelector(".workspace-right-panel")?.getAttribute("aria-hidden") === "false" || null,
    3000
  );
  await evaluate(win, () => {
    const picker = document.querySelector(".workspace-panel-add");
    if (picker instanceof HTMLButtonElement) {
      picker.click();
    }
  });
  await waitFor(
    win,
    () => Boolean(document.querySelector(".workspace-tool-menu-item")),
    3000
  );
  await evaluate(win, () => {
    const fileTool = Array.from(document.querySelectorAll(".workspace-tool-menu-item")).find(
      (candidate) => candidate.textContent?.includes("文件")
    );
    if (!(fileTool instanceof HTMLButtonElement)) {
      throw new Error("Right panel file tool button not found.");
    }
    fileTool.click();
  });
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
