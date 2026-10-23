import type { Thread } from "../shared/protocol";
import { isThreadRunning } from "./AppState";
import { translateCurrent as t } from "./i18n";
import { userFacingErrorForMessage } from "./UserFacingErrors";

export type TurnContinuationTarget = { threadID: string; turnID: string };

/** Continue in the original conversation without consuming its composer draft. */
export const CONTINUE_TURN_EVENT = "wuu:continue-turn";
export type ContinueTurnDetail = {
  threadID: string;
  turnID: string;
  text: string;
  done: (sent: boolean) => void;
};

export function threadContinuationTarget(thread: Thread | undefined): TurnContinuationTarget | undefined {
  if (!thread || thread.read_only || thread.archived || isThreadRunning(thread)) return undefined;
  const turn = thread.turns.at(-1);
  if (!turn || turn.kind === "compact") return undefined;
  const recoverableFailure = turn.status === "failed" && (
    userFacingErrorForMessage(turn.error ?? "", "turn").category === "network" ||
    (turn.error?.status_code !== undefined && turn.error.status_code >= 500 && turn.error.status_code < 600)
  );
  if (turn.status !== "interrupted" && !recoverableFailure) return undefined;
  return { threadID: thread.id, turnID: turn.id };
}

export function requestTurnContinuation(threadID: string, turnID: string): Promise<boolean> {
  return new Promise((resolve) => {
    const detail: ContinueTurnDetail = {
      threadID, turnID, text: t("turnFailure.continuePrompt"), done: resolve,
    };
    if (window.dispatchEvent(new CustomEvent(CONTINUE_TURN_EVENT, { detail, cancelable: true }))) resolve(false);
  });
}
