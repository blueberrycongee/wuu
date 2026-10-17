const { test } = require('node:test');
const assert = require('node:assert/strict');
const { mkdtempSync, writeFileSync, readFileSync, mkdirSync, copyFileSync, rmSync } = require('node:fs');
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

const distributionFiles = ['wuu-mac-arm64.dmg', 'wuu-mac-arm64.zip', 'wuu-mac-arm64.zip.blockmap', 'signed-dmg.dmg', 'latest-mac.yml'];

test('distribution checkpoints bind every consumed artifact and the submitted DMG', () => {
  const root = mkdtempSync(join(tmpdir(), 'wuu-distribution-checkpoint-'));
  const identity = { commit: 'a'.repeat(40), version: '2026.10.16', stage: 'distribution' };
  try {
    for (const name of distributionFiles) writeFileSync(join(root, name), 'signed bytes');
    saveCheckpoint(root, identity, distributionFiles);
    assert.doesNotThrow(() => verifyCheckpoint(root, identity));
    const complete = readFileSync(join(root, 'checkpoint.json'), 'utf8');
    for (const missing of distributionFiles) {
      const manifest = JSON.parse(complete);
      delete manifest.sha256[missing];
      writeFileSync(join(root, 'checkpoint.json'), JSON.stringify(manifest));
      assert.throws(() => verifyCheckpoint(root, identity), /missing|incomplete/i);
    }
    writeFileSync(join(root, 'signed-dmg.dmg'), 'different signed bytes');
    saveCheckpoint(root, identity, distributionFiles);
    assert.throws(() => verifyCheckpoint(root, identity), /DMG.*match/i);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('a checkpoint cannot leave a consumed archive or update manifest unbound', () => {
  const root = mkdtempSync(join(tmpdir(), 'wuu-incomplete-checkpoint-'));
  try {
    writeFileSync(join(root, 'unrelated.zip'), 'unrelated');
    const identity = { commit: 'a'.repeat(40), version: '2026.10.16', stage: 'signed-app' };
    saveCheckpoint(root, identity, ['unrelated.zip']);
    assert.throws(() => verifyCheckpoint(root, identity), /missing|incomplete/i);
    for (const name of distributionFiles) writeFileSync(join(root, name), 'signed bytes');
    saveCheckpoint(root, { ...identity, stage: 'distribution' }, distributionFiles);
    writeFileSync(join(root, 'stale-mac.yml'), 'unbound metadata');
    assert.throws(() => verifyCheckpoint(root, { ...identity, stage: 'distribution' }), /unbound/i);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('receipt recovery prefers the newest attempt and retains a submission saved before polling', () => {
  const { restoreNotarization } = require('./restore-notarization.cjs');
  const root = mkdtempSync(join(tmpdir(), 'wuu-receipt-restore-'));
  const artifacts = [
    { name: 'app-receipts-1', expired: false },
    { name: 'app-submission-2', expired: false },
    { name: 'app-receipts-3', expired: false },
  ];
  const calls = [];
  try {
    mkdirSync(join(root, 'notarization'));
    writeFileSync(join(root, 'notarization/app.json'), JSON.stringify({ id: 'imported' }));
    restoreNotarization('app', { repository: 'owner/repo', runId: '42', beforeAttempt: 3, root,
      run: (_command, args) => {
        calls.push(args);
        if (args[0] === 'api') return JSON.stringify([{ artifacts }]);
        assert.equal(args[args.indexOf('--name') + 1], 'app-submission-2');
        writeFileSync(join(args[args.indexOf('--dir') + 1], 'app.json'), JSON.stringify({ id: 'newer' }));
      },
    });
    assert.equal(JSON.parse(readFileSync(join(root, 'notarization/app.json'))).id, 'newer');
    assert.equal(calls.length, 2);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

for (const stage of ['app', 'dmg']) {
  test(`missing ${stage} receipt fails closed after a previous Apple step started`, () => {
    const { restoreNotarization } = require('./restore-notarization.cjs');
    const root = mkdtempSync(join(tmpdir(), 'wuu-missing-receipt-'));
    const step = stage === 'app' ? 'Notarize the saved app and build distribution archives' : 'Notarize and verify the saved distribution';
    try {
      const run = (_command, args) => JSON.stringify(args.at(-1).includes('/artifacts')
        ? [{ artifacts: [] }]
        : [{ jobs: [{ run_attempt: 1, steps: [{ name: step, status: 'completed', conclusion: 'cancelled' }] }] }]);
      assert.throws(() => restoreNotarization(stage, { repository: 'owner/repo', runId: '42', beforeAttempt: 2, root, run }), /missing.*receipt|receipt.*missing/i);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
}

test('new Apple work is allowed only when earlier matching steps never started', () => {
  const { restoreNotarization } = require('./restore-notarization.cjs');
  const root = mkdtempSync(join(tmpdir(), 'wuu-not-started-'));
  try {
    assert.doesNotThrow(() => restoreNotarization('app', { repository: 'owner/repo', runId: '42', beforeAttempt: 2, root,
      run: (_command, args) => JSON.stringify(args.at(-1).includes('/artifacts') ? [{ artifacts: [] }] : [{ jobs: [
        { run_attempt: 1, steps: [{ name: 'Notarize the saved app and build distribution archives', status: 'completed', conclusion: 'skipped' }] },
        { run_attempt: 2, steps: [{ name: 'Notarize the saved app and build distribution archives', status: 'in_progress', conclusion: null }] },
      ] }]),
    }));
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('recovery imports the furthest complete checkpoint and retains its receipt across runs', () => {
  const { restoreReleaseCheckpoint } = require('./restore-release-checkpoint.cjs');
  const source = mkdtempSync(join(tmpdir(), 'wuu-source-checkpoint-'));
  const root = mkdtempSync(join(tmpdir(), 'wuu-import-checkpoint-'));
  const identity = { commit: 'a'.repeat(40), version: '2026.10.16' };
  const metadata = { repository: { full_name: 'owner/repo' }, head_repository: { full_name: 'owner/repo' }, path: '.github/workflows/release.yml', event: 'workflow_dispatch', status: 'completed', head_sha: 'b'.repeat(40) };
  const downloaded = [];
  try {
    for (const name of distributionFiles) writeFileSync(join(source, name), 'signed bytes');
    saveCheckpoint(source, { ...identity, stage: 'distribution' }, distributionFiles);
    const run = (command, args) => {
      if (command === 'git') return '';
      if (args[0] === 'api') {
        if (args.at(-1).includes('/artifacts')) return JSON.stringify([{ artifacts: [{ name: 'signed-app' }, { name: 'signed-distribution' }, { name: 'dmg-submission-1' }] }]);
        return JSON.stringify(metadata);
      }
      const name = args[args.indexOf('--name') + 1];
      downloaded.push(name);
      if (name === 'signed-distribution') for (const file of [...distributionFiles, 'checkpoint.json']) copyFileSync(join(source, file), join(root, file));
      else writeFileSync(join(args[args.indexOf('--dir') + 1], 'dmg.json'), JSON.stringify({ status: 'UploadUnknown' }));
    };
    assert.equal(restoreReleaseCheckpoint('42', identity, { repository: 'owner/repo', currentRunId: '43', root, run }), 'distribution');
    assert.deepEqual(downloaded, ['signed-distribution', 'dmg-submission-1']);
    assert.equal(JSON.parse(readFileSync(join(root, 'notarization/dmg.json'))).status, 'UploadUnknown');
    for (const mismatch of [{ status: 'in_progress' }, { event: 'pull_request' }, { path: '.github/workflows/ci.yml' }, { repository: { full_name: 'other/repo' } }, { head_repository: { full_name: 'fork/repo' } }]) {
      assert.throws(() => restoreReleaseCheckpoint('42', identity, { repository: 'owner/repo', currentRunId: '43', root,
        run: () => JSON.stringify({ ...metadata, ...mismatch }),
      }), /source|release|completed|repository/i);
    }
    assert.throws(() => restoreReleaseCheckpoint('42', { ...identity, commit: 'c'.repeat(40) }, { repository: 'owner/repo', currentRunId: '43', root, run }), /identity/);
  } finally { rmSync(source, { recursive: true, force: true }); rmSync(root, { recursive: true, force: true }); }
});

test('a newer receipt is never replaced by an imported seed when recovery fails', () => {
  const { restoreNotarization } = require('./restore-notarization.cjs');
  const root = mkdtempSync(join(tmpdir(), 'wuu-receipt-fail-closed-'));
  try {
    mkdirSync(join(root, 'notarization'));
    writeFileSync(join(root, 'notarization/app.json'), JSON.stringify({ status: 'Accepted', id: 'seed' }));
    for (const expired of [true, false]) {
      assert.throws(() => restoreNotarization('app', { repository: 'owner/repo', runId: '42', beforeAttempt: 3, root,
        run: (_command, args) => args[0] === 'api'
          ? JSON.stringify([{ artifacts: [{ name: 'app-receipts-2', expired }] }]) : '',
      }), /expired|missing/i);
    }
    restoreNotarization('app', { repository: 'owner/repo', runId: '42', beforeAttempt: 3, root,
      run: (_command, args) => {
        if (args[0] === 'api') return JSON.stringify([{ artifacts: [{ name: 'app-submission-2' }, { name: 'app-receipts-2' }] }]);
        assert.equal(args[args.indexOf('--name') + 1], 'app-receipts-2');
        writeFileSync(join(args[args.indexOf('--dir') + 1], 'app.json'), JSON.stringify({ status: 'UploadUnknown' }));
      },
    });
    assert.equal(JSON.parse(readFileSync(join(root, 'notarization/app.json'))).status, 'UploadUnknown');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('imported checkpoints can be recovered again without rebuilding or losing receipts', () => {
  const { restoreReleaseCheckpoint } = require('./restore-release-checkpoint.cjs');
  const source = mkdtempSync(join(tmpdir(), 'wuu-chain-source-'));
  const root = mkdtempSync(join(tmpdir(), 'wuu-chain-recovery-'));
  const identity = { commit: 'a'.repeat(40), version: '2026.10.16' };
  const metadata = { repository: { full_name: 'owner/repo' }, head_repository: { full_name: 'owner/repo' }, path: '.github/workflows/release.yml', event: 'push', status: 'completed', head_sha: 'b'.repeat(40) };
  try {
    writeFileSync(join(source, 'signed-app.zip'), 'signed bytes');
    saveCheckpoint(source, { ...identity, stage: 'signed-app' }, ['signed-app.zip']);
    const run = (command, args) => {
      if (command === 'git') return '';
      if (args[0] === 'api') {
        if (args.at(-1).includes('/artifacts')) return JSON.stringify([{ artifacts: [{ name: 'resume-1-signed-app' }, { name: 'resume-2-signed-app' }] }]);
        assert.ok(!args.at(-1).includes('/jobs'), 'a retained receipt needs no missing-submission inference');
        return JSON.stringify(metadata);
      }
      assert.equal(args[args.indexOf('--name') + 1], 'resume-2-signed-app');
      for (const file of ['signed-app.zip', 'checkpoint.json']) copyFileSync(join(source, file), join(root, file));
      mkdirSync(join(root, 'notarization'), { recursive: true });
      writeFileSync(join(root, 'notarization/app.json'), JSON.stringify({ status: 'Accepted', id: 'retained' }));
    };
    assert.equal(restoreReleaseCheckpoint('43', identity, { repository: 'owner/repo', currentRunId: '44', root, run }), 'signed-app');
    assert.equal(JSON.parse(readFileSync(join(root, 'notarization/app.json'))).id, 'retained');
    for (const mismatch of [{ version: '2026.10.17' }, { commit: 'c'.repeat(40) }]) {
      assert.throws(() => restoreReleaseCheckpoint('43', { ...identity, ...mismatch }, { repository: 'owner/repo', currentRunId: '44', root, run }), /identity/);
    }
    const untrusted = (command, args, options) => {
      if (command === 'git') throw new Error('tooling is not an ancestor of main');
      return run(command, args, options);
    };
    assert.throws(() => restoreReleaseCheckpoint('43', identity, { repository: 'owner/repo', currentRunId: '44', root, run: untrusted }), /ancestor/);
    const corrupted = (command, args, options) => {
      const result = run(command, args, options);
      if (args[0] === 'run') writeFileSync(join(root, 'signed-app.zip'), 'changed bytes');
      return result;
    };
    assert.throws(() => restoreReleaseCheckpoint('43', identity, { repository: 'owner/repo', currentRunId: '44', root, run: corrupted }), /hash/);
  } finally { rmSync(source, { recursive: true, force: true }); rmSync(root, { recursive: true, force: true }); }
});
