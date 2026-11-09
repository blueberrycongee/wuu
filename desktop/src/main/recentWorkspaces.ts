import { open, readdir, realpath, stat } from "node:fs/promises";
import { homedir } from "node:os";
import { basename, isAbsolute, join, relative, resolve } from "node:path";
import type { RecentWorkspace, RecentWorkspacesResult } from "../shared/protocol";

const MAX_FILES_PER_SOURCE = 1500;
const MAX_DIRECTORIES_PER_SOURCE = 2000;
const MAX_PREFIX_BYTES = 128 * 1024;
const MAX_SCAN_BYTES = 32 * 1024 * 1024;

/** Read local session metadata without importing conversations or modifying their stores. */
export async function discoverRecentWorkspaces(options: {
  homeDir?: string;
  codexHome?: string;
  claudeHome?: string;
  existingPaths?: readonly string[];
} = {}): Promise<RecentWorkspacesResult> {
  const home = options.homeDir ?? homedir();
  const candidates = new Map<string, RecentWorkspace>();
  const existing = new Set(await Promise.all((options.existingPaths ?? []).map(
    (path) => realpath(path).catch(() => resolve(path)),
  )));
  const excluded = await Promise.all([home, join(home, "Downloads"), join(home, "Documents", "Codex")]
    .map((path) => realpath(path).catch(() => resolve(path))));
  let incomplete = false;
  let bytesRead = 0;
  const expand = (path: string) => path === "~" ? home : path.startsWith("~/") ? join(home, path.slice(2)) : resolve(path);
  const sources = [
    { source: "codex" as const, root: expand(options.codexHome ?? join(home, ".codex")), folders: ["sessions", "archived_sessions"], depth: 3 },
    { source: "claude" as const, root: expand(options.claudeHome ?? join(home, ".claude")), folders: ["projects"], depth: 1 },
  ];

  for (const source of sources) {
    let fileCount = 0;
    let directoryCount = 0;
    const files: Array<{ path: string; modified: number }> = [];
    async function collect(directory: string, depth: number): Promise<void> {
      if (++directoryCount > MAX_DIRECTORIES_PER_SOURCE || fileCount >= MAX_FILES_PER_SOURCE) {
        incomplete = true;
        return;
      }
      try {
        const entries = await readdir(directory, { withFileTypes: true });
        // Date directories sort newest first so a bounded scan favors current work.
        entries.sort((a, b) => b.name.localeCompare(a.name));
        for (const entry of entries) {
          if (fileCount >= MAX_FILES_PER_SOURCE || directoryCount > MAX_DIRECTORIES_PER_SOURCE) {
            incomplete = true;
            break;
          }
          const path = join(directory, entry.name);
          if (entry.isDirectory() && depth > 0) await collect(path, depth - 1);
          else if (entry.isFile() && entry.name.endsWith(".jsonl")) {
            fileCount++;
            try { files.push({ path, modified: (await stat(path)).mtimeMs }); }
            catch { incomplete = true; }
          }
        }
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code !== "ENOENT") incomplete = true;
      }
    }
    for (const folder of source.folders) await collect(join(source.root, folder), source.depth);
    files.sort((a, b) => b.modified - a.modified);
    for (const file of files) {
      if (bytesRead >= MAX_SCAN_BYTES) { incomplete = true; break; }
      let cwd: string | undefined;
      try {
        const handle = await open(file.path, "r");
        try {
          const buffer = Buffer.alloc(Math.min(MAX_PREFIX_BYTES, MAX_SCAN_BYTES - bytesRead));
          const read = await handle.read(buffer, 0, buffer.length, 0);
          bytesRead += read.bytesRead;
          for (const line of buffer.subarray(0, read.bytesRead).toString("utf8").split("\n")) {
            try {
              const record = JSON.parse(line);
              const path = source.source === "codex"
                ? record?.type === "session_meta" ? record.payload?.cwd : undefined
                : record?.cwd;
              if (typeof path === "string" && isAbsolute(path)) { cwd = path; break; }
            } catch { /* A damaged record does not invalidate the remaining metadata. */ }
          }
          if (!cwd && (await handle.stat()).size >= buffer.length) incomplete = true;
        } finally { await handle.close(); }
      } catch { incomplete = true; }
      if (!cwd) continue;
      try {
        const path = await realpath(cwd);
        if (existing.has(path) || path === excluded[0] || excluded.slice(1).some((root) => {
          const child = relative(root, path);
          return child === "" || (!child.startsWith("..") && !isAbsolute(child));
        }) || !(await stat(path)).isDirectory()) continue;
        const git = await stat(join(path, ".git")).catch(() => undefined);
        // Linked worktrees are execution checkouts, not standalone workspace suggestions.
        if (git?.isFile()) continue;
        const previous = candidates.get(path);
        const lastUsedAt = new Date(file.modified).toISOString();
        if (previous) {
          previous.sessionCount++;
          if (!previous.sources.includes(source.source)) previous.sources.push(source.source);
          if (lastUsedAt > previous.lastUsedAt) previous.lastUsedAt = lastUsedAt;
        } else {
          candidates.set(path, {
            path, name: basename(path), sources: [source.source],
            lastUsedAt, sessionCount: 1, isRepository: git?.isDirectory() ?? false,
          });
        }
      } catch { /* Historical paths can have been deleted or moved. */ }
    }
  }
  return {
    candidates: [...candidates.values()].sort((a, b) => b.lastUsedAt.localeCompare(a.lastUsedAt) || a.path.localeCompare(b.path)),
    incomplete,
  };
}
