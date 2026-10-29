import { randomUUID } from 'node:crypto';
import type { RuntimeHost } from '@wuu/plugin-sdk';
import { CalendarWriteError, operationDigest, writeIntent, type CalendarWriter, type PreparedWrite, type WriteProof, type WriteTarget } from './google-writer.js';
type OperationStatus = 'proposed' | 'confirmed' | 'running' | 'uncertain' | 'completed' | 'failed' | 'cancelled';
interface RecordState {
  version: 1; proposal: PreparedWrite; intent_hash: string; revision: number; status: OperationStatus; created_at: string;
  confirmation?: { expected_revision: number; preview_digest: string; confirmed_at: string; channel: 'plugin.client.request' };
  attempt?: { id: string; owner: string; started_at: string }; result?: WriteProof; error_code?: string;
  reconciliation_required: boolean; cancel_requested?: boolean;
}
export interface OperationLedgerOptions {
  host: RuntimeHost; space: string; owner: string; signal: AbortSignal; writer?: CalendarWriter; clock: () => Date;
  guard: () => Promise<void>;
  beforeMutation: (proposal: PreparedWrite) => Promise<void>;
  validateTarget: (proposal: { task_id: string; requirements_revision: number } & WriteTarget) => Promise<void>;
}
/** Durable business-operation identity. Once an attempt may have reached the
 * provider, recovery only reads; a new mutation is never inferred from silence. */
export class CalendarOperations {
  private prefix: string;
  private active = new Map<string, { abort: AbortController; promise: Promise<unknown> }>();
  constructor(private options: OperationLedgerOptions) { this.prefix = `calendar.operations.v1.${operationDigest(options.space).slice(0, 32)}.`; }
  private key(id: string): string { if (!/^[A-Za-z0-9._-]{1,128}$/.test(id)) throw new Error('invalid operation_id'); return `${this.prefix}${operationDigest(id)}`; }
  private view(state: RecordState) { return { ...structuredClone(state.proposal), revision: state.revision, status: state.status, created_at: state.created_at, confirmation: state.confirmation, result: state.result, error_code: state.error_code, reconciliation_required: state.reconciliation_required, cancel_requested: state.cancel_requested === true }; }
  private async read(id: string): Promise<{ raw: string | null; state?: RecordState }> {
    const { value } = await this.options.host.call('host.storage.get', { scope: 'user', key: this.key(id) });
    if (!value) return { raw: value };
    const state = JSON.parse(value) as RecordState;
    if (state.version !== 1 || state.proposal?.user_space_id !== this.options.space || state.proposal.operation_id !== id || !Number.isInteger(state.revision)) throw new Error('invalid operation storage scope');
    return { raw: value, state };
  }
  private async update(id: string, change: (current?: RecordState) => RecordState): Promise<RecordState> {
    for (let attempt = 0; attempt < 16; attempt++) {
      await this.options.guard();
      const { raw, state } = await this.read(id), next = change(state); if (next === state) return next;
      next.revision = (state?.revision ?? 0) + 1;
      const result = await this.options.host.call('host.storage.compare_exchange', { scope: 'user', key: this.key(id), expected: raw, value: JSON.stringify(next) });
      if (result.swapped) return next;
    }
    throw new Error('operation revision conflict');
  }
  async activate(): Promise<void> {
    for (const operation of await this.list()) if (operation.status === 'running') await this.update(operation.operation_id, current => current!.status === 'running' && current!.attempt?.owner !== this.options.owner ? { ...current!, status: 'uncertain', error_code: 'runtime_interrupted', reconciliation_required: true } : current!);
  }
  stopOutstanding(): void { for (const value of this.active.values()) value.abort.abort(); }
  async list() {
    await this.options.guard(); const { keys } = await this.options.host.call('host.storage.keys', { scope: 'user' }); const result = [];
    for (const key of keys.filter(key => key.startsWith(this.prefix))) {
      const { value } = await this.options.host.call('host.storage.get', { scope: 'user', key }); if (!value) continue;
      const parsed = JSON.parse(value) as RecordState;
      const { state } = await this.read(parsed.proposal?.operation_id); if (state) result.push(this.view(state));
    }
    return result.sort((a, b) => b.created_at.localeCompare(a.created_at));
  }
  async get(id: string) { await this.options.guard(); const { state } = await this.read(id); if (!state) throw new Error('operation not found'); return this.view(state); }
  async propose(raw: unknown, target: WriteTarget, signal: AbortSignal) {
    if (!this.options.writer) throw new Error('calendar writes unconnected: authorization_not_configured');
    const intent = writeIntent(raw); await this.options.guard(); await this.options.validateTarget({ ...target, task_id: intent.task_id, requirements_revision: intent.requirements_revision });
    const hash = operationDigest({ intent, target }), existing = (await this.read(intent.operation_id)).state;
    if (existing) { if (existing.intent_hash !== hash) throw new Error('operation_id_conflict'); return this.view(existing); }
    const proposal = await this.options.writer.prepare(intent, target, signal);
    const state = await this.update(intent.operation_id, current => { if (current) { if (current.intent_hash !== hash) throw new Error('operation_id_conflict'); return current; } return { version: 1, proposal, intent_hash: hash, revision: 0, status: 'proposed', created_at: this.options.clock().toISOString(), reconciliation_required: false }; });
    return this.view(state);
  }
  /** Call only from the user-client capability, after it displays this digest's
   * immutable proposal. No tool or public service route delegates here. */
  async confirm(id: string, expectedRevision: number, digest: string, signal: AbortSignal) {
    await this.options.guard(); const original = (await this.read(id)).state; if (!original) throw new Error('operation not found');
    if (digest !== original.proposal.preview_digest) throw new Error('confirmation preview mismatch');
    if (original.confirmation && original.confirmation.expected_revision === expectedRevision && original.confirmation.preview_digest === digest) {
      if (original.status === 'confirmed') return this.run(id, signal);
      return this.view(original);
    }
    await this.options.validateTarget(original.proposal);
    const confirmed = await this.update(id, current => {
      if (current?.confirmation?.expected_revision === expectedRevision && current.confirmation.preview_digest === digest) return current;
      if (!current || current.status !== 'proposed' || current.revision !== expectedRevision) throw new Error('operation confirmation revision conflict');
      return { ...current, status: 'confirmed', confirmation: { expected_revision: expectedRevision, preview_digest: digest, confirmed_at: this.options.clock().toISOString(), channel: 'plugin.client.request' } };
    });
    if (confirmed.status !== 'confirmed') return this.view(confirmed);
    return this.run(id, signal);
  }
  private async run(id: string, signal: AbortSignal): Promise<unknown> {
    const existing = this.active.get(id); if (existing) return existing.promise;
    const abort = new AbortController(); const combined = AbortSignal.any([signal, this.options.signal, abort.signal]);
    const promise = this.perform(id, combined); this.active.set(id, { abort, promise });
    try { return await promise; } finally { if (this.active.get(id)?.promise === promise) this.active.delete(id); }
  }
  private async perform(id: string, signal: AbortSignal): Promise<unknown> {
    const attempt = randomUUID();
    const running = await this.update(id, current => current!.status !== 'confirmed' ? current! : { ...current!, status: 'running', attempt: { id: attempt, owner: this.options.owner, started_at: this.options.clock().toISOString() }, reconciliation_required: true });
    if (running.status !== 'running' || running.attempt?.id !== attempt) return this.view(running);
    let proof: WriteProof | undefined, failure: CalendarWriteError | undefined;
    try {
      if (!this.options.writer) throw new CalendarWriteError('authorization_not_configured');
      proof = await this.options.writer.execute(running.proposal, async () => {
        await this.options.guard(); const state = (await this.read(id)).state;
        if (state?.status !== 'running' || state.attempt?.id !== attempt || state.cancel_requested) throw new Error('operation cancelled or generation replaced');
        await this.options.validateTarget(running.proposal); await this.options.beforeMutation(running.proposal);
      }, signal);
    } catch (error) { failure = error instanceof CalendarWriteError ? error : new CalendarWriteError('write_outcome_unknown', 'uncertain'); }
    if (this.options.signal.aborted) return this.view({ ...running, status: 'uncertain', error_code: 'runtime_interrupted', reconciliation_required: true });
    const saved = await this.update(id, current => {
      if (current!.attempt?.id !== attempt || current!.status !== 'running') return current!;
      if (proof) return { ...current!, status: 'completed', result: proof, error_code: undefined, reconciliation_required: false };
      return { ...current!, status: failure!.outcome === 'uncertain' ? 'uncertain' : 'failed', error_code: failure!.code, reconciliation_required: failure!.outcome === 'uncertain' };
    });
    return this.view(saved);
  }
  async reconcile(id: string, signal: AbortSignal) {
    await this.options.guard(); const current = (await this.read(id)).state; if (!current) throw new Error('operation not found');
    if (!['uncertain', 'running'].includes(current.status) || this.active.has(id)) return this.view(current);
    if (!this.options.writer) throw new Error('calendar writes unconnected: authorization_not_configured');
    let proof: WriteProof | null = null, errorCode = 'remote_result_unresolved';
    try { proof = await this.options.writer.reconcile(current.proposal, signal); } catch { errorCode = 'reconciliation_unavailable'; }
    return this.view(await this.update(id, state => {
      if (!['uncertain', 'running'].includes(state!.status) || state!.attempt?.id !== current.attempt?.id) return state!;
      return proof ? { ...state!, status: 'completed', result: proof, reconciliation_required: false, error_code: undefined } : { ...state!, status: 'uncertain', reconciliation_required: true, error_code: errorCode };
    }));
  }
  async cancel(id: string, expectedRevision: number) {
    const state = await this.update(id, current => {
      if (!current || current.revision !== expectedRevision) throw new Error('operation cancellation revision conflict');
      if (!['proposed', 'confirmed', 'running', 'uncertain'].includes(current.status)) throw new Error('operation cannot be cancelled; remote deletion is unsupported');
      const mayHaveWritten = ['running', 'uncertain'].includes(current.status);
      return { ...current, cancel_requested: true, status: mayHaveWritten ? 'uncertain' : 'cancelled', reconciliation_required: mayHaveWritten, error_code: mayHaveWritten ? 'cancellation_requires_reconciliation' : undefined };
    });
    this.active.get(id)?.abort.abort(); return this.view(state);
  }
}
