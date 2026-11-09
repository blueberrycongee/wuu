const { resolve } = require("node:path");
const { spawnSync } = require("node:child_process");

if (process.env.WUU_SKIP_CUA_MAC === "1") {
  console.log("skipping cua-mac tests for this release");
  process.exit(0);
}

if (process.platform !== "darwin") {
  console.log("skipping cua-mac tests outside macOS");
  process.exit(0);
}

const packageRoot = resolve(__dirname, "..", "native", "cua-mac");
run("swift", ["test"]);
run("swift", ["run", "cua-mac-protocol-tests"]);
run("node", ["ProtocolTests/stdio-smoke.mjs"]);

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: packageRoot,
    env: process.env,
    encoding: "utf8",
    stdio: "inherit",
  });
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}
