import { describe, expect, it, vi } from "vitest";
import { ApplicationBrowser } from "./ApplicationBrowser";
import type { BrowserSurfaceSnapshot, WuuDesktopApi } from "../../shared/protocol";

function fixture() {
  const surface = { workdir: "/repo", tabID: "one", url: "https://example.com", title: "Page", loading: false, canGoBack: false, canGoForward: false };
  const listeners: { surface?: (value: BrowserSurfaceSnapshot) => void; adopted?: Parameters<NonNullable<WuuDesktopApi["onBrowserTabAdopted"]>>[0]; input?: Parameters<NonNullable<WuuDesktopApi["onBrowserUserInput"]>>[0] } = {};
  const off = vi.fn();
  const api = {
    browserSurface: vi.fn().mockResolvedValue(surface), browserCommand: vi.fn().mockResolvedValue(surface),
    onBrowserSurface: vi.fn((listener: NonNullable<typeof listeners.surface>) => { listeners.surface = listener; return off; }),
    onBrowserTabAdopted: vi.fn((listener: NonNullable<typeof listeners.adopted>) => { listeners.adopted = listener; return off; }),
    onBrowserUserInput: vi.fn((listener: NonNullable<typeof listeners.input>) => { listeners.input = listener; return off; }),
    onBrowserDock: vi.fn((_listener: Parameters<NonNullable<WuuDesktopApi["onBrowserDock"]>>[0]) => off), onBrowserInvalidate: vi.fn((_listener: (value: { workdir: string }) => void) => off),
    reportBrowserBounds: vi.fn(), suppressBrowserOverlay: vi.fn(), openExternal: vi.fn().mockResolvedValue(undefined),
    takeoverActivity: vi.fn().mockResolvedValue({ activity: { id: "activity", workdir: "/repo", thread_id: "thread", execution_id: "run" } }),
  };
  const onActivityChanged = vi.fn();
  return { api, surface, listeners, off, onActivityChanged, browser: new ApplicationBrowser({ api, onActivityChanged }) };
}
const target = { workdir: "/repo", tabID: "one", threadID: "thread" };

describe("ApplicationBrowser presentation leases", () => {
  it("filters native events to the lease, copies snapshots and removes every subscription", async () => {
    const f = fixture(); const lease = f.browser.acquireSurface(target);
    const surface = vi.fn(), adopted = vi.fn(), input = vi.fn();
    lease.onSurface(surface); lease.onTabAdopted(adopted); lease.onUserInput(input);
    f.listeners.surface!({ ...f.surface, workdir: "/other" });
    f.listeners.surface!(f.surface);
    expect(surface).toHaveBeenCalledTimes(1);
    expect(Object.isFrozen(surface.mock.calls[0][0])).toBe(true);
    f.listeners.adopted!({ workdir: "/repo", openerTabID: "other", tabID: "popup", url: "x" });
    f.listeners.adopted!({ workdir: "/repo", openerTabID: "one", tabID: "popup", url: "x" });
    expect(adopted).toHaveBeenCalledTimes(1);
    f.listeners.input!({ ...target, workdir: "/other" }); expect(input).not.toHaveBeenCalled();
    lease.dispose(); lease.dispose();
    expect(f.off).toHaveBeenCalledTimes(3);
    f.listeners.surface!(f.surface); expect(surface).toHaveBeenCalledTimes(1);
  });

  it("parks and unsuppresses a revoked lease without letting its cleanup hide the next owner", () => {
    const f = fixture(); const old = f.browser.acquireSurface(target);
    old.reportBounds({ x: 1, y: 2, width: 300, height: 200 }, true); old.suppressOverlay(true);
    const current = f.browser.acquireSurface(target);
    expect(f.api.reportBrowserBounds).toHaveBeenLastCalledWith("/repo", "one", null);
    expect(f.api.suppressBrowserOverlay).toHaveBeenLastCalledWith("/repo", "one", false);
    current.reportBounds({ x: 2, y: 3, width: 200, height: 100 }, true);
    f.api.reportBrowserBounds.mockClear(); old.dispose(); old.reportBounds(null); old.suppressOverlay(false);
    expect(f.api.reportBrowserBounds).not.toHaveBeenCalled();
    f.browser.dispose(); expect(f.api.reportBrowserBounds).toHaveBeenLastCalledWith("/repo", "one", null);
    expect(() => f.browser.acquireSurface(target)).toThrow();
  });

  it("rejects stale async reads and commands after release or abort", async () => {
    const f = fixture(); let finish!: (value: BrowserSurfaceSnapshot) => void;
    f.api.browserSurface.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    const lease = f.browser.acquireSurface(target); const pending = lease.read(); lease.dispose(); finish(f.surface);
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    const current = f.browser.acquireSurface(target); const abort = new AbortController(); abort.abort();
    await expect(current.command({ command: "reload" }, { signal: abort.signal })).rejects.toMatchObject({ name: "AbortError" });
    expect(f.api.browserCommand).not.toHaveBeenCalled();
    f.browser.dispose();
  });

  it("passes exact takeover generation and never updates the activity owner after lease release", async () => {
    const f = fixture(); const lease = f.browser.acquireSurface(target);
    await lease.takeOver({ id: "activity", thread_id: "thread", execution_id: "run" }, 7);
    expect(f.api.takeoverActivity).toHaveBeenCalledWith("thread", "activity", 7, "run");
    expect(f.onActivityChanged).toHaveBeenCalledTimes(1);
    let finish!: (value: unknown) => void;
    f.api.takeoverActivity.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    const pending = lease.takeOver({ id: "activity", thread_id: "thread", execution_id: "old" });
    lease.dispose(); finish({ activity: {} });
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(f.onActivityChanged).toHaveBeenCalledTimes(1);
  });

  it("fails unavailable without substituting another browser and retains native errors", async () => {
    const f = fixture(); const browser = new ApplicationBrowser({ api: {}, onActivityChanged: vi.fn() });
    expect(browser.available).toBe(false); expect(() => browser.acquireSurface(target)).toThrow("unavailable");
    f.api.browserCommand.mockRejectedValueOnce(new Error("native failed"));
    const lease = f.browser.acquireSurface(target);
    await expect(lease.command({ command: "reload" })).rejects.toThrow("native failed");
    expect(f.api.browserCommand).toHaveBeenCalledTimes(1); f.browser.dispose();
  });
});

it("owns dock and invalidation subscriptions for the application lifetime", () => {
  const f = fixture();
  let receive!: (value: { workdir: string }) => void;
  f.api.onBrowserInvalidate.mockImplementation((listener: (value: { workdir: string }) => void) => { receive = listener; return f.off; });
  const listener = vi.fn();
  const unsubscribe = f.browser.onInvalidate(listener);
  receive({ workdir: "/repo" });
  expect(listener).toHaveBeenCalledExactlyOnceWith({ workdir: "/repo" });
  f.browser.dispose(); unsubscribe();
  expect(f.off).toHaveBeenCalledTimes(1);
  receive({ workdir: "/other" }); expect(listener).toHaveBeenCalledTimes(1);
});

it("routes modified links externally and respects foreground browser focus when opening ordinary links", async () => {
  const f = fixture(); const openBrowser = vi.fn();
  const browser = new ApplicationBrowser({ api: f.api, onActivityChanged: vi.fn(), workspaceNavigation: {
    enabled: true, getFocusState: () => ({ rightPanelOpen: true, activeTabID: "files", browserForegroundOccupied: false }), openBrowser,
  } });
  await browser.openURL({ url: "https://example.com/docs", modifiers: { ctrlKey: true } });
  expect(f.api.openExternal).toHaveBeenCalledWith("https://example.com/docs");
  expect(openBrowser).not.toHaveBeenCalled();
  await browser.openURL({ url: "https://example.com/docs" });
  expect(openBrowser).toHaveBeenCalledWith(false);
  await browser.openURL({ url: "mailto:reader@example.com" });
  expect(f.api.openExternal).toHaveBeenLastCalledWith("mailto:reader@example.com");
  await expect(browser.openURL({ url: "javascript:alert(1)" })).rejects.toThrow();
  browser.dispose(); f.browser.dispose();
});

it("fails missing embedded navigation explicitly and supports an explicitly external-only host", async () => {
  const f = fixture();
  await expect(f.browser.openURL({ url: "https://example.com" })).rejects.toThrow("unavailable");
  expect(f.api.openExternal).not.toHaveBeenCalled();
  const browser = new ApplicationBrowser({ api: f.api, onActivityChanged: vi.fn(), workspaceNavigation: { enabled: false,
    getFocusState: vi.fn(), openBrowser: vi.fn(),
  } });
  await browser.openURL({ url: "https://example.com" });
  expect(f.api.openExternal).toHaveBeenCalledWith("https://example.com/");
  browser.dispose(); f.browser.dispose();
});
