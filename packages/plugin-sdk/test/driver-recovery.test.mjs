import test from 'node:test';
import assert from 'node:assert/strict';
import { driverTurn } from '../dist/driver.js';

test('exact recovery forwards original identity and never falls back to fresh tools', async () => {
  const calls = [];
  const failure = new Error('original admission unavailable');
  const turn = driverTurn({ call: async (method, input) => {
    calls.push({ method, input });
    throw failure;
  } }, { execution_id: 'resumed', session_id: 'session' });
  const intent = [{ id: 'call', name: 'read', arguments: '{}' }];
  await assert.rejects(turn.recoverTools('original', 'batch', intent), error => error === failure);
  assert.deepEqual(calls, [{ method: 'host.service.call', input: {
    service: 'driver.turn', method: 'recover_tools', params: {
      execution_id: 'resumed', original_execution_id: 'original', batch_id: 'batch', calls: intent,
    },
  } }]);
});
