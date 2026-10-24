import { describe, expect, it, vi } from "vitest";
import { ApplicationPreviewContent } from "./ApplicationPreviewContent";

function setup() {
  const api = {
    platform: "darwin" as const,
    listWorkspaceDirectory: vi.fn().mockResolvedValue({ root: "/a", path: "", entries: [{ name: "x", path: "x", kind: "file" }], truncated: false }),
    readWorkspaceFile: vi.fn().mockResolvedValue({ root: "/a", path: "x", absolute_path: "/a/x", size_bytes: 1, mtime_ms: 1, sha256: "s", binary: false, truncated: false, text: "x" }),
    revealWorkspaceItem: vi.fn().mockResolvedValue(undefined),
    showWorkspaceItemMenu: vi.fn().mockResolvedValue({ action: "none" }),
    exportWorkspaceFile: vi.fn().mockResolvedValue(undefined),
  };
  return { api, service: new ApplicationPreviewContent(api) };
}

describe("ApplicationPreviewContent", () => {
  it("keeps the requested workspace and returns immutable detached file data", async () => {
    const { api, service } = setup();
    const target = { root: "/a", path: "x" };
    const result = await service.readFile(target);
    expect(api.readWorkspaceFile).toHaveBeenCalledWith("x", "/a");
    expect(Object.isFrozen(result)).toBe(true);
    const listing = await service.listDirectory({ root: "/a", path: "" });
    expect(Object.isFrozen(listing.entries[0])).toBe(true);
    expect(Object.isFrozen(listing.entries)).toBe(true);
    expect(listing).not.toBe(await api.listWorkspaceDirectory.mock.results[0].value);
  });

  it("rejects late results when disposed and admits no later operations", async () => {
    const { api, service } = setup();
    let resolve!: (value: unknown) => void;
    api.readWorkspaceFile.mockReturnValue(new Promise(done => { resolve = done; }));
    const pending = service.readFile({ root: "/a", path: "x" });
    service.dispose();
    resolve({ text: "late" });
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    await expect(service.revealItem({ root: "/a", path: "x" })).rejects.toMatchObject({ name: "AbortError" });
    expect(api.revealWorkspaceItem).not.toHaveBeenCalled();
  });

  it("does not publish an aborted read or replay failed effects", async () => {
    const { api, service } = setup();
    const controller = new AbortController();
    const pending = service.readFile({ root: "/a", path: "x" }, { signal: controller.signal });
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    api.exportWorkspaceFile.mockRejectedValue(new Error("export failed"));
    await expect(service.exportFile({ root: "/a", path: "x" })).rejects.toThrow("export failed");
    expect(api.exportWorkspaceFile).toHaveBeenCalledTimes(1);
  });

  it("preserves paths with spaces for native actions and optional host support", async () => {
    const { api } = setup();
    const service = new ApplicationPreviewContent({ ...api, exportWorkspaceFile: undefined, unsupportedMethods: ["revealWorkspaceItem"] });
    expect(service.capabilities.exportFile).toBe(false);
    expect(service.capabilities.revealItem).toBe(false);
    await service.showItemMenu({ root: "/a/", path: " file.txt " });
    expect(api.showWorkspaceItemMenu).toHaveBeenCalledWith("/a/ file.txt ");
    await expect(service.exportFile({ root: "/a", path: "x" })).rejects.toThrow("unavailable");
  });
});
