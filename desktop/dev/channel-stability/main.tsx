import { useState } from "react";
import { createRoot } from "react-dom/client";
import type { ChannelMessage, ChannelRoom, InitializeResult, NamedAgent, WuuDesktopApi } from "../../src/shared/protocol";
import { AgentOnboarding, createAgentOnboardingDraft } from "../../src/renderer/AgentOnboarding";
import { ChannelView } from "../../src/renderer/ChannelView";
import { I18nProvider } from "../../src/renderer/i18n";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

// Isolated browser fixture: real renderer components, deferred host responses,
// no credentials, persistence, or real agent/model calls.
const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
document.documentElement.dataset.platform = "mac";
document.documentElement.style.setProperty("--conversation-message-font-size", `${params.get("font") || 14}px`);
const created_at = new Date().toISOString();
const agent: NamedAgent = { id: "stability-agent", name: "阿铁打", avatar_key: "abstract-1", memory_dir: "/preview", autostart: false, created_at };
const room: ChannelRoom = { id: "stability-room", name: agent.name, kind: "dm", created_by: "human", created_at, members: [
  { room_id: "stability-room", member_type: "human", member_id: "local-user", joined_at: created_at },
  { room_id: "stability-room", member_type: "agent", member_id: agent.id, joined_at: created_at },
] };
const initialized: InitializeResult = { protocol_version: "1", workspace_root: "/preview", provider: "preview", model: "reasoner", effort: "high", providers: [
  { name: "preview", type: "openai-compatible", model: "reasoner", api_key_configured: true, models: [{ id: "reasoner", supported_efforts: ["low", "high"], default_effort: "high" }] },
] };
let releaseOpen: (() => void) | undefined;
let releaseSend: (() => void) | undefined;
let sequence = 0;
let polls = 0;
const messages: ChannelMessage[] = [];
const controls = {
  get canOpen() { return Boolean(releaseOpen); },
  get canAcknowledge() { return Boolean(releaseSend); },
  get polls() { return polls; },
  open: () => releaseOpen?.(),
  acknowledge: () => releaseSend?.(),
  append: (count: number) => {
    for (let i = 0; i < count; i++) messages.push({ id: `history-${++sequence}`, room_id: room.id, seq: sequence, kind: "text", author_type: "agent", author_id: agent.id, body: `History ${i}: A complete message for checking the reading anchor.`, created_at });
  },
};
Object.assign(window, { stability: controls });
const api: Partial<WuuDesktopApi> = {
  bootstrapChannels: async () => ({ agents: [agent], rooms: [room] }),
  listNamedAgents: async () => ({ agents: [agent] }),
  listChannelRooms: async () => ({ rooms: [room] }),
  listChannelMessages: async () => { polls++; return { messages: [...messages], responses: [] }; },
  listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }),
  onServerEvent: () => () => {},
  sendChannelMessage: async (input) => {
    const message: ChannelMessage = { id: `sent-${++sequence}`, room_id: room.id, seq: sequence, kind: "text", author_type: "human", author_id: "local-user", body: input.body, images: input.images, files: input.files, created_at: new Date().toISOString() };
    messages.push(message);
    await new Promise<void>(resolve => { releaseSend = resolve; });
    return { message };
  },
};
window.wuu = api as WuuDesktopApi;

function Preview() {
  const [draft, setDraft] = useState(() => createAgentOnboardingDraft(initialized));
  const [opened, setOpened] = useState(false);
  return <div style={{ display: "flex", height: "100dvh" }}>
    {opened ? <ChannelView initialized={initialized} selectedRoomID={room.id} /> : <AgentOnboarding
      initialized={initialized} draft={draft} onDraftChange={setDraft} onCreate={async (input) => ({ ...agent, name: input.name })} onClose={() => {}}
      onOpenConversation={async (created, onboarding) => {
        await new Promise<void>(resolve => { releaseOpen = resolve; });
        agent.name = created.name;
        room.name = created.name;
        room.onboarding = onboarding;
        setOpened(true);
      }} />}
  </div>;
}
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><ImagePreviewProvider><Preview /></ImagePreviewProvider></WuuUIRoot></I18nProvider>);
