import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pathToFileURL } from 'node:url';

const promptEntry = process.env.WUU_PARITY_PROMPT_ENTRY;
test('independent prompt owns the complete main and worker content', {skip: !promptEntry}, async () => {
  const {createPromptProvider} = await import(pathToFileURL(promptEntry));
  const provider = createPromptProvider();
  const invoke = role => provider.invokeService({service: 'agent.prompt', method: 'assemble', caller: 'acceptance', params: {role, cwd: '/synthetic', date: '2026-01-01', surface: {}, instructions: [{Content: 'default contamination'}], contributions: [{content: 'default contamination'}]}});
  provider.initialize();
  assert.throws(() => invoke('main'), error => error.code === 'service_unavailable');
  provider.activate();
  assert.deepEqual(invoke('main'), {content: 'independent-prompt:main', sections: []});
  assert.deepEqual(invoke('worker'), {content: 'independent-prompt:worker', sections: []});
  assert.throws(() => invoke('invalid'), /role/);
  provider.shutdown();
  assert.throws(() => invoke('main'), error => error.code === 'service_unavailable');
});

const compactionEntry = process.env.WUU_PARITY_COMPACTION_ENTRY;
test('independent refusal policy rejects mutations and preserves caller history', {skip: !compactionEntry}, async () => {
  const {createCompactionProvider} = await import(pathToFileURL(compactionEntry));
  const provider = createCompactionProvider();
  const invoke = (method, params = {}) => provider.invokeService({service: 'agent.compaction', method, caller: 'acceptance', params});
  provider.initialize();
  assert.throws(() => invoke('describe'), error => error.code === 'service_unavailable');
  provider.activate();
  const policy = invoke('describe');
  assert.equal(policy.notes_enabled, false);
  const messages = [{Role: 'user', Content: 'retain this question', seq: 1}, {Role: 'assistant', Content: 'retain this answer', seq: 2}];
  const original = structuredClone(messages);
  for (const method of ['compact', 'window', 'recover']) {
    assert.throws(() => invoke(method, {policy_key: policy.key, messages}), error => error.code === 'author_policy_refused');
    assert.deepEqual(messages, original);
    assert.throws(() => invoke(method, {policy_key: 'stale', messages}), error => error.code === 'policy_binding_changed');
  }
  for (const method of ['handoff', 'note-plan', 'compact-note', 'note-attempt']) assert.throws(() => invoke(method), error => error.code === 'note_unsupported');
  assert.throws(() => invoke('unknown'), error => error.code === 'method_not_found');
  provider.shutdown();
  assert.throws(() => invoke('describe'), error => error.code === 'service_unavailable');
});

const titleEntry = process.env.WUU_PARITY_TITLE_ENTRY;
test('independent title policy completes directly and owns output resolution', {skip: !titleEntry}, async () => {
  const {createTitleProvider, independentTitle} = await import(pathToFileURL(titleEntry));
  const provider = createTitleProvider();
  const invoke = (method, params) => provider.invokeService({service:'session.title',method,params,caller:'acceptance'});
  provider.initialize();
  assert.throws(() => invoke('plan',{}), error => error.code === 'service_unavailable');
  provider.activate();
  assert.deepEqual(invoke('plan',{model:'synthetic',human_messages:['question'],persist:true}),{action:'complete',title:independentTitle});
  assert.deepEqual(invoke('resolve',{text:'  unchanged  '}),{title:'independent-resolved:  unchanged  '});
  assert.throws(() => invoke('unknown',{}), error => error.code === 'method_not_found');
  provider.shutdown();
  assert.throws(() => invoke('plan',{}), error => error.code === 'service_unavailable');
});
