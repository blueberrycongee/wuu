const assert = require('node:assert/strict');
const { test } = require('node:test');
const { runLinuxBuild, runPhase } = require('./build-linux-ci.cjs');

test('only exact PR events bound xz threads without changing level, targets or parent environment', () => {
  for (const inherited of [undefined, '-T1 --memlimit=1GiB']) {
    const env = { PATH: '/bin', XZ_OPT: '-7', KEEP: 'yes', ...(inherited === undefined ? {} : { XZ_DEFAULTS: inherited }) };
    const before = { ...env };
    for (const event of ['pull_request', 'push', 'workflow_dispatch', 'pull_request_target', 'PULL_REQUEST', '', undefined]) {
      let call;
      runLinuxBuild(event, { env, log: () => {}, exec: (command, args, options) => { call = { command, args, options }; } });
      assert.equal(call.command, 'node_modules/.bin/electron-builder');
      assert.deepEqual(call.args, ['--linux', 'AppImage', 'deb', '--x64', '--publish=never', '--config.electronDist=node_modules/electron/dist']);
      assert.deepEqual(call.options.env, event === 'pull_request' ? { ...before, XZ_DEFAULTS: '-T4' } : before);
      assert.equal(call.options.env.XZ_OPT, '-7');
      assert.equal(call.options.stdio, 'inherit');
      assert.deepEqual(env, before);
      if (event === 'pull_request') assert.notEqual(call.options.env, env);
    }
  }
});

test('builder launch and nonzero failures propagate unchanged in both modes', () => {
  for (const event of ['pull_request', 'push']) {
    for (const failure of [Object.assign(new Error('exit'), { status: 7 }), Object.assign(new Error('signal'), { signal: 'SIGTERM' }), new Error('ENOENT')]) {
      assert.throws(() => runLinuxBuild(event, { log: () => {}, exec: () => { throw failure; } }), error => error === failure);
    }
  }
});

test('phase timestamps distinguish completion from failure and do not swallow errors', () => {
  const lines = [];
  const options = { log: line => lines.push(line), now: () => new Date('2026-11-01T00:00:00Z') };
  assert.equal(runPhase('verification', () => 42, options), 42);
  assert.deepEqual(lines, ['2026-11-01T00:00:00.000Z Linux packaging phase verification: started', '2026-11-01T00:00:00.000Z Linux packaging phase verification: completed']);
  lines.length = 0;
  const failure = new Error('failed');
  assert.throws(() => runPhase('builder', () => { throw failure; }, options), error => error === failure);
  assert.deepEqual(lines, ['2026-11-01T00:00:00.000Z Linux packaging phase builder: started', '2026-11-01T00:00:00.000Z Linux packaging phase builder: failed']);
});

test('release entry point keeps prerequisites, builder and verification ordered and stops on any failure', () => {
  const vm = require('node:vm');
  const fs = require('node:fs');
  const path = require('node:path');
  const source = fs.readFileSync(path.join(__dirname, 'release-linux.cjs'), 'utf8');
  for (const failed of ['', 'prerequisites', 'builder', 'verification']) {
    const calls = [], logs = [];
    const failure = new Error(failed);
    const env = { GITHUB_EVENT_NAME: 'pull_request', XZ_DEFAULTS: '-T1' };
    const exec = (command, args, options) => {
      if (command === 'git') return 'commit\n';
      const name = command.includes('electron-builder') ? 'builder' : args[0].includes('build-package-prerequisites') ? 'prerequisites' : args[0].includes('verify-linux-release') ? 'verification' : 'version';
      calls.push(name);
      assert.equal(options.env.XZ_DEFAULTS, name === 'builder' ? '-T4' : '-T1');
      if (name === failed) throw failure;
    };
    const requireFixture = id => id === 'node:child_process' ? { execFileSync: exec }
      : id === 'node:fs' ? { rmSync: () => {} }
      : id === './build-linux-ci.cjs' ? {
        runLinuxBuild: (event, options) => runLinuxBuild(event, { ...options, exec, log: line => logs.push(line) }),
        runPhase: (name, action) => runPhase(name, action, { log: line => logs.push(line) }),
      } : require(id);
    const action = () => vm.runInNewContext(source, { require: requireFixture, __dirname, process: { platform: 'linux', arch: 'x64', execPath: process.execPath, env } });
    if (failed) assert.throws(action, error => error === failure); else action();
    const order = ['version', 'prerequisites', 'builder', 'verification'];
    assert.deepEqual(calls, failed ? order.slice(0, order.indexOf(failed) + 1) : order);
    assert.equal(env.XZ_DEFAULTS, '-T1');
    assert.ok(logs.at(-1).endsWith(failed ? ': failed' : ': completed'));
  }
});
