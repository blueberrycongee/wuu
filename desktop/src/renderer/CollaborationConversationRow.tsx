import { Copy, EyeOff, Pencil, Pin, PinOff, Trash2 } from "./WuuIcons";
import { useEffect, useState, type MouseEvent as ReactMouseEvent } from "react";

import type { NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { ChannelGroupAvatar } from "./ChannelGroupAvatar";
import type { CollaborationConversation } from "./CollaborationConversations";
import { copyToClipboard, ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { showErrorToast, showToast } from "./Toast";
import { useI18n } from "./i18n";

export function CollaborationConversationRow({
  conversation, agents, selected, initialized, collapsed = false, showPinMark,
  onSelect, onTogglePinned, onHideConversation, onDeleteConversation, onEditAgent, onEditRoom,
}: {
  conversation: CollaborationConversation;
  agents: NamedAgent[];
  selected: boolean;
  initialized: boolean;
  collapsed?: boolean;
  showPinMark?: boolean;
  onSelect: () => void;
  onTogglePinned?: (conversation: CollaborationConversation) => void;
  onHideConversation?: (conversation: CollaborationConversation) => void;
  onDeleteConversation?: (conversation: CollaborationConversation) => void;
  onEditAgent?: (agentID: string) => void;
  onEditRoom?: (roomID: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const { id, name, agent, room, pinned } = conversation;
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null);
  useEffect(() => { setContextMenu(null); }, [id, collapsed, initialized]);
  const unread = selected ? 0 : (room?.unread_count ?? 0);
  // An agent's avatar stays active across all of its rooms.
  const avatarThinking = agent?.activity_status === "thinking" || room?.activity_status === "thinking";
  const contextItems: ThreadContextMenuItem[] = [];
  if (onTogglePinned) contextItems.push({ label: t(pinned ? "sidebar.unpin" : "sidebar.pin"), icon: pinned ? <PinOff size={16} /> : <Pin size={16} />, onSelect: () => onTogglePinned(conversation) });
  if (agent && onEditAgent) contextItems.push({ label: t("channels.editAgent"), icon: <Pencil size={16} />, onSelect: () => onEditAgent(agent.id) });
  else if (room?.kind === "channel" && onEditRoom) contextItems.push({ label: t("channels.roomDetails"), icon: <Pencil size={16} />, onSelect: () => onEditRoom(room.id) });
  if (contextItems.length > 0) contextItems.push({ separator: true });
  contextItems.push({
    label: t(room ? "threadSidebar.copyConversationID" : "channels.copyAgentID"), icon: <Copy size={16} />,
    onSelect: async () => {
      if (await copyToClipboard(room?.id ?? agent!.id)) showToast({ message: t("settings.copied") });
      else showErrorToast(t("common.copyFailed"));
    },
  });
  if (onHideConversation || onDeleteConversation) contextItems.push({ separator: true });
  if (onHideConversation) contextItems.push({ label: t("channels.hideConversation"), icon: <EyeOff size={16} />, onSelect: () => onHideConversation(conversation) });
  if (onDeleteConversation && (agent || room)) contextItems.push({ label: t(room?.kind === "dm" ? "channels.deleteConversation" : room ? "channels.deleteRoom" : "channels.deleteAgent"), icon: <Trash2 size={16} />, danger: true, onSelect: () => onDeleteConversation(conversation) });

  function openContextMenu(event: ReactMouseEvent<HTMLButtonElement>): void {
    if (!initialized) return;
    event.preventDefault();
    const rect = event.currentTarget.getBoundingClientRect();
    setContextMenu({
      x: event.clientX || rect.left,
      y: event.clientY || rect.bottom,
    });
  }

  return (
    <>
      <button type="button" className={`collaboration-contact-row${selected ? " active" : ""}${unread > 0 ? " has-unread" : ""}${pinned ? " pinned" : ""}`}
        aria-current={selected ? "page" : undefined} disabled={!initialized}
        aria-label={collapsed ? `${name}${unread > 0 ? `, ${t("channels.unreadMessages", { count: unread })}` : ""}` : undefined}
        title={`${name}${avatarThinking ? ` · ${t("channels.agentWorking")}` : ""}`}
        onContextMenu={openContextMenu}
        onClick={onSelect}>
        <span className="collaboration-contact-avatar" aria-hidden="true">
          {agent ? <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} status={avatarThinking ? "thinking" : "idle"} motion="expressive" />
            : room ? <ChannelGroupAvatar room={room} agents={agents} layout="stack" /> : null}
        </span>
        {collapsed && unread > 0 ? <span className="collaboration-rail-unread" aria-hidden="true">{unread > 99 ? "99+" : unread}</span> : null}
        <span className="collaboration-contact-copy">
          <span className="collaboration-contact-heading"><strong>{name}</strong>
            {unread > 0 ? <span className="collaboration-contact-unread" aria-label={t("channels.unreadMessages", { count: unread })}>{unread > 99 ? "99+" : unread}</span>
              : (showPinMark ?? pinned) ? <Pin className="collaboration-contact-pin" aria-label={t("channels.pinnedConversation")} /> : null}
          </span>
        </span>
      </button>
      {contextMenu && initialized ? <ThreadContextMenu
        x={contextMenu.x} y={contextMenu.y}
        items={contextItems}
        onClose={() => setContextMenu(null)}
      /> : null}
    </>
  );
}
