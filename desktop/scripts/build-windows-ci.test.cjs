const assert = require('node:assert/strict');
const { test } = require('node:test');
const { runWindowsBuild } = require('./build-windows-ci.cjs');

test('only pull-request validation changes compression; release and manual routes retain defaults', () => {
  for (const event of ['pull_request', 'push', 'workflow_dispatch', 'pull_request_target', '', 'PULL_REQUEST']) {
    let called;
    runWindowsBuild(event, { spawn: (command, args, options) => { called = { command, args, options }; return { status: 0 }; } });
    assert.equal(called.args.includes('--config.compression=store'), event === 'pull_request', event);
    assert.deepEqual(called.args.filter(arg => !arg.startsWith('--config.compression=' )).slice(1), ['--win', 'nsis', 'zip', '--publish=never']);
    assert.equal(called.options.stdio, 'inherit');
    assert.equal(called.args.some(arg => /npmRebuild|sign|differentialPackage/.test(arg)), false);
  }
});

test('packaging failures and process launch failures remain failures in both modes', () => {
  for (const event of ['pull_request', 'push', 'workflow_dispatch']) {
    assert.equal(runWindowsBuild(event, { spawn: () => ({ status: 7 }) }), 7);
    assert.equal(runWindowsBuild(event, { spawn: () => ({ status: null, signal: 'SIGTERM' }) }), 1);
    assert.throws(() => runWindowsBuild(event, { spawn: () => ({ error: new Error('cannot launch') }) }), /cannot launch/);
  }
});
