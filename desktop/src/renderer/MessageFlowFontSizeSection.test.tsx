import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MESSAGE_FLOW_FONT_SIZE_RANGE, type WuuDesktopApi } from "../shared/protocol";
import { I18nProvider } from "./i18n";
import {
  applyMessageFlowFontSize,
  MessageFlowFontSizeControl,
} from "./MessageFlowFontSizeSection";

const STYLE_PROP = "--conversation-message-font-size" as const;

function readStamp(): string {
  return document.documentElement.style.getPropertyValue(STYLE_PROP);
}

function clearStamp(): void {
  document.documentElement.style.removeProperty(STYLE_PROP);
}

beforeEach(clearStamp);
afterEach(clearStamp);

describe("applyMessageFlowFontSize", () => {
  it("clamps values above the configured maximum", () => {
    applyMessageFlowFontSize(99);
    expect(readStamp()).toBe(`${MESSAGE_FLOW_FONT_SIZE_RANGE.max}px`);
  });

  it("clamps values below the configured minimum", () => {
    applyMessageFlowFontSize(8);
    expect(readStamp()).toBe(`${MESSAGE_FLOW_FONT_SIZE_RANGE.min}px`);
  });

  it("falls back to the configured default for non-finite values", () => {
    applyMessageFlowFontSize(Number.NaN);
    expect(readStamp()).toBe(`${MESSAGE_FLOW_FONT_SIZE_RANGE.default}px`);
    applyMessageFlowFontSize(Number.POSITIVE_INFINITY);
    expect(readStamp()).toBe(`${MESSAGE_FLOW_FONT_SIZE_RANGE.default}px`);
  });

  it("supports the half-step resolution the numeric field emits", () => {
    applyMessageFlowFontSize(14.5);
    expect(readStamp()).toBe("14.5px");
    applyMessageFlowFontSize(15.5);
    expect(readStamp()).toBe("15.5px");
  });
});

describe("MessageFlowFontSizeControl", () => {
  let container: HTMLDivElement;
  let root: Root;
  let setMessageFlowFontSize: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    setMessageFlowFontSize = vi.fn(() => Promise.resolve({ ok: true, fontSize: 16 }));
    window.wuu = {
      initialLanguagePreference: "zh-CN",
      initialSystemLocale: "zh-CN",
      initialMessageFlowFontSize: 14,
      getMessageFlowFontSize: () => Promise.resolve(14),
      setMessageFlowFontSize,
    } as unknown as WuuDesktopApi;
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    delete (window as { wuu?: WuuDesktopApi }).wuu;
  });

  async function renderControl(): Promise<HTMLInputElement> {
    await act(async () => {
      root.render(
        <I18nProvider>
          <MessageFlowFontSizeControl />
        </I18nProvider>,
      );
    });
    return container.querySelector<HTMLInputElement>(
      '[data-testid="settings-message-flow-font-size-input"]',
    )!;
  }

  async function changeInput(input: HTMLInputElement, value: string): Promise<void> {
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
      setter?.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
  }

  async function commitInput(input: HTMLInputElement): Promise<void> {
    await act(async () => {
      input.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
    });
  }

  it("applies a typed in-range size immediately and stamps it on <html>", async () => {
    const input = await renderControl();
    expect(input.value).toBe("14");

    await changeInput(input, "16");

    expect(input.value).toBe("16");
    expect(readStamp()).toBe("16px");
    expect(setMessageFlowFontSize).toHaveBeenCalledWith(16);
  });

  it("clamps an out-of-range value on commit", async () => {
    const input = await renderControl();
    await changeInput(input, "99");
    await commitInput(input);

    expect(input.value).toBe(String(MESSAGE_FLOW_FONT_SIZE_RANGE.max));
    expect(readStamp()).toBe(`${MESSAGE_FLOW_FONT_SIZE_RANGE.max}px`);
    expect(setMessageFlowFontSize).toHaveBeenCalledWith(MESSAGE_FLOW_FONT_SIZE_RANGE.max);
  });

  it("snaps a mid-step value onto the configured half-step grid", async () => {
    const input = await renderControl();
    await changeInput(input, "14.7");
    expect(input.value).toBe("14.7");
    expect(setMessageFlowFontSize).not.toHaveBeenCalled();

    await commitInput(input);

    expect(input.value).toBe("14.5");
    expect(readStamp()).toBe("14.5px");
    expect(setMessageFlowFontSize).toHaveBeenCalledWith(14.5);
  });

  it("restores the last committed size when the field is emptied", async () => {
    const input = await renderControl();
    await changeInput(input, "16");
    setMessageFlowFontSize.mockClear();

    await changeInput(input, "");
    await commitInput(input);

    expect(input.value).toBe("16");
    expect(readStamp()).toBe("16px");
    expect(setMessageFlowFontSize).toHaveBeenCalledWith(16);
  });
});
