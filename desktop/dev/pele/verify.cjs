// Reproducible desktop acceptance and same-browser engine comparison.
// Run after npm run build: cd desktop && npx electron dev/pele/verify.cjs
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const { app, BrowserWindow } = require("electron");
const desktop = path.resolve(__dirname, "../..");
const output = path.join(desktop, "out/pele-acceptance");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
app.on("window-all-closed", () => {});
let server;
let win;
const report = { environment: { electron: process.versions.electron, chrome: process.versions.chrome, platform: process.platform, arch: process.arch, cpu: os.cpus()[0].model }, compatibility: [], behavior: [], benchmark: [] };
const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(value => JSON.stringify(value)).join(",")})`, true);
const frames = () => evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function until(fn) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await evaluate(fn)) return;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function show(code, live = false) {
  await evaluate((code, live) => window.diagramFixture.show(code, live), code, live);
  await frames();
}
const capture = async name => fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
const timeout = setTimeout(() => { console.error("Diagram acceptance timed out"); app.exit(1); }, 240000);

app.whenReady().then(async () => {
  report.environment.packages = Object.fromEntries(["pele", "mermaid"].map(name => [name, require(path.join(desktop, "node_modules", name, "package.json")).version]));
  const { build } = require("esbuild");
  const { gzipSync } = require("node:zlib");
  report.bundles = {};
  for (const engine of ["pele", "mermaid"]) {
    const result = await build({
      stdin: { contents: engine === "pele" ? 'export { render } from "pele";' : 'export { default } from "mermaid";', resolveDir: desktop },
      bundle: true, write: false, minify: true, format: "esm", platform: "browser", target: "es2022",
    });
    const bytes = result.outputFiles[0].contents;
    report.bundles[engine] = { bytes: bytes.length, gzipBytes: gzipSync(bytes).length };
  }
  const { createServer } = await import("vite");
  const { default: react } = await import("@vitejs/plugin-react");
  server = await createServer({
    configFile: false, root: desktop, plugins: [react()],
    cacheDir: path.join(desktop, "node_modules/.vite/pele-acceptance"),
    define: { __DESKTOP_VERSION__: '"acceptance"', __DESKTOP_BUILD_DATE__: '"local"', __WUU_ACCOUNT_SERVER__: '""' },
    optimizeDeps: { entries: ["dev/pele/index.html"], include: ["pele", "mermaid"] },
    server: { host: "127.0.0.1", port: 0, fs: { allow: [path.dirname(desktop)] }, watch: { ignored: ["**/out/**", "**/build/**"] } },
  });
  await server.listen();
  win = new BrowserWindow({ width: 1100, height: 800, show: false, webPreferences: { backgroundThrottling: false } });
  win.webContents.on("console-message", event => {
    if (event.level === "error") console.error(event.message);
  });
  await win.loadURL(`http://127.0.0.1:${server.httpServer.address().port}/dev/pele/index.html`);
  await until(() => window.diagramFixture && document.querySelector(".rich-mermaid svg"));
  const samples = await evaluate(() => window.diagramFixture.cases);
  for (const [width, size, theme] of [[1100, 15, "light"], [1100, 20, "dark"], [420, 15, "dark"], [420, 20, "light"]]) {
    win.setContentSize(width, 800);
    await evaluate((size, theme) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--font-ui", `${size}px`);
      window.dispatchEvent(new Event("wuu-content-size-change"));
    }, size, theme);
    for (const sample of samples) {
      await show(sample.code);
      await until(() => document.querySelector(".rich-mermaid")?.dataset.mermaidState !== "rendering");
      const result = await evaluate(() => {
        const shell = document.querySelector(".rich-mermaid");
        const svg = shell.querySelector("svg");
        const rect = svg?.getBoundingClientRect();
        return { state: shell.dataset.mermaidState, text: shell.textContent, width: rect?.width, height: rect?.height,
          pageOverflow: document.documentElement.scrollWidth > innerWidth,
          activeElements: shell.querySelectorAll("script,foreignObject,a,image,[onclick],[onload],[onerror]").length };
      });
      report.compatibility.push({ name: sample.name, viewportWidth: width, size, theme, ...result });
      assert.equal(result.state, "rendered", `${sample.name}: ${result.text}`);
      assert.ok(result.width > 0 && result.height > 0, sample.name);
      assert.equal(result.pageOverflow, false, `${sample.name}: page overflow`);
      assert.equal(result.activeElements, 0, `${sample.name}: inert SVG`);
      if (["flowchart-cjk", "sequence", "pie", "mindmap"].includes(sample.name)) await capture(`${sample.name}-${theme}-${width}-${size}`);
    }
  }
  // Colors update on the existing SVG, including extension theme token changes.
  await show(samples[0].code);
  const before = await evaluate(() => {
    window.originalDiagram = document.querySelector(".rich-mermaid svg");
    return getComputedStyle(window.originalDiagram.querySelector("text")).fill;
  });
  await evaluate(() => { document.documentElement.dataset.theme = "dark"; });
  await frames();
  assert.ok(await evaluate(before => window.originalDiagram === document.querySelector(".rich-mermaid svg") && getComputedStyle(window.originalDiagram.querySelector("text")).fill !== before, before));
  await evaluate(() => { document.documentElement.style.setProperty("--wuu-color-text", "rgb(230, 190, 120)"); });
  assert.equal(await evaluate(() => getComputedStyle(window.originalDiagram.querySelector("text")).fill), "rgb(230, 190, 120)");
  report.behavior.push("theme and extension tokens update without SVG replacement");
  // The shared preview and its saved SVG must retain the resolved colors.
  await evaluate(() => {
    window.wuu = { saveArtifactFile: async (_name, source) => { window.savedDiagram = source; } };
    document.querySelector(".rich-mermaid-diagram").click();
  });
  await until(() => document.querySelector(".image-preview-svg svg"));
  assert.equal(await evaluate(() => getComputedStyle(document.querySelector(".image-preview-svg svg text")).fill), "rgb(230, 190, 120)");
  await capture("preview-dark");
  await evaluate(() => document.querySelector('[aria-label="图片另存为…"], [aria-label="Save image as…"]').click());
  await until(() => window.savedDiagram);
  const saved = await evaluate(() => decodeURIComponent(window.savedDiagram.split(",").slice(1).join(",")));
  fs.writeFileSync(path.join(output, "exported.svg"), saved);
  const exported = new BrowserWindow({ show: false, webPreferences: { backgroundThrottling: false } });
  await exported.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(saved)}`);
  assert.equal(await exported.webContents.executeJavaScript('getComputedStyle(document.querySelector("svg text")).fill'), "rgb(230, 190, 120)");
  exported.destroy();
  await evaluate(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  report.behavior.push("shared preview and standalone SVG export retain colors");
  const prefix = "flowchart LR\nA[开始] --> B[中间]\n";
  await show(prefix, true);
  await until(() => document.querySelector(".rich-mermaid svg")?.textContent.includes("中间"));
  const previous = await evaluate(() => document.querySelector(".rich-mermaid svg").outerHTML);
  await show(prefix + "B --> C[", true);
  assert.equal(await evaluate(() => document.querySelector(".rich-mermaid svg").outerHTML), previous);
  await show(prefix + "B --> C[完成]\n", true);
  await until(() => document.querySelector(".rich-mermaid svg")?.textContent.includes("完成"));
  await show(prefix + "B --> C[完成]", false);
  await until(() => document.querySelector(".rich-mermaid svg")?.textContent.includes("完成"));
  await show("flowchart LR\nA[", false);
  await until(() => document.querySelector(".rich-mermaid")?.dataset.mermaidState === "error");
  assert.equal(await evaluate(() => document.querySelector(".rich-mermaid code").textContent), "flowchart LR\nA[");
  await show("", true);
  await show(samples[0].code);
  await until(() => document.querySelector(".rich-mermaid svg"));
  report.behavior.push("live partial source, completed lines, settlement, final error and recovery");
  win.setContentSize(1100, 800);
  report.benchmark = await evaluate(() => window.diagramFixture.benchmark());
  assert.ok(report.benchmark.every(result => !Object.keys(result.errors).length), "both engines must accept benchmark sources");
  for (const result of report.benchmark) {
    result.medianMs = Object.fromEntries(Object.entries(result.timings).map(([engine, times]) => [engine, [...times].sort((a,b) => a-b)[Math.floor(times.length / 2)]]));
    result.speedup = result.medianMs.mermaid / result.medianMs.pele;
  }
  // Also load the production renderer and its CSP in the full desktop shell.
  // The existing preload supplies only a synthetic conversation, with no model.
  const now = new Date().toISOString();
  const fixture = path.join(output, "threads.json");
  fs.writeFileSync(fixture, JSON.stringify([{
    id: "pele-desktop", preview: "Pele desktop acceptance", cwd: output, model_provider: "e2e", model: "mock-stream", status: "idle", created_at: now, updated_at: now,
    turns: [{ id: "pele-turn", status: "completed", items_view: "full", started_at: now, completed_at: now, items: [
      { id: "question", type: "user_message", status: "completed", text: "Show the rendering examples." },
      { id: "answer", type: "agent_message", status: "completed", text: samples.slice(0, 3).map(sample => `${sample.name}\n\n\`\`\`mermaid\n${sample.code}\n\`\`\``).join("\n\n") },
    ] }],
  }]));
  process.env.WUU_IMAGE_LAYOUT_FIXTURE = fixture;
  process.env.WUU_STREAM_E2E_CWD = output;
  win.destroy();
  win = new BrowserWindow({ width: 1280, height: 900, show: false, webPreferences: {
    preload: path.join(desktop, "scripts/image-layout-e2e-preload.cjs"), contextIsolation: true, sandbox: false, backgroundThrottling: false,
  } });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await until(() => document.querySelector(".thread-row-main"));
  await evaluate(() => document.querySelector(".thread-row-main").click());
  await until(() => document.querySelectorAll(".rich-mermaid svg.pele").length === 3);
  await evaluate(() => document.querySelector(".rich-mermaid").scrollIntoView({ block: "center" }));
  await frames();
  await capture("production-desktop");
  report.behavior.push("production desktop shell renders three Mermaid fences under its CSP");
  console.log(JSON.stringify({ cases: report.compatibility.length, behavior: report.behavior, bundles: report.bundles, benchmark: report.benchmark.map(({ name, medianMs, speedup }) => ({ name, medianMs, speedup })) }, null, 2));
}).then(async () => {
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2) + "\n");
  clearTimeout(timeout);
  await server?.close();
  app.exit(0);
}).catch(async error => {
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify({ ...report, failure: String(error) }, null, 2) + "\n");
  console.error(error);
  await server?.close();
  app.exit(1);
});
