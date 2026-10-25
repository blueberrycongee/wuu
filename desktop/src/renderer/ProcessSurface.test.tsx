/**
 * Tests for `ProcessSurface`.
 *
 * The headline assertion of this file is "same DOM node from first tool
 * call to final settle". ProcessSurface owns the entire 1 → N → settled
 * lifecycle in a single component, so the assertion below guards the
 * gray process row against remounting mid-stream.
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, beforeAll, describe, expect, it, vi } from "vitest";
import { ProcessSurface } from "./ProcessSurface";
import { ContextCompactionNotice } from "./TurnNotice";
import type { ThreadItem } from "../shared/protocol";
import { ConversationControlsTestProvider } from "./ConversationControlsTestProvider";
import type { UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
let composition: UICompositionV1 = { Slot: ({ fallback }) => <>{fallback}</> };
import { modelMascotAccessory } from "./WuuMascot";

beforeEach(() => {
  window.wuu = { ...window.wuu, reportPluginDesktopActivation: vi.fn().mockResolvedValue(undefined) };
});

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

function makeBrowserNavigate(
  id: string,
  url: string,
  status: ThreadItem["status"] = "completed",
): ThreadItem {
  return {
    id,
    type: "tool_call",
    status,
    name: "browser",
    arguments: JSON.stringify({ action: "navigate", url }),
  };
}

it("keeps source actions beside the aggregated disclosure without nesting buttons", () => {
  const results = Array.from({ length: 8 }, (_, i) => ({ title: `Source ${i}`, url: `https://site-${i}.example.com/article` }));
  const item: ThreadItem = { id: "web-sources", type: "tool_call", name: "web_search", status: "completed", arguments: '{"query":"motion"}', result: JSON.stringify({ results }) };
  const onOpenURL = vi.fn();
  const { container } = render({ processItems: [item], streaming: false, onOpenURL });
  const header = container.querySelector(".process-surface-inline-controls")!;
  expect(header).not.toBeNull();
  const source = header.querySelector<HTMLButtonElement>(".web-source-link")!;
  expect(source.closest("summary")).toBeNull();
  expect(header.querySelector("summary .web-source-link")).toBeNull();
  act(() => source.click());
  expect(onOpenURL).toHaveBeenCalledExactlyOnceWith(results[0].url);
  expect(header.querySelector("details")?.open).toBe(false);
  const summary = header.querySelector<HTMLElement>("summary")!;
  act(() => summary.click());
  act(() => header.querySelector("details")!.dispatchEvent(new Event("toggle")));
  expect(header.querySelector("details")?.open).toBe(true);
  expect(header.querySelectorAll(".web-research-expanded .web-source-link")).toHaveLength(8);
  expect(header.querySelector(".process-surface-tool-list")).not.toBeNull();
  const chips = header.querySelectorAll<HTMLButtonElement>(".web-research-expanded .web-source-link");
  act(() => { chips[0].focus(); chips[0].dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
  expect(header.querySelector("details")?.open).toBe(false);
  expect(document.activeElement).toBe(summary);
  act(() => summary.click());
  act(() => header.querySelector("details")!.dispatchEvent(new Event("toggle")));
  expect(header.querySelector("details")?.open).toBe(true);
  expect(header.querySelectorAll(".web-research-expanded .web-source-link")).toHaveLength(8);
  expect(header.querySelectorAll(".process-surface-tool-list")).toHaveLength(1);
  const first = header.querySelector<HTMLButtonElement>(".web-research-expanded .web-source-link")!;
  act(() => { first.focus(); first.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
  expect(document.activeElement).toBe(summary);
});

it("keeps its disclosure node and source receipt ledger when live search results arrive", () => {
  const searching: ThreadItem = { id: "live-web", type: "tool_call", name: "web_search", status: "in_progress", arguments: '{"query":"motion"}' };
  const { container } = render({ processItems: [searching], streaming: true, active: true });
  const fold = container.querySelector(".process-surface-fold");
  const summary = container.querySelector(".process-surface-row");
  rerender({ processItems: [{ ...searching, status: "completed", result: JSON.stringify({ results: [{ title: "Motion", url: "https://example.com/motion" }] }) }], streaming: true, active: true });
  expect(container.querySelector(".process-surface-fold")).toBe(fold);
  expect(container.querySelector(".process-surface-row")).toBe(summary);
  expect(container.querySelector(".web-source-avatar.is-arriving")).not.toBeNull();
});

it("announces a running compaction from the summary only while details are closed", () => {
  const compaction: ThreadItem = { id: "compact-live", type: "context_compaction", status: "in_progress" };
  const { container } = render({
    processItems: [compaction], streaming: true,
    renderProcessItem: item => <ContextCompactionNotice status={item.status} />,
  });
  const indicator = container.querySelector(".context-compaction-indicator")!;
  expect(indicator.getAttribute("role")).toBe("status");
  expect(indicator.getAttribute("aria-live")).toBe("polite");

  const details = container.querySelector<HTMLDetailsElement>(".process-surface-fold")!;
  act(() => {
    details.open = true;
    details.dispatchEvent(new Event("toggle"));
  });
  expect(indicator.getAttribute("role")).toBe("img");
  expect(indicator.hasAttribute("aria-live")).toBe(false);
  expect(container.querySelector(".context-compaction-notice")?.getAttribute("aria-live")).toBe("polite");
});

it.each([
  { name: "mcp_example_computer", arguments: { display_title: "Inspect target", action: "observe" }, source: JSON.stringify({ display_title: "Inspect target", action: "observe" }, null, 2) },
  { name: "exec", arguments: { command: "git status --short" }, source: "git status --short" },
  { name: "run_code", arguments: { code: "await tools.read_file({path: 'notes.md'});" }, source: "await tools.read_file({path: 'notes.md'});" },
])("keeps a single $name source disclosure reachable", ({ name, arguments: args, source }) => {
  const item: ThreadItem = { id: "source", type: "tool_call", name, status: "completed", arguments: JSON.stringify(args) };
  const { container } = render({ processItems: [item], streaming: false });
  const details = container.querySelector<HTMLDetailsElement>(".process-surface-fold");
  expect(details).not.toBeNull();
  act(() => {
    details!.open = true;
    details!.dispatchEvent(new Event("toggle"));
  });
  expect(details!.querySelector("pre code")?.textContent).toBe(source);
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

function makeSearch(
  id: string,
  pattern: string,
  status: ThreadItem["status"] = "in_progress",
): ThreadItem {
  return {
    id,
    type: "tool_call",
    status,
    name: "grep",
    arguments: JSON.stringify({ pattern }),
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
    root!.render(<ConversationControlsTestProvider composition={composition}><ProcessSurface {...props} /></ConversationControlsTestProvider>);
  });
  return { container };
}

function rerender(props: SurfaceProps): void {
  act(() => {
    root!.render(<ConversationControlsTestProvider composition={composition}><ProcessSurface {...props} /></ConversationControlsTestProvider>);
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
  composition = { Slot: ({ fallback }) => <>{fallback}</> };
});

describe("ProcessSurface", () => {
  it("opens a compact browser activity from the process summary", () => {
    const onOpenURL = vi.fn();
    const { container } = render({
      processItems: [makeBrowserNavigate("browser-1", "http://app.local:3000")],
      streaming: false,
      onOpenURL,
    });
    const summary = container.querySelector(".process-surface-row") as HTMLElement | null;
    expect(summary).not.toBeNull();
    act(() => {
      summary?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(onOpenURL).toHaveBeenCalledWith("http://app.local:3000", undefined);
  });

  it("lets the public process slot replace the complete single-tool surface", () => {
    composition = { Slot: ({ name, fallback }) => name.endsWith("/conversation.process") ? <section data-process-replacement>process</section> : <>{fallback}</> };
    const { container } = render({ processItems: [makeReadFile("tool-1", "a.ts")], streaming: false });
    expect(container.querySelector("[data-process-replacement]")?.textContent).toBe("process");
    expect(container.querySelector(".process-surface")).toBeNull();
  });

  it("dispatches public tool slots in an expanded multi-tool timeline", () => {
    composition = { Slot: ({ name, context, fallback }) => name.endsWith("/conversation.tool-activity") && context?.itemId === "tool-1" ? <div data-timeline-presenter="tool-1" /> : <>{fallback}</> };
    const { container } = render({ processItems: [makeReadFile("tool-1", "a.ts"), makeReadFile("tool-2", "b.ts")], streaming: false });
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
    expect(container.querySelector(".process-surface-summary-line")?.getAttribute("aria-label")).toContain("a.ts");
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

  it("keeps the latest tool in the summary when reasoning settles", () => {
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
    expect(label).toBeNull();
    expect(container.querySelector(".process-surface-summary-line")?.getAttribute("aria-label")).toContain("a.ts");
  });

  it("keeps the latest tool in the summary while reasoning continues", () => {
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
    expect(label).toBeNull();
    expect(container.querySelector(".process-surface-summary-line")?.getAttribute("aria-label")).toContain("a.ts");
  });

  it("keeps the previous process label briefly while the new label enters", async () => {
    const { container } = render({
      processItems: [makeReadFile("tool-1", "a.ts", "in_progress")],
      streaming: true,
    });
    expect(
      container.querySelector(".process-text-motion-current")?.textContent,
    ).toContain("a.ts");

    rerender({
      processItems: [makeReadFile("tool-1", "b.ts", "in_progress")],
      streaming: true,
    });

    expect(
      container.querySelector(".process-text-motion-current")?.textContent,
    ).toContain("b.ts");
    expect(
      container.querySelector(".process-text-motion-exit")?.textContent,
    ).toContain("a.ts");

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

  it("summarizes files in a settled group", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
        makeReadFile("tool-3", "c.ts", "completed"),
      ],
      streaming: false,
    });
    expect(container.querySelector(".process-surface-summary-line")?.getAttribute("aria-label")).toContain("3");
  });

  it("keeps concrete actions in a mixed summary and the remaining tools in its details", () => {
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
          arguments: JSON.stringify({ command: "bun run verify-database" }),
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
    expect(summary?.getAttribute("aria-label")).toContain("cron");
    expect(summary?.getAttribute("aria-label")).toContain("bun run verify-database");
    setProcessFoldOpen(container.querySelector("details.process-surface-fold"), true);
    expect(container.querySelector(".activity-timeline")?.textContent).toContain("cron");
  });

  it("shows the latest tool immediately when opening a live group with a long backlog", () => {
    const items: ThreadItem[] = Array.from({ length: 12 }, (_, index) =>
      makeReadFile(`read-${index}`, `file-${index}.ts`),
    );
    items.push({
      id: "latest-command",
      type: "tool_call",
      name: "bash",
      status: "in_progress",
    });
    const { container } = render({ processItems: items, streaming: true });
    setProcessFoldOpen(container.querySelector("details.process-surface-fold"), true);
    expect(container.querySelectorAll(".activity-timeline-item")).toHaveLength(items.length);

    rerender({
      processItems: items.map(item => item.id === "latest-command"
        ? { ...item, arguments: JSON.stringify({ command: "bun run verify-recovery" }) }
        : item),
      streaming: true,
    });
    const rows = container.querySelectorAll(".activity-timeline-item");
    expect(rows[rows.length - 1].textContent).toContain("bun run verify-recovery");
    const summary = container.querySelector(".process-surface-summary-line");
    expect(summary?.getAttribute("aria-label")).toContain("bun run verify-recovery");
    expect(container.querySelector<HTMLDetailsElement>("details.process-surface-fold")?.open).toBe(true);

    rerender({
      processItems: items.map(item => ({ ...item, status: "completed" })),
      streaming: false,
    });
    expect(summary?.getAttribute("aria-label")).toContain("12");
    expect(container.querySelectorAll(".activity-timeline-item")).toHaveLength(items.length);
  });

  it("summarizes a long historical group of the same kind", () => {
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
      container.querySelector(".process-surface-summary-line")?.getAttribute("aria-label"),
    ).toContain("4");
  });

  it("keeps the last command visible while reasoning continues", () => {
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
      container.querySelector(".process-surface-summary-line .process-label-content")?.textContent,
    ).toContain("npm test");
    const summary = container.querySelector(".process-surface-summary-line");
    const blobatar = summary?.querySelector<SVGSVGElement>(
      "svg.process-surface-blobatar",
    );
    expect(blobatar).not.toBeNull();
    expect(summary?.firstElementChild).toBe(blobatar);
    expect(blobatar?.tagName.toLowerCase()).toBe("svg");
  });

  it("keeps an active tool in the summary after an earlier call of the same kind fails", () => {
    const items: ThreadItem[] = [
      makeReadFile("failed-read", "a.ts", "failed"),
      makeReadFile("retry-read", "a.ts", "in_progress"),
      ...["cache_read", "cache_write"].map((pattern): ThreadItem => ({
        id: pattern,
        type: "tool_call",
        name: "grep",
        status: "completed",
        arguments: JSON.stringify({ pattern }),
      })),
      makeReasoning("reason-1", "checking results", "in_progress"),
    ];
    const { container } = render({ processItems: items, streaming: true });
    const summary = container.querySelector(".process-surface-summary-line");
    expect(summary?.getAttribute("aria-label")).toContain("a.ts");

    rerender({
      processItems: items.map((item) => item.id === "retry-read"
        ? { ...item, status: "completed" }
        : item),
      streaming: true,
    });
    expect(summary?.getAttribute("aria-label")).toContain("cache_write");
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
    expect(modelMascotAccessory(model)).not.toBe("none");
    expect(blobatar?.getAttribute("data-wuu-mascot-accessory")).toBe(modelMascotAccessory(model));
    expect(blobatar?.querySelector(".wuu-mascot-accessory")).not.toBeNull();
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
    const content = text?.querySelector(".process-label-content");
    const highlight = text?.querySelector(".process-label-highlight");
    expect(content?.textContent).toBe(text?.getAttribute("data-text"));
    expect(highlight?.getAttribute("aria-hidden")).toBe("true");
    expect(highlight?.textContent).toBe(content?.textContent);

    rerender({ processItems: [makeReadFile("tool-1", "a.ts", "completed")], streaming: false, active: false });
    expect(container.querySelector(".process-label-highlight")).toBeNull();
    expect(container.querySelector(".process-label-content")).toBe(content);
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
    expect(summary?.querySelector(".process-label-content")?.textContent).toBe("正在思考");
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
  });

  it("uses the renderProcessItem callback for reasoning items in the body", () => {
    const { container } = render({
      processItems: [
        makeReadFile("tool-1", "a.ts", "completed"),
        makeReadFile("tool-2", "b.ts", "completed"),
        makeReasoning("reason-1", "thinking aloud", "in_progress"),
      ],
      streaming: true,
      renderProcessItem: (item, isStreaming) => (
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

  it("omits reasoning body items when renderProcessItem is not provided", () => {
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
    expect(container.querySelector(".process-surface-summary-line")?.getAttribute("aria-label")).toContain("2");
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
          layout.scrollTop = Math.max(0, Math.min(value, layout.scrollHeight - layout.clientHeight));
        },
      });
      return layout;
    }

    it("renders reasoning items inside the bounded fold body", () => {
      const { container } = render({
        processItems: [makeReasoning("reason-1", "thinking aloud", "in_progress")],
        streaming: true,
        renderProcessItem: (item) => (
          <span data-testid="reasoning-mock">{item.id}</span>
        ),
      });
      const body = container.querySelector(".process-surface-body");
      expect(body).toBeTruthy();
      expect(body?.getAttribute("data-scroll-fade")).toBe("compact");
      expect(body?.parentElement?.hasAttribute("data-scroll-fade")).toBe(false);
      const items = body?.querySelectorAll(
        ".process-surface-reasoning-item",
      );
      expect(items?.length).toBe(1);
      // The mock item is wrapped inside the reasoning item slot.
      const mock = body?.querySelector('[data-testid="reasoning-mock"]');
      expect(mock?.textContent).toBe("reason-1");
    });

    it("shows interleaved tools and reasoning in event order", () => {
      const { container } = render({
        processItems: [
          makeReadFile("tool-1", "first.ts"),
          makeReasoning("reason-1", "middle reasoning"),
          makeReadFile("tool-2", "last.ts"),
        ],
        streaming: false,
        renderProcessItem: item => <span>{item.text}</span>,
      });
      const text = container.querySelector(".process-surface-body")!.textContent!;
      expect(text.indexOf("first.ts")).toBeLessThan(text.indexOf("middle reasoning"));
      expect(text.indexOf("middle reasoning")).toBeLessThan(text.indexOf("last.ts"));
    });

    it("starts completed history at the beginning", () => {
      const { container } = render({
        processItems: [makeReasoning("reason-1", "thinking aloud", "completed")],
        streaming: false,
        renderProcessItem: item => <span>{item.text}</span>,
      });
      const details = container.querySelector<HTMLDetailsElement>("details.process-surface-fold")!;
      const layout = stubScrollLayout(details.querySelector<HTMLElement>(".process-surface-body")!, {});
      setProcessFoldOpen(details, true);
      expect(layout.scrollTop).toBe(0);
    });

    it("positions a live fold at latest before its first paint", () => {
      const { container } = render({
        processItems: [makeReasoning("reason-1", "thinking aloud", "completed")],
        streaming: true,
        renderProcessItem: (item) => <span>{item.id}</span>,
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

      expect(layout.scrollTop).toBe(800);
    });

    it("restores reading history after closing, hidden growth and reopening", () => {
      const { container } = render({
        processItems: [makeReasoning("reason-1", "thinking aloud", "completed")],
        streaming: true,
        renderProcessItem: (item) => <span>{item.id}</span>,
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

      setProcessFoldOpen(details, true);
      expect(layout.scrollTop).toBe(800);

      // User scrolls up to read earlier reasoning.
      act(() => {
        body!.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
      });
      layout.scrollTop = 240;
      act(() => {
        body!.dispatchEvent(new UIEvent("scroll", { bubbles: true }));
      });

      setProcessFoldOpen(details, false);
      layout.scrollHeight += 300;
      // Native hidden layout can reset the DOM offset; the reading snapshot survives.
      layout.scrollTop = 0;
      act(() => body!.dispatchEvent(new UIEvent("scroll", { bubbles: true })));
      setProcessFoldOpen(details, true);
      expect(layout.scrollTop).toBe(240);
    });

    it("does not override upward scrolling during the opening motion", () => {
      vi.useFakeTimers();
      try {
        const { container } = render({
          processItems: [makeReasoning("reason-1", "thinking aloud", "in_progress")],
          streaming: true,
          renderProcessItem: item => <span>{item.text}</span>,
        });
        const details = container.querySelector<HTMLDetailsElement>("details.process-surface-fold")!;
        const body = details.querySelector<HTMLElement>(".process-surface-body")!;
        const layout = stubScrollLayout(body, {});
        setProcessFoldOpen(details, true);
        act(() => {
          body.dispatchEvent(new WheelEvent("wheel", { deltaY: -100 }));
          layout.scrollTop = 240;
          body.dispatchEvent(new Event("scroll"));
          vi.advanceTimersByTime(500);
        });
        expect(layout.scrollTop).toBe(240);
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
