// Message-row derivation: only user and agent messages reach the mobile
// thread view. Reasoning and tool activity remain hidden.

import type { ThreadItem, Turn } from "@wuu/protocol";

import { isAgentHandoffItem } from "./handoff";

export type ChatRow =
  | { kind: "user"; id: string; turnID: string; item: ThreadItem }
  | { kind: "agent"; id: string; turnID: string; item: ThreadItem };

export function chatRowsFromTurns(
  turns: ReadonlyArray<Pick<Turn, "id" | "items">>,
): ChatRow[] {
  const rows: ChatRow[] = [];
  for (const turn of turns) {
    for (const item of turn.items ?? []) {
      const id = `${turn.id}:${item.id}`;
      if (item.type === "user_message") {
        // Item-aware gate: AGENT_NOTIFICATION_NAME is the reliable wire
        // signal the backend stamps on every agent self-addressed
        // user_message. Text sniffing stays available via the helper for
        // items where the backend hasn't propagated `name` yet.
        if (isAgentHandoffItem(item)) {
          continue; // inter-agent machinery, never a chat bubble
        }
        rows.push({ kind: "user", id, turnID: turn.id, item });
      } else if (item.type === "agent_message") {
        rows.push({ kind: "agent", id, turnID: turn.id, item });
      }
    }
  }
  return rows;
}
