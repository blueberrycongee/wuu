import { describe, expect, it, vi } from "vitest";
import { clipboardAttachmentFiles, composerFileFromFile, composerImageFromFile, COMPOSER_ATTACHMENT_MAX_BYTES } from "./ComposerMessages";
import { ApplicationDraftResources } from "./plugins/ApplicationDrafts";
import type { ClipboardEvent } from "react";

describe("video attachments", () => {
  it("preserves original image bytes instead of storing the provider-sized canvas copy", async () => {
    const raw = new Uint8Array([0, 255, 2, 3]);
    const file = new File([raw], "photo.png", { type: "image/png" });
    Object.defineProperty(file, "arrayBuffer", { value: async () => raw.buffer });
    vi.stubGlobal("createImageBitmap", async () => ({ width: 4000, height: 3000, close() {} }));
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ drawImage() {} } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(callback => {
      const resized = new Blob([new Uint8Array([4])]);
      Object.defineProperty(resized, "arrayBuffer", { value: async () => new Uint8Array([4]).buffer });
      callback(resized);
    });
    try {
      expect(await composerImageFromFile(file)).toMatchObject({ media_type: "image/png", data: "AP8CAw==" });
    } finally { vi.restoreAllMocks(); vi.unstubAllGlobals(); }
  });
  it("delivers ordinary files byte-for-byte through the same draft lifecycle", async () => {
    const file = new File([new Uint8Array([0, 255, 2, 3])], "archive.zip", { type: "application/zip" });
    Object.defineProperty(file, "arrayBuffer", { value: async () => new Uint8Array([0, 255, 2, 3]).buffer });
    const resources = new ApplicationDraftResources(() => {});
    const [attachment] = resources.attach([file]);
    expect(await resources.resolve(attachment.id)).toMatchObject({ name: "archive.zip", mediaType: "application/zip", data: "AP8CAw==" });
  });
  it("retains a pasted file through encoding and the application-owned draft resource", async () => {
    const file = new File([new Uint8Array([0, 1, 2, 3])], "clip.mp4", { type: "video/mp4" });
    Object.defineProperty(file, "arrayBuffer", { value: async () => new Uint8Array([0, 1, 2, 3]).buffer });
    const event = { clipboardData: { items: [{ kind: "file", getAsFile: () => file }] } } as unknown as ClipboardEvent<HTMLTextAreaElement>;
    const pasted = clipboardAttachmentFiles(event);
    expect(pasted).toEqual([file]);
    const resources = new ApplicationDraftResources(() => {});
    const [attachment] = resources.attach(pasted);
    expect(await resources.resolve(attachment.id)).toMatchObject({ name: "clip.mp4", mediaType: "video/mp4", data: "AAECAw==" });
  });

  it("rejects oversized video before allocating its byte buffer", async () => {
    const arrayBuffer = vi.fn();
    const file = { name: "large.mov", type: "", size: COMPOSER_ATTACHMENT_MAX_BYTES + 1, arrayBuffer } as unknown as File;
    await expect(composerFileFromFile(file)).rejects.toThrow(/20/);
    expect(arrayBuffer).not.toHaveBeenCalled();
  });
});
