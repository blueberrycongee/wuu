import { createContext, useContext } from "react";
import type { TurnArtifact } from "./ArtifactOutputs";

export type ArtifactPreviewRequest = {
  threadID: string;
  cwd?: string;
  artifact: TurnArtifact;
};

export const ArtifactPreviewContext = createContext<((request: ArtifactPreviewRequest) => void) | undefined>(undefined);
export const ArtifactThreadContext = createContext<string | undefined>(undefined);

export function useArtifactPreview(fallback: (artifact: TurnArtifact) => void, cwd?: string): (artifact: TurnArtifact) => void {
  const open = useContext(ArtifactPreviewContext);
  const threadID = useContext(ArtifactThreadContext);
  return (artifact) => {
    if (open && threadID) open({ threadID, cwd, artifact });
    else fallback(artifact);
  };
}
