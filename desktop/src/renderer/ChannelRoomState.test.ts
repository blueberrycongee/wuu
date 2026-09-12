import { describe, expect, it } from "vitest";
import type { ChannelMessage, ChannelRoom, NamedAgent } from "../shared/protocol";
import {
  sameChannelMessages,
  sameChannelRooms,
  sameNamedAgents,
} from "./ChannelRoomState";

function room(overrides: Partial<ChannelRoom> = {}): ChannelRoom {
  return {
    id: "room-1",
    kind: "channel",
    name: "Engineering",
    created_by: "human-1",
    created_at: "2026-08-06T00:00:00.000Z",
    unread_count: 2,
    members: [
      {
        room_id: "room-1",
        member_type: "human",
        member_id: "human-1",
        joined_at: "2026-08-06T00:00:00.000Z",
      },
    ],
    ...overrides,
  };
}

function agent(overrides: Partial<NamedAgent> = {}): NamedAgent {
  return {
    id: "agent-1",
    name: "Alpha",
    memory_dir: "/agents/agent-1/memory",
    avatar_key: "abstract-3",
    autostart: true,
    created_at: "2026-08-06T00:00:00.000Z",
    ...overrides,
  };
}

function message(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "message-1",
    room_id: "room-1",
    seq: 1,
    author_type: "human",
    author_id: "human-1",
    kind: "text",
    body: "Hello",
    created_at: "2026-08-06T00:00:00.000Z",
    ...overrides,
  };
}

describe("sameChannelRooms", () => {
  it("refreshes the conversation preview even when membership and unread count are unchanged", () => {
    const preview = { id: "m1", author_type: "agent" as const, author_id: "agent-1", kind: "text" as const, body: "First reply", has_attachments: false, created_at: "2026-09-12T10:00:00Z" };
    const current = [room({ last_message: preview })];
    expect(sameChannelRooms(current, [room({ last_message: { ...preview } })])).toBe(true);
    expect(sameChannelRooms(current, [room({ last_message: { ...preview, body: "Updated reply" } })])).toBe(false);
    expect(sameChannelRooms(current, [room({ last_message: { ...preview, id: "m2", created_at: "2026-09-12T10:10:00Z" } })])).toBe(false);
  });

  it("treats equivalent poll snapshots as unchanged", () => {
    const current = [room()];
    const incoming = current.map((item) => ({
      ...item,
      members: item.members.map((member) => ({ ...member })),
    }));

    expect(sameChannelRooms(current, incoming)).toBe(true);
  });

  it("treats an omitted zero unread count as the same visible state", () => {
    expect(
      sameChannelRooms(
        [room({ unread_count: undefined })],
        [room({ unread_count: 0 })],
      ),
    ).toBe(true);
  });

  it("detects user-visible room and membership changes", () => {
    const current = [room()];

    expect(sameChannelRooms(current, [room({ unread_count: 3 })])).toBe(false);
    expect(sameChannelRooms(current, [room({ name: "Product" })])).toBe(false);
    expect(
      sameChannelRooms(current, [
        room({
          members: [
            {
              ...current[0].members[0],
              member_id: "human-2",
            },
          ],
        }),
      ]),
    ).toBe(false);
  });

  it("detects room ordering changes", () => {
    const first = room();
    const second = room({ id: "room-2", name: "Design" });

    expect(sameChannelRooms([first, second], [second, first])).toBe(false);
  });
});

describe("sameNamedAgents", () => {
  it("treats equivalent activity snapshots as unchanged", () => {
    expect(
      sameNamedAgents(
        [agent()],
        [agent({ activity_status: "idle", activity_room_ids: [] })],
      ),
    ).toBe(true);
  });

  it("detects user-visible and activity changes", () => {
    const current = [agent({ activity_status: "idle" })];

    expect(sameNamedAgents(current, [agent({ name: "Beta" })])).toBe(false);
    expect(
      sameNamedAgents(current, [agent({ activity_status: "thinking", activity_room_ids: ["room-1"] })]),
    ).toBe(false);
  });
});

describe("sameChannelMessages", () => {
  it("treats equivalent message snapshots as unchanged", () => {
    const current = [message()];
    expect(sameChannelMessages(current, [message()])).toBe(true);
  });

  it("detects body, task, and attachment changes", () => {
    const current = [message({
      kind: "task",
      task_state: "doing",
      work: { state: "working" } as ChannelMessage["work"],
      images: [{ media_type: "image/png", data: "aW1hZ2U=" }],
    })];

    expect(sameChannelMessages(current, [message({ ...current[0], body: "Changed" })])).toBe(false);
    expect(sameChannelMessages(current, [message({ ...current[0], task_state: "done" })])).toBe(false);
    expect(sameChannelMessages(current, [message({ ...current[0], images: [] })])).toBe(false);
  });
});
