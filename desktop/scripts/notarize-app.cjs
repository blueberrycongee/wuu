const { join } = require("node:path");
const { mkdtempSync, rmSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { notarizeArchive } = require("./notarization.cjs");
const { execFileSync } = require("node:child_process");
const { releaseCredentials } = require("./check-release-signing.cjs");

module.exports = async ({ appOutDir, packager }) => {
  releaseCredentials();
  const appPath = join(appOutDir, `${packager.appInfo.productFilename}.app`);
  execFileSync(process.execPath, [join(__dirname, "verify-mac-release.cjs"), appPath], { stdio: "inherit" });
  const temp = mkdtempSync(join(tmpdir(), "wuu-notarize-"));
  try {
    const archive = join(temp, "wuu.zip");
    execFileSync("ditto", ["-c", "-k", "--sequesterRsrc", "--keepParent", appPath, archive]);
    await notarizeArchive(archive, join("release", "notarization", "app.json"));
    execFileSync("xcrun", ["stapler", "staple", appPath], { stdio: "inherit" });
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
  execFileSync("xcrun", ["stapler", "validate", appPath], { stdio: "inherit" });
  execFileSync("spctl", ["--assess", "--type", "execute", "--verbose=4", appPath], { stdio: "inherit" });
};
