import { createContext, createElement, useContext, useMemo, useSyncExternalStore, type ReactNode } from "react";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationContentSourceV1 } from "../../../packages/plugin-sdk/src/ui-submission";
import type { RendererInteractionSnapshotV1, RendererSubmissionV1 } from "../shared/workbench";
import type { Thread } from "../shared/protocol";
import { isThreadRunning } from "./AppState";
import type { TurnArtifact } from "./ArtifactOutputs";
import { parseContentSubmission } from "./ContentSubmission";

/** The source is captured by the artifact renderer, never supplied by a plugin action. */
export interface ArtifactInteractionSource {
  readonly threadId: string;
  readonly artifact: TurnArtifact;
}

export interface ArtifactInteractionController {
  subscribe(listener: () => void): () => void;
  getSnapshot(source: ArtifactInteractionSource): RendererInteractionSnapshotV1;
  submit(source: ArtifactInteractionSource, input: RendererSubmissionV1, isCurrent: () => boolean, signal: AbortSignal): Promise<void>;
}

export const ArtifactInteractionContext = createContext<ArtifactInteractionController | undefined>(undefined);
const NO_ACTIONS: readonly string[] = Object.freeze([]);
const CONTENT_SUBMIT_ACTION = "conversation.composer.submit";
const SUBMIT_ACTIONS: readonly string[] = Object.freeze([CONTENT_SUBMIT_ACTION]);
const noSubscription = () => () => {};

// The provider wraps the whole shell. Its value stays fixed per application so a
// publication does not make React rescan the shell for consumers; artifact cards
// subscribe to the interaction state they render instead.
export function ArtifactInteractionProvider({ application, children }: { application: ApplicationServiceV1; children: ReactNode }): ReactNode {
  const controller = useMemo(() => createArtifactInteractionController(application), [application]);
  return createElement(ArtifactInteractionContext.Provider, { value: controller }, children);
}

function createArtifactInteractionController(application: ApplicationServiceV1): ArtifactInteractionController {
  const getSnapshot = (source: ArtifactInteractionSource): RendererInteractionSnapshotV1 => {
    const view = application.getSnapshot();
    const thread = view.conversationWorkspace.threads.find(candidate => candidate.id === source.threadId) as Thread | undefined;
    const turn = thread?.turns.find(candidate => candidate.items.some(item => item.id === source.artifact.itemId));
    const item = turn?.items.find(candidate => candidate.id === source.artifact.itemId);
    const readOnly = !thread || thread.read_only || thread.archived || source.artifact.preview === true
      || turn?.status !== "completed" || item?.status !== "completed" || item.read_only || Boolean(item.result_detail?.is_error);
    const running = Boolean(thread && (isThreadRunning(thread)
      || view.submission.operations.some(operation => operation.threadId === thread.id && ["preparing", "sending"].includes(operation.phase))
      || view.submission.stopRequests.some(operation => operation.threadId === thread.id)
      || view.composerPending.some(row => row.threadId === thread.id && (row.queued.length || row.guides.length))));
    const disabledReason = readOnly ? "This artifact is not an active completed source"
      : running ? "Wait for this conversation to finish"
      : view.phase !== "ready" || view.tabs.pending && !view.tabs.pending.contentReady ? "The conversation is not ready" : undefined;
    return { contractVersion: 1, threadId: source.threadId, running, readOnly: Boolean(readOnly), disabledReason };
  };
  const submit = async (source: ArtifactInteractionSource, input: RendererSubmissionV1, isCurrent: () => boolean, signal: AbortSignal): Promise<void> => {
    const current = getSnapshot(source);
    if (!isCurrent() || current.disabledReason) throw new Error(current.disabledReason ?? "Artifact source is no longer active");
    const artifact = source.artifact;
    const captured: ApplicationContentSourceV1 = { threadId: source.threadId, itemId: artifact.itemId, index: artifact.index,
      mimeType: artifact.mimeType, ref: artifact.ref, sha256: artifact.sha256, text: artifact.text, resource: artifact.resource };
    const result = await application.invoke("conversation.submit-content", { source: captured, ...input }, { signal });
    if (!result.accepted) throw new Error("The response was not sent. Your inputs have been kept.");
  };
  return { subscribe: application.subscribe, getSnapshot, submit };
}

export function useArtifactInteraction(artifact: TurnArtifact, threadId?: string) {
  const controller = useContext(ArtifactInteractionContext);
  const source = threadId ? { threadId, artifact } : undefined;
  // Select primitives: a fresh snapshot object per read would never settle.
  const state = useSyncExternalStore(controller?.subscribe ?? noSubscription, () => {
    if (!controller || !source) return "";
    const { running, readOnly, disabledReason } = controller.getSnapshot(source);
    return JSON.stringify([running, readOnly, disabledReason ?? null]);
  });
  const snapshot = useMemo<RendererInteractionSnapshotV1>(() => {
    if (!state) return { contractVersion: 1, threadId, running: false, readOnly: true, disabledReason: "Open the originating conversation to respond" };
    const [running, readOnly, disabledReason] = JSON.parse(state) as [boolean, boolean, string | null];
    return { contractVersion: 1, threadId, running, readOnly, disabledReason: disabledReason ?? undefined };
  }, [state, threadId]);
  return {
    snapshot,
    actions: snapshot.disabledReason || snapshot.running || snapshot.readOnly ? NO_ACTIONS : SUBMIT_ACTIONS,
    dispatchAction: async (action: string, input: unknown, isCurrent: () => boolean, signal: AbortSignal): Promise<{ accepted: true }> => {
      if (action !== CONTENT_SUBMIT_ACTION) throw new Error(`Unsupported artifact action: ${action}`);
      if (!controller || !source || !isCurrent()) throw new Error("Artifact action source is no longer active");
      await controller.submit(source, parseContentSubmission(input), isCurrent, signal);
      return { accepted: true };
    },
  };
}
