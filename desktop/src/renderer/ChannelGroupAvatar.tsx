import "./styles/channel-group-avatar.css";
import type { CSSProperties, JSX } from "react";
import type { ChannelRoom, ChannelRoomMember, NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { HumanAvatarMark } from "./DefaultAvatar";

const MAX_GROUP_AVATAR_MEMBERS = 9;

const GROUP_AVATAR_ROWS: Readonly<Record<number, readonly number[]>> = {
  1: [1],
  2: [2],
  3: [1, 2],
  4: [2, 2],
  5: [2, 3],
  6: [3, 3],
  7: [1, 3, 3],
  8: [2, 3, 3],
  9: [3, 3, 3],
};

export function groupAvatarRowSizes(memberCount: number): readonly number[] {
  const visibleCount = Math.max(1, Math.min(MAX_GROUP_AVATAR_MEMBERS, memberCount));
  return GROUP_AVATAR_ROWS[visibleCount];
}

function MemberAvatar({ member, agent }: { member: ChannelRoomMember; agent?: NamedAgent }): JSX.Element {
  if (member.member_type === "agent" && agent) {
    return <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} />;
  }
  return <HumanAvatarMark />;
}

export function ChannelGroupAvatar({ room, agents }: { room: ChannelRoom; agents: NamedAgent[] }): JSX.Element {
  if (room.avatar_image) {
    return <img className="channel-group-avatar-image" src={room.avatar_image} alt="" aria-hidden="true" />;
  }

  const visibleMembers = [...room.members]
    .sort((left, right) => {
      const joinedOrder = left.joined_at.localeCompare(right.joined_at);
      if (joinedOrder !== 0) return joinedOrder;
      if (left.member_type !== right.member_type) return left.member_type === "human" ? -1 : 1;
      return left.member_id.localeCompare(right.member_id);
    })
    .slice(0, MAX_GROUP_AVATAR_MEMBERS);
  const members = visibleMembers.length > 0
    ? visibleMembers
    : [{ room_id: room.id, member_type: "human" as const, member_id: "local-user", joined_at: room.created_at }];
  const agentByID = new Map(agents.map((agent) => [agent.id, agent]));
  const rowSizes = groupAvatarRowSizes(members.length);
  const style = {
    "--channel-group-columns": Math.max(...rowSizes),
  } as CSSProperties;
  let memberIndex = 0;

  return (
    <span className="channel-group-avatar-grid" style={style} aria-hidden="true">
      {rowSizes.map((rowSize, rowIndex) => {
        const rowMembers = members.slice(memberIndex, memberIndex + rowSize);
        memberIndex += rowSize;
        return <span className="channel-group-avatar-row" key={`${rowIndex}-${rowSize}`}>
          {rowMembers.map((member) => (
            <span className="channel-group-avatar-cell" key={`${member.member_type}:${member.member_id}`}>
              <MemberAvatar member={member} agent={agentByID.get(member.member_id)} />
            </span>
          ))}
        </span>;
      })}
    </span>
  );
}
