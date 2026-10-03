import type { GitChangeFile } from "../shared/protocol";
import { formatCurrentNumber, translateCurrent as t } from "./i18n";

export type GitChangeGroup = {
  // Folder relative to the repository root; empty for files at the root.
  directory: string;
  files: GitChangeFile[];
};

export type GitDiffDisplayLine = {
  content: string;
  kind: string;
  oldLine?: number;
  newLine?: number;
};

export function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${formatCurrentNumber(bytes)} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${formatCurrentNumber(Math.round(bytes / 102.4) / 10)} KB`;
  }
  return `${formatCurrentNumber(Math.round(bytes / 1024 / 102.4) / 10)} MB`;
}

export function summarizeGitChangeFiles(files: GitChangeFile[]): { additions: number; deletions: number } {
  return files.reduce(
    (summary, file) => ({
      additions: summary.additions + file.additions,
      deletions: summary.deletions + file.deletions
    }),
    { additions: 0, deletions: 0 }
  );
}

export function filterGitChangeFiles(files: GitChangeFile[], query: string): GitChangeFile[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) {
    return files;
  }
  return files.filter((file) => {
    const current = file.path.toLocaleLowerCase();
    const previous = file.old_path?.toLocaleLowerCase() ?? "";
    return current.includes(normalized) || previous.includes(normalized);
  });
}

/**
 * Files under their folder, one level deep: root files first, then folders
 * in path order, names sorted within each. The list order is also the order
 * the review steps through.
 */
export function groupGitChangeFiles(files: GitChangeFile[]): GitChangeGroup[] {
  const groups = new Map<string, GitChangeFile[]>();
  for (const file of files) {
    const directory = file.path.slice(0, Math.max(file.path.lastIndexOf("/"), 0));
    groups.set(directory, [...(groups.get(directory) ?? []), file]);
  }
  return [...groups.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([directory, grouped]) => ({
      directory,
      files: grouped.sort((left, right) =>
        gitPathName(left.path).localeCompare(gitPathName(right.path), undefined, { sensitivity: "base" })
      ),
    }));
}

export function gitPathName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

/**
 * Git reports paths from the repository root; the workspace opens files
 * relative to its own folder, which may sit inside the repository. Returns
 * undefined for a file outside the workspace folder.
 */
export function workspaceRelativeGitPath(path: string, gitRoot?: string, workspaceRoot?: string): string | undefined {
  if (!gitRoot || !workspaceRoot) {
    return undefined;
  }
  const normalize = (value: string): string => value.replace(/\\/g, "/").replace(/\/+$/, "");
  const repository = normalize(gitRoot);
  const workspace = normalize(workspaceRoot);
  if (workspace === repository) {
    return path;
  }
  if (!workspace.startsWith(`${repository}/`)) {
    return undefined;
  }
  const prefix = `${workspace.slice(repository.length + 1)}/`;
  return path.startsWith(prefix) ? path.slice(prefix.length) : undefined;
}

export function gitChangeStatusLabel(status: GitChangeFile["status"]): string {
  switch (status) {
    case "modified":
      return "M";
    case "added":
      return "A";
    case "deleted":
      return "D";
    case "renamed":
      return "R";
    case "copied":
      return "C";
    case "untracked":
      return "U";
    default:
      return "?";
  }
}

export function gitChangeStatusText(status: GitChangeFile["status"]): string {
  switch (status) {
    case "modified":
      return t("gitStatus.modified");
    case "added":
      return t("gitStatus.added");
    case "deleted":
      return t("gitStatus.deleted");
    case "renamed":
      return t("gitStatus.renamed");
    case "copied":
      return t("gitStatus.copied");
    case "untracked":
      return t("gitStatus.untracked");
    case "ignored":
      return t("gitStatus.ignored");
    default:
      return t("gitStatus.changed");
  }
}

export function gitDiffDisplayLines(patch: string): GitDiffDisplayLine[] {
  const lines: GitDiffDisplayLine[] = [];
  let oldLine: number | undefined;
  let newLine: number | undefined;
  for (const content of patch.split("\n")) {
    if (content.startsWith("@@")) {
      const match = /@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(content);
      oldLine = match ? Number(match[1]) : undefined;
      newLine = match ? Number(match[2]) : undefined;
      lines.push({ content, kind: "hunk" });
      continue;
    }
    if (content.startsWith("diff --git") || content.startsWith("index ") || content.startsWith("--- ") || content.startsWith("+++ ")) {
      lines.push({ content, kind: "meta" });
      continue;
    }
    if (content.startsWith("\\ No newline")) {
      lines.push({ content, kind: "meta" });
      continue;
    }
    if (content.startsWith("+")) {
      lines.push({ content, kind: "add", newLine });
      if (newLine !== undefined) {
        newLine++;
      }
      continue;
    }
    if (content.startsWith("-")) {
      lines.push({ content, kind: "delete", oldLine });
      if (oldLine !== undefined) {
        oldLine++;
      }
      continue;
    }
    lines.push({ content, kind: "context", oldLine, newLine });
    if (oldLine !== undefined) {
      oldLine++;
    }
    if (newLine !== undefined) {
      newLine++;
    }
  }
  return lines;
}

export function desktopApiSupportsGitReview(): boolean {
  const maybeApi = window.wuu as Partial<typeof window.wuu>;
  return typeof maybeApi.listGitChanges === "function" && typeof maybeApi.readGitFileDiff === "function";
}

export function desktopApiErrorMessage(error: unknown, fallback: string): string {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  if (message.includes("No handler registered")) {
    return t("workspaceReview.apiUnavailable");
  }
  return message || fallback;
}
