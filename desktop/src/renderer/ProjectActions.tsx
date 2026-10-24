import { createContext, useContext, useMemo, useSyncExternalStore, type ReactNode } from "react";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { NavigationThreadV1 } from "../../../packages/plugin-sdk/src/ui-navigation";
import { baseThreadTitle } from "./ThreadTitles";
import { showErrorToast } from "./Toast";

export type ProjectThread = Pick<NavigationThreadV1,
  "id" | "title" | "preview" | "forked_from_id" | "source" | "project_id" | "project_role" |
  "archived" | "created_at" | "status" | "child_agents"> & {
    readonly turns: readonly Pick<NavigationThreadV1["turns"][number], "status">[];
  };
type ProjectApplication = ApplicationServiceV1;
export type ProjectActions = {
  threads: readonly ProjectThread[];
  openThread: (threadID: string) => void;
  openProjectPanel: (project: ProjectThread) => void;
  release: (session: ProjectThread) => void;
};
const ProjectActionsContext = createContext<ProjectActions | null>(null);
const ProjectApplicationContext = createContext<ProjectApplication | null>(null);

export function ProjectActionsProvider({ application, children }: { application: ProjectApplication; children: ReactNode }): JSX.Element {
  const view = useSyncExternalStore(application.subscribe, application.getSnapshot).navigationWorkspace;
  const actions = useMemo<ProjectActions>(() => ({
    threads: view.threads,
    openThread: (threadId) => {
      void application.invoke("navigation.show-native", {})
        .then(() => application.invoke("thread.activate", { threadId })).catch(showErrorToast);
    },
    openProjectPanel: (project) => {
      void application.invoke("preview.open-project", { projectId: project.id, title: baseThreadTitle(project) }).catch(showErrorToast);
    },
    release: (session) => {
      if (session.project_id) void application.projects.releaseSession({ projectId: session.project_id, threadId: session.id }).catch(showErrorToast);
    },
  }), [application, view.threads]);
  return <ProjectApplicationContext.Provider value={application}>
    <ProjectActionsContext.Provider value={view.projectAgentEnabled ? actions : null}>{children}</ProjectActionsContext.Provider>
  </ProjectApplicationContext.Provider>;
}
export function useProjectActions(): ProjectActions | null { return useContext(ProjectActionsContext); }
export function useProjectApplication(): ProjectApplication {
  const application = useContext(ProjectApplicationContext);
  if (!application) throw new Error("Project views require an application service");
  return application;
}
