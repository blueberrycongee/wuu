import { describe, expect, it, vi } from "vitest";
import type { UpdateCheckResult } from "electron-updater";
import { createUpdateController } from "./desktopUpdates";

const info = { version: "2026.10.16", files: [], path: "update.zip", sha512: "test", releaseDate: "2026-10-16T00:00:00Z" };
const available: UpdateCheckResult = { isUpdateAvailable: true, updateInfo: info, versionInfo: info };

function fixture(responses: number[]) {
  const updater = {
    checkForUpdates: vi.fn(async () => available),
    downloadUpdate: vi.fn(async () => ["update.zip"]),
  };
  const prompt = vi.fn(async () => ({ response: responses.shift() ?? 1, checkboxChecked: false }));
  const install = vi.fn(async () => {});
  const changed = vi.fn();
  const controller = createUpdateController({ updater, prompt, install, changed });
  return { controller, updater, prompt, install, changed };
}

describe("desktop update consent and recovery", () => {
  it("does not download without consent", async () => {
    const { controller, updater, install } = fixture([1]);
    await controller.check(false);
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
    expect(install).not.toHaveBeenCalled();
  });

  it("keeps a downloaded update ready until explicit restart", async () => {
    const { controller, updater, install } = fixture([0, 1, 0]);
    await controller.check(true);
    expect(controller.state.kind).toBe("ready");
    expect(install).not.toHaveBeenCalled();
    await controller.check(true);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(1);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1);
    expect(install).toHaveBeenCalledTimes(1);
  });

  it("serializes concurrent checks and never installs a failed download", async () => {
    const { controller, updater, install } = fixture([0, 0, 0, 1]);
    updater.downloadUpdate.mockRejectedValueOnce(new Error("offline"));
    await Promise.all([controller.check(true), controller.check(true)]);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(1);
    expect(install).not.toHaveBeenCalled();
    expect(controller.state.kind).toBe("idle");
    await controller.check(true);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
  });

  it("keeps failed background checks quiet and suppresses repeated version prompts", async () => {
    const { controller, updater, prompt } = fixture([1, 1]);
    updater.checkForUpdates.mockRejectedValueOnce(new Error("offline"));
    await controller.check(false);
    expect(prompt).not.toHaveBeenCalled();
    await controller.check(false);
    await controller.check(false);
    expect(prompt).toHaveBeenCalledTimes(1);
    await controller.check(true);
    expect(prompt).toHaveBeenCalledTimes(2);
  });
});
