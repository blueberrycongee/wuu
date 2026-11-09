import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thread, ThreadItem, ThreadSearchResult } from "../../shared/protocol";
import { initialState, reduceServerEvent, type AppState } from "../AppState";
import { ApplicationSearch } from "./ApplicationSearch";
const original = window.wuu;
afterEach(() => { vi.useRealTimers(); Object.defineProperty(window, "wuu", { configurable: true, value: original }); });
function deferred<T>() { let resolve!: (value: T) => void, reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
function thread(items: ThreadItem[] = [{ id: "answer", seq: 7, type: "agent_message", text: "late needle" }]): Thread {
  return { id: "history", title: "History", preview: "", cwd: "/workspace", model_provider: "wuu", model: "model", status: "idle", created_at: "", updated_at: "", turns: [{ id: "turn", status: "completed", items_view: "full", items }], history_cursor: "page-1" };
}
function fixture(initial = thread()) {
  let state: AppState = { ...initialState, activeContext: { kind: "no_project", cwd: "/workspace" }, thread: initial, threads: [initial] };
  let navigation = { revision: 0, pending: false };
  const listeners = new Set<() => void>();
  const changed = vi.fn();
  const emit = () => { for (const listener of [...listeners]) listener(); };
  const install = (next: AppState) => { state = next; emit(); };
  const api = { searchThreads: vi.fn().mockResolvedValue({ results: [{ thread: initial, message_seq: 7, snippet: "late needle" }] }),
    loadEarlierThreadHistory: vi.fn().mockResolvedValue(undefined), readRemoteItem: vi.fn().mockResolvedValue(initial.turns[0].items[0]) };
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
  const activate = vi.fn(async (id: string, _signal: AbortSignal) => { navigation = { revision: navigation.revision + 1, pending: false }; install({ ...state, thread: state.threads.find(row => row.id === id) }); });
  const cache = vi.fn();
  const search = new ApplicationSearch({ getState: () => state, updateState: update => install(typeof update === "function" ? update(state) : update), subscribeState: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, changed, cacheThreads: cache, activateThread: activate, getNavigation: () => navigation });
  const stop = search.start();
  return { search, stop, api, activate, cache, changed, get: () => state, install, emit,
    navigate: (next = state) => { navigation = { revision: navigation.revision + 1, pending: false }; install(next); },
    pending: (value: boolean) => { navigation = { ...navigation, pending: value }; emit(); },
    page: (cursor: string, history_cursor: string | undefined, items: ThreadItem[]) => install(reduceServerEvent(state, { kind: "notification", workdir: "/workspace", message: { method: "thread/historyLoaded", params: { thread_id: "history", cursor, history_cursor, turns: [{ id: "turn", status: "completed", items_view: "full", items }] } } })),
    async select() { await search.invoke("search.run", { query: "late needle" }); const resultId = search.getSnapshot().results[0].resultId; return search.invoke("search.locate", { resultId }); },
  };
}
async function flush() { for (let i = 0; i < 8; i++) await Promise.resolve(); }

describe("application search workflow", () => {
  it("fences stale responses before debounce and keeps summary caching on the shared owner", async () => {
    vi.useFakeTimers(); const h = fixture(), first = deferred<ThreadSearchResult>(); h.api.searchThreads.mockReturnValueOnce(first.promise);
    const pending = h.search.invoke("search.run", { query: "old" });
    await h.search.invoke("search.query", { query: "new" }); first.resolve({ results: [{ thread: thread(), message_seq: 7 }] }); await pending;
    expect(h.cache).not.toHaveBeenCalled(); expect(h.search.getSnapshot().results).toEqual([]);
    await vi.advanceTimersByTimeAsync(140); expect(h.api.searchThreads).toHaveBeenLastCalledWith("new", 100); expect(h.cache).toHaveBeenCalledOnce();
    const view = h.search.getSnapshot(); expect(Object.isFrozen(view.results[0].thread)).toBe(true);
    await h.search.invoke("search.query", { query: "newer", composing: true }); await vi.advanceTimersByTimeAsync(1000);
    await expect(h.search.invoke("search.locate", { resultId: view.results[0].resultId })).rejects.toThrow("no longer current"); expect(h.api.searchThreads).toHaveBeenCalledTimes(2); h.stop();
  });
  it("rejects context-switched responses and disposed work", async () => {
    const h = fixture(), first = deferred<ThreadSearchResult>(); h.api.searchThreads.mockReturnValue(first.promise);
    const pending = h.search.invoke("search.run", { query: "needle" }); h.install({ ...h.get(), activeContext: { kind: "no_project", cwd: "/elsewhere" } }); first.resolve({ results: [{ thread: thread() }] }); await pending;
    expect(h.cache).not.toHaveBeenCalled(); expect(h.search.getSnapshot().loading).toBe(false);
    const second = deferred<ThreadSearchResult>(); h.api.searchThreads.mockReturnValue(second.promise); const disposed = h.search.invoke("search.run", {}); h.stop(); second.resolve({ results: [{ thread: thread() }] }); await disposed; expect(h.cache).not.toHaveBeenCalled();
  });
  it("pages past same-sequence tools, hydrates the addressed body and publishes only its real item address", async () => {
    const h = fixture(thread([{ id: "tool", seq: 7, type: "tool_call", name: "read_file" }]));
    const content = deferred<ThreadItem>(); h.api.readRemoteItem.mockReturnValue(content.promise);
    const { targetId } = await h.select(); expect(h.api.loadEarlierThreadHistory).toHaveBeenCalledExactlyOnceWith("history", "page-1");
    h.emit(); await h.search.invoke("search.page", { targetId: targetId! }); expect(h.api.loadEarlierThreadHistory).toHaveBeenCalledOnce();
    h.page("page-1", "page-2", [{ id: "tool-2", seq: 7, type: "tool_call", name: "read_file" }]); await flush();
    expect(h.api.loadEarlierThreadHistory).toHaveBeenCalledTimes(2);
    h.page("page-2", undefined, [{ id: "answer", seq: 7, type: "agent_message", text: "Preview", remote_content_ref: "content:answer" }]); await flush();
    expect(h.api.readRemoteItem).toHaveBeenCalledExactlyOnceWith("content:answer"); expect(h.search.getSnapshot().target?.phase).toBe("hydrating");
    content.resolve({ id: "answer", seq: 7, type: "agent_message", text: "A complete late needle body" }); await flush();
    expect(h.search.getSnapshot().target).toMatchObject({ id: targetId, phase: "ready", turnId: "turn", itemId: "answer", messageSeq: 7 });
    expect(h.get().thread?.turns[0].items.find(row => row.id === "answer")?.text).toContain("late needle");
    await h.search.invoke("search.complete-target", { targetId: targetId! }); expect(h.activate.mock.calls[0][1].aborted).toBe(false); expect(h.search.getSnapshot().target).toBeUndefined(); expect(h.get().conversationSearchTarget).toBeUndefined(); h.stop();
  });
  it("waits for shared navigation content readiness before publishing a text address", async () => {
    const h = fixture(); h.activate.mockImplementationOnce(async () => { h.pending(true); });
    await h.select(); expect(h.search.getSnapshot().target?.phase).toBe("activating");
    h.pending(false); expect(h.search.getSnapshot().target).toMatchObject({ phase: "ready", itemId: "answer" }); h.stop();
  });
  it("invalidates content after navigation away and back even when the destination ID matches again", async () => {
    const preview: ThreadItem = { id: "answer", seq: 7, type: "agent_message", text: "Preview", remote_content_ref: "content:answer" };
    const h = fixture(thread([preview])), content = deferred<ThreadItem>(); h.api.readRemoteItem.mockReturnValue(content.promise);
    await h.select(); h.navigate({ ...h.get(), thread: { ...thread(), id: "other" } }); h.navigate({ ...h.get(), thread: thread([preview]) });
    content.resolve({ ...preview, remote_content_ref: undefined, text: "late needle" }); await flush();
    expect(h.get().thread?.turns[0].items[0].text).toBe("Preview"); expect(h.search.getSnapshot().target).toBeUndefined(); h.stop();
  });
  it("cancels pending activation and does not let an older completion erase a newer target", async () => {
    const h = fixture(), activated = deferred<void>(); let firstSignal!: AbortSignal;
    h.activate.mockImplementationOnce(async (_id, signal) => { firstSignal = signal; await activated.promise; });
    await h.search.invoke("search.run", { query: "late needle" }); const resultId = h.search.getSnapshot().results[0].resultId;
    const first = h.search.invoke("search.locate", { resultId }); const firstId = h.search.getSnapshot().target!.id;
    const second = await h.search.invoke("search.locate", { resultId }); expect(firstSignal.aborted).toBe(true);
    activated.resolve(); await first;
    await h.search.invoke("search.complete-target", { targetId: firstId }); expect(h.search.getSnapshot().target?.id).toBe(second.targetId); h.stop();
  });
  it("keeps malformed remote content out of shared state and retries explicitly", async () => {
    const preview: ThreadItem = { id: "answer", seq: 7, type: "agent_message", text: "Preview", remote_content_ref: "content:answer" };
    const h = fixture(thread([preview])); h.api.readRemoteItem.mockResolvedValueOnce({ id: "different", type: "agent_message", text: "late needle" });
    const { targetId } = await h.select(); await flush(); expect(h.search.getSnapshot().target?.phase).toBe("failed"); expect(h.get().thread?.turns[0].items[0]).toEqual(preview);
    h.api.readRemoteItem.mockResolvedValue({ ...preview, remote_content_ref: undefined, text: "late needle" });
    await h.search.invoke("search.page", { targetId: targetId! }); await flush(); expect(h.search.getSnapshot().target?.phase).toBe("ready"); h.stop();
  });
  it("does not claim metadata-only hits as a text match or page past the address", async () => {
    const h = fixture(thread([{ id: "answer", seq: 7, type: "agent_message", text: "Different body" }])); await h.select(); expect(h.search.getSnapshot().target?.phase).toBe("not-found"); expect(h.api.loadEarlierThreadHistory).not.toHaveBeenCalled(); h.stop();
    const before = fixture(thread([{ id: "earlier", seq: 6, type: "user_message", text: "earlier" }])); await before.select(); expect(before.search.getSnapshot().target?.phase).toBe("not-found"); expect(before.api.loadEarlierThreadHistory).not.toHaveBeenCalled(); before.stop();
  });
  it("preserves explicit paging failures and retries without replaying a completed cursor", async () => {
    const h = fixture(thread([{ id: "tool", seq: 7, type: "tool_call", name: "read_file" }])); h.api.loadEarlierThreadHistory.mockRejectedValueOnce(new Error("offline"));
    const { targetId } = await h.select(); await flush(); expect(h.search.getSnapshot().target?.error).toBe("offline");
    await h.search.invoke("search.page", { targetId: targetId! }); await flush(); expect(h.api.loadEarlierThreadHistory).toHaveBeenCalledTimes(2); h.emit(); expect(h.api.loadEarlierThreadHistory).toHaveBeenCalledTimes(2); h.stop();
  });
  it("activates title-only results without inventing a message jump", async () => {
    const h = fixture(); h.api.searchThreads.mockResolvedValue({ results: [{ thread: thread() }] });
    await expect(h.select()).resolves.toEqual({}); expect(h.activate).toHaveBeenCalledOnce(); expect(h.search.getSnapshot().target).toBeUndefined(); h.stop();
  });
});
