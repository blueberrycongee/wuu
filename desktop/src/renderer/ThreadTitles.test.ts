import { describe, expect, it } from "vitest";
import type { Thread } from "../shared/protocol";
import {
  conversationHeadingTitle,
  customDraftConversationTitle,
  threadDisplayTitle,
} from "./ThreadTitles";

function thread(overrides: Partial<Thread>): Thread {
  return {
    id: "thread",
    preview: "Build the app",
    model_provider: "test",
    model: "test",
    cwd: "/tmp/project",
    status: "idle",
    created_at: "2026-05-28T00:00:00.000Z",
    updated_at: "2026-05-28T00:00:00.000Z",
    turns: [],
    ...overrides
  };
}

describe("threadDisplayTitle", () => {
  it("uses the preview for regular threads", () => {
    expect(threadDisplayTitle(thread({ preview: "Fix tabs" }))).toBe("Fix tabs");
  });

  it("labels forked threads from their source preview", () => {
    const source = thread({ id: "source", preview: "Fix tabs" });
    const fork = thread({ id: "fork", preview: "Fix tabs", forked_from_id: "source" });

    expect(threadDisplayTitle(fork, [source, fork])).toBe("Fix tabs · 分叉");
  });

  it("falls back to the fork preview when the source is unavailable", () => {
    const fork = thread({ id: "fork", preview: "Fix tabs", forked_from_id: "source" });

    expect(threadDisplayTitle(fork, [fork])).toBe("Fix tabs · 分叉");
  });

  it("does not label a fork that has visible child forks as the fork endpoint", () => {
    const root = thread({ id: "root", preview: "Root session" });
    const middle = thread({ id: "middle", preview: "Middle session", forked_from_id: "root" });
    const leaf = thread({ id: "leaf", preview: "Leaf session", forked_from_id: "middle" });

    expect(threadDisplayTitle(middle, [root, middle, leaf])).toBe("Middle session");
    expect(threadDisplayTitle(leaf, [root, middle, leaf])).toBe("Middle session · 分叉");
  });
});

describe("conversation heading titles", () => {
  it("prefers a saved title over the generated preview", () => {
    expect(conversationHeadingTitle(
      thread({ title: "Release notes", preview: "Fix tabs" }),
      undefined,
      "新建对话",
    )).toBe("Release notes");
  });

  it("shows a renamed draft before a session exists", () => {
    expect(conversationHeadingTitle(undefined, "发布说明", "新建对话")).toBe("发布说明");
    expect(conversationHeadingTitle(undefined, "  ", "新建对话")).toBe("新建对话");
  });

  it("does not treat the new-conversation placeholder as a chosen title", () => {
    expect(customDraftConversationTitle("新建对话", "新建对话")).toBe("");
    expect(customDraftConversationTitle("  发布说明  ", "新建对话")).toBe("发布说明");
  });
});
