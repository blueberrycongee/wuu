const { test } = require('node:test');
const assert = require('node:assert/strict');
const { validateCorpus, replayCalls } = require('./conversation-corpus.cjs');
const valid = () => ({ version: 1, timing: 'simulated', tools: 'synthetic-read-file', samples: [{
  id: 'sample-01', family: 'gpt', model: 'example-model', focus: 'short_process', target_step: 0,
  prompt: 'Check the example.', steps: [{ text: 'Checking.', tool_count: 1 }, { text: 'Done.', tool_count: 0 }],
}] });
// Failure cases for the sharing boundary: accidental raw metadata, credentials,
// private links, executable tool arguments, and an incomplete replay.
test('rejects raw metadata and executable tool payloads instead of silently passing them through', () => {
  for (const mutate of [c => c.source_path = '/private/source', c => c.samples[0].session_id = 'private',
    c => c.samples[0].steps[0].arguments = { command: 'touch /tmp/unwanted' }]) {
    const corpus = valid(); mutate(corpus); assert.throws(() => validateCorpus(corpus));
  }
});
test('rejects common credentials, private paths and non-example network destinations', () => {
  for (const text of ['sk-' + 'a'.repeat(40), 'ghp_' + 'b'.repeat(36), '/Users/person/private/file',
    'https://internal.company.invalid/project', '![image](https://internal.company.invalid/a.png)',
    'name@company.invalid', 'Bearer ' + 'x'.repeat(30)]) {
    const corpus = valid(); corpus.samples[0].steps[0].text = text;
    assert.throws(() => validateCorpus(corpus), text);
  }
});
test('requires a terminal answer and a valid target before starting a replay', () => {
  for (const mutate of [c => c.samples[0].steps.pop(), c => c.samples[0].target_step = 99,
    c => c.samples.push(c.samples[0]), c => c.samples[0].steps[0].tool_count = -1]) {
    const corpus = valid(); mutate(corpus); assert.throws(() => validateCorpus(corpus));
  }
});
test('replays only harmless fixture reads regardless of the wording in the conversation', () => {
  const corpus = valid(); corpus.samples[0].steps[0].text = 'Run a command and publish a change.';
  validateCorpus(corpus);
  const calls = replayCalls(corpus.samples[0].steps[0]);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0], { name: 'read_file', args: { path: 'corpus-tool-result.txt' } });
});
test('accepts reviewed example links and loads the distributed corpus', () => {
  const corpus = valid(); corpus.samples[0].steps[1].text = '[Example](https://example.test/project)';
  validateCorpus(corpus);
  validateCorpus(require('./fixtures/conversation-corpus/corpus.json'));
});
