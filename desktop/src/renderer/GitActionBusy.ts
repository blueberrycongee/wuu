import { useSyncExternalStore } from "react";
import type { ApplicationEnvironmentV1 } from "../../../packages/plugin-sdk/src/ui-environment";

export function useGitActionBusy(environment: ApplicationEnvironmentV1): boolean {
  return useSyncExternalStore(
    listener => environment.subscribe(listener),
    () => environment.getSnapshot().actionBusy,
  );
}
