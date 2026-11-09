const assert = require('node:assert/strict');

// This is a publication guard, not an automatic anonymizer. New samples still
// require a local review of prose, code, links and metadata before admission.
function validateCorpus(corpus) {
  const fields = (value, names) => assert.deepEqual(Object.keys(value).sort(), names.split(' ').sort(), 'Unexpected corpus fields');
  const safeText = value => {
    assert.equal(typeof value, 'string');
    assert.ok(!/(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|Bearer\s+\S{16,}|-----BEGIN .*PRIVATE KEY|\/(?:Users|home)\/[^\s/]+|[A-Z]:\\Users\\|[\w.+-]+@[\w.-]+\.[a-z]{2,})/i.test(value), 'Sensitive-looking corpus text');
    for (const match of value.matchAll(/https?:\/\/[^\s<>"'`)]+/g)) {
      const url = new URL(match[0]);
      assert.ok(['example.test', 'example.com'].includes(url.hostname) && !url.username && !url.password, 'Non-example URL in corpus');
    }
  };
  fields(corpus, 'version timing tools samples');
  assert.equal(corpus.version, 1);
  assert.equal(corpus.timing, 'simulated');
  assert.equal(corpus.tools, 'synthetic-read-file');
  assert.ok(Array.isArray(corpus.samples) && corpus.samples.length > 0);
  const ids = new Set();
  for (const sample of corpus.samples) {
    fields(sample, 'id family model focus target_step prompt steps');
    assert.match(sample.id, /^sample-\d{2}$/);
    assert.ok(!ids.has(sample.id), 'Duplicate sample'); ids.add(sample.id);
    for (const field of ['family', 'model', 'focus', 'prompt']) safeText(sample[field]);
    assert.ok(sample.prompt.trim());
    assert.ok(Array.isArray(sample.steps) && sample.steps.length > 0);
    assert.ok(Number.isInteger(sample.target_step) && sample.target_step >= 0 && sample.target_step < sample.steps.length);
    for (const [index, step] of sample.steps.entries()) {
      fields(step, 'text tool_count'); safeText(step.text);
      assert.ok(Number.isInteger(step.tool_count) && step.tool_count >= 0 && step.tool_count <= 32);
      assert.equal(step.tool_count === 0, index === sample.steps.length - 1, 'Only the last step may finish the turn');
    }
    assert.ok(sample.steps.at(-1).text.trim(), 'Missing final answer');
    assert.ok(sample.steps[sample.target_step].text.trim(), 'Missing target text');
  }
  return corpus;
}

// Original tools, arguments, outputs and attachments never enter the package.
// Preserve call counts and placement using reads of one disposable fixture file.
function replayCalls(step) {
  return Array.from({ length: step.tool_count }, () => ({ name: 'read_file', args: { path: 'corpus-tool-result.txt' } }));
}
module.exports = { validateCorpus, replayCalls };
