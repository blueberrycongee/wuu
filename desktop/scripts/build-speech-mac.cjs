const {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  unlinkSync,
  writeFileSync,
} = require("node:fs");
const { createHash } = require("node:crypto");
const { join, resolve } = require("node:path");
const { spawnSync } = require("node:child_process");

if (process.platform !== "darwin") {
  console.log("skipping speech-mac helper build outside macOS");
  process.exit(0);
}

const desktopRoot = resolve(__dirname, "..");
const packageRoot = join(desktopRoot, "native", "speech-mac");
const source = join(packageRoot, ".build", "release", "wuu-speech-mac");
const outDir = join(desktopRoot, "build", "bin");
const destination = join(outDir, "wuu-speech-mac");
const buildInfo = join(outDir, "wuu-speech-mac.build.json");
const signingIdentity = process.env.WUU_SPEECH_MAC_SIGN_ID || "-";

run("swift", ["build", "-c", "release", "--package-path", packageRoot]);
if (!existsSync(source)) {
  throw new Error(`Swift build did not produce ${source}`);
}
const sourceHash = createHash("sha256").update(readFileSync(source)).digest("hex");
if (outputIsCurrent(sourceHash, signingIdentity)) {
  console.log(`reusing unchanged ${destination}`);
  process.exit(0);
}
mkdirSync(outDir, { recursive: true });
try {
  unlinkSync(destination);
} catch (error) {
  if (error.code !== "ENOENT") throw error;
}
copyFileSync(source, destination);
chmodSync(destination, 0o755);
run("codesign", [
  "--force",
  "--sign",
  signingIdentity,
  "--identifier",
  "com.blueberrycongee.wuu.speech-mac",
  destination,
]);
writeFileSync(buildInfo, `${JSON.stringify({ sourceHash, signingIdentity })}\n`);

console.log(`built ${destination}`);

function outputIsCurrent(sourceHash, signingIdentity) {
  if (!existsSync(destination) || !existsSync(buildInfo)) return false;
  try {
    const info = JSON.parse(readFileSync(buildInfo, "utf8"));
    if (info.sourceHash !== sourceHash || info.signingIdentity !== signingIdentity) {
      return false;
    }
    return spawnSync("codesign", ["--verify", "--strict", destination], {
      cwd: desktopRoot,
      stdio: "ignore",
    }).status === 0;
  } catch {
    return false;
  }
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
