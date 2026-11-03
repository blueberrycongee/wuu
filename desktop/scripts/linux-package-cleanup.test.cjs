const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { test } = require('node:test');
const { stopChild } = require('./linux-package-e2e.cjs');

function childFixture() {
  const child = new EventEmitter();
  Object.assign(child, { exitCode: null, signalCode: null, kills: [] });
  child.kill = signal => { child.kills.push(signal); return true; };
  child.finish = signal => { child.signalCode = signal; child.emit('exit', null, signal); };
  return child;
}

test('normal process exit clears its fallback timer, including synchronous SIGTERM exit', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const child = childFixture();
  child.kill = signal => { child.kills.push(signal); child.finish(signal); return true; };
  await stopChild(child);
  t.mock.timers.tick(5000);
  assert.deepEqual(child.kills, ['SIGTERM']);
  assert.equal(child.listenerCount('exit'), 0);
});

test('unresponsive packaged process receives SIGKILL and cleanup waits for its exit', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const child = childFixture();
  let settled = false;
  const done = stopChild(child).then(() => { settled = true; });
  t.mock.timers.tick(5000);
  await Promise.resolve();
  assert.deepEqual(child.kills, ['SIGTERM', 'SIGKILL']);
  assert.equal(settled, false);
  child.finish('SIGKILL');
  await done;
  t.mock.timers.tick(5000);
  assert.equal(child.kills.length, 2);
});

test('already signal-exited process is not killed again or awaited indefinitely', async () => {
  const child = childFixture();
  child.signalCode = 'SIGTERM';
  await stopChild(child);
  assert.deepEqual(child.kills, []);
  assert.equal(child.listenerCount('exit'), 0);
});
