import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { SettingsControlsProvider } from "./SettingsControlsContext";
import { SystemPermissionsPanel } from "./SystemPermissions";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";
import { retainPresentationReads } from "./plugins/PresentationApplication";
import { scopeApplicationSettingsControls, type ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";

it("rebinds permission reads and focus polling while rejecting old gap events and late requests", async () => {
  vi.useFakeTimers();
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  const make = () => {
    const lifetime = new AbortController();
    let rejectRequest!: (cause: Error) => void;
    const base = createApplicationSettingsControls();
    const source = { ...base, capabilities: { ...base.capabilities, systemPermissions: true, permissionGuide: true, permissionGuideUpdate: true, permissionGuideClose: true },
      getSystemPermissions: vi.fn().mockResolvedValue({ available: true, screen: false, accessibility: false }),
      onSystemPermissionGuideChanged: vi.fn(() => vi.fn()), updateSystemPermissionGuide: vi.fn(), closeSystemPermissionGuide: vi.fn().mockResolvedValue({ ok: true }),
      requestSystemPermission: vi.fn(() => new Promise<never>((_, reject) => { rejectRequest = reject; })) };
    const scoped = scopeApplicationSettingsControls(() => source, lifetime.signal, () => lifetime.signal.throwIfAborted());
    const controls = retainPresentationReads({ settingsControls: scoped } as ApplicationServiceV1, lifetime.signal).settingsControls;
    return { lifetime, source, controls, reject: (cause: Error) => rejectRequest(cause) };
  };
  const old = make(), next = make();
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  const render = (controls: typeof old.controls) => root.render(<SettingsControlsProvider controls={controls}><SystemPermissionsPanel /></SettingsControlsProvider>);
  try {
    await act(async () => render(old.controls));
    const panel = container.querySelector('[data-testid="system-permissions"]');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-permission="screen"] button')!.click());
    expect(old.source.requestSystemPermission).toHaveBeenCalledOnce();
    old.lifetime.abort(new PluginLifecycleCancellationError());
    await act(async () => { window.dispatchEvent(new Event("focus")); await vi.advanceTimersByTimeAsync(1500); });
    expect(old.source.getSystemPermissions).toHaveBeenCalledTimes(1);
    expect(next.source.getSystemPermissions).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alert"]')).toBeNull();
    await act(async () => render(next.controls));
    expect(container.querySelector('[data-testid="system-permissions"]')).toBe(panel);
    expect(next.source.onSystemPermissionGuideChanged).toHaveBeenCalledOnce();
    expect(next.source.getSystemPermissions).toHaveBeenCalledOnce();
    await act(async () => old.reject(new Error("old request failure")));
    expect(container.querySelector('[role="alert"]')).toBeNull();
    await act(async () => window.dispatchEvent(new Event("focus")));
    expect(next.source.getSystemPermissions).toHaveBeenCalledTimes(2);
    next.source.getSystemPermissions.mockRejectedValueOnce(new Error("real permission read failure"));
    await act(async () => window.dispatchEvent(new Event("focus")));
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
  } finally {
    act(() => root.unmount()); container.remove(); vi.useRealTimers(); vi.unstubAllGlobals();
  }
});
