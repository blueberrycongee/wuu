import { act, useSyncExternalStore } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { Thread, ThreadItem } from "../shared/protocol";
import { initialState } from "./AppState";
import { useConversationSearchNavigation } from "./ConversationSearchNavigation";
import { ApplicationController } from "./plugins/ApplicationController";

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

function setup(items?: ThreadItem[], options?: { deferActivation?: boolean; startElsewhere?: boolean }) {
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
  const activation: Array<() => void> = [];
  const application = new ApplicationController({ ...initialState, thread: options?.startElsewhere ? { ...thread, id: "source" } : thread, threads: [thread] });
  let navigate: ReturnType<typeof useConversationSearchNavigation>;
  let generation = 0;
  let readingIntent = 0;
  function Harness() {
    const state = useSyncExternalStore(application.subscribeState, application.getState);
    const props = {
      thread: state.thread, target: state.conversationSearchTarget, switching: false,
      activateThread: () => options?.deferActivation ? new Promise<void>(resolve => activation.push(resolve)) : Promise.resolve(),
      captureConversationScrollIntent: () => { const captured = readingIntent; return () => captured === readingIntent; },
      jumpToConversationMessage: jump, setAppState: application.updateState,
    };
    navigate = useConversationSearchNavigation(props);
    return null;
  }
  root = createRoot(document.createElement("div"));
  act(() => root.render(<Harness />));
  return {
    item, thread, read, pending, activation,
    get state() { return application.getState(); },
    select: (query = "late needle") => act(async () => navigate({ thread, message_seq: 7, snippet: query }, query)),
    leave: () => act(() => { readingIntent++; application.updateState(current => ({ ...current, thread: { ...thread, id: "other" } })); }),
    install: (thread: Thread) => application.updateState(current => ({ ...current, thread })),
    remount: () => act(() => root.render(<Harness key={++generation} />)),
    readerInput: () => { readingIntent++; },
  };
}

it("retains a cross-workspace search through shell remount, paging and full-content hydration", async () => {
  const h = setup([{ id: "tool", type: "tool_call", seq: 7, name: "read_file" }], { deferActivation: true, startElsewhere: true });
  window.wuu.loadEarlierThreadHistory = vi.fn(async () => h.install({
    ...h.thread, history_cursor: undefined, turns: [{ ...h.thread.turns[0], items: [h.item] }],
  }));
  await h.select();
  h.remount();
  await act(async () => { h.install(h.thread); h.activation[0](); });
  expect(window.wuu.loadEarlierThreadHistory).toHaveBeenCalledExactlyOnceWith("history", "earlier-items");
  expect(h.read).toHaveBeenCalledExactlyOnceWith("content:old-answer");
  expect(jump).not.toHaveBeenCalled();
  const full = { ...h.item, remote_content_ref: undefined, text: "Leading preview\n" + "context ".repeat(10000) + "late needle" };
  await act(async () => h.pending[0].resolve(full));
  expect(jump).toHaveBeenCalledExactlyOnceWith("old-turn", full, "late needle");
  h.remount();
  expect(jump).toHaveBeenCalledOnce();
});

it("resumes hydration in the new shell without accepting the disposed shell's response", async () => {
  const h = setup(); await h.select(); h.remount();
  expect(h.read).toHaveBeenCalledTimes(2);
  await act(async () => h.pending[0].resolve({ ...h.item, remote_content_ref: undefined, text: "obsolete late needle" }));
  expect(h.state.thread?.turns[0].items[0]).toEqual(h.item);
  expect(jump).not.toHaveBeenCalled();
  const full = { ...h.item, remote_content_ref: undefined, text: "current late needle" };
  await act(async () => h.pending[1].resolve(full));
  expect(jump).toHaveBeenCalledExactlyOnceWith("old-turn", full, "late needle");
});

it("cannot revive an older activation after a new search is selected in the replacement shell", async () => {
  const h = setup(undefined, { deferActivation: true });
  await h.select(); h.remount(); await h.select("new needle");
  await act(async () => h.activation[0]());
  expect(h.read).not.toHaveBeenCalled();
  await act(async () => h.activation[1]());
  const full = { ...h.item, remote_content_ref: undefined, text: "new needle" };
  await act(async () => h.pending[0].resolve(full));
  expect(jump).toHaveBeenCalledExactlyOnceWith("old-turn", full, "new needle");
});

it("does not revive a search after newer reader input rejected its late content", async () => {
  const h = setup(); await h.select(); h.readerInput();
  await act(async () => h.pending[0].resolve({ ...h.item, remote_content_ref: undefined, text: "late needle" }));
  h.remount();
  expect(h.read).toHaveBeenCalledOnce();
  expect(h.state.thread?.turns[0].items[0]).toEqual(h.item);
  expect(jump).not.toHaveBeenCalled();
});

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
