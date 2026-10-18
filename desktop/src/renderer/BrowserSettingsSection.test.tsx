import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { WuuDesktopApi } from "../shared/protocol";
import { BrowserSettingsSection } from "./BrowserSettingsSection";
import { I18nProvider } from "./i18n";

type BrowserApi = Pick<
  WuuDesktopApi,
  | "platform"
  | "listBrowserProfiles"
  | "importBrowserCookies"
  | "listBrowserExtensions"
  | "addBrowserExtension"
  | "setBrowserExtensionEnabled"
  | "removeBrowserExtension"
>;

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  delete (window as { wuu?: WuuDesktopApi }).wuu;
  vi.restoreAllMocks();
});

function stubBrowserAPI(overrides: Partial<BrowserApi> = {}): BrowserApi {
  const stub: BrowserApi = {
    platform: "darwin",
    listBrowserProfiles: vi.fn().mockResolvedValue([
      {
        browserId: "test",
        appName: "Test Browser",
        profileDirectoryName: "Default",
        profileName: "Work",
        profilePath: "/tmp/test-browser/Default",
        hasCookies: true,
        hasPasswords: false,
        userName: "person@example.com",
      },
    ]),
    importBrowserCookies: vi.fn().mockResolvedValue({
      appName: "Test Browser",
      profileName: "Work",
      profilePath: "/tmp/test-browser/Default",
      discovered: 4,
      imported: 3,
      skippedExpired: 1,
      skippedInvalid: 0,
      failed: 0,
    }),
    listBrowserExtensions: vi.fn().mockResolvedValue([]),
    addBrowserExtension: vi.fn().mockResolvedValue([]),
    setBrowserExtensionEnabled: vi.fn().mockResolvedValue([]),
    removeBrowserExtension: vi.fn().mockResolvedValue([]),
    ...overrides,
  };
  (window as { wuu?: BrowserApi }).wuu = stub as WuuDesktopApi;
  return stub;
}

async function render(): Promise<void> {
  act(() => {
    root = createRoot(container);
    root.render(
      <I18nProvider>
        <BrowserSettingsSection />
      </I18nProvider>,
    );
  });
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("BrowserSettingsSection", () => {
  it("imports cookies from the selected local profile", async () => {
    const api = stubBrowserAPI();
    await render();

    expect(container.textContent).toContain("Import from a browser");
    const importButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent === "Import cookies");
    expect(importButton).not.toBeUndefined();

    await act(async () => {
      importButton?.click();
    });

    expect(api.importBrowserCookies).toHaveBeenCalledWith(
      "/tmp/test-browser/Default",
    );
    expect(container.textContent).toContain("Imported 3 of 4 cookies");
  });

  it("shows the empty state when no profiles are available", async () => {
    stubBrowserAPI({
      listBrowserProfiles: vi.fn().mockResolvedValue([]),
    });
    await render();

    expect(container.textContent).toContain("No supported browser profiles");
  });
});
