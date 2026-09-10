const { spawn, spawnSync } = require("node:child_process");
const { readFileSync } = require("node:fs");
const { dirname, join, resolve } = require("node:path");
const {
  helperPathForApp,
  pipHelperPathForApp,
  prepareDevElectronApp,
  speechHelperPathForApp,
} = require("./prepare-dev-electron-app.cjs");
const { buildCodeModeHost } = require("./build-codemode-host.cjs");
const { ensureDevSigningIdentity } = require("./dev-signing.cjs");

const desktopRoot = resolve(__dirname, "..");
const buildHelper = join(__dirname, "build-cua-mac.cjs");
const buildSpeechHelper = join(__dirname, "build-speech-mac.cjs");
const buildCoreAndPluginHelpers = join(__dirname, "build-core.cjs");
const electronVitePackage = require.resolve("electron-vite/package.json", { paths: [desktopRoot] });
const electronViteManifest = JSON.parse(readFileSync(electronVitePackage, "utf8"));
const electronVite = join(dirname(electronVitePackage), electronViteManifest.bin["electron-vite"]);

const webBuild = spawnSync(process.execPath, [join(__dirname, "build-web.cjs")], { cwd: desktopRoot, env: process.env, stdio: "inherit" });
if (webBuild.status !== 0) process.exit(webBuild.status ?? 1);

const devSigning = process.platform === "darwin" ? ensureDevSigningIdentity() : undefined;
const build = spawnSync(process.execPath, [buildHelper], {
  cwd: desktopRoot,
  env: {
    ...process.env,
    ...(devSigning ? { WUU_CUA_MAC_SIGN_ID: devSigning.identity } : {}),
  },
  stdio: "inherit",
});
if (build.status !== 0) {
  process.exit(build.status ?? 1);
}
const speechBuild = spawnSync(process.execPath, [buildSpeechHelper], {
  cwd: desktopRoot,
  env: {
    ...process.env,
    ...(devSigning ? { WUU_SPEECH_MAC_SIGN_ID: devSigning.identity } : {}),
  },
  stdio: "inherit",
});
if (speechBuild.status !== 0) {
  process.exit(speechBuild.status ?? 1);
}
const coreBuild = spawnSync(process.execPath, [buildCoreAndPluginHelpers], {
  cwd: desktopRoot,
  env: process.env,
  stdio: "inherit",
});
if (coreBuild.status !== 0) {
  process.exit(coreBuild.status ?? 1);
}

// Code mode is opt-in at runtime through `code_mode.mode`. Its V8 host is
// expensive to build, so development launches skip it unless a developer
// explicitly requests it for code-mode work or tests.
if (process.env.WUU_BUILD_CODE_MODE_HOST === "1") {
  buildCodeModeHost();
}

const env = { ...process.env };
env.WUU_DESKTOP_CORE = join(
  desktopRoot,
  "build",
  "bin",
  process.platform === "win32" ? "wuu-core.exe" : "wuu-core",
);
if (env.WUU_ENABLE_BROWSER === "1") {
  env.VITE_ENABLE_BROWSER = "true";
}
if (process.platform === "darwin") {
  // Keep the stable signed host for macOS permissions, but do not bypass
  // feature gates. CUA is enabled only when the caller explicitly exports
  // WUU_ENABLE_CUA_MAC=1 (for example through npm run dev:direct).
  env.WUU_DEV_ELECTRON_APP = prepareDevElectronApp(devSigning);
  env.WUU_CUA_MAC_HELPER = helperPathForApp(env.WUU_DEV_ELECTRON_APP);
  env.WUU_CUA_MAC_PIP_HELPER = pipHelperPathForApp(env.WUU_DEV_ELECTRON_APP);
  env.WUU_SPEECH_MAC_HELPER = speechHelperPathForApp(
    env.WUU_DEV_ELECTRON_APP,
  );
  env.ELECTRON_EXEC_PATH = join(__dirname, "launch-electron-via-open.cjs");
}

const dev = spawn(process.execPath, [electronVite, desktopRoot], {
  cwd: desktopRoot,
  env,
  stdio: "inherit",
});
dev.on("error", (error) => {
  console.error(`failed to start electron-vite: ${error.message}`);
  process.exit(1);
});
dev.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});
