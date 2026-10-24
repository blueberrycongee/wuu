import type {
  EnvironmentReviewTargetV1, EnvironmentChangesV1, EnvironmentFileDiffV1,
  ApplicationEnvironmentV1, EnvironmentCallOptionsV1, EnvironmentCommitResultV1,
  EnvironmentPullRequestResultV1, EnvironmentSnapshotV1,
} from "../../../../packages/plugin-sdk/src/ui-environment";
import type { GitStatusResult, ServerEvent } from "../../shared/protocol";
import { hostSupports } from "../HostCapabilities";
import { translateCurrent } from "../i18n";
import { toastErrorMessage } from "../Toast";

type RefreshRequest = { options?: EnvironmentCallOptionsV1; detach(): void };
type Source = { contextKey: string; root?: string; status?: GitStatusResult; runningThreadKey?: string };
const busyLifecycleMethods = new Set(["turn/started", "turn/completed", "turn/error", "thread/started", "thread/resumed"]);
function immutable<T>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => {
    if (!item || typeof item !== "object" || Object.isFrozen(item)) return;
    Object.values(item).forEach(freeze);
    Object.freeze(item);
  };
  freeze(copy);
  return copy;
}

/** Owns Git transport and refresh lifetime, without panel or DOM state. */
export class ApplicationEnvironment implements ApplicationEnvironmentV1 {
  private disposed = false;
  private busyKey?: string;
  private actionBusy = true;
  private busyRevision = 0;
  private busyScheduled = false;
  private timer?: ReturnType<typeof setTimeout>;
  private activeRefresh?: RefreshRequest;
  private pendingRefresh?: RefreshRequest;
  private listeners = new Set<() => void>();
  private snapshot?: EnvironmentSnapshotV1;
  private snapshotKey = "";

  constructor(private readonly host: {
    getSource: () => Source;
    publishStatus: (status: GitStatusResult, ready: boolean) => void;
    assertActive: () => void;
    reportError: (error: unknown) => void;
  }) {}

  private assertActive(): void {
    if (this.disposed) throw new Error("Environment application generation disposed");
    this.host.assertActive();
  }

  getSnapshot(): EnvironmentSnapshotV1 {
    this.assertActive();
    const source = this.host.getSource();
    this.syncBusySource();
    const value = { actionBusy: this.actionBusy, contextKey: source.contextKey, root: source.root, status: source.status, capabilities: {
      review: typeof window.wuu.listGitChanges === "function" && typeof window.wuu.readGitFileDiff === "function"
        && hostSupports("listGitChanges") && hostSupports("readGitFileDiff"),
      checkout: hostSupports("checkoutGitBranch"), createBranch: hostSupports("createCheckoutGitBranch"),
      commit: hostSupports("commitGitChanges"), pullRequest: hostSupports("createPullRequest"),
    } };
    const key = JSON.stringify(value);
    if (!this.snapshot || key !== this.snapshotKey) {
      this.snapshot = immutable(value);
      this.snapshotKey = key;
    }
    return this.snapshot;
  }

  subscribe(listener: () => void): () => void {
    this.assertActive();
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  notifyChanged(): void {
    if (this.disposed) return;
    this.syncBusySource();
    for (const listener of this.listeners) listener();
  }

  /** Consumes the controller's existing stream, including background worktrees. */
  receive(event: ServerEvent): void {
    if (this.disposed || !(event.kind === "server-exit"
      || event.kind === "notification" && busyLifecycleMethods.has(event.message.method))) return;
    this.invalidateBusy();
    this.notifyChanged();
  }

  private syncBusySource(): void {
    const source = this.host.getSource();
    const key = JSON.stringify([source.contextKey, source.root, source.runningThreadKey]);
    if (key === this.busyKey) return;
    this.busyKey = key;
    this.invalidateBusy();
  }

  private invalidateBusy(): void {
    this.actionBusy = Boolean(this.host.getSource().root);
    this.busyRevision++;
    if (this.busyScheduled) return;
    this.busyScheduled = true;
    // One lifecycle event can also change canonical running state. Read once
    // after both invalidations, while exposing conservative busy immediately.
    queueMicrotask(() => {
      this.busyScheduled = false;
      if (this.disposed) return;
      const source = this.host.getSource();
      const revision = this.busyRevision;
      if (!source.root || typeof window.wuu?.gitActionBusy !== "function") return;
      void window.wuu.gitActionBusy(source.root).then(busy => {
        if (this.disposed || revision !== this.busyRevision || !this.isCurrent(source)) return;
        this.actionBusy = busy;
        this.notifyChanged();
      }, () => {
        // The conservative value remains true until a later successful read.
      });
    });
  }

  dispose(): void {
    this.disposed = true;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined;
    this.pendingRefresh?.detach(); this.pendingRefresh = undefined;
    this.activeRefresh?.detach();
    this.listeners.clear();
  }

  private begin(options?: EnvironmentCallOptionsV1): Source & { root: string } {
    this.assertActive();
    options?.signal?.throwIfAborted();
    const source = this.host.getSource();
    if (!source.root || !source.contextKey) throw new Error("session working directory is unavailable");
    return { ...source, root: source.root };
  }

  private isCurrent(source: Source): boolean {
    const current = this.host.getSource();
    return source.contextKey === current.contextKey && source.root === current.root;
  }

  private finish(source: Source, options?: EnvironmentCallOptionsV1): void {
    this.assertActive();
    options?.signal?.throwIfAborted();
    if (!this.isCurrent(source)) throw new Error("Environment workspace changed during the operation");
  }

  private publish(status: GitStatusResult, ready: boolean): void {
    this.host.publishStatus(status, ready);
    this.notifyChanged();
  }

  private branchError(error: unknown, fallback: string): Error {
    const message = toastErrorMessage(error, fallback);
    return new Error(message === "cannot run Git actions while a thread is running in this working tree"
      ? translateCurrent("git.checkoutBlockedByRunningThread") : message);
  }

  async checkoutBranch(branch: string, options?: EnvironmentCallOptionsV1): Promise<void> {
    const source = this.begin(options);
    if (!branch) return;
    let status: GitStatusResult;
    try {
      // Occupancy is checked by the host against the actual mutation target.
      status = await window.wuu.checkoutGitBranch(branch, source.root);
    } catch (error) {
      this.finish(source, options);
      throw this.branchError(error, translateCurrent("git.checkoutFailed"));
    }
    this.finish(source, options);
    this.publish(status, false);
  }

  async createAndCheckoutBranch(branch: string, options?: EnvironmentCallOptionsV1): Promise<void> {
    const source = this.begin(options);
    if (!branch) return;
    let result;
    try {
      result = await window.wuu.createCheckoutGitBranch(branch, source.root);
    } catch (error) {
      this.finish(source, options);
      throw this.branchError(error, translateCurrent("git.branch.createFailed"));
    }
    this.finish(source, options);
    this.publish(result.status, false);
  }

  async commit(input: { message: string; includeUnstaged: boolean }, options?: EnvironmentCallOptionsV1): Promise<EnvironmentCommitResultV1> {
    const source = this.begin(options);
    const result = await window.wuu.commitGitChanges({ message: input.message, include_unstaged: input.includeUnstaged }, source.root);
    this.finish(source, options);
    this.publish(result.status, true);
    return immutable(result);
  }

  async generateCommitMessage(input: { includeUnstaged: boolean }, options?: EnvironmentCallOptionsV1): Promise<string> {
    const source = this.begin(options);
    const result = await window.wuu.generateCommitMessage({ include_unstaged: input.includeUnstaged }, source.root);
    this.finish(source, options);
    return result.message;
  }

  async createPullRequest(input: { title: string; body: string; draft: boolean }, options?: EnvironmentCallOptionsV1): Promise<EnvironmentPullRequestResultV1> {
    const source = this.begin(options);
    const result = await window.wuu.createPullRequest({ ...input }, source.root);
    this.finish(source, options);
    this.publish(result.status, true);
    return immutable(result);
  }

  async listChanges(target: EnvironmentReviewTargetV1, options?: EnvironmentCallOptionsV1): Promise<EnvironmentChangesV1> {
    const source = this.begin(options);
    this.finish(target, options);
    try {
      const result = await window.wuu.listGitChanges(source.root);
      this.finish(source, options);
      return immutable(result);
    } catch (error) {
      this.finish(source, options);
      throw error;
    }
  }

  async readDiff(target: EnvironmentReviewTargetV1 & { readonly path: string }, options?: EnvironmentCallOptionsV1): Promise<EnvironmentFileDiffV1> {
    const source = this.begin(options);
    this.finish(target, options);
    try {
      const result = await window.wuu.readGitFileDiff(target.path, source.root);
      this.finish(source, options);
      return immutable(result);
    } catch (error) {
      this.finish(source, options);
      throw error;
    }
  }

  scheduleStatusRefresh(delayMs: number, options?: EnvironmentCallOptionsV1): void {
    this.assertActive(); options?.signal?.throwIfAborted();
    const source = this.host.getSource();
    if (!source.contextKey || !source.root) return;
    this.pendingRefresh?.detach();
    if (this.timer !== undefined) clearTimeout(this.timer);
    const abort = () => {
      if (this.pendingRefresh === request) {
        this.pendingRefresh = undefined;
        if (this.timer !== undefined) clearTimeout(this.timer);
        this.timer = undefined;
      }
      request.detach();
    };
    const request: RefreshRequest = { options, detach: () => options?.signal?.removeEventListener("abort", abort) };
    this.pendingRefresh = request;
    options?.signal?.addEventListener("abort", abort, { once: true });
    this.timer = setTimeout(() => { this.timer = undefined; void this.refreshStatus(); }, delayMs);
  }

  private async refreshStatus(): Promise<void> {
    if (this.disposed || this.activeRefresh || !this.pendingRefresh) return;
    const request = this.pendingRefresh; this.pendingRefresh = undefined;
    let source: Source & { root: string };
    try { source = this.begin(request.options); } catch { request.detach(); return; }
    this.activeRefresh = request;
    try {
      const status = await window.wuu.gitStatus(source.root);
      this.finish(source, request.options);
      this.publish(status, false);
    } catch (error) {
      // A retired caller or old worktree must not report into its successor.
      try { this.finish(source, request.options); } catch { return; }
      this.host.reportError(error instanceof Error ? error : new Error(translateCurrent("git.refreshFailed")));
    } finally {
      request.detach(); this.activeRefresh = undefined;
      if (this.pendingRefresh && this.timer === undefined && !this.disposed) {
        this.timer = setTimeout(() => { this.timer = undefined; void this.refreshStatus(); }, 150);
      }
    }
  }
}
