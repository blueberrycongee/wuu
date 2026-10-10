// Build the real host fixture, then run Electron in a graphical session.
const { spawnSync } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const root = path.resolve(__dirname, "..");
const output = path.join(root, "out-e2e", "browser-visual.main.cjs");
async function main() {
  fs.mkdirSync(path.dirname(output), { recursive: true });
  await require("esbuild").build({ entryPoints: [path.join(__dirname, "browser-visual-e2e-entry.ts")], bundle: true, platform: "node", format: "cjs", target: "node22", outfile: output, external: ["electron"] });
  if (process.env.WUU_BROWSER_E2E_BUILD_ONLY === "1") return;
  const args = ["--force-device-scale-factor=2", "--disable-features=OverlayScrollbar,FluentOverlayScrollbar", ...(process.env.CI ? ["--no-sandbox"] : []), output];
  const result = spawnSync(String(require("electron")), args, { cwd: root, stdio: "inherit", env: process.env });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
}
main().catch(error => { console.error(error); process.exitCode = 1; });
