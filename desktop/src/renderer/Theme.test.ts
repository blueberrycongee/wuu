import { afterEach, describe, expect, it, vi } from "vitest";
import type { ExtensionInventoryRecord, ThemePreference } from "../shared/protocol";
import {
  applyExtensionTheme,
  applyThemePreference,
  availableExtensionThemes,
  clearExtensionTheme,
  currentAppliedTheme,
  observeAppliedTheme,
  resolveThemePreference,
  startThemePreferenceSync,
  syncExtensionTheme,
  selectExtensionTheme,
  selectedExtensionThemeKey,
} from "./Theme";

type MediaListener = (event: { matches: boolean }) => void;

function stubMatchMedia(initialMatches: boolean): {
  fire: (matches: boolean) => void;
  listenerCount: () => number;
} {
  const listeners = new Set<MediaListener>();
  let matches = initialMatches;
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      get matches() {
        return matches;
      },
      addEventListener: (_type: string, listener: MediaListener) => {
        listeners.add(listener);
      },
      removeEventListener: (_type: string, listener: MediaListener) => {
        listeners.delete(listener);
      },
    })),
  );
  return {
    fire: (next: boolean) => {
      matches = next;
      for (const listener of listeners) {
        listener({ matches: next });
      }
    },
    listenerCount: () => listeners.size,
  };
}

afterEach(() => {
  clearExtensionTheme();
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.theme;
});

describe("extension themes", () => {
  const inventory: ExtensionInventoryRecord[] = [
    {
      id: "calm-ui",
      name: "Calm UI",
      kind: "plugin",
      provenance: {
        kind: "plugin",
        source: "local",
        scope: "user",
        plugin_id: "calm-ui",
      },
      state: "active",
      enabled: true,
      contributions: {
        themes: [
          {
            id: "violet",
            name: "Violet",
            base: "dark",
            tokens: { "--wuu-accent": "#7659ff" },
            syntax: { "--hljs-keyword": "#ff79c6" },
          },
        ],
      },
    },
  ];

  it("lists active plugin themes and applies their declared tokens", () => {
    const [theme] = availableExtensionThemes(inventory);
    expect(theme?.key).toBe("calm-ui:violet");
    applyThemePreference("dark");
    applyExtensionTheme(theme!);

    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("#7659ff");
    expect(document.documentElement.style.getPropertyValue("--wuu-color-accent")).toBe("#7659ff");
    expect(document.documentElement.style.getPropertyValue("--hljs-keyword")).toBe("#ff79c6");
  });

  it("removes extension tokens when returning to a built-in theme", () => {
    const [theme] = availableExtensionThemes(inventory);
    applyExtensionTheme(theme!);
    applyThemePreference("light");

    expect(document.documentElement.dataset.theme).toBe("light");
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("");
    expect(document.documentElement.style.getPropertyValue("--wuu-color-accent")).toBe("");
    expect(window.localStorage.getItem("wuu.extension-theme")).toBeNull();
  });

  it("does not offer disabled plugin themes", () => {
    expect(availableExtensionThemes([{ ...inventory[0]!, enabled: false }])).toEqual([]);
  });

  it("uses the stable manifest plugin ID and migrates an older subject-scoped key", () => {
    const subjectInventory = [{ ...inventory[0]!, id: "plugin:dev:calm-ui" }];
    const [theme] = availableExtensionThemes(subjectInventory);
    expect(theme?.key).toBe("calm-ui:violet");
    expect(theme?.legacyKeys).toEqual(["plugin:dev:calm-ui:violet"]);

    window.localStorage.setItem("wuu.extension-theme", "plugin:dev:calm-ui:violet");
    applyThemePreference("dark");
    syncExtensionTheme(subjectInventory);
    expect(selectedExtensionThemeKey("dark")).toBe("calm-ui:violet");
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("#7659ff");
  });
});

describe("independent mode and palette selection", () => {
  it("keeps both selections across OS changes and restores a temporarily disabled theme", () => {
    const media = stubMatchMedia(false);
    const inventory: ExtensionInventoryRecord[] = [{
      id: "paired", name: "Paired", kind: "plugin", state: "active", enabled: true,
      provenance: { kind: "plugin", source: "local", scope: "user", plugin_id: "paired" },
      contributions: { themes: [
        { id: "day", name: "Day", base: "light", tokens: { "--wuu-accent": "red" } },
        { id: "night", name: "Night", base: "dark", tokens: { "--wuu-accent": "blue" } },
      ] },
    }];
    applyThemePreference("system");
    syncExtensionTheme(inventory);
    selectExtensionTheme("light", "paired:day");
    selectExtensionTheme("dark", "paired:night");
    expect(currentAppliedTheme()).toBe("light");
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("red");
    media.fire(true);
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("blue");
    syncExtensionTheme([{ ...inventory[0]!, enabled: false }]);
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("");
    expect(selectedExtensionThemeKey("dark")).toBe("paired:night");
    syncExtensionTheme(inventory);
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("blue");
    media.fire(false);
    expect(document.documentElement.style.getPropertyValue("--wuu-accent")).toBe("red");
  });
});

describe("resolveThemePreference", () => {
  it("resolves system from prefers-color-scheme", () => {
    const media = stubMatchMedia(true);
    expect(resolveThemePreference("system")).toBe("dark");
    media.fire(false);
    expect(resolveThemePreference("system")).toBe("light");
  });
});

describe("applyThemePreference", () => {
  it("follows live OS changes while the preference is system", () => {
    const media = stubMatchMedia(false);
    applyThemePreference("system");
    expect(document.documentElement.dataset.theme).toBe("light");

    media.fire(true);
    expect(document.documentElement.dataset.theme).toBe("dark");
    media.fire(false);
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("stops following the OS once an explicit theme is applied", () => {
    const media = stubMatchMedia(false);
    applyThemePreference("system");
    expect(media.listenerCount()).toBe(1);

    applyThemePreference("light");
    expect(media.listenerCount()).toBe(0);

    media.fire(true);
    expect(document.documentElement.dataset.theme).toBe("light");
  });
});

describe("startThemePreferenceSync", () => {
  afterEach(() => {
    delete (window as { wuu?: unknown }).wuu;
  });

  it("applies preferences broadcast from other windows and disposes", () => {
    stubMatchMedia(false);
    let handler: ((theme: ThemePreference) => void) | undefined;
    const dispose = vi.fn();
    (window as { wuu?: unknown }).wuu = {
      onThemePreferenceChange: (next: (theme: ThemePreference) => void) => {
        handler = next;
        return dispose;
      },
    };

    const stop = startThemePreferenceSync();
    handler?.("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    handler?.("light");
    expect(document.documentElement.dataset.theme).toBe("light");

    stop();
    expect(dispose).toHaveBeenCalledTimes(1);
  });

  it("is a no-op without the desktop bridge", () => {
    stubMatchMedia(false);
    expect(() => startThemePreferenceSync()).not.toThrow();
  });
});

describe("observeAppliedTheme", () => {
  it("reports concrete theme changes from the document attribute", async () => {
    document.documentElement.dataset.theme = "light";
    expect(currentAppliedTheme()).toBe("light");
    const onChange = vi.fn();
    const stop = observeAppliedTheme(onChange);

    document.documentElement.dataset.theme = "dark";
    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith("dark"));

    document.documentElement.dataset.theme = "dark";
    await Promise.resolve();
    expect(onChange).toHaveBeenCalledTimes(1);

    stop();
    document.documentElement.dataset.theme = "light";
    await Promise.resolve();
    expect(onChange).toHaveBeenCalledTimes(1);
  });
});
