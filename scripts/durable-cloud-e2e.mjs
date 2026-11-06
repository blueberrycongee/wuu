#!/usr/bin/env node
// Socket-free acceptance: independent plugin processes and native persistent
// services, reached through authenticated appserver handlers.
import { execFileSync } from 'node:child_process';
import { appendFileSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = resolve(process.env.WUU_DURABLE_OUTPUT || join(root, '.tmp/e2e/durable-cloud'));
mkdirSync(output, { recursive: true });
const run = mkdtempSync(join(output, 'cloud-'));
const report = {
  status: 'failed',
  checks: [],
  limitations: [
    'Appserver handler ingress is in-process; no browser, network socket transport, or client transport-cursor replay is exercised.',
    'The task-read ingress scenario uses seeded metadata. The approval scenario uses actual controlled inference and a real file effect.',
    'Cloud model integration uses direct tool calls. Separate real-plugin and reference-worker scenarios verify PTC journal recovery.',
    'Fleet deployment and rendered clients are not covered. The approval effect uses the reference execution worker over a command/stdio adapter.',
  ],
};
function command(program, args, env = {}) {
  const log = join(run, 'commands.log');
  appendFileSync(log, `$ ${program} ${args.join(' ')}\n`);
  try {
    const result = execFileSync(program, args, {
      cwd: root, env: { ...process.env, ...env }, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024,
    });
    appendFileSync(log, result);
    process.stdout.write(result);
    return result;
  } catch (error) {
    appendFileSync(log, `${error.stdout || ''}${error.stderr || ''}`);
    throw error;
  }
}
try {
  let fixture = process.env.WUU_DURABLE_FIXTURE;
  let resetComplete = process.env.WUU_DURABLE_RESET_COMPLETE_FIXTURE;
  let resetContext = process.env.WUU_DURABLE_RESET_CONTEXT_FIXTURE;
  if (!fixture) {
    const previous = new Set(readdirSync(output));
    command(process.execPath, ['scripts/durable-driver-e2e.mjs'], { WUU_DURABLE_OUTPUT: output });
    const reports = readdirSync(output)
      .filter(name => name.startsWith('driver-') && !previous.has(name))
      .map(name => join(output, name, 'report.json')).filter(existsSync);
    if (reports.length !== 1) throw new Error('Expected one new independent driver build report');
    const driver = JSON.parse(readFileSync(reports[0], 'utf8'));
    if (driver.status !== 'passed') throw new Error('Driver process acceptance did not pass');
    fixture = driver.fixture;
    resetComplete = driver.reset_complete_crash_fixture;
    resetContext = driver.reset_context_crash_fixture;
    report.checks.push({ driver_report: reports[0] });
  }
  fixture = resolve(fixture);
  if (!existsSync(join(fixture, 'dist/runtime.js'))) {
    throw new Error('WUU_DURABLE_FIXTURE must name a built independent plugin');
  }
  if (!resetComplete || !resetContext) throw new Error('Reset crash fixtures are required for complete cloud acceptance');
  report.build_fixture = fixture;
  const installed = join(run, 'installed-plugin');
  // Activate the actual distribution, without build dependencies or npm symlinks.
  if (!existsSync(join(fixture, 'package/plugin.json'))) {
    throw new Error('Independent fixture lacks a built installable package');
  }
  cpSync(join(fixture, 'package'), installed, { recursive: true });
  fixture = installed;
  report.fixture = fixture;
  report.commit = command('git', ['rev-parse', 'HEAD']).trim();
  const worker = join(run, 'wuu');
  command('go', ['build', '-o', worker, './cmd/wuu']);
  const unknownEvidence = join(run, 'worker-unknown');
  mkdirSync(unknownEvidence);
  command('go', ['test', '-race', './internal/agent', '-run', '^TestDurableWorkerUnknownOutcomeContractProbe$', '-count=1', '-v'], {
    WUU_DURABLE_PROBE_EVIDENCE: unknownEvidence,
  });
  const markers = ['0', '1'].map(kind => join(unknownEvidence, `worker-effect-${kind}.txt`));
  for (const marker of markers) {
    if (readFileSync(marker, 'utf8') !== 'effect\n') throw new Error('Lost-receipt worker effect count is not one');
  }
  report.checks.push({ scenario: 'worker-effect-before-lost-receipt', markers, outcome: 'unknown; no failed receipt or replay' });
  command('go', ['test', '-race', './internal/appserver', '-run', '^TestDurable(Cloud.*|Ingress.*)$', '-count=1', '-timeout=20m', '-v'], {
    WUU_DURABLE_FIXTURE: fixture, WUU_DURABLE_EVIDENCE: run, WUU_DURABLE_WORKER: worker,
    WUU_DURABLE_RESET_COMPLETE_FIXTURE: resetComplete, WUU_DURABLE_RESET_CONTEXT_FIXTURE: resetContext,
  });
  const observed = JSON.parse(readFileSync(join(run, 'cloud-ingress.json'), 'utf8'));
  if (observed.scenario !== 'authenticated-process-ingress' || !observed.reconnect_kept_wait || !observed.unknown_dispatch_rejected) {
    throw new Error('Missing cloud ingress evidence');
  }
  report.checks.push(observed);
  const approval = JSON.parse(readFileSync(join(run, 'cloud-approval-effect.json'), 'utf8'));
  if (approval.scenario !== 'model-approval-effect' || approval.effect_before_approval !== false || approval.marker_receipts !== 1 || approval.execution_backend !== 'reference-worker-stdio') {
    throw new Error('Missing actual model approval effect evidence');
  }
  report.checks.push(approval);
  const crash = JSON.parse(readFileSync(join(run, 'cloud-approval-crash.json'), 'utf8'));
  if (!crash.plugin_killed_while_waiting || crash.marker_receipts !== 1 || crash.execution_backend !== 'reference-worker-stdio') {
    throw new Error('Missing pending-approval process-kill recovery evidence');
  }
  report.checks.push(crash);
  for (const scenario of ['cloud-child-crash', 'cloud-inbox', 'cloud-abort-continue', 'cloud-input-plugin-boundary', 'cloud-wait-input', 'cloud-wait-event', 'cloud-wait-plugin-event', 'cloud-sleep-auto', 'cloud-sleep-cancel', 'cloud-sleep-restart', 'cloud-context-reset', 'cloud-context-handoff', 'cloud-context-compact', 'cloud-child-false', 'cloud-child-true', 'cloud-child-cancel-foreground', 'cloud-child-cancel-background-default', 'cloud-child-cancel-background-inclusive', 'cloud-failed-owner-cleanup', 'cloud-held-completion-recovery', 'cloud-inclusive-completed-owner', 'cloud-active-child-shutdown', 'cloud-active-root-recovery', 'cloud-killed-host-root', 'cloud-killed-host-child', 'cloud-background-survives-failure', 'cloud-context-semantics-empty-reset', 'cloud-context-semantics-reset-with-note', 'cloud-context-semantics-handoff', 'cloud-model-retry-automatic', 'cloud-model-retry-same-text-user', 'cloud-model-retry-cas-conflict', 'cloud-model-retry-restart-before-deadline', 'cloud-model-retry-restart-overdue', 'cloud-model-retry-cancel', 'cloud-model-retry-exhausted', 'cloud-historical-fork', 'cloud-historical-first-input-fork', 'cloud-historical-code-store', 'cloud-reset-crash-complete', 'cloud-reset-crash-context', 'cloud-automatic-compaction-threshold-failure', 'cloud-automatic-compaction-overflow-once', 'cloud-automatic-compaction-overflow-repeated', 'cloud-automatic-compaction-background']) {
    const artifact = join(run, `${scenario}.json`);
    JSON.parse(readFileSync(artifact, 'utf8'));
    report.checks.push({ scenario, artifact });
  }
  command('go', ['test', '-race', './internal/executionworker', '-run', '^TestWorker(TransportResultViews|TransportProgramCancellation|TransportStateIsActorScopedAndVolatile|TransportUnknownCallbackCannotBeCaught|DurableJournalRestart)$', '-count=1', '-v'], { WUU_DURABLE_EVIDENCE: run });
  for (const backend of ['jsonl', 'sqlite']) {
    for (const fault of ['before_delivery_commit', 'after_delivery_commit']) {
      const dir = join(run, `remote-code-journal-${backend}-${fault}`);
      const assertions = JSON.parse(readFileSync(join(dir, 'assertions.json'), 'utf8'));
      if (assertions.backend !== backend || assertions.fault !== fault ||
          !Object.values(assertions.assertions).every(value => value === true)) {
        throw new Error(`Missing remote journal assertions: ${backend}/${fault}`);
      }
      for (const artifact of ['effects-before-restart.txt', 'effects.txt', 'before-restart.json', 'after-restart.json', 'results.json']) readFileSync(join(dir, artifact));
      report.checks.push({ scenario: `remote-code-journal-${backend}-${fault}`, artifact: join(dir, 'assertions.json'), ...assertions });
    }
  }
  report.checks.push({ scenario: 'reference-worker-stdio', scope: 'separate subprocess protocol acceptance, not durable driver execution placement' });
  report.status = 'passed';
} catch (error) {
  report.error = String(error);
  process.exitCode = 1;
} finally {
  report.artifacts = readdirSync(run).filter(name => name.endsWith('.json') && name !== 'report.json').map(name => join(run, name));
  writeFileSync(join(run, 'report.json'), JSON.stringify(report, null, 2));
  console.log(`Durable cloud ${report.status}. Evidence: ${run}`);
}
