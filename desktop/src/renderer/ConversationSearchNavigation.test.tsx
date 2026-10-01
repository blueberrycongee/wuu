import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { Thread, ThreadItem } from "../shared/protocol";
import { initialState, type AppState } from "./AppState";
import { useConversationSearchNavigation } from "./ConversationSearchNavigation";

const { jump, cancelJump, unscopedJump, showErrorToast } = vi.hoisted(() => {
  const cancelJump = vi.fn();
  return { jump: vi.fn(() => cancelJump), cancelJump, unscopedJump: vi.fn(() => () => {}), showErrorToast: vi.fn() };
});
vi.mock("./TurnViewHelpers", () => ({ scrollToConversationMessage: unscopedJump }));
vi.mock("./Toast", () => ({ showErrorToast }));
let root: Root;
afterEach(() => {
  act(() => root?.unmount());
  vi.useRealTimers(); vi.clearAllMocks();
  Reflect.deleteProperty(window, "wuu");
});

function setup(items?: ThreadItem[]) {
  vi.useFakeTimers();
  const item: ThreadItem = {
    id: "answer", seq: 7, type: "agent_message", text: "Leading preview",
    remote_content_ref: "content:old-answer",
  };
  const thread: Thread = {
    id: "history", title: "History", preview: "", cwd: "/project", status: "idle",
    model_provider: "fixture", model: "fixture", created_at: "", updated_at: "",
    turns: [{ id: "old-turn", status: "completed", items_view: "full", items: items ?? [item] }],
    history_cursor: items ? "earlier-items" : undefined,
  };
  const pending: { resolve: (item: ThreadItem) => void; reject: (error: Error) => void }[] = [];
  const read = vi.fn(() => new Promise<ThreadItem>((resolve, reject) => pending.push({ resolve, reject })));
  window.wuu = { readRemoteItem: read } as unknown as typeof window.wuu;
  let navigate: ReturnType<typeof useConversationSearchNavigation>;
  let setState: React.Dispatch<React.SetStateAction<AppState>>;
  let state: AppState;
  function Harness() {
    [state, setState] = useState<AppState>({ ...initialState, thread, threads: [thread] });
    navigate = useConversationSearchNavigation({
      thread: state.thread, switching: false, activateThread: async () => {},
      captureConversationScrollIntent: () => () => true, jumpToConversationMessage: jump, setAppState: setState,
    });
    return null;
  }
  root = createRoot(document.createElement("div"));
  act(() => root.render(<Harness />));
  return {
    item, thread, read, pending,
    get state() { return state; },
    select: () => act(async () => navigate({ thread, message_seq: 7, snippet: "late needle" }, "late needle")),
    leave: () => act(() => setState(current => ({ ...current, thread: { ...thread, id: "other" } }))),
    install: (thread: Thread) => setState(current => ({ ...current, thread })),
  };
}

it("hydrates an addressed preview before matching and revealing the full historical message", async () => {
  const h = setup();
  await h.select();
  expect(h.read).toHaveBeenCalledExactlyOnceWith("content:old-answer");
  expect(jump).not.toHaveBeenCalled();
  const full = { ...h.item, remote_content_ref: undefined, text: "Leading preview\n" + "context ".repeat(10000) + "late needle" };
  await act(async () => h.pending[0].resolve(full));
  await act(async () => vi.advanceTimersByTimeAsync(30));
  expect(h.state.thread?.turns[0].items[0]).toEqual(full);
  expect(jump).toHaveBeenCalledWith("old-turn", full, "late needle");
});

it("does not hydrate or jump after the reader leaves while content is loading", async () => {
  const h = setup(); await h.select(); h.leave();
  await act(async () => h.pending[0].resolve({ ...h.item, remote_content_ref: undefined, text: "late needle" }));
  await act(async () => vi.advanceTimersByTimeAsync(30));
  expect(h.state.thread?.id).toBe("other");
  expect(h.state.threads[0].turns[0].items[0]).toEqual(h.item);
  expect(jump).not.toHaveBeenCalled();
});

it("keeps the preview after a failed read and retries on a new search selection", async () => {
  const h = setup(); await h.select();
  await act(async () => h.pending[0].reject(new Error("offline")));
  expect(h.state.thread?.turns[0].items[0]).toEqual(h.item);
  expect(showErrorToast).toHaveBeenCalledOnce();
  expect(jump).not.toHaveBeenCalled();
  await h.select();
  expect(h.read).toHaveBeenCalledTimes(2);
});

it("does not jump to message text when the complete item only matched metadata", async () => {
  const h = setup(); await h.select();
  await act(async () => h.pending[0].resolve({ ...h.item, remote_content_ref: undefined, text: "Different body" }));
  await act(async () => vi.advanceTimersByTimeAsync(30));
  expect(jump).not.toHaveBeenCalled();
});

it("continues an item-split history page past tools sharing the addressed message sequence", async () => {
  const h = setup([{ id: "tool", type: "tool_call", seq: 7, name: "read_file" }]);
  const full = { ...h.item, remote_content_ref: undefined, text: "late needle" };
  window.wuu.loadEarlierThreadHistory = vi.fn(async () => h.install({
    ...h.thread, history_cursor: undefined,
    turns: [{ ...h.thread.turns[0], items: [full, ...h.thread.turns[0].items] }],
  }));
  await h.select();
  await act(async () => vi.advanceTimersByTimeAsync(30));
  expect(window.wuu.loadEarlierThreadHistory).toHaveBeenCalledExactlyOnceWith("history", "earlier-items");
  expect(jump).toHaveBeenCalledWith("old-turn", full, "late needle");
});

it("cancels the controller-owned search jump when its destination changes", async () => {
  const h = setup([{ id: "answer", seq: 7, type: "agent_message", text: "late needle" }]);
  await h.select();
  await act(async () => vi.advanceTimersByTimeAsync(30));
  expect(jump).toHaveBeenCalledOnce();
  expect(unscopedJump).not.toHaveBeenCalled();
  h.leave();
  expect(cancelJump).toHaveBeenCalledOnce();
});
