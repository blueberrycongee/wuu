import type { RuntimeHost, RuntimeHostCallOptions, HostServiceContracts, ServiceDescriptor, ServiceRequirement } from './index.js';
import type { PrincipalIdentity } from './identity.js';
import type { ResourceReference } from './resource-policy.js';

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
  readonly default_lifetime_seconds: number;
  readonly max_lifetime_seconds: number;
  readonly authority_kinds: readonly string[];
  readonly scope_kinds: readonly string[];
}
export const EXECUTION_AUTHORIZATION_SERVICE = 'execution.authorization';
export const EXECUTION_AUTHORIZATION_DESCRIPTOR: ServiceDescriptor = {
  name: EXECUTION_AUTHORIZATION_SERVICE, version: '1.0.0',
  methods: ['describe', 'inspect', 'issue', 'validate', 'revoke'].map(name => ({name,
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
export interface ExecutionLineageObservation {
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
    /** Revalidate an exact-run grant before routing through this consumer's
     * declared selected service. The reference is not lent to the provider. */
    callService: <T = unknown>(input: {reference: ExecutionAuthorizationReference; service: string; method: string; params: unknown}) => call<T>('call-service', input),
    issue: (input: ExecutionAuthorizationIntent) => call<ExecutionAuthorizationGrant>('issue', input),
    inspect: (reference: ExecutionAuthorizationReference) => call<ExecutionAuthorizationGrant>('inspect', {reference}),
    revoke: (reference: ExecutionAuthorizationReference) => call<ExecutionAuthorizationRevocation>('revoke', {reference}),
  };
}
