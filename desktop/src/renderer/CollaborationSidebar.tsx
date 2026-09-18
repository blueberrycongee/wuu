import { AgentOnboardingAvatar } from "./AgentOnboardingAvatar";
import { ChevronRight, Code2, Copy, EyeOff, PanelLeftOpen, Pencil, Pin, PinOff, Plus, Search, Trash2 } from "lucide-react";
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
  collapsed = false, onToggleCollapsed, embedded = false,
  onSelectAgent, onSelectRoom, onCreateRoom, draftAgent, draftSelected, onSelectDraft,
  onEditAgent, onEditRoom,
  onTogglePinned, onHideConversation, onDeleteConversation,
  onSwitchToHarness, onOpenSettings, onOpenAccount, onPointerEnter, onPointerLeave,
}: {
  initialized: boolean;
  embedded?: boolean;
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
  // Retain the callback contract while the footer entry is temporarily hidden.
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
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [sectionCollapsed, setSectionCollapsed] = useState(false);
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
  const newConversationButton = <button className="icon-button" type="button" disabled={!initialized}
    aria-label={t("channels.newConversation")} title={t("channels.newConversation")} onClick={onCreateRoom}>
    <Plus aria-hidden="true" />
  </button>;

  const Container = embedded ? "section" : "aside";
  return (
    <Container className={embedded ? "sidebar-functional-group collaboration-sidebar-section" : `sidebar collaboration-sidebar${collapsed ? " collaboration-sidebar-rail" : ""}`} data-wuu-component="collaboration-sidebar"
      onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}>
      <div className={embedded ? undefined : "sidebar-content"}>
        {embedded ? <div className="sidebar-functional-heading">
          <button type="button" className="sidebar-functional-heading-toggle" aria-expanded={!sectionCollapsed}
            onClick={() => setSectionCollapsed((value) => !value)}>
            <ChevronRight className="sidebar-functional-heading-chevron" data-expanded={!sectionCollapsed || undefined} aria-hidden="true" />
            <span className="sidebar-functional-heading-label">{t("sidebar.collaboration")}</span>
          </button>
          <div className="sidebar-functional-heading-action">{newConversationButton}</div>
        </div> : <div className="collaboration-sidebar-topbar">
          {!collapsed ? newConversationButton : null}

        </div>}
        {!embedded && !collapsed ? <AppModeSwitch mode="collaboration" /> : null}
        {!embedded && !collapsed ? <div className="collaboration-sidebar-tools">
          <label className="collaboration-sidebar-search">
            <Search aria-hidden="true" />
            <input type="search" value={query} placeholder={t("channels.searchConversations")}
              aria-label={t("channels.searchConversations")} onChange={(event) => setQuery(event.currentTarget.value)} />
          </label>
        </div> : null}
        <nav className="collaboration-sidebar-main" hidden={embedded && sectionCollapsed} data-scroll-fade={embedded ? undefined : ""} aria-label={t("channels.conversations")}>
          {draftAgent ? <button type="button" className={`collaboration-contact-row${draftSelected ? " active" : ""}`} aria-current={draftSelected ? "page" : undefined} onClick={onSelectDraft} aria-label={draftAgent.name || t("channels.newAgent")}>
            <span className="collaboration-contact-avatar" aria-hidden="true"><AgentOnboardingAvatar avatarKey={draftAgent.avatarKey} /></span>
            <span className="collaboration-contact-copy"><span className="collaboration-contact-heading"><strong>{draftAgent.name || t("channels.newAgent")}</strong></span></span>
          </button> : null}
          {conversations.map(({ id, name, agent, room, pinned }) => {
            const selected = !draftSelected && (room ? selectedRoomID === room.id : selectedAgentID === agent?.id);
            const unread = selected ? 0 : (room?.unread_count ?? 0);
            // An agent's avatar stays active across all of its rooms.
            const avatarThinking = agent?.activity_status === "thinking" || room?.activity_status === "thinking";
            return <button key={id} type="button" className={`collaboration-contact-row${selected ? " active" : ""}${unread > 0 ? " has-unread" : ""}${pinned ? " pinned" : ""}`}
              aria-current={selected ? "page" : undefined} disabled={!initialized}
              aria-label={collapsed ? `${name}${unread > 0 ? `, ${t("channels.unreadMessages", { count: unread })}` : ""}` : undefined}
              title={`${name}${avatarThinking ? ` · ${t("channels.agentWorking")}` : ""}`}
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
                {agent ? <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} status={avatarThinking ? "thinking" : "idle"} motion="expressive" />
                  : room ? <ChannelGroupAvatar room={room} agents={agents} /> : null}
              </span>
              {collapsed && unread > 0 ? <span className="collaboration-rail-unread" aria-hidden="true">{unread > 99 ? "99+" : unread}</span> : null}
              <span className="collaboration-contact-copy">
                <span className="collaboration-contact-heading"><strong>{name}</strong>
                  {unread > 0 ? <span className="collaboration-contact-unread" aria-label={t("channels.unreadMessages", { count: unread })}>{unread > 99 ? "99+" : unread}</span>
                    : pinned ? <Pin className="collaboration-contact-pin" aria-label={t("channels.pinnedConversation")} /> : null}
                </span>
              </span>
            </button>;
          })}
          {!collapsed && query.trim() && conversations.length === 0 && !draftAgent ? <div className="collaboration-contact-empty">{t("channels.noMatchingConversations")}</div> : null}
        </nav>
        {!embedded ? <div className="collaboration-sidebar-footer">
          {collapsed ? <>
            <button className="collaboration-sidebar-footer-action" type="button" aria-label={t("app.expandLeftSidebar")} title={t("app.expandLeftSidebar")} onClick={onToggleCollapsed}><PanelLeftOpen aria-hidden="true" /></button>
            {newConversationButton}
            <button className="collaboration-sidebar-footer-action" type="button" aria-label={t("sidebar.harness")} title={t("sidebar.harness")} onClick={onSwitchToHarness}><Code2 aria-hidden="true" /></button>
          </> : null}
          <SidebarAccountMenu disabled={!initialized} onOpenSettings={onOpenSettings} onOpenAccount={onOpenAccount} />
        </div> : null}
      </div>
      {contextMenu && initialized && contextConversation ? <ThreadContextMenu
        x={contextMenu.x} y={contextMenu.y}
        items={contextItems}
        onClose={() => setContextMenu(null)}
      /> : null}
    </Container>
  );
}
