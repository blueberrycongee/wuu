import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import { streamTextKey, streamTextStore } from "./StreamText";
import { ThreadItemView } from "./ThreadItemView";
import { clearToasts, ToastViewport } from "./Toast";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { setOpenThreadInSplitHandler } from "./ConversationSplitBridge";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement | undefined;
let root: Root | undefined;

function makeFinalAnswer(status: ThreadItem["status"]): ThreadItem {
  return {
    id: "final-1",
    type: "agent_message",
    status,
    terminal: true,
    text: "Final answer text.",
  };
}

function makeUserMessage(text: string, id = "user-1"): ThreadItem {
  return {
    id,
    type: "user_message",
    status: "completed",
    text,
  };
}

function render({
  item,
  turnStatus,
  turnStartedAt,
  actionableAgentMessageID,
  latestAgentMessageID,
  streaming,
  onEditMessage,
  onForkMessage,
}: {
  item: ThreadItem;
  turnStatus: Turn["status"];
  turnStartedAt?: string;
  actionableAgentMessageID?: string;
  latestAgentMessageID?: string;
  streaming: boolean;
  onEditMessage?: (turnID: string, item: ThreadItem) => void;
  onForkMessage?: (turnID: string, itemID: string) => void;
}): void {
  if (!container) {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  }
  act(() => {
    root!.render(
      <WuuUIRoot>
        <ThreadItemView
          turnID="turn-1"
          turnStatus={turnStatus}
          turnStartedAt={turnStartedAt}
          item={item}
          streaming={streaming}
          actionableAgentMessageID={actionableAgentMessageID}
          latestAgentMessageID={latestAgentMessageID}
          onStreamFrame={() => {}}
          onEditMessage={onEditMessage}
          onForkMessage={onForkMessage}
        />
        <ToastViewport />
      </WuuUIRoot>,
    );
  });
}

function actionBar(): HTMLElement {
  const node = container?.querySelector<HTMLElement>(".agent-message-actions");
  if (!node) {
    throw new Error("expected agent action bar");
  }
  return node;
}

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  container?.remove();
  streamTextStore.clearItem("turn-1", "final-1");
  clearToasts();
  desktopPluginHost.unload("thread-item-production-test");
  desktopPluginHost.setActiveConversationThread(undefined);
  vi.restoreAllMocks();
  root = undefined;
  container = undefined;
});

describe("ThreadItemView", () => {
  it("mounts sanitized frozen context before and after each conversation message", async () => {
    const contexts: Array<Readonly<Record<string, unknown>>> = [];
    await desktopPluginHost.activateGeneration({
      pluginId: "thread-item-production-test",
      generation: "one",
      register(api) {
        for (const slotId of ["conversation.message.before", "conversation.message.after"] as const) {
          api.registerSlot(slotId, {
            id: slotId,
            render(context) {
              contexts.push(context);
              return <span data-testid={slotId}>{slotId}</span>;
            },
          });
        }
      },
    });

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      streaming: false,
    });

    expect(container?.querySelector('[data-testid="conversation.message.before"]')).not.toBeNull();
    expect(container?.querySelector('[data-testid="conversation.message.after"]')).not.toBeNull();
    expect(contexts).toHaveLength(2);
    expect(contexts[0]).toEqual({
      kind: "agent_message",
      turnStatus: "completed",
      streaming: false,
      editing: false,
      locale: "zh-CN",
      translate: expect.any(Function),
    });
    expect(Object.isFrozen(contexts[0])).toBe(true);
    expect(contexts[0]).not.toHaveProperty("item");
    expect(contexts[0]).not.toHaveProperty("text");
  });

  it("uses a conversation item presenter for the complete production item root", async () => {
    await desktopPluginHost.activateGeneration({
      pluginId: "thread-item-production-test",
      generation: "gen-1",
      register: (api) => {
        api.registerPresenter({
          id: "assistant-root",
          target: "conversation.item",
          key: "assistant-message",
          render: ({ snapshot }) => (
            <section data-production-presenter>
              {(snapshot as { text?: string }).text}
            </section>
          ),
        });
      },
    });
    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      streaming: false,
    });

    expect(container?.querySelector("[data-production-presenter]")?.textContent).toBe("Final answer text.");
    expect(container?.querySelector(".agent-block")).toBeNull();
  });

  it("uses the legacy message surface as the exact native fallback with a sanitized context", async () => {
    let received: Readonly<Record<string, unknown>> | undefined;
    await desktopPluginHost.activateGeneration({
      pluginId: "thread-item-production-test",
      generation: "gen-1",
      register: (api) => {
        api.registerSurface("conversation.message", {
          id: "legacy-message",
          mode: "replace",
          render: (context) => {
            received = context;
            return <section data-legacy-message>{String(context.kind)}</section>;
          },
        });
      },
    });

    const item = makeFinalAnswer("completed");
    (item as ThreadItem & { privateValue: string }).privateValue = "must-not-leak";
    render({ item, turnStatus: "completed", streaming: false });

    expect(container?.querySelector("[data-legacy-message]")?.textContent).toBe("assistant-message");
    expect(container?.querySelector(".agent-block")).toBeNull();
    expect(received).toEqual({
      version: 1,
      messageId: "final-1",
      turnId: "turn-1",
      kind: "assistant-message",
      status: "completed",
      phase: "final_answer",
      streaming: false,
      attachmentCount: 0,
      actions: { edit: undefined, fork: undefined },
    });
    expect(received).not.toHaveProperty("item");
    expect(received).not.toHaveProperty("text");
    expect(received).not.toHaveProperty("privateValue");

    act(() => desktopPluginHost.unload("thread-item-production-test"));
    expect(container?.querySelector(".agent-block")).not.toBeNull();
  });

  it("exposes the active thread id on the conversation message surface", async () => {
    let received: Readonly<Record<string, unknown>> | undefined;
    await desktopPluginHost.activateGeneration({
      pluginId: "thread-item-production-test",
      generation: "gen-thread-id",
      register: (api) => {
        api.registerSurface("conversation.message", {
          id: "thread-aware-message",
          mode: "replace",
          render: (context) => {
            received = context;
            return <section data-thread-aware>{String(context.threadId)}</section>;
          },
        });
      },
    });

    desktopPluginHost.setActiveConversationThread("thread-aware-1");
    render({ item: makeFinalAnswer("completed"), turnStatus: "completed", streaming: false });

    expect(container?.querySelector("[data-thread-aware]")?.textContent).toBe("thread-aware-1");
    expect(received).toHaveProperty("threadId", "thread-aware-1");
    desktopPluginHost.setActiveConversationThread(undefined);
  });

  it("lets the keyed presenter replace the legacy surface and native fallback coherently", async () => {
    const legacyRender = vi.fn(() => <section data-legacy-message />);
    await desktopPluginHost.activateGeneration({
      pluginId: "thread-item-production-test",
      generation: "gen-1",
      register: (api) => {
        api.registerSurface("conversation.message", {
          id: "legacy-message",
          mode: "replace",
          render: legacyRender,
        });
        api.registerPresenter({
          id: "assistant-root",
          target: "conversation.item",
          key: "assistant-message",
          render: () => <section data-keyed-presenter />,
        });
      },
    });

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      streaming: false,
    });

    expect(container?.querySelector("[data-keyed-presenter]")).not.toBeNull();
    expect(container?.querySelector("[data-legacy-message]")).toBeNull();
    expect(container?.querySelector(".agent-block")).toBeNull();
    expect(legacyRender).not.toHaveBeenCalled();
  });

  it("shows short user messages in full without a collapse control", () => {
    render({
      item: makeUserMessage("Short query."),
      turnStatus: "completed",
      streaming: false,
    });

    expect(container?.querySelector(".user-message-expand-toggle")).toBeNull();
    expect(container?.querySelector(".user-message-long-card")).toBeNull();
    expect(container?.textContent).toContain("Short query.");
  });

  it("renders pasted text beside a separate regular-text bubble", () => {
    const pastedText = "pasted line\n".repeat(80);
    render({
      item: {
        ...makeUserMessage(`${pastedText}Follow-up question.`),
        content_parts: [
          { type: "pasted_text", text: pastedText, title: "Pasted notes" },
          { type: "text", text: "Follow-up question." },
        ],
      },
      turnStatus: "completed",
      streaming: false,
    });

    const attachments = container?.querySelector(".user-message-attachments");
    const textBubble = container?.querySelector(".message.user-message");
    expect(attachments).not.toBeNull();
    expect(attachments?.nextElementSibling).toBe(textBubble);
    expect(textBubble?.querySelector(".user-message-pasted-text")).toBeNull();
    expect(container?.querySelector(".user-message-long-card")).toBeNull();
    expect(container?.querySelector(".user-message-pasted-text")?.textContent).toContain("Pasted notes");
    expect(container?.textContent).toContain("Follow-up question.");

    act(() => {
      container?.querySelector<HTMLButtonElement>(".user-message-pasted-text-toggle")?.click();
    });
    expect(container?.querySelector(".user-message-pasted-text-content")?.textContent).toBe(pastedText);
  });

  it.each([false, true])("collapses structured text across parts and preserves attachments (split=%s)", (split) => {
    const paragraphs = Array.from({ length: 16 }, (_, index) =>
      `段落 ${index + 1}：懂，你是说**折叠后的预览还是太长，占的空间太多**，不是想取消折叠。`,
    );
    const text = paragraphs.join("\n\n");
    render({
      item: {
        ...makeUserMessage(text),
        content_parts: [
          { type: "pasted_text", text: "separate attachment", title: "Notes" },
          ...(split ? paragraphs : [text]).map((part) => ({ type: "text" as const, text: part })),
        ],
      },
      turnStatus: "completed",
      streaming: false,
    });

    const bubble = container?.querySelector(".user-message");
    const toggle = bubble?.querySelector<HTMLButtonElement>(".user-message-expand-toggle");
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(bubble?.textContent).toContain("段落 1：");
    expect(bubble?.textContent).not.toContain("段落 16：");
    expect(container?.querySelector(".user-message-pasted-text")).not.toBeNull();
    expect(bubble?.textContent).not.toContain("separate attachment");

    act(() => toggle?.click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(bubble?.textContent).toContain("段落 16：");
    expect(bubble?.querySelector("strong")).not.toBeNull();

    act(() => toggle?.click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(bubble?.textContent).not.toContain("段落 16：");
  });

  it("collapses long wrapped user messages without explicit line breaks", () => {
    const longSingleParagraph = "pasted query ".repeat(150);

    render({
      item: makeUserMessage(longSingleParagraph),
      turnStatus: "completed",
      streaming: false,
    });

    const bubble = container?.querySelector<HTMLElement>(".user-message");
    const rawQuery = container?.querySelector<HTMLElement>(
      ".user-message-raw-query",
    );
    const toggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    expect(bubble?.classList.contains("user-message-long-card")).toBe(true);
    expect(bubble?.contains(toggle ?? null)).toBe(true);
    expect(rawQuery?.textContent?.endsWith("...")).toBe(true);
    expect((rawQuery?.textContent?.length ?? 0)).toBeLessThan(
      longSingleParagraph.length,
    );
    expect(toggle?.textContent).toContain("显示更多");
  });

  it("shows long query previews as raw pasted text", () => {
    const markdownQuery = [
      "# Final plan",
      "",
      "## 0. Metadata",
      "",
      "- **Goal**: keep DefaultSystemPrompt() readable",
      "- **Reference**: default.md / gpt_5_1_prompt.md / prompt_with_apply_patch_instructions.md",
      "- **Scope**: internal/config/config.go / internal/runtime/session.go",
      "- **Step**: preserve the copied query shape",
      "- **Step**: avoid rendered markdown changing the preview",
      "- **Step**: keep a full rounded bubble",
      "- **Step**: wrap long paths and identifiers",
      "- **Step**: expose a clear show more control",
      "- **Step**: keep short queries unchanged",
      "- **Step**: keep copy and edit using the full original text",
      "- **Step**: each query starts collapsed",
      "- **Step**: expanded queries can collapse again",
    ].join("\n");

    render({
      item: makeUserMessage(markdownQuery),
      turnStatus: "completed",
      streaming: false,
    });

    const rawQuery = container?.querySelector<HTMLElement>(
      ".user-message-raw-query",
    );
    expect(rawQuery?.textContent).toContain("# Final plan");
    expect(rawQuery?.textContent).toContain("- **Goal**");
    expect(container?.querySelector(".rich-heading")).toBeNull();
  });

  it("collapses long user messages and toggles the full text", () => {
    const longText = Array.from(
      { length: 20 },
      (_, index) => `line ${index + 1}`,
    ).join("\n");

    render({
      item: makeUserMessage(longText),
      turnStatus: "completed",
      streaming: false,
    });

    const toggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    const rawQuery = container?.querySelector<HTMLElement>(
      ".user-message-raw-query",
    );

    expect(rawQuery?.textContent).not.toContain("line 20");
    expect(rawQuery?.textContent?.endsWith("...")).toBe(true);
    expect(rawQuery?.textContent).toContain("line 5");
    expect(rawQuery?.textContent).not.toContain("line 6");
    expect(toggle?.textContent).toContain("显示更多");
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");

    act(() => {
      toggle?.click();
    });

    expect(rawQuery?.textContent).toBe(longText);
    expect(toggle?.textContent).toContain("收起");
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");

    act(() => {
      toggle?.click();
    });

    expect(rawQuery?.textContent).not.toContain("line 20");
    expect(toggle?.textContent).toContain("显示更多");
  });

  it("defaults a different long user query back to collapsed", () => {
    const firstLongText = Array.from(
      { length: 20 },
      (_, index) => `first query line ${index + 1}`,
    ).join("\n");
    const secondLongText = Array.from(
      { length: 20 },
      (_, index) => `second query line ${index + 1}`,
    ).join("\n");

    render({
      item: makeUserMessage(firstLongText, "user-1"),
      turnStatus: "completed",
      streaming: false,
    });

    const firstToggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    act(() => {
      firstToggle?.click();
    });

    expect(
      container?.querySelector(".user-message-raw-query")?.textContent,
    ).toContain("first query line 20");

    render({
      item: makeUserMessage(secondLongText, "user-2"),
      turnStatus: "completed",
      streaming: false,
    });

    const secondToggle = container?.querySelector<HTMLButtonElement>(
      ".user-message-expand-toggle",
    );
    expect(
      container?.querySelector(".user-message-raw-query")?.textContent,
    ).not.toContain("second query line 20");
    expect(secondToggle?.getAttribute("aria-expanded")).toBe("false");
  });

  it("enables copy and fork as soon as the final item completes", () => {
    const onForkMessage = vi.fn();
    render({
      item: makeFinalAnswer("in_progress"),
      turnStatus: "in_progress",
      streaming: true,
      onForkMessage,
    });

    expect(actionBar().getAttribute("aria-hidden")).toBe("true");
    expect(actionBar().querySelector("button, [tabindex]")).toBeNull();

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "in_progress",
      latestAgentMessageID: "final-1",
      streaming: false,
      onForkMessage,
    });

    const finalizingActions = actionBar();
    expect(finalizingActions.getAttribute("aria-hidden")).toBeNull();
    expect(finalizingActions.dataset.wuuPlacement).toBe("persistent");
    expect(finalizingActions.querySelectorAll("button")).toHaveLength(2);
    const finalizingButtons = finalizingActions.querySelectorAll<HTMLButtonElement>("button");
    expect(finalizingButtons[0]?.disabled).toBe(false);
    expect(finalizingButtons[1]?.disabled).toBe(false);
    finalizingButtons[1]?.click();
    expect(onForkMessage).toHaveBeenCalledWith("turn-1", "final-1");

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      actionableAgentMessageID: "final-1",
      latestAgentMessageID: "final-1",
      streaming: false,
      onForkMessage,
    });

    const visibleActions = actionBar();
    expect(visibleActions.getAttribute("aria-label")).toBe("助手消息操作");
    expect(visibleActions.dataset.wuuComponent).toBe("message-actions");
    expect(visibleActions.dataset.wuuPlacement).toBe("persistent");
    expect(visibleActions.querySelectorAll("button")).toHaveLength(2);
    expect(visibleActions.querySelectorAll<HTMLButtonElement>("button")[1]?.disabled).toBe(false);
    const block = container?.querySelector(".agent-block");
    expect(block?.classList.contains("agent-actions-persistent")).toBe(true);
    expect(block?.classList.contains("agent-actions-overlay")).toBe(false);
    expect(block?.classList.contains("agent-actions-enter")).toBe(true);
  });

  it.each([undefined, false])("reserves actions before terminal is known (%s)", (terminal) => {
    render({
      item: { ...makeFinalAnswer("in_progress"), terminal },
      turnStatus: "in_progress",
      latestAgentMessageID: "final-1",
      streaming: true,
    });
    expect(actionBar().getAttribute("aria-hidden")).toBe("true");
    expect(actionBar().querySelectorAll("button")).toHaveLength(0);
    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      actionableAgentMessageID: "final-1",
      latestAgentMessageID: "final-1",
      streaming: false,
    });
    expect(actionBar().getAttribute("aria-hidden")).toBeNull();
    expect(actionBar().querySelectorAll("button")).toHaveLength(2);
  });

  it("keeps historical answer actions hover-revealed rather than persistent", () => {
    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      actionableAgentMessageID: "final-1",
      latestAgentMessageID: "final-99",
      streaming: false,
    });

    expect(actionBar().dataset.wuuPlacement).toBe("overlay");
    const block = container?.querySelector(".agent-block");
    expect(block?.classList.contains("agent-actions-overlay")).toBe(true);
    expect(block?.classList.contains("agent-actions-persistent")).toBe(false);
  });

  it("releases completed stream text after the settled view keeps the final answer", async () => {
    const key = streamTextKey("turn-1", "final-1", "text");
    streamTextStore.set(key, "Final answer text.");

    render({
      item: makeFinalAnswer("completed"),
      turnStatus: "completed",
      actionableAgentMessageID: "final-1",
      latestAgentMessageID: "final-1",
      streaming: false,
    });

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });

    expect(container?.textContent).toContain("Final answer text.");
    expect(streamTextStore.has(key)).toBe(false);
  });

  it("renders a plugin-generated query as a read-only user message", () => {
    const onEditMessage = vi.fn();
    render({
      item: {
        id: "plugin-query-1",
        type: "user_message",
        text: "子任务 太阳 已更新",
        read_only: true,
        origin: "plugin",
        origin_id: "subagent",
        cause: "subagent.completion",
        presentation_kind: "query_bubble",
      },
      turnStatus: "completed",
      streaming: false,
      onEditMessage,
    });

    expect(container?.querySelector(".user-message")?.textContent).toBe("子任务 太阳 已更新");
    const actions = container?.querySelectorAll<HTMLButtonElement>(".user-message-actions button");
    expect(actions).toHaveLength(1);
    expect(container?.querySelector<HTMLElement>(".user-message-actions")?.dataset.wuuPlacement).toBe("overlay");
    expect(onEditMessage).not.toHaveBeenCalled();
  });

  it("expands and copies a session message body and opens its source without exposing internal input", async () => {
    const openInSplit = vi.fn();
    setOpenThreadInSplitHandler(openInSplit);
    const copy = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: copy } });
    const body = "Coordination update.\n".repeat(100) + "End of update.";
    const onEditMessage = vi.fn();
    render({
      item: {
        id: "peer-message", type: "user_message", text: body,
        input_text: "Private delivery instructions", related_session_id: "source-session",
        name: "Source task", origin: "plugin", origin_id: "alternative-messenger",
        presentation_kind: "session_message", read_only: true,
      },
      turnStatus: "completed", streaming: false, onEditMessage,
    });
    expect(container?.textContent).toContain("Source task");
    expect(container?.textContent).not.toContain("Private delivery instructions");
    expect(container?.textContent).not.toContain("End of update.");
    const toggle = container!.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
    act(() => toggle.click());
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(container?.textContent).toContain("End of update.");
    const actions = container!.querySelectorAll<HTMLButtonElement>(".user-message-actions button");
    expect(actions).toHaveLength(1);
    await act(async () => actions[0].click());
    expect(copy).toHaveBeenCalledWith(body);
    act(() => container!.querySelector<HTMLButtonElement>(".session-message-source")!.click());
    expect(openInSplit).toHaveBeenCalledWith("source-session");
    expect(onEditMessage).not.toHaveBeenCalled();
    act(() => toggle.click());
    expect(container?.textContent).not.toContain("End of update.");
    setOpenThreadInSplitHandler(undefined);
  });

  it("does not show a related-session action without a related session", () => {
    render({
      item: {
        id: "user-own-1",
        type: "user_message",
        text: "这是我的普通消息",
        input_text: "这是发给 Agent 的隐藏消息",
        status: "completed",
      },
      turnStatus: "completed",
      streaming: false,
    });

    const actions = container?.querySelectorAll<HTMLButtonElement>(".user-message-actions button");
    expect(actions).toHaveLength(1);
    expect([...(actions ?? [])].some((button) => button.getAttribute("aria-label") === "打开关联会话")).toBe(false);
  });

  it("shows the user message time before its copy action", () => {
    const sentAt = new Date(2026, 7, 31, 9, 7).toISOString();
    render({
      item: makeUserMessage("带发送时间的消息"),
      turnStatus: "completed",
      turnStartedAt: sentAt,
      streaming: false,
    });

    const actions = container?.querySelector(".user-message-actions");
    const time = actions?.querySelector<HTMLTimeElement>(".user-message-time");
    const copy = actions?.querySelector<HTMLButtonElement>("button");
    expect(time?.dateTime).toBe(sentAt);
    expect(time?.textContent).toBe("09:07");
    expect(time?.nextElementSibling).toBe(copy);
  });

  it("splits the conversation and opens the related session", () => {
    const openInSplit = vi.fn();
    setOpenThreadInSplitHandler(openInSplit);
    render({
      item: {
        id: "plugin-query-1",
        type: "user_message",
        text: "子任务 太阳 已更新",
        input_text: "子任务 太阳（session 20260817-171746-edd1069c0780f11a）已完成。请检查并整合以下交接结果：\n\n三个命令均已成功执行",
        related_session_id: "20260817-171746-edd1069c0780f11a",
        read_only: true,
        origin: "plugin",
        origin_id: "subagent",
        cause: "subagent.completion",
        presentation_kind: "query_bubble",
      },
      turnStatus: "completed",
      streaming: false,
    });

    const actions = container?.querySelectorAll<HTMLButtonElement>(".user-message-actions button");
    expect(actions).toHaveLength(2);
    const openRelated = [...(actions ?? [])].find((button) => button.getAttribute("aria-label") === "打开关联会话");
    expect(openRelated).toBeDefined();

    act(() => openRelated!.click());
    expect(openInSplit).toHaveBeenCalledWith("20260817-171746-edd1069c0780f11a");

    act(() => setOpenThreadInSplitHandler(undefined));
  });

});
