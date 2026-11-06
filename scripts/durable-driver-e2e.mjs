#!/usr/bin/env node
// Build a separately installed public-SDK plugin, then exercise real plugin
// processes and checkpoint crash barriers. Evidence never substitutes skips for
// successful acceptance; failures leave the report and command log behind.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
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
  for (const name of ['runtime.ts', 'state.ts', 'orchestration.ts', 'build.mjs', 'tsconfig.json', 'plugin.json']) cpSync(join(root, 'examples/plugins/durable-driver', name), join(fixture, name));
  const manifest = JSON.parse(readFileSync(join(root, 'examples/plugins/durable-driver/package.json'), 'utf8'));
  manifest.dependencies['@wuu/plugin-sdk'] = `file:${sdk}`;
  writeFileSync(join(fixture, 'package.json'), JSON.stringify(manifest, null, 2));
  command('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', '--package-lock=false'], fixture);
  command('npm', ['run', 'build'], fixture);
  result.checks.push('Independent SDK tarball installation and TypeScript build');
  result.artifacts = Object.fromEntries(['runtime.ts', 'state.ts', 'orchestration.ts', 'build.mjs', 'plugin.json', 'dist/runtime.js'].map(name => [name, createHash('sha256').update(readFileSync(join(fixture, name))).digest('hex')]));
  result.environment = { node: process.version, go: command('go', ['version']).trim(), commit: command('git', ['rev-parse', 'HEAD']).trim() };
  const evidence = join(run, 'process');
  mkdirSync(evidence);
  command('go', ['test', '-race', './internal/runtime', '-run', '^TestDurableDriverProcess$', '-count=1', '-v'], root, { WUU_DURABLE_FIXTURE: fixture, WUU_DURABLE_EVIDENCE: evidence });
  for (const scenario of ['complete', 'killed-before-tools', 'killed-after-model-archive', 'killed-after-settled-effect', 'killed-after-tool-archive', 'killed-after-committed-tool-error', 'killed-before-effect-receipt', 'killed-finalizing']) {
    const observed = JSON.parse(readFileSync(join(evidence, scenario, 'result.json'), 'utf8'));
    if (observed.scenario !== scenario) throw new Error(`missing process evidence: ${scenario}`);
    result.checks.push(observed);
  }
  result.status = 'passed';
} catch (error) {
  result.error = String(error);
  process.exitCode = 1;
} finally {
  writeFileSync(join(run, 'report.json'), JSON.stringify(result, null, 2));
  console.log(`Durable driver ${result.status}. Evidence: ${run}`);
}
