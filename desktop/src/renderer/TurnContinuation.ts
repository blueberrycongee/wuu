import type { ApplicationConversationThreadV1 } from "../../../packages/plugin-sdk/src/ui-conversation";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import { isThreadRunning } from "./AppState";
import { OPTIMISTIC_TURN_ID_PREFIX } from "./ComposerMessages";
import { translateCurrent as t } from "./i18n";
import { userFacingErrorForMessage } from "./UserFacingErrors";

export type TurnContinuationTarget = { threadID: string; turnID: string; continue: () => Promise<boolean> };

/** Continue in the original conversation without consuming its composer draft. */
export function threadContinuationTarget(thread: ApplicationConversationThreadV1 | undefined, invoke: ApplicationServiceV1["invoke"]): TurnContinuationTarget | undefined {
  if (!thread || thread.read_only || thread.archived || isThreadRunning({ status: thread.status, turns: thread.turns.map(turn => ({ status: turn.status })) })) return undefined;
  const turn = thread.turns.at(-1);
  if (!turn || turn.kind === "compact" || turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX)) return undefined;
  const recoverableFailure = turn.status === "failed" && (
    !thread.engine_id || thread.engine_id === "wuu" ||
    userFacingErrorForMessage(turn.error ?? "", "turn").category === "network" ||
    (turn.error?.status_code !== undefined && turn.error.status_code >= 500 && turn.error.status_code < 600)
  );
  if (turn.status !== "interrupted" && !recoverableFailure) return undefined;
  return {
    threadID: thread.id,
    turnID: turn.id,
    continue: async () => (await invoke("conversation.continue", {
      threadId: thread.id, turnId: turn.id, text: t("turnFailure.continuePrompt"),
    })).accepted,
  };
}
