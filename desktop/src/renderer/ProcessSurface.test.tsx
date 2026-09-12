/**
 * Tests for `ProcessSurface`.
 *
 * The headline assertion of this file is "same DOM node from first tool
 * call to final settle". ProcessSurface owns the entire 1 → N → settled
 * lifecycle in a single component, so the assertion below guards the
 * gray process row against remounting mid-stream.
 */
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { ProcessSurface } from "./ProcessSurface";
import type { ThreadItem } from "../shared/protocol";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { modelMascotAccessory } from "./WuuMascot";

beforeAll(() => {
  // jsdom does not lay out real heights. Stub getBoundingClientRect so
  // React's effects do not crash on layout queries.
  Element.prototype.getBoundingClientRect = function (): DOMRect {
    return {
      x: 0,
      y: 0,
      top: 0,
      left: 0,
      right: 0,
      bottom: 0,
      width: 0,
      height: 0,
      toJSON() {
        return this;
      },
    } as DOMRect;
  };
});

function makeReadFile(
  id: string,
  path: string,
  status: ThreadItem["status"] = "completed",
): ThreadItem {
  return {
    id,
    type: "tool_call",
    status,
    name: "read_file",
    arguments: JSON.stringify({ path }),
  };
}

function makeReasoning(
  id: string,
  text: string,
  status: ThreadItem["status"] = "completed",
): ThreadItem {
  return {
    id,
    type: "reasoning",
    status,
    text,
  };
}

let container: HTMLDivElement | null = null;
let root: Root | null = null;

type SurfaceProps = Parameters<typeof ProcessSurface>[0];

function render(
  props: SurfaceProps,
): { container: HTMLDivElement } {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<ProcessSurface {...props} /> as ReactElement);
  });
  return { container };
}

function rerender(props: SurfaceProps): void {
  act(() => {
    root!.render(<ProcessSurface {...props} /> as ReactElement);
  });
}

function unmount(): void {
  if (root) {
    act(() => {
      root!.unmount();
    });
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
}

function setProcessFoldOpen(
  details: HTMLDetailsElement | null,
  open: boolean,
): void {
  act(() => {
    if (!details) return;
    details.open = open;
    details.dispatchEvent(new Event("toggle", { bubbles: true }));
  });
}

afterEach(() => {
  unmount();
  desktopPluginHost.unload("test:tool-activity-presenter");
  desktopPluginHost.unload("test:process-wrapper");
  desktopPluginHost.unload("test:process-replacement");
});

describe("ProcessSurface", () => {
  it("nests the single-tool presenter inside the complete process boundary", async () => {
    await desktopPluginHost.activateGeneration({
      pluginId: "test:tool-activity-presenter",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({
          id: "read",
          key: "activity.read",
          render: () => <span data-tool-presenter-root>tool</span>,
        });
      },
    });
    await desktopPluginHost.activateGeneration({
      pluginId: "test:process-wrapper",
      generation: "one",
      register(api) {
        api.registerPresenter({
          id: "wrapper",
          target: "conversation.process",
          key: "tool-group",
          mode: "wrap",
          render: ({ fallback }) => <section data-process-wrapper>{fallback}</section>,
        });
      },
    });
    const item = makeReadFile("tool-1", "a.ts");
    item.display = { capability: "activity.read" };
    const { container } = render({ processItems: [item], streaming: false });

    expect(container.querySelector("[data-process-wrapper] [data-tool-presenter-root]")).not.toBeNull();

    await act(async () => desktopPluginHost.activateGeneration({
      pluginId: "test:process-replacement",
      generation: "one",
      register(api) {
        api.registerPresenter({
          id: "replacement",
          target: "conversation.process",
          key: "tool-group",
          render: () => <strong data-process-replacement>process</strong>,
        });
      },
    }));

    expect(container.querySelector("[data-process-wrapper] [data-process-replacement]")?.textContent).toBe("process");
    expect(container.querySelector("[data-tool-presenter-root]")).toBeNull();
  });

  it("lets a matching presenter replace the complete single-tool surface root", async () => {
    await desktopPluginHost.activateGeneration({
      pluginId: "test:tool-activity-presenter",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({
          id: "read",
          key: "activity.read",
          render: ({ activity }) => (
            <section data-tool-presenter-root>{activity.toolName}</section>
          ),
        });
      },
    });
    const item = makeReadFile("tool-1", "a.ts");
    item.display = { capability: "activity.read" };
    const { container } = render({ processItems: [item], streaming: false });

    expect(container.querySelector("section[data-tool-presenter-root]")?.textContent).toBe("read_file");
    expect(container.querySelector(".process-surface")).toBeNull();
  });

  it("dispatches matching presenter rows in an expanded multi-tool timeline", async () => {
    await desktopPluginHost.activateGeneration({
      pluginId: "test:tool-activity-presenter",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({
          id: "read",
          key: "activity.read",
          render: ({ activity }) => <div data-timeline-presenter={activity.id} />,
        });
      },
    });
    const presented = makeReadFile("tool-1", "a.ts");
    presented.display = { capability: "activity.read" };
    const native = makeReadFile("tool-2", "b.ts");
    const { container } = render({ processItems: [presented, native], streaming: false });
    setProcessFoldOpen(container.querySelector("details"), true);

    expect(container.querySelector("[data-timeline-presenter=tool-1]")).toBeTruthy();
    expect(container.querySelectorAll(".activity-timeline-item")).toHaveLength(1);
  });

  it("keeps the same surface node when items grow from one to many (no unmount)", () => {
    const { container: initialContainer } = render({
      processItems: [makeReadFile("tool-1", "a.ts")],
      streaming: true,
    });
    const surfaceBefore = initialContainer.querySelector(".process-surface");
    const rowBefore = initialContainer.querySelector(".process-surface-row");
    expect(surfaceBefore).toBeTruthy();
    expect(rowBefore).toBeTruthy();

    rerender({
      processItems: [
        makeReadFile("tool-1", "a.ts"),
        makeReadFile("tool-2", "b.ts"),
      ],
      streaming: true,
    });
    const surfaceAfter = initialContainer.querySelector(".process-surface");
    const rowAfter = initialContainer.querySelector(".process-surface-row");

    // Same DOM node, not just same selector — proves React reused the
    // component instance instead of unmounting and remounting.
    expect(surfaceAfter).toBe(surfaceBefore);
    expect(rowAfter).toBe(rowBefore);
  });

  it("renders one compact row for a single in-progress tool with no body", () => {
    const { container } = render({
      processItems: [makeReadFile("tool-1", "a.ts", "in_progress")],
      streaming: true,
    });
    const details = container.querySelector("details.process-surface-fold");
    expect(details).toBeTruthy();
    expect(details?.classList.contains("no-details")).toBe(true);
    expect(details?.hasAttribute("open")).toBe(false);
    expect(container.querySelector(".process-surface-body")).toBeNull();
    const surface = container.querySelector("div.process-surface");
    expect(surface).toBeTruthy();
    expect(surface?.classList.contains("is-streaming")).toBe(true);
    const row = container.querySelector(".process-surface-row");
    expect(row).toBeTruthy();
    expect(container.querySelector(".process-surface-chevron")).toBeNull();
  });

  it("renders a collapsed fold when there are multiple tools", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "in_progress"),
      ],
      streaming: true,
    });
    const details = container.querySelector("details.process-surface-fold");
    expect(details).toBeTruthy();
    expect(details?.hasAttribute("open")).toBe(false);
  });

  it("applies is-streaming class on the row while the surface is streaming", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "in_progress"),
        makeReadFile("tool-2", "b.ts", "in_progress"),
      ],
      streaming: true,
    });
    const row = container.querySelector(".process-surface-row");
    expect(row?.classList.contains("is-streaming")).toBe(true);
  });

  it("removes is-streaming class once the surface settles", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
      ],
      streaming: false,
    });
    const row = container.querySelector(".process-surface-row");
    expect(row?.classList.contains("is-streaming")).toBe(false);
  });

  it("sweeps an active settled row without marking its contents streaming", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReasoning("reason-1", "settled thinking", "completed"),
      ],
      streaming: false,
      active: true,
    });
    const row = container.querySelector(".process-surface-row");
    expect(row?.classList.contains("is-live-gray")).toBe(true);
    expect(row?.classList.contains("is-streaming")).toBe(false);
    const label = container.querySelector(".process-surface-reasoning-label");
    expect(label?.textContent).toBe("思考过程");
  });

  it("does not surface intermediate tool failures in the conversation", () => {
    const failedTool = {
      ...makeReadFile("tool-1", "a.ts", "failed"),
      error: "command failed",
    };
    const { container } = render({
      processItems: [
        failedTool,
        makeReasoning("reason-1", "recovering", "in_progress"),
      ],
      streaming: true,
      active: false,
    });
    const row = container.querySelector(".process-surface-row");
    expect(row?.classList.contains("is-streaming")).toBe(true);
    expect(row?.classList.contains("is-live-gray")).toBe(false);
    expect(row?.classList.contains("failed")).toBe(false);

    rerender({
      processItems: [
        failedTool,
        makeReasoning("reason-1", "recovered", "completed"),
      ],
      streaming: false,
    });
    expect(
      container
        .querySelector(".process-surface-row")
        ?.classList.contains("failed"),
    ).toBe(false);
    expect(container.querySelector(".activity-detail-error")).toBeNull();
    expect(container.textContent).not.toContain("命令执行失败");
  });

  it("keeps the fold collapsed when streaming starts", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "in_progress"),
      ],
      streaming: false,
    });
    const detailsBefore = container.querySelector(
      "details.process-surface-fold",
    );
    expect(detailsBefore?.hasAttribute("open")).toBe(false);

    rerender({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "in_progress"),
      ],
      streaming: true,
    });
    const detailsAfter = container.querySelector(
      "details.process-surface-fold",
    );
    expect(detailsAfter?.hasAttribute("open")).toBe(false);
  });

  it("renders the reasoning summary segment with '思考过程' when settled", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReasoning("reason-1", "thinking aloud", "completed"),
      ],
      streaming: false,
    });
    const label = container.querySelector(
      ".process-surface-reasoning-label",
    );
    expect(label?.textContent).toBe("思考过程");
  });

  it("renders '正在思考' while reasoning is still in progress", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReasoning("reason-1", "thinking aloud", "in_progress"),
      ],
      streaming: true,
    });
    const label = container.querySelector(
      ".process-surface-reasoning-label",
    );
    expect(label?.textContent).toBe("正在思考");
  });

  it("keeps the previous process label briefly while the new label enters", async () => {
    const { container } = render({
      processItems: [makeReadFile("tool-1", "a.ts", "in_progress")],
      streaming: true,
    });
    expect(
      container.querySelector(".process-text-motion-current")?.textContent,
    ).toBe("查看 a.ts");

    rerender({
      processItems: [makeReadFile("tool-1", "b.ts", "in_progress")],
      streaming: true,
    });

    expect(
      container.querySelector(".process-text-motion-current")?.textContent,
    ).toBe("查看 b.ts");
    expect(
      container.querySelector(".process-text-motion-exit")?.textContent,
    ).toBe("查看 a.ts");

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 220));
    });

    expect(container.querySelector(".process-text-motion-exit")).toBeNull();
  });

  it("animates reasoning label changes from active to settled", () => {
    const { container } = render({
      processItems: [makeReasoning("reason-1", "thinking aloud", "in_progress")],
      streaming: true,
    });
    expect(
      container.querySelector(".process-text-motion-current")?.textContent,
    ).toBe("正在思考");

    rerender({
      processItems: [makeReasoning("reason-1", "thinking aloud", "completed")],
      streaming: false,
    });

    expect(
      container.querySelector(".process-text-motion-current")?.textContent,
    ).toBe("思考过程");
    expect(
      container.querySelector(".process-text-motion-exit")?.textContent,
    ).toBe("正在思考");
  });

  it("preserves the user expand choice after they toggle", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "in_progress"),
        makeReadFile("tool-2", "b.ts", "in_progress"),
      ],
      streaming: true,
    });
    const details = container.querySelector(
      "details.process-surface-fold",
    ) as HTMLDetailsElement | null;
    expect(details?.open).toBe(false);

    // User clicks the summary to expand.
    setProcessFoldOpen(details, true);
    expect(details?.open).toBe(true);

    // Streaming flips off; the fold should NOT auto-reopen or
    // auto-collapse, because the user has interacted.
    rerender({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
      ],
      streaming: false,
    });
    const detailsAfter = container.querySelector(
      "details.process-surface-fold",
    ) as HTMLDetailsElement | null;
    expect(detailsAfter?.open).toBe(true);
  });

  it("renders a count segment for multiple files of the same kind", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
        makeReadFile("tool-3", "c.ts", "completed"),
      ],
      streaming: false,
    });
    const count = container.querySelector(".process-surface-count");
    expect(count?.textContent).toBe("3");
  });

  it("condenses many mixed tool calls into one bounded summary sentence", () => {
    const { container } = render({
      processItems: [
        {
          id: "search-1",
          type: "tool_call",
          name: "grep",
          status: "failed",
          error: "search failed",
          arguments: JSON.stringify({ pattern: "cache_read" }),
        },
        {
          id: "search-2",
          type: "tool_call",
          name: "grep",
          status: "completed",
          arguments: JSON.stringify({ pattern: "cache_write" }),
        },
        makeReadFile("read-1", "session.ts"),
        makeReadFile("read-2", "storage.ts"),
        {
          id: "command-1",
          type: "tool_call",
          name: "bash",
          status: "completed",
          arguments: JSON.stringify({ command: "sqlite3 sessions.db" }),
        },
        {
          id: "schedule-1",
          type: "tool_call",
          name: "cron",
          status: "completed",
          arguments: JSON.stringify({ action: "list" }),
        },
      ],
      streaming: false,
    });

    const summary = container.querySelector(".process-surface-summary-line");
    expect(summary?.textContent).toBe(
      "已完成 6 项操作，包括搜索、查看文件等",
    );
    expect(summary?.textContent).not.toContain("未完成");
    expect(summary?.querySelectorAll(".process-surface-segment")).toHaveLength(0);
  });

  it("keeps a useful count when many calls are all the same kind", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts"),
        makeReadFile("tool-2", "b.ts"),
        makeReadFile("tool-3", "c.ts"),
        makeReadFile("tool-4", "d.ts"),
      ],
      streaming: false,
    });

    expect(
      container.querySelector(".process-surface-summary-line")?.textContent,
    ).toBe("查看 4 个文件");
  });

  it("reports the current phase instead of appending reasoning to a long summary", () => {
    const { container } = render({
      processItems: [
        makeReadFile("read-1", "a.ts"),
        makeReadFile("read-2", "b.ts"),
        {
          id: "search-1",
          type: "tool_call",
          name: "grep",
          status: "completed",
          arguments: JSON.stringify({ pattern: "cache" }),
        },
        {
          id: "command-1",
          type: "tool_call",
          name: "bash",
          status: "completed",
          arguments: JSON.stringify({ command: "npm test" }),
        },
        makeReasoning("reason-1", "checking results", "in_progress"),
      ],
      streaming: true,
    });

    expect(
      container.querySelector(".process-surface-summary-line")?.textContent,
    ).toBe("完成 4 项操作后，正在思考");
    const summary = container.querySelector(".process-surface-summary-line");
    const blobatar = summary?.querySelector<SVGSVGElement>(
      "svg.process-surface-blobatar",
    );
    expect(blobatar).not.toBeNull();
    expect(summary?.firstElementChild).toBe(blobatar);
    expect(blobatar?.tagName.toLowerCase()).toBe("svg");
  });

  it("keeps the blobatar off a settled synthesized toolcall row", () => {
    const { container } = render({
      processItems: [makeReadFile("tool-1", "a.ts", "completed")],
      streaming: false,
    });

    expect(
      container.querySelector(".process-surface-summary-line .process-surface-blobatar"),
    ).toBeNull();
  });

  it("keeps model accessories on the blobatar sibling, not inside the text clip", () => {
    const model = "claude-sonnet-4";
    const { container } = render({
      processItems: [makeReadFile("tool-1", "a.ts", "in_progress")],
      streaming: true,
      model,
    });

    const line = container.querySelector(".process-surface-summary-line");
    const blobatar = line?.querySelector("svg.process-surface-blobatar");
    const text = line?.querySelector(".process-surface-summary-text");
    expect(blobatar).not.toBeNull();
    expect(blobatar?.nextElementSibling).toBe(text);
    expect(text?.querySelector(".process-surface-blobatar")).toBeNull();
    expect(modelMascotAccessory(model)).toBe("top-hat");
    expect(blobatar?.getAttribute("data-wuu-mascot-accessory")).toBe("top-hat");
  });

  it("anchors the text sweep to the text wrapper so the blobatar does not offset it", () => {
    const { container } = render({
      processItems: [makeReadFile("tool-1", "a.ts", "completed")],
      streaming: false,
      active: true,
    });

    const line = container.querySelector(".process-surface-summary-line");
    expect(line?.classList.contains("wuu-live-text-wave")).toBe(false);
    const text = container.querySelector(
      ".process-surface-summary-text.wuu-live-text-wave",
    );
    expect(text).not.toBeNull();
    expect(text?.querySelector(".process-surface-blobatar")).toBeNull();
    expect(text?.getAttribute("data-text")).toBe(text?.textContent);
  });

  it("shows the fixed round blobatar while reasoning without tool calls", () => {
    const { container } = render({
      processItems: [makeReasoning("reason-1", "thinking", "in_progress")],
      streaming: true,
    });

    const summary = container.querySelector(".process-surface-summary-line");
    expect(
      summary?.querySelector("svg.process-surface-blobatar"),
    ).not.toBeNull();
    expect(summary?.textContent).toBe("正在思考");
  });

  it("morphs the process-row blobatar across activity switches instead of replacing it", () => {
    const { container } = render({
      processItems: [makeReasoning("reason-1", "thinking", "in_progress")],
      streaming: true,
    });
    const svg = container.querySelector<SVGSVGElement>(
      "svg.process-surface-blobatar",
    );
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("data-wuu-mascot-activity")).toBe("thinking");
    expect(container.querySelector(".wuu-mascot-activity-prop-thinking")).not.toBeNull();

    rerender({
      processItems: [
        makeReasoning("reason-1", "thinking", "completed"),
        {
          id: "edit-1",
          type: "tool_call",
          name: "edit_file",
          status: "in_progress",
          arguments: JSON.stringify({ path: "a.ts" }),
        },
      ],
      streaming: true,
    });

    const next = container.querySelector<SVGSVGElement>(
      "svg.process-surface-blobatar",
    );
    expect(next).toBe(svg);
    expect(next?.getAttribute("data-wuu-mascot-activity")).toBe("edit");
    expect(container.querySelector(".wuu-mascot-activity-prop-thinking")).toBeNull();
    expect(container.querySelector(".wuu-mascot-activity-prop-edit")).not.toBeNull();
  });

  it("marks the count is-changing for ~180ms when the value changes", async () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
      ],
      streaming: false,
    });
    const countBefore = container.querySelector(".process-surface-count");
    expect(countBefore?.textContent).toBe("2");
    expect(countBefore?.classList.contains("is-changing")).toBe(false);

    rerender({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
        makeReadFile("tool-3", "c.ts", "completed"),
      ],
      streaming: false,
    });
    const countDuring = container.querySelector(".process-surface-count");
    expect(countDuring?.textContent).toBe("3");
    expect(countDuring?.classList.contains("is-changing")).toBe(true);

    // Past the 180ms window, the class is cleared.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 220));
    });
    const countAfter = container.querySelector(".process-surface-count");
    expect(countAfter?.classList.contains("is-changing")).toBe(false);
  });

  it("uses the renderReasoningItem callback for reasoning items in the body", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
        makeReasoning("reason-1", "thinking aloud", "in_progress"),
      ],
      streaming: true,
      renderReasoningItem: (item, isStreaming) => (
        <span
          data-testid="reasoning-mock"
          data-streaming={String(isStreaming)}
        >
          {item.id}
        </span>
      ),
    });
    const mocked = container.querySelectorAll(
      '[data-testid="reasoning-mock"]',
    );
    expect(mocked.length).toBe(1);
    expect(mocked[0].getAttribute("data-streaming")).toBe("true");
  });

  it("omits reasoning body items when renderReasoningItem is not provided", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
        makeReasoning("reason-1", "thinking aloud", "completed"),
      ],
      streaming: false,
    });
    const list = container.querySelector(
      ".process-surface-reasoning-list",
    );
    expect(list).toBeNull();
    // Summary still mentions 思考过程.
    const label = container.querySelector(
      ".process-surface-reasoning-label",
    );
    expect(label?.textContent).toBe("思考过程");
  });

  describe("fold body scroll container", () => {
    type ScrollLayout = {
      scrollHeight: number;
      clientHeight: number;
      scrollTop: number;
    };

    function stubScrollLayout(
      node: HTMLElement,
      opts: Partial<ScrollLayout>,
    ): ScrollLayout {
      const layout: ScrollLayout = {
        scrollHeight: opts.scrollHeight ?? 1000,
        clientHeight: opts.clientHeight ?? 200,
        scrollTop: opts.scrollTop ?? 0,
      };
      Object.defineProperty(node, "scrollHeight", {
        configurable: true,
        get: () => layout.scrollHeight,
      });
      Object.defineProperty(node, "clientHeight", {
        configurable: true,
        get: () => layout.clientHeight,
      });
      Object.defineProperty(node, "scrollTop", {
        configurable: true,
        get: () => layout.scrollTop,
        set: (value) => {
          layout.scrollTop = value;
        },
      });
      return layout;
    }

    it("renders reasoning items inside the bounded fold body", () => {
      const { container } = render({
        processItems: [makeReasoning("reason-1", "thinking aloud", "in_progress")],
        streaming: true,
        renderReasoningItem: (item) => (
          <span data-testid="reasoning-mock">{item.id}</span>
        ),
      });
      const body = container.querySelector(".process-surface-body");
      expect(body).toBeTruthy();
      const items = body?.querySelectorAll(
        ".process-surface-reasoning-item",
      );
      expect(items?.length).toBe(1);
      // The mock item is wrapped inside the reasoning item slot.
      const mock = body?.querySelector('[data-testid="reasoning-mock"]');
      expect(mock?.textContent).toBe("reason-1");
    });

    it("snaps the fold body to the bottom when the fold opens", async () => {
      const { container } = render({
        processItems: [makeReasoning("reason-1", "thinking aloud", "completed")],
        streaming: true,
        renderReasoningItem: (item) => <span>{item.id}</span>,
      });
      const details = container.querySelector(
        "details.process-surface-fold",
      ) as HTMLDetailsElement | null;
      expect(details).toBeTruthy();
      const body = details?.querySelector(
        ".process-surface-body",
      ) as HTMLElement | null;
      expect(body).toBeTruthy();

      // jsdom does not lay out real heights — mock the scroll geometry
      // so snap-to-bottom has measurable values to read.
      const layout = stubScrollLayout(body!, {
        scrollHeight: 1000,
        clientHeight: 200,
      });

      setProcessFoldOpen(details, true);

      // The fold-open snap fires ~280ms after opening. Wait it out.
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 320));
      });

      expect(layout.scrollTop).toBe(1000);
    });

    it("snaps again on every fold re-open so the user always lands on the latest reasoning", async () => {
      const { container } = render({
        processItems: [makeReasoning("reason-1", "thinking aloud", "completed")],
        streaming: true,
        renderReasoningItem: (item) => <span>{item.id}</span>,
      });
      const details = container.querySelector(
        "details.process-surface-fold",
      ) as HTMLDetailsElement | null;
      const body = details?.querySelector(
        ".process-surface-body",
      ) as HTMLElement | null;
      const layout = stubScrollLayout(body!, {
        scrollHeight: 1000,
        clientHeight: 200,
      });

      // First snap.
      setProcessFoldOpen(details, true);
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 320));
      });
      expect(layout.scrollTop).toBe(1000);

      // User scrolls up to read earlier reasoning.
      act(() => {
        body!.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
      });
      layout.scrollTop = 240;
      act(() => {
        body!.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
      });

      // User collapses then re-expands the fold; the scroll should
      // snap back to the latest reasoning so they land on it instead
      // of where they were when they collapsed.
      setProcessFoldOpen(details, false);
      setProcessFoldOpen(details, true);
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 320));
      });

      expect(layout.scrollTop).toBe(1000);
    });
  });
});
