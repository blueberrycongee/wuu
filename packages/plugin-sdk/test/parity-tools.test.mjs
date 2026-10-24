import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pathToFileURL } from 'node:url';

// Compile the example against the packed public SDK first; this test deliberately
// does not import a source-tree alias or substitute the default tool provider.
const entry = process.env.WUU_PARITY_TOOLS_ENTRY;
test('independent registry owns discovery, resolution, execution and cleanup', { skip: !entry }, async () => {
  const { createToolsProvider } = await import(pathToFileURL(entry));
  const plugin = createToolsProvider('author-one');
  const invoke = (method, params) => plugin.invokeService({ service: 'agent.tools', method, params, caller: 'acceptance' });
  const initialized = plugin.initialize();
  assert.equal(initialized.provided_services[0].name, 'agent.tools');
  assert.throws(() => invoke('list', {}), /not active/);
  plugin.activate();
  assert.deepEqual(invoke('prepare', {history: [{Role: 'user', Content: 'probe'}]}), {messages: []});
  invoke('freeze', {});
  const listed = invoke('list', {});
  invoke('unfreeze', {});
  assert.deepEqual(invoke('list', {}), listed);
  assert.deepEqual(listed.tools.map(tool => tool.name), ['author_echo']);
  assert.deepEqual(invoke('resolve', { call: { name: 'shell' } }), {found: false, supports: false});
  assert.equal(invoke('resolve', { call: { name: 'author_echo' } }).metadata.read_only, true);
  assert.throws(() => invoke('execute', { call: { name: 'shell', arguments: '{}' } }), /unknown tool/);
  assert.throws(() => invoke('execute', { call: { name: 'author_echo', arguments: '{' } }), /JSON/);
  assert.throws(() => invoke('execute', { call: { name: 'author_echo', arguments: '{"text":1}' } }), /text must be a string/);
  const result = invoke('execute', { call: { name: 'author_echo', arguments: JSON.stringify({text: '独立实现'}) } });
  assert.equal(result.result.content[0].text, 'author-one:独立实现');
  listed.tools.length = 0;
  assert.equal(invoke('list', {}).tools.length, 1);
  plugin.shutdown();
  assert.throws(() => invoke('execute', { call: { name: 'author_echo', arguments: '{}' } }), /not active/);
});
