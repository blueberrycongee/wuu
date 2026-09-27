import { act, createElement, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { useConversationScrollState } from "./ConversationScrollState";

let api: ReturnType<typeof useConversationScrollState> | null = null;

function Probe({ running }: { running: boolean }): ReactNode {
  api = useConversationScrollState({
    activeThreadID: "thread-1",
    activePane: "primary",
    splitConversation: false,
    emptyConversation: false,
    initialized: true,
    running,
  });
  return createElement("div", {
    ref: (node: HTMLDivElement | null) => {
      if (api) api.conversationScrollRef.current = node;
    },
  });
}

describe("stream following motion", () => {
  let container: HTMLDivElement;
  let root: Root | null = null;

  afterEach(() => {
    act(() => { root?.unmount(); });
    root = null;
    api = null;
    container.remove();
    document.documentElement.removeAttribute("data-stream-following");
  });

  function mount(running: boolean): void {
    container = document.createElement("div");
    document.body.appendChild(container);
    act(() => {
      root = createRoot(container);
      root.render(createElement(Probe, { running }));
    });
  }

  it("drops decorative stream motion only while a running turn is followed", () => {
    mount(false);
    expect(document.documentElement.hasAttribute("data-stream-following")).toBe(false);
    act(() => { root?.render(createElement(Probe, { running: true })); });
    expect(document.documentElement.hasAttribute("data-stream-following")).toBe(true);
    act(() => { api?.disableConversationAutoFollow(); });
    expect(document.documentElement.hasAttribute("data-stream-following")).toBe(false);
    act(() => { api?.enableConversationAutoFollow(); });
    expect(document.documentElement.hasAttribute("data-stream-following")).toBe(true);
  });
});
