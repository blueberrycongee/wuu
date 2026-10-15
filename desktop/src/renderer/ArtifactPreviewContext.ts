import { createContext } from "react";
import type { TurnArtifact } from "./ArtifactOutputs";

export type ArtifactPreviewRequest = {
  threadID: string;
  cwd?: string;
  artifact: TurnArtifact;
  origin?: WeakRef<HTMLElement>;
  page?: number;
  pageRequest?: string;
};

export const ArtifactPreviewContext = createContext<((request: ArtifactPreviewRequest) => void) | undefined>(undefined);
export const ArtifactThreadContext = createContext<string | undefined>(undefined);

export function restoreArtifactPreviewFocus(origin?: WeakRef<HTMLElement>): void {
  const node = origin?.deref();
  if (!node?.isConnected || node.closest('[inert], [aria-hidden="true"]')
    || !document.activeElement?.closest(".workspace-right-panel")) return;
  const rect = node.getBoundingClientRect();
  if (rect.width > 0 && rect.height > 0 && rect.bottom > 0 && rect.top < innerHeight
    && rect.right > 0 && rect.left < innerWidth) {
    node.focus({ preventScroll: true });
  }
}
