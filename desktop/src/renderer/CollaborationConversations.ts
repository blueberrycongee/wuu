import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import type { ThreadSummary } from "./AppState";

export type CollaborationConversation = {
  id: string;
  name: string;
  agent?: NamedAgent;
  room?: ChannelRoom;
  updatedAt: string;
  pinned: boolean;
};

// Only move sessions when their manager has a visible navigation entry. Hidden
// or deleted agents must not make otherwise accessible sessions disappear.
export function managedSidebarThreads(
  threads: readonly ThreadSummary[], conversations: readonly CollaborationConversation[],
): { byAgentID: Record<string, ThreadSummary[]>; threadIDs: Set<string> } {
  const visibleAgents = new Set(conversations.flatMap(item => item.agent ? [item.agent.id] : []));
  const byAgentID: Record<string, ThreadSummary[]> = {};
  const threadIDs = new Set<string>();
  for (const thread of threads) {
    const managerID = thread.session_control?.manager_id;
    if (!managerID || !visibleAgents.has(managerID) || thread.archived || thread.ephemeral || thread.parent_id) continue;
    (byAgentID[managerID] ??= []).push(thread);
    threadIDs.add(thread.id);
  }
  return { byAgentID, threadIDs };
}

function searchable(value: string): string {
  return value.normalize("NFKD").replace(/\p{M}/gu, "").toLocaleLowerCase();
}

export function collaborationConversations(
  agents: NamedAgent[], rooms: ChannelRoom[], pinnedRoomIDs: readonly string[], query: string,
  archivedRoomIDs: readonly string[] = [],
): CollaborationConversation[] {
  const agentsByID = new Map(agents.map((agent) => [agent.id, agent]));
  const representedAgents = new Set<string>();
  const conversations: CollaborationConversation[] = rooms.map((room) => {
    const agentID = room.kind === "dm"
      ? room.members.find((member) => member.member_type === "agent")?.member_id : undefined;
    if (agentID) representedAgents.add(agentID);
    const agent = agentID ? agentsByID.get(agentID) : undefined;
    return {
      id: room.id, room, agent, name: agent?.name ?? room.name,
      updatedAt: room.last_message?.created_at ?? room.created_at,
      pinned: pinnedRoomIDs.includes(room.id),
    };
  });
  // Newly created agents remain reachable before their first DM is opened.
  for (const agent of agents) {
    if (!representedAgents.has(agent.id)) conversations.push({
      id: `agent:${agent.id}`, name: agent.name, agent,
      updatedAt: agent.created_at, pinned: false,
    });
  }
  const normalizedQuery = searchable(query.trim());
  return conversations.filter((item) => !archivedRoomIDs.includes(item.id) && (!normalizedQuery || searchable(item.name).includes(normalizedQuery)))
    .sort((left, right) => Number(right.pinned) - Number(left.pinned)
      || (Date.parse(right.updatedAt) || 0) - (Date.parse(left.updatedAt) || 0)
      || left.id.localeCompare(right.id));
}
