import { afterEach, describe, expect, it, vi } from "vitest";
import {
  localizedText,
  resolveLocale,
  resolveLocalizedText,
  setActiveLocale,
  translate,
  translateCurrent,
} from ".";
import type { TranslationKey } from "./resources/zh-CN";

describe("i18n", () => {
  afterEach(() => setActiveLocale("zh-CN"));
  it("resolves system Chinese locales to zh-CN and all others to en-US", () => {
    expect(resolveLocale("system", "zh-Hans-CN")).toBe("zh-CN");
    expect(resolveLocale("system", "en-GB")).toBe("en-US");
    expect(resolveLocale("system", "ja-JP")).toBe("en-US");
  });

  it("honors an explicit language preference", () => {
    expect(resolveLocale("zh-CN", "en-US")).toBe("zh-CN");
    expect(resolveLocale("en-US", "zh-CN")).toBe("en-US");
  });

  it("interpolates values and exposes missing keys in tests", () => {
    expect(translate("en-US", "settings.language")).toBe("Language");
    const warning = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    expect(translate("en-US", "missing" as TranslationKey)).toBe("Content unavailable");
    expect(warning).toHaveBeenCalledWith("[i18n] Missing translation: en-US.missing");
    warning.mockRestore();
  });

  it("provides the active locale to non-React renderer helpers", () => {
    setActiveLocale("en-US");
    expect(translateCurrent("settings.language")).toBe("Language");
    setActiveLocale("zh-CN");
    expect(translateCurrent("settings.language")).toBe("语言");
  });

  it("resolves state-safe localized text using the current locale", () => {
    const stored = localizedText("appState.images", { count: 2 });
    const nested = localizedText("appState.fileNamed", {
      name: localizedText("appState.fileNumber", { number: 1 }),
    });
    setActiveLocale("zh-CN");
    expect(resolveLocalizedText(stored)).toBe("[2 张图片]");
    expect(resolveLocalizedText(nested)).toBe("[文件 #1]");
    setActiveLocale("en-US");
    expect(resolveLocalizedText(stored)).toBe("[2 images]");
    expect(resolveLocalizedText(nested)).toBe("[File #1]");
    expect(resolveLocalizedText("server supplied text")).toBe("server supplied text");
    expect(resolveLocalizedText("wuu:i18n:not-json")).toBe("wuu:i18n:not-json");
  });
});
