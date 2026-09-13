import { describe, expect, it, vi } from "vitest";
import { clipboardAttachmentFiles, composerFileFromFile, COMPOSER_VIDEO_MAX_BYTES } from "./ComposerMessages";
import { buildComposerAttachments } from "./ComposerDraftState";
import type { ClipboardEvent } from "react";

describe("video attachments", () => {
  it("retains a pasted file through encoding and the shared draft attachment builder", async () => {
    const file = new File([new Uint8Array([0, 1, 2, 3])], "clip.mp4", { type: "video/mp4" });
    Object.defineProperty(file, "arrayBuffer", { value: async () => new Uint8Array([0, 1, 2, 3]).buffer });
    const event = { clipboardData: { items: [{ kind: "file", getAsFile: () => file }] } } as unknown as ClipboardEvent<HTMLTextAreaElement>;
    const pasted = clipboardAttachmentFiles(event);
    expect(pasted).toEqual([file]);
    const attached = vi.fn();
    await buildComposerAttachments(pasted, vi.fn(), vi.fn(), attached);
    expect(attached).toHaveBeenCalledOnce();
    expect(attached.mock.calls[0][0]).toMatchObject({ filename: "clip.mp4", media_type: "video/mp4", data: "AAECAw==" });
  });

  it("rejects oversized video before allocating its byte buffer", async () => {
    const arrayBuffer = vi.fn();
    const file = { name: "large.mov", type: "", size: COMPOSER_VIDEO_MAX_BYTES + 1, arrayBuffer } as unknown as File;
    await expect(composerFileFromFile(file)).rejects.toThrow(/20/);
    expect(arrayBuffer).not.toHaveBeenCalled();
  });
});
