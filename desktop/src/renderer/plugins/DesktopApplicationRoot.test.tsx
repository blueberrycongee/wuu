import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({ current: true, pending: false, failure: undefined as string | undefined, revision: 1,
  initialized: { application_surfaces: {}, extension_inventory: [] }, notice: vi.fn(), dismiss: vi.fn() }));
vi.mock("./DesktopBootstrap", () => ({ desktopBootstrap: { subscribe: () => () => {}, getSnapshot: () => snapshot, start: () => () => {} } }));
const snapshot = { initialized: fixture.initialized, connectionGeneration: 0 };
vi.mock("./DesktopPluginRuntime", () => ({ useDesktopPluginRuntime: () => fixture.pending,
  desktopPluginHost: { subscribe: () => () => {}, getActivationReportFailure: () => undefined,
    selectApplicationBinding: async () => {}, applicationProviders: { subscribe: () => () => {}, getRevision: () => fixture.revision,
      getBindingRevision: () => fixture.revision, get current() { return fixture.current ? {} : undefined; }, get failure() { return fixture.failure; } } } }));
vi.mock("./ApplicationShell", () => ({ ApplicationShellOutlet: () => <div>Working application</div> }));
vi.mock("../Toast", () => ({ showErrorToast: fixture.notice, dismissToast: fixture.dismiss }));
vi.mock("../i18n", () => ({ useI18n: () => ({ t: (key: string) => key }) }));
vi.mock("../LoadingViews", () => ({ RuntimeLoading: ({ status, onRecovery, onRetry }: {status:string; onRecovery?: () => void; onRetry?: () => void}) => <div>{status}{onRecovery && <button onClick={onRecovery} aria-label="recovery" />}{onRetry && <button onClick={onRetry} aria-label="retry" />}</div>, ViewSwitchLoading: () => <div data-switch="true" /> }));
import { DesktopApplicationRoot } from "./DesktopApplicationRoot";
afterEach(() => { fixture.current = true; fixture.pending = false; fixture.failure = undefined; fixture.notice.mockReset(); fixture.dismiss.mockReset(); });
it("does not flash startup or technical failure text during reload, and deduplicates the final notice", async () => {
  const container = document.createElement("div"); const root = createRoot(container);
  const render = async () => { fixture.revision++; await act(async () => root.render(<DesktopApplicationRoot />)); };
  try {
    await render(); expect(container.textContent).toBe("Working application");
    fixture.current = false; fixture.pending = true; fixture.failure = "sensitive technical detail one";
    await render(); expect(container.textContent).toBe(""); expect(container.querySelector("[data-switch]")).not.toBeNull();
    expect(fixture.notice).not.toHaveBeenCalled();
    fixture.notice.mockReturnValueOnce(71);
    fixture.pending = false; await render();
    expect(container.textContent).toBe("plugins.applicationUnavailable");
    expect(fixture.notice).toHaveBeenLastCalledWith("plugins.applicationUnavailable", "", "application-activation");
    fixture.failure = "same failure plus effective graph identity"; await render();
    expect(container.textContent).not.toContain("graph identity");
    expect(fixture.notice).toHaveBeenLastCalledWith("plugins.applicationUnavailable", "", "application-activation");
    fixture.current = true; fixture.failure = undefined; await render(); expect(container.textContent).toBe("Working application");
    expect(fixture.dismiss).toHaveBeenCalledWith(71);
  } finally { await act(async () => root.unmount()); }
});

it("opens recovery independently when application provider is unavailable", async () => {
  const openRecovery = vi.fn(async () => {});
  vi.stubGlobal("wuu", { openRecovery });
  fixture.current = false; fixture.pending = false; fixture.failure = "private graph details";
  const container = document.createElement("div"); const root = createRoot(container);
  try {
    await act(async () => root.render(<DesktopApplicationRoot />));
    const button = container.querySelector<HTMLButtonElement>('[aria-label="recovery"]');
    expect(button).not.toBeNull();
    expect(container.querySelector('[aria-label="retry"]')).toBeNull();
    await act(async () => button!.click());
    expect(openRecovery).toHaveBeenCalledTimes(1);
    openRecovery.mockRejectedValueOnce(new Error("private filesystem details"));
    await act(async () => button!.click());
    expect(fixture.notice).toHaveBeenLastCalledWith("loading.recoveryUnavailable", "", "application-recovery");
    expect(container.textContent).not.toContain("private graph");
    vi.stubGlobal("wuu", {}); fixture.revision++;
    await act(async () => root.render(<DesktopApplicationRoot />));
    expect(container.querySelector('[aria-label="recovery"]')).toBeNull();
    expect(container.querySelector('[aria-label="retry"]')).not.toBeNull();
  } finally { await act(async () => root.unmount()); vi.unstubAllGlobals(); }
});
