import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { runTimedCommand } from './go-test-timing.mjs';

async function fixture(t, script) {
  const directory = mkdtempSync(join(tmpdir(), 'go-timing-test-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  let stdout = '', stderr = '';
  const result = await runTimedCommand({ command: process.execPath, args: ['-e', script], directory,
    stdout: { write: chunk => { stdout += chunk; } }, stderr: { write: chunk => { stderr += chunk; } } });
  return { result, stdout, stderr, directory, summary: JSON.parse(readFileSync(join(directory, 'summary.json'), 'utf8')) };
}

test('streams split JSON Output verbatim and retains per-test/package timings and raw bytes', async t => {
  const events = [
    { Action: 'output', Package: 'example/a', Test: 'TestOne', Output: '=== RUN   TestOne\n中文\n' },
    { Action: 'pass', Package: 'example/a', Test: 'TestOne', Elapsed: 1.25 },
    { Action: 'output', Package: 'example/a', Output: 'ok  example/a 1.5s\n' },
    { Action: 'pass', Package: 'example/a', Elapsed: 1.5 },
  ];
  const raw = events.map(e => JSON.stringify(e) + '\n').join('');
  const f = await fixture(t, `const b=Buffer.from(${JSON.stringify(raw)});for(let i=0;i<b.length;i++)process.stdout.write(b.subarray(i,i+1));process.stderr.write('compiler warning\\n')`);
  assert.equal(f.result.code, 0);
  assert.equal(f.stdout, '=== RUN   TestOne\n中文\nok  example/a 1.5s\n');
  assert.equal(f.stderr, 'compiler warning\n');
  assert.equal(readFileSync(join(f.directory, 'stdout.log'), 'utf8'), raw);
  assert.equal(readFileSync(join(f.directory, 'events.jsonl'), 'utf8'), raw);
  assert.equal(f.summary.tests[0].elapsedSeconds, 1.25);
  assert.equal(f.summary.packages[0].elapsedSeconds, 1.5);
});

test('failed output and stderr survive, failure status is not converted to success', async t => {
  const f = await fixture(t, `console.log('make prelude');console.log(JSON.stringify({Action:'output',Package:'p',Test:'TestBad',Output:'assertion failed\\n'}));console.log(JSON.stringify({Action:'fail',Package:'p',Test:'TestBad',Elapsed:2}));process.stderr.write('build error\\n');process.exitCode=7`);
  assert.equal(f.result.code, 7);
  assert.equal(f.stdout, 'make prelude\nassertion failed\n');
  assert.equal(f.stderr, 'build error\n');
  assert.equal(f.summary.tests[0].action, 'fail');
  assert.equal(readFileSync(join(f.directory, 'stderr.log'), 'utf8'), 'build error\n');
});

test('cached packages are explicit and do not pretend zero tests ran successfully', async t => {
  const f = await fixture(t, `console.log(JSON.stringify({Action:'output',Package:'p',Output:'ok  p (cached)\\n'}));console.log(JSON.stringify({Action:'pass',Package:'p',Elapsed:0}));console.log(JSON.stringify({Action:'skip',Package:'q',Elapsed:0}))`);
  assert.equal(f.summary.packages[0].cached, true);
  assert.equal(f.summary.packages[0].elapsedSeconds, null);
  assert.deepEqual(f.summary.tests, []);
  assert.equal(f.summary.packages[1].action, 'skip');
});

test('unterminated non-JSON output is retained', async t => {
  const f = await fixture(t, `process.stdout.write('last diagnostic without newline')`);
  assert.equal(f.stdout, 'last diagnostic without newline');
});

test('child signal cannot produce success', async t => {
  const f = await fixture(t, `process.kill(process.pid,'SIGTERM')`);
  assert.equal(f.result.signal, 'SIGTERM');
  assert.notEqual(f.result.code, 0);
});

test('Go build-output diagnostics remain readable even without Package', async t => {
  const f = await fixture(t, `console.log(JSON.stringify({Action:'build-output',ImportPath:'p',Output:'syntax error\\n'}));console.log(JSON.stringify({Action:'build-fail',ImportPath:'p'}));process.exitCode=1`);
  assert.equal(f.stdout, 'syntax error\n');
  assert.equal(f.result.code, 1);
  assert.equal(f.summary.eventCount, 2);
});

test('cached replay timings are marked non-current on individual tests too', async t => {
  const f = await fixture(t, `console.log(JSON.stringify({Action:'pass',Package:'p',Test:'TestReplayed',Elapsed:8}));console.log(JSON.stringify({Action:'output',Package:'p',Output:'ok  p (cached)\\n'}));console.log(JSON.stringify({Action:'pass',Package:'p',Elapsed:0}))`);
  assert.equal(f.summary.tests[0].cached, true);
  assert.equal(f.summary.tests[0].elapsedSeconds, null);
  assert.equal(f.summary.tests[0].reportedElapsedSeconds, 8);
});

test('incoming cancellation is forwarded and cannot be swallowed by a zero-exit child', { timeout: 5000 }, async t => {
  const directory = mkdtempSync(join(tmpdir(), 'go-timing-signal-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const childScript = `process.on('SIGTERM',()=>{console.log('signal received');process.exit(0)});console.log('ready');setInterval(()=>{},1000)`;
  const script = `import {runTimedCommand} from ${JSON.stringify(new URL('./go-test-timing.mjs', import.meta.url).href)}; const r=await runTimedCommand({command:process.execPath,args:['-e',${JSON.stringify(childScript)}],directory:${JSON.stringify(directory)}});process.exitCode=r.code`;
  const child = spawn(process.execPath, ['--input-type=module', '-e', script], { stdio: ['ignore', 'pipe', 'pipe'] });
  t.after(() => child.kill('SIGKILL'));
  let output = '', sent = false;
  child.stdout.on('data', chunk => { output += chunk; if (!sent && output.includes('ready')) { sent = true; child.kill('SIGTERM'); } });
  const result = await new Promise((resolve, reject) => { child.on('error', reject); child.on('close', (code, signal) => resolve({ code, signal })); });
  assert.equal(sent, true);
  assert.match(output, /signal received/);
  assert.notEqual(result.code, 0);
  const summary = JSON.parse(readFileSync(join(directory, 'summary.json'), 'utf8'));
  assert.equal(summary.signal, 'SIGTERM');
});

test('real Makefile keeps identical discovery, exclusions and package order with optional reporting flag', t => {
  const directory = mkdtempSync(join(tmpdir(), 'go-timing-make-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const prefix = 'github.com/blueberrycongee/wuu/';
  const inventory = ['internal/access', 'cmd/wuu', 'internal/appserver', 'internal/codemode', 'internal/runtime', 'internal/tools', 'internal/worktree'].map(p => prefix + p);
  writeFileSync(join(directory, 'go'), `#!/usr/bin/env node\nconst fs=require('node:fs');const args=process.argv.slice(2);fs.appendFileSync(process.env.ARG_LOG,JSON.stringify(args)+'\\n');if(args[0]==='list')console.log(${JSON.stringify(inventory.join('\n'))});`, { mode: 0o755 });
  const invocations = [];
  for (const flag of ['', 'GO_TEST_OUTPUT_FLAGS=-json']) {
    const log = join(directory, `args-${invocations.length}.jsonl`);
    const result = spawnSync('make', ['-f', fileURLToPath(new URL('../../Makefile', import.meta.url)), 'test-go-packages', ...(flag ? [flag] : [])], {
      env: { ...process.env, PATH: directory + ':' + process.env.PATH, ARG_LOG: log }, encoding: 'utf8', timeout: 5000,
    });
    assert.equal(result.status, 0, result.stderr);
    invocations.push(readFileSync(log, 'utf8').trim().split('\n').map(JSON.parse));
  }
  assert.deepEqual(invocations[0][0], invocations[1][0]);
  assert.equal(invocations[0][0][0], 'list');
  assert.deepEqual(invocations[1][1], ['test', '-json', ...invocations[0][1].slice(1)]);
  assert.deepEqual(invocations[0][1], ['test', ...['cmd/wuu', 'internal/codemode', 'internal/runtime', 'internal/tools', 'internal/access', 'internal/worktree'].map(p => prefix + p)]);
});
