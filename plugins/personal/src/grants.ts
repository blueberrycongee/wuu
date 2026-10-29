import { executionGrants, type ExecutionAuthorizationIntent, type ExecutionAuthorizationReference, type RuntimeHost } from '@wuu/plugin-sdk';

/** Business state stores only the selected authority's opaque reference and its
 * display expiry. Neither field grants authority without host revalidation. */
export interface PersonalAuthorization {
  reference: ExecutionAuthorizationReference;
  expires_at: string;
}
export const authorizationCurrent = (authorization?: PersonalAuthorization): authorization is PersonalAuthorization => Boolean(authorization?.reference?.provider && authorization.reference.id && Number.isFinite(Date.parse(authorization.expires_at)) && Date.parse(authorization.expires_at) > Date.now());
export const sameAuthorization = (a: ExecutionAuthorizationReference | undefined, b: ExecutionAuthorizationReference | undefined): boolean => Boolean(a && b && a.provider === b.provider && a.id === b.id);
export async function issueAuthorization(host: RuntimeHost, input: Omit<ExecutionAuthorizationIntent, 'authority_service'>): Promise<PersonalAuthorization> {
  const grant = await executionGrants(host).issue({...input,authority_service:'personal.agents'});
  const authorization = {reference:grant.reference,expires_at:grant.expires_at};
  if (!authorization.reference?.provider || !authorization.reference.id || !Number.isFinite(Date.parse(authorization.expires_at))) throw new Error('Invalid selected execution authorization response');
  if (!authorizationCurrent(authorization)) throw authorizationError('selected grant has expired');
  return authorization;
}

/** The selected provider owns lifetime policy. Save this intent before issuance,
 * so a response lost after admission cannot change the idempotency digest. */
export interface PersonalAuthorizationIntent { request_id: string; expires_at: string }
export async function authorizationExpiry(host: RuntimeHost, parent?: PersonalAuthorization): Promise<string> {
  if (parent) return parent.expires_at;
  const policy = await executionGrants(host).describe();
  if (!Number.isSafeInteger(policy.default_lifetime_seconds) || policy.default_lifetime_seconds <= 0 || !Number.isSafeInteger(policy.max_lifetime_seconds) || policy.default_lifetime_seconds > policy.max_lifetime_seconds) throw new Error('Invalid selected authorization lifetime policy');
  return new Date(Date.now() + policy.default_lifetime_seconds * 1000).toISOString();
}
export function authorizationRequired(error: unknown): boolean {
  return Boolean(error && typeof error === 'object' && 'code' in error && error.code === 'execution_authorization_required');
}

/** These are ceilings checked by the selected resource policy at admission, not
 * permission to bypass native review, connector scopes or tool policy. */
export const TASK_EXECUTION_ACTIONS = [
  'personal/memory.read', 'personal/memory.operation', 'personal/memory.source', 'personal/memory.commit',
  'user/session/send', 'user/session/inspect', 'user/session/cancel',
  'user/session/context', 'user/session/history.read', 'user/session/history.search', 'user/session/attachment.read',
  'personal/review.execution.status', 'personal/actions.sync', 'personal/actions.resume', 'personal/file.execute',
  'personal/connectors.read.list', 'personal/connectors.read.actions', 'personal/connectors.call', 'personal/connectors.poll',
  'personal/calendar.read.status', 'personal/calendar.read.events', 'personal/calendar.read.authorize',
] as const;
export const FEEDBACK_ACTIONS = ['personal/memory.read','user/session/send','user/session/inspect','user/session/context','user/session/history.read'] as const;

/** Tools-disabled delivery still runs scoped conversation/pre-step hooks. These
 * session reads do not authorize tool effects or another execution session. */
export const SESSION_DELIVERY_ACTIONS = ['personal/memory.read','user/session/send','user/session/inspect','user/session/cancel','user/session/context','user/session/history.read'] as const;

export function authorizationError(reason: string): Error & {code:string} {
  return Object.assign(new Error(`needs_authorization: ${reason}`),{code:'execution_authorization_required'});
}
