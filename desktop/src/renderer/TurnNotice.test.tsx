/**
 * Tests for `ContextCompactionNotice`'s process-activity row rendering.
 *
 * These tests pin the shared process-surface classes, visible detail,
 * active sweep target, and failed-state semantics.
 */
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ContextCompactionNotice, StreamReconnectNotice, TurnNotice } from "./TurnNotice";
import { userFacingErrorForMessage } from "./UserFacingErrors";
import { setActiveLocale, translateCurrent as t } from "./i18n";

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

let root: Root | null = null;
let container: HTMLDivElement | null = null;

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  setActiveLocale("zh-CN");
  if (root) {
    act(() => {
      root?.unmount();
    });
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
  vi.useRealTimers();
});

function mount(element: ReactElement): HTMLDivElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root?.render(element);
  });
  return container;
}

describe("ContextCompactionNotice", () => {
  it("localizes recognized compaction events", () => {
    setActiveLocale("en-US");
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        text="Compacted history: 18 → 5 messages (~12k → ~3k tokens)"
      />,
    );

    expect(host.querySelector(".context-compaction-title")?.textContent).toBe(t("compaction.complete"));
    expect(host.querySelector(".context-compaction-detail")?.textContent).toBe("12k → 3k");
  });
  it("renders the in_progress host with the shimmer-ready label when status is in_progress", () => {
    const host = mount(<ContextCompactionNotice status="in_progress" />);
    const aside = host.querySelector("aside.process-surface.context-compaction-notice");
    expect(aside).not.toBeNull();
    expect(aside?.classList.contains("in_progress")).toBe(true);
    expect(aside?.getAttribute("role")).toBe("status");
    expect(aside?.getAttribute("aria-live")).toBe("polite");

    const label = host.querySelector(".context-compaction-title");
    expect(label).not.toBeNull();
    expect(label?.textContent).toBe(t("compaction.autoCompacting"));
    expect(host.querySelector(".process-surface-row.is-live-gray")).not.toBeNull();
    expect(
      host.querySelector(".process-surface-blobatar")?.getAttribute("data-wuu-mascot-activity"),
    ).toBe("compact");
    expect(host.querySelector(".context-compaction-mascot")).not.toBeNull();
    expect(host.querySelector(".context-compaction-black-hole")).toBeNull();
    expect(host.querySelector(".context-compaction-copy")).not.toBeNull();
    expect(host.querySelector(".process-surface-summary-line")?.getAttribute("aria-label")).toBe(
      t("compaction.autoCompacting"),
    );
    expect(host.querySelector(".turn-event-notice")).toBeNull();
  });

  it("uses the manual compact progress label for slash compact", () => {
    const host = mount(
      <ContextCompactionNotice
        status="in_progress"
        reason="manual"
        text="Manual context compaction in progress."
      />,
    );

    expect(host.querySelector(".context-compaction-title")?.textContent).toBe(
      t("compaction.compacting"),
    );
    expect(host.querySelector(".context-compaction-notice.in_progress .process-surface-row.is-live-gray")).not.toBeNull();
  });

  it("renders the process activity row with visible detail when status is completed", () => {
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        text="✦ Compacted history: 18 → 5 messages (was ~12k tokens)"
      />,
    );
    const aside = host.querySelector("aside.process-surface.context-compaction-notice");
    expect(aside).not.toBeNull();
    expect(aside?.classList.contains("completed")).toBe(true);
    expect(aside?.getAttribute("aria-live")).toBeNull();

    const title = host.querySelector(".context-compaction-title");
    expect(title?.textContent).toBe(t("compaction.complete"));

    expect(host.querySelector(".context-compaction-detail")?.textContent).not.toContain("消息");
    expect(host.querySelector(".process-surface-row.is-live-gray")).toBeNull();
    expect(host.querySelector(".context-compaction-mascot")).toBeNull();
  });

  it("does not render a completed compaction attempt that changed nothing", () => {
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        reason="proactive"
        text="Nothing to compact yet; history is unchanged."
      />,
    );

    expect(host.childElementCount).toBe(0);
  });

  it("shows both the old and replacement context token estimates", () => {
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        text="✦ Compacted history: 119 → 1 messages (~239k → ~49k tokens)"
      />,
    );

    expect(host.querySelector(".context-compaction-detail")?.textContent).toBe(
      "239k → 49k",
    );
    expect(host.querySelector(".context-compaction-detail")?.textContent).not.toContain("消息");
  });

  it("shows the same token-only range after starting a fresh context", () => {
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        reason="new_context"
        text="✦ Started a fresh context window with history: 119 → 1 messages (~239k → ~49k tokens)"
      />,
    );

    expect(host.querySelector(".context-compaction-detail")?.textContent).toBe("239k → 49k");
  });

  it("labels manual compact completion as success", () => {
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        reason="manual"
        text="✦ Manually compacted history: 18 → 5 messages (was ~12k tokens)"
      />,
    );

    expect(host.querySelector(".context-compaction-title")?.textContent).toBe(
      t("compaction.manualComplete"),
    );
    expect(host.querySelector(".context-compaction-detail")?.textContent).not.toContain("消息");
  });

  it("keeps unfamiliar compaction diagnostics out of the collapsed status", () => {
    const diagnostic = "Compaction implementation diagnostic";
    const host = mount(<ContextCompactionNotice status="completed" text={diagnostic} />);
    expect(host.querySelector("summary")?.textContent).not.toContain(diagnostic);
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.querySelector(".context-compaction-summary")?.textContent).toBe(diagnostic);
  });

  it("does not render a recoverable background note failure as compaction success", () => {
    const host = mount(
      <ContextCompactionNotice status="completed" reason="context_note"
        text="Context note failed. uncovered history cannot fit beside the continuation note" />,
    );
    expect(host.childElementCount).toBe(0);
  });

  it("labels failed manual compact status as failed", () => {
    const host = mount(
      <ContextCompactionNotice
        status="failed"
        reason="manual"
        text="Manual context compaction failed; history is unchanged."
      />,
    );

    expect(host.querySelector(".context-compaction-title")?.textContent).toBe(
      t("compaction.failed"),
    );
    expect(host.querySelector(".context-compaction-detail")?.textContent).toContain(
      t("compaction.failedDetail"),
    );
    expect(host.querySelector("aside")?.classList.contains("failed")).toBe(true);
    expect(host.querySelector("aside")?.getAttribute("role")).toBe("alert");
  });

  it("falls back to the completed layout when status is omitted", () => {
    const host = mount(<ContextCompactionNotice text="" />);
    const aside = host.querySelector("aside.process-surface.context-compaction-notice");
    expect(aside?.classList.contains("completed")).toBe(true);
    expect(host.querySelector(".context-compaction-title")?.textContent).toBe(
      t("compaction.complete"),
    );
  });

  it("uses the same completed title for overflow recovery compaction", () => {
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        text="Recovered from context overflow — compacted history: 18 → 5 messages (was ~12k tokens)"
      />,
    );

    expect(host.querySelector(".context-compaction-title")?.textContent).toBe(
      t("compaction.complete"),
    );
  });

  it("does not present failed proactive compaction as a successful compact", () => {
    const failedCompactText =
      "Context compaction failed; continuing without compacting history.";
    const host = mount(
      <ContextCompactionNotice
        status="completed"
        text={failedCompactText}
      />,
    );

    expect(host.querySelector(".context-compaction-title")?.textContent).toBe(
      t("compaction.failed"),
    );
    expect(host.querySelector(".context-compaction-detail")?.textContent).toContain(
      t("compaction.failedDetail"),
    );
    expect(host.textContent).not.toContain(t("compaction.complete"));
  });

  it.each(["zh-CN", "en-US"] as const)("recovers a legacy failed fresh-window notice in %s", (locale) => {
    setActiveLocale(locale);
    const text = "Fresh context could not be installed; active history is unchanged.";
    const host = mount(<ContextCompactionNotice status="completed" reason="new_context" text={text} summary="not installed" />);
    expect(host.querySelector('[role="alert"]')).not.toBeNull();
    expect(host.textContent).toContain(t("compaction.failed"));
    expect(host.textContent).not.toContain(t("compaction.complete"));
    expect(host.querySelector("summary")?.textContent).not.toContain(text);
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.querySelector(".context-compaction-summary")?.textContent).toBe(text);
    expect(host.textContent).not.toContain("not installed");
  });
});

describe("TurnNotice process row", () => {
  it("uses the shared process surface and keeps the full detail expandable", () => {
    const display = userFacingErrorForMessage("connection reset by peer", "turn");
    const host = mount(<TurnNotice display={display} />);
    const aside = host.querySelector("aside.system-event-notice");
    expect(aside).not.toBeNull();
    expect(aside?.querySelector(".process-surface-row")).not.toBeNull();
    expect(aside?.querySelector(".system-event-title")?.textContent).toBe(display.title);
    expect(aside?.querySelector("summary")?.textContent).not.toContain(display.detail);
    expect(aside?.querySelector("summary")?.textContent).not.toContain(display.diagnostic);
    expect(aside?.querySelector("details")?.open).toBe(false);
    expect(aside?.querySelector(".system-event-expanded-detail")?.textContent).toContain(display.detail);
    expect(aside?.querySelector(".system-event-expanded-detail")?.textContent).toContain(display.diagnostic);
    expect(aside?.getAttribute("aria-label")).toContain(display.title);
    expect(aside?.querySelector(".process-surface-chevron")).not.toBeNull();
  });

  it("renders cancellation in the same neutral process row", () => {
    const display = userFacingErrorForMessage("context canceled", "turn");
    const host = mount(<TurnNotice display={display} />);
    const aside = host.querySelector("aside.system-event-notice");
    expect(aside?.querySelector(".system-event-title")?.textContent).toBe(display.title);
    expect(aside?.classList.contains("neutral")).toBe(false);
  });

  it("keeps alert semantics without applying a colored tone class", () => {
    const display = userFacingErrorForMessage("401 unauthorized", "turn");
    const host = mount(<TurnNotice display={display} />);
    const aside = host.querySelector("aside.system-event-notice");
    expect(aside?.getAttribute("role")).toBe("alert");
    expect(aside?.classList.contains("auth")).toBe(false);
  });

  it("shows retry progress and counts down in one live process row", () => {
    const retryAtMs = Date.now() + 2_000;
    const host = mount(
      <StreamReconnectNotice
        item={{
          id: "reconnect-1",
          type: "stream_reconnect",
          status: "in_progress",
          text: "connection reset by peer",
          reason: "rate_limit",
          retry_count: 2,
          max_retries: 5,
          retry_at_ms: retryAtMs,
        }}
      />,
    );

    const notice = host.querySelector("aside.stream-reconnect-notice");
    expect(notice?.querySelectorAll(".process-surface-row")).toHaveLength(1);
    expect(notice?.textContent).toContain("429 触发限流");
    expect(notice?.textContent).toContain("第 2 次重试");
    expect(notice?.textContent).toContain("2 秒后重试");
    // The redacted provider cause stays out of the row; the structured
    // category maps to a localized title instead.
    expect(notice?.textContent).not.toContain("connection reset");

    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(notice?.textContent).toContain("1 秒后重试");

    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(notice?.textContent).toContain("正在重试");
    expect(notice?.querySelector(".process-surface-chevron")).toBeNull();
  });

  it("marks a failed stream reconnect row as stopped without retry counts", () => {
    const host = mount(
      <StreamReconnectNotice
        item={{
          id: "reconnect-1",
          type: "stream_reconnect",
          status: "failed",
          text: "connection reset by peer",
          reason: "network",
          retry_count: 5,
          max_retries: 5,
        }}
      />,
    );

    const notice = host.querySelector("aside.stream-reconnect-notice");
    expect(notice?.getAttribute("role")).toBe("alert");
    expect(notice?.textContent).toContain("已停止");
    expect(notice?.textContent).toContain("网络异常");
    expect(notice?.textContent).not.toContain("次重试");
    expect(notice?.textContent).not.toContain("秒后重试");
  });

  it.each([
    ["authentication", undefined, "认证失败"],
    ["rate_limit", undefined, "429 触发限流"],
    ["quota", undefined, "429 触发限流"],
    ["overloaded", undefined, "上游过载"],
    ["server", undefined, "模型服务异常"],
    ["deadline", undefined, "请求超时"],
    ["network", undefined, "网络异常"],
    ["incomplete_stream", undefined, "网络异常"],
    ["context_overflow", undefined, "上下文超出模型上限"],
    ["request_too_large", undefined, "请求超出大小限制"],
    // App-servers that predate the structured category only carry the
    // redacted cause text; unmapped causes read as a generic request failure.
    [undefined, "Authentication failed", "认证失败"],
    [undefined, "Provider is overloaded", "上游过载"],
    [undefined, "connection reset by peer", "请求失败"],
  ])(
    "titles the reconnect row from category %s or the redacted cause",
    (reason, text, title) => {
      const host = mount(
        <StreamReconnectNotice
          item={{
            id: "reconnect-1",
            type: "stream_reconnect",
            status: "failed",
            text,
            reason,
            retry_count: 1,
            max_retries: 1,
          }}
        />,
      );

      const notice = host.querySelector("aside.stream-reconnect-notice");
      expect(notice?.textContent).toContain(title);
      if (text) {
        expect(notice?.textContent).not.toContain(text);
      }
    },
  );
});
