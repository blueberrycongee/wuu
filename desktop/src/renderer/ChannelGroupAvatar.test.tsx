import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { ChannelGroupAvatar } from "./ChannelGroupAvatar";

const agents: NamedAgent[] = [
  { id: "alpha", name: "Alpha", memory_dir: "", avatar_key: "abstract-1", autostart: true, created_at: "2026-09-01T00:00:00Z" },
  { id: "beta", name: "Beta", memory_dir: "", avatar_key: "abstract-2", autostart: true, created_at: "2026-09-01T00:00:00Z" },
];

function room(members: ChannelRoom["members"]): ChannelRoom {
  return {
    id: "room",
    kind: "channel",
    name: "Design",
    created_by: "human",
    created_at: "2026-09-01T00:00:00Z",
    members,
  };
}

function member(id: string, type: "agent" | "human" = "agent"): ChannelRoom["members"][number] {
  return { room_id: "room", member_id: id, member_type: type, joined_at: "2026-09-01T00:00:00Z" };
}

it("stacks the first two members in the sidebar instead of a padded mosaic", () => {
  const html = renderToStaticMarkup(
    <ChannelGroupAvatar
      room={room([member("alpha"), member("beta"), member("local-user", "human")])}
      agents={agents}
      layout="stack"
    />,
  );
  expect(html).toContain("channel-group-avatar-stack");
  expect(html).not.toContain("channel-group-avatar-grid");
  expect(html.match(/channel-group-avatar-stack-cell/g)).toHaveLength(2);
});

it("keeps the mosaic grid in room headers", () => {
  const html = renderToStaticMarkup(
    <ChannelGroupAvatar room={room([member("alpha"), member("beta")])} agents={agents} />,
  );
  expect(html).toContain("channel-group-avatar-grid");
  expect(html).not.toContain("channel-group-avatar-stack");
});
