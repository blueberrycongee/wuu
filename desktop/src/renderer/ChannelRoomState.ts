import type {
  ChannelMessage,
  ChannelRoom,
  ChannelRoomMember,
  NamedAgent,
} from "../shared/protocol";

export function sameNamedAgents(
  current: readonly NamedAgent[],
  incoming: readonly NamedAgent[],
): boolean {
  return (
    current.length === incoming.length &&
    current.every((agent, index) => sameNamedAgent(agent, incoming[index]))
  );
}

function sameNamedAgent(
  current: NamedAgent,
  incoming: NamedAgent | undefined,
): boolean {
  return (
    incoming !== undefined &&
    current.id === incoming.id &&
    current.name === incoming.name &&
    (current.role ?? "") === (incoming.role ?? "") &&
    current.memory_dir === incoming.memory_dir &&
    current.avatar_key === incoming.avatar_key &&
    (current.avatar_image ?? "") === (incoming.avatar_image ?? "") &&
    (current.engine_override ?? "") === (incoming.engine_override ?? "") &&
    (current.provider_override ?? "") === (incoming.provider_override ?? "") &&
    (current.model_override ?? "") === (incoming.model_override ?? "") &&
    (current.effort_override ?? "") === (incoming.effort_override ?? "") &&
    current.autostart === incoming.autostart &&
    current.created_at === incoming.created_at &&
    (current.activity_status ?? "idle") === (incoming.activity_status ?? "idle") &&
    sameStringArray(current.activity_room_ids, incoming.activity_room_ids)
  );
}

function sameStringArray(
  current: readonly string[] | undefined,
  incoming: readonly string[] | undefined,
): boolean {
  const currentValues = current ?? [];
  const incomingValues = incoming ?? [];
  return (
    currentValues.length === incomingValues.length &&
    currentValues.every((value, index) => value === incomingValues[index])
  );
}

export function sameChannelMessages(
  current: readonly ChannelMessage[],
  incoming: readonly ChannelMessage[],
): boolean {
  return (
    current.length === incoming.length &&
    current.every((message, index) =>
      sameChannelMessage(message, incoming[index]),
    )
  );
}

function sameChannelMessage(
  current: ChannelMessage,
  incoming: ChannelMessage | undefined,
): boolean {
  return (
    incoming !== undefined &&
    current.id === incoming.id &&
    current.room_id === incoming.room_id &&
    current.seq === incoming.seq &&
    (current.thread_id ?? "") === (incoming.thread_id ?? "") &&
    current.source_session_ref === incoming.source_session_ref &&
    current.source_turn_id === incoming.source_turn_id &&
    current.author_type === incoming.author_type &&
    current.author_id === incoming.author_id &&
    current.kind === incoming.kind &&
    current.body === incoming.body &&
    sameSerialized(current.images, incoming.images) &&
    sameSerialized(current.files, incoming.files) &&
    sameStringArray(current.mentions, incoming.mentions) &&
    (current.reply_to ?? "") === (incoming.reply_to ?? "") &&
    (current.task_title ?? "") === (incoming.task_title ?? "") &&
    (current.task_state ?? "") === (incoming.task_state ?? "") &&
    (current.task_owner ?? "") === (incoming.task_owner ?? "") &&
    (current.task_verification_required ?? false) ===
      (incoming.task_verification_required ?? false) &&
    (current.task_goal_revision ?? -1) === (incoming.task_goal_revision ?? -1) &&
    (current.task_candidate_revision ?? -1) ===
      (incoming.task_candidate_revision ?? -1) &&
    sameSerialized(current.agent_creation_proposal, incoming.agent_creation_proposal) &&
    sameSerialized(current.work, incoming.work) &&
    current.created_at === incoming.created_at
  );
}

function sameSerialized(current: unknown, incoming: unknown): boolean {
  return JSON.stringify(current ?? null) === JSON.stringify(incoming ?? null);
}

export function sameChannelRooms(
  current: readonly ChannelRoom[],
  incoming: readonly ChannelRoom[],
): boolean {
  return (
    current.length === incoming.length &&
    current.every((room, index) => sameChannelRoom(room, incoming[index]))
  );
}

function sameChannelRoom(
  current: ChannelRoom,
  incoming: ChannelRoom | undefined,
): boolean {
  return (
    incoming !== undefined &&
    current.id === incoming.id &&
    current.kind === incoming.kind &&
    current.name === incoming.name &&
    (current.avatar_key ?? "") === (incoming.avatar_key ?? "") &&
    current.avatar_image === incoming.avatar_image &&
    current.created_by === incoming.created_by &&
    current.created_at === incoming.created_at &&
    (current.membership_revision ?? 0) === (incoming.membership_revision ?? 0) &&
    (current.unread_count ?? 0) === (incoming.unread_count ?? 0) &&
    (current.activity_status ?? "idle") === (incoming.activity_status ?? "idle") &&
    sameSerialized(current.last_message, incoming.last_message) &&
    current.members.length === incoming.members.length &&
    current.members.every((member, index) =>
      sameChannelRoomMember(member, incoming.members[index]),
    )
  );
}

function sameChannelRoomMember(
  current: ChannelRoomMember,
  incoming: ChannelRoomMember | undefined,
): boolean {
  return (
    incoming !== undefined &&
    current.room_id === incoming.room_id &&
    current.member_type === incoming.member_type &&
    current.member_id === incoming.member_id &&
    current.joined_at === incoming.joined_at
  );
}
