const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { test } = require('node:test');
const { runBuilds } = require('./build-package-prerequisites.cjs');

// Packaging must wait for every successful prerequisite, and must never continue
// after an asynchronous child failure (including a failed process launch).
test('bounded build scheduling joins all prerequisites', async () => {
  const children = [];
  const spawn = (command, args) => { const child = new EventEmitter(); children.push({ command, args, child }); return child; };
  const done = runBuilds(['core', 'web', 'native'], { spawn, cwd: '/fixture' });
  assert.equal(children.length, 2);
  let settled = false; done.then(() => { settled = true; });
  children[0].child.emit('close', 0);
  await Promise.resolve(); await Promise.resolve();
  assert.equal(children.length, 3);
  children[2].child.emit('close', 0);
  await Promise.resolve();
  assert.equal(settled, false);
  children[1].child.emit('close', 0);
  await done;
});
test('failed prerequisite stops queued work but joins active builds', async () => {
  for (const launchFailure of [false, true]) {
    const children = [];
    const done = runBuilds(['core', 'web', 'native'], { spawn: () => { const c = new EventEmitter(); children.push(c); return c; } });
    const rejected = assert.rejects(done, /core/);
    if (launchFailure) children[0].emit('error', new Error('spawn failure'));
    else children[0].emit('close', 7);
    await Promise.resolve(); await Promise.resolve();
    assert.equal(children.length, 2);
    children[1].emit('close', 0);
    await rejected;
  }
});
