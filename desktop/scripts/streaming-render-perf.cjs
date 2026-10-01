// Run with: electron [--no-sandbox --ozone-platform=headless] scripts/streaming-render-perf.cjs
// Optional WUU_STREAM_PERF_BASE_REF builds StreamingMarkdown.tsx from that git ref.
// Compare alternating baseline/candidate runs; timings are diagnostics, not CI gates.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const { app, BrowserWindow } = require("electron");
const esbuild = require("esbuild");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const output = path.resolve(process.env.WUU_STREAM_PERF_OUTPUT || path.join(desktopRoot, "out", "streaming-performance"));
const requestedRef = process.env.WUU_STREAM_PERF_BASE_REF;
const baseRef = requestedRef
  ? execFileSync("git", ["rev-parse", "--verify", `${requestedRef}^{commit}`], { cwd: repoRoot, encoding: "utf8" }).trim()
  : undefined;
const sourcePath = "desktop/src/renderer/StreamingMarkdown.tsx";
const source = baseRef
  ? execFileSync("git", ["show", `${baseRef}:${sourcePath}`], { cwd: repoRoot, encoding: "utf8" })
  : fs.readFileSync(path.join(repoRoot, sourcePath), "utf8");
const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const median = (values) => {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
};
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "profile"));
app.disableHardwareAcceleration();

app.whenReady().then(async () => {
  await esbuild.build({
    entryPoints: [path.join(desktopRoot, "dev/streaming-performance/entry.tsx")],
    outfile: path.join(output, "bundle.js"),
    bundle: true,
    platform: "browser",
    format: "iife",
    jsx: "automatic",
    define: { "process.env.NODE_ENV": '"production"', "import.meta.env.DEV": "false", "import.meta.env.MODE": '"production"' },
    loader: { ".svg": "dataurl", ".woff2": "file", ".woff": "file", ".png": "file" },
    plugins: [{
      name: "measured-streaming-source",
      setup(build) {
        build.onLoad({ filter: /[/\\]StreamingMarkdown\.tsx$/ }, () => ({ contents: source, loader: "tsx" }));
      },
    }],
    logLevel: "warning",
  });
  fs.writeFileSync(path.join(output, "index.html"), '<!doctype html><html data-theme="light"><head><meta charset="utf-8"><link rel="stylesheet" href="bundle.css"></head><body><script src="bundle.js"></script></body></html>');
  const win = new BrowserWindow({
    show: false, width: 1100, height: 820,
    webPreferences: { offscreen: true, backgroundThrottling: false },
  });
  win.webContents.session.webRequest.onBeforeRequest((details, callback) =>
    callback({ cancel: !/^(file:|data:)/.test(details.url) }));
  await win.loadFile(path.join(output, "index.html"));
  await win.webContents.executeJavaScript("document.fonts.ready");
  const run = (script) => win.webContents.executeJavaScript(script);
  const rounds = [];
  for (let round = 0; round < 7; round++) {
    for (const blocks of [25, 100, 400]) {
      const characters = await run(`streamingPerformance.mount(${blocks})`);
      await run("streamingPerformance.commit(10)");
      const samples = await run("streamingPerformance.commit(80)");
      const checks = await run("streamingPerformance.finish()");
      assert.ok(checks.sameFirstBlock && checks.completeTail, "Stream output or existing DOM changed");
      rounds.push({ round, blocks, characters, samples, checks });
    }
  }
  // Allocation sampling is a separate pass: its overhead must not enter timings.
  win.webContents.debugger.attach("1.3");
  const allocationRounds = [];
  for (let round = 0; round < 5; round++) {
    await run("streamingPerformance.mount(400)");
    await run("streamingPerformance.commit(10)");
    await win.webContents.debugger.sendCommand("HeapProfiler.startSampling", {
      samplingInterval: 128,
      includeObjectsCollectedByMajorGC: true,
      includeObjectsCollectedByMinorGC: true,
    });
    await run("streamingPerformance.commit(80)");
    const { profile } = await win.webContents.debugger.sendCommand("HeapProfiler.stopSampling");
    const allocated = (node) => node.selfSize + node.children.reduce((total, child) => total + allocated(child), 0);
    allocationRounds.push({ round, sampledAllocatedBytes: allocated(profile.head) });
    const checks = await run("streamingPerformance.finish()");
    assert.ok(checks.sameFirstBlock && checks.completeTail);
  }
  const report = {
    scope: "Production React synchronous tail commits in offscreen Electron with real Markdown and CSS; no inference, provider timing, or physical paint latency. Allocation sampling is a separate diagnostic pass.",
    sourceRef: baseRef || execFileSync("git", ["rev-parse", "HEAD"], { cwd: repoRoot, encoding: "utf8" }).trim(),
    sourceSha256: sha256(source),
    bundleSha256: sha256(fs.readFileSync(path.join(output, "bundle.js"))),
    versions: process.versions,
    iterations: 80, warmup: 10, rounds, allocationRounds,
    summary: [25, 100, 400].map((blocks) => {
      const selected = rounds.filter((row) => row.blocks === blocks);
      return {
        blocks, characters: selected[0].characters,
        medianCommitMs: median(selected.flatMap((row) => row.samples)),
        medianBatchMs: median(selected.map((row) => row.samples.reduce((sum, value) => sum + value, 0))),
      };
    }),
    medianSampledAllocatedBytes: median(allocationRounds.map((row) => row.sampledAllocatedBytes)),
  };
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ summary: report.summary, medianSampledAllocatedBytes: report.medianSampledAllocatedBytes }, null, 2));
  win.destroy();
  app.quit();
}).catch((error) => { console.error(error); app.exit(1); });
