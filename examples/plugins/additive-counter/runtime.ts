import type { RuntimePlugin } from '@wuu/plugin-sdk';
import { COUNTER_SERVICE, counterDescriptor, type CounterSnapshot } from './contract.js';

/** Ephemeral state: a new runtime deliberately starts from zero. */
export function createCounter(): RuntimePlugin {
  let state: CounterSnapshot = { value: 0, revision: 0 };
  const listeners = new Set<(snapshot: CounterSnapshot) => void>();
  return {
    concurrentServices: [COUNTER_SERVICE],
    initialize() { return { provided_services: [counterDescriptor] }; },
    async invokeService(request, _host, execution) {
      execution.signal.throwIfAborted();
      if (request.service !== COUNTER_SERVICE) throw new Error('Unknown counter service');
      if (request.method === 'read') return state;
      if (request.method === 'add') {
        const delta = (request.params as { delta?: unknown })?.delta;
        if (typeof delta !== 'number' || !Number.isSafeInteger(delta) || !Number.isSafeInteger(state.value + delta)) throw new Error('delta and resulting value must be safe integers');
        state = { value: state.value + delta, revision: state.revision + 1 };
        for (const listener of [...listeners]) listener(state);
        return state;
      }
      if (request.method !== 'watch') throw new Error('Unknown counter method');
      const after = (request.params as { after?: unknown })?.after;
      if (typeof after !== 'number' || !Number.isSafeInteger(after) || after < 0) throw new Error('after must be a nonnegative integer');
      if (after !== state.revision) return state;
      return new Promise<CounterSnapshot>((resolve, reject) => {
        const cleanup = () => { listeners.delete(changed); execution.signal.removeEventListener('abort', aborted); };
        const changed = (next: CounterSnapshot) => { cleanup(); resolve(next); };
        const aborted = () => { cleanup(); reject(execution.signal.reason); };
        listeners.add(changed);
        execution.signal.addEventListener('abort', aborted, { once: true });
        if (execution.signal.aborted) aborted();
      });
    },
  };
}
