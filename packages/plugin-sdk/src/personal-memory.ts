import type { RuntimeHost, RuntimeHostCallOptions, ServiceRequirement } from './index.js';

/** Additive operations of the declared host.personal@1 service. The caller must
 * own the host-resolved session and be the selected Personal task authority.
 * Session IDs, source refs and record JSON are selectors, never authorization.
 * No operation grants a worker ordinary read access to the main conversation. */
export interface PersonalMemoryEvidence { readonly ref: string; readonly occurred_at: string; readonly content?: string; }
export interface PersonalMemoryWrite {
  /** Stable exact invocation identity, persisted before authority acquisition.
   * A changed intent must not reuse this ID; unknown results need reconciliation. */
  readonly id: string;
  readonly key: string;
  readonly expected: string | null;
  readonly value: string;
}
export interface PersonalMemoryWriteResult { readonly swapped: boolean; readonly value: string | null; }
export interface PersonalMemoryRecords { readonly records: Readonly<Record<string, string>>; }
export function requirePersonalMemory(): ServiceRequirement { return { name: 'host.personal', major_version: 1, required: true }; }
/** Records in this authoritative journal override legacy plugin-storage rows by
 * stable ID, including tombstones. All new writes use commit; legacy storage is
 * read-only compatibility input, never a replica to update or a fallback write.
 * Source and commit revalidate the exact current run and host-bound user source.
 * Only main evidence review returns source text. Unknown commits retain durable
 * guards until their terminal receipt is established; no automatic retry/renewal.
 */
export function personalMemory(host: RuntimeHost) {
  async function call<T>(operation: string, input: unknown, session_id?: string, options?: RuntimeHostCallOptions): Promise<T> {
    const raw = await host.call('host.service.call', { service: 'host.personal', method: 'call', params: { operation, input, ...(session_id ? { session_id } : {}) } }, options) as {ok?:boolean;value?:T};
    if (raw?.ok !== true || !('value' in raw)) throw new Error('Invalid Personal memory host response');
    return raw.value as T;
  }
  return {
    records: (sessionID: string, options?: RuntimeHostCallOptions) => call<PersonalMemoryRecords>('memory.read', {}, sessionID, options),
    operation: (sessionID: string, id: string, options?: RuntimeHostCallOptions) => call<{write: PersonalMemoryWrite} | null>('memory.operation', {id}, sessionID, options),
    source: (sessionID: string, ref: string, options?: RuntimeHostCallOptions) => call<PersonalMemoryEvidence>('memory.source', {ref}, sessionID, options),
    commit: (sessionID: string, input: PersonalMemoryWrite, options?: RuntimeHostCallOptions) => call<PersonalMemoryWriteResult>('memory.commit', input, sessionID, options),
    /** Owner-client maintenance only; unavailable to model/worker admission. */
    operations: (options?: RuntimeHostCallOptions) => call<{operations: readonly unknown[]}>('memory.operations', {}, undefined, options),
    /** Drains this host's writers, requires single-host selected providers and
     * explicit stopped-writer evidence. Never guesses an unknown commit result. */
    reconcile: (input: {id:string;writers_stopped:true;reason:string}, options?: RuntimeHostCallOptions) => call<unknown>('memory.reconcile', input, undefined, options),
  };
}
