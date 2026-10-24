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

it("resolves local Markdown resources through the host converter and preserves remote image sources", () => {
  const { api } = setup();
  const convert = vi.fn((encoded: string) => `https://remote.example/files/${encoded}`);
  const service = new ApplicationPreviewContent(api, { encodedFileURL: convert });
  const local = service.resolveResourceURL({ source: "../images/猫.png", cwd: "/repo/docs" });
  const encoded = convert.mock.calls[0][0];
  expect(new TextDecoder().decode(Uint8Array.from(atob(encoded.replace(/-/g, "+").replace(/_/g, "/")), char => char.charCodeAt(0)))).toBe("/repo/images/猫.png");
  expect(local).toBe(`https://remote.example/files/${encoded}`);
  expect(service.resolveResourceURL({ source: `wuu-file://local/${encoded}` })).toBe(local);
  expect(service.resolveResourceURL({ source: "https://example.com/image.png" })).toBe("https://example.com/image.png");
  expect(service.resolveResourceURL({ source: "data:image/png;base64,AA==" })).toBe("data:image/png;base64,AA==");
  service.dispose();
  expect(service.resolveResourceURL({ source: "./image.png", cwd: "/repo" })).toBeUndefined();
});

it("opens the captured workspace file reference without stripping selection or replaying errors", async () => {
  const { api } = setup(); const openFile = vi.fn().mockRejectedValue(new Error("missing workspace"));
  const service = new ApplicationPreviewContent(api, { openFile });
  await expect(service.openFile({ root: "/repo/worktree", path: "src/a.ts#L12,4-L18,9" })).rejects.toThrow("missing workspace");
  expect(openFile).toHaveBeenCalledExactlyOnceWith({ root: "/repo/worktree", path: "src/a.ts#L12,4-L18,9" });
});
