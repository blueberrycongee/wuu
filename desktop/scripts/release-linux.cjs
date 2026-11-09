const { execFileSync } = require("node:child_process");
const { rmSync } = require("node:fs");
const { join } = require("node:path");
const { runLinuxBuild, runPhase } = require("./build-linux-ci.cjs");

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
// Experimental Linux packages remain separate from the macOS release workflow.
rmSync("release", { recursive: true, force: true });
runPhase("prerequisites", () => run(process.execPath, [join(__dirname, "build-package-prerequisites.cjs")]));
runPhase("builder", () => runLinuxBuild(process.env.GITHUB_EVENT_NAME, { env }));
runPhase("verification", () => run(process.execPath, [join(__dirname, "verify-linux-release.cjs")]));
