import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { resolve } from "node:path";
import { runInNewContext } from "node:vm";
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

  it("switches and dismisses through the shared E2E preload's persisted language contract", async () => {
    const preloadPath = resolve("scripts/resize-e2e-preload.cjs");
    const requireFixture = createRequire(preloadPath);
    runInNewContext(readFileSync(preloadPath, "utf8"), {
      process: { env: { WUU_RESIZE_E2E_LOCALE: "zh-CN" }, cwd: () => "/fixture" },
      require: (name: string) => name === "electron" ? {
        contextBridge: { exposeInMainWorld: (_name: string, api: WuuDesktopApi) => { window.wuu = api; } },
        ipcRenderer: {},
      } : requireFixture(name),
    });
    expect(typeof window.wuu.setLanguagePreference).toBe("function");
    expect(await window.wuu.getLanguagePreference()).toBe("zh-CN");
    await act(async () => root.render(<I18nProvider><LanguagePreferenceControl /></I18nProvider>));
    await act(async () => container.querySelector<HTMLButtonElement>(".select-menu-trigger")!.click());
    await act(async () => document.querySelector<HTMLButtonElement>('[role="menuitemradio"][data-value="en-US"]')!.click());
    expect(document.documentElement.lang).toBe("en-US");
    expect(document.querySelector(".select-menu-panel")).toBeNull();
    expect(await window.wuu.getLanguagePreference()).toBe("en-US");
  });

});
