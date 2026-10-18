const { chmodSync, copyFileSync, mkdirSync, rmSync } = require("node:fs");
const { join, resolve } = require("node:path");
const { spawnSync } = require("node:child_process");
const { resolveBuildTarget } = require("./build-core.cjs");

const target = resolveBuildTarget(process.argv.slice(2));
if (target.platform !== "linux") {
  console.log("skipping cua-linux helper build outside Linux");
  process.exit(0);
}

const desktopRoot = resolve(__dirname, "..");
const outDir = join(desktopRoot, "build", "bin");
const destination = join(outDir, "wuu-cua-linux");
const licenseDir = join(desktopRoot, "build", "licenses", "xgb");
if (process.env.WUU_SKIP_CUA_LINUX === "1") {
  rmSync(destination, { force: true });
  console.log("skipping cua-linux helper build for this package");
  process.exit(0);
}

mkdirSync(outDir, { recursive: true });
const result = spawnSync("go", ["build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", destination, "./cmd/wuu-cua-linux"], {
  cwd: resolve(desktopRoot, ".."),
  env: { ...process.env, CGO_ENABLED: "0", GOOS: target.goos, GOARCH: target.goarch },
  stdio: "inherit",
});
if (result.error) throw result.error;
if (result.status !== 0) process.exit(result.status ?? 1);
chmodSync(destination, 0o755);
const dependency = spawnSync("go", ["list", "-m", "-f", "{{.Dir}}", "github.com/jezek/xgb"], {
  cwd: resolve(desktopRoot, ".."),
  env: process.env,
  encoding: "utf8",
});
if (dependency.error) throw dependency.error;
if (dependency.status !== 0) throw new Error(dependency.stderr || "could not locate the XGB license");
mkdirSync(licenseDir, { recursive: true });
copyFileSync(join(dependency.stdout.trim(), "LICENSE"), join(licenseDir, "LICENSE"));
console.log(`built ${destination} (${target.goos}/${target.goarch})`);
