import { createContext, useContext, type ReactNode } from "react";
import type { Thread } from "../shared/protocol";
import type { ThreadSummary } from "./AppState";

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
  release: (session: ProjectThread) => void;
};

const ProjectActionsContext = createContext<ProjectActions | null>(null);

export function ProjectActionsProvider({ value, children }: { value: ProjectActions | null; children: ReactNode }): JSX.Element {
  return <ProjectActionsContext.Provider value={value}>{children}</ProjectActionsContext.Provider>;
}

export function useProjectActions(): ProjectActions | null {
  return useContext(ProjectActionsContext);
}
