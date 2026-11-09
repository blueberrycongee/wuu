import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { ApplicationStreamingMarkdown } from "./StreamingMarkdown";
import { ConversationControlsTestProvider, createConversationTestApplication } from "./ConversationControlsTestProvider";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";

it("uses the explicit public text address, pauses hidden painting and accepts replacement", () => {
  const preview = createConversationTestApplication();
  let value = "first", changed: (() => void) | undefined;
  const unsubscribe = vi.fn();
  const getSnapshot = vi.fn(() => value);
  const subscribe = vi.fn((_target, listener) => { changed = listener; return unsubscribe; });
  const application = { ...preview,
    getSnapshot: () => ({ ...preview.getSnapshot(), conversationWorkspace: { ...preview.getSnapshot().conversationWorkspace, activePane: "primary" as const, threads: [{ id: "source", title: "Source", preview: "", cwd: "/repo", model_provider: "test", model: "test", status: "idle" as const, created_at: "", updated_at: "", turns: [{ id: "turn", status: "in_progress" as const, items_view: "full" as const, items: [{ id: "answer", type: "agent_message" as const }] }] }] } }),
    conversationText: { getSnapshot, subscribe },
  };
  const container = document.createElement("div"), root = createRoot(container);
  const paint = (active: boolean) => act(() => root.render(<ConversationControlsTestProvider service={application}><ConversationRenderActivityProvider active={active}><ApplicationStreamingMarkdown threadId="source" turnId="turn" itemId="answer" initialText="stale" isLive phase="final_answer" /></ConversationRenderActivityProvider></ConversationControlsTestProvider>));
  try {
    paint(true); expect(container.textContent).toContain("first");
    expect(subscribe).toHaveBeenCalledWith({ threadId: "source", turnId: "turn", itemId: "answer", field: "text" }, expect.any(Function));
    paint(false); expect(unsubscribe).toHaveBeenCalled();
    value = "first appended"; act(() => changed?.()); expect(container.textContent).not.toContain("appended");
    paint(true); expect(container.textContent).toContain("appended");
    value = "short"; act(() => changed?.()); expect(container.textContent).toContain("short"); expect(container.textContent).not.toContain("appended");
  } finally { act(() => root.unmount()); }
});
