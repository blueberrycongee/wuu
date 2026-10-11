// Build the production Go/Electron browser boundary fixture, then run with a
// display (CI uses xvfb). Only a local scripted provider and local pages are used.
const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const root = path.resolve(__dirname, "..");
const output = path.join(root, "out-e2e", "browser-task-ownership.mjs");
async function main() {
  fs.mkdirSync(path.dirname(output), { recursive: true });
  await require("esbuild").build({
    entryPoints: [path.join(__dirname, "browser-task-ownership-e2e-entry.ts")],
    outfile: output, bundle: true, platform: "node", format: "esm", target: "node22",
    external: ["electron"],
    plugins: [{ name: "svg", setup(build) {
      build.onResolve({ filter: /\.svg\?raw$/ }, args => ({ path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: "svg" }));
      build.onLoad({ filter: /.*/, namespace: "svg" }, args => ({ contents: fs.readFileSync(args.path, "utf8"), loader: "text" }));
    } }],
  });
  if (process.env.WUU_BROWSER_OWNERSHIP_BUILD_ONLY === "1") return;
  const result = spawnSync(String(require("electron")), [
    ...(process.platform === "linux" ? ["--no-sandbox"] : []), output,
  ], { stdio: "inherit", env: process.env });
  process.exit(result.status ?? 1);
}
main().catch(error => { console.error(error); process.exit(1); });
