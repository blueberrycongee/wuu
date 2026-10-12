// Exercise real lazy imports and dependency-cache isolation with synthetic
// workspace data. Never open the installed app profile or start a Go server.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { once } = require("node:events");

const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/e2e/workspace-preview");

// Minimal deterministic PDF bytes, served locally to the real PDF.js worker.
function pdfFixture(label = "Retry fixture") {
  const objects = ["<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 2 /Kids [4 0 R 6 0 R] >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"];
  for (const page of [1, 2]) {
    const content = `BT /F1 16 Tf 28 200 Td (${label} page ${page}) Tj ET`;
    objects.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 260] /Resources << /Font << /F1 3 0 R >> >> /Contents ${page * 2 + 3} 0 R >>`,
      `<< /Length ${Buffer.byteLength(content)} >>\nstream\n${content}\nendstream`);
  }
  let text = "%PDF-1.4\n";
  const offsets = [0];
  objects.forEach((object, i) => { offsets.push(Buffer.byteLength(text)); text += `${i + 1} 0 obj\n${object}\nendobj\n`; });
  const xref = Buffer.byteLength(text);
  text += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.slice(1).map(n => `${String(n).padStart(10, "0")} 00000 n \n`).join("")}trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(text);
}

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
    const pdfRoot = path.join(run, "pdf");
    fs.mkdirSync(pdfRoot);
    fs.writeFileSync(path.join(pdfRoot, "index.html"), '<div id="root"></div><script type="module" src="./fixture.tsx"></script>');
    fs.writeFileSync(path.join(pdfRoot, "fixture.tsx"), `
      import { useState } from "react";
      import { createRoot } from "react-dom/client";
      import { I18nProvider } from "/src/renderer/i18n";
      import { WuuUIRoot } from "/src/renderer/ui/layers/UILayerHost";
      import { WorkspaceFilePreview } from "/src/renderer/WorkspaceFiles";
      import { FileSelectionProvider } from "/src/renderer/FileSelectionContext";
      import { readCollapsedPromptParts } from "/src/renderer/ComposerCollapsedPrompt";
      import { applyMessageFlowFontSize } from "/src/renderer/MessageFlowFontSizeSection";
      import "/src/renderer/styles.css";
      const query = new URLSearchParams(location.search);
      document.documentElement.dataset.theme = query.get("theme") || "light";
      document.documentElement.dataset.platform = "darwin";
      applyMessageFlowFontSize(Number(query.get("size")) || 14);
      const context = { cwd: "/synthetic", thread_id: "pdf-retry" };
      window.pdfReads = [];
      window.pdfParts = [];
      Object.defineProperty(window, "wuu", { value: {
        initialLanguagePreference: "en-US",
        readWorkspaceFile: async (path) => {
          window.pdfReads.push(path);
          const count = window.pdfReads.filter(value => value === path).length;
          if (path === "metadata.pdf" && count === 2) throw new Error("Metadata temporarily unavailable");
          if (path.startsWith("metadata-") && count === 2) {
            await fetch("/__pdf__/metadata?name=" + path);
            window.dispatchEvent(new CustomEvent("pdf-metadata-settled", { detail: path }));
          }
          const changed = path === "changed.pdf" && count > 1;
          return { path, binary: true, size_bytes: 1000, mtime_ms: changed ? 8 : 7, sha256: (changed ? "b" : "a").repeat(64),
            renderable_kind: "pdf", renderable_url: location.origin + "/__pdf__/" + path };
        },
      } });
      function Fixture() {
        const [selected, setSelected] = useState();
        const [draft, setDraft] = useState("Keep this conversation draft");
        return <FileSelectionProvider ownerKey="pdf-retry" getPrompt={() => draft}
          setPrompt={value => { setDraft(value); window.pdfParts = readCollapsedPromptParts("pdf-retry", value); }}
          onEdit={async () => false} onOpenFile={setSelected}>
          <div style={{display: "grid", gridTemplateColumns: "minmax(180px, 1fr) minmax(0, 1fr)", height: "100vh"}}>
            <section aria-label="Conversation"><textarea aria-label="Draft" value={draft} onChange={event => setDraft(event.target.value)} />
              {["transient", "corrupt", "stale", "close", "replacement", "changed", "metadata", "metadata-stale", "metadata-close"].map(name =>
                <button key={name} data-open-pdf={name} onClick={() => setSelected(name + ".pdf")}>{name}.pdf</button>)}
              <button data-close-pdf onClick={() => setSelected(undefined)}>Close preview</button>
            </section>
            <aside aria-label="File preview" style={{minWidth: 0, minHeight: 0}}>
              {selected ? <WorkspaceFilePreview activeContext={context} selectedFilePath={selected} anchor="pdf-page=2" /> : null}
            </aside>
          </div>
        </FileSelectionProvider>;
      }
      createRoot(document.getElementById("root")).render(<I18nProvider><WuuUIRoot><Fixture /></WuuUIRoot></I18nProvider>);
    `);
    const pdfRequests = [];
    const pendingPDFs = new Map();
    const requestWaiters = [];
    const pdfBytes = pdfFixture();
    const changedPDFBytes = pdfFixture("Updated fixture");
    const pdfServer = {
      name: "synthetic-pdf-recovery",
      configureServer(server) {
        server.middlewares.use((request, response, next) => {
          const url = new URL(request.url, "http://fixture");
          if (!url.pathname.startsWith("/__pdf__/")) return next();
          response.setHeader("Cache-Control", "no-store");
          if (url.pathname === "/__pdf__/requests") return response.end(JSON.stringify(pdfRequests));
          if (url.pathname === "/__pdf__/wait") {
            const observed = () => pdfRequests.filter(name => name === url.searchParams.get("name")).length >= Number(url.searchParams.get("count"));
            if (observed()) return response.end("observed");
            requestWaiters.push(() => { if (observed() && !response.writableEnded) response.end("observed"); });
            return;
          }
          if (url.pathname === "/__pdf__/release") {
            const name = url.searchParams.get("name");
            pendingPDFs.get(name)?.(); pendingPDFs.delete(name);
            return response.end("released");
          }
          if (url.pathname === "/__pdf__/metadata") {
            const name = "metadata:" + url.searchParams.get("name");
            pdfRequests.push(name);
            for (const resolve of requestWaiters) resolve();
            pendingPDFs.set(name, () => { if (!response.destroyed) response.end("refreshed"); });
            return;
          }
          const name = url.pathname.slice("/__pdf__/".length);
          pdfRequests.push(name);
          for (const resolve of requestWaiters) resolve();
          const count = pdfRequests.filter(value => value === name).length;
          const sendPDF = () => {
            if (response.destroyed) return;
            response.setHeader("Content-Type", "application/pdf");
            const bytes = name === "changed.pdf" ? changedPDFBytes : pdfBytes;
            response.setHeader("Content-Length", bytes.length);
            response.end(bytes);
          };
          if (name === "corrupt.pdf") return response.end("corrupt PDF bytes");
          if (name !== "replacement.pdf" && count === 1) {
            response.statusCode = 503; return response.end("Temporary PDF failure");
          }
          if (name !== "replacement.pdf") pendingPDFs.set(name, sendPDF);
          else sendPDF();
        });
      },
    };
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
      ...renderer, plugins: [...renderer.plugins, pdfServer], configFile: false, root: desktop, logLevel: "warn",
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
      const env = { ...process.env, WUU_PREVIEW_E2E_PDF: `/${path.relative(desktop, path.join(pdfRoot, "index.html")).split(path.sep).join("/")}`, WUU_PREVIEW_E2E_URL: base, WUU_PREVIEW_E2E_REVIEW: `/${path.relative(desktop, path.join(reviewRoot, "index.html")).split(path.sep).join("/")}`, WUU_PREVIEW_E2E_OUTPUT: run, WUU_HOME: path.join(run, "wuu-home") };
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
      const roots = new Set();
      const check = () => {
        for (const host of document.querySelectorAll("*")) {
          if (host.shadowRoot && !roots.has(host.shadowRoot)) {
            roots.add(host.shadowRoot); observer.observe(host.shadowRoot, { subtree: true, childList: true, attributes: true });
          }
        }
        if (${condition}) { observer.disconnect(); clearTimeout(timeout); resolve(true); }
      };
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
      // Real WorkspaceFilePreview -> lazy PDF.js -> worker/network failures.
      // Only the workspace-read IPC is synthetic; no PDF.js APIs are mocked.
      await window.loadURL(`${base}${process.env.WUU_PREVIEW_E2E_PDF}`);
      const shadow = 'document.querySelector("[data-workspace-pdf-preview]")?.shadowRoot';
      const retry = `${shadow}?.querySelector("button.workspace-pdf-retry")`;
      const failed = () => waitFor(`${shadow}?.querySelector("[role=alert]") && !${retry}?.disabled`);
      const ready = () => waitFor(`${shadow}?.querySelector('.workspace-pdf-page-count')?.textContent === '2 / 2' && ${shadow}?.querySelector('.page[data-page-number="2"] .textLayer span')`);
      const requests = async () => (await fetch(`${base}/__pdf__/requests`)).json();
      const waitForRequest = (name, count) => fetch(`${base}/__pdf__/wait?name=${name}&count=${count}`, { signal: AbortSignal.timeout(30_000) });
      const release = name => fetch(`${base}/__pdf__/release?name=${name}`);
      await click('[data-open-pdf="transient"]');
      await failed();
      await evaluate(`window.originalPDFHost = document.querySelector("[data-workspace-pdf-preview]"); window.documentSentinel = {};`);
      assert.equal(await evaluate('Boolean(document.querySelector(".pdf-selection-menu"))'), false);
      for (const theme of ["light", "dark"]) {
        for (const size of [14, 20]) {
          await evaluate(`document.documentElement.dataset.theme = ${JSON.stringify(theme)};
            document.documentElement.style.setProperty("--ui-font-size", "${size}px");`);
          window.setContentSize(size === 14 ? 1440 : 760, 900);
          fs.writeFileSync(path.join(run, `pdf-error-${theme}-${size}.png`), (await window.webContents.capturePage()).toPNG());
        }
      }
      await evaluate(`(() => { const button = ${retry}; button.click(); button.click(); })()`);
      await waitFor(`${retry}?.disabled && ${shadow}?.querySelector('[role=status]')`);
      await waitForRequest("transient.pdf", 2);
      assert.equal((await requests()).filter(name => name === "transient.pdf").length, 2);
      assert.equal(await evaluate('document.querySelector("[aria-label=Draft]").value'), "Keep this conversation draft");
      assert.deepEqual(await evaluate('window.pdfReads'), ["transient.pdf", "transient.pdf"]);
      await release("transient.pdf");
      await ready();
      assert.equal(await evaluate('window.originalPDFHost === document.querySelector("[data-workspace-pdf-preview]") && Boolean(window.documentSentinel)'), true);
      // Capture actual page-2 text and verify the preserved source revision.
      await evaluate(`(() => {
        const root = ${shadow}; const text = root.querySelector('.page[data-page-number="2"] .textLayer span');
        const range = document.createRange(); range.selectNodeContents(text);
        const selection = root.getSelection(); selection.removeAllRanges(); selection.addRange(range);
        document.dispatchEvent(new Event("selectionchange"));
      })()`);
      await waitFor('document.querySelector(".pdf-selection-menu button")');
      await click(".pdf-selection-menu button");
      const parts = await evaluate('window.pdfParts');
      const quote = parts.find(part => part.type === "file_selection");
      assert.equal(quote.source.revision, `workspace-pdf:1000:7:${"a".repeat(64)}`);
      assert.equal(quote.source.path, "transient.pdf");
      assert.deepEqual(quote.source.pdf, { start_page: 2, end_page: 2 });
      fs.writeFileSync(path.join(run, "pdf-recovered.png"), (await window.webContents.capturePage()).toPNG());
      // Mutable workspace paths must refresh their owner-provided revision.
      await click('[data-open-pdf="changed"]'); await failed();
      await evaluate(`${retry}.click()`);
      await waitForRequest("changed.pdf", 2);
      await release("changed.pdf"); await ready();
      await evaluate(`(() => {
        const root = ${shadow}; const text = root.querySelector('.page[data-page-number="2"] .textLayer span');
        const range = document.createRange(); range.selectNodeContents(text);
        const selection = root.getSelection(); selection.removeAllRanges(); selection.addRange(range);
        document.dispatchEvent(new Event("selectionchange"));
      })()`);
      await waitFor('document.querySelector(".pdf-selection-menu button")');
      await click(".pdf-selection-menu button");
      const changedQuote = (await evaluate('window.pdfParts')).find(part => part.type === "file_selection" && part.source.path === "changed.pdf");
      assert.equal(changedQuote.source.revision, `workspace-pdf:1000:8:${"b".repeat(64)}`);
      assert.match(changedQuote.source.quote, /Updated fixture page 2/);
      assert.equal((await requests()).filter(name => name === "changed.pdf").length, 2);
      assert.equal((await evaluate('window.pdfParts')).filter(part => part.type === "file_selection").length, 2, "one selection action must add only one excerpt after retry");
      // Metadata read failures remain recoverable without another byte request.
      await click('[data-open-pdf="metadata"]'); await failed();
      await evaluate(`${retry}.click()`); await failed();
      assert.equal((await requests()).filter(name => name === "metadata.pdf").length, 1);
      assert.match(await evaluate(`${shadow}.querySelector('[role=alert]').textContent`), /Metadata temporarily unavailable/);
      await evaluate(`${retry}.click()`); await waitForRequest("metadata.pdf", 2);
      await release("metadata.pdf"); await ready();
      for (const name of ["metadata-stale", "metadata-close"]) {
        await click(`[data-open-pdf="${name}"]`); await failed();
        await evaluate(`${retry}.click()`);
        await waitForRequest(`metadata:${name}.pdf`, 1);
        await waitFor(`${retry}?.disabled && ${shadow}?.querySelector('[role=status]')`);
        if (name === "metadata-stale") {
          await click('[data-open-pdf="replacement"]'); await ready();
        } else {
          await click("[data-close-pdf]"); await waitFor('!document.querySelector("[data-workspace-pdf-preview]")');
        }
        await evaluate(`window.metadataSettled = new Promise(resolve => {
          const settled = event => {
            if (event.detail !== ${JSON.stringify(name + ".pdf")}) return;
            window.removeEventListener("pdf-metadata-settled", settled); resolve(true);
          };
          window.addEventListener("pdf-metadata-settled", settled);
        }); true`);
        await release(`metadata:${name}.pdf`); await evaluate('window.metadataSettled');
        await evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
        assert.equal((await requests()).filter(value => value === `${name}.pdf`).length, 1);
        assert.equal(await evaluate('document.querySelector("[data-workspace-pdf-preview]")?.shadowRoot?.querySelector("section")?.getAttribute("aria-label") ?? null'), name === "metadata-stale" ? "replacement.pdf" : null);
      }
      await click('[data-open-pdf="corrupt"]');
      await failed();
      for (let i = 0; i < 2; i += 1) {
        await evaluate(`${retry}.click()`); await waitForRequest("corrupt.pdf", i + 2); await failed();
      }
      assert.equal((await requests()).filter(name => name === "corrupt.pdf").length, 3);
      for (const name of ["stale", "close"]) {
        await click(`[data-open-pdf="${name}"]`); await failed();
        await evaluate(`${retry}.click()`);
        await waitFor(`${retry}?.disabled`);
        await waitForRequest(`${name}.pdf`, 2);
        if (name === "stale") {
          await click('[data-open-pdf="replacement"]'); await ready();
        } else {
          await click("[data-close-pdf]"); await waitFor('!document.querySelector("[data-workspace-pdf-preview]")');
        }
        await release(`${name}.pdf`);
        // Flush queued observer/paint work after releasing the cancelled request.
        await evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
        assert.equal(await evaluate('document.querySelector("[data-workspace-pdf-preview]")?.shadowRoot?.querySelector("section")?.getAttribute("aria-label") ?? null'), name === "stale" ? "replacement.pdf" : null);
        assert.equal(await evaluate('Boolean(document.querySelector(".pdf-selection-menu"))'), false);
      }
      assert.equal(await evaluate('document.querySelector("[aria-label=Draft]").value.endsWith("Keep this conversation draft")'), true);
      measurements.push({ view: "pdf-retry", boundary: "real renderer and PDF.js; synthetic workspace IPC and local HTTP",
        requests: await requests(), quote: quote.source, changedQuote: changedQuote.source, checks: ["in-place-retry", "double-click", "source-revision", "refreshed-source-revision", "metadata-retry", "switch-during-metadata", "close-during-metadata", "page-identity", "corrupt-retry", "switch-during-retry", "close-during-retry", "draft-retained"] });
      fs.writeFileSync(path.join(run, "browser-result.json"), JSON.stringify({ importFailureContained: true, navigationSurvived: true, reloadRecovered: true, measurements, logs }, null, 2));
    } finally {
      window.destroy();
    }
    app.quit();
  }).catch((error) => { console.error(error); app.exit(1); });
}
