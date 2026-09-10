import { type JSX } from "react";
import type { ThreadItem, Turn } from "../shared/protocol";
import { streamFieldValue } from "./ThreadItemText";
import { summarizeToolActivity } from "./ToolActivityHelpers";
import { isUnchangedContextCompaction } from "./TurnEvents";
import { isCancellationMessage } from "./UserFacingErrors";

/**
 * The single, ordered list of items that make up an assistant turn.
 *
 * The shell renders this list top-to-bottom inside two visual groups
 * (`process` fold + `answer` body) but the data itself is one
 * chronological sequence — the old `frontEntries` / `finalAnswerItems`
 * split let the same item drift between arrays as `phase` flipped,
 * which is exactly the transition race that produced the "commentary
 * appeared in the final slot" bug.
 *
 * `settled` is derived from `item.status`, not from `phase`:
 *   - `settled = item.status !== "in_progress"`
 *   - the back end marks the item complete when the entire message
 *     (including all deltas) has been committed, so `settled` flips
 *     atomically the moment streaming is over.
 *
 * `position` only describes which visual group the entry belongs to,
 * not whether it is "active" — that's `settled`.
 */
export type TurnEntry = {
  /** Stable React key. Distinct from `item.id` only when several items
   *  share an id (e.g. consecutive tool calls collapsed into one group). */
  key: string;
  item: ThreadItem;
  items?: ThreadItem[];
  position: "process" | "answer";
  settled: boolean;
  streaming: boolean;
  kind: TurnEntryKind;
  count?: number;
};

export type TurnEntryKind =
  | "commentary"
  | "answer"
  | "activity"
  | "process"
  | "process_group";

export type AssistantTurnDisplay = {
  entries: TurnEntry[];
  /** True when the turn has at least one `answer`-position entry. The shell
   *  separately checks that its streamed text is non-empty before starting
   *  the process-to-answer handoff. */
  hasAnswer: boolean;
  /** Latest process record shown in the fold header while the turn is
   *  live. Usually commentary text, but it can also be the latest tool
   *  action when no newer text has arrived yet. */
  latestProcessPreview?: TurnProcessPreview;
};

export type TurnProcessPreview = {
  text: string;
  kind: TurnEntryKind;
};

export function buildAssistantTurnDisplay(
  turn: Turn,
  _actionableAgentMessageID: string | undefined,
  renderThreadItem?: (
    item: ThreadItem,
    streaming: boolean,
    pendingCompanionReasoning?: boolean,
  ) => JSX.Element | null,
): AssistantTurnDisplay | undefined {
  const entries: TurnEntry[] = [];
  // Defensive dedupe. The back-end's `upsertItemLocked` already de-dupes
  // by item id, but a partial resync could in principle leave the list
  // with two of the same id; we never want to render an item twice.
  const seenItemIDs = new Set<string>();
  let sawAssistantWork = false;
  let hasAnswer = false;
  const isInProgress = turn.status === "in_progress";
  const keepsEmptyProcessShell =
    isInProgress || turn.status === "interrupted";
  const turnHasReasoning = turn.items.some((item) => item.type === "reasoning");
  let firstTextItemRendered = false;

  function isProcessItemLive(item: ThreadItem): boolean {
    return isInProgress && item.status === "in_progress";
  }

  function entryPosition(item: ThreadItem): "process" | "answer" {
    // Only a terminal assistant message (one that carried no tool calls)
    // moves into the answer region. Streaming text stays in process until the
    // turn settles and the terminal flag is set structurally.
    if (item.type === "agent_message" && item.terminal) {
      return "answer";
    }
    return "process";
  }

  function entryKind(item: ThreadItem): TurnEntryKind {
    if (item.type === "agent_message") {
      if (item.terminal) return "answer";
      // Non-terminal agent text is ordinary process text; it is not driven by
      // a provider phase signal.
      return "commentary";
    }
    if (item.type === "tool_call") {
      return "activity";
    }
    return "process";
  }

  for (let index = 0; index < turn.items.length; index++) {
    const item = turn.items[index];
    if (seenItemIDs.has(item.id)) continue;
    seenItemIDs.add(item.id);
    if (item.type === "user_message") {
      continue;
    }
    // A no-op completion exists only to settle the transient progress item.
    // It changed no conversation state, so it should not become transcript
    // history after the progress row closes.
    if (
      item.type === "context_compaction" &&
      isUnchangedContextCompaction(item.text)
    ) {
      continue;
    }
    // Background note diagnostics are not compaction outcomes. A failed note
    // can fall back to compaction; only the actual outcome belongs in the UI.
    if (
      item.type === "context_compaction" &&
      item.reason === "context_note"
    ) {
      continue;
    }

    sawAssistantWork = true;

    // An agent_message with no visible text is only a transport placeholder.
    // Never turn it into a layout entry: providers can leave more than one
    // live placeholder between tool calls, and every empty entry would still
    // contribute the process list's vertical gap, producing a large blank
    // block before the next visible activity row. The in-progress turn keeps
    // the shell and timer mounted on its own; once text reaches the stream
    // store, the next render inserts the real entry.
    if (item.type === "agent_message") {
      const streaming = isProcessItemLive(item);
      const text = streamFieldValue(turn.id, item, "text");
      if (text.trim().length === 0) continue;
      const shouldDelayCursor = turnHasReasoning && !firstTextItemRendered;
      firstTextItemRendered = true;
      if (
        renderThreadItem &&
        !renderThreadItem(
          item,
          streaming,
          shouldDelayCursor ? true : undefined,
        )
      ) {
        continue;
      }
      const position = entryPosition(item);
      if (position === "answer") hasAnswer = true;
      entries.push({
        key: item.id,
        item,
        position,
        settled: !streaming,
        streaming,
        kind: entryKind(item),
      });
      continue;
    }

    if (item.type === "tool_call") {
      const group: ThreadItem[] = [item];
      let nextIndex = index + 1;
      while (
        nextIndex < turn.items.length &&
        turn.items[nextIndex].type === "tool_call"
      ) {
        group.push(turn.items[nextIndex]);
        nextIndex++;
      }
      const allSettled = group.every((g) => g.status !== "in_progress");
      entries.push({
        key: `${item.id}-activity`,
        item,
        items: group,
        position: "process",
        settled: allSettled,
        streaming: isInProgress && !allSettled,
        kind: "activity",
        count: group.length,
      });
      index = nextIndex - 1;
      continue;
    }

    if (shouldSuppressProcessErrorItem(turn, item)) {
      continue;
    }

    // reasoning, error, context_compaction, ...
    const streaming = isProcessItemLive(item);
    if (renderThreadItem && !renderThreadItem(item, streaming)) continue;
    entries.push({
      key: item.id,
      item,
      position: "process",
      settled: !streaming,
      streaming,
      kind: entryKind(item),
    });
  }

  // Keep the process shell mounted before the first assistant item, including
  // when the user interrupts in that window. Dropping the display as soon as
  // status changes from in_progress to interrupted removes the entire
  // "正在处理 + elapsed" row and shifts the conversation layout.
  if (!sawAssistantWork && !keepsEmptyProcessShell) {
    return undefined;
  }

  const latestProcessPreview = isInProgress
    ? latestInProgressProcessPreview(turn)
    : undefined;

  if (
    entries.length === 0 &&
    !latestProcessPreview &&
    !keepsEmptyProcessShell
  ) {
    return undefined;
  }

  return {
    entries: groupProcessEntries(entries),
    hasAnswer,
    latestProcessPreview,
  };
}

function shouldSuppressProcessErrorItem(turn: Turn, item: ThreadItem): boolean {
  if (item.type !== "error") {
    return false;
  }
  const interruptionError = turn.error?.message.trim();
  const hasTurnLevelInterruptionError =
    turn.status === "interrupted" &&
    interruptionError != null &&
    interruptionError.length > 0 &&
    !isCancellationMessage(interruptionError.toLowerCase());
  if (
    turn.status === "failed" ||
    hasTurnLevelInterruptionError
  ) {
    // A structured turn error is the authoritative notice. Interrupted turns
    // can carry one too (for example when an internal failure stops a request),
    // so keeping their matching item would render the same failure twice.
    return true;
  }
  // A stream error item only lands while a retryable attempt has just failed
  // terminally. If the turn is still running, the reconnect chip already
  // names the cause; if it completed, the retry recovered — in both cases a
  // settled error line would either pile up per attempt or stay behind as
  // stale noise after a successful recovery.
  if (turn.status === "in_progress" || turn.status === "completed") {
    return true;
  }
  // The turn-level interruption notice already explains user-initiated stops.
  // Rendering the matching cancellation error item here would show the same
  // stop twice in one turn.
  return (
    turn.status === "interrupted" &&
    isCancellationMessage((item.error ?? "").toLowerCase())
  );
}

function groupProcessEntries(entries: TurnEntry[]): TurnEntry[] {
  const grouped: TurnEntry[] = [];
  let pending: TurnEntry[] = [];

  const flushPending = (): void => {
    if (pending.length === 0) {
      return;
    }
    const items = pending.flatMap((entry) => entry.items ?? [entry.item]);
    const hasActivity = pending.some((entry) => entry.kind === "activity");
    const shouldGroup =
      items.length > 1 && (hasActivity || pending.length > 1);
    if (!shouldGroup) {
      grouped.push(...pending);
      pending = [];
      return;
    }
    const first = pending[0];
    grouped.push({
      key: first.key,
      item: first.item,
      items,
      position: "process",
      settled: pending.every((entry) => entry.settled),
      streaming: pending.some((entry) => entry.streaming),
      kind: "process_group",
      count: items.length,
    });
    pending = [];
  };

  for (const entry of entries) {
    if (isProcessGroupCandidate(entry)) {
      pending.push(entry);
      continue;
    }
    flushPending();
    grouped.push(entry);
  }
  flushPending();
  return grouped;
}

function isProcessGroupCandidate(entry: TurnEntry): boolean {
  if (entry.position !== "process") {
    return false;
  }
  if (entry.kind === "activity") {
    return true;
  }
  return entry.item.type === "reasoning";
}

function latestInProgressProcessPreview(
  turn: Turn,
): TurnProcessPreview | undefined {
  for (let index = turn.items.length - 1; index >= 0; index--) {
    const item = turn.items[index];
    if (item.type === "agent_message" && !item.terminal) {
      // The fold-header preview is a committed snapshot, not a live mirror
      // of the streaming store. Read item.text (last settled value) so the
      // preview either shows a previously committed topic or stays empty
      // until the stream settles.
      const text = compactProcessPreview(item.text ?? "");
      if (text) {
        return { text, kind: "commentary" };
      }
    }
  }

  const toolItems = turn.items.filter((item) => item.type === "tool_call");
  if (toolItems.length === 0) {
    return undefined;
  }
  // No model narration: synthesize a deterministic progress summary from the
  // tool activity itself. This is the shell-owned fallback that keeps long
  // silent tool chains readable instead of showing only the latest tool call.
  const summary = summarizeToolActivity(toolItems);
  const text = compactProcessPreview(summary.text);
  return text ? { text, kind: "activity" } : undefined;
}

function compactProcessPreview(raw: string | undefined): string | undefined {
  const text = raw?.replace(/\s+/g, " ").trim();
  if (!text) {
    return undefined;
  }
  const previewMax = 120;
  return text.length > previewMax ? `${text.slice(0, previewMax)}…` : text;
}
