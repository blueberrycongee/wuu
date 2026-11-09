const { spawnSync } = require("node:child_process");
const { resolve } = require("node:path");
const root = resolve(__dirname, "../../clients/mobile-web");
const result = spawnSync(process.execPath, [resolve(root, "node_modules/vite/bin/vite.js"), "build", "--outDir", resolve(__dirname, "../build/mobile-web"), "--emptyOutDir"], { cwd: root, stdio: "inherit", env: process.env });
if (result.error) throw result.error;
process.exit(result.status ?? 1);
