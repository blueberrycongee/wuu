// Browser PiP E2E launcher: bundles the TS entry (which imports src/main
// modules directly) with esbuild, then runs it under Electron.
// Usage: node scripts/browser-pip-e2e.cjs
const { spawnSync } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const os = require("node:os");

const desktopRoot = path.resolve(__dirname, "..");
const outDir = path.join(desktopRoot, "out-e2e");
const outFile = path.join(outDir, "browser-pip-e2e.main.mjs");

fs.mkdirSync(outDir, { recursive: true });

const esbuild = require("esbuild");
async function main() {
  await esbuild.build({
    entryPoints: [path.join(__dirname, "browser-pip-e2e-entry.ts")],
    bundle: true,
    platform: "node",
    format: "esm",
    target: "node22",
    outfile: outFile,
    external: ["electron"],
    plugins: [{
      name: "raw-svg",
      setup(build) {
        build.onResolve({ filter: /\.svg\?raw$/ }, (args) => ({
          path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: "raw-svg",
        }));
        build.onLoad({ filter: /.*/, namespace: "raw-svg" }, (args) => ({
          contents: fs.readFileSync(args.path, "utf8"), loader: "text",
        }));
      },
    }],
    logLevel: "warning",
  });

  if (process.env.WUU_BROWSER_PIP_BUILD_ONLY === "1") return;
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-browser-pip-"));
  try {
    const result = spawnSync(String(require("electron")), [
      ...(process.platform === "linux" ? ["--no-sandbox", ...(!process.env.DISPLAY ? ["--ozone-platform=headless", "--ozone-override-screen-size=1440,1000"] : [])] : []), outFile,
    ], { stdio: "inherit", env: { ...process.env, WUU_BROWSER_PIP_FIXTURE: fixture } });
    console.log(`browser-pip-e2e: Electron exited ${JSON.stringify({ status: result.status, signal: result.signal })}`);
    process.exitCode = result.status ?? 1;
    if (result.error) throw result.error;
    if (result.status === 0 && !fs.existsSync(path.join(fixture, "completed"))) throw new Error("Electron exited before browser PiP assertions completed");
  } finally {
    fs.rmSync(fixture, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}
main().catch((error) => { console.error(error); process.exit(1); });
