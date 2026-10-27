const assert = require('node:assert/strict');
const { test } = require('node:test');
const { ownsHeaderSample } = require('./header-hit-ownership.cjs');
const drawer = { left: 52, right: 240, top: 58, bottom: 675 };
const title = { contentHeader: true, history: false, drawerOwned: false };
const covered = { owned: false, drawer: true, x: 239.925, y: 75.3667 };

test('partially covered content keeps each uncovered sample strict', () => {
  const samples = [covered, { ...covered, y: 93.9667 },
    { owned: true, drawer: false, x: 429.28125, y: 84.6667 }];
  assert.ok(samples.every(sample => ownsHeaderSample(title, sample, drawer)));
});
test('a wrong hit outside the drawer fails even if its owner is a drawer descendant', () => {
  assert.equal(ownsHeaderSample(title, { ...covered, x: 241 }, drawer), false);
});
test('a point within drawer bounds requires actual drawer descendant ownership', () => {
  assert.equal(ownsHeaderSample(title, { ...covered, drawer: false }, drawer), false);
});
test('drawer controls cannot borrow another drawer control hit', () => {
  assert.equal(ownsHeaderSample({ ...title, drawerOwned: true }, covered, drawer), false);
});
test('app-history and other shell controls cannot use drawer occlusion', () => {
  assert.equal(ownsHeaderSample({ ...title, history: true }, covered, drawer), false);
  assert.equal(ownsHeaderSample({ ...title, contentHeader: false }, covered, drawer), false);
});
