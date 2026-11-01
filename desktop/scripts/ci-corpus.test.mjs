import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { corpusGroups } from './ci-corpus.mjs';

test('parallel corpus lanes replay every original sample exactly once without dropping steps', () => {
  const { samples } = JSON.parse(readFileSync(new URL('./fixtures/conversation-corpus/corpus.json', import.meta.url)));
  const groups = corpusGroups(samples);
  assert.equal(groups.length, 2);
  assert.deepEqual(groups.flat().sort(), samples.map(s => s.id).sort());
  const weights = groups.map(group => samples.filter(s => group.includes(s.id)).reduce((n, s) => n + s.steps.length, 0));
  assert(Math.abs(weights[0] - weights[1]) <= Math.max(...samples.map(s => s.steps.length)));
  assert.deepEqual(corpusGroups([...samples].reverse()), groups);
});
test('invalid corpus inventory cannot silently succeed', () => {
  assert.throws(() => corpusGroups([]));
  assert.throws(() => corpusGroups([{ id: 'same', steps: [] }, { id: 'same', steps: [] }]));
});
