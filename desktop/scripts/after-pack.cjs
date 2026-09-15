const { execFileSync, spawnSync } = require("node:child_process");
const { join } = require("node:path");

const REMOVED_MAC_PERMISSIONS = [
  "NSMicrophoneUsageDescription",
  "NSSpeechRecognitionUsageDescription",
];

module.exports = async function afterPack(context) {
  if (context.electronPlatformName !== "darwin") return;

  const plist = join(
    context.appOutDir,
    `${context.packager.appInfo.productFilename}.app`,
    "Contents",
    "Info.plist",
  );
  for (const key of REMOVED_MAC_PERMISSIONS) {
    const probe = spawnSync(
      "/usr/bin/plutil",
      ["-extract", key, "raw", "-o", "-", plist],
      { encoding: "utf8" },
    );
    if (probe.status !== 0) continue;
    execFileSync("/usr/bin/plutil", ["-remove", key, plist]);
    const verify = spawnSync(
      "/usr/bin/plutil",
      ["-extract", key, "raw", "-o", "-", plist],
      { stdio: "ignore" },
    );
    if (verify.status === 0) {
      throw new Error(`failed to remove ${key} from ${plist}`);
    }
  }
};
