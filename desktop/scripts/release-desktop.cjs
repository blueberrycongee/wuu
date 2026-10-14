const { execFileSync } = require("node:child_process");
const { rmSync } = require("node:fs");
const { releaseCredentials, checkReleaseSigning } = require("./check-release-signing.cjs");

releaseCredentials();
checkReleaseSigning();
const env = { ...process.env, WUU_RELEASE: "1", WUU_SKIP_CUA_MAC: "1", WUU_ENABLE_CUA_MAC: "0",
  CSC_IDENTITY_AUTO_DISCOVERY: "false", CSC_KEYCHAIN: process.env.WUU_RELEASE_KEYCHAIN };
// Old archives must never be uploaded alongside the verified build.
rmSync("release", { recursive: true, force: true });
for (const script of ["build:core", "build:ripgrep", "build:app-context-mac", "build"]) {
  execFileSync("npm", ["run", script], { env, stdio: "inherit" });
}
execFileSync("node_modules/.bin/electron-builder", ["--config", "scripts/release-builder.cjs", "--mac", "dmg", "zip",
  "--arm64", "--publish=never", "--config.electronDist=node_modules/electron/dist"], { env, stdio: "inherit" });
execFileSync(process.execPath, ["scripts/verify-mac-artifacts.cjs"], { env, stdio: "inherit" });
