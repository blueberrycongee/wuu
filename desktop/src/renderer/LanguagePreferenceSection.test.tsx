import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WuuDesktopApi } from "../shared/protocol";
import { I18nProvider } from "./i18n";
import { LanguagePreferenceControl } from "./LanguagePreferenceSection";

describe("LanguagePreferenceControl", () => {
  let container: HTMLDivElement;
  let root: Root;
  let setLanguagePreference: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    setLanguagePreference = vi.fn().mockResolvedValue({ ok: true, language: "zh-CN" });
    window.wuu = {
      initialLanguagePreference: "en-US",
      initialSystemLocale: "en-US",
      setLanguagePreference,
    } as unknown as WuuDesktopApi;
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it("switches the current page immediately and persists the preference", async () => {
    await act(async () => {
      root.render(
        <I18nProvider>
          <LanguagePreferenceControl />
        </I18nProvider>,
      );
    });
    expect(container.textContent).toContain("Use system language");

    const chinese = container.querySelector<HTMLButtonElement>(
      '[data-testid="settings-language-zh-CN"]',
    );
    await act(async () => chinese?.click());

    expect(container.textContent).toContain("跟随系统");
    expect(document.documentElement.lang).toBe("zh-CN");
    expect(setLanguagePreference).toHaveBeenCalledWith("zh-CN");
  });
});
