import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
const workflow = readFileSync(new URL('../workflows/ci.yml', import.meta.url), 'utf8');
function script() {
  const block = workflow.match(/      - name: Check repository and prepare selected Desktop build\n([\s\S]*?)(?=      - )/);
  assert.ok(block, 'joined producer preparation step exists');
  return block[1].split('        run: |\n')[1].split('\n').map(line => line.slice(10)).join('\n');
}
for (const failure of ['', 'contracts', 'metadata', 'npm-desktop', 'npm-core']) {
  test(`producer preparation joins all eligible work: ${failure || 'success'}`, () => {
    verify({ failure, required: 'true', selection: 'success', cache: 'success', failed: !!failure, npm: true });
  });
}
for (const mode of [
  { label: 'docs-only', required: 'false', selection: 'success', cache: 'skipped', failed: false, npm: false },
  { label: 'unit-only', required: 'false', selection: 'success', cache: 'skipped', failed: false, npm: false },
  { label: 'selector failure', required: 'false', selection: 'failure', cache: 'skipped', failed: true, npm: false },
  { label: 'cache failure', required: 'true', selection: 'success', cache: 'failure', failed: true, npm: false },
]) test(`producer preserves diagnostic checks without heavy prep: ${mode.label}`, () => verify(mode));
function verify({ failure = '', required, selection, cache, failed, npm }) {
  const root = mkdtempSync(join(tmpdir(), 'wuu-producer-join-'));
  try {
    const bin = join(root, 'bin'); mkdirSync(bin);
    for (const name of ['node', 'make', 'npm']) {
      const label = name === 'node' ? 'contracts' : name === 'make' ? 'metadata' : 'npm-${3##*/}';
      writeFileSync(join(bin, name), `#!/bin/bash\nset -eu\nlabel="${label}"\necho "$label:start" >> "$RECORD"\nsleep .03\necho "$label:done" >> "$RECORD"\n[ "$FAILURE" != "$label" ]\n`, { mode: 0o755 });
    }
    const record = join(root, 'record');
    const result = spawnSync('bash', ['--noprofile', '--norc', '-e', '-o', 'pipefail'], {
      input: `trap 'cp \"$RECORD\" \"$BOUNDARY\"' EXIT\n${script()}`, encoding: 'utf8', timeout: 5000,
      env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, RECORD: record, BOUNDARY: join(root, 'boundary'), FAILURE: failure, BUILD_REQUIRED: required, BUILD_SELECTION_RESULT: selection, BUILD_NODE_RESULT: cache },
    });
    assert.equal(result.status, failed ? 1 : 0, result.stderr);
    const lines = readFileSync(join(root, 'boundary'), 'utf8').trim().split('\n');
    assert.ok(lines.includes('contracts:done'));
    assert.equal(lines.includes('metadata:done'), failure !== 'contracts');
    for (const name of ['desktop', 'core']) assert.equal(lines.includes(`npm-${name}:done`), npm, lines.join(','));
  } finally { rmSync(root, { recursive: true, force: true }); }
}

test('missing-wait red control cannot hide behind inherited child output pipes', () => {
  const root = mkdtempSync(join(tmpdir(), 'wuu-producer-red-'));
  try {
    const bin = join(root, 'bin'); mkdirSync(bin);
    const record = join(root, 'record'), boundary = join(root, 'boundary');
    writeFileSync(record, '');
    for (const name of ['contracts', 'npm-desktop', 'npm-core']) {
      assert.equal(spawnSync('mkfifo', [join(root, name)]).status, 0);
    }
    for (const name of ['node', 'make', 'npm']) {
      const label = name === 'node' ? 'contracts' : name === 'make' ? 'metadata' : 'npm-${3##*/}';
      writeFileSync(join(bin, name), `#!/bin/bash\nset -eu\nlabel="${label}"\necho "$label:start" >> "$RECORD"\nif [ "$label" != metadata ]; then read -r token < "$ROOT/$label"; fi\necho "$label:done" >> "$RECORD"\n`, { mode: 0o755 });
    }
    const mutated = script().replace('for pid in "${pids[@]}"; do wait "$pid" || status=1; done', ': # intentionally omitted waits');
    assert.notEqual(mutated, script());
    // Hold every worker on a FIFO until the shell reaches EXIT. This makes the
    // negative control deterministic; inherited stdout pipes finish only later.
    const result = spawnSync('bash', ['--noprofile', '--norc', '-e', '-o', 'pipefail'], {
      input: `trap 'cp "$RECORD" "$BOUNDARY"; for name in contracts npm-desktop npm-core; do echo release > "$ROOT/$name"; done' EXIT\n${mutated}`,
      encoding: 'utf8', timeout: 5000,
      env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, ROOT: root, RECORD: record, BOUNDARY: boundary, BUILD_REQUIRED: 'true', BUILD_SELECTION_RESULT: 'success', BUILD_NODE_RESULT: 'success' },
    });
    assert.equal(result.status, 0, result.stderr);
    const atBoundary = readFileSync(boundary, 'utf8');
    const afterPipesClosed = readFileSync(record, 'utf8');
    for (const label of ['contracts', 'npm-desktop', 'npm-core']) {
      assert.ok(!atBoundary.includes(`${label}:done`), atBoundary);
      assert.ok(afterPipesClosed.includes(`${label}:done`), afterPipesClosed);
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});
