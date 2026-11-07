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
    for (const name of ['GO_VALIDATION_RESULT', 'GO_RACE_RESULT', 'ARCHITECTURE_RESULT']) {
      assert.equal(passes({ [name]: result }), false, `${name}=${result}`);
    }
  }
  assert.equal(passes({ SELECTION_RESULT: 'failure' }), false);
});

test('unselected checks must be skipped, not failed or run unexpectedly', () => {
  const unselected = {
    GO_REQUIRED: 'false', GO_VALIDATION_RESULT: 'skipped', GO_RACE_RESULT: 'skipped',
    ARCHITECTURE_REQUIRED: 'false', ARCHITECTURE_RESULT: 'skipped',
  };
  assert.equal(passes(unselected), true);
  for (const name of ['GO_VALIDATION_RESULT', 'GO_RACE_RESULT', 'ARCHITECTURE_RESULT']) {
    assert.equal(passes({ ...unselected, [name]: 'failure' }), false, name);
    assert.equal(passes({ ...unselected, [name]: 'success' }), false, name);
  }
});

// This boundary previously existed only as a manual development command. Its
// result must fail the required job, not become an optional diagnostic workflow.
test('architecture conformance requires the MCP reload executable and its evidence', () => {
  const architecture = workflow.split('\n  architecture-conformance:\n')[1]?.split('\n  go-check:\n')[0];
  const step = architecture?.split('      - name: Verify MCP authentication across compatible reloads\n')[1]?.split('      - name: ')[0];
  assert.ok(step, 'selected-service MCP reload must run in the required architecture job');
  assert.doesNotMatch(step, /continue-on-error:/);
  assert.match(step, /set -euo pipefail/);
  assert.match(step, /go build.*-ldflags.*internal\/version.Version/);
  assert.match(step, /\.tmp\/bin\/mcp-auth-reload-e2e 2>&1 \| tee/);
  assert.match(architecture, /node \.github\/scripts\/check-mcp-auth-evidence\.mjs/);
  assert.match(architecture, /\.tmp\/e2e\/mcp-auth-reload\/results\.json/);
  assert.match(architecture, /\.tmp\/e2e\/mcp-auth-reload\/source-commit\.txt/);
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

test('Desktop gate requires both selected partitions and exactly skips unselected groups', () => {
  for (const suites of [[], ['unit', 'unit-extra'], ['layout'], ['unit', 'unit-extra', 'layout', 'session']]) {
    const e2e = suites.filter(suite => !suite.startsWith('unit'));
    const env = {
      SELECTION_RESULT: 'success', DESKTOP_REQUIRED: String(suites.length > 0),
      DESKTOP_SUITES: JSON.stringify(suites), E2E_SUITES: JSON.stringify(e2e),
      UNIT_RESULT: suites.includes('unit') ? 'success' : 'skipped',
      SUITES_RESULT: e2e.length ? 'success' : 'skipped',
    };
    assert.equal(gatePasses('desktop-check', env), true, JSON.stringify(suites));
    for (const name of ['UNIT_RESULT', 'SUITES_RESULT']) {
      for (const result of ['failure', 'cancelled', 'skipped', 'success', '']) {
        assert.equal(gatePasses('desktop-check', { ...env, [name]: result }), result === env[name], `${suites}: ${name}=${result}`);
      }
    }
    for (const result of ['failure', 'cancelled', 'skipped', '']) {
      assert.equal(gatePasses('desktop-check', { ...env, SELECTION_RESULT: result }), false);
    }
    for (const bad of ['', 'garbage', 'TRUE']) {
      assert.equal(gatePasses('desktop-check', { ...env, DESKTOP_REQUIRED: bad }), false);
    }
    assert.equal(gatePasses('desktop-check', { ...env, DESKTOP_SUITES: '' }), false);
    assert.equal(gatePasses('desktop-check', { ...env, E2E_SUITES: '["unexpected"]' }), false);
  }
  const full = { SELECTION_RESULT: 'success', DESKTOP_REQUIRED: 'true', DESKTOP_SUITES: '["unit","unit-extra","layout","session"]', E2E_SUITES: '["layout"]', UNIT_RESULT: 'success', SUITES_RESULT: 'success' };
  assert.equal(gatePasses('desktop-check', full), false, 'a successful subset cannot omit a selected E2E suite');
  assert.equal(gatePasses('desktop-check', { ...full, DESKTOP_SUITES: '["unit"]', E2E_SUITES: '[]', SUITES_RESULT: 'skipped' }), false, 'both unit shards remain required');
});

test('unit work has no artifact dependency while the stable Desktop gate joins both partitions', () => {
  const unit = workflow.split('\n  desktop-unit:\n')[1].split('\n  desktop-suite:\n')[0];
  const e2e = workflow.split('\n  desktop-suite:\n')[1].split('\n  desktop-macos-controls:\n')[0];
  const gate = workflow.split('\n  desktop-check:\n')[1].split('\n  clients-check:\n')[0];
  assert.match(unit, /^    needs: changes$/m);
  assert.match(unit, /contains\(fromJSON\(needs.changes.outputs.desktop_suites\), 'unit'\)/);
  assert.match(unit, /suite: \[unit, unit-extra\]/);
  assert.doesNotMatch(unit, /actions\/download-artifact/);
  assert.match(e2e, /needs: \[changes, repository-check\]/);
  assert.match(e2e, /fromJSON\(needs.changes.outputs.desktop_e2e_suites\)/);
  assert.match(gate, /needs: \[changes, desktop-unit, desktop-suite\]/);
});

test('final CI gate never reports unsuccessful required jobs as passing', () => {
  const needs = Object.fromEntries([
    'changes', 'repository-check', 'go-validation', 'go-race', 'architecture-conformance', 'desktop-unit', 'desktop-suite',
    'desktop-macos-controls', 'clients-check', 'macos-check', 'windows-check', 'linux-check',
  ].map(job => [job, { result: 'success' }]));
  needs.changes.outputs = { go: 'true', architecture: 'true', desktop: 'true', desktop_suites: '["unit","unit-extra","shell"]', desktop_e2e_suites: '["shell"]', clients: 'true', native: 'true' };
  const passes = () => gatePasses('ci-check', { RESULTS: JSON.stringify(needs), EVENT: 'pull_request' });
  assert.equal(passes(), true);
  for (const job of Object.keys(needs)) {
    for (const result of ['failure', 'cancelled', 'skipped', '']) {
      needs[job].result = result;
      assert.equal(passes(), false, `${job}=${result}`);
    }
    needs[job].result = 'success';
  }
  needs.changes.outputs = { go: 'false', architecture: 'false', desktop: 'false', desktop_suites: '[]', desktop_e2e_suites: '[]', clients: 'false', native: 'false' };
  for (const job of ['go-validation', 'go-race', 'architecture-conformance', 'desktop-unit', 'desktop-suite', 'desktop-macos-controls', 'clients-check', 'macos-check', 'windows-check', 'linux-check']) {
    needs[job].result = 'skipped';
  }
  assert.equal(passes(), true, 'unrelated paths permit only unselected jobs to skip');
  needs['repository-check'].result = 'skipped';
  assert.equal(passes(), false, 'pull requests still require repository checks');
  assert.equal(gatePasses('ci-check', { RESULTS: JSON.stringify(needs), EVENT: 'push' }), true);
});

test('final gate joins real jobs directly without an extra gate hop', () => {
  const block = workflow.split('\n  ci-check:\n')[1];
  const dependencies = block.match(/needs: \[([^\]]+)\]/)[1].split(',').map(value => value.trim());
  assert.deepEqual(dependencies, ['changes', 'repository-check', 'go-validation', 'go-race', 'architecture-conformance', 'desktop-unit', 'desktop-suite', 'desktop-macos-controls', 'clients-check', 'macos-check', 'linux-check', 'windows-check']);
});

for (const name of ['ci', 'docs', 'windows-desktop', 'remote-service', 'native-mobile']) {
  test(`${name} preserves every push run while superseding PR runs`, () => {
    const source = readFileSync(`.github/workflows/${name}.yml`, 'utf8');
    const concurrency = source.split('\nconcurrency:\n')[1].split('\njobs:')[0];
    assert.match(concurrency, /github.event_name == 'pull_request' && 'pr' \|\| github.run_id/);
    assert.match(concurrency, /cancel-in-progress: \$\{\{ github.event_name == 'pull_request' \}\}/);
  });
}
