import { RuntimeHostServiceError, type RuntimePlugin, type ServiceDescriptor } from '@wuu/plugin-sdk';

const descriptor: ServiceDescriptor = {
  name: 'agent.tools', version: '1.0.0',
  methods: ['list', 'resolve', 'execute'].map(name => ({
    name, input_schema: `agent.tools.${name}.input.v1`, output_schema: `agent.tools.${name}.output.v1`,
  })),
};

/** A complete, deliberately small registry: it owns its only tool and calls no host service. */
export function createToolsProvider(owner = 'independent-tools'): RuntimePlugin {
  let active = false;
  return {
    initialize() { return { protocol_version: 3, provided_services: [descriptor] }; },
    activate() { active = true; },
    invokeService({service, method, params}) {
      if (!active) throw new RuntimeHostServiceError({code: 'service_unavailable', message: 'tool provider is not active'});
      if (service !== descriptor.name) throw new Error('unknown service');
      if (method === 'list') return {
        tools: [{name: 'author_echo', description: 'Echo text with the selected provider identity.', input_schema: {
          type: 'object', properties: {text: {type: 'string'}}, required: ['text'], additionalProperties: false,
        }}], catalog: [], catalog_prompt: '',
      };
      const call = (params as {call?: {name?: string; arguments?: string}} | undefined)?.call;
      if (method === 'resolve') return call?.name === 'author_echo'
        ? {found: true, metadata: {orchestrator: false, read_only: true, concurrency_safe: true, destructive: false, risk: 'low', reason: 'pure text transformation'}}
        : {found: false};
      if (method !== 'execute') throw new Error('unknown tool service method');
      if (call?.name !== 'author_echo') throw new RuntimeHostServiceError({code: 'tool_not_found', message: 'unknown tool'});
      const input = JSON.parse(call.arguments ?? '{}') as {text?: unknown};
      if (typeof input?.text !== 'string') throw new Error('text must be a string');
      return {result: {content: [{type: 'text', text: `${owner}:${input.text}`}]}};
    },
    shutdown() { active = false; },
  };
}

export default createToolsProvider();
