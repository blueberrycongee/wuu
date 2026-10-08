import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import { ASSISTANT_TURN_PRESENTATION_STABILIZE_MS } from "./AssistantTurnPresentation";
import { PROCESS_NOTIFICATION_NAME } from "./InternalUserNotification";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { TurnView } from "./TurnView";
import { CONTINUE_TURN_EVENT, OPEN_SETTINGS_EVENT, type ContinueTurnDetail } from "./TurnNotice";
import { translateCurrent as t } from "./i18n";
import type { TurnStreamStatus } from "./AppState";
import { ImagePreviewProvider } from "./ImagePreview";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";
import { STREAM_TEXT_NOTIFY_INTERVAL_MS, streamTextKey, streamTextStore } from "./StreamText";

// Keep the temporarily hidden review surface's lifecycle coverage for restoration.
vi.mock("./FeatureFlags", async (importOriginal) => ({
  ...await importOriginal<typeof import("./FeatureFlags")>(),
  ENABLE_TURN_EDIT_SUMMARY: true,
  ENABLE_TURN_ARTIFACT_SUMMARY: true,
}));

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function makeTurn(
  status: Turn["status"],
  items: ThreadItem[] = [],
  error?: string,
): Turn {
  return {
    id: "turn-1",
    items,
    items_view: "full",
    status,
    error: error ? { message: error } : undefined,
  };
}

function makeCommentary(text: string): ThreadItem {
  return {
    id: "commentary-1",
    type: "agent_message",
    status: "completed",
    terminal: false,
    role: "assistant",
    text,
  };
}

function makeFinalAnswer(
  text: string,
  status: ThreadItem["status"] = "completed",
): ThreadItem {
  return {
    id: "answer-1",
    type: "agent_message",
    status,
    terminal: true,
    role: "assistant",
    text,
  };
}

function makeError(error: string): ThreadItem {
  return {
    id: "error-1",
    type: "error",
    status: "failed",
    error,
  };
}

function makeReasoning(text: string, id = "reasoning-1"): ThreadItem {
  return {
    id,
    type: "reasoning",
    status: "completed",
    text,
  };
}

function render(turn: Turn, isLatestTurn = false, streamStatus?: TurnStreamStatus): HTMLDivElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(
      <TurnView
        turn={turn}
        onStreamFrame={() => {}}
        isLatestTurn={isLatestTurn}
        streamStatus={streamStatus}
      />,
    );
  });
  return container;
}

function rerender(turn: Turn, isLatestTurn = false): void {
  act(() => {
    root!.render(
      <TurnView
        turn={turn}
        onStreamFrame={() => {}}
        isLatestTurn={isLatestTurn}
      />,
    );
  });
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  act(() => {
    root?.unmount();
  });
  container?.remove();
  streamTextStore.clearItem("turn-1", "commentary-1");
  desktopPluginHost.setActiveConversationThread(undefined);
  root = undefined;
  container = undefined;
});

describe("TurnView", () => {
  it.each(["interrupted", "failed", "completed"] as const)(
    "settles streamed text when the turn becomes %s before item completion",
    (status) => {
      vi.useFakeTimers();
      const items: ThreadItem[] = [
        { ...makeReasoning("Partial reasoning"), status: "in_progress" },
        { ...makeCommentary("Partial answer"), status: "in_progress" },
      ];
      const view = render(makeTurn("in_progress", items), true);
      const streams = () => Array.from(view.querySelectorAll(".streaming-markdown"));
      expect(streams()).toHaveLength(2);
      expect(streams().every((stream) => stream.getAttribute("data-stream-state") === "streaming")).toBe(true);

      rerender(makeTurn(status, items), true);
      act(() => vi.advanceTimersByTime(500));
      const toggle = view.querySelector<HTMLButtonElement>(".turn-process-toggle");
      if (toggle?.getAttribute("aria-expanded") === "false") {
        act(() => toggle.click());
      }
      expect(streams()).toHaveLength(2);
      expect(view.textContent).toContain("Partial reasoning");
      expect(view.textContent).toContain("Partial answer");
      for (const stream of streams()) {
        expect(stream.getAttribute("data-stream-state")).toBe("settled");
        expect(stream.getAttribute("data-cursor-state")).toBe("fading");
      }
    },
  );

  it("keeps the conversation timeline plugin surface on each real turn", async () => {
    await desktopPluginHost.activateGeneration({
      pluginId: "test:turn-timeline",
      generation: "one",
      register(api) {
        api.registerSurface("conversation.timeline", {
          id: "turn-replacement",
          mode: "replace",
          render(context) {
            const turns = context.turns as Turn[];
            return <div data-testid="plugin-turn">{turns[0]?.id}</div>;
          },
        });
      },
    });

    const view = render(makeTurn("completed"));
    expect(view.querySelector('[data-testid="plugin-turn"]')?.textContent).toBe(
      "turn-1",
    );

    await act(async () => desktopPluginHost.unload("test:turn-timeline"));
  });

  it("exposes the active thread id on the conversation timeline surface", async () => {
    let received: Readonly<Record<string, unknown>> | undefined;
    await desktopPluginHost.activateGeneration({
      pluginId: "test:turn-timeline",
      generation: "thread-id",
      register(api) {
        api.registerSurface("conversation.timeline", {
          id: "thread-aware-turn",
          mode: "replace",
          render(context) {
            received = context;
            return <div data-thread-aware-turn>{String(context.threadId)}</div>;
          },
        });
      },
    });

    desktopPluginHost.setActiveConversationThread("thread-timeline-1");
    const view = render(makeTurn("completed"));

    expect(view.querySelector("[data-thread-aware-turn]")?.textContent).toBe("thread-timeline-1");
    expect(received).toHaveProperty("threadId", "thread-timeline-1");
    desktopPluginHost.setActiveConversationThread(undefined);
    await act(async () => desktopPluginHost.unload("test:turn-timeline"));
  });

  it("marks the turn root with the live status used by scroll-stable CSS", () => {
    const view = render(makeTurn("in_progress"));

    const turn = view.querySelector<HTMLElement>(".turn");
    expect(turn?.dataset.turnStatus).toBe("in_progress");
  });

  it("reveals actions when a fast answer first mounts already completed", () => {
    vi.useFakeTimers();
    const userItem: ThreadItem = {
      id: "user-1",
      type: "user_message",
      status: "completed",
      text: "Reply quickly.",
    };
    const view = render(makeTurn("in_progress", [userItem]), true);

    rerender(
      makeTurn("completed", [userItem, makeFinalAnswer("Done.")]),
      true,
    );
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS);
    });

    expect(
      view.querySelector(".agent-block")?.classList.contains("agent-actions-enter"),
    ).toBe(true);
  });

  it("does not replay the action entrance for a historical completed answer", () => {
    const view = render(
      makeTurn("completed", [makeFinalAnswer("Already finished.")]),
      true,
    );

    expect(view.querySelector(".agent-message-actions")).not.toBeNull();
    expect(
      view.querySelector(".agent-block")?.classList.contains("agent-actions-enter"),
    ).toBe(false);
  });

  it("does not invent transport notice space when an ordinary live turn settles", () => {
    const userItem: ThreadItem = {
      id: "user-1",
      type: "user_message",
      status: "completed",
      text: "Stream something.",
    };
    const view = render(makeTurn("in_progress", [userItem]), true);

    // Ordinary streaming never displayed a transport notice.
    expect(view.querySelector(".turn-stream-status-spacer")).toBeNull();

    rerender(
      makeTurn("completed", [userItem, makeFinalAnswer("Settled answer.")]),
      true,
    );

    expect(view.querySelector(".turn-stream-status-spacer")).toBeNull();
  });

  it("retains only the notice actually visible when a live turn settles", () => {
    const answer = makeFinalAnswer("Answer text.", "in_progress");
    const view = render(makeTurn("in_progress", [answer]), true, { text: "Transport recovery", liveProgress: false });
    const notice = view.querySelector(".stream-status-notice");
    expect(notice).not.toBeNull();
    expect(notice?.closest('[aria-hidden="true"]')).toBeNull();
    rerender(makeTurn("completed", [{ ...answer, status: "completed" }]), true);
    expect(view.querySelector(".stream-status-notice")).toBe(notice);
    expect(notice?.closest('[aria-hidden="true"]')).not.toBeNull();
  });

  it("does not preserve a transport notice that cleared before completion", () => {
    const answer = makeFinalAnswer("Answer text.", "in_progress");
    const view = render(makeTurn("in_progress", [answer]), true, { text: "Transport recovery", liveProgress: false });
    rerender(makeTurn("in_progress", [answer]), true);
    rerender(makeTurn("completed", [{ ...answer, status: "completed" }]), true);
    expect(view.querySelector(".turn-stream-status-spacer")).toBeNull();
  });

  it("does not reserve the stream footprint for a historical completed turn", () => {
    const view = render(
      makeTurn("completed", [makeFinalAnswer("Already finished.")]),
      true,
    );

    expect(view.querySelector(".turn-stream-status-spacer")).toBeNull();
  });

  it("hides named and legacy process notifications from the direct turn renderer", () => {
    const processText =
      '<process_notification>{"process_id":"proc-legacy"}</process_notification>';
    const view = render(
      makeTurn("completed", [
        {
          id: "process-named",
          type: "user_message",
          name: PROCESS_NOTIFICATION_NAME,
          text: '<process_notification>{"process_id":"proc-named"}</process_notification>',
        },
        {
          id: "process-legacy",
          type: "user_message",
          text: processText,
        },
        {
          id: "user-1",
          type: "user_message",
          text: "真正的用户消息",
        },
      ]),
    );

    expect(view.textContent).not.toContain("proc-named");
    expect(view.textContent).not.toContain("proc-legacy");
    expect(view.textContent).toContain("真正的用户消息");
    expect(view.querySelectorAll(".user-message-block")).toHaveLength(1);
  });

  it("renders a plugin-generated query through the existing user-message flow", () => {
    const view = render(
      makeTurn("completed", [
        {
          id: "plugin-update",
          type: "user_message",
          text: "子任务 太阳 已更新",
          read_only: true,
          origin: "plugin",
          origin_id: "subagent",
          cause: "subagent.completion",
          presentation_kind: "query_bubble",
        },
      ]),
    );

    expect(view.querySelectorAll(".user-message-block")).toHaveLength(1);
    expect(view.querySelector(".user-message")?.textContent).toBe("子任务 太阳 已更新");
    expect(view.querySelector(".message-edit-button")).toBeNull();
  });

  it("removes the temporary review card when the next turn begins, retaining tool history", () => {
    vi.useFakeTimers();
    const turn = makeTurn("completed", [
        {
          id: "write-1",
          type: "tool_call",
          name: "write_file",
          status: "completed",
          arguments: JSON.stringify({ path: "notes/brief.md", content: "# Brief\n" }),
          result: JSON.stringify({
            path: "notes/brief.md",
            diff: { new_file: true, lines: 1 },
          }),
        },
      ]);
    const view = render(turn, true);
    expect(view.querySelector(".turn-edit-summary-card")).not.toBeNull();
    rerender(turn, false);
    expect(view.querySelector("[inert] .turn-edit-summary-card")).not.toBeNull();
    act(() => vi.advanceTimersByTime(220));
    expect(view.querySelector(".turn-edit-summary-card")).toBeNull();
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
    expect(view.querySelector(".assistant-turn-shell")).not.toBeNull();
    expect(turn.items).toHaveLength(1);
  });

  it.each(["failed", "interrupted"] as const)(
    "keeps %s edits collapsed after execution events and available on demand",
    (status) => {
      vi.useFakeTimers();
      const turn = makeTurn(status, [{
        id: "write-1",
        type: "tool_call",
        name: "write_file",
        status: "completed",
        result: JSON.stringify({ path: "partial.ts", diff: { new_file: true, lines: 1 } }),
      }], status === "failed" ? "network connection lost" : undefined);
      const view = render(turn, true);
      const edits = view.querySelector(".turn-edit-presentation")!;
      expect(view.querySelector(".turn-edit-summary-card")).toBeNull();
      const process = view.querySelector(".assistant-turn-shell")!;
      expect(process.compareDocumentPosition(edits) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      const notice = view.querySelector(".turn-failure");
      if (status === "failed") {
        expect(notice).not.toBeNull();
        expect(notice!.compareDocumentPosition(edits) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      }
      const editToggle = edits.querySelector<HTMLButtonElement>("button")!;
      expect(editToggle.getAttribute("aria-expanded")).toBe("false");
      act(() => editToggle.click());
      expect(editToggle.getAttribute("aria-expanded")).toBe("true");
      expect(edits.querySelector(".turn-edit-summary-card")?.textContent).toContain("partial.ts");
      rerender(turn, false);
      act(() => vi.advanceTimersByTime(220));
      expect(view.querySelector(".turn-edit-presentation")).toBeNull();
      if (notice) expect(view.contains(notice)).toBe(true);
    },
  );

  it("does not resurrect historical cards on mount", () => {
    const view = render(makeTurn("completed", [{
      id: "write-old",
      type: "tool_call",
      name: "write_file",
      status: "completed",
      result: JSON.stringify({ path: "old.ts", diff: { new_file: true, lines: 1 } }),
    }]), false);
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
  });

  it("removes old cards immediately when reduced motion is requested", () => {
    vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
    const turn = makeTurn("completed", [{
      id: "write-1", type: "tool_call", name: "write_file", status: "completed",
      result: JSON.stringify({ path: "brief.md", diff: { new_file: true, lines: 1 } }),
    }]);
    const view = render(turn, true);
    expect(view.querySelector(".turn-edit-summary-card")).not.toBeNull();
    rerender(turn, false);
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
  });

  it("does not offer retained edits when a failed turn made no changes", () => {
    const view = render(makeTurn("failed", [makeError("network connection lost")], "network connection lost"), true);
    expect(view.querySelector(".turn-failure")).not.toBeNull();
    expect(view.querySelector(".turn-edit-presentation")).toBeNull();
  });

  it("attaches the edit summary to an answer before turn settlement", () => {
    const turn = makeTurn("in_progress", [
      {
        id: "write-1",
        type: "tool_call",
        name: "write_file",
        status: "completed",
        result: JSON.stringify({
          path: "notes/brief.md",
          diff: { new_file: true, lines: 1 },
        }),
      },
      makeFinalAnswer("done"),
    ]);
    turn.answer_ready_at = "2026-08-29T04:00:10.000Z";

    const view = render(turn, true);
    const agentBlock = view.querySelector(".agent-block");

    expect(agentBlock?.textContent).toContain("done");
    expect(agentBlock?.textContent).toContain("本轮修改 1 个文件");
    expect(agentBlock?.querySelector(".turn-edit-summary-card")).toBeTruthy();
  });

  it.each([1, 3])("keeps %s image inspection tools in the process fold with previews on demand", async (count) => {
    const inspect = (index: number): ThreadItem => ({
      id: `inspect-${index}`, type: "tool_call", name: "read_file", status: "completed",
      arguments: JSON.stringify({ path: `inspection-${index}.svg` }),
      result_detail: { content: [
        { type: "text", text: `Inspection metadata ${index}` },
        { type: "image", name: `inspection-${index}.svg`, mime_type: "image/svg+xml", data: btoa('<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><rect width="40" height="40" fill="blue"/></svg>') },
      ] },
    });
    const tools = Array.from({ length: count }, (_, index) => inspect(index));
    const turn = makeTurn("completed", [...tools, makeFinalAnswer("Checked.")]);
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    act(() => root!.render(<ImagePreviewProvider><TurnView turn={turn} isLatestTurn onStreamFrame={() => {}} /></ImagePreviewProvider>));
    expect(container.querySelectorAll('.turn-answer-body [data-wuu-component="turn-artifacts-inline"]')).toHaveLength(0);
    expect(container.querySelectorAll('img')).toHaveLength(0);
    await act(async () => { container!.querySelector<HTMLElement>('.turn-process-toggle')!.click(); });
    expect(container.querySelectorAll('.turn-process-entry')).toHaveLength(1);
    const process = container.querySelector<HTMLDetailsElement>('.turn-process-entry .process-surface-fold')!;
    expect(process).not.toBeNull();
    await act(async () => { process.open = true; process.dispatchEvent(new Event("toggle")); });
    expect(process.querySelectorAll('img')).toHaveLength(count);
    await act(async () => { process.open = false; process.dispatchEvent(new Event("toggle")); });
    expect(container.querySelectorAll('img')).toHaveLength(0);
  });

  it("resolves grouped inspection images against their owning conversation directory", async () => {
    const turn = makeTurn("completed", [{ id: "relative-inspection", type: "tool_call", name: "read_file", status: "completed",
      result_detail: { content: [{ type: "image", mime_type: "image/png", uri: "images/preview.png", name: "preview.png" }] },
    }]);
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    act(() => root!.render(<ImagePreviewProvider><TurnView turn={turn} cwd="/workspace/owning-thread" onStreamFrame={() => {}} /></ImagePreviewProvider>));
    const process = container.querySelector<HTMLDetailsElement>('.turn-process-entry .process-surface-fold')!;
    await act(async () => { process.open = true; process.dispatchEvent(new Event("toggle")); });
    const source = container.querySelector('img')?.getAttribute('src');
    expect(source).toBe(`wuu-file://local/${btoa('/workspace/owning-thread/images/preview.png').replace(/=+$/, '')}`);
  });

  it("keeps explicit artifact previews separate from the unchanged file-diff summary", () => {
    const uri = "wuu-artifact://workspace/thread/artifact/chart.svg?sha256=old";
    const turn = makeTurn("completed", [
      { id: "write-chart", type: "tool_call", name: "write_file", status: "completed",
        result: JSON.stringify({ path: "chart.svg", diff: { new_file: true, lines: 68 } }) },
      { id: "present-chart", type: "tool_call", name: "present_artifact", status: "completed",
        result_detail: { content: [{ type: "image", mime_type: "image/svg+xml", name: "chart.svg", uri,
          artifact: { ref: "artifact", sha256: "old", placement: "inline" } }] } },
      makeFinalAnswer("图已生成。"),
    ]);
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    act(() => root!.render(<ImagePreviewProvider><TurnView turn={turn} isLatestTurn onStreamFrame={() => {}} /></ImagePreviewProvider>));
    const preview = container.querySelector('[data-wuu-component="turn-artifacts-inline"]')!;
    const diff = container.querySelector(".turn-edit-summary-card")!;
    expect(preview.querySelector("img")?.getAttribute("src")).toBe(uri);
    expect(diff.textContent).toContain("本轮修改 1 个文件");
    expect(diff.textContent).toContain("chart.svg");
    expect(diff.textContent).toContain("68");
    expect(diff.contains(preview)).toBe(false);
    expect(preview.contains(diff)).toBe(false);
    expect(container.querySelector(".agent-block")?.contains(diff)).toBe(true);
    expect(preview.closest(".turn-process-fold")).toBeNull();
  });

  it("attaches presented files to the answer using the file-change summary card", () => {
    const view = render(makeTurn("completed", [
      {
        id: "present-video",
        type: "tool_call",
        name: "present_artifact",
        status: "completed",
        result_detail: {
          content: [{
            type: "file",
            mime_type: "video/mp4",
            name: "wuu-promo.mp4",
            uri: "wuu-artifact://workspace/thread/artifact/wuu-promo.mp4",
            artifact: { ref: "video", sha256: "vid", placement: "turn_end" },
          }],
        },
      },
      makeFinalAnswer("成品：wuu-promo.mp4"),
    ]), true);
    const card = view.querySelector('[data-wuu-component="turn-artifacts"]');
    expect(card?.classList.contains("turn-edit-summary-card")).toBe(true);
    expect(view.querySelector(".agent-block")?.contains(card)).toBe(true);
    expect(card?.querySelector(".turn-edit-summary-overview")).toBeTruthy();
    expect(card?.textContent).toContain("wuu-promo.mp4");
    expect(card?.textContent).not.toContain("video/mp4");
  });

  it("keeps text after published images in place through streaming, more output, and completion", () => {
    vi.useFakeTimers();
    const image = (id: string): ThreadItem => ({
      id, type: "tool_call", name: "present_artifact", status: "completed",
      result_detail: { content: [{ type: "image", mime_type: "image/svg+xml", name: `${id}.svg`,
        uri: `wuu-artifact://workspace/thread/${id}/chart.svg`,
        artifact: { ref: id, placement: "inline" } }] },
    });
    const first = image("first"), second = image("second");
    const text: ThreadItem = { ...makeCommentary("Reading the chart"), status: "in_progress" };
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    const update = (turn: Turn): void => {
      act(() => root!.render(<ImagePreviewProvider><TurnView turn={turn} isLatestTurn onStreamFrame={() => {}} /></ImagePreviewProvider>));
      act(() => vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS));
    };
    const before = (a: Element, b: Element): boolean => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
    update(makeTurn("in_progress", [first]));
    const firstImage = container.querySelector("img")!;
    update(makeTurn("in_progress", [first, text]));
    const message = container.querySelector(".agent-block")!;
    expect(before(firstImage, message)).toBe(true);
    expect(message.closest(".turn-process-fold")).toBeNull();

    const streamKey = streamTextKey("turn-1", text.id, "text");
    act(() => {
      streamTextStore.seed(streamKey, text.text!);
      streamTextStore.append(streamKey, " with streamed details");
    });
    act(() => vi.advanceTimersByTime(STREAM_TEXT_NOTIFY_INTERVAL_MS + 100));
    expect(message.textContent).toContain("with streamed details");
    expect(container.querySelector(".agent-block")).toBe(message);
    expect(container.querySelector("img")).toBe(firstImage);

    // A terminal turn snapshot carries its streamed items already settled.
    const settledText = { ...text, text: streamTextStore.get(streamKey), status: "completed" as const };
    update(makeTurn("interrupted", [first, settledText]));
    expect(container.querySelector(".agent-block")).toBe(message);
    expect(before(firstImage, message)).toBe(true);

    // A second output must not gather both images after all the commentary.
    const final = { ...makeFinalAnswer("The comparison"), terminal: false, status: "in_progress" as const };
    update(makeTurn("in_progress", [first, settledText, second, final]));
    const secondImage = container.querySelectorAll("img")[1];
    const finalMessage = container.querySelectorAll(".agent-block")[1];
    expect(before(message, secondImage)).toBe(true);
    expect(before(secondImage, finalMessage)).toBe(true);

    // Confirming terminal status must preserve both the image and text nodes.
    update(makeTurn("completed", [first, settledText, second, { ...final, terminal: true, status: "completed" }]));
    expect(container.querySelectorAll("img")[0]).toBe(firstImage);
    expect(container.querySelectorAll("img")[1]).toBe(secondImage);
    expect(container.querySelectorAll(".agent-block")[0]).toBe(message);
    expect(container.querySelectorAll(".agent-block")[1]).toBe(finalMessage);
    expect(before(secondImage, finalMessage)).toBe(true);
  });

  it("buffers structural process changes briefly while keeping the current text visible", () => {
    vi.useFakeTimers();
    const view = render(
      makeTurn("in_progress", [makeCommentary("checking the files")]),
    );

    expect(view.textContent).toContain("checking the files");
    expect(view.textContent).not.toContain("查看思考过程");

    rerender(
      makeTurn("in_progress", [
        makeCommentary("checking the files"),
        makeReasoning("settled reasoning"),
      ]),
    );

    expect(view.textContent).toContain("checking the files");
    expect(view.textContent).not.toContain("查看思考过程");

    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS - 1);
    });
    expect(view.textContent).not.toContain("查看思考过程");

    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(view.textContent).toContain("查看思考过程");
  });

  it("publishes a frozen process catch-up immediately when the conversation becomes visible", () => {
    vi.useFakeTimers();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    const turn = makeTurn("in_progress", [makeCommentary("checking the files")]);
    act(() => {
      root!.render(
        <ConversationRenderActivityProvider active={false}>
          <TurnView turn={turn} onStreamFrame={() => {}} />
        </ConversationRenderActivityProvider>,
      );
    });
    act(() => {
      root!.render(
        <ConversationRenderActivityProvider active={false}>
          <TurnView
            turn={makeTurn("in_progress", [
              makeCommentary("checking the files"),
              makeReasoning("settled reasoning"),
            ])}
            onStreamFrame={() => {}}
          />
        </ConversationRenderActivityProvider>,
      );
    });
    act(() => {
      root!.render(
        <ConversationRenderActivityProvider active>
          <TurnView
            turn={makeTurn("in_progress", [
              makeCommentary("checking the files"),
              makeReasoning("settled reasoning"),
            ])}
            onStreamFrame={() => {}}
          />
        </ConversationRenderActivityProvider>,
      );
    });

    expect(container.textContent).toContain("查看思考过程");
  });

  it("publishes a live tool or reasoning continuation without a delayed layout jump", () => {
    vi.useFakeTimers();
    const view = render(
      makeTurn("in_progress", [makeCommentary("checking the files")]),
    );

    rerender(
      makeTurn("in_progress", [
        makeCommentary("checking the files"),
        { ...makeReasoning("checking the result"), status: "in_progress" },
      ]),
    );

    expect(view.textContent).toContain("正在思考");
    expect(view.textContent).toContain("checking the result");
  });

  it("publishes the completed final answer without the structural buffer delay", () => {
    vi.useFakeTimers();
    const view = render(
      makeTurn("in_progress", [makeFinalAnswer("partial reply", "in_progress")]),
    );

    expect(view.textContent).toContain("partial reply");

    rerender(
      makeTurn("completed", [makeFinalAnswer("partial reply and final text")]),
    );

    expect(view.textContent).toContain("partial reply and final text");
  });

  it("coalesces repeated structural changes without extending the buffer forever", () => {
    vi.useFakeTimers();
    const view = render(makeTurn("in_progress", [makeCommentary("checking")]));

    rerender(
      makeTurn("in_progress", [
        makeCommentary("checking"),
        makeReasoning("first reasoning", "reasoning-1"),
      ]),
    );
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS / 2);
    });
    expect(view.textContent).not.toContain("思考过程");

    rerender(
      makeTurn("in_progress", [
        makeCommentary("checking"),
        makeReasoning("first reasoning", "reasoning-1"),
        makeReasoning("second reasoning", "reasoning-2"),
      ]),
    );
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS / 2);
    });

    expect(view.textContent).toContain("思考过程");
  });

  it("keeps partial output without a stop notice after a resumable pause", () => {
    const view = render(
      makeTurn("interrupted", [
        makeCommentary("partial progress"),
        makeError("context canceled"),
      ]),
    );

    expect(view.textContent).toContain("partial progress");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
    expect(view.textContent).not.toContain("已停止");
    expect(view.textContent).not.toContain("回复已中断");
  });

  it("renders one failure notice when a failed turn also records an error item", () => {
    const view = render(
      makeTurn(
        "failed",
        [
          makeCommentary("partial progress"),
          makeError("wait: context deadline exceeded"),
        ],
        "stream request failed: stream error (previous_response_not_found)",
      ),
    );

    expect(view.textContent).toContain("partial progress");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(1);
    const notice = view.querySelector(".turn-notice")!;
    expect(notice.textContent).not.toContain("previous_response_not_found");
    const disclosure = notice.querySelector<HTMLButtonElement>(".turn-failure-details-toggle")!;
    act(() => disclosure.click());
    expect(notice.querySelector(".turn-failure-diagnostic")?.textContent).toContain("previous_response_not_found");
    // A historical turn explains itself but offers no actions.
    expect(notice.querySelectorAll("button:not(.turn-failure-details-toggle), a")).toHaveLength(0);
  });

  it("renders one failure notice when an interrupted turn also records its internal error as an item", () => {
    const error = "wuu internal error: request did not complete";
    const view = render(
      makeTurn(
        "interrupted",
        [makeCommentary("partial progress"), makeError(error)],
        error,
      ),
    );

    expect(view.textContent).toContain("partial progress");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(1);
    expect(view.textContent).toContain("内部错误");
  });

  it("hides a transient stream error item while the turn is still retrying", () => {
    // A retryable attempt that failed terminally lands an error item, but
    // the turn keeps running (reconnect chip carries the cause). Rendering
    // the item here would pile up one settled error line per attempt.
    const view = render(
      makeTurn("in_progress", [
        makeCommentary("partial progress"),
        makeError("HTTP 429: Too Many Requests"),
      ]),
    );

    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
    expect(view.textContent).not.toContain("429");
  });

  it("drops a transient stream error item once the turn recovered and completed", () => {
    // The retry succeeded: nothing about the superseded failure should stay
    // behind in the finished turn.
    const view = render(
      makeTurn("completed", [
        makeFinalAnswer("done"),
        makeError("HTTP 429: Too Many Requests"),
      ]),
    );

    expect(view.textContent).toContain("done");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
    expect(view.textContent).not.toContain("429");
  });

  it("does not render a notice when a completed turn has commentary but no final answer", () => {
    vi.useFakeTimers();
    const view = render(
      makeTurn("completed", [makeCommentary("thinking out loud")]),
    );
    // The presentation buffer delays publishing the display for
    // ASSISTANT_TURN_PRESENTATION_STABILIZE_MS so the fold body has
    // time to settle. Advance past it so the chip mounts.
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS);
    });

    // The process records (commentary) are still visible in the fold.
    expect(view.textContent).toContain("thinking out loud");
    expect(view.querySelectorAll(".turn-notice")).toHaveLength(0);
  });

  it("keeps command runs out of the final answer actions", () => {
    const view = render(
      makeTurn("completed", [
        {
          id: "call-1",
          type: "tool_call",
          status: "completed",
          name: "bash",
          arguments: JSON.stringify({ command: "npm test" }),
          display: { kind: "command", capability: "command.bash" },
          result: JSON.stringify({ exit_code: 0 }),
        },
        makeFinalAnswer("done"),
      ]),
    );

    expect(view.querySelector("button:has(.lucide-square-terminal)")).toBeNull();
    expect(view.querySelectorAll(".agent-message-actions button")).toHaveLength(2);
  });

  it("keeps the failed reconnect card outside the collapsible process", () => {
    vi.useFakeTimers();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    const turn = makeTurn(
      "failed",
      [
        makeCommentary("partial work"),
        {
          id: "reconnect-1",
          type: "stream_reconnect",
          status: "failed",
          text: "connection reset by peer",
          reason: "network",
          retry_count: 5,
          max_retries: 5,
        },
      ],
      "network down",
    );
    act(() => {
      root!.render(
        <TurnView
          turn={turn}
          onStreamFrame={() => {}}
          isLatestTurn
        />,
      );
    });
    act(() => {
      vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS);
    });

    // Failures remain visible even when the process fold is closed.
    const notice = container!.querySelector("aside.turn-failure");
    expect(notice?.querySelector(".turn-failure-title")?.textContent).toBe(t("turnFailure.network"));
    expect(notice?.closest(".assistant-turn-shell")).toBeNull();
    // It stands in for the generic turn error notice.
    expect(container!.querySelectorAll("aside")).toHaveLength(1);
  });
});

it("removes the recovery card immediately on item removal without duplicating stream status", () => {
  const item: ThreadItem = { id: "retry", type: "stream_reconnect", status: "in_progress", reason: "network", retry_at_ms: Date.now() + 5000 };
  const turn = makeTurn("in_progress", [makeCommentary("Working"), item]);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => { root!.render(<TurnView turn={turn} isLatestTurn onStreamFrame={() => {}} streamStatus={{ text: "reconnecting", liveProgress: true }} />); });
  expect(container.querySelectorAll(".turn-failure.is-retrying")).toHaveLength(1);
  expect(container.querySelector(".stream-status-notice")).toBeNull();
  act(() => { root!.render(<TurnView turn={{ ...turn, items: [turn.items[0]] }} isLatestTurn onStreamFrame={() => {}} />); });
  expect(container.querySelector(".turn-failure")).toBeNull();
});

it("keeps the retrying card's node when automatic recovery gives up", () => {
  const item: ThreadItem = { id: "retry", type: "stream_reconnect", status: "in_progress", reason: "network", retry_at_ms: Date.now() + 5000 };
  const turn = makeTurn("in_progress", [makeCommentary("Working"), item]);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => { root!.render(<TurnView turn={turn} isLatestTurn onStreamFrame={() => {}} />); });
  const card = container.querySelector(".turn-failure.is-retrying");
  expect(card).not.toBeNull();
  act(() => { root!.render(<TurnView turn={{ ...turn, status: "interrupted", items: [turn.items[0], { ...item, status: "failed" }] }} isLatestTurn onStreamFrame={() => {}} />); });
  expect(container.querySelectorAll("aside.turn-failure")).toHaveLength(1);
  expect(container.querySelector("aside.turn-failure")).toBe(card);
  expect(card!.getAttribute("role")).toBe("alert");
});

// Automatic recovery that gives up settles the turn as interrupted, not
// failed; its card explains the same failure and needs the same retry.
it.each(["failed", "interrupted"] as const)("routes the retry of a turn that ended %s through the existing history retry action", async (status) => {
  const user: ThreadItem = {
    id: "user", type: "user_message", status: "completed", text: "Display text", input_text: "Check this",
    images: [{ media_type: "image/png", data: "image-bytes" }],
    files: [{ media_type: "text/plain", data: "file-bytes", filename: "log.txt" }],
    content_parts: [{ type: "text", text: "Check this" }],
  };
  const item: ThreadItem = { id: "retry", type: "stream_reconnect", status: "failed", reason: "network" };
  const onEditMessage = vi.fn();
  const onSubmitEditMessage = vi.fn();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  const turn = makeTurn(status, [user, item]);
  act(() => { root!.render(<ImagePreviewProvider><TurnView turn={turn} isLatestTurn onEditMessage={onEditMessage} onSubmitEditMessage={onSubmitEditMessage} onStreamFrame={() => {}} /></ImagePreviewProvider>); });
  const retryButton = () => [...container!.querySelectorAll<HTMLButtonElement>(".turn-failure-recovery button")]
    .find((button) => button.textContent === t("appState.retryAction"));
  await act(async () => { retryButton()?.click(); });
  expect(onSubmitEditMessage).toHaveBeenCalledWith(turn.id, user, user.input_text, user.images, user.files, user.content_parts);
  expect(onEditMessage).not.toHaveBeenCalled();
  act(() => { root!.render(<ImagePreviewProvider><TurnView turn={turn} onEditMessage={onEditMessage} onStreamFrame={() => {}} /></ImagePreviewProvider>); });
  expect(retryButton()).toBeUndefined();
});

// The thread keeps what an interrupted turn already wrote. Resending the
// message would rewind past it, so the card continues from it instead.
it("continues a turn that already wrote part of its reply instead of resending it", async () => {
  const user: ThreadItem = { id: "user", type: "user_message", status: "completed", text: "Write the report" };
  const partial: ThreadItem = { id: "partial", type: "agent_message", status: "completed", text: "First half of the report" };
  const item: ThreadItem = { id: "retry", type: "stream_reconnect", status: "failed", reason: "incomplete_stream" };
  const onSubmitEditMessage = vi.fn();
  const continued = vi.fn((event: Event) => {
    event.preventDefault();
    (event as CustomEvent<ContinueTurnDetail>).detail.done(true);
  });
  window.addEventListener(CONTINUE_TURN_EVENT, continued);
  try {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    const turn = makeTurn("interrupted", [user, partial, item]);
    act(() => { root!.render(<TurnView turn={turn} threadID="thread-1" isLatestTurn onEditMessage={vi.fn()} onSubmitEditMessage={onSubmitEditMessage} onStreamFrame={() => {}} />); });
    const labels = [...container.querySelectorAll<HTMLButtonElement>(".turn-failure-recovery button")].map((button) => button.textContent);
    expect(labels).toEqual([t("turnFailure.continue")]);
    await act(async () => { container!.querySelector<HTMLButtonElement>(".turn-failure-recovery button")!.click(); });
    expect(continued).toHaveBeenCalledOnce();
    expect((continued.mock.calls[0][0] as CustomEvent<ContinueTurnDetail>).detail).toMatchObject({ threadID: "thread-1", text: t("turnFailure.continuePrompt") });
    expect(onSubmitEditMessage).not.toHaveBeenCalled();
  } finally {
    window.removeEventListener(CONTINUE_TURN_EVENT, continued);
  }
});

it("sends a rejected credential to Model services from the latest turn only", () => {
  const error = {
    message: "stream request failed: HTTP 401: 401 Unauthorized",
    category: "auth" as const,
    status_code: 401,
    recovery: { attempt_count: 1, retry_count: 0, max_attempts: 11, submission_count: 1, stop_reason: "non_retryable", failure_category: "authentication" },
  };
  const turn: Turn = { ...makeTurn("failed", [{ id: "user", type: "user_message", status: "completed", text: "Check this" }]), error };
  const opened = vi.fn();
  window.addEventListener(OPEN_SETTINGS_EVENT, opened);
  try {
    const view = render(turn, true);
    const settings = [...view.querySelectorAll<HTMLButtonElement>(".turn-failure-recovery button")]
      .find((button) => button.textContent === t("turnFailure.openSettings"));
    act(() => settings?.click());
    expect(opened).toHaveBeenCalledOnce();
    expect((opened.mock.calls[0][0] as CustomEvent).detail).toEqual({ page: "providers" });
    rerender(turn, false);
    expect(view.querySelector(".turn-failure")).not.toBeNull();
    expect(view.querySelectorAll(".turn-failure-recovery button")).toHaveLength(0);
  } finally {
    window.removeEventListener(OPEN_SETTINGS_EVENT, opened);
  }
});

describe("TurnView optimistic placeholder", () => {
  it("shows the status label and live timer immediately for a just-sent optimistic turn", async () => {
    const { createOptimisticTurn } = await import("./ComposerMessages");
    const optimistic = createOptimisticTurn(
      { id: "queued-1", text: "帮我看看这个测试", images: [], files: [] },
      Date.now(),
    );
    const view = render(optimistic);

    // The user's message is visible right away.
    expect(view.textContent).toContain("帮我看看这个测试");
    // The process header mounts in the same frame — the user never faces
    // a bare hairline: label + elapsed timer are there from the start.
    expect(view.querySelector(".turn-process-title")?.textContent).toBe(
      "正在处理",
    );
    expect(view.querySelector(".turn-process-meta")?.textContent).toMatch(
      /^\d+s$/,
    );
  });
});


it("keeps click-to-answer elapsed through acknowledgement and a shorter server duration", async () => {
  const { createOptimisticTurn, reconcileOptimisticTurns } = await import("./ComposerMessages");
  const { forgetLocalTurnTiming } = await import("./LocalTurnTiming");
  vi.useFakeTimers();
  vi.setSystemTime(100_000);
  const optimistic = createOptimisticTurn({ id: "render-clock", text: "work", images: [], files: [] }, Date.now());
  const view = render(optimistic);
  await act(async () => { vi.advanceTimersByTime(8000); });
  expect(view.querySelector(".turn-process-meta")?.textContent).toBe("8s");
  const real: Turn = { ...optimistic, id: "render-real", started_at: new Date(107_000).toISOString() };
  reconcileOptimisticTurns([optimistic], [real]);
  rerender(real);
  expect(view.querySelector(".turn-process-meta")?.textContent).toBe("8s");
  await act(async () => { vi.advanceTimersByTime(2000); });
  rerender({ ...real, status: "completed", duration_ms: 3000, items: [...real.items, makeFinalAnswer("done")] });
  await act(async () => { vi.advanceTimersByTime(ASSISTANT_TURN_PRESENTATION_STABILIZE_MS); });
  expect(view.querySelector(".turn-process-title")?.textContent).toContain("10 秒");
  await act(async () => { vi.advanceTimersByTime(5000); });
  expect(view.querySelector(".turn-process-title")?.textContent).toContain("10 秒");
  forgetLocalTurnTiming(optimistic.id);
});
