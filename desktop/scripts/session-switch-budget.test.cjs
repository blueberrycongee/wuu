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
