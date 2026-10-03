// Exercise real lazy imports and dependency-cache isolation with synthetic
// workspace data. Never open the installed app profile or start a Go server.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { once } = require("node:events");

const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/e2e/workspace-preview");

if (!process.versions.electron) {
  (async () => {
    const { createServer, loadConfigFromFile } = await import("vite");
    const { spawn } = require("node:child_process");
    fs.mkdirSync(output, { recursive: true });
    const run = fs.mkdtempSync(path.join(output, "run-"));
    const reviewRoot = path.join(run, "review");
    fs.mkdirSync(reviewRoot);
    fs.writeFileSync(path.join(reviewRoot, "index.html"), '<div id="root"></div><script type="module" src="./fixture.tsx"></script>');
    fs.writeFileSync(path.join(reviewRoot, "fixture.tsx"), `
      import { createRoot } from "react-dom/client";
      import { I18nProvider } from "/src/renderer/i18n";
      import { WuuUIRoot } from "/src/renderer/ui/layers/UILayerHost";
      import { WorkspaceReviewPanel } from "/src/renderer/WorkspaceReviewPanels";
      import { applyMessageFlowFontSize } from "/src/renderer/MessageFlowFontSizeSection";
      import "/src/renderer/styles.css";
      const query = new URLSearchParams(location.search);
      document.documentElement.dataset.theme = query.get("theme") || "light";
      document.documentElement.dataset.platform = "darwin";
      applyMessageFlowFontSize(Number(query.get("size")) || 14);
      const files = [
        { path: "one.ts", status: "modified", additions: 1, deletions: 1 },
        { path: "lib/two.ts", status: "modified", additions: 1, deletions: 1 },
      ];
      const status = { is_repo: true, branch: "preview", dirty_count: 2, diff: { files: 2, additions: 2, deletions: 2 } };
      Object.defineProperty(window, "wuu", { value: {
        initialLanguagePreference: "en-US",
        listGitChanges: async () => ({ is_repo: true, root: "/synthetic", files }),
        readGitFileDiff: async (path) => ({ is_repo: true, path, status: "modified", additions: 1, deletions: 1, patch: "",
          original_text: "export const revision = 1;", modified_text: "export const revision = 2;", truncated: false }),
      } });
      createRoot(document.getElementById("root")).render(
        <I18nProvider><WuuUIRoot><div style={{width: Number(query.get("width")) || 900, height: "100vh"}}>
          <WorkspaceReviewPanel workspaceRoot="/synthetic" gitStatus={status} />
        </div></WuuUIRoot></I18nProvider>);
    `);
    const loaded = await loadConfigFromFile({ command: "serve", mode: "development" }, path.join(desktop, "electron.vite.config.ts"));
    const renderer = loaded.config.renderer;
    const defaultCache = path.join(desktop, "node_modules/.vite");
    const probeCache = path.join(run, "node_modules/.vite");
    const probeRoot = path.join(run, "competing-preview");
    fs.mkdirSync(probeRoot);
    fs.symlinkSync(path.join(desktop, "node_modules"), path.join(probeRoot, "node_modules"), "dir");
    fs.writeFileSync(path.join(probeRoot, "index.html"), '<script type="module" src="/probe.js"></script>');
    fs.writeFileSync(path.join(probeRoot, "probe.js"), 'import React from "react"; console.log(React.version);');
    const primary = await createServer({
      ...renderer, configFile: false, root: desktop, logLevel: "warn",
      // Preserve the production namespace relationship without touching the
      // running developer desktop's cache. Removing its namespace must fail.
      cacheDir: path.resolve(probeCache, path.relative(defaultCache, renderer.cacheDir ?? defaultCache)),
      server: { host: "127.0.0.1", port: 0, fs: { allow: [path.dirname(desktop)] } },
    });
    let competing;
    try {
      await primary.listen();
      const base = `http://127.0.0.1:${primary.httpServer.address().port}`;
      const source = await (await fetch(`${base}/src/renderer/WorkspaceMonacoDiffEditor.tsx`)).text();
      const dependency = source.match(/from "([^"\n]*monaco-editor[^"\n]*)"/)[1];
      assert.equal((await fetch(new URL(dependency, base))).status, 200);
      competing = await createServer({ configFile: false, root: probeRoot, cacheDir: probeCache, logLevel: "warn", server: { host: "127.0.0.1", port: 0 } });
      await competing.listen();
      const other = `http://127.0.0.1:${competing.httpServer.address().port}`;
      const probe = await (await fetch(`${other}/probe.js`)).text();
      const react = probe.match(/from "([^"\n]+)"/)[1];
      assert.equal((await fetch(new URL(react, other))).status, 200);
      assert.equal((await fetch(new URL(dependency, base))).status, 200, "another preview must not invalidate an already loaded desktop dependency");
      const env = { ...process.env, WUU_PREVIEW_E2E_URL: base, WUU_PREVIEW_E2E_REVIEW: `/${path.relative(desktop, path.join(reviewRoot, "index.html")).split(path.sep).join("/")}`, WUU_PREVIEW_E2E_OUTPUT: run, WUU_HOME: path.join(run, "wuu-home") };
      delete env.ELECTRON_RUN_AS_NODE;
      const child = spawn(require("electron"), [__filename], { env, stdio: "inherit" });
      const timeout = setTimeout(() => child.kill("SIGTERM"), 120_000);
      const [code] = await once(child, "exit");
      clearTimeout(timeout);
      assert.equal(code, 0, "workspace preview browser probe failed");
      const evidence = JSON.parse(fs.readFileSync(path.join(run, "browser-result.json")));
      fs.writeFileSync(path.join(output, "result.json"), JSON.stringify({ cacheIsolation: "passed", run, ...evidence }, null, 2));
      console.log(`Workspace preview E2E passed. Evidence: ${path.join(output, "result.json")}`);
    } finally {
      await competing?.close();
      await primary.close();
    }
  })().catch((error) => { console.error(error); process.exitCode = 1; });
} else {
  const { app, BrowserWindow, session } = require("electron");
  const run = process.env.WUU_PREVIEW_E2E_OUTPUT;
  const base = process.env.WUU_PREVIEW_E2E_URL;
  const review = process.env.WUU_PREVIEW_E2E_REVIEW;
  app.setPath("userData", path.join(run, "profile"));
  app.on("window-all-closed", () => {});
  app.whenReady().then(async () => {
    const window = new BrowserWindow({ show: false, width: 1440, height: 1000, webPreferences: { partition: "workspace-preview-e2e", contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
    const logs = [];
    window.webContents.on("console-message", (event) => logs.push(event.message));
    const evaluate = (code) => window.webContents.executeJavaScript(code);
    const waitFor = (condition) => evaluate(`new Promise((resolve, reject) => {
      const check = () => { if (${condition}) { observer.disconnect(); clearTimeout(timeout); resolve(true); } };
      const observer = new MutationObserver(check);
      const timeout = setTimeout(() => { observer.disconnect(); reject(new Error(${JSON.stringify(condition)})); }, 30000);
      observer.observe(document.documentElement, { subtree: true, childList: true, attributes: true }); check();
    })`);
    const click = (selector) => evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);
    // Hidden windows do not reliably advance paint-driven transitions. Finish
    // finite effects before recording static surfaces; leave activity loops.
    const settle = () => evaluate(`document.getAnimations().filter(animation => animation.effect.getComputedTiming().iterations !== Infinity).forEach(animation => animation.finish())`);
    const measurements = [];
    try {
      await window.loadURL(`${base}${review}?width=420`);
      await waitFor('document.querySelector(".workspace-review-row")');
      // Cancel the real lazy module request, rather than throwing from a mock.
      session.fromPartition("workspace-preview-e2e").webRequest.onBeforeRequest({ urls: [`${base}/*WorkspaceMonacoDiffEditor*`] }, (_details, callback) => callback({ cancel: true }));
      await click(".workspace-review-row");
      await waitFor('document.querySelector("[data-wuu-action=reload-preview]")');
      assert.equal(await evaluate('Boolean(document.querySelector("[data-wuu-action=back]"))'), true);
      await click("[data-wuu-action=back]");
      await waitFor('document.querySelector(".workspace-review-row")');
      await click(".workspace-review-row");
      await waitFor('document.querySelector("[data-wuu-action=reload-preview]")');
      session.fromPartition("workspace-preview-e2e").webRequest.onBeforeRequest(null);
      const reloaded = once(window.webContents, "did-finish-load");
      await click("[data-wuu-action=reload-preview]");
      await reloaded;
      await waitFor('document.querySelector(".workspace-review-row")');
      await click(".workspace-review-row");
      await waitFor('document.querySelector(".workspace-monaco-diff-editor .monaco-editor .view-line")');
      for (const theme of ["light", "dark"]) {
        for (const size of [14, 20]) {
          await window.loadURL(`${base}${review}?theme=${theme}&size=${size}&width=900`);
          await waitFor('document.querySelector(".workspace-monaco-diff-editor .monaco-editor .view-line")');
          measurements.push({ view: "review", theme, size, rendered: await evaluate('document.querySelectorAll(".monaco-editor .view-line").length') });
          await window.loadURL(`${base}/dev/three-pane/index.html?theme=${theme}&size=${size}`);
          await waitFor('document.querySelectorAll(".workspace-tool-menu-item").length === 4');
          await settle();
          measurements.push({ view: "navigation", theme, size, surfaces: await evaluate(`Array.from(document.querySelectorAll(".thread-row.active, .workspace-tool-menu-item")).map(element => { const rect = element.getBoundingClientRect(); return { role: element.className, background: getComputedStyle(element).backgroundColor, width: rect.width, height: rect.height }; })`) });
          await window.loadURL(`${base}/dev/settings/index.html?theme=${theme}&size=${size}&page=appearance`);
          await waitFor('document.querySelector(".settings-nav-item.active")');
          await settle();
          measurements.push({ view: "settings", theme, size, surfaces: await evaluate(`Array.from(document.querySelectorAll(".settings-main, .settings-nav-item.active, .settings-group")).map(element => ({ role: element.className, background: getComputedStyle(element).backgroundColor }))`) });
        }
      }
      fs.writeFileSync(path.join(run, "browser-result.json"), JSON.stringify({ importFailureContained: true, navigationSurvived: true, reloadRecovered: true, measurements, logs }, null, 2));
    } finally {
      window.destroy();
    }
    app.quit();
  }).catch((error) => { console.error(error); app.exit(1); });
}
