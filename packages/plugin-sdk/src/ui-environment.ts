export type EnvironmentGitStatusV1 = {
  is_repo: boolean;
  branch?: string;
  branches?: string[];
  dirty_count: number;
  detached?: boolean;
  diff?: EnvironmentGitDiffStatsV1;
  staged_diff?: EnvironmentGitDiffStatsV1;
  upstream?: string;
  ahead_count?: number;
  behind_count?: number;
  remote?: string;
  default_branch?: string;
  gh_available?: boolean;
  pr_url?: string;
};

export type EnvironmentGitDiffStatsV1 = {
  files: number;
  additions: number;
  deletions: number;
};

export interface EnvironmentCommitResultV1 {
  readonly status: EnvironmentGitStatusV1;
  readonly commit: string;
  readonly message: string;
}
export interface EnvironmentPullRequestResultV1 {
  readonly status: EnvironmentGitStatusV1;
  readonly url: string;
  readonly already_exists: boolean;
}
export interface EnvironmentSnapshotV1 {
  readonly contextKey: string;
  readonly root?: string;
  readonly status?: EnvironmentGitStatusV1;
  readonly capabilities: { readonly review: boolean; readonly checkout: boolean; readonly createBranch: boolean; readonly commit: boolean; readonly pullRequest: boolean };
}
export interface EnvironmentCallOptionsV1 { readonly signal?: AbortSignal; }
/** Calls retain their selected application generation and captured worktree.
 * Aborting an admitted Git mutation does not roll it back; callers must not replay it.
 * A changed workspace rejects a foreground result rather than updating a new workspace.
 */
export interface EnvironmentReviewTargetV1 { readonly root: string; readonly contextKey: string; }
export type EnvironmentChangeStatusV1 = "modified" | "added" | "deleted" | "renamed" | "copied" | "untracked" | "ignored" | "unknown";

export type EnvironmentChangeFileV1 = {
  path: string;
  old_path?: string;
  status: EnvironmentChangeStatusV1;
  additions: number;
  deletions: number;
  binary?: boolean;
};

export type EnvironmentChangesV1 = {
  is_repo: boolean;
  root?: string;
  files: EnvironmentChangeFileV1[];
};

export type EnvironmentFileDiffV1 = {
  is_repo: boolean;
  path: string;
  old_path?: string;
  status: EnvironmentChangeStatusV1;
  additions: number;
  deletions: number;
  binary?: boolean;
  patch: string;
  original_text?: string;
  modified_text?: string;
  truncated: boolean;
};

export interface ApplicationEnvironmentV1 {
  listChanges(target: EnvironmentReviewTargetV1, options?: EnvironmentCallOptionsV1): Promise<EnvironmentChangesV1>;
  readDiff(target: EnvironmentReviewTargetV1 & { readonly path: string }, options?: EnvironmentCallOptionsV1): Promise<EnvironmentFileDiffV1>;
  getSnapshot(): EnvironmentSnapshotV1;
  subscribe(listener: () => void): () => void;
  checkoutBranch(branch: string, options?: EnvironmentCallOptionsV1): Promise<void>;
  createAndCheckoutBranch(branch: string, options?: EnvironmentCallOptionsV1): Promise<void>;
  commit(input: { message: string; includeUnstaged: boolean }, options?: EnvironmentCallOptionsV1): Promise<EnvironmentCommitResultV1>;
  generateCommitMessage(input: { includeUnstaged: boolean }, options?: EnvironmentCallOptionsV1): Promise<string>;
  createPullRequest(input: { title: string; body: string; draft: boolean }, options?: EnvironmentCallOptionsV1): Promise<EnvironmentPullRequestResultV1>;
  /** Debounces scheduled reads and coalesces requests arriving during a read. */
  scheduleStatusRefresh(delayMs: number): void;
}
