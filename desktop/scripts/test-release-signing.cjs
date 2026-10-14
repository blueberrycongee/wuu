const assert = require("node:assert/strict");
const { test } = require("node:test");
const { execFileSync } = require("node:child_process");
const { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, copyFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { dirname, join } = require("node:path");
const { releaseSigningIdentity, releaseCredentials } = require("./check-release-signing.cjs");

test("release credentials reject every missing secret before building", () => {
  const credentials = {
    WUU_RELEASE_SIGN_ID: "A".repeat(40), WUU_RELEASE_TEAM_ID: "A".repeat(10),
    APPLE_API_KEY: "/temporary/AuthKey.p8", APPLE_API_KEY_ID: "B".repeat(10),
    APPLE_API_ISSUER: "00000000-0000-0000-0000-000000000000",
  };
  assert.doesNotThrow(() => releaseCredentials(credentials));
  for (const key of Object.keys(credentials)) {
    assert.throws(() => releaseCredentials({ ...credentials, [key]: "" }), undefined, key);
  }
});
// Mirror the bundled ripgrep that verify-mac-release.cjs requires in packaged apps.
function addBundledRipgrep(dir, contents) {
  const tools = join(contents, "Resources", "tools");
  const licenses = join(contents, "Resources", "licenses", "ripgrep");
  mkdirSync(tools, { recursive: true });
  mkdirSync(licenses, { recursive: true });
  const source = join(dir, "rg.c");
  writeFileSync(source, "int main(void) { return 0; }\n");
  execFileSync("clang", [source, "-o", join(tools, "rg")]);
  writeFileSync(join(licenses, "LICENSE-MIT"), "MIT\n");
}

// Keep the existing package fixtures aligned with the snapshot release resources.
function addBundledAppSnapshot(dir, contents) {
  const resources = join(contents, "Resources", "app-context");
  const helper = join(resources, "Wuu App Snapshot.app", "Contents");
  mkdirSync(join(helper, "MacOS"), { recursive: true });
  writeFileSync(join(helper, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.blueberrycongee.wuu.app-context</string><key>CFBundleExecutable</key><string>wuu-app-context</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`);
  const source = join(dir, "snapshot.c");
  writeFileSync(source, "int main(void) { return 0; }\n");
  execFileSync("clang", [source, "-o", join(helper, "MacOS", "wuu-app-context")]);
  execFileSync("clang", ["-bundle", source, "-o", join(resources, "capture-target.node")]);
  copyFileSync(join(dirname(require.resolve("node-api-headers")), "LICENSE"), join(resources, "LICENSE.node-api-headers.txt"));
}

test("certificate-backed signing rejects missing or ad-hoc identity", () => {
  for (const value of [undefined, "-", "Wuu Dev Signing"]) {
    assert.throws(() => releaseSigningIdentity({ WUU_RELEASE_SIGN_ID: value }));
  }
});

test("certificate-free preview signs, verifies, and rejects tampered resources", { skip: process.platform !== "darwin" }, async () => {
  const dir = mkdtempSync(join(tmpdir(), "wuu-adhoc-signing-test-"));
  const saved = { id: process.env.WUU_RELEASE_SIGN_ID, keychain: process.env.WUU_RELEASE_KEYCHAIN };
  try {
    delete process.env.WUU_RELEASE_SIGN_ID;
    delete process.env.WUU_RELEASE_KEYCHAIN;
    const app = join(dir, "wuu.app");
    const contents = join(app, "Contents");
    const bin = join(contents, "Resources", "bin");
    mkdirSync(bin, { recursive: true });
    mkdirSync(join(contents, "MacOS"));
    writeFileSync(join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.blueberrycongee.wuu</string><key>CFBundleExecutable</key><string>wuu</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`);
    const source = join(dir, "main.c");
    const executable = join(contents, "MacOS", "wuu");
    writeFileSync(source, "int main(void) { return 0; }\n");
    execFileSync("clang", [source, "-o", executable]);
    copyFileSync(executable, join(bin, "wuu-core"));
    addBundledRipgrep(dir, contents);
    addBundledAppSnapshot(dir, contents);
    const resource = join(contents, "Resources", "data.txt");
    writeFileSync(resource, "original");
    await require("./sign-mac.cjs")({ app, platform: "darwin", version: "44.1.0" });
    const verify = () => execFileSync(process.execPath, [join(__dirname, "verify-mac-release.cjs"), app], {
      env: { ...process.env, WUU_SKIP_CUA_MAC: "1" }, stdio: "pipe",
    });
    verify();
    writeFileSync(resource, "tampered");
    assert.throws(verify);
  } finally {
    if (saved.id === undefined) delete process.env.WUU_RELEASE_SIGN_ID; else process.env.WUU_RELEASE_SIGN_ID = saved.id;
    if (saved.keychain === undefined) delete process.env.WUU_RELEASE_KEYCHAIN; else process.env.WUU_RELEASE_KEYCHAIN = saved.keychain;
    rmSync(dir, { recursive: true, force: true });
  }
});
