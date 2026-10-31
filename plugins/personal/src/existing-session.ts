import type { SessionLocatorV1, SessionRouteResult } from '@wuu/plugin-sdk';

/** A locator addresses existing work; it is never an authorization claim. */
export function existingSessionTarget(value: unknown): SessionLocatorV1 {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('existing_session requires an exact session locator');
  const input = value as Record<string, unknown>;
  if (Object.keys(input).some(key => !['version', 'execution_host', 'session_id', 'storage_host', 'display_host'].includes(key)) || input.version !== 1) throw new Error('unsupported existing session locator');
  for (const key of ['execution_host', 'session_id', 'storage_host', 'display_host']) {
    if (input[key] === undefined && ['storage_host', 'display_host'].includes(key)) continue;
    if (typeof input[key] !== 'string' || !input[key].trim() || input[key] !== input[key].trim() || input[key].length > 256 || input[key].includes('\0')) throw new Error(`existing_session.${key} must be an exact stable identity`);
  }
  return { version: 1, execution_host: input.execution_host as string, session_id: input.session_id as string,
    ...(input.storage_host ? {storage_host: input.storage_host as string} : {}), ...(input.display_host ? {display_host: input.display_host as string} : {}) };
}

/** Transport completion acknowledges a command, not completion of the worker.
 * Unknown outcomes retain their original operation identity for reconciliation. */
export function existingSessionResult<T>(receipt: SessionRouteResult, target: SessionLocatorV1, operationID: string): T {
  if (!receipt || receipt.version !== 1 || receipt.operation_id !== operationID || receipt.locator?.version !== 1 || receipt.locator.execution_host !== target.execution_host || receipt.locator.session_id !== target.session_id || receipt.status !== 'completed') {
    throw Object.assign(new Error('Session delivery is unconfirmed; inspect the original request before continuing'), {code: 'session_outcome_unknown'});
  }
  if (receipt.error) throw Object.assign(new Error(receipt.error.message), {code: receipt.error.code});
  if (receipt.result === undefined || receipt.result === null || typeof receipt.result !== 'object') throw Object.assign(new Error('Session response is unconfirmed; inspect the original request'), {code: 'session_outcome_unknown'});
  return receipt.result as T;
}
