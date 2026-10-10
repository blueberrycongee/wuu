const { execFileSync } = require("node:child_process");
const { statSync } = require("node:fs");
const { join, resolve } = require("node:path");
const { releaseSigningIdentity } = require("./check-release-signing.cjs");

const app = resolve(process.argv[2] || "release/mac-arm64/wuu.app");
// Certificate-backed local builds still verify their pinned identity. Public
// previews use ad-hoc signatures, which seal code without asserting a publisher.
const requirementArgs = process.env.WUU_RELEASE_SIGN_ID
  ? ["-R", `=certificate leaf = H"${releaseSigningIdentity()}"`]
  : [];
execFileSync("codesign", ["--verify", "--deep", "--strict", ...requirementArgs, app]);
const bin = join(app, "Contents", "Resources", "bin");
const skipCua = process.env.WUU_SKIP_CUA_MAC === "1";
const requiredBinaries = skipCua
  ? ["wuu-core"]
  : ["wuu-core", "wuu-cua-mac", "wuu-cua-mac-pip"];
for (const name of requiredBinaries) {
  const path = join(bin, name);
  if (!(statSync(path).mode & 0o111)) throw new Error(`${name} is not executable`);
  execFileSync("codesign", ["--verify", "--strict", ...requirementArgs, path]);
}
if (skipCua) {
  for (const name of ["wuu-cua-mac", "wuu-cua-mac-pip"]) {
    try {
      statSync(join(bin, name));
      throw new Error(`release must not include ${name}`);
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
  }
} else {
  const main = statSync(join(bin, "wuu-cua-mac"));
  const pip = statSync(join(bin, "wuu-cua-mac-pip"));
  if (main.dev === pip.dev && main.ino === pip.ino) throw new Error("CUA capture roles must use separate files");
}
const appSnapshot = join(app, "Contents", "Resources", "app-context", "Wuu App Snapshot.app");
const appSnapshotExecutable = join(appSnapshot, "Contents", "MacOS", "wuu-app-context");
if (!(statSync(appSnapshotExecutable).mode & 0o111)) throw new Error("App Snapshot helper is not executable");
execFileSync("codesign", ["--verify", "--deep", "--strict", ...requirementArgs, appSnapshot]);
const captureBridge = join(app, "Contents", "Resources", "app-context", "capture-target.node");
const bridgeNotice = statSync(join(app, "Contents", "Resources", "app-context", "LICENSE.node-api-headers.txt"));
if (!bridgeNotice.isFile() || bridgeNotice.size === 0) throw new Error("Capture bridge dependency license is missing");
execFileSync("codesign", ["--verify", "--strict", ...requirementArgs, captureBridge]);
const mainExecutable = execFileSync("/usr/libexec/PlistBuddy", ["-c", "Print :CFBundleExecutable", join(app, "Contents/Info.plist")], { encoding: "utf8" }).trim();
const appArchitectures = execFileSync("lipo", ["-archs", join(app, "Contents", "MacOS", mainExecutable)], { encoding: "utf8" }).trim().split(/\s+/);
for (const binary of [appSnapshotExecutable, captureBridge]) execFileSync("lipo", [binary, "-verify_arch", ...appArchitectures]);
const bundleID = execFileSync("/usr/libexec/PlistBuddy", ["-c", "Print :CFBundleIdentifier", join(app, "Contents/Info.plist")], { encoding: "utf8" }).trim();
if (bundleID !== "com.blueberrycongee.wuu") throw new Error("Release bundle identity changed");
console.log(skipCua
  ? "Verified release signature, bundle identity, and absence of CUA executables."
  : "Verified release signature, bundle identity, and bundled CUA executables.");
