const { join } = require("node:path");
const { notarize } = require("@electron/notarize");
const { execFileSync } = require("node:child_process");
const { releaseCredentials } = require("./check-release-signing.cjs");

module.exports = async ({ appOutDir, packager }) => {
  releaseCredentials();
  const appPath = join(appOutDir, `${packager.appInfo.productFilename}.app`);
  execFileSync(process.execPath, [join(__dirname, "verify-mac-release.cjs"), appPath], { stdio: "inherit" });
  await notarize({
    appPath,
    appleApiKey: process.env.APPLE_API_KEY,
    appleApiKeyId: process.env.APPLE_API_KEY_ID,
    appleApiIssuer: process.env.APPLE_API_ISSUER,
  });
  execFileSync("xcrun", ["stapler", "validate", appPath], { stdio: "inherit" });
  execFileSync("spctl", ["--assess", "--type", "execute", "--verbose=4", appPath], { stdio: "inherit" });
};
