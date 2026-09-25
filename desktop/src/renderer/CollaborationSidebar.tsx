import { AgentOnboardingAvatar } from "./AgentOnboardingAvatar";
import { ChevronRight, Code2, PanelLeftOpen, Plus, Search } from "./WuuIcons";
import { useMemo, useState, type PointerEvent as ReactPointerEvent } from "react";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { AppModeSwitch } from "./AppModeSwitch";
import { CollaborationConversationRow } from "./CollaborationConversationRow";
import { collaborationConversations, type CollaborationConversation } from "./CollaborationConversations";
import { SidebarAccountMenu } from "./SidebarAccountMenu";
import { useSidebarSectionDragHandle } from "./SidebarSection";
import { useI18n } from "./i18n";

export function CollaborationSidebar({
  initialized, agents, rooms, pinnedRoomIDs = [], archivedRoomIDs = [], selectedAgentID, selectedRoomID,
  collapsed = false, onToggleCollapsed, embedded = false,
  sectionCollapsed = false, onToggleSectionCollapsed,
  onSelectAgent, onSelectRoom, onCreateRoom, draftAgent, draftSelected, onSelectDraft,
  onEditAgent, onEditRoom,
  onTogglePinned, onHideConversation, onDeleteConversation,
  onSwitchToHarness, onOpenSettings, onOpenAccount, onPointerEnter, onPointerLeave,
}: {
  initialized: boolean;
  embedded?: boolean;
  collapsed?: boolean;
  onToggleCollapsed?: () => void;
  sectionCollapsed?: boolean;
  onToggleSectionCollapsed?: () => void;
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
  const sectionDragHandle = useSidebarSectionDragHandle();
  const dragHandle = embedded ? sectionDragHandle : null;
  const [query, setQuery] = useState("");
  const conversations = useMemo(() => {
    const items = collaborationConversations(agents, rooms, pinnedRoomIDs, collapsed ? "" : query, archivedRoomIDs);
    // Embedded in the shared sidebar, pinning leaves this section for the
    // Pinned group. Standalone / rail mode still keeps pinned rooms first.
    return embedded
      ? items.filter((conversation) => !conversation.pinned)
      : [...items].sort((left, right) => Number(right.pinned) - Number(left.pinned));
  }, [agents, rooms, pinnedRoomIDs, archivedRoomIDs, query, collapsed, embedded]);
  // Embedded headings share the sidebar's trailing accessory column with the
  // built-in group actions and use the same control; the standalone topbar and
  // footer keep the generic icon button.
  const newConversationButton = (actionClass: string) => <button className={actionClass} type="button" disabled={!initialized}
    aria-label={t("channels.newConversation")} title={t("channels.newConversation")} onClick={onCreateRoom}>
    <Plus aria-hidden="true" />
  </button>;

  const Container = embedded ? "section" : "aside";
  return (
    <Container className={embedded ? `${dragHandle ? "" : "sidebar-functional-group "}collaboration-sidebar-section` : `sidebar collaboration-sidebar${collapsed ? " collaboration-sidebar-rail" : ""}`} data-wuu-component="collaboration-sidebar"
      onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}>
      <div className={embedded ? undefined : "sidebar-content"}>
        {embedded ? <div className="sidebar-functional-heading">
          <button type="button" className="sidebar-functional-heading-toggle" aria-expanded={!sectionCollapsed}
            onPointerDown={dragHandle?.dragHandleProps.onPointerDown}
            onClick={() => { if (!dragHandle?.isDragging) onToggleSectionCollapsed?.(); }}>
            <span className="sidebar-functional-heading-label">{t("sidebar.collaboration")}</span>
            <ChevronRight className="sidebar-functional-heading-chevron" data-expanded={!sectionCollapsed || undefined} aria-hidden="true" />
          </button>
          <div className="sidebar-functional-heading-action">{newConversationButton("sidebar-functional-action")}</div>
        </div> : <div className="collaboration-sidebar-topbar">
          {!collapsed ? newConversationButton("icon-button") : null}

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
          {conversations.map((conversation) => {
            const { agent, room } = conversation;
            const selected = !draftSelected && (room ? selectedRoomID === room.id : selectedAgentID === agent?.id);
            return <CollaborationConversationRow
              key={conversation.id}
              conversation={conversation}
              agents={agents}
              selected={selected}
              initialized={initialized}
              collapsed={collapsed}
              onSelect={() => { if (room) onSelectRoom(room.id); else if (agent) onSelectAgent(agent.id); }}
              onTogglePinned={onTogglePinned}
              onHideConversation={onHideConversation}
              onDeleteConversation={onDeleteConversation}
              onEditAgent={onEditAgent}
              onEditRoom={onEditRoom}
            />;
          })}
          {!collapsed && query.trim() && conversations.length === 0 && !draftAgent ? <div className="collaboration-contact-empty">{t("channels.noMatchingConversations")}</div> : null}
        </nav>
        {!embedded ? <div className="collaboration-sidebar-footer">
          {collapsed ? <>
            <button className="collaboration-sidebar-footer-action" type="button" aria-label={t("app.expandLeftSidebar")} title={t("app.expandLeftSidebar")} onClick={onToggleCollapsed}><PanelLeftOpen aria-hidden="true" /></button>
            {newConversationButton("icon-button")}
            <button className="collaboration-sidebar-footer-action" type="button" aria-label={t("sidebar.harness")} title={t("sidebar.harness")} onClick={onSwitchToHarness}><Code2 aria-hidden="true" /></button>
          </> : null}
          <SidebarAccountMenu disabled={!initialized} onOpenSettings={onOpenSettings} onOpenAccount={onOpenAccount} />
        </div> : null}
      </div>
    </Container>
  );
}
