import type { Thread } from "../shared/protocol";
import { isThreadExecuting, type ThreadSummary } from "./AppState";

// Wire values of Thread.source for a project coordinator and the sessions it manages.
export const PROJECT_SOURCE = "project";
export const PROJECT_SESSION_SOURCE = "project-session";

export function isProjectCoordinator(thread: Pick<Thread, "source">): boolean {
  return thread.source === PROJECT_SOURCE;
}

export function sortProjectSessions<T extends ThreadSummary | Thread>(threads: readonly T[]): T[] {
  return [...threads].sort((a, b) => Number(isThreadExecuting(b)) - Number(isThreadExecuting(a))
    || (Date.parse(b.updated_at) || 0) - (Date.parse(a.updated_at) || 0) || a.id.localeCompare(b.id));
}

export type ProjectSessionNesting = {
  // The list without the sessions that render under their project.
  rows: ThreadSummary[];
  sessionsByProjectID: ReadonlyMap<string, ThreadSummary[]>;
};

/**
 * Nests each managed session under its project. A session whose project is
 * not shown (archived, deleted, or not loaded) stays an ordinary row.
 * `projectIDs` defaults to the projects in `threads`; callers pass the
 * projects shown elsewhere in the sidebar so their sessions follow them.
 */
export function nestProjectSessions(
  threads: readonly ThreadSummary[],
  projectIDs: ReadonlySet<string> = new Set(threads.filter(isProjectCoordinator).map((thread) => thread.id)),
): ProjectSessionNesting {
  const rows: ThreadSummary[] = [];
  const sessionsByProjectID = new Map<string, ThreadSummary[]>();
  for (const thread of threads) {
    const projectID = thread.source === PROJECT_SESSION_SOURCE ? thread.project_id : undefined;
    if (!projectID || !projectIDs.has(projectID)) {
      rows.push(thread);
      continue;
    }
    const sessions = sessionsByProjectID.get(projectID);
    if (sessions) sessions.push(thread);
    else sessionsByProjectID.set(projectID, [thread]);
  }
  // Nested rows keep the order the coordinator started them, so a session that
  // starts or stops running never moves under the pointer.
  for (const sessions of sessionsByProjectID.values()) {
    sessions.sort((a, b) => (Date.parse(a.created_at) || 0) - (Date.parse(b.created_at) || 0) || a.id.localeCompare(b.id));
  }
  return { rows, sessionsByProjectID };
}

/** A project's live managed sessions among the loaded conversations. */
export function projectSessionsOf<T extends ThreadSummary | Thread>(projectID: string, threads: readonly T[]): T[] {
  return sortProjectSessions(threads.filter((thread) =>
    thread.source === PROJECT_SESSION_SOURCE && thread.project_id === projectID && !thread.archived));
}
