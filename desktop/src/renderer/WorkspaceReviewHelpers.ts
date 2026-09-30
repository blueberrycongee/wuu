import type { GitChangeFile } from "../shared/protocol";
import { formatCurrentNumber, translateCurrent as t } from "./i18n";

export type GitChangeTreeNode = {
  kind: "directory" | "file";
  id: string;
  name: string;
  path: string;
  children: GitChangeTreeNode[];
  file?: GitChangeFile;
  additions: number;
  deletions: number;
  fileCount: number;
  binary: boolean;
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

export function buildGitChangeTree(files: GitChangeFile[]): GitChangeTreeNode[] {
  const root = createGitChangeDirectoryNode("", "");
  for (const file of files) {
    insertGitChangeTreeFile(root, file);
  }
  summarizeGitChangeTreeNode(root);
  sortGitChangeTreeNodes(root.children);
  return root.children;
}

function createGitChangeDirectoryNode(name: string, path: string): GitChangeTreeNode {
  return {
    kind: "directory",
    id: `dir:${path || "root"}`,
    name,
    path,
    children: [],
    additions: 0,
    deletions: 0,
    fileCount: 0,
    binary: false
  };
}

function insertGitChangeTreeFile(root: GitChangeTreeNode, file: GitChangeFile): void {
  const parts = file.path.split("/").filter(Boolean);
  if (parts.length === 0) {
    return;
  }
  let parent = root;
  for (let index = 0; index < parts.length - 1; index++) {
    const path = parts.slice(0, index + 1).join("/");
    let child = parent.children.find((node) => node.kind === "directory" && node.path === path);
    if (!child) {
      child = createGitChangeDirectoryNode(parts[index], path);
      parent.children.push(child);
    }
    parent = child;
  }
  parent.children.push({
    kind: "file",
    id: `file:${file.path}`,
    name: parts[parts.length - 1],
    path: file.path,
    children: [],
    file,
    additions: file.additions,
    deletions: file.deletions,
    fileCount: 1,
    binary: file.binary === true
  });
}

function summarizeGitChangeTreeNode(node: GitChangeTreeNode): void {
  if (node.kind === "file") {
    return;
  }
  let additions = 0;
  let deletions = 0;
  let fileCount = 0;
  let binary = false;
  for (const child of node.children) {
    summarizeGitChangeTreeNode(child);
    additions += child.additions;
    deletions += child.deletions;
    fileCount += child.fileCount;
    binary = binary || child.binary;
  }
  node.additions = additions;
  node.deletions = deletions;
  node.fileCount = fileCount;
  node.binary = binary;
}

function sortGitChangeTreeNodes(nodes: GitChangeTreeNode[]): void {
  nodes.sort((left, right) => {
    if (left.kind !== right.kind) {
      return left.kind === "directory" ? -1 : 1;
    }
    return left.name.localeCompare(right.name, undefined, { sensitivity: "base" });
  });
  for (const node of nodes) {
    sortGitChangeTreeNodes(node.children);
  }
}

export function selectGitChangePath(files: GitChangeFile[], preferredPath?: string): string | undefined {
  if (preferredPath && files.some((file) => file.path === preferredPath)) {
    return preferredPath;
  }
  return files[0]?.path;
}

export function expandedGitChangeTreePathsForSelection(path?: string): Set<string> {
  return new Set(path ? gitPathAncestors(path) : []);
}

export function gitPathAncestors(path: string): string[] {
  const parts = path.split("/").filter(Boolean);
  const ancestors: string[] = [];
  for (let index = 0; index < parts.length - 1; index++) {
    ancestors.push(parts.slice(0, index + 1).join("/"));
  }
  return ancestors;
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
