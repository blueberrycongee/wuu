import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ComposerDraftState } from "./AppState";
import type { ComposerFile, ComposerImage } from "./ComposerMessages";
import {
  COMPOSER_PROMPT_IDLE_COMMIT_MS,
  useComposerDraftState,
  type ComposerDraftStateController,
} from "./ComposerDraftState";
import { resolveLocalizedText } from "./i18n";

let mountedRoots: Root[] = [];
let cleanupCallbacks: Array<() => void> = [];

afterEach(() => {
  act(() => {
    for (const root of mountedRoots) root.unmount();
  });
  mountedRoots = [];
  for (const cleanup of cleanupCallbacks.splice(0)) cleanup();
  document.body.innerHTML = "";
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function flushEffects(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

async function renderComposerDraftState(): Promise<{
  get: () => ComposerDraftStateController;
  setStatus: ReturnType<typeof vi.fn>;
}> {
  let latest: ComposerDraftStateController | undefined;
  const setStatus = vi.fn();

  function Probe() {
    latest = useComposerDraftState({ setStatus });
    return null;
  }

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  mountedRoots.push(root);

  await act(async () => {
    root.render(createElement(Probe));
    await flushEffects();
  });

  return {
    get: () => {
      if (!latest) {
        throw new Error("composer draft state was not rendered");
      }
      return latest;
    },
    setStatus,
  };
}

function encodedImage(id = "image-1"): ComposerImage {
  return {
    id,
    media_type: "image/png",
    data: "encoded",
  };
}

function pdfFile(id = "file-1"): ComposerFile {
  return {
    id,
    media_type: "application/pdf",
    data: "encoded-pdf",
    filename: "doc.pdf",
  };
}

function stubFileReader(result: string): void {
  const originalWindow = window.FileReader;
  const originalGlobal = globalThis.FileReader;
  class MockReader {
    public result: string | ArrayBuffer | null = null;
    public onload: ((event: ProgressEvent<FileReader>) => void) | null = null;
    public onerror: ((event: ProgressEvent<FileReader>) => void) | null = null;

    public readAsDataURL(blob: Blob): void {
      void blob;
      this.result = result;
      queueMicrotask(() => {
        const event = { target: this } as unknown as ProgressEvent<FileReader>;
        this.onload?.(event);
      });
    }
  }
  Object.defineProperty(window, "FileReader", {
    configurable: true,
    writable: true,
    value: MockReader,
  });
  Object.defineProperty(globalThis, "FileReader", {
    configurable: true,
    writable: true,
    value: MockReader,
  });
  cleanupCallbacks.push(() => {
    Object.defineProperty(window, "FileReader", {
      configurable: true,
      writable: true,
      value: originalWindow,
    });
    Object.defineProperty(globalThis, "FileReader", {
      configurable: true,
      writable: true,
      value: originalGlobal,
    });
  });
}

describe("useComposerDraftState", () => {
  it("rejects unsupported primary composer attachments without changing the draft", async () => {
    const hook = await renderComposerDraftState();
    const unsupported = new File(["hello"], "notes.txt", { type: "text/plain" });

    await act(async () => {
      await hook.get().attachComposerAttachmentFiles([unsupported]);
    });

    expect(resolveLocalizedText(hook.setStatus.mock.calls[0][0] as string)).toBe(
      "仅支持图片、PDF 和视频附件",
    );
    expect(hook.get().composerImages).toEqual([]);
    expect(hook.get().composerFiles).toEqual([]);
  });

  it("attaches PDF files to the primary composer draft", async () => {
    const hook = await renderComposerDraftState();
    stubFileReader("data:application/pdf;base64,JVBERi0xLjc=");
    const pdf = new File(["%PDF-1.7"], "brief.pdf", {
      type: "application/pdf",
    });
    Object.defineProperty(pdf, "arrayBuffer", {
      configurable: true,
      value: () => Promise.resolve(new Uint8Array([37, 80, 68, 70]).buffer),
    });

    await act(async () => {
      await hook.get().attachComposerAttachmentFiles([pdf]);
      await flushEffects();
    });

    expect(hook.setStatus).not.toHaveBeenCalled();
    expect(hook.get().composerFiles).toHaveLength(1);
    expect(hook.get().composerFiles[0]).toMatchObject({
      media_type: "application/pdf",
      filename: "brief.pdf",
    });
    expect(hook.get().composerImages).toEqual([]);
  });

  it("rejects PDFs over the 20MB limit with a reason and skips reading them", async () => {
    const hook = await renderComposerDraftState();
    const pdf = new File(["%PDF-1.7"], "huge.pdf", { type: "application/pdf" });
    Object.defineProperty(pdf, "size", { configurable: true, value: 21 * 1024 * 1024 });
    const arrayBuffer = vi.fn();
    Object.defineProperty(pdf, "arrayBuffer", { configurable: true, value: arrayBuffer });

    await act(async () => {
      await hook.get().attachComposerAttachmentFiles([pdf]);
      await flushEffects();
    });

    expect(arrayBuffer).not.toHaveBeenCalled();
    expect(resolveLocalizedText(hook.setStatus.mock.calls[0][0] as string)).toBe(
      "「huge.pdf」超过 PDF 20MB 大小上限",
    );
    expect(hook.get().composerFiles).toEqual([]);
    expect(hook.get().composerImages).toEqual([]);
  });

  it("keeps split composer drafts isolated and can move one draft back to the primary composer", async () => {
    const hook = await renderComposerDraftState();
    const file = pdfFile();

    act(() => {
      hook.get().setSplitComposerPrompt("primary", "primary draft");
      hook.get().setSplitComposerPrompt("secondary", "secondary draft");
      hook.get().setSplitComposerDrafts((drafts) => ({
        ...drafts,
        secondary: { ...drafts.secondary, files: [file] },
      }));
    });

    expect(hook.get().splitComposerDrafts.primary.prompt).toBe("primary draft");
    expect(hook.get().splitComposerDrafts.secondary.files).toEqual([file]);

    act(() => {
      hook.get().moveSplitDraftToGlobalComposer("secondary");
    });

    expect(hook.get().prompt).toBe("secondary draft");
    expect(hook.get().composerFiles).toEqual([file]);
    expect(hook.get().splitComposerDrafts.primary).toEqual({
      prompt: "",
      images: [],
      files: [],
    });
    expect(hook.get().splitComposerDrafts.secondary).toEqual({
      prompt: "",
      images: [],
      files: [],
    });
  });

  it("restores and snapshots primary drafts with cloned attachments", async () => {
    const hook = await renderComposerDraftState();
    const image = encodedImage();
    const file = pdfFile();
    const draft: ComposerDraftState = {
      prompt: "hello",
      images: [image],
      files: [file],
    };

    act(() => {
      hook.get().restorePrimaryComposerDraft(draft);
    });

    expect(hook.get().prompt).toBe("hello");
    expect(hook.get().composerImages[0]).toEqual(image);
    expect(hook.get().composerImages[0]).not.toBe(image);
    expect(hook.get().composerFiles[0]).toEqual(file);
    expect(hook.get().composerFiles[0]).not.toBe(file);

    const snapshot = hook.get().currentPrimaryComposerDraft();
    expect(snapshot.images[0]).toEqual(image);
    expect(snapshot.images[0]).not.toBe(hook.get().composerImages[0]);
    expect(snapshot.files[0]).toEqual(file);
    expect(snapshot.files[0]).not.toBe(hook.get().composerFiles[0]);
  });

  it("publishes rapid textarea edits to App only after the input becomes idle", async () => {
    vi.useFakeTimers();
    const hook = await renderComposerDraftState();

    act(() => {
      hook.get().setPromptFromInput("a");
      hook.get().setPromptFromInput("ab");
      hook.get().setPromptFromInput("abc");
    });

    expect(hook.get().prompt).toBe("");
    expect(hook.get().promptRevision).toBe(0);
    expect(hook.get().currentPrimaryComposerDraft().prompt).toBe("abc");

    await act(async () => {
      vi.advanceTimersByTime(COMPOSER_PROMPT_IDLE_COMMIT_MS - 1);
      await flushEffects();
    });
    expect(hook.get().prompt).toBe("");

    await act(async () => {
      vi.advanceTimersByTime(1);
      await flushEffects();
    });

    expect(hook.get().prompt).toBe("abc");
    expect(hook.get().promptRevision).toBe(0);
  });

  it("does not let a pending input commit overwrite an immediate draft restore", async () => {
    vi.useFakeTimers();
    const hook = await renderComposerDraftState();

    act(() => {
      hook.get().setPromptFromInput("stale local draft");
      hook.get().setPrompt("restored draft");
      vi.runAllTimers();
    });

    expect(hook.get().prompt).toBe("restored draft");
    expect(hook.get().promptRevision).toBe(1);
    expect(hook.get().currentPrimaryComposerDraft().prompt).toBe("restored draft");
  });

  it("signals a programmatic clear even when App still stores an empty prompt", async () => {
    vi.useFakeTimers();
    const hook = await renderComposerDraftState();

    act(() => {
      hook.get().setPromptFromInput("not published yet");
      hook.get().setPrompt("");
    });

    expect(hook.get().prompt).toBe("");
    expect(hook.get().promptRevision).toBe(1);
    expect(hook.get().currentPrimaryComposerDraft().prompt).toBe("");
  });

  it("applies functional prompt updates to the latest uncommitted input", async () => {
    vi.useFakeTimers();
    const hook = await renderComposerDraftState();

    act(() => {
      hook.get().setPromptFromInput("latest local draft");
      hook.get().setPrompt((current) => `${current}!`);
      vi.runAllTimers();
    });

    expect(hook.get().prompt).toBe("latest local draft!");
    expect(hook.get().currentPrimaryComposerDraft().prompt).toBe(
      "latest local draft!",
    );
  });

});
