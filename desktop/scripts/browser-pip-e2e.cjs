// Browser PiP E2E launcher: bundles the TS entry (which imports src/main
// modules directly) with esbuild, then runs it under Electron.
// Usage: node scripts/browser-pip-e2e.cjs
const { spawnSync } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");

const desktopRoot = path.resolve(__dirname, "..");
const outDir = path.join(desktopRoot, "out-e2e");
const outFile = path.join(outDir, "browser-pip-e2e.main.cjs");

fs.mkdirSync(outDir, { recursive: true });

const esbuild = require("esbuild");
async function main() {
await esbuild.build({
  entryPoints: [path.join(__dirname, "browser-pip-e2e-entry.ts")],
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node20",
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

const electronBinary = require("electron");
const result = spawnSync(String(electronBinary), [outFile], {
  stdio: "inherit",
  env: { ...process.env },
});
process.exit(result.status ?? 1);
}
main().catch((error) => { console.error(error); process.exit(1); });
