const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const { createHash } = require("node:crypto");
const { accessSync, constants, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join, resolve } = require("node:path");
const asar = require("@electron/asar");

const root = resolve(__dirname, "../release");
const version = require("../package.json").version;
const commit = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
// Match build-core.cjs: Git may choose seven digits in a shallow CI checkout.
const shortCommit = execFileSync("git", ["rev-parse", "--short", "HEAD"], { encoding: "utf8" }).trim();
const artifacts = ["wuu-linux-x64.AppImage", "wuu-linux-x64.deb"];
const run = (command, args, options = {}) => execFileSync(command, args, { encoding: "utf8", timeout: 120_000, ...options }).trim();
const hash = (path, algorithm = "sha256", encoding = "hex") => createHash(algorithm).update(readFileSync(path)).digest(encoding);

function verifyApp(path) {
  const resources = join(path, "resources");
  const home = join(scratch, "home");
  mkdirSync(join(home, ".wuu"), { recursive: true });
  const env = { ...process.env, HOME: home, WUU_HOME: join(home, ".wuu") };
  for (const file of ["wuu", "resources/bin/wuu-core", "resources/bin/wuu-cua-linux", "resources/tools/rg"]) {
    const binary = join(path, file);
    accessSync(binary, constants.X_OK);
    const header = readFileSync(binary).subarray(0, 20);
    assert.equal(header.subarray(0, 4).toString("hex"), "7f454c46", `${file} must be ELF`);
    assert.equal(header[4], 2, `${file} must be 64-bit`);
    assert.equal(header.readUInt16LE(18), 62, `${file} must be x86-64`);
  }
  const manifest = JSON.parse(asar.extractFile(join(resources, "app.asar"), "package.json"));
  assert.equal(manifest.version, version);
  const core = run(join(resources, "bin/wuu-core"), ["--version"], { env });
  assert.ok(core.includes(`v${version}`) && core.includes(shortCommit), `Wrong bundled core: ${core}`);
  assert.ok(!core.includes("dirty"), "Bundled core must be clean");
  assert.match(run(join(resources, "tools/rg"), ["--version"]), /^ripgrep 15\.2\.0/);
  for (const file of ["COPYING", "LICENSE-MIT", "UNLICENSE"]) accessSync(join(resources, "licenses/ripgrep", file));
  assert.ok(existsSync(join(resources, "mobile-web/index.html")), "Missing bundled Web assets");
  assert.ok(!readdirSync(join(resources, "bin")).some(name => /\.exe$|cua-mac|^wuu$/.test(name)), "Wrong-platform helper in Linux package");
  assert.ok(!existsSync(join(resources, "app-update.yml")), "Experimental packages must not enable a public update feed");
  accessSync(join(resources, "licenses/xgb/LICENSE"));
  // Catch missing transitive dependencies in the actual ASAR, not the build tree.
  run(join(path, "wuu"), ["-e", "require(process.argv[1])", join(resources, "app.asar/node_modules/electron-updater")],
    { env: { ...env, ELECTRON_RUN_AS_NODE: "1" } });
  // Load and exercise the packaged native module using the bundled Electron ABI.
  const ptyPath = join(resources, "app.asar.unpacked/node_modules/node-pty");
  const pty = run(join(path, "wuu"), ["-e", `
    const pty = require(${JSON.stringify(ptyPath)});
    const child = pty.spawn('/bin/sh', ['-c', 'printf wuu-packaged-pty'], {env: process.env});
    let output = '';
    const timer = setTimeout(() => process.exit(2), 10000);
    child.onData(data => output += data);
    child.onExit(({exitCode}) => { clearTimeout(timer); if (exitCode || !output.includes('wuu-packaged-pty')) process.exit(3); console.log('PTY OK'); });
  `], { env: { ...env, ELECTRON_RUN_AS_NODE: "1" } });
  assert.match(pty, /PTY OK/);
  return { core, nativePty: true, updaterModule: true };
}

const scratch = mkdtempSync(join(tmpdir(), "wuu-linux-artifacts-"));
try {
  assert.equal(process.platform, "linux");
  assert.equal(process.arch, "x64");
  const appImage = join(root, artifacts[0]);
  run(appImage, ["--appimage-extract"], { cwd: scratch, maxBuffer: 16 * 1024 * 1024 });
  const imageRoot = join(scratch, "squashfs-root");
  assert.deepEqual(readFileSync(join(imageRoot, "AppRun")), readFileSync(join(__dirname, "linux-AppRun")), "AppImage must preserve the sandbox-safe launcher");
  accessSync(join(imageRoot, "AppRun"), constants.X_OK);
  assert.doesNotMatch(readFileSync(join(imageRoot, "wuu.desktop"), "utf8"), /--no-sandbox|--disable-setuid-sandbox/);
  const appImageChecks = verifyApp(imageRoot);
  const deb = join(root, artifacts[1]);
  assert.equal(run("dpkg-deb", ["--field", deb, "Architecture"]), "amd64");
  assert.equal(run("dpkg-deb", ["--field", deb, "Version"]), version.replace(/-/g, "~"));
  assert.equal(run("dpkg-deb", ["--field", deb, "Package"]), "wuu");
  const dependencies = run("dpkg-deb", ["--field", deb, "Depends"]);
  assert.match(dependencies, /libasound2t64 \| libasound2/);
  assert.match(dependencies, /libgbm1/);
  const debRoot = join(scratch, "deb");
  run("dpkg-deb", ["--extract", deb, debRoot]);
  const debChecks = verifyApp(join(debRoot, "opt/wuu"));
  assert.doesNotMatch(readFileSync(join(debRoot, "usr/share/applications/wuu.desktop"), "utf8"), /--no-sandbox|--disable-setuid-sandbox/);
  const manifests = readdirSync(root).filter(name => /-linux(?:-x64)?\.yml$/.test(name));
  assert.equal(manifests.length, 0, "Experimental packages must not generate public update metadata");
  const files = artifacts.map(name => ({ name, sha256: hash(join(root, name)) }));
  writeFileSync(join(root, "SHA256SUMS-linux"), files.map(file => `${file.sha256}  ${file.name}\n`).join(""));
  writeFileSync(join(root, "verification-linux.json"), JSON.stringify({
    platform: "linux", arch: "x64", version, commit, files,
    checks: { appImage: appImageChecks, deb: debChecks, publicUpdates: false, sandboxPreserved: true },
  }, null, 2) + "\n");
  console.log(`Verified Linux x64 AppImage, Debian package, bundled runtime and artifact hashes for ${version}`);
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
