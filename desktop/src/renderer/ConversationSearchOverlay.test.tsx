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
