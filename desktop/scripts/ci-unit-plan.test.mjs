import { test } from 'node:test';
import assert from 'node:assert/strict';
import { unitTestPlan } from './ci-unit-plan.mjs';

test('both unit shards cover the same Vitest inventory while Git and launcher run once', () => {
  for (const files of [[], ['src/renderer/Example.test.tsx', 'src/main/gitService.test.ts']]) {
    const first = unitTestPlan(files, '1/2'), second = unitTestPlan(files, '2/2');
    assert.equal(first.filter(c => c.tool === 'npm').length, 1);
    assert.equal(second.length, 1);
    const a = first.find(c => c.args.includes('--shard=1/2'));
    const b = second.find(c => c.args.includes('--shard=2/2'));
    assert.deepEqual(a.args.slice(0, -1), b.args.slice(0, -1));
    for (const name of ['gitService', 'remoteGit']) {
      assert(a.args.includes(`--exclude=src/main/${name}.test.ts`));
      assert(first.at(-1).args.includes(`src/main/${name}.test.ts`));
    }
    assert(first.at(-1).args.includes('--maxWorkers=1'));
    assert(first.at(-1).args.includes('--testTimeout=15000'));
  }
});
test('unsharded local execution preserves all three original commands', () => {
  const plan = unitTestPlan();
  assert.equal(plan.length, 3);
  assert(!plan.some(c => c.args.some(arg => arg.startsWith('--shard='))));
});
test('malformed or incomplete shard requests fail closed', () => {
  for (const shard of ['0/2', '3/2', '1/3', '2', 'bad']) assert.throws(() => unitTestPlan([], shard));
});
