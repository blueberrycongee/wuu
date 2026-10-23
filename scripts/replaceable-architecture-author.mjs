import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { appendFileSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const sdk = resolve(process.env.WUU_REPLACEABLE_SDK_DIR ?? join(root, "packages/plugin-sdk"));
const examples = resolve(process.env.WUU_REPLACEABLE_EXAMPLES_DIR ?? join(root, "examples/plugins"));
const evidence = join(root, ".tmp/replaceable-architecture/author");
mkdirSync(evidence, { recursive: true });
const output = mkdtempSync(join(evidence, "run-"));
// Keep dependency resolution outside the repository and its node_modules tree.
const temporaryRoot = resolve(process.env.WUU_REPLACEABLE_AUTHOR_TEMP ?? tmpdir());
if (temporaryRoot === root || temporaryRoot.startsWith(root + sep)) {
  throw new Error("Set WUU_REPLACEABLE_AUTHOR_TEMP to a temporary directory outside the repository.");
}
const standalone = mkdtempSync(join(temporaryRoot, "wuu-replaceable-author-"));
const log = join(output, "verification.log");
const digest = path => createHash("sha256").update(readFileSync(path)).digest("hex");
const report = {
  status: "running", acceptanceCommit: execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim(),
  sdk, examples, standalone, packages: [],
  limitations: ["Verifies independent public-SDK author typechecking and distributable builds. Preview/reload and real product shell interactions require separate isolated GUI acceptance."],
};
function command(args, cwd) {
  appendFileSync(log, `npm ${args.join(" ")} [${cwd}]\n`);
  try {
    const result = execFileSync("npm", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 180000 });
    appendFileSync(log, result);
    return result;
  } catch (error) {
    appendFileSync(log, `${error.stdout ?? ""}${error.stderr ?? ""}\n`);
    throw error;
  }
}
console.log(`Independent author evidence: ${output}`);
try {
  const [packed] = JSON.parse(command(["pack", "--json", "--pack-destination", standalone], sdk));
  const tarball = join(standalone, packed.filename);
  report.sdkPackageSHA256 = digest(tarball);
  for (const id of ["research-workbench", "research-evidence"]) {
    const source = join(examples, id), project = join(standalone, id);
    const installable = join(output, "packages", id);
    mkdirSync(project);
    const files = readdirSync(source).filter(name => /\.(tsx|json)$/.test(name) && name !== "package-lock.json");
    const item = { id, project, installable, sourceFiles: {}, status: "running" };
    report.packages.push(item);
    for (const name of files) {
      copyFileSync(join(source, name), join(project, name));
      item.sourceFiles[name] = digest(join(source, name));
    }
    const packagePath = join(project, "package.json");
    const manifest = JSON.parse(readFileSync(packagePath, "utf8"));
    manifest.devDependencies["@wuu/plugin-sdk"] = `file:${tarball}`;
    writeFileSync(packagePath, JSON.stringify(manifest, null, 2));
    command(["install", "--no-audit", "--no-fund"], project);
    command(["run", "typecheck"], project);
    command(["run", "build"], project);
    const plugin = JSON.parse(readFileSync(join(project, "plugin.json"), "utf8"));
    const entry = plugin.desktop.entry;
    mkdirSync(dirname(join(installable, entry)), { recursive: true });
    copyFileSync(join(project, "plugin.json"), join(installable, "plugin.json"));
    copyFileSync(join(project, entry), join(installable, entry));
    item.entry = entry;
    item.entrySHA256 = digest(join(installable, entry));
    item.installLockSHA256 = digest(join(project, "package-lock.json"));
    item.status = "passed";
  }
  report.status = "passed";
} catch (error) {
  report.status = "failed";
  report.error = String(error.message);
  console.error(`Independent author acceptance failed; inspect ${log}`);
  process.exitCode = 1;
} finally {
  writeFileSync(join(output, "report.json"), `${JSON.stringify(report, null, 2)}\n`);
}
