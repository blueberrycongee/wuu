#!/usr/bin/env node
// Build a separately installed public-SDK plugin, then exercise real plugin
// processes and checkpoint crash barriers. Evidence never substitutes skips for
// successful acceptance; failures leave the report and command log behind.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = join(root, 'examples/plugins/durable-driver');
const sourceFiles = readdirSync(source).filter(name => name.endsWith('.ts')).sort();
const output = resolve(process.env.WUU_DURABLE_OUTPUT || join(root, '.tmp/e2e/durable-cloud'));
mkdirSync(output, { recursive: true });
const run = mkdtempSync(join(output, 'driver-'));
const fixture = join(run, 'independent-plugin');
mkdirSync(fixture);
const log = join(run, 'commands.log');
const result = { status: 'failed', run, fixture, checks: [], limitations: ['Controlled inference, real host, plugin processes, persistent session state and marker tool. Fleet infrastructure and rendered UI are separate checks.'] };
function command(program, args, cwd = root, extraEnv = {}) {
  appendFileSync(log, `$ ${program} ${args.join(' ')}\n`);
  try {
    const stdout = execFileSync(program, args, { cwd, env: { ...process.env, ...extraEnv }, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
    appendFileSync(log, stdout);
    process.stdout.write(stdout);
    return stdout;
  } catch (error) {
    appendFileSync(log, `${error.stdout || ''}${error.stderr || ''}\n`);
    throw error;
  }
}
try {
  command('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', '--package-lock=false'], join(root, 'packages/plugin-sdk'));
  command('npm', ['run', 'build'], join(root, 'packages/plugin-sdk'));
  const packed = JSON.parse(command('npm', ['pack', '--json', '--pack-destination', run], join(root, 'packages/plugin-sdk')));
  const sdk = join(run, packed[0].filename);
  for (const name of [...sourceFiles, 'build.mjs', 'tsconfig.json', 'plugin.json']) cpSync(join(source, name), join(fixture, name));
  const manifest = JSON.parse(readFileSync(join(root, 'examples/plugins/durable-driver/package.json'), 'utf8'));
  manifest.dependencies['@wuu/plugin-sdk'] = `file:${sdk}`;
  writeFileSync(join(fixture, 'package.json'), JSON.stringify(manifest, null, 2));
  command('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', '--package-lock=false'], fixture);
  command('npm', ['run', 'build'], fixture);
  const usageTest = join(run, 'usage-projection.test.mjs');
  command(process.execPath, ['--input-type=module', '-e', `import { build } from 'esbuild'; await build({ entryPoints: ['usage.test.ts'], bundle: true, platform: 'node', format: 'esm', outfile: ${JSON.stringify(usageTest)} });`], fixture);
  command(process.execPath, [usageTest]);
  result.checks.push('Authoritative usage identity, deduplication, completeness and numeric bounds');
  command(process.execPath, ['scripts/durable-task-definitions-test.mjs'], root, { WUU_DURABLE_SOURCE: fixture });
  command(process.execPath, ['scripts/durable-scheduler-definitions-test.mjs'], root, { WUU_DURABLE_SOURCE: fixture });
  result.checks.push('Independent task registry, migration CAS and scheduler definition fences');
  const upgradeFixture = join(run, 'independent-upgrade-plugin');
  cpSync(fixture, upgradeFixture, { recursive: true });
  const upgradeSource = join(upgradeFixture, 'runtime.ts');
  const registration = 'name: "durable.run",\n  version: 1,';
  const original = readFileSync(upgradeSource, 'utf8');
  if (!original.includes(registration)) throw new Error('builtin definition registration not found');
  writeFileSync(upgradeSource, original.replace(registration, `name: "durable.run",
  version: 2,
  migrate(input, checkpoint, fromVersion) {
    if (fromVersion !== 1) throw new Error("unsupported migration");
    return { input: { ...(input as Record<string, Json>), migrated: true }, checkpoint: checkpoint as State };
  },`));
  command('npm', ['run', 'build'], upgradeFixture);
  // Deterministic crash barriers live only in separately built test fixtures.
  // The production plugin has no test switches or process-kill hooks.
  for (const point of ['complete', 'context']) {
    const crashFixture = join(run, `independent-reset-${point}-crash-plugin`);
    cpSync(fixture, crashFixture, { recursive: true });
    const sourcePath = join(crashFixture, point === 'complete' ? 'runtime.ts' : 'context.ts');
    const sourceText = readFileSync(sourcePath, 'utf8');
    const needle = point === 'complete'
      ? `            const receipt = await turn.complete("", [], {
              stop_reason: "reset",
            });`
      : '    const replaced = await turn.replaceContext(latest.revision, messages);';
    if (sourceText.split(needle).length !== 2) throw new Error(`Reset ${point} crash barrier must match once`);
    const injected = `
    if (${point === 'context' ? 'trigger === "reset"' : 'true'}) {
      const fs = await import("node:fs");
      const marker = "__WUU_TEST_RESET_CRASH_MARKER__";
      if (!fs.existsSync(marker)) {
        fs.writeFileSync(marker, "reset ${point} barrier\\n");
        process.kill(process.pid, "SIGKILL");
        await new Promise(() => {});
      }
    }`;
    writeFileSync(sourcePath, sourceText.replace(needle, needle + injected));
    command('npm', ['run', 'build'], crashFixture);
    result[`reset_${point}_crash_fixture`] = crashFixture;
    result[`reset_${point}_crash_sha256`] = createHash('sha256').update(readFileSync(join(crashFixture, 'dist/runtime.js'))).digest('hex');
  }
  result.checks.push('Independent SDK tarball installation and TypeScript build');
  result.artifacts = Object.fromEntries([...sourceFiles, 'build.mjs', 'plugin.json', 'dist/runtime.js'].map(name => [name, createHash('sha256').update(readFileSync(join(fixture, name))).digest('hex')]));
  result.upgrade_artifacts = Object.fromEntries(['runtime.ts', 'task-definitions.ts', 'dist/runtime.js'].map(name => [name, createHash('sha256').update(readFileSync(join(upgradeFixture, name))).digest('hex')]));
  result.environment = { node: process.version, go: command('go', ['version']).trim(), commit: command('git', ['rev-parse', 'HEAD']).trim() };
  const evidence = join(run, 'process');
  mkdirSync(evidence);
  command('go', ['test', '-race', './internal/runtime', '-run', '^TestDurable(Driver(Process|ModelRetryProcess|NoProgressProcess|ReplayProcess)|TaskDefinitionProcess|TaskMigrationProcess)$', '-count=1', '-v'], root, { WUU_DURABLE_FIXTURE: fixture, WUU_DURABLE_UPGRADE_FIXTURE: upgradeFixture, WUU_DURABLE_EVIDENCE: evidence });
  for (const scenario of ['complete', 'killed-before-tools', 'killed-after-model-archive', 'killed-after-settled-effect', 'killed-after-tool-archive', 'killed-after-committed-tool-error', 'killed-before-effect-receipt', 'killed-finalizing']) {
    const observed = JSON.parse(readFileSync(join(evidence, scenario, 'result.json'), 'utf8'));
    if (observed.scenario !== scenario) throw new Error(`missing process evidence: ${scenario}`);
    result.checks.push(observed);
  }
  for (const [scenario, file] of [['model-retry-double-kill', 'retry-result.json'], ['no-progress-crash-budget', 'no-progress-result.json']]) {
    const artifact = join(evidence, scenario, file);
    const observed = JSON.parse(readFileSync(artifact, 'utf8'));
    result.checks.push({ scenario, artifact, ...observed });
  }
  for (const reason of ['missing_task', 'task_too_old']) {
    const artifact = join(evidence, 'task-definitions', `${reason}.json`);
    result.checks.push({ scenario: reason, artifact, ...JSON.parse(readFileSync(artifact, 'utf8')) });
  }
  for (const scenario of ['safe-read', 'revoked-read', 'undeclared-read', 'lost-unsafe-write', 'code-safe-read', 'code-revoked-read', 'code-lost-unsafe-write']) {
    // The process test writes authoritative effect counts and ledger identities.
    const artifact = join(evidence, `replay-${scenario}`, 'result.json');
    result.checks.push({ scenario: `replay-${scenario}`, artifact, ...JSON.parse(readFileSync(artifact, 'utf8')) });
  }
  const migrationArtifact = join(evidence, 'task-migration', 'result.json');
  result.checks.push({ scenario: 'task-migration', artifact: migrationArtifact, ...JSON.parse(readFileSync(migrationArtifact, 'utf8')) });
  const usageArtifact = join(evidence, 'killed-after-model-archive', 'recovered-model-usage.json');
  result.checks.push({ scenario: 'recovered-model-usage', artifact: usageArtifact, ...JSON.parse(readFileSync(usageArtifact, 'utf8')) });
  result.status = 'passed';
} catch (error) {
  result.error = String(error);
  process.exitCode = 1;
} finally {
  writeFileSync(join(run, 'report.json'), JSON.stringify(result, null, 2));
  console.log(`Durable driver ${result.status}. Evidence: ${run}`);
}
