import {PROMPT_SERVICE_DESCRIPTOR, RuntimeHostServiceError, type RuntimePlugin, type PromptAssemblyRequest, type PromptAssemblyResult} from '@wuu/plugin-sdk';

/** This provider owns complete wording; it intentionally needs none of the supplied context. */
export function createPromptProvider(): RuntimePlugin {
  let active = false;
  return {
    initialize() {return {protocol_version: 3, provided_services: [PROMPT_SERVICE_DESCRIPTOR]};},
    activate() {active = true;},
    invokeService({service, method, params}) {
      if (!active) throw new RuntimeHostServiceError({code: 'service_unavailable', message: 'prompt provider is not active'});
      if (service !== PROMPT_SERVICE_DESCRIPTOR.name || method !== 'assemble') throw new Error('unknown prompt method');
      const {role} = params as PromptAssemblyRequest;
      if (role !== 'main' && role !== 'worker') throw new Error('invalid prompt role');
      return {content: `independent-prompt:${role}`, sections: []} satisfies PromptAssemblyResult;
    },
    shutdown() {active = false;},
  };
}
export default createPromptProvider();
