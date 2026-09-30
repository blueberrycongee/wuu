import { act, createElement, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { RuntimeContext, ThreadSearchResult, ThreadSearchResultItem } from "../shared/protocol";
import type { AppState } from "./AppState";
import { useConversationSearch } from "./ConversationSearchState";

let root: Root;
afterEach(() => {
  act(() => root?.unmount());
  vi.useRealTimers();
  Reflect.deleteProperty(window, "wuu");
});

function result(id: string): ThreadSearchResultItem {
  return { snippet: id, thread: {
    id, title: id, preview: "", turns: [], model_provider: "test", model: "test",
    cwd: "/workspace", status: "idle", created_at: "", updated_at: "",
  } };
}

function setup() {
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
  const cacheThreads = vi.fn();
  let api: ReturnType<typeof useConversationSearch>;
  function Harness() {
    api = useConversationSearch({ activeContext,
      getAppState: () => ({ activeContext } as AppState),
      cacheThreads, onOpen: () => {}, onSelectThread });
    return null;
  }
  root = createRoot(document.createElement("div"));
  act(() => root.render(createElement(Harness)));
  act(() => api.toggleConversationSearch());
  return { get api() { return api; }, pending, searchThreads, cacheThreads, onSelectThread };
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
    h.api.selectConversationSearchResult(fresh);
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
  act(() => h.api.selectConversationSearchResult(match));
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
  act(() => h.api.selectConversationSearchResult(h.api.conversationSearchResults[0]));
  expect(h.onSelectThread).toHaveBeenCalledWith("recent");
  // The chosen conversation takes focus next; the composer it left does not.
  expect(document.activeElement).not.toBe(composer);
  composer.remove();
  input.remove();
});
