import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const evidence = join(root, ".tmp/e2e/document-state");
mkdirSync(evidence, { recursive: true });
const run = mkdtempSync(join(evidence, "run-"));
const fixture = join(run, "independent-plugin");
const log = join(run, "verification.log");
mkdirSync(fixture);
function command(program, args, cwd = root, extraEnv = {}) {
  appendFileSync(log, `${program} ${args.join(" ")}\n`);
  try {
    const output = execFileSync(program, args, {
      cwd, env: { ...process.env, ...extraEnv }, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"],
    });
    appendFileSync(log, output);
    process.stdout.write(output);
    return output;
  } catch (error) {
    appendFileSync(log, `${error.stdout ?? ""}${error.stderr ?? ""}\n`);
    throw error;
  }
}
console.log(`Document state evidence: ${run}`);
const packed = JSON.parse(command("npm", ["pack", "--json", "--pack-destination", run], join(root, "packages/plugin-sdk")));
cpSync(join(root, "examples/plugins/document-state/runtime.ts"), join(fixture, "runtime.ts"));
writeFileSync(join(fixture, "package.json"), JSON.stringify({
  name: "independent-document-state", private: true, type: "module",
  dependencies: { "@wuu/plugin-sdk": `file:${join(run, packed[0].filename)}` },
  devDependencies: { typescript: "^5.9.0", "@types/node": "^22.0.0" },
}, null, 2));
writeFileSync(join(fixture, "tsconfig.json"), JSON.stringify({
  compilerOptions: { target: "ES2023", module: "NodeNext", moduleResolution: "NodeNext", strict: true, skipLibCheck: true, types: ["node"] },
  include: ["runtime.ts"],
}, null, 2));
command("npm", ["install", "--no-audit", "--no-fund"], fixture);
command("npm", ["exec", "--", "tsc", "--noEmit"], fixture);
command("go", ["test", "-race", "-count=1", "-v", "./internal/runtime", "-run", "^TestDocumentStateProcess$"], root, { WUU_STATE_FIXTURE: fixture });
const cli = join(run, process.platform === "win32" ? "wuu.exe" : "wuu");
command("go", ["build", "-o", cli, "./cmd/wuu"]);
const state = (args) => command(cli, ["state", ...args]);
const source = join(run, "source.json");
writeFileSync(source, JSON.stringify({ version: 1, revision: "0", documents: [{ namespace: "notes", key: "imported", value: { title: "portable evidence" } }], receipts: [] }));
const store = join(run, "store");
state(["import", "--dir", store, "--backend", "sqlite", "--file", source]);
assert.equal(JSON.parse(state(["check", "--dir", store])).documents, 1);
state(["migrate", "--dir", store, "--from", "sqlite", "--to", "jsonl"]);
assert.equal(JSON.parse(state(["check", "--dir", store])).backend, "jsonl");
const exported = join(run, "export.json");
state(["export", "--dir", store, "--file", exported]);
assert.deepEqual(JSON.parse(readFileSync(exported, "utf8")), JSON.parse(readFileSync(source, "utf8")));
assert.throws(() => state(["import", "--dir", store, "--backend", "sqlite", "--file", source]));
writeFileSync(join(run, "environment.json"), JSON.stringify({
  node: process.version, go: command("go", ["version"]).trim(), commit: command("git", ["rev-parse", "HEAD"]).trim(),
}, null, 2));
console.log(`Verified public SDK process persistence, lifecycle and CLI transfers. Log: ${log}`);
