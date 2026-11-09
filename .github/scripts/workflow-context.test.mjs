import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

// Guard the block-style job env mappings used by CI. Runner context exists
// only after job scheduling; it is valid in step env, but not job env.
function jobEnvWithRunner(source) {
  return [...source.matchAll(/^    env:\n((?:      .*\n)*)/gm)]
    .filter(([, env]) => /\$\{\{[^}]*\brunner\./.test(env));
}

test('CI job env does not reference unavailable runner context', () => {
  assert.deepEqual(jobEnvWithRunner(readFileSync('.github/workflows/ci.yml', 'utf8')), []);
});

test('runner-context guard distinguishes job env from step env', () => {
  assert.equal(jobEnvWithRunner('    env:\n      OUTPUT: ${{ runner.temp }}/evidence\n').length, 1);
  assert.equal(jobEnvWithRunner('        env:\n          OUTPUT: ${{ runner.temp }}/evidence\n').length, 0);
});
