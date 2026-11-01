const assert = require('node:assert/strict');
const { test } = require('node:test');
const { runWindowsBuild } = require('./build-windows-ci.cjs');

test('only pull-request validation overrides archive compression without leaking its environment', () => {
  const key = 'ELECTRON_BUILDER_COMPRESSION_LEVEL';
  const original = process.env[key];
  try {
    for (const inherited of [undefined, '9']) {
      if (inherited === undefined) delete process.env[key];
      else process.env[key] = inherited;
      const before = { ...process.env };
      for (const event of ['pull_request', 'push', 'workflow_dispatch', 'pull_request_target', '', 'PULL_REQUEST']) {
        let called;
        runWindowsBuild(event, { spawn: (command, args, options) => { called = { command, args, options }; return { status: 0 }; } });
        const validation = event === 'pull_request';
        assert.equal(called.args.includes('--config.compression=store'), validation, event);
        assert.deepEqual(called.args.filter(arg => !arg.startsWith('--config.compression=')).slice(1), ['--win', 'nsis', 'zip', '--publish=never']);
        assert.equal(called.options.stdio, 'inherit');
        assert.equal(called.args.some(arg => /npmRebuild|sign|differentialPackage/.test(arg)), false);
        assert.deepEqual({ ...called.options.env }, validation ? { ...before, [key]: '1' } : before);
        if (validation) assert.notEqual(called.options.env, process.env);
        assert.deepEqual({ ...process.env }, before, 'the parent environment is unchanged');
      }
    }
  } finally {
    if (original === undefined) delete process.env[key];
    else process.env[key] = original;
  }
});

test('packaging failures and process launch failures remain failures in both modes', () => {
  for (const event of ['pull_request', 'push', 'workflow_dispatch']) {
    assert.equal(runWindowsBuild(event, { spawn: () => ({ status: 7 }) }), 7);
    assert.equal(runWindowsBuild(event, { spawn: () => ({ status: null, signal: 'SIGTERM' }) }), 1);
    assert.throws(() => runWindowsBuild(event, { spawn: () => ({ error: new Error('cannot launch') }) }), /cannot launch/);
  }
});
