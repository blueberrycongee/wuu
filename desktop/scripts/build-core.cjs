const { chmodSync, mkdirSync, readFileSync, readdirSync, rmSync } = require("node:fs");
const { join, resolve } = require("node:path");
const { spawnSync } = require("node:child_process");

const GOOS_BY_PLATFORM = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};
const GOARCH_BY_NODE_ARCH = {
  arm64: "arm64",
  ia32: "386",
  x64: "amd64",
};

function resolveBuildTarget(
  args,
  hostPlatform = process.platform,
  hostArch = process.arch,
) {
  const platform = optionValue(args, "--platform") || hostPlatform;
  const arch = optionValue(args, "--arch") || hostArch;
  const goos = GOOS_BY_PLATFORM[platform];
  const goarch = GOARCH_BY_NODE_ARCH[arch];
  if (!goos) {
    throw new Error(`unsupported core build platform: ${platform}`);
  }
  if (!goarch) {
    throw new Error(`unsupported core build architecture: ${arch}`);
  }
  return {
    platform,
    arch,
    goos,
    goarch,
    binaryName: platform === "win32" ? "wuu-core.exe" : "wuu-core",
    staleBinaryName: platform === "win32" ? "wuu-core" : "wuu-core.exe",
  };
}

function optionValue(args, name) {
  const prefix = `${name}=`;
  const option = args.find((arg) => arg.startsWith(prefix));
  return option ? option.slice(prefix.length) : undefined;
}

function main() {
  const desktopRoot = resolve(__dirname, "..");
  const repoRoot = resolve(desktopRoot, "..");
  const version = readFileSync(join(repoRoot, "VERSION"), "utf8").trim() || "2026.1.1";
  const commit =
    run("git", ["rev-parse", "--short", "HEAD"], { cwd: repoRoot, optional: true }) || "none";
  const date = new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
  const target = resolveBuildTarget(process.argv.slice(2));
  const outDir = join(desktopRoot, "build", "bin");
  const outPath = join(outDir, target.binaryName);
  const pluginsOnly = process.argv.includes("--plugins-only");

  mkdirSync(outDir, { recursive: true });
  for (const artifact of readdirSync(outDir, { withFileTypes: true })) {
    if (artifact.isFile() && /^wuu-.+-plugin(?:\.exe)?$/.test(artifact.name)) {
      rmSync(join(outDir, artifact.name), { force: true });
    }
  }

  const ldflags = [
    "-s",
    "-w",
    `-X github.com/blueberrycongee/wuu/internal/version.Version=v${version}`,
    `-X github.com/blueberrycongee/wuu/internal/version.Commit=${commit}`,
    `-X github.com/blueberrycongee/wuu/internal/version.Date=${date}`,
  ].join(" ");

  if (!pluginsOnly) {
    run("go", ["build", "-ldflags", ldflags, "-o", outPath, "./cmd/wuu"], {
      cwd: repoRoot,
      env: {
        ...process.env,
        CGO_ENABLED: "0",
        GOOS: target.goos,
        GOARCH: target.goarch,
      },
    });
  }

  for (const command of readdirSync(join(repoRoot, "cmd"), { withFileTypes: true })) {
    if (!command.isDirectory() || !/^wuu-.+-plugin$/.test(command.name)) continue;
    // A plugin directory without Go sources (empty leftover of a rename or an
    // in-flight plugin removal) cannot be built; skip it instead of failing the
    // whole dev launch. Safe to drop once empty wuu-*-plugin directories can no
    // longer appear in the tree.
    const commandDir = join(repoRoot, "cmd", command.name);
    if (!readdirSync(commandDir).some((file) => file.endsWith(".go"))) continue;
    const binaryName = target.platform === "win32" ? `${command.name}.exe` : command.name;
    const pluginPath = join(outDir, binaryName);
    run("go", ["build", "-ldflags", "-s -w", "-o", pluginPath, `./cmd/${command.name}`], {
      cwd: repoRoot,
      env: {
        ...process.env,
        CGO_ENABLED: "0",
        GOOS: target.goos,
        GOARCH: target.goarch,
      },
    });
    if (target.platform !== "win32") chmodSync(pluginPath, 0o755);
  }

  if (!pluginsOnly && target.platform !== "win32") {
    chmodSync(outPath, 0o755);
  }
  // extraResources accepts both filenames. Remove a binary left by a build
  // for another platform so a Windows package can never prefer a stale Unix
  // core over the freshly-built .exe.
  if (!pluginsOnly) {
    rmSync(join(outDir, target.staleBinaryName), { force: true });
  }

  console.log(
    pluginsOnly
      ? `built first-party plugin helpers (${target.goos}/${target.goarch})`
      : `built ${outPath} (${target.goos}/${target.goarch})`,
  );
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd,
    env: options.env ?? process.env,
    encoding: "utf8",
    stdio: options.optional ? ["ignore", "pipe", "ignore"] : "inherit",
  });
  if (result.status !== 0) {
    if (options.optional) {
      return "";
    }
    process.exit(result.status ?? 1);
  }
  return options.optional ? result.stdout.trim() : "";
}

if (require.main === module) {
  main();
}

module.exports = { resolveBuildTarget };
