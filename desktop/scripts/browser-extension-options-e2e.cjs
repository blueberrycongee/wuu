// Failure cases: settings must use the browser profile, never expose Wuu/Node,
// never escape to file/remote pages, and close when their extension is unloaded.
const { spawnSync } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const root = path.resolve(__dirname, "..");
const output = path.join(root, "out-e2e", "browser-extension-options.main.cjs");
fs.mkdirSync(path.dirname(output), { recursive: true });
require("esbuild").build({
  entryPoints: [path.join(__dirname, "browser-extension-options-e2e-entry.ts")],
  outfile: output, bundle: true, platform: "node", format: "cjs",
  external: ["electron"],
}).then(() => {
  const result = spawnSync(process.env.WUU_E2E_ELECTRON || String(require("electron")), [output], {
    stdio: "inherit", env: process.env,
  });
  process.exit(result.status ?? 1);
}).catch(error => { console.error(error); process.exit(1); });
