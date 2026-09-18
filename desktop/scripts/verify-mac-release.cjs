const { execFileSync } = require("node:child_process");
const { statSync } = require("node:fs");
const { join, resolve } = require("node:path");
const { releaseSigningIdentity } = require("./check-release-signing.cjs");

const app = resolve(process.argv[2] || "release/mac-arm64/wuu.app");
const identity = releaseSigningIdentity();
const requirement = `certificate leaf = H"${identity}"`;
execFileSync("codesign", ["--verify", "--deep", "--strict", "-R", `=${requirement}`, app]);
const bin = join(app, "Contents", "Resources", "bin");
const skipCua = process.env.WUU_SKIP_CUA_MAC === "1";
const requiredBinaries = skipCua
  ? ["wuu-core"]
  : ["wuu-core", "wuu-cua-mac", "wuu-cua-mac-pip"];
for (const name of requiredBinaries) {
  const path = join(bin, name);
  if (!(statSync(path).mode & 0o111)) throw new Error(`${name} is not executable`);
  execFileSync("codesign", ["--verify", "--strict", "-R", `=${requirement}`, path]);
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
const bundleID = execFileSync("/usr/libexec/PlistBuddy", ["-c", "Print :CFBundleIdentifier", join(app, "Contents/Info.plist")], { encoding: "utf8" }).trim();
if (bundleID !== "com.blueberrycongee.wuu") throw new Error("Release bundle identity changed");
console.log(skipCua
  ? "Verified release signature, bundle identity, and absence of CUA executables."
  : "Verified release signature, bundle identity, and bundled CUA executables.");
