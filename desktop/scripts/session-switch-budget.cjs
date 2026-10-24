const assert = require('node:assert/strict');
const { execFileSync, spawnSync } = require('node:child_process');
const path = require('node:path');
const budget = require('./session-switch-budget.json');

function assertSample(sample) {
  for (const [name, ceiling] of Object.entries(budget.ceilings)) {
    assert.ok(Number.isInteger(ceiling) && ceiling >= 0, `Invalid ${name} ceiling`);
    assert.ok(Number.isFinite(sample[name]) && sample[name] >= 0 && sample[name] <= ceiling,
      `${name}: observed ${sample[name]}, ceiling ${ceiling}`);
  }
}

function assertRatchet(previous, next) {
  if (previous.compositionMigration === undefined && previous.fixture.safeMode === '1' && next.fixture.safeMode === '0') {
    assert.deepEqual({ ...next.fixture, safeMode: '1' }, previous.fixture, 'The composition migration cannot change the fixture workload');
    assert.deepEqual(next.compositionMigration, { fromSafeMode: '1', toSafeMode: '0', comparable: false },
      'The one-time composition migration must record that prior samples are not comparable');
  } else {
    assert.deepEqual(next.fixture, previous.fixture, 'The ratchet fixture must remain comparable');
    assert.deepEqual(next.compositionMigration, previous.compositionMigration, 'Cannot remove or replace the composition migration');
  }
  assert.deepEqual(Object.keys(next.ceilings).sort(), Object.keys(previous.ceilings).sort(), 'Cannot remove or replace counters');
  for (const [name, ceiling] of Object.entries(next.ceilings)) {
    assert.ok(Number.isInteger(ceiling) && ceiling >= 0 && ceiling <= previous.ceilings[name],
      `${name}: ceilings can only decrease (${previous.ceilings[name]} -> ${ceiling})`);
  }
}

module.exports = { budget, assertSample, assertRatchet };

if (require.main === module) {
  const ref = process.argv[2];
  assert.ok(ref, 'Pass the PR base commit for the ratchet check');
  const cwd = path.resolve(__dirname, '../..');
  execFileSync('git', ['rev-parse', '--verify', `${ref}^{commit}`], { cwd });
  const file = 'desktop/scripts/session-switch-budget.json';
  const previous = spawnSync('git', ['show', `${ref}:${file}`], { cwd, encoding: 'utf8' });
  if (previous.status === 0) {
    const baseline = JSON.parse(previous.stdout);
    assertRatchet(baseline, budget);
    if (baseline.fixture.safeMode !== budget.fixture.safeMode) {
      console.log('Fixture composition migrated from safe mode to normal composition; prior samples are not comparable. Workload and counter ceilings were not relaxed.');
    }
    console.log('Session switch budget did not increase');
  } else {
    const entry = execFileSync('git', ['ls-tree', '--name-only', ref, '--', file], { cwd, encoding: 'utf8' }).trim();
    assert.equal(entry, '', previous.stderr);
    console.log('Installing the initial session switch budget');
  }
}
