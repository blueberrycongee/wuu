import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelRoom, ChannelSessionReadResult, CollaborationSessionBinding, InitializeResult, NamedAgent, WuuDesktopApi } from "../shared/protocol";
import { ChannelSessions } from "./ChannelSessions";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

const agent: NamedAgent = { id: "researcher", name: "Researcher", avatar_key: "abstract-3", memory_dir: "/memory", autostart: true, created_at: "2026-09-12T00:00:00Z" };
const room: ChannelRoom = { id: "room", name: "Lab", kind: "channel", created_by: "human", created_at: agent.created_at, members: [{ room_id: "room", member_type: "agent", member_id: agent.id, joined_at: agent.created_at }] };
const binding = (id: string, state: CollaborationSessionBinding["state"] = "running"): CollaborationSessionBinding => ({
  session_ref: id, principal_id: agent.id, named_agent_id: agent.id, room_id: room.id, purpose: "work", state,
  title: id === "first" ? "Check connection" : "Test recovery", objective: `Investigate ${id}`, provider: "byok", model: id === "first" ? "model-a" : "model-b", created_at: agent.created_at, updated_at: agent.created_at,
});
const readResult = (session: CollaborationSessionBinding): ChannelSessionReadResult => ({ session, thread: {
  created_at: agent.created_at, updated_at: agent.created_at, id: session.session_ref, preview: session.title ?? "", cwd: "/project", model_provider: session.provider ?? "", model: session.model ?? "", status: "idle",
  turns: [{ id: "turn", status: "completed", items_view: "full", items: [{ id: "reply", type: "agent_message", text: `Evidence for ${session.session_ref}` }] }],
} });
let container: HTMLDivElement;
let root: Root;
let sessions: CollaborationSessionBinding[];
let api: Partial<WuuDesktopApi>;

async function settle(): Promise<void> { await act(async () => { await Promise.resolve(); await Promise.resolve(); }); }
async function click(text: string): Promise<void> {
  const button = [...container.querySelectorAll<HTMLButtonElement>("button")].find((node) => node.textContent?.trim() === text);
  expect(button, text).toBeTruthy();
  await act(async () => button!.click());
  await settle();
}
async function fill(selector: string, value: string): Promise<void> {
  const input = container.querySelector<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(selector)!;
  expect(input).toBeTruthy();
  const prototype = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  await act(async () => {
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
  });
}
async function render(props: { roomId?: string; agentId?: string; initialized?: InitializeResult } = { roomId: room.id }): Promise<void> {
  await act(async () => root.render(<WuuUIRoot><ChannelSessions agents={[agent]} rooms={[room]} {...props} /></WuuUIRoot>));
  await settle();
}
async function open(): Promise<void> {
  await act(async () => container.querySelector<HTMLButtonElement>("[aria-haspopup=dialog]")!.click());
  await settle();
}

beforeEach(() => {
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  sessions = [binding("first"), binding("second")];
  api = {
    listChannelSessions: vi.fn(async () => ({ sessions: [...sessions] })),
    readChannelSession: vi.fn(async ({ sessionRef }) => readResult(sessions.find((session) => session.session_ref === sessionRef)!)),
    createChannelSession: vi.fn(async (params) => { const session = { ...binding("new", "starting"), title: params.title, objective: params.prompt, provider: params.provider, model: params.model }; sessions.push(session); return { session }; }),
    stopChannelSession: vi.fn(async ({ sessionRef }) => { sessions = sessions.map((session) => session.session_ref === sessionRef ? { ...session, state: "cancelled" } : session); return { session: sessions.find((session) => session.session_ref === sessionRef)! }; }),
    resumeChannelSession: vi.fn(async ({ sessionRef }) => { sessions = sessions.map((session) => session.session_ref === sessionRef ? { ...session, state: "starting" } : session); return { session: sessions.find((session) => session.session_ref === sessionRef)! }; }),
    sendChannelSession: vi.fn(async ({ sessionRef }) => ({ session: sessions.find((session) => session.session_ref === sessionRef)! })),
    openChannelDirectMessage: vi.fn(async () => ({ room: { ...room, id: "dm", kind: "dm" as const } })),
  };
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
});
afterEach(() => { act(() => root.unmount()); container.remove(); vi.useRealTimers(); vi.restoreAllMocks(); });

describe("collaboration sessions", () => {
  it("keeps two sessions under one identity and targets stop, resume, and follow-up to the selected session", async () => {
    await render(); await open();
    expect(container.querySelectorAll(".channel-session-group")).toHaveLength(1);
    expect(container.querySelectorAll(".channel-session-row")).toHaveLength(2);
    expect(container.textContent).toContain("model-a"); expect(container.textContent).toContain("model-b");
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-session-row")!.click()); await settle();
    expect(container.textContent).toContain("Evidence for first");
    await click("停止");
    expect(sessions[0].state).toBe("cancelled"); expect(sessions[1].state).toBe("running");
    await click("继续");
    expect(api.resumeChannelSession).toHaveBeenCalledWith({ sessionRef: "first" });
    await fill(".channel-session-followup textarea", "Check the reconnect race too"); await click("发送");
    expect(api.sendChannelSession).toHaveBeenCalledWith({ sessionRef: "first", prompt: "Check the reconnect race too", requestId: expect.any(String) });
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("");
  });

  it("starts independent work with an explicit BYOK model under the existing identity", async () => {
    await render({ roomId: room.id, initialized: { provider: "byok", model: "default", providers: [{ name: "byok", model: "model-b", models: [{ id: "model-b", display_name: "Model B" }] }] } as InitializeResult });
    await open(); await click("新建会话");
    await fill(".channel-session-create input", "Independent experiment");
    await fill(".channel-session-create textarea", "Try another hypothesis");
    await fill(".channel-session-create label:last-of-type select", "byok\u0000model-b");
    await click("启动会话");
    expect(api.createChannelSession).toHaveBeenCalledWith({ agentId: agent.id, roomId: room.id, title: "Independent experiment", prompt: "Try another hypothesis", requestId: expect.any(String), provider: "byok", model: "model-b" });
    expect(api.openChannelDirectMessage).not.toHaveBeenCalled();
    expect(sessions).toHaveLength(3);
    expect(sessions.filter((session) => session.named_agent_id === agent.id)).toHaveLength(3);
    expect(container.textContent).toContain("Evidence for new");
  });

  it("opens a private room before creating a session from the identity page", async () => {
    await render({ agentId: agent.id }); await open(); await click("新建会话");
    await fill("textarea", "Independent research"); await click("启动会话");
    expect(api.listChannelSessions).toHaveBeenCalledWith({ agentId: agent.id });
    expect(api.openChannelDirectMessage).toHaveBeenCalledWith({ agent_id: agent.id });
    expect(api.createChannelSession).toHaveBeenCalledWith({ agentId: agent.id, roomId: "dm", prompt: "Independent research", requestId: expect.any(String) });
  });

  it("preserves an unsent follow-up on failure and offers the same session for retry", async () => {
    api.sendChannelSession = vi.fn().mockRejectedValueOnce(new Error("Provider offline")).mockImplementation(async ({ sessionRef }) => ({ session: sessions.find((session) => session.session_ref === sessionRef)! }));
    await render(); await open();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-session-row")!.click()); await settle();
    await fill("textarea", "Keep this request"); await click("发送");
    expect(container.querySelector("[role=alert]")?.textContent).toContain("Provider offline");
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("Keep this request");
    await click("发送"); expect(api.sendChannelSession).toHaveBeenCalledTimes(2);
    const calls = vi.mocked(api.sendChannelSession!).mock.calls;
    expect(calls[0][0].requestId).toBeTruthy();
    expect(calls[0][0].requestId).toBe(calls[1][0].requestId);
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("");
  });

  it("keeps uncertain delivery identifiers separate when switching between concurrent sessions", async () => {
    api.sendChannelSession = vi.fn().mockRejectedValueOnce(new Error("Connection lost after sending")).mockImplementation(async ({ sessionRef }) => ({ session: sessions.find((session) => session.session_ref === sessionRef)! }));
    await render(); await open();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-session-row")!.click()); await settle();
    await fill("textarea", "Request for first"); await click("发送");
    await click("全部会话");
    await act(async () => container.querySelectorAll<HTMLButtonElement>(".channel-session-row")[1].click()); await settle();
    await fill("textarea", "Request for second"); await click("发送");
    await click("全部会话");
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-session-row")!.click()); await settle();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("Request for first");
    await click("发送");
    const calls = vi.mocked(api.sendChannelSession!).mock.calls.map(([params]) => params);
    expect(calls.map((params) => params.sessionRef)).toEqual(["first", "second", "first"]);
    expect(calls[0].requestId).toBe(calls[2].requestId);
    expect(calls[1].requestId).not.toBe(calls[0].requestId);
  });

  it("loads execution details only when the user expands them", async () => {
    api.readChannelSession = vi.fn(async ({ sessionRef }) => {
      const result = readResult(sessions.find((session) => session.session_ref === sessionRef)!);
      result.thread.turns[0].items.push({ id: "tool", type: "tool_call", name: "exec_command", text: "recovery test output" });
      return result;
    });
    await render(); await open();
    await act(async () => container.querySelector<HTMLButtonElement>(".channel-session-row")!.click()); await settle();
    expect(container.textContent).not.toContain("recovery test output");
    await act(async () => { const details = container.querySelector("details")!; details.open = true; details.dispatchEvent(new Event("toggle")); });
    expect(container.textContent).toContain("recovery test output");
  });

  it("ignores a delayed list response from the previous room", async () => {
    let finishOld!: (result: { sessions: CollaborationSessionBinding[] }) => void;
    api.listChannelSessions = vi.fn().mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; })).mockResolvedValue({ sessions: [{ ...binding("second"), room_id: "other" }] });
    await render({ roomId: room.id });
    await render({ roomId: "other" });
    await act(async () => finishOld({ sessions: [binding("first")] }));
    await open();
    expect(container.textContent).toContain("Test recovery"); expect(container.textContent).not.toContain("Check connection");
  });
});
