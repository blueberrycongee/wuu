import { createContext } from "react";
import type { WorkspaceFileContext, WorkspaceDirectoryListResult, WorkspaceFileReadResult } from "../shared/protocol";
export function workspacePathToSlash(path: string, root: string): string {
  // Use the workspace's path syntax; a remote host can differ from this client.
  return /^(?:[a-z]:[\\/]|[\\/]{2})/i.test(root) ? path.replace(/\\/g, "/") : path;
}

export function workspaceFileContextKey(context?: WorkspaceFileContext): string {
  return JSON.stringify([context?.cwd, context?.file_target?.thread_id, context?.file_target?.environment_id]);
}

// Capture the destination before creating a tab. The tab never follows a new
// conversation, global default, or host path with the same filename.
export async function bindWorkspaceFileContext(context: WorkspaceFileContext): Promise<WorkspaceFileContext> {
  if (!context.file_target || context.file_target.environment_id) return context;
  const read = window.wuu.getThreadExecutionEnvironment;
  if (!read) throw new Error("Session workspace inspection is unavailable");
  const result = await read(context.file_target.thread_id);
  if (result.thread_id !== context.file_target.thread_id) throw new Error("Session workspace changed");
  return {
    ...context,
    file_root: result.environment.root || context.cwd,
    file_target: { thread_id: result.thread_id, environment_id: result.environment.id || "local" },
  };
}

function checkWorkspaceTarget<T extends WorkspaceDirectoryListResult | WorkspaceFileReadResult>(result: T, context: WorkspaceFileContext): T {
  const expected = context.file_target;
  if (expected && (result.target?.thread_id !== expected.thread_id ||
    (expected.environment_id && result.target.environment_id !== expected.environment_id))) {
    throw new Error("Session execution destination changed; reopen the file view");
  }
  return result;
}

export async function listWorkspaceDirectoryInContext(path: string, context: WorkspaceFileContext): Promise<WorkspaceDirectoryListResult> {
  const result = context.file_target
    ? await window.wuu.listWorkspaceDirectory(path, context.cwd, context.file_target)
    : await window.wuu.listWorkspaceDirectory(path, context.cwd);
  return checkWorkspaceTarget(result, context);
}

export async function readWorkspaceFileInContext(path: string, context?: WorkspaceFileContext): Promise<WorkspaceFileReadResult> {
  const result = context?.file_target
    ? await window.wuu.readWorkspaceFile(path, context.cwd, context.file_target)
    : await window.wuu.readWorkspaceFile(path, context?.cwd);
  return context ? checkWorkspaceTarget(result, context) : result;
}

export const WorkspaceFileSource = createContext<{ context: WorkspaceFileContext; path: string } | undefined>(undefined);
