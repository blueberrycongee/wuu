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

// Directory-only builds must embed updater configuration before the app is signed.
test("signed app checkpoint verifies runtime payloads and includes the update feed", async () => {
  const dir = mkdtempSync(join(tmpdir(), "wuu-update-feed-test-"));
  try {
    const source = join(dir, "source");
    const files = {
      "package.json": JSON.stringify({ version: "2026.10.19", main: "out/main/index.js" }),
      "out/main/index.js": "// main",
      "out/preload/index.cjs": "// preload",
      "out/renderer/index.html": "<!doctype html>",
      "out/renderer/THIRD-PARTY-LICENSES.txt": "monaco-editor@1 pdfjs-dist@1 pele@1 blobatar@1",
      "node_modules/node-pty/package.json": "{}",
      "node_modules/node-pty/build/Release/pty.node": "fixture binding",
      "node_modules/electron-updater/package.json": "{}",
    };
    for (const worker of ["editor.worker", "ts.worker", "pdf.worker"]) files[`out/renderer/assets/${worker}-fixture.js`] = "// worker";
    for (const [file, contents] of Object.entries(files)) {
      mkdirSync(dirname(join(source, file)), { recursive: true });
      writeFileSync(join(source, file), contents);
    }
    mkdirSync(join(dir, "bin"));
    writeFileSync(join(dir, "bin/wuu-core"), "fixture core");
    await require("@electron/asar").createPackageWithOptions(source, join(dir, "app.asar"), { unpack: "**/*.node" });
    // Exercise the real ASAR lookup implementation with Windows path semantics,
    // even on Linux/macOS CI. Slash-only nested reads fail in asar 3.4.1.
    const { createRequire } = require("node:module");
    const { runInNewContext } = require("node:vm");
    const nativePath = require("node:path");
    const asar = require("@electron/asar");
    const filesystemModule = require.resolve("@electron/asar/lib/filesystem.js");
    const filesystemExports = {};
    const filesystemRequire = createRequire(filesystemModule);
    runInNewContext(readFileSync(filesystemModule, "utf8"), {
      exports: filesystemExports,
      require: name => name === "path" ? nativePath.win32 : filesystemRequire(name),
      Buffer, process,
    });
    const windowsFilesystem = new filesystemExports.Filesystem(dir);
    windowsFilesystem.setHeader(asar.getRawHeader(join(dir, "app.asar")).header, 0);
    const windowsAsar = { ...asar };
    for (const method of ["extractFile", "statFile"]) {
      windowsAsar[method] = (archive, file) => {
        windowsFilesystem.getFile(file);
        return asar[method](archive, nativePath.normalize(file.replaceAll("\\", "/")));
      };
    }
    const smokeModule = { exports: {} };
    runInNewContext(readFileSync(join(__dirname, "packaged-runtime-smoke.cjs"), "utf8"), {
      module: smokeModule,
      require: name => name === "@electron/asar" ? windowsAsar
        : name === "node:path" ? { ...nativePath, normalize: nativePath.win32.normalize } : require(name),
      Buffer, process,
    });
    assert.doesNotThrow(() => smokeModule.exports.verifyResources(dir));
    const rendererFiles = windowsFilesystem.getHeader().files.out.files.renderer.files;
    const notice = rendererFiles["THIRD-PARTY-LICENSES.txt"];
    delete rendererFiles["THIRD-PARTY-LICENSES.txt"];
    assert.throws(() => smokeModule.exports.verifyResources(dir), /THIRD-PARTY-LICENSES/);
    rendererFiles["THIRD-PARTY-LICENSES.txt"] = notice;
    const runHook = () => execFileSync(process.execPath, ["-e", `
      const fs = require("node:fs");
      const path = require("node:path");
      require("node:child_process").execFileSync = () => "A".repeat(40) + " Developer ID Application:";
      const config = require("./scripts/release-builder.cjs");
      const resources = process.env.TEST_RESOURCES;
      Promise.resolve(config.afterPack({ appOutDir: resources, electronPlatformName: "darwin", packager: { getResourcesDir: p => p } }))
        .then(() => {
          const feed = require("js-yaml").load(fs.readFileSync(path.join(resources, "app-update.yml"), "utf8"));
          require("node:assert/strict").deepEqual(feed, config.publish);
        }).catch(error => { console.error(error); process.exitCode = 1; });
    `], { cwd: dirname(__dirname), env: { ...process.env, WUU_RELEASE: "1",
      WUU_RELEASE_SIGN_ID: "A".repeat(40), WUU_RELEASE_TEAM_ID: "A".repeat(10),
      APPLE_API_KEY: "/temporary/key", APPLE_API_KEY_ID: "key", APPLE_API_ISSUER: "issuer",
      TEST_RESOURCES: dir }, stdio: "pipe" });
    runHook();
    rmSync(join(dir, "app.asar"));
    assert.throws(runHook, /app\.asar/);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
