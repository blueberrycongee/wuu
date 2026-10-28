/** Acceptance-only submission marker for cancelling a terminal-wait observation. */
import {reportExecutionUpdate, type HostServiceContracts, type RuntimePlugin} from '@wuu/plugin-sdk';
import {createProgressPlugin} from './runtime.js';

export function createObservedProgressPlugin(): RuntimePlugin {
  const plugin = createProgressPlugin();
  const initialize = plugin.initialize;
  const invoke = plugin.invokeCapability!;
  return {
    ...plugin,
    async initialize(...args) {
      const result = await initialize(...args);
      return {...result, required_services: [...result.required_services ?? [],
        {name: 'execution.update', major_version: 1, required: true}]};
    },
    async invokeCapability(params, host, execution) {
      const request = params.input as {method: string; input: HostServiceContracts['host.session.inspect']['params']};
      if (request.method !== 'fixture.inspect-wait') return invoke(params, host, execution);
      const pending = host.call('host.session.inspect', {...request.input, wait: 'terminal', timeout_ms: 20_000})
        .then(result => ({ok: true as const, result}), error => ({ok: false as const, error}));
      await reportExecutionUpdate(host, {execution_id: execution.executionId, message: 'progress-inspect-waiting'});
      const outcome = await pending;
      if (!outcome.ok) throw outcome.error;
      return {output: {result: outcome.result}};
    },
  };
}
