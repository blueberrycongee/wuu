// Generated Personal task/grant + trusted-local UserHost + Desktop registered
// pool + real Coding receiver acceptance. Receiver policy and model are explicit
// fixtures; no external accounts, owner-bit forwarding, or replacement target.
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const artifacts = mkdtempSync(join(tmpdir(), 'wuu-registered-session-acceptance-'));
const binary = join(artifacts, process.platform === 'win32' ? 'receiver.test.exe' : 'receiver.test');
const evidence = process.env.WUU_REGISTERED_SESSION_EVIDENCE || join(artifacts, 'evidence.json');
function run(command, args, cwd, env = process.env) {
  const result = spawnSync(command, args, { cwd, env, stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status})`);
}
run(process.env.GO_BINARY || 'go', ['test', '-c', '-ldflags', '-X github.com/blueberrycongee/wuu/internal/version.Version=v2026.10.31-dev', './internal/appserver', '-o', binary], root);
run(process.platform === 'win32' ? 'npx.cmd' : 'npx', ['--no-install', 'vitest', 'run', 'src/main/registeredSessionTransport.test.ts'], join(root, 'desktop'), {
  ...process.env, WUU_REGISTERED_SESSION_TEST_BINARY: binary, WUU_REGISTERED_SESSION_EVIDENCE: evidence,
});
const proof = JSON.parse(readFileSync(evidence, 'utf8'));
if (proof.passed !== true || proof.checks.length < 5) throw new Error('Real-core acceptance did not execute successfully');
console.log(`Registered transport acceptance evidence: ${evidence}`);

const personal = JSON.parse(readFileSync(evidence + '.personal.json', 'utf8'));
if (personal.passed !== true || personal.checks.length < 4) throw new Error('Generated Personal acceptance did not execute successfully');
console.log(`Generated Personal acceptance evidence: ${evidence}.personal.json`);

const federationPersonal = JSON.parse(readFileSync(evidence + '.personal-federation.json', 'utf8'));
if (federationPersonal.passed !== true || federationPersonal.checks.length < 6) throw new Error('Conversational federation acceptance did not execute successfully');
console.log(`Conversational federation evidence: ${evidence}.personal-federation.json`);

const ptc = JSON.parse(readFileSync(evidence + ".personal-ptc.json", "utf8"));
if (ptc.passed !== true || !ptc.checks.some(check => check.includes("Code Mode") && check.includes("controller decision"))) throw new Error("Actual Code Mode review acceptance did not execute successfully");
console.log(`Code Mode direct controller review evidence: ${evidence}.personal-ptc.json`);
