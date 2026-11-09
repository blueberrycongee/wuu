// Explicit maintainer operation, never run by builds or compatibility tests.
// Capture a new directory; an existing baseline is immutable, not regenerated.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync, cpSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const revision = "08000266921da772f2ca999b8687f34fb4dbc911";
const target = resolve(process.argv[2] ?? "");
if (!process.argv[2] || existsSync(target)) throw new Error("Pass a new, empty destination path; never overwrite a compatibility baseline.");
const compiler = join(root, "packages/plugin-sdk/node_modules/typescript/bin/tsc");
if (execFileSync(process.execPath, [compiler, "--version"], { encoding: "utf8" }).trim() !== "Version 5.9.3") {
  throw new Error("Historical package lock requires TypeScript 5.9.3");
}
const scratch = mkdtempSync(join(tmpdir(), "wuu-historical-sdk-"));
const digest = bytes => createHash("sha256").update(bytes).digest("hex");
const source = {}, files = {};
function historical(path) {
  const bytes = execFileSync("git", ["show", `${revision}:${path}`], { cwd: root, maxBuffer: 8 << 20 });
  source[path] = digest(bytes);
  return bytes;
}
for (const path of execFileSync("git", ["ls-tree", "-r", "--name-only", revision, "packages/plugin-sdk"], { cwd: root, encoding: "utf8" }).trim().split("\n")) {
  const local = join(scratch, path.slice("packages/plugin-sdk/".length));
  mkdirSync(dirname(local), { recursive: true });
  writeFileSync(local, historical(path));
}
execFileSync(process.execPath, [compiler, "--project", join(scratch, "tsconfig.json")], { stdio: "inherit" });
// Pack the historical package's allowlist, not today's SDK or package.json.
const [packed] = JSON.parse(execFileSync("npm", ["pack", "--ignore-scripts", "--json"], { cwd: scratch, encoding: "utf8" }));
mkdirSync(join(target, "sdk"), { recursive: true });
for (const { path } of packed.files) {
  const bytes = readFileSync(join(scratch, path));
  const destination = join(target, "sdk", path);
  mkdirSync(dirname(destination), { recursive: true });
  writeFileSync(destination, bytes);
  files[`sdk/${path}`] = digest(bytes);
}
for (const [name, path] of Object.entries({
  "todo.mjs": "internal/plugin/bundled/todo/runtime.mjs",
  "LICENSE": "LICENSE",
})) {
  const bytes = historical(path);
  writeFileSync(join(target, name), bytes);
  files[name] = digest(bytes);
}
// Keep the historical package lock as provenance; it is not installed by tests.
cpSync(join(scratch, "package-lock.json"), join(target, "sdk-package-lock.json"));
files["sdk-package-lock.json"] = digest(readFileSync(join(target, "sdk-package-lock.json")));
writeFileSync(join(target, "provenance.json"), JSON.stringify({
  revision, source_url: `https://github.com/blueberrycongee/wuu/tree/${revision}`,
  sdk_version: packed.version, compiler: "typescript@5.9.3",
  origin: "Repository-distributed source and exact shipped TODO bundle; not an asserted npm registry release.",
  capture: "node scripts/capture-plugin-compatibility.mjs <new-directory>",
  source, files,
}, null, 2) + "\n");
console.log(target);
