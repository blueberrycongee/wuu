import { afterEach, describe, expect, it } from "vitest";
import type { ThreadItem, Turn, TurnError } from "../shared/protocol";
import { turnEventForItem, turnEventForTurn } from "./TurnEvents";
import { userFacingErrorForMessage } from "./UserFacingErrors";
import { setActiveLocale, translateCurrent as t } from "./i18n";

afterEach(() => setActiveLocale("zh-CN"));

describe("userFacingErrorForMessage", () => {
  it.each(["zh-CN", "en-US"] as const)("distinguishes empty replies from generic provider errors in %s", (locale) => {
    setActiveLocale(locale);
    const message = "model returned empty answer (stop_reason=completed)";
    const live = userFacingErrorForMessage({ message, category: "provider" }, "turn");
    const restored = userFacingErrorForMessage(message, "turn");
    expect(live).toEqual(restored);
    expect(live.title).toBe(t("error.modelEmpty"));
    expect(live.detail).toBe(t("error.modelEmptyDetail"));
    expect(live.title).not.toBe(t("error.modelError"));
    expect(live.detail).not.toBe(t("error.providerDetail"));
    expect(live.diagnostic).toBe(message);
  });
  it("classifies wrapped context overflow as a provider error", () => {
    const display = userFacingErrorForMessage(
      "stream request failed: stream error (context_length_exceeded): Your input exceeds the context window",
      "turn"
    );

    expect(display.category).toBe("provider");
    // Known diagnostic identifiers are translated into one user-facing label.
    expect(display.title).toBe("上下文超出窗口");
  });

  it("shows the HTTP status code with a Chinese phrase as the title for network errors", () => {
    const display = userFacingErrorForMessage(
      "upstream returned 503 service unavailable",
      "turn",
    );
    expect(display.category).toBe("network");
    expect(display.title).toBe("503 服务不可用");
  });

  it("shows the HTTP status code with a Chinese phrase as the title for auth errors", () => {
    const display = userFacingErrorForMessage("401 unauthorized", "turn");
    expect(display.category).toBe("auth");
    expect(display.title).toBe("401 未授权");
    expect(display.detail).toContain("Provider");
  });

  it("uses a keyword-based title when no HTTP code is present in the error", () => {
    const display = userFacingErrorForMessage("connection reset by peer", "turn");
    expect(display.category).toBe("network");
    expect(display.title).toBe("连接被重置");
  });

  it("shows partial Responses stream closes as a specific provider-stream state", () => {
    const display = userFacingErrorForMessage(
      "stream request failed: websocket stream closed after provider event: websocket stream closed before response.completed",
      "turn",
    );

    expect(display.category).toBe("provider");
    expect(display.title).toBe("回答未完整返回");
    expect(display.detail).toContain("response.completed");
    expect(display.detail).toContain("这次回答可能不完整");
  });

  it("falls back to the category title when the message has no specific identifier", () => {
    const display = userFacingErrorForMessage("login required", "turn");
    expect(display.category).toBe("auth");
    // "login required" matches the auth classifier but no HTTP code or
    // keyword matcher fires, so we fall back to the category name.
    expect(display.title).toBe("认证失败");
  });

  it("uses the internal category title for unrecognized errors", () => {
    const display = userFacingErrorForMessage("panic: nil pointer", "turn");
    expect(display.category).toBe("internal");
    expect(display.title).toBe("wuu 内部错误");
  });

  it("localizes generated labels while leaving provider input available for classification", () => {
    setActiveLocale("en-US");

    const display = userFacingErrorForMessage("connection reset by peer", "turn");

    expect(display.category).toBe("network");
    expect(display.title).toBe("Connection reset");
    expect(display.detail).toContain("Try again later");
  });

  describe("structured TurnError input from the Go core", () => {
    it("models a 404 as one read-only user-facing message", () => {
      const display = userFacingErrorForMessage(
        {
          message: 'HTTP 404: {"error":{"code":"internal_error","message":"resource not found"}}',
          code: "internal_error",
          category: "internal",
          provider: "compatible",
          status_code: 404,
        },
        "turn",
      );

      expect(display.title).toBe("请求失败 · HTTP 404");
      expect(display).not.toHaveProperty("code");
      expect(display).not.toHaveProperty("recommendedActions");
    });

    it("keeps a provider code visible when the stream failed after HTTP 200", () => {
      const display = userFacingErrorForMessage(
        {
          message: "some raw provider body",
          code: "insufficient_quota",
          category: "provider",
        },
        "turn",
      );
      expect(display.title).toBe("请求失败 · insufficient_quota");
      expect(display).not.toHaveProperty("code");
      expect(display.category).toBe("provider");
    });

    it("uses structured status_code as a provider chip fallback when code is missing", () => {
      const display = userFacingErrorForMessage(
        {
          message: "gateway returned a provider error without a parseable code",
          category: "provider",
          status_code: 429,
        },
        "turn",
      );

      expect(display.category).toBe("provider");
      expect(display.title).toBe("请求失败 · HTTP 429");
    });

    it("prefers the exact HTTP status over an inferred quota classification", () => {
      const display = userFacingErrorForMessage(
        {
          message: "usage limit reached",
          code: "usage_limit_reached",
          category: "auth",
          provider: "anthropic",
          status_code: 403,
        },
        "turn",
      );

      expect(display.title).toBe("请求失败 · HTTP 403");
      expect(display.category).toBe("auth");
    });

    it("shows the provider code for a post-200 stream failure", () => {
      const display = userFacingErrorForMessage(
        {
          message: "usage limit reached",
          code: "usage_limit_reached",
          category: "provider",
          provider: "anthropic",
        },
        "turn",
      );

      expect(display.title).toBe("请求失败 · usage_limit_reached");
    });

    it("uses structured partial-stream facts without exposing the code", () => {
      const display = userFacingErrorForMessage(
        {
          message:
            "stream request failed: websocket stream closed after provider event: websocket stream closed before response.completed",
          code: "stream_closed_before_response.completed",
          category: "provider",
          provider: "openai-codex",
        },
        "turn",
      );

      expect(display.title).toBe("回答未完整返回");
      expect(display).not.toHaveProperty("code");
      expect(display.category).toBe("provider");
      expect(display.detail).toContain("这次回答可能不完整");
    });

    it("renders an HTTP-sourced invalid_request with the status phrase and error tone", () => {
      const display = userFacingErrorForMessage(
        {
          message: 'HTTP 400: {"error":{"type":"invalid_request_error","message":"max_tokens must be positive"}}',
          code: "invalid_request_error",
          category: "invalid_request",
          provider: "compatible",
          status_code: 400,
        },
        "turn",
      );

      expect(display.category).toBe("invalid_request");
      expect(display.tone).toBe("error");
      // The message-embedded status wins over the structured status_code
      // fact so the wording survives a history rebuild (tab switch).
      expect(display.title).toBe("400 请求无效");
      expect(display.detail).toBe("Provider 认为这次请求参数无效。原始错误已留在调试信息中。");
    });

    it("falls back to the localized invalid-request title for stream-sourced 400s", () => {
      const display = userFacingErrorForMessage(
        {
          message: "stream request failed: stream error (invalid_request_error)",
          code: "invalid_request_error",
          category: "invalid_request",
        },
        "turn",
      );

      expect(display.category).toBe("invalid_request");
      expect(display.tone).toBe("error");
      expect(display.title).toBe("请求参数无效");
      expect(display.detail).toBe("Provider 认为这次请求参数无效。原始错误已留在调试信息中。");
    });

    it("degrades an unknown wire category to the internal-error rendering", () => {
      // A newer Go core may emit a category this renderer has not
      // learned yet; it must never produce a blank, tone-less display.
      const display = userFacingErrorForMessage(
        {
          message: "some future failure mode",
          category: "quota_exhausted" as unknown as TurnError["category"],
        },
        "turn",
      );

      expect(display.category).toBe("internal");
      expect(display.tone).toBe("error");
      expect(display.title).toBe("wuu 内部错误");
      expect(display.detail).toBe("没有完成这次请求。调试信息可用于排查。");
    });

    it("falls back to the string classifier when the structured input omits `category`", () => {
      // An older Go core may not yet send the `category` field; the
      // front-end must still produce a sensible display from the
      // message alone. "context_length_exceeded" maps to provider.
      const display = userFacingErrorForMessage(
        {
          message: "stream request failed: stream error (context_length_exceeded)",
          code: "context_length_exceeded",
        },
        "turn",
      );
      expect(display.category).toBe("provider");
      expect(display.title).toBe("上下文超出窗口");
      expect(display).not.toHaveProperty("code");
    });

    it("renders message-size-limit overflow identically in live and rebuilt paths", () => {
      // Kimi-for-coding rejects oversized prompts with a 400 whose body
      // names no context keyword; both the wire-category path and the
      // message-only fallback must converge on the context-overflow display.
      const raw =
        'stream request failed: HTTP 400: 400 Bad Request: {"error":{"type":"invalid_request_error","message":"total message size 2306631 exceeds limit 2097152"},"type":"error"}';
      const live = userFacingErrorForMessage(
        {
          message: raw,
          code: "invalid_request_error",
          category: "provider",
          provider: "kimi-code",
          status_code: 400,
        },
        "turn",
      );
      const rebuilt = userFacingErrorForMessage(raw, "turn");

      for (const display of [live, rebuilt]) {
        expect(display.category).toBe("provider");
        expect(display.title).toBe("上下文超出窗口");
      }
    });

    it("keeps every core overflow phrasing identical in live and rebuilt paths", () => {
      const phrasings = [
        "context length exceeded",
        "Your input exceeds the context window of this model",
        "context window exceeds limit",
        "maximum context length",
        "model_context_window_exceeded",
        "prompt is too long",
        "request_too_large",
        "input is too long",
        "input token count (120000) exceeds the maximum number of tokens allowed",
        "maximum prompt length is 131072",
        "reduce the length of the messages",
        "exceeds the maximum allowed input length",
        "is longer than the model's context length",
        "prompt token count of 240000 exceeds the limit of 200000",
        "exceeds the available context size",
        "greater than the context length",
        "exceeded model token limit",
        "message size 2,306,631 exceeds limit",
        "prompt too long; exceeded max context length",
      ];

      for (const phrasing of phrasings) {
        const message = `stream request failed: HTTP 400: ${phrasing}`;
        const live = userFacingErrorForMessage(
          {
            message,
            category: "provider",
            status_code: 400,
          },
          "turn",
        );
        const rebuilt = userFacingErrorForMessage(message, "turn");

        expect(live.category, phrasing).toBe("provider");
        expect(rebuilt.category, phrasing).toBe("provider");
        expect(live.title, phrasing).toBe("上下文超出窗口");
        expect(rebuilt.title, phrasing).toBe(live.title);
      }
    });
  });

  describe("resume stability (tab switch rebuilds the turn from the persisted message only)", () => {
    // After a thread resume the app-server rebuilds turn.error from the
    // terminal history record: only `message` survives — status_code,
    // code and category are lost. The renderer must derive the same
    // category and title from the message alone as it did from the
    // structured TurnError, or the user watches an error rename itself
    // across a tab switch.
    function expectResumeStable(input: TurnError, expectedCategory: string) {
      const live = userFacingErrorForMessage(input, "turn");
      const resumed = userFacingErrorForMessage(input.message, "turn");
      expect(resumed.category).toBe(expectedCategory);
      expect(resumed.title).toBe(live.title);
      expect(resumed.tone).toBe(live.tone);
    }

    it("keeps a 429 rate-limit failure identical across a resume", () => {
      expectResumeStable(
        {
          message: 'HTTP 429: {"error":{"type":"rate_limit_error","message":"slow down"}}',
          code: "rate_limit_error",
          category: "provider",
          status_code: 429,
        },
        "provider",
      );
    });

    it("keeps a 429 whose body carries no provider keywords identical across a resume", () => {
      expectResumeStable(
        {
          message: "HTTP 429: Too Many Requests",
          category: "provider",
          status_code: 429,
        },
        "provider",
      );
    });

    it("keeps a 401 auth failure identical across a resume", () => {
      expectResumeStable(
        {
          message: 'HTTP 401: {"error":{"message":"invalid api key"}}',
          category: "auth",
          status_code: 401,
        },
        "auth",
      );
    });

    it("keeps a 400 invalid_request failure identical across a resume", () => {
      expectResumeStable(
        {
          message: 'HTTP 400: {"error":{"type":"invalid_request_error","message":"max_tokens must be positive"}}',
          code: "invalid_request_error",
          category: "invalid_request",
          status_code: 400,
        },
        "invalid_request",
      );
    });

    it("keeps a post-200 rate-limit stream error identical across a resume", () => {
      expectResumeStable(
        {
          message: "stream request failed: stream error (rate_limit_error)",
          code: "rate_limit_error",
          category: "provider",
        },
        "provider",
      );
    });

    it("keeps a 503 upstream failure identical across a resume", () => {
      expectResumeStable(
        {
          message: "HTTP 503: service unavailable",
          category: "network",
          status_code: 503,
        },
        "network",
      );
    });
  });
});

describe("TurnEvents", () => {
  it("does not render an event for a manual interruption", () => {
    const turn: Turn = {
      id: "turn-1",
      items: [],
      items_view: "full",
      status: "interrupted",
    };

    const event = turnEventForTurn(turn);

    expect(event).toBeUndefined();
  });

  it("preserves a real failure attached to an interrupted turn", () => {
    const turn: Turn = {
      id: "turn-1",
      items: [],
      items_view: "full",
      status: "interrupted",
      error: {
        message: "connection reset by peer",
        category: "network",
      },
    };

    const event = turnEventForTurn(turn);

    expect(event?.kind).toBe("network_lost");
    expect(event?.source).toBe("turn");
  });

  it("surfaces partial Responses stream failures as a notice", () => {
    const turn: Turn = {
      id: "turn-1",
      items: [],
      items_view: "full",
      status: "failed",
      error: {
        message:
          "stream request failed: websocket stream closed after provider event: websocket stream closed before response.completed",
        code: "stream_closed_before_response.completed",
        category: "provider",
      },
    };

    const event = turnEventForTurn(turn);

    expect(event?.presentation).toBe("notice");
    if (event?.presentation !== "notice") {
      throw new Error("expected notice event");
    }
    expect(event.notice.title).toBe("回答未完整返回");
    expect(event.notice.detail).toContain("这次回答可能不完整");
  });

  it("maps in-progress compaction to a compaction event instead of an error notice", () => {
    const item: ThreadItem = {
      id: "compact-1",
      type: "context_compaction",
      status: "in_progress",
    };

    const event = turnEventForItem(item);

    expect(event?.kind).toBe("context_compacting");
    expect(event?.presentation).toBe("context_compaction");
  });

});
