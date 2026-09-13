import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { CollaborationSidebar } from "./CollaborationSidebar";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
import { translateCurrent as t } from "./i18n";

const agent: NamedAgent = { id: "alpha", name: "Alpha", memory_dir: "", avatar_key: "abstract-1", autostart: true, created_at: "2026-09-01T00:00:00Z" };
const dm: ChannelRoom = {
  id: "dm", kind: "dm", name: "Old agent name", created_by: "human", created_at: agent.created_at,
  members: [{ room_id: "dm", member_id: agent.id, member_type: "agent", joined_at: agent.created_at }],
  last_message: { id: "m1", author_type: "agent", author_id: agent.id, kind: "text", body: "The report is ready", has_attachments: false, created_at: "2026-09-12T10:00:00Z" },
};
const group: ChannelRoom = {
  ...dm, id: "group", kind: "channel", name: "Design", members: [], unread_count: 3,
  last_message: { ...dm.last_message!, id: "m2", body: "Updated mockups", created_at: "2026-09-12T11:00:00Z" },
};
let host: HTMLDivElement;
let root: Root;
const callbacks = {
  onSelectAgent: vi.fn(), onSelectRoom: vi.fn(), onManageAgents: vi.fn(), onCreateAgent: vi.fn(),
  onCreateRoom: vi.fn(), onSwitchToHarness: vi.fn(), onOpenSettings: vi.fn(),
  onToggleCollapsed: vi.fn(),
  onEditAgent: vi.fn(), onEditRoom: vi.fn(),
  onTogglePinned: vi.fn(), onHideConversation: vi.fn(), onDeleteConversation: vi.fn(),
};
function render(rooms: ChannelRoom[] = [dm, group], pinnedRoomIDs: string[] = [], collapsed = false, agents: NamedAgent[] = [agent, { ...agent, id: "beta", name: "Beta" }]) {
  act(() => root.render(<WuuUIRoot><CollaborationSidebar initialized agents={agents}
    rooms={rooms} pinnedRoomIDs={pinnedRoomIDs} selectedRoomID="dm" collapsed={collapsed} {...callbacks} /></WuuUIRoot>));
}
function rows() { return Array.from(host.querySelectorAll<HTMLButtonElement>("nav button")); }
function rightClick(row: HTMLButtonElement) {
  act(() => row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 80 })));
}
beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, "wuu", { configurable: true, value: {} });
  host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

it("mixes recent groups and DMs, keeps new agents reachable, and opens each target exactly once", () => {
  render();
  expect(rows().map((row) => row.querySelector("strong")?.textContent)).toEqual(["Design", "Alpha", "Beta"]);
  expect(rows()[0].textContent).toContain("Updated mockups");
  expect(rows()[0].textContent).toContain("3");
  expect(rows()[1].textContent).toContain("The report is ready");
  expect(rows()[1].getAttribute("aria-current")).toBe("page");
  act(() => rows()[1].click());
  expect(callbacks.onSelectRoom).toHaveBeenCalledWith("dm");
  act(() => rows()[2].click());
  expect(callbacks.onSelectAgent).toHaveBeenCalledWith("beta");
  expect(callbacks.onSelectRoom).toHaveBeenCalledTimes(1);
});

it("keeps pinned rooms first, updates incoming previews, and filters by the current agent name", () => {
  render([dm, group], ["dm"]);
  expect(rows()[0].querySelector("strong")?.textContent).toBe("Alpha");
  render([{ ...dm, last_message: { ...dm.last_message!, body: "Next reply", created_at: "2026-09-12T12:00:00Z" } }, group]);
  expect(rows()[0].textContent).toContain("Next reply");
  const input = host.querySelector<HTMLInputElement>('input[type="search"]')!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "ALPHA");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(rows()).toHaveLength(1);
  expect(rows()[0].querySelector("strong")?.textContent).toBe("Alpha");
});

it.each([false, true])("manages groups, DM agents, and agents without a DM from the context menu (rail=%s)", (collapsed) => {
  render([dm, group], [], collapsed);
  for (const [index, target, callback] of [
    [0, "group", callbacks.onEditRoom], [1, "alpha", callbacks.onEditAgent], [2, "beta", callbacks.onEditAgent],
  ] as const) {
    rightClick(rows()[index]);
    expect(callbacks.onSelectAgent).not.toHaveBeenCalled();
    expect(callbacks.onSelectRoom).not.toHaveBeenCalled();
    const action = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).find(button => button.textContent === t(index === 0 ? "channels.roomDetails" : "channels.editAgent"));
    expect(action).not.toBeNull();
    act(() => action!.click());
    expect(callback).toHaveBeenLastCalledWith(target);
    expect(document.querySelector('[role="menu"]')).toBeNull();
  }
});

it("dismisses management without taking action and drops menus for removed targets", () => {
  render();
  rightClick(rows()[0]);
  act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(document.querySelector('[role="menu"]')).toBeNull();
  rightClick(rows()[0]);
  act(() => document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
  expect(document.querySelector('[role="menu"]')).toBeNull();
  rightClick(rows()[0]);
  render([dm]);
  expect(document.querySelector('[role="menu"]')).toBeNull();
  expect(callbacks.onEditAgent).not.toHaveBeenCalled();
  expect(callbacks.onEditRoom).not.toHaveBeenCalled();
});

it("exposes pin, hide, delete and ID copying for the exact conversation", async () => {
  const writeText = vi.fn(async () => {});
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
  render([dm, group], ["dm"]);
  for (const [key, callback] of [
    ["sidebar.unpin", callbacks.onTogglePinned], ["channels.hideConversation", callbacks.onHideConversation], ["channels.deleteAgent", callbacks.onDeleteConversation],
  ] as const) {
    rightClick(rows()[0]);
    const item = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).find(button => button.textContent === t(key))!;
    act(() => item.click());
    expect(callback).toHaveBeenCalledWith(expect.objectContaining({ id: "dm", agent, room: dm }));
  }
  rightClick(rows()[0]);
  const copy = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).find(button => button.textContent === t("threadSidebar.copyConversationID"))!;
  await act(async () => copy.click());
  expect(writeText).toHaveBeenCalledWith("dm");
});

it("does not resurrect a hidden DM as a standalone Agent and restores it when unhidden", () => {
  act(() => root.render(<WuuUIRoot><CollaborationSidebar initialized agents={[agent]} rooms={[dm, group]} archivedRoomIDs={["dm"]} {...callbacks} /></WuuUIRoot>));
  expect(rows().map(row => row.querySelector("strong")?.textContent)).toEqual(["Design"]);
  act(() => root.render(<WuuUIRoot><CollaborationSidebar initialized agents={[agent]} rooms={[dm, group]} archivedRoomIDs={[]} {...callbacks} /></WuuUIRoot>));
  expect(rows().map(row => row.querySelector("strong")?.textContent)).toEqual(["Design", "Alpha"]);
});

it("opens the conversation picker directly from the add button", () => {
  render();
  const trigger = host.querySelector<HTMLButtonElement>('[aria-label="新建对话"]')!;
  act(() => trigger.click());
  expect(callbacks.onCreateRoom).toHaveBeenCalledOnce();
  expect(document.querySelector('[role="menu"]')).toBeNull();
});

it("keeps the shared mode switch in the sidebar with Collaboration selected", () => {
  render();
  const modes = host.querySelector('[role="group"]')!;
  const currentMode = modes.querySelector<HTMLButtonElement>('[aria-pressed="true"]')!;
  expect(currentMode).not.toBeNull();
  act(() => currentMode.click());
  expect(callbacks.onSwitchToHarness).not.toHaveBeenCalled();
  const harness = modes.querySelector<HTMLButtonElement>('[aria-pressed="false"]')!;
  act(() => harness.click());
  expect(callbacks.onSwitchToHarness).toHaveBeenCalledOnce();
});

it("keeps collapsed conversations accessible, selected and unread while exposing expand and creation actions", () => {
  render([dm, group], ["group"], true);
  expect(host.querySelector('input[type="search"]')).toBeNull();
  expect(rows()[0].getAttribute("aria-label")).toContain("Design");
  expect(rows()[0].getAttribute("aria-label")).toContain("3");
  expect(rows()[0].querySelector(".collaboration-rail-unread")?.textContent).toBe("3");
  expect(rows()[1].getAttribute("aria-current")).toBe("page");
  expect(rows()[1].getAttribute("title")).toBe("Alpha");
  act(() => rows()[0].click());
  expect(callbacks.onSelectRoom).toHaveBeenCalledWith("group");
  const footer = host.querySelector(".collaboration-sidebar-footer")!;
  act(() => footer.querySelector<HTMLButtonElement>('[title="展开左侧栏"]')?.click());
  expect(callbacks.onToggleCollapsed).toHaveBeenCalledOnce();
  const create = footer.querySelector<HTMLButtonElement>('[aria-label="新建对话"]')!;
  act(() => create.click());
  expect(callbacks.onCreateRoom).toHaveBeenCalledOnce();
});

it("does not let a hidden search filter remove rail shortcuts", () => {
  render();
  const input = host.querySelector<HTMLInputElement>('input[type="search"]')!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "ALPHA");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(rows()).toHaveLength(1);
  render([dm, group], [], true);
  expect(rows()).toHaveLength(3);
  render();
  expect(rows()).toHaveLength(1);
});

it.each([false, true])("keeps an agent active across group work until all its work ends (collapsed=%s)", (collapsed) => {
  const beta = { ...agent, id: "beta", name: "Beta" };
  const avatar = (id: string) => host.querySelector(`[data-agent-avatar-id="${id}"]`)!;
  const update = (activityRoomIDs: string[]) => render([dm, group], [], collapsed, [
    { ...agent, activity_status: activityRoomIDs.length ? "thinking" : "idle", activity_room_ids: activityRoomIDs }, beta,
  ]);
  update([]);
  const identity = avatar(agent.id);
  update([group.id, "other-group"]);
  expect(avatar(agent.id)).toBe(identity);
  expect(identity.getAttribute("data-agent-avatar-state")).toBe("thinking");
  expect(identity.getAttribute("data-agent-avatar-motion")).toBe("expressive");
  expect(avatar(beta.id).getAttribute("data-agent-avatar-state")).toBe("idle");
  expect(rows().find(row => row.contains(identity))?.textContent).toContain(dm.last_message!.body);
  update(["other-group"]);
  expect(identity.getAttribute("data-agent-avatar-state")).toBe("thinking");
  update([]);
  expect(identity.getAttribute("data-agent-avatar-state")).toBe("idle");
  expect(identity.hasAttribute("data-agent-avatar-turn")).toBe(false);
});
