import * as React from "react";
import { describe, expect, it, vi } from "vitest";
import { createApplicationSettingsControls } from "./hostApplicationSettingsControls";
import { PluginHost } from "./PluginHost";
describe("public settings control boundary", () => {
  it("keeps quota reads available with an older app server", async () => {
    const expected = { engines: [] };
    const listEngines = vi.fn().mockRejectedValueOnce(new Error('invalid params: json: unknown field "include_activity"')).mockResolvedValue(expected);
    window.wuu = { listEngines } as unknown as typeof window.wuu;
    const controls = createApplicationSettingsControls();
    const context = { kind: "project" as const, project_id: "fixture", cwd: "/fixture" };
    await expect(controls.listEngines({ include_quota: true, include_activity: false }, context)).resolves.toEqual(expected);
    expect(listEngines).toHaveBeenNthCalledWith(1, { include_quota: true, include_activity: false }, context);
    expect(listEngines).toHaveBeenNthCalledWith(2, { include_quota: true }, context);

    const failure = new Error("quota service unavailable");
    listEngines.mockReset().mockRejectedValue(failure);
    await expect(controls.listEngines({ include_quota: true, include_activity: false }, context)).rejects.toBe(failure);
    expect(listEngines).toHaveBeenCalledOnce();
  });

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
