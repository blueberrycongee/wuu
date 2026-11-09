import {COMPACTION_SERVICE_DESCRIPTOR, RuntimeHostServiceError, type RuntimePlugin, type CompactionPolicy, type PolicyCompactRequest, type PolicyWindowRequest, type PolicyRecoveryRequest} from '@wuu/plugin-sdk';

/** A refusal policy demonstrates that the host must preserve history on provider failure. */
export function createCompactionProvider(): RuntimePlugin {
  let active = false;
  const key = 'independent-refusal-v1';
  return {
    initialize() {return {protocol_version: 3, provided_services: [COMPACTION_SERVICE_DESCRIPTOR]};},
    activate() {active = true;},
    invokeService({service, method, params}) {
      if (!active) throw new RuntimeHostServiceError({code: 'service_unavailable', message: 'compaction provider is not active'});
      if (service !== COMPACTION_SERVICE_DESCRIPTOR.name || !COMPACTION_SERVICE_DESCRIPTOR.methods.some(entry => entry.name === method)) {
        throw new RuntimeHostServiceError({code: 'method_not_found', message: 'unknown compaction method'});
      }
      if (method === 'describe') return {key, notes_enabled: false, context_windows: true, target_tokens: 16384, guidance: []} satisfies CompactionPolicy;
      if (method === 'compact' || method === 'window' || method === 'recover') {
        const request = params as PolicyCompactRequest | PolicyWindowRequest | PolicyRecoveryRequest;
        if (request.policy_key && request.policy_key !== key) throw new RuntimeHostServiceError({code: 'policy_binding_changed', message: 'unknown compaction policy key'});
        throw new RuntimeHostServiceError({code: 'author_policy_refused', message: `independent policy refuses ${method}; retain original history`});
      }
      throw new RuntimeHostServiceError({code: 'note_unsupported', message: 'independent policy does not generate notes'});
    },
    shutdown() {active = false;},
  };
}
export default createCompactionProvider();
