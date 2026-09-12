import { Code2, MessagesSquare, PanelLeftOpen, Pin, Plus, Search, UsersRound } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { AppModeSwitch } from "./AppModeSwitch";
import { ChannelGroupAvatar } from "./ChannelGroupAvatar";
import { collaborationConversations } from "./CollaborationConversations";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { SidebarAccountMenu } from "./SidebarAccountMenu";
import { useI18n } from "./i18n";

export function CollaborationSidebar({
  initialized, agents, rooms, pinnedRoomIDs = [], selectedAgentID, selectedRoomID,
  collapsed = false, onToggleCollapsed,
  onSelectAgent, onSelectRoom, onManageAgents, onCreateAgent, onCreateRoom,
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
  onCreateAgent: () => void;
  onCreateRoom: () => void;
  onSwitchToHarness: () => void;
  onOpenSettings: (page?: "providers" | "usage") => void;
  onOpenAccount?: () => void;
  onPointerEnter?: () => void;
  onPointerLeave?: (event: ReactPointerEvent<HTMLElement>) => void;
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const [query, setQuery] = useState("");
  const [newMenuOpen, setNewMenuOpen] = useState(false);
  const newButtonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const menuID = useId();
  const conversations = useMemo(
    () => collaborationConversations(agents, rooms, pinnedRoomIDs, collapsed ? "" : query),
    [agents, rooms, pinnedRoomIDs, query, collapsed],
  );
  const agentNames = useMemo(() => new Map(agents.map((agent) => [agent.id, agent.name])), [agents]);
  useEffect(() => {
    if (!newMenuOpen) return;
    menuRef.current?.querySelector<HTMLButtonElement>("button")?.focus();
    const outside = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node) && !newButtonRef.current?.contains(event.target as Node)) setNewMenuOpen(false);
    };
    document.addEventListener("pointerdown", outside, true);
    return () => document.removeEventListener("pointerdown", outside, true);
  }, [newMenuOpen]);
  const closeMenu = () => { setNewMenuOpen(false); newButtonRef.current?.focus(); };
  const newConversationButton = <button ref={newButtonRef} className="icon-button" type="button" disabled={!initialized}
    aria-label={t("channels.newConversation")} title={t("channels.newConversation")}
    aria-haspopup="menu" aria-expanded={newMenuOpen} aria-controls={newMenuOpen ? menuID : undefined}
    onClick={() => setNewMenuOpen((open) => !open)}
    onKeyDown={(event) => { if (event.key === "ArrowDown") { event.preventDefault(); setNewMenuOpen(true); } }}>
    <Plus aria-hidden="true" />
  </button>;

  return (
    <aside className={`sidebar collaboration-sidebar${collapsed ? " collaboration-sidebar-rail" : ""}`} data-wuu-component="collaboration-sidebar"
      onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}>
      <div className="sidebar-content">
        <div className="collaboration-sidebar-topbar">
          {!collapsed ? newConversationButton : null}
          {newMenuOpen ? <FloatingMenuPortal anchorRef={newButtonRef} owner="collaboration-new" placement={collapsed ? "above" : "below"} align="right" width={200}>
            <div ref={menuRef} id={menuID} className="select-menu-panel" role="menu" aria-label={t("channels.newConversation")}
              onBlur={(event) => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node) && event.relatedTarget !== newButtonRef.current) setNewMenuOpen(false); }}
              onKeyDown={(event) => {
                if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeMenu(); }
                if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
                  event.preventDefault();
                  const buttons = Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>("button") ?? []);
                  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
                  const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
                  buttons[next]?.focus();
                }
              }}>
              <button className="select-menu-item" role="menuitem" type="button" onClick={() => { closeMenu(); onCreateAgent(); }}><UsersRound size={16} aria-hidden="true" /><span>{t("channels.newAgent")}</span></button>
              <button className="select-menu-item" role="menuitem" type="button" onClick={() => { closeMenu(); onCreateRoom(); }}><MessagesSquare size={16} aria-hidden="true" /><span>{t("channels.newGroup")}</span></button>
            </div>
          </FloatingMenuPortal> : null}
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
          {conversations.map(({ id, name, agent, room, pinned }) => {
            const selected = room ? selectedRoomID === room.id : selectedAgentID === agent?.id;
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
                {agent ? <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} status={thinking ? "thinking" : "idle"} />
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
          {conversations.length === 0 ? <div className="collaboration-contact-empty">{t(query.trim() ? "channels.noMatchingConversations" : "channels.noConversations")}</div> : null}
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
