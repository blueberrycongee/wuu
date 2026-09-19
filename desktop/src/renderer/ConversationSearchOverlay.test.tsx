/**
 * Tests for `PreviewTurnGroup` — the row renderer used by
 * `ConversationSearchOverlay` on the right pane of cmd+P search.
 *
 * Each `Turn` in this codebase bundles the user's opening message with
 * every `agent_message` / `reasoning` / `tool_call` that followed it
 * (see `turnsFromPersistedHistoryInScope` in `internal/appserver/model.go`).
 * The preview used to render the whole turn as one row with a single role,
 * which hid either the user query or the assistant's reply depending on
 * who spoke last. The renderer now emits up to two rows per turn (one
 * per non-empty `user_message` / `agent_message`) so the visible timeline
 * matches the actual conversation. Role distinction lives in CSS — the
 * user row is right-aligned with a chat-bubble background and the
 * assistant row reads flush left. Both rows reuse RichContent so the search
 * preview has the same Markdown semantics as the live conversation.
 *
 * Real React via `react-dom/client` + `act`, no `@testing-library/react`
 * dependency — matches the TurnSourcesRow / AssistantTurnShell pattern.
 */
import { act, createElement, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import type { Thread, ThreadItem, Turn } from "../shared/protocol";
import {
  ConversationSearchOverlay,
  PreviewTurnGroup,
} from "./ConversationSearchOverlay";
import type { ConversationSearchState } from "./ConversationSearchState";

let mountedRoots: Root[] = [];

afterEach(() => {
  for (const root of mountedRoots) {
    act(() => root.unmount());
  }
  mountedRoots = [];
});

function userItem(text: string): ThreadItem {
  return { id: `u-${text}`, type: "user_message", text };
}

function agentItem(
  text: string,
  terminal?: boolean,
): ThreadItem {
  return { id: `a-${text}`, type: "agent_message", text, terminal };
}

function toolItem(): ThreadItem {
  return { id: "t-1", type: "tool_call" };
}

function turnWith(items: ThreadItem[], id = "turn-1"): Turn {
  return {
    id,
    items,
    items_view: "full",
    status: "completed",
  };
}

function renderTurn(turn: Turn): HTMLDivElement {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(createElement(PreviewTurnGroup, { turn }));
  });
  mountedRoots.push(root);
  return container;
}

function rows(container: HTMLDivElement): HTMLElement[] {
  return Array.from(
    container.querySelectorAll<HTMLElement>(
      ".conversation-search-preview-turn",
    ),
  );
}

describe("ConversationSearchOverlay", () => {
  it.each(["[UI]", "details"])("keeps results title-only and shows matching details in the preview for %s", (query) => {
    const container = document.createElement("div");
    const root = createRoot(container);
    mountedRoots.push(root);
    const thread: Thread = {
      id: "match", title: "<img src=x> Fix [UI] layout", preview: "", turns: [],
      model_provider: "test", model: "test", cwd: "/workspace", status: "idle",
      created_at: "2026-09-07T00:00:00Z", updated_at: "2026-09-07T00:00:00Z",
    };
    const result = { thread, snippet: "Only details [ui] and [UI]" };
    const state: ConversationSearchState = {
      open: true, closing: false, query, loading: false, error: "",
      results: [result], selectedIndex: 0, previewedThreadID: "",
      previewedTurns: [], previewLoading: false, previewError: "",
    };
    let opened = "";
    act(() => root.render(createElement(ConversationSearchOverlay, {
      state, results: [result], threads: [thread], projects: [],
      dialogRef: createRef<HTMLDivElement>(), inputRef: createRef<HTMLInputElement>(),
      onClose: () => {}, onQueryChange: () => {}, onClearQuery: () => {},
      onKeyDown: () => {}, onSelectIndex: () => {},
      onSelectResult: (item) => { opened = item.thread.id; },
    })));
    const button = container.querySelector<HTMLButtonElement>(".conversation-search-result")!;
    expect(Array.from(button.querySelectorAll("mark"), (mark) => mark.textContent)).toEqual(query === "[UI]" ? ["[UI]"] : []);
    expect(button.querySelector("img")).toBeNull();
    expect(button.querySelector(".conversation-search-result-title")?.textContent).toBe(thread.title);
    expect(button.textContent).not.toContain(result.snippet);
    expect(container.querySelector("aside")?.textContent).toContain(result.snippet);
    act(() => button.click());
    expect(opened).toBe(thread.id);
  });

  it("places results directly below the search field without a status row", () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    const state: ConversationSearchState = {
      open: true,
      closing: false,
      query: "",
      loading: false,
      error: "",
      results: [],
      selectedIndex: 0,
      previewedThreadID: "",
      previewedTurns: [],
      previewLoading: false,
      previewError: "",
    };

    act(() => {
      root.render(
        createElement(ConversationSearchOverlay, {
          state,
          results: [],
          threads: [],
          projects: [],
          dialogRef: createRef<HTMLDivElement>(),
          inputRef: createRef<HTMLInputElement>(),
          onClose: () => {},
          onQueryChange: () => {},
          onClearQuery: () => {},
          onKeyDown: () => {},
          onSelectIndex: () => {},
          onSelectResult: () => {},
        }),
      );
    });
    mountedRoots.push(root);

    expect(container.querySelector(".conversation-search-overlay")?.classList).toContain(
      "app-modal-backdrop",
    );
    expect(container.querySelector(".conversation-search-status")).toBeNull();
    expect(
      container.querySelector(".conversation-search-input-wrap")?.nextElementSibling,
    ).toBe(container.querySelector(".conversation-search-body"));
    expect(container.textContent).not.toContain("刷新");
  });
});

describe("PreviewTurnGroup", () => {
  it("renders only the user row when the agent has not replied yet", () => {
    const container = renderTurn(turnWith([userItem("帮我看看这个 bug")]));
    const rendered = rows(container);
    expect(rendered).toHaveLength(1);
    expect(rendered[0].dataset.role).toBe("user");
    expect(rendered[0].textContent).toContain("帮我看看这个 bug");
  });

  it("renders only the assistant row when the turn has no user_message", () => {
    const container = renderTurn(
      turnWith([agentItem("好的，让我看看。", true)]),
    );
    const rendered = rows(container);
    expect(rendered).toHaveLength(1);
    expect(rendered[0].dataset.role).toBe("assistant");
  });

  it("renders the user row before the assistant row when the turn has both", () => {
    const container = renderTurn(
      turnWith([
        userItem("Q1 — 为什么 cmd+P 右边看不见我的 query？"),
        agentItem("A1 — 因为之前每条 turn 合并成一行", true),
      ]),
    );
    const rendered = rows(container);
    expect(rendered.map((el) => el.dataset.role)).toEqual([
      "user",
      "assistant",
    ]);
    expect(rendered[0].textContent).toContain("Q1");
    expect(rendered[1].textContent).toContain("A1");
    expect(
      container.querySelector(".conversation-search-preview-turn-group"),
    ).not.toBeNull();
  });

  it("renders Markdown in both sides of the conversation preview", () => {
    const container = renderTurn(
      turnWith([
        userItem("请检查 `src/App.tsx`"),
        agentItem(
          "# 修复结果\n\n这里有 **重点**：\n\n- 第一项\n- 第二项",
          true,
        ),
      ]),
    );
    const rendered = rows(container);
    const user = rendered.find((element) => element.dataset.role === "user");
    const assistant = rendered.find(
      (element) => element.dataset.role === "assistant",
    );

    expect(user?.querySelector(".rich-content code")?.textContent).toBe(
      "src/App.tsx",
    );
    expect(
      assistant?.querySelector(".rich-heading.rich-heading--h1")?.textContent,
    ).toBe("修复结果");
    expect(assistant?.querySelector("strong")?.textContent).toBe("重点");
    expect(
      Array.from(assistant?.querySelectorAll("li") ?? []).map(
        (element) => element.textContent,
      ),
    ).toEqual(["第一项", "第二项"]);
  });

  it("prefers the final_answer agent_message for the assistant row", () => {
    const container = renderTurn(
      turnWith([
        userItem("Q2"),
        agentItem("commentary 先说两句", false),
        agentItem("final 答案", true),
      ]),
    );
    const rendered = rows(container);
    const assistant = rendered.find((el) => el.dataset.role === "assistant");
    expect(assistant?.textContent).toContain("final 答案");
    expect(assistant?.textContent).not.toContain("commentary 先说两句");
  });

  it("falls back to the last agent_message when no final_answer is tagged", () => {
    const container = renderTurn(
      turnWith([
        userItem("Q3"),
        agentItem("先说一句"),
        agentItem("最后一句"),
      ]),
    );
    const rendered = rows(container);
    const assistant = rendered.find((el) => el.dataset.role === "assistant");
    expect(assistant?.textContent).toContain("最后一句");
  });

  it("renders nothing for turns that only carry tool_calls", () => {
    const container = renderTurn(turnWith([toolItem()]));
    expect(rows(container)).toHaveLength(0);
  });
});