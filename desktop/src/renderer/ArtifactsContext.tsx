import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationArtifactsV1 } from "../../../packages/plugin-sdk/src/ui-artifacts";
const ArtifactsContext = createContext<ApplicationArtifactsV1 | undefined>(undefined);
export function ArtifactsProvider({ application, children }: { application: { readonly artifacts: ApplicationArtifactsV1 }; children: ReactNode }): JSX.Element {
  return <ArtifactsContext.Provider value={application.artifacts}>{children}</ArtifactsContext.Provider>;
}
export function useArtifacts(): ApplicationArtifactsV1 {
  const service = useContext(ArtifactsContext);
  if (!service) throw new Error("ArtifactsProvider requires the public application artifact service");
  return service;
}
