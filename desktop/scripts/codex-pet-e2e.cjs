// Codex pet E2E launcher: bundles the TS entry (which imports src/main
// modules directly) with esbuild, then runs it under Electron.
// Usage: node scripts/codex-pet-e2e.cjs
const { spawnSync } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const esbuild = require("esbuild");

const desktopRoot = path.resolve(__dirname, "..");
const outDir = path.join(desktopRoot, "out-e2e");
const outFile = path.join(outDir, "codex-pet-e2e.main.cjs");

async function main() {
  fs.mkdirSync(outDir, { recursive: true });
  await esbuild.build({
    entryPoints: [path.join(__dirname, "codex-pet-e2e-entry.ts")],
    bundle: true,
    platform: "node",
    format: "cjs",
    target: "node20",
    outfile: outFile,
    external: ["electron"],
    logLevel: "warning",
  });
  const result = spawnSync(String(require("electron")), [outFile], { stdio: "inherit", env: process.env });
  process.exit(result.status ?? 1);
}
main().catch((error) => { console.error(error); process.exit(1); });
