import * as React from "react";
import { describe, expect, it, vi } from "vitest";
import { createApplicationSettingsControls } from "./hostApplicationSettingsControls";
import { PluginHost } from "./PluginHost";
describe("public settings control boundary", () => {
  it("reports unsupported browser operations without a fallback", async () => {
    window.wuu = {} as typeof window.wuu;
    const controls = createApplicationSettingsControls();
    expect(controls.capabilities.browser).toBe(false);
    await expect(controls.listBrowserProfiles()).rejects.toThrow("unavailable");
  });
  it("preserves exact plugin generation and rejects failed writes", async () => {
    const failure = new Error("stale generation");
    window.wuu = {
      setPluginSetting: vi.fn().mockRejectedValue(failure)
    } as unknown as typeof window.wuu;
    const controls = createApplicationSettingsControls();
    const input = {
      id: "user:one",
      fingerprint: "pinned",
      key: "token",
      value: "secret"
    };
    await expect(controls.setPluginSetting(input)).rejects.toBe(failure);
    expect(window.wuu.setPluginSetting).toHaveBeenCalledWith(input);
  });
  it("reads connector authorization from the active host without fabricating identity", async () => {
    const status = { name: "plugin.external.docs", authenticated: true, scopes: ["read"] };
    window.wuu = { getMCPAuthStatus: vi.fn().mockResolvedValue(status) } as unknown as typeof window.wuu;
    const controls = createApplicationSettingsControls();
    await expect(controls.getMCPAuthStatus(status.name)).resolves.toBe(status);
    expect(window.wuu.getMCPAuthStatus).toHaveBeenCalledWith(status.name);
    const failure = new Error("credential provider unavailable");
    vi.mocked(window.wuu.getMCPAuthStatus).mockRejectedValue(failure);
    await expect(controls.getMCPAuthStatus(status.name)).rejects.toBe(failure);
    window.wuu = {} as typeof window.wuu;
    await expect(controls.getMCPAuthStatus(status.name)).rejects.toThrow("unavailable");
  });
  it("retries connector identity through the active host without starting authorization", async () => {
    const status = { name: "plugin.external.docs", authenticated: true, identity: { state: "unavailable" } };
    window.wuu = { refreshMCPIdentity: vi.fn().mockResolvedValue(status), startMCPAuth: vi.fn() } as unknown as typeof window.wuu;
    const controls = createApplicationSettingsControls();
    await expect(controls.refreshMCPIdentity(status.name)).resolves.toBe(status);
    expect(window.wuu.refreshMCPIdentity).toHaveBeenCalledExactlyOnceWith(status.name);
    expect(window.wuu.startMCPAuth).not.toHaveBeenCalled();
    const failure = new Error("connection changed");
    vi.mocked(window.wuu.refreshMCPIdentity).mockRejectedValue(failure);
    await expect(controls.refreshMCPIdentity(status.name)).rejects.toBe(failure);
    window.wuu = {} as typeof window.wuu;
    await expect(controls.refreshMCPIdentity(status.name)).rejects.toThrow("unavailable");
  });
  it("only applies a conflict preference after persistence succeeds", async () => {
    const host = new PluginHost({
      react: React
    });
    const apply = vi.spyOn(host, "setConflictPreferences");
    window.wuu = {
      setPluginConflictPreference: vi.fn().mockRejectedValue(new Error("disk"))
    } as unknown as typeof window.wuu;
    await expect(createApplicationSettingsControls({
      host
    }).chooseConflict("surface:settings", "alternate")).rejects.toThrow("disk");
    expect(apply).not.toHaveBeenCalled();
  });
  it("owns remote listener disposal and immutable capability data", () => {
    const off = vi.fn();
    const listener = vi.fn();
    window.wuu = {
      onRemoteControlEvent: vi.fn().mockReturnValue(off)
    } as unknown as typeof window.wuu;
    const controls = createApplicationSettingsControls();
    expect(Object.isFrozen(controls.capabilities)).toBe(true);
    controls.subscribeRemote(listener)();
    expect(off).toHaveBeenCalledOnce();
  });
});
