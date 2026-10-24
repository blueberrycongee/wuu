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
