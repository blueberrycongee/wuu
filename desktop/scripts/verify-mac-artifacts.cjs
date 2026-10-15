const { execFileSync } = require("node:child_process");
const { mkdtempSync, readdirSync, rmSync, writeFileSync, readFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join, resolve } = require("node:path");
const { createHash } = require("node:crypto");
const { releaseCredentials, releaseSigningIdentity } = require("./check-release-signing.cjs");
const { notarizeArchive, stapleArtifact } = require("./notarization.cjs");
const { finalizeUpdateMetadata } = require("./update-metadata.cjs");

async function main() {
  releaseCredentials();
  if (process.env.WUU_RELEASE !== "1") throw new Error("Artifact verification requires release mode");
  const root = resolve("release");
  const version = require("../package.json").version;
  const dmg = join(root, `wuu-${version}-mac-arm64.dmg`);
  const zip = join(root, `wuu-${version}-mac-arm64.zip`);
  const run = (command, args) => execFileSync(command, args, { stdio: "inherit" });

  // Notarize the final disk image as well as the already-stapled application.
  run("codesign", ["--force", "--sign", releaseSigningIdentity(), ...(process.env.WUU_RELEASE_KEYCHAIN ? ["--keychain", process.env.WUU_RELEASE_KEYCHAIN] : []),
    "--timestamp", dmg]);
  const submission = await notarizeArchive(dmg, join(root, "notarization", "dmg.json"));
  await stapleArtifact(dmg);
  run("xcrun", ["stapler", "validate", dmg]);
  run("codesign", ["--verify", "--strict", "-R", `=anchor apple generic and certificate leaf = H"${releaseSigningIdentity()}"`, dmg]);
  run("spctl", ["--assess", "--type", "open", "--context", "context:primary-signature", "--verbose=4", dmg]);
  run("hdiutil", ["verify", dmg]);

  const dir = mkdtempSync(join(tmpdir(), "wuu-artifact-verification-"));
  const mount = join(dir, "mounted");
  let mounted = false;
  try {
    run("hdiutil", ["attach", "-readonly", "-nobrowse", "-mountpoint", mount, dmg]);
    mounted = true;
    run("ditto", ["-x", "-k", zip, join(dir, "zip")]);
    for (const [format, directory] of [["dmg", mount], ["zip", join(dir, "zip")]]) {
      const apps = readdirSync(directory).filter((name) => name.endsWith(".app"));
      if (apps.length !== 1 || apps[0] !== "wuu.app") throw new Error(`Unexpected ${format} contents`);
      const app = join(directory, apps[0]);
      run(process.execPath, [join(__dirname, "verify-mac-release.cjs"), app]);
      run("xcrun", ["stapler", "validate", app]);
      run("spctl", ["--assess", "--type", "execute", "--verbose=4", app]);
      const actual = execFileSync("/usr/libexec/PlistBuddy", ["-c", "Print :CFBundleShortVersionString", join(app, "Contents/Info.plist")], { encoding: "utf8" }).trim();
      if (actual !== version) throw new Error(`Unexpected ${format} app version`);
    }
    const updateManifests = finalizeUpdateMetadata(root, version);
    const evidence = {
      updateManifests,
      version, commit: process.env.GITHUB_SHA || null, notarization: submission.id,
      checks: ["Developer ID", "hardened runtime", "secure timestamp", "DMG ticket", "DMG Gatekeeper", "DMG app ticket and Gatekeeper", "ZIP app ticket and Gatekeeper"],
      sha256: Object.fromEntries([dmg, zip].map((path) => [path.split("/").pop(), createHash("sha256").update(readFileSync(path)).digest("hex")])),
    };
    writeFileSync(join(root, "verification.json"), JSON.stringify(evidence, null, 2) + "\n");
    console.log("Verified final DMG and ZIP applications; evidence: release/verification.json");
  } finally {
    if (mounted) run("hdiutil", ["detach", mount]);
    rmSync(dir, { recursive: true, force: true });
  }
}

main().catch((error) => { console.error(error.message); process.exitCode = 1; });
