const {
  chmodSync,
  copyFileSync,
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  statSync,
  unlinkSync,
  writeFileSync,
} = require("node:fs");
const { createHash } = require("node:crypto");
const { join, resolve } = require("node:path");
const { spawnSync } = require("node:child_process");

if (process.platform !== "darwin") {
  console.log("skipping cua-mac helper build outside macOS");
  process.exit(0);
}

const desktopRoot = resolve(__dirname, "..");
const outDir = join(desktopRoot, "build", "bin");
const cuaOutputs = [
  join(outDir, "wuu-cua-mac"),
  join(outDir, "wuu-cua-mac-pip"),
  join(outDir, "wuu-cua-mac.build.json"),
];

if (process.env.WUU_SKIP_CUA_MAC === "1") {
  for (const output of cuaOutputs) removeOutput(output);
  console.log("skipping cua-mac helper build for this release");
  process.exit(0);
}

const packageRoot = join(desktopRoot, "native", "cua-mac");
const source = join(packageRoot, ".build", "release", "wuu-cua-mac");
const destination = join(outDir, "wuu-cua-mac");
const pipDestination = join(outDir, "wuu-cua-mac-pip");
const buildInfo = join(outDir, "wuu-cua-mac.build.json");
const signingIdentity = process.env.WUU_CUA_MAC_SIGN_ID || process.env.WUU_RELEASE_SIGN_ID || "-";

run("swift", ["build", "-c", "release", "--package-path", packageRoot]);
if (!existsSync(source)) {
  throw new Error(`Swift build did not produce ${source}`);
}
const sourceHash = createHash("sha256").update(readFileSync(source)).digest("hex");
if (outputsAreCurrent(sourceHash, signingIdentity)) {
  console.log(`reusing unchanged ${destination} and ${pipDestination}`);
  process.exit(0);
}
mkdirSync(outDir, { recursive: true });
removeOutput(destination);
removeOutput(pipDestination);
copyFileSync(source, destination);
chmodSync(destination, 0o755);

// The development launcher supplies WUU_CUA_MAC_SIGN_ID from a stable local
// certificate. Release builds use the persistent release identity; local packs
// may use ad-hoc signing for tests.
run("codesign", [
  "--force",
  "--sign",
  signingIdentity,
  ...(process.env.WUU_RELEASE_KEYCHAIN ? ["--keychain", process.env.WUU_RELEASE_KEYCHAIN] : []),
  "--identifier",
  "com.blueberrycongee.wuu.cua-mac",
  destination,
]);
// replayd identifies capture clients by executable path on current macOS
// releases. Keep the MCP server and live PiP on separate physical files so an
// observation cannot invalidate the PiP stream's application connection. Copy
// after signing so both roles use the exact same signed bytes.
copyFileSync(destination, pipDestination);
chmodSync(pipDestination, 0o755);
const mcpStat = statSync(destination);
const pipStat = statSync(pipDestination);
if (lstatSync(pipDestination).isSymbolicLink() || (mcpStat.dev === pipStat.dev && mcpStat.ino === pipStat.ino)) {
  throw new Error("CUA MCP and PiP helpers must be separate physical files");
}
writeFileSync(buildInfo, `${JSON.stringify({ sourceHash, signingIdentity })}\n`);

console.log(`built ${destination} and ${pipDestination}`);

function removeOutput(path) {
  try {
    unlinkSync(path);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
}

function outputsAreCurrent(sourceHash, signingIdentity) {
  if (!existsSync(destination) || !existsSync(pipDestination) || !existsSync(buildInfo)) {
    return false;
  }
  try {
    const info = JSON.parse(readFileSync(buildInfo, "utf8"));
    if (info.sourceHash !== sourceHash || info.signingIdentity !== signingIdentity) {
      return false;
    }
    const mcpStat = statSync(destination);
    const pipStat = statSync(pipDestination);
    if (
      lstatSync(pipDestination).isSymbolicLink()
      || (mcpStat.dev === pipStat.dev && mcpStat.ino === pipStat.ino)
    ) {
      return false;
    }
    return signatureIsValid(destination) && signatureIsValid(pipDestination);
  } catch {
    return false;
  }
}

function signatureIsValid(path) {
  return spawnSync("codesign", ["--verify", "--strict", path], {
    cwd: desktopRoot,
    stdio: "ignore",
  }).status === 0;
}

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: desktopRoot,
    env: process.env,
    encoding: "utf8",
    stdio: "inherit",
  });
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}
