import type { ThreadItem, ThreadItemStatus, Turn } from "../shared/protocol";
import {
  isCancellationMessage,
  userFacingErrorForMessage,
  type UserFacingErrorDisplay,
} from "./UserFacingErrors";

export type TurnEventKind =
  | "user_stopped"
  | "network_lost"
  | "auth_required"
  | "provider_failed"
  | "tool_failed"
  | "local_failed"
  | "internal_error"
  | "context_compacting"
  | "context_compacted"
  | "context_compaction_failed";

export type TurnEventSource = "turn" | "item";

export type TurnEventDisplay =
  | {
      kind: TurnEventKind;
      source: TurnEventSource;
      presentation: "notice";
      notice: UserFacingErrorDisplay;
    }
  | {
      kind:
        | "context_compacting"
        | "context_compacted"
        | "context_compaction_failed";
      source: "item";
      presentation: "context_compaction";
      text?: string;
      reason?: string;
      status?: ThreadItemStatus;
      /** Replacement-context body produced by the completed compaction pass. */
      summary?: string;
    };

export function turnEventForTurn(turn: Turn): TurnEventDisplay | undefined {
  // A manual stop is an expected user action. Preserve any generated output,
  // but do not add a redundant turn-level divider after the user just clicked
  // the stop control. Preserve a non-cancellation structured error if an
  // interrupted turn also carries a real provider or local failure.
  const interruptionError = turn.error?.message.trim();
  if (
    turn.status === "interrupted" &&
    (!interruptionError || isCancellationMessage(interruptionError.toLowerCase()))
  ) {
    return undefined;
  }
  if (turn.kind === "compact" && hasContextCompactionOutcome(turn)) {
    return undefined;
  }
  // A failed reconnect card already explains the cause; a generic
  // turn-level notice would repeat it.
  if (hasSettledStreamReconnect(turn)) {
    return undefined;
  }
  const rawMessage = turn.error?.message || latestTurnItemError(turn);
  const baseDisplay =
    isCancellationMessage((rawMessage ?? "").toLowerCase())
      ? userFacingErrorForMessage(rawMessage, "turn")
      // For a failed turn, prefer the structured `turn.error` populated
      // by the Go core's BuildTurnError so the renderer can derive one
      // accurate user-facing label while keeping diagnostics internal.
      // The legacy string fallback in userFacingErrorForMessage still
      // works for older app-servers that only send the message.
      : turn.status === "failed" || (turn.status === "interrupted" && turn.error)
        ? userFacingErrorForMessage(turn.error, "turn")
        : undefined;
  if (!baseDisplay) {
    return undefined;
  }

  return {
    kind: turnEventKindForNotice(baseDisplay),
    source: "turn",
    presentation: "notice",
    notice: baseDisplay,
  };
}

export function turnEventForItem(item: ThreadItem): TurnEventDisplay | undefined {
  if (item.type === "context_compaction") {
    return {
      kind: contextCompactionKind(item.text, item.status),
      source: "item",
      presentation: "context_compaction",
      text: item.text,
      reason: item.reason,
      status: item.status,
      summary: item.summary,
    };
  }
  if (item.type === "error") {
    const notice = userFacingErrorForMessage(item.error ?? "", "turn");
    return {
      kind: turnEventKindForNotice(notice),
      source: "item",
      presentation: "notice",
      notice,
    };
  }
  return undefined;
}

export function isUnchangedContextCompaction(text?: string): boolean {
  const normalized = (text ?? "").trim().replace(/^[✦*•]\s*/, "");
  return /^Nothing to compact yet\b/i.test(normalized);
}

function latestTurnItemError(turn: Turn): string | undefined {
  for (let i = turn.items.length - 1; i >= 0; i--) {
    const item = turn.items[i];
    if (item.type === "error") {
      const error = item.error?.trim();
      if (error) {
        return error;
      }
    }
  }
  return undefined;
}

function turnEventKindForNotice(display: UserFacingErrorDisplay): TurnEventKind {
  switch (display.category) {
    case "cancelled":
      return "user_stopped";
    case "network":
      return "network_lost";
    case "auth":
      return "auth_required";
    case "provider":
      return "provider_failed";
    case "tool":
      return "tool_failed";
    case "local":
      return "local_failed";
    case "internal":
    default:
      return "internal_error";
  }
}

function contextCompactionKind(
  _text: string | undefined,
  status: ThreadItemStatus | undefined,
): "context_compacting" | "context_compacted" | "context_compaction_failed" {
  if (status === "in_progress") {
    return "context_compacting";
  }
  if (status === "failed") {
    return "context_compaction_failed";
  }
  return "context_compacted";
}

function hasContextCompactionOutcome(turn: Turn): boolean {
  return turn.items.some(
    (item) =>
      item.type === "context_compaction" &&
      (item.status === "completed" || item.status === "failed"),
  );
}

function hasSettledStreamReconnect(turn: Turn): boolean {
  return turn.items.some(
    (item) => item.type === "stream_reconnect" && item.status === "failed",
  );
}
