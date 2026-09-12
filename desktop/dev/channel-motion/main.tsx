import { useState } from "react";
import { createRoot } from "react-dom/client";
import type { ChannelMessage, ChannelResponse, ChannelRoom, NamedAgent, WuuDesktopApi } from "../../src/shared/protocol";
import { ChannelView } from "../../src/renderer/ChannelView";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

document.documentElement.dataset.theme = "light";
document.documentElement.dataset.platform = "mac";
const created_at = new Date().toISOString();
const agent: NamedAgent = { id: "preview-agent", name: "Research", memory_dir: "/preview", avatar_key: "mascot-v1:round:none:150", autostart: false, created_at };
const room: ChannelRoom = { id: "motion-preview", name: "消息动效", kind: "channel", created_by: "human", created_at, members: [
  { room_id: "motion-preview", member_type: "human", member_id: "local-user", joined_at: created_at },
  { room_id: "motion-preview", member_type: "agent", member_id: agent.id, joined_at: created_at },
] };
let sequence = 0;
const makeMessage = (body: string, own: boolean): ChannelMessage => ({ id: `preview-${++sequence}`, seq: sequence, room_id: room.id, author_type: own ? "human" : "agent", author_id: own ? "local-user" : agent.id, kind: "text", body, created_at: new Date().toISOString() });
const messages = [makeMessage("我们把这次的协作体验再打磨一下。", true), makeMessage("可以，先看消息发出和回复出现时的衔接。", false)];
let responses: ChannelResponse[] = [];
const listeners = new Set<Parameters<WuuDesktopApi["onServerEvent"]>[0]>();
const examples = ["帮我看看这次改动。", "消息稍微长一点的时候，也希望它自然地落下来，不要打断阅读。", "这个节奏可以，再看一条。"];
const answers = ["我看完了，气泡的出现和发送确认现在能接起来了。", "这条是完整发布的回复。\n\n入场只发生一次，已有消息会保持原位，文字可以直接阅读。", "收到。保持短促、轻柔的入场节奏。"];
let sent = 0;
const api: Partial<WuuDesktopApi> = {
  bootstrapChannels: async () => ({ agents: [agent], rooms: [room] }),
  listNamedAgents: async () => ({ agents: [agent] }),
  listChannelRooms: async () => ({ rooms: [room] }),
  listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }),
  onServerEvent: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
  listChannelMessages: async () => ({ messages: [...messages], responses }),
  sendChannelMessage: async input => {
    await new Promise(resolve => setTimeout(resolve, 100));
    const message = { ...makeMessage(input.body, true), images: input.images, files: input.files };
    messages.push(message);
    responses = [{ id: "preview-response", room_id: room.id, agent_id: agent.id, session_ref: "preview-session", turn_id: "preview-turn", state: "thinking", body: "", created_at }];
    const answer = answers[sent++ % answers.length];
    window.setTimeout(() => {
      messages.push(makeMessage(answer, false)); responses = [];
      for (const listener of listeners) listener({ workdir: "/preview", kind: "notification", message: { method: "turn/completed", params: { thread_id: "preview-session" } } });
    }, 900);
    return { message };
  },
};
window.wuu = api as WuuDesktopApi;

function Preview() {
  const [dark, setDark] = useState(false);
  const play = () => {
    const input = document.querySelector<HTMLTextAreaElement>(".channel-composer textarea");
    if (!input) return;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, examples[sent % examples.length]);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(".composer-send-button")?.click());
  };
  return <div style={{ height: "100dvh", display: "flex", flexDirection: "column" }}>
    <header style={{ display: "flex", alignItems: "center", gap: 12, padding: "8px 16px", borderBottom: "1px solid var(--hairline)", flexShrink: 0 }}>
      <span style={{ flex: 1, fontSize: 12, color: "var(--text-secondary)" }}>Collaboration · 消息动效</span>
      <button type="button" onClick={play}>播放对话</button>
      <button type="button" aria-pressed={dark} onClick={() => { setDark(!dark); document.documentElement.dataset.theme = dark ? "light" : "dark"; }}>深色</button>
    </header>
    <div style={{ display: "flex", flex: 1, minHeight: 0 }}><ChannelView selectedRoomID={room.id} /></div>
  </div>;
}
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><ImagePreviewProvider><Preview /></ImagePreviewProvider></WuuUIRoot></I18nProvider>);
