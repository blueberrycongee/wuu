const { test } = require('node:test');
const assert = require('node:assert/strict');
const { mkdtempSync, writeFileSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { saveCheckpoint, verifyCheckpoint } = require('./release-checkpoint.cjs');

test('release checkpoint binds saved signed bytes to source, version and stage', () => {
  const root = mkdtempSync(join(tmpdir(), 'wuu-checkpoint-'));
  const identity = { commit: 'a'.repeat(40), version: '2026.10.16', stage: 'signed-app' };
  try {
    writeFileSync(join(root, 'signed-app.zip'), 'signed bytes');
    saveCheckpoint(root, identity, ['signed-app.zip']);
    assert.doesNotThrow(() => verifyCheckpoint(root, identity));
    for (const mismatch of [{ commit: 'b'.repeat(40) }, { version: '2026.10.17' }, { stage: 'distribution' }]) {
      assert.throws(() => verifyCheckpoint(root, { ...identity, ...mismatch }), /identity/);
    }
    writeFileSync(join(root, 'signed-app.zip'), 'changed bytes');
    assert.throws(() => verifyCheckpoint(root, identity), /hash/);
    assert.throws(() => saveCheckpoint(root, identity, ['../outside']), /filename/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
