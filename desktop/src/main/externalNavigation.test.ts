import { describe, expect, it, vi } from "vitest";
import {
  isAllowedRendererNavigation,
  normalizeExternalURL,
  openExternalURL,
  wireExternalNavigationGuards,
  type ExternalNavigationWindow,
} from "./externalNavigation";

describe("external navigation", () => {
  it("allows browser and mail links but rejects local and executable protocols", () => {
    expect(normalizeExternalURL(" https://example.com/docs ")).toBe("https://example.com/docs");
    expect(normalizeExternalURL("mailto:team@example.com")).toBe("mailto:team@example.com");
    expect(normalizeExternalURL("file:///tmp/token.txt")).toBeUndefined();
    expect(normalizeExternalURL("javascript:alert(1)")).toBeUndefined();
  });

  it("opens only normalized external URLs", async () => {
    const opener = vi.fn().mockResolvedValue(undefined);
    await expect(openExternalURL("https://example.com", opener)).resolves.toBe(true);
    await expect(openExternalURL("wuu://settings", opener)).resolves.toBe(false);
    expect(opener).toHaveBeenCalledTimes(1);
    expect(opener).toHaveBeenCalledWith("https://example.com/");
  });

  it("confines navigation to the renderer and denies child windows", async () => {
    let windowOpenHandler: Parameters<ExternalNavigationWindow["webContents"]["setWindowOpenHandler"]>[0] | undefined;
    let navigateHandler:
      | ((event: { preventDefault: () => void }, url: string) => void)
      | undefined;
    const window: ExternalNavigationWindow = {
      webContents: {
        setWindowOpenHandler: (handler) => {
          windowOpenHandler = handler;
        },
        on: (_event, handler) => {
          navigateHandler = handler;
        },
      },
    };
    const openExternal = vi.fn().mockResolvedValue(true);
    wireExternalNavigationGuards(window, {
      rendererURL: "http://localhost:5173/",
      openExternal,
    });

    expect(windowOpenHandler?.({ url: "https://example.com" })).toEqual({ action: "deny" });
    const internalEvent = { preventDefault: vi.fn() };
    navigateHandler?.(internalEvent, "http://localhost:5173/thread/1");
    expect(internalEvent.preventDefault).not.toHaveBeenCalled();

    const externalEvent = { preventDefault: vi.fn() };
    navigateHandler?.(externalEvent, "https://example.com/docs");
    expect(externalEvent.preventDefault).toHaveBeenCalledTimes(1);
    expect(openExternal).toHaveBeenCalledWith("https://example.com");
    expect(openExternal).toHaveBeenCalledWith("https://example.com/docs");
  });

  it("allows only an explicitly handled shell popup and keeps external links guarded", () => {
    let handler: Parameters<ExternalNavigationWindow["webContents"]["setWindowOpenHandler"]>[0] | undefined;
    const openExternal = vi.fn().mockResolvedValue(true);
    wireExternalNavigationGuards({ webContents: {
      setWindowOpenHandler: value => { handler = value; }, on: () => undefined,
    } }, {
      rendererURL: "file:///app/index.html", openExternal,
      openWindow: details => details.url === "about:blank" && details.frameName === "wuu-browser-popover"
        ? { action: "allow" } : undefined,
    });
    expect(handler?.({ url: "about:blank", frameName: "wuu-browser-popover" })).toEqual({ action: "allow" });
    expect(openExternal).not.toHaveBeenCalled();
    expect(handler?.({ url: "https://example.com", frameName: "wuu-browser-popover" })).toEqual({ action: "deny" });
    expect(handler?.({ url: "about:blank", frameName: "other" })).toEqual({ action: "deny" });
  });

  it("does not trust URLs that only share the renderer prefix", () => {
    expect(
      isAllowedRendererNavigation(
        "http://localhost:5173.evil.example/",
        "http://localhost:5173/",
      ),
    ).toBe(false);
    expect(
      isAllowedRendererNavigation(
        "file:///Applications/wuu.app/index.html#thread",
        "file:///Applications/wuu.app/index.html",
      ),
    ).toBe(true);
    expect(
      isAllowedRendererNavigation(
        "file:///Users/me/.ssh/id_ed25519",
        "file:///Applications/wuu.app/index.html",
      ),
    ).toBe(false);
  });
});
