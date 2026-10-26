import { PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
import { settleOnAbort } from "./activation";
import type { ApplicationCheckpointV1, ApplicationProviderDefinition, ApplicationProviderInstanceV1, ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";

const MAX_RETIREMENT_PREPARATION_MS = 120;

/** Capture only presentation for this still-current binding; never retain its business scopes. */
export interface ApplicationRetirementPreparation { readonly binding: number; readonly signal: AbortSignal }

export interface ApplicationProviderBinding { readonly pluginId: string; readonly generation: string; readonly id: string }
export interface ApplicationProviderRecord extends ApplicationProviderBinding {
  readonly definition: ApplicationProviderDefinition;
  readonly graphGeneration?: string;
  instance?: ApplicationProviderInstanceV1;
  lifetime?: AbortController;
  removed?: boolean;
  removal?: Promise<void>;
  readonly preparation: AbortController;
}

/** One committed graph binding; this registry owns transitions, never application policy. */
export class ApplicationProviders {
  private records = new Set<ApplicationProviderRecord>();
  private checkpoint?: ApplicationCheckpointV1;
  private uncertainWriter?: ApplicationProviderRecord;
  private queue: Promise<unknown> = Promise.resolve();
  private listeners = new Set<() => void>();
  private revision = 0;
  private bindingRevision = 0;
  private presentationRevision = 0;
  private retirementPreparationListeners = new Set<(preparation: ApplicationRetirementPreparation) => void | Promise<void>>();
  private retirementPreparation?: { cancel(): void };
  private retirement?: { readonly phase: { readonly binding: number }; readonly signal: AbortSignal };
  current?: ApplicationProviderRecord;
  failure?: string;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };
  subscribeRetirementPreparation = (listener: (preparation: ApplicationRetirementPreparation) => void | Promise<void>): (() => void) => {
    this.retirementPreparationListeners.add(listener); return () => this.retirementPreparationListeners.delete(listener);
  };
  getRevision = (): number => this.revision;
  getBindingRevision = (): number => this.bindingRevision;
  /** A writer gap retires authority immediately, but is not a new presentation. */
  getPresentationRevision = (): number => this.presentationRevision;
  getRetirement = (): { readonly binding: number } | undefined => this.retirement?.signal.aborted ? undefined : this.retirement?.phase;
  /** Give presentation a bounded capture opportunity before the first consumer retires.
   * Capture failure cannot block retirement. Cancellation invalidates late capture;
   * success leaves its pixels alone. Intent identity remains the presentation owner's policy. */
  async prepareRetirement(signal: AbortSignal): Promise<() => void> {
    const source = this.current, binding = this.bindingRevision;
    if (!source || source.removed || signal.aborted) return () => {};
    const previous = this.retirementPreparation;
    const controller = new AbortController();
    let ready!: () => void;
    const waiting = new Promise<void>(resolve => { ready = resolve; });
    const preparation = { cancel: () => { controller.abort(); ready(); } };
    this.retirementPreparation = preparation;
    signal.addEventListener("abort", preparation.cancel, { once: true });
    const timeout = setTimeout(preparation.cancel, MAX_RETIREMENT_PREPARATION_MS);
    // Publish before notifying cancellation: an abort listener may start a newer capture.
    previous?.cancel();
    const request = { binding, signal: controller.signal };
    // Observe all failures, including late rejections after timeout or supersession.
    void Promise.allSettled([...this.retirementPreparationListeners].map(listener => Promise.resolve().then(() => {
      if (!controller.signal.aborted) return listener(request);
    }))).then(ready);
    await waiting;
    clearTimeout(timeout);
    signal.removeEventListener("abort", preparation.cancel);
    // An old completion cannot clear a newer capture, even if the same source returns.
    if (this.retirementPreparation !== preparation) return () => {};
    this.retirementPreparation = undefined;
    if (signal.aborted || source.removed || this.current !== source || this.bindingRevision !== binding) return () => {};
    return this.beginRetirement(signal);
  }
  /** Prepared replacement is about to drain consumers; this does not stop or retain a writer. */
  beginRetirement(signal: AbortSignal): () => void {
    if (!this.current || signal.aborted) return () => {};
    const retirement = { phase: { binding: this.bindingRevision }, signal };
    const finish = () => {
      signal.removeEventListener("abort", finish);
      if (this.retirement !== retirement) return;
      this.retirement = undefined;
      this.changed();
    };
    this.retirement = retirement;
    signal.addEventListener("abort", finish, { once: true });
    this.changed();
    return finish;
  }
  private changed(bindingChanged = false): void {
    this.revision++;
    if (bindingChanged) { this.bindingRevision++; this.retirementPreparation?.cancel(); }
    if (this.current) this.presentationRevision = this.bindingRevision;
    for (const listener of this.listeners) listener();
  }
  reportFailure(message: string): void {
    const effective = this.current ? `${this.current.pluginId}/${this.current.id}@${this.current.generation} (graph ${this.current.graphGeneration ?? "local"})` : "none";
    this.failure = `${message}; effective application provider: ${effective}`;
    this.changed();
  }
  register(binding: ApplicationProviderBinding & { readonly graphGeneration?: string }, definition: ApplicationProviderDefinition): ApplicationProviderRecord {
    const record = { ...binding, definition, preparation: new AbortController() }; this.records.add(record); return record;
  }
  find(binding: ApplicationProviderBinding): ApplicationProviderRecord | undefined {
    return [...this.records].reverse().find(record => !record.removed && record.pluginId === binding.pluginId && record.generation === binding.generation && record.id === binding.id);
  }
  createScope(signal: AbortSignal): ApplicationServiceV1 {
    const record = this.current;
    if (!record?.instance || !record.lifetime || record.removed) throw new Error("No application provider is active");
    const scope = new AbortController();
    const sources = [signal, record.lifetime.signal];
    const abort = () => { scope.abort(sources.find(source => source.aborted)?.reason); for (const source of sources) source.removeEventListener("abort", abort); };
    for (const source of sources) source.addEventListener("abort", abort, { once: true });
    if (sources.some(source => source.aborted)) abort();
    try { return record.instance.createScope(scope.signal); }
    catch (error) { abort(); throw error; }
  }
  private enqueue(operation: () => Promise<void>): Promise<void> {
    const next = this.queue.then(operation).catch(error => {
      const effective = this.current ? `${this.current.pluginId}/${this.current.id}@${this.current.generation}` : "none";
      this.failure = `${String(error instanceof Error ? error.message : error)}; effective application provider: ${effective}`;
      this.changed(); throw error;
    });
    this.queue = next.catch(() => undefined); return next;
  }
  prepare(record: ApplicationProviderRecord): Promise<void> { return this.enqueue(() => this.prepareRecord(record)); }
  private async prepareRecord(record: ApplicationProviderRecord): Promise<void> {
    if (record.removed) throw new Error("Selected application provider is retired");
    if (!record.instance) {
      const prepared = Promise.resolve().then(() => record.definition.prepare());
      void prepared.then(async instance => {
        if (!record.preparation.signal.aborted) return;
        try { await instance.dispose(); }
        catch (error) { this.reportFailure(`Late application preparation cleanup failed: ${String(error)}`); }
      }, () => undefined);
      record.instance = await settleOnAbort(() => prepared, record.preparation.signal);
    }
    if (record.removed) throw new Error("Selected application provider is retired");
  }
  select(record: ApplicationProviderRecord | undefined): Promise<void> {
    return this.enqueue(() => this.transition(record));
  }
  private async transition(record: ApplicationProviderRecord | undefined): Promise<void> {
    if (record === this.current) return;
    if (this.uncertainWriter) throw new Error(`Application writer cleanup is uncertain: ${this.uncertainWriter.pluginId}@${this.uncertainWriter.generation}; dispose or restart before admitting another writer`);
    if (record?.removed) throw new Error("Selected application provider is retired");
    // Preparation cannot touch the stopped-writer checkpoint or displace the active instance.
    if (record) await this.prepareRecord(record);
    const previous = this.current;
    if (previous?.instance) {
      this.current = undefined;
      previous.lifetime?.abort(new PluginLifecycleCancellationError(new Error("Application provider is switching")));
      this.changed(true);
      try { this.checkpoint = await previous.instance.deactivate(); }
      catch (error) { this.uncertainWriter = previous; throw new Error(`Previous application writer stop is uncertain: ${String(error instanceof Error ? error.message : error)}`, { cause: error }); }
    }
    if (!record?.instance) { this.failure = undefined; this.changed(); return; }
    try {
      if (record.removed) throw new Error("Selected application provider is retired");
      await record.instance.activate(this.checkpoint);
      if (record.removed) throw new Error("Selected application provider is retired");
      record.lifetime = new AbortController();
      this.current = record;
      this.failure = undefined;
    } catch (error) {
      // External side effects are not rolled back. An uncertain writer must not overlap recovery.
      try { await record.instance.deactivate(); }
      catch (cleanupError) {
        this.uncertainWriter = record;
        throw new AggregateError([error, cleanupError], `${String(error instanceof Error ? error.message : error)}; ${String(cleanupError instanceof Error ? cleanupError.message : cleanupError)}; candidate writer stop is uncertain`);
      }
      if (previous?.graphGeneration && record.graphGeneration && previous.graphGeneration !== record.graphGeneration) {
        throw new Error(`Renderer activation failed after Go graph ${record.graphGeneration} committed: ${String(error instanceof Error ? error.message : error)}; previous writer remains stopped; repair or restore the composition to resume`, { cause: error });
      }
      if (previous?.instance && !previous.removed) {
        try {
          await previous.instance.activate(this.checkpoint);
          previous.lifetime = new AbortController();
          this.current = previous;
        } catch (recoveryError) {
          this.uncertainWriter = previous;
          throw new AggregateError([error, recoveryError], `Application activation and recovery failed: ${String(error)}; ${String(recoveryError)}`);
        }
      }
      throw error;
    } finally { this.changed(true); }
  }
  remove(record: ApplicationProviderRecord): Promise<void> {
    if (record.removal) return record.removal;
    // Fence admissions immediately, then drain preparation/activation on the same transition queue.
    record.removed = true;
    if (this.current === record) this.retirementPreparation?.cancel();
    record.preparation.abort(new Error("Selected application provider is retired"));
    record.removal = this.enqueue(async () => {
      let failure: unknown;
      if (this.current === record) {
        try { await this.transition(undefined); } catch (error) { failure = error; }
      }
      try { await record.instance?.dispose(); if (this.uncertainWriter === record) this.uncertainWriter = undefined; }
      catch (error) { failure = failure ? new AggregateError([failure, error], "Application stop and disposal failed") : error; }
      this.records.delete(record);
      if (failure) throw failure;
    });
    return record.removal;
  }
}
