import { SettingsControlsProvider } from "./SettingsControlsContext";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { BrowserExtensionRecord, BrowserProfile, WuuDesktopApi } from "../shared/protocol";
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

async function render(controls = createApplicationSettingsControls()): Promise<void> {
  act(() => {
    root = createRoot(container);
    root.render(
      <SettingsControlsProvider controls={controls}>{<I18nProvider>
        <BrowserSettingsSection />
      </I18nProvider>}</SettingsControlsProvider>,
    );
  });
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function remount(controls: ReturnType<typeof createApplicationSettingsControls>): Promise<void> {
  act(() => {
    root!.unmount();
  });
  root = null;
  return render(controls);
}

describe("BrowserSettingsSection", () => {
  it("does not refresh extensions after a removed settings view receives a reload failure", async () => {
    const api = stubBrowserAPI({ listBrowserExtensions: vi.fn().mockResolvedValue([{ id: "fixture", name: "Fixture", version: "1", path: "/fixture", enabled: true, loaded: true }]) });
    let rejectReload!: (reason: Error) => void;
    Object.assign(api, { reloadBrowserExtension: vi.fn().mockReturnValue(new Promise((_, reject) => { rejectReload = reject; })) });
    await render();
    const reload = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(button => button.getAttribute("aria-label")?.includes("Reload"));
    expect(reload).toBeDefined();
    await act(async () => reload!.click());
    act(() => root!.render(null));
    await act(async () => rejectReload(new Error("late reload failure")));
    expect(api.listBrowserExtensions).toHaveBeenCalledTimes(1);
  });

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

  it("does not report an empty extension list before the first read settles", async () => {
    const pending = deferred<never[]>();
    stubBrowserAPI({ listBrowserExtensions: vi.fn(() => pending.promise) });
    await render();

    expect(container.textContent).not.toContain("No extensions loaded");
    await act(async () => pending.resolve([]));
    expect(container.textContent).toContain("No extensions loaded");
  });

  it("keeps the last browser read visible while a return refreshes it", async () => {
    const api = stubBrowserAPI({
      listBrowserExtensions: vi.fn().mockResolvedValue([
        { id: "fixture", name: "Fixture extension", version: "1", path: "/fixture", enabled: true, loaded: true },
      ]),
    });
    const controls = createApplicationSettingsControls();
    await render(controls);
    expect(container.textContent).toContain("Test Browser · Work");
    expect(container.textContent).toContain("Fixture extension");

    const profiles = deferred<BrowserProfile[]>();
    const extensions = deferred<BrowserExtensionRecord[]>();
    api.listBrowserProfiles = vi.fn(() => profiles.promise);
    api.listBrowserExtensions = vi.fn(() => extensions.promise);
    await remount(controls);

    expect(container.textContent).toContain("Test Browser · Work");
    expect(container.textContent).toContain("Fixture extension");
    expect(container.textContent).not.toContain("Loading browser data");
    expect(container.textContent).not.toContain("No extensions loaded");
    await act(async () => {
      profiles.resolve([{
        browserId: "next",
        appName: "Next Browser",
        profileDirectoryName: "Default",
        profileName: "Work",
        profilePath: "/tmp/next-browser/Default",
        hasCookies: false,
        hasPasswords: false,
      }]);
      extensions.resolve([
        { id: "next", name: "Next extension", version: "2", path: "/next", enabled: false, loaded: false },
      ]);
    });
    expect(container.textContent).toContain("Next Browser · Work");
    expect(container.textContent).toContain("Next extension");
    expect(container.textContent).not.toContain("Test Browser · Work");
  });
});
