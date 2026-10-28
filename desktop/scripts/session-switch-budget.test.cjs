const assert = require('node:assert/strict');
const { test } = require('node:test');
const { budget, assertSample, assertRatchet } = require('./session-switch-budget.cjs');

test('a measured regression in any work counter fails the guard', () => {
  const sample = { resumeCalls: 1, layoutCount: 20, recalcStyleCount: 40 };
  assert.doesNotThrow(() => assertSample(sample));
  for (const name of Object.keys(budget.ceilings)) {
    assert.throws(() => assertSample({ ...sample, [name]: budget.ceilings[name] + 1 }), new RegExp(name));
  }
  assert.throws(() => assertSample({ resumeCalls: 1, layoutCount: 20 }), /recalcStyleCount/);
});

test('a ratchet permits equal or tighter ceilings but rejects weakening or changing the workload', () => {
  assert.doesNotThrow(() => assertRatchet(budget, structuredClone(budget)));
  const lower = structuredClone(budget);
  lower.ceilings.layoutCount--;
  assert.doesNotThrow(() => assertRatchet(budget, lower));
  for (const name of Object.keys(budget.ceilings)) {
    const higher = structuredClone(budget);
    higher.ceilings[name]++;
    assert.throws(() => assertRatchet(budget, higher), new RegExp(name));
  }
  const missing = structuredClone(budget);
  delete missing.ceilings.recalcStyleCount;
  assert.throws(() => assertRatchet(budget, missing));
  const smaller = structuredClone(budget);
  smaller.fixture.turns--;
  assert.throws(() => assertRatchet(budget, smaller), /fixture/);
});

test('the legacy safe-mode fixture can migrate once without relaxing its workload or counters', () => {
  const previous = {
    fixture: { turns: 160, rounds: 3, variant: 'wide', safeMode: '1' },
    ceilings: { resumeCalls: 1, layoutCount: 64, recalcStyleCount: 128 },
  };
  const next = {
    ...structuredClone(previous),
    fixture: { ...previous.fixture, safeMode: '0' },
    compositionMigration: { fromSafeMode: '1', toSafeMode: '0', comparable: false },
  };
  assert.doesNotThrow(() => assertRatchet(previous, next));
  assert.doesNotThrow(() => assertRatchet(next, structuredClone(next)));
  for (const [field, value] of Object.entries({ turns: 159, rounds: 2, variant: 'narrow', safeMode: '2' })) {
    const changed = structuredClone(next);
    changed.fixture[field] = value;
    assert.throws(() => assertRatchet(previous, changed), /fixture/);
  }
  for (const name of Object.keys(previous.ceilings)) {
    const changed = structuredClone(next);
    changed.ceilings[name]++;
    assert.throws(() => assertRatchet(previous, changed), new RegExp(name));
  }
  const missing = structuredClone(next);
  delete missing.ceilings.recalcStyleCount;
  assert.throws(() => assertRatchet(previous, missing), /counters/);
  assert.throws(() => assertRatchet(previous, { ...next, compositionMigration: undefined }), /migration/i);
  assert.throws(() => assertRatchet(previous, { ...next, compositionMigration: { ...next.compositionMigration, comparable: true } }), /migration/i);
  assert.throws(() => assertRatchet(next, previous), /fixture/);
  assert.throws(() => assertRatchet(next, { ...next, compositionMigration: undefined }), /migration/i);
  assert.throws(() => assertRatchet({ ...previous, compositionMigration: next.compositionMigration }, next), /fixture/);
});
