import type {
  ApplicationEnvironmentV1, EnvironmentCallOptionsV1, EnvironmentCommitResultV1,
  EnvironmentPullRequestResultV1, EnvironmentSnapshotV1,
} from "../../../../packages/plugin-sdk/src/ui-environment";
import type { GitStatusResult } from "../../shared/protocol";
import { hostSupports } from "../HostCapabilities";
import { translateCurrent } from "../i18n";
import { toastErrorMessage } from "../Toast";

type Source = { contextKey: string; root?: string; status?: GitStatusResult };
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
  private timer?: ReturnType<typeof setTimeout>;
  private refreshing = false;
  private queued = false;
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
    const value = { contextKey: source.contextKey, root: source.root, status: source.status, capabilities: {
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
    for (const listener of this.listeners) listener();
  }

  dispose(): void {
    this.disposed = true;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = undefined;
    this.queued = false;
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

  scheduleStatusRefresh(delayMs: number): void {
    this.assertActive();
    const source = this.host.getSource();
    if (!source.contextKey || !source.root) return;
    if (this.timer !== undefined) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.refreshStatus();
    }, delayMs);
  }

  private async refreshStatus(): Promise<void> {
    if (this.disposed) return;
    if (this.refreshing) { this.queued = true; return; }
    let source: Source & { root: string };
    try { source = this.begin(); } catch { return; }
    this.refreshing = true;
    try {
      const status = await window.wuu.gitStatus(source.root);
      this.finish(source);
      this.publish(status, false);
    } catch (error) {
      // A retired generation or old worktree must not report into its successor.
      try { this.finish(source); } catch { return; }
      this.host.reportError(error instanceof Error ? error : new Error(translateCurrent("git.refreshFailed")));
    } finally {
      this.refreshing = false;
      if (this.queued && !this.disposed) {
        this.queued = false;
        try { this.scheduleStatusRefresh(150); } catch { /* Generation retired during the read. */ }
      }
    }
  }
}
