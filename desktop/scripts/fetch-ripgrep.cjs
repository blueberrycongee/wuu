const { createHash } = require("node:crypto");
const {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} = require("node:fs");
const { tmpdir } = require("node:os");
const { join, resolve } = require("node:path");
const { spawnSync } = require("node:child_process");
const { resolveBuildTarget } = require("./build-core.cjs");

// Bundled ripgrep serves the core's search tools and agent shell commands.
// Checksums come from the upstream release's .sha256 assets.
const VERSION = "15.2.0";
const ASSETS = {
  "darwin-arm64": {
    triple: "aarch64-apple-darwin",
    extension: "tar.gz",
    sha256: "3750b2e93f37e0c692657da574d7019a101c0084da05a790c83fd335bad973e4",
  },
  "darwin-x64": {
    triple: "x86_64-apple-darwin",
    extension: "tar.gz",
    sha256: "af7825fcc69a2afc7a7aea55fc9af90e26421d8f20fe59df32e233c0b8a231c1",
  },
  "win32-arm64": {
    triple: "aarch64-pc-windows-msvc",
    extension: "zip",
    sha256: "e4abca10c3a64ebea742667dd7009449d49403db5460dd6873e389fa2945360f",
  },
  "win32-x64": {
    triple: "x86_64-pc-windows-msvc",
    extension: "zip",
    sha256: "71b2fef860abe467217a538ff31de02f5258807c0129f771846f87bd029aafc5",
  },
};
const LICENSE_FILES = ["COPYING", "LICENSE-MIT", "UNLICENSE"];

async function main() {
  const target = resolveBuildTarget(process.argv.slice(2));
  const asset = ASSETS[`${target.platform}-${target.arch}`];
  if (!asset) {
    throw new Error(`no bundled ripgrep for ${target.platform}-${target.arch}`);
  }
  const desktopRoot = resolve(__dirname, "..");
  const toolsDir = join(desktopRoot, "build", "tools");
  const licenseDir = join(desktopRoot, "build", "licenses", "ripgrep");
  const binaryName = target.platform === "win32" ? "rg.exe" : "rg";
  const binaryPath = join(toolsDir, binaryName);
  const stampPath = join(toolsDir, ".ripgrep");
  const stamp = `${VERSION} ${asset.triple}\n`;

  if (existsSync(binaryPath) && existsSync(stampPath) && readFileSync(stampPath, "utf8") === stamp) {
    console.log(`ripgrep ${VERSION} (${asset.triple}) already present`);
    return;
  }

  const name = `ripgrep-${VERSION}-${asset.triple}`;
  const url = `https://github.com/BurntSushi/ripgrep/releases/download/${VERSION}/${name}.${asset.extension}`;
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`download ${url}: HTTP ${response.status}`);
  }
  const archive = Buffer.from(await response.arrayBuffer());
  const digest = createHash("sha256").update(archive).digest("hex");
  if (digest !== asset.sha256) {
    throw new Error(`ripgrep checksum mismatch for ${name}: got ${digest}, want ${asset.sha256}`);
  }

  const workDir = mkdtempSync(join(tmpdir(), "wuu-ripgrep-"));
  try {
    const archivePath = join(workDir, `${name}.${asset.extension}`);
    writeFileSync(archivePath, archive);
    extract(archivePath, asset.extension, workDir);
    const extracted = join(workDir, name);

    mkdirSync(toolsDir, { recursive: true });
    mkdirSync(licenseDir, { recursive: true });
    // Remove a binary left by a build for the other platform so the package
    // only carries the executable for this target.
    rmSync(join(toolsDir, "rg"), { force: true });
    rmSync(join(toolsDir, "rg.exe"), { force: true });
    copyFileSync(join(extracted, binaryName), binaryPath);
    if (target.platform !== "win32") chmodSync(binaryPath, 0o755);
    for (const file of LICENSE_FILES) {
      copyFileSync(join(extracted, file), join(licenseDir, file));
    }
    writeFileSync(stampPath, stamp);
  } finally {
    rmSync(workDir, { recursive: true, force: true });
  }
  console.log(`bundled ripgrep ${VERSION} (${asset.triple}) at ${binaryPath}`);
}

function extract(archivePath, extension, destination) {
  // bsdtar (macOS, Windows 10+) reads zip archives; GNU tar on Linux does not.
  const [command, args] =
    extension === "zip" && process.platform === "linux"
      ? ["unzip", ["-q", archivePath, "-d", destination]]
      : ["tar", ["-xf", archivePath, "-C", destination]];
  const result = spawnSync(command, args, { stdio: "inherit" });
  if (result.status !== 0) {
    throw new Error(`extract ${archivePath} with ${command} failed`);
  }
}

main().catch((error) => {
  console.error(error.message);
  process.exit(1);
});
