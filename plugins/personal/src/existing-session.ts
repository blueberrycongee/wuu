import { isSessionRouteResult, type SessionLocatorV1, type SessionRouteResult } from '@wuu/plugin-sdk';

/** A locator addresses work; it is never an authorization claim. */
function target(value: unknown, workspace: boolean): SessionLocatorV1 {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('An exact session or workspace locator is required');
  const input = value as Record<string, unknown>;
  if (Object.keys(input).some(key => !['version', 'execution_host', 'session_id', 'storage_host', 'display_host'].includes(key)) || input.version !== 1 || workspace && Object.hasOwn(input, 'session_id')) throw new Error('unsupported session locator');
  for (const key of ['execution_host', ...workspace ? [] : ['session_id'], 'storage_host', 'display_host']) {
    if (input[key] === undefined && ['storage_host', 'display_host'].includes(key)) continue;
    if (typeof input[key] !== 'string' || !input[key].trim() || input[key] !== input[key].trim() || input[key].length > 256 || input[key].includes('\0')) throw new Error(`locator.${key} must be an exact stable identity`);
  }
  return { version: 1, execution_host: input.execution_host as string, ...(!workspace ? {session_id: input.session_id as string} : {}),
    ...(input.storage_host ? {storage_host: input.storage_host as string} : {}), ...(input.display_host ? {display_host: input.display_host as string} : {}) };
}
export function existingSessionTarget(value: unknown): SessionLocatorV1 { return target(value, false); }
export function workspaceSessionTarget(value: unknown): SessionLocatorV1 { return target(value, true); }

/** Transport completion acknowledges a command, not completion of the worker.
 * Unknown outcomes retain their original operation identity for reconciliation. */
export function existingSessionResult<T>(receipt: SessionRouteResult, target: SessionLocatorV1, operationID: string, operation: 'create' | 'send' = 'send'): T {
  if (!isSessionRouteResult(receipt, {operation, operation_id: operationID, locator: target}) || receipt.status !== 'completed') {
    throw Object.assign(new Error('Session delivery is unconfirmed; inspect the original request before continuing'), {code: 'session_outcome_unknown'});
  }
  if (receipt.error) throw Object.assign(new Error(receipt.error.message), {code: receipt.error.code});
  if (receipt.result === undefined || receipt.result === null || typeof receipt.result !== 'object') throw Object.assign(new Error('Session response is unconfirmed; inspect the original request'), {code: 'session_outcome_unknown'});
  return receipt.result as T;
}
