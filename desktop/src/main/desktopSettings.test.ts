import { chmod, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  DEFAULT_MESSAGE_FLOW_FONT_SIZE,
  getCodexPetScale,
  getCodexPetSettings,
  getCodexPetSize,
  getMainWindowBounds,
  getMessageFlowFontSize,
  getPluginConflictPreferences,
  getThemePreference,
  isOnboardingComplete,
  getLanguagePreference,
  readDesktopSettings,
  setCodexPetSettings,
  setMainWindowBounds,
  setMessageFlowFontSize,
  setPluginConflictPreference,
  setThemePreference,
  setLanguagePreference,
  completeOnboarding,
  writeDesktopSettings,
} from "./desktopSettings";

let dir: string;
let file: string;

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), "wuu-desktop-settings-"));
  file = join(dir, "desktop-settings.json");
});

afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

describe("desktopSettings", () => {
  it("creates parent directories on write", async () => {
    const nested = join(dir, "a", "b", "settings.json");
    writeDesktopSettings({ theme: "dark" }, nested);
    expect(JSON.parse(await readFile(nested, "utf8"))).toEqual({ theme: "dark" });
  });

  it("preserves existing file permissions", async () => {
    await writeFile(file, "{}\n", { mode: 0o640 });
    await chmod(file, 0o640);

    writeDesktopSettings({ theme: "dark" }, file);

    if (process.platform !== "win32") {
      expect((await stat(file)).mode & 0o777).toBe(0o640);
    }
  });

  it("falls back to defaults on corrupted or malformed files", async () => {
    await writeFile(file, "{not json");
    expect(readDesktopSettings(file)).toEqual({});
    await writeFile(file, JSON.stringify({ theme: "sepia" }));
    expect(readDesktopSettings(file)).toEqual({});
  });

  it("persists completion of the mandatory first-run flow without losing other settings", () => {
    setThemePreference("dark", file);

    expect(isOnboardingComplete(file)).toBe(false);
    completeOnboarding(file);

    expect(isOnboardingComplete(file)).toBe(true);
    expect(readDesktopSettings(file)).toMatchObject({
      theme: "dark",
      onboarding_version: 1,
    });
  });

  it("rejects malformed onboarding versions", async () => {
    await writeFile(file, JSON.stringify({ onboarding_version: -1 }));
    expect(isOnboardingComplete(file)).toBe(false);
    await writeFile(file, JSON.stringify({ onboarding_version: 1.5 }));
    expect(isOnboardingComplete(file)).toBe(false);
  });

  it("defaults and round-trips the language preference", () => {
    expect(getLanguagePreference(file)).toBe("system");
    setLanguagePreference("en-US", file);
    expect(getLanguagePreference(file)).toBe("en-US");
    setLanguagePreference("zh-CN", file);
    expect(getLanguagePreference(file)).toBe("zh-CN");
  });

  it("rejects unknown language preferences", async () => {
    await writeFile(file, JSON.stringify({ language: "fr-FR" }));
    expect(getLanguagePreference(file)).toBe("system");
  });

  it("round-trips the theme preference", () => {
    setThemePreference("dark", file);
    expect(getThemePreference(file)).toBe("dark");
    setThemePreference("light", file);
    expect(getThemePreference(file)).toBe("light");
    setThemePreference("system", file);
    expect(getThemePreference(file)).toBe("system");
  });

  it("round-trips plugin conflict winners without replacing other settings", () => {
    setThemePreference("dark", file);
    expect(getPluginConflictPreferences(file)).toEqual({});
    setPluginConflictPreference("surface:app.main", "alpha", file);
    setPluginConflictPreference("presenter:conversation.item:", "beta", file);
    expect(getPluginConflictPreferences(file)).toEqual({
      "surface:app.main": "alpha",
      "presenter:conversation.item:": "beta",
    });
    expect(getThemePreference(file)).toBe("dark");
  });

  it("rejects unknown theme values on read", async () => {
    await writeFile(file, JSON.stringify({ theme: "sepia" }));
    expect(getThemePreference(file)).toBe("system");
  });

  it("keeps the theme when changing other settings", () => {
    setThemePreference("dark", file);
    setMessageFlowFontSize(16, file);
    expect(getThemePreference(file)).toBe("dark");
    expect(getMessageFlowFontSize(file)).toBe(16);
  });

  it("round-trips the message-flow font size", () => {
    setMessageFlowFontSize(13, file);
    expect(getMessageFlowFontSize(file)).toBe(13);
    setMessageFlowFontSize(20, file);
    expect(getMessageFlowFontSize(file)).toBe(20);
    setMessageFlowFontSize(15, file);
    expect(getMessageFlowFontSize(file)).toBe(15);
  });

  it("preserves the former default when it is already saved", async () => {
    await writeFile(file, JSON.stringify({ message_flow_font_size: 14 }));
    setThemePreference("dark", file);
    expect(getMessageFlowFontSize(file)).toBe(14);
  });

  it("rejects out-of-range message-flow font size values on read", async () => {
    await writeFile(file, JSON.stringify({ message_flow_font_size: 5 }));
    expect(getMessageFlowFontSize(file)).toBe(DEFAULT_MESSAGE_FLOW_FONT_SIZE);
    await writeFile(file, JSON.stringify({ message_flow_font_size: 100 }));
    expect(getMessageFlowFontSize(file)).toBe(DEFAULT_MESSAGE_FLOW_FONT_SIZE);
    await writeFile(file, JSON.stringify({ message_flow_font_size: "huge" }));
    expect(getMessageFlowFontSize(file)).toBe(DEFAULT_MESSAGE_FLOW_FONT_SIZE);
    await writeFile(file, JSON.stringify({ message_flow_font_size: null }));
    expect(getMessageFlowFontSize(file)).toBe(DEFAULT_MESSAGE_FLOW_FONT_SIZE);
  });

  it("keeps the message-flow font size when toggling other settings", () => {
    setMessageFlowFontSize(16, file);
    setThemePreference("dark", file);
    expect(getMessageFlowFontSize(file)).toBe(16);
    expect(getThemePreference(file)).toBe("dark");
  });

  it("defaults codex pets to disabled with no selected pet", () => {
    // getCodexPetSettings backfills the size axis so downstream callers
    // never juggle undefined.
    expect(getCodexPetSettings(file)).toEqual({
      enabled: false,
      selected_id: "",
      size: "default",
    });
  });

  it("round-trips codex pet settings while preserving other desktop settings", () => {
    setThemePreference("dark", file);
    setCodexPetSettings({ enabled: true, selected_id: "pixel-duck" }, file);
    expect(getCodexPetSettings(file)).toEqual({
      enabled: true,
      selected_id: "pixel-duck",
      size: "default",
    });
    expect(getThemePreference(file)).toBe("dark");

    setCodexPetSettings({ enabled: false, selected_id: "" }, file);
    expect(getCodexPetSettings(file)).toEqual({
      enabled: false,
      selected_id: "",
      size: "default",
    });
    expect(getThemePreference(file)).toBe("dark");
  });

  it("ignores a legacy skin field left in the settings file", async () => {
    // Older builds persisted a `skin` preference; after the skin axis was
    // removed the reader must simply drop the unknown field, not choke.
    await writeFile(file, JSON.stringify({ theme: "dark", skin: "work" }));
    expect(readDesktopSettings(file)).toEqual({ theme: "dark" });
    expect(getThemePreference(file)).toBe("dark");
  });

  it("round-trips the codex pet size while preserving other settings", () => {
    setThemePreference("dark", file);
    setCodexPetSettings(
      { enabled: true, selected_id: "pixel-duck", size: "large" },
      file,
    );
    expect(getCodexPetSize(file)).toBe("large");
    expect(getCodexPetSettings(file)).toEqual({
      enabled: true,
      selected_id: "pixel-duck",
      size: "large",
    });
    expect(getThemePreference(file)).toBe("dark");
  });

  it("treats a missing size field on disk as the default", async () => {
    // Older desktop-settings.json files predate the size axis entirely.
    await writeFile(
      file,
      JSON.stringify({ codex_pet: { enabled: true, selected_id: "alpha" } }),
    );
    expect(getCodexPetSize(file)).toBe("default");
    expect(getCodexPetSettings(file).size).toBe("default");
  });

  it("rejects unknown size values on read", async () => {
    await writeFile(
      file,
      JSON.stringify({
        codex_pet: { enabled: true, selected_id: "alpha", size: "huge" },
      }),
    );
    expect(getCodexPetSize(file)).toBe("default");
    expect(getCodexPetSettings(file).size).toBe("default");
  });

  it("treats each write as a full replace of the codex_pet object", () => {
    // setCodexPetSettings 是整体替换：省略的可选字段（size/scale）即从
    // 持久化文件删除。部分更新语义由 index.ts 的 updateCodexPetSettings
    // 承担——它先读旧值再合并，写入时总是显式带上要保留的字段。
    setCodexPetSettings(
      { enabled: true, selected_id: "pixel-duck", size: "extra-large" },
      file,
    );
    setCodexPetSettings({ enabled: true, selected_id: "pixel-duck" }, file);
    expect(getCodexPetSize(file)).toBe("default");
  });

  it("round-trips the continuous pet scale from edge-drag resize", () => {
    setCodexPetSettings(
      { enabled: true, selected_id: "pixel-duck", scale: 0.62 },
      file,
    );
    expect(getCodexPetScale(file)).toBe(0.62);
    expect(getCodexPetSettings(file).scale).toBe(0.62);
  });

  it("drops the persisted scale when the caller writes without one", () => {
    // 显式选 size 档位的路径写入时不带 scale——codex_pet 对象整体重写，
    // 所以省略即删除，档位重新生效。
    setCodexPetSettings(
      { enabled: true, selected_id: "pixel-duck", scale: 0.62 },
      file,
    );
    setCodexPetSettings(
      { enabled: true, selected_id: "pixel-duck", size: "large" },
      file,
    );
    expect(getCodexPetScale(file)).toBeUndefined();
    expect(getCodexPetSize(file)).toBe("large");
  });

  it("rejects corrupted scale values on read", async () => {
    await writeFile(
      file,
      JSON.stringify({
        codex_pet: { enabled: true, selected_id: "alpha", scale: "big" },
      }),
    );
    expect(getCodexPetScale(file)).toBeUndefined();
    await writeFile(
      file,
      JSON.stringify({
        codex_pet: { enabled: true, selected_id: "alpha", scale: -1 },
      }),
    );
    expect(getCodexPetScale(file)).toBeUndefined();
  });

  it("returns undefined when no main_window_bounds have been saved", () => {
    expect(getMainWindowBounds(file)).toBeUndefined();
  });

  it("round-trips main_window_bounds while preserving other settings", () => {
    setThemePreference("dark", file);
    setMainWindowBounds({ x: 100, y: 200, width: 1280, height: 800 }, file);
    expect(getMainWindowBounds(file)).toEqual({
      x: 100,
      y: 200,
      width: 1280,
      height: 800,
    });
    expect(getThemePreference(file)).toBe("dark");

    setMainWindowBounds({ x: 0, y: 0, width: 880, height: 560 }, file);
    expect(getMainWindowBounds(file)).toEqual({
      x: 0,
      y: 0,
      width: 880,
      height: 560,
    });
    expect(getThemePreference(file)).toBe("dark");
  });

  it("ignores invalid main_window_bounds shapes on read", async () => {
    await writeFile(
      file,
      JSON.stringify({ main_window_bounds: "near the screen" }),
    );
    expect(getMainWindowBounds(file)).toBeUndefined();

    await writeFile(
      file,
      JSON.stringify({
        main_window_bounds: { x: "1", y: 2, width: 100, height: 100 },
      }),
    );
    expect(getMainWindowBounds(file)).toBeUndefined();

    await writeFile(
      file,
      JSON.stringify({
        main_window_bounds: { x: 1, y: 2, width: -1, height: 100 },
      }),
    );
    expect(getMainWindowBounds(file)).toBeUndefined();

    await writeFile(
      file,
      JSON.stringify({ theme: "light", main_window_bounds: { x: 1, y: 2 } }),
    );
    // Partial object (missing width / height) is dropped, theme survives.
    expect(getMainWindowBounds(file)).toBeUndefined();
    expect(getThemePreference(file)).toBe("light");
  });
});
