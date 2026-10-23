import { spawn, execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createWriteStream, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const { values } = parseArgs({ options: {
  suite: { type: "string", default: "domains" },
  candidate: { type: "string" },
  output: { type: "string" },
  help: { type: "boolean" },
} });
if (values.help) {
  console.log("node scripts/replaceable-architecture-acceptance.mjs --suite domains|boundaries|sdk|external|all [--candidate SHA] [--output DIR]");
  console.log("Domains validates real cross-domain document migration. Boundaries runs existing race-tested storage/graph contracts. SDK runs the independent SDK process and CLI journey. These checks do not certify production composition, legacy import, or UI acceptance.");
  process.exit(0);
}
const suites = {
  domains: [["cross-domain-migration", "go", ["test", "-race", "-count=1", "-json", "./scripts/replaceable-architecture-acceptance", "-run", "^TestAcceptanceCrossDomainMigration$"]]],
  boundaries: [
    ["storage-and-host", "go", ["test", "-race", "-count=1", "-json", "./internal/persistence", "./internal/pluginhost"]],
    ["retained-generations", "go", ["test", "-race", "-count=1", "-json", "./internal/runtime", "-run", "Test(RetainedGeneration|Candidate|FailedCandidate|ActivatePluginGeneration|DisabledRuntime|ThreadModel|BuiltPluginGenerations|PluginSystemPromptFailure|RefreshCandidate|PackageDependency)"]],
  ],
  external: [["external-provider-parity", process.execPath, ["scripts/replaceable-architecture-external.mjs"]]],
  sdk: [["sdk-process-and-transfer", process.execPath, ["scripts/document-state-e2e.mjs"]]],
};
const selection = values.suite === "all" ? Object.values(suites).flat() : suites[values.suite];
if (!selection) throw new Error(`Unknown suite ${values.suite}`);
const git = (...args) => execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim();
const commit = git("rev-parse", "HEAD");
if (values.candidate && git("rev-parse", `${values.candidate}^{commit}`) !== commit) {
  throw new Error(`Candidate mismatch: checkout is ${commit}`);
}
const started = new Date().toISOString();
const output = resolve(values.output ?? join(root, ".tmp", "replaceable-architecture", started.replace(/[:.]/g, "-")));
mkdirSync(output, { recursive: true });
const trackedDiff = execFileSync("git", ["diff", "HEAD", "--binary"], { cwd: root });
const untracked = git("ls-files", "--others", "--exclude-standard").split("\n").filter(Boolean).map(path => ({
  path, sha256: createHash("sha256").update(readFileSync(join(root, path))).digest("hex"),
}));
const report = {
  version: 1, started, commit, source: root, suite: values.suite,
  sourceChanges: { status: git("status", "--short"), trackedDiffSHA256: createHash("sha256").update(trackedDiff).digest("hex"), untracked },
  environment: { platform: process.platform, arch: process.arch, node: process.version, go: execFileSync("go", ["version"], { cwd: root, encoding: "utf8" }).trim() },
  checks: [],
  limitations: [
    "Passing domain/host contracts does not establish that all production consumers use the selected authority.",
    "Document backend transfer does not cover legacy SQLite domain conversion.",
    "The SDK journey uses an external consumer with built-in SQLite/JSONL providers; it does not prove an external storage implementation.",
    "Both full shells, third-party nesting, author TSX preview/reload and visual acceptance require separate evidence.",
    "No real-account inference or macOS native integration/performance is exercised.",
  ],
};
const persist = () => writeFileSync(join(output, "report.json"), `${JSON.stringify(report, null, 2)}\n`);
writeFileSync(join(output, "tracked.patch"), trackedDiff);
persist();
console.log(`Acceptance evidence: ${output}`);
for (const [id, command, args] of selection) {
  const logPath = join(output, `${id}.log`);
  const log = createWriteStream(logPath);
  const start = Date.now();
  const result = await new Promise(resolveResult => {
    const child = spawn(command, args, { cwd: root, env: process.env, stdio: ["ignore", "pipe", "pipe"] });
    let spawnError;
    child.stdout.pipe(log, { end: false });
    child.stderr.pipe(log, { end: false });
    child.on("error", error => { spawnError = error.message; });
    child.on("close", (exitCode, signal) => log.end(() => resolveResult({ exitCode, signal, spawnError })));
  });
  const check = { id, command: [command, ...args], ...result, durationMs: Date.now() - start, log: logPath, status: result.exitCode === 0 ? "passed" : "failed" };
  if (args.includes("-json")) {
    const events = readFileSync(logPath, "utf8").split("\n").filter(line => line.startsWith("{")).flatMap(line => {
      try { return [JSON.parse(line)]; } catch { return []; }
    });
    check.passedTests = events.filter(event => event.Action === "pass" && event.Test).map(event => `${event.Package}/${event.Test}`);
    check.skippedTests = events.filter(event => event.Action === "skip" && event.Test).map(event => `${event.Package}/${event.Test}`);
    check.failedTests = events.filter(event => event.Action === "fail").map(event => `${event.Package}/${event.Test ?? "package"}`);
    if (!check.passedTests.length || check.failedTests.length) check.status = "failed";
  }
  report.checks.push(check);
  persist();
  console.log(`${check.status.toUpperCase()}: ${id} (${check.durationMs} ms); ${logPath}`);
}
report.finished = new Date().toISOString();
report.status = report.checks.every(check => check.status === "passed") ? "passed" : "failed";
persist();
console.log(`Selected suite ${report.status}; limitations remain recorded in report.json.`);
if (report.status !== "passed") process.exitCode = 1;
