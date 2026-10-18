const { existsSync, readdirSync, statSync } = require("node:fs");
const { join, resolve } = require("node:path");

function fail(message) {
  console.error(`windows package verification failed: ${message}`);
  process.exit(1);
}

function requireFile(path, minimumBytes = 1) {
  if (!existsSync(path)) fail(`missing ${path}`);
  const stat = statSync(path);
  if (!stat.isFile()) fail(`expected a file at ${path}`);
  if (stat.size < minimumBytes) {
    fail(`${path} is unexpectedly small (${stat.size} bytes)`);
  }
  return stat.size;
}

const desktopRoot = resolve(__dirname, "..");
const releaseDir = join(desktopRoot, "release");
if (!existsSync(releaseDir)) fail(`missing release directory ${releaseDir}`);

const arch = process.arch === "ia32" ? "ia32" : process.arch;
const setupName = `wuu-win-${arch}-setup.exe`;
const zipName = `wuu-win-${arch}.zip`;
const unpackedDir = join(releaseDir, "win-unpacked");

const setupBytes = requireFile(join(releaseDir, setupName), 1024 * 1024);
const zipBytes = requireFile(join(releaseDir, zipName), 1024 * 1024);
requireFile(join(unpackedDir, "wuu.exe"), 1024 * 1024);
requireFile(join(unpackedDir, "resources", "app.asar"), 1024);
requireFile(join(unpackedDir, "resources", "bin", "wuu-core.exe"), 1024 * 1024);
requireFile(join(unpackedDir, "resources", "tools", "rg.exe"), 1024 * 1024);
requireFile(join(unpackedDir, "resources", "mobile-web", "index.html"), 1);

const metadata = readdirSync(releaseDir).filter((name) =>
  name.endsWith(".yml") || name.endsWith(".blockmap"),
);

console.log(`verified ${setupName} (${Math.round(setupBytes / 1024 / 1024)} MiB)`);
console.log(`verified ${zipName} (${Math.round(zipBytes / 1024 / 1024)} MiB)`);
console.log("verified packaged wuu-core.exe, rg.exe, app.asar, and mobile web assets");
if (metadata.length) console.log(`update metadata: ${metadata.join(", ")}`);
