import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const script = resolve('.github/scripts/go-test-shard.mjs');
const names = ['TestAdmission', 'TestRecovery', 'TestOwnership', 'ExampleClient', 'FuzzProtocol'];
function fixture(run) {
  const dir = mkdtempSync(join(tmpdir(), 'wuu-go-shard-'));
  writeFileSync(join(dir, 'go'), `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.CALLS, JSON.stringify(args) + '\\n');
if (args.some(arg => arg.startsWith('-list='))) {
  console.log(process.env.NAMES);
  process.exit(Number(process.env.LIST_EXIT || 0));
}
process.exit(Number(process.env.TEST_EXIT || 0));
`, { mode: 0o755 });
  const execute = (shard, extra = {}) => spawnSync(process.execPath, [script, './internal/appserver', String(shard), '3', 'fixture_tag'], {
    encoding: 'utf8', env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, CALLS: join(dir, 'calls'),
      NAMES: [...names, 'BenchmarkIgnored', 'ok example/appserver 0.1s'].join('\n'),
      WUU_GO_SHARD_EVIDENCE: join(dir, 'manifest.json'), ...extra },
  });
  try { run(execute, dir); } finally { rmSync(dir, { recursive: true, force: true }); }
}

test('all top-level tests, examples and fuzz seeds run exactly once across shards', () => fixture((execute, dir) => {
  const selected = [];
  for (let shard = 0; shard < 3; shard++) {
    const result = execute(shard);
    assert.equal(result.status, 0, result.stderr);
    const manifest = JSON.parse(readFileSync(join(dir, 'manifest.json')));
    assert.equal(manifest.total, names.length);
    selected.push(...manifest.tests);
    const calls = readFileSync(join(dir, 'calls'), 'utf8').trim().split('\n').map(JSON.parse);
    const args = calls.at(-1);
    assert(args.includes('-count=1'));
    assert(args.includes('-tags=fixture_tag'));
    const pattern = new RegExp(args.find(arg => arg.startsWith('-run=')).slice(5));
    assert.deepEqual(names.filter(name => pattern.test(name)), manifest.tests);
    assert.equal(pattern.test(manifest.tests[0] + 'Extra'), false);
  }
  assert.deepEqual(selected.sort(), [...names].sort());
}));

test('invalid shards, failed enumeration and missing tests fail closed', () => fixture((execute) => {
  for (const shard of [-1, 3, 'oops']) assert.notEqual(execute(shard).status, 0);
  assert.notEqual(execute(0, { LIST_EXIT: '1' }).status, 0);
  assert.notEqual(execute(0, { NAMES: 'ok example/appserver (cached)' }).status, 0);
}));

test('a failing test fails its shard', () => fixture((execute) => {
  assert.equal(execute(0, { TEST_EXIT: '1' }).status, 1);
}));

 test('filtered race shards preserve instrumentation and the exact guard inventory', () => fixture((execute, dir) => {
  const result = execute(0, { WUU_GO_TEST_RACE: 'true', WUU_GO_TEST_PATTERN: '^TestReceipt' });
  assert.equal(result.status, 0, result.stderr);
  const calls = readFileSync(join(dir, 'calls'), 'utf8').trim().split('\n').map(JSON.parse);
  assert(calls[0].includes('-race'));
  assert(calls[0].includes('-list=^TestReceipt'));
  assert(calls[1].includes('-race'));
  const manifest = JSON.parse(readFileSync(join(dir, 'manifest.json')));
  assert.equal(manifest.race, true);
  assert.equal(manifest.pattern, '^TestReceipt');
}));

test('weighted shards separate expensive guards and retain every new test exactly once', () => fixture((execute, dir) => {
  const weights = JSON.stringify({ TestAdmission: 200, TestRecovery: 50 });
  const groups = [];
  for (let shard = 0; shard < 3; shard++) {
    const result = execute(shard, { WUU_GO_SHARD_WEIGHTS: weights });
    assert.equal(result.status, 0, result.stderr);
    groups.push(JSON.parse(readFileSync(join(dir, 'manifest.json'))).tests);
  }
  assert.deepEqual(groups.flat().sort(), [...names].sort());
  assert.notEqual(groups.findIndex(g => g.includes('TestAdmission')), groups.findIndex(g => g.includes('TestRecovery')));
  for (const value of ['[]', 'null', '{"TestAdmission":-1}', '{"TestAdmission":"200"}']) {
    assert.notEqual(execute(0, { WUU_GO_SHARD_WEIGHTS: value }).status, 0);
  }
}));
