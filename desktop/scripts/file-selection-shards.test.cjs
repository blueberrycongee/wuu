const assert = require('node:assert/strict');
const { test } = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { variants, caseKinds, selectVariants } = require('./file-selection-variants.cjs');
const { runShards } = require('./file-selection-shards.cjs');

function fixture(t) {
  const output = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-selection-join-'));
  t.after(() => fs.rmSync(output, { recursive: true, force: true }));
  return output;
}
function report(child) {
  const selected = selectVariants(child.shard);
  const screenshots = selected.map(variant => {
    const target = path.join(child.output, `${variant.name}-fixture.png`);
    fs.writeFileSync(target, 'synthetic test image');
    return target;
  });
  fs.mkdirSync(path.join(child.output, 'profile'));
  fs.writeFileSync(path.join(child.output, 'profile', 'secret.png'), 'not an artifact');
  return { boundary: 'synthetic child fixture', renderer: 'fixture.html', output: child.output,
    completed: true, errors: [], screenshots, layoutObservations: selected.flatMap(variant => [
      { label: `${variant.name}-md`, sideLayout: {} }, { name: `${variant.name}-wrapped`, placement: {} }]),
    cases: selected.flatMap(variant => caseKinds.map(kind => ({ variant,
      ...(kind.startsWith('selection-') ? { file: kind } : { kind }), checks: ['fixture'] }))) };
}
function save(child, value) { fs.writeFileSync(path.join(child.output, 'report.json'), JSON.stringify(value)); }

test('default retains four variants; two strict shards partition them without unknown-input fallback', () => {
  assert.deepEqual(selectVariants(), variants);
  assert.deepEqual(selectVariants('1/2'), [variants[0], variants[2]]);
  assert.deepEqual(selectVariants('2/2'), [variants[1], variants[3]]);
  for (const value of ['', '0/2', '3/2', '1/3', 'all', '1/2 ']) assert.throws(() => selectVariants(value), /shard/);
});

test('successful join preserves all 24 identities and root report/screenshots, not profiles', async t => {
  const output = fixture(t);
  const launched = [];
  const result = await runShards({ output, launch: async child => {
    launched.push(child); save(child, report(child)); return { code: 0 };
  } });
  assert.equal(result.completed, true);
  assert.equal(result.cases.length, 24);
  assert.equal(new Set(result.cases.map(item => `${item.variant.name}:${item.file || item.kind}`)).size, 24);
  assert.deepEqual(result.cases.filter((_, index) => index % 6 === 0).map(item => item.variant.name), variants.map(v => v.name));
  assert.equal(new Set(launched.map(child => child.output)).size, 2);
  assert.equal(result.screenshots.length, 4);
  for (const screenshot of result.screenshots) assert.equal(path.dirname(screenshot), output);
  assert.equal(fs.existsSync(path.join(output, 'profile')), false);
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(output, 'report.json'))), result);
});

for (const [name, mutate] of [
  ['missing report', () => null],
  ['incomplete report', value => ({ ...value, completed: false })],
  ['reported error', value => ({ ...value, errors: [{ message: 'renderer crashed' }] })],
  ['missing case', value => ({ ...value, cases: value.cases.slice(1) })],
  ['duplicate case', value => ({ ...value, cases: [...value.cases.slice(1), value.cases[1]] })],
  ['foreign variant', value => ({ ...value, cases: value.cases.map(item => ({ ...item, variant: variants[1] })) })],
  ['profile screenshot', (value, child) => ({ ...value, screenshots: [path.join(child.output, 'profile', 'secret.png')] })],
]) test(`join fails closed for ${name}`, async t => {
  const output = fixture(t);
  await assert.rejects(runShards({ output, launch: async child => {
    let value = report(child);
    if (child.shard === '1/2') value = mutate(value, child);
    if (value) save(child, value);
    return { code: 0 };
  } }));
  const result = JSON.parse(fs.readFileSync(path.join(output, 'report.json')));
  assert.equal(result.completed, false);
  assert.ok(result.errors.length > 0);
  if (name === 'missing report') {
    assert.equal(result.cases.length, 12, 'keep the successful sibling evidence');
    assert.equal(result.renderer, 'fixture.html');
  }
});

test('failed child still joins its sibling before reporting failure', async t => {
  const output = fixture(t);
  const completions = [];
  const result = runShards({ output, launch: child => {
    save(child, report(child));
    return new Promise(resolve => completions.push(resolve));
  } });
  let settled = false;
  result.then(() => { settled = true; }, () => { settled = true; });
  assert.equal(completions.length, 2);
  completions[0]({ code: 1 });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(settled, false, 'first failure must not leave the sibling running');
  completions[1]({ code: 0 });
  await assert.rejects(result, /failed/);
  assert.equal(JSON.parse(fs.readFileSync(path.join(output, 'report.json'))).completed, false);
});

test('failed children retain their failure screenshots in the flat artifact root', async t => {
  const output = fixture(t);
  await assert.rejects(runShards({ output, launch: async child => {
    const value = report(child);
    const failure = path.join(child.output, 'failure.png');
    fs.writeFileSync(failure, 'synthetic failure image');
    value.screenshots.push(failure);
    value.completed = false;
    value.errors.push({ message: 'assertion failed' });
    save(child, value);
    return { code: 1 };
  } }));
  for (const index of [1, 2]) assert.equal(fs.readFileSync(path.join(output, `shard-${index}-failure.png`), 'utf8'), 'synthetic failure image');
});
