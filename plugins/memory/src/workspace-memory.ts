import { readdir, readFile, stat, unlink } from "node:fs/promises";
import { basename, join, relative, sep } from "node:path";
import { acquireFileLock, releaseFileLock, type JSONSchemaObject, type RuntimeExecutionContext, type RuntimeHost } from "@wuu/plugin-sdk";
import { INDEX_FILE, MAX_MEMORY_BYTES, missing, unsafeLine, writeAtomicFile } from "./notebook.js";

const READ_MAX_BYTES = 64 * 1024;
const targets = ["project_memory", "summary", "checkpoint", "notes"];
export interface SessionMemoryArgs { action: string; target: string; content: string; source: string; file: string }

export function sessionMemorySchema(): JSONSchemaObject {
  return {
    type: "object",
    properties: {
      action: { type: "string", enum: ["status", "read", "append", "replace", "delete"] },
      target: { type: "string", enum: targets },
      file: { type: "string", description: "project_memory topic page such as build.md; omit for its MEMORY.md index. delete requires a page." },
      content: { type: "string", description: "Markdown content for append or replace." },
      source: { type: "string", description: "Optional source label such as agent, dream, workflow, or user." },
    },
    required: ["action"], additionalProperties: false,
  };
}

export async function runSessionMemory(stateDir: string, args: SessionMemoryArgs, threadId: string, lifecycleHost: RuntimeHost, execution: RuntimeExecutionContext): Promise<unknown> {
  if (!stateDir.trim()) throw new Error("memory plugin requires workspace_state_dir");
  threadId = threadId.trim();
  const projectDir = join(stateDir, "memory");
  const paths: Record<string, string> = { project_memory: join(projectDir, INDEX_FILE) };
  if (threadId) {
    if (basename(threadId) !== threadId || threadId === "." || threadId === "..") throw new Error("memory plugin requires a safe session id");
    const root = join(stateDir, "sessions", threadId);
    paths.summary = join(root, "session-memory", "summary.md");
    paths.checkpoint = join(root, "memory", "checkpoint.md");
    paths.notes = join(root, "memory", "notes.md");
  }
  switch (args.action.trim()) {
    case "status": {
      const files: { target: string; path: string; exists: boolean; bytes: number }[] = [];
      for (const target of targets) {
        const path = paths[target] ?? "";
        let info;
        try { info = await stat(path); }
        catch (error) { if (!missing(error)) throw error; }
        files.push({ target, path, exists: info !== undefined, bytes: info?.size ?? 0 });
      }
      const project_pages: { file: string; bytes: number }[] = [];
      for (const entry of await readdir(projectDir, { withFileTypes: true }).catch((error: unknown) => { if (missing(error)) return []; throw error; })) {
        if (!entry.isFile() || !projectPage(entry.name)) continue;
        project_pages.push({ file: entry.name, bytes: (await stat(join(projectDir, entry.name))).size });
      }
      project_pages.sort((a, b) => Buffer.compare(Buffer.from(a.file), Buffer.from(b.file)));
      return { action: "status", files, project_pages };
    }
    case "read": {
      const [target, path] = checkedTarget(args, paths, projectDir);
      let content = Buffer.alloc(0);
      let exists = false;
      try { content = await readFile(path); exists = true; }
      catch (error) { if (!missing(error)) throw error; }
      const truncated = content.length > READ_MAX_BYTES;
      return { action: "read", target, path, exists, content: truncated ? headTailText(content) : content.toString(), truncated };
    }
    case "append":
    case "replace":
    case "delete": {
      const [target, path] = checkedTarget(args, paths, projectDir);
      const content = args.content.trim();
      if (args.action === "delete") {
        if (!args.file.trim()) throw new Error("session_memory delete requires a project_memory topic file");
      } else {
        if (!content) throw new Error("session_memory content is required");
        if (args.content.split("\n").some(unsafeLine)) throw new Error("session_memory content failed the prompt-injection safety scan");
        if (Buffer.byteLength(content) > MAX_MEMORY_BYTES) throw new Error(`session_memory content exceeds ${MAX_MEMORY_BYTES} bytes`);
      }
      execution.signal.throwIfAborted();
      // Wait for the authoritative acquire reply, even if cancellation races
      // the grant. Otherwise the caller could lose the handle before releasing.
      const lock = await acquireFileLock(lifecycleHost, { root: "workspace_state", path: relative(stateDir, path + ".lock").split(sep).join("/") }, execution.executionId);
      try {
        execution.signal.throwIfAborted();
        if (args.action === "delete") {
          let deleted = true;
          await unlink(path).catch((error: unknown) => { if (!missing(error)) throw error; deleted = false; });
          return { action: "delete", target, file: args.file.trim(), path, deleted };
        }
        let next: string;
        if (args.action === "append") {
          let previous = "";
          try { previous = await readFile(path, "utf8"); }
          catch (error) { if (!missing(error)) throw error; }
          const existing = previous.trim() || sessionMemoryTemplate(target);
          const source = args.source.trim() || "agent";
          const header = new Date().toISOString().replace(/\.\d{3}Z$/, "Z") + " (" + source + ")";
          next = existing.replace(/\n+$/, "") + "\n\n## " + header + "\n\n" + content + "\n";
        } else {
          next = content + "\n";
        }
        await writeAtomicFile(path, next.trim() + "\n", 0o644, true);
        return { action: args.action, target, path, written: true, length: Buffer.byteLength(next) };
      } finally {
        // Cleanup is generation-scoped, independent of the cancelled execution.
        await releaseFileLock(lifecycleHost, lock.lock_id);
      }
    }
    default: throw new Error("session_memory action must be status, read, append, replace, or delete");
  }
}

function projectPage(file: string): boolean {
  return file === basename(file) && !file.startsWith(".") && !/[\\/]/.test(file) && file.endsWith(".md") && file.toLowerCase() !== INDEX_FILE.toLowerCase();
}

function checkedTarget(args: SessionMemoryArgs, paths: Record<string, string>, projectDir: string): [string, string] {
  const target = args.target.trim(), file = args.file.trim();
  if (!Object.hasOwn(paths, target)) throw new Error(`unknown session_memory target ${JSON.stringify(target)}`);
  if (!file) return [target, paths[target]!];
  if (target !== "project_memory") throw new Error("session_memory file is only valid for project_memory");
  if (!projectPage(file)) throw new Error("session_memory file must be a plain, non-hidden .md topic page other than MEMORY.md");
  return [target, join(projectDir, file)];
}

function headTailText(bytes: Buffer): string {
  let headEnd = READ_MAX_BYTES / 2;
  while (headEnd > 0 && (bytes[headEnd]! & 0xc0) === 0x80) headEnd--;
  let tailStart = bytes.length - READ_MAX_BYTES / 2;
  while (tailStart < bytes.length && (bytes[tailStart]! & 0xc0) === 0x80) tailStart++;
  return bytes.subarray(0, headEnd).toString() + "\n\n[trimmed session memory]\n\n" + bytes.subarray(tailStart).toString();
}

function sessionMemoryTemplate(target: string): string {
  switch (target) {
    case "project_memory": return "# Project Memory\n\nDurable workspace facts that should survive across sessions.\n\n## Project Context\n\n## Rules\n\n## Architecture Decisions\n\n## Discovered Durable Knowledge\n";
    case "summary":
    case "checkpoint": return "# " + (target === "summary" ? "Session Summary" : "Session Checkpoint") + "\n\nCompact recoverable state for the active session.\n\n## Active Intent\n\n## Next Action\n\n## Current Work\n\n## Decisions\n\n## Open Questions\n";
    case "notes": return "# Session Notes\n\nScratch notes for this session. Promote durable facts to project memory or user memory.\n";
    default: return "";
  }
}
