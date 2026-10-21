import { describe, expect, it, vi } from "vitest";
import type { UpdateCheckResult } from "electron-updater";
import { createUpdateController } from "./desktopUpdates";

const info = { version: "2026.10.16", files: [], path: "update.zip", sha512: "test", releaseDate: "2026-10-16T00:00:00Z" };
const available: UpdateCheckResult = { isUpdateAvailable: true, updateInfo: info, versionInfo: info };
const latestInfo = { ...info, version: "2026.10.18" };
const latest: UpdateCheckResult = { isUpdateAvailable: true, updateInfo: latestInfo, versionInfo: latestInfo };
const current: UpdateCheckResult = { ...available, isUpdateAvailable: false };

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
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1);
    expect(install).toHaveBeenCalledTimes(1);
  });

  it("serializes concurrent checks and never installs a failed download", async () => {
    const { controller, updater, install } = fixture([0, 0, 0, 1]);
    updater.downloadUpdate.mockRejectedValueOnce(new Error("offline"));
    await Promise.all([controller.check(true), controller.check(true)]);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(install).not.toHaveBeenCalled();
    expect(controller.state.kind).toBe("error");
    await controller.check(true);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
  });

  it("keeps background checks quiet and leaves available updates for explicit action", async () => {
    const { controller, updater, prompt } = fixture([1, 1]);
    updater.checkForUpdates.mockRejectedValueOnce(new Error("offline"));
    await controller.check(false);
    expect(prompt).not.toHaveBeenCalled();
    await controller.check(false);
    await controller.check(false);
    expect(prompt).not.toHaveBeenCalled();
    expect(controller.state).toEqual({ kind: "available", version: info.version });
    await controller.check(true);
    expect(prompt).toHaveBeenCalledTimes(1);
  });

  it("refreshes an available version on subsequent background checks without downloading", async () => {
    const { controller, updater, prompt } = fixture([]);
    await controller.check(false);
    updater.checkForUpdates.mockResolvedValueOnce(latest);
    await controller.check(false);
    expect(controller.state).toEqual({ kind: "available", version: latestInfo.version });
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
    expect(prompt).not.toHaveBeenCalled();
  });

  it("retains the available version after a failed poll and refreshes it on the next poll", async () => {
    const { controller, updater, prompt } = fixture([]);
    await controller.check(false);
    updater.checkForUpdates.mockRejectedValueOnce(new Error("offline"));
    await controller.check(false);
    expect(controller.state).toEqual({ kind: "available", version: info.version });
    updater.checkForUpdates.mockResolvedValueOnce(latest);
    await controller.check(false);
    expect(controller.state).toEqual({ kind: "available", version: latestInfo.version });
    expect(prompt).not.toHaveBeenCalled();
  });

  it("downloads the latest version when the sidebar download action is invoked", async () => {
    const { controller, updater, prompt, install } = fixture([]);
    await controller.check(false);
    updater.checkForUpdates.mockResolvedValueOnce(latest);
    updater.downloadUpdate.mockImplementationOnce(async () => {
      expect(controller.state).toEqual({ kind: "downloading", version: latestInfo.version, percent: 0 });
      controller.progress(42);
      expect(controller.state).toEqual({ kind: "downloading", version: latestInfo.version, percent: 42 });
      return ["latest.zip"];
    });
    await controller.download();
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1);
    expect(controller.state).toEqual({ kind: "ready", version: latestInfo.version });
    expect(prompt).not.toHaveBeenCalled();
    expect(install).not.toHaveBeenCalled();
  });

  it("refreshes the version after the manual download prompt is accepted", async () => {
    const { controller, updater } = fixture([0, 1]);
    updater.checkForUpdates.mockResolvedValueOnce(available).mockResolvedValueOnce(latest);
    updater.downloadUpdate.mockImplementationOnce(async () => {
      expect(controller.state).toEqual({ kind: "downloading", version: latestInfo.version, percent: 0 });
      return ["latest.zip"];
    });
    await controller.check(true);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1);
    expect(controller.state).toEqual({ kind: "ready", version: latestInfo.version });
  });

  it("clears a withdrawn update during polling", async () => {
    const { controller, updater } = fixture([]);
    await controller.check(false);
    updater.checkForUpdates.mockResolvedValueOnce(current);
    await controller.check(false);
    expect(controller.state.kind).toBe("idle");
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
  });

  it.each(["sidebar", "menu"])("does not download a withdrawn update through the %s", async action => {
    const { controller, updater, prompt, install } = fixture([0]);
    updater.checkForUpdates.mockResolvedValueOnce(available).mockResolvedValueOnce(current);
    if (action === "sidebar") {
      await controller.check(false);
      await controller.download();
      expect(prompt).not.toHaveBeenCalled();
    } else {
      await controller.check(true);
      expect(prompt).toHaveBeenCalledTimes(1);
    }
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
    expect(install).not.toHaveBeenCalled();
    expect(controller.state.kind).toBe("idle");
  });

  it.each(["sidebar", "menu"])("does not use stale metadata after a failed %s refresh and retries with the latest version", async action => {
    const { controller, updater, install } = fixture([0]);
    updater.checkForUpdates.mockResolvedValueOnce(available).mockRejectedValueOnce(new Error("offline"));
    if (action === "sidebar") {
      await controller.check(false);
      await controller.download();
    } else {
      await controller.check(true);
    }
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
    expect(controller.state).toEqual({ kind: "error", version: info.version });
    updater.checkForUpdates.mockResolvedValueOnce(latest);
    await controller.download();
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1);
    expect(controller.state).toEqual({ kind: "ready", version: latestInfo.version });
    expect(install).not.toHaveBeenCalled();
  });

  it("continues background polling after a failed download", async () => {
    const { controller, updater } = fixture([]);
    await controller.check(false);
    updater.downloadUpdate.mockRejectedValueOnce(new Error("offline"));
    await controller.download();
    expect(controller.state.kind).toBe("error");
    updater.checkForUpdates.mockResolvedValueOnce(latest);
    await controller.check(false);
    expect(controller.state).toEqual({ kind: "available", version: latestInfo.version });
  });

  it("serializes refresh and download against other actions and stops before downloading on shutdown", async () => {
    const { controller, updater } = fixture([]);
    await controller.check(false);
    let finishCheck!: (result: UpdateCheckResult) => void;
    updater.checkForUpdates.mockImplementationOnce(() => new Promise(resolve => { finishCheck = resolve; }));
    const downloading = controller.download();
    await Promise.all([controller.check(false), controller.check(true), controller.download()]);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
    controller.stop();
    finishCheck(latest);
    await downloading;
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
  });
});
