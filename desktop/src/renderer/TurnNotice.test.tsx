/**
 * Tests for `ContextCompactionNotice`'s process-activity row rendering.
 *
 * These tests pin the shared process-surface classes, visible detail,
 * active sweep target, and failed-state semantics.
 */
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ContextCompactionNotice, StreamReconnectNotice, TurnFailureNotice, TurnNotice } from "./TurnNotice";
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

  it("advances retry progress and switches to retrying at the deadline", () => {
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

    const notice = host.querySelector("aside.turn-failure.is-retrying");
    expect(notice?.querySelector("[role=progressbar]")?.getAttribute("aria-valuenow")).toBe("0");
    expect(notice?.textContent).toContain(t("turnFailure.rateLimit"));
    expect(notice?.textContent).toContain(t("appState.retrySeconds", { count: "2" }));
    // The redacted provider cause stays out of the row; the structured
    // category maps to a localized title instead.
    expect(notice?.textContent).not.toContain("connection reset");
    expect(notice?.querySelector("button")).toBeNull();

    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(notice?.textContent).toContain(t("appState.retrySecond", { count: "1" }));
    expect(notice?.querySelector("[role=progressbar]")?.getAttribute("aria-valuenow")).toBe("50");

    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(notice?.textContent).toContain(t("appState.retryNow"));
    expect(notice?.querySelector("[role=progressbar]")).toBeNull();
  });

  it("counts a wait just over a minute as one minute, not two", () => {
    const host = mount(
      <StreamReconnectNotice
        item={{ id: "retry", type: "stream_reconnect", status: "in_progress", reason: "server", retry_at_ms: Date.now() + 61_000 }}
      />,
    );
    expect(host.textContent).toContain(t("appState.retryMinute", { count: "1" }));
    act(() => { vi.advanceTimersByTime(2_000); });
    expect(host.textContent).toContain(t("appState.retrySeconds", { count: "59" }));
  });

  it("resets progress for the next attempt and removes a recovered card", () => {
    const item = { id: "retry", type: "stream_reconnect", status: "in_progress", retry_at_ms: Date.now() + 2000 } as const;
    const host = mount(<StreamReconnectNotice item={item} />);
    const card = host.querySelector("aside");
    act(() => { vi.advanceTimersByTime(1000); });
    act(() => { root?.render(<StreamReconnectNotice item={{ ...item, retry_at_ms: Date.now() + 4000 }} />); });
    expect(host.querySelector("aside")).toBe(card);
    expect(host.querySelector("[role=progressbar]")?.getAttribute("aria-valuenow")).toBe("0");
    act(() => { vi.advanceTimersByTime(2000); });
    expect(host.querySelector("[role=progressbar]")?.getAttribute("aria-valuenow")).toBe("50");
    act(() => { root?.render(<StreamReconnectNotice item={{ ...item, status: "completed" }} />); });
    expect(host.querySelector("aside")).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("TurnFailureNotice", () => {
  const serverError = {
    message: "stream request failed: HTTP 500: 500 Internal Server Error: fixture failure",
    category: "provider" as const,
    status_code: 500,
    recovery: { attempt_count: 11, retry_count: 10, max_attempts: 11, submission_count: 11, stop_reason: "retry_limit", failure_category: "server" },
  };
  const authError = {
    message: "stream request failed: HTTP 401: 401 Unauthorized: {\"error\":{\"message\":\"Incorrect API key provided.\"}}",
    code: "invalid_request_error",
    category: "auth" as const,
    status_code: 401,
    recovery: { attempt_count: 1, retry_count: 0, max_attempts: 11, submission_count: 1, stop_reason: "non_retryable", failure_category: "authentication" },
  };
  const buttons = (host: HTMLElement) => [...host.querySelectorAll<HTMLButtonElement>(".turn-failure-recovery > button")];

  it("offers Model services beside a retry when the service rejects the credentials", async () => {
    const retry = vi.fn();
    const openSettings = vi.fn();
    const host = mount(
      <TurnFailureNotice display={userFacingErrorForMessage(authError, "turn")} error={authError} onRetry={retry} onOpenSettings={openSettings} />,
    );
    const aside = host.querySelector("aside.turn-failure");
    expect(aside?.getAttribute("role")).toBe("alert");
    expect(aside?.querySelector(".turn-failure-title")?.textContent).toBe(t("turnFailure.auth"));
    expect(aside?.querySelector(".turn-failure-body")?.textContent).toBe(t("turnFailure.authBody"));
    const actions = buttons(host);
    expect(actions.map((button) => button.textContent).sort()).toEqual([t("appState.retryAction"), t("turnFailure.openSettings")].sort());
    act(() => { actions.find((button) => button.textContent === t("turnFailure.openSettings"))!.click(); });
    expect(openSettings).toHaveBeenCalledOnce();
    await act(async () => { actions.find((button) => button.textContent === t("appState.retryAction"))!.click(); });
    expect(retry).toHaveBeenCalledOnce();
  });

  it("keeps the technical record behind a closed disclosure", () => {
    const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(serverError, "turn")} error={serverError} />);
    const toggle = host.querySelector<HTMLButtonElement>(".turn-failure-details-toggle")!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector(".turn-failure-diagnostic")).toBeNull();
    act(() => { toggle.click(); });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector(".turn-failure-diagnostic")?.textContent).toBe(serverError.message);
  });

  it("keeps the status and the automatic retries with the technical record", () => {
    const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(serverError, "turn")} error={serverError} onRetry={vi.fn()} />);
    expect(host.querySelector(".turn-failure-title")?.textContent).toBe(t("turnFailure.unavailable"));
    expect(host.querySelector(".turn-failure-body")?.textContent).toBe(t("turnFailure.unavailableBody"));
    expect(host.querySelector(".turn-failure-message")?.textContent).not.toContain("500");
    expect(buttons(host).map((button) => button.textContent)).toEqual([t("appState.retryAction")]);
    act(() => { host.querySelector<HTMLButtonElement>(".turn-failure-details-toggle")!.click(); });
    expect(host.querySelector(".turn-failure-facts")?.textContent).toBe(
      `HTTP 500 · ${t("turnFailure.retried", { count: "10" })}`,
    );
  });

  it("continues from a partial reply rather than resending the message", async () => {
    const retry = vi.fn();
    const resume = vi.fn();
    const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(serverError, "turn")} error={serverError} onRetry={retry} onContinue={resume} />);
    expect(buttons(host).map((button) => button.textContent)).toEqual([t("turnFailure.continue")]);
    await act(async () => { buttons(host)[0].click(); });
    expect(resume).toHaveBeenCalledOnce();
    expect(retry).not.toHaveBeenCalled();
  });

  it("does not offer a retry that would overflow the context again", () => {
    const error = { ...serverError, status_code: undefined, recovery: { ...serverError.recovery, retry_count: 0, failure_category: "context_overflow" } };
    const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(error, "turn")} error={error} onRetry={vi.fn()} onOpenSettings={vi.fn()} />);
    expect(host.querySelector(".turn-failure-title")?.textContent).toBe(t("turnFailure.context"));
    expect(buttons(host)).toHaveLength(0);
  });

  it("disables manual retry until submission settles", async () => {
    let finish!: () => void;
    const retry = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(serverError, "turn")} error={serverError} onRetry={retry} />);
    const button = buttons(host)[0];
    act(() => { button.click(); });
    expect(button.disabled).toBe(true);
    act(() => { button.click(); });
    expect(retry).toHaveBeenCalledOnce();
    await act(async () => { finish(); });
    expect(button.disabled).toBe(false);
  });

  it("unfolds the retrying card into the failure instead of replacing it", () => {
    const item = { id: "reconnect-1", type: "stream_reconnect", status: "in_progress", reason: "rate_limit", retry_count: 2, retry_at_ms: Date.now() + 3000 } as const;
    const host = mount(<TurnFailureNotice reconnect={item} />);
    const card = host.querySelector("aside.turn-failure")!;
    expect(card.getAttribute("role")).toBe("status");
    expect(host.querySelector(".turn-failure-body")).toBeNull();
    const failed = { ...item, status: "failed" } as const;
    act(() => { root?.render(<TurnFailureNotice reconnect={failed} display={userFacingErrorForMessage("rate limited", "turn")} onRetry={vi.fn()} />); });
    expect(host.querySelector("aside.turn-failure")).toBe(card);
    expect(card.getAttribute("role")).toBe("alert");
    expect(card.classList.contains("is-retrying")).toBe(false);
    expect(card.querySelector(".turn-failure-title")?.textContent).toBe(t("turnFailure.rateLimit"));
    expect(buttons(host).map((button) => button.textContent)).toEqual([t("appState.retryAction")]);
  });

  it("names an unrecognized status only in the details", () => {
    const error = {
      message: "stream request failed: HTTP 400: 400 Bad Request: {\"error\":{\"type\":\"invalid_request_error\"}}",
      category: "provider" as const,
      status_code: 400,
    };
    const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(error, "turn")} error={error} onRetry={vi.fn()} />);
    expect(host.querySelector(".turn-failure-message")?.textContent).not.toContain("400");
    expect(host.querySelector(".turn-failure-body")?.textContent).toBe(t("turnFailure.genericBody"));
    act(() => { host.querySelector<HTMLButtonElement>(".turn-failure-details-toggle")!.click(); });
    expect(host.querySelector(".turn-failure-facts")?.textContent).toBe("HTTP 400");
  });

  it("says the device is offline when a transport failure happens without a network", () => {
    vi.spyOn(navigator, "onLine", "get").mockReturnValue(false);
    try {
      const item = { id: "reconnect-1", type: "stream_reconnect", status: "failed", text: "dial tcp: lookup api.example.com: no such host", reason: "network", retry_count: 3 } as const;
      const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(item.text, "turn")} reconnect={item} />);
      expect(host.querySelector(".turn-failure-title")?.textContent).toBe(t("turnFailure.offline"));
      expect(host.querySelector(".turn-failure-body")?.textContent).toBe(t("turnFailure.offlineBody"));
    } finally {
      vi.restoreAllMocks();
    }
  });

  it("explains a historical failure without offering actions", () => {
    const item = { id: "reconnect-1", type: "stream_reconnect", status: "failed", text: "connection reset by peer", reason: "network", retry_count: 5, max_retries: 5 } as const;
    const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(item.text, "turn")} reconnect={item} />);
    expect(host.querySelector(".turn-failure-title")?.textContent).toBe(t("turnFailure.network"));
    expect(buttons(host)).toHaveLength(0);
  });

  it.each([
    ["authentication", undefined, "turnFailure.auth"],
    ["rate_limit", undefined, "turnFailure.rateLimit"],
    ["quota", undefined, "turnFailure.quota"],
    ["overloaded", undefined, "turnFailure.unavailable"],
    ["server", undefined, "turnFailure.unavailable"],
    ["deadline", undefined, "turnFailure.timeout"],
    ["network", undefined, "turnFailure.network"],
    ["incomplete_stream", undefined, "turnFailure.dropped"],
    ["context_overflow", undefined, "turnFailure.context"],
    ["request_too_large", undefined, "error.requestTooLargeTitle"],
    // App-servers that predate the structured category only carry the
    // redacted cause text.
    [undefined, "Authentication failed", "turnFailure.auth"],
    [undefined, "Provider is overloaded", "turnFailure.unavailable"],
    [undefined, "connection reset by peer", "turnFailure.network"],
  ] as const)(
    "titles a failed reconnect from category %s or the redacted cause",
    (reason, text, titleKey) => {
      const item = { id: "reconnect-1", type: "stream_reconnect", status: "failed", text, reason, retry_count: 1, max_retries: 1 } as const;
      const host = mount(<TurnFailureNotice display={userFacingErrorForMessage(text ?? "", "turn")} reconnect={item} />);
      expect(host.querySelector(".turn-failure-title")?.textContent).toBe(t(titleKey));
      if (text) {
        expect(host.querySelector(".turn-failure-message")?.textContent).not.toContain(text);
      }
    },
  );
});
