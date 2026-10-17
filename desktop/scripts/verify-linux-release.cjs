const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const { createHash } = require("node:crypto");
const { accessSync, constants, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join, resolve } = require("node:path");
const asar = require("@electron/asar");
const yaml = require("js-yaml");

const root = resolve(__dirname, "../release");
const version = require("../package.json").version;
const commit = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
const artifacts = ["wuu-linux-x64.AppImage", "wuu-linux-x64.deb"];
const run = (command, args, options = {}) => execFileSync(command, args, { encoding: "utf8", timeout: 120_000, ...options }).trim();
const hash = (path, algorithm = "sha256", encoding = "hex") => createHash(algorithm).update(readFileSync(path)).digest(encoding);

function verifyApp(path) {
  const resources = join(path, "resources");
  for (const file of ["wuu", "resources/bin/wuu-core", "resources/tools/rg"]) {
    const binary = join(path, file);
    accessSync(binary, constants.X_OK);
    const header = readFileSync(binary).subarray(0, 20);
    assert.equal(header.subarray(0, 4).toString("hex"), "7f454c46", `${file} must be ELF`);
    assert.equal(header[4], 2, `${file} must be 64-bit`);
    assert.equal(header.readUInt16LE(18), 62, `${file} must be x86-64`);
  }
  const manifest = JSON.parse(asar.extractFile(join(resources, "app.asar"), "package.json"));
  assert.equal(manifest.version, version);
  const core = run(join(resources, "bin/wuu-core"), ["--version"]);
  assert.ok(core.includes(`v${version}`) && core.includes(commit.slice(0, 8)), `Wrong bundled core: ${core}`);
  assert.ok(!core.includes("dirty"), "Bundled core must be clean");
  assert.match(run(join(resources, "tools/rg"), ["--version"]), /^ripgrep 15\.2\.0/);
  for (const file of ["COPYING", "LICENSE-MIT", "UNLICENSE"]) accessSync(join(resources, "licenses/ripgrep", file));
  assert.ok(existsSync(join(resources, "mobile-web/index.html")), "Missing bundled Web assets");
  assert.ok(!readdirSync(join(resources, "bin")).some(name => /\.exe$|cua-mac|^wuu$/.test(name)), "Wrong-platform helper in Linux package");
  const provider = yaml.load(readFileSync(join(resources, "app-update.yml"), "utf8"));
  assert.equal(provider.provider, "github");
  assert.equal(provider.owner, "blueberrycongee");
  assert.equal(provider.repo, "wuu");
  // Load and exercise the packaged native module using the bundled Electron ABI.
  const ptyPath = join(resources, "app.asar.unpacked/node_modules/node-pty");
  const pty = run(join(path, "wuu"), ["-e", `
    const pty = require(${JSON.stringify(ptyPath)});
    const child = pty.spawn('/bin/sh', ['-c', 'printf wuu-packaged-pty'], {env: process.env});
    let output = '';
    const timer = setTimeout(() => process.exit(2), 10000);
    child.onData(data => output += data);
    child.onExit(({exitCode}) => { clearTimeout(timer); if (exitCode || !output.includes('wuu-packaged-pty')) process.exit(3); console.log('PTY OK'); });
  `], { env: { ...process.env, ELECTRON_RUN_AS_NODE: "1" } });
  assert.match(pty, /PTY OK/);
  return { core, nativePty: true };
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
  assert.equal(readFileSync(join(debRoot, "opt/wuu/resources/package-type"), "utf8").trim(), "deb");
  assert.doesNotMatch(readFileSync(join(debRoot, "usr/share/applications/wuu.desktop"), "utf8"), /--no-sandbox|--disable-setuid-sandbox/);
  const manifests = readdirSync(root).filter(name => /-linux(?:-x64)?\.yml$/.test(name));
  assert.equal(manifests.length, 1, "Expected one Linux update manifest");
  const info = yaml.load(readFileSync(join(root, manifests[0]), "utf8"));
  assert.equal(info.version, version);
  assert.deepEqual(info.files.map(file => file.url).sort(), [...artifacts].sort());
  for (const file of info.files) {
    assert.equal(file.sha512, hash(join(root, file.url), "sha512", "base64"));
    assert.equal(file.size, statSync(join(root, file.url)).size);
  }
  assert.equal(info.path, artifacts[0], "Linux update default must be the AppImage");
  assert.equal(info.sha512, info.files.find(file => file.url === info.path).sha512);
  const files = [...artifacts, ...manifests].map(name => ({ name, sha256: hash(join(root, name)) }));
  writeFileSync(join(root, "SHA256SUMS-linux"), files.map(file => `${file.sha256}  ${file.name}\n`).join(""));
  writeFileSync(join(root, "verification-linux.json"), JSON.stringify({
    platform: "linux", arch: "x64", version, commit, files,
    checks: { appImage: appImageChecks, deb: debChecks, updaterMetadata: true, sandboxPreserved: true },
  }, null, 2) + "\n");
  console.log(`Verified Linux x64 AppImage, Debian package, bundled runtime and update hashes for ${version}`);
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
