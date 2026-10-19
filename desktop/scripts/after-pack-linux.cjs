const { copyFileSync } = require("node:fs");
const { join } = require("node:path");
const { verifyResources } = require("./packaged-runtime-smoke.cjs");

module.exports = async function afterPackLinux(context) {
  const resources = context.packager.getResourcesDir(context.appOutDir);
  // Catch missing runtime payloads and duplicate renderer dependencies before signing.
  verifyResources(resources);
  if (context.electronPlatformName !== "linux") return;
  // The builder's default AppRun silently disables Chromium's sandbox when its
  // probe fails. Copying our launcher into the app directory replaces that
  // generated launcher when electron-builder stages the AppImage.
  copyFileSync(join(__dirname, "linux-AppRun"), join(context.appOutDir, "AppRun"));
};
