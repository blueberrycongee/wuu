import { useCallback, useSyncExternalStore } from "react";
import type { ApplicationProjectWorkV1 } from "../../../packages/plugin-sdk/src/ui-projects";
import { useProjectApplication } from "./ProjectActions";

export function useProjectWork(projectID: string) {
  const { projects } = useProjectApplication();
  const subscribe = useCallback((listener: () => void) => projects.subscribe(projectID, listener), [projects, projectID]);
  const getSnapshot = useCallback(() => projects.getSnapshot(projectID), [projects, projectID]);
  const view = useSyncExternalStore(subscribe, getSnapshot);
  const control = useCallback(async (work: ApplicationProjectWorkV1, operation: "stop" | "resume") => {
    // The shared view publishes command errors for every mounted project observer.
    await projects.control({ projectId: projectID, workId: work.id, revision: work.revision, operation }).catch(() => {});
  }, [projects, projectID]);
  return { snapshot: view.snapshot, error: view.error, busy: view.busyWorkId ?? "", control };
}
