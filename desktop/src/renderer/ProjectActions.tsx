import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import type { ProjectCandidate, Thread } from "../shared/protocol";
import type { ThreadSummary } from "./AppState";
import { showErrorToast } from "./Toast";

export type ProjectThread = Thread | ThreadSummary;

/**
 * Project navigation and decisions for views deep in the conversation tree
 * and the right panel, which the app shell owns.
 */
export type ProjectActions = {
  // Every conversation the renderer knows, across workspaces.
  threads: readonly ProjectThread[];
  openThread: (threadID: string) => void;
  openProjectPanel: (project: ProjectThread) => void;
  openProposal: (session: ProjectThread) => void;
  takeOver: (session: ProjectThread) => void;
  returnToProject: (session: ProjectThread) => void;
  release: (session: ProjectThread) => void;
};

const ProjectActionsContext = createContext<ProjectActions | null>(null);

export function ProjectActionsProvider({ value, children }: { value: ProjectActions; children: ReactNode }): JSX.Element {
  return <ProjectActionsContext.Provider value={value}>{children}</ProjectActionsContext.Provider>;
}

export function useProjectActions(): ProjectActions | null {
  return useContext(ProjectActionsContext);
}

/**
 * The candidates of one managed session or of a whole project, newest first,
 * reloaded whenever `refreshKey` changes. The host announces a frozen or
 * decided candidate through the thread's pending count, so callers derive
 * the key from it and the session's latest turn.
 */
export function useProjectCandidates(
  scope: { session_id: string } | { project_id: string } | undefined,
  refreshKey: string,
): { candidates: ProjectCandidate[]; reload: () => Promise<void> } {
  const [candidates, setCandidates] = useState<ProjectCandidate[]>([]);
  const scopeKey = scope ? JSON.stringify(scope) : "";
  const load = useCallback(async (isCurrent: () => boolean = () => true): Promise<void> => {
    if (!scopeKey || !window.wuu.projectCandidate) {
      // Keep the same empty list so conversations outside projects never re-render.
      setCandidates((current) => current.length ? [] : current);
      return;
    }
    try {
      const result = await window.wuu.projectCandidate({ action: "list", ...JSON.parse(scopeKey) });
      if (!isCurrent()) return;
      setCandidates([...(result.candidates ?? [])].sort((a, b) =>
        (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0)));
    } catch (error) {
      if (isCurrent()) showErrorToast(error);
    }
  }, [scopeKey]);
  useEffect(() => {
    let current = true;
    void load(() => current);
    return () => { current = false; };
  }, [load, refreshKey]);
  return { candidates, reload: () => load() };
}
