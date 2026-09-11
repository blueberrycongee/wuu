const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join, resolve } = require("node:path");
const packageJSON = require("../package.json");
const {
  launchEnvironment,
  normalizeElectronArguments,
  processIDForLaunchToken,
} = require("./launch-electron-via-open.cjs");
const {
  electronInstallerScriptFromManifest,
  ensureSourceElectronApp,
  ensureSourceForStaleDevHost,
  helperPathForApp,
  pipHelperPathForApp,
  speechHelperPathForApp,
  sourceHashFromBuildInfo,
} = require("./prepare-dev-electron-app.cjs");
const {
  DEFAULT_DEV_SIGNING_ID,
  matchingIdentity,
  parseCodeSigningIdentities,
} = require("./dev-signing.cjs");
const { resolveBuildTarget } = require("./build-core.cjs");

assert.deepEqual(
  normalizeElectronArguments([".", "--inspect=9229"], "/repo/desktop"),
  [resolve("/repo/desktop"), "--inspect=9229"],
);
assert.deepEqual(
  normalizeElectronArguments(["/repo/desktop", "--no-sandbox"], "/ignored"),
  ["/repo/desktop", "--no-sandbox"],
);

const environment = launchEnvironment(
  {
    ELECTRON_RENDERER_URL: "http://localhost:5173",
    WUU_ENABLE_CUA_MAC: "1",
    WUU_DESKTOP_CORE: "/repo/desktop/build/bin/wuu-core",
  },
  "token-1",
  "/repo",
  "/repo/desktop/build/bin/wuu-cua-mac",
  "/repo/desktop/build/bin/wuu-cua-mac-pip",
);
assert.ok(environment.includes("ELECTRON_RENDERER_URL=http://localhost:5173"));
assert.ok(environment.includes("WUU_DEV_LAUNCH_TOKEN=token-1"));
assert.ok(environment.includes("WUU_DESKTOP_CORE=/repo/desktop/build/bin/wuu-core"));
assert.ok(!environment.includes("WUU_DESKTOP_USE_GO_RUN=1"));
assert.ok(environment.includes("WUU_SOURCE_ROOT=/repo"));
assert.ok(environment.includes("WUU_ENABLE_CUA_MAC=1"));
assert.ok(environment.includes("WUU_CUA_MAC_HELPER=/repo/desktop/build/bin/wuu-cua-mac"));
assert.ok(environment.includes("WUU_CUA_MAC_PIP_HELPER=/repo/desktop/build/bin/wuu-cua-mac-pip"));

const disabledEnvironment = launchEnvironment(
  { ELECTRON_RENDERER_URL: "http://localhost:5173" },
  "token-disabled",
  "/repo",
  "/repo/desktop/build/bin/wuu-cua-mac",
  "/repo/desktop/build/bin/wuu-cua-mac-pip",
);
assert.ok(!disabledEnvironment.some((entry) => entry.startsWith("WUU_ENABLE_CUA_MAC=")));
assert.ok(disabledEnvironment.includes("WUU_DESKTOP_USE_GO_RUN=1"));

const processList = [
  "  41 /path/Electron Helper WUU_DEV_LAUNCH_TOKEN=token-1",
  "  42 /repo/desktop/build/dev-host/Wuu Dev.app/Contents/MacOS/Electron /repo/desktop WUU_DEV_LAUNCH_TOKEN=token-1",
].join("\n");
assert.equal(processIDForLaunchToken(processList, "token-1"), 42);
assert.equal(processIDForLaunchToken(processList, "missing"), undefined);
assert.equal(
  processIDForLaunchToken(
    "  43 /repo/desktop/build/dev-host/Wuu Dev.app/Contents/MacOS/Electron /repo/desktop --wuu-dev-launch-token=token-2",
    "token-2",
  ),
  43,
);
assert.equal(
  helperPathForApp("/repo/desktop/build/dev-host/Wuu Dev.app"),
  join("/repo/desktop/build/dev-host/Wuu Dev.app", "Contents", "Resources", "bin", "wuu-cua-mac"),
);
assert.equal(
  pipHelperPathForApp("/repo/desktop/build/dev-host/Wuu Dev.app"),
  join("/repo/desktop/build/dev-host/Wuu Dev.app", "Contents", "Resources", "bin", "wuu-cua-mac-pip"),
);
assert.equal(
  speechHelperPathForApp("/repo/desktop/build/dev-host/Wuu Dev.app"),
  join("/repo/desktop/build/dev-host/Wuu Dev.app", "Contents", "Resources", "bin", "wuu-speech-mac"),
);
assert.equal(
  sourceHashFromBuildInfo({ sourceHash: "a".repeat(64) }, () => "fallback"),
  "a".repeat(64),
);
assert.equal(sourceHashFromBuildInfo({}, () => "fallback"), "fallback");
assert.match(packageJSON.scripts["pack:mac"], /CSC_IDENTITY_AUTO_DISCOVERY=false/);
assert.match(packageJSON.scripts["dist:mac"], /CSC_IDENTITY_AUTO_DISCOVERY=false/);
assert.match(packageJSON.scripts["pack:mac"], /--config\.electronDist=node_modules\/electron\/dist/);
assert.match(packageJSON.scripts["dist:mac"], /--config\.electronDist=node_modules\/electron\/dist/);
// The env assignment rides the cross-shell env-run launcher; the point
// stays the same — CUA dev mode is opt-in per invocation, never baked
// into dev.cjs itself (the doesNotMatch below).
assert.equal(
  packageJSON.scripts["dev:direct"],
  "node scripts/env-run.cjs WUU_ENABLE_CUA_MAC=1 node scripts/dev.cjs",
);
const devLauncherSource = readFileSync(resolve(__dirname, "dev.cjs"), "utf8");
assert.doesNotMatch(devLauncherSource, /env\.WUU_ENABLE_CUA_MAC\s*=\s*["']1["']/);
assert.match(devLauncherSource, /build-core\.cjs/);
assert.doesNotMatch(devLauncherSource, /--plugins-only/);
assert.match(devLauncherSource, /env\.WUU_DESKTOP_CORE\s*=/);
assert.equal(packageJSON.scripts["build:core"], "node scripts/build-core.cjs");
assert.equal(
  packageJSON.scripts["build:core:win"],
  "node scripts/build-core.cjs --platform=win32",
);
assert.match(packageJSON.scripts["pack:win"], /^npm run build:core:win /);
assert.match(packageJSON.scripts["dist:win"], /^npm run build:core:win /);
assert.deepEqual(resolveBuildTarget(["--platform=win32"], "darwin", "arm64"), {
  platform: "win32",
  arch: "arm64",
  goos: "windows",
  goarch: "arm64",
  binaryName: "wuu-core.exe",
  staleBinaryName: "wuu-core",
});
assert.deepEqual(
  resolveBuildTarget(["--platform=win32", "--arch=x64"], "darwin", "arm64"),
  {
    platform: "win32",
    arch: "x64",
    goos: "windows",
    goarch: "amd64",
    binaryName: "wuu-core.exe",
    staleBinaryName: "wuu-core",
  },
);
assert.doesNotMatch(packageJSON.scripts["pack:mac"], /cua-mac/);
assert.doesNotMatch(packageJSON.scripts["dist:mac"], /cua-mac/);
assert.doesNotMatch(packageJSON.scripts["pack:mac"], /speech-mac/);
assert.doesNotMatch(packageJSON.scripts["dist:mac"], /speech-mac/);
assert.deepEqual(packageJSON.build.extraResources[0].filter, [
  "wuu-core",
  "wuu-core.exe",
  "wuu-*-plugin",
  "wuu-*-plugin.exe",
]);
assert.equal(packageJSON.build.mac.extendInfo, undefined);
assert.equal(packageJSON.build.afterPack, "scripts/after-pack.cjs");

const identities = parseCodeSigningIdentities([
  '  1) 0123456789ABCDEF0123456789ABCDEF01234567 "Wuu Dev Signing"',
  "     1 valid identities found",
].join("\n"));
assert.deepEqual(identities, [{
  sha1: "0123456789ABCDEF0123456789ABCDEF01234567",
  name: DEFAULT_DEV_SIGNING_ID,
}]);
assert.equal(
  matchingIdentity(identities, DEFAULT_DEV_SIGNING_ID)?.sha1,
  "0123456789ABCDEF0123456789ABCDEF01234567",
);
assert.equal(
  matchingIdentity(identities, "0123456789abcdef0123456789abcdef01234567")?.name,
  DEFAULT_DEV_SIGNING_ID,
);

// Electron bootstrap for the dev host. The real installer downloads a binary
// over the network; these tests replace that step with a fake so they verify
// our decision logic without touching the network or the real Electron.app.
const realSourceApp = resolve(
  __dirname,
  "..",
  "node_modules",
  "electron",
  "dist",
  "Electron.app",
);

assert.equal(
  electronInstallerScriptFromManifest({ bin: { "install-electron": "install.js" } }),
  "install.js",
);
assert.equal(electronInstallerScriptFromManifest({}), "install.js");

// Test 1: source Electron.app already present -> installer not called.
let installCalls = 0;
let appExists = true;
assert.equal(
  ensureSourceElectronApp({
    sourceAppExists: () => appExists,
    installElectron: () => {
      installCalls += 1;
      return { status: 0 };
    },
    electronVersion: "42.3.0",
  }),
  true,
);
assert.equal(installCalls, 0);

// Test 2: missing first, then present after install -> installer called once.
let installCallsTwo = 0;
let appExistsTwo = false;
assert.equal(
  ensureSourceElectronApp({
    sourceAppExists: () => appExistsTwo,
    installElectron: () => {
      installCallsTwo += 1;
      appExistsTwo = true;
      return { status: 0 };
    },
    electronVersion: "42.3.0",
  }),
  true,
);
assert.equal(installCallsTwo, 1);

// Test 3: installer reports success but the app is still missing -> clear error.
assert.throws(
  () => ensureSourceElectronApp({
    sourceAppExists: () => false,
    installElectron: () => ({ status: 0 }),
    electronVersion: "42.3.0",
  }),
  (error) => {
    assert.ok(error instanceof Error);
    assert.ok(error.message.includes("still missing"));
    assert.ok(error.message.includes(realSourceApp));
    return true;
  },
);

// Test 4: installer itself fails -> error keeps the exit status.
assert.throws(
  () => ensureSourceElectronApp({
    sourceAppExists: () => false,
    installElectron: () => ({ status: 1 }),
    electronVersion: "42.3.0",
  }),
  /status 1/,
);

// Test 5: an already-current Wuu Dev.app never checks or installs the source
// Electron.app, while a stale host prepares it exactly once.
let sourcePreparationCalls = 0;
const ensureSource = () => {
  sourcePreparationCalls += 1;
};
assert.equal(ensureSourceForStaleDevHost({ current: true, ensureSource }), false);
assert.equal(sourcePreparationCalls, 0);
assert.equal(ensureSourceForStaleDevHost({ current: false, ensureSource }), true);
assert.equal(sourcePreparationCalls, 1);

console.log("dev launcher tests passed");
