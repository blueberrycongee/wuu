import { act, createElement, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { RuntimeContext, ThreadSearchResult, ThreadSearchResultItem } from "../shared/protocol";
import { initialState, type AppState } from "./AppState";
import { ApplicationSearch } from "./plugins/ApplicationSearch";
import { useConversationSearch } from "./ConversationSearchState";

let root: Root;
let container: HTMLDivElement;
afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  vi.useRealTimers();
  Reflect.deleteProperty(window, "wuu");
});

function result(id: string): ThreadSearchResultItem {
  return { snippet: id, thread: {
    id, title: id, preview: "", turns: [], model_provider: "test", model: "test",
    cwd: "/workspace", status: "idle", created_at: "", updated_at: "",
  } };
}

function setup(controlled?: { open: boolean }, renderInput = false) {
  vi.useFakeTimers();
  const pending: { resolve: (value: ThreadSearchResult) => void; reject: (error: Error) => void }[] = [];
  const searchThreads = vi.fn(() => new Promise<ThreadSearchResult>((resolve, reject) => {
    pending.push({ resolve, reject });
  }));
  Object.defineProperty(window, "wuu", { configurable: true, value: {
    searchThreads, getThreadPreview: vi.fn(async () => ({ turns: [] })),
  } });
  const activeContext: RuntimeContext = { kind: "no_project", cwd: "/workspace" };
  const onSelectThread = vi.fn();
  const onOpenChange = vi.fn();
  const cacheThreads = vi.fn();
  let state: AppState = { ...initialState, activeContext };
  const listeners = new Set<() => void>();
  const publish = () => { snapshot = { search: search.getSnapshot() }; listeners.forEach(listener => listener()); };
  const search = new ApplicationSearch({ getState: () => state,
    updateState: update => { state = typeof update === "function" ? update(state) : update; },
    subscribeState: () => () => {}, changed: publish, cacheThreads,
    activateThread: async id => { onSelectThread(id); }, getNavigation: () => ({ revision: 0, pending: false }) });
  let snapshot = { search: search.getSnapshot() };
  const application = { getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    invoke: search.invoke.bind(search) };
  let api: ReturnType<typeof useConversationSearch>;
  function Harness() {
    api = useConversationSearch({ application, open: controlled?.open, onOpenChange,
      onOpen: () => {}, onSelect: () => {} });
    return renderInput && (api.conversationSearch.open || api.conversationSearch.closing)
      ? createElement("input", { ref: api.conversationSearchInputRef, defaultValue: "previous query" }) : null;
  }
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  act(() => root.render(createElement(Harness)));
  if (!controlled) act(() => api.toggleConversationSearch());
  return { get api() { return api; }, pending, searchThreads, cacheThreads, onSelectThread, onOpenChange,
    setOpen: (open: boolean) => { if (controlled) controlled.open = open; act(() => root.render(createElement(Harness))); } };
}

async function advance(ms: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
}

it("debounces typing and discards responses arriving inside the next debounce window", async () => {
  const h = setup();
  await advance(0);
  act(() => h.api.setConversationSearchQuery("on"));
  await advance(80);
  act(() => h.api.setConversationSearchQuery("onboarding"));
  await act(async () => h.pending[0].resolve({ results: [result("old")] }));
  expect(h.api.conversationSearchResults).toEqual([]);
  expect(h.cacheThreads).not.toHaveBeenCalled();
  await advance(139);
  expect(h.searchThreads).toHaveBeenCalledTimes(1);
  await advance(1);
  expect(h.searchThreads).toHaveBeenLastCalledWith("onboarding", 100);
  const fresh = result("fresh");
  await act(async () => h.pending[1].resolve({ results: [fresh] }));
  act(() => {
    h.api.setConversationSearchQuery("other");
    h.api.selectConversationSearchResult(h.api.conversationSearchResults[0]);
  });
  expect(h.onSelectThread).not.toHaveBeenCalled();
  act(() => h.api.clearConversationSearchQuery());
  await advance(0);
  expect(h.searchThreads).toHaveBeenLastCalledWith("", 100);
});

it("waits for IME commit and does not interpret candidate confirmation as selection", async () => {
  const h = setup();
  await advance(0);
  await act(async () => h.pending[0].resolve({ results: [result("recent")] }));
  const preventDefault = vi.fn();
  act(() => h.api.handleConversationSearchKeyDown({ key: "Enter", nativeEvent: { isComposing: true },
    preventDefault } as unknown as ReactKeyboardEvent<HTMLInputElement>));
  expect(h.onSelectThread).not.toHaveBeenCalled();
  expect(preventDefault).not.toHaveBeenCalled();
  act(() => h.api.setConversationSearchQuery("zhong", true));
  await advance(1000);
  expect(h.searchThreads).toHaveBeenCalledTimes(1);
  act(() => h.api.setConversationSearchQuery("中文", false));
  await advance(140);
  expect(h.searchThreads).toHaveBeenLastCalledWith("中文", 100);
  const match = result("中文");
  await act(async () => h.pending[1].resolve({ results: [match] }));
  await act(async () => h.api.selectConversationSearchResult(h.api.conversationSearchResults[0]));
  expect(h.onSelectThread).toHaveBeenCalledWith("中文");
});

it("ignores stale errors and responses after closing", async () => {
  const h = setup();
  await advance(0);
  act(() => h.api.setConversationSearchQuery("new"));
  await act(async () => h.pending[0].reject(new Error("old failure")));
  expect(h.api.conversationSearch.error).toBe("");
  await advance(140);
  act(() => h.api.closeConversationSearch({ immediate: true }));
  await act(async () => h.pending[1].resolve({ results: [result("late")] }));
  expect(h.cacheThreads).not.toHaveBeenCalled();
  expect(h.api.conversationSearchResults).toEqual([]);
});

it("returns focus to where it was when dismissed, but not when a result is chosen", async () => {
  const h = setup();
  await advance(0);
  const match = result("recent");
  await act(async () => h.pending[0].resolve({ results: [match] }));
  // The palette opened with focus in the composer; reopen it from there.
  act(() => h.api.closeConversationSearch({ immediate: true }));
  const composer = document.createElement("textarea");
  const input = document.createElement("input");
  document.body.append(composer, input);
  const openFrom = async (): Promise<void> => {
    composer.focus();
    act(() => h.api.toggleConversationSearch());
    input.focus();
    await advance(0);
  };
  const key = (name: string) => ({ key: name, nativeEvent: { isComposing: false },
    preventDefault: () => {} }) as unknown as ReactKeyboardEvent<HTMLInputElement>;

  await openFrom();
  act(() => h.api.handleConversationSearchKeyDown(key("Escape")));
  expect(document.activeElement).toBe(composer);

  await openFrom();
  await act(async () => h.pending.at(-1)!.resolve({ results: [match] }));
  await act(async () => h.api.selectConversationSearchResult(h.api.conversationSearchResults[0]));
  expect(h.onSelectThread).toHaveBeenCalledWith("recent");
  // The chosen conversation takes focus next; the composer it left does not.
  expect(document.activeElement).not.toBe(composer);
  composer.remove();
  input.remove();
});

it("follows the selected navigation visibility and publishes keyboard dismissal", () => {
  const h = setup({ open: false });
  expect(h.api.conversationSearch.open).toBe(false);
  h.setOpen(true);
  expect(h.api.conversationSearch.open).toBe(true);
  act(() => h.api.closeConversationSearch({ immediate: true }));
  expect(h.onOpenChange).toHaveBeenCalledWith(false);
  h.setOpen(false);
  expect(h.api.conversationSearch.open).toBe(false);
});

it("focuses the committed search input even when a frame ran before React committed the opening", () => {
  const h = setup({ open: false }, true);
  const composer = document.createElement("textarea");
  document.body.append(composer);
  composer.focus();
  act(() => {
    h.api.toggleConversationSearch();
    // Native events can yield a frame before React commits the dialog. Focus
    // must follow its input commit, not assume the next frame contains it.
    vi.advanceTimersByTime(16);
  });
  const input = container.querySelector("input")!;
  expect(document.activeElement).toBe(input);
  expect(input.selectionStart).toBe(0);
  expect(input.selectionEnd).toBe(input.value.length);
  composer.remove();
});

it("does not replace a newer focus owner while the search opening is committing", () => {
  const h = setup({ open: false }, true);
  const composer = document.createElement("textarea"), newer = document.createElement("button");
  document.body.append(composer, newer);
  composer.focus();
  act(() => {
    h.api.toggleConversationSearch();
    newer.focus();
  });
  act(() => vi.advanceTimersByTime(16));
  expect(document.activeElement).toBe(newer);
  composer.remove(); newer.remove();
});

it("does not return focus to a closing search from an earlier opening frame", () => {
  const h = setup({ open: false }, true);
  const composer = document.createElement("textarea");
  document.body.append(composer);
  composer.focus();
  act(() => h.api.toggleConversationSearch());
  act(() => h.api.closeConversationSearch());
  expect(document.activeElement).toBe(composer);
  act(() => vi.advanceTimersByTime(16));
  expect(document.activeElement).toBe(composer);
  composer.remove();
});

it("does not focus a pending search after a newer non-focusable pointer interaction", () => {
  const h = setup({ open: false }, true);
  const composer = document.createElement("textarea");
  document.body.append(composer);
  composer.focus();
  act(() => {
    h.api.toggleConversationSearch();
    document.body.dispatchEvent(new Event("pointerdown", { bubbles: true }));
  });
  act(() => vi.advanceTimersByTime(16));
  expect(document.activeElement).toBe(composer);
  composer.remove();
});
