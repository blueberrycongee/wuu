// Exercise real lazy imports and dependency-cache isolation with synthetic
// workspace data. Never open the installed app profile or start a Go server.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { once } = require("node:events");

const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out/e2e/workspace-preview");

function pdfBytes(objects) {
  let text = "%PDF-1.4\n";
  const offsets = [0];
  objects.forEach((object, i) => { offsets.push(Buffer.byteLength(text)); text += `${i + 1} 0 obj\n${object}\nendobj\n`; });
  const xref = Buffer.byteLength(text);
  text += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.slice(1).map(n => `${String(n).padStart(10, "0")} 00000 n \n`).join("")}trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(text);
}

// Minimal deterministic PDF bytes, served locally to the real PDF.js worker.
function pdfFixture(label = "Retry fixture") {
  const objects = ["<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 2 /Kids [4 0 R 6 0 R] >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"];
  for (const page of [1, 2]) {
    const content = `BT /F1 16 Tf 28 200 Td (${label} page ${page}) Tj ET`;
    objects.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 260] /Resources << /Font << /F1 3 0 R >> >> /Contents ${page * 2 + 3} 0 R >>`,
      `<< /Length ${Buffer.byteLength(content)} >>\nstream\n${content}\nendstream`);
  }
  return pdfBytes(objects);
}

// Original, generated PDF with a name tree, indirect page references, explicit
// page indices, broken destinations, and safe/unsafe external actions.
function pdfNavigationFixture() {
  const links = [
    ["named", "/Dest (chapter-two)", 2],
    ["reference", "/Dest [8 0 R /XYZ null 740 null]", 3],
    ["index", "/Dest [1 /Fit]", 2],
    ["missing-name", "/Dest (missing)"],
    ["missing-reference", "/Dest [999 0 R /Fit]"],
    ["past-end", "/Dest [99 /Fit]"],
    ["negative-page", "/Dest [-1 /Fit]"],
    ["https", "/A << /S /URI /URI (https://example.com/pdf-link) >>"],
    ["mailto", "/A << /S /URI /URI (mailto:pdf@example.com) >>"],
    ["javascript-url", "/A << /S /URI /URI (javascript:alert\\(1\\)) >>"],
    ["file-url", "/A << /S /URI /URI (file:///tmp/forbidden.pdf) >>"],
    ["remote-file", "/A << /S /GoToR /F (file:///tmp/forbidden.pdf) /D [0 /Fit] >>"],
    ["script-action", "/A << /S /JavaScript /JS (app.alert\\(1\\)) >>"],
    ["named-action", "/A << /S /Named /N /LastPage >>"],
  ];
  const objects = [
    "<< /Type /Catalog /Pages 2 0 R /Names << /Dests << /Names [(chapter-two) [6 0 R /Fit]] >> >> >>",
    "<< /Type /Pages /Count 3 /Kids [4 0 R 6 0 R 8 0 R] >>",
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    ...Array(6).fill(""),
  ];
  const annotations = links.map(([name, action, targetPage], index) => {
    const object = objects.length + 1;
    const y = 700 - index * 34;
    objects.push(`<< /Type /Annot /Subtype /Link /Rect [25 ${y - 5} 370 ${y + 19}] /Border [0 0 1] ${action} >>`);
    return { name, object, id: `${object}R`, y, targetPage };
  });
  const backObject = objects.length + 1;
  objects.push("<< /Type /Annot /Subtype /Link /Rect [25 695 370 719] /Border [0 0 1] /Dest [4 0 R /Fit] >>");
  for (const page of [1, 2, 3]) {
    const text = page === 1 ? annotations : [{ name: "back", y: 700 }];
    const content = `BT /F1 16 Tf 28 750 Td (Navigation fixture page ${page}) Tj ET\n`
      + text.map(({ name, y }) => `BT /F1 12 Tf 28 ${y} Td (${name}) Tj ET`).join("\n");
    const refs = page === 1 ? annotations.map(item => `${item.object} 0 R`).join(" ") : `${backObject} 0 R`;
    objects[page * 2 + 1] = `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 780] /Resources << /Font << /F1 3 0 R >> >> /Contents ${page * 2 + 3} 0 R /Annots [${refs}] >>`;
    objects[page * 2 + 2] = `<< /Length ${Buffer.byteLength(content)} >>\nstream\n${content}\nendstream`;
  }
  return { bytes: pdfBytes(objects), annotations, backID: `${backObject}R` };
}

if (process.argv.includes("--pdf-fixtures-only")) {
  (async () => {
    const { getDocument, isValidExplicitDest } = await import("pdfjs-dist/legacy/build/pdf.mjs");
    fs.mkdirSync(output, { recursive: true });
    const navigation = pdfNavigationFixture();
    const result = { boundary: "Real Node PDF.js parsing and destination resolution; no renderer or GUI", fixtures: [] };
    for (const [name, bytes, pages] of [["retry", pdfFixture(), 2], ["navigation", navigation.bytes, 3]]) {
      fs.writeFileSync(path.join(output, `${name}.pdf`), bytes);
      const task = getDocument({ data: new Uint8Array(bytes), isEvalSupported: false, useSystemFonts: true });
      try {
        const pdf = await task.promise;
        assert.equal(pdf.numPages, pages);
        const texts = [];
        for (let page = 1; page <= pages; page += 1) texts.push((await (await pdf.getPage(page)).getTextContent()).items.map(item => item.str).join(" "));
        const destinations = [];
        if (name === "navigation") {
          const annotations = await (await pdf.getPage(1)).getAnnotations();
          for (const fixture of navigation.annotations) {
            const annotation = annotations.find(item => item.id === fixture.id);
            assert.ok(annotation, fixture.name);
            if (fixture.targetPage) {
              const destination = typeof annotation.dest === "string" ? await pdf.getDestination(annotation.dest) : annotation.dest;
              assert.equal(isValidExplicitDest(destination), true, fixture.name);
              const page = typeof destination[0] === "number" ? destination[0] + 1 : await pdf.getPageIndex(destination[0]) + 1;
              assert.equal(page, fixture.targetPage, fixture.name);
              destinations.push({ name: fixture.name, page, destination });
            }
            if (["javascript-url", "file-url", "remote-file", "script-action"].includes(fixture.name)) assert.equal(annotation.url, undefined, fixture.name);
          }
          assert.equal(await pdf.getDestination("missing"), null);
          await assert.rejects(pdf.getPageIndex({ num: 999, gen: 0 }));
        }
        result.fixtures.push({ name, pages, texts, destinations });
      } finally { await task.destroy(); }
    }
    fs.writeFileSync(path.join(output, "node-pdf-fixtures.json"), JSON.stringify(result, null, 2));
    console.log(JSON.stringify(result, null, 2));
  })().catch(error => { console.error(error); process.exitCode = 1; });
} else if (!process.versions.electron) {
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
      // PDFViewer requires the main PDF.js module before its first evaluation.
      // The production preview imports that module itself, but is lazy here.
      import "pdfjs-dist";
      import { PDFViewer } from "pdfjs-dist/web/pdf_viewer.mjs";
      import "/src/renderer/styles.css";
      const query = new URLSearchParams(location.search);
      document.documentElement.dataset.theme = query.get("theme") || "light";
      document.documentElement.dataset.platform = "darwin";
      applyMessageFlowFontSize(Number(query.get("size")) || 14);
      const context = { cwd: "/synthetic", thread_id: "pdf-retry" };
      window.pdfReads = [];
      window.pdfParts = [];
      window.pdfExternalURLs = [];
      const setDocument = PDFViewer.prototype.setDocument;
      PDFViewer.prototype.setDocument = function(pdf) {
        if (pdf) {
          window.pdfViewer = this;
          const navigate = this.linkService.goToDestination.bind(this.linkService);
          this.linkService.goToDestination = destination => {
            window.pdfNavigationPending = navigate(destination);
            return window.pdfNavigationPending;
          };
        }
        return setDocument.call(this, pdf);
      };
      // Delay a real PDF.js result, without replacing its parsed destination.
      // The document restores its method before waiting so newer clicks proceed.
      window.holdPDFDestination = method => {
        const pdf = window.pdfViewer.pdfDocument;
        const original = pdf[method].bind(pdf);
        const cached = pdf.cachedPageNumber.bind(pdf);
        if (method === "getPageIndex") pdf.cachedPageNumber = () => null;
        window.pdfDestinationHeld = false;
        const barrier = new Promise(resolve => { window.releasePDFDestination = resolve; });
        pdf[method] = async (...args) => {
          const result = await original(...args);
          pdf[method] = original;
          pdf.cachedPageNumber = cached;
          window.pdfDestinationHeld = true;
          await barrier;
          return result;
        };
      };
      Object.defineProperty(window, "wuu", { value: {
        initialLanguagePreference: "en-US",
        openExternal: async url => { window.pdfExternalURLs.push(url); },
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
        const [navigationAnchor, setNavigationAnchor] = useState();
        const [previewActive, setPreviewActive] = useState(true);
        return <FileSelectionProvider ownerKey="pdf-retry" getPrompt={() => draft}
          setPrompt={value => { setDraft(value); window.pdfParts = readCollapsedPromptParts("pdf-retry", value); }}
          onEdit={async () => false} onOpenFile={setSelected}>
          <div style={{display: "grid", gridTemplateColumns: "minmax(180px, 1fr) minmax(0, 1fr)", height: "100vh"}}>
            <section aria-label="Conversation"><textarea aria-label="Draft" value={draft} onChange={event => setDraft(event.target.value)} />
              {["transient", "corrupt", "stale", "close", "replacement", "changed", "metadata", "metadata-stale", "metadata-close", "navigation", "navigation-other"].map(name =>
                <button key={name} data-open-pdf={name} onClick={() => { setNavigationAnchor(undefined); setPreviewActive(true); setSelected(name + ".pdf"); }}>{name}.pdf</button>)}
              <button data-close-pdf onClick={() => setSelected(undefined)}>Close preview</button>
              <button data-hide-pdf onClick={() => setPreviewActive(false)}>Hide preview</button>
              <button data-show-pdf onClick={() => setPreviewActive(true)}>Show preview</button>
              <button data-request-pdf-page onClick={() => setNavigationAnchor("pdf-page=2")}>Request PDF page 2</button>
            </section>
            <aside aria-label="File preview" hidden={!previewActive} style={{minWidth: 0, minHeight: 0}}>
              {selected ? <WorkspaceFilePreview activeContext={context} selectedFilePath={selected} active={previewActive}
                anchor={selected.startsWith("navigation") ? navigationAnchor : "pdf-page=2"} /> : null}
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
    const navigationPDF = pdfNavigationFixture();
    fs.writeFileSync(path.join(run, "navigation.pdf"), navigationPDF.bytes);
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
            const bytes = name.startsWith("navigation") ? navigationPDF.bytes : name === "changed.pdf" ? changedPDFBytes : pdfBytes;
            response.setHeader("Content-Length", bytes.length);
            response.end(bytes);
          };
          if (name === "corrupt.pdf") return response.end("corrupt PDF bytes");
          const immediate = name === "replacement.pdf" || name.startsWith("navigation");
          if (!immediate && count === 1) {
            response.statusCode = 503; return response.end("Temporary PDF failure");
          }
          if (!immediate) pendingPDFs.set(name, sendPDF);
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
      server: { host: "127.0.0.1", port: 0, fs: { allow: [path.dirname(desktop), fs.realpathSync(path.join(desktop, "node_modules/pdfjs-dist"))] } },
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
      const child = spawn(require("electron"), [...(process.env.WUU_PREVIEW_HEADLESS === "1" ? ["--no-sandbox", "--ozone-platform=headless", "--ozone-override-screen-size=1440,1100"] : []), __filename], { env, stdio: "inherit" });
      const timeout = setTimeout(() => child.kill("SIGTERM"), 300_000);
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
  process.env.HOME = path.join(run, "profile");
  app.on("window-all-closed", () => {});
  app.whenReady().then(async () => {
    const window = new BrowserWindow({ show: true, width: 1440, height: 1000, ...(process.platform === "linux" ? { frame: false, autoHideMenuBar: true } : {}), webPreferences: { partition: "workspace-preview-e2e", contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
    const logs = [];
    window.webContents.on("console-message", (event) => logs.push(event.message));
    window.webContents.on("render-process-gone", (_event, details) => { logs.push(JSON.stringify({ rendererExit: details })); console.error("rendererExit", details); });
    window.webContents.on("did-fail-load", (_event, errorCode, errorDescription, url) => logs.push(JSON.stringify({ errorCode, errorDescription, url })));
    const evaluate = (code) => window.webContents.executeJavaScript(code);
    const waitFor = (condition) => evaluate(`new Promise((resolve, reject) => {
      const roots = new Set();
      const check = () => {
        for (const host of document.querySelectorAll("*")) {
          if (host.shadowRoot && !roots.has(host.shadowRoot)) {
            roots.add(host.shadowRoot); observer.observe(host.shadowRoot, { subtree: true, childList: true, attributes: true, characterData: true });
          }
        }
        if (${condition}) { observer.disconnect(); clearTimeout(timeout); resolve(true); }
      };
      const observer = new MutationObserver(check);
      const timeout = setTimeout(() => { observer.disconnect(); reject(new Error(${JSON.stringify(condition)})); }, 30000);
      observer.observe(document.documentElement, { subtree: true, childList: true, attributes: true, characterData: true }); check();
    })`);
    const trustedInteractions = [];
    const nativeClick = async (expression, button = "left", count = 1) => {
      await waitFor(`Boolean(${expression})`);
      const marker = "preview-trusted-click:" + trustedInteractions.length;
      const point = await evaluate(`(async () => {
        const element = ${expression};
        if (!element) throw new Error("Missing native click target");
        element.scrollIntoView({ block: "center", inline: "center" });
        let previous = "", stable = 0, bounds;
        for (let frame = 0; frame < 120; frame += 1) {
          await new Promise(resolve => requestAnimationFrame(resolve));
          bounds = element.getBoundingClientRect();
          const current = [bounds.x, bounds.y, bounds.width, bounds.height].join(",");
          stable = current === previous ? stable + 1 : 0; previous = current;
          if (stable >= 6) break;
        }
        if (stable < 6) throw new Error("Click target did not settle");
        element.addEventListener(${JSON.stringify(button === "middle" ? "auxclick" : "click")}, event => {
          if (event.isTrusted) console.info(${JSON.stringify(marker)});
        }, { once: true });
        return { x: Math.round(bounds.x + bounds.width / 2), y: Math.round(bounds.y + bounds.height / 2) };
      })()`);
      for (let index = 0; index < count; index += 1) {
        window.webContents.sendInputEvent({ type: "mouseDown", button, clickCount: 1, ...point });
        window.webContents.sendInputEvent({ type: "mouseUp", button, clickCount: 1, ...point });
      }
      const deadline = Date.now() + 5000;
      while (!logs.includes(marker) && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 10));
      assert.equal(logs.includes(marker), true, expression);
      trustedInteractions.push({ expression, button, count });
    };
    const click = selector => nativeClick(`document.querySelector(${JSON.stringify(selector)})`);
    // Finish finite effects before recording static surfaces; leave activity loops.
    const settle = () => evaluate(`document.getAnimations().filter(animation => animation.effect.getComputedTiming().iterations !== Infinity).forEach(animation => animation.finish())`);
    const measurements = [];
    const capture = async name => {
      await evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))');
      fs.writeFileSync(path.join(run, name), (await window.webContents.capturePage()).toPNG());
    };
    try {
      await window.loadURL(`${base}${review}?width=420`);
      window.webContents.debugger.attach("1.3");
      await window.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
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
      console.log("PASS workspace import recovery and theme surfaces");
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
            document.documentElement.style.setProperty("--conversation-message-font-size", "${size}px");
            document.documentElement.style.setProperty("--appearance-scale", "${size / 14}");`);
          window.setContentSize(size === 14 ? 1440 : 760, 900);
          await evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))');
          const errorBounds = await evaluate(`(() => {
            const root = ${shadow};
            const viewport = root.querySelector('.workspace-pdf-viewport').getBoundingClientRect();
            const detail = root.querySelector('[role=alert] span');
            const bounds = detail.getBoundingClientRect();
            return { left: bounds.left, right: bounds.right, viewportLeft: viewport.left, viewportRight: viewport.right,
              clientWidth: detail.clientWidth, scrollWidth: detail.scrollWidth };
          })()`);
          assert.ok(errorBounds.left >= errorBounds.viewportLeft && errorBounds.right <= errorBounds.viewportRight, "PDF error details fit the panel");
          assert.ok(errorBounds.scrollWidth <= errorBounds.clientWidth, "long PDF errors wrap without clipping");
          measurements.push({ view: "pdf-error", theme, size, ...errorBounds });
          await capture(`pdf-error-${theme}-${size}.png`);
        }
      }
      await nativeClick(retry, "left", 2);
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
      await capture("pdf-recovered.png");
      // Mutable workspace paths must refresh their owner-provided revision.
      await click('[data-open-pdf="changed"]'); await failed();
      await nativeClick(retry);
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
      await nativeClick(retry); await failed();
      assert.equal((await requests()).filter(name => name === "metadata.pdf").length, 1);
      assert.match(await evaluate(`${shadow}.querySelector('[role=alert]').textContent`), /Metadata temporarily unavailable/);
      await nativeClick(retry); await waitForRequest("metadata.pdf", 2);
      await release("metadata.pdf"); await ready();
      for (const name of ["metadata-stale", "metadata-close"]) {
        await click(`[data-open-pdf="${name}"]`); await failed();
        await nativeClick(retry);
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
        await nativeClick(retry); await waitForRequest("corrupt.pdf", i + 2); await failed();
      }
      assert.equal((await requests()).filter(name => name === "corrupt.pdf").length, 3);
      for (const name of ["stale", "close"]) {
        await click(`[data-open-pdf="${name}"]`); await failed();
        await nativeClick(retry);
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

      console.log("PASS PDF retry, metadata, source replacement and draft retention");
      const navigation = pdfNavigationFixture();
      const windows = [];
      const navigations = [];
      window.webContents.setWindowOpenHandler(details => { windows.push(details.url); return { action: "deny" }; });
      window.webContents.on("will-navigate", (event, url) => { event.preventDefault(); navigations.push(url); });
      const pageAt = page => waitFor(`${shadow}?.querySelector('.workspace-pdf-page-count')?.textContent === '${page} / 3' && ${shadow}?.querySelector('.page[data-page-number="${page}"] .textLayer span')`);
      const linkSelector = name => `.page[data-page-number="1"] a[data-element-id="${navigation.annotations.find(item => item.name === name).id}"]`;
      const destinationClick = async (name, middle = false) => {
        const selector = linkSelector(name);
        await waitFor(`${shadow}?.querySelector(${JSON.stringify(selector)})`);
        await nativeClick(`${shadow}.querySelector(${JSON.stringify(selector)})`, middle ? "middle" : "left");
      };
      const back = async () => {
        const selector = `.page[data-page-number="${await evaluate('window.pdfViewer.currentPageNumber')}"] a[data-element-id="${navigation.backID}"]`;
        await waitFor(`${shadow}?.querySelector(${JSON.stringify(selector)})`);
        await nativeClick(`${shadow}.querySelector(${JSON.stringify(selector)})`);
        await pageAt(1);
      };
      await click('[data-open-pdf="navigation"]'); await pageAt(1);
      const navigationURL = await evaluate('location.href');
      const initialScale = await evaluate('window.pdfViewer.currentScale');
      for (const [name, page, middle] of [["named", 2, false], ["reference", 3, false], ["index", 2, true]]) {
        await destinationClick(name, middle); await pageAt(page);
        assert.equal(await evaluate('window.pdfViewer.currentScale'), initialScale, "internal links preserve the reader's zoom");
        await back();
      }
      // Keyboard activation uses the annotation's ordinary anchor handler.
      await evaluate(`${shadow}.querySelector(${JSON.stringify(linkSelector("named"))}).focus()`);
      window.webContents.sendInputEvent({ type: "keyDown", keyCode: "Return" });
      window.webContents.sendInputEvent({ type: "keyUp", keyCode: "Return" });
      await pageAt(2); await back();
      for (const name of ["missing-name", "missing-reference", "past-end", "negative-page", "named-action"]) {
        await destinationClick(name);
        await evaluate('window.pdfNavigationPending');
        assert.equal(await evaluate('window.pdfViewer.currentPageNumber'), 1, name);
        assert.equal(await evaluate(`Boolean(${shadow}.querySelector('[role=alert]'))`), false, name);
      }
      for (const name of ["https", "mailto"]) await destinationClick(name, name === "mailto");
      assert.deepEqual(await evaluate('window.pdfExternalURLs'), ["https://example.com/pdf-link", "mailto:pdf@example.com"]);
      for (const name of ["javascript-url", "file-url", "remote-file", "script-action"]) {
        assert.equal(await evaluate(`Boolean(${shadow}.querySelector(${JSON.stringify(linkSelector(name))}))`), false, name);
      }
      assert.equal(await evaluate('location.href'), navigationURL);
      await capture("pdf-navigation.png");

      const holdDestination = async method => {
        await evaluate(`window.holdPDFDestination(${JSON.stringify(method)})`);
        await destinationClick(method === "getDestination" ? "named" : "reference");
        // The promise changes a JS value rather than the DOM, so observe its
        // barrier with a bounded frame probe instead of the DOM-only waiter.
        await evaluate(`new Promise((resolve, reject) => {
          const timeout = setTimeout(() => reject(new Error("Destination did not reach barrier")), 30000);
          const check = () => {
            if (window.pdfDestinationHeld) { clearTimeout(timeout); window.pdfHeldNavigationPending = window.pdfNavigationPending; resolve(true); }
            else requestAnimationFrame(check);
          }; check();
        })`);
      };
      const releaseDestination = () => evaluate('window.releasePDFDestination(); window.pdfHeldNavigationPending');
      for (const method of ["getDestination", "getPageIndex"]) {
        await holdDestination(method);
        await destinationClick("index"); await pageAt(2);
        await releaseDestination();
        assert.equal(await evaluate('window.pdfViewer.currentPageNumber'), 2, "newest click wins");
        await back();
        for (const showBeforeRelease of [false, true]) {
          await holdDestination(method);
          await evaluate('window.retainedPDFViewer = window.pdfViewer; true');
          await click('[data-hide-pdf]');
          await waitFor('document.querySelector("aside[hidden]")');
          if (showBeforeRelease) {
            await click('[data-show-pdf]'); await waitFor('!document.querySelector("aside[hidden]")');
          }
          await click('[aria-label="Draft"]');
          await releaseDestination();
          assert.equal(await evaluate(`document.activeElement === document.querySelector('[aria-label="Draft"]')`), true, "hidden completion does not steal draft focus");
          if (!showBeforeRelease) {
            await click('[data-show-pdf]'); await waitFor('!document.querySelector("aside[hidden]")');
          }
          assert.equal(await evaluate('window.pdfViewer === window.retainedPDFViewer'), true, "hidden tabs retain their PDF viewer");
          assert.equal(await evaluate('window.pdfViewer.currentPageNumber'), 1, "hidden navigation does not replay after showing the panel");
        }
        await holdDestination(method);
        await click('[data-open-pdf="navigation-other"]'); await pageAt(1);
        await releaseDestination();
        assert.equal(await evaluate('window.pdfViewer.currentPageNumber'), 1, "source replacement fences old resolutions");
        await click('[data-open-pdf="navigation"]'); await pageAt(1);
        await holdDestination(method);
        await click('[data-close-pdf]');
        await waitFor('!document.querySelector("[data-workspace-pdf-preview]")');
        await releaseDestination();
        assert.equal(await evaluate('Boolean(document.querySelector("[data-workspace-pdf-preview]"))'), false);
        await click('[data-open-pdf="navigation"]'); await pageAt(1);
      }
      await holdDestination("getDestination");
      await click('[data-request-pdf-page]'); await pageAt(2);
      await releaseDestination();
      assert.equal(await evaluate('window.pdfViewer.currentPageNumber'), 2, "new page requests supersede pending links");
      await back();
      await holdDestination("getPageIndex");
      // Reuse the existing recovery boundary while a real destination result is
      // held. The loading task and replacement document remain real PDF.js.
      await evaluate('window.pdfViewer.eventBus.dispatch("pagerendered", { error: new Error("Synthetic page render failure") })');
      await failed();
      await nativeClick(retry); await pageAt(1);
      await releaseDestination();
      assert.equal(await evaluate('window.pdfViewer.currentPageNumber'), 1, "retry fences pending page references");
      await destinationClick("named"); await pageAt(2);
      await back();
      // Inject only the requested page's transport failure; PDF parsing,
      // annotations, navigation, error surface and retried document stay real.
      await evaluate(`(() => {
        const pdf = window.pdfViewer.pdfDocument;
        const getPage = pdf.getPage.bind(pdf);
        pdf.getPage = number => number === 3 ? Promise.reject(new Error("Synthetic page fetch failure")) : getPage(number);
      })()`);
      await destinationClick("reference"); await failed();
      assert.match(await evaluate(`${shadow}.querySelector('[role=alert]').textContent`), /Synthetic page fetch failure/);
      await capture("pdf-page-failure.png");
      await nativeClick(retry); await pageAt(3);
      await back();
      await destinationClick("named"); await pageAt(2);
      await capture("pdf-navigation-recovered.png");
      assert.deepEqual(windows, [], "annotations must not request new windows");
      assert.deepEqual(navigations, [], "annotations must not navigate the renderer");
      assert.equal(await evaluate('location.href'), navigationURL);
      measurements.push({ view: "pdf-internal-navigation", boundary: "real renderer and PDF.js; synthetic workspace/external IPC, delayed real destination results, injected page-fetch/render failures",
        checks: ["named", "indirect-reference", "explicit-page-index", "middle-click", "keyboard", "zoom-retained", "missing-destination", "page-bounds", "safe-external-IPC", "unsafe-actions-inert", "latest-click", "hidden-retained-viewer", "source-replacement", "close-reopen", "new-page-request", "retry", "page-fetch-failure-retry", "hidden-focus-retained", "no-new-windows", "no-renderer-navigation"], windows, navigations });
      fs.writeFileSync(path.join(run, "browser-result.json"), JSON.stringify({ importFailureContained: true, navigationSurvived: true, reloadRecovered: true, measurements, trustedInteractions, logs }, null, 2));
    } catch (error) {
      fs.writeFileSync(path.join(run, "browser-failure.json"), JSON.stringify({ error: String(error.stack || error), measurements, trustedInteractions, logs }, null, 2));
      try { fs.writeFileSync(path.join(run, "failure.png"), (await window.webContents.capturePage()).toPNG()); } catch {}
      throw error;
    } finally {
      window.destroy();
    }
    app.quit();
  }).catch((error) => { console.error(error); app.exit(1); });
}
