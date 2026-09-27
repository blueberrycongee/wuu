import {
  closeSync,
  openSync,
  readdirSync,
  readSync,
  realpathSync,
  statSync,
  type Dirent,
} from "node:fs";
import { createHash } from "node:crypto";
import { homedir } from "node:os";
import { basename, isAbsolute, join, relative, resolve, sep } from "node:path";
import type {
  FileTreeListResult,
  RuntimeContext,
  WorkspaceDirectoryListResult,
  WorkspaceFileReferenceResolveResult,
  WorkspaceFileReadResult,
  WorkspaceFileSaveParams,
  WorkspaceFileSaveResult,
} from "../shared/protocol";
import {
  isRenderableImageFile,
  isRenderablePdfFile,
  isRenderableVideoFile,
  renderableFileURL,
} from "./renderableFileURLs";
import { writeTextFileAtomicSync } from "./atomicFile";

const FILE_TREE_MAX_PATHS = 4000;
const FILE_PREVIEW_MAX_BYTES = 512 * 1024;
const FILE_REFERENCE_SEARCH_MAX_VISITS = 8000;
const FILE_TREE_IGNORED_DIRS = new Set([
  ".git",
  ".next",
  ".turbo",
  ".vite",
  "coverage",
  "dist",
  "node_modules",
  "out",
  "target",
]);
const FILE_TREE_IGNORED_FILES = new Set([".DS_Store"]);

export class WorkspaceFileService {
  constructor(private readonly getRuntimeContext: () => RuntimeContext) {}

  // resolveContext optionally overrides the global runtime context's cwd
  // with an explicit root. The override is used, for instance, when the
  // active thread's own cwd (a git worktree) differs from the active
  // project's cwd — see workspacePanelContext in AppState.ts. The override
  // originates from the app server (Thread.cwd) so it is trusted, but is
  // still normalized before use.
  private resolveContext(root?: string): RuntimeContext {
    const context = this.getRuntimeContext();
    if (!root) {
      return context;
    }
    return { ...context, cwd: normalizeWorkspaceRoot(root) };
  }

  fileTreeList(root?: string): FileTreeListResult {
    return fileTreeListResult(this.resolveContext(root));
  }

  directoryList(path?: string, root?: string): WorkspaceDirectoryListResult {
    return workspaceDirectoryListResult(this.resolveContext(root), path);
  }

  readFile(path: string, root?: string): WorkspaceFileReadResult {
    return readWorkspaceFileResult(this.resolveContext(root), path);
  }

  writeFile(
    params: WorkspaceFileSaveParams,
    root?: string,
  ): WorkspaceFileSaveResult {
    return writeWorkspaceFileResult(this.resolveContext(root), params);
  }

  resolveFileReference(
    reference: string,
    root?: string,
  ): WorkspaceFileReferenceResolveResult {
    return resolveWorkspaceFileReferenceResult(
      this.resolveContext(root),
      reference,
    );
  }
}

function normalizeWorkspaceRoot(root: string): string {
  return resolve(root);
}

function fileTreeListResult(context: RuntimeContext): FileTreeListResult {
  const paths: string[] = [];
  const truncated = collectFileTreePaths(context.cwd, "", paths);
  return { root: context.cwd, paths, truncated };
}

function workspaceDirectoryListResult(
  context: RuntimeContext,
  path?: string,
): WorkspaceDirectoryListResult {
  const relativeDirectoryPath = normalizeWorkspaceDirectoryPath(path ?? "");
  const absoluteDirectoryPath = resolveWorkspaceDirectoryPath(
    context.cwd,
    relativeDirectoryPath,
  );
  const stats = statSync(absoluteDirectoryPath);
  if (!stats.isDirectory()) {
    throw new Error("selected path is not a directory");
  }

  let entries: Dirent[];
  try {
    entries = readdirSync(absoluteDirectoryPath, { withFileTypes: true });
  } catch {
    entries = [];
  }

  entries.sort(compareFileTreeEntries);

  const visibleEntries = [];
  let truncated = false;
  for (const entry of entries) {
    if (FILE_TREE_IGNORED_FILES.has(entry.name)) {
      continue;
    }

    const relativePath = relativeDirectoryPath
      ? `${relativeDirectoryPath}/${entry.name}`
      : entry.name;
    if (entry.isDirectory()) {
      if (FILE_TREE_IGNORED_DIRS.has(entry.name)) {
        continue;
      }
      visibleEntries.push({
        name: entry.name,
        path: `${relativePath}/`,
        kind: "directory" as const,
      });
    } else if (entry.isFile() || entry.isSymbolicLink()) {
      visibleEntries.push({
        name: entry.name,
        path: relativePath,
        kind: "file" as const,
      });
    }

    if (visibleEntries.length >= FILE_TREE_MAX_PATHS) {
      truncated = true;
      break;
    }
  }

  return {
    root: context.cwd,
    path: relativeDirectoryPath,
    entries: visibleEntries,
    truncated,
  };
}

function readWorkspaceFileResult(
  context: RuntimeContext,
  path: string,
): WorkspaceFileReadResult {
  const relativeFilePath = normalizeWorkspaceRelativePath(path);
  const absolutePath = resolveWorkspacePath(context.cwd, relativeFilePath);
  const stats = statSync(absolutePath);
  if (!stats.isFile()) {
    throw new Error("selected path is not a file");
  }

  const readLimit = Math.min(stats.size, FILE_PREVIEW_MAX_BYTES + 1);
  const buffer = Buffer.alloc(readLimit);
  const descriptor = openSync(absolutePath, "r");
  let bytesRead = 0;
  try {
    bytesRead = readSync(descriptor, buffer, 0, readLimit, 0);
  } finally {
    closeSync(descriptor);
  }
  const truncated = stats.size > FILE_PREVIEW_MAX_BYTES;
  const previewBuffer = buffer.subarray(
    0,
    truncated ? FILE_PREVIEW_MAX_BYTES : bytesRead,
  );
  const binary = previewBuffer.includes(0);

  const renderableKind = isRenderableImageFile(absolutePath)
    ? ("image" as const)
    : isRenderablePdfFile(absolutePath)
      ? ("pdf" as const)
      : isRenderableVideoFile(absolutePath)
        ? ("video" as const)
        : undefined;

  return {
    root: context.cwd,
    path: relativeFilePath,
    absolute_path: absolutePath,
    size_bytes: stats.size,
    mtime_ms: stats.mtimeMs,
    sha256: sha256Hex(previewBuffer),
    binary,
    truncated,
    text: binary ? undefined : previewBuffer.toString("utf8"),
    renderable_url: renderableKind
      ? renderableFileURL(absolutePath)
      : undefined,
    renderable_kind: renderableKind,
  };
}

function writeWorkspaceFileResult(
  context: RuntimeContext,
  params: WorkspaceFileSaveParams,
): WorkspaceFileSaveResult {
  if (!params || typeof params !== "object") {
    throw new Error("missing file save parameters");
  }
  if (toWorkspaceSlash(params.path).split("/").some((segment) => segment === "..")) {
    throw new Error("file is outside workspace");
  }
  if (typeof params.text !== "string") {
    throw new Error("file text must be a string");
  }

  const current = readWorkspaceFileResult(context, params.path);
  if (current.binary) {
    throw new Error("cannot save binary workspace file");
  }
  if (current.truncated) {
    throw new Error("cannot save truncated workspace file");
  }
  if (
    current.sha256 !== params.base_sha256 ||
    current.mtime_ms !== params.base_mtime_ms
  ) {
    return { status: "conflict", file: current };
  }

  const realRoot = realpathSync(context.cwd);
  const writePath = realpathSync(current.absolute_path);
  if (!isPathInsideRoot(writePath, realRoot)) {
    throw new Error("file is outside the current workspace");
  }
  writeTextFileAtomicSync(writePath, params.text);
  return {
    status: "saved",
    file: readWorkspaceFileResult(context, current.path),
  };
}

function resolveWorkspaceFileReferenceResult(
  context: RuntimeContext,
  reference: string,
): WorkspaceFileReferenceResolveResult {
  const root = context.cwd;
  const filePath = filePathFromReference(reference);
  if (!filePath) {
    return { root, reference, status: "invalid" };
  }

  if (isQualifiedWorkspaceReference(filePath)) {
    const resolved = resolveDirectWorkspaceFile(root, filePath);
    return resolved
      ? { root, reference, status: "resolved", ...resolved }
      : { root, reference, status: "missing" };
  }

  const search = collectBasenameMatches(root, filePath);
  if (search.matches.length === 1 && !search.truncated) {
    const resolved = resolveDirectWorkspaceFile(root, search.matches[0]);
    return resolved
      ? { root, reference, status: "resolved", ...resolved }
      : { root, reference, status: "missing" };
  }
  if (search.matches.length > 1 || search.truncated) {
    return { root, reference, status: "ambiguous", matches: search.matches };
  }
  return { root, reference, status: "missing" };
}

const FILE_REFERENCE_LINE_RANGE_SEPARATOR_SOURCE = String.raw`[-:\u2013\u2014]`;
const FILE_REFERENCE_LINE_SUFFIX_PATTERN = new RegExp(
  String.raw`^(.*?)(?::\d+(?:${FILE_REFERENCE_LINE_RANGE_SEPARATOR_SOURCE}\d+)?|\s+\((?:line|lines)\s+\d+(?:${FILE_REFERENCE_LINE_RANGE_SEPARATOR_SOURCE}\d+)?\))$`,
  "i",
);

function filePathFromReference(reference: string): string | undefined {
  if (typeof reference !== "string") {
    return undefined;
  }
  const trimmed = stripReferenceWrappers(reference.trim());
  const filePath = (trimmed.match(FILE_REFERENCE_LINE_SUFFIX_PATTERN)?.[1] ?? trimmed)
    .trim()
    .replace(/\\/g, "/")
    .replace(/\/+$/, "");
  if (!filePath || filePath.includes("\0") || /^[a-z][a-z0-9+.-]*:\/\//i.test(filePath)) {
    return undefined;
  }
  return filePath;
}

function stripReferenceWrappers(value: string): string {
  const pairs: Array<[string, string]> = [
    ["`", "`"],
    ["<", ">"],
    ['"', '"'],
    ["'", "'"],
  ];
  for (const [open, close] of pairs) {
    if (value.startsWith(open) && value.endsWith(close)) {
      return value.slice(open.length, -close.length).trim();
    }
  }
  return value;
}

function isQualifiedWorkspaceReference(path: string): boolean {
  return path.includes("/") || path.startsWith("~");
}

function resolveDirectWorkspaceFile(
  root: string,
  path: string,
): Pick<WorkspaceFileReferenceResolveResult, "path" | "absolute_path"> | undefined {
  const absolutePath = absolutePathForReference(root, path);
  if (!absolutePath || !isPathInsideRoot(absolutePath, root)) {
    return undefined;
  }

  let stats;
  try {
    stats = statSync(absolutePath);
  } catch {
    return undefined;
  }
  if (!stats.isFile()) {
    return undefined;
  }

  const realRoot = realpathSync(root);
  const realFile = realpathSync(absolutePath);
  if (!isPathInsideRoot(realFile, realRoot)) {
    return undefined;
  }

  return {
    path: toWorkspaceSlash(relative(root, absolutePath)),
    absolute_path: absolutePath,
  };
}

function absolutePathForReference(root: string, path: string): string | undefined {
  if (path.startsWith("~/")) {
    return resolve(homedir(), path.slice(2));
  }
  if (isAbsolute(path)) {
    return resolve(path);
  }
  return resolve(root, path.replace(/^\.\/+/, ""));
}

function collectBasenameMatches(
  root: string,
  fileName: string,
): { matches: string[]; truncated: boolean } {
  if (basename(fileName) !== fileName || fileName === "." || fileName === "..") {
    return { matches: [], truncated: false };
  }

  const matches: string[] = [];
  const directoriesToRead = [""];
  let visited = 0;

  for (let index = 0; index < directoriesToRead.length; index += 1) {
    if (visited >= FILE_REFERENCE_SEARCH_MAX_VISITS) {
      return { matches, truncated: true };
    }

    const currentRelativeDirectory = directoriesToRead[index];
    const directory = currentRelativeDirectory
      ? join(root, currentRelativeDirectory)
      : root;
    let entries: Dirent[];
    try {
      entries = readdirSync(directory, { withFileTypes: true });
    } catch {
      continue;
    }

    entries.sort(compareFileTreeEntries);

    for (const entry of entries) {
      visited += 1;
      if (FILE_TREE_IGNORED_FILES.has(entry.name)) {
        continue;
      }

      const relativePath = currentRelativeDirectory
        ? `${currentRelativeDirectory}/${entry.name}`
        : entry.name;
      if (entry.isDirectory()) {
        if (!FILE_TREE_IGNORED_DIRS.has(entry.name)) {
          directoriesToRead.push(relativePath);
        }
        continue;
      }
      if ((entry.isFile() || entry.isSymbolicLink()) && entry.name === fileName) {
        const resolved = resolveDirectWorkspaceFile(root, relativePath);
        if (resolved) {
          matches.push(resolved.path ?? relativePath);
          if (matches.length > 1) {
            return { matches, truncated: false };
          }
        }
      }
    }
  }

  return { matches, truncated: false };
}

function isPathInsideRoot(path: string, root: string): boolean {
  const relativePath = relative(root, path);
  return (
    relativePath === "" ||
    (!relativePath.startsWith("..") && !isAbsolute(relativePath))
  );
}

function toWorkspaceSlash(path: string): string {
  // Backslashes are filename characters on POSIX, not directory separators.
  return path.split(sep).join("/");
}

function sha256Hex(buffer: Buffer): string {
  return createHash("sha256").update(buffer).digest("hex");
}

function normalizeWorkspaceRelativePath(path: string): string {
  const value = toWorkspaceSlash(path)
    .replace(/^\/+/, "")
    .replace(/\/+$/, "");
  if (
    !value ||
    value.includes("\0") ||
    value.split("/").some((segment) => segment === "..")
  ) {
    throw new Error("invalid workspace file path");
  }
  return value;
}

function normalizeWorkspaceDirectoryPath(path: string): string {
  const value = toWorkspaceSlash(path)
    .replace(/^\/+/, "")
    .replace(/\/+$/, "");
  if (
    value.includes("\0") ||
    value.split("/").some((segment) => segment === "..")
  ) {
    throw new Error("invalid workspace directory path");
  }
  return value;
}

function resolveWorkspacePath(root: string, relativeFilePath: string): string {
  const absolutePath = resolve(root, relativeFilePath);
  const relativeToRoot = relative(root, absolutePath);
  if (
    !relativeToRoot ||
    relativeToRoot.startsWith("..") ||
    isAbsolute(relativeToRoot)
  ) {
    throw new Error("file is outside the current workspace");
  }

  const realRoot = realpathSync(root);
  const realFile = realpathSync(absolutePath);
  const realRelative = relative(realRoot, realFile);
  if (
    !realRelative ||
    realRelative.startsWith("..") ||
    isAbsolute(realRelative)
  ) {
    throw new Error("file is outside the current workspace");
  }
  return absolutePath;
}

function resolveWorkspaceDirectoryPath(
  root: string,
  relativeDirectoryPath: string,
): string {
  const absolutePath = relativeDirectoryPath
    ? resolve(root, relativeDirectoryPath)
    : root;
  const relativeToRoot = relative(root, absolutePath);
  if (
    relativeToRoot &&
    (relativeToRoot.startsWith("..") || isAbsolute(relativeToRoot))
  ) {
    throw new Error("directory is outside the current workspace");
  }

  const realRoot = realpathSync(root);
  const realDirectory = realpathSync(absolutePath);
  const realRelative = relative(realRoot, realDirectory);
  if (
    realRelative &&
    (realRelative.startsWith("..") || isAbsolute(realRelative))
  ) {
    throw new Error("directory is outside the current workspace");
  }
  return absolutePath;
}

function compareFileTreeEntries(left: Dirent, right: Dirent): number {
  const leftDirectory = left.isDirectory();
  const rightDirectory = right.isDirectory();
  if (leftDirectory !== rightDirectory) {
    return leftDirectory ? -1 : 1;
  }
  return left.name.localeCompare(right.name, undefined, {
    sensitivity: "base",
  });
}

function collectFileTreePaths(
  root: string,
  relativeDirectory: string,
  paths: string[],
): boolean {
  if (paths.length >= FILE_TREE_MAX_PATHS) {
    return true;
  }

  const directoriesToRead = [relativeDirectory];
  for (let index = 0; index < directoriesToRead.length; index += 1) {
    const currentRelativeDirectory = directoriesToRead[index];
    const directory = currentRelativeDirectory
      ? join(root, currentRelativeDirectory)
      : root;
    let entries: Dirent[];
    try {
      entries = readdirSync(directory, { withFileTypes: true });
    } catch {
      continue;
    }

    entries.sort(compareFileTreeEntries);

    for (const entry of entries) {
      if (FILE_TREE_IGNORED_FILES.has(entry.name)) {
        continue;
      }

      const relativePath = currentRelativeDirectory
        ? `${currentRelativeDirectory}/${entry.name}`
        : entry.name;
      if (entry.isDirectory()) {
        if (FILE_TREE_IGNORED_DIRS.has(entry.name)) {
          continue;
        }
        paths.push(`${relativePath}/`);
        directoriesToRead.push(relativePath);
      } else if (entry.isFile() || entry.isSymbolicLink()) {
        paths.push(relativePath);
      }

      if (paths.length >= FILE_TREE_MAX_PATHS) {
        return true;
      }
    }
  }

  return false;
}
