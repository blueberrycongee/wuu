import type { ApplicationProjectsV1, ApplicationProjectViewV1, ApplicationProjectCallOptionsV1 } from "../../../../packages/plugin-sdk/src/ui-projects";
import type { ServerEvent, Thread } from "../../shared/protocol";
import { subscribeServerEvents } from "../ServerEvents";

type Entry = { view: ApplicationProjectViewV1; version: number; listeners: Set<() => void> };
function immutable<T>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => {
    if (!item || typeof item !== "object" || Object.isFrozen(item)) return;
    Object.values(item).forEach(freeze); Object.freeze(item);
  };
  freeze(copy); return copy;
}

/** A shared UI observation of the core ledger, independent of any mounted shell. */
export class ApplicationProjects implements ApplicationProjectsV1 {
  private readonly entries = new Map<string, Entry>();
  private contextKey?: string;
  private enabled = false;
  private disposed = false;
  private disconnect?: () => void;
  constructor(private readonly host: {
    getSource: () => { contextKey: string; enabled: boolean };
    updateThread: (thread: Thread) => void;
  }) {}

  private syncSource(): boolean {
    const source = this.host.getSource();
    const enabled = source.enabled && typeof window.wuu.projectWork === "function" && !window.wuu.unsupportedMethods?.includes("projectWork");
    if (this.contextKey === source.contextKey && this.enabled === enabled) return false;
    this.contextKey = source.contextKey; this.enabled = enabled;
    for (const [projectId, entry] of this.entries) {
      entry.version++;
      entry.view = Object.freeze({ projectId, enabled, loading: false, error: "" });
    }
    return true;
  }
  private entry(projectId: string): Entry {
    if (this.disposed) throw new Error("Project application disposed");
    this.syncSource();
    let entry = this.entries.get(projectId);
    if (!entry) {
      entry = { view: Object.freeze({ projectId, enabled: this.enabled, loading: false, error: "" }), version: 0, listeners: new Set() };
      this.entries.set(projectId, entry);
    }
    return entry;
  }
  private publish(entry: Entry, changes: Partial<ApplicationProjectViewV1>): void {
    entry.view = immutable({ ...entry.view, ...changes });
    for (const listener of entry.listeners) listener();
  }
  getSnapshot(projectId: string): ApplicationProjectViewV1 { return this.entry(projectId).view; }
  subscribe(projectId: string, listener: () => void): () => void {
    const entry = this.entry(projectId);
    const firstObserver = entry.listeners.size === 0;
    entry.listeners.add(listener);
    this.disconnect ??= subscribeServerEvents(event => this.receive(event));
    // Events are not retained while no view observes this project.
    if (this.enabled && firstObserver && !entry.view.loading) void this.refresh(projectId).catch(() => {});
    let active = true;
    return () => {
      if (!active) return; active = false; entry.listeners.delete(listener);
      if (entry.listeners.size === 0) { entry.version++; this.publish(entry, { loading: false }); }
      if (![...this.entries.values()].some(item => item.listeners.size)) { this.disconnect?.(); this.disconnect = undefined; }
    };
  }
  private receive(event: ServerEvent): void {
    if (this.disposed || event.kind !== "notification") return;
    const { method, params } = event.message;
    const payload = params as { project_id?: string; thread_id?: string; item?: { type?: string } } | undefined;
    for (const [projectId, entry] of this.entries) {
      if (!entry.listeners.size || !this.enabled) continue;
      const ledger = method === "project/work/updated" && payload?.project_id === projectId;
      const session = ["turn/started", "turn/completed", "item/completed"].includes(method)
        && (method !== "item/completed" || payload?.item?.type === "user_message")
        && (payload?.thread_id === projectId || entry.view.snapshot?.works.some(work => [work.lead_id, work.executor_id].includes(payload?.thread_id)));
      if (ledger || session) void this.refresh(projectId).catch(() => {});
    }
  }
  notifyChanged(): void {
    if (this.disposed || !this.syncSource()) return;
    for (const [projectId, entry] of this.entries) {
      for (const listener of entry.listeners) listener();
      if (this.enabled && entry.listeners.size) void this.refresh(projectId).catch(() => {});
    }
  }
  private begin(options?: ApplicationProjectCallOptionsV1): string {
    if (this.disposed) throw new Error("Project application disposed");
    options?.signal?.throwIfAborted(); this.syncSource();
    if (!this.enabled) throw new Error("Project work is unavailable");
    return this.contextKey!;
  }
  private finish(source: string, options?: ApplicationProjectCallOptionsV1): void {
    if (this.disposed) throw new Error("Project application disposed");
    options?.signal?.throwIfAborted();
    if (source !== this.host.getSource().contextKey || !this.host.getSource().enabled) throw new Error("Project source changed during the operation");
  }
  async refresh(projectId: string, options?: ApplicationProjectCallOptionsV1): Promise<void> {
    const source = this.begin(options); const entry = this.entry(projectId); const version = ++entry.version;
    this.publish(entry, { loading: true, error: "" });
    try {
      const result = await window.wuu.projectWork!({ project_id: projectId, operation: "list" });
      this.finish(source, options);
      if (entry.version !== version) return;
      if (!("works" in result) || result.project_id !== projectId) throw new Error("Project work response identity mismatch");
      this.publish(entry, { snapshot: result, loading: false });
    } catch (error) {
      if (!this.disposed && source === this.host.getSource().contextKey && entry.version === version) {
        this.publish(entry, { loading: false, error: options?.signal?.aborted ? "" : error instanceof Error ? error.message : String(error) });
      }
      throw error;
    }
  }
  async control(input: Parameters<ApplicationProjectsV1["control"]>[0], options?: ApplicationProjectCallOptionsV1): Promise<void> {
    const source = this.begin(options); const entry = this.entry(input.projectId);
    const work = entry.view.snapshot?.works.find(work => work.id === input.workId);
    if (!work || work.revision !== input.revision) {
      const error = new Error("Project work revision changed; refresh before acting");
      this.publish(entry, { error: error.message });
      throw error;
    }
    if (entry.view.busyWorkId) {
      const error = new Error("A project work change is already pending");
      this.publish(entry, { error: error.message });
      throw error;
    }
    this.publish(entry, { busyWorkId: work.id, error: "" });
    try {
      await window.wuu.projectWork!({ project_id: input.projectId, request_id: crypto.randomUUID(), operation: input.operation,
        work_id: work.id, revision: work.revision, brief: work.brief, acceptance: work.acceptance, authority: work.authority, source_refs: work.source_refs });
      this.finish(source, options); await this.refresh(input.projectId, options);
    } catch (error) {
      if (!this.disposed && source === this.host.getSource().contextKey) this.publish(entry, { error: error instanceof Error ? error.message : String(error) });
      throw error;
    } finally {
      if (!this.disposed && source === this.host.getSource().contextKey) this.publish(entry, { busyWorkId: undefined });
    }
  }
  async releaseSession(input: Parameters<ApplicationProjectsV1["releaseSession"]>[0], options?: ApplicationProjectCallOptionsV1): Promise<void> {
    const source = this.begin(options);
    if (!window.wuu.projectSession || window.wuu.unsupportedMethods?.includes("projectSession")) throw new Error("Project membership is unavailable");
    const result = await window.wuu.projectSession({ action: "release", project_id: input.projectId, session_id: input.threadId });
    this.finish(source, options); this.host.updateThread(result.thread);
  }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true; this.disconnect?.(); this.disconnect = undefined;
    for (const entry of this.entries.values()) { entry.version++; entry.listeners.clear(); }
    this.entries.clear();
  }
}
