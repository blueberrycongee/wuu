import type { ExecutionAuthorizationReference, RuntimeHost } from '@wuu/plugin-sdk';
import type { CalendarConnection } from './connected-calendar.js';

export function unwrapPersonalHost(value: unknown): unknown {
  if (!value || typeof value !== 'object' || !('ok' in value)) throw new Error('Invalid Personal host response');
  if (value.ok === true && 'value' in value) return value.value;
  if (value.ok === false && 'error' in value && value.error && typeof value.error === 'object' && 'code' in value.error && typeof value.error.code === 'string') {
    throw { code: value.error.code, ...('persistence_failed' in value.error && value.error.persistence_failed === true ? { persistence_failed: true } : {}) };
  }
  throw new Error('Invalid Personal host response');
}
export function decodeCalendarConnection(value: unknown): CalendarConnection {
  if (!value || typeof value !== 'object') throw new Error('Invalid Calendar connection');
  const v = value as Record<string,unknown>;
  if (v.product_domain !== 'personal' || v.provider !== 'google-calendar' || typeof v.state !== 'string' || !['unavailable','unconnected','connected','needs_reauth','disconnected'].includes(v.state) || !Array.isArray(v.selected_calendars) || !v.selected_calendars.every(x => typeof x === 'string')) throw new Error('Invalid Calendar connection');
  for (const key of ['connection_id','user_space_id','account_id','selection_revision','error_code']) if (v[key] !== undefined && typeof v[key] !== 'string') throw new Error('Invalid Calendar connection');
  if (v.state === 'connected' && (!v.connection_id || !v.account_id || !v.user_space_id || !v.selection_revision)) throw new Error('Incomplete Calendar connection');
  if (v.authorization_diagnostics !== undefined && (!Array.isArray(v.authorization_diagnostics) || !v.authorization_diagnostics.every(item => item && typeof item === 'object' && typeof item.attempt_id === 'string' && item.attempt_id && typeof item.error_code === 'string' && item.error_code && (item.persistence_failed === undefined || typeof item.persistence_failed === 'boolean')))) throw new Error('Invalid Calendar authorization diagnostics');
  return structuredClone(v) as unknown as CalendarConnection;
}
export async function callPersonalHost(host: RuntimeHost, operation: string, input: unknown = {}, sessionID?: string, signal?: AbortSignal, executionAuthorization?: ExecutionAuthorizationReference): Promise<unknown> {
  return unwrapPersonalHost(await host.call('host.service.call', { service: 'host.personal', method: 'call', params: { operation, input, ...(executionAuthorization ? {execution_authorization:executionAuthorization} : {}), ...(sessionID ? { session_id: sessionID } : {}) } }, { signal }));
}
