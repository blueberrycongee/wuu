import { createContext, useContext } from "react";
import { COMPOSER_ACTIONS, type RendererInteractionSnapshotV1, type RendererSubmissionV1 } from "../shared/workbench";
import type { TurnArtifact } from "./ArtifactOutputs";

/** The source is captured by the artifact renderer, never supplied by a plugin action. */
export interface ArtifactInteractionSource {
  readonly threadId: string;
  readonly artifact: TurnArtifact;
}

export interface ArtifactInteractionController {
  getSnapshot(source: ArtifactInteractionSource): RendererInteractionSnapshotV1;
  submit(source: ArtifactInteractionSource, input: RendererSubmissionV1, isCurrent: () => boolean): Promise<void>;
}

export const ArtifactInteractionContext = createContext<ArtifactInteractionController | undefined>(undefined);
const NO_ACTIONS: readonly string[] = Object.freeze([]);
const SUBMIT_ACTIONS: readonly string[] = Object.freeze([COMPOSER_ACTIONS.submit]);

export function useArtifactInteraction(artifact: TurnArtifact, threadId?: string) {
  const controller = useContext(ArtifactInteractionContext);
  const source = threadId ? { threadId, artifact } : undefined;
  const snapshot: RendererInteractionSnapshotV1 = controller && source ? controller.getSnapshot(source) : {
    contractVersion: 1, threadId, running: false, readOnly: true,
    disabledReason: "Open the originating conversation to respond",
  };
  return {
    snapshot,
    actions: snapshot.disabledReason || snapshot.running || snapshot.readOnly ? NO_ACTIONS : SUBMIT_ACTIONS,
    dispatchAction: async (action: string, input: unknown, isCurrent: () => boolean): Promise<{ accepted: true }> => {
      if (action !== COMPOSER_ACTIONS.submit) throw new Error(`Unsupported artifact action: ${action}`);
      if (!controller || !source || !isCurrent()) throw new Error("Artifact action source is no longer active");
      const submission = parseRendererSubmission(input);
      await controller.submit(source, submission, isCurrent);
      return { accepted: true };
    },
  };
}

function parseRendererSubmission(input: unknown): RendererSubmissionV1 {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("Content submission must be an object");
  const value = input as Record<string, unknown>;
  if (Object.keys(value).some(key => key !== "text" && key !== "context")
    || typeof value.text !== "string" || !value.text.trim() || value.text.length > 8_000) {
    throw new Error("Content submission requires text of at most 8,000 characters");
  }
  if (value.context === undefined) return { text: value.text.trim() };
  const json = JSON.stringify(value.context, (_key, item: unknown) => {
    if (item === undefined || typeof item === "function" || typeof item === "symbol"
      || typeof item === "bigint" || (typeof item === "number" && !Number.isFinite(item))) {
      throw new Error("Content submission context must be JSON");
    }
    return item;
  });
  if (json.length > 16_384) throw new Error("Content submission context exceeds 16,384 characters");
  return { text: value.text.trim(), context: JSON.parse(json) as unknown };
}
