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
