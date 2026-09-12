import { Code2, PanelLeftOpen, Pin, Plus, Search, UsersRound } from "lucide-react";
import { useMemo, useState, type PointerEvent as ReactPointerEvent } from "react";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { AppModeSwitch } from "./AppModeSwitch";
import { ChannelGroupAvatar } from "./ChannelGroupAvatar";
import { collaborationConversations } from "./CollaborationConversations";
import { SidebarAccountMenu } from "./SidebarAccountMenu";
import { useI18n } from "./i18n";


export function CollaborationSidebar({
  initialized, agents, rooms, pinnedRoomIDs = [], selectedAgentID, selectedRoomID,
  collapsed = false, onToggleCollapsed,
  onSelectAgent, onSelectRoom, onManageAgents, onCreateRoom, draftAgent, draftSelected, onSelectDraft,
  onSwitchToHarness, onOpenSettings, onOpenAccount, onPointerEnter, onPointerLeave,
}: {
  initialized: boolean;
  collapsed?: boolean;
  onToggleCollapsed?: () => void;
  agents: NamedAgent[];
  rooms: ChannelRoom[];
  pinnedRoomIDs?: readonly string[];
  selectedAgentID?: string;
  selectedRoomID?: string;
  onSelectAgent: (agentID: string) => void;
  onSelectRoom: (roomID: string) => void;
  onManageAgents: () => void;
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
  const conversations = useMemo(
    () => collaborationConversations(agents, rooms, pinnedRoomIDs, collapsed ? "" : query),
    [agents, rooms, pinnedRoomIDs, query, collapsed],
  );
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
    </aside>
  );
}
