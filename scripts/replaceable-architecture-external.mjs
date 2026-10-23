import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const sdkRoot = resolve(process.env.WUU_REPLACEABLE_SDK_DIR ?? join(root, "packages/plugin-sdk"));
const base = join(root, ".tmp", "replaceable-architecture", "external");
mkdirSync(base, { recursive: true });
const output = mkdtempSync(join(base, "run-"));
const fixture = join(output, "independent-provider");
mkdirSync(fixture);
const log = join(output, "verification.log");
const report = { status: "running", commit: execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim(), output, sdkRoot, checks: [], limitations: ["SDK-only fixture is a single-process atomic file backend, not a production backend.", "Real domain consumer and host registry are exercised; appserver composition wiring and discovery require separate acceptance."] };
function command(program, args, cwd = root, extraEnv = {}) {
  appendFileSync(log, `${program} ${args.join(" ")}\n`);
  try {
    const result = execFileSync(program, args, { cwd, env: { ...process.env, ...extraEnv }, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 180000 });
    appendFileSync(log, result);
    report.checks.push({ command: [program, ...args], status: "passed" });
    return result;
  } catch (error) {
    appendFileSync(log, `${error.stdout ?? ""}${error.stderr ?? ""}\n`);
    report.checks.push({ command: [program, ...args], status: "failed" });
    throw error;
  }
}
console.log(`External provider evidence: ${output}`);
try {
  const packed = JSON.parse(command("npm", ["pack", "--json", "--pack-destination", output], sdkRoot));
  report.sdkPackageSHA256 = createHash("sha256").update(readFileSync(join(output, packed[0].filename))).digest("hex");
  cpSync(join(root, "scripts/replaceable-architecture-acceptance/fixtures/provider.ts"), join(fixture, "provider.ts"));
  writeFileSync(join(fixture, "package.json"), JSON.stringify({ name: "independent-state-provider", private: true, type: "module", dependencies: { "@wuu/plugin-sdk": `file:${join(output, packed[0].filename)}` } }));
  command("npm", ["install", "--no-audit", "--no-fund"], fixture);
  command("go", ["test", "-race", "-count=1", "-v", "./scripts/replaceable-architecture-acceptance", "-run", "TestAcceptance(SelectedProviderLifecycle|ProviderErrorParity|ProviderQueryParity)$"], root, { WUU_REPLACEABLE_FIXTURE: fixture });
  report.status = "passed";
} catch (error) {
  report.status = "failed";
  console.error(`External provider acceptance failed; inspect ${log}`);
  process.exitCode = 1;
} finally {
  writeFileSync(join(output, "report.json"), `${JSON.stringify(report, null, 2)}\n`);
}
