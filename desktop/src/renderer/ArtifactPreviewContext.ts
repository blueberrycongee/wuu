import { createContext, useContext } from "react";
import type { TurnArtifact } from "./ArtifactOutputs";
import { createArtifactPreviewMotion, type ArtifactPreviewMotion } from "./ArtifactPreviewMotion";

export type ArtifactPreviewRequest = {
  threadID: string;
  cwd?: string;
  artifact: TurnArtifact;
  motion?: ArtifactPreviewMotion;
};

export const ArtifactPreviewContext = createContext<((request: ArtifactPreviewRequest) => void) | undefined>(undefined);
export const ArtifactThreadContext = createContext<string | undefined>(undefined);

export function useArtifactPreview(
  fallback: (artifact: TurnArtifact, origin?: HTMLElement) => void,
  cwd?: string,
): (artifact: TurnArtifact, origin?: HTMLElement) => void {
  const open = useContext(ArtifactPreviewContext);
  const threadID = useContext(ArtifactThreadContext);
  return (artifact, origin) => {
    if (open && threadID) open({ threadID, cwd, artifact, ...(origin ? { motion: createArtifactPreviewMotion(origin, artifact.name) } : {}) });
    else fallback(artifact, origin);
  };
}
