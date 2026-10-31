import type { RuntimeHost, RuntimeHostCallOptions, HostServiceContracts, ServiceDescriptor, ServiceRequirement } from './index.js';
import type { PrincipalIdentity } from './identity.js';
import type { ResourceReference } from './resource-policy.js';
import type { SessionLocatorV1, SessionRouteRequest, SessionRouteResult } from './session-transport.js';

/** Opaque provider-owned reference, not a serialized principal or bearer grant.
 * Persist only this reference; validation always uses the selected authority. */
export interface ExecutionAuthorizationReference { readonly provider: string; readonly id: string; }
export interface ExecutionAuthorizationAuthority {
  readonly service: string;
  readonly major: number;
  readonly provider: string;
  readonly subject: string;
  readonly fingerprint: string;
}
export interface ExecutionAuthorizationBounds {
  /** Required nonempty allowlists. Empty never means unrestricted. */
  readonly model_providers: readonly string[];
  readonly models: readonly string[];
  readonly permission_modes: readonly string[];
  readonly max_depth: number;
}
export interface ExecutionAuthorizationScope {
  /** task-lineage requires empty run_id/session_id and permits only attested
   * child derivation, never a wildcard session effect. exact-run requires both. */
  readonly kind: 'task-lineage' | 'exact-run';
  readonly authority: ExecutionAuthorizationAuthority;
  readonly bounds: ExecutionAuthorizationBounds;
  readonly depth: number;
  readonly consumer: string;
  readonly user_space_id: string;
  readonly product_domain: string;
  readonly task_id: string;
  readonly run_id: string;
  readonly session_id: string;
  readonly context_digest: string;
  readonly revision: number;
}
export interface ExecutionAuthorizationCapability {
  readonly action: string;
  readonly resources: readonly ResourceReference[];
}
export interface ExecutionAuthorizationIssue {
  readonly request_id: string;
  readonly scope: ExecutionAuthorizationScope;
  readonly capabilities: readonly ExecutionAuthorizationCapability[];
  /** Finite RFC3339 validity bound; no implicit perpetual grant. */
  readonly expires_at: string;
  readonly parent?: ExecutionAuthorizationReference;
}
export interface ExecutionAuthorizationGrant {
  readonly reference: ExecutionAuthorizationReference;
  readonly principal: PrincipalIdentity;
  readonly scope: ExecutionAuthorizationScope;
  readonly capabilities: readonly ExecutionAuthorizationCapability[];
  readonly expires_at: string;
}
export interface ExecutionAuthorizationValidate {
  readonly reference: ExecutionAuthorizationReference;
  readonly scope: ExecutionAuthorizationScope;
  readonly action: string;
  readonly resources: readonly ResourceReference[];
}
export interface ExecutionAuthorizationValidation {
  readonly valid: boolean;
  readonly reason?: string;
  readonly principal?: PrincipalIdentity;
  readonly scope?: ExecutionAuthorizationScope;
  readonly expires_at?: string;
}
export interface ExecutionAuthorizationRevoke {
  readonly reference: ExecutionAuthorizationReference;
  readonly scope: ExecutionAuthorizationScope;
}
export interface ExecutionAuthorizationRevocation { readonly revoked: boolean; }
export interface ExecutionAuthorizationPolicy {
  /** Added by the host grant gateway, not provider authorization. Missing means
   * unsupported; session-route.v1 enables exact existing-session routing only. */
  readonly host_features?: readonly string[];
  readonly default_lifetime_seconds: number;
  readonly max_lifetime_seconds: number;
  readonly authority_kinds: readonly string[];
  readonly scope_kinds: readonly string[];
}
/** Persist this exact intent before acquisition. The digest covers the exact
 * publication; it cannot authorize another candidate, payload, or resource. */
export interface ExecutionAuthorizationCommit {
  readonly id: string;
  readonly operation_digest: string;
  readonly authorization: ExecutionAuthorizationValidate;
}
/** Expiry is an admission cutoff, not a timeout: this exact admitted operation
 * remains fenced through terminal receipt disposition, even after restart. */
export interface ExecutionAuthorizationCommitGuard {
  readonly id: string;
  readonly operation_digest: string;
  readonly authorization_digest: string;
  readonly principal: PrincipalIdentity;
  readonly expires_at: string;
}
export type ExecutionAuthorizationCommitRelease = ExecutionAuthorizationCommit & (
  | { readonly outcome: 'committed'; readonly receipt_digest: string }
  | { readonly outcome: 'not-committed'; readonly receipt_digest?: never }
);
export interface ExecutionAuthorizationCommitReleased { readonly released: true; }
export interface ExecutionAuthorizationCommitStatus {
  readonly guard: ExecutionAuthorizationCommitGuard;
  readonly caller: string;
  readonly outcome?: 'committed' | 'not-committed';
  readonly receipt_digest?: string;
  readonly reconciled_by?: PrincipalIdentity;
  readonly reason?: string;
}
export interface ExecutionAuthorizationCommitList {
  readonly commits: readonly (ExecutionAuthorizationCommitStatus & { readonly request: ExecutionAuthorizationCommit })[];
}
export type ExecutionAuthorizationCommitReconcile = ExecutionAuthorizationCommitRelease & {
  readonly expected_caller: string;
  readonly reason: string;
};

// The v1 authorization digest uses the Go contract field order, including empty
// fields and HTML/U+2028/U+2029 JSON escaping, not the caller object's insertion
// order. This is a wire commitment, not permission derived from a local hash.
async function commitAuthorizationDigest(input: ExecutionAuthorizationValidate): Promise<string> {
  const s = input.scope, a = s.authority, b = s.bounds;
  const normalized = {
    reference: {provider: input.reference.provider, id: input.reference.id},
    scope: {kind:s.kind,authority:{service:a.service,major:a.major,provider:a.provider,subject:a.subject,fingerprint:a.fingerprint},
      bounds:{model_providers:b.model_providers,models:b.models,permission_modes:b.permission_modes,max_depth:b.max_depth},
      depth:s.depth,consumer:s.consumer,user_space_id:s.user_space_id,product_domain:s.product_domain,task_id:s.task_id,
      run_id:s.run_id,session_id:s.session_id,context_digest:s.context_digest,revision:s.revision},
    action: input.action, resources: input.resources.map(resource => ({kind:resource.kind,...(resource.id ? {id:resource.id} : {})})),
  };
  const json = JSON.stringify(normalized).replace(/[<>&\u2028\u2029]/g, character => `\\u${character.charCodeAt(0).toString(16).padStart(4,'0')}`);
  const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(json));
  return Array.from(new Uint8Array(hash), byte => byte.toString(16).padStart(2,'0')).join('');
}
function boundedCommitString(value: unknown, maxBytes: number, forbidden: RegExp): value is string {
  return typeof value === 'string' && value.trim() !== '' && new TextEncoder().encode(value).byteLength <= maxBytes && !forbidden.test(value);
}
async function verifyCommitGuard(request: ExecutionAuthorizationCommit, guard: ExecutionAuthorizationCommitGuard, terminalWithoutAdmission = false): Promise<void> {
  if (!guard || guard.id !== request.id || guard.operation_digest !== request.operation_digest || guard.authorization_digest !== await commitAuthorizationDigest(request.authorization)) throw new Error('Invalid execution commit admission binding');
  if (terminalWithoutAdmission) return;
  const principal = guard.principal;
  if (!principal || !['user','device','agent'].includes(principal.kind) || ![principal.issuer,principal.tenant,principal.subject].every(value => boundedCommitString(value,4096,/\0/))) throw new Error('Invalid execution commit principal');
  // Date.parse coerces nonstrings and normalizes invalid calendar dates. Match
  // RFC3339 and the actual calendar before using it to check finite validity.
  const expiry = typeof guard.expires_at === 'string' && /^(\d{4})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])T([01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.exec(guard.expires_at);
  if (!expiry) throw new Error('Invalid execution commit expiry');
  const year = Number(expiry[1]), month = Number(expiry[2]), day = Number(expiry[3]);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31,leap ? 29 : 28,31,30,31,30,31,31,30,31,30,31];
  const milliseconds = Date.parse(guard.expires_at);
  if (day > days[month-1] || !Number.isFinite(milliseconds) || milliseconds === Date.parse('0001-01-01T00:00:00Z')) throw new Error('Invalid execution commit expiry');
}
async function verifyCommitStatus(request: ExecutionAuthorizationCommit, status: ExecutionAuthorizationCommitStatus): Promise<void> {
  if (!status || !boundedCommitString(status.caller,1024,/[\0\r\n*]/) || status.outcome !== undefined && status.outcome !== 'committed' && status.outcome !== 'not-committed' || status.outcome === 'committed' && (typeof status.receipt_digest !== 'string' || !/^[0-9a-f]{64}$/.test(status.receipt_digest)) || status.outcome !== 'committed' && status.receipt_digest !== undefined) throw new Error('Invalid execution commit inspection result');
  await verifyCommitGuard(request, status.guard, status.outcome === 'not-committed');
}
function verifyCommitReleased(result: ExecutionAuthorizationCommitReleased): ExecutionAuthorizationCommitReleased {
  if (!result || result.released !== true) throw new Error('Execution commit release omitted confirmation');
  return result;
}
export const EXECUTION_AUTHORIZATION_SERVICE = 'execution.authorization';
export const EXECUTION_AUTHORIZATION_DESCRIPTOR: ServiceDescriptor = {
  name: EXECUTION_AUTHORIZATION_SERVICE, version: '1.0.0',
  methods: ['describe', 'inspect', 'issue', 'validate', 'revoke', 'acquire-commit', 'release-commit', 'inspect-commit', 'list-commits', 'reconcile-commit'].map(name => ({name,
    ...(name === 'release-commit' ? {cleanup:true} : {}),
    input_schema: `execution.authorization.${name}.input.v1`,
    output_schema: `execution.authorization.${name}.output.v1`,
  })),
};
export function requireExecutionAuthorization(): ServiceRequirement {
  return {name: EXECUTION_AUTHORIZATION_SERVICE, major_version: 1, required: true};
}
/** Issue/revoke require host-attested admission metadata; request JSON, owner
 * labels, authorization_refs and persisted business records cannot create that
 * proof. A child grant can only narrow its parent's exact actions/resources.
 * Never cache validation as effect authority. The host revalidates scope,
 * selected provider/generation, expiry and revocation at each admitted effect.
 * Unsupported providers and unknown outcomes propagate without fallback/retry. */
export function executionAuthorization(host: RuntimeHost) {
  const call = <T>(method: string, params: unknown, options?: RuntimeHostCallOptions) =>
    host.call('host.service.call', {service: EXECUTION_AUTHORIZATION_SERVICE, method, params}, options) as Promise<T>;
  return {
    inspect: (params: ExecutionAuthorizationInspect, options?: RuntimeHostCallOptions) => call<ExecutionAuthorizationGrant>('inspect', params, options),
    describe: (options?: RuntimeHostCallOptions) => call<ExecutionAuthorizationPolicy>('describe', {}, options),
    issue: (params: ExecutionAuthorizationIssue, options?: RuntimeHostCallOptions) => call<ExecutionAuthorizationGrant>('issue', params, options),
    validate: (params: ExecutionAuthorizationValidate, options?: RuntimeHostCallOptions) => call<ExecutionAuthorizationValidation>('validate', params, options),
    revoke: (params: ExecutionAuthorizationRevoke, options?: RuntimeHostCallOptions) => call<ExecutionAuthorizationRevocation>('revoke', params, options),
    /** Requires exact host-attested execution/commit/acquire admission. Unknown
     * response admits no effect; never automatically replay a publication. */
    acquireCommit: async (params: ExecutionAuthorizationCommit, options?: RuntimeHostCallOptions) => {
      const request = structuredClone(params);
      const result = await call<ExecutionAuthorizationCommitGuard>('acquire-commit', request, options);
      await verifyCommitGuard(request, result);
      return result;
    },
    /** Release only after an authoritative terminal receipt. Unknown publication
     * stays fenced. Cleanup retains the captured generation, not a replacement. */
    releaseCommit: async (params: ExecutionAuthorizationCommitRelease, options?: RuntimeHostCallOptions) =>
      verifyCommitReleased(await call<ExecutionAuthorizationCommitReleased>('release-commit', params, {...options,cleanup:true})),
    inspectCommit: async (params: ExecutionAuthorizationCommit, options?: RuntimeHostCallOptions) => {
      const request = structuredClone(params);
      const result = await call<ExecutionAuthorizationCommitStatus>('inspect-commit', request, options);
      await verifyCommitStatus(request, result);
      return result;
    },
    /** Owner-attested maintenance only; discovery does not authorize replay. */
    listCommits: async (options?: RuntimeHostCallOptions) => {
      const result = await call<ExecutionAuthorizationCommitList>('list-commits', {}, options);
      if (!result || !Array.isArray(result.commits)) throw new Error('Execution commit listing omitted intents');
      for (const entry of result.commits) {
        if (entry.outcome !== undefined) throw new Error('Invalid unresolved execution commit intent');
        await verifyCommitStatus(entry.request, entry);
      }
      return result;
    },
    /** Owner-attested recovery after stopping the writer and proving disposition;
     * it does not borrow the expired grant or reconstruct its old principal. */
    reconcileCommit: async (params: ExecutionAuthorizationCommitReconcile, options?: RuntimeHostCallOptions) =>
      verifyCommitReleased(await call<ExecutionAuthorizationCommitReleased>('reconcile-commit', params, options)),
  };
}

/** Informational lookup only; invalid/expired/revoked references reject. It
 * cannot replace validation against the current effect and resource policy. */
export interface ExecutionAuthorizationInspect {
  readonly reference: ExecutionAuthorizationReference;
  readonly consumer: string;
}
export interface ExecutionAuthorizationIntent {
  readonly request_id: string;
  /** Exact execution/delivery run, independent of the stable issuance nonce.
   * Omitted means request_id for existing callers. Renewal never retargets it. */
  readonly run_id?: string;
  readonly kind: 'exact-run' | 'task-lineage';
  readonly authority_service: string;
  readonly task_id: string;
  readonly session_id: string;
  readonly requested_actions: readonly string[];
  /** Persist before the first issue call; retries keep the same finite expiry. */
  readonly expires_at: string;
  readonly max_depth?: number;
  readonly parent?: ExecutionAuthorizationReference;
}
export interface ExecutionLineageRequest {
  /** Host restoration purpose, never permission to widen a grant. */
  readonly action?: string;
  readonly reference?: ExecutionAuthorizationReference;
  readonly creation?: Omit<HostServiceContracts['host.session.create']['params'], 'execution_authorization'>;
  readonly parent?: ExecutionAuthorizationGrant;
  readonly kind: 'exact-run' | 'task-lineage';
  readonly task_id: string;
  readonly session_id?: string;
  /** Required for exact-run; lineage restoration resolves its bound main session. */
  readonly request_id?: string;
}
/** Business facts observed from the selected authority. The host independently
 * verifies session ownership and fills selected authority/model/permission/depth
 * before attesting lineage; providers cannot claim those host-owned facts. */
export interface ExecutionSessionRouteBinding {
  readonly locator: SessionLocatorV1;
  readonly operations: readonly ('send' | 'inspect' | 'cancel')[];
}
export interface ExecutionLineageObservation {
  /** Exact existing target; the host binds it to the grant and resource policy.
   * session_id remains the caller-owned controller, not the target session. */
  readonly route?: ExecutionSessionRouteBinding;
  readonly creation?: Omit<HostServiceContracts['host.session.create']['params'], 'execution_authorization'>;
  readonly parent_task_id: string;
  readonly parent_revision: number;
  readonly task_id: string;
  readonly run_id: string;
  readonly session_id: string;
  readonly revision: number;
  readonly context_digest: string;
}
export const EXECUTION_GRANTS_SERVICE = 'host.execution.authorization';
export function requireExecutionGrants(): ServiceRequirement {
  return {name: EXECUTION_GRANTS_SERVICE, major_version: 1, required: true};
}
/** Product-facing intents, not provider IssueRequest payloads. Host admission
 * resolves immutable identity, authority binding, effective selection and exact
 * resources. A background caller needs a valid parent; it cannot borrow owner
 * authority. No helper retries issuance, revocation or unknown outcomes. */
export function executionGrants(host: RuntimeHost) {
  const call = <T>(operation: string, input: object) => host.call('host.service.call', {
    service: EXECUTION_GRANTS_SERVICE, method: 'call', params: {operation, ...input},
  }) as Promise<T>;
  return {
    describe: () => call<ExecutionAuthorizationPolicy>('describe', {}),
    /** Route only this grant's existing target and run. Never creates a worker
     * or lends the source reference to a transport provider. */
    route: (reference: ExecutionAuthorizationReference, route: SessionRouteRequest) => call<SessionRouteResult>('route-session', {reference, route}),
    /** Revalidate an exact-run grant before routing through this consumer's
     * declared selected service. The reference is not lent to the provider. */
    callService: <T = unknown>(input: {reference: ExecutionAuthorizationReference; service: string; method: string; params: unknown}) => call<T>('call-service', input),
    issue: (input: ExecutionAuthorizationIntent) => call<ExecutionAuthorizationGrant>('issue', input),
    inspect: (reference: ExecutionAuthorizationReference) => call<ExecutionAuthorizationGrant>('inspect', {reference}),
    revoke: (reference: ExecutionAuthorizationReference) => call<ExecutionAuthorizationRevocation>('revoke', {reference}),
  };
}
