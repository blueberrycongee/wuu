import { SidebarAccountMenu } from "./SidebarAccountMenu";
import {
  MessageSquare,
  MessagesSquare,
  Plus,
  Search,
  Settings,
  UserRound,
  UsersRound,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";
import {
  closestCenter,
  DndContext,
  DragOverlay,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import type { ChannelRoom, NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { AppModeSwitch } from "./AppModeSwitch";
import { ChannelGroupAvatar } from "./ChannelGroupAvatar";
import { formatChannelUnreadCount } from "./ChannelView";
import { SidebarSection } from "./SidebarSection";
import {
  reorderSidebarSections,
  SidebarSectionDragPreview,
  SortableSidebarSection,
  type SidebarSectionHeaderInfo,
} from "./SortableSidebarSection";
import { SidebarPointerSensor } from "./SidebarPointerSensor";
import { useI18n } from "./i18n";

const COLLABORATION_AGENTS_SECTION_ID = "collaboration-agents";
const COLLABORATION_GROUPS_SECTION_ID = "collaboration-groups";
const DEFAULT_COLLABORATION_SECTION_ORDER = [
  COLLABORATION_AGENTS_SECTION_ID,
  COLLABORATION_GROUPS_SECTION_ID,
];
const COLLABORATION_SECTION_ORDER_KEY = "wuu.desktop.collaborationSidebarSectionOrder";
const COLLABORATION_COLLAPSED_SECTION_IDS_KEY = "wuu.desktop.collaborationCollapsedSectionIDs";

function storedCollaborationSectionOrder(): string[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(COLLABORATION_SECTION_ORDER_KEY) ?? "[]");
    const stored = Array.isArray(parsed) ? parsed.filter(
      (id): id is string => DEFAULT_COLLABORATION_SECTION_ORDER.includes(id),
    ) : [];
    return [
      ...new Set(stored),
      ...DEFAULT_COLLABORATION_SECTION_ORDER.filter((id) => !stored.includes(id)),
    ];
  } catch {
    return DEFAULT_COLLABORATION_SECTION_ORDER;
  }
}

function storedCollaborationCollapsedSectionIDs(): Set<string> {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(COLLABORATION_COLLAPSED_SECTION_IDS_KEY) ?? "[]");
    return new Set(Array.isArray(parsed) ? parsed.filter(
      (id): id is string => DEFAULT_COLLABORATION_SECTION_ORDER.includes(id),
    ) : []);
  } catch {
    return new Set();
  }
}

function searchable(value: string): string {
  return value.normalize("NFKD").replace(/\p{M}/gu, "").toLocaleLowerCase();
}

export function CollaborationSidebar({
  initialized,
  agents,
  rooms,
  selectedAgentID,
  selectedRoomID,
  onSelectAgent,
  onSelectRoom,
  onManageAgents,
  onCreateRoom,
  onSwitchToHarness,
  onOpenSettings,
  onOpenAccount,
  onPointerEnter,
  onPointerLeave,
}: {
  initialized: boolean;
  agents: NamedAgent[];
  rooms: ChannelRoom[];
  selectedAgentID?: string;
  selectedRoomID?: string;
  onSelectAgent: (agentID: string) => void;
  onSelectRoom: (roomID: string) => void;
  onManageAgents: () => void;
  onCreateRoom: () => void;
  onSwitchToHarness: () => void;
  onOpenSettings: (page?: "providers" | "usage") => void;
  onOpenAccount?: () => void;
  onPointerEnter?: () => void;
  onPointerLeave?: (event: ReactPointerEvent<HTMLElement>) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [sectionOrder, setSectionOrder] = useState(storedCollaborationSectionOrder);
  const [collapsedSectionIDs, setCollapsedSectionIDs] = useState(storedCollaborationCollapsedSectionIDs);
  const [draggingSectionID, setDraggingSectionID] = useState<string>();
  const sectionHeaderInfoByIDRef = useRef<Map<string, SidebarSectionHeaderInfo>>(new Map());
  const sensors = useSensors(
    useSensor(SidebarPointerSensor, { activationConstraint: { distance: 6 } }),
  );
  const normalizedQuery = searchable(query.trim());
  const visibleAgents = useMemo(
    () => normalizedQuery
      ? agents.filter((agent) => searchable(agent.name).includes(normalizedQuery))
      : agents,
    [agents, normalizedQuery],
  );
  const visibleRooms = useMemo(
    () => normalizedQuery
      ? rooms.filter((room) => room.kind === "channel" && searchable(room.name).includes(normalizedQuery))
      : rooms.filter((room) => room.kind === "channel"),
    [normalizedQuery, rooms],
  );
  const directMessagesByAgentID = useMemo(() => {
    const result = new Map<string, ChannelRoom>();
    for (const room of rooms) {
      if (room.kind !== "dm") continue;
      const agentID = room.members.find((member) => member.member_type === "agent")?.member_id;
      if (agentID) result.set(agentID, room);
    }
    return result;
  }, [rooms]);
  useEffect(() => {
    window.localStorage.setItem(COLLABORATION_SECTION_ORDER_KEY, JSON.stringify(sectionOrder));
  }, [sectionOrder]);
  useEffect(() => {
    window.localStorage.setItem(
      COLLABORATION_COLLAPSED_SECTION_IDS_KEY,
      JSON.stringify([...collapsedSectionIDs]),
    );
  }, [collapsedSectionIDs]);
  const registerSectionHeaderInfo = useCallback(
    (id: string, info: SidebarSectionHeaderInfo | null): void => {
      if (info) sectionHeaderInfoByIDRef.current.set(id, info);
      else sectionHeaderInfoByIDRef.current.delete(id);
    },
    [],
  );
  function toggleSection(sectionID: string): void {
    setCollapsedSectionIDs((current) => {
      const next = new Set(current);
      if (next.has(sectionID)) next.delete(sectionID);
      else next.add(sectionID);
      return next;
    });
  }
  function handleDragStart(event: DragStartEvent): void {
    setDraggingSectionID(String(event.active.id));
  }
  function handleDragEnd(event: DragEndEvent): void {
    setSectionOrder((current) => reorderSidebarSections(
      current,
      String(event.active.id),
      event.over ? String(event.over.id) : undefined,
    ));
    setDraggingSectionID(undefined);
  }
  const draggingSectionInfo = draggingSectionID
    ? sectionHeaderInfoByIDRef.current.get(draggingSectionID)
    : undefined;

  return (
    <aside
      className="sidebar collaboration-sidebar"
      data-wuu-component="collaboration-sidebar"
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
    >
      <div className="sidebar-content">
        <div className="traffic-spacer" />
        <AppModeSwitch
          mode="collaboration"
          collaborationEnabled
          onChange={(mode) => { if (mode === "harness") onSwitchToHarness(); }}
        />

        <div className="collaboration-sidebar-tools">
          <label className="collaboration-sidebar-search">
            <Search aria-hidden="true" />
            <input
              type="search"
              value={query}
              placeholder={t("channels.searchConversations")}
              aria-label={t("channels.searchConversations")}
              onChange={(event) => setQuery(event.currentTarget.value)}
            />
          </label>
        </div>

        <DndContext
          sensors={sensors}
          collisionDetection={closestCenter}
          modifiers={[restrictToVerticalAxis]}
          onDragStart={handleDragStart}
          onDragEnd={handleDragEnd}
          onDragCancel={() => setDraggingSectionID(undefined)}
        >
          <SortableContext items={sectionOrder} strategy={verticalListSortingStrategy}>
            <div className="collaboration-sidebar-main">
              {sectionOrder.map((sectionID) => {
                const isAgents = sectionID === COLLABORATION_AGENTS_SECTION_ID;
                const label = t(isAgents ? "channels.agents" : "channels.groups");
                const expanded = !collapsedSectionIDs.has(sectionID);
                const toggleLabel = t(expanded ? "sidebar.collapseSection" : "sidebar.expandSection", { section: label });
                const CollapsedIcon = isAgents ? UserRound : MessageSquare;
                const ExpandedIcon = isAgents ? UsersRound : MessagesSquare;
                return (
                  <SortableSidebarSection
                    key={sectionID}
                    id={sectionID}
                    className="project-section collaboration-contact-section"
                    ariaLabel={label}
                    headerInfo={{ label, iconKind: isAgents ? "agents" : "groups", CollapsedIcon, ExpandedIcon }}
                    registerHeaderInfo={registerSectionHeaderInfo}
                  >
                    <SidebarSection
                      expanded={expanded}
                      iconKind={isAgents ? "agents" : "groups"}
                      CollapsedIcon={CollapsedIcon}
                      ExpandedIcon={ExpandedIcon}
                      label={label}
                      ariaLabel={toggleLabel}
                      title={toggleLabel}
                      onToggle={() => toggleSection(sectionID)}
                      actions={(
                        <button
                          className="sidebar-functional-action sidebar-section-add-action"
                          type="button"
                          aria-label={t(isAgents ? "channels.manageAgents" : "channels.newRoom")}
                          title={t(isAgents ? "channels.manageAgents" : "channels.newRoom")}
                          onClick={isAgents ? onManageAgents : onCreateRoom}
                        >
                          {isAgents ? <Settings aria-hidden="true" /> : <Plus aria-hidden="true" />}
                        </button>
                      )}
                    >
                      <div className="collaboration-contact-list">
                        {isAgents ? visibleAgents.map((agent) => {
                          const directMessage = directMessagesByAgentID.get(agent.id);
                          const selected = selectedAgentID === agent.id
                            || Boolean(selectedRoomID && directMessage?.id === selectedRoomID);
                          const unread = selected ? 0 : (directMessage?.unread_count ?? 0);
                          return (
                            <button
                              className={`collaboration-contact-row${selected ? " active" : ""}${unread > 0 ? " has-unread" : ""}`}
                              type="button"
                              aria-current={selected ? "page" : undefined}
                              key={agent.id}
                              onClick={() => onSelectAgent(agent.id)}
                            >
                              <span className="collaboration-contact-avatar" aria-hidden="true">
                                <AgentAvatarMark
                                  seed={agent.id}
                                  avatarKey={agent.avatar_key}
                                  avatarImage={agent.avatar_image}
                                  status={agent.activity_status === "thinking" ? "thinking" : "idle"}
                                />
                              </span>
                              <span className="collaboration-contact-copy"><strong>{agent.name}</strong></span>
                              {unread > 0 ? <span className="collaboration-contact-unread">{formatChannelUnreadCount(unread)}</span> : null}
                            </button>
                          );
                        }) : visibleRooms.map((room) => {
                          const selected = selectedRoomID === room.id && !selectedAgentID;
                          const unread = selected ? 0 : (room.unread_count ?? 0);
                          return (
                            <button
                              className={`collaboration-contact-row${selected ? " active" : ""}${unread > 0 ? " has-unread" : ""}`}
                              type="button"
                              aria-current={selected ? "page" : undefined}
                              key={room.id}
                              onClick={() => onSelectRoom(room.id)}
                            >
                              <span className="collaboration-contact-avatar collaboration-room-avatar" aria-hidden="true">
                                <ChannelGroupAvatar room={room} agents={agents} />
                              </span>
                              <span className="collaboration-contact-copy"><strong>{room.name}</strong></span>
                              {unread > 0 ? <span className="collaboration-contact-unread">{formatChannelUnreadCount(unread)}</span> : null}
                            </button>
                          );
                        })}
                        {normalizedQuery && (isAgents ? visibleAgents.length : visibleRooms.length) === 0 ? (
                          <div className="collaboration-contact-empty">
                            {t(isAgents ? "channels.noMatchingAgents" : "channels.noMatchingConversations")}
                          </div>
                        ) : null}
                      </div>
                    </SidebarSection>
                  </SortableSidebarSection>
                );
              })}
            </div>
          </SortableContext>
          <DragOverlay dropAnimation={{ duration: 150, easing: "cubic-bezier(0.16, 1, 0.3, 1)" }}>
            {draggingSectionInfo ? <SidebarSectionDragPreview info={draggingSectionInfo} /> : null}
          </DragOverlay>
        </DndContext>

        <div className="sidebar-settings">
          <SidebarAccountMenu disabled={!initialized} onOpenSettings={onOpenSettings} onOpenAccount={onOpenAccount} />
        </div>
      </div>
    </aside>
  );
}
