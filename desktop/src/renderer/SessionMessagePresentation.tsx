import { useEffect } from "react";
import type { ThreadItem, Turn } from "../shared/protocol";
import { AgentAvatarMark, serializeAgentAvatarConfig } from "./AgentAvatarMark";
import { AVATAR_HUES, avatarHueIndex } from "./DefaultAvatar";
import { requestOpenThreadInSplit } from "./ConversationSplitBridge";
import { useConversationRenderActive } from "./ConversationRenderActivity";
import { groupProjectEvents } from "./ProjectViews";
import { useI18n } from "./i18n";
import {
  CONVERSATION_TURN_REVEAL_EVENT,
  type ConversationTurnRevealDetail,
  turnAnchorID,
  userMessageAnchorID,
} from "./TurnViewHelpers";

export function isSessionMessage(item: ThreadItem): boolean {
  return item.type === "user_message"
    && (item.origin === "host" || item.origin === "plugin")
    && item.presentation_kind === "session_message";
}

/** Fold only adjacent notifications that identify the same durable source. */
export function groupUserMessages(items: ThreadItem[]): Array<ThreadItem | [ThreadItem, ...ThreadItem[]]> {
  const entries: Array<ThreadItem | [ThreadItem, ...ThreadItem[]]> = [];
  for (const entry of groupProjectEvents(items)) {
    if (Array.isArray(entry)) {
      entries.push(entry);
      continue;
    }
    const previous = entries.at(-1);
    const first = Array.isArray(previous) ? previous[0] : previous;
    const source = entry.related_session_id?.trim();
    if (first && source && isSessionMessage(entry) && isSessionMessage(first)
      && source === first.related_session_id?.trim()) {
      if (Array.isArray(previous)) previous.push(entry);
      else entries[entries.length - 1] = [first, entry];
    } else entries.push(entry);
  }
  return entries.filter(entry => (Array.isArray(entry) ? entry[0] : entry).type === "user_message");
}

type SessionMessageOnlyTurn = { turn: Turn; item: ThreadItem; source: string };

export function sessionMessageOnlyTurn(turn: Turn): SessionMessageOnlyTurn | undefined {
  const item = turn.items[0];
  const source = item?.related_session_id?.trim();
  if (!item || !source || turn.status !== "completed" || turn.error || turn.items_view !== "full") return undefined;
  return turn.items.every(item => isSessionMessage(item) && item.related_session_id?.trim() === source)
    ? { turn, item, source } : undefined;
}

/** Preserve every turn containing output, errors, or an intervening context card. */
export function groupSessionMessageTurns(
  turns: Turn[],
  boundaries: ReadonlySet<string>,
): Array<Turn | { first: SessionMessageOnlyTurn; turns: [Turn, ...Turn[]] }> {
  const entries: Array<Turn | { first: SessionMessageOnlyTurn; turns: [Turn, ...Turn[]] }> = [];
  for (const turn of turns) {
    const previous = entries.at(-1);
    const first = previous && ("first" in previous ? previous.first : sessionMessageOnlyTurn(previous));
    const next = sessionMessageOnlyTurn(turn);
    if (first && next && first.source === next.source
      && !boundaries.has(turn.id) && !boundaries.has(first.turn.id)) {
      if (previous && "first" in previous) previous.turns.push(turn);
      else entries[entries.length - 1] = { first, turns: [first.turn, turn] };
    } else entries.push(turn);
  }
  return entries;
}

export function SessionMessageRow({
  item,
  turnID,
  count = 1,
  aliases = [],
}: {
  item: ThreadItem;
  turnID: string;
  count?: number;
  aliases?: readonly { turnID: string; itemID: string }[];
}): JSX.Element {
  const { t } = useI18n();
  const renderActive = useConversationRenderActive();
  const source = item.related_session_id?.trim();
  const originalName = item.cause === "fusion" ? "Fusion Lead"
    : item.cause === "fusion_result" ? "Fusion Sidekick"
    : item.name?.trim() || t("message.anotherSession");
  const name = originalName === "Fusion Sidekick" ? "Sidekick"
    : originalName === "Fusion Lead" ? "Lead" : originalName;
  const otherTurnIDs = new Set(aliases.filter(alias => alias.turnID !== turnID).map(alias => alias.turnID));
  useEffect(() => {
    if (!source || !renderActive) return;
    const reveal = (event: Event): void => {
      if (!(event instanceof CustomEvent)) return;
      const detail: ConversationTurnRevealDetail = event.detail;
      if ((detail.turnID === turnID && detail.itemID === item.id)
        || aliases.some(alias => alias.turnID === detail.turnID && alias.itemID === detail.itemID)) {
        requestOpenThreadInSplit(source);
      }
    };
    window.addEventListener(CONVERSATION_TURN_REVEAL_EVENT, reveal);
    return () => window.removeEventListener(CONVERSATION_TURN_REVEAL_EVENT, reveal);
  }, [aliases, item.id, renderActive, source, turnID]);

  return <div className="session-message-row" id={userMessageAnchorID(turnID, item.id)}
    data-user-message-id={item.id} data-turn-id={turnID}>
    {aliases.map(alias => <span key={`${alias.turnID}:${alias.itemID}`} className="session-message-anchor"
      id={userMessageAnchorID(alias.turnID, alias.itemID)} data-user-message-id={alias.itemID} data-turn-id={alias.turnID} />)}
    {[...otherTurnIDs].map(id => <span key={id} className="session-message-anchor" id={turnAnchorID(id)} data-turn-id={id} />)}
    {item.source_id?.startsWith("fusion-task-result:") ? null : <SessionMessageSource source={source} name={name} count={count} />}
  </div>;
}

export function SessionMessageSource({ source, name, count = 1 }: { source: string | undefined; name: string; count?: number }): JSX.Element {
  const { t, formatNumber } = useI18n();
  const label = t("message.fromSession", { name });
  const identity = source || name;
  const avatarKey = serializeAgentAvatarConfig({ shape: "round", accessory: "none", hue: AVATAR_HUES[avatarHueIndex(identity)] });
  return <button type="button" className="session-message-source" disabled={!source}
    aria-label={label} title={label} onClick={() => source && requestOpenThreadInSplit(source)}>
    <span className="session-message-prefix">{t("message.fromSessionPrefix")}</span>
    <AgentAvatarMark seed={identity} avatarKey={avatarKey} motion="static" disableMorph />
    <span className="session-message-name">{name}</span>
    {count > 1 ? <span className="session-message-count">· {formatNumber(count)}</span> : null}
  </button>;
}
