import { execFileSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync, appendFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const evidenceRoot = join(root, ".tmp/e2e/workflow-driver");
mkdirSync(evidenceRoot, { recursive: true });
const run = mkdtempSync(join(evidenceRoot, "run-"));
const fixture = join(run, "independent-plugin");
const log = join(run, "verification.log");
mkdirSync(fixture);
function command(program, args, cwd = root, env = process.env) {
  appendFileSync(log, `${program} ${args.join(" ")}\n`);
  try {
    const output = execFileSync(program, args, { cwd, env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
    appendFileSync(log, output);
    process.stdout.write(output);
    return output;
  } catch (error) {
    appendFileSync(log, `${error.stdout ?? ""}${error.stderr ?? ""}\n`);
    console.error(`${error.stdout ?? ""}${error.stderr ?? ""}`);
    throw error;
  }
}

console.log(`Workflow driver evidence: ${run}`);
const packed = JSON.parse(command("npm", ["pack", "--json", "--pack-destination", run], join(root, "packages/plugin-sdk")));
const sdk = join(run, packed[0].filename);
for (const name of ["runtime.ts", "tsconfig.json"]) cpSync(join(root, "examples/plugins/workflow-loop", name), join(fixture, name));
cpSync(join(root, "internal/runtime/testdata/streaming-driver.ts"), join(fixture, "streaming.ts"));
const tsconfig = JSON.parse(readFileSync(join(fixture, "tsconfig.json"), "utf8"));
tsconfig.include.push("streaming.ts");
writeFileSync(join(fixture, "tsconfig.json"), JSON.stringify(tsconfig, null, 2));
writeFileSync(join(fixture, "package.json"), JSON.stringify({
  name: "independent-workflow-verification", private: true, type: "module",
  dependencies: { "@wuu/plugin-sdk": `file:${sdk}` },
  devDependencies: { "typescript": "^5.9.0", "@types/node": "^22.0.0" },
}, null, 2));
command("npm", ["install", "--no-audit", "--no-fund"], fixture);
command("npm", ["exec", "--", "tsc", "--noEmit"], fixture);
command("go", ["test", "-race", "./internal/runtime", "-run", "^Test(Workflow|Streaming)DriverProcess$", "-count=1", "-v"], root, { ...process.env, WUU_WORKFLOW_FIXTURE: fixture });
writeFileSync(join(run, "environment.json"), JSON.stringify({
  node: process.version,
  go: command("go", ["version"]).trim(),
  commit: command("git", ["rev-parse", "HEAD"]).trim(),
  sdk: JSON.parse(readFileSync(join(fixture, "node_modules/@wuu/plugin-sdk/package.json"), "utf8")).version,
}, null, 2));
console.log(`Verified independent SDK installation, external workflow, streaming tool receipts, cancellation and resume. Log: ${log}`);
