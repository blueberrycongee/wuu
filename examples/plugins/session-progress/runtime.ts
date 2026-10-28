import {
  PLUGIN_CLIENT_REQUEST_CAPABILITY, SESSION_PROGRESS_HOST_FEATURE,
  requireKernelService, type HostServiceContracts, type RuntimePlugin,
} from '@wuu/plugin-sdk';
import {botStatusCard, fleetProgress} from './projections.js';

/** Independent bot/fleet adapter. The caller owns polling cadence and transport;
 * inspect is an opt-in bounded snapshot, not a subscription or durable cursor. */
export function createProgressPlugin(): RuntimePlugin {
  return {
    initialize: () => ({
      protocol_version: 3,
      required_host_features: [SESSION_PROGRESS_HOST_FEATURE],
      capabilities: [{id: PLUGIN_CLIENT_REQUEST_CAPABILITY, kind: 'decision', version: 1}],
      required_services: [
        requireKernelService('host.session.create'), requireKernelService('host.session.send'),
        requireKernelService('host.session.inspect'), requireKernelService('host.session.cancel'),
      ],
    }),
    async invokeCapability(params, host) {
      const request = params.input as {method: string; input: unknown};
      switch (request.method) {
        case 'session.start': {
          const input = request.input as {request_id: string; prompt: string};
          if (!input.request_id || !input.prompt) throw new Error('request_id and prompt are required');
          const created = await host.call('host.session.create', {
            request_id: `${input.request_id}-create`, name: 'Progress example',
            visibility: 'plugin', context_source: 'fresh',
          });
          const sent = await host.call('host.session.send', {
            request_id: input.request_id, session_id: created.session_id,
            input: {prompt: input.prompt}, cause: 'session-progress.example',
          });
          return {output: {result: sent}};
        }
        case 'session.send':
          return {output: {result: await host.call('host.session.send', request.input as HostServiceContracts['host.session.send']['params'])}};
        case 'session.cancel':
          return {output: {result: await host.call('host.session.cancel', request.input as HostServiceContracts['host.session.cancel']['params'])}};
        case 'session.inspect':
          return {output: {result: await host.call('host.session.inspect', request.input as HostServiceContracts['host.session.inspect']['params'])}};
        case 'progress.bot':
        case 'progress.fleet': {
          const input = request.input as {session_id: string; request_id: string};
          const inspected = await host.call('host.session.inspect', {
            session_id: input.session_id, request_id: input.request_id, include_progress: true,
          });
          // Feature negotiation guarantees the field on opted-in responses.
          if (!inspected.progress) throw new Error('Host omitted negotiated session progress');
          return {output: {result: request.method === 'progress.bot'
            ? botStatusCard(inspected.progress) : fleetProgress(inspected.progress)}};
        }
        default: throw new Error(`Unknown progress example method: ${request.method}`);
      }
    },
  };
}
