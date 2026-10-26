import test from 'node:test';
import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
const entry = process.env.WUU_PARITY_APPLICATION_ENTRY;
test('independent application owns navigation, checkpoints and consumer lifetime', {skip: !entry}, async () => {
  const {createExplorer} = await import(pathToFileURL(entry));
  const calls = [], signal = new AbortController().signal;
  const api = {signal,
    async listWorkspaces() {calls.push('workspaces'); return {workspaces: [{id: 'p', name: 'Fixture project', root: '/fixture', available: true}]};},
    async listThreads(root) {assert.equal(root, '/fixture'); calls.push('threads'); return [{id: 't', title: 'Fixture session', updatedAt: '', latestCompletedTurnId: 'boundary'}];},
    async readVisibleHistory(request) {calls.push(request); return {turns: [{id: 'prior', userText: 'Fixture user', assistantText: 'Fixture assistant', imageCount: 0, fileCount: 0}]};},
  };
  const owner = createExplorer(api);
  assert.equal(calls.length, 0, 'preparation does not read or subscribe');
  await owner.activate();
  const consumer = new AbortController(), app = owner.createScope(consumer.signal);
  assert.equal(app.getSnapshot().navigation.nodes[0].label, 'Fixture session');
  assert.equal(app.getSnapshot(), app.getSnapshot(), 'snapshot identity is stable');
  let updates = 0; app.subscribe(() => ++updates);
  await app.invoke('thread.select', {threadId: 't'});
  assert.equal(app.getSnapshot().conversation.turns[0].items[1].text, 'Fixture assistant');
  assert.equal(calls.at(-1).beforeTurnId, 'boundary');
  assert.equal(updates, 1);
  await assert.rejects(app.invoke('composer.stop', {}), /does not support/);
  assert.throws(() => app.settingsControls, /does not support/);
  const abortSelection = new AbortController(); abortSelection.abort();
  await assert.rejects(app.invoke('workspace.select', {workspaceId: 'not-committed'}, {signal: abortSelection.signal}), /abort/i);
  const checkpoint = await owner.deactivate();
  assert.deepEqual(checkpoint.value, {workspaceId: 'p', threadId: 't'});
  assert.throws(() => app.getSnapshot(), /stopped|abort/i);
  const replacement = createExplorer(api); await replacement.activate(checkpoint);
  assert.equal(replacement.createScope(signal).getSnapshot().conversation.threadId, 't');
  const scoped = new AbortController(), stale = replacement.createScope(scoped.signal);
  scoped.abort(); assert.throws(() => stale.getSnapshot(), /abort/i);
  replacement.dispose();
  await assert.rejects(createExplorer(api).activate({schema: 'other', value: {}}), /Incompatible/);
});
test('retired explorer cannot publish delayed reads', {skip: !entry}, async () => {
  const {createExplorer} = await import(pathToFileURL(entry));
  let release, held = false;
  const owner = createExplorer({signal: new AbortController().signal,
    async listWorkspaces() {if (held) await new Promise(resolve => {release = resolve;}); return {workspaces: []};},
  });
  await owner.activate(); const app = owner.createScope(new AbortController().signal);
  held = true; const operation = app.invoke('navigation.refresh', {});
  const rejected = assert.rejects(operation, /stopped/);
  await owner.deactivate(); release(); await rejected;
});
