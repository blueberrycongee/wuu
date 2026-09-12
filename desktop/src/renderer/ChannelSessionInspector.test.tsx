import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChannelSessionReadResult, WuuDesktopApi } from "../shared/protocol";
import { ChannelSessionInspector } from "./ChannelSessionInspector";
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
async function render(ref = "session-a") {
  await act(async () => root.render(<WuuUIRoot><ChannelSessionInspector sessionRef={ref} name="Andy2" left={900} width={480} onClose={() => {}} /></WuuUIRoot>));
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
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });

it("uses the Harness turn list and refreshes only the addressed live session", async () => {
  await render();
  expect(host.querySelector(".channel-sessions-launcher")).toBeNull();
  const panel = document.querySelector(".session-inspector-extension")!;
  expect(panel.querySelectorAll(".turn")).toHaveLength(22);
  expect(panel.textContent).toContain("Question 0");
  expect(panel.textContent).toContain("Partial reply");
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  api.readChannelSession = vi.fn(async ({ sessionRef }) => snapshot(sessionRef, "Streaming reply updated"));
  await act(async () => {
    notify({ workdir: "", kind: "notification", message: { method: "item/agentMessage/delta", params: { thread_id: "other" } } });
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(api.readChannelSession).not.toHaveBeenCalled();
  await act(async () => {
    for (let n = 0; n < 10; n++) notify({ workdir: "", kind: "notification", message: { method: "item/agentMessage/delta", params: { thread_id: "session-a" } } });
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(api.readChannelSession).toHaveBeenCalledTimes(1);
  expect(api.readChannelSession).toHaveBeenCalledWith({ sessionRef: "session-a" });
  expect(panel.textContent).toContain("Streaming reply updated");
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
