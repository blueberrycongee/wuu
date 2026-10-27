import type { RuntimeHost, ServiceDescriptor, ServiceRequirement } from './index.js';

export const SESSION_SHARING_SERVICE = 'session.sharing';
export const SESSION_SHARING_DESCRIPTOR: ServiceDescriptor = {
  name: SESSION_SHARING_SERVICE, version: '1.0.0',
  methods: ['preview', 'create', 'resolve', 'read', 'attachment', 'revoke', 'authorize'].map(name => ({
    name, input_schema: `session.sharing.${name}.input.v1`, output_schema: `session.sharing.${name}.output.v1`,
  })),
};
export function requireSessionSharing(required = false): ServiceRequirement {
  return { name: SESSION_SHARING_SERVICE, major_version: 1, required };
}
/** A namespace-qualified identity is a claim, not authentication. Only the host
 * copies transport-verified identities into these provider requests. */
export interface SharePrincipal { readonly kind: 'user' | 'device' | 'agent'; readonly issuer: string; readonly tenant: string; readonly subject: string }
/** Every selector intersects the [after,before) time range. Empty lists disclose
 * nothing. IDs cannot select reasoning, arguments, raw input or host metadata. */
export interface ShareSelection {
  readonly message_ids?: readonly string[];
  readonly tool_result_ids?: readonly string[];
  readonly attachment_ids?: readonly string[];
  readonly after?: string;
  readonly before?: string;
}
export interface ShareSourceItem {
  readonly id: string;
  readonly turn_id?: string;
  readonly kind: 'message' | 'tool_result' | 'attachment';
  readonly role?: 'user' | 'assistant';
  readonly text?: string;
  readonly at: string;
  readonly media_type?: string;
  /** Base64; only explicit attachment items have bytes. */
  readonly data?: string;
}
export interface ShareSource { readonly session_id: string; readonly items: readonly ShareSourceItem[] }
export interface SharedAttachment { readonly id: string; readonly sha256: string; readonly media_type: string; readonly bytes: number }
export interface SharedItem {
  readonly id: string; readonly turn_id?: string; readonly kind: ShareSourceItem['kind'];
  readonly role?: 'user' | 'assistant'; readonly text?: string; readonly at: string;
  readonly attachment?: SharedAttachment;
}
/** Render text inertly. Embedded URLs never authorize fetching; no raw artifact
 * or filesystem address is returned. Download bytes using attachment(). */
export interface SharedDocument { readonly version: 1; readonly session_id: string; readonly items: readonly SharedItem[] }
export interface SharePreviewRequest { readonly source: ShareSource; readonly selection: ShareSelection }
export interface SharePreview { readonly document: SharedDocument; readonly review_digest: string; readonly warning: string }
export interface ShareCreate extends SharePreviewRequest {
  /** Stable across lost acknowledgements/restarts; scoped to actor. Changed intent conflicts. */
  readonly operation_id: string;
  readonly actor: SharePrincipal; readonly mode: 'snapshot' | 'live'; readonly recipients: readonly SharePrincipal[];
  readonly expires_at: string; readonly reviewed: boolean; readonly review_digest: string;
  /** Live mode explicitly consents to subsequent edits of the selected IDs, not
   * future messages. Unselected message/tool/attachment IDs remain invisible. */
  readonly live_updates?: boolean;
}
export interface SessionShare { readonly id: string; readonly session_id: string; readonly mode: 'snapshot' | 'live'; readonly expires_at: string }
export interface ShareRead { readonly actor: SharePrincipal; readonly session_id: string; readonly share_id: string; readonly source?: ShareSource }
export interface ShareReadPlan { readonly mode: SessionShare['mode']; readonly selection: ShareSelection }
export interface ShareAuthorization {
  readonly principal: SharePrincipal; readonly owner: boolean; readonly action: string;
  readonly resources: readonly { readonly kind: string; readonly id?: string }[];
}
export interface ShareDecision { readonly allow: boolean; readonly reason?: string }

/** Provider-to-provider API. UI/remote callers instead invoke authenticated
 * session/share/* app-server methods, which replace actor claims with the
 * verified principal and obtain source from the authoritative session. A missing
 * provider is an error, never permission to fall back to raw thread history. */
export function sessionSharing(host: RuntimeHost) {
  const call = async <T>(method: string, params: unknown): Promise<T> =>
    await host.call('host.service.call', { service: SESSION_SHARING_SERVICE, method, params }) as T;
  return {
    preview: (input: SharePreviewRequest) => call<SharePreview>('preview', input),
    create: (input: ShareCreate) => call<SessionShare>('create', input),
    resolve: (input: ShareRead) => call<ShareReadPlan>('resolve', input),
    read: (input: ShareRead) => call<SharedDocument>('read', input),
    attachment: (input: ShareRead & { readonly attachment_id: string }) => call<SharedAttachment & { readonly data: string }>('attachment', input),
    revoke: (input: Omit<ShareRead, 'source'>) => call<Record<string, never>>('revoke', input),
    authorize: (input: ShareAuthorization) => call<ShareDecision>('authorize', input),
  };
}
