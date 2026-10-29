import { execFileSync } from "node:child_process";
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const evidence = join(root, ".tmp/e2e/terminal-unknown-external");
mkdirSync(evidence, { recursive: true });
const run = mkdtempSync(join(evidence, "run-"));
const fixture = join(run, "independent-plugin");
mkdirSync(fixture);
const log = join(run, "verification.log");
function command(program, args, cwd = root, env = {}) {
  appendFileSync(log, `${program} ${args.join(" ")}\n`);
  try {
    const output = execFileSync(program, args, { cwd, env: { ...process.env, ...env }, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
    appendFileSync(log, output); process.stdout.write(output); return output;
  } catch (error) { appendFileSync(log, `${error.stdout ?? ""}${error.stderr ?? ""}\n`); throw error; }
}
console.log(`Terminal recovery evidence: ${run}`);
const [packed] = JSON.parse(command("npm", ["pack", "--json", "--pack-destination", run], join(root, "packages/plugin-sdk")));
for (const name of ["provider.ts", "backend.ts"]) cpSync(join(root, "scripts/state-authority-acceptance", name), join(fixture, name));
writeFileSync(join(fixture, "package.json"), JSON.stringify({ name: "independent-terminal-http-authority", private: true, type: "module", dependencies: { "@wuu/plugin-sdk": `file:${join(run, packed.filename)}` }, devDependencies: { typescript: "5.9.3", "@types/node": "22.10.0" } }, null, 2));
writeFileSync(join(fixture, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2023", module: "NodeNext", moduleResolution: "NodeNext", strict: true, skipLibCheck: true, types: ["node"] }, include: ["*.ts"] }, null, 2));
command("npm", ["install", "--no-audit", "--no-fund"], fixture);
command("npm", ["exec", "--", "tsc", "--noEmit"], fixture);
command("go", ["test", "-race", "-count=1", "-v", "-timeout=120s", "./internal/appserver", "-run", "^TestUnconfirmedTerminalRecoveryThroughExternalDocuments$"], root, { WUU_PROVIDER_AUTHORITY_FIXTURE: fixture });
writeFileSync(join(run, "environment.json"), JSON.stringify({ node: process.version, go: command("go", ["version"]).trim(), commit: command("git", ["rev-parse", "HEAD"]).trim() }, null, 2));
console.log(`Verified real worker crash recovery through the independent SDK and HTTP authority. Log: ${log}`);
