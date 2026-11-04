import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

function verify(mode, failure = '') {
  const directory = mkdtempSync(join(tmpdir(), 'wuu-native-ci-'));
  const calls = join(directory, 'calls');
  try {
    for (const tool of ['go', 'swift', 'xcodebuild']) {
      writeFileSync(join(directory, tool), `#!/usr/bin/env node
const fs = require('node:fs');
fs.appendFileSync(process.env.CALLS, JSON.stringify({tool: '${tool}', args: process.argv.slice(2), host: process.env.WUU_NATIVE_TESTHOST, account: process.env.WUU_NATIVE_TESTACCOUNT}) + '\\n');
process.exit(process.env.FAIL_TOOL === '${tool}' ? 1 : 0);
`, { mode: 0o755 });
    }
    writeFileSync(calls, '');
    const result = spawnSync('bash', ['clients/native/verify.sh', mode], { encoding: 'utf8', env: {
      ...process.env, PATH: `${directory}:${process.env.PATH}`, CALLS: calls,
      WUU_NATIVE_PRIVATE_DATABASE: '1', WUU_NATIVE_SKIP_GO_TESTS: '0', FAIL_TOOL: failure,
    } });
    return { status: result.status, calls: readFileSync(calls, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) };
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

test('standalone iOS verification retains core, real-host Swift tests and simulator build', () => {
  const result = verify('ios');
  assert.equal(result.status, 0);
  assert.deepEqual(result.calls.map(call => call.tool), ['go', 'go', 'go', 'go', 'swift', 'xcodebuild']);
  const swift = result.calls.find(call => call.tool === 'swift');
  assert(swift.host.endsWith('/clients/native/.build/testhost'));
  assert(swift.account.endsWith('/clients/native/.build/testaccount'));
});

test('CI core lane executes the same integration commands without a simulator build', () => {
  const full = verify('ios');
  const core = verify('ios-core');
  assert.equal(core.status, 0);
  assert.deepEqual(core.calls, full.calls.filter(call => call.tool !== 'xcodebuild'));
});

test('core and platform build failures propagate to the caller', () => {
  for (const tool of ['go', 'swift', 'xcodebuild']) assert.notEqual(verify('ios', tool).status, 0, tool);
  assert.notEqual(verify('ios-core', 'swift').status, 0);
  assert.notEqual(verify('invalid').status, 0);
});
