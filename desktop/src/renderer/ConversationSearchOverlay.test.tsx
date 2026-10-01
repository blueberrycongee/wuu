import { act, createElement, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it } from "vitest";
import type { Thread } from "../shared/protocol";
import { ConversationSearchOverlay } from "./ConversationSearchOverlay";
import type { ConversationSearchState } from "./ConversationSearchState";

let root: Root;
afterEach(() => act(() => root?.unmount()));

it("renders untrusted matching text literally and selects the addressed result", () => {
  const container = document.createElement("div");
  root = createRoot(container);
  const thread: Thread = {
    id: "match", title: "<img src=x> Fix layout", preview: "", turns: [],
    model_provider: "test", model: "test", cwd: "/workspace", status: "idle",
    created_at: "2026-09-07T00:00:00Z", updated_at: "2026-09-07T00:00:00Z",
  };
  const result = { thread, snippet: "Literal <img src=x> details [ui] and [UI]", message_seq: 7 };
  const state: ConversationSearchState = {
    open: true, closing: false, query: "[UI]", loading: false, error: "",
    results: [result], selectedIndex: 0,
  };
  let opened: unknown;
  act(() => root.render(createElement(ConversationSearchOverlay, {
    state, results: [result], threads: [thread], projects: [],
    dialogRef: createRef<HTMLDivElement>(), inputRef: createRef<HTMLInputElement>(),
    onClose: () => {}, onQueryChange: () => {}, onClearQuery: () => {},
    onKeyDown: () => {}, onSelectIndex: () => {},
    onSelectResult: (item) => { opened = item; },
  })));
  const button = container.querySelector<HTMLButtonElement>(".conversation-search-result")!;
  expect(Array.from(button.querySelectorAll("mark"), mark => mark.textContent)).toEqual(["[ui]", "[UI]"]);
  expect(button.querySelector("img")).toBeNull();
  expect(button.querySelector(".conversation-search-result-title")?.textContent).toBe(thread.title);
  expect(button.querySelector(".conversation-search-result-snippet")?.textContent).toContain("details [ui] and [UI]");
  act(() => button.click());
  expect(opened).toBe(result);
});

it("distinguishes otherwise identical title results without adding metadata to distinct rows", () => {
  const container = document.createElement("div");
  root = createRoot(container);
  const threads: Thread[] = [
    { id: "spring", title: "Release planning", updated_at: "2026-04-01T09:00:00Z", cwd: "/same" },
    { id: "autumn", title: "Release planning", updated_at: "2026-09-01T09:00:00Z", cwd: "/same" },
    { id: "other-project", title: "Release planning", updated_at: "2026-09-01T09:00:00Z", cwd: "/other" },
    { id: "unique", title: "Release checklist", updated_at: "2026-09-01T09:00:00Z", cwd: "/same" },
    { id: "fork-12345abcdef", title: "Follow-up", updated_at: "2026-09-01T09:00:05Z", cwd: "/same" },
    { id: "fork-67890abcdef", title: "Follow-up", updated_at: "2026-09-01T09:00:10Z", cwd: "/same" },
  ].map(thread => ({ ...thread, preview: "", turns: [], model_provider: "test", model: "test", status: "idle", created_at: thread.updated_at }));
  const results = threads.map(thread => ({ thread, snippet: thread.title }));
  act(() => root.render(createElement(ConversationSearchOverlay, {
    state: { open: true, closing: false, query: "Release", loading: false, error: "", results, selectedIndex: 0 },
    results, threads, projects: [
      { id: "same", name: "Same project", path: "/same", created_at: "", updated_at: "" },
      { id: "other", name: "Other project", path: "/other", created_at: "", updated_at: "" },
    ], dialogRef: createRef<HTMLDivElement>(), inputRef: createRef<HTMLInputElement>(),
    onClose: () => {}, onQueryChange: () => {}, onClearQuery: () => {},
    onKeyDown: () => {}, onSelectIndex: () => {}, onSelectResult: () => {},
  })));
  const rows = container.querySelectorAll(".conversation-search-result");
  expect(rows[0].querySelector("time")?.dateTime).toBe(threads[0].updated_at);
  expect(rows[1].querySelector("time")?.dateTime).toBe(threads[1].updated_at);
  expect(rows[0].querySelector("time")?.textContent).not.toBe(rows[1].querySelector("time")?.textContent);
  expect(rows[2].querySelector("time")).toBeNull();
  expect(rows[3].querySelector("time")).toBeNull();
  expect(rows[4].querySelector("time")?.textContent).toBe(rows[5].querySelector("time")?.textContent);
  expect(rows[4].querySelector(".conversation-search-result-snippet")?.textContent)
    .not.toBe(rows[5].querySelector(".conversation-search-result-snippet")?.textContent);
});
