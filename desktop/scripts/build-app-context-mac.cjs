const { chmodSync, copyFileSync, mkdirSync, writeFileSync } = require("node:fs");
const { dirname, join, resolve } = require("node:path");
const { execFileSync } = require("node:child_process");

if (process.platform !== "darwin") {
  console.log("App snapshots are available only on macOS; helper build skipped.");
  process.exit(0);
}
const desktop = resolve(__dirname, "..");
const source = join(desktop, "native", "app-context-mac");
const arch = process.arch === "arm64" ? "arm64" : process.arch === "x64" ? "x86_64" : undefined;
if (!arch) throw new Error(`Unsupported macOS architecture: ${process.arch}`);
execFileSync("swift", ["build", "-c", "release", "--arch", arch, "--package-path", source], { stdio: "inherit" });
const bundle = join(desktop, "build", "app-context", "Wuu App Snapshot.app");
const contents = join(bundle, "Contents");
const executable = join(contents, "MacOS", "wuu-app-context");
mkdirSync(join(contents, "MacOS"), { recursive: true });
copyFileSync(join(source, ".build", "release", "wuu-app-context"), executable);
chmodSync(executable, 0o755);
writeFileSync(join(contents, "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.blueberrycongee.wuu.app-context</string>
<key>CFBundleName</key><string>Wuu App Snapshot</string>
<key>CFBundleExecutable</key><string>wuu-app-context</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>LSUIElement</key><true/>
<key>NSScreenCaptureUsageDescription</key><string>Capture one app window when you press the snapshot shortcut.</string>
</dict></plist>\n`);
const bridge = join(desktop, "build", "app-context", "capture-target.node");
// Preserve the complete upstream notice beside the distributed native bridge.
copyFileSync(join(dirname(require.resolve("node-api-headers")), "LICENSE"),
  join(desktop, "build", "app-context", "LICENSE.node-api-headers.txt"));
execFileSync("xcrun", ["clang++", "-std=c++17", "-fobjc-arc", "-bundle", "-undefined", "dynamic_lookup",
  "-mmacosx-version-min=14.0", "-arch", arch, "-DNAPI_VERSION=8", "-I", require("node-api-headers").include_dir,
  "-framework", "AppKit", "-framework", "CoreGraphics", join(source, "bridge", "CaptureTarget.mm"), "-o", bridge], { stdio: "inherit" });
for (const binary of [executable, bridge]) execFileSync("lipo", ["-verify_arch", arch, binary], { stdio: "inherit" });
const identity = process.env.WUU_APP_CONTEXT_SIGN_ID || process.env.WUU_RELEASE_SIGN_ID || "-";
for (const target of [bridge, bundle]) {
  execFileSync("codesign", ["--force", "--sign", identity, ...(process.env.WUU_RELEASE_KEYCHAIN ? ["--keychain", process.env.WUU_RELEASE_KEYCHAIN] : []), target], { stdio: "inherit" });
  execFileSync("codesign", ["--verify", "--deep", "--strict", target], { stdio: "inherit" });
}
if (typeof require(bridge).pinTarget !== "function") throw new Error("Capture target bridge did not load.");
console.log("Built the read-only App Snapshot helper. Computer Use packaging is unchanged.");
