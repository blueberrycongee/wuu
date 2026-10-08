import { afterEach, describe, expect, it, vi } from "vitest";
import { createArtifactComposerFile } from "./ArtifactComposerFile";
import { COMPOSER_ATTACHMENT_MAX_BYTES, awaitComposerFiles } from "./ComposerMessages";
import type { ArtifactPreviewRequest } from "./ArtifactPreviewContext";

const uri = `wuu-artifact://workspace/thread/${"a".repeat(32)}/brief.pdf?sha256=${"b".repeat(64)}`;
function request(): ArtifactPreviewRequest {
  return { threadID: "thread", artifact: { id: "output", itemId: "tool", index: 0, type: "file", name: "brief.pdf", mimeType: "application/pdf", placement: "turn_end", uri } };
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("PDF preview attachment submission", () => {
  it("rejects other sessions and non-managed sources before reading bytes", () => {
    const fetcher = vi.spyOn(globalThis, "fetch");
    expect(() => createArtifactComposerFile(request(), "other")).toThrow();
    for (const source of [uri.replace("/thread/", "/other/"), "https://example.com/brief.pdf", "wuu-file://local/anything"]) {
      expect(() => createArtifactComposerFile({ ...request(), artifact: { ...request().artifact, uri: source } }, "thread")).toThrow();
    }
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("captures the selected snapshot and returns bytes through the existing attachment pipeline", async () => {
    vi.stubGlobal("File", class extends File {
      arrayBuffer() { return new Promise<ArrayBuffer>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result as ArrayBuffer);
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(this);
      }); }
    });
    const bytes = new TextEncoder().encode("%PDF-1.7\noriginal snapshot");
    let release!: (response: Response) => void;
    const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise(resolve => { release = resolve; }));
    const selected = request();
    const file = createArtifactComposerFile(selected, "thread");
    selected.threadID = "other";
    selected.artifact = { ...selected.artifact, uri: uri.replace("brief.pdf", "later.pdf"), name: "later.pdf" };
    release(new Response(bytes));
    const [encoded] = await awaitComposerFiles([file]);
    expect(fetcher).toHaveBeenCalledWith(uri);
    expect(encoded).toMatchObject({ id: file.id, filename: "brief.pdf", media_type: "application/pdf", data: btoa(String.fromCharCode(...bytes)) });
    expect(encoded.encodePromise).toBeUndefined();
  });

  it("rejects actual oversized streams even when metadata understates their size", async () => {
    const cancel = vi.fn();
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(new ReadableStream({
      start(controller) { controller.enqueue(new Uint8Array(COMPOSER_ATTACHMENT_MAX_BYTES)); controller.enqueue(new Uint8Array(1)); },
      cancel,
    })));
    const selected = request();
    selected.artifact = { ...selected.artifact, sizeBytes: 1 };
    await expect(createArtifactComposerFile(selected, "thread").encodePromise).rejects.toThrow(/20/);
    expect(cancel).toHaveBeenCalled();
  });

  it("observes an early rejection while queue admission has not awaited the attachment", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new Error("Read unavailable"));
    const file = createArtifactComposerFile(request(), "thread");
    // Cross an event-loop boundary before the sender attaches its await handler.
    await new Promise<void>(resolve => setTimeout(resolve, 0));
    await expect(awaitComposerFiles([file])).rejects.toThrow("Read unavailable");
  });

  it("surfaces failed reads and allows a fresh retry with the same attachment identity", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("Not found", { status: 404 }));
    const first = createArtifactComposerFile(request(), "thread");
    await expect(first.encodePromise).rejects.toThrow(/404/);
    const retry = createArtifactComposerFile(request(), "thread");
    expect(retry.id).toBe(first.id);
    expect(retry.encodePromise).not.toBe(first.encodePromise);
    await expect(retry.encodePromise).rejects.toThrow(/404/);
  });
});
