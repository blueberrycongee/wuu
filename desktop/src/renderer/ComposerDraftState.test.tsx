import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import { ApplicationController } from "./plugins/ApplicationController";
import type { ComposerDraftState } from "./AppState";
import { captureComposerDrafts, cloneSessionTabDraft, createDraftSessionTab, createThreadSessionTab, initialState, sessionTabDraftForThread, threadSessionTabID, type AppState } from "./AppState";
import type { Thread } from "../shared/protocol";
import { createConversationPaneActions } from "./ConversationPaneActions";
import { createWorkspaceActions } from "./WorkspaceActions";
import { awaitComposerFiles, type ComposerFile, type ComposerImage } from "./ComposerMessages";
import {
  COMPOSER_PROMPT_IDLE_COMMIT_MS,
  useComposerDraftState,
  type ComposerDraftStateController,
} from "./ComposerDraftState";
import { clearToasts, ToastViewport } from "./Toast";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let mountedRoots: Root[] = [];
let cleanupCallbacks: Array<() => void> = [];

afterEach(() => {
  act(() => {
    for (const root of mountedRoots) root.unmount();
  });
  mountedRoots = [];
  for (const cleanup of cleanupCallbacks.splice(0)) cleanup();
  document.body.innerHTML = "";
  clearToasts();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function flushEffects(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

async function renderComposerDraftState(application?: ApplicationServiceV1): Promise<{
  unmount: () => void;
  get: () => ComposerDraftStateController;
}> {
  let latest: ComposerDraftStateController | undefined;
  const context = { kind: "no_project" as const, cwd: "/tmp" };
  const service = application ?? new ApplicationController({ ...initialState, activeContext: context,
    activeSessionTabID: "draft", sessionTabs: [createDraftSessionTab("draft", context)] });

  function Probe() {
    latest = useComposerDraftState(service);
    return null;
  }

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  mountedRoots.push(root);

  await act(async () => {
    root.render(<WuuUIRoot><Probe /><ToastViewport /></WuuUIRoot>);
    await flushEffects();
  });

  return {
    unmount: () => { act(() => root.unmount()); mountedRoots = mountedRoots.filter(value => value !== root); },
    get: () => {
      if (!latest) {
        throw new Error("composer draft state was not rendered");
      }
      return latest;
    },
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
  it("retains complete captured draft data when its UI consumer is replaced", async () => {
    const context = { kind: "no_project" as const, cwd: "/tmp" };
    const application = new ApplicationController({ ...initialState, activeContext: context,
      activeSessionTabID: "draft", sessionTabs: [createDraftSessionTab("draft", context)] });
    const first = await renderComposerDraftState(application);
    const draft = { prompt: "Question", images: [encodedImage()], files: [pdfFile()],
      selections: [{ id: "quote", text: "Answer", source: { thread_id: "a", turn_id: "t", item_id: "i", start_offset: 0, end_offset: 6 } }] };
    await act(async () => first.get().restorePrimaryComposerDraft(draft));
    first.unmount();
    const second = await renderComposerDraftState(application);
    expect(second.get().currentPrimaryComposerDraft()).toEqual(draft);
  });

  it.each([
    ["primary", "primary"], ["primary", "secondary"],
    ["secondary", "primary"], ["secondary", "secondary"],
  ] as const)("preserves both thread drafts when closing %s with %s active", async (closing, active) => {
    const context = { kind: "no_project" as const, cwd: "/tmp" };
    const makeThread = (id: string): Thread => ({
      id, title: id, preview: "", cwd: context.cwd, status: "idle",
      model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", turns: [],
    });
    const threads = { primary: makeThread("primary"), secondary: makeThread("secondary") };
    const makeDraft = (id: string): ComposerDraftState => ({
      prompt: `Keep my ${id} draft`, images: [encodedImage(`${id}-image`)], files: [pdfFile(`${id}-file`)],
      selections: [{ id: `${id}-quote`, text: id, source: { thread_id: id, turn_id: "turn", item_id: "answer", start_offset: 0, end_offset: id.length } }],
    });
    const drafts = { primary: makeDraft("primary"), secondary: makeDraft("secondary") };
    const application = new ApplicationController({
      ...initialState, activeContext: context, thread: threads.primary, secondaryThread: threads.secondary,
      sessionTabs: [createThreadSessionTab(threads.primary, context, drafts.primary), createThreadSessionTab(threads.secondary, context)],
      activeSessionTabID: threadSessionTabID("primary"), activePane: "primary",
    });
    const hook = await renderComposerDraftState(application);
    await act(async () => hook.get().setSplitComposerDrafts(drafts));
    const deps = {
      setAppState: application.updateState,
      getComposerDraftSnapshot: () => captureComposerDrafts(application.getState(), hook.get().splitComposerDrafts),
      moveSplitDraftToGlobalComposer: (pane: "primary" | "secondary") => hook.get().moveSplitDraftToGlobalComposer(pane),
    };
    const actions = createConversationPaneActions(deps);
    await act(async () => actions.activateConversationPane(active));
    await act(async () => actions.closeConversationPane(closing));

    const remaining = closing === "primary" ? "secondary" : "primary";
    expect(application.getState().thread?.id).toBe(remaining);
    expect(application.getState().secondaryThread).toBeUndefined();
    expect(hook.get().currentPrimaryComposerDraft()).toEqual(drafts[remaining]);
    for (const id of ["primary", "secondary"] as const) {
      expect(sessionTabDraftForThread(application.getState(), id)).toEqual(drafts[id]);
    }
    await act(async () => hook.get().setSplitComposerDrafts({
      primary: hook.get().currentPrimaryComposerDraft(),
      secondary: sessionTabDraftForThread(application.getState(), closing),
    }));
    expect(hook.get().splitComposerDrafts.secondary).toEqual(drafts[closing]);
  });

  it("persists the complete draft before opening skills and clears the primary composer", async () => {
    const hook = await renderComposerDraftState();
    const draft = { prompt: "Question", images: [], files: [], selections: [{ id: "quote", text: "Answer", source: { thread_id: "a", turn_id: "t", item_id: "i", start_offset: 0, end_offset: 6 } }] };
    await act(async () => hook.get().restorePrimaryComposerDraft(draft));
    const context = { kind: "no_project" as const, cwd: "/tmp" };
    let state: AppState = { ...initialState, activeContext: context, activeSessionTabID: "draft", sessionTabs: [createDraftSessionTab("draft", context)] };
    const actions = createWorkspaceActions({
      getAppState: () => state,
      setAppState: (update) => { state = typeof update === "function" ? update(state) : update; },
      getActiveTitle: () => "Draft",
      getComposerDraftSnapshot: () => captureComposerDrafts(state, hook.get().currentPrimaryComposerDraft()),
      restorePrimaryComposerDraft: (value) => hook.get().restorePrimaryComposerDraft(value),
      setSplitComposerDrafts: (value) => hook.get().setSplitComposerDrafts(value),
      cancelViewSwitch: vi.fn(), setContextCompositionEntries: vi.fn(), setInstructionFilesEntries: vi.fn(),
      scheduleStreamScroll: vi.fn(), closeWorkspaceMenus: vi.fn(), setSettingsInitialPage: vi.fn(), setSettingsOpen: vi.fn(),
    });
    await act(async () => actions.openSkillsTab());
    expect(cloneSessionTabDraft(state.sessionTabs.find(tab => tab.id === "draft")!)).toEqual(draft);
    expect(hook.get().currentPrimaryComposerDraft()).toEqual({ prompt: "", images: [], files: [] });
    expect(hook.get().composerSelections).toEqual([]);
  });

  it("keeps response selections independent across restored and split drafts", async () => {
    const hook = await renderComposerDraftState();
    const selection = { id: "quote-a", text: "同一段 🌊", comment: "Why?", source: { thread_id: "a", turn_id: "turn", item_id: "answer", start_offset: 8, end_offset: 14 } };
    const original = { prompt: "Compare", images: [], files: [], selections: [selection] };
    await act(async () => { hook.get().restorePrimaryComposerDraft(original); });
    const saved = hook.get().currentPrimaryComposerDraft();
    await act(async () => { hook.get().restorePrimaryComposerDraft({ prompt: "Other thread", images: [], files: [] }); });
    expect(hook.get().currentPrimaryComposerDraft().selections ?? []).toEqual([]);
    await act(async () => {
      hook.get().setSplitComposerDrafts({ primary: { prompt: "Other", images: [], files: [] }, secondary: saved });
    });
    await act(async () => { hook.get().moveSplitDraftToGlobalComposer("secondary"); });
    expect(hook.get().currentPrimaryComposerDraft()).toEqual(original);
    expect(hook.get().splitComposerDrafts.secondary.selections ?? []).toEqual([]);
    expect(hook.get().currentPrimaryComposerDraft().selections?.[0]).not.toBe(selection);
  });

  it.each(["global", "primary", "secondary"] as const)("retains an unreadable %s attachment and retries restored snapshots explicitly", async (pane) => {
    const context = { kind: "no_project" as const, cwd: "/tmp" };
    const primary: Thread = { id: "primary", preview: "", cwd: context.cwd, model_provider: "fixture", model: "fixture", status: "idle", created_at: "", updated_at: "", turns: [] };
    const secondary: Thread = { ...primary, id: "secondary" };
    const application = pane === "global" ? undefined : new ApplicationController({ ...initialState, activeContext: context,
      thread: primary, secondaryThread: secondary, activePane: "primary", activeSessionTabID: threadSessionTabID(primary.id),
      sessionTabs: [createThreadSessionTab(primary, context), createThreadSessionTab(secondary, context)] });
    const hook = await renderComposerDraftState(application);
    stubFileReader("data:text/plain;base64,aGVsbG8=");
    const file = new File(["hello"], "notes.txt", { type: "text/plain" });
    const read = vi.fn().mockRejectedValueOnce(new Error("File temporarily unavailable"))
      .mockResolvedValue(new TextEncoder().encode("hello").buffer);
    Object.defineProperty(file, "arrayBuffer", { configurable: true, value: read });
    await act(async () => {
      if (pane === "global") await hook.get().attachComposerAttachmentFiles([file]);
      else await hook.get().attachSplitComposerAttachmentFiles(pane, [file]);
    });
    const files = () => pane === "global" ? hook.get().composerFiles : hook.get().splitComposerDrafts[pane].files;
    expect(files()).toHaveLength(1);
    const failed = { ...files()[0] };
    expect(failed.preparation?.error).toBe("File temporarily unavailable");
    await expect(awaitComposerFiles([failed])).rejects.toThrow("File temporarily unavailable");
    expect(read).toHaveBeenCalledTimes(1);
    await act(async () => { await files()[0].preparation?.retry?.(); });
    expect(files()).toMatchObject([{ id: failed.id, filename: "notes.txt", data: "aGVsbG8=" }]);
    // A failed-send snapshot keeps the preparation lifetime, not its old rejected promise.
    await expect(awaitComposerFiles([failed])).resolves.toMatchObject([{ id: failed.id, data: "aGVsbG8=" }]);
    expect(read).toHaveBeenCalledTimes(2);
    expect(hook.get().composerImages).toEqual([]);
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

    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(hook.get().composerFiles).toHaveLength(1);
    expect(hook.get().composerFiles[0]).toMatchObject({
      media_type: "application/pdf",
      filename: "brief.pdf",
    });
    expect(hook.get().composerImages).toEqual([]);
  });

  it("keeps valid files when another file in the same paste exceeds the limit", async () => {
    const hook = await renderComposerDraftState();
    const valid = new File(["ok"], "notes.txt", { type: "text/plain" });
    Object.defineProperty(valid, "arrayBuffer", { value: async () => new TextEncoder().encode("ok").buffer });
    const oversized = new File(["bad"], "huge.pdf", { type: "application/pdf" });
    Object.defineProperty(oversized, "size", { value: 21 * 1024 * 1024 });
    await act(async () => hook.get().attachComposerAttachmentFiles([valid, oversized]));
    expect(hook.get().composerFiles.map(file => file.filename)).toEqual(["notes.txt"]);
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
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("huge.pdf");
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

  it("retains rapid input across shell teardown without per-keystroke structural publication", async () => {
    vi.useFakeTimers();
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const application = new ApplicationController({ ...initialState, activeSessionTabID: "draft", sessionTabs: [
      createDraftSessionTab("draft", context),
    ] });
    const structural = vi.fn();
    application.subscribeState(structural);
    const hook = await renderComposerDraftState(application);
    act(() => {
      hook.get().setPromptFromInput("Keep");
      hook.get().setPromptFromInput("Keep this draft");
    });
    expect(hook.get().prompt).toBe("");
    expect(application.getSnapshot().composer.draftText).toBe("Keep this draft");
    expect(structural).not.toHaveBeenCalled();
    hook.unmount();
    act(() => vi.advanceTimersByTime(COMPOSER_PROMPT_IDLE_COMMIT_MS));
    expect(application.getSnapshot().composer.draftText).toBe("Keep this draft");
    application.updateState(state => ({ ...state, status: "shell replaced" }));
    expect(application.getState().sessionTabs[0]).toMatchObject({ prompt: "Keep this draft" });
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
