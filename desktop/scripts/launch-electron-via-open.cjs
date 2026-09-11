#!/usr/bin/env node

const { randomBytes } = require("node:crypto");
const { execFileSync, spawnSync } = require("node:child_process");
const {
  closeSync,
  existsSync,
  fstatSync,
  openSync,
  readSync,
  rmSync,
} = require("node:fs");
const { tmpdir } = require("node:os");
const { isAbsolute, join, resolve } = require("node:path");

const desktopRoot = resolve(__dirname, "..");
const repoRoot = resolve(desktopRoot, "..");
const electronApp = process.env.WUU_DEV_ELECTRON_APP
  || join(desktopRoot, "node_modules", "electron", "dist", "Electron.app");
const helper = process.env.WUU_CUA_MAC_HELPER
  || join(electronApp, "Contents", "Resources", "bin", "wuu-cua-mac");
const pipHelper = process.env.WUU_CUA_MAC_PIP_HELPER
  || join(electronApp, "Contents", "Resources", "bin", "wuu-cua-mac-pip");

function normalizeElectronArguments(args, root = desktopRoot) {
  if (args.length === 0 || args[0].startsWith("-") || isAbsolute(args[0])) {
    return [...args];
  }
  return [resolve(root, args[0]), ...args.slice(1)];
}

function launchEnvironment(
  env,
  token,
  root = repoRoot,
  helperPath = helper,
  pipHelperPath = pipHelper,
) {
  const desktopCore = env.WUU_DESKTOP_CORE;
  const values = {
    ELECTRON_RENDERER_URL: env.ELECTRON_RENDERER_URL,
    NODE_ENV: env.NODE_ENV ?? "development",
    NODE_ENV_ELECTRON_VITE: env.NODE_ENV_ELECTRON_VITE ?? "development",
    WUU_ENABLE_CUA_MAC: env.WUU_ENABLE_CUA_MAC,
    WUU_CUA_MAC_HELPER: helperPath,
    WUU_CUA_MAC_PIP_HELPER: pipHelperPath,
    WUU_DEV_LAUNCH_TOKEN: token,
    WUU_DESKTOP_CORE: desktopCore,
    WUU_DESKTOP_USE_GO_RUN: desktopCore ? undefined : "1",
    WUU_SOURCE_ROOT: root,
    WUU_WEB_URL: env.WUU_WEB_URL,
    WUU_WEB_LISTEN: env.WUU_WEB_LISTEN,
    WUU_WEB_RELAY_URL: env.WUU_WEB_RELAY_URL,
    WUU_WEB_TLS_CERT: env.WUU_WEB_TLS_CERT,
    WUU_WEB_TLS_KEY: env.WUU_WEB_TLS_KEY,
  };
  return Object.entries(values)
    .filter(([, value]) => typeof value === "string" && value.length > 0)
    .map(([name, value]) => `${name}=${value}`);
}

function processIDForLaunchToken(processList, token) {
  const environmentMarker = `WUU_DEV_LAUNCH_TOKEN=${token}`;
  const argumentMarker = `--wuu-dev-launch-token=${token}`;
  const line = processList
    .split("\n")
    .find((candidate) =>
      (candidate.includes(environmentMarker) || candidate.includes(argumentMarker))
      && candidate.includes("/Contents/MacOS/Electron"),
    );
  if (!line) return undefined;
  const value = Number.parseInt(line.trim().split(/\s+/, 1)[0], 10);
  return Number.isFinite(value) && value > 0 ? value : undefined;
}

function makeLogTail(path, destination) {
  let offset = 0;
  const timer = setInterval(() => {
    if (!existsSync(path)) return;
    const file = openSync(path, "r");
    try {
      const size = fstatSync(file).size;
      if (size <= offset) return;
      const data = Buffer.alloc(size - offset);
      const count = readSync(file, data, 0, data.length, offset);
      offset += count;
      if (count > 0) destination.write(data.subarray(0, count));
    } finally {
      closeSync(file);
    }
  }, 100);
  timer.unref?.();
  return () => {
    clearInterval(timer);
    rmSync(path, { force: true });
  };
}

function waitForElectron(token, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    // The token is also passed as an Electron argument below, so avoid `ps e`.
    // Reading every process environment is slow and can time out on busy hosts.
    const processes = execFileSync("ps", ["-axo", "pid=,command="], {
      encoding: "utf8",
    });
    const pid = processIDForLaunchToken(processes, token);
    if (pid) return pid;
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 100);
  }
  throw new Error("LaunchServices did not start the Electron development host");
}

function run() {
  if (process.platform !== "darwin") {
    console.error("launch-electron-via-open is only supported on macOS");
    process.exit(1);
  }
  if (!existsSync(electronApp)) {
    console.error(`Electron app not found: ${electronApp}`);
    process.exit(1);
  }

  const token = randomBytes(12).toString("hex");
  const stdoutPath = join(tmpdir(), `wuu-electron-dev-${token}.stdout.log`);
  const stderrPath = join(tmpdir(), `wuu-electron-dev-${token}.stderr.log`);
  closeSync(openSync(stdoutPath, "a"));
  closeSync(openSync(stderrPath, "a"));
  const stopStdout = makeLogTail(stdoutPath, process.stdout);
  const stopStderr = makeLogTail(stderrPath, process.stderr);

  const args = ["-F", "-n", "-a", electronApp, "--stdout", stdoutPath, "--stderr", stderrPath];
  for (const variable of launchEnvironment(process.env, token)) {
    args.push("--env", variable);
  }
  const electronArguments = normalizeElectronArguments(process.argv.slice(2));
  // Chromium recommends the mock keychain for local developer builds. It
  // prevents a rebuilt development app from asking for the login password to
  // read "Safe Storage" on every launch. Set WUU_DEV_USE_SYSTEM_KEYCHAIN=1
  // only when intentionally testing Chromium's real Keychain integration.
  if (process.env.WUU_DEV_USE_SYSTEM_KEYCHAIN !== "1") {
    electronArguments.unshift("--use-mock-keychain");
  }
  args.push("--args", ...electronArguments);
  args.push(`--wuu-dev-launch-token=${token}`);

  const opened = spawnSync("open", args, { cwd: desktopRoot, stdio: "inherit" });
  if (opened.status !== 0) {
    stopStdout();
    stopStderr();
    process.exit(opened.status ?? 1);
  }

  let electronPID;
  try {
    electronPID = waitForElectron(token);
  } catch (error) {
    stopStdout();
    stopStderr();
    console.error(error instanceof Error ? error.message : String(error));
    process.exit(1);
  }

  console.log(`Wuu development host started through LaunchServices (pid ${electronPID}).`);
  console.log("macOS Screen Recording permission is now attached to Wuu Dev, not the terminal or Codex that ran npm.");

  let stopping = false;
  const stop = (signal = "SIGTERM") => {
    if (stopping) return;
    stopping = true;
    try { process.kill(electronPID, signal); } catch {}
    setTimeout(() => {
      try { process.kill(electronPID, "SIGKILL"); } catch {}
    }, 2_000).unref?.();
  };
  process.on("SIGINT", () => stop("SIGINT"));
  process.on("SIGTERM", () => stop("SIGTERM"));

  const monitor = setInterval(() => {
    try {
      process.kill(electronPID, 0);
    } catch {
      clearInterval(monitor);
      stopStdout();
      stopStderr();
      process.exit(0);
    }
  }, 150);
}

module.exports = {
  launchEnvironment,
  normalizeElectronArguments,
  processIDForLaunchToken,
};

if (require.main === module) run();
