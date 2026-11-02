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

const { runInNewContext } = require('node:vm');
const { waitForSessionContentFrame } = require('./session-switch-readiness.cjs');

function contentProbe() {
  const state = {
    started: 100, now: 100, pane: true, inert: false, last: true,
    text: 'Session 1 question 159', viewport: true,
    paneRect: { top: 0, bottom: 100 }, lastRect: { top: 20, bottom: 80, height: 60 },
    reads: [], marks: [], frames: [], settled: false,
  };
  const last = {
    get textContent() { return state.text; },
    getBoundingClientRect() { state.reads.push('last'); return state.lastRect; },
  };
  const pane = {
    closest() { return state.inert ? {} : null; },
    getElementsByClassName() { return state.last ? [last] : []; },
  };
  const viewport = { getBoundingClientRect() { state.reads.push('viewport'); return state.paneRect; } };
  const context = {
    document: { querySelector(selector) {
      return selector.startsWith('.cached-conversation-pane') ? (state.pane ? pane : null) : (state.viewport ? viewport : null);
    } },
    window: { __switchProbe: { get start() { return state.started; } }, __switchTrace: true },
    performance: { now: () => state.now, mark: name => state.marks.push(name) },
    requestAnimationFrame: callback => state.frames.push(callback),
  };
  // Execute the same serialized renderer function as Electron evaluate(), with
  // controllable frame opportunities rather than wall-clock sleeps.
  const ready = runInNewContext(`(${waitForSessionContentFrame.toString()})(1)`, context);
  ready.then(() => { state.settled = true; }, () => {});
  const frame = async (elapsed = 16) => {
    state.now += elapsed;
    assert.equal(state.frames.length, 1);
    state.frames.shift()();
    await Promise.resolve();
  };
  return { state, ready, frame };
}

test('content readiness does not measure geometry while navigation cannot be ready', async () => {
  const { state, ready, frame } = contentProbe();
  for (const blocked of [
    { started: undefined }, { pane: false }, { inert: true },
    { last: false }, { text: 'A different conversation' },
  ]) {
    const previous = Object.fromEntries(Object.keys(blocked).map(key => [key, state[key]]));
    Object.assign(state, blocked);
    await frame();
    assert.deepEqual(state.reads, [], 'Pending navigation must not flush loading animation styles');
    assert.equal(state.settled, false);
    Object.assign(state, previous);
  }
  await frame();
  assert.deepEqual(state.reads, ['viewport', 'last']);
  assert.equal(state.settled, false, 'One visible frame is not the content endpoint');
  await frame();
  assert.equal(await ready, state.now - state.started);
  assert.deepEqual(state.reads, ['viewport', 'last', 'viewport', 'last']);
  assert.deepEqual(state.marks, ['switch-1-content']);
});

test('content readiness still needs intersecting nonzero geometry on two consecutive frames', async () => {
  const { state, ready, frame } = contentProbe();
  await frame();
  assert.equal(state.settled, false);
  state.lastRect = { top: 100, bottom: 160, height: 60 };
  await frame();
  state.lastRect = { top: -60, bottom: 0, height: 60 };
  await frame();
  state.lastRect = { top: 20, bottom: 20, height: 0 };
  await frame();
  state.lastRect = { top: 20, bottom: 80, height: 60 };
  await frame();
  assert.equal(state.settled, false, 'Offscreen and zero-height frames must reset the endpoint');
  await frame();
  await ready;
  assert.equal(state.reads.length, 12, 'Re-read real geometry on every eligible frame');
});

test('a renewed pending state resets consecutive content frames without measuring geometry', async () => {
  const { state, ready, frame } = contentProbe();
  await frame();
  state.inert = true;
  await frame();
  assert.equal(state.reads.length, 2);
  state.inert = false;
  await frame();
  assert.equal(state.settled, false);
  await frame();
  await ready;
  assert.equal(state.reads.length, 6);
});

test('missing viewport never satisfies readiness and the original deadline still rejects', async () => {
  const { state, ready, frame } = contentProbe();
  const rejected = assert.rejects(ready, /Target conversation never became visible/);
  state.viewport = false;
  await frame();
  await frame();
  assert.equal(state.settled, false);
  await frame(30000);
  await rejected;
  assert.deepEqual(state.marks, []);
});
