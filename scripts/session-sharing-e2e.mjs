import { execFileSync } from 'node:child_process';
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const evidence = join(root, '.tmp/e2e/session-sharing');
mkdirSync(evidence, { recursive: true });
const run = mkdtempSync(join(evidence, 'run-'));
const fixture = join(run, 'independent-provider');
mkdirSync(fixture);
const log = join(run, 'verification.log');
function command(program, args, cwd = root, extraEnv = {}) {
  appendFileSync(log, `${program} ${args.join(' ')}\n`);
  try {
    const output = execFileSync(program, args, { cwd, env: { ...process.env, ...extraEnv }, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
    appendFileSync(log, output); process.stdout.write(output); return output;
  } catch (error) { appendFileSync(log, `${error.stdout ?? ''}${error.stderr ?? ''}\n`); throw error; }
}
console.log(`Sharing evidence: ${run}`);
const packed = JSON.parse(command('npm', ['pack', '--json', '--pack-destination', run], join(root, 'packages/plugin-sdk')));
cpSync(join(root, 'examples/plugins/restricted-session-sharing.ts'), join(fixture, 'provider.ts'));
writeFileSync(join(fixture, 'runner.ts'), `import plugin from './provider.ts';\nimport { runJSONLRuntime } from '@wuu/plugin-sdk';\nrunJSONLRuntime(plugin,{input:process.stdin,output:process.stdout}).catch(error=>{console.error(error);process.exitCode=1});\n`);
writeFileSync(join(fixture, 'package.json'), JSON.stringify({ name: 'independent-session-sharing', private: true, type: 'module', dependencies: { '@wuu/plugin-sdk': `file:${join(run, packed[0].filename)}` }, devDependencies: { typescript: '^5.9.0', '@types/node': '^22.0.0' } }, null, 2));
writeFileSync(join(fixture, 'tsconfig.json'), JSON.stringify({ compilerOptions: { target: 'ES2023', module: 'NodeNext', moduleResolution: 'NodeNext', strict: true, skipLibCheck: true, allowImportingTsExtensions: true, noEmit: true, types: ['node'] }, include: ['*.ts'] }, null, 2));
command('npm', ['install', '--no-audit', '--no-fund'], fixture);
command('npm', ['exec', '--', 'tsc', '--noEmit'], fixture);
command('go', ['test', '-race', '-count=1', '-v', './internal/sessionshare']);
command('go', ['test', '-race', '-count=1', '-v', './internal/runtime', '-run', '^TestSessionSharingExternalProcess$'], root, { WUU_SHARING_FIXTURE: fixture });
command('go', ['test', '-count=1', '-v', './internal/appserver', '-run', '^TestSessionShar']);
command('go', ['test', '-count=1', '-v', './cmd/wuu', '-run', '^TestSessionShare']);
writeFileSync(join(run, 'environment.json'), JSON.stringify({ node: process.version, go: command('go', ['version']).trim(), commit: command('git', ['rev-parse', 'HEAD']).trim() }, null, 2));
console.log(`Verified restricted sharing and independent SDK replacement. Log: ${log}`);
