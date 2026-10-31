import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const workflow = readFileSync('.github/workflows/ci.yml', 'utf8');
const section = workflow.split('\n  go-check:\n')[1]?.split('\n  desktop-suite:\n')[0];
assert.ok(section, 'Go check must remain a distinct merge gate');
const script = section.match(/^        run: \|\n((?: {10}.*\n)+)/m)?.[1]
  .split('\n').map(line => line.slice(10)).join('\n');
assert.ok(script, 'Go check must have an executable gate');

const selected = {
  SELECTION_RESULT: 'success',
  GO_REQUIRED: 'true',
  GO_VALIDATION_RESULT: 'success',
  GO_RACE_RESULT: 'success',
  PROJECT_AGENT_RESULT: 'success',
  ARCHITECTURE_REQUIRED: 'true',
  ARCHITECTURE_RESULT: 'success',
};
function passes(overrides = {}) {
  return spawnSync('bash', ['-e', '-o', 'pipefail', '-c', script], {
    env: { ...process.env, ...selected, ...overrides },
  }).status === 0;
}

test('the Go gate waits for the independent race job', () => {
  assert.match(section, /needs: \[[^\]]*\bgo-race\b[^\]]*\]/);
  assert.match(section, /GO_RACE_RESULT: \$\{\{ needs\.go-race\.result \}\}/);
});

test('selected Go and architecture checks must all succeed', () => {
  assert.equal(passes(), true);
  for (const result of ['failure', 'cancelled', 'skipped', '']) {
    for (const name of ['GO_VALIDATION_RESULT', 'GO_RACE_RESULT', 'PROJECT_AGENT_RESULT', 'ARCHITECTURE_RESULT']) {
      assert.equal(passes({ [name]: result }), false, `${name}=${result}`);
    }
  }
  assert.equal(passes({ SELECTION_RESULT: 'failure' }), false);
});

test('unselected checks must be skipped, not failed or run unexpectedly', () => {
  const unselected = {
    GO_REQUIRED: 'false', GO_VALIDATION_RESULT: 'skipped', GO_RACE_RESULT: 'skipped',
    PROJECT_AGENT_RESULT: 'skipped', ARCHITECTURE_REQUIRED: 'false', ARCHITECTURE_RESULT: 'skipped',
  };
  assert.equal(passes(unselected), true);
  for (const name of ['GO_VALIDATION_RESULT', 'GO_RACE_RESULT', 'PROJECT_AGENT_RESULT', 'ARCHITECTURE_RESULT']) {
    assert.equal(passes({ ...unselected, [name]: 'failure' }), false, name);
    assert.equal(passes({ ...unselected, [name]: 'success' }), false, name);
  }
});

// These jobs must still evaluate failed/skipped dependencies, but must not keep
// a superseded workflow alive in the per-ref concurrency group.
for (const job of ['go-check', 'desktop-check', 'ci-check']) {
  test(`${job} remains eligible after failure but stops on workflow cancellation`, () => {
    const block = workflow.split(`\n  ${job}:\n`)[1]?.split(/\n  [\w-]+:\n/)[0];
    assert.match(block, /^    if: \$\{\{ !cancelled\(\) \}\}$/m);
  });
}

function gateScript(job) {
  const block = workflow.split(`\n  ${job}:\n`)[1]?.split(/\n  [\w-]+:\n/)[0];
  const body = block?.match(/^        run: \|\n((?: {10}.*\n)+)/m)?.[1];
  assert.ok(body, `${job} must have an executable gate`);
  return body.split('\n').map(line => line.slice(10)).join('\n');
}

function gatePasses(job, env) {
  return spawnSync('bash', ['-e', '-o', 'pipefail', '-c', gateScript(job)], {
    env: { ...process.env, ...env },
  }).status === 0;
}

test('Desktop gate rejects unsuccessful selected suites and selection', () => {
  const env = { SELECTION_RESULT: 'success', DESKTOP_REQUIRED: 'true', SUITES_RESULT: 'success' };
  assert.equal(gatePasses('desktop-check', env), true);
  for (const result of ['failure', 'cancelled', 'skipped', '']) {
    assert.equal(gatePasses('desktop-check', { ...env, SUITES_RESULT: result }), false, result);
    assert.equal(gatePasses('desktop-check', { ...env, SELECTION_RESULT: result }), false, result);
  }
  const unselected = { ...env, DESKTOP_REQUIRED: 'false', SUITES_RESULT: 'skipped' };
  assert.equal(gatePasses('desktop-check', unselected), true);
  for (const result of ['failure', 'cancelled', 'success', '']) {
    assert.equal(gatePasses('desktop-check', { ...unselected, SUITES_RESULT: result }), false, result);
  }
});

test('final CI gate never reports unsuccessful required jobs as passing', () => {
  const needs = Object.fromEntries([
    'changes', 'repository-check', 'go-check', 'desktop-check',
    'desktop-macos-controls', 'clients-check', 'macos-check', 'windows-check', 'linux-check',
  ].map(job => [job, { result: 'success' }]));
  needs.changes.outputs = { desktop: 'true', desktop_suites: '["shell"]', clients: 'true', native: 'true' };
  const passes = () => gatePasses('ci-check', { RESULTS: JSON.stringify(needs), EVENT: 'pull_request' });
  assert.equal(passes(), true);
  for (const job of Object.keys(needs)) {
    for (const result of ['failure', 'cancelled', 'skipped', '']) {
      needs[job].result = result;
      assert.equal(passes(), false, `${job}=${result}`);
    }
    needs[job].result = 'success';
  }
  needs.changes.outputs = { desktop: 'false', desktop_suites: '[]', clients: 'false', native: 'false' };
  for (const job of ['desktop-macos-controls', 'clients-check', 'macos-check', 'windows-check', 'linux-check']) {
    needs[job].result = 'skipped';
  }
  assert.equal(passes(), true, 'unrelated paths permit only unselected jobs to skip');
  needs['repository-check'].result = 'skipped';
  assert.equal(passes(), false, 'pull requests still require repository checks');
  assert.equal(gatePasses('ci-check', { RESULTS: JSON.stringify(needs), EVENT: 'push' }), true);
});
