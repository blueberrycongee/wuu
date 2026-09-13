import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { UserQuestionRequest, WuuDesktopApi } from "../shared/protocol";
import { I18nProvider, setActiveLocale } from "./i18n";
import { UserQuestionCard } from "./UserQuestionCard";

let root: Root | undefined;

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  document.body.innerHTML = "";
  delete (window as unknown as { wuu?: unknown }).wuu;
  setActiveLocale("zh-CN");
  vi.useRealTimers();
});

function offerRequest(overrides: Partial<UserQuestionRequest> = {}): UserQuestionRequest {
  return {
    request_id: "offer-1",
    plugin_id: "ask-user",
    execution_id: "execution-1",
    thread_id: "thread-1",
    turn_id: "turn-1",
    mode: "offer",
    created_at: new Date().toISOString(),
    questions: [{
      id: "path",
      question: "Which path?",
      options: [{ label: "Safe" }],
      allow_custom: true,
    }],
    ...overrides,
  };
}

describe("UserQuestionCard", () => {
  it("collects an option and custom answer before continuing", async () => {
    const onAnswer = vi.fn(async () => undefined);
    const request: UserQuestionRequest = {
      request_id: "request-1",
      plugin_id: "ask-user",
      execution_id: "execution-1",
      thread_id: "thread-1",
      turn_id: "turn-1",
      created_at: new Date().toISOString(),
      questions: [{
        id: "path",
        question: "Which path?",
        options: [{ label: "Safe", description: "Take fewer risks" }],
        allow_custom: true,
      }],
    };
    window.wuu = {
      initialLanguagePreference: "en-US",
      initialSystemLocale: "en-US",
    } as unknown as WuuDesktopApi;
    const container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root?.render(
        <I18nProvider>
          <UserQuestionCard request={request} onAnswer={onAnswer} onCancel={async () => undefined} />
        </I18nProvider>,
      );
    });

    const option = Array.from(container.querySelectorAll("button"))
      .find((button) => button.textContent?.includes("Safe"));
    const custom = container.querySelector("input");
    expect(option).toBeTruthy();
    expect(custom).toBeTruthy();
    await act(async () => {
      option?.click();
      if (custom) {
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
        setter?.call(custom, "Keep the rollback simple");
        custom.dispatchEvent(new Event("input", { bubbles: true }));
      }
    });
    const submit = Array.from(container.querySelectorAll("button"))
      .find((button) => button.textContent === "Continue");
    await act(async () => { submit?.click(); });

    expect(onAnswer).toHaveBeenCalledWith({
      answers: [{ id: "path", selected: ["Safe"], custom: "Keep the rollback simple" }],
    });
  });

  it("steers an offer option immediately", async () => {
    const onAnswer = vi.fn(async () => undefined);
    const request: UserQuestionRequest = {
      request_id: "offer-1",
      plugin_id: "ask-user",
      execution_id: "execution-1",
      thread_id: "thread-1",
      turn_id: "turn-1",
      mode: "offer",
      created_at: new Date().toISOString(),
      questions: [{
        id: "path",
        question: "Which path?",
        options: [{ label: "Safe" }],
        allow_custom: true,
      }],
    };
    window.wuu = {
      initialLanguagePreference: "en-US",
      initialSystemLocale: "en-US",
    } as unknown as WuuDesktopApi;
    const container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root?.render(
        <I18nProvider>
          <UserQuestionCard request={request} onAnswer={onAnswer} onCancel={async () => undefined} />
        </I18nProvider>,
      );
    });
    expect(container.textContent).not.toContain("Your input is needed");
    const option = Array.from(container.querySelectorAll("button"))
      .find((button) => button.textContent?.includes("Safe"));
    await act(async () => { option?.click(); });
    expect(onAnswer).toHaveBeenCalledWith({
      answers: [{ id: "path", selected: ["Safe"] }],
    });
  });

  it("counts down an offer on the custom button and expires silently", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-13T12:00:00.000Z"));
    const onCancel = vi.fn(async () => undefined);
    const request = offerRequest({
      expires_at: new Date("2026-09-13T12:00:20.000Z").toISOString(),
    });
    window.wuu = {
      initialLanguagePreference: "en-US",
      initialSystemLocale: "en-US",
    } as unknown as WuuDesktopApi;
    const container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root?.render(
        <I18nProvider>
          <UserQuestionCard
            request={request}
            onAnswer={async () => undefined}
            onCancel={onCancel}
          />
        </I18nProvider>,
      );
    });

    const skip = Array.from(container.querySelectorAll("button"))
      .find((button) => button.className.includes("user-question-skip"));
    expect(skip?.textContent).toBe("(20s)");
    expect(container.querySelector(".user-question-option-index")?.textContent).toBe("1");

    await act(async () => {
      vi.advanceTimersByTime(19_000);
    });
    expect(skip?.textContent).toBe("(1s)");
    expect(onCancel).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(1_000);
    });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("keeps counting down while hovering and stops after opening custom input", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-13T12:00:00.000Z"));
    const onCancel = vi.fn(async () => undefined);
    const onHold = vi.fn(async () => undefined);
    const request = offerRequest({
      expires_at: new Date("2026-09-13T12:00:20.000Z").toISOString(),
    });
    window.wuu = {
      initialLanguagePreference: "en-US",
      initialSystemLocale: "en-US",
    } as unknown as WuuDesktopApi;
    const container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root?.render(
        <I18nProvider>
          <UserQuestionCard
            request={request}
            onAnswer={async () => undefined}
            onCancel={onCancel}
            onHold={onHold}
          />
        </I18nProvider>,
      );
    });
    const skip = Array.from(container.querySelectorAll("button"))
      .find((button) => button.className.includes("user-question-skip"));
    const custom = Array.from(container.querySelectorAll("button"))
      .find((button) => button.className.includes("user-question-offer-custom"));
    expect(skip?.textContent).toBe("(20s)");
    expect(custom?.textContent).toBe("No, and tell Wuu what to do differently");

    await act(async () => {
      container.querySelector(".user-question-card")?.dispatchEvent(
        new MouseEvent("mouseover", { bubbles: true, cancelable: true }),
      );
      vi.advanceTimersByTime(5_000);
    });
    expect(skip?.textContent).toBe("(15s)");
    expect(onHold).not.toHaveBeenCalled();

    await act(async () => { custom?.click(); });
    expect(onHold).toHaveBeenCalledTimes(1);
    expect(container.querySelector(".user-question-skip")).toBeNull();
    expect(container.querySelector("input.user-question-offer-input")).toBeTruthy();

    await act(async () => {
      vi.advanceTimersByTime(20_000);
    });
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("hides the offer countdown after hold", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-13T12:00:00.000Z"));
    let request = offerRequest({
      expires_at: new Date("2026-09-13T12:00:20.000Z").toISOString(),
    });
    const onHold = vi.fn(async () => undefined);
    const onCustom = vi.fn(async () => undefined);
    const renderCard = async (): Promise<void> => {
      await act(async () => {
        root?.render(
          <I18nProvider>
            <UserQuestionCard
              request={request}
              onAnswer={async () => undefined}
              onCancel={async () => undefined}
              onHold={onHold}
              onCustom={onCustom}
            />
          </I18nProvider>,
        );
      });
    };
    window.wuu = {
      initialLanguagePreference: "en-US",
      initialSystemLocale: "en-US",
    } as unknown as WuuDesktopApi;
    const container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await renderCard();
    const custom = Array.from(container.querySelectorAll("button"))
      .find((button) => button.className.includes("user-question-offer-custom"));
    expect(custom?.textContent).toContain("No, and tell Wuu what to do differently");
    await act(async () => { custom?.click(); });
    expect(onHold).toHaveBeenCalledTimes(1);
    expect(container.querySelector("input.user-question-offer-input")).toBeTruthy();

    const { expires_at: _expiresAt, ...held } = request;
    request = held;
    await renderCard();
    expect(container.querySelector(".user-question-skip")).toBeNull();
    const input = container.querySelector("input.user-question-offer-input");
    expect(input).toBeTruthy();
    await act(async () => {
      if (input) {
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
        setter?.call(input, "Do it another way");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    });
    const submit = Array.from(container.querySelectorAll("button"))
      .find((button) => button.textContent === "Submit");
    await act(async () => { submit?.click(); });
    expect(onCustom).toHaveBeenCalledWith("Do it another way");
  });

  it("localizes engine approval choices while submitting protocol labels", async () => {
    const onAnswer = vi.fn(async () => undefined);
    const request: UserQuestionRequest = {
      request_id: "approval-request-1",
      plugin_id: "agent-engine-codex",
      execution_id: "execution-1",
      thread_id: "thread-1",
      turn_id: "turn-1",
      created_at: new Date().toISOString(),
      questions: [{
        id: "approval.command_execution",
        header: "codex approval",
        question: "Allow this command to run?",
        detail: "git status",
        options: [
          { label: "Allow once", description: "Approve only this request" },
          { label: "Allow for this session", description: "Approve matching requests for this engine session" },
          { label: "Deny", description: "Do not allow this request" },
        ],
      }],
    };
    window.wuu = {
      initialLanguagePreference: "system",
      initialSystemLocale: "zh-CN",
    } as unknown as WuuDesktopApi;
    const container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root?.render(
        <I18nProvider>
          <UserQuestionCard request={request} onAnswer={onAnswer} onCancel={async () => undefined} />
        </I18nProvider>,
      );
    });

    expect(container.textContent).toContain("需要你的回答");
    expect(container.textContent).toContain("允许 Codex 执行这个命令吗？");
    const option = Array.from(container.querySelectorAll("button"))
      .find((button) => button.textContent?.includes("仅允许这一次"));
    expect(option).toBeTruthy();
    await act(async () => { option?.click(); });
    const submit = Array.from(container.querySelectorAll("button"))
      .find((button) => button.textContent === "继续");
    await act(async () => { submit?.click(); });

    expect(onAnswer).toHaveBeenCalledWith({
      answers: [{ id: "approval.command_execution", selected: ["Allow once"] }],
    });
  });
});
