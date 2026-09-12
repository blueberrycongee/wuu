import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChannelSessionReadResult, ThreadItem, WuuDesktopApi } from "../shared/protocol";
import { ChannelSessionInspector } from "./ChannelSessionInspector";
import { streamTextKey, streamTextStore } from "./StreamText";
import { handleStreamingNotification, initialState } from "./AppState";
import { subscribeServerEvents } from "./ServerEvents";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let host: HTMLDivElement;
let root: Root;
let api: Partial<WuuDesktopApi>;
let notify: Parameters<WuuDesktopApi["onServerEvent"]>[0];
const off = vi.fn();
const snapshot = (ref: string, text: string): ChannelSessionReadResult => ({
  session: { session_ref: ref, principal_id: "agent", named_agent_id: "agent", room_id: "room", purpose: "conversation", state: "running", created_at: "", updated_at: "" },
  thread: { id: ref, cwd: "", preview: "", created_at: "", updated_at: "", model_provider: "", model: "", status: "idle",
    turns: Array.from({ length: 22 }, (_, index) => ({ id: `turn-${index}`, status: "completed", items_view: "full", items: [
      { id: `user-${index}`, type: "user_message", text: `Question ${index}` },
      { id: `tool-${index}`, type: "tool_call", name: "read_file", result: `Evidence ${index}` },
      { id: `answer-${index}`, type: "agent_message", text: index === 21 ? text : `Answer ${index}` },
    ] })),
  },
});
async function render(ref = "session-a", closing = false) {
  await act(async () => root.render(<WuuUIRoot><ChannelSessionInspector sessionRef={ref} name="Andy2" closing={closing} onClose={() => {}} /></WuuUIRoot>));
}
beforeEach(() => {
  vi.useFakeTimers(); off.mockClear();
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  api = {
    readChannelSession: vi.fn(async ({ sessionRef }) => snapshot(sessionRef, "Partial reply")),
    onServerEvent: vi.fn((handler) => { notify = handler; return off; }),
  };
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
});
afterEach(() => { act(() => root.unmount()); host.remove(); for (let n = 0; n < 22; n++) streamTextStore.clearTurn(`turn-${n}`); vi.useRealTimers(); });

function liveSnapshot(ref = "session-a", text = "Partial reply"): ChannelSessionReadResult {
  const result = snapshot(ref, text);
  result.thread.status = "in_progress";
  const turn = result.thread.turns.at(-1)!;
  turn.status = "in_progress";
  turn.items.at(-1)!.status = "in_progress";
  turn.items.at(-1)!.terminal = true;
  return result;
}
function emit(method: string, params: Record<string, unknown>, workdir = "/owner") {
  notify({ workdir, kind: "notification", message: { method, params: { thread_id: "session-a", turn_id: "turn-21", ...params } } });
}

it("streams the addressed session through Harness views without polling snapshots", async () => {
  api.readChannelSession = vi.fn(async () => liveSnapshot());
  await render();
  const panel = document.querySelector(".session-inspector-extension")!;
  expect(panel.querySelectorAll(".turn")).toHaveLength(22);
  expect(panel.textContent).toContain("Question 0");
  expect(panel.textContent).toContain("Partial reply");
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  await act(async () => {
    emit("item/agentMessage/delta", { thread_id: "other", item_id: "answer-21", delta: "WRONG" });
    emit("item/agentMessage/delta", { item_id: "answer-21", delta: " continues live" });
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(panel.textContent).toContain("Partial reply continues live");
  expect(panel.textContent).not.toContain("WRONG");
  await act(async () => { await vi.advanceTimersByTimeAsync(4_000); });
  expect(api.readChannelSession).toHaveBeenCalledTimes(1);
  const completed = liveSnapshot("session-a", "Partial reply continues live").thread.turns.at(-1)!;
  completed.status = "completed";
  completed.items.at(-1)!.status = "completed";
  await act(async () => emit("turn/completed", { turn: completed }));
  expect(panel.querySelector('[data-turn-id="turn-21"]')?.getAttribute("data-turn-status")).toBe("completed");
  expect(panel.textContent).toContain("Partial reply continues live");
});

it("keeps events after the ordered snapshot when the invoke promise resolves late", async () => {
  let resolveRead!: (result: ChannelSessionReadResult) => void;
  let requestID = "";
  api.readChannelSession = vi.fn(({ requestId }) => {
    requestID = requestId!;
    return new Promise<ChannelSessionReadResult>(resolve => { resolveRead = resolve; });
  });
  await render();
  const initial = liveSnapshot();
  await act(async () => {
    emit("channel/session/snapshot", { request_id: requestID, result: initial });
    emit("item/agentMessage/delta", { item_id: "answer-21", delta: " after snapshot" });
    resolveRead(initial);
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(host.textContent).toContain("Partial reply after snapshot");
  await act(async () => {
    emit("item/agentMessage/replace", { item_id: "answer-21", text: "Revised" });
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(host.textContent).toContain("Revised");
  expect(host.textContent).not.toContain("after snapshot");
});

it("appends shared stream events once when Harness also observes the conversation", async () => {
  const result = liveSnapshot();
  api.readChannelSession = vi.fn(async () => result);
  const disconnectHarness = subscribeServerEvents(event => handleStreamingNotification(event, { ...initialState, thread: result.thread }));
  try {
    await render();
    await act(async () => {
      emit("item/agentMessage/delta", { item_id: "answer-21", delta: " once" });
      await vi.advanceTimersByTimeAsync(250);
    });
    expect(streamTextStore.get(streamTextKey("turn-21", "answer-21", "text"))).toBe("Partial reply once");
    expect(api.onServerEvent).toHaveBeenCalledTimes(1);
  } finally { disconnectHarness(); }
});

it("shows reasoning and tool lifecycle events even before the first answer", async () => {
  const result = liveSnapshot("session-a", "");
  result.thread.turns.at(-1)!.items = [{ id: "user-21", type: "user_message", text: "Inspect the code" }];
  api.readChannelSession = vi.fn(async () => result);
  await render();
  const tool: ThreadItem = { id: "live-tool", type: "tool_call", name: "read_file", status: "in_progress", arguments: "", result: "" };
  await act(async () => {
    emit("item/started", { item: { id: "thinking", type: "reasoning", status: "in_progress", text: "" } });
    emit("item/reasoning/delta", { item_id: "thinking", delta: "Checking callers" });
    emit("item/started", { item: tool });
    emit("item/toolCall/delta", { item_id: tool.id, delta: '{"path":"src/main.ts"}' });
    emit("item/toolCall/outputDelta", { item_id: tool.id, delta: "File contents" });
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(host.querySelector('[data-turn-id="turn-21"] .assistant-turn-shell')).not.toBeNull();
  expect(streamTextStore.get(streamTextKey("turn-21", "thinking", "text"))).toBe("Checking callers");
  expect(streamTextStore.get(streamTextKey("turn-21", tool.id, "result"))).toBe("File contents");
  await act(async () => {
    emit("item/completed", { item: { ...tool, status: "completed", result: "File contents", arguments: '{"path":"src/main.ts"}' } });
    emit("item/removed", { item_id: "thinking" });
  });
  expect(streamTextStore.has(streamTextKey("turn-21", "thinking", "text"))).toBe(false);
});

it("ignores an old session's late snapshot and stops its subscription when switching", async () => {
  let resolveOld!: (result: ChannelSessionReadResult) => void;
  api.readChannelSession = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }))
    .mockImplementation(async ({ sessionRef }) => snapshot(sessionRef, "New session"));
  await render();
  await act(async () => { await vi.advanceTimersByTimeAsync(4_000); });
  expect(api.readChannelSession).toHaveBeenCalledTimes(1);
  await render("session-b");
  expect(off).toHaveBeenCalledTimes(1);
  await act(async () => resolveOld(snapshot("session-a", "Old stale content")));
  expect(document.querySelector(".session-inspector-extension")?.textContent).toContain("New session");
  expect(document.querySelector(".session-inspector-extension")?.textContent).not.toContain("Old stale content");
});

it("stops consuming events as soon as closing begins", async () => {
  api.readChannelSession = vi.fn(async () => liveSnapshot());
  await render();
  await render("session-a", true);
  expect(off).toHaveBeenCalledTimes(1);
  await act(async () => {
    emit("item/agentMessage/delta", { item_id: "answer-21", delta: " after close" });
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(host.textContent).not.toContain("after close");
});
