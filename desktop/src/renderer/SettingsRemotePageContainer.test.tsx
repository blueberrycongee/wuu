import { afterEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { SettingsControlsProvider } from "./SettingsControlsContext";
import { SettingsRemotePageContainer } from "./SettingsRemotePageContainer";
import { clearToasts, ToastViewport } from "./Toast";

let root: Root;
let container: HTMLDivElement;
afterEach(() => { if (root) act(() => root.unmount()); container?.remove(); clearToasts(); });
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: unknown) => void; const promise = new Promise<T>((a,b) => { resolve=a; reject=b; }); return {promise,resolve,reject}; }
const snapshot = { status: { fingerprint: "host", devices: [{ pub: "device", fingerprint: "device", name: "My phone", added_at: "2026-10-01" }], store: "" }, host_running: true, host_enabled: true, pair_uri: null };
function setup(read: () => Promise<unknown>) {
  let refresh = () => {};
  const controls = { getRemoteControlSnapshot: vi.fn(read), subscribeRemote: vi.fn((listener: () => void) => { refresh=listener; return vi.fn(); }), setRemoteHostEnabled: vi.fn() } as unknown as ApplicationSettingsControlsV1;
  container=document.createElement("div"); document.body.append(container); root=createRoot(container);
  const render = (value = controls) => act(() => root.render(<SettingsControlsProvider controls={value}><SettingsRemotePageContainer /><ToastViewport /></SettingsControlsProvider>));
  render(); return { controls, render, refresh: () => act(() => refresh()) };
}
async function flush() { await act(async () => { await Promise.resolve(); }); }
describe("remote settings read recovery", () => {
  it("does not claim disabled or empty before a successful read; retries initial failure", async () => {
    const first=deferred<unknown>(); const {controls}=setup(() => first.promise);
    expect(container.querySelector('[role="switch"]')).toBeNull();
    await act(async () => first.reject(new Error("offline")));
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    expect(container.querySelector('[role="switch"]')).toBeNull();
    vi.mocked(controls.getRemoteControlSnapshot).mockResolvedValue(snapshot as never);
    act(() => container.querySelector<HTMLButtonElement>('[data-testid="settings-error-retry"]')!.click()); await flush();
    expect(container.querySelector('[role="switch"]')?.getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).toContain("My phone");
  });
  it("keeps cached state on refresh failure and clears the failure on retry", async () => {
    const {controls,refresh}=setup(async () => snapshot); await flush();
    vi.mocked(controls.getRemoteControlSnapshot).mockRejectedValueOnce(new Error("refresh offline"));
    refresh(); await flush();
    expect(container.textContent).toContain("My phone");
    expect(container.querySelector('[role="switch"]')?.getAttribute("aria-checked")).toBe("true");
    expect(container.querySelector('[data-testid="settings-error-retry"]')).not.toBeNull();
    act(() => container.querySelector<HTMLButtonElement>('[data-testid="settings-error-retry"]')!.click()); await flush();
    expect(container.querySelector('[data-testid="settings-error-retry"]')).toBeNull();
  });
  it("ignores a previous settings provider's late result", async () => {
    const old=deferred<unknown>(); const {controls,render}=setup(() => old.promise);
    render({...controls, getRemoteControlSnapshot: async () => ({...snapshot, host_running:false, host_enabled:false})} as ApplicationSettingsControlsV1); await flush();
    await act(async () => old.resolve(snapshot));
    expect(container.querySelector('[role="switch"]')?.getAttribute("aria-checked")).toBe("false");
  });
  it("does not let a stale refresh undo a completed toggle", async () => {
    const {controls,refresh}=setup(async () => snapshot); await flush();
    const pending=deferred<unknown>();
    vi.mocked(controls.getRemoteControlSnapshot).mockReturnValueOnce(pending.promise as never);
    refresh();
    vi.mocked(controls.setRemoteHostEnabled).mockResolvedValue({...snapshot, host_running:false, host_enabled:false} as never);
    act(() => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click()); await flush();
    await act(async () => pending.resolve(snapshot));
    expect(container.querySelector('[role="switch"]')?.getAttribute("aria-checked")).toBe("false");
  });
  it("does not surface a rejected read after unmount", async () => {
    const pending=deferred<unknown>(); setup(() => pending.promise);
    act(() => root.unmount()); root=undefined as unknown as Root;
    await act(async () => pending.reject(new Error("late failure")));
    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(document.querySelector('[data-wuu-component="notice"]')).toBeNull();
  });

});
