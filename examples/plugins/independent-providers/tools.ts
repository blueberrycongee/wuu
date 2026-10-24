import { RuntimeHostServiceError, TOOL_SERVICE_DESCRIPTOR, type RuntimePlugin, type ToolServiceList, type ToolServiceResolution, type ToolServiceExecution, type ToolServiceCallRequest } from '@wuu/plugin-sdk';

const descriptor = TOOL_SERVICE_DESCRIPTOR;

/** A complete, deliberately small registry: it owns its only tool and calls no host service. */
export function createToolsProvider(owner = 'independent-tools'): RuntimePlugin {
  let active = false;
  return {
    initialize() { return { protocol_version: 3, provided_services: [descriptor] }; },
    activate() { active = true; },
    invokeService({service, method, params}) {
      if (!active) throw new RuntimeHostServiceError({code: 'service_unavailable', message: 'tool provider is not active'});
      if (service !== descriptor.name) throw new Error('unknown service');
      // The catalog is immutable for this provider generation, so freezing cannot change it.
      if (method === 'freeze' || method === 'unfreeze') return {};
      if (method === 'prepare') return {messages: []};
      if (method === 'list') return {
        tools: [{name: 'author_echo', description: 'Echo text with the selected provider identity.', input_schema: {
          type: 'object', properties: {text: {type: 'string'}}, required: ['text'], additionalProperties: false,
        }}], catalog: [], catalog_prompt: '', system_fragment: '',
      } satisfies ToolServiceList;
      const call = (params as ToolServiceCallRequest | undefined)?.call;
      if (method === 'resolve') return call?.name === 'author_echo'
        ? {found: true, supports: true, display: {label: 'Independent echo'}, metadata: {orchestrator: false, read_only: true, concurrency_safe: true, destructive: false, risk: 'low', reason: 'pure text transformation'}} satisfies ToolServiceResolution
        : {found: false, supports: false} satisfies ToolServiceResolution;
      if (method !== 'execute') throw new Error('unknown tool service method');
      if (call?.name !== 'author_echo') throw new RuntimeHostServiceError({code: 'tool_not_found', message: 'unknown tool'});
      const input = JSON.parse(call.arguments ?? '{}') as {text?: unknown};
      if (typeof input?.text !== 'string') throw new Error('text must be a string');
      return {result: {content: [{type: 'text', text: `${owner}:${input.text}`}]}} satisfies ToolServiceExecution;
    },
    shutdown() { active = false; },
  };
}

export default createToolsProvider();
