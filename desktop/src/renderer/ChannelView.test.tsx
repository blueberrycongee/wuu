import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ChannelMessage, ChannelResponse, ChannelRoom, CollaborationSessionBinding, InitializeResult, NamedAgent, WuuDesktopApi } from "../shared/protocol";
import { graphDensityScale } from "./AgentRelationshipGraph";
import { groupAvatarRowSizes } from "./ChannelGroupAvatar";
import { assignmentState, ChannelView, formatChannelUnreadCount } from "./ChannelView";
import { clearToasts, ToastViewport } from "./Toast";
import { userFacingErrorForMessage } from "./UserFacingErrors";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement;
let root: Root | null = null;

const agents: NamedAgent[] = [
  {
    id: "agent-1",
    name: "Alpha",
    memory_dir: "/agents/agent-1/memory",
    avatar_key: "abstract-3",
    avatar_image: "data:image/png;base64,iVBORw0KGgo=",
    model_override: "",
    autostart: true,
    created_at: "2026-07-23T00:00:00Z",
    activity_status: "thinking",
    activity_room_ids: ["room-1"],
  },
  {
    id: "agent-2",
    name: "Beta",
    memory_dir: "/agents/agent-2/memory",
    avatar_key: "abstract-6",
    model_override: "",
    autostart: true,
    created_at: "2026-07-23T00:00:00Z",
    activity_status: "idle",
  },
];

const rooms: ChannelRoom[] = [
  {
    id: "room-1",
    name: "general",
    kind: "channel",
    created_by: "human",
    members: [
      { room_id: "room-1", member_type: "agent", member_id: "agent-1", joined_at: "2026-07-23T00:00:00Z" },
      { room_id: "room-1", member_type: "agent", member_id: "agent-2", joined_at: "2026-07-23T00:00:00Z" },
    ],
    created_at: "2026-07-23T00:00:00Z",
  },
  {
    id: "room-2",
    name: "research",
    kind: "channel",
    created_by: "human",
    members: [],
    created_at: "2026-07-23T00:00:00Z",
  },
];

function createApi(): Partial<WuuDesktopApi> {
  return {
    revealWorkspaceItem: vi.fn(async () => undefined),
    bootstrapChannels: vi.fn(async () => ({ agents, rooms })),
    listNamedAgents: vi.fn(async () => ({ agents })),
    createNamedAgent: vi.fn(async (params) => ({ agent: { ...agents[0], name: params.name } })),
    updateNamedAgent: vi.fn(async (params) => ({ agent: { ...agents[0], name: params.name } })),
    deleteNamedAgent: vi.fn(async () => ({ deleted: true })),
    startNamedAgent: vi.fn(async () => ({ agent: agents[0] })),
    resetNamedAgent: vi.fn(async () => ({
      agent: agents[0],
      wake_state: {
        agent_id: "agent-1",
        outstanding: false,
        pending: false,
        updated_at: "2026-07-23T00:00:00Z",
      },
      requested: true,
      thread_id: "channel-agent-agent-1",
    })),
    resolveChannelAgentCreation: vi.fn(async (params) => ({ proposal: {
      id: params.proposal_id,
      message_id: "proposal-message",
      room_id: "room-1",
      name: "Designer",
      role: "Design delivery",
      state: params.approve ? "approved" as const : "cancelled" as const,
      provider: params.provider,
      model: params.model,
      created_at: "2026-07-23T00:03:00Z",
    } })),
    listChannelRooms: vi.fn(async () => ({ rooms })),
    markChannelRoomRead: vi.fn(async () => ({ read: true })),
    createChannelRoom: vi.fn(async (params) => ({ room: { ...rooms[1], name: params.name } })),
    updateChannelRoom: vi.fn(async (params) => ({ room: { ...rooms[0], avatar_image: params.avatar_image } })),
    deleteChannelRoom: vi.fn(async () => ({ deleted: true })),
    listChannelMessages: vi.fn(async ({ room_id }) => ({
      messages: room_id === "room-1"
        ? [{
            id: "message-1",
            room_id,
            seq: 1,
            author_type: "agent" as const,
            author_id: "agent-1",
            kind: "text" as const,
            body: "Hello from **Alpha** with `markdown`\n\n<img src=x onerror=alert(1)>",
            created_at: "2026-07-23T00:00:00Z",
          }, {
            id: "message-2",
            room_id,
            seq: 2,
            author_type: "human" as const,
            author_id: "human",
            kind: "text" as const,
            body: "Human direction",
            images: [{ media_type: "image/png", data: "aW1hZ2U=" }],
            files: [{ media_type: "application/pdf", data: "cGRm", filename: "brief.pdf" }],
            created_at: "2026-07-23T00:00:30Z",
          }, {
            id: "task-1",
            room_id,
            seq: 3,
            author_type: "human" as const,
            author_id: "human",
            kind: "task" as const,
            body: "Investigate flaky build",
            task_state: "doing",
            task_owner: "agent-1",
            created_at: "2026-07-23T00:01:00Z",
          }, {
            id: "message-3",
            room_id,
            seq: 4,
            thread_id: "message-1",
            reply_to: "message-1",
            author_type: "agent" as const,
            author_id: "agent-2",
            kind: "text" as const,
            body: "A threaded answer",
            created_at: "2026-07-23T00:02:00Z",
          }]
        : [],
    })),
    sendChannelMessage: vi.fn(async (params) => ({
      message: {
        id: "message-2",
        room_id: params.room_id,
        seq: 2,
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: params.body,
        created_at: "2026-07-23T00:01:00Z",
      },
    })),
    createChannelTask: vi.fn(async (params) => ({
      task: {
        id: "task-1",
        room_id: params.room_id,
        seq: 3,
        author_type: "human" as const,
        author_id: "human",
        kind: "task" as const,
        body: params.title,
        task_state: "open",
        task_owner: params.owner_id,
        created_at: "2026-07-23T00:02:00Z",
      },
    })),
    updateChannelTask: vi.fn(async (params) => ({
      task: {
        id: params.task_id,
        room_id: "room-1",
        seq: 3,
        author_type: "human" as const,
        author_id: "human",
        kind: "task" as const,
        body: "Investigate",
        task_state: params.state ?? "open",
        task_owner: "agent-1",
        created_at: "2026-07-23T00:02:00Z",
      },
    })),
  };
}

async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function setInputValue(input: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  const prototype = input instanceof HTMLTextAreaElement
    ? HTMLTextAreaElement.prototype
    : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

function setSelectValue(select: HTMLSelectElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
  setter?.call(select, value);
  select.dispatchEvent(new Event("change", { bubbles: true }));
}

beforeEach(() => {
  clearToasts();
  window.localStorage.clear();
  container = document.createElement("div");
  document.body.appendChild(container);
  Object.defineProperty(Element.prototype, "scrollIntoView", {
    configurable: true,
    value: vi.fn(),
  });
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
  clearToasts();
  vi.restoreAllMocks();
});

describe("ChannelView", () => {
  it("preserves verifier task states for honest room activity", () => {
    expect(assignmentState("checking")).toBe("checking");
    expect(assignmentState("revising")).toBe("revising");
    expect(assignmentState("needs_human")).toBe("needs_human");
  });

  it("does not start a second directory poll when the parent owns the directory", async () => {
    vi.useFakeTimers();
    try {
      const api = createApi();
      const onDirectoryAgentsChange = vi.fn();
      const onDirectoryRoomsChange = vi.fn();
      Object.defineProperty(window, "wuu", { configurable: true, value: api });
      root = createRoot(container);
      act(() => root?.render(
        <ChannelView
          directoryAgents={agents}
          directoryRooms={rooms}
          onDirectoryAgentsChange={onDirectoryAgentsChange}
          onDirectoryRoomsChange={onDirectoryRoomsChange}
          selectedRoomID="room-2"
        />,
      ));
      await settle();

      expect(api.bootstrapChannels).not.toHaveBeenCalled();
      expect(api.listNamedAgents).not.toHaveBeenCalled();
      expect(api.listChannelRooms).not.toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(3_000);
      });

      expect(api.listNamedAgents).not.toHaveBeenCalled();
      expect(api.listChannelRooms).not.toHaveBeenCalled();
      expect(onDirectoryAgentsChange).not.toHaveBeenCalled();
      expect(onDirectoryRoomsChange).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps Room-created roles pending until the user chooses a configured model", async () => {
    const api = createApi();
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({ messages: room_id === "room-1" ? [{
      id: "proposal-message", room_id, seq: 1, author_type: "human" as const, author_id: "human",
      kind: "system" as const, body: "建议创建新角色：Designer", created_at: "2026-07-23T00:03:00Z",
      agent_creation_proposal: {
        id: "proposal-1", message_id: "proposal-message", room_id, name: "Designer", role: "Design delivery",
        state: "pending" as const, created_at: "2026-07-23T00:03:00Z",
      },
    }] : [] }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    const initialized = {
      protocol_version: "1", provider: "openai", model: "gpt-default", workspace_root: "/workspace",
      providers: [
        { name: "openai", type: "openai", model: "gpt-default", models: [{ id: "gpt-default", display_name: "GPT Default" }, { id: "gpt-design", display_name: "GPT Design" }] },
        { name: "anthropic", type: "anthropic", model: "claude-sonnet", models: [{ id: "claude-sonnet", display_name: "Claude Sonnet" }] },
      ],
    } as InitializeResult;
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" initialized={initialized} />));
    await settle();

    const card = container.querySelector(".channel-agent-proposal");
    expect(card?.textContent).toContain("Designer");
    const select = card?.querySelector<HTMLSelectElement>("select");
    expect(Array.from(select?.options ?? []).map((option) => option.textContent)).toEqual(expect.arrayContaining(["openai · GPT Default", "openai · GPT Design", "anthropic · Claude Sonnet"]));
    act(() => setSelectValue(select!, "anthropic\u0000claude-sonnet"));
    act(() => card?.querySelector<HTMLButtonElement>(".channel-agent-proposal-actions button")?.click());
    await settle();

    expect(api.resolveChannelAgentCreation).toHaveBeenCalledWith({
      proposal_id: "proposal-1", approve: true, provider: "anthropic", model: "claude-sonnet",
    });
  });

  it("renders resolved role proposals with the same card structure and a clear state", async () => {
    const api = createApi();
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({ messages: room_id === "room-1" ? [{
      id: "proposal-message", room_id, seq: 1, author_type: "human" as const, author_id: "human",
      kind: "system" as const, body: "建议创建新角色：Designer", created_at: "2026-07-23T00:03:00Z",
      agent_creation_proposal: {
        id: "proposal-1", message_id: "proposal-message", room_id, name: "Designer", role: "Design delivery",
        state: "approved" as const, provider: "anthropic", model: "claude-sonnet", created_agent_id: "agent-1",
        created_at: "2026-07-23T00:03:00Z", resolved_at: "2026-07-23T00:04:00Z",
      },
    }] : [] }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();

    const card = container.querySelector(".channel-agent-proposal");
    expect(card?.getAttribute("data-state")).toBe("approved");
    expect(card?.querySelector(".channel-agent-proposal-state")?.textContent).toBe("已创建");
    expect(card?.querySelector(".channel-agent-proposal-identity strong")?.textContent).toBe("Designer");
    expect(card?.querySelector(".channel-agent-proposal-status")?.textContent).toContain("已创建并加入群聊");
    expect(card?.querySelector("select")).toBeNull();
    expect(card?.querySelector(".channel-agent-proposal-actions")).toBeNull();
  });

  it("renders durable Work evidence under the visible owner without hidden personas", async () => {
    const api = createApi();
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({ messages: room_id === "room-1" ? [{
      id: "work-1", room_id, seq: 1, author_type: "agent" as const, author_id: "agent-1",
      kind: "task" as const, body: "Reject callback replay", task_title: "Fix callback",
      task_state: "checking", task_owner: "agent-1", created_at: "2026-07-23T00:00:00Z",
      work: {
        id: "work-1", room_id, source_message_id: "source-1", owner_named_agent_id: "agent-1",
        title: "Fix callback", brief: "Reject callback replay", goal_revision: 1, candidate_revision: 2,
        state: "checking" as const, verification_state: "block" as const, verification_required: true,
        max_verifier_attempts: 3, max_candidates: 1, verifier_attempts_used: 1, candidates_used: 1,
        checks_summary: "focused tests passed", changed_files_count: 3, unresolved_items: "full suite unavailable",
        created_at: "2026-07-23T00:00:00Z", updated_at: "2026-07-23T00:08:00Z",
        events: [
          { id: "event-1", work_id: "work-1", kind: "state" as const, state: "working", goal_revision: 1, candidate_revision: 0, created_at: "2026-07-23T00:00:00Z" },
          { id: "event-2", work_id: "work-1", kind: "state" as const, state: "checking", goal_revision: 1, candidate_revision: 1, created_at: "2026-07-23T00:04:00Z" },
          { id: "event-3", work_id: "work-1", kind: "state" as const, state: "revising", goal_revision: 1, candidate_revision: 1, created_at: "2026-07-23T00:05:00Z" },
          { id: "event-4", work_id: "work-1", kind: "state" as const, state: "checking", goal_revision: 1, candidate_revision: 2, created_at: "2026-07-23T00:08:00Z" },
        ],
        verification: {
          task_id: "work-1", room_id, owner_id: "agent-1", decision: "block" as const,
          report: "Replay still created a session before repair.", attempt: 1,
          goal_revision: 1, candidate_revision: 1, updated_at: "2026-07-23T00:05:00Z",
        },
        artifacts: [{ id: "artifact-1", work_id: "work-1", kind: "diff" as const, uri: "artifact://diff-1", summary: "callback diff", created_at: "2026-07-23T00:04:00Z" }],
        runs: [{
          id: "run-1", work_id: "work-1", kind: "verifier" as const, profile: "security",
          session_ref: "session-check-1", state: "completed" as const, goal_revision: 1,
          candidate_revision: 1, created_at: "2026-07-23T00:04:00Z", updated_at: "2026-07-23T00:05:00Z",
        }],
      },
    }] : [] }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    const onOpenSession = vi.fn();
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" onOpenSession={onOpenSession} />));
    await settle();

    const task = container.querySelector<HTMLDetailsElement>(".channel-assignment-item");
    expect(task?.open).toBe(false);
    const heading = task?.querySelector("summary");
    expect(heading?.textContent).toBe("Fix callback");
    expect(heading?.querySelector(".agent-avatar-mark")).not.toBeNull();
    act(() => heading?.click());
    expect(task?.open).toBe(true);
    expect(task?.textContent).toContain("Reject callback replay");
    expect(container.querySelector(".channel-work-activity")).toBeNull();
    expect(container.querySelector(".channel-assignment-status")?.textContent).toBe("验收中");
    expect(container.querySelector(".channel-work-summary-line")?.textContent).toContain("3");
    const evidence = container.querySelector<HTMLDetailsElement>(".channel-work-evidence");
    expect(evidence).not.toBeNull();
    act(() => evidence?.querySelector("summary")?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(evidence?.textContent).toContain("Replay still created a session before repair.");
    expect(evidence?.textContent).toContain("session-check-1");
    act(() => evidence?.querySelector<HTMLButtonElement>(".channel-work-session-link")?.click());
    expect(onOpenSession).toHaveBeenCalledWith("session-check-1");
    expect(container.textContent).not.toContain("Verifier Bot");
  });

  it("caps channel unread counts at 99+", () => {
    expect(formatChannelUnreadCount(1)).toBe("1");
    expect(formatChannelUnreadCount(99)).toBe("99");
    expect(formatChannelUnreadCount(100)).toBe("99+");
  });

  it("shows each agent's effective model in the avatar status card", async () => {
    const modelAgents = [
      agents[0],
      { ...agents[1], provider_override: "openai", model_override: "gpt-5.3-codex" },
    ];
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents: modelAgents, rooms }));
    api.listNamedAgents = vi.fn(async () => ({ agents: modelAgents }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    const initialized = {
      protocol_version: "1",
      provider: "anthropic",
      model: "claude-opus-4-1",
      workspace_root: "/workspace",
    } as InitializeResult;

    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" initialized={initialized} />));
    await settle();

    const cards = Array.from(container.querySelectorAll<HTMLElement>(".channel-agent-status-card"));
    expect(cards).toHaveLength(2);
    expect(cards[0].querySelector(".channel-agent-model")?.textContent).toBe("claude-opus-4-1");
    expect(cards[1].querySelector(".channel-agent-model")?.textContent).toBe("gpt-5.3-codex");
    expect(cards[0].querySelector(".channel-agent-model svg")).toBeNull();
    expect(container.querySelector('[aria-label="Alpha: 处理中, 模型: claude-opus-4-1"]')).not.toBeNull();
  });

  it("honors controlled room selection without an in-canvas room list", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(
      <ChannelView
        selectedRoomID="room-2"
        onSelectRoom={() => undefined}
      />,
    ));
    await settle();

    // Controlled value wins over the internal auto-select-first fallback.
    expect(api.listChannelMessages).toHaveBeenCalledWith(
      expect.objectContaining({ room_id: "room-2" }),
    );

    // Room switching lives in the app sidebar; the canvas renders a header
    // for the selected room and no room directory of its own.
    expect(container.querySelector(".channel-room-header")?.textContent).toContain("research");
    expect(container.querySelector(".channel-room-row")).toBeNull();
  });

  it("returns from an identity's task session to a room that is already selected in the app shell", async () => {
    const api = createApi();
    const session: CollaborationSessionBinding = {
      session_ref: "work-session", principal_id: agents[0].id, named_agent_id: agents[0].id,
      room_id: "room-1", work_id: "work-1", purpose: "work", state: "cancelled", title: "Review evidence",
      created_at: agents[0].created_at, updated_at: agents[0].created_at,
    };
    api.listChannelSessions = vi.fn(async () => ({ sessions: [session] }));
    api.readChannelSession = vi.fn(async () => ({ session, thread: {
      id: session.session_ref, created_at: session.created_at, updated_at: session.updated_at,
      preview: "", cwd: "", model: "", model_provider: "", status: "idle" as const, turns: [],
    } }));
    const onSelectRoom = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<WuuUIRoot><ChannelView section="agents" selectedRoomID="room-1" onSelectRoom={onSelectRoom} /></WuuUIRoot>));
    await settle();
    act(() => container.querySelector<HTMLButtonElement>(".channel-agent-directory-identity")!.click());
    await settle();
    act(() => container.querySelector<HTMLButtonElement>(".channel-sessions-launcher")!.click());
    await settle();
    act(() => container.querySelector<HTMLButtonElement>(".channel-session-row")!.click());
    await settle();
    const returnButton = [...container.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "返回频道处理任务");
    expect(returnButton).toBeTruthy();
    act(() => returnButton!.click());
    expect(onSelectRoom).toHaveBeenCalledExactlyOnceWith("room-1");
    expect(container.querySelector("[role=dialog]")).toBeNull();
  });

  it("uses WeChat-style centered rows for one through nine members", () => {
    expect(Array.from({ length: 10 }, (_, index) => groupAvatarRowSizes(index))).toEqual([
      [1], [1], [2], [1, 2], [2, 2], [2, 3], [3, 3], [1, 3, 3], [2, 3, 3], [3, 3, 3],
    ]);
  });

  it("scales graph nodes down within a bounded range as the graph grows", () => {
    expect(graphDensityScale(2)).toBe(1.35);
    expect(graphDensityScale(12)).toBeLessThan(graphDensityScale(4));
    expect(graphDensityScale(10_000)).toBe(0.68);
  });

  it("collapses the agent directory with a persistent control", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" />));
    await settle();

    const collapse = container.querySelector<HTMLButtonElement>('[aria-label="收起列表"]');
    expect(collapse?.getAttribute("aria-expanded")).toBe("true");
    act(() => collapse?.click());

    expect(container.querySelector(".channel-view")?.classList.contains("channel-list-collapsed")).toBe(true);
    expect(container.querySelector(".channel-agent-directory-list")).toBeNull();
    expect(container.querySelector(".channel-agent-graph-pane")).not.toBeNull();
    expect(container.querySelector('[aria-label="展开列表"]')).not.toBeNull();
    expect(window.localStorage.getItem("wuu.channels.listCollapsed")).toBe("true");

    act(() => container.querySelector<HTMLButtonElement>('[aria-label="展开列表"]')?.click());
    expect(container.querySelector(".channel-agent-directory-list")).not.toBeNull();
    expect(window.localStorage.getItem("wuu.channels.listCollapsed")).toBe("false");
  });

  it("does not show a conversation composer without a selected room", async () => {
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents, rooms: [] }));
    api.listChannelRooms = vi.fn(async () => ({ rooms: [] }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    expect(container.querySelector(".channel-empty-action")?.textContent).toBe("新建对话");
    expect(container.querySelector(".channel-conversation-footer")).toBeNull();
    expect(container.querySelector(".channel-composer")).toBeNull();
    expect(container.querySelector(".channel-room-members-button")).toBeNull();
    expect(container.querySelector(".channel-sessions-launcher")).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-empty-action")?.click());
    expect(container.querySelector(".channel-recipient-picker")).not.toBeNull();
    expect(container.querySelector(".channel-new-room-surface")).not.toBeNull();
    expect(container.querySelector<HTMLButtonElement>(".channel-new-room-surface .composer-send-button")?.disabled).toBe(true);
    expect(document.querySelector("#channel-room-dialog-title")).toBeNull();
    expect(api.createNamedAgent).not.toHaveBeenCalled();
  });

  it.each(["rooms", "agents"] as const)("opens the shared Agent onboarding from an empty %s directory", async (section) => {
    const api = createApi();
    const onCreateAgent = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView section={section} directoryAgents={[]} directoryRooms={[]} onCreateAgent={onCreateAgent} />));
    await settle();
    const create = container.querySelector<HTMLButtonElement>(".channel-start-empty button");
    expect(create).not.toBeNull();
    await act(async () => create?.click());
    expect(onCreateAgent).toHaveBeenCalledOnce();
    expect(document.querySelector(".channel-agent-editor-dialog")).toBeNull();
    expect(api.createNamedAgent).not.toHaveBeenCalled();
    expect(api.createChannelRoom).not.toHaveBeenCalled();
  });

  it("creates through the shared standalone onboarding and retries opening its conversation without creating again", async () => {
    const api = createApi();
    const createdAgent = { ...agents[1], id: "new-agent", name: "Researcher", provider_override: "openai", model_override: "gpt-reasoner" };
    const directRoom: ChannelRoom = { ...rooms[0], id: "direct-new", kind: "dm", name: "Researcher", members: [{ room_id: "direct-new", member_type: "agent", member_id: createdAgent.id, joined_at: rooms[0].created_at }] };
    let storedAgents: NamedAgent[] = [];
    let storedRooms: ChannelRoom[] = [];
    api.bootstrapChannels = vi.fn(async () => ({ agents: storedAgents, rooms: storedRooms }));
    api.listNamedAgents = vi.fn(async () => ({ agents: storedAgents }));
    api.listChannelRooms = vi.fn(async () => ({ rooms: storedRooms }));
    api.createNamedAgent = vi.fn(async () => { storedAgents = [createdAgent]; return { agent: createdAgent }; });
    api.openChannelDirectMessage = vi.fn().mockRejectedValueOnce(new Error("Room temporarily unavailable")).mockImplementation(async () => {
      storedRooms = [directRoom];
      return { room: directRoom };
    });
    const initialized = { provider: "openai", model: "gpt-reasoner", providers: [{ name: "openai", type: "openai", api_key_configured: true, model: "gpt-reasoner", models: [{ id: "gpt-reasoner", supported_efforts: ["low", "high"], default_effort: "low" }] }] } as InitializeResult;
    const onSelectRoom = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<WuuUIRoot><ChannelView initialized={initialized} onSelectRoom={onSelectRoom} /></WuuUIRoot>));
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-start-empty button")?.click());
    const dialog = document.querySelector('[data-wuu-component="agent-onboarding"]')!;
    expect(dialog).not.toBeNull();
    act(() => setInputValue(dialog.querySelector<HTMLInputElement>('[name="agent-name"]')!, "Researcher"));
    expect(api.createNamedAgent).not.toHaveBeenCalled();
    await act(async () => dialog.querySelector<HTMLFormElement>("form")?.requestSubmit());
    await settle();
    expect(api.createNamedAgent).toHaveBeenCalledOnce();
    expect(api.createNamedAgent).toHaveBeenCalledWith(expect.objectContaining({ name: "Researcher", provider_override: "openai", model_override: "gpt-reasoner", effort_override: "low", request_id: expect.any(String) }));
    expect(dialog.querySelector('[role="alert"]')?.textContent).toContain("Room temporarily unavailable");
    expect(container.querySelector<HTMLTextAreaElement>(".channel-composer textarea")?.disabled).toBe(true);

    await act(async () => dialog.querySelector<HTMLFormElement>("form")?.requestSubmit());
    await settle();
    expect(api.createNamedAgent).toHaveBeenCalledOnce();
    expect(api.openChannelDirectMessage).toHaveBeenCalledTimes(2);
    expect(api.openChannelDirectMessage).toHaveBeenLastCalledWith({ agent_id: createdAgent.id });
    expect(document.querySelector('[data-wuu-component="agent-onboarding"]')).toBeNull();
    expect(onSelectRoom).toHaveBeenCalledWith(directRoom.id);
    expect(container.querySelector(".channel-room-header")?.textContent).toContain("Researcher");
    expect(container.querySelector(".channel-conversation-footer")).not.toBeNull();
  });

  it("marks the selected room as read", async () => {
    const unreadRooms = [
      { ...rooms[0], unread_count: 0 },
      { ...rooms[1], unread_count: 120 },
    ];
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents, rooms: unreadRooms }));
    api.listChannelRooms = vi.fn(async () => ({ rooms: unreadRooms }));
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({
      messages: [{
        id: "selected-room-message",
        room_id,
        seq: 1,
        author_type: "agent" as const,
        author_id: "agent-1",
        kind: "text" as const,
        body: "Visible message",
        created_at: "2026-07-23T00:00:00Z",
      }],
    }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-2" />));
    await settle();

    // Unread badges live in the app sidebar; the canvas persists the read
    // cursor for whichever room the parent selects.
    expect(api.markChannelRoomRead).toHaveBeenCalledWith({ room_id: "room-2" });
  });

  it("keeps one view mounted and restores cached messages synchronously across rooms", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);

    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    const view = container.querySelector(".channel-view");
    expect(container.textContent).toContain("Hello from Alpha");

    act(() => root?.render(<ChannelView selectedRoomID="room-2" />));
    await settle();
    expect(container.querySelector(".channel-view")).toBe(view);
    expect(container.textContent).not.toContain("Hello from Alpha");

    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    expect(container.querySelector(".channel-view")).toBe(view);
    expect(container.textContent).toContain("Hello from Alpha");
    await settle();
  });

  it("marks newly polled messages read while their room stays visible", async () => {
    vi.useFakeTimers();
    try {
      const firstMessage = {
        id: "visible-1",
        room_id: "room-1",
        seq: 1,
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: "Already visible",
        created_at: "2026-07-23T00:00:00Z",
      };
      const secondMessage = {
        ...firstMessage,
        id: "visible-2",
        seq: 2,
        author_type: "agent" as const,
        author_id: "agent-1",
        body: "Arrived while visible",
        created_at: "2026-07-23T00:00:01Z",
      };
      const api = createApi();
      api.listChannelMessages = vi.fn()
        .mockResolvedValueOnce({ messages: [firstMessage] })
        .mockResolvedValue({ messages: [firstMessage, secondMessage] });
      const onRoomRead = vi.fn();
      Object.defineProperty(window, "wuu", { configurable: true, value: api });
      root = createRoot(container);
      act(() => root?.render(
        <ChannelView selectedRoomID="room-1" onRoomRead={onRoomRead} />,
      ));
      await settle();

      expect(api.markChannelRoomRead).toHaveBeenCalledTimes(1);
      expect(onRoomRead).toHaveBeenCalledTimes(1);
      vi.mocked(api.markChannelRoomRead!).mockClear();
      onRoomRead.mockClear();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_000);
      });

      expect(api.markChannelRoomRead).toHaveBeenCalledTimes(1);
      expect(api.markChannelRoomRead).toHaveBeenCalledWith({ room_id: "room-1" });
      expect(onRoomRead).toHaveBeenCalledWith("room-1");
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows active agents from the selected room in the response status bar", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const status = container.querySelector<HTMLElement>(".channel-response-status");
    expect(status?.textContent).toContain("Alpha");
    expect(status?.textContent).toContain("正在工作");
    expect(status?.closest(".channel-conversation-footer")).not.toBeNull();
    expect(status?.querySelectorAll(".channel-response-status-avatar")).toHaveLength(1);
    expect(status?.textContent).not.toContain("Beta");
  });

  it("keeps an active room member visible when cross-server activity has no room scope", async () => {
    const unscopedAgent: NamedAgent = {
      ...agents[1],
      id: "agent-3",
      name: "Gamma",
      activity_status: "thinking",
      activity_room_ids: undefined,
    };
    const activeAgents = [...agents, unscopedAgent];
    const activeRooms = rooms.map((room) => room.id === "room-1"
      ? {
          ...room,
          members: [
            ...room.members,
            { room_id: room.id, member_type: "agent" as const, member_id: unscopedAgent.id, joined_at: "2026-07-23T00:00:00Z" },
          ],
        }
      : room);
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents: activeAgents, rooms: activeRooms }));
    api.listNamedAgents = vi.fn(async () => ({ agents: activeAgents }));
    api.listChannelRooms = vi.fn(async () => ({ rooms: activeRooms }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const status = container.querySelector<HTMLElement>(".channel-activity-region");
    expect(status?.textContent).toContain("Alpha");
    expect(status?.textContent).toContain("Gamma");
    expect(status?.querySelectorAll(".channel-response-status-avatar")).toHaveLength(2);
  });

  it("does not show an agent responding in another room", async () => {
    const crossRoomAgents = agents.map((agent) => agent.id === "agent-1"
      ? { ...agent, activity_room_ids: ["room-2"] }
      : agent);
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents: crossRoomAgents, rooms }));
    api.listNamedAgents = vi.fn(async () => ({ agents: crossRoomAgents }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    // Idle rooms show no status widget at all instead of a permanent
    // "nothing happening" placeholder.
    expect(container.querySelector(".channel-activity-slot:not([inert]) .channel-response-status")).toBeNull();
  });

  it("inserts and focuses a mention when an agent author name is clicked", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const author = container.querySelector<HTMLButtonElement>('[aria-label="提及 Alpha"]');
    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-conversation-footer textarea");
    expect(author?.querySelector("span")?.textContent).toBe("@");
    act(() => author?.click());
    await vi.waitFor(() => expect(document.activeElement).toBe(textarea));

    expect(textarea?.value).toBe("@Alpha ");
    expect(document.activeElement).toBe(textarea);
  });

  it("mentions the selected identity when room members share a display name", async () => {
    const api = createApi();
    const sameNameAgents = agents.map((agent) => ({ ...agent, name: "Alex" }));
    api.bootstrapChannels = vi.fn(async () => ({ agents: sameNameAgents, rooms }));
    api.listNamedAgents = vi.fn(async () => ({ agents: sameNameAgents }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();
    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-conversation-footer textarea")!;
    act(() => setInputValue(textarea, "@"));
    await act(async () => { await new Promise<number>(requestAnimationFrame); });
    const options = document.querySelectorAll<HTMLButtonElement>(".channel-mention-menu button");
    expect(options).toHaveLength(2);
    act(() => options[1].click());
    expect(textarea.value).toBe(`@${sameNameAgents[1].id} `);
    act(() => setInputValue(textarea, ""));
    act(() => container.querySelector<HTMLButtonElement>('[aria-label="提及 Alex"]')!.click());
    expect(textarea.value).toBe(`@${sameNameAgents[0].id} `);
  });

  it("opens and filters the member picker when typing @", async () => {
    const api = createApi();
    const mentionAgents = [agents[0], { ...agents[1], model_override: "gpt-5.3-codex" }];
    api.bootstrapChannels = vi.fn(async () => ({
      agents: mentionAgents,
      rooms,
    }));
    api.listNamedAgents = vi.fn(async () => ({ agents: mentionAgents }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-conversation-footer textarea");
    act(() => setInputValue(textarea!, "@"));
    await act(async () => { await new Promise<number>(requestAnimationFrame); });
    expect(Array.from(document.querySelectorAll(".channel-mention-name")).map((name) => name.textContent)).toEqual(["Alpha", "Beta"]);
    expect(document.querySelector(".channel-mention-model")?.textContent).toBe("gpt-5.3-codex");
    expect(document.querySelector(".channel-mention-menu button.selected .channel-mention-key")?.textContent).toBe("↵");
    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
      textarea?.dispatchEvent(new KeyboardEvent("keyup", { key: "ArrowDown", bubbles: true }));
    });
    expect(document.querySelector(".channel-mention-menu button.selected .channel-mention-name")?.textContent).toBe("Beta");
    act(() => {
      textarea?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
      textarea?.dispatchEvent(new KeyboardEvent("keyup", { key: "ArrowUp", bubbles: true }));
    });
    expect(document.querySelector(".channel-mention-menu button.selected .channel-mention-name")?.textContent).toBe("Alpha");

    act(() => setInputValue(textarea!, "@Be"));
    await act(async () => { await new Promise<number>(requestAnimationFrame); });
    expect(Array.from(document.querySelectorAll(".channel-mention-name")).map((name) => name.textContent)).toEqual(["Beta"]);
    act(() => textarea?.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(textarea?.value).toBe("@Beta ");
    expect(document.querySelector(".channel-mention-menu")).toBeNull();
  });

  it("loads rooms, selects a room, and sends a human message", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });

    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    expect(container.querySelector(".channel-conversation-heading")).toBeNull();
    expect(document.querySelector(".sidebar-name-dialog")).toBeNull();
    const agentBubble = container.querySelector(".channel-message.agent .channel-message-bubble");
    expect(agentBubble?.textContent).toBe("Hello from Alpha with markdown\n<img src=x onerror=alert(1)>");
    expect(
      container
        .querySelector<HTMLElement>(".channel-conversation")
        ?.style.getPropertyValue("--channel-composer-height"),
    ).toBe("");
    expect(agentBubble?.querySelector("strong")?.textContent).toBe("Alpha");
    expect(agentBubble?.querySelector("code")?.textContent).toBe("markdown");
    expect(agentBubble?.querySelector("img")).toBeNull();
    expect(agentBubble?.textContent).not.toContain("**");
    expect(container.querySelector(".channel-message.own .channel-message-bubble")?.textContent).toBe("Human direction");
    expect(container.querySelector(".channel-message-actions")).toBeNull();
    expect(container.querySelector(".channel-thread-panel")).toBeNull();
    expect(container.querySelector<HTMLImageElement>(".channel-message.own .composer-image-attachment img")?.src).toContain("data:image/png;base64,aW1hZ2U=");
    expect(container.querySelector(".channel-message.own .composer-file-attachment")?.textContent).toContain("brief.pdf");
    expect(container.querySelector(".channel-message.own .composer-attachments button")).toBeNull();
    expect(container.querySelector(".channel-message.own .channel-human-avatar")).toBeNull();
    expect(container.querySelector(".channel-message.own .channel-message-meta strong")).toBeNull();
    expect(container.querySelector(".channel-task-card")).toBeNull();
    expect(container.querySelector(".channel-orchestration-message")).not.toBeNull();
    expect(container.querySelector(".channel-message-stream")?.textContent).toContain("Investigate flaky build");
    expect(container.querySelector('[aria-label="Alpha: 处理中"]')).not.toBeNull();
    expect(container.querySelector(".channel-agent-status-card")?.textContent).toBe("处理中");
    expect(container.querySelector(".channel-agent-status-card strong")).toBeNull();
    const firstRoomRow = container.querySelector(".channel-room-row");
    expect(firstRoomRow).toBeNull();
    const roomHeader = container.querySelector(".channel-room-header");
    expect(roomHeader?.textContent).toContain("general");
    expect(roomHeader?.querySelector(".channel-room-scope")).toBeNull();
    const detailsToggle = roomHeader?.querySelector<HTMLButtonElement>(".channel-room-settings-trigger");
    expect(detailsToggle).not.toBeNull();
    act(() => detailsToggle?.click());
    act(() => container.querySelector<HTMLButtonElement>(".channel-settings-manage")?.click());
    const detailsDialog = document.querySelector(".sidebar-name-dialog");
    expect(detailsDialog?.textContent).toContain("群聊详情");
    expect(detailsDialog?.textContent).toContain("群成员");
    expect(detailsDialog?.querySelector(".sidebar-name-dialog-actions")).toBeNull();
    const roomNameInput = detailsDialog?.querySelector<HTMLInputElement>(".channel-room-settings-section input");
    act(() => {
      setInputValue(roomNameInput!, "renamed group");
      roomNameInput?.focus();
      roomNameInput?.blur();
    });
    await settle();
    expect(api.updateChannelRoom).toHaveBeenCalledWith({
      room_id: "room-1",
      name: "renamed group",
      agent_ids: ["agent-1", "agent-2"],
    });
    expect(container.querySelector(".channel-conversation")?.classList.contains("details-open")).toBe(false);
    expect(container.querySelector(".channel-room-main")).not.toBeNull();
    expect(container.querySelector(".channel-conversation-heading")).toBeNull();
    const detailsOverlay = document.querySelector<HTMLElement>(".sidebar-name-dialog-overlay-drawer");
    act(() => detailsOverlay?.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    expect(detailsDialog?.classList.contains("closing")).toBe(true);
    expect(document.querySelector(".sidebar-name-dialog")).not.toBeNull();
    act(() => detailsDialog?.dispatchEvent(new Event("animationend", { bubbles: true })));
    expect(document.querySelector(".sidebar-name-dialog")).toBeNull();
    act(() => root?.render(<ChannelView selectedRoomID="room-2" />));
    await settle();
    expect(api.listChannelMessages).toHaveBeenCalledWith({ room_id: "room-2", limit: 500 });

    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-composer textarea");
    expect(textarea).not.toBeNull();
    expect(container.querySelector(".channel-composer .composer-plus-button")).toBeNull();
    expect(container.querySelector(".channel-composer .permission-chip")).toBeNull();
    act(() => setInputValue(textarea!, "Ask Alpha"));
    const send = container.querySelector<HTMLButtonElement>(".channel-composer .composer-send-button");
    await act(async () => send?.click());

    expect(api.sendChannelMessage).toHaveBeenCalledWith({ room_id: "room-2", body: "Ask Alpha", images: [], files: [] });
  });

  it("restores and publishes the active room tab draft", async () => {
    const api = createApi();
    const onComposerDraftChange = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => {
      root?.render(
        <ChannelView
          selectedRoomID="room-1"
          composerDraft={{ prompt: "unfinished", images: [], files: [] }}
          onComposerDraftChange={onComposerDraftChange}
        />,
      );
    });
    await settle();

    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-composer textarea");
    expect(textarea?.value).toBe("unfinished");
    act(() => setInputValue(textarea!, "continue later"));
    await settle();

    expect(onComposerDraftChange).toHaveBeenLastCalledWith({
      prompt: "continue later",
      images: [],
      files: [],
    });
  });

  it("sends the textarea value before the deferred room draft catches up", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();

    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-composer textarea");
    expect(textarea).not.toBeNull();
    act(() => {
      setInputValue(textarea!, "send immediately");
      textarea?.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }));
    });
    await settle();

    expect(api.sendChannelMessage).toHaveBeenCalledWith({
      room_id: "room-1",
      body: "send immediately",
      images: [],
      files: [],
    });
  });

  it("hydrates the next room draft without publishing the previous room draft", async () => {
    const api = createApi();
    const onComposerDraftChange = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => {
      root?.render(
        <ChannelView
          selectedRoomID="room-1"
          composerDraft={{ prompt: "room one draft", images: [], files: [] }}
          onComposerDraftChange={onComposerDraftChange}
        />,
      );
    });
    await settle();
    onComposerDraftChange.mockClear();

    act(() => {
      root?.render(
        <ChannelView
          selectedRoomID="room-2"
          composerDraft={{ prompt: "room two draft", images: [], files: [] }}
          onComposerDraftChange={onComposerDraftChange}
        />,
      );
    });

    expect(container.querySelector<HTMLTextAreaElement>(".channel-composer textarea")?.value)
      .toBe("room two draft");
    expect(onComposerDraftChange).not.toHaveBeenCalledWith({
      prompt: "room one draft",
      images: [],
      files: [],
    });
    await settle();
  });

  it("treats slash-prefixed channel messages as plain text without opening commands", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });

    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-composer textarea");
    act(() => setInputValue(textarea!, "/compact"));

    expect(document.querySelector(".slash-command-menu")).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-composer .composer-send-button")?.click());
    expect(api.sendChannelMessage).toHaveBeenCalledWith({
      room_id: "room-1",
      body: "/compact",
      images: [],
      files: [],
    });
  });

  it("groups adjacent messages from one author and restores identity after a time gap", async () => {
    const api = createApi();
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({
      messages: [{
        id: "message-1",
        room_id,
        seq: 1,
        author_type: "agent" as const,
        author_id: "agent-1",
        kind: "text" as const,
        body: "First update",
        created_at: "2026-07-23T00:00:00Z",
      }, {
        id: "message-2",
        room_id,
        seq: 2,
        author_type: "agent" as const,
        author_id: "agent-1",
        kind: "text" as const,
        body: "Follow-up update",
        created_at: "2026-07-23T00:03:00Z",
      }, {
        id: "message-3",
        room_id,
        seq: 3,
        author_type: "agent" as const,
        author_id: "agent-1",
        kind: "text" as const,
        body: "Later update",
        created_at: "2026-07-23T00:09:01Z",
      }, {
        id: "message-4",
        room_id,
        seq: 4,
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: "New speaker",
        created_at: "2026-07-23T00:10:00Z",
      }, {
        id: "message-5", room_id, seq: 5, author_type: "agent" as const,
        author_id: "agent-2", kind: "text" as const, body: "Another agent replies",
        created_at: "2026-07-23T00:10:10Z",
      }],
    }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const renderedMessages = container.querySelectorAll<HTMLElement>(".channel-message-stream > .channel-message");
    expect(renderedMessages).toHaveLength(5);
    expect(renderedMessages[0].querySelector(".channel-agent-avatar")).not.toBeNull();
    expect(renderedMessages[0].querySelector(".channel-author-mention")?.textContent).toBe("@Alpha");
    expect(renderedMessages[1].textContent).toContain("Follow-up update");
    expect(renderedMessages[1].querySelector(".channel-agent-avatar")).toBeNull();
    expect(renderedMessages[1].querySelector(".channel-author-mention")).toBeNull();
    expect(renderedMessages[2].querySelector(".channel-agent-avatar")).not.toBeNull();
    expect(renderedMessages[2].querySelector(".channel-author-mention")?.textContent).toBe("@Alpha");
    expect(container.querySelectorAll(".channel-message-stream > time")).toHaveLength(2);
    expect(renderedMessages[3].querySelector(".channel-human-avatar")).toBeNull();
    expect(renderedMessages[4].querySelector(".channel-author-mention")?.textContent).toBe("@Beta");
  });

  it("opens the DM agent settings without replacing the chat and keeps a failed draft for retry", async () => {
    const api = createApi();
    const dm: ChannelRoom = { ...rooms[0], kind: "dm", members: [rooms[0].members[1]] };
    const update = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ agent: agents[1] });
    api.updateNamedAgent = update;
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView directoryAgents={agents} directoryRooms={[dm]} selectedRoomID={dm.id} />));
    await settle();
    const stream = container.querySelector('[role="log"]');
    const composer = container.querySelector(".channel-composer");
    act(() => container.querySelector<HTMLButtonElement>(".channel-room-settings-trigger")!.click());
    const panel = container.querySelector("aside.channel-settings-panel")!;
    expect(panel).not.toBeNull();
    expect(document.querySelector('[aria-modal="true"]')).toBeNull();
    expect(container.querySelector('[role="log"]')).toBe(stream);
    expect(container.querySelector(".channel-composer")).toBe(composer);
    expect(stream?.closest("[inert]")).toBeNull();
    const name = panel.querySelector<HTMLInputElement>(".channel-agent-editor-name input")!;
    expect(name.value).toBe("Beta");
    const role = panel.querySelector<HTMLTextAreaElement>("textarea")!;
    act(() => { setInputValue(name, "Researcher"); setInputValue(role, "Check primary sources"); });
    act(() => panel.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    await settle();
    expect(update).toHaveBeenCalledWith(expect.objectContaining({ agent_id: "agent-2", name: "Researcher", role: "Check primary sources", avatar_key: agents[1].avatar_key }));
    expect(panel.querySelector('[role="alert"]')).not.toBeNull();
    expect(name.value).toBe("Researcher");
    act(() => panel.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    await settle();
    expect(update).toHaveBeenCalledTimes(2);
    expect(container.querySelector(".channel-settings-panel")).toBeNull();
    expect(container.querySelector('[role="log"]')).toBe(stream);
  });

  it("selects the exact group member, returns to members, and clears settings when switching rooms", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    const renderRoom = (id: string) => root?.render(<ChannelView directoryAgents={agents} directoryRooms={rooms} selectedRoomID={id} />);
    act(() => renderRoom("room-1"));
    await settle();
    act(() => container.querySelector<HTMLButtonElement>(".channel-room-settings-trigger")!.click());
    expect(container.querySelectorAll(".channel-settings-member")).toHaveLength(2);
    act(() => container.querySelectorAll<HTMLButtonElement>(".channel-settings-member")[1].click());
    expect(container.querySelector<HTMLInputElement>(".channel-agent-editor-name input")?.value).toBe("Beta");
    act(() => container.querySelector<HTMLButtonElement>('.channel-settings-header [aria-label="返回"]')!.click());
    act(() => container.querySelectorAll<HTMLButtonElement>(".channel-settings-member")[0].click());
    expect(container.querySelector<HTMLInputElement>(".channel-agent-editor-name input")?.value).toBe("Alpha");
    act(() => renderRoom("room-2"));
    await settle();
    expect(container.querySelector(".channel-settings-panel")).toBeNull();
    expect(document.querySelector(".channel-agent-editor-dialog")).toBeNull();
    expect(api.updateNamedAgent).not.toHaveBeenCalled();
  });

  it("identifies a DM in the header and keeps its message stream free of repeated sender chrome", async () => {
    const api = createApi();
    const dm: ChannelRoom = { ...rooms[0], kind: "dm", members: [rooms[0].members[0]] };
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({ messages: [
      { id: "dm-1", room_id, seq: 1, author_type: "agent" as const, author_id: "agent-1", kind: "text" as const, body: "Here is the report", created_at: "2026-09-12T10:00:00Z" },
      { id: "dm-2", room_id, seq: 2, author_type: "agent" as const, author_id: "agent-1", kind: "text" as const, body: "And its sources", created_at: "2026-09-12T10:00:10Z" },
    ] }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView directoryAgents={agents} directoryRooms={[dm]} selectedRoomID={dm.id} />));
    await settle();
    expect(container.querySelector(".channel-room-settings-name")?.textContent).toBe("Alpha");
    expect(container.querySelector(".channel-room-header .channel-agent-avatar")).not.toBeNull();
    const stream = container.querySelector(".channel-message-stream")!;
    expect(stream.querySelectorAll("article")).toHaveLength(2);
    expect(stream.textContent).toContain("And its sources");
    expect(stream.querySelector(".channel-author-mention")).toBeNull();
    expect(stream.querySelector(".chat-avatar-slot")).toBeNull();
  });

  it("shows public thread replies with their original message context", async () => {
    const api = createApi();
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({
      messages: [{
        id: "message-1",
        room_id,
        seq: 1,
        author_type: "agent" as const,
        author_id: "agent-1",
        kind: "text" as const,
        body: "Before the thread",
        created_at: "2026-07-23T00:00:00Z",
      }, {
        id: "message-2",
        room_id,
        seq: 2,
        author_type: "agent" as const,
        author_id: "agent-1",
        kind: "text" as const,
        body: "Thread root",
        created_at: "2026-07-23T00:00:30Z",
      }, {
        id: "reply-1",
        room_id,
        seq: 3,
        thread_id: "message-2",
        reply_to: "message-2",
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: "Thread reply 1",
        created_at: "2026-07-23T00:00:45Z",
      }, {
        id: "reply-2",
        room_id,
        seq: 4,
        thread_id: "message-2",
        reply_to: "message-2",
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: "Thread reply 2",
        created_at: "2026-07-23T00:00:46Z",
      }, {
        id: "reply-3",
        room_id,
        seq: 5,
        thread_id: "message-2",
        reply_to: "message-2",
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: "Thread reply 3",
        created_at: "2026-07-23T00:00:47Z",
      }, {
        id: "reply-4",
        room_id,
        seq: 6,
        thread_id: "message-2",
        reply_to: "message-2",
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: "Thread reply 4",
        created_at: "2026-07-23T00:00:48Z",
      }, {
        id: "message-3",
        room_id,
        seq: 7,
        author_type: "agent" as const,
        author_id: "agent-1",
        kind: "text" as const,
        body: "After the thread",
        created_at: "2026-07-23T00:01:00Z",
      }],
    }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const renderedMessages = container.querySelectorAll<HTMLElement>(".channel-message-stream > .channel-message");
    expect(renderedMessages).toHaveLength(7);
    expect(container.textContent).toContain("Thread reply 1");
    expect(container.textContent).toContain("Thread reply 4");
    expect(renderedMessages[2].querySelector("blockquote")?.textContent).toBe("AlphaThread root");
    expect(renderedMessages[6].textContent).toContain("After the thread");
  });

  it("collapses long human messages and restores rich content on demand", async () => {
    const api = createApi();
    const longBody = `**First detail**\n${Array.from({ length: 16 }, (_, index) => `Line ${index + 1}`).join("\n")}\n**Final detail**`;
    api.listChannelMessages = vi.fn(async () => ({
      messages: [{
        id: "long-message",
        room_id: "room-1",
        seq: 1,
        author_type: "human" as const,
        author_id: "human",
        kind: "text" as const,
        body: longBody,
        created_at: "2026-07-23T00:00:00Z",
      }],
    }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const bubble = container.querySelector(".channel-message-bubble.long-card");
    const toggle = bubble?.querySelector<HTMLButtonElement>(".channel-message-expand-toggle");
    expect(bubble?.classList.contains("collapsed")).toBe(true);
    expect(bubble?.querySelector(".channel-message-raw-query")).toBeNull();
    expect(Array.from(bubble?.querySelectorAll("strong") ?? []).map((node) => node.textContent)).toEqual(["First detail", "Final detail"]);
    expect(toggle?.textContent).toContain("显示更多");
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");

    const stream = container.querySelector<HTMLElement>(".channel-message-stream");
    expect(stream?.style.overflowAnchor).toBe("none");

    act(() => toggle?.click());
    expect(bubble?.classList.contains("expanded")).toBe(true);
    expect(Array.from(bubble?.querySelectorAll("strong") ?? []).at(-1)?.textContent).toBe("Final detail");
    expect(toggle?.textContent).toContain("收起");
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(stream?.style.overflowAnchor).toBe("auto");
  });

  it("does not issue another bottom scroll when polling returns the same messages", async () => {
    vi.useFakeTimers();
    try {
      const api = createApi();
      Object.defineProperty(window, "wuu", { configurable: true, value: api });
      root = createRoot(container);
      act(() => root?.render(<ChannelView />));
      await settle();

      const stream = container.querySelector<HTMLDivElement>(".channel-message-stream");
      expect(stream).not.toBeNull();
      let scrollTop = 600;
      let scrollWrites = 0;
      Object.defineProperties(stream!, {
        scrollHeight: { configurable: true, get: () => 1000 },
        clientHeight: { configurable: true, get: () => 400 },
        scrollTop: {
          configurable: true,
          get: () => scrollTop,
          set: (value: number) => {
            scrollTop = value;
            scrollWrites += 1;
          },
        },
      });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(20);
      });
      scrollTop = 600;
      scrollWrites = 0;

      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_000);
      });

      expect(api.listChannelMessages).toHaveBeenCalledTimes(2);
      expect(scrollWrites).toBe(0);
      expect(scrollTop).toBe(600);
    } finally {
      vi.useRealTimers();
    }
  });

  it("offers the shared jump-to-latest control after the user leaves the bottom", async () => {
    vi.useFakeTimers();
    try {
      Object.defineProperty(window, "wuu", { configurable: true, value: createApi() });
      root = createRoot(container);
      act(() => root?.render(<ChannelView />));
      await settle();

      const stream = container.querySelector<HTMLDivElement>(".channel-message-stream");
      expect(stream).not.toBeNull();
      let scrollTop = 600;
      const scrollTo = vi.fn();
      Object.defineProperties(stream!, {
        scrollHeight: { configurable: true, get: () => 1000 },
        clientHeight: { configurable: true, get: () => 400 },
        scrollTop: {
          configurable: true,
          get: () => scrollTop,
          set: (value: number) => { scrollTop = value; },
        },
        scrollTo: { configurable: true, value: scrollTo },
      });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(20);
      });
      act(() => {
        scrollTop = 300;
        stream?.dispatchEvent(new WheelEvent("wheel", { deltaY: -20 }));
        stream?.dispatchEvent(new Event("scroll"));
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(20);
      });

      const jump = document.body.querySelector<HTMLButtonElement>(".jump-to-latest-pill");
      expect(jump).not.toBeNull();
      act(() => jump?.click());
      expect(scrollTo).toHaveBeenCalledWith({ top: 1000, behavior: "smooth" });
    } finally {
      vi.useRealTimers();
    }
  });

  it("resizes the agents directory pane and persists the width", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    // The rooms canvas is single-column now; no pane, no resizer.
    expect(container.querySelector(".channel-split-resizer")).toBeNull();
    expect(container.querySelector<HTMLElement>(".channel-view")?.style.gridTemplateColumns).toBe("");

    act(() => root?.render(<ChannelView section="agents" />));
    await settle();
    const agentSeparator = container.querySelector<HTMLButtonElement>(".channel-split-resizer");
    expect(agentSeparator?.getAttribute("aria-valuenow")).toBe("208");
    act(() => agentSeparator?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true })));

    expect(agentSeparator?.getAttribute("aria-valuenow")).toBe("224");
    expect(window.localStorage.getItem("wuu.channels.splitPaneWidth")).toBe("224");
    expect(container.querySelector<HTMLElement>(".channel-view")?.style.gridTemplateColumns).toBe("224px minmax(0, 1fr)");
    expect(container.querySelector<HTMLElement>(".channel-agent-workspace")?.style.gridTemplateColumns).toBe("");
    const agentRow = container.querySelector(".channel-agent-directory-row");
    expect(agentRow?.classList.contains("channel-directory-row")).toBe(true);
    expect(agentRow?.children).toHaveLength(2);
    const agentAvatar = agentRow?.querySelector<HTMLButtonElement>("button.channel-directory-avatar");
    expect(agentAvatar).not.toBeNull();
    expect(agentRow?.querySelector(".channel-directory-identity")?.textContent).toContain("Alpha");
    expect(agentRow?.querySelectorAll(".channel-directory-settings")).toHaveLength(0);
    expect(agentRow?.querySelector(".channel-agent-directory-actions")).toBeNull();
    const graphEntry = container.querySelector<HTMLButtonElement>(".channel-agent-graph-entry");
    expect(graphEntry?.getAttribute("aria-current")).toBe("page");
    act(() => agentAvatar?.click());
    expect(container.querySelector(".channel-agent-detail h2")?.textContent).toBe("Alpha");
    expect(container.querySelector(".channel-agent-graph-canvas")).toBeNull();
    const detailName = container.querySelector<HTMLInputElement>(".channel-agent-detail-form input");
    act(() => {
      if (!detailName) return;
      setInputValue(detailName, "Alpha Prime");
    });
    expect(container.querySelector(".channel-agent-detail-actions .channel-management-primary")).toBeNull();
    act(() => graphEntry?.click());
    await settle();
    expect(api.updateNamedAgent).toHaveBeenCalledWith(expect.objectContaining({ agent_id: "agent-1", name: "Alpha Prime" }));
    expect(agentRow?.querySelector(".channel-directory-settings")).toBeNull();
    act(() => graphEntry?.click());
    expect(container.querySelector(".channel-agent-graph-canvas")).not.toBeNull();

    act(() => agentSeparator?.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
    expect(agentSeparator?.getAttribute("aria-valuenow")).toBe("156");
  });

  it("hides archived rooms from an agent's channel list", async () => {
    const agentRooms = rooms.map((room) => ({
      ...room,
      members: room.id === "room-2"
        ? [{ room_id: room.id, member_type: "agent" as const, member_id: "agent-1", joined_at: "2026-07-23T00:00:00Z" }]
        : room.members,
    }));
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents, rooms: agentRooms }));
    api.listChannelRooms = vi.fn(async () => ({ rooms: agentRooms }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" archivedRoomIDs={["room-1"]} />));
    await settle();

    act(() => container.querySelector<HTMLButtonElement>("button.channel-directory-avatar")?.click());
    const channelList = container.querySelector(".channel-agent-detail-rooms");
    expect(channelList?.textContent).toContain("# research");
    expect(channelList?.textContent).not.toContain("# general");
  });

  it("keeps an agent's stored model override visible when its provider is no longer configured", async () => {
    const staleAgents = [
      { ...agents[0], provider_override: "tokenhub", model_override: "gpt-5.6-sol", effort_override: "high" },
    ];
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents: staleAgents, rooms }));
    api.listNamedAgents = vi.fn(async () => ({ agents: staleAgents }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    const initialized = {
      protocol_version: "1",
      provider: "openai",
      model: "gpt-default",
      workspace_root: "/workspace",
      providers: [{
        name: "openai",
        type: "openai",
        model: "gpt-reasoner",
        models: [{ id: "gpt-reasoner", display_name: "GPT Reasoner" }],
      }],
    } as InitializeResult;

    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" initialized={initialized} />));
    await settle();

    act(() => container.querySelector<HTMLButtonElement>("button.channel-directory-avatar")?.click());
    const trigger = container.querySelector<HTMLButtonElement>('.channel-agent-detail-form button[aria-label="模型"]');
    expect(trigger?.textContent).toContain("gpt-5.6-sol");
    expect(trigger?.textContent).not.toContain("请选择");
  });

  it("shows agent save failures in the global toast without an inline detail error", async () => {
    const api = createApi();
    api.updateNamedAgent = vi.fn(async () => {
      throw new Error("Error invoking remote method 'wuu:channel-agent-update': Error: save failed");
    });
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(
      <WuuUIRoot>
        <ChannelView section="agents" />
        <ToastViewport />
      </WuuUIRoot>,
    ));
    await settle();

    act(() => container.querySelector<HTMLButtonElement>("button.channel-directory-avatar")?.click());
    const detailName = container.querySelector<HTMLInputElement>(".channel-agent-detail-form input");
    act(() => {
      if (detailName) setInputValue(detailName, "Alpha Prime");
    });
    act(() => container.querySelector<HTMLButtonElement>(".channel-agent-graph-entry")?.click());
    await settle();

    expect(container.querySelector('[role="alert"]')?.textContent).toContain("save failed");
    expect(container.querySelector(".channel-agent-detail .channel-error")).toBeNull();
  });

  it("tracks tasks across channels from the task section", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView section="tasks" />));
    await settle();

    const board = container.querySelector(".channel-task-board");
    expect(board?.textContent).toContain("Investigate flaky build");
    expect(board?.textContent).toContain("# general");
    expect(board?.querySelectorAll(".channel-task-column")).toHaveLength(5);
    expect(board?.querySelector('[data-column="in_progress"]')?.textContent).toContain("Investigate flaky build");
    expect(container.querySelector(".channel-conversation")).toBeNull();
    expect(container.querySelector(".channel-list-pane")).toBeNull();
    expect(api.listChannelMessages).toHaveBeenCalledWith({ room_id: "room-2", limit: 500 });
  });

  it("opens the shared creation flow from the agent directory", async () => {
    const api = createApi();
    const onCreateAgent = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });

    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" onCreateAgent={onCreateAgent} />));
    await settle();

    expect(container.querySelector(".channel-conversation")).toBeNull();
    const agentDirectory = container.querySelector(".channel-agent-directory");
    expect(agentDirectory?.classList.contains("channel-list-pane")).toBe(true);
    expect(agentDirectory?.textContent).toContain("Alpha");
    expect(container.querySelector(".channel-agent-directory .agent-avatar-image")).not.toBeNull();
    expect(container.querySelector('svg[aria-label="关系图谱"]')).not.toBeNull();
    expect(container.querySelectorAll(".channel-agent-graph-links line.relationship")).toHaveLength(1);
    expect(container.querySelectorAll(".channel-agent-graph-links line.membership")).toHaveLength(2);
    expect(container.querySelectorAll(".channel-agent-graph-node.agent")).toHaveLength(2);
    expect(container.querySelectorAll(".channel-agent-graph-node.room")).toHaveLength(1);
    expect(container.querySelector('button[aria-label="放大图谱"]')).not.toBeNull();
    const graphSettingsButton = container.querySelector<HTMLButtonElement>('button[aria-label="图谱设置"]');
    act(() => graphSettingsButton?.click());
    expect(container.querySelector(".channel-agent-graph-settings")?.textContent).toContain("节点斥力");
    const newAgentButton = container.querySelector<HTMLButtonElement>('button[aria-label="新建 Agent"]');
    act(() => newAgentButton?.click());
    expect(onCreateAgent).toHaveBeenCalledOnce();
    expect(document.querySelector(".channel-agent-editor-dialog")).toBeNull();
    expect(api.createNamedAgent).not.toHaveBeenCalled();
  });

  it("opens an agent memory directory from its path link", async () => {
    const api = createApi();
    const onOpenMemoryDirectory = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" onOpenMemoryDirectory={onOpenMemoryDirectory} />));
    await settle();

    act(() => container.querySelector<HTMLButtonElement>('button[aria-label="查看Alpha"]')?.click());
    const memoryLink = container.querySelector<HTMLButtonElement>(".channel-agent-memory-link");
    expect(memoryLink?.textContent).toBe("./memory");
    expect(memoryLink?.title).toBe("/agents/agent-1/memory");
    await act(async () => memoryLink?.click());

    expect(onOpenMemoryDirectory).toHaveBeenCalledWith("/agents/agent-1/memory");
    expect(api.revealWorkspaceItem).not.toHaveBeenCalled();
  });

  it("persists a selected effort when editing an existing agent model", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    const initialized = {
      protocol_version: "1",
      provider: "openai",
      model: "gpt-default",
      workspace_root: "/workspace",
      providers: [{
        name: "openai",
        type: "openai",
        model: "gpt-reasoner",
        models: [{
          id: "gpt-reasoner",
          display_name: "GPT Reasoner",
          supported_efforts: ["low", "high"],
          default_effort: "low",
        }],
      }],
    } as InitializeResult;

    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" initialized={initialized} editAgentRequestID="agent-1" />));
    await settle();

    const editor = document.querySelector(".channel-agent-editor-dialog")!;
    expect(editor.querySelector(".agent-avatar-creator")).toBeNull();
    expect(editor.querySelector<HTMLDetailsElement>("details")?.open).toBe(false);
    act(() => editor.querySelector<HTMLButtonElement>('button[aria-label="编辑头像"]')?.click());
    expect(editor.querySelector(".agent-avatar-creator")).not.toBeNull();
    act(() => editor.querySelector<HTMLButtonElement>('button[aria-label="云朵"]')?.click());
    act(() => editor.querySelector<HTMLButtonElement>('button[aria-label="编辑头像"]')?.click());
    expect(editor.querySelector(".agent-avatar-creator")).toBeNull();
    act(() => setInputValue(editor.querySelector<HTMLTextAreaElement>("textarea")!, "Reviews the interface"));
    const nameInput = document.querySelector<HTMLInputElement>(".channel-agent-editor-name input");
    act(() => setInputValue(nameInput!, "Reasoner"));
    act(() => document.querySelector<HTMLButtonElement>('button[aria-label="模型"]')?.click());
    const modelOption = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]'))
      .find((button) => button.textContent?.includes("GPT Reasoner"));
    act(() => modelOption?.click());
    act(() => document.querySelector<HTMLButtonElement>('.channel-agent-editor-dialog button[aria-label="推理强度"]')?.click());
    const highOption = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]'))
      .find((button) => button.textContent?.trim() === "High");
    expect(highOption).not.toBeNull();
    act(() => highOption?.click());
    await act(async () => document.querySelector<HTMLFormElement>(".sidebar-name-dialog")?.requestSubmit());

    expect(api.updateNamedAgent).toHaveBeenCalledWith(expect.objectContaining({
      agent_id: "agent-1",
      name: "Reasoner",
      role: "Reviews the interface",
      avatar_key: expect.stringContaining(":cloud:"),
      avatar_image: "",
      provider_override: "openai",
      model_override: "gpt-reasoner",
      effort_override: "high",
    }));
  });

  it("keeps invalid avatar feedback beside the avatar control", async () => {
    Object.defineProperty(window, "wuu", { configurable: true, value: createApi() });
    root = createRoot(container);
    act(() => root?.render(<ChannelView section="agents" editAgentRequestID="agent-1" />));
    await settle();
    const input = document.querySelector<HTMLInputElement>(".channel-avatar-file-input");
    expect(input).not.toBeNull();
    const oversizedImage = new File(["image"], "avatar.png", { type: "image/png" });
    Object.defineProperty(oversizedImage, "size", { configurable: true, value: 10 * 1024 * 1024 + 1 });
    Object.defineProperty(input!, "files", { configurable: true, value: [oversizedImage] });

    await act(async () => input?.dispatchEvent(new Event("change", { bubbles: true })));

    const dialog = document.querySelector(".sidebar-name-dialog");
    const alert = dialog?.querySelector<HTMLElement>("#channel-agent-avatar-error");
    const avatarButton = dialog?.querySelector<HTMLButtonElement>('button[aria-label="编辑头像"]');
    expect(alert?.textContent).toBe("请选择不超过 10 MB 的 PNG、JPEG 或 WebP 图片。");
    expect(avatarButton?.getAttribute("aria-invalid")).toBe("true");
    expect(avatarButton?.getAttribute("aria-describedby")).toBe(alert?.id);
    expect(container.querySelector(".channel-error")).toBeNull();
  });

  it("creates a named group from recipients when its first message is sent", async () => {
    const api = createApi();
    const onNewRoomRequestHandled = vi.fn();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });

    root = createRoot(container);
    act(() => root?.render(
      <ChannelView newRoomRequest={1} onNewRoomRequestHandled={onNewRoomRequestHandled} />,
    ));
    await settle();

    expect(onNewRoomRequestHandled).toHaveBeenCalledOnce();
    act(() => setInputValue(container.querySelector<HTMLInputElement>(".channel-recipient-control input")!, "Alpha"));
    act(() => container.querySelector<HTMLButtonElement>("#channel-recipient-create-group")?.click());
    expect(container.querySelector<HTMLInputElement>(".channel-recipient-control input")?.value).toBe("");
    const recipientInput = container.querySelector<HTMLInputElement>('.channel-recipient-control input');
    expect(recipientInput?.getAttribute("role")).toBe("combobox");
    act(() => recipientInput?.dispatchEvent(new KeyboardEvent("keydown", { code: "Digit1", key: "1", metaKey: true, bubbles: true })));
    act(() => container.querySelector<HTMLButtonElement>('.channel-recipient-options [role="option"]')?.click());
    expect(container.querySelectorAll(".channel-recipient-chip")).toHaveLength(2);
    expect(api.createChannelRoom).not.toHaveBeenCalled();
    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-new-room-surface textarea");
    act(() => setInputValue(textarea!, "Review the release"));
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-new-room-surface .composer-send-button")?.click());

    expect(api.createChannelRoom).toHaveBeenCalledWith(expect.objectContaining({
      name: expect.stringMatching(/Alpha.*Beta/u),
      agent_ids: ["agent-1", "agent-2"],
    }));
    expect(api.sendChannelMessage).toHaveBeenCalledWith({ room_id: "room-2", body: "Review the release", images: [], files: [] });
  });

  it("adds channel members through an explicit selection flow and separates channel deletion", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-2" />));
    await settle();
    const manageResearch = container.querySelector<HTMLButtonElement>(".channel-room-settings-trigger");
    expect(manageResearch).not.toBeNull();
    act(() => manageResearch?.click());
    act(() => container.querySelector<HTMLButtonElement>(".channel-settings-manage")?.click());
    const detailsDialog = document.querySelector(".sidebar-name-dialog");
    expect(detailsDialog?.textContent).toContain("群聊详情");
    expect(document.querySelector(".sidebar-name-dialog-overlay-drawer")).not.toBeNull();
    expect(detailsDialog?.classList.contains("sidebar-name-dialog-drawer")).toBe(true);
    expect(detailsDialog?.textContent).toContain("群成员");
    expect(detailsDialog?.textContent).not.toContain("群公告");
    expect(detailsDialog?.querySelectorAll(".channel-room-member-row")).toHaveLength(0);
    expect(detailsDialog?.textContent).toContain("危险操作");
    expect(detailsDialog?.querySelector(".sidebar-name-dialog-actions")).toBeNull();
    const addMemberButton = detailsDialog?.querySelector<HTMLButtonElement>(".channel-room-member-add");
    expect(addMemberButton?.textContent).toContain("新增成员");
    act(() => addMemberButton?.click());
    const memberDialog = document.querySelector<HTMLFormElement>(".channel-room-member-dialog");
    expect(memberDialog?.textContent).toContain("添加 Agent");
    expect(memberDialog?.classList.contains("sidebar-name-dialog-drawer")).toBe(false);
    expect(document.querySelectorAll('.sidebar-name-dialog[role="dialog"]')).toHaveLength(2);
    expect(memberDialog?.querySelector(".sidebar-name-dialog-actions")).not.toBeNull();
    expect(memberDialog?.querySelector(".select-menu")).toBeNull();
    expect(detailsDialog?.getAttribute("aria-hidden")).toBe("true");
    expect(detailsDialog?.textContent).toContain("群聊详情");
    const addAgent = memberDialog?.querySelector<HTMLButtonElement>('.channel-member-picker-option[role="option"]');
    expect(addAgent).not.toBeNull();
    act(() => addAgent?.click());
    const saveButton = memberDialog?.querySelector<HTMLButtonElement>('button[type="submit"]');
    expect(saveButton?.textContent).toBe("添加（1）");
    await act(async () => saveButton?.click());

    expect(api.updateChannelRoom).toHaveBeenCalledWith({
      room_id: "room-2",
      name: "research",
      agent_ids: ["agent-1"],
    });

    expect(document.querySelector(".channel-room-member-dialog")).toBeNull();
    expect(document.querySelector(".sidebar-name-dialog-drawer")?.textContent).toContain("群聊详情");
    const confirmDelete = vi.spyOn(window, "confirm").mockReturnValue(true);
    const deleteButton = document.querySelector<HTMLButtonElement>(".channel-room-danger-zone button");
    expect(deleteButton?.textContent).toBe("删除频道");
    await act(async () => deleteButton?.click());
    expect(confirmDelete).toHaveBeenCalledWith("删除“research”？频道及其中的消息将被永久删除。");
    expect(api.deleteChannelRoom).toHaveBeenCalledWith({ room_id: "room-2" });
  });

  it("removes members per-row with inline confirmation", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView />));
    await settle();

    const general = Array.from(container.querySelectorAll<HTMLButtonElement>(".channel-room-select"))
      .find((button) => button.textContent?.includes("general"));
    act(() => general?.click());
    await settle();
    act(() => container.querySelector<HTMLButtonElement>(".channel-room-settings-trigger")?.click());
    act(() => container.querySelector<HTMLButtonElement>(".channel-settings-manage")?.click());

    const detailsDialog = document.querySelector(".sidebar-name-dialog");
    expect(detailsDialog?.querySelectorAll(".channel-room-member-row")).toHaveLength(2);
    const alphaRow = Array.from(detailsDialog?.querySelectorAll<HTMLDivElement>(".channel-room-member-row") ?? [])
      .find((row) => row.textContent?.includes("Alpha"));
    expect(alphaRow).not.toBeNull();
    const removeButton = alphaRow?.querySelector<HTMLButtonElement>('button[aria-label="移除 Alpha"]');
    expect(removeButton).not.toBeNull();

    const confirmRemove = vi.spyOn(window, "confirm").mockReturnValue(false);
    await act(async () => removeButton?.click());
    expect(confirmRemove).toHaveBeenCalledWith("将“Alpha”移出当前群聊？该 Agent 及其保存的状态不会被删除。");
    expect(api.updateChannelRoom).not.toHaveBeenCalled();

    confirmRemove.mockReturnValue(true);
    await act(async () => removeButton?.click());

    expect(api.updateChannelRoom).toHaveBeenCalledWith({
      room_id: "room-1",
      name: "general",
      agent_ids: ["agent-2"],
    });
  });

  it("creates a task for a named agent", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });

    root = createRoot(container);
    act(() => root?.render(<ChannelView section="tasks" />));
    await settle();

    act(() => container.querySelector<HTMLButtonElement>(".channel-management-primary")?.click());
    const title = document.querySelector<HTMLInputElement>(".channel-setup-form input");
    expect(title).not.toBeNull();
    act(() => setInputValue(title!, "Investigate flaky build"));
    const form = document.querySelector<HTMLFormElement>(".sidebar-name-dialog");
    await act(async () => form?.requestSubmit());

    expect(api.createChannelTask).toHaveBeenCalledWith({
      room_id: "room-1",
      title: "Investigate flaky build",
      owner_id: "agent-1",
    });
  });

  it("cascades task owner choices from the selected room", async () => {
    const scopedRooms: ChannelRoom[] = [
      {
        ...rooms[0],
        members: [rooms[0].members[0]],
      },
      {
        ...rooms[1],
        members: [{
          room_id: "room-2",
          member_type: "agent",
          member_id: "agent-2",
          joined_at: "2026-07-23T00:00:00Z",
        }],
      },
    ];
    const api = createApi();
    api.bootstrapChannels = vi.fn(async () => ({ agents, rooms: scopedRooms }));
    api.listChannelRooms = vi.fn(async () => ({ rooms: scopedRooms }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });

    root = createRoot(container);
    act(() => root?.render(<ChannelView section="tasks" />));
    await settle();

    act(() => container.querySelector<HTMLButtonElement>(".channel-management-primary")?.click());
    const roomSelect = document.querySelector<HTMLButtonElement>('button[aria-label="频道"]');
    const ownerSelect = document.querySelector<HTMLButtonElement>('button[aria-label="负责人"]');
    expect(roomSelect?.textContent).toContain("general");
    expect(ownerSelect?.textContent).toContain("Alpha");

    act(() => roomSelect?.click());
    const researchOption = document.querySelector<HTMLButtonElement>('[role="menuitemradio"][data-value="room-2"]');
    act(() => researchOption?.click());
    await settle();

    expect(ownerSelect?.textContent).toContain("Beta");
    act(() => ownerSelect?.click());
    expect(document.querySelector('[role="menuitemradio"][data-value="agent-2"]')).not.toBeNull();
    expect(document.querySelector('[role="menuitemradio"][data-value="agent-1"]')).toBeNull();
    act(() => ownerSelect?.click());

    const title = document.querySelector<HTMLInputElement>(".channel-setup-form input");
    act(() => setInputValue(title!, "Review research"));
    await act(async () => document.querySelector<HTMLFormElement>(".sidebar-name-dialog")?.requestSubmit());

    expect(api.createChannelTask).toHaveBeenCalledWith({
      room_id: "room-2",
      title: "Review research",
      owner_id: "agent-2",
    });
  });
  it("opens the working agent's exact session without a header launcher or partial text in the room", async () => {
    const api = createApi();
    api.listChannelMessages = vi.fn(async () => ({ messages: [], responses: [{ id: "reply-beta", room_id: "room-1", agent_id: "agent-2", session_ref: "beta-session", turn_id: "turn", state: "responding" as const, body: "Private live text", created_at: "2026-09-12T00:00:00Z" }] }));
    api.readChannelSession = vi.fn().mockResolvedValue({ session: { state: "running" }, thread: { id: "beta-session", turns: [{ id: "turn", status: "in_progress", items: [{ id: "text", type: "agent_message", text: "Private live text" }] }] } });
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    expect(container.querySelector(".channel-room-header .channel-sessions-launcher")).toBeNull();
    expect(container.querySelector(".channel-message-stream")?.textContent).not.toContain("Private live text");
    const trigger = container.querySelector<HTMLButtonElement>(".channel-activity-inspect")!;
    expect(trigger.textContent).toContain("Beta");
    await act(async () => trigger.click());
    await settle();
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(container.querySelector(".channel-conversation")?.classList.contains("has-session-inspector")).toBe(true);
    expect(container.querySelector(".session-inspector-extension")?.textContent).toContain("Private live text");
    expect(api.readChannelSession).toHaveBeenCalledWith({ sessionRef: "beta-session" });
    expect(container.querySelector(".channel-message-stream")?.textContent).not.toContain("Private live text");
    vi.useFakeTimers();
    try {
      await act(async () => trigger.click());
      expect(container.querySelector<HTMLElement>(".session-inspector-extension")?.hasAttribute("inert")).toBe(true);
      await act(async () => { await vi.advanceTimersByTimeAsync(100); trigger.click(); });
      await act(async () => { await vi.advanceTimersByTimeAsync(300); });
      expect(container.querySelector<HTMLElement>(".session-inspector-extension")?.hasAttribute("inert")).toBe(false);
      expect(trigger.getAttribute("aria-expanded")).toBe("true");
      await act(async () => trigger.click());
      await act(async () => { await vi.advanceTimersByTimeAsync(220); });
      expect(container.querySelector(".session-inspector-extension")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    { model: "selected-model", effort: "high", expectedModel: "selected-model", expectedEffort: "High" },
    { model: "selected-model", effort: "", expectedModel: "selected-model", expectedEffort: "Low" },
    { model: "", effort: "", expectedModel: "workspace-model", expectedEffort: "Ultra" },
  ])("shows the bot runtime in its avatar card and edits that bot: $expectedModel / $expectedEffort", async ({ model, effort, expectedModel, expectedEffort }) => {
    const api = createApi();
    api.readChannelSession = vi.fn();
    const bot = { ...agents[1], provider_override: "openai", model_override: model, effort_override: effort };
    api.listChannelMessages = vi.fn(async () => ({ messages: [{
      id: "reply", room_id: "room-1", seq: 1, author_type: "agent" as const, author_id: bot.id,
      kind: "text" as const, body: "Published answer", created_at: "2026-09-12T00:00:00Z",
      source_session_ref: "original-session", source_turn_id: "original-turn",
    }], responses: [] }));
    const initialized = {
      protocol_version: "1", provider: "openai", model: "workspace-model", effort: "ultra", workspace_root: "/workspace",
      providers: [{ name: "openai", type: "openai", model: "workspace-model", models: [{ id: "selected-model", default_effort: "low", supported_efforts: ["low", "high"] }] }],
    } as InitializeResult;
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" directoryAgents={[bot]} initialized={initialized} />));
    await settle();
    const name = container.querySelector<HTMLButtonElement>(".channel-author-mention")!;
    await act(async () => { name.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); name.focus(); });
    expect(document.querySelector(".channel-agent-hover-card")).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-agent-hover-trigger")!.focus());
    const card = document.querySelector(".channel-agent-hover-card")!;
    expect(card.querySelector('[aria-label="模型"]')?.textContent).toBe(expectedModel);
    expect(card.querySelector('[aria-label="推理强度"]')?.textContent).toBe(expectedEffort);
    await act(async () => card.querySelector<HTMLButtonElement>(".channel-agent-hover-edit")!.click());
    expect(document.querySelector(".channel-agent-hover-card")).toBeNull();
    const editor = container.querySelector(".channel-settings-panel")!;
    expect(editor).not.toBeNull();
    expect(editor.querySelector<HTMLInputElement>("input")?.value).toBe(bot.name);
    expect(api.readChannelSession).not.toHaveBeenCalled();
  });

  it("reopens a published reply's original execution after the active response has disappeared", async () => {
    const api = createApi();
    api.listChannelMessages = vi.fn(async () => ({ messages: [{
      id: "reply-old", room_id: "room-1", seq: 1, author_type: "agent" as const, author_id: "agent-2",
      kind: "text" as const, body: "Published answer", created_at: "2026-09-12T00:00:00Z",
      source_session_ref: "original-session", source_turn_id: "original-turn",
    }], responses: [] }));
    api.readChannelSession = vi.fn().mockResolvedValue({ session: { state: "running" }, thread: { id: "original-session", turns: [
      { id: "original-turn", status: "completed", items: [{ id: "old", type: "agent_message", text: "Original evidence" }] },
      { id: "later-turn", status: "in_progress", items: [{ id: "new", type: "agent_message", text: "Later work" }] },
    ] } });
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    expect(container.querySelector(".channel-activity-inspect")).toBeNull();
    expect(container.querySelector(".channel-message-stream")?.textContent).not.toContain("查看轨迹");
    const avatar = container.querySelector<HTMLButtonElement>(".channel-agent-hover-trigger")!;
    await act(async () => { avatar.focus(); });
    const trace = document.querySelector<HTMLButtonElement>(".channel-agent-view-trace")!;
    expect(trace.textContent).toBe("查看轨迹");
    await act(async () => { trace.click(); });
    expect(document.querySelector(".channel-agent-hover-card")).toBeNull();
    await settle();
    expect(api.readChannelSession).toHaveBeenCalledWith({ sessionRef: "original-session" });
    const panel = container.querySelector<HTMLElement>(".session-inspector-extension")!;
    expect(panel.textContent).toContain("Original evidence");
    expect(panel.querySelector(".channel-session-meta")?.textContent).toBe("完成");
    vi.useFakeTimers();
    try {
      await act(async () => { panel.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
      await act(async () => { await vi.advanceTimersByTimeAsync(220); });
      expect(container.querySelector(".session-inspector-extension")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps partial answers out of the transcript and shows one bubble only after publication", async () => {
    vi.useFakeTimers();
    try {
      const api = createApi();
      let messages: ChannelMessage[] = [];
      let responses: ChannelResponse[] = [{ id: "reply-live", room_id: "room-1", agent_id: "agent-1", session_ref: "room-session", turn_id: "turn-live", state: "thinking", body: "", created_at: "2026-07-23T00:02:00Z" }];
      api.listChannelMessages = vi.fn(async ({ room_id }) => ({ messages: room_id === "room-1" ? messages : [], responses: room_id === "room-1" ? responses : [] }));
      Object.defineProperty(window, "wuu", { configurable: true, value: api });
      root = createRoot(container);
      act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
      await settle();
      expect(container.querySelector(".channel-response")).toBeNull();
      expect(container.querySelector(".channel-response-status")?.textContent).toContain("Alpha");
      expect(container.querySelector(".channel-room-header [role=status]")).toBeNull();
      responses = [{ ...responses[0], state: "responding", body: "Here is **the answer**" }];
      await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
      expect(container.querySelector(".channel-message-stream")?.textContent).not.toContain("the answer");
      expect(container.querySelector('.channel-activity-region [data-agent-avatar-id="agent-1"]')?.getAttribute("data-agent-avatar-state")).toBe("responding");
      messages = [{ id: "reply-live", room_id: "room-1", seq: 1, author_type: "agent", author_id: "agent-1", kind: "text", body: responses[0].body, created_at: responses[0].created_at }];
      await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
      expect(container.querySelector(".channel-response")).toBeNull();
      expect(container.querySelectorAll(".channel-message-bubble")).toHaveLength(1);
      expect(container.querySelector(".channel-activity-slot:not([inert]) .channel-response-status")).toBeNull();
      expect(container.querySelector(".channel-message-bubble")?.textContent).toContain("the answer");
      expect(container.querySelector('.channel-message [data-agent-avatar-id="agent-1"]')?.getAttribute("data-agent-avatar-state")).toBe("idle");
    } finally {
      vi.useRealTimers();
    }
  });

  it("orders published messages by server sequence regardless of first token order or refresh", async () => {
    vi.useFakeTimers();
    try {
      const api = createApi();
      let messages: ChannelMessage[] = [];
      const alpha: ChannelResponse = { id: "reply-alpha", room_id: "room-1", agent_id: "agent-1", session_ref: "alpha-session", turn_id: "alpha-turn", state: "thinking", body: "", created_at: "2026-07-23T00:02:00Z" };
      const beta: ChannelResponse = { ...alpha, id: "reply-beta", agent_id: "agent-2", session_ref: "beta-session", turn_id: "beta-turn" };
      let responses = [alpha, beta];
      api.listChannelMessages = vi.fn(async () => ({ messages, responses }));
      Object.defineProperty(window, "wuu", { configurable: true, value: api });
      root = createRoot(container);
      act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
      await settle();
      expect(container.querySelectorAll(".channel-response")).toHaveLength(0);
      expect(container.querySelectorAll(".channel-response-status")).toHaveLength(2);
      expect(container.querySelector(".channel-message-stream .channel-response-status")).toBeNull();
      const activityNames = (): string[] => Array.from(container.querySelectorAll(".channel-activity-slot:not([inert]) strong"), (node) => node.textContent ?? "");
      expect(activityNames()).toEqual(["Alpha", "Beta"]);
      const authors = (): string[] => Array.from(container.querySelectorAll(".channel-message-stream .channel-author-mention"), (node) => (node.textContent ?? "").replace(/^@/, ""));
      const refresh = async (): Promise<void> => { await act(async () => { await vi.advanceTimersByTimeAsync(2_000); }); };
      responses = [alpha, { ...beta, state: "responding", body: "Beta starts first" }];
      await refresh();
      expect(authors()).toEqual([]);
      expect(activityNames()).toEqual(["Alpha", "Beta"]);
      responses = [{ ...alpha, state: "responding", body: "Alpha starts second" }, responses[1]];
      await refresh();
      expect(authors()).toEqual([]);
      responses.reverse();
      await refresh();
      expect(activityNames()).toEqual(["Alpha", "Beta"]);
      messages = [{ id: alpha.id, room_id: "room-1", seq: 1, author_type: "agent", author_id: alpha.agent_id, kind: "text", body: "Alpha finishes first", created_at: alpha.created_at }];
      await refresh();
      expect(authors()).toEqual(["Alpha"]);
      expect(container.querySelectorAll(".channel-response")).toHaveLength(0);
      expect(activityNames()).toEqual(["Beta"]);
      messages = [...messages, { ...messages[0], id: beta.id, seq: 2, author_id: beta.agent_id, body: "Beta finishes second" }];
      responses = [];
      await refresh();
      expect(authors()).toEqual(["Alpha", "Beta"]);
      expect(container.querySelectorAll(".channel-response")).toHaveLength(0);
      expect(container.querySelectorAll(".channel-message-bubble")).toHaveLength(2);
      await refresh();
      expect(authors()).toEqual(["Alpha", "Beta"]);
      act(() => root?.unmount());
      root = createRoot(container);
      act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
      await settle();
      expect(authors()).toEqual(["Alpha", "Beta"]);
    } finally {
      vi.useRealTimers();
    }
  });

  it.each(["failed", "interrupted"] as const)("offers session recovery for a %s reply and scopes it to that response", async (state) => {
    const api = createApi();
    api.resumeChannelSession = vi.fn().mockResolvedValue({});
    const rawError = "model returned empty answer (stop_reason=completed)";
    api.listChannelMessages = vi.fn(async ({ room_id }) => ({ messages: [], responses: room_id === "room-1" ? [{ id: "failed-reply", room_id, agent_id: "agent-2", session_ref: "beta-room-session", turn_id: "turn-failed", state, body: "Partial answer", error: rawError, created_at: "2026-07-23T00:03:00Z" }] : [] }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    const alert = container.querySelector(".channel-activity-region [role=alert]");
    expect(alert?.textContent).toContain(userFacingErrorForMessage(rawError, "turn").title);
    expect(alert?.textContent).not.toContain(rawError);
    expect(container.textContent).not.toContain("Partial answer");
    expect(container.querySelector(".channel-message-stream [role=alert]")).toBeNull();
    await act(async () => alert?.querySelector<HTMLButtonElement>("button:not(.channel-activity-inspect)")?.click());
    expect(api.resumeChannelSession).toHaveBeenCalledWith({ sessionRef: "beta-room-session" });
    expect(api.sendChannelMessage).not.toHaveBeenCalled();
    act(() => root?.render(<ChannelView selectedRoomID="room-2" />));
    await settle();
    expect(container.querySelector(".channel-response")).toBeNull();
  });

  it("keeps replies beyond the first history page visible after refresh", async () => {
    vi.useFakeTimers();
    try {
      const api = createApi();
      const messages: ChannelMessage[] = Array.from({ length: 501 }, (_, index) => ({
        id: `history-${index + 1}`, room_id: "room-1", seq: index + 1,
        author_type: "human", author_id: "local-user", kind: "text",
        body: `Message ${index + 1}`, created_at: "2026-07-23T00:03:00Z",
      }));
      messages.push({ ...messages[0], id: "latest-answer", seq: 502, author_type: "agent", author_id: "agent-1", body: "The complete answer after a long conversation" });
      api.listChannelMessages = vi.fn(async ({ room_id, after_seq = 0, limit = 500 }) => ({
        messages: room_id === "room-1" ? messages.filter((message) => message.seq > after_seq).slice(0, limit) : [], responses: [],
      }));
      Object.defineProperty(window, "wuu", { configurable: true, value: api });
      root = createRoot(container);
      act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
      await settle();
      expect(container.querySelectorAll(".channel-message-bubble")).toHaveLength(502);
      expect(container.querySelector(".channel-message.agent")?.textContent).toContain("The complete answer after a long conversation");
      await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
      expect(container.querySelectorAll(".channel-message-bubble")).toHaveLength(502);
      expect(container.querySelector(".channel-message.agent")?.textContent).toContain("The complete answer after a long conversation");
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the acknowledged bubble before a slow refresh and preserves the next draft", async () => {
    const api = createApi();
    let resolveSend!: (value: { message: ChannelMessage }) => void;
    let refreshStarted = false;
    api.listChannelMessages = vi.fn(() => refreshStarted ? new Promise<{ messages: ChannelMessage[]; responses: ChannelResponse[] }>(() => {}) : Promise.resolve({ messages: [], responses: [] }));
    api.sendChannelMessage = vi.fn(() => new Promise<{ message: ChannelMessage }>((resolve) => { resolveSend = resolve; }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-composer textarea")!;
    act(() => setInputValue(textarea, "Please investigate"));
    act(() => container.querySelector<HTMLButtonElement>(".composer-send-button")?.click());
    await settle();
    expect(container.querySelector(".channel-message-pending")?.textContent).toContain("Please investigate");
    act(() => setInputValue(textarea, "And check the logs"));
    refreshStarted = true;
    await act(async () => resolveSend({ message: { id: "sent", room_id: "room-1", seq: 1, author_type: "human", author_id: "local-user", kind: "text", body: "Please investigate", created_at: "2026-07-23T00:03:00Z" } }));
    expect(container.querySelector(".channel-message-pending")).toBeNull();
    expect(container.querySelector(".channel-message-bubble")?.textContent).toBe("Please investigate");
    expect(textarea.value).toBe("And check the logs");
  });

  it("keeps a failed send in the composer with a retry action", async () => {
    const api = createApi();
    api.sendChannelMessage = vi.fn().mockRejectedValue(new Error("Network disconnected"));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    const textarea = container.querySelector<HTMLTextAreaElement>(".channel-composer textarea")!;
    act(() => setInputValue(textarea, "Keep this message"));
    await act(async () => container.querySelector<HTMLButtonElement>(".composer-send-button")?.click());
    await settle();
    expect(textarea.value).toBe("Keep this message");
    expect(container.querySelector(".channel-send-error")?.textContent).toContain("Network disconnected");
    expect(container.querySelector(".channel-message-pending")).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-send-error button")?.click());
    expect(api.sendChannelMessage).toHaveBeenCalledTimes(2);
  });

  it("lets touch users choose channel attachments without a workspace menu", async () => {
    const api = createApi();
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    const input = container.querySelector<HTMLInputElement>(".channel-attachment-input")!;
    const click = vi.spyOn(input, "click");
    act(() => container.querySelector<HTMLButtonElement>(".channel-attachment-button")?.click());
    expect(click).toHaveBeenCalledOnce();
    expect(container.querySelector(".composer-plus-menu")).toBeNull();
  });

  it("coalesces live events and accepts a slow room snapshot without overlapping polls", async () => {
    vi.useFakeTimers();
    try {
      const api = createApi();
      let listener: Parameters<WuuDesktopApi["onServerEvent"]>[0] | undefined;
      api.onServerEvent = vi.fn((handler) => { listener = handler; return () => {}; });
      const response: ChannelResponse = { id: "live", room_id: "room-1", agent_id: "agent-1", session_ref: "visible-session", turn_id: "turn", state: "responding", body: "Beginning", created_at: "2026-07-23T00:03:00Z" };
      let resolveSnapshot!: (value: { messages: ChannelMessage[]; responses: ChannelResponse[] }) => void;
      api.listChannelMessages = vi.fn().mockResolvedValueOnce({ messages: [], responses: [response] }).mockImplementation(() => new Promise((resolve) => { resolveSnapshot = resolve; }));
      Object.defineProperty(window, "wuu", { configurable: true, value: api });
      root = createRoot(container);
      act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
      await settle();
      const emit = (threadID: string): void => listener?.({ workdir: "/workspace", kind: "notification", message: { method: "item/agentMessage/delta", params: { thread_id: threadID, delta: "PRIVATE EVENT CONTENT" } } });
      act(() => emit("private-child-session"));
      await act(async () => { await vi.advanceTimersByTimeAsync(250); });
      expect(api.listChannelMessages).toHaveBeenCalledTimes(1);
      act(() => { emit("visible-session"); emit("visible-session"); emit("visible-session"); });
      await act(async () => { await vi.advanceTimersByTimeAsync(200); });
      expect(api.listChannelMessages).toHaveBeenCalledTimes(2);
      act(() => emit("visible-session"));
      await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });
      expect(api.listChannelMessages).toHaveBeenCalledTimes(2);
      await act(async () => resolveSnapshot({ messages: [], responses: [{ ...response, body: "Public streamed result" }] }));
      expect(container.querySelector(".channel-message-stream")?.textContent).not.toContain("Public streamed result");
      expect(container.querySelector(".channel-activity-region")?.textContent).toContain("Alpha");
      expect(container.textContent).not.toContain("PRIVATE EVENT CONTENT");
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps the full agent answer visible including its table and code", async () => {
    const api = createApi();
    const longBody = `${"Analysis with useful context. ".repeat(60)}\n\n| Check | Result |\n| --- | --- |\n| Runtime | Passed |\n\n\`\`\`go\nfunc main() {}\n\`\`\`\n\nFinal recommendation`;
    api.listChannelMessages = vi.fn(async () => ({ messages: [{ id: "long-answer", room_id: "room-1", seq: 1, author_type: "agent" as const, author_id: "agent-1", kind: "text" as const, body: longBody, created_at: "2026-07-23T00:02:00Z" }] }));
    Object.defineProperty(window, "wuu", { configurable: true, value: api });
    root = createRoot(container);
    act(() => root?.render(<ChannelView selectedRoomID="room-1" />));
    await settle();
    const answer = container.querySelector(".channel-message.agent .channel-message-bubble");
    expect(answer?.querySelector("table")?.textContent).toContain("Passed");
    expect(answer?.querySelector("pre")?.textContent).toContain("func main");
    expect(answer?.textContent).toContain("Final recommendation");
    expect(answer?.querySelector(".channel-message-expand-toggle")).toBeNull();
  });

});
