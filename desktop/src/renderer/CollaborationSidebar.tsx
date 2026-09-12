import { Code2, Copy, EyeOff, PanelLeftOpen, Pencil, Pin, PinOff, Plus, Search, Trash2, UsersRound } from "lucide-react";
import { useEffect, useMemo, useState, type PointerEvent as ReactPointerEvent } from "react";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { AppModeSwitch } from "./AppModeSwitch";
import { ChannelGroupAvatar } from "./ChannelGroupAvatar";
import { collaborationConversations, type CollaborationConversation } from "./CollaborationConversations";
import { SidebarAccountMenu } from "./SidebarAccountMenu";
import { copyToClipboard, ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { showErrorToast, showToast } from "./Toast";
import { useI18n } from "./i18n";


export function CollaborationSidebar({
  initialized, agents, rooms, pinnedRoomIDs = [], archivedRoomIDs = [], selectedAgentID, selectedRoomID,
  collapsed = false, onToggleCollapsed,
  onSelectAgent, onSelectRoom, onManageAgents, onCreateRoom, draftAgent, draftSelected, onSelectDraft,
  onEditAgent, onEditRoom,
  onTogglePinned, onHideConversation, onDeleteConversation,
  onSwitchToHarness, onOpenSettings, onOpenAccount, onPointerEnter, onPointerLeave,
}: {
  initialized: boolean;
  collapsed?: boolean;
  onToggleCollapsed?: () => void;
  agents: NamedAgent[];
  rooms: ChannelRoom[];
  pinnedRoomIDs?: readonly string[];
  archivedRoomIDs?: readonly string[];
  selectedAgentID?: string;
  selectedRoomID?: string;
  onSelectAgent: (agentID: string) => void;
  onSelectRoom: (roomID: string) => void;
  onManageAgents: () => void;
  onEditAgent?: (agentID: string) => void;
  onEditRoom?: (roomID: string) => void;
  onTogglePinned?: (conversation: CollaborationConversation) => void;
  onHideConversation?: (conversation: CollaborationConversation) => void;
  onDeleteConversation?: (conversation: CollaborationConversation) => void;
  draftAgent?: { name: string; avatarKey: string };
  draftSelected?: boolean;
  onSelectDraft?: () => void;
  onCreateRoom: () => void;
  onSwitchToHarness: () => void;
  onOpenSettings: (page?: "providers" | "usage") => void;
  onOpenAccount?: () => void;
  onPointerEnter?: () => void;
  onPointerLeave?: (event: ReactPointerEvent<HTMLElement>) => void;
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const [query, setQuery] = useState("");
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; id: string } | null>(null);
  useEffect(() => { setContextMenu(null); }, [query, collapsed, initialized]);
  const conversations = useMemo(
    () => collaborationConversations(agents, rooms, pinnedRoomIDs, collapsed ? "" : query, archivedRoomIDs),
    [agents, rooms, pinnedRoomIDs, archivedRoomIDs, query, collapsed],
  );
  const contextConversation = conversations.find((conversation) => conversation.id === contextMenu?.id);
  const contextItems: ThreadContextMenuItem[] = [];
  if (contextConversation) {
    const { agent, room, pinned } = contextConversation;
    if (onTogglePinned) contextItems.push({ label: t(pinned ? "sidebar.unpin" : "sidebar.pin"), icon: pinned ? <PinOff size={16} /> : <Pin size={16} />, onSelect: () => onTogglePinned(contextConversation) });
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
    if (onHideConversation) contextItems.push({ label: t("channels.hideConversation"), icon: <EyeOff size={16} />, onSelect: () => onHideConversation(contextConversation) });
    if (onDeleteConversation && (agent || room?.kind === "channel")) contextItems.push({ label: t(agent ? "channels.deleteAgent" : "channels.deleteRoom"), icon: <Trash2 size={16} />, danger: true, onSelect: () => onDeleteConversation(contextConversation) });
  }
  const agentNames = useMemo(() => new Map(agents.map((agent) => [agent.id, agent.name])), [agents]);
  const newConversationButton = <button className="icon-button" type="button" disabled={!initialized}
    aria-label={t("channels.newConversation")} title={t("channels.newConversation")} onClick={onCreateRoom}>
    <Plus aria-hidden="true" />
  </button>;

  return (
    <aside className={`sidebar collaboration-sidebar${collapsed ? " collaboration-sidebar-rail" : ""}`} data-wuu-component="collaboration-sidebar"
      onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}>
      <div className="sidebar-content">
        <div className="collaboration-sidebar-topbar">
          {!collapsed ? newConversationButton : null}

        </div>
        {!collapsed ? <AppModeSwitch mode="collaboration" collaborationEnabled onChange={(mode) => { if (mode === "harness") onSwitchToHarness(); }} /> : null}
        {!collapsed ? <div className="collaboration-sidebar-tools">
          <label className="collaboration-sidebar-search">
            <Search aria-hidden="true" />
            <input type="search" value={query} placeholder={t("channels.searchConversations")}
              aria-label={t("channels.searchConversations")} onChange={(event) => setQuery(event.currentTarget.value)} />
          </label>
        </div> : null}
        <nav className="collaboration-sidebar-main" aria-label={t("channels.conversations")}>
          {draftAgent ? <button type="button" className={`collaboration-contact-row${draftSelected ? " active" : ""}`} aria-current={draftSelected ? "page" : undefined} onClick={onSelectDraft} aria-label={draftAgent.name || t("channels.newAgent")}>
            <span className="collaboration-contact-avatar" aria-hidden="true"><AgentAvatarMark seed="draft-agent" avatarKey={draftAgent.avatarKey} /></span>
            <span className="collaboration-contact-copy"><span className="collaboration-contact-heading"><strong>{draftAgent.name || t("channels.newAgent")}</strong></span><span className="collaboration-contact-preview">{t("agentOnboarding.chooseModelFirst")}</span></span>
          </button> : null}
          {conversations.map(({ id, name, agent, room, pinned }) => {
            const selected = !draftSelected && (room ? selectedRoomID === room.id : selectedAgentID === agent?.id);
            const unread = selected ? 0 : (room?.unread_count ?? 0);
            const message = room?.last_message;
            const thinking = room?.activity_status === "thinking" || (agent?.activity_status === "thinking" && (!room || agent.activity_room_ids?.includes(room.id)));
            const text = message?.body.replace(/\s+/gu, " ").trim() || (message?.has_attachments ? t("channels.attachmentPreview") : "");
            const author = message?.kind === "system" ? "" : message?.author_type === "human" ? t("channels.you") : room?.kind === "channel" ? agentNames.get(message?.author_id ?? "") : "";
            const preview = thinking ? t("channels.agentStatus.thinking") : text ? `${author ? `${author}: ` : ""}${text}` : agent?.role || (room?.kind === "channel" ? t("channels.memberCount", { count: room.members.length }) : t("channels.startConversation"));
            const date = message ? new Date(message.created_at) : undefined;
            const timestamp = date && !Number.isNaN(date.getTime()) ? formatDate(date, date.toDateString() === new Date().toDateString()
              ? { hour: "2-digit", minute: "2-digit" } : { month: "short", day: "numeric" }) : "";
            return <button key={id} type="button" className={`collaboration-contact-row${selected ? " active" : ""}${unread > 0 ? " has-unread" : ""}${pinned ? " pinned" : ""}`}
              aria-current={selected ? "page" : undefined} disabled={!initialized}
              aria-label={collapsed ? `${name}${unread > 0 ? `, ${t("channels.unreadMessages", { count: unread })}` : ""}` : undefined}
              title={collapsed ? `${name}${thinking ? ` · ${t("channels.agentWorking")}` : ""}` : undefined}
              onContextMenu={(event) => {
                if (!initialized) return;
                event.preventDefault();
                const rect = event.currentTarget.getBoundingClientRect();
                setContextMenu({
                  x: event.clientX || rect.left,
                  y: event.clientY || rect.bottom,
                  id,
                });
              }}
              onClick={() => { if (room) onSelectRoom(room.id); else if (agent) onSelectAgent(agent.id); }}>
              <span className="collaboration-contact-avatar" aria-hidden="true">
                {agent ? <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} status={thinking ? "thinking" : "idle"} motion="subtle" />
                  : room ? <ChannelGroupAvatar room={room} agents={agents} /> : null}
              </span>
              {collapsed && unread > 0 ? <span className="collaboration-rail-unread" aria-hidden="true">{unread > 99 ? "99+" : unread}</span> : null}
              <span className="collaboration-contact-copy">
                <span className="collaboration-contact-heading"><strong>{name}</strong>{timestamp ? <time dateTime={message?.created_at}>{timestamp}</time> : null}</span>
                <span className="collaboration-contact-detail"><span className={`collaboration-contact-preview${thinking ? " thinking" : ""}`}>{preview}</span>
                  {unread > 0 ? <span className="collaboration-contact-unread" aria-label={t("channels.unreadMessages", { count: unread })}>{unread > 99 ? "99+" : unread}</span>
                    : pinned ? <Pin className="collaboration-contact-pin" aria-label={t("channels.pinnedConversation")} /> : null}
                </span>
              </span>
            </button>;
          })}
          {conversations.length === 0 && !draftAgent ? <div className="collaboration-contact-empty">{t(query.trim() ? "channels.noMatchingConversations" : "channels.noConversations")}</div> : null}
        </nav>
        <div className="collaboration-sidebar-footer">
          {collapsed ? <>
            <button className="collaboration-sidebar-footer-action" type="button" aria-label={t("app.expandLeftSidebar")} title={t("app.expandLeftSidebar")} onClick={onToggleCollapsed}><PanelLeftOpen aria-hidden="true" /></button>
            {newConversationButton}
            <button className="collaboration-sidebar-footer-action" type="button" aria-label={t("sidebar.harness")} title={t("sidebar.harness")} onClick={onSwitchToHarness}><Code2 aria-hidden="true" /></button>
          </> : null}
          <button className="collaboration-sidebar-footer-action" type="button" disabled={!initialized} aria-label={t("channels.manageAgents")} title={collapsed ? t("channels.manageAgents") : undefined} onClick={onManageAgents}><UsersRound aria-hidden="true" /><span>{t("channels.manageAgents")}</span></button>
          <SidebarAccountMenu disabled={!initialized} onOpenSettings={onOpenSettings} onOpenAccount={onOpenAccount} />
        </div>
      </div>
      {contextMenu && initialized && contextConversation ? <ThreadContextMenu
        x={contextMenu.x} y={contextMenu.y}
        items={contextItems}
        onClose={() => setContextMenu(null)}
      /> : null}
    </aside>
  );
}
