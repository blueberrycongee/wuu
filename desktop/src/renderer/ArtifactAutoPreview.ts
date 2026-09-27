import { useLayoutEffect, useRef } from "react";
import type { Thread, Turn } from "../shared/protocol";
import { collectTurnArtifacts, type TurnArtifact } from "./ArtifactOutputs";
import type { ArtifactPreviewRequest } from "./ArtifactPreviewContext";

export function automaticPreviewArtifact(turn: Turn): TurnArtifact | undefined {
  if (turn.status !== "completed") return undefined;
  const files = collectTurnArtifacts(turn).filter((artifact) => artifact.delivered && artifact.type !== "text");
  if (files.length !== 1) return undefined;
  const artifact = files[0];
  const supported = artifact.mimeType === "application/pdf"
    || artifact.mimeType.startsWith("text/")
    || artifact.mimeType.startsWith("image/");
  const limit = artifact.mimeType.startsWith("text/") && artifact.mimeType !== "text/html"
    ? 2 * 1024 * 1024 : 20 * 1024 * 1024;
  // Automatic reads are restricted to immutable, host-managed deliveries.
  // References, external URLs and editable workspace paths need a user action.
  if (!supported || !artifact.uri?.startsWith("wuu-artifact://") || !artifact.sha256
    || artifact.sizeBytes === undefined || artifact.sizeBytes < 0 || artifact.sizeBytes > limit) return undefined;
  return artifact;
}

type Observation = {
  key: string;
  status: Turn["status"];
  panel: string;
  suppressed: boolean;
};

export function useArtifactAutoPreview({
  thread, enabled, panelOpen, activeTabID, onOpen,
}: {
  thread?: Thread;
  enabled: boolean;
  panelOpen: boolean;
  activeTabID?: string;
  onOpen: (request: ArtifactPreviewRequest) => void;
}): void {
  const observation = useRef<Observation | undefined>(undefined);
  const pending = useRef<AbortController | undefined>(undefined);
  const open = useRef(onOpen);
  useLayoutEffect(() => {
    open.current = onOpen;
    const turn = thread?.turns.at(-1);
    const key = JSON.stringify([thread?.id, thread?.cwd, turn?.id]);
    const panel = JSON.stringify([panelOpen, activeTabID]);
    const previous = observation.current;
    const sameTurn = previous?.key === key;
    const suppressed = !enabled || panelOpen
      || Boolean(sameTurn && (previous.suppressed || previous.panel !== panel));
    if (!sameTurn || suppressed || turn?.status !== "completed") {
      pending.current?.abort();
      pending.current = undefined;
    }
    observation.current = turn ? { key, status: turn.status, panel, suppressed } : undefined;
    // History hydration, later item updates and revisiting a completed turn
    // must not replay a completion or reopen a dismissed preview.
    if (!thread || !turn || !sameTurn || suppressed
      || previous.status !== "in_progress" || turn.status !== "completed") return;
    const artifact = automaticPreviewArtifact(turn);
    if (!artifact) return;
    const controller = new AbortController();
    pending.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 5_000);
    void (async () => {
      try {
        const response = await fetch(artifact.uri!, {
          signal: controller.signal,
          headers: { Range: "bytes=0-0" },
          redirect: "error",
        });
        await response.body?.cancel();
        if (response.ok && !controller.signal.aborted && pending.current === controller) {
          pending.current = undefined;
          open.current({ threadID: thread.id, cwd: thread.cwd, artifact });
        }
      } catch {
        // An unavailable snapshot stays on its output card for manual opening.
      } finally {
        window.clearTimeout(timeout);
        if (pending.current === controller) pending.current = undefined;
      }
    })();
  });
  useLayoutEffect(() => () => pending.current?.abort(), []);
}
