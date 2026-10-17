const { execFileSync } = require("node:child_process");
const { rmSync } = require("node:fs");
const { join } = require("node:path");

if (process.platform !== "linux" || process.arch !== "x64") {
  throw new Error("Linux releases require a native Linux x64 builder");
}
const env = { ...process.env, VITE_ENABLE_ACCOUNT: "false", VITE_ENABLE_REMOTE_CONTROL: "false" };
const run = (command, args) => execFileSync(command, args, { stdio: "inherit", env });
const commit = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
if (process.env.WUU_RELEASE_SOURCE_COMMIT && process.env.WUU_RELEASE_SOURCE_COMMIT !== commit) {
  throw new Error("Linux release source commit mismatch");
}
run(process.execPath, ["../scripts/release-version.mjs", "check", process.env.RELEASE_TAG || ""]);
// CI retains the verified Linux distribution independently of Apple's checkpoints.
rmSync("release", { recursive: true, force: true });
for (const script of ["build:core", "build:ripgrep", "build"]) run("npm", ["run", script]);
run("node_modules/.bin/electron-builder", [
  "--config", join(__dirname, "release-linux-builder.cjs"), "--linux", "AppImage", "deb", "--x64",
  "--publish=never", "--config.electronDist=node_modules/electron/dist",
]);
run(process.execPath, [join(__dirname, "verify-linux-release.cjs")]);
