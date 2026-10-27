import { execFileSync } from "node:child_process";
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const evidence = resolve(process.env.WUU_SETTINGS_EVIDENCE ?? join(root, ".tmp/e2e/plugin-settings"));
mkdirSync(evidence, { recursive: true });
const run = mkdtempSync(join(evidence, "run-"));
const fixture = join(run, "independent-plugin");
const log = join(run, "verification.log");
mkdirSync(fixture);
function command(program, args, cwd = root, extra = {}) {
  appendFileSync(log, `${program} ${args.join(" ")}\n`);
  try {
    const out = execFileSync(program, args, { cwd, env: { ...process.env, ...extra }, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
    appendFileSync(log, out);
    process.stdout.write(out);
    return out;
  } catch (error) {
    appendFileSync(log, `${error.stdout ?? ""}${error.stderr ?? ""}\n`);
    throw error;
  }
}
console.log(`Settings evidence: ${run}`);
const packed = JSON.parse(command("npm", ["pack", "--json", "--pack-destination", run], join(root, "packages/plugin-sdk")));
const installed = join(fixture, "node_modules/@wuu/plugin-sdk");
mkdirSync(installed, { recursive: true });
// Exercise the actual published file list without network installs or source aliases.
command("tar", ["-xzf", join(run, packed[0].filename), "--strip-components=1", "-C", installed]);
cpSync(join(root, "internal/runtime/testdata/settings-snapshot/runtime.ts"), join(fixture, "runtime.ts"));
writeFileSync(join(fixture, "package.json"), JSON.stringify({ name: "independent-settings-worker", private: true, type: "module" }));
command("go", ["test", "-race", "-count=1", "-v", "./internal/runtime", "-run", "^TestPluginSettingsSDKProcess$"], root, { WUU_SETTINGS_FIXTURE: fixture });
writeFileSync(join(run, "environment.json"), JSON.stringify({ node: process.version, go: command("go", ["version"]).trim(), commit: command("git", ["rev-parse", "HEAD"]).trim(), sdkIntegrity: packed[0].integrity }, null, 2));
console.log(`Verified packed SDK settings effectiveness. Log: ${log}`);
