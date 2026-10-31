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
    expect(container.textContent).toContain("English");
    expect(container.textContent).not.toContain("Use system language");
    await act(async () => container.querySelector<HTMLButtonElement>(".select-menu-trigger")?.click());
    expect(document.querySelectorAll(".select-menu-item")).toHaveLength(3);

    const chinese = [...document.querySelectorAll<HTMLButtonElement>(".select-menu-item")]
      .find((option) => option.textContent?.includes("简体中文"));
    await act(async () => chinese?.click());

    expect(container.textContent).toContain("简体中文");
    expect(document.querySelector(".select-menu-panel")).toBeNull();
    expect(document.documentElement.lang).toBe("zh-CN");
    expect(setLanguagePreference).toHaveBeenCalledWith("zh-CN");
  });
});
