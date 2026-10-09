import type { Thread, ThreadItem, Turn } from "../shared/protocol";
import { isInternalUserNotificationItem } from "./InternalUserNotification";
import {
  messageFlowFinalTextIndex,
  messageFlowStatusLabel,
} from "./message-flow-display";
import { streamFieldValue } from "./ThreadItemText";
import { prefersReducedMotion } from "./motion";
import { createScrollGlide } from "./ScrollGlide";
import { conversationSearchPattern } from "./ConversationSearchDisplay";
import { syncConversationRenderWindow } from "./ConversationRenderWindow";
import { formatCurrentNumber, getActiveLocale, translateCurrent as t } from "./i18n";

type TurnProgressContent = {
  label: string;
  detail?: string;
};

function latestNonUserItem(turn: Turn): ThreadItem | undefined {
  for (let index = turn.items.length - 1; index >= 0; index--) {
    const item = turn.items[index];
    if (item.type !== "user_message") {
      return item;
    }
  }
  return undefined;
}

// Anchor IDs used by the input-box query history popover to scroll
// back to a past user message. Kept as plain DOM ids (no hash routing
// involvement) so document.getElementById / scrollIntoView stay cheap.
export function turnAnchorID(turnID: string): string {
  return `turn-${turnID}`;
}

export function userMessageAnchorID(turnID: string, itemID: string): string {
  return `user-msg-${turnID}-${itemID}`;
}

export function messageAnchorID(turnID: string, itemID: string): string {
  return `message-${turnID}-${itemID}`;
}

export const CONVERSATION_TURN_REVEAL_EVENT =
  "wuu:conversation-turn-reveal";

export type ConversationTurnRevealDetail = {
  turnID: string;
  itemID?: string;
};

export function requestConversationTurnReveal(turnID: string, itemID?: string): void {
  if (typeof window === "undefined") {
    return;
  }
  window.dispatchEvent(
    new CustomEvent<ConversationTurnRevealDetail>(
      CONVERSATION_TURN_REVEAL_EVENT,
      { detail: { turnID, itemID } },
    ),
  );
}

export type UserMessageAnchor = {
  turnID: string;
  itemID: string;
};

// Walks a thread's or a single turn's turns/items in order and returns
// the anchor of the earliest user_message. Sidebar rows pass a full
// Thread; the conversation turn rail passes a single Turn. For a
// Turn, the function treats it as a single-element sequence so the
// rail can scroll straight to that turn's first prompt.
// Returns undefined when there is no user_message yet (defensive
// against malformed fixtures); callers should treat that as "skip
// the scroll".
export function firstUserMessageAnchor(
  source: Thread | Turn | undefined,
): UserMessageAnchor | undefined {
  if (!source) {
    return undefined;
  }
  // Thread has `turns: Turn[]`; Turn has `items: ThreadItem[]`
  // directly. The `in` narrowing picks the right field without a
  // full type-guard.
  const turns: Turn[] =
    "turns" in source ? source.turns ?? [] : [source as Turn];
  for (const turn of turns) {
    for (const item of turn.items ?? []) {
      if (item.type === "user_message" && !isInternalUserNotificationItem(item)) {
        return { turnID: turn.id, itemID: item.id };
      }
    }
  }
  return undefined;
}

// Mirrors `firstUserMessageAnchor` but walks the same turns/items in
// reverse. Used by the fork picker to detect when the user clicked
// the "分叉" button on the latest user message (no choice dialog
// needed) versus any older user message (the picker asks whether to
// fork into a new worktree or stay local).
export function lastUserMessageAnchor(
  source: Thread | Turn | undefined,
): UserMessageAnchor | undefined {
  if (!source) {
    return undefined;
  }
  const turns: Turn[] =
    "turns" in source ? source.turns ?? [] : [source as Turn];
  for (let turnIndex = turns.length - 1; turnIndex >= 0; turnIndex -= 1) {
    const turn = turns[turnIndex];
    const items = turn.items ?? [];
    for (let itemIndex = items.length - 1; itemIndex >= 0; itemIndex -= 1) {
      const item = items[itemIndex];
      if (item.type === "user_message" && !isInternalUserNotificationItem(item)) {
        return { turnID: turn.id, itemID: item.id };
      }
    }
  }
  return undefined;
}

export type ThreadReplySnippet = {
  text: string;
  totalAgentMessages: number;
};

// Builds a short preview of the thread's agent replies for sidebar hover
// popovers. Skips agent_message items with no committed text (still-active
// streams that haven't produced visible output yet). Returns undefined when
// there is nothing readable to show — callers fall back to the thread's
// own `preview` field in that case.
export function threadReplySnippet(
  thread: Thread | undefined,
): ThreadReplySnippet | undefined {
  if (!thread) {
    return undefined;
  }
  let firstReply: string | undefined;
  let total = 0;
  for (const turn of thread.turns ?? []) {
    for (const item of turn.items ?? []) {
      if (item.type !== "agent_message") {
        continue;
      }
      const trimmed = (item.text ?? "").trim();
      if (!trimmed) {
        continue;
      }
      total += 1;
      if (firstReply === undefined) {
        firstReply = trimmed;
      }
    }
  }
  if (!firstReply) {
    return undefined;
  }
  return {
    text: firstReply,
    totalAgentMessages: total,
  };
}

// Per-turn variant of threadReplySnippet. The conversation turn rail shows
// one horizontal bar per turn on the left side of the message stream;
// hovering a bar must surface *that turn's* reply, not the whole thread's
// first reply. Same empty-text skipping rules as the thread-level helper.
export function turnReplySnippet(
  turn: Turn | undefined,
): ThreadReplySnippet | undefined {
  if (!turn) {
    return undefined;
  }
  let firstReply: string | undefined;
  let total = 0;
  for (const item of turn.items ?? []) {
    if (item.type !== "agent_message") {
      continue;
    }
    const trimmed = (item.text ?? "").trim();
    if (!trimmed) {
      continue;
    }
    total += 1;
    if (firstReply === undefined) {
      firstReply = trimmed;
    }
  }
  if (!firstReply) {
    return undefined;
  }
  return {
    text: firstReply,
    totalAgentMessages: total,
  };
}

// Per-turn variant for the first user message text. Used in the
// conversation turn rail's hover preview to show what the user
// asked for alongside the first agent reply. Same empty-text
// skipping rules as turnReplySnippet.
export function firstUserMessageText(
  turn: Turn | undefined,
): string | undefined {
  if (!turn) {
    return undefined;
  }
  for (const item of turn.items ?? []) {
    if (item.type !== "user_message") {
      continue;
    }
    // Gate first on the item-level signal so corrupted payload text never
    // reaches the trim path.
    if (isInternalUserNotificationItem(item)) {
      continue;
    }
    const trimmed = (item.text ?? "").trim();
    if (!trimmed) {
      continue;
    }
    return trimmed;
  }
  return undefined;
}

// Caps the preview body to ~140 characters on a single line. The Claude.ai
// reference card uses three short lines, but the wuu sidebar is narrow
// (326px) and the title already sits above the popover — a tighter single
// line keeps the popover visually compact and avoids duplicating context
// the title already conveys.
const THREAD_REPLY_PREVIEW_MAX_CHARS = 140;

export function truncateReplyPreview(text: string): string {
  const normalized = text.replace(/\s+/g, " ").trim();
  if (normalized.length <= THREAD_REPLY_PREVIEW_MAX_CHARS) {
    return normalized;
  }
  return `${normalized.slice(0, THREAD_REPLY_PREVIEW_MAX_CHARS - 1).trimEnd()}…`;
}

// Compact "X 分钟前" / "X 小时前" formatter for sidebar hover previews.
// Kept here (not in ThreadSidebar) because the preview now lives inside
// the conversation pane, not the sidebar DOM. The cadence mirrors the
// relative-time labels already used elsewhere in the sidebar; we do not
// pull in a date library because the input is always an ISO string from
// the server.
export function formatRelativeTime(iso: string | undefined): string {
  if (!iso) {
    return "";
  }
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) {
    return "";
  }
  const diffMs = Date.now() - then;
  const minutes = Math.round(diffMs / 60_000);
  if (minutes < 1) {
    return t("time.justNow");
  }
  if (minutes < 60) {
    return t(minutes === 1 ? "time.minuteAgo" : "time.minutesAgo", { count: formatCurrentNumber(minutes) });
  }
  const hours = Math.round(minutes / 60);
  if (hours < 24) {
    return t(hours === 1 ? "time.hourAgo" : "time.hoursAgo", { count: formatCurrentNumber(hours) });
  }
  const days = Math.round(hours / 24);
  return t(days === 1 ? "time.dayAgo" : "time.daysAgo", { count: formatCurrentNumber(days) });
}

/** The latest turn's end or start, falling back to the thread's own update time. */
export function threadLastActivity(
  thread: Pick<Thread, "updated_at"> & { turns: ReadonlyArray<Pick<Turn, "started_at" | "completed_at">> },
): string | undefined {
  const latestTurn = thread.turns.at(-1);
  return latestTurn?.completed_at ?? latestTurn?.started_at ?? thread.updated_at;
}

/**
 * A list-row age such as "5m" or "3d". Units floor so a value never reads
 * older than it is, and coarsen with distance because a row only needs
 * enough precision to tell recent work from old work.
 */
export function formatCompactAge(iso: string | undefined, now: number): string {
  const then = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(then)) {
    return "";
  }
  const minutes = Math.floor(Math.max(0, now - then) / 60_000);
  const count = (value: number) => ({ count: formatCurrentNumber(value) });
  if (minutes < 1) return t("time.compact.now");
  if (minutes < 60) return t("time.compact.minutes", count(minutes));
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t("time.compact.hours", count(hours));
  const days = Math.floor(hours / 24);
  if (days < 7) return t("time.compact.days", count(days));
  if (days < 30) return t("time.compact.weeks", count(Math.floor(days / 7)));
  if (days < 365) return t("time.compact.months", count(Math.floor(days / 30)));
  return t("time.compact.years", count(Math.floor(days / 365)));
}

const JUMP_HIGHLIGHT_CLASS = "user-message-jump-flash";
const JUMP_HIGHLIGHT_DURATION_MS = 800;
// Try, then retry. The first attempt usually wins, but split conversations
// (and just-mounted threads) can take a frame or two to mount the anchor.
const JUMP_RETRY_DELAYS_MS: readonly number[] = [0, 80, 200];
// Padding above the target so the previous turn header stays in view
// and the message doesn't slam into the scroll container's top edge.
const JUMP_TOP_OFFSET_PX = 64;

function flashJumpTarget(node: HTMLElement): void {
  node.classList.remove(JUMP_HIGHLIGHT_CLASS);
  // Force reflow so re-adding the class restarts the animation even when
  // the user clicks two entries back-to-back.
  void node.offsetWidth;
  node.classList.add(JUMP_HIGHLIGHT_CLASS);
  window.setTimeout(() => {
    node.classList.remove(JUMP_HIGHLIGHT_CLASS);
  }, JUMP_HIGHLIGHT_DURATION_MS);
}

export type ConversationMessageJumpScope = {
  viewport: HTMLElement;
  content: HTMLElement;
  /** Includes the controller's thread/pane binding and operation generation. */
  isCurrent: () => boolean;
};

type ConversationMessageJumpOptions = {
  highlight?: boolean;
  scope: ConversationMessageJumpScope;
  onComplete?: () => void;
};

const messageJumps = new WeakMap<HTMLElement, () => void>();

function searchMatchRange(node: HTMLElement, query?: string): Range | undefined {
  let matchRange: Range | undefined;
  if (query) {
    // Markdown block boundaries are searchable whitespace even when the DOM
    // has no literal text node between its paragraphs. Preserve an offset map
    // so a phrase spanning blocks or inline formatting still lands on its text.
    const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
    const runs: { node: Node; start: number; end: number }[] = [];
    let text = "";
    let previousBlock: Element | null = null;
    let textNode: Node | null;
    while ((textNode = walker.nextNode())) {
      if (textNode.parentElement?.closest(".rich-code-header,.message-actions,.message-copy-button,[aria-hidden=true]")) continue;
      if (textNode instanceof Element) {
        if (textNode.tagName === "BR") text += "\n";
        continue;
      }
      const block = textNode.parentElement?.closest("p,h1,h2,h3,h4,h5,h6,li,pre,td,th,blockquote,div") ?? null;
      if (text && block !== previousBlock) text += "\n";
      previousBlock = block;
      const start = text.length;
      text += textNode.textContent ?? "";
      runs.push({ node: textNode, start, end: text.length });
    }
    const match = conversationSearchPattern(query)?.exec(text);
    if (match) for (const run of runs) {
      if (!matchRange && match.index < run.end) {
        matchRange = document.createRange();
        matchRange.setStart(run.node, match.index - run.start);
      }
      if (matchRange && match.index + match[0].length <= run.end) {
        matchRange.setEnd(run.node, match.index + match[0].length - run.start);
        break;
      }
    }
  }
  return matchRange;
}

/** Jump within one visible conversation; the caller owns cancellation. */
export function scrollToUserMessage(
  turnID: string,
  itemID: string,
  options: ConversationMessageJumpOptions,
): () => void {
  return scrollToMessageAnchor(turnID, userMessageAnchorID(turnID, itemID), options);
}

export function scrollToConversationMessage(
  turnID: string,
  item: ThreadItem,
  query: string | undefined,
  options: ConversationMessageJumpOptions,
): () => void {
  const anchorID = item.type === "user_message"
    ? userMessageAnchorID(turnID, item.id)
    : messageAnchorID(turnID, item.id);
  return scrollToMessageAnchor(turnID, anchorID, { ...options, itemID: item.id, query });
}

function scrollToMessageAnchor(
  turnID: string,
  anchorID: string,
  options: ConversationMessageJumpOptions & { itemID?: string; query?: string },
): () => void {
  if (typeof window === "undefined") return () => undefined;
  const { viewport, content } = options.scope;
  let cancelled = false;
  let timer: number | undefined;
  let frame: number | undefined;
  let laidOutTurn: HTMLElement | null = null;
  let priorVisibility = "";
  const inputTypes = ["wheel", "pointerdown", "touchstart"] as const;
  const cancelForKey = (event: KeyboardEvent): void => {
    if (event.target instanceof Element && event.target.closest("input,textarea,[contenteditable=true]")) return;
    if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(event.key)) cancel();
  };
  const current = (): boolean => !cancelled && viewport.isConnected && content.isConnected &&
    (content === viewport || viewport.contains(content)) && !content.closest('[inert], [aria-hidden="true"]') &&
    options.scope.isCurrent();
  const cancel = (): void => {
    if (cancelled) return;
    cancelled = true;
    if (timer !== undefined) window.clearTimeout(timer);
    if (frame !== undefined) window.cancelAnimationFrame(frame);
    for (const type of inputTypes) viewport.removeEventListener(type, cancel, true);
    viewport.removeEventListener("keydown", cancelForKey, true);
    if (laidOutTurn) laidOutTurn.style.contentVisibility = priorVisibility;
    if (messageJumps.get(viewport) === cancel) messageJumps.delete(viewport);
    options.onComplete?.();
  };
  messageJumps.get(viewport)?.();
  messageJumps.set(viewport, cancel);
  for (const type of inputTypes) viewport.addEventListener(type, cancel, { capture: true, passive: true });
  viewport.addEventListener("keydown", cancelForKey, true);
  let attemptIndex = 0;
  const tryOnce = (): void => {
    timer = undefined;
    if (!current()) { cancel(); return; }
    if (options.itemID) requestConversationTurnReveal(turnID, options.itemID);
    const node = content.querySelector<HTMLElement>(`#${anchorID}`);
    if (!node || node.closest('[inert], [aria-hidden="true"]') || (options.itemID &&
      (node.closest('.turn-process-fold.collapsed') || node.querySelector('.user-message-long-card.collapsed')))) {
      const nextDelay = JUMP_RETRY_DELAYS_MS[++attemptIndex];
      if (nextDelay === undefined) { cancel(); return; }
      timer = window.setTimeout(tryOnce, nextDelay);
      return;
    }
    // A descendant Range can have full geometry while the skipped turn still
    // contributes a collapsed scrollHeight. Hold its layout until placement ends.
    laidOutTurn = node.closest<HTMLElement>(".turn");
    priorVisibility = laidOutTurn?.style.contentVisibility ?? "";
    if (laidOutTurn) laidOutTurn.style.contentVisibility = "visible";
    void node.offsetWidth;
    const match = searchMatchRange(node, options.query);
    // Reveal text in nested code/table surfaces before measuring the viewport target.
    for (let parent = match?.startContainer.parentElement; parent && parent !== viewport; parent = parent.parentElement) {
      const style = getComputedStyle(parent);
      const bounds = parent.getBoundingClientRect();
      const target = match!.getBoundingClientRect();
      if (/auto|scroll/.test(style.overflowY) && parent.scrollHeight > parent.clientHeight) {
        parent.scrollTop += target.top - bounds.top - JUMP_TOP_OFFSET_PX;
      }
      if (/auto|scroll/.test(style.overflowX) && parent.scrollWidth > parent.clientWidth) {
        parent.scrollLeft += target.left - bounds.left - JUMP_TOP_OFFSET_PX;
      }
    }
    const targetRect = match?.getBoundingClientRect() ?? node.getBoundingClientRect();
    const targetTop = Math.max(0, Math.min(
      targetRect.top - viewport.getBoundingClientRect().top + viewport.scrollTop - JUMP_TOP_OFFSET_PX,
      viewport.scrollHeight - viewport.clientHeight,
    ));
    if (options.highlight !== false) flashJumpTarget(node);
    if (Math.abs(targetTop - viewport.scrollTop) < 2) { cancel(); return; }
    if (prefersReducedMotion()) {
      viewport.scrollTop = targetTop;
      syncConversationRenderWindow(viewport);
      cancel();
      return;
    }
    const glide = createScrollGlide();
    glide.start(viewport.scrollTop);
    let commanded = viewport.scrollTop;
    // One device pixel of rounding can exceed a CSS pixel at reduced desktop zoom.
    const roundingTolerance = Math.max(1, 1 / window.devicePixelRatio) + 0.01;
    const step = (now: number): void => {
      frame = undefined;
      if (!current() || Math.abs(viewport.scrollTop - commanded) > roundingTolerance) { cancel(); return; }
      const { position, done } = glide.step(now, targetTop, viewport.clientHeight);
      viewport.scrollTop = position;
      commanded = viewport.scrollTop;
      syncConversationRenderWindow(viewport);
      if (done) cancel();
      else frame = window.requestAnimationFrame(step);
    };
    frame = window.requestAnimationFrame(step);
  };
  if (!current()) { cancel(); return cancel; }
  requestConversationTurnReveal(turnID, options.itemID);
  // Item addressing may expand a collapsed message in the next React commit.
  if (options.itemID) timer = window.setTimeout(tryOnce, 0);
  else tryOnce();
  return cancel;
}

/**
 * Automatic recovery that gives up settles a turn as interrupted rather than
 * failed; either way the turn ended in a failure its card explains.
 */
export function turnEndedInFailure(turn: Turn): boolean {
  return turn.status === "failed" || (
    turn.status === "interrupted" &&
    turn.items.some((item) => item.type === "stream_reconnect" && item.status === "failed")
  );
}

/**
 * Whether a failed turn left output the conversation keeps: reply text or a
 * step that ran. The next turn sees both, so continuing builds on them while
 * resending the message would discard them. Reasoning alone is not kept.
 */
export function turnLeftPartialWork(turn: Turn): boolean {
  return turn.items.some((item) =>
    item.type === "tool_call" ||
    (item.type === "agent_message" &&
      (streamFieldValue(turn.id, item, "text").trim().length > 0 || (item.images?.length ?? 0) > 0)),
  );
}

export function turnProgressContent(
  turn: Turn,
  elapsedMs: number,
  hasFinalText: boolean,
): TurnProgressContent {
  const locale = getActiveLocale() === "zh-CN" ? "zh" : "en";
  if (turn.status !== "in_progress") {
    return {
      label: messageFlowStatusLabel({
        done: true,
        failed: turn.status === "failed",
        hasFinalText,
        locale,
      }),
    };
  }

  const runningTool = turn.items.find(
    (item) =>
      item.type === "tool_call" &&
      (item.status ?? "in_progress") === "in_progress",
  );
  if (runningTool) {
    return {
      label: messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText: false,
        locale,
      }),
    };
  }

  const latestItem = latestNonUserItem(turn);
  if (!latestItem) {
    return {
      label: messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText,
        locale,
      }),
      detail: waitingDetail(elapsedMs, t("turn.waitingForModel")),
    };
  }
  if (latestItem.type === "agent_message") {
    const hasText =
      hasFinalText ||
      (latestItem.terminal === true &&
        streamFieldValue(turn.id, latestItem, "text").length > 0);
    return {
      label: messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText: hasText,
        finalizing:
          hasText &&
          latestItem.terminal === true &&
          latestItem.status === "completed",
        locale,
      }),
      detail: hasText ? undefined : waitingDetail(elapsedMs, t("turn.organizingAnswer")),
    };
  }
  if (latestItem.type === "reasoning") {
    return {
      label: messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText: false,
        locale,
      }),
      detail: waitingDetail(elapsedMs, t("turn.organizingAnswer")),
    };
  }
  if (latestItem.type === "tool_call") {
    return {
      label: messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText: false,
        locale,
      }),
    };
  }
  if (latestItem.type === "context_compaction") {
    return {
      label: messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText,
        locale,
      }),
    };
  }
  if (latestItem.type === "error") {
    return {
      label: messageFlowStatusLabel({
        done: false,
        failed: false,
        hasFinalText,
        locale,
      }),
    };
  }

  return {
    label: messageFlowStatusLabel({
      done: false,
      failed: false,
      hasFinalText,
      locale,
    }),
    detail: waitingDetail(elapsedMs, t("turn.processingRequest")),
  };
}

function waitingDetail(elapsedMs: number, defaultDetail: string): string {
  if (elapsedMs >= 30_000) {
    return t("turn.takingLonger");
  }
  if (elapsedMs >= 8_000) {
    return t("turn.continuingRequest");
  }
  return defaultDetail;
}

export function latestAgentMessageItemID(turns: Turn[]): string | undefined {
  for (let turnIndex = turns.length - 1; turnIndex >= 0; turnIndex--) {
    const itemID = latestAgentMessageItemIDForTurn(turns[turnIndex]);
    if (itemID) {
      return itemID;
    }
  }
  return undefined;
}

// Same scan as latestAgentMessageItemID, but also reports which turn owns the
// item. Callers rendering per-turn subtrees can then scope the "latest"
// affordance to the owner turn instead of handing every turn an id it can
// never match (item ids are unique per thread).
export function latestAgentMessageLocation(
  turns: Turn[],
): { turnID: string; itemID: string } | undefined {
  for (let turnIndex = turns.length - 1; turnIndex >= 0; turnIndex--) {
    const itemID = latestAgentMessageItemIDForTurn(turns[turnIndex]);
    if (itemID) {
      return { turnID: turns[turnIndex].id, itemID };
    }
  }
  return undefined;
}

function latestAgentMessageItemIDForTurn(turn: Turn): string | undefined {
  for (let itemIndex = turn.items.length - 1; itemIndex >= 0; itemIndex--) {
    const item = turn.items[itemIndex];
    if (item.type === "agent_message") {
      return item.id;
    }
  }
  return undefined;
}

export function messageFlowAgentMessageItemID(
  turn: Turn,
): string | undefined {
  const explicitFinalID = explicitFinalAgentMessageItemID(turn);
  if (explicitFinalID) {
    return explicitFinalID;
  }

  const finalIndex = messageFlowFinalTextIndex(turn.items, (item) => {
    if (item.type === "agent_message") {
      return streamFieldValue(turn.id, item, "text").trim().length > 0
        ? "text"
        : "ignore";
    }
    if (
      item.type === "reasoning" ||
      item.type === "tool_call" ||
      item.type === "context_compaction"
    ) {
      return "process";
    }
    return "ignore";
  });

  return finalIndex >= 0 ? turn.items[finalIndex]?.id : undefined;
}

function explicitFinalAgentMessageItemID(turn: Turn): string | undefined {
  for (let itemIndex = turn.items.length - 1; itemIndex >= 0; itemIndex--) {
    const item = turn.items[itemIndex];
    if (item.type !== "agent_message" || !item.terminal) {
      continue;
    }
    if (streamFieldValue(turn.id, item, "text").trim().length > 0) {
      return item.id;
    }
  }
  return undefined;
}
