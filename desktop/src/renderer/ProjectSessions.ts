import type { Thread } from "../shared/protocol";
import { isThreadExecuting, isThreadUnread, type ThreadSummary } from "./AppState";

// Wire values of Thread.source for a project coordinator and the sessions it manages.
export const PROJECT_SOURCE = "project";
export const PROJECT_SESSION_SOURCE = "project-session";

export function isProjectCoordinator(thread: Pick<Thread, "source">): boolean {
  return thread.source === PROJECT_SOURCE;
}

// What a project row shows for its hidden sessions.
export type ProjectRowSummary = {
  running: boolean;
  unread: boolean;
  pendingCandidates: number;
};

export type ProjectDirectory = {
  // Live coordinators: loaded and not archived.
  projects: ThreadSummary[];
  sessionsByProjectID: ReadonlyMap<string, ThreadSummary[]>;
  summaries: ReadonlyMap<string, ProjectRowSummary>;
  // Sessions shown under their project instead of their workspace.
  managedSessionIDs: ReadonlySet<string>;
};

/**
 * Groups the loaded conversations into projects. A session whose project is
 * archived, deleted or not loaded is an ordinary conversation.
 */
export function projectDirectory(
  threads: readonly ThreadSummary[],
  lastViewedTurnByThreadID: Record<string, string>,
  activeThreadID?: string,
): ProjectDirectory {
  const projects = threads.filter((thread) => isProjectCoordinator(thread) && !thread.archived);
  const projectIDs = new Set(projects.map((thread) => thread.id));
  const sessionsByProjectID = new Map<string, ThreadSummary[]>();
  const managedSessionIDs = new Set<string>();
  for (const thread of threads) {
    const projectID = thread.source === PROJECT_SESSION_SOURCE ? thread.project_id : undefined;
    if (!projectID || !projectIDs.has(projectID) || thread.archived) continue;
    managedSessionIDs.add(thread.id);
    const sessions = sessionsByProjectID.get(projectID);
    if (sessions) sessions.push(thread);
    else sessionsByProjectID.set(projectID, [thread]);
  }
  const unread = (thread: ThreadSummary): boolean => thread.id !== activeThreadID &&
    !isThreadExecuting(thread) && isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
  const summaries = new Map<string, ProjectRowSummary>();
  for (const project of projects) {
    const rows = [project, ...(sessionsByProjectID.get(project.id) ?? [])];
    summaries.set(project.id, {
      running: rows.some(isThreadExecuting),
      unread: rows.some(unread),
      pendingCandidates: project.pending_candidates ?? 0,
    });
  }
  return { projects, sessionsByProjectID, summaries, managedSessionIDs };
}

/**
 * A project's live managed sessions, oldest first, so a session that starts
 * or stops running never moves.
 */
export function projectSessionsOf<T extends ThreadSummary | Thread>(projectID: string, threads: readonly T[]): T[] {
  return threads
    .filter((thread) => thread.source === PROJECT_SESSION_SOURCE && thread.project_id === projectID && !thread.archived)
    .sort((a, b) => (Date.parse(a.created_at) || 0) - (Date.parse(b.created_at) || 0) || a.id.localeCompare(b.id));
}
