import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';

const execute = promisify(execFile);
const binary = process.env.WUU_DEPLOY_TESTSERVER;
const database = process.env.WUU_TEST_DATABASE_URL;

test('deployment probes survive restart and reject revoked devices', {
  skip: !binary || !database ? 'Set WUU_DEPLOY_TESTSERVER and run under clients/native/with-postgres.sh' : false,
  timeout: 90000,
}, async t => {
  const directory = await mkdtemp(path.join(tmpdir(), 'wuu-deploy-probe-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const state = path.join(directory, 'probe.json');
  let process;
  let address;
  async function stop() {
    if (!process || process.exitCode !== null) return;
    const exited = once(process, 'exit');
    process.kill('SIGTERM');
    await exited;
  }
  t.after(stop);
  async function start() {
    const child = spawn(binary, ['--addr', address || '127.0.0.1:0', '--state', path.join(directory, 'relay.json'), '--registration'], {
      env: { ...globalThis.process.env, WUU_DATABASE_URL: database, WUU_GITHUB_CLIENT_ID: '', WUU_GITHUB_CLIENT_SECRET: '', WUU_ACCOUNT_PUBLIC_URL: '' },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    process = child;
    await new Promise((resolve, reject) => {
      let output = '';
      const timer = setTimeout(() => { child.kill('SIGKILL'); reject(new Error('Test server startup timed out')); }, 15000);
      child.once('error', error => { clearTimeout(timer); reject(error); });
      child.once('exit', () => { clearTimeout(timer); reject(new Error('Test server exited before startup')); });
      child.stdout.on('data', data => {
        output += data;
        const match = output.match(/wuu relay listening on (127\.0\.0\.1:\d+)/);
        if (match) { address = match[1]; clearTimeout(timer); resolve(); }
      });
      // Consume errors without printing database or identity diagnostics into CI.
      child.stderr.resume();
    });
  }
  const run = phase => execute(globalThis.process.execPath, ['deploy/remote/verify.mjs', phase, 'http://' + address, state], { timeout: 30000 });
  await start();
  await run('create');
  await run('verify');
  await run('relay');
  await stop();
  await start();
  await run('verify');
  await run('relay');
  await run('revoke');
  await assert.rejects(run('verify'), 'Revoked credentials must fail the deployment probe');
});
