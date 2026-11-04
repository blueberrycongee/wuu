import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

test('package scheduling retains every non-appserver package and propagates Go failures', () => {
  const root = mkdtempSync(join(tmpdir(), 'wuu-go-packages-'));
  const prefix = 'github.com/blueberrycongee/wuu/';
  const inventory = ['cmd/wuu', 'internal/agent', 'internal/appserver', 'internal/codemode', 'internal/new-package', 'internal/runtime', 'internal/tools', 'prompts'];
  try {
    writeFileSync(join(root, 'go'), `#!/bin/sh
if [ "$1" = list ]; then
  if [ "$FAIL_LIST" = 1 ]; then exit 9; fi
  cat "$INVENTORY"
else
  printf '%s\\n' "$@" > "$RECORD"
  if [ "$FAIL_TEST" = 1 ]; then exit 7; fi
fi
`, { mode: 0o755 });
    writeFileSync(join(root, 'inventory'), inventory.map(p => prefix + p).join('\n') + '\n');
    for (const failure of [{}, { FAIL_LIST: '1' }, { FAIL_TEST: '1' }]) {
      const record = join(root, 'record');
      rmSync(record, { force: true });
      const result = spawnSync('make', ['-f', resolve('Makefile'), 'test-go-packages'], {
        encoding: 'utf8', env: { ...process.env, PATH: `${root}:${process.env.PATH}`, INVENTORY: join(root, 'inventory'), RECORD: record, FAIL_LIST: '0', FAIL_TEST: '0', ...failure },
      });
      assert.equal(result.status === 0, Object.keys(failure).length === 0, result.stdout + result.stderr);
      if (failure.FAIL_LIST) {
        assert.throws(() => readFileSync(record));
        continue;
      }
      const [command, ...packages] = readFileSync(record, 'utf8').trim().split('\n');
      assert.equal(command, 'test');
      assert.deepEqual([...packages].sort(), inventory.filter(p => p !== 'internal/appserver').map(p => prefix + p).sort());
      assert.deepEqual(new Set(packages.slice(0, 4)), new Set(['cmd/wuu', 'internal/codemode', 'internal/runtime', 'internal/tools'].map(p => prefix + p)));
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});
