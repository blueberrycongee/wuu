const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join, win32 } = require('node:path');
const { test } = require('node:test');
const { runInNewContext } = require('node:vm');

test('Windows zip extraction bypasses Git Bash GNU tar and keeps drive-letter paths', () => {
  const calls = [];
  const sandbox = {
    module: { exports: {} },
    process: { platform: 'win32', env: { SystemRoot: 'C:\\Windows', PATH: 'C:\\Program Files\\Git\\usr\\bin' } },
    require(name) {
      if (name === 'node:path') return win32;
      if (name === 'node:child_process') return { spawnSync: (...args) => { calls.push(args); return { status: 0 }; } };
      return require(name);
    },
  };
  runInNewContext(readFileSync(join(__dirname, 'fetch-ripgrep.cjs'), 'utf8'), sandbox);
  sandbox.module.exports.extract('C:\\Temp\\ripgrep.zip', 'zip', 'C:\\Temp\\unpacked');
  assert.equal(calls[0][0], 'C:\\Windows\\System32\\tar.exe');
  assert.deepEqual(Array.from(calls[0][1]), ['-xf', 'C:\\Temp\\ripgrep.zip', '-C', 'C:\\Temp\\unpacked']);
});
