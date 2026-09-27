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
  assert.deepEqual(next.fixture, previous.fixture, 'The ratchet fixture must remain comparable');
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
    assertRatchet(JSON.parse(previous.stdout), budget);
    console.log('Session switch budget did not increase');
  } else {
    const entry = execFileSync('git', ['ls-tree', '--name-only', ref, '--', file], { cwd, encoding: 'utf8' }).trim();
    assert.equal(entry, '', previous.stderr);
    console.log('Installing the initial session switch budget');
  }
}
