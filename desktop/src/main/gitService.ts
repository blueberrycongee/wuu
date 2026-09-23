import { spawnSync } from "node:child_process";
import { readFileSync, statSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";
import type {
  GitChangeFile,
  GitChangesResult,
  GitCommitMessageResult,
  GitCommitParams,
  GitCommitResult,
  GitCreateBranchResult,
  GitDiffStats,
  GitFileDiffResult,
  GitPullRequestParams,
  GitPullRequestResult,
  GitStatusResult,
  RuntimeContext,
} from "../shared/protocol";
import {
  readFilePreviewBuffer,
  truncateTextBytes,
} from "./textPreviews";

const FILE_PREVIEW_MAX_BYTES = 512 * 1024;
const GIT_DIFF_PREVIEW_MAX_BYTES = 512 * 1024;
const GIT_DIFF_COMMAND_MAX_BUFFER = 8 * 1024 * 1024;
// COMMIT_MESSAGE_DIFF_MAX_BYTES caps the staged diff sent to the app-server
// for AI commit message generation; the server enforces its own guard too.
const COMMIT_MESSAGE_DIFF_MAX_BYTES = 150 * 1024;

// CommitMessageGenerator produces a commit message from the staged change.
// Injected by the IPC layer so gitService stays free of app-server client
// wiring; when it throws or returns empty, the commit must not happen.
export type CommitMessageGenerator = (
  context: RuntimeContext,
  input: { diff: string; files: string[] },
) => Promise<string>;

export class GitService {
  constructor(
    private readonly getRuntimeContext: () => RuntimeContext,
    private readonly getRunningThreadCwds: () => string[] = () => [],
    private readonly getKnownThreadCwds: () => string[] = () => [],
    private readonly generateCommitMessage?: CommitMessageGenerator,
  ) {}

  worktreeRoot(cwd: string): string {
    return gitWorktreeRoot(cwd);
  }

  actionBusy(root?: string): boolean {
    return gitWorkingTreeBusy(
      this.contextForRoot(root).cwd,
      this.getRunningThreadCwds(),
    );
  }

  status(options: GitStatusOptions = {}, root?: string): GitStatusResult {
    return gitStatusResult(this.contextForRoot(root), options);
  }

  changes(root?: string): GitChangesResult {
    return gitChangesResult(this.contextForRoot(root));
  }

  fileDiff(path: string, root?: string): GitFileDiffResult {
    return gitFileDiffResult(this.contextForRoot(root), path);
  }

  checkoutBranch(branch: string, root?: string): GitStatusResult {
    const context = this.contextForRoot(root);
    this.assertMutationAllowed(context.cwd);
    return checkoutGitBranch(context, branch);
  }

  createCheckoutBranch(branch: string, root?: string): GitCreateBranchResult {
    const context = this.contextForRoot(root);
    this.assertMutationAllowed(context.cwd);
    return createCheckoutGitBranch(context, branch);
  }

  commit(params: GitCommitParams, root?: string): Promise<GitCommitResult> {
    const context = this.contextForRoot(root);
    this.assertMutationAllowed(context.cwd);
    return commitGitChanges(context, params);
  }

  // commitMessage generates an AI commit message for the current staged
  // change without committing — the dialog fills the result into the input
  // so the user confirms or edits it before the actual commit.
  commitMessage(
    params: GitCommitParams,
    root?: string,
  ): Promise<GitCommitMessageResult> {
    const context = this.contextForRoot(root);
    this.assertMutationAllowed(context.cwd);
    return generateStagedCommitMessage(context, params, this.generateCommitMessage);
  }

  createPullRequest(params: GitPullRequestParams, root?: string): GitPullRequestResult {
    const context = this.contextForRoot(root);
    this.assertMutationAllowed(context.cwd);
    return createPullRequest(context, params);
  }

  private contextForRoot(root?: string): RuntimeContext {
    const context = this.getRuntimeContext();
    const requestedRoot = root?.trim();
    if (!requestedRoot) {
      return context;
    }
    if (!isAbsolute(requestedRoot)) {
      throw new Error("Git working directory must be absolute");
    }
    const normalizedRoot = resolve(requestedRoot);
    const allowedRoots = [context.cwd, ...this.getKnownThreadCwds()].map((cwd) => resolve(cwd));
    if (!allowedRoots.includes(normalizedRoot)) {
      throw new Error("Git working directory is not associated with the current project");
    }
    return { ...context, cwd: normalizedRoot };
  }

  private assertMutationAllowed(cwd: string): void {
    if (gitWorkingTreeBusy(cwd, this.getRunningThreadCwds())) {
      throw new Error(
        "cannot run Git actions while a thread is running in this working tree",
      );
    }
  }
}

export function gitWorktreeRoot(cwd: string): string {
  const root = resolveGitWorktreeRoot(cwd);
  if (!root) {
    throw new Error("working directory is not a Git working tree");
  }
  return root;
}

// Only a confirmed non-repository is safe to ignore when checking other
// sessions. Missing/inaccessible directories and other Git failures stay locked.
function resolveGitWorktreeRoot(cwd: string): string | undefined {
  if (cwd === "") {
    throw new Error("working directory is required");
  }
  const result = spawnSync("git", ["-C", cwd, "rev-parse", "--show-toplevel"], {
    cwd,
    encoding: "utf8",
    env: { ...process.env, LC_ALL: "C", LANGUAGE: "C" },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    if (result.status === 128 && /^fatal: not a git repository \(or any (?:of the )?parent/.test(result.stderr)) {
      return undefined;
    }
    throw new Error(result.stderr.trim() || "failed to resolve Git working tree root");
  }
  const root = result.stdout.trim();
  if (!root) {
    throw new Error("failed to resolve Git working tree root");
  }
  return root;
}

export function gitWorkingTreeBusy(
  cwd: string,
  runningThreadCwds: readonly string[],
): boolean {
  let targetRoot: string;
  try {
    targetRoot = gitWorktreeRoot(cwd);
  } catch {
    return true;
  }
  if (runningThreadCwds.length === 0) {
    return false;
  }
  for (const runningCwd of runningThreadCwds) {
    try {
      const runningRoot = resolveGitWorktreeRoot(runningCwd);
      if (runningRoot && sameGitWorktreeRoot(targetRoot, runningRoot)) {
        return true;
      }
    } catch {
      return true;
    }
  }
  return false;
}

function sameGitWorktreeRoot(left: string, right: string): boolean {
  return process.platform === "win32"
    ? left.toLowerCase() === right.toLowerCase()
    : left === right;
}

type GitStatusOptions = {
  includePullRequestURL?: boolean;
  includeRemoteDefaultBranchFallback?: boolean;
};

function gitStatusResult(
  context: RuntimeContext,
  options: GitStatusOptions = {},
): GitStatusResult {
  const root =
    gitOutput(context.cwd, ["rev-parse", "--show-toplevel"]) ?? context.cwd;
  const insideWorkTree =
    gitOutput(root, ["rev-parse", "--is-inside-work-tree"]) === "true";
  if (!insideWorkTree) {
    return {
      is_repo: false,
      dirty_count: 0,
      diff: emptyGitDiffStats(),
      staged_diff: emptyGitDiffStats(),
    };
  }

  const branchName = gitOutput(root, ["branch", "--show-current"]);
  const head = gitOutput(root, ["rev-parse", "--short", "HEAD"]);
  const branch = branchName || head;
  const branches = gitOutput(root, [
    "for-each-ref",
    "--format=%(refname:short)",
    "refs/heads",
  ])
    ?.split("\n")
    .map((item) => item.trim())
    .filter(Boolean);
  const porcelain = gitOutput(root, ["status", "--porcelain"]);
  const dirtyCount = porcelain
    ? porcelain.split("\n").filter((line) => line.trim()).length
    : 0;
  const upstream = gitOutput(root, [
    "rev-parse",
    "--abbrev-ref",
    "--symbolic-full-name",
    "@{u}",
  ]);
  const [aheadCount, behindCount] = upstream ? gitAheadBehind(root) : [0, 0];
  const remote = upstream?.split("/")[0] || firstGitRemote(root);
  const defaultBranch = remote
    ? gitDefaultBranch(
        root,
        remote,
        Boolean(options.includeRemoteDefaultBranchFallback),
      )
    : undefined;
  const ghAvailable = commandAvailable("gh", ["--version"]);
  const prURL =
    options.includePullRequestURL && branchName && ghAvailable
      ? ghPullRequestURL(root)
      : undefined;

  return {
    is_repo: true,
    branch,
    branches,
    dirty_count: dirtyCount,
    detached: !branchName,
    diff: gitDiffStats(root, true),
    staged_diff: gitStagedDiffStats(root),
    upstream,
    ahead_count: aheadCount,
    behind_count: behindCount,
    remote,
    default_branch: defaultBranch,
    gh_available: ghAvailable,
    pr_url: prURL,
  };
}

function gitChangesResult(context: RuntimeContext): GitChangesResult {
  const root =
    gitOutput(context.cwd, ["rev-parse", "--show-toplevel"]) ?? context.cwd;
  const insideWorkTree =
    gitOutput(root, ["rev-parse", "--is-inside-work-tree"]) === "true";
  if (!insideWorkTree) {
    return { is_repo: false, files: [] };
  }

  const filesByPath = new Map<string, GitChangeFile>();
  for (const file of parseGitNameStatus(
    gitRawOutput(root, [
      "diff",
      "--name-status",
      "-z",
      "--find-renames",
      "HEAD",
      "--",
    ]) ?? "",
  )) {
    filesByPath.set(file.path, file);
  }

  for (const file of parseGitNumstatFiles(
    gitRawOutput(root, ["diff", "--numstat", "-z", "--find-renames", "HEAD", "--"]) ??
      "",
  )) {
    const existing = filesByPath.get(file.path);
    filesByPath.set(file.path, {
      ...file,
      ...existing,
      additions: file.additions,
      deletions: file.deletions,
      binary: existing?.binary || file.binary,
    });
  }

  for (const path of listUntrackedGitFiles(root)) {
    const stats = untrackedGitFileStats(root, path);
    filesByPath.set(path, {
      path,
      status: "untracked",
      additions: stats.additions,
      deletions: 0,
      binary: stats.binary,
    });
  }

  return {
    is_repo: true,
    root,
    files: Array.from(filesByPath.values()).sort((left, right) =>
      left.path.localeCompare(right.path),
    ),
  };
}

function gitFileDiffResult(
  context: RuntimeContext,
  path: string,
): GitFileDiffResult {
  const root =
    gitOutput(context.cwd, ["rev-parse", "--show-toplevel"]) ?? context.cwd;
  const insideWorkTree =
    gitOutput(root, ["rev-parse", "--is-inside-work-tree"]) === "true";
  const { relativePath, absolutePath } = resolveGitRelativePath(root, path);
  if (!insideWorkTree) {
    return emptyGitFileDiffResult(relativePath, false);
  }

  const change = gitChangesResult(context).files.find(
    (file) => file.path === relativePath,
  ) ?? {
    path: relativePath,
    status: "unknown" as const,
    additions: 0,
    deletions: 0,
  };

  if (change.status === "untracked") {
    return gitNewFileDiffResult(absolutePath, change);
  }

  if (change.status === "unknown" && gitPathIgnored(root, relativePath)) {
    const stats = untrackedGitFileStats(root, relativePath);
    return gitNewFileDiffResult(absolutePath, {
      ...change,
      status: "ignored",
      additions: stats.additions,
      binary: stats.binary,
    });
  }

  const rawPatch = gitDiffOutput(
    root,
    change.old_path ? [change.old_path, relativePath] : [relativePath],
  );
  const truncatedPatch = truncateTextBytes(
    rawPatch,
    GIT_DIFF_PREVIEW_MAX_BYTES,
  );
  const binary =
    change.binary ||
    rawPatch.includes("Binary files ") ||
    rawPatch.includes("GIT binary patch");
  const originalText = binary
    ? undefined
    : gitRevisionFileText(root, "HEAD", change.old_path ?? change.path);
  const modifiedText = binary
    ? undefined
    : readWorkingTreeFileText(absolutePath);
  return {
    is_repo: true,
    path: change.path,
    old_path: change.old_path,
    status: change.status,
    additions: change.additions,
    deletions: change.deletions,
    binary,
    patch: truncatedPatch.text,
    original_text: originalText,
    modified_text: modifiedText,
    truncated: truncatedPatch.truncated,
  };
}

function checkoutGitBranch(
  context: RuntimeContext,
  branch: string,
): GitStatusResult {
  const current = gitStatusResult(context);
  const target = branch.trim();
  if (!current.is_repo) {
    throw new Error("current workspace is not a git repository");
  }
  if (!target || !current.branches?.includes(target)) {
    throw new Error("branch not found");
  }
  const result = spawnSync("git", ["-C", context.cwd, "checkout", target], {
    cwd: context.cwd,
    encoding: "utf8",
    env: process.env,
  });
  if (result.status !== 0) {
    throw new Error(result.stderr.trim() || `failed to checkout ${target}`);
  }
  return gitStatusResult(context);
}

function createCheckoutGitBranch(
  context: RuntimeContext,
  branch: string,
): GitCreateBranchResult {
  const current = gitStatusResult(context);
  const target = branch.trim();
  if (!current.is_repo) {
    throw new Error("current workspace is not a git repository");
  }
  validateGitBranchName(context.cwd, target);
  if (current.branches?.includes(target)) {
    throw new Error("branch already exists");
  }
  gitRun(context.cwd, ["checkout", "-b", target]);
  return { status: gitStatusResult(context) };
}

async function commitGitChanges(
  context: RuntimeContext,
  params: GitCommitParams,
): Promise<GitCommitResult> {
  const current = gitStatusResult(context);
  if (!current.is_repo) {
    throw new Error("current workspace is not a git repository");
  }
  if (params.include_unstaged !== false) {
    gitRun(context.cwd, ["add", "-A"]);
  }
  const stagedDiff = gitStagedDiffStats(context.cwd);
  if (stagedDiff.files === 0) {
    throw new Error("there are no staged changes to commit");
  }
  // The message is always user-confirmed here: either typed directly, or
  // AI-generated via commitMessage() and then edited/accepted in the dialog.
  const message = params.message?.trim();
  if (!message) {
    throw new Error("commit message is required");
  }
  gitRun(context.cwd, ["commit", "-m", message]);
  const commit = gitOutput(context.cwd, ["rev-parse", "--short", "HEAD"]) ?? "";
  return {
    status: gitStatusResult(context),
    commit,
    message,
  };
}

// generateStagedCommitMessage stages the same change a commit would include
// and asks the app-server's BYOK model for a commit message — but never
// commits. Any failure (no generator wired, provider error, empty result)
// surfaces as an error; the staged state is left intact either way.
async function generateStagedCommitMessage(
  context: RuntimeContext,
  params: GitCommitParams,
  generateCommitMessage?: CommitMessageGenerator,
): Promise<GitCommitMessageResult> {
  const current = gitStatusResult(context);
  if (!current.is_repo) {
    throw new Error("current workspace is not a git repository");
  }
  if (params.include_unstaged !== false) {
    gitRun(context.cwd, ["add", "-A"]);
  }
  const stagedDiff = gitStagedDiffStats(context.cwd);
  if (stagedDiff.files === 0) {
    throw new Error("there are no staged changes to commit");
  }
  const message = await generateAICommitMessage(context, generateCommitMessage);
  return { message };
}

// generateAICommitMessage asks the app-server's BYOK model for a commit
// message from the staged change. Any failure (no generator wired, provider
// error, empty result) aborts the commit — the staged state is left intact
// so the user can type a message or retry.
async function generateAICommitMessage(
  context: RuntimeContext,
  generateCommitMessage?: CommitMessageGenerator,
): Promise<string> {
  if (!generateCommitMessage) {
    throw new Error("AI commit message generation is not available");
  }
  const files = (
    gitRawOutput(context.cwd, ["diff", "--cached", "--name-only", "-z", "--"]) ?? ""
  )
    .split("\0")
    .filter(Boolean);
  const diff = gitStagedDiffForPrompt(context.cwd);
  let generated: string;
  try {
    generated = await generateCommitMessage(context, { diff, files });
  } catch (error) {
    throw new Error(
      `AI commit message generation failed: ${
        error instanceof Error ? error.message : String(error)
      }`,
    );
  }
  const message = generated.trim();
  if (!message) {
    throw new Error("AI commit message generation returned an empty message");
  }
  return message;
}

// gitStagedDiffForPrompt returns the staged diff capped to
// COMMIT_MESSAGE_DIFF_MAX_BYTES; when the diff overflows the command buffer
// entirely (giant binary drops, huge generated files), it degrades to the
// --stat summary so the model still sees the shape of the change.
function gitStagedDiffForPrompt(cwd: string): string {
  const result = spawnSync(
    "git",
    ["-C", cwd, "diff", "--cached", "--no-ext-diff", "--find-renames", "--"],
    {
      cwd,
      encoding: "utf8",
      env: process.env,
      maxBuffer: GIT_DIFF_COMMAND_MAX_BUFFER,
    },
  );
  if (result.status !== 0 || result.error) {
    const stat = gitOutput(cwd, ["diff", "--cached", "--stat", "--"]);
    if (!stat) {
      throw new Error("failed to read staged changes");
    }
    return `[full diff unavailable]\n${stat}`;
  }
  return truncateTextBytes(result.stdout, COMMIT_MESSAGE_DIFF_MAX_BYTES).text;
}

function createPullRequest(
  context: RuntimeContext,
  params: GitPullRequestParams,
): GitPullRequestResult {
  const status = gitStatusResult(context, {
    includePullRequestURL: true,
    includeRemoteDefaultBranchFallback: true,
  });
  if (!status.is_repo) {
    throw new Error("current workspace is not a git repository");
  }
  if (!status.gh_available) {
    throw new Error("GitHub CLI is not available");
  }
  const branch = gitOutput(context.cwd, ["branch", "--show-current"]);
  if (!branch) {
    throw new Error("pull requests require a named branch");
  }
  if (status.default_branch && branch === status.default_branch) {
    throw new Error("create a feature branch before opening a pull request");
  }
  if (status.dirty_count > 0) {
    throw new Error(
      "commit or discard local changes before opening a pull request",
    );
  }

  const existingURL = status.pr_url;
  if (existingURL) {
    return { status, url: existingURL, already_exists: true };
  }

  if (!status.upstream) {
    const remote = status.remote || "origin";
    gitRun(context.cwd, ["push", "-u", remote, branch]);
  }

  const args = ["pr", "create"];
  if (params.draft) {
    args.push("--draft");
  }
  const title = params.title?.trim();
  const body = params.body?.trim();
  if (title || body) {
    args.push("--title", title || branch, "--body", body || "");
  } else {
    args.push("--fill");
  }
  const url = ghOutput(context.cwd, args);
  if (!url) {
    throw new Error("GitHub CLI did not return a pull request URL");
  }
  return {
    status: {
      ...gitStatusResult(context, { includeRemoteDefaultBranchFallback: true }),
      pr_url: url,
    },
    url,
    already_exists: false,
  };
}

function validateGitBranchName(cwd: string, branch: string): void {
  if (!branch) {
    throw new Error("branch name is required");
  }
  const result = spawnSync(
    "git",
    ["-C", cwd, "check-ref-format", "--branch", branch],
    {
      cwd,
      encoding: "utf8",
      env: process.env,
    },
  );
  if (result.status !== 0) {
    throw new Error(result.stderr.trim() || "invalid branch name");
  }
}

function gitRun(cwd: string, args: string[]): string {
  const result = spawnSync("git", ["-C", cwd, ...args], {
    cwd,
    encoding: "utf8",
    env: process.env,
  });
  if (result.status !== 0) {
    throw new Error(
      result.stderr.trim() ||
        result.stdout.trim() ||
        `git ${args.join(" ")} failed`,
    );
  }
  return result.stdout.trim();
}

function gitOutput(cwd: string, args: string[]): string | undefined {
  return gitRawOutput(cwd, args)?.trim() || undefined;
}

function gitRawOutput(cwd: string, args: string[]): string | undefined {
  const result = spawnSync("git", ["-C", cwd, ...args], {
    cwd,
    encoding: "utf8",
    env: process.env,
  });
  if (result.status !== 0) {
    return undefined;
  }
  return result.stdout;
}

function emptyGitDiffStats(): GitDiffStats {
  return { files: 0, additions: 0, deletions: 0 };
}

function gitDiffStats(cwd: string, includeUntracked: boolean): GitDiffStats {
  const stats = parseGitNumstat(
    gitRawOutput(cwd, ["diff", "--numstat", "-z", "HEAD", "--"]) ?? "",
  );
  if (!includeUntracked) {
    return stats;
  }
  const untracked = listUntrackedGitFiles(cwd);
  if (!untracked.length) {
    return stats;
  }
  let additions = 0;
  for (const path of untracked.slice(0, 100)) {
    additions += countTextFileLines(resolve(cwd, path));
  }
  return {
    files: stats.files + untracked.length,
    additions: stats.additions + additions,
    deletions: stats.deletions,
  };
}

function gitStagedDiffStats(cwd: string): GitDiffStats {
  return parseGitNumstat(
    gitRawOutput(cwd, ["diff", "--cached", "--numstat", "-z", "--"]) ?? "",
  );
}

function gitDiffOutput(cwd: string, relativePaths: string[]): string {
  const result = spawnSync(
    "git",
    [
      "--literal-pathspecs",
      "-C",
      cwd,
      "diff",
      "--no-ext-diff",
      "--find-renames",
      "--unified=3",
      "HEAD",
      "--",
      ...relativePaths,
    ],
    {
      cwd,
      encoding: "utf8",
      env: process.env,
      maxBuffer: GIT_DIFF_COMMAND_MAX_BUFFER,
    },
  );
  if (result.status !== 0 && !result.stdout) {
    throw new Error(
      result.stderr.trim() || `git diff failed for ${relativePaths.join(", ")}`,
    );
  }
  return result.stdout;
}

function parseGitNumstat(output: string): GitDiffStats {
  const stats = emptyGitDiffStats();
  for (const file of parseGitNumstatFiles(output)) {
    stats.files += 1;
    stats.additions += file.additions;
    stats.deletions += file.deletions;
  }
  return stats;
}

function parseGitNameStatus(output: string): GitChangeFile[] {
  const files: GitChangeFile[] = [];
  const fields = output.split("\0");
  for (let index = 0; index < fields.length - 1;) {
    const status = gitChangeStatus(fields[index++]);
    const firstPath = fields[index++];
    const oldPath =
      status === "renamed" || status === "copied" ? firstPath : undefined;
    const path = oldPath === undefined ? firstPath : fields[index++];
    files.push({
      path,
      old_path: oldPath,
      status,
      additions: 0,
      deletions: 0,
    });
  }
  return files;
}

function parseGitNumstatFiles(output: string): GitChangeFile[] {
  const files: GitChangeFile[] = [];
  const fields = output.split("\0");
  for (let index = 0; index < fields.length - 1; index++) {
    const record = fields[index];
    // Only the first two tabs are separators; filenames may contain tabs.
    const firstTab = record.indexOf("\t");
    const secondTab = record.indexOf("\t", firstTab + 1);
    const additions = record.slice(0, firstTab);
    const deletions = record.slice(firstTab + 1, secondTab);
    let path = record.slice(secondTab + 1);
    if (!path) {
      // A rename/copy has an empty path field followed by old and new paths.
      index += 2;
      path = fields[index];
    }
    files.push({
      path,
      status: "unknown",
      additions: additions === "-" ? 0 : Number(additions) || 0,
      deletions: deletions === "-" ? 0 : Number(deletions) || 0,
      binary: additions === "-" || deletions === "-",
    });
  }
  return files;
}

function gitChangeStatus(statusCode: string): GitChangeFile["status"] {
  switch (statusCode[0]) {
    case "M":
      return "modified";
    case "A":
      return "added";
    case "D":
      return "deleted";
    case "R":
      return "renamed";
    case "C":
      return "copied";
    default:
      return "unknown";
  }
}

function listUntrackedGitFiles(cwd: string): string[] {
  return (
    gitRawOutput(cwd, ["ls-files", "--others", "--exclude-standard", "-z"]) ?? ""
  ).split("\0").filter(Boolean);
}

function untrackedGitFileStats(
  root: string,
  path: string,
): { additions: number; binary: boolean } {
  const { absolutePath } = resolveGitRelativePath(root, path);
  try {
    const stats = statSync(absolutePath);
    if (!stats.isFile()) {
      return { additions: 0, binary: false };
    }
    const previewBuffer = readFilePreviewBuffer(
      absolutePath,
      Math.min(stats.size, FILE_PREVIEW_MAX_BYTES),
    );
    const binary = previewBuffer.includes(0);
    return {
      additions: binary ? 0 : countTextFileLines(absolutePath),
      binary,
    };
  } catch {
    return { additions: 0, binary: false };
  }
}

function gitNewFileDiffResult(
  absolutePath: string,
  change: GitChangeFile,
): GitFileDiffResult {
  try {
    const stats = statSync(absolutePath);
    if (!stats.isFile()) {
      return emptyGitFileDiffResult(change.path, true);
    }
    const readLimit = Math.min(stats.size, GIT_DIFF_PREVIEW_MAX_BYTES + 1);
    const buffer = readFilePreviewBuffer(absolutePath, readLimit);
    const truncated = stats.size > GIT_DIFF_PREVIEW_MAX_BYTES;
    const previewBuffer = buffer.subarray(
      0,
      truncated ? GIT_DIFF_PREVIEW_MAX_BYTES : buffer.length,
    );
    const binary = previewBuffer.includes(0);
    const patch = binary
      ? `Binary file ${change.path} is not tracked by Git`
      : buildUntrackedPatch(
          change.path,
          previewBuffer.toString("utf8"),
          truncated,
        );
    return {
      is_repo: true,
      path: change.path,
      old_path: change.old_path,
      status: change.status,
      additions: change.additions,
      deletions: change.deletions,
      binary,
      patch,
      original_text: binary ? undefined : "",
      modified_text: binary ? undefined : previewBuffer.toString("utf8"),
      truncated,
    };
  } catch {
    return emptyGitFileDiffResult(change.path, true);
  }
}

function gitRevisionFileText(
  root: string,
  revision: string,
  relativePath: string,
): string {
  const result = spawnSync(
    "git",
    ["-C", root, "show", `${revision}:${relativePath}`],
    {
      cwd: root,
      encoding: "utf8",
      env: process.env,
      maxBuffer: GIT_DIFF_COMMAND_MAX_BUFFER,
    },
  );
  if (result.status !== 0) {
    return "";
  }
  return truncateTextBytes(result.stdout, GIT_DIFF_PREVIEW_MAX_BYTES).text;
}

function readWorkingTreeFileText(absolutePath: string): string {
  try {
    const stats = statSync(absolutePath);
    if (!stats.isFile()) {
      return "";
    }
    return readFilePreviewBuffer(
      absolutePath,
      Math.min(stats.size, GIT_DIFF_PREVIEW_MAX_BYTES),
    ).toString("utf8");
  } catch {
    return "";
  }
}

function gitPathIgnored(root: string, relativePath: string): boolean {
  const result = spawnSync(
    "git",
    ["-C", root, "check-ignore", "--quiet", "--", relativePath],
    {
      cwd: root,
      encoding: "utf8",
      env: process.env,
    },
  );
  return result.status === 0;
}

function buildUntrackedPatch(
  path: string,
  text: string,
  truncated: boolean,
): string {
  const lines = splitPatchTextLines(text);
  const patchLines = [
    `diff --git a/${path} b/${path}`,
    "new file mode 100644",
    "--- /dev/null",
    `+++ b/${path}`,
    `@@ -0,0 +1,${lines.length} @@`,
    ...lines.map((line) => `+${line}`),
  ];
  if (truncated) {
    patchLines.push("+");
    patchLines.push("+[diff truncated]");
  }
  return patchLines.join("\n");
}

function splitPatchTextLines(text: string): string[] {
  if (!text) {
    return [];
  }
  const withoutFinalNewline = text.endsWith("\n") ? text.slice(0, -1) : text;
  return withoutFinalNewline ? withoutFinalNewline.split(/\r?\n/) : [];
}

function normalizeGitRelativePath(path: string): string {
  // Backslashes are literal filename characters on POSIX, not separators.
  const portablePath = process.platform === "win32" ? path.replace(/\\/g, "/") : path;
  const normalized = portablePath.replace(/^\/+/, "");
  if (!normalized || normalized.split("/").some((part) => part === ".." || part === "")) {
    throw new Error("invalid workspace file path");
  }
  return normalized;
}

function resolveGitRelativePath(
  root: string,
  path: string,
): { relativePath: string; absolutePath: string } {
  const relativePath = normalizeGitRelativePath(path);
  const absolutePath = resolve(root, relativePath);
  const relativeToRoot = relative(root, absolutePath);
  if (
    !relativeToRoot ||
    relativeToRoot.startsWith("..") ||
    isAbsolute(relativeToRoot)
  ) {
    throw new Error("file is outside the current git repository");
  }
  return { relativePath, absolutePath };
}

function emptyGitFileDiffResult(
  path: string,
  isRepo: boolean,
): GitFileDiffResult {
  return {
    is_repo: isRepo,
    path,
    status: "unknown",
    additions: 0,
    deletions: 0,
    binary: false,
    patch: "",
    truncated: false,
  };
}

function countTextFileLines(filePath: string): number {
  try {
    const stats = statSync(filePath);
    if (!stats.isFile() || stats.size > 1024 * 1024) {
      return 0;
    }
    const content = readFileSync(filePath);
    if (content.includes(0)) {
      return 0;
    }
    const text = content.toString("utf8");
    if (!text) {
      return 0;
    }
    return text.endsWith("\n")
      ? text.split("\n").length - 1
      : text.split(/\r\n|\n|\r/).length;
  } catch {
    return 0;
  }
}

function gitAheadBehind(cwd: string): [number, number] {
  const output = gitOutput(cwd, [
    "rev-list",
    "--left-right",
    "--count",
    "HEAD...@{u}",
  ]);
  const [ahead, behind] = output
    ?.split(/\s+/, 2)
    .map((item) => Number(item) || 0) ?? [0, 0];
  return [ahead, behind];
}

function firstGitRemote(cwd: string): string | undefined {
  return gitOutput(cwd, ["remote"])
    ?.split("\n")
    .map((item) => item.trim())
    .find(Boolean);
}

function gitDefaultBranch(
  cwd: string,
  remote: string,
  includeRemoteFallback = false,
): string | undefined {
  const symbolic = gitOutput(cwd, [
    "symbolic-ref",
    "--short",
    `refs/remotes/${remote}/HEAD`,
  ]);
  if (symbolic?.startsWith(`${remote}/`)) {
    return symbolic.slice(remote.length + 1);
  }
  if (!includeRemoteFallback) {
    return undefined;
  }
  return gitOutput(cwd, ["remote", "show", remote])
    ?.split("\n")
    .map((line) => line.trim())
    .find((line) => line.startsWith("HEAD branch:"))
    ?.replace("HEAD branch:", "")
    .trim();
}

function commandAvailable(command: string, args: string[]): boolean {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    env: process.env,
  });
  return result.status === 0;
}

function ghOutput(cwd: string, args: string[]): string | undefined {
  const result = spawnSync("gh", args, {
    cwd,
    encoding: "utf8",
    env: process.env,
  });
  if (result.status !== 0) {
    if (args[0] === "pr" && args[1] === "view") {
      return undefined;
    }
    throw new Error(
      result.stderr.trim() ||
        result.stdout.trim() ||
        `gh ${args.join(" ")} failed`,
    );
  }
  return result.stdout.trim() || undefined;
}

function ghPullRequestURL(cwd: string): string | undefined {
  return ghOutput(cwd, ["pr", "view", "--json", "url", "--jq", ".url"]);
}
