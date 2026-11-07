import type { ExecutionAuthorizationReference, RuntimeHost } from '@wuu/plugin-sdk';

export function unwrapPersonalHost(value: unknown): unknown {
  if (!value || typeof value !== 'object' || !('ok' in value)) throw new Error('Invalid Personal host response');
  if (value.ok === true && 'value' in value) return value.value;
  if (value.ok === false && 'error' in value && value.error && typeof value.error === 'object' && 'code' in value.error && typeof value.error.code === 'string') {
    throw { code: value.error.code, ...('persistence_failed' in value.error && value.error.persistence_failed === true ? { persistence_failed: true } : {}) };
  }
  throw new Error('Invalid Personal host response');
}
export async function callPersonalHost(host: RuntimeHost, operation: string, input: unknown = {}, sessionID?: string, signal?: AbortSignal, executionAuthorization?: ExecutionAuthorizationReference): Promise<unknown> {
  return unwrapPersonalHost(await host.call('host.service.call', { service: 'host.personal', method: 'call', params: { operation, input, ...(executionAuthorization ? {execution_authorization:executionAuthorization} : {}), ...(sessionID ? { session_id: sessionID } : {}) } }, { signal }));
}
